package hook

import (
	"os"
	"path/filepath"
	"strconv"
)

// compactedPath resuelve el archivo marcador —clave por session_id— que registra que la sesión ha
// pasado por una compactación. Mismo patrón y directorio que firstMessage y el registro de ids
// inyectados. Sin session_id usa el PID.
func compactedPath(sessionID string) string {
	key := sessionID
	if key == "" {
		key = strconv.Itoa(os.Getpid())
	}
	return filepath.Join(os.TempDir(), "kandel-claude-"+key+"-compacted")
}

// markCompacted deja constancia de que la sesión se compactó. Best-effort: si la escritura falla, lo
// único que ocurre es que se seguirá filtrando lo guardado en la sesión, que es la conducta previa.
func markCompacted(sessionID string) {
	_ = os.WriteFile(compactedPath(sessionID), []byte{}, 0o644)
}

// wasCompacted indica si la sesión ya pasó por una compactación.
//
// Importa porque invierte la regla de la inyección. Lo normal es NO reinyectar lo guardado en la
// sesión en curso: el agente lo tiene delante en la conversación. Pero una compactación borra esa
// conversación, así que a partir de ahí lo guardado antes en la misma sesión es justo lo que hay que
// recuperar, no lo que hay que esconder.
func wasCompacted(sessionID string) bool {
	_, err := os.Stat(compactedPath(sessionID))
	return err == nil
}

// resetInjectedIDs borra el registro de ids ya inyectados en la sesión, de modo que vuelvan a poder
// inyectarse.
//
// Se llama al compactar y es imprescindible: el dedup por sesión sigue vivo tras la compactación, así
// que sin este reinicio el agente se quedaría doblemente sin contexto —perdió la conversación y
// además el sistema se niega a repetirle lo que ya le había dado—. Best-effort: si el borrado falla,
// el dedup sigue como estaba.
func resetInjectedIDs(sessionID string) {
	_ = os.Remove(injectedIDsPath(sessionID))
}
