package hook

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/javierv-dev/kandel/internal/config"
)

// logDir resuelve el directorio de logs de los hooks. config lo sobreescribe (env KANDEL_LOG_DIR >
// archivo); por defecto es ~/.kandel/logs (el mismo que usaban los scripts shell). Devuelve "" si no
// se puede resolver.
func logDir() string {
	if d := config.Resolve(config.LogDir); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".kandel", "logs")
}

// filepathServeLog es la ruta del log del daemon (kandel serve), para mencionarla en los mensajes.
func filepathServeLog() string {
	dir := logDir()
	if dir == "" {
		return "serve.log"
	}
	return filepath.Join(dir, "serve.log")
}

// klog anexa una línea con marca de tiempo al log de hooks. Nunca falla el hook: cualquier error
// al escribir se ignora (la observabilidad es deseable, no crítica).
func (r *runner) klog(msg string) {
	dir := logDir()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "hooks.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s [%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), r.event, msg)
}

// klogResultado registra el desenlace de una llamada al daemon. Un fallo se marca con el prefijo
// ERROR para que sea distinguible de un vistazo (y con grep) entre las líneas normales.
//
// Es la señal mínima que exige la auditoría de durabilidad: un hook NO puede terminar con exit≠0
// —bloquearía el mensaje del usuario en el agente—, así que el registro diferenciado es lo máximo
// que puede hacer sin romper esa restricción. Ver docs/auditoria-durabilidad.md §6, corrección 2.
func (r *runner) klogResultado(operación, code string, err error) {
	if err != nil {
		r.klog(fmt.Sprintf("ERROR %s -> HTTP %s: %v", operación, code, err))
		return
	}
	r.klog(fmt.Sprintf("%s -> HTTP %s", operación, code))
}

// orQ devuelve s, o "?" si está vacío (para que el log muestre un marcador legible).
func orQ(s string) string {
	if s == "" {
		return "?"
	}
	return s
}
