package cli

import (
	"context"
	"flag"
	"fmt"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/service"
)

// saveCmd guarda una memoria curada. Título y contenido son posicionales; el resto, flags.
type saveCmd struct{}

func init() { register(saveCmd{}) }

func (saveCmd) Name() string     { return "save" }
func (saveCmd) Synopsis() string { return "guarda una memoria nueva" }

func (saveCmd) Run(ctx context.Context, args []string, app *App) error {
	fs := flag.NewFlagSet("save", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	typ := fs.String("type", "", "tipo de memoria (decision|architecture|bugfix|pattern|config|discovery|learning)")
	project := fs.String("project", "", "proyecto (por defecto KANDEL_PROJECT)")
	scope := fs.String("scope", "", "alcance (project|personal|global)")
	topic := fs.String("topic", "", "topic_key para fusionar revisiones del mismo tema")
	session := fs.String("session", "", "id de sesión asociada")
	tool := fs.String("tool", "", "herramienta que originó la memoria")
	fs.Usage = func() {
		fmt.Fprintln(app.Err, "uso: kandel save <título> <contenido> [--type TIPO] [--project P] [--scope S] [--topic K]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		fs.Usage()
		return fmt.Errorf("save: faltan título y contenido")
	}

	parsedScope, err := domain.ParseScope(*scope)
	if err != nil {
		return err
	}

	res, err := app.Memory.Save(ctx, service.SaveInput{
		SessionID: *session,
		Project:   resolveProject(*project),
		Type:      domain.MemoryType(*typ),
		Scope:     parsedScope,
		Title:     fs.Arg(0),
		Content:   fs.Arg(1),
		ToolName:  *tool,
		TopicKey:  *topic,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(app.Out, "%s memoria #%d: %s\n", res.Outcome, res.Memory.ID, res.Memory.Title)
	return nil
}
