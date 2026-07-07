// Package config centraliza la configuración de kandel en un único origen. Cada opción se resuelve
// por la cadena de precedencia: variable de entorno > archivo ~/.kandel/config > default interno.
// El mismo registro de claves genera un archivo autodocumentado (todas las claves comentadas con su
// default), para que la superficie de configuración sea descubrible. Reemplaza al antiguo paquete
// agentconf: kandel deja de sangrar sus ajustes en archivos ajenos (p. ej. ~/.claude.json).
//
// Dos ajustes NO viven en el archivo, a propósito:
//   - el directorio de datos (KANDEL_DATA_DIR) localiza el propio archivo → sería huevo-gallina;
//   - el proyecto (KANDEL_PROJECT) es contexto por-invocación, no un ajuste persistente.
//
// Ambos quedan solo por entorno/flag.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Key describe una opción configurable de kandel. El mismo valor sirve para resolverla (File+Env) y
// para documentarla en el archivo autodocumentado (Default+Doc).
type Key struct {
	// File es la clave dentro del archivo de configuración (p. ej. "agent_cli").
	File string
	// Env es la variable de entorno equivalente, que tiene prioridad sobre el archivo (p. ej.
	// "KANDEL_AGENT_CLI").
	Env string
	// Default es el valor efectivo cuando ni el entorno ni el archivo fijan la clave. Vacío significa
	// que el consumidor interpreta la ausencia (p. ej. "que el servicio use su propio default").
	Default string
	// Doc es la descripción corta que acompaña a la clave en el archivo autodocumentado.
	Doc string
}

// Claves reconocidas. Cada una se resuelve con Resolve y se documenta con Render. El orden de esta
// lista es el orden en que aparecen en el archivo generado.
var (
	// AgentCLI es el agente activo que juzga conflictos y siembra (uno solo). Vacío = sin agente
	// configurado (el juez cae a Noop, ver internal/adapter/llm).
	AgentCLI = Key{File: "agent_cli", Env: "KANDEL_AGENT_CLI", Default: "",
		Doc: "agente activo que juzga conflictos y siembra (p. ej. claude)"}
	// SeedModel es el modelo que usa el seed por defecto; vacío deja que el adaptador elija el suyo.
	SeedModel = Key{File: "seed_model", Env: "KANDEL_SEED_MODEL", Default: "",
		Doc: "modelo del seed (generación); vacío = default del adaptador"}
	// OllamaHost es el host del servidor Ollama (embebedor).
	OllamaHost = Key{File: "ollama_host", Env: "KANDEL_OLLAMA_HOST", Default: "http://127.0.0.1:11434",
		Doc: "host del servidor Ollama (embebedor)"}
	// EmbedModel es el modelo de embeddings. Es contrato de datos: cambiarlo obliga a re-embedding total.
	EmbedModel = Key{File: "embed_model", Env: "KANDEL_EMBED_MODEL", Default: "bge-m3",
		Doc: "modelo de embeddings (cambiarlo obliga a re-embedding total)"}
	// EmbedDimensions es la dimensión del vector; vacío deja el default del embebedor (1024 para bge-m3).
	EmbedDimensions = Key{File: "embed_dimensions", Env: "KANDEL_EMBED_DIMENSIONS", Default: "",
		Doc: "dimensión del vector; vacío = default del embebedor (1024 para bge-m3)"}
	// Port es el puerto del daemon HTTP (mismo origen de verdad para hooks y servidor).
	Port = Key{File: "port", Env: "KANDEL_PORT", Default: "8765",
		Doc: "puerto del daemon HTTP"}
	// SyncURL es la URL del hub de sincronización (change-feed). Vacío = sync deshabilitado.
	SyncURL = Key{File: "sync_url", Env: "KANDEL_SYNC_URL", Default: "",
		Doc: "URL del hub de sincronización (p. ej. http://localhost:8766); vacío = sync deshabilitado"}
	// SyncToken es el token de auth y namespace del usuario en el hub. Vacío = sync deshabilitado.
	SyncToken = Key{File: "sync_token", Env: "KANDEL_SYNC_TOKEN", Default: "",
		Doc: "token de auth/namespace del hub; vacío = sync deshabilitado"}
	// SyncHubAddr es la dirección donde escucha el hub (solo la máquina que lo hospeda: `serve
	// --sync-hub`). Distinta del daemon (Port, 8765) para poder correr ambos en la misma máquina.
	SyncHubAddr = Key{File: "sync_hub_addr", Env: "KANDEL_SYNC_HUB_ADDR", Default: ":8766",
		Doc: "dirección de escucha del hub de sync (host:puerto); solo para `serve --sync-hub`"}
	// LogDir es el directorio de logs; vacío = ~/.kandel/logs.
	LogDir = Key{File: "log_dir", Env: "KANDEL_LOG_DIR", Default: "",
		Doc: "directorio de logs; vacío = ~/.kandel/logs"}
	// ConflictCutoff es el corte de similitud del juez de conflictos; vacío = default del servicio.
	ConflictCutoff = Key{File: "conflict_sim_cutoff", Env: "KANDEL_CONFLICT_SIM_CUTOFF", Default: "",
		Doc: "corte de similitud del juez de conflictos (0..1); vacío = default del servicio (0.85)"}
	// ConflictTopK es el top-K de candidatas que juzga la detección; vacío = default del servicio.
	ConflictTopK = Key{File: "conflict_topk", Env: "KANDEL_CONFLICT_TOPK", Default: "",
		Doc: "top-K de candidatas del juez; vacío = default del servicio (3)"}
	// ConflictFactualCutoff es el corte de la pasada de supersede factual; vacío = default del servicio.
	ConflictFactualCutoff = Key{File: "conflict_factual_cutoff", Env: "KANDEL_CONFLICT_FACTUAL_CUTOFF", Default: "",
		Doc: "corte de similitud de la pasada de supersede factual (0..1); vacío = default del servicio (0.90)"}
	// ConflictTitleCutoff es el corte del portón por título de la detección de casi-duplicados; vacío = default.
	ConflictTitleCutoff = Key{File: "conflict_title_cutoff", Env: "KANDEL_CONFLICT_TITLE_CUTOFF", Default: "",
		Doc: "corte de similitud del portón por título de casi-duplicados (0..1); vacío = default del servicio (0.88)"}
	// NormativeThreshold es el umbral del aviso "demasiadas normativas"; vacío = default del servicio.
	NormativeThreshold = Key{File: "normative_threshold", Env: "KANDEL_NORMATIVE_THRESHOLD", Default: "",
		Doc: "umbral del aviso 'demasiadas normativas'; vacío = default del servicio (50)"}
	// BackfillInterval es cada cuánto el daemon barre y reembebe memorias sin vector; vacío = default;
	// "0" desactiva el barrido.
	BackfillInterval = Key{File: "backfill_interval", Env: "KANDEL_BACKFILL_INTERVAL", Default: "",
		Doc: "intervalo del barrido de backfill (p. ej. 90s, 5m); vacío = default (2m); 0 desactiva"}
	// UpdateCheck controla el chequeo de versión nueva; "off" lo desactiva (offline-first estricto).
	UpdateCheck = Key{File: "update_check", Env: "KANDEL_UPDATE_CHECK", Default: "",
		Doc: "chequeo de versión nueva; 'off' lo desactiva"}
	// InstalledAgents es el conjunto de agentes con kandel instalado (lista separada por comas). Lo
	// gestionan install/uninstall; no se edita a mano. Sirve para validar el cambio de agente activo.
	InstalledAgents = Key{File: "installed_agents", Env: "KANDEL_INSTALLED_AGENTS", Default: "",
		Doc: "agentes con kandel instalado (gestionado por install/uninstall; no editar a mano)"}
)

// Keys es el registro completo, en orden de aparición en el archivo generado.
var Keys = []Key{
	AgentCLI, SeedModel, OllamaHost, EmbedModel, EmbedDimensions, Port, SyncURL, SyncToken, SyncHubAddr, LogDir,
	ConflictCutoff, ConflictTopK, ConflictFactualCutoff, ConflictTitleCutoff, NormativeThreshold, BackfillInterval, UpdateCheck, InstalledAgents,
}

// configFileName es el nombre del archivo dentro del directorio de datos.
const configFileName = "config"

// FilePath devuelve la ruta del archivo de configuración dentro del directorio dado.
func FilePath(dir string) string {
	return filepath.Join(dir, configFileName)
}

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

// Source indica de dónde salió el valor efectivo de una clave (para que `kandel config list` muestre
// el origen y no confunda un env que pisa el archivo).
type Source int

const (
	// SourceDefault: ni el entorno ni el archivo la fijan; se usa el default interno.
	SourceDefault Source = iota
	// SourceFile: la fija el archivo ~/.kandel/config.
	SourceFile
	// SourceEnv: la fija una variable de entorno (pisa al archivo).
	SourceEnv
)

// String describe el origen en español para la salida de `kandel config list`.
func (s Source) String() string {
	switch s {
	case SourceEnv:
		return "entorno"
	case SourceFile:
		return "archivo"
	default:
		return "default"
	}
}

// Resolve devuelve el valor efectivo de una clave aplicando la precedencia env > archivo > default.
func Resolve(k Key) string {
	v, _ := ResolveWithSource(k)
	return v
}

// ResolveWithSource es como Resolve pero además indica de dónde salió el valor. Lee el entorno en
// vivo y el archivo del directorio de datos actual; no cachea, para respetar cambios de
// KANDEL_DATA_DIR entre llamadas (importa en tests) sin coste relevante (archivo pequeño).
func ResolveWithSource(k Key) (string, Source) {
	if v := strings.TrimSpace(os.Getenv(k.Env)); v != "" {
		return v, SourceEnv
	}
	if dir, err := DefaultDir(); err == nil {
		if v := strings.TrimSpace(Get(dir, k.File)); v != "" {
			return v, SourceFile
		}
	}
	return k.Default, SourceDefault
}

// KeyByFile busca una clave conocida por su nombre de archivo (p. ej. "agent_cli"). Lo usa
// `kandel config get/set` para validar que la clave existe antes de tocarla.
func KeyByFile(fileKey string) (Key, bool) {
	fileKey = strings.TrimSpace(fileKey)
	for _, k := range Keys {
		if k.File == fileKey {
			return k, true
		}
	}
	return Key{}, false
}

// Agents devuelve el conjunto de agentes instalados (installed_agents) del directorio dado, ordenado
// y sin duplicados ni vacíos.
func Agents(dir string) []string {
	return splitAgents(Get(dir, InstalledAgents.File))
}

// splitAgents normaliza una lista separada por comas a un conjunto ordenado sin duplicados ni vacíos.
func splitAgents(raw string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// AddAgent añade un agente al conjunto installed_agents (idempotente). No cambia el agente activo.
func AddAgent(dir, agent string) error {
	if strings.TrimSpace(agent) == "" {
		return nil
	}
	set := splitAgents(Get(dir, InstalledAgents.File) + "," + agent)
	return Save(dir, map[string]string{InstalledAgents.File: strings.Join(set, ",")})
}

// RemoveAgent quita un agente del conjunto installed_agents (idempotente).
func RemoveAgent(dir, agent string) error {
	agent = strings.TrimSpace(agent)
	var kept []string
	for _, a := range Agents(dir) {
		if a != agent {
			kept = append(kept, a)
		}
	}
	return Save(dir, map[string]string{InstalledAgents.File: strings.Join(kept, ",")})
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
// el archivo no está). Conveniencia para resolver una sola clave por su nombre de archivo.
func Get(dir, fileKey string) string {
	kv, err := Load(dir)
	if err != nil {
		return ""
	}
	return kv[fileKey]
}

// Save fusiona los pares dados en el config del directorio (creando el directorio y el archivo si
// faltan) y lo reescribe. Si el archivo aún no existía, se genera autodocumentado (todas las claves
// conocidas comentadas con su default) y encima se aplican los valores dados. Las claves ya presentes
// se sobreescriben; las demás se conservan. No borra claves.
func Save(dir string, updates map[string]string) error {
	existing, err := Load(dir)
	if err != nil {
		return err
	}
	for k, v := range updates {
		existing[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("crear directorio de datos %q: %w", dir, err)
	}
	path := filepath.Join(dir, configFileName)
	if err := os.WriteFile(path, []byte(Render(existing)), 0o644); err != nil {
		return fmt.Errorf("escribir config %q: %w", path, err)
	}
	return nil
}

// EnsureFile escribe el archivo autodocumentado si aún no existe (sin ningún valor activo: todas las
// claves comentadas). Si ya existe, no lo toca. Devuelve true si lo creó. Lo usa el instalador para
// garantizar que el archivo siempre exista y sea descubrible.
func EnsureFile(dir string) (bool, error) {
	path := filepath.Join(dir, configFileName)
	if _, err := os.Stat(path); err == nil {
		return false, nil // ya existe: no se pisa
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("comprobar config %q: %w", path, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("crear directorio de datos %q: %w", dir, err)
	}
	if err := os.WriteFile(path, []byte(Render(nil)), 0o644); err != nil {
		return false, fmt.Errorf("escribir config %q: %w", path, err)
	}
	return true, nil
}

// Render produce el contenido del archivo autodocumentado. Para cada clave conocida emite una línea
// de comentario con su descripción y una línea con la clave: activa (sin '#') si active la fija, o
// comentada con su default si no. Los valores activos que no correspondan a ninguna clave conocida
// se emiten al final (para no perder ajustes de una versión futura). active puede ser nil.
func Render(active map[string]string) string {
	var b strings.Builder
	b.WriteString("# Configuración de kandel. Descomenta una línea para fijar un valor.\n")
	b.WriteString("# Precedencia: variable de entorno KANDEL_<CLAVE> > este archivo > default interno.\n")
	b.WriteString("# El directorio de datos (KANDEL_DATA_DIR) y el proyecto (KANDEL_PROJECT) NO se\n")
	b.WriteString("# configuran aquí: solo por entorno o flag.\n")

	known := map[string]bool{}
	for _, k := range Keys {
		known[k.File] = true
		fmt.Fprintf(&b, "\n# %s\n", k.Doc)
		if v, ok := active[k.File]; ok && strings.TrimSpace(v) != "" {
			fmt.Fprintf(&b, "%s=%s\n", k.File, strings.TrimSpace(v))
			continue
		}
		if k.Default != "" {
			fmt.Fprintf(&b, "# %s=%s\n", k.File, k.Default)
		} else {
			fmt.Fprintf(&b, "# %s=\n", k.File)
		}
	}

	// Claves activas desconocidas (p. ej. de una versión más nueva): se conservan al final.
	var extra []string
	for key := range active {
		if !known[key] {
			extra = append(extra, key)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		b.WriteString("\n# (claves no reconocidas por esta versión, conservadas)\n")
		for _, key := range extra {
			fmt.Fprintf(&b, "%s=%s\n", key, strings.TrimSpace(active[key]))
		}
	}
	return b.String()
}
