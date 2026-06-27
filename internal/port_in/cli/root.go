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
	Delete(ctx context.Context, id int64, hard bool) error
}

// SessionUseCases es la cara de los casos de uso de sesión que la CLI consume.
type SessionUseCases interface {
	Start(ctx context.Context, in service.StartInput) (domain.Session, error)
	End(ctx context.Context, id, summary string) error
	Get(ctx context.Context, id string) (*domain.Session, error)
	MostRecentActive(ctx context.Context, project string) (string, bool, error)
}

// LifecycleUseCases es la cara de los casos de uso de ciclo de vida que la CLI consume.
type LifecycleUseCases interface {
	Pin(ctx context.Context, id int64) error
	Unpin(ctx context.Context, id int64) error
	NeedingReview(ctx context.Context, project string, limit int) ([]domain.Memory, error)
}

// EmbeddingUseCases es la cara del mantenimiento de vectores que la CLI consume.
type EmbeddingUseCases interface {
	Backfill(ctx context.Context) (service.BackfillResult, error)
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

// SyncUseCases es la cara de la sincronización: exportar la cola e importar chunks ajenos.
type SyncUseCases interface {
	Export(ctx context.Context) (service.ExportResult, error)
	Import(ctx context.Context) (service.ImportResult, error)
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

	Out     io.Writer
	Err     io.Writer
	Version string
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
		fmt.Fprintf(app.Out, "kandel %s\n", app.Version)
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
