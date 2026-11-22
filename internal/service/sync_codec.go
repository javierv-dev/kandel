package service

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
)

// wireMemory refleja los campos serializables conocidos de domain.Memory para el change-feed de
// sync. Mantiene los nombres de clave históricos (PascalCase) sin contaminar el dominio con
// etiquetas json ni incluir UnknownFields como clave anidada.
type wireMemory struct {
	ID              int64
	SyncID          string
	SessionID       string
	Project         string
	Type            domain.MemoryType
	Scope           domain.Scope
	Title           string
	Content         string
	ToolName        string
	NormalizedHash  string
	RevisionCount   int
	DuplicateCount  int
	Resolution      domain.Resolution
	ConflictChecked bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
	LastSeenAt      time.Time
	DeletedAt       *time.Time
}

// knownMemoryKeys contiene los nombres en minúsculas de los campos conocidos (encoding/json los
// compara de forma insensible a mayúsculas). Cualquier otra clave en un payload entrante se conserva
// en UnknownFields para evitar pérdida por schema drift.
var knownMemoryKeys = map[string]bool{
	"id":              true,
	"syncid":          true,
	"sessionid":       true,
	"project":         true,
	"type":            true,
	"scope":           true,
	"title":           true,
	"content":         true,
	"toolname":        true,
	"normalizedhash":  true,
	"revisioncount":   true,
	"duplicatecount":  true,
	"resolution":      true,
	"conflictchecked": true,
	"createdat":       true,
	"updatedat":       true,
	"lastseenat":      true,
	"deletedat":       true,
	"unknownfields":   true,
}

func toWireMemory(m domain.Memory) wireMemory {
	return wireMemory{
		ID:              m.ID,
		SyncID:          m.SyncID,
		SessionID:       m.SessionID,
		Project:         m.Project,
		Type:            m.Type,
		Scope:           m.Scope,
		Title:           m.Title,
		Content:         m.Content,
		ToolName:        m.ToolName,
		NormalizedHash:  m.NormalizedHash,
		RevisionCount:   m.RevisionCount,
		DuplicateCount:  m.DuplicateCount,
		Resolution:      m.Resolution,
		ConflictChecked: m.ConflictChecked,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
		LastSeenAt:      m.LastSeenAt,
		DeletedAt:       m.DeletedAt,
	}
}

func fromWireMemory(w wireMemory) domain.Memory {
	return domain.Memory{
		ID:              w.ID,
		SyncID:          w.SyncID,
		SessionID:       w.SessionID,
		Project:         w.Project,
		Type:            w.Type,
		Scope:           w.Scope,
		Title:           w.Title,
		Content:         w.Content,
		ToolName:        w.ToolName,
		NormalizedHash:  w.NormalizedHash,
		RevisionCount:   w.RevisionCount,
		DuplicateCount:  w.DuplicateCount,
		Resolution:      w.Resolution,
		ConflictChecked: w.ConflictChecked,
		CreatedAt:       w.CreatedAt,
		UpdatedAt:       w.UpdatedAt,
		LastSeenAt:      w.LastSeenAt,
		DeletedAt:       w.DeletedAt,
	}
}

// marshalMemoryPayload serializa una memoria para el sync. Si trae UnknownFields (campos de una
// versión más nueva recibidos en un Pull previo), los reinyecta en el objeto JSON sin pisar los
// campos conocidos de esta versión.
func marshalMemoryPayload(m domain.Memory) ([]byte, error) {
	base, err := json.Marshal(toWireMemory(m))
	if err != nil {
		return nil, err
	}
	if len(m.UnknownFields) == 0 {
		return base, nil
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(base, &obj); err != nil {
		return nil, err
	}
	for k, rawVal := range m.UnknownFields {
		if knownMemoryKeys[strings.ToLower(k)] {
			continue
		}
		if !json.Valid([]byte(rawVal)) {
			continue
		}
		obj[k] = json.RawMessage(rawVal)
	}
	return json.Marshal(obj)
}

// unmarshalMemoryPayload deserializa el payload recibido del hub y captura en UnknownFields cualquier
// clave desconocida para que no se pierda si esta máquina modifica y vuelve a subir la memoria.
func unmarshalMemoryPayload(data []byte) (domain.Memory, error) {
	var w wireMemory
	if err := json.Unmarshal(data, &w); err != nil {
		return domain.Memory{}, err
	}
	m := fromWireMemory(w)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err == nil && len(raw) > 0 {
		var unknown map[string]string
		for k, v := range raw {
			if knownMemoryKeys[strings.ToLower(k)] {
				continue
			}
			if unknown == nil {
				unknown = make(map[string]string)
			}
			var buf bytes.Buffer
			if err := json.Compact(&buf, v); err != nil {
				unknown[k] = strings.TrimSpace(string(v))
				continue
			}
			unknown[k] = buf.String()
		}
		if len(unknown) > 0 {
			m.UnknownFields = unknown
		}
	}
	return m, nil
}
