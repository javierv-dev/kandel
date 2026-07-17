// Package hook es el adaptador de entrada de los hooks de ciclo de vida de los agentes (hoy
// Claude Code). Toda la lógica del hook vive aquí, en Go, en lugar de en scripts shell: así la
// integración no depende de herramientas externas como jq (que no viene en Windows) y el binario
// kandel puede dar un seguimiento exacto de cada evento.
//
// Cada hook recibe un JSON por stdin (lo envía el agente), habla con el daemon HTTP local
// (kandel serve) y, cuando corresponde, imprime contexto por stdout para que el agente lo inyecte.
// Los scripts instalados quedan como wrappers de una línea: `exec kandel hook <evento>`.
package hook

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/javierv-dev/kandel/internal/ports"
	"github.com/javierv-dev/kandel/internal/setup"
)

// runner agrupa el estado común de un hook: el cliente HTTP del daemon, el destino de salida
// (stdout del proceso, que el agente interpreta como contexto) y el nombre del evento (para el log).
type runner struct {
	event  string
	client *client
	out    io.Writer
	ctx    context.Context

	// running es la versión del binario en ejecución; installedVersion resuelve la versión de los
	// assets instalados (§21 Eje 3). Juntas alimentan el banner de desalineación en SessionStart.
	// installedVersion es un campo (no una llamada directa) para poder inyectar un doble en tests.
	running          string
	installedVersion func() (version string, present bool, err error)

	// updates avisa si hay una versión de kandel más nueva publicada (§21 Eje 6); nil = sin aviso.
	updates ports.ReleaseChecker
}

// Run ejecuta el hook indicado por event, leyendo el JSON del agente de stdin y escribiendo la
// salida para el agente en out. version es la del binario (para los banners de desalineación de
// assets y de versión nueva); updates consulta si hay una release más nueva (§21 Eje 6; puede ser
// nil). NUNCA debe devolver error para eventos válidos: un fallo del hook (exit≠0) puede bloquear el
// mensaje del usuario en el agente. Solo devuelve error ante un evento desconocido (error de
// configuración del instalador, no del flujo normal).
func Run(ctx context.Context, event, version string, updates ports.ReleaseChecker, stdin io.Reader, out io.Writer) error {
	r := &runner{
		event:            event,
		client:           newClient(),
		out:              out,
		ctx:              ctx,
		running:          version,
		installedVersion: defaultInstalledVersion,
		updates:          updates,
	}
	// Los eventos de Antigravity (ag-*) traen otro contrato de stdin (conversationId, workspacePaths,
	// invocationNum) → se parsean aparte. El resto son los de Claude Code (payload común).
	switch event {
	case "ag-pre-invocation":
		return r.agPreInvocation(parseAgInput(stdin))
	case "ag-stop":
		return r.agStop(parseAgInput(stdin))
	}

	payload := parsePayload(stdin)
	switch event {
	case "session-start":
		return r.sessionStart(payload)
	case "post-compaction":
		return r.postCompaction(payload)
	case "user-prompt-submit":
		return r.userPromptSubmit(payload)
	case "session-stop":
		return r.sessionStop(payload)
	default:
		return fmt.Errorf("hook desconocido: %q", event)
	}
}

// defaultInstalledVersion resuelve la versión de los assets instalados del agente primario
// (claude-code) a partir del home del usuario. Es el valor por defecto de runner.installedVersion.
func defaultInstalledVersion() (string, bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	return setup.InstalledVersion("claude-code", home)
}
