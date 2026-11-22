package service

import (
	"context"
	"sync"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// pushBatchLimit acota cuántas mutaciones se drenan de la cola en un Push.
const pushBatchLimit = 500

// syncNamespaceMemory es el namespace del hub para las memorias. El transporte es genérico: otras
// clases (skills, …) usarían otro namespace con el mismo cliente.
const syncNamespaceMemory = "memory"

// SyncService orquesta la sincronización local-first por change-feed contra el hub. La cola de
// mutaciones (SyncRepo) es la fuente de verdad de lo que falta subir; el cursor recuerda hasta dónde
// se ha bajado. Push manda el estado ACTUAL de las memorias cambiadas y el hub arbitra por LWW
// (updated_at); Pull trae lo ajeno y lo aplica con el mismo guard LWW del lado cliente, de forma
// idempotente por sync_id.
type SyncService struct {
	sync     ports.SyncRepo
	client   ports.SyncClient
	memories ports.MemoryRepo
	uow      ports.UnitOfWork

	// queueTarget es la clave de partición LOCAL de la cola de mutaciones (y del cursor/lease). Es un
	// valor fijo del cliente (defaultSyncTarget), el MISMO con el que los productores encolan
	// (memory/prompt/session Service). NO es el token del hub: confundir ambos hacía que ListPending no
	// encontrara nada y Push fuera un no-op silencioso cuando el token no era "remote".
	queueTarget string
	// hubToken es el token/namespace del usuario en el hub (viaja como Bearer y en la ruta). Solo lo usa
	// el transporte (client.Push/Pull); puede diferir del queueTarget sin romper el drenaje de la cola.
	hubToken string

	// autoMu serializa los disparos AUTOMÁTICOS (timer del daemon + hooks). Si ya hay uno corriendo,
	// el siguiente se omite en vez de encolarse (§13.3 del plan). No afecta a los caminos manuales
	// (CLI/MCP), que llaman a Push/Pull directo: el usuario que pide un sync explícito lo espera.
	autoMu sync.Mutex
}

// NewSyncService construye el servicio. hubToken es el token/namespace del usuario en el hub (Bearer);
// vacío deja el transporte sin destino (el sync se rechaza antes, en la capa de entrada, si no está
// configurado). La partición local de la cola es fija (defaultSyncTarget) y no depende del token: así
// el token puede ser cualquier secreto sin desalinear la cola con la que llenan los productores.
func NewSyncService(sync ports.SyncRepo, client ports.SyncClient, memories ports.MemoryRepo, uow ports.UnitOfWork, hubToken string) *SyncService {
	return &SyncService{sync: sync, client: client, memories: memories, uow: uow, queueTarget: defaultSyncTarget, hubToken: hubToken}
}

// PushResult resume un Push.
type PushResult struct {
	Count int // número de filas (memorias) enviadas al hub
}

// Push drena las mutaciones pendientes, arma una fila por sync_id con el ESTADO ACTUAL de cada memoria
// y la manda al hub. Tras un envío correcto confirma (Ack) el lote. Es seguro reejecutar: el hub aplica
// LWW por updated_at, así que reenviar una fila igual o más vieja no hace daño.
//
// La cola se drena en lotes de pushBatchLimit hasta vaciarla, en un bucle: con más pendientes que el
// tope de lote, un solo Push las sube TODAS (antes hacía falta reejecutar varias veces). Cada vuelta
// confirma su lote, así que la cola encoge y el bucle termina.
//
// Solo se replican memorias (como en el modelo anterior). Las mutaciones de otras entidades
// (sesiones, prompts) se drenan igual para no dejar la cola creciendo, pero no viajan: su sync se
// abordaría en otro namespace más adelante.
func (s *SyncService) Push(ctx context.Context) (PushResult, error) {
	total := 0
	for {
		pending, err := s.sync.ListPending(ctx, s.queueTarget, 0, pushBatchLimit)
		if err != nil {
			return PushResult{}, err
		}
		if len(pending) == 0 {
			break
		}
		sent, err := s.pushBatch(ctx, pending)
		if err != nil {
			return PushResult{}, err
		}
		total += sent
		// Lote parcial: no quedan más pendientes que drenar, evita una consulta de más.
		if len(pending) < pushBatchLimit {
			break
		}
	}
	return PushResult{Count: total}, nil
}

// pushBatch colapsa un lote de mutaciones a una fila por sync_id (el estado ACTUAL de cada memoria),
// lo manda al hub y confirma (Ack) los seqs del lote. Devuelve cuántas filas se enviaron.
func (s *SyncService) pushBatch(ctx context.Context, pending []domain.SyncMutation) (int, error) {
	// Colapsa la cola a una entrada por sync_id (la última gana), en orden de primera aparición.
	var order []string
	seen := map[string]bool{}
	project := map[string]string{}
	occurred := map[string]domain.SyncMutation{}
	seqs := make([]int64, 0, len(pending))
	for _, m := range pending {
		seqs = append(seqs, m.Seq)
		if m.Entity != domain.SyncEntityMemory {
			continue
		}
		if !seen[m.EntityKey] {
			seen[m.EntityKey] = true
			order = append(order, m.EntityKey)
		}
		project[m.EntityKey] = m.Project
		occurred[m.EntityKey] = m
	}

	changes := make([]ports.Change, 0, len(order))
	for _, key := range order {
		cur, err := s.memories.GetBySyncID(ctx, key)
		if err != nil {
			return 0, err
		}
		if cur == nil {
			// La memoria ya no está viva (borrada o nunca existió): manda un tombstone. El updated_at
			// del borrado es el de la mutación que lo originó, para que el LWW lo arbitre bien.
			changes = append(changes, ports.Change{
				Key:       key,
				Op:        domain.SyncOpDelete,
				Payload:   []byte("{}"),
				UpdatedAt: occurred[key].OccurredAt,
				Project:   project[key],
			})
			continue
		}
		payload, err := marshalMemoryPayload(*cur)
		if err != nil {
			return 0, err
		}
		changes = append(changes, ports.Change{
			Key:       key,
			Op:        domain.SyncOpUpsert,
			Payload:   payload,
			UpdatedAt: cur.UpdatedAt,
			Project:   cur.Project,
		})
	}

	if err := s.client.Push(ctx, syncNamespaceMemory, s.hubToken, changes); err != nil {
		return 0, err
	}
	if err := s.sync.Ack(ctx, s.queueTarget, seqs); err != nil {
		return 0, err
	}
	return len(changes), nil
}

// PullResult resume un Pull.
type PullResult struct {
	Applied     int   // número de filas aplicadas localmente
	Conflicts   int   // filas NO aplicadas por edición concurrente (quedan en la bandeja de conflictos)
	SchemaDrift int   // memorias aplicadas que traían campos desconocidos de una versión más nueva (se preservan)
	NewCursor   int64 // cursor tras el Pull
}

// Pull pide al hub las filas con server_seq mayor al cursor local y las aplica de forma idempotente
// por sync_id, con guard LWW (solo pisa si el entrante es más nuevo). El apply y el avance del cursor
// van en una sola transacción: o entra el lote entero y el cursor avanza, o no cambia nada.
//
// Aviso de edición concurrente (§6.5): si llega una fila para una memoria que localmente tiene una
// mutación PENDIENTE (sin subir), no se aplica a ciegas —no se pisa el disco—; se guarda en la bandeja
// de conflictos para que el usuario decida. El cursor avanza igual (la fila queda en la bandeja, no se
// pierde).
func (s *SyncService) Pull(ctx context.Context) (PullResult, error) {
	cursor, err := s.sync.GetCursor(ctx, s.queueTarget)
	if err != nil {
		return PullResult{}, err
	}
	pendingKeys, err := s.pendingMemoryKeys(ctx)
	if err != nil {
		return PullResult{}, err
	}
	changes, newCursor, err := s.client.Pull(ctx, syncNamespaceMemory, s.hubToken, cursor)
	if err != nil {
		return PullResult{}, err
	}

	result := PullResult{NewCursor: newCursor}
	err = s.uow.Do(ctx, func(ctx context.Context) error {
		for _, ch := range changes {
			if pendingKeys[ch.Key] {
				// Pendiente local + fila remota en la misma memoria. Solo es edición concurrente REAL si
				// la remota ganaría el LWW (es más nueva que lo local); si no, es el eco de nuestro propio
				// push (misma fila que acabamos de subir) o una fila más vieja: no es conflicto. Sin este
				// guard, un push+pull seguidos generaban un "conflicto" espurio sobre lo recién subido.
				overwrite, err := s.wouldOverwrite(ctx, ch)
				if err != nil {
					return err
				}
				if overwrite {
					// No se pisa el disco; se guarda para que el usuario resuelva.
					if err := s.sync.RecordConflict(ctx, domain.SyncConflict{
						SyncID: ch.Key, Project: ch.Project, IncomingOp: ch.Op,
						Incoming: ch.Payload, IncomingAt: ch.UpdatedAt,
					}); err != nil {
						return err
					}
					result.Conflicts++
					continue
				}
				// Eco o fila vieja: cae a applyChange, que la salta por LWW (no-op).
			}
			applied, drift, err := s.applyChange(ctx, ch, false)
			if err != nil {
				return err
			}
			if applied {
				result.Applied++
				if drift {
					result.SchemaDrift++
				}
			}
		}
		return s.sync.SetCursor(ctx, s.queueTarget, newCursor)
	})
	if err != nil {
		return PullResult{}, err
	}
	return result, nil
}

// AutoPush corre Push bajo el guard de exclusión de los disparos automáticos: si ya hay un auto-sync
// (push o pull) en curso, lo omite y devuelve skipped=true en vez de encolarse. Lo usan el timer del
// daemon y el disparo del Stop hook, donde solapar syncs no aporta (la cola es la verdad; el siguiente
// tick reintenta). El resultado sin pendientes es un no-op barato.
func (s *SyncService) AutoPush(ctx context.Context) (res PushResult, skipped bool, err error) {
	if !s.autoMu.TryLock() {
		return PushResult{}, true, nil
	}
	defer s.autoMu.Unlock()
	res, err = s.Push(ctx)
	return res, false, err
}

// AutoPull corre Pull bajo el mismo guard que AutoPush (comparten autoMu, así push y pull automáticos
// no se solapan). Lo dispara el hook SessionStart para traer lo ajeno al empezar. skipped=true si ya
// había un auto-sync corriendo.
func (s *SyncService) AutoPull(ctx context.Context) (res PullResult, skipped bool, err error) {
	if !s.autoMu.TryLock() {
		return PullResult{}, true, nil
	}
	defer s.autoMu.Unlock()
	res, err = s.Pull(ctx)
	return res, false, err
}

// wouldOverwrite indica si la fila entrante ganaría el guard LWW contra el estado local (es decir, si
// applyChange la aplicaría). Espeja esa comparación sin tocar el disco: la usa Pull para decidir si una
// fila remota, cuando hay una mutación local pendiente para la misma memoria, es una edición concurrente
// real (remota más nueva) o solo el eco de nuestro propio push (misma fila o más vieja).
func (s *SyncService) wouldOverwrite(ctx context.Context, ch ports.Change) (bool, error) {
	existing, err := s.memories.GetBySyncID(ctx, ch.Key)
	if err != nil {
		return false, err
	}
	if existing == nil {
		// No hay local: un upsert entrante crearía; un delete entrante es no-op.
		return ch.Op != domain.SyncOpDelete, nil
	}
	if ch.Op == domain.SyncOpDelete {
		return existing.UpdatedAt.Before(ch.UpdatedAt), nil
	}
	incoming, err := unmarshalMemoryPayload(ch.Payload)
	if err != nil {
		return false, err
	}
	return incoming.UpdatedAt.After(existing.UpdatedAt), nil
}

// pendingMemoryKeys devuelve el conjunto de sync_id de memorias con una mutación local sin subir. Es
// la señal barata de edición concurrente: si un cambio remoto llega para una de estas keys, ambos
// lados tocaron la misma memoria.
func (s *SyncService) pendingMemoryKeys(ctx context.Context) (map[string]bool, error) {
	pending, err := s.sync.ListPending(ctx, s.queueTarget, 0, pushBatchLimit)
	if err != nil {
		return nil, err
	}
	keys := make(map[string]bool)
	for _, m := range pending {
		if m.Entity == domain.SyncEntityMemory {
			keys[m.EntityKey] = true
		}
	}
	return keys, nil
}

// ListConflicts devuelve los conflictos de edición concurrente pendientes (todos los proyectos).
func (s *SyncService) ListConflicts(ctx context.Context) ([]domain.SyncConflict, error) {
	return s.sync.ListConflicts(ctx, "")
}

// ResolveConflict cierra un conflicto de edición concurrente. Con takeRemote aplica la fila remota
// guardada (forzando, aunque su updated_at sea más viejo: es decisión explícita del usuario) y borra
// el conflicto. Sin takeRemote conserva la versión local y solo descarta la remota de la bandeja.
//
// Límite honesto: al conservar la local, si su updated_at es más viejo que el del hub, el próximo Push
// no la propagará (el hub arbitra por LWW). Localmente sí se conserva.
func (s *SyncService) ResolveConflict(ctx context.Context, syncID string, takeRemote bool) error {
	c, err := s.sync.GetConflict(ctx, syncID)
	if err != nil {
		return err
	}
	if c == nil {
		return nil // ya resuelto o inexistente: idempotente
	}
	return s.uow.Do(ctx, func(ctx context.Context) error {
		if takeRemote {
			if _, _, err := s.applyChange(ctx, ports.Change{
				Key: c.SyncID, Op: c.IncomingOp, Payload: c.Incoming, UpdatedAt: c.IncomingAt, Project: c.Project,
			}, true); err != nil {
				return err
			}
		}
		return s.sync.DeleteConflict(ctx, syncID)
	})
}

// applyChange aplica una fila entrante al estado local con guard LWW. Devuelve applied=true si
// aplicó algo y schemaDrift=true si traía campos desconocidos de una versión más nueva (que se
// preservan en UnknownFields). Solo trata memorias (las demás clases se ignoran sin error). El
// apply esquiva MemoryService.Save a propósito (import atómico, sin embeber ni juzgar en el camino):
// el backfill recalcula el vector y el barrido de conflictos corre después. Por eso, para tipos
// normativo/factual, fuerza ConflictChecked=false al importar, así el barrido del destino re-evalúa
// la memoria importada contra las suyas (fix §6.4 del plan).
//
// force omite el guard LWW: lo usa la resolución de conflictos "tomar la remota", donde el usuario
// decide aplicar la fila remota aunque su updated_at sea más viejo.
func (s *SyncService) applyChange(ctx context.Context, ch ports.Change, force bool) (applied bool, schemaDrift bool, err error) {
	existing, err := s.memories.GetBySyncID(ctx, ch.Key)
	if err != nil {
		return false, false, err
	}

	if ch.Op == domain.SyncOpDelete {
		if existing == nil {
			return false, false, nil // ya no está: idempotente
		}
		if !force && !existing.UpdatedAt.Before(ch.UpdatedAt) {
			return false, false, nil // guard LWW: lo local es igual o más nuevo → no se borra
		}
		return true, false, s.memories.SoftDelete(ctx, existing.ID)
	}

	incoming, err := unmarshalMemoryPayload(ch.Payload)
	if err != nil {
		return false, false, err
	}
	drift := len(incoming.UnknownFields) > 0
	incoming.SyncID = ch.Key
	// La sesión es contexto LOCAL de la máquina de origen: su id no existe en el destino y viola la FK
	// memories.session_id → sessions.id al importar. No tiene significado cross-máquina, así que se
	// descarta (solo se replica el namespace de memorias, no las sesiones).
	incoming.SessionID = ""
	forceRecheck(&incoming)

	if existing != nil {
		if !force && !incoming.UpdatedAt.After(existing.UpdatedAt) {
			return false, false, nil // guard LWW: lo local es igual o más nuevo → no se pisa
		}
		incoming.ID = existing.ID // conserva el id local; el resto se reemplaza con el entrante
		_, err := s.memories.Update(ctx, existing.ID, incoming)
		return true, drift, err
	}
	// Memoria nueva: el id lo asigna el repo local (el del payload es de la máquina origen).
	incoming.ID = 0
	_, err = s.memories.Add(ctx, incoming)
	return true, drift, err
}

// forceRecheck marca la memoria importada como no-verificada para que el barrido de conflictos del
// destino la vuelva a evaluar. Solo aplica a tipos normativo/factual (los que participan en la
// detección); el resto no la necesita.
func forceRecheck(m *domain.Memory) {
	if m.Type.IsNormative() || m.Type.IsFactual() {
		m.ConflictChecked = false
	}
}
