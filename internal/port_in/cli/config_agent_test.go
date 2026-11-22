package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/javierv-dev/kandel/internal/config"
)

// dispatchCapturing corre Dispatch con la salida capturada en un buffer, aislando el directorio de
// datos a un temporal (para no tocar el ~/.kandel real).
func dispatchCapturing(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Setenv("KANDEL_DATA_DIR", t.TempDir())
	out := &bytes.Buffer{}
	app := &App{Out: out, Err: out}
	err := Dispatch(context.Background(), args, app)
	return out.String(), err
}

func TestConfigSetGet(t *testing.T) {
	t.Setenv("KANDEL_DATA_DIR", t.TempDir())
	out := &bytes.Buffer{}
	app := &App{Out: out, Err: out}

	if err := Dispatch(context.Background(), []string{"config", "set", "port", "9001"}, app); err != nil {
		t.Fatalf("config set: %v", err)
	}
	out.Reset()
	if err := Dispatch(context.Background(), []string{"config", "get", "port"}, app); err != nil {
		t.Fatalf("config get: %v", err)
	}
	if strings.TrimSpace(out.String()) != "9001" {
		t.Errorf("config get port = %q, se esperaba 9001", out.String())
	}
}

func TestConfigSetRejectsUnknownKey(t *testing.T) {
	if _, err := dispatchCapturing(t, "config", "set", "inexistente", "x"); err == nil {
		t.Error("config set de clave desconocida debe fallar")
	}
}

func TestConfigListShowsSource(t *testing.T) {
	out, err := dispatchCapturing(t, "config", "list")
	if err != nil {
		t.Fatalf("config list: %v", err)
	}
	// Debe listar al menos agent_cli y port, con la cabecera de origen.
	for _, want := range []string{"CLAVE", "ORIGEN", "agent_cli", "port"} {
		if !strings.Contains(out, want) {
			t.Errorf("config list no contiene %q:\n%s", want, out)
		}
	}
}

func TestAgentUseValidatesInstalledAndSupported(t *testing.T) {
	t.Setenv("KANDEL_DATA_DIR", t.TempDir())
	dir, _ := config.DefaultDir()
	out := &bytes.Buffer{}
	app := &App{Out: out, Err: out}

	// Sin instalar: `agent use claude` falla (no está en installed_agents).
	if err := Dispatch(context.Background(), []string{"agent", "use", "claude"}, app); err == nil {
		t.Error("agent use de un agente no instalado debe fallar")
	}

	// Instalado pero no soportado: falla por la allowlist.
	if err := config.AddAgent(dir, "gemini"); err != nil {
		t.Fatal(err)
	}
	if err := Dispatch(context.Background(), []string{"agent", "use", "gemini"}, app); err == nil {
		t.Error("agent use de un agente no soportado debe fallar")
	}

	// Instalado y soportado: éxito, y agent_cli queda fijado.
	if err := config.AddAgent(dir, "claude"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Dispatch(context.Background(), []string{"agent", "use", "claude"}, app); err != nil {
		t.Fatalf("agent use claude: %v", err)
	}
	if got := config.Get(dir, config.AgentCLI.File); got != "claude" {
		t.Errorf("agent_cli tras use = %q, se esperaba claude", got)
	}
	// `agent use` alinea el seed_model al del agente (§22.E): claude → sonnet.
	if got := config.Get(dir, config.SeedModel.File); got != "sonnet" {
		t.Errorf("seed_model tras use claude = %q, se esperaba sonnet", got)
	}

	// Instalado y soportado (pi): éxito, y agent_cli + seed_model quedan alineados a pi.
	if err := config.AddAgent(dir, "pi"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Dispatch(context.Background(), []string{"agent", "use", "pi"}, app); err != nil {
		t.Fatalf("agent use pi: %v", err)
	}
	if got := config.Get(dir, config.AgentCLI.File); got != "pi" {
		t.Errorf("agent_cli tras use pi = %q, se esperaba pi", got)
	}
	if got := config.Get(dir, config.SeedModel.File); got != "gemini-4-hydrogen" {
		t.Errorf("seed_model tras use pi = %q, se esperaba gemini-4-hydrogen", got)
	}
	if got := config.Get(dir, config.JudgeModel.File); got != "gemini-3-8-flash" {
		t.Errorf("judge_model tras use pi = %q, se esperaba gemini-3-8-flash", got)
	}

	// Con --model específico para el juez y limpieza automática de alerta de juez.
	config.RecordJudgeFailure(dir, "claude", "haiku", context.DeadlineExceeded)
	out.Reset()
	if err := Dispatch(context.Background(), []string{"agent", "use", "pi", "--model", "claude-sonnet-4-6"}, app); err != nil {
		t.Fatalf("agent use pi --model: %v", err)
	}
	if got := config.Get(dir, config.JudgeModel.File); got != "claude-sonnet-4-6" {
		t.Errorf("judge_model tras use pi --model = %q, se esperaba claude-sonnet-4-6", got)
	}
	if alerts := config.ActiveAlerts(dir); len(alerts) != 0 {
		t.Errorf("agent use debe limpiar alertas previas del juez, quedaron: %+v", alerts)
	}
}
