package diagnostic

import (
	"context"
	"fmt"
	"strings"

	"github.com/javierv-dev/kandel/internal/ports"
)

// EmbedderProbe es lo que el chequeo del embebedor necesita: intentar convertir un texto en
// vector. Lo cumple ports.Embedder.
type EmbedderProbe interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	Model() string
}

// EmbedderCheck verifica que el embebedor responde. Si no, la BÚSQUEDA SEMÁNTICA del agente queda
// inutilizada (no hay alternativa léxica), pero las lecturas directas del usuario siguen
// funcionando: por eso es warning, no error.
type EmbedderCheck struct {
	Embedder EmbedderProbe
}

// Name identifica el chequeo.
func (EmbedderCheck) Name() string { return "embedder" }

// Run intenta embeber un texto de sondeo.
func (c EmbedderCheck) Run(ctx context.Context) []Finding {
	if c.Embedder == nil {
		return []Finding{{
			Check:    c.Name(),
			Severity: SeverityWarning,
			Message:  "no hay embebedor configurado; la búsqueda semántica del agente no está disponible",
		}}
	}
	if _, err := c.Embedder.Embed(ctx, "ping"); err != nil {
		return []Finding{{
			Check:    c.Name(),
			Severity: SeverityWarning,
			Message:  fmt.Sprintf("el embebedor (%s) no responde: %s", c.Embedder.Model(), err),
			NextStep: "verifica que Ollama esté corriendo y el modelo descargado (ollama pull <modelo>); las lecturas directas siguen funcionando",
		}}
	}
	return []Finding{{
		Check:    c.Name(),
		Severity: SeverityInfo,
		Message:  fmt.Sprintf("el embebedor (%s) responde correctamente", c.Embedder.Model()),
	}}
}

// InstalledAssetsProbe reporta la versión de los assets del agente ya instalados (skill/hooks/MCP).
// Lo cumple un adaptador sobre internal/setup; se inyecta para no acoplar diagnostic al instalador.
type InstalledAssetsProbe interface {
	InstalledVersion() (version string, present bool, err error)
}

// InstalledAssetsCheck compara la versión de los assets instalados con la del binario en ejecución
// (§21 Eje 3). Si difieren, el protocolo del agente puede haber quedado viejo respecto al binario
// (típico tras actualizar kandel sin reinstalar). Es warning, no error: kandel sigue operando.
type InstalledAssetsCheck struct {
	Running string // versión del binario en ejecución
	Probe   InstalledAssetsProbe
}

// Name identifica el chequeo.
func (InstalledAssetsCheck) Name() string { return "installed_assets" }

// Run compara versiones. Sin probe o sin instalación previa no hay nada que verificar.
func (c InstalledAssetsCheck) Run(_ context.Context) []Finding {
	if c.Probe == nil {
		return nil
	}
	installed, present, err := c.Probe.InstalledVersion()
	if err != nil {
		return []Finding{{
			Check:    c.Name(),
			Severity: SeverityWarning,
			Message:  "no se pudo leer la versión de los assets instalados: " + err.Error(),
		}}
	}
	if !present {
		return []Finding{{
			Check:    c.Name(),
			Severity: SeverityInfo,
			Message:  "no se detectó una instalación de agente por kandel",
		}}
	}
	if installed != c.Running {
		return []Finding{{
			Check:    c.Name(),
			Severity: SeverityWarning,
			Message:  fmt.Sprintf("los assets instalados (%s) no coinciden con el binario (%s)", installed, c.Running),
			NextStep: "reinstala para alinear skill/hooks/MCP con el binario: kandel install claude-code",
		}}
	}
	return []Finding{{
		Check:    c.Name(),
		Severity: SeverityInfo,
		Message:  fmt.Sprintf("los assets instalados están al día (%s)", installed),
	}}
}

// LatestReleaseCheck avisa si hay una versión de kandel más nueva que la que corre (§21 Eje 6).
// Consulta un ports.ReleaseChecker (que cachea y degrada en silencio). Es warning cuando hay una
// versión nueva (acción sugerida: actualizar), info si está al día, y no reporta nada si no se pudo
// determinar (offline/desactivado): un chequeo opcional no debe ensuciar el diagnóstico.
type LatestReleaseCheck struct {
	Running string
	Checker ports.ReleaseChecker
}

// Name identifica el chequeo.
func (LatestReleaseCheck) Name() string { return "latest_release" }

// Run consulta la última versión y compara.
func (c LatestReleaseCheck) Run(ctx context.Context) []Finding {
	if c.Checker == nil {
		return nil
	}
	latest, newer := c.Checker.Newer(ctx, c.Running)
	if latest == "" {
		return nil // no se pudo determinar: silencio
	}
	if newer {
		return []Finding{{
			Check:    c.Name(),
			Severity: SeverityWarning,
			Message:  fmt.Sprintf("hay una versión nueva de kandel disponible (%s); tienes %s", latest, c.Running),
			NextStep: "descarga el binario nuevo de las releases y reinstala: kandel install claude-code",
		}}
	}
	return []Finding{{
		Check:    c.Name(),
		Severity: SeverityInfo,
		Message:  fmt.Sprintf("kandel está en la última versión (%s)", latest),
	}}
}

// PendingEmbeddingsCheck cuenta cuántas memorias vivas no tienen su vector al día (faltante o de otro
// modelo), tanto el del CONTENIDO como el del TÍTULO. No es un fallo: son memorias accesibles por
// lectura directa pero invisibles a la búsqueda semántica (contenido) o a la detección de
// casi-duplicados por título (título) hasta reindexarlas. Por eso es info/warning, no error.
type PendingEmbeddingsCheck struct {
	Vectors      ports.VectorIndex
	TitleVectors ports.VectorIndex // opcional; si se da, también verifica el vector del título
	Model        string
}

// Name identifica el chequeo.
func (PendingEmbeddingsCheck) Name() string { return "pending_embeddings" }

// Run cuenta las memorias pendientes de vector de contenido y de título.
func (c PendingEmbeddingsCheck) Run(ctx context.Context) []Finding {
	if c.Vectors == nil {
		return nil
	}
	contentIDs, err := c.Vectors.PendingEmbedding(ctx, c.Model, 0)
	if err != nil {
		return []Finding{{
			Check:    c.Name(),
			Severity: SeverityError,
			Message:  "no se pudo consultar las memorias pendientes de vector de contenido: " + err.Error(),
		}}
	}
	var titleIDs []int64
	if c.TitleVectors != nil {
		titleIDs, err = c.TitleVectors.PendingEmbedding(ctx, c.Model, 0)
		if err != nil {
			return []Finding{{
				Check:    c.Name(),
				Severity: SeverityError,
				Message:  "no se pudo consultar las memorias pendientes de vector de título: " + err.Error(),
			}}
		}
	}
	if len(contentIDs) == 0 && len(titleIDs) == 0 {
		return []Finding{{
			Check:    c.Name(),
			Severity: SeverityInfo,
			Message:  "todas las memorias tienen su vector (contenido y título) al día",
		}}
	}
	var parts []string
	if len(contentIDs) > 0 {
		parts = append(parts, fmt.Sprintf("%d sin vector de contenido (no aparecen en la búsqueda semántica)", len(contentIDs)))
	}
	if len(titleIDs) > 0 {
		parts = append(parts, fmt.Sprintf("%d sin vector de título (no participan en la detección de casi-duplicados por título)", len(titleIDs)))
	}
	return []Finding{{
		Check:    c.Name(),
		Severity: SeverityWarning,
		Message:  "memorias con vector pendiente: " + strings.Join(parts, "; "),
		NextStep: "corre `kandel embeddings backfill` para reindexarlas",
	}}
}

// UsageCounter cuenta las memorias que se inyectan y nunca se usan. La service.UsageService la
// cumple. Se declara aquí, en el consumidor, para que diagnostic no dependa de service.
type UsageCounter interface {
	CountDormant(ctx context.Context) (int, error)
}

// MemoryUsageCheck avisa de las memorias "dormidas": se han inyectado suficientes veces como para
// juzgarlas y el agente nunca las mencionó. Son peso constante en el contexto sin rendimiento
// observable, y por tanto las primeras candidatas a reescribir o podar.
//
// Es warning y no error a propósito: una memoria dormida no rompe nada, solo cuesta tokens. Y el
// dato tarda en tener sentido —una instalación recién actualizada no tiene historial de inyecciones
// todavía—, así que sin ninguna dormida el chequeo se limita a informar.
type MemoryUsageCheck struct {
	Usage UsageCounter
}

// Name identifica el chequeo.
func (MemoryUsageCheck) Name() string { return "memory_usage" }

// Run cuenta las memorias dormidas y las reporta.
func (c MemoryUsageCheck) Run(ctx context.Context) []Finding {
	if c.Usage == nil {
		return nil
	}
	n, err := c.Usage.CountDormant(ctx)
	if err != nil {
		return []Finding{{
			Check:    c.Name(),
			Severity: SeverityError,
			Message:  "no se pudo consultar la telemetría de uso de la memoria: " + err.Error(),
		}}
	}
	if n == 0 {
		return []Finding{{
			Check:    c.Name(),
			Severity: SeverityInfo,
			Message:  "ninguna memoria se está inyectando repetidamente sin usarse",
		}}
	}
	return []Finding{{
		Check:    c.Name(),
		Severity: SeverityWarning,
		Message:  fmt.Sprintf("%d memoria(s) inyectada(s) repetidamente y nunca mencionada(s) por el agente", n),
		NextStep: "revísalas con `kandel usage`: reescríbelas para que digan cuándo hacen falta, o bórralas",
	}}
}
