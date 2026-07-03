package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// Límites de cuántos elementos entran en cada bandeja del contexto inyectado. Las normativas van
// SIN tope (son must-see); el resto se acota para no inundar la ventana del agente al arrancar.
const (
	contextSessions = 5
	// contextFactual es la ventana chica de continuidad factual: unas pocas recientes como ancla;
	// el resto se recupera bajo demanda con mem_search durante la tarea (factual perezoso, §20.D).
	contextFactual = 8
	// contextPending es un tope generoso de pendientes (en la práctica hay pocos).
	contextPending = 100
	// defaultNormativeThreshold dispara el aviso de mantenimiento "demasiadas normativas".
	defaultNormativeThreshold = 50
)

// ContextService arma el bloque de contexto que se inyecta al agente al arrancar una sesión (o tras
// una compactación). Se organiza en bandejas (§20.D): normativas siempre presentes, pendientes por
// resolver, factuales recientes, sesiones y salud de memoria. Es lectura directa (no depende del
// embebedor). Los prompts crudos ya NO se inyectan (solo quedan buscables).
type ContextService struct {
	sessions  ports.SessionRepo
	memories  ports.MemoryRepo
	relations ports.RelationRepo
	threshold int // umbral del aviso "demasiadas normativas"
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

// NewContextService construye el servicio con sus repos inyectados.
func NewContextService(sessions ports.SessionRepo, memories ports.MemoryRepo, relations ports.RelationRepo, opts ...ContextOption) *ContextService {
	c := &ContextService{sessions: sessions, memories: memories, relations: relations, threshold: defaultNormativeThreshold}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Format devuelve el contexto del proyecto en Markdown, en bandejas, listo para inyectar. Si no hay
// nada que mostrar devuelve cadena vacía (el hook entonces no inyecta nada).
func (c *ContextService) Format(ctx context.Context, project string, scope domain.Scope) (string, error) {
	project = normalizeProject(project)

	// Bandeja "Siempre": normativas (decision/preference), sin tope, sin reemplazadas ni resueltas.
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

	// Bandeja "Pendientes": resolution = open (excluye reemplazadas dentro del repo).
	pendings, err := c.memories.PendingResolution(ctx, project, contextPending)
	if err != nil {
		return "", err
	}

	// Bandeja "Recientes" (factual perezoso): ventana chica de factuales por recencia.
	factual, err := c.memories.Recent(ctx, ports.RecentQuery{
		Project:           project,
		Scope:             scope,
		Types:             []domain.MemoryType{domain.TypeDiscovery, domain.TypeArchitecture, domain.TypeBugfix},
		ExcludeSuperseded: true,
		Limit:             contextFactual,
	})
	if err != nil {
		return "", err
	}

	sessions, err := c.sessions.Recent(ctx, project, contextSessions)
	if err != nil {
		return "", err
	}

	// Conflictos reales sin resolver (contradicciones mismo-scope que decide un humano). Se cuentan
	// aparte porque viven en memory_relations, no en las normativas ya cargadas. Un fallo aquí no debe
	// tumbar la inyección entera: se degrada a 0 (sin banner de conflictos).
	openConflicts, err := c.relations.CountOpenConflicts(ctx, project)
	if err != nil {
		openConflicts = 0
	}

	health := healthBanners(normatives, openConflicts, c.threshold)

	if len(normatives) == 0 && len(pendings) == 0 && len(factual) == 0 && len(sessions) == 0 && len(health) == 0 {
		return "", nil
	}

	var b strings.Builder
	b.WriteString("## Memoria de sesiones anteriores\n\n")

	if len(normatives) > 0 {
		b.WriteString("### Siempre presente (normativas)\n")
		for _, m := range normatives {
			writeMemoryLine(&b, m)
		}
		b.WriteString("\n")
	}

	if len(pendings) > 0 {
		b.WriteString("### Pendientes por resolver\n")
		for _, m := range pendings {
			writeMemoryLine(&b, m)
		}
		b.WriteString("\n")
	}

	if len(factual) > 0 {
		b.WriteString("### Recientes\n")
		for _, m := range factual {
			writeMemoryLine(&b, m)
		}
		b.WriteString("\n")
	}

	if len(sessions) > 0 {
		b.WriteString("### Sesiones recientes\n")
		for _, s := range sessions {
			summary := ""
			if t := strings.TrimSpace(s.Summary); t != "" {
				summary = ": " + truncate(t, 200)
			}
			fmt.Fprintf(&b, "- **%s** (%s)%s\n", s.Project, s.StartedAt.Format("2006-01-02 15:04"), summary)
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

// writeMemoryLine escribe una memoria como una viñeta uniforme (tipo, título y contenido recortado).
func writeMemoryLine(b *strings.Builder, m domain.Memory) {
	fmt.Fprintf(b, "- [%s] **%s**: %s\n", m.Type, m.Title, truncate(m.Content, 300))
}

// healthBanners deriva los avisos de la bandeja "Salud de memoria": conflictos reales sin resolver
// (contado aparte, en openConflicts), normativas sin verificar contra conflictos (derivado de las ya
// cargadas para la bandeja "Siempre") y aviso de mantenimiento cuando hay demasiadas (§20.D/§20.F).
func healthBanners(normatives []domain.Memory, openConflicts, threshold int) []string {
	var out []string
	unverified := 0
	for _, m := range normatives {
		if !m.ConflictChecked {
			unverified++
		}
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

// truncate recorta un texto a n runas, añadiendo una elipsis si lo recortó.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
