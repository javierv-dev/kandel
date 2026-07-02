package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/javierv-dev/kandel/internal/config"
	"github.com/javierv-dev/kandel/internal/project"
	"github.com/javierv-dev/kandel/internal/seedsource"
	"github.com/javierv-dev/kandel/internal/service"
)

// seedCmd puebla la memoria de un proyecto existente leyendo su documentación y dejando que un LLM
// extraiga las decisiones de alto valor. Entrada canónica y agente-independiente: se ejecuta en el
// repo ANTES de abrir cualquier agente (ver Fase 12, §19 del plan).
type seedCmd struct{}

func init() { register(seedCmd{}) }

func (seedCmd) Name() string { return "seed" }
func (seedCmd) Synopsis() string {
	return "puebla la memoria con las decisiones de un proyecto existente"
}

func (seedCmd) Run(ctx context.Context, args []string, app *App) error {
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	dir := fs.String("dir", "", "directorio del proyecto a analizar (por defecto el actual)")
	projectFlag := fs.String("project", "", "proyecto de las memorias (por defecto KANDEL_PROJECT o detección git)")
	agent := fs.String("agent", "", "CLI de agente para generar (por defecto KANDEL_AGENT_CLI o config)")
	model := fs.String("model", "", "modelo del agente (por defecto KANDEL_SEED_MODEL o config)")
	limit := fs.Int("limit", 0, "tope de memorias a proponer (0 = sin tope, el LLM decide cuántas decisiones reales hay)")
	dryRun := fs.Bool("dry-run", false, "solo muestra los candidatos, no guarda nada")
	yes := fs.Bool("yes", false, "guarda sin pedir confirmación (uso no interactivo)")
	fs.Usage = func() {
		fmt.Fprintln(app.Err, "uso: kandel seed [--dir D] [--project P] [--agent CLI] [--model M] [--limit N] [--dry-run] [--yes]")
		fs.PrintDefaults()
	}
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}

	workDir := strings.TrimSpace(*dir)
	if workDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("seed: no se pudo resolver el directorio actual: %w", err)
		}
		workDir = cwd
	}

	agentCLI := resolveAgentCLI(*agent)
	if agentCLI == "" {
		return fmt.Errorf("seed: no hay CLI de agente configurada; usa --agent, la variable KANDEL_AGENT_CLI o reinstala kandel para un agente")
	}

	// 1. Reunir la documentación del proyecto (lectura pura, sin LLM ni embebedor).
	sources, err := seedsource.Collect(ctx, workDir)
	if err != nil {
		return fmt.Errorf("seed: no se pudo leer el directorio %q: %w", workDir, err)
	}

	// 2. Generar candidatos (LLM).
	fmt.Fprintf(app.Out, "Analizando %q con %q…\n", workDir, agentCLI)
	candidates, err := app.Seed.Generate(ctx, service.SeedGenerateInput{
		Sources:  sources,
		AgentCLI: agentCLI,
		Model:    resolveSeedModel(*model),
		Limit:    *limit,
	})
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		fmt.Fprintln(app.Out, "No se encontraron decisiones de alto valor para guardar.")
		return nil
	}

	printCandidates(app, candidates)

	// 3. Gate: dry-run no escribe; sin --yes pide confirmación.
	if *dryRun {
		fmt.Fprintln(app.Out, "\n(dry-run) No se guardó nada.")
		return nil
	}
	if !*yes && !confirm(app, len(candidates)) {
		fmt.Fprintln(app.Out, "Cancelado. No se guardó nada.")
		return nil
	}

	// 4. Guardar (con pre-chequeo del embebedor dentro del servicio).
	proj := resolveProject(*projectFlag)
	if proj == "" {
		proj = project.Detect(workDir).Project
	}
	res, err := app.Seed.Save(ctx, candidates, proj)
	if err != nil {
		return err
	}
	fmt.Fprintf(app.Out, "\nGuardadas: %d nuevas, %d fusionadas, %d duplicadas, %d fallidas.\n",
		res.Created, res.Merged, res.Duplicate, res.Failed)
	for _, e := range res.Errors {
		fmt.Fprintf(app.Err, "  ! %s\n", e)
	}
	return nil
}

// printCandidates muestra los candidatos numerados con su tipo, alcance y título.
func printCandidates(app *App, candidates []service.SeedCandidate) {
	fmt.Fprintf(app.Out, "\nCandidatos propuestos (%d):\n", len(candidates))
	for i, c := range candidates {
		fmt.Fprintf(app.Out, "  %d. (%s/%s) %s\n", i+1, c.Type, c.Scope, c.Title)
	}
}

// confirm pregunta al usuario si guardar los candidatos, leyendo una respuesta de app.In.
func confirm(app *App, n int) bool {
	fmt.Fprintf(app.Out, "\n¿Guardar estas %d memorias? [y/N]: ", n)
	reader := bufio.NewReader(app.In)
	line, _ := reader.ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes" || answer == "s" || answer == "si" || answer == "sí"
}

// resolveAgentCLI aplica la cadena de resolución de la CLI de agente: flag > (entorno > config local).
// El flag explícito manda; el resto lo resuelve config (env KANDEL_AGENT_CLI > archivo).
func resolveAgentCLI(explicit string) string {
	if v := strings.TrimSpace(explicit); v != "" {
		return v
	}
	return strings.TrimSpace(config.Resolve(config.AgentCLI))
}

// resolveSeedModel aplica la cadena de resolución del modelo: flag > (entorno > config local). Vacío
// deja que el adaptador llm elija su modelo por defecto del seed.
func resolveSeedModel(explicit string) string {
	if v := strings.TrimSpace(explicit); v != "" {
		return v
	}
	return strings.TrimSpace(config.Resolve(config.SeedModel))
}
