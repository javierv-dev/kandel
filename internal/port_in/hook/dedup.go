package hook

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// injectedIDsPath resuelve el archivo de estado —clave por session_id— donde se registran los ids de
// memoria ya inyectados por relevancia en la sesión, para no repetirlos en turnos siguientes (dedup
// por sesión, Capa 2). Mismo patrón y directorio que firstMessage. Si no hay session_id usa el PID.
func injectedIDsPath(sessionID string) string {
	key := sessionID
	if key == "" {
		key = strconv.Itoa(os.Getpid())
	}
	return filepath.Join(os.TempDir(), "kandel-claude-"+key+"-injected-ids")
}

// loadInjectedIDs lee los ids ya inyectados en la sesión. Devuelve nil si no hay archivo o falla la
// lectura (en el peor caso se re-inyecta algo: inocuo, nunca bloquea).
func loadInjectedIDs(sessionID string) []int64 {
	raw, err := os.ReadFile(injectedIDsPath(sessionID))
	if err != nil {
		return nil
	}
	var ids []int64
	for _, part := range strings.Fields(string(raw)) {
		if id, err := strconv.ParseInt(part, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// recordInjectedIDs añade ids al registro de la sesión (append, uno por línea). Best-effort: un fallo
// de escritura solo significa que esos ids podrían re-inyectarse más adelante, lo cual es inocuo.
func recordInjectedIDs(sessionID string, ids []int64) {
	if len(ids) == 0 {
		return
	}
	f, err := os.OpenFile(injectedIDsPath(sessionID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	var b strings.Builder
	for _, id := range ids {
		b.WriteString(strconv.FormatInt(id, 10))
		b.WriteByte('\n')
	}
	_, _ = f.WriteString(b.String())
}
