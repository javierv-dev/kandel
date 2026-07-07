package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	nethttp "net/http"
	"strings"
	"sync"
	"time"

	"github.com/javierv-dev/kandel/internal/config"
	"github.com/javierv-dev/kandel/internal/hub"
	httpadapter "github.com/javierv-dev/kandel/internal/port_in/http"
)

// defaultBackfillInterval es cada cuánto el daemon barre las memorias guardadas sin vector (p. ej.
// con Ollama caído) y las reembebe. config lo sobreescribe (backfill_interval); "0" lo desactiva.
const defaultBackfillInterval = 2 * time.Minute

// resolvePort devuelve el puerto efectivo del daemon, resuelto por config (env KANDEL_PORT >
// archivo > default 8765). Único origen de verdad del puerto entre hooks y servidor.
func resolvePort() string {
	return config.Resolve(config.Port)
}

// serveCmd arranca la API HTTP local. Expone los casos de uso como endpoints REST para
// integraciones (paneles, scripts) que prefieran HTTP al MCP.
type serveCmd struct{}

func init() { register(serveCmd{}) }

func (serveCmd) Name() string     { return "serve" }
func (serveCmd) Synopsis() string { return "arranca la API HTTP local" }

func (serveCmd) Run(ctx context.Context, args []string, app *App) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	// Por defecto vacío: si no se pasa --addr, se compone desde KANDEL_PORT en 127.0.0.1.
	addr := fs.String("addr", "", "dirección donde escuchar (por defecto 127.0.0.1:$KANDEL_PORT)")
	syncHub := fs.Bool("sync-hub", false, "arranca el hub de sincronización en vez de la API local")
	fs.Usage = func() {
		fmt.Fprintln(app.Err, "uso: kandel serve [--addr 127.0.0.1:8765] [--sync-hub]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *syncHub {
		return serveHub(ctx, app, strings.TrimSpace(*addr))
	}
	if strings.TrimSpace(*addr) == "" {
		*addr = "127.0.0.1:" + resolvePort()
	}

	// srv se declara antes de construir el handler para que el disparador de apagado (OnShutdown)
	// pueda cerrarlo de forma ordenada. sync.Once protege contra varias peticiones a /shutdown.
	srv := &nethttp.Server{Addr: *addr}
	var shutdownOnce sync.Once
	srv.Handler = httpadapter.NewHandler(httpadapter.Services{
		Memory:    app.Memory,
		Session:   app.Session,
		Lifecycle: app.Lifecycle,
		Prompt:    app.Prompt,
		Context:   app.Context,
		Project:   app.Project,
		Embedding: app.Embedding,
		// OnShutdown apaga el daemon con drenaje acotado. Lo usa `kandel stop` para liberar el
		// binario en ejecución (en Windows el daemon bloquea el .exe e impide actualizarlo).
		OnShutdown: func() {
			shutdownOnce.Do(func() {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = srv.Shutdown(shutdownCtx)
			})
		},
	})

	// Barrido periódico de backfill: reembebe en segundo plano las memorias que se guardaron sin
	// vector (Save es tolerante al embebedor caído). Es la red de seguridad; los hooks disparan
	// además un backfill inmediato. Se omite si el intervalo es 0 o si no hay embebedor cableado.
	if app.Embedding != nil {
		if interval := resolveBackfillInterval(); interval > 0 {
			go runBackfillSweep(ctx, app, interval)
		}
	}

	fmt.Fprintf(app.Out, "kandel HTTP escuchando en http://%s\n", *addr)
	// ListenAndServe bloquea hasta que el proceso termina o falla. Un apagado ordenado (vía
	// /shutdown → srv.Shutdown) devuelve ErrServerClosed, que aquí es salida limpia, no error.
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, nethttp.ErrServerClosed) {
		return err
	}
	return nil
}

// serveHub arranca el hub de sincronización (servidor del change-feed) en vez de la API local. Es un
// proceso APARTE del daemon (aunque comparta binario): no corre lógica de dominio, solo guarda filas
// y arbitra por LWW. Escucha en la dirección dada, o en sync_hub_addr (default :8766) si va vacía.
func serveHub(ctx context.Context, app *App, addr string) error {
	if app.OpenHub == nil {
		return errors.New("hub no cableado: esta build no puede hospedar el hub de sync")
	}
	if addr == "" {
		addr = strings.TrimSpace(config.Resolve(config.SyncHubAddr))
	}
	store, closeStore, err := app.OpenHub(ctx)
	if err != nil {
		return fmt.Errorf("abrir el almacén del hub: %w", err)
	}
	defer closeStore()

	srv := &nethttp.Server{Addr: addr, Handler: hub.Handler(store)}
	fmt.Fprintf(app.Out, "kandel hub de sync escuchando en http://%s\n", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, nethttp.ErrServerClosed) {
		return err
	}
	return nil
}

// resolveBackfillInterval devuelve el intervalo del barrido de backfill: KANDEL_BACKFILL_INTERVAL
// (formato de time.ParseDuration, p. ej. "90s", "5m") si está definido, si no el valor por defecto.
// "0" (o cualquier valor <= 0) desactiva el barrido.
func resolveBackfillInterval() time.Duration {
	raw := strings.TrimSpace(config.Resolve(config.BackfillInterval))
	if raw == "" {
		return defaultBackfillInterval
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return defaultBackfillInterval
	}
	return d
}

// runBackfillSweep corre el barrido hasta que el contexto se cancela. Cada tick dispara un
// SweepBackfill (que se omite solo si ya hay uno en curso). Los errores se registran y no detienen
// el barrido: Ollama puede estar caído ahora y volver en el siguiente tick.
func runBackfillSweep(ctx context.Context, app *App, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			res, skipped, err := app.Embedding.SweepBackfill(ctx)
			switch {
			case err != nil:
				log.Printf("barrido de backfill: error tras %d memorias: %v", res.Embedded, err)
			case skipped:
				// ya había uno en curso; nada que registrar.
			case res.Embedded > 0:
				log.Printf("barrido de backfill: %d memoria(s) embebida(s)", res.Embedded)
			}
		}
	}
}
