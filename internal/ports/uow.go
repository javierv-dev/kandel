package ports

import "context"

// UnitOfWork ejecuta una función dentro de una transacción atómica. Si fn devuelve error,
// se revierte todo; si no, se confirma. Garantiza que un caso de uso pueda escribir varias
// entidades (p. ej. una memoria y su mutación de sync) de forma indivisible, sin conocer
// el motor de base de datos.
//
// La transacción viaja dentro del context que recibe fn: los repos llamados con ese context
// participan automáticamente en la misma transacción. El servicio no manipula ningún handle de
// transacción; solo propaga el context. El adaptador es quien materializa todo esto.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}
