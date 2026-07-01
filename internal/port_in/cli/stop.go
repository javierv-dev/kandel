package cli

import (
	"context"
	"fmt"
	nethttp "net/http"
	"time"
)

// stopCmd detiene el daemon HTTP de forma ordenada (POST /shutdown). Los hooks arrancan el daemon
// `kandel serve` como proceso desacoplado que sobrevive al agente; como corre la imagen del propio
// binario, en Windows lo mantiene bloqueado e impide reemplazar el .exe al actualizar. `kandel stop`
// lo apaga para liberar el binario; el siguiente hook lo vuelve a levantar con la versión nueva.
type stopCmd struct{}

func init() { register(stopCmd{}) }

func (stopCmd) Name() string { return "stop" }
func (stopCmd) Synopsis() string {
	return "detiene el daemon HTTP (libera el binario para actualizar)"
}

func (stopCmd) Run(ctx context.Context, _ []string, app *App) error {
	base := "http://127.0.0.1:" + resolvePort()
	client := &nethttp.Client{Timeout: 3 * time.Second}

	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, base+"/shutdown", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		// Conexión rechazada = el daemon no estaba corriendo. No es un fallo: el binario ya está libre.
		fmt.Fprintf(app.Out, "daemon:   no estaba corriendo (%s)\n", base)
		return nil
	}
	resp.Body.Close()
	if resp.StatusCode != nethttp.StatusAccepted && resp.StatusCode != nethttp.StatusOK {
		return fmt.Errorf("el daemon respondió HTTP %d al pedir el apagado", resp.StatusCode)
	}

	// Espera acotada a que /health deje de responder = el proceso terminó y liberó el binario.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(150 * time.Millisecond)
		if !daemonAlive(ctx, base) {
			fmt.Fprintf(app.Out, "daemon:   detenido (%s)\n", base)
			return nil
		}
	}
	fmt.Fprintf(app.Out, "daemon:   apagado solicitado; puede tardar un momento en cerrar (%s)\n", base)
	return nil
}

// daemonAlive indica si el daemon sigue respondiendo a GET /health.
func daemonAlive(ctx context.Context, base string) bool {
	reqCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, err := nethttp.NewRequestWithContext(reqCtx, nethttp.MethodGet, base+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := (&nethttp.Client{Timeout: time.Second}).Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == nethttp.StatusOK
}
