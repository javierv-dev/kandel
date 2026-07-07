// Package cli es el adaptador de entrada de línea de comandos. Aplica el patrón Command:
// cada subcomando es un tipo que cumple la interfaz Command y se registra en un mapa de
// despacho. Añadir un comando = añadir un archivo y registrarlo, sin tocar el despachador
// (principio Open/Closed). Los comandos hablan SOLO con los casos de uso (service/) a través
// de las interfaces de App; nunca tocan repos ni SQL.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/javierv-dev/kandel/internal/diagnostic"
	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/hub"
	"github.com/javierv-dev/kandel/internal/ports"
	"github.com/javierv-dev/kandel/internal/service"
)

// MemoryUseCases es la cara de los casos de uso de memoria que la CLI consume. Se define como
// interfaz (no el *service.MemoryService concreto) para invertir la dependencia: los tests
// inyectan un App con dobles en memoria. *service.MemoryService la cumple por estructura.
type MemoryUseCases interface {
	Save(ctx context.Context, in service.SaveInput) (service.SaveResult, error)
	Get(ctx context.Context, id int64) (*domain.Memory, error)
	Recent(ctx context.Context, q ports.RecentQuery) ([]domain.Memory, error)
	Search(ctx context.Context, q ports.SearchQuery) ([]domain.SearchResult, error)
	Update(ctx context.Context, id int64, in service.UpdateInput) (domain.Memory, error)
	Delete(ctx context.Context, id int64) error
	Restore(ctx context.Context, id int64) (domain.Memory, error)
	Projects(ctx context.Context) ([]string, error)
}

// SessionUseCases es la cara de los casos de uso de sesión que la CLI consume.
type SessionUseCases interface {
	Start(ctx context.Context, in service.StartInput) (domain.Session, error)
	End(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (*domain.Session, error)
	MostRecentActive(ctx context.Context, project string) (string, bool, error)
}

// LifecycleUseCases es la cara de los casos de uso de ciclo de vida que la CLI consume.
type LifecycleUseCases interface {
	PendingResolution(ctx context.Context, project string, limit int) ([]domain.Memory, error)
}

// EmbeddingUseCases es la cara del mantenimiento de vectores que la CLI consume. Incluye las
// variantes automáticas (SweepBackfill, Warmup) porque App.Embedding también se cablea al daemon
// HTTP (barrido periódico + endpoints que disparan los hooks).
type EmbeddingUseCases interface {
	Backfill(ctx context.Context) (service.BackfillResult, error)
	SweepBackfill(ctx context.Context) (service.BackfillResult, bool, error)
	Warmup(ctx context.Context) error
	Reembed(ctx context.Context, id int64) error
}

// PromptUseCases es la cara del guardado de prompts. La CLI no la usa directamente todavía
// (no hay subcomando), pero la transporta en App para cablear el servidor MCP.
type PromptUseCases interface {
	Save(ctx context.Context, in service.SavePromptInput) (service.SavePromptResult, error)
}

// DoctorUseCases corre los chequeos de salud de solo lectura. La diagnostic.Runner la cumple.
type DoctorUseCases interface {
	Run(ctx context.Context) diagnostic.Report
}

// ConflictUseCases es la cara de la detección de conflictos. La CLI solo usa FindCandidates
// (read-only); Compare/RecordVerdict viajan en App para cablear el servidor MCP (igual que Prompt).
type ConflictUseCases interface {
	FindCandidates(ctx context.Context, memoryID int64, limit int) ([]service.Candidate, error)
	Compare(ctx context.Context, sourceID, targetID int64) (domain.MemoryRelation, error)
	RecordVerdict(ctx context.Context, sourceID, targetID int64, verdict domain.Verdict) (domain.MemoryRelation, error)
}

// SyncUseCases es la cara de la sincronización por change-feed contra el hub: subir lo local (Push),
// bajar lo ajeno (Pull) y gestionar los conflictos de edición concurrente (§6.5).
type SyncUseCases interface {
	Push(ctx context.Context) (service.PushResult, error)
	Pull(ctx context.Context) (service.PullResult, error)
	ListConflicts(ctx context.Context) ([]domain.SyncConflict, error)
	ResolveConflict(ctx context.Context, syncID string, takeRemote bool) error
}

// ContextUseCases arma el bloque de contexto del proyecto. La CLI no lo usa directamente; viaja
// en App para cablear el servidor HTTP (los hooks lo consumen vía GET /context).
type ContextUseCases interface {
	Format(ctx context.Context, project string, scope domain.Scope) (string, error)
}

// ProjectUseCases migra (renombra) un proyecto. Viaja en App para cablear el servidor HTTP (los
// hooks lo consumen vía POST /projects/migrate).
type ProjectUseCases interface {
	Migrate(ctx context.Context, oldProject, newProject string) (service.MigrateResult, error)
}

// SeedUseCases puebla la memoria de un proyecto existente: genera candidatos de decisión a partir de
// la documentación (LLM) y, tras aprobación, los guarda. Separa generación y guardado para meter el
// gate de aprobación entre ambos (ver Fase 12, §19 del plan).
type SeedUseCases interface {
	Generate(ctx context.Context, in service.SeedGenerateInput) ([]service.SeedCandidate, error)
	Save(ctx context.Context, candidates []service.SeedCandidate, project string) (service.SeedSaveResult, error)
}

// App agrupa los casos de uso ya construidos y los flujos de salida. El wiring real (qué
// adaptador concreto hay detrás de cada interfaz) vive en cmd/kandel/main.go; la CLI solo ve
// las interfaces.
type App struct {
	Memory    MemoryUseCases
	Session   SessionUseCases
	Lifecycle LifecycleUseCases
	Embedding EmbeddingUseCases
	Prompt    PromptUseCases
	Doctor    DoctorUseCases
	Conflict  ConflictUseCases
	Sync      SyncUseCases
	Context   ContextUseCases
	Project   ProjectUseCases
	Seed      SeedUseCases

	In  io.Reader // entrada para confirmaciones interactivas (por defecto os.Stdin)
	Out io.Writer
	Err io.Writer

	// Metadatos de build para `kandel version` (§21 Eje 5). Version es el SemVer; Commit y
	// BuildDate se inyectan por ldflags (vacíos en dev); Schema es la versión de esquema migrada.
	Version   string
	Commit    string
	BuildDate string
	Schema    int

	// Update consulta si hay una versión más nueva publicada (§21 Eje 6). Opcional: si es nil, el
	// comando version no añade la nota de actualización.
	Update ports.ReleaseChecker

	// OpenHub abre el almacén del hub de sincronización bajo demanda (solo lo usa `serve
	// --sync-hub`). Devuelve el store, una función para cerrarlo y un error. Es una fábrica para no
	// abrir la base del hub en cada invocación de la CLI. nil si el hub no está cableado.
	OpenHub func(ctx context.Context) (hub.Store, func() error, error)
}

// Command es un subcomando de la CLI. Name es la palabra que lo invoca; Synopsis es la línea
// de ayuda; Run ejecuta la acción con los argumentos ya sin el nombre del comando.
type Command interface {
	Name() string
	Synopsis() string
	Run(ctx context.Context, args []string, app *App) error
}

// registry mapea nombre → comando. Se puebla en los init() de cada archivo de comando.
var registry = map[string]Command{}

// register añade un comando al despachador. Se llama desde los init() de cada comando; un
// nombre duplicado es un error de programación y entra en pánico al arrancar.
func register(c Command) {
	if _, exists := registry[c.Name()]; exists {
		panic(fmt.Sprintf("cli: comando duplicado %q", c.Name()))
	}
	registry[c.Name()] = c
}

// Dispatch interpreta args (sin el nombre del binario) y ejecuta el comando correspondiente.
// Devuelve un error con el código de salida apropiado a través de su mensaje; main lo traduce.
func Dispatch(ctx context.Context, args []string, app *App) error {
	if app.Out == nil {
		app.Out = os.Stdout
	}
	if app.Err == nil {
		app.Err = os.Stderr
	}
	if app.In == nil {
		app.In = os.Stdin
	}
	if len(args) == 0 {
		printUsage(app)
		return nil
	}
	name := args[0]
	switch name {
	case "help", "--help", "-h":
		printUsage(app)
		return nil
	case "version", "--version", "-v":
		printVersion(app)
		printUpdateNote(ctx, app)
		return nil
	}
	cmd, ok := registry[name]
	if !ok {
		fmt.Fprintf(app.Err, "comando desconocido: %s\n\n", name)
		printUsage(app)
		return fmt.Errorf("comando desconocido: %s", name)
	}
	return cmd.Run(ctx, args[1:], app)
}

// printVersion imprime la versión y los metadatos de build (§21 Eje 5): commit y fecha (vacíos
// en construcciones dev => "desconocido") y la versión de esquema de la base ya migrada.
func printVersion(app *App) {
	fmt.Fprintf(app.Out, "kandel %s\n", app.Version)
	fmt.Fprintf(app.Out, "commit:  %s\n", orUnknown(app.Commit))
	fmt.Fprintf(app.Out, "built:   %s\n", orUnknown(app.BuildDate))
	fmt.Fprintf(app.Out, "schema:  %d\n", app.Schema)
}

// printUpdateNote añade, tras la versión, un aviso si hay una release más nueva (§21 Eje 6). No hace
// nada si no hay checker o si no se detecta versión nueva (offline/desactivado/al día).
func printUpdateNote(ctx context.Context, app *App) {
	if app.Update == nil {
		return
	}
	if latest, newer := app.Update.Newer(ctx, app.Version); newer {
		fmt.Fprintf(app.Out, "\nHay una versión nueva disponible: %s\nActualiza con: kandel install claude-code\n", latest)
	}
}

// orUnknown devuelve "desconocido" para cadenas vacías (metadatos no inyectados en dev).
func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "desconocido"
	}
	return s
}

// printUsage imprime la ayuda general listando los comandos registrados en orden alfabético.
func printUsage(app *App) {
	fmt.Fprintln(app.Out, "uso: kandel <comando> [argumentos]")
	fmt.Fprintln(app.Out, "\nComandos:")
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(app.Out, "  %-12s %s\n", n, registry[n].Synopsis())
	}
	fmt.Fprintln(app.Out, "  help         muestra esta ayuda")
	fmt.Fprintln(app.Out, "  version      muestra la versión")
}

// parseFlags interpreta args permitiendo que los flags aparezcan en CUALQUIER posición, incluso
// después de los argumentos posicionales. El paquete flag estándar deja de parsear en el primer
// no-flag (así `save "título" "contenido" --type X` perdería --type); este helper repite el
// parseo extrayendo los posicionales uno a uno. Devuelve los posicionales en orden.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		// El primer resto es un posicional; el flag se reanuda con lo que sigue.
		positionals = append(positionals, rest[0])
		rest = rest[1:]
	}
	return positionals, nil
}

// resolveProject aplica la cadena de resolución de proyecto para la CLI: el valor explícito del
// flag gana; si está vacío, se usa la variable de entorno KANDEL_PROJECT; si tampoco, queda
// vacío (lectura laxa / escritura estricta lo rechazará).
func resolveProject(explicit string) string {
	if p := strings.TrimSpace(explicit); p != "" {
		return p
	}
	return strings.TrimSpace(os.Getenv("KANDEL_PROJECT"))
}
