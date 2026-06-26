package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/javierv-dev/kandel/internal/ports"
)

// txCtxKey es la clave (privada al paquete) bajo la que viaja la transacción en el context.
type txCtxKey struct{}

// withTx devuelve un context que lleva la transacción dada.
func withTx(ctx context.Context, tx *sql.Tx) context.Context {
	return context.WithValue(ctx, txCtxKey{}, tx)
}

// execFromCtx devuelve la transacción que lleva el context, o el fallback si no hay ninguna.
// Es lo que hace tx-aware a todos los repos: si se les llama con un context dentro de
// UnitOfWork.Do, operan sobre la transacción; si no, directo contra la conexión.
func execFromCtx(ctx context.Context, fallback execer) execer {
	if tx, ok := ctx.Value(txCtxKey{}).(*sql.Tx); ok && tx != nil {
		return tx
	}
	return fallback
}

// UnitOfWork implementa ports.UnitOfWork sobre SQLite: ejecuta una función dentro de una
// transacción y confirma o revierte según el resultado.
type UnitOfWork struct {
	db *DB
}

// Aserción en tiempo de compilación: cumple el contrato del port.
var _ ports.UnitOfWork = (*UnitOfWork)(nil)

// NewUnitOfWork crea la unidad de trabajo sobre la conexión dada.
func NewUnitOfWork(db *DB) *UnitOfWork {
	return &UnitOfWork{db: db}
}

// Do abre una transacción, la inyecta en el context que recibe fn y la confirma; si fn devuelve
// error (o entra en pánico), revierte todo. Los repos llamados con ese context participan en la
// misma transacción.
func (u *UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	tx, err := u.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: abrir transacción: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(withTx(ctx, tx)); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: confirmar transacción: %w", err)
	}
	return nil
}
