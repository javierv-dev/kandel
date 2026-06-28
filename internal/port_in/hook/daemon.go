package hook

import (
	"os"
	"os/exec"
	"path/filepath"
)

// selfPath devuelve la ruta del propio binario kandel para relanzarlo (serve, sync). Si no se
// puede resolver, cae a "kandel" confiando en el PATH.
func selfPath() string {
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "kandel"
}

// spawnServe lanza `kandel serve` como proceso desacoplado (sobrevive a la salida del hook) con su
// salida redirigida a serve.log. No espera a que termine: el daemon queda corriendo de fondo.
func spawnServe() error {
	cmd := exec.Command(selfPath(), "serve")
	if dir := logDir(); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err == nil {
			if f, err := os.OpenFile(filepath.Join(dir, "serve.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
				cmd.Stdout = f
				cmd.Stderr = f
				defer f.Close()
			}
		}
	}
	detach(cmd)
	return cmd.Start()
}

// spawnSyncImport lanza `kandel sync --import` en cwd, desacoplado y sin esperar (importa los
// chunks sincronizados por git en segundo plano, sin bloquear el arranque de la sesión).
func spawnSyncImport(cwd string) {
	cmd := exec.Command(selfPath(), "sync", "--import")
	cmd.Dir = cwd
	detach(cmd)
	_ = cmd.Start()
}
