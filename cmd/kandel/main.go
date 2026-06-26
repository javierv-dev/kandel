// Command kandel es el punto de entrada del binario. Construye las dependencias concretas
// (base de datos SQLite, embebedor Ollama, índice vectorial, servicios) y delega el despacho
// de comandos en el adaptador de entrada internal/port_in/cli. Es el único lugar donde se
// decide qué implementación concreta hay detrás de cada interfaz (wiring).
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/javierv-dev/kandel/internal/adapter/clock"
	"github.com/javierv-dev/kandel/internal/adapter/embedding"
	"github.com/javierv-dev/kandel/internal/adapter/sqlite"
	"github.com/javierv-dev/kandel/internal/port_in/cli"
	"github.com/javierv-dev/kandel/internal/service"
)

// version se inyecta en tiempo de compilación con -ldflags "-X main.version=...".
// Mantiene el valor "dev" en construcciones locales sin release.
var version = "dev"

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

	// Adaptadores concretos detrás de los ports.
	clk := clock.System{}
	memoryRepo := sqlite.NewMemoryRepo(db)
	sessionRepo := sqlite.NewSessionRepo(db)
	promptRepo := sqlite.NewPromptRepo(db)
	syncRepo := sqlite.NewSyncRepo(db)
	vectorIndex := sqlite.NewVectorIndex(db)
	uow := sqlite.NewUnitOfWork(db)
	embedder := embedding.NewOllama(
		os.Getenv("KANDEL_OLLAMA_HOST"),
		os.Getenv("KANDEL_EMBED_MODEL"),
		embedDimensions(),
	)

	// Casos de uso (service/).
	memoryService := service.NewMemoryService(memoryRepo, syncRepo, uow, clk, embedder, vectorIndex)
	sessionService := service.NewSessionService(sessionRepo, syncRepo, uow, clk)
	lifecycleService := service.NewLifecycleService(memoryRepo)
	embeddingService := service.NewEmbeddingService(memoryRepo, embedder, vectorIndex)
	promptService := service.NewPromptService(promptRepo, syncRepo, uow, clk)

	app := &cli.App{
		Memory:    memoryService,
		Session:   sessionService,
		Lifecycle: lifecycleService,
		Embedding: embeddingService,
		Prompt:    promptService,
		Out:       os.Stdout,
		Err:       os.Stderr,
		Version:   version,
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

// embedDimensions lee la dimensión del vector de KANDEL_EMBED_DIMENSIONS; 0 deja que el
// embebedor use su valor por defecto.
func embedDimensions() int {
	raw := strings.TrimSpace(os.Getenv("KANDEL_EMBED_DIMENSIONS"))
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
