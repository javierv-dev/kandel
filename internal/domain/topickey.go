package domain

import (
	"regexp"
	"strings"
)

// TopicKey agrupa la evolución de un mismo tema. Cuando una memoria nueva trae el mismo
// topic_key que una existente, la actualiza en su lugar (sube revision_count) en vez de
// crear un duplicado.
//
// Convención: kebab-case en minúsculas, formato familia/descripción, máximo 2 niveles.
// El kebab-case importa porque cada fragmento queda buscable y no genera tokens raros.
// Ejemplos válidos: "architecture/auth-model", "bug/nil-panic-user-list", "config".
type TopicKey string

// segmentPattern valida un único segmento kebab-case: minúsculas y dígitos separados por
// guiones, sin guion al inicio ni al final.
var segmentPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

const maxTopicKeyLevels = 2

// ParseTopicKey valida y normaliza una clave de tema. Devuelve ErrInvalidTopicKey si el
// formato no cumple la convención. La cadena vacía NO es válida aquí: la opcionalidad del
// topic_key la decide quien llama (una observación sin tema deja TopicKey en su valor cero).
func ParseTopicKey(raw string) (TopicKey, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ErrInvalidTopicKey
	}
	segments := strings.Split(trimmed, "/")
	if len(segments) > maxTopicKeyLevels {
		return "", ErrInvalidTopicKey
	}
	for _, segment := range segments {
		if !segmentPattern.MatchString(segment) {
			return "", ErrInvalidTopicKey
		}
	}
	return TopicKey(trimmed), nil
}

// IsZero indica si la clave está ausente (la observación no pertenece a ningún tema).
func (k TopicKey) IsZero() bool {
	return k == ""
}
