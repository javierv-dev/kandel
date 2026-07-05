// Command kandel es el punto de entrada del binario. Construye las dependencias concretas
// (base de datos SQLite, embebedor Ollama, índice vectorial, servicios) y delega el despacho
// de comandos en el adaptador de entrada internal/port_in/cli. Es el único lugar donde se
// decide qué implementación concreta hay detrás de cada interfaz (wiring).
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/javierv-dev/kandel/internal/adapter/clock"
	"github.com/javierv-dev/kandel/internal/adapter/embedding"
	"github.com/javierv-dev/kandel/internal/adapter/git"
	"github.com/javierv-dev/kandel/internal/adapter/llm"
	"github.com/javierv-dev/kandel/internal/adapter/release"
	"github.com/javierv-dev/kandel/internal/adapter/sqlite"
	"github.com/javierv-dev/kandel/internal/config"
	"github.com/javierv-dev/kandel/internal/diagnostic"
	"github.com/javierv-dev/kandel/internal/port_in/cli"
	"github.com/javierv-dev/kandel/internal/service"
	"github.com/javierv-dev/kandel/internal/setup"
)

// installedAssetsProbe consulta la versión de los assets instalados del agente primario
// (claude-code) para el chequeo de desalineación (§21 Eje 3). Cumple diagnostic.InstalledAssetsProbe.
type installedAssetsProbe struct{}

func (installedAssetsProbe) InstalledVersion() (string, bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	return setup.InstalledVersion("claude-code", home)
}

// version, commit y buildDate se inyectan en tiempo de compilación con
// -ldflags "-X main.version=... -X main.commit=... -X main.buildDate=..." (§21 Eje 5).
// En construcciones locales sin release conservan estos valores por defecto.
var (
	version   = "dev"
	commit    = ""
	buildDate = ""
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "kandel:", err)
		os.Exit(1)
	}
}

// run construye la App con sus dependencias y despacha el comando. Se separa de main para que
// el manejo de errores quede en un solo sitio y el código sea testeable.
func run(args []string) error {
	ctx := context.Background()

	dbPath, err := databasePath()
	if err != nil {
		return err
	}
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	// Versión de esquema ya migrada, para el diagnóstico de `kandel version` (§21 Eje 5). Si la
	// lectura falla queda en 0; no es motivo para abortar el arranque.
	schemaVersion, _ := db.SchemaVersion(ctx)

	// Adaptadores concretos detrás de los ports.
	clk := clock.System{}
	memoryRepo := sqlite.NewMemoryRepo(db)
	sessionRepo := sqlite.NewSessionRepo(db)
	promptRepo := sqlite.NewPromptRepo(db)
	relationRepo := sqlite.NewRelationRepo(db)
	syncRepo := sqlite.NewSyncRepo(db)
	vectorIndex := sqlite.NewVectorIndex(db)
	titleVectorIndex := sqlite.NewTitleVectorIndex(db)
	uow := sqlite.NewUnitOfWork(db)
	agentRunner := llm.FromEnv()
	embedder := embedding.NewOllama(
		config.Resolve(config.OllamaHost),
		config.Resolve(config.EmbedModel),
		embedDimensions(),
	)

	// Casos de uso (service/).
	memoryService := service.NewMemoryService(memoryRepo, syncRepo, uow, clk, embedder, vectorIndex,
		service.WithWarnLogger(embeddingWarnLogger()),
		service.WithTitleVectors(titleVectorIndex))
	sessionService := service.NewSessionService(sessionRepo, syncRepo, uow, clk)
	lifecycleService := service.NewLifecycleService(memoryRepo)
	promptService := service.NewPromptService(promptRepo, syncRepo, uow, clk)
	conflictService := service.NewConflictService(memoryRepo, vectorIndex, embedder, relationRepo, agentRunner, uow, clk,
		service.WithDetectionTuning(conflictCutoff(), conflictTopK()),
		service.WithFactualTuning(conflictFactualCutoff()),
		service.WithTitleGate(titleVectorIndex, conflictTitleCutoff()))
	// El backfill de vectores engancha la detección de conflictos diferida: al ponerse al día los
	// vectores, se barren las normativas sin verificar (§20.F). Acoplamiento flojo vía callback.
	embeddingService := service.NewEmbeddingService(memoryRepo, embedder, vectorIndex,
		service.WithPostBackfill(detectionSweep(conflictService)),
		service.WithTitleBackfill(titleVectorIndex))
	syncService := service.NewSyncService(syncRepo, git.New(syncDir()), memoryRepo, uow, clk, "")
	contextService := service.NewContextService(sessionRepo, memoryRepo, relationRepo,
		service.WithNormativeThreshold(normativeThreshold()))
	projectMigrator := service.NewProjectMigrator(memoryRepo, sessionRepo, promptRepo, uow)
	// El seed construye su generador bajo demanda: el agente y el modelo se resuelven en el comando
	// (flags > entorno > config), no aquí, porque dependen de cómo se invoque.
	seedService := service.NewSeedService(memoryService, llm.Generator)
	// Chequeo de versión nueva (§21 Eje 6): única llamada de red saliente, opcional y cacheada.
	updateChecker := release.NewChecker()
	doctor := diagnostic.NewRunner(
		diagnostic.EmbedderCheck{Embedder: embedder},
		diagnostic.PendingEmbeddingsCheck{Vectors: vectorIndex, TitleVectors: titleVectorIndex, Model: embedder.Model()},
		diagnostic.InstalledAssetsCheck{Running: version, Probe: installedAssetsProbe{}},
		diagnostic.LatestReleaseCheck{Running: version, Checker: updateChecker},
	)

	app := &cli.App{
		Memory:    memoryService,
		Session:   sessionService,
		Lifecycle: lifecycleService,
		Embedding: embeddingService,
		Prompt:    promptService,
		Doctor:    doctor,
		Conflict:  conflictService,
		Sync:      syncService,
		Context:   contextService,
		Project:   projectMigrator,
		Seed:      seedService,
		In:        os.Stdin,
		Out:       os.Stdout,
		Err:       os.Stderr,
		Version:   version,
		Commit:    commit,
		BuildDate: buildDate,
		Schema:    schemaVersion,
		Update:    updateChecker,
	}
	return cli.Dispatch(ctx, args, app)
}

// databasePath resuelve la ruta del archivo SQLite. KANDEL_DATA_DIR la sobreescribe; por
// defecto vive en ~/.kandel/kandel.db (mismo lugar que el proyecto viejo).
func databasePath() (string, error) {
	dataDir := strings.TrimSpace(os.Getenv("KANDEL_DATA_DIR"))
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("no se pudo resolver el directorio del usuario: %w", err)
		}
		dataDir = filepath.Join(home, ".kandel")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return "", fmt.Errorf("no se pudo crear el directorio de datos %q: %w", dataDir, err)
	}
	return filepath.Join(dataDir, "kandel.db"), nil
}

// syncDir resuelve el directorio donde viven los chunks de sincronización. KANDEL_SYNC_DIR lo
// sobreescribe; por defecto es ".kandel" relativo al directorio de trabajo (pensado para vivir
// dentro del repositorio del proyecto y versionarse con git).
func syncDir() string {
	return config.Resolve(config.SyncDir)
}

// detectionSweep envuelve el barrido de detección de conflictos como el callback que dispara el
// backfill de vectores. Registra el resultado; nunca propaga error (es best-effort).
func detectionSweep(c *service.ConflictService) func(context.Context) {
	return func(ctx context.Context) {
		n, err := c.SweepDetection(ctx)
		switch {
		case err != nil:
			log.Printf("detección de conflictos: error tras %d normativa(s): %v", n, err)
		case n > 0:
			log.Printf("detección de conflictos: %d normativa(s) verificada(s)", n)
		}
	}
}

// conflictCutoff lee el corte de similitud de la detección de KANDEL_CONFLICT_SIM_CUTOFF; 0 (o
// inválido) deja el valor por defecto del servicio.
func conflictCutoff() float64 {
	raw := strings.TrimSpace(config.Resolve(config.ConflictCutoff))
	if raw == "" {
		return 0
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || f <= 0 {
		return 0
	}
	return f
}

// conflictFactualCutoff lee el corte de la pasada de supersede factual de
// KANDEL_CONFLICT_FACTUAL_CUTOFF; 0 (o inválido) deja el valor por defecto del servicio.
func conflictFactualCutoff() float64 {
	raw := strings.TrimSpace(config.Resolve(config.ConflictFactualCutoff))
	if raw == "" {
		return 0
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || f <= 0 {
		return 0
	}
	return f
}

// conflictTitleCutoff lee el corte del portón por título de KANDEL_CONFLICT_TITLE_CUTOFF; 0 (o
// inválido) deja el valor por defecto del servicio.
func conflictTitleCutoff() float64 {
	raw := strings.TrimSpace(config.Resolve(config.ConflictTitleCutoff))
	if raw == "" {
		return 0
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || f <= 0 {
		return 0
	}
	return f
}

// conflictTopK lee el top-K de la detección de KANDEL_CONFLICT_TOPK; 0 (o inválido) deja el valor
// por defecto del servicio.
func conflictTopK() int {
	raw := strings.TrimSpace(config.Resolve(config.ConflictTopK))
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// normativeThreshold lee el umbral del aviso "demasiadas normativas" de KANDEL_NORMATIVE_THRESHOLD;
// 0 (o inválido) deja el valor por defecto del servicio.
func normativeThreshold() int {
	raw := strings.TrimSpace(config.Resolve(config.NormativeThreshold))
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// embedDimensions lee la dimensión del vector de KANDEL_EMBED_DIMENSIONS; 0 deja que el
// embebedor use su valor por defecto.
func embedDimensions() int {
	raw := strings.TrimSpace(config.Resolve(config.EmbedDimensions))
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// embeddingWarnLogger devuelve el destino de los avisos del MemoryService (p. ej. "memoria guardada
// sin vector porque el embebedor está caído"). Anexa al log de embeddings (~/.kandel/logs/embeddings.log,
// o KANDEL_LOG_DIR si está definido). Si no se puede resolver la ruta, escribe a stderr. Nunca
// entra en pánico: la observabilidad es deseable, no crítica.
func embeddingWarnLogger() func(string, ...any) {
	dir := strings.TrimSpace(config.Resolve(config.LogDir))
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, ".kandel", "logs")
		}
	}
	return func(format string, args ...any) {
		line := time.Now().Format("2006-01-02 15:04:05") + " [embeddings] " + fmt.Sprintf(format, args...) + "\n"
		if dir == "" {
			fmt.Fprint(os.Stderr, line)
			return
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprint(os.Stderr, line)
			return
		}
		f, err := os.OpenFile(filepath.Join(dir, "embeddings.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprint(os.Stderr, line)
			return
		}
		defer f.Close()
		_, _ = f.WriteString(line)
	}
}
