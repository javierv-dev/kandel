package hook

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// safeTempKey convierte un session_id en un nombre de archivo seguro en cualquier sistema (en
// Windows NTFS, un ':' como el de "opencode:ses_1" crea un Alternate Data Stream en vez de un
// archivo plano). Si el id está vacío usa el PID como respaldo.
func safeTempKey(sessionID string) string {
	if sessionID == "" {
		return strconv.Itoa(os.Getpid())
	}
	r := strings.NewReplacer(":", "_", "/", "_", "\\", "_")
	return r.Replace(sessionID)
}

// firstMessage indica si este es el primer prompt de la sesión, usando un archivo de estado en el
// directorio temporal con clave por session_id. La primera vez lo crea y devuelve true; las
// siguientes lo encuentra y devuelve false. Si no hay session_id usa el PID como respaldo.
func firstMessage(sessionID string) bool {
	path := filepath.Join(os.TempDir(), "kandel-claude-"+safeTempKey(sessionID)+"-tools-loaded")
	if _, err := os.Stat(path); err == nil {
		return false
	}
	// Crea el marcador; si falla, se asume primer mensaje igualmente (inyectar de más es inocuo).
	_ = os.WriteFile(path, []byte{}, 0o644)
	return true
}
