package sqlite

import (
	"context"
	"fmt"

	"github.com/javierv-dev/kandel/internal/ports"
)

// sqlTx materializa ports.Tx envolviendo la transacción real de database/sql. ports.Tx es una
// interfaz marcador vacía: los repos transaccionales reciben un ports.Tx y lo afirman a *sqlTx
// para obtener el execer subyacente, sin que el servicio conozca el detalle.
type sqlTx struct {
	exec execer
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

// Do abre una transacción, ejecuta fn y la confirma; si fn devuelve error (o entra en pánico),
// revierte todo. Garantiza atomicidad para los casos de uso que escriben varias entidades.
func (u *UnitOfWork) Do(ctx context.Context, fn func(tx ports.Tx) error) (err error) {
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
	if err := fn(&sqlTx{exec: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: confirmar transacción: %w", err)
	}
	return nil
}
