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
		SELECT memory_id, injected_count, mentioned_count, last_injected_at, last_mentioned_at,
		       base_injected, base_mentioned, baselined_at
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
// sobre lo que se está sirviendo hoy. Con total=false ordena por lo ocurrido desde la línea base;
// con total=true, por el acumulado de por vida. orderExpr es una constante de este archivo, nunca
// entrada externa.
func (r *UsageRepo) TopInjected(ctx context.Context, limit int, total bool) ([]domain.MemoryUsage, error) {
	if limit <= 0 {
		limit = 20
	}
	orderExpr := "(u.injected_count - u.base_injected)"
	if total {
		orderExpr = "u.injected_count"
	}
	q := `
		SELECT u.memory_id, u.injected_count, u.mentioned_count, u.last_injected_at, u.last_mentioned_at,
		       u.base_injected, u.base_mentioned, u.baselined_at
		FROM memory_usage u
		JOIN memories m ON m.id = u.memory_id AND m.deleted_at IS NULL
		ORDER BY ` + orderExpr + ` DESC, u.memory_id ASC
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

// dormantWhere es el criterio de "dormida" medido desde la línea base: inyectada al menos N veces
// desde el corte y sin una sola mención desde entonces. Compartido por el conteo y por los ids para
// que no puedan divergir.
const dormantWhere = `(u.injected_count - u.base_injected) >= ? AND (u.mentioned_count - u.base_mentioned) = 0`

// CountDormant cuenta las memorias vivas inyectadas al menos minInjections veces desde su línea base
// y no mencionadas desde entonces: peso constante en el contexto sin rendimiento observable.
func (r *UsageRepo) CountDormant(ctx context.Context, minInjections int) (int, error) {
	if minInjections <= 0 {
		minInjections = 1
	}
	q := `
		SELECT count(*)
		FROM memory_usage u
		JOIN memories m ON m.id = u.memory_id AND m.deleted_at IS NULL
		WHERE ` + dormantWhere
	var n int
	if err := execFromCtx(ctx, r.db).QueryRowContext(ctx, q, minInjections).Scan(&n); err != nil {
		return 0, fmt.Errorf("sqlite: contar memorias dormidas: %w", err)
	}
	return n, nil
}

// DormantIDs devuelve los ids de las memorias que cuenta CountDormant, en orden ascendente.
func (r *UsageRepo) DormantIDs(ctx context.Context, minInjections int) ([]int64, error) {
	if minInjections <= 0 {
		minInjections = 1
	}
	q := `
		SELECT u.memory_id
		FROM memory_usage u
		JOIN memories m ON m.id = u.memory_id AND m.deleted_at IS NULL
		WHERE ` + dormantWhere + `
		ORDER BY u.memory_id ASC`
	rows, err := execFromCtx(ctx, r.db).QueryContext(ctx, q, minInjections)
	if err != nil {
		return nil, fmt.Errorf("sqlite: listar memorias dormidas: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("sqlite: escanear memoria dormida: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Rebase fija la línea base de cada id a sus contadores actuales. Los totales no se tocan. Sin fila
// previa la crea con contadores 0 y base 0 (rebasar algo nunca inyectada es válido y no falla). Una
// memoria inexistente (clave foránea) se salta, igual que en bump.
func (r *UsageRepo) Rebase(ctx context.Context, ids []int64, at time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	const q = `
		INSERT INTO memory_usage (memory_id, baselined_at) VALUES (?, ?)
		ON CONFLICT(memory_id) DO UPDATE SET
			base_injected  = injected_count,
			base_mentioned = mentioned_count,
			baselined_at   = excluded.baselined_at`
	stamp := formatTime(at)
	exec := execFromCtx(ctx, r.db)
	for _, id := range ids {
		if _, err := exec.ExecContext(ctx, q, id, stamp); err != nil {
			if esViolaciónDeClaveForánea(err) {
				continue
			}
			return fmt.Errorf("sqlite: fijar línea base de la memoria %d: %w", id, err)
		}
	}
	return nil
}

// scanUsage escanea una fila de memory_usage al modelo de dominio, normalizando los timestamps
// anulables a punteros (nil = nunca ocurrió).
func scanUsage(sc rowScanner) (domain.MemoryUsage, error) {
	var (
		u                                domain.MemoryUsage
		injectedAt, mentioned, baselined sql.NullString
	)
	if err := sc.Scan(&u.MemoryID, &u.InjectedCount, &u.MentionedCount, &injectedAt, &mentioned,
		&u.BaseInjected, &u.BaseMentioned, &baselined); err != nil {
		return domain.MemoryUsage{}, err
	}
	for _, f := range []struct {
		raw sql.NullString
		dst **time.Time
	}{{injectedAt, &u.LastInjectedAt}, {mentioned, &u.LastMentionedAt}, {baselined, &u.BaselinedAt}} {
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
