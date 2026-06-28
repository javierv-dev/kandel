package cli

import (
	"context"
	"fmt"
	nethttp "net/http"
	"os"
	"path/filepath"
	"time"
)

// statusCmd informa si el daemon HTTP responde y dónde viven la base de datos y los logs. Es la
// herramienta de seguimiento rápida para diagnosticar la integración con los agentes.
type statusCmd struct{}

func init() { register(statusCmd{}) }

func (statusCmd) Name() string     { return "status" }
func (statusCmd) Synopsis() string { return "informa el estado del daemon, la DB y los logs" }

func (statusCmd) Run(_ context.Context, _ []string, app *App) error {
	port := resolvePort()
	url := "http://127.0.0.1:" + port + "/health"

	client := &nethttp.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(app.Out, "daemon:   CAÍDO (%s)\n          %v\n", url, err)
	} else {
		resp.Body.Close()
		if resp.StatusCode == nethttp.StatusOK {
			fmt.Fprintf(app.Out, "daemon:   OK (%s)\n", url)
		} else {
			fmt.Fprintf(app.Out, "daemon:   responde pero con HTTP %d (%s)\n", resp.StatusCode, url)
		}
	}

	home, _ := os.UserHomeDir()
	dataDir := os.Getenv("KANDEL_DATA_DIR")
	if dataDir == "" && home != "" {
		dataDir = filepath.Join(home, ".kandel")
	}
	logDir := os.Getenv("KANDEL_LOG_DIR")
	if logDir == "" && home != "" {
		logDir = filepath.Join(home, ".kandel", "logs")
	}
	fmt.Fprintf(app.Out, "puerto:   %s (KANDEL_PORT)\n", port)
	fmt.Fprintf(app.Out, "base:     %s\n", filepath.Join(dataDir, "kandel.db"))
	fmt.Fprintf(app.Out, "logs:     %s\n", logDir)
	return nil
}
