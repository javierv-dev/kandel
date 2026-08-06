package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// Límites de cuántos elementos entran en el arranque. Tras el rediseño "contexto por relevancia"
// (Fase 2) el arranque es MÍNIMO: solo la continuidad de la bitácora abierta y los avisos de salud.
// El contenido pesado (normativas, factuales, pendientes) ya NO se vuelca aquí: se inyecta bajo
// demanda por relevancia al prompt del usuario (hook UserPromptSubmit) o se recupera con mem_search.
const (
	// defaultContextJournalOpen acota los hilos de bitácora "abiertos" (estado actual). En la práctica
	// hay muy pocos temas vivos a la vez, y esta lista es el único contenido de memoria que se paga
	// SIEMPRE al abrir sesión: por encima de 3 el arranque empieza a listar hilos que ya no son "dónde
	// nos quedamos" sino cola de trabajo vieja. Ajustable por config (context_journal_open).
	defaultContextJournalOpen = 3
	// defaultNormativeThreshold dispara el aviso de mantenimiento "demasiadas normativas".
	defaultNormativeThreshold = 50
)

// ContextService arma el bloque de contexto que se inyecta al agente al arrancar una sesión (o tras
// una compactación). Tras el rediseño "contexto por relevancia" es deliberadamente MÍNIMO: solo
// superficia el proyecto, la continuidad de la bitácora abierta (títulos, "dónde nos quedamos") y la
// salud de memoria. Es lectura directa (no depende del embebedor). El resto de la memoria llega por
// relevancia al prompt (hook UserPromptSubmit) o por mem_search; los prompts crudos NO se inyectan.
type ContextService struct {
	memories    ports.MemoryRepo
	relations   ports.RelationRepo
	conflicts   ports.SyncConflictReader // opcional: avisa de ediciones concurrentes del sync (§6.5)
	threshold   int                      // umbral del aviso "demasiadas normativas"
	journalOpen int                      // tope de hilos de bitácora abiertos listados al arranque
}

// ContextOption configura un ContextService opcionalmente.
type ContextOption func(*ContextService)

// WithNormativeThreshold fija el umbral del aviso de mantenimiento de normativas. Un valor no
// positivo se ignora (se conserva el default).
func WithNormativeThreshold(n int) ContextOption {
	return func(c *ContextService) {
		if n > 0 {
			c.threshold = n
		}
	}
}

// WithJournalOpen fija cuántos hilos de bitácora abiertos lista el arranque. Un valor no positivo se
// ignora (se conserva el default).
func WithJournalOpen(n int) ContextOption {
	return func(c *ContextService) {
		if n > 0 {
			c.journalOpen = n
		}
	}
}

// WithSyncConflicts cablea el lector de conflictos de edición concurrente, para que la bandeja "Salud
// de memoria" avise cuando hay ediciones sin resolver. Opcional: sin él, no se muestra ese aviso.
func WithSyncConflicts(r ports.SyncConflictReader) ContextOption {
	return func(c *ContextService) { c.conflicts = r }
}

// NewContextService construye el servicio con sus repos inyectados.
func NewContextService(memories ports.MemoryRepo, relations ports.RelationRepo, opts ...ContextOption) *ContextService {
	c := &ContextService{
		memories:    memories,
		relations:   relations,
		threshold:   defaultNormativeThreshold,
		journalOpen: defaultContextJournalOpen,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Format devuelve el contexto del proyecto en Markdown, listo para inyectar. Si no hay nada que
// mostrar devuelve cadena vacía (el hook entonces no inyecta nada).
func (c *ContextService) Format(ctx context.Context, project string, scope domain.Scope) (string, error) {
	project = normalizeProject(project)

	// Normativas (decision/preference): ya NO se vuelcan al arranque —una decisión concreta aparece
	// por relevancia cuando el prompt la toca (rediseño contexto-por-relevancia, Fase 2)—. Se siguen
	// cargando SOLO para derivar los avisos de "Salud de memoria" (conflictos / sin verificar /
	// demasiadas): son señales de higiene, no volcado de contenido, así que no cuestan tokens.
	normatives, err := c.memories.Recent(ctx, ports.RecentQuery{
		Project:           project,
		Scope:             scope,
		Types:             []domain.MemoryType{domain.TypeDecision, domain.TypePreference},
		ExcludeSuperseded: true,
		ExcludeResolved:   true,
		Limit:             -1,
	})
	if err != nil {
		return "", err
	}

	// Bitácora — estado actual: entradas journal abiertas (resolution = open). Es el cursor "dónde nos
	// quedamos", la ÚNICA continuidad que no es apuesta al arrancar. Solo TÍTULOS: el cuerpo se trae
	// bajo demanda (mem_get / mem_thread) o por relevancia al prompt.
	journalOpen, err := c.memories.Recent(ctx, ports.RecentQuery{
		Project:    project,
		Types:      []domain.MemoryType{domain.TypeJournal},
		Resolution: domain.ResolutionOpen,
		Limit:      c.journalOpen,
	})
	if err != nil {
		return "", err
	}

	// Conflictos reales sin resolver (contradicciones mismo-scope que decide un humano). Se cuentan
	// aparte porque viven en memory_relations. Un fallo aquí no debe tumbar la inyección: se degrada a 0.
	openConflicts, err := c.relations.CountOpenConflicts(ctx, project)
	if err != nil {
		openConflicts = 0
	}

	// Ediciones concurrentes sin resolver del sync (§6.5). Opcional y tolerante: un fallo (o sin lector
	// cableado) se degrada a 0, sin tumbar la inyección.
	concurrent := 0
	if c.conflicts != nil {
		if n, err := c.conflicts.CountConflicts(ctx, project); err == nil {
			concurrent = n
		}
	}

	health := healthBanners(normatives, openConflicts, concurrent, c.threshold)

	if len(journalOpen) == 0 && len(health) == 0 {
		return "", nil
	}

	var b strings.Builder
	b.WriteString("## Memoria de sesiones anteriores\n\n")

	// Superficia el proyecto actual para que el agente sepa qué valor pasar como `project` al guardar
	// memorias de scope project (incluido journal). El proceso MCP compartido no conoce el CWD de la
	// conversación, así que este es el canal por el que ese dato llega al agente.
	if project != "" {
		fmt.Fprintf(&b, "Proyecto actual: %s\n\n", project)
	}

	if len(journalOpen) > 0 {
		b.WriteString("### Bitácora — estado actual\n")
		for _, m := range journalOpen {
			writeJournalTitle(&b, m, c.continuesPredecessorID(ctx, m))
		}
		b.WriteString("\n")
	}

	if len(health) > 0 {
		b.WriteString("### Salud de memoria\n")
		for _, line := range health {
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	return b.String(), nil
}

// writeJournalTitle escribe una entrada de bitácora como una línea de solo título, con su id para
// pedir el cuerpo con mem_get o recorrer el hilo con mem_thread. Sin cuerpo: el arranque es un índice
// de continuidad, no un volcado (el cuerpo llega por relevancia o bajo demanda). Si la entrada
// continúa a una fase anterior (predID > 0), añade una miga de pan para que el agente pueda seguir la
// cadena hacia atrás con mem_thread.
func writeJournalTitle(b *strings.Builder, m domain.Memory, predID int64) {
	if predID > 0 {
		fmt.Fprintf(b, "- id %d · %s  (⭱ viene de id %d — usa mem_thread para el hilo)\n", m.ID, m.Title, predID)
		return
	}
	fmt.Fprintf(b, "- id %d · %s\n", m.ID, m.Title)
}

// continuesPredecessorID devuelve el id local de la fase anterior enlazada por 'continues', o 0 si no
// hay enlace (o falla la búsqueda). Es tolerante: cualquier error se degrada a "sin miga", nunca
// tumba la inyección.
func (c *ContextService) continuesPredecessorID(ctx context.Context, m domain.Memory) int64 {
	if m.SyncID == "" {
		return 0
	}
	rel, err := c.relations.FindBySourceKind(ctx, m.SyncID, domain.RelationContinues)
	if err != nil || rel == nil || rel.TargetID == "" {
		return 0
	}
	pred, err := c.memories.GetBySyncID(ctx, rel.TargetID)
	if err != nil || pred == nil {
		return 0
	}
	return pred.ID
}

// healthBanners deriva los avisos de la bandeja "Salud de memoria": conflictos reales sin resolver
// (contado aparte, en openConflicts), normativas sin verificar contra conflictos (derivado de las
// cargadas para la higiene) y aviso de mantenimiento cuando hay demasiadas (§20.D/§20.F).
func healthBanners(normatives []domain.Memory, openConflicts, concurrent, threshold int) []string {
	var out []string
	unverified := 0
	for _, m := range normatives {
		if !m.ConflictChecked {
			unverified++
		}
	}
	if concurrent > 0 {
		out = append(out, fmt.Sprintf("- ⚠️ %d memoria(s) con edición concurrente (se editó aquí y en otra máquina a la vez; NO se pisó nada). Revisa y decide con `kandel sync conflicts`", concurrent))
	}
	if openConflicts > 0 {
		out = append(out, fmt.Sprintf("- ⚠️ %d conflicto(s) sin resolver entre normativas (contradicciones detectadas; revísalas y decide cuál prevalece)", openConflicts))
	}
	if unverified > 0 {
		out = append(out, fmt.Sprintf("- ⚠️ %d normativa(s) sin verificar contra conflictos (aún se obedecen; revisa que no se contradigan)", unverified))
	}
	if threshold > 0 && len(normatives) >= threshold {
		out = append(out, fmt.Sprintf("- ⚠️ demasiadas normativas activas (%d ≥ %d): considera consolidar o archivar", len(normatives), threshold))
	}
	return out
}
