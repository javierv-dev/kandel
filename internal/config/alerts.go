package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	alertsFileName     = "alerts.json"
	defaultMaxAttempts = 5
)

// SystemAlert representa un evento de salud o fallo del sistema (por ejemplo, el juez LLM de
// detección de conflictos fallando en segundo plano por límite de uso o error de CLI) que debe
// notificarse al agente principal al iniciar una conversación y ocultarse cuando ya se solucionó
// o el usuario pidió descartarlo.
type SystemAlert struct {
	Kind        string    `json:"kind"`                 // identificador del tipo de evento (p. ej. "judge_failure")
	Agent       string    `json:"agent,omitempty"`      // agente que falló (p. ej. "claude", "pi")
	Model       string    `json:"model,omitempty"`      // modelo que falló (p. ej. "haiku")
	LastError   string    `json:"last_error,omitempty"` // resumen legible del error (p. ej. límite de uso alcanzado)
	Failures    int       `json:"failures"`             // intentos fallidos consecutivos acumulados
	MaxAttempts int       `json:"max_attempts"`         // límite de intentos configurado
	Paused      bool      `json:"paused"`               // true cuando alcanzó el límite y dejó de intentar en segundo plano
	Dismissed   bool      `json:"dismissed,omitempty"`  // true si el usuario/agente ya dio de baja el aviso
	FirstSeenAt time.Time `json:"first_seen_at"`        // primer fallo de la racha actual
	UpdatedAt   time.Time `json:"updated_at"`           // último intento fallido
}

// AlertKindJudgeFailure es la clave canónica del evento de fallo del juez LLM de conflictos.
const AlertKindJudgeFailure = "judge_failure"

// AlertsFilePath devuelve la ruta del archivo de eventos/alertas dentro del directorio de datos.
func AlertsFilePath(dir string) string {
	return filepath.Join(dir, alertsFileName)
}

// ResolveMaxAttempts devuelve el límite de intentos consecutivos del juez (config conflict_max_attempts,
// por defecto 5).
func ResolveMaxAttempts() int {
	raw := strings.TrimSpace(Resolve(ConflictMaxAttempts))
	if raw == "" {
		return defaultMaxAttempts
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultMaxAttempts
	}
	return n
}

// LoadAlerts lee el mapa de alertas del directorio dado. Si el archivo no existe, devuelve un mapa vacío.
func LoadAlerts(dir string) (map[string]SystemAlert, error) {
	if strings.TrimSpace(dir) == "" {
		var err error
		dir, err = DefaultDir()
		if err != nil {
			return map[string]SystemAlert{}, err
		}
	}
	path := AlertsFilePath(dir)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]SystemAlert{}, nil
		}
		return nil, fmt.Errorf("leer alertas %q: %w", path, err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return map[string]SystemAlert{}, nil
	}
	var alerts map[string]SystemAlert
	if err := json.Unmarshal(raw, &alerts); err != nil {
		// Si el archivo se corrompió, no rompemos el flujo del daemon ni del hook: empezamos limpio.
		return map[string]SystemAlert{}, nil
	}
	if alerts == nil {
		alerts = map[string]SystemAlert{}
	}
	return alerts, nil
}

// SaveAlerts persiste el mapa de alertas de forma atómica en el directorio de datos.
func SaveAlerts(dir string, alerts map[string]SystemAlert) error {
	if strings.TrimSpace(dir) == "" {
		var err error
		dir, err = DefaultDir()
		if err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("crear directorio de datos %q: %w", dir, err)
	}
	path := AlertsFilePath(dir)
	if len(alerts) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	raw, err := json.MarshalIndent(alerts, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return fmt.Errorf("escribir alertas %q: %w", tmp, err)
	}
	return os.Rename(tmp, path)
}

// ActiveAlerts devuelve las alertas activas (no descartadas por el usuario) ordenadas por tipo.
func ActiveAlerts(dir string) []SystemAlert {
	alerts, err := LoadAlerts(dir)
	if err != nil || len(alerts) == 0 {
		return nil
	}
	out := make([]SystemAlert, 0, len(alerts))
	for _, a := range alerts {
		if a.Dismissed {
			continue
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

// IsJudgePaused indica si el juez LLM alcanzó el límite de intentos fallidos para la combinación
// actual de (agent, model) y debe omitirse en los barridos automáticos de segundo plano.
// Si el usuario o el agente principal cambiaron de agente o de modelo en la configuración, la pausa
// anterior ya no aplica (y se limpia sola).
func IsJudgePaused(dir, agent, model string) (bool, SystemAlert) {
	alerts, err := LoadAlerts(dir)
	if err != nil {
		return false, SystemAlert{}
	}
	a, ok := alerts[AlertKindJudgeFailure]
	if !ok {
		return false, SystemAlert{}
	}
	// Si cambió el agente o el modelo respecto al que falló, la racha vieja ya no aplica.
	if a.Agent != strings.TrimSpace(agent) || a.Model != strings.TrimSpace(model) {
		return false, SystemAlert{}
	}
	maxAtt := ResolveMaxAttempts()
	if a.Failures >= maxAtt {
		a.Paused = true
		a.MaxAttempts = maxAtt
		return true, a
	}
	return false, a
}

// RecordJudgeFailure registra un fallo del juez LLM en segundo plano, incrementando el contador de
// intentos consecutivos. Si alcanza maxAttempts, marca Paused = true para que el daemon deje de
// reintentar y expone el evento al agente principal.
func RecordJudgeFailure(dir, agent, model string, judgeErr error) SystemAlert {
	if strings.TrimSpace(dir) == "" {
		dir, _ = DefaultDir()
	}
	alerts, _ := LoadAlerts(dir)
	if alerts == nil {
		alerts = map[string]SystemAlert{}
	}
	now := time.Now().UTC()
	maxAtt := ResolveMaxAttempts()
	agent = strings.TrimSpace(agent)
	model = strings.TrimSpace(model)

	prev, ok := alerts[AlertKindJudgeFailure]
	// Si es el primer fallo o cambió el agente/modelo, reinicia la cuenta.
	if !ok || prev.Agent != agent || prev.Model != model {
		prev = SystemAlert{
			Kind:        AlertKindJudgeFailure,
			Agent:       agent,
			Model:       model,
			FirstSeenAt: now,
		}
	}
	prev.Failures++
	prev.MaxAttempts = maxAtt
	prev.LastError = summarizeJudgeError(judgeErr)
	prev.UpdatedAt = now
	if prev.Failures >= maxAtt {
		prev.Paused = true
	}
	// Si había sido ocultada pero volvió a fallar un intento nuevo, reactivar el aviso.
	prev.Dismissed = false

	alerts[AlertKindJudgeFailure] = prev
	_ = SaveAlerts(dir, alerts)
	return prev
}

// ClearJudgeAlert elimina por completo la alerta de fallo del juez (se llama automáticamente cuando
// el juez tiene éxito o cuando se cambia de agente/modelo con `kandel agent use`).
func ClearJudgeAlert(dir string) error {
	if strings.TrimSpace(dir) == "" {
		var err error
		dir, err = DefaultDir()
		if err != nil {
			return err
		}
	}
	alerts, err := LoadAlerts(dir)
	if err != nil || len(alerts) == 0 {
		return err
	}
	if _, ok := alerts[AlertKindJudgeFailure]; !ok {
		return nil
	}
	delete(alerts, AlertKindJudgeFailure)
	return SaveAlerts(dir, alerts)
}

// DismissAlert da de baja (oculta) el aviso de una alerta concreta (o todas si kind == "" o "all").
// Si la alerta estaba pausada por haber alcanzado el límite de intentos, conserva Paused = true
// con Dismissed = true para que no siga spameando el banner ni reintentando en bucle hasta que
// cambie el agente/modelo o se pida `ClearAlerts` con reset.
func DismissAlert(dir, kind string, resetRetries bool) error {
	if strings.TrimSpace(dir) == "" {
		var err error
		dir, err = DefaultDir()
		if err != nil {
			return err
		}
	}
	alerts, err := LoadAlerts(dir)
	if err != nil || len(alerts) == 0 {
		return err
	}
	kind = strings.TrimSpace(kind)
	if resetRetries {
		if kind == "" || kind == "all" {
			return SaveAlerts(dir, nil)
		}
		delete(alerts, kind)
		return SaveAlerts(dir, alerts)
	}
	for k, a := range alerts {
		if kind == "" || kind == "all" || k == kind {
			a.Dismissed = true
			alerts[k] = a
		}
	}
	return SaveAlerts(dir, alerts)
}

// summarizeJudgeError extrae un resumen limpio y legible del fallo de la CLI del agente. Si la CLI
// devolvió un sobre JSON (como `claude --output-format json` con `"result":"You've hit your weekly limit..."`
// o `"api_error":"usage_limit_reached"`), extrae ese motivo humano en vez de volcar todo el JSON crudo.
func summarizeJudgeError(err error) string {
	if err == nil {
		return ""
	}
	raw := strings.TrimSpace(err.Error())
	// Intentar localizar un objeto JSON dentro del mensaje de error ("... terminó con código 1: {...}").
	if idx := strings.Index(raw, "{"); idx >= 0 {
		prefix := strings.TrimSpace(raw[:idx])
		jsonPart := raw[idx:]
		// Si fue truncado con "…[recortado]", intentar también buscar campos clave por regex/substring
		// o decodificar si el JSON está completo.
		var env struct {
			Result       string `json:"result"`
			APIError     string `json:"api_error"`
			APIErrorType string `json:"api_error_type"`
			APIStatus    int    `json:"api_error_status"`
			Error        any    `json:"error"`
			Message      string `json:"message"`
		}
		if json.Unmarshal([]byte(jsonPart), &env) == nil {
			var parts []string
			if env.APIError != "" {
				parts = append(parts, env.APIError)
			} else if env.APIErrorType != "" {
				parts = append(parts, env.APIErrorType)
			}
			if env.Result != "" {
				parts = append(parts, env.Result)
			} else if env.Message != "" {
				parts = append(parts, env.Message)
			}
			if len(parts) > 0 {
				if prefix != "" {
					return prefix + " " + strings.Join(parts, " — ")
				}
				return strings.Join(parts, " — ")
			}
		}
		// Si el JSON venía truncado en el error de runner.go, extraer campos conocidos si aparecen.
		if extracted := extractTruncatedJSONHint(jsonPart); extracted != "" {
			if prefix != "" {
				return prefix + " " + extracted
			}
			return extracted
		}
	}
	if len(raw) > 280 {
		return raw[:280] + "…"
	}
	return raw
}

// extractTruncatedJSONHint busca pistas legibles cuando el JSON de error de la CLI es muy largo.
func extractTruncatedJSONHint(s string) string {
	var hints []string
	for _, key := range []string{`"result":"`, `"api_error":"`, `"api_error_type":"`, `"terminal_reason":"`, `"message":"`} {
		if i := strings.Index(s, key); i >= 0 {
			rest := s[i+len(key):]
			if j := strings.Index(rest, `"`); j > 0 {
				val := rest[:j]
				if val != "" && !containsStr(hints, val) {
					hints = append(hints, val)
				}
			}
		}
	}
	return strings.Join(hints, " / ")
}

func containsStr(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}
