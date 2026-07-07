package ports

import (
	"context"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
)

// Change es una fila del change-feed: el estado ACTUAL de una entidad lista para replicarse al hub.
// A diferencia de SyncMutation (que es una entrada de cola con su Seq local), Change es lo que viaja
// por la red: identidad + operación + payload + el árbitro de LWW (UpdatedAt). El hub no interpreta
// el payload (es tonto de dominio); solo lo guarda keyed por Key y ordena con su server_seq.
type Change struct {
	Key       string        // sync_id: identidad estable de la entidad
	Op        domain.SyncOp // upsert | delete (delete = tombstone)
	Payload   []byte        // JSON del estado de la entidad (para memoria: domain.Memory completa)
	UpdatedAt time.Time     // reloj del origen; árbitro de LWW en servidor y cliente
	Project   string        // proyecto de origen (para filtrado por enrolamiento; opcional)
}

// SyncClient es el transporte del sync por change-feed: reemplaza a Transport (chunk+manifest). El
// orden vive en el servidor (server_seq) y el cliente solo lleva un cursor por target. El mismo
// cliente sirve para cualquier namespace (memory, skills, …): el hub es genérico.
//
// El par (ns, target) localiza el almacén del hub: ns es la clase de dato (memory, skills) y target
// es el token/namespace del usuario (identifica su slot y autentica). El adaptador HTTP lo traduce a
// la ruta /{ns}/{target}/changes con el token como Bearer.
type SyncClient interface {
	// Push envía las filas cambiadas; el servidor aplica LWW por UpdatedAt y asigna server_seq. Un
	// lote vacío es un no-op (no viaja nada).
	Push(ctx context.Context, ns, target string, changes []Change) error
	// Pull devuelve las filas con server_seq > cursor y el nuevo cursor (el mayor server_seq servido).
	// Con cursor 0 devuelve el estado actual completo (arranque en frío).
	Pull(ctx context.Context, ns, target string, cursor int64) (changes []Change, newCursor int64, err error)
}
