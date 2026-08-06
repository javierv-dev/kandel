package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// UsageRepo implementa ports.UsageRepo sobre SQLite. Guarda la telemetría de uso de la memoria
// (inyecciones y menciones) en la tabla memory_usage, keyed por id LOCAL. Es deliberadamente local:
// nada de esto entra en el sync.
type UsageRepo struct {
	db execer
}

// Aserción en tiempo de compilación: el repo cumple el contrato del port.
var _ ports.UsageRepo = (*UsageRepo)(nil)

// NewUsageRepo crea el repositorio sobre la conexión dada.
func NewUsageRepo(db *DB) *UsageRepo {
	return &UsageRepo{db: db.sql}
}

// RecordInjected suma una inyección a cada id. Corre en el camino caliente (cada prompt del
// usuario), así que evita leer antes de escribir: el UPSERT crea la fila en el primer contacto y
// suma en los siguientes, en una sola sentencia por id.
func (r *UsageRepo) RecordInjected(ctx context.Context, ids []int64, at time.Time) error {
	return r.bump(ctx, ids, at, "injected_count", "last_injected_at")
}

// RecordMentioned suma una mención a cada id.
func (r *UsageRepo) RecordMentioned(ctx context.Context, ids []int64, at time.Time) error {
	return r.bump(ctx, ids, at, "mentioned_count", "last_mentioned_at")
}

// bump es el UPSERT compartido por los dos contadores. countCol y stampCol son nombres de columna
// fijados por los llamadores de este archivo, nunca entrada externa: no hay superficie de inyección.
func (r *UsageRepo) bump(ctx context.Context, ids []int64, at time.Time, countCol, stampCol string) error {
	if len(ids) == 0 {
		return nil
	}
	q := fmt.Sprintf(`
		INSERT INTO memory_usage (memory_id, %s, %s) VALUES (?, 1, ?)
		ON CONFLICT(memory_id) DO UPDATE SET %s = %s + 1, %s = excluded.%s`,
		countCol, stampCol, countCol, countCol, stampCol, stampCol)
	stamp := formatTime(at)
	exec := execFromCtx(ctx, r.db)
	for _, id := range ids {
		if _, err := exec.ExecContext(ctx, q, id, stamp); err != nil {
			// Una memoria borrada de golpe entre la inyección y el registro incumple la clave foránea.
			// No es un fallo del sistema: la telemetría de algo que ya no existe simplemente no se anota.
			if esViolaciónDeClaveForánea(err) {
				continue
			}
			return fmt.Errorf("sqlite: registrar uso de la memoria %d: %w", id, err)
		}
	}
	return nil
}

// Get devuelve la telemetría de una memoria. Sin fila devuelve el cero con el id puesto: "nunca
// inyectada" es una respuesta válida, no un error.
func (r *UsageRepo) Get(ctx context.Context, memoryID int64) (domain.MemoryUsage, error) {
	const q = `
		SELECT memory_id, injected_count, mentioned_count, last_injected_at, last_mentioned_at
		FROM memory_usage WHERE memory_id = ?`
	u, err := scanUsage(execFromCtx(ctx, r.db).QueryRowContext(ctx, q, memoryID))
	if err != nil {
		if err == sql.ErrNoRows {
			return domain.MemoryUsage{MemoryID: memoryID}, nil
		}
		return domain.MemoryUsage{}, fmt.Errorf("sqlite: leer uso de la memoria %d: %w", memoryID, err)
	}
	return u, nil
}

// TopInjected lista la telemetría de las memorias VIVAS más inyectadas. El JOIN contra memories
// descarta las borradas: sus contadores siguen ahí (el borrado es lógico) pero ya no dicen nada
// sobre lo que se está sirviendo hoy.
func (r *UsageRepo) TopInjected(ctx context.Context, limit int) ([]domain.MemoryUsage, error) {
	if limit <= 0 {
		limit = 20
	}
	const q = `
		SELECT u.memory_id, u.injected_count, u.mentioned_count, u.last_injected_at, u.last_mentioned_at
		FROM memory_usage u
		JOIN memories m ON m.id = u.memory_id AND m.deleted_at IS NULL
		ORDER BY u.injected_count DESC, u.memory_id ASC
		LIMIT ?`
	rows, err := execFromCtx(ctx, r.db).QueryContext(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: listar uso de memorias: %w", err)
	}
	defer rows.Close()
	var out []domain.MemoryUsage
	for rows.Next() {
		u, err := scanUsage(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: escanear uso de memorias: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CountDormant cuenta las memorias vivas inyectadas al menos minInjections veces y nunca
// mencionadas: peso constante en el contexto sin rendimiento observable.
func (r *UsageRepo) CountDormant(ctx context.Context, minInjections int) (int, error) {
	if minInjections <= 0 {
		minInjections = 1
	}
	const q = `
		SELECT count(*)
		FROM memory_usage u
		JOIN memories m ON m.id = u.memory_id AND m.deleted_at IS NULL
		WHERE u.injected_count >= ? AND u.mentioned_count = 0`
	var n int
	if err := execFromCtx(ctx, r.db).QueryRowContext(ctx, q, minInjections).Scan(&n); err != nil {
		return 0, fmt.Errorf("sqlite: contar memorias dormidas: %w", err)
	}
	return n, nil
}

// scanUsage escanea una fila de memory_usage al modelo de dominio, normalizando los timestamps
// anulables a punteros (nil = nunca ocurrió).
func scanUsage(sc rowScanner) (domain.MemoryUsage, error) {
	var (
		u                     domain.MemoryUsage
		injectedAt, mentioned sql.NullString
	)
	if err := sc.Scan(&u.MemoryID, &u.InjectedCount, &u.MentionedCount, &injectedAt, &mentioned); err != nil {
		return domain.MemoryUsage{}, err
	}
	for _, f := range []struct {
		raw sql.NullString
		dst **time.Time
	}{{injectedAt, &u.LastInjectedAt}, {mentioned, &u.LastMentionedAt}} {
		if !f.raw.Valid {
			continue
		}
		t, err := parseTime(f.raw.String)
		if err != nil {
			return domain.MemoryUsage{}, err
		}
		*f.dst = &t
	}
	return u, nil
}
