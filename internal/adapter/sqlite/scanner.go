package sqlite

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
)

// timeLayout es el formato en que SQLite guarda los timestamps (compatible con datetime()).
// Se interpreta y se escribe siempre en UTC para que las comparaciones sean estables.
const timeLayout = "2006-01-02 15:04:05"

// formatTime convierte un instante a su representación TEXT en UTC. El tiempo cero se serializa
// igualmente; quien no quiera persistir un cero debe usar nullTime.
func formatTime(t time.Time) string {
	return t.UTC().Format(timeLayout)
}

// nullTime devuelve un valor para columnas TEXT anulables: NULL si el tiempo es cero, o su
// representación en UTC si no.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return formatTime(t)
}

// nullString devuelve NULL para cadenas vacías y la cadena en otro caso. Mantiene las columnas
// opcionales realmente nulas en la base de datos.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// esViolaciónDeClaveForánea indica si el error viene de incumplir una clave foránea (SQLite código
// 787, SQLITE_CONSTRAINT_FOREIGNKEY). Se compara por texto porque el driver puro-Go no expone
// centinelas comparables; el número va incluido en el mensaje, que es lo más estable que hay.
func esViolaciónDeClaveForánea(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "foreign key constraint failed") || strings.Contains(msg, "(787)")
}

// parseTime interpreta un TEXT de la base de datos como instante UTC. Una cadena vacía da el
// tiempo cero sin error.
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.ParseInLocation(timeLayout, s, time.UTC)
}

// rowScanner es lo común entre *sql.Row y *sql.Rows: ambos saben escanear una fila.
type rowScanner interface {
	Scan(dest ...any) error
}

// memoryColumns es la lista de columnas, en orden, que espera scanMemory. Se usa en
// todas las consultas de lectura de memorias para que columna y destino no se desalineen.
// Las columnas anulables se normalizan con ifnull para escanear a string sin sql.Null*.
const memoryColumns = `id, ifnull(sync_id, ''), ifnull(session_id, ''), type, title, content,
	ifnull(tool_name, ''), ifnull(project, ''), scope,
	ifnull(normalized_hash, ''), revision_count, duplicate_count,
	created_at, updated_at, last_seen_at, ifnull(resolution, ''), conflict_checked, deleted_at,
	ifnull(unknown_fields, '')`

// scanMemory escanea una fila al modelo de dominio. Centraliza la conversión de tipos
// (TEXT→time.Time, INTEGER→bool, columnas anulables) en un único sitio.
func scanMemory(sc rowScanner) (domain.Memory, error) {
	var (
		o                       domain.Memory
		typ, scope              string
		toolName, project, hash string
		resolution              string
		conflictChecked         int
		createdAt, updatedAt    string
		lastSeen, deletedAt     sql.NullString
		unknownFieldsRaw        string
	)
	if err := sc.Scan(
		&o.ID, &o.SyncID, &o.SessionID, &typ, &o.Title, &o.Content,
		&toolName, &project, &scope, &hash,
		&o.RevisionCount, &o.DuplicateCount,
		&createdAt, &updatedAt, &lastSeen, &resolution, &conflictChecked, &deletedAt,
		&unknownFieldsRaw,
	); err != nil {
		return domain.Memory{}, err
	}

	o.Type = domain.MemoryType(typ)
	o.Scope = domain.Scope(scope)
	o.ToolName = toolName
	o.Project = project
	o.NormalizedHash = hash
	o.Resolution = domain.Resolution(resolution)
	o.ConflictChecked = conflictChecked != 0

	var err error
	if o.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.Memory{}, err
	}
	if o.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return domain.Memory{}, err
	}
	if lastSeen.Valid {
		if o.LastSeenAt, err = parseTime(lastSeen.String); err != nil {
			return domain.Memory{}, err
		}
	}
	if deletedAt.Valid {
		t, err := parseTime(deletedAt.String)
		if err != nil {
			return domain.Memory{}, err
		}
		o.DeletedAt = &t
	}
	if unknownFieldsRaw != "" {
		o.UnknownFields, err = decodeUnknownFields(unknownFieldsRaw)
		if err != nil {
			return domain.Memory{}, err
		}
	}
	return o, nil
}

// encodeUnknownFields serializa los campos desconocidos en un JSON compacto para SQLite; devuelve ""
// cuando el mapa está vacío.
func encodeUnknownFields(fields map[string]string) (string, error) {
	if len(fields) == 0 {
		return "", nil
	}
	obj := make(map[string]json.RawMessage, len(fields))
	for k, v := range fields {
		if !json.Valid([]byte(v)) {
			continue
		}
		obj[k] = json.RawMessage(v)
	}
	if len(obj) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// decodeUnknownFields reconstruye el mapa de campos desconocidos desde la columna JSON de SQLite.
func decodeUnknownFields(raw string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil, err
	}
	if len(obj) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(obj))
	for k, v := range obj {
		var buf bytes.Buffer
		if err := json.Compact(&buf, v); err != nil {
			out[k] = strings.TrimSpace(string(v))
			continue
		}
		out[k] = buf.String()
	}
	return out, nil
}

// scanMemories escanea todas las filas de un *sql.Rows a memorias de dominio.
func scanMemories(rows *sql.Rows) ([]domain.Memory, error) {
	defer rows.Close()
	var out []domain.Memory
	for rows.Next() {
		o, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
