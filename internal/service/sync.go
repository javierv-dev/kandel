package service

import (
	"context"
	"encoding/json"

	"github.com/javierv-dev/kandel/internal/chunkcodec"
	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// exportBatchLimit acota cuántas mutaciones entran en un chunk de exportación.
const exportBatchLimit = 500

// SyncService orquesta la sincronización local-first: la cola de mutaciones es la fuente de verdad
// de lo que falta replicar; el Transport solo mueve chunks (a un directorio git hoy, a un servidor
// mañana). Exportar drena la cola a un chunk; importar aplica los chunks ajenos de forma
// idempotente (por sync_id), de modo que reimportar no duplica.
type SyncService struct {
	sync      ports.SyncRepo
	transport ports.Transport
	memories  ports.MemoryRepo
	uow       ports.UnitOfWork
	clock     ports.Clock
	target    string
}

// NewSyncService construye el servicio. target es la clave del destino de sincronización.
func NewSyncService(sync ports.SyncRepo, transport ports.Transport, memories ports.MemoryRepo, uow ports.UnitOfWork, clock ports.Clock, target string) *SyncService {
	if target == "" {
		target = defaultSyncTarget
	}
	return &SyncService{sync: sync, transport: transport, memories: memories, uow: uow, clock: clock, target: target}
}

// ExportResult resume una exportación.
type ExportResult struct {
	ChunkID string // id del chunk escrito; vacío si no había nada que exportar
	Count   int    // número de mutaciones incluidas
}

// Export drena las mutaciones pendientes del destino a un chunk nuevo, lo escribe en el transporte,
// actualiza el manifiesto, lo marca como sincronizado y confirma (Ack) las mutaciones. Si no hay
// pendientes, no escribe nada. Es seguro reejecutar: tras el Ack, las mismas mutaciones ya no
// vuelven a salir.
func (s *SyncService) Export(ctx context.Context) (ExportResult, error) {
	pending, err := s.sync.ListPending(ctx, s.target, 0, exportBatchLimit)
	if err != nil {
		return ExportResult{}, err
	}
	if len(pending) == 0 {
		return ExportResult{}, nil
	}

	data, chunkID, err := chunkcodec.Encode(pending)
	if err != nil {
		return ExportResult{}, err
	}
	entry := ports.ChunkEntry{ChunkID: chunkID, Count: len(pending), Size: int64(len(data))}
	if err := s.transport.WriteChunk(chunkID, data, entry); err != nil {
		return ExportResult{}, err
	}

	manifest, err := s.transport.ReadManifest()
	if err != nil {
		return ExportResult{}, err
	}
	if !manifestHas(manifest, chunkID) {
		manifest.Chunks = append(manifest.Chunks, entry)
		if err := s.transport.WriteManifest(manifest); err != nil {
			return ExportResult{}, err
		}
	}

	if err := s.sync.RecordChunk(ctx, s.target, chunkID); err != nil {
		return ExportResult{}, err
	}
	seqs := make([]int64, 0, len(pending))
	for _, m := range pending {
		seqs = append(seqs, m.Seq)
	}
	if err := s.sync.Ack(ctx, s.target, seqs); err != nil {
		return ExportResult{}, err
	}
	return ExportResult{ChunkID: chunkID, Count: len(pending)}, nil
}

// ImportResult resume una importación.
type ImportResult struct {
	ChunksImported   int
	MutationsApplied int
}

// Import lee el manifiesto del transporte y aplica los chunks que aún no están registrados como
// sincronizados, en orden. Cada chunk se verifica por su id antes de aplicarse. Las mutaciones se
// aplican de forma idempotente por sync_id. Reimportar no hace nada (los chunks ya registrados se
// saltan).
func (s *SyncService) Import(ctx context.Context) (ImportResult, error) {
	manifest, err := s.transport.ReadManifest()
	if err != nil {
		return ImportResult{}, err
	}
	synced, err := s.sync.SyncedChunks(ctx, s.target)
	if err != nil {
		return ImportResult{}, err
	}

	var result ImportResult
	for _, entry := range manifest.Chunks {
		if synced[entry.ChunkID] {
			continue
		}
		data, err := s.transport.ReadChunk(entry.ChunkID)
		if err != nil {
			return result, err
		}
		if err := chunkcodec.VerifyID(data, entry.ChunkID); err != nil {
			return result, err
		}
		mutations, err := chunkcodec.Decode(data)
		if err != nil {
			return result, err
		}
		// Cada chunk se aplica en una transacción: o entra entero o no entra.
		err = s.uow.Do(ctx, func(ctx context.Context) error {
			for _, m := range mutations {
				applied, err := s.applyMutation(ctx, m)
				if err != nil {
					return err
				}
				if applied {
					result.MutationsApplied++
				}
			}
			return s.sync.RecordChunk(ctx, s.target, entry.ChunkID)
		})
		if err != nil {
			return result, err
		}
		result.ChunksImported++
	}
	return result, nil
}

// applyMutation aplica una mutación al estado local. Solo trata memorias en este slice (la entidad
// central); las demás entidades se ignoran sin error (su importación se añade más adelante).
// Devuelve true si aplicó algo.
func (s *SyncService) applyMutation(ctx context.Context, m domain.SyncMutation) (bool, error) {
	if m.Entity != domain.SyncEntityMemory {
		return false, nil
	}
	existing, err := s.memories.GetBySyncID(ctx, m.EntityKey)
	if err != nil {
		return false, err
	}
	if m.Op == domain.SyncOpDelete {
		if existing == nil {
			return false, nil // ya no está: idempotente
		}
		return true, s.memories.SoftDelete(ctx, existing.ID)
	}

	var incoming domain.Memory
	if err := json.Unmarshal(m.Payload, &incoming); err != nil {
		return false, err
	}
	incoming.SyncID = m.EntityKey
	if existing != nil {
		// Conserva el id local; el resto del estado se reemplaza con el entrante.
		incoming.ID = existing.ID
		_, err := s.memories.Update(ctx, existing.ID, incoming)
		return true, err
	}
	// Memoria nueva: el id lo asigna el repo local (el del payload es de la máquina origen).
	incoming.ID = 0
	_, err = s.memories.Add(ctx, incoming)
	return true, err
}

// manifestHas indica si el manifiesto ya contiene un chunk con ese id.
func manifestHas(m *ports.Manifest, chunkID string) bool {
	for _, e := range m.Chunks {
		if e.ChunkID == chunkID {
			return true
		}
	}
	return false
}
