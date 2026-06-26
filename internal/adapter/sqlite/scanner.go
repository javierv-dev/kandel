package sqlite

import (
	"database/sql"
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
const memoryColumns = `id, ifnull(sync_id, ''), session_id, type, title, content,
	ifnull(tool_name, ''), ifnull(project, ''), scope, ifnull(topic_key, ''),
	ifnull(normalized_hash, ''), revision_count, duplicate_count, pinned,
	created_at, updated_at, last_seen_at, review_after, deleted_at`

// scanMemory escanea una fila al modelo de dominio. Centraliza la conversión de tipos
// (TEXT→time.Time, INTEGER→bool, columnas anulables) en un único sitio.
func scanMemory(sc rowScanner) (domain.Memory, error) {
	var (
		o                                 domain.Memory
		typ, scope                        string
		toolName, project, topicKey, hash string
		pinned                            int
		createdAt, updatedAt              string
		lastSeen, reviewAfter, deletedAt  sql.NullString
	)
	if err := sc.Scan(
		&o.ID, &o.SyncID, &o.SessionID, &typ, &o.Title, &o.Content,
		&toolName, &project, &scope, &topicKey, &hash,
		&o.RevisionCount, &o.DuplicateCount, &pinned,
		&createdAt, &updatedAt, &lastSeen, &reviewAfter, &deletedAt,
	); err != nil {
		return domain.Memory{}, err
	}

	o.Type = domain.MemoryType(typ)
	o.Scope = domain.Scope(scope)
	o.ToolName = toolName
	o.Project = project
	o.TopicKey = domain.TopicKey(topicKey)
	o.NormalizedHash = hash
	o.Pinned = pinned != 0

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
	if reviewAfter.Valid {
		if o.ReviewAfter, err = parseTime(reviewAfter.String); err != nil {
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
	return o, nil
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
