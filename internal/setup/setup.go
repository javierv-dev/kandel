// Package setup instala la integración de kandel en la configuración de usuario de un agente de
// IA, sin empaquetarse como plugin (decisión §14.9 del plan). Escribe los tres artefactos —skill,
// registro MCP y hooks de ciclo de vida— en el directorio del agente. Nace con un único writer
// real (claude-code, agente primario, §14.10) y un registro preparado para sumar los demás.
package setup

import (
	"embed"
	"fmt"
	"io"
	"sort"
)

// assets contiene la skill y los scripts de hook que el instalador escribe en disco.
//
//go:embed all:assets
var assets embed.FS

// Agent describe cómo cablear kandel en un agente concreto. Cada implementación conoce las rutas
// y formatos de configuración de su agente; la orquestación (registro, despacho) es común.
type Agent interface {
	// Name es el identificador del agente en la CLI (p. ej. "claude-code").
	Name() string
	// Install escribe los artefactos en la config del usuario (home = directorio del usuario) y
	// estampa la versión del binario que instala (§21 Eje 3), para detectar assets desalineados.
	Install(home, version string, out io.Writer) error
	// Uninstall retira los artefactos de kandel sin pisar el resto de la config del usuario.
	Uninstall(home string, out io.Writer) error
	// InstalledVersion devuelve la versión estampada en la última instalación. present es false si
	// no hay marca (agente no instalado por esta vía).
	InstalledVersion(home string) (version string, present bool, err error)
}

// registry mapea nombre → agente soportado.
var registry = map[string]Agent{}

// register añade un agente al registro. Se llama desde los init() de cada writer.
func register(a Agent) {
	if _, exists := registry[a.Name()]; exists {
		panic(fmt.Sprintf("setup: agente duplicado %q", a.Name()))
	}
	registry[a.Name()] = a
}

// Names devuelve los nombres de los agentes soportados, ordenados.
func Names() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Install instala el agente indicado, estampando version. Devuelve un error si el nombre no está
// soportado.
func Install(name, home, version string, out io.Writer) error {
	a, ok := registry[name]
	if !ok {
		return fmt.Errorf("agente no soportado: %q (soportados: %v)", name, Names())
	}
	return a.Install(home, version, out)
}

// Uninstall desinstala el agente indicado.
func Uninstall(name, home string, out io.Writer) error {
	a, ok := registry[name]
	if !ok {
		return fmt.Errorf("agente no soportado: %q (soportados: %v)", name, Names())
	}
	return a.Uninstall(home, out)
}

// InstalledVersion devuelve la versión estampada por la última instalación del agente indicado.
// present es false si el agente no está instalado por esta vía.
func InstalledVersion(name, home string) (version string, present bool, err error) {
	a, ok := registry[name]
	if !ok {
		return "", false, fmt.Errorf("agente no soportado: %q (soportados: %v)", name, Names())
	}
	return a.InstalledVersion(home)
}
