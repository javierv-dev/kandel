package domain

import "errors"

// Errores de validación del dominio. Son centinelas (comparables con errors.Is) para que
// las capas superiores reaccionen sin acoplarse al texto del mensaje.
var (
	// ErrEmptyTitle indica que el título de una memoria está vacío.
	ErrEmptyTitle = errors.New("domain: el título no puede estar vacío")
	// ErrEmptyContent indica que el contenido de una memoria está vacío.
	ErrEmptyContent = errors.New("domain: el contenido no puede estar vacío")
	// ErrInvalidType indica un tipo de memoria no reconocido.
	ErrInvalidType = errors.New("domain: tipo de memoria inválido")
	// ErrInvalidScope indica un alcance no reconocido.
	ErrInvalidScope = errors.New("domain: alcance (scope) inválido")
	// ErrInvalidResolution indica un estado de resolución no reconocido.
	ErrInvalidResolution = errors.New("domain: resolución inválida")
	// ErrAmbiguousProject indica que el proyecto no se pudo resolver sin ambigüedad.
	ErrAmbiguousProject = errors.New("domain: proyecto ambiguo")
	// ErrNotFound indica que la entidad solicitada no existe.
	ErrNotFound = errors.New("domain: no encontrado")
	// ErrSelfContinue indica que se intentó continuar una entrada de bitácora a sí misma.
	ErrSelfContinue = errors.New("domain: una entrada no puede continuarse a sí misma")
	// ErrUnknownSession indica que se intentó ligar algo a una sesión que no existe. Lo traduce el
	// adaptador desde la violación de la clave foránea, para que la capa de servicio pueda decidir
	// qué hacer sin conocer los códigos de error de SQLite.
	ErrUnknownSession = errors.New("domain: la sesión referenciada no existe")
)
