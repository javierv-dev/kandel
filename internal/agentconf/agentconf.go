// Package agentconf lee y escribe la configuración local de kandel relacionada con el agente
// (qué CLI de agente usar y qué modelo para el seed). Vive junto a la base de datos en el directorio
// de datos (~/.kandel/config) como pares clave=valor. El instalador la escribe al integrar un agente;
// comandos standalone como `kandel seed` la leen para resolver el LLM por defecto sin depender del
// entorno del proceso del servidor MCP.
package agentconf

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Claves reconocidas del archivo de configuración.
const (
	// KeyAgentCLI es el nombre de la CLI de agente (p. ej. "claude", "gemini").
	KeyAgentCLI = "agent_cli"
	// KeySeedModel es el modelo que el seed usa por defecto (p. ej. "sonnet").
	KeySeedModel = "seed_model"
)

// configFileName es el nombre del archivo dentro del directorio de datos.
const configFileName = "config"

// DefaultDir resuelve el directorio de datos de kandel: KANDEL_DATA_DIR si está definido, si no
// ~/.kandel. Es el mismo criterio que usa la base de datos, para que config y datos convivan.
func DefaultDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("KANDEL_DATA_DIR")); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no se pudo resolver el directorio del usuario: %w", err)
	}
	return filepath.Join(home, ".kandel"), nil
}

// Load lee el archivo de configuración del directorio dado y devuelve sus pares clave=valor. Un
// archivo ausente NO es un error: devuelve un mapa vacío (el config es opcional).
func Load(dir string) (map[string]string, error) {
	path := filepath.Join(dir, configFileName)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("abrir config %q: %w", path, err)
	}
	defer f.Close()

	kv := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue // línea sin '=': se ignora en vez de fallar
		}
		kv[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("leer config %q: %w", path, err)
	}
	return kv, nil
}

// Get carga el config del directorio dado y devuelve el valor de la clave (vacío si no existe o si
// el archivo no está). Conveniencia para resolver una sola clave.
func Get(dir, key string) string {
	kv, err := Load(dir)
	if err != nil {
		return ""
	}
	return kv[key]
}

// Save fusiona los pares dados en el config del directorio (creando el directorio y el archivo si
// faltan) y lo reescribe ordenado por clave. Las claves ya presentes se sobreescriben; las demás se
// conservan. No borra claves: para quitar una, hay que reescribir el archivo entero.
func Save(dir string, updates map[string]string) error {
	kv, err := Load(dir)
	if err != nil {
		return err
	}
	for k, v := range updates {
		kv[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("crear directorio de datos %q: %w", dir, err)
	}

	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("# Configuración local de kandel (escrita por el instalador). Pares clave=valor.\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, kv[k])
	}

	path := filepath.Join(dir, configFileName)
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("escribir config %q: %w", path, err)
	}
	return nil
}
