package setup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func init() { register(antigravity{}) }

// antigravity cablea kandel en Antigravity CLI (base Gemini): registra el servidor MCP en
// ~/.gemini/config/mcp_config.json, añade el protocolo de memoria como regla global always-on en
// ~/.gemini/GEMINI.md y engancha los hooks de ciclo de vida en ~/.gemini/config/hooks.json
// (PreInvocation para el arranque/contexto, Stop para el cierre de sesión). NO toca la CLI de agente
// activa (agent_cli): el juez de conflictos y el seed corren con el agente soportado que haya (hoy solo
// "claude"); si no hay ninguno, degradan limpio (Noop + banner), como ya está diseñado.
type antigravity struct{}

func (antigravity) Name() string { return "antigravity" }

// paths devuelve las rutas de los artefactos a partir del home del usuario. El directorio de
// configuración de Antigravity CLI (heredado de Gemini) es ~/.gemini.
func (antigravity) paths(home string) (geminiDir, mcpFile, rulesFile, hooksFile string) {
	geminiDir = filepath.Join(home, ".gemini")
	mcpFile = filepath.Join(geminiDir, "config", "mcp_config.json")
	rulesFile = filepath.Join(geminiDir, "GEMINI.md")
	hooksFile = filepath.Join(geminiDir, "config", "hooks.json")
	return
}

// agHookSpecs describe los hooks de kandel para Antigravity. Cada uno es una entrada nombrada en
// hooks.json (nombre → evento → handlers), con el comando apuntando directo al binario `kandel` (que
// está en el PATH), sin script wrapper. El nombre identifica el hook para el uninstall quirúrgico.
var agHookSpecs = []struct {
	name    string // clave de nivel superior en hooks.json (identifica el hook de kandel)
	event   string // evento de Antigravity (PreInvocation, Stop)
	command string // comando a ejecutar (kandel hook <evento interno>)
	timeout int    // timeout en segundos
}{
	{name: "kandel-pre-invocation", event: "PreInvocation", command: "kandel hook ag-pre-invocation", timeout: 10},
	{name: "kandel-stop", event: "Stop", command: "kandel hook ag-stop", timeout: 5},
}

// versionFile es la ruta del marcador de versión de los assets instalados (§21 Eje 3). Vive bajo
// ~/.gemini/kandel/, así el uninstall (que borra esa carpeta) lo retira solo.
func (antigravity) versionFile(home string) string {
	return filepath.Join(home, ".gemini", "kandel", "VERSION")
}

// kandelRulesStart y kandelRulesEnd delimitan el bloque que kandel gestiona dentro de GEMINI.md. El
// merge es quirúrgico: solo toca lo que hay entre estos marcadores, sin pisar el resto de las reglas
// globales del usuario (mismo criterio que el merge de hooks en Claude Code).
const (
	kandelRulesStart = "<!-- kandel:start (gestionado por kandel — no editar dentro de este bloque) -->"
	kandelRulesEnd   = "<!-- kandel:end -->"
)

// antigravityRulesBlock es el protocolo de memoria que kandel añade a GEMINI.md como regla always-on.
// Es deliberadamente conciso (GEMINI.md tiene un tope de 12 000 caracteres y lo comparte con las
// reglas del usuario). Como en Fase 1 no hay hooks, el arranque (mem_context) lo pide el propio texto.
const antigravityRulesBlock = `## Memoria persistente kandel (protocolo activo)

Tienes memoria persistente entre sesiones mediante las herramientas MCP del servidor **kandel**
(mem_save, mem_search, mem_context, mem_get, mem_update, mem_session_summary, …). El almacén es
COMPARTIDO con cualquier otro agente que use kandel en este mismo proyecto: lo que guardes aquí lo verá
también Claude Code, y viceversa.

Reglas:
- El contexto reciente del proyecto (decisiones, convenciones, sesiones) se **inyecta automáticamente al
  arrancar** vía el hook de kandel; no necesitas llamar a mem_context solo para verlo. Usa **mem_search**
  para un recall más profundo o específico cuando el contexto inyectado no baste.
- **Guarda de forma proactiva** con **mem_save** —sin esperar a que te lo pidan— tras: una decisión, un
  arreglo de bug (con su causa raíz), una convención, un descubrimiento no obvio, un patrón, o una
  preferencia/rechazo del usuario.
- Si una memoria quedó obsoleta y conoces su id, usa **mem_update** en vez de crear otra.
- Antes de terminar una tarea, llama a **mem_session_summary** con lo logrado y los próximos pasos.`

func (a antigravity) Install(home, version string, out io.Writer) error {
	_, mcpFile, rulesFile, hooksFile := a.paths(home)

	// 1. Registro del servidor MCP en ~/.gemini/config/mcp_config.json.
	if err := a.installMCP(mcpFile); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ servidor MCP 'kandel' registrado en %s\n", mcpFile)

	// 2. Protocolo de memoria como regla global always-on en ~/.gemini/GEMINI.md.
	if err := mergeRulesBlock(rulesFile); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ protocolo de memoria añadido a %s\n", rulesFile)

	// 3. Hooks de ciclo de vida en ~/.gemini/config/hooks.json.
	if err := installAgHooks(hooksFile); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ hooks de ciclo de vida enganchados en %s\n", hooksFile)

	// 4. Marca de versión de los assets (§21 Eje 3).
	if err := writeTextFile(a.versionFile(home), version, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ versión de assets (%s) marcada en %s\n", version, a.versionFile(home))

	fmt.Fprintln(out, "\nListo. Reinicia Antigravity para que tome la configuración.")
	fmt.Fprintln(out, "Las memorias se comparten con los demás agentes de este proyecto (mismo repo).")
	fmt.Fprintln(out, "Requisito: Ollama corriendo con el modelo de embeddings (por defecto bge-m3).")
	fmt.Fprintln(out, "  Si falta: ollama pull bge-m3")
	return nil
}

func (a antigravity) Uninstall(home string, out io.Writer) error {
	geminiDir, mcpFile, rulesFile, hooksFile := a.paths(home)

	// 1. Quita el servidor MCP.
	if err := a.uninstallMCP(mcpFile); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ servidor MCP 'kandel' retirado de %s\n", mcpFile)

	// 2. Quita el bloque de kandel de GEMINI.md sin tocar el resto de las reglas.
	if err := stripRulesBlock(rulesFile); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ protocolo de memoria retirado de %s\n", rulesFile)

	// 3. Quita los hooks de kandel de hooks.json sin tocar los ajenos.
	if err := uninstallAgHooks(hooksFile); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ hooks de kandel retirados de %s\n", hooksFile)

	// 4. Borra la carpeta propia de kandel (con el marcador de versión).
	if err := os.RemoveAll(filepath.Join(geminiDir, "kandel")); err != nil {
		return fmt.Errorf("borrar carpeta de kandel: %w", err)
	}
	fmt.Fprintln(out, "✓ carpeta de kandel borrada")
	return nil
}

// InstalledVersion lee el marcador de versión de los assets. present es false si no existe (agente no
// instalado por esta vía).
func (a antigravity) InstalledVersion(home string) (string, bool, error) {
	data, err := os.ReadFile(a.versionFile(home))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("leer versión de assets: %w", err)
	}
	return strings.TrimSpace(string(data)), true, nil
}

// installMCP registra el servidor MCP de kandel en mcpServers.kandel. El formato de mcp_config.json de
// Antigravity es idéntico al de Claude Code (objeto mcpServers con command/args), así que la entrada es
// la misma: el binario único `kandel mcp --tools=agent`. El agente activo del juez se resuelve desde
// ~/.kandel/config, no desde aquí (un único origen de verdad).
func (antigravity) installMCP(mcpFile string) error {
	cfg, err := loadJSONObject(mcpFile)
	if err != nil {
		return err
	}
	servers := mapAt(cfg, "mcpServers")
	servers["kandel"] = map[string]any{
		"command": "kandel",
		"args":    []any{"mcp", "--tools=agent"},
	}
	return saveJSONObject(mcpFile, cfg)
}

// uninstallMCP quita la clave mcpServers.kandel sin tocar otros servidores.
func (antigravity) uninstallMCP(mcpFile string) error {
	cfg, err := loadJSONObject(mcpFile)
	if err != nil {
		return err
	}
	servers, ok := cfg["mcpServers"].(map[string]any)
	if !ok {
		return nil
	}
	delete(servers, "kandel")
	if len(servers) == 0 {
		delete(cfg, "mcpServers")
	}
	return saveJSONObject(mcpFile, cfg)
}

// installAgHooks engancha los hooks de kandel en hooks.json de Antigravity. Cada hook es una entrada
// nombrada de nivel superior (nombre → evento → lista de handlers). Es idempotente: reescribe la
// entrada por su nombre, así reinstalar no duplica. Para PreInvocation/Stop los handlers van directos
// bajo la clave del evento (sin el envoltorio matcher/hooks que usan Pre/PostToolUse). Preserva los
// hooks ajenos que ya hubiera en el archivo.
func installAgHooks(hooksFile string) error {
	cfg, err := loadJSONObject(hooksFile)
	if err != nil {
		return err
	}
	for _, spec := range agHookSpecs {
		handler := map[string]any{"type": "command", "command": spec.command, "timeout": spec.timeout}
		cfg[spec.name] = map[string]any{spec.event: []any{handler}}
	}
	return saveJSONObject(hooksFile, cfg)
}

// uninstallAgHooks quita las entradas de kandel de hooks.json por su nombre, sin tocar las ajenas. Si
// el archivo queda sin ninguna entrada, no se borra (puede recrearlo Antigravity); solo se vacía.
func uninstallAgHooks(hooksFile string) error {
	cfg, err := loadJSONObject(hooksFile)
	if err != nil {
		return err
	}
	for _, spec := range agHookSpecs {
		delete(cfg, spec.name)
	}
	return saveJSONObject(hooksFile, cfg)
}

// mergeRulesBlock añade (o refresca) el bloque de kandel en un archivo de reglas markdown, sin pisar el
// contenido ajeno. Es idempotente: primero quita cualquier bloque de kandel previo (por sus marcadores)
// y luego anexa el actual al final, separado por una línea en blanco. Crea el archivo si no existe.
func mergeRulesBlock(rulesFile string) error {
	existing, err := readTextIfExists(rulesFile)
	if err != nil {
		return err
	}
	body := strings.TrimRight(stripBlock(existing), "\n")
	block := kandelRulesStart + "\n" + antigravityRulesBlock + "\n" + kandelRulesEnd + "\n"
	var merged string
	if body == "" {
		merged = block
	} else {
		merged = body + "\n\n" + block
	}
	return writeTextFile(rulesFile, merged, 0o644)
}

// stripRulesBlock quita el bloque de kandel de un archivo de reglas. Si tras quitarlo el archivo queda
// vacío (solo era el bloque de kandel), lo borra; si queda contenido ajeno, lo reescribe sin el bloque.
// No hace nada si el archivo no existe.
func stripRulesBlock(rulesFile string) error {
	existing, err := readTextIfExists(rulesFile)
	if err != nil {
		return err
	}
	if existing == "" {
		return nil
	}
	body := strings.TrimSpace(stripBlock(existing))
	if body == "" {
		if err := os.Remove(rulesFile); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("borrar %q: %w", rulesFile, err)
		}
		return nil
	}
	return writeTextFile(rulesFile, body+"\n", 0o644)
}

// stripBlock devuelve el texto sin el bloque delimitado por los marcadores de kandel (inclusive). Si
// falta alguno de los dos marcadores (archivo sin bloque o corrupto), devuelve el texto sin cambios,
// para no borrar contenido ajeno por error.
func stripBlock(text string) string {
	start := strings.Index(text, kandelRulesStart)
	if start < 0 {
		return text
	}
	endMarker := strings.Index(text, kandelRulesEnd)
	if endMarker < 0 {
		return text
	}
	end := endMarker + len(kandelRulesEnd)
	return text[:start] + text[end:]
}

// readTextIfExists lee un archivo de texto; devuelve "" (sin error) si no existe.
func readTextIfExists(path string) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("leer %q: %w", path, err)
	}
	return string(data), nil
}
