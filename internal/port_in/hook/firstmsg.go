package hook

import (
	"os"
	"path/filepath"
	"strconv"
)

// firstMessage indica si este es el primer prompt de la sesión, usando un archivo de estado en el
// directorio temporal con clave por session_id. La primera vez lo crea y devuelve true; las
// siguientes lo encuentra y devuelve false. Si no hay session_id usa el PID como respaldo.
func firstMessage(sessionID string) bool {
	key := sessionID
	if key == "" {
		key = strconv.Itoa(os.Getpid())
	}
	path := filepath.Join(os.TempDir(), "kandel-claude-"+key+"-tools-loaded")
	if _, err := os.Stat(path); err == nil {
		return false
	}
	// Crea el marcador; si falla, se asume primer mensaje igualmente (inyectar de más es inocuo).
	_ = os.WriteFile(path, []byte{}, 0o644)
	return true
}
