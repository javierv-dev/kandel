package diagnostic

import (
	"context"
	"fmt"

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

// PendingEmbeddingsCheck cuenta cuántas memorias vivas no tienen vector al día (faltante o de otro
// modelo). No es un fallo: son memorias accesibles por lectura directa pero invisibles a la
// búsqueda semántica hasta reindexarlas. Por eso es info/warning, no error.
type PendingEmbeddingsCheck struct {
	Vectors ports.VectorIndex
	Model   string
}

// Name identifica el chequeo.
func (PendingEmbeddingsCheck) Name() string { return "pending_embeddings" }

// Run lista las memorias pendientes de vector.
func (c PendingEmbeddingsCheck) Run(ctx context.Context) []Finding {
	if c.Vectors == nil {
		return nil
	}
	ids, err := c.Vectors.PendingEmbedding(ctx, c.Model, 0)
	if err != nil {
		return []Finding{{
			Check:    c.Name(),
			Severity: SeverityError,
			Message:  "no se pudo consultar las memorias pendientes de vector: " + err.Error(),
		}}
	}
	if len(ids) == 0 {
		return []Finding{{
			Check:    c.Name(),
			Severity: SeverityInfo,
			Message:  "todas las memorias tienen su vector al día",
		}}
	}
	return []Finding{{
		Check:    c.Name(),
		Severity: SeverityWarning,
		Message:  fmt.Sprintf("%d memoria(s) sin vector al día; no aparecen en la búsqueda semántica", len(ids)),
		NextStep: "corre `kandel embeddings backfill` para reindexarlas",
	}}
}
