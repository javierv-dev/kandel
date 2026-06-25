package ports

import "context"

// Tx representa una transacción en curso. Es deliberadamente opaco: la capa de servicio lo
// recibe y lo pasa a los repos que deben participar en la misma transacción (p. ej.
// SyncRepo.EnqueueMutation), pero nunca conoce el tipo concreto (*sql.Tx). El adaptador de
// persistencia es quien lo materializa.
type Tx interface {
	// marcador de transacción; sin métodos públicos para no filtrar el detalle de SQL.
	tx()
}

// UnitOfWork ejecuta una función dentro de una transacción atómica. Si fn devuelve error,
// se revierte todo; si no, se confirma. Permite que un caso de uso escriba entidad + vector +
// mutación de sync de forma indivisible, sin que el servicio conozca el motor de base de datos.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(tx Tx) error) error
}
