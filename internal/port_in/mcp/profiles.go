// Package mcp es el adaptador de entrada Model Context Protocol: expone la memoria de kandel
// como herramientas mem_* sobre el transporte stdio, para que cualquier agente (Claude Code,
// OpenCode, Cursor, etc.) la use añadiendo kandel como servidor MCP. Los handlers solo hablan
// con los casos de uso (service/) a través de las interfaces de Services; nunca tocan SQL.
//
// La allowlist y los perfiles son seguridad por reducción de capacidades: ocultar a la IA las
// herramientas destructivas (p. ej. mem_delete) recorta lo que puede hacer.
package mcp

import "strings"

// ProfileAgent son las herramientas que un agente usa durante una sesión de programación.
// (Subconjunto ya implementado sobre los services existentes; las herramientas de fases
// posteriores —conflictos, stats, prompts— se añadirán cuando aporten su service.)
var ProfileAgent = map[string]bool{
	"mem_save":              true,
	"mem_search":            true,
	"mem_context":           true,
	"mem_get":               true,
	"mem_session_start":     true,
	"mem_session_end":       true,
	"mem_session_summary":   true,
	"mem_suggest_topic_key": true,
	"mem_update":            true,
	"mem_review":            true,
	"mem_pin":               true,
	"mem_unpin":             true,
	"mem_save_prompt":       true,
	"mem_capture_passive":   true,
	"mem_current_project":   true,
	"mem_doctor":            true,
	"mem_compare":           true,
	"mem_judge":             true,
}

// ProfileAdmin son las herramientas de curación manual (TUI, paneles), no referenciadas en los
// protocolos de los agentes. mem_delete es destructiva: por eso vive aquí, fuera del perfil agente.
var ProfileAdmin = map[string]bool{
	"mem_delete": true,
}

// Profiles asocia nombre de perfil → conjunto de herramientas.
var Profiles = map[string]map[string]bool{
	"agent": ProfileAgent,
	"admin": ProfileAdmin,
}

// ResolveTools interpreta una cadena separada por comas de nombres de perfil y/o de herramientas
// individuales y devuelve el conjunto de herramientas a registrar. Una entrada vacía (o "all")
// devuelve nil, que significa "registrar todas".
func ResolveTools(input string) map[string]bool {
	input = strings.TrimSpace(input)
	if input == "" || input == "all" {
		return nil
	}
	result := make(map[string]bool)
	for _, token := range strings.Split(input, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if token == "all" {
			return nil
		}
		if profile, ok := Profiles[token]; ok {
			for tool := range profile {
				result[tool] = true
			}
		} else {
			// Nombre de herramienta individual.
			result[token] = true
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// shouldRegister indica si una herramienta debe registrarse dada la allowlist. Una allowlist nil
// permite todo.
func shouldRegister(name string, allowlist map[string]bool) bool {
	if allowlist == nil {
		return true
	}
	return allowlist[name]
}
