package sqlite

import (
	"context"
	"testing"
	"time"
)

// addMemoryForUsage inserta una memoria mínima (sin sesión: la telemetría no depende de ella) y
// devuelve su id, para colgarle telemetría.
func addMemoryForUsage(t *testing.T, db *DB, title string) int64 {
	t.Helper()
	m := sampleMemory()
	m.SessionID = ""
	m.SyncID = "mem-" + title
	m.Title = title
	m.NormalizedHash = "hash-" + title
	id, err := NewMemoryRepo(db).Add(context.Background(), m)
	if err != nil {
		t.Fatalf("Add memoria %q: %v", title, err)
	}
	return id
}

// La primera inyección crea la fila y las siguientes suman: es un UPSERT, no un insert que falle al
// repetirse. Sin esto el contador se quedaría en 1 para siempre.
func TestUsageRecordInjectedAcumula(t *testing.T) {
	db := newTestDB(t)
	repo := NewUsageRepo(db)
	ctx := context.Background()
	id := addMemoryForUsage(t, db, "una decisión cualquiera")

	for range 3 {
		if err := repo.RecordInjected(ctx, []int64{id}, baseTime); err != nil {
			t.Fatalf("RecordInjected: %v", err)
		}
	}

	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.InjectedCount != 3 {
		t.Errorf("injected_count = %d, se esperaba 3", got.InjectedCount)
	}
	if got.MentionedCount != 0 {
		t.Errorf("mentioned_count = %d, se esperaba 0 (no se mencionó)", got.MentionedCount)
	}
	if got.LastInjectedAt == nil || !got.LastInjectedAt.Equal(baseTime) {
		t.Errorf("last_injected_at = %v, se esperaba %v", got.LastInjectedAt, baseTime)
	}
}

// Los dos contadores son independientes: mencionar no debe tocar las inyecciones ni al revés.
func TestUsageContadoresIndependientes(t *testing.T) {
	db := newTestDB(t)
	repo := NewUsageRepo(db)
	ctx := context.Background()
	id := addMemoryForUsage(t, db, "otra decisión")

	if err := repo.RecordInjected(ctx, []int64{id}, baseTime); err != nil {
		t.Fatalf("RecordInjected: %v", err)
	}
	if err := repo.RecordMentioned(ctx, []int64{id}, baseTime); err != nil {
		t.Fatalf("RecordMentioned: %v", err)
	}

	got, _ := repo.Get(ctx, id)
	if got.InjectedCount != 1 || got.MentionedCount != 1 {
		t.Errorf("contadores = (%d, %d), se esperaba (1, 1)", got.InjectedCount, got.MentionedCount)
	}
}

// Una memoria sin fila devuelve el cero, no un error: "nunca inyectada" es una respuesta válida y
// es el estado de TODO lo guardado antes de que existiera la telemetría.
func TestUsageGetSinFilaEsCero(t *testing.T) {
	db := newTestDB(t)
	got, err := NewUsageRepo(db).Get(context.Background(), 999)
	if err != nil {
		t.Fatalf("Get de una memoria sin telemetría no debe fallar: %v", err)
	}
	if got.InjectedCount != 0 || got.LastInjectedAt != nil {
		t.Errorf("se esperaba el cero, fue %+v", got)
	}
}

// CountDormant es la señal de poda: inyectada lo bastante como para juzgarla y nunca mencionada. La
// que sí se mencionó no cuenta, por muchas veces que se haya inyectado.
func TestUsageCountDormant(t *testing.T) {
	db := newTestDB(t)
	repo := NewUsageRepo(db)
	ctx := context.Background()
	dormida := addMemoryForUsage(t, db, "memoria que nadie usa")
	usada := addMemoryForUsage(t, db, "memoria que sí se usa")

	for range 5 {
		repo.RecordInjected(ctx, []int64{dormida, usada}, baseTime)
	}
	repo.RecordMentioned(ctx, []int64{usada}, baseTime)

	n, err := repo.CountDormant(ctx, 5)
	if err != nil {
		t.Fatalf("CountDormant: %v", err)
	}
	if n != 1 {
		t.Errorf("dormidas = %d, se esperaba 1 (solo la que nunca se mencionó)", n)
	}

	// Con el mínimo por encima de las inyecciones registradas, ninguna califica: la muestra es
	// demasiado pequeña para condenar a una memoria.
	if n, _ := repo.CountDormant(ctx, 6); n != 0 {
		t.Errorf("con mínimo 6 no debía haber dormidas, hubo %d", n)
	}
}

// TopInjected solo mira memorias vivas: los contadores de una borrada siguen en la tabla (el borrado
// es lógico) pero ya no dicen nada sobre lo que se está sirviendo hoy.
func TestUsageTopInjectedIgnoraBorradas(t *testing.T) {
	db := newTestDB(t)
	repo := NewUsageRepo(db)
	ctx := context.Background()
	viva := addMemoryForUsage(t, db, "memoria viva")
	borrada := addMemoryForUsage(t, db, "memoria borrada")
	repo.RecordInjected(ctx, []int64{viva}, baseTime)
	repo.RecordInjected(ctx, []int64{borrada, borrada}, baseTime)

	if err := NewMemoryRepo(db).SoftDelete(ctx, borrada); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	rows, err := repo.TopInjected(ctx, 10, true)
	if err != nil {
		t.Fatalf("TopInjected: %v", err)
	}
	if len(rows) != 1 || rows[0].MemoryID != viva {
		t.Errorf("se esperaba solo la memoria viva (%d), fue %+v", viva, rows)
	}
}

// Rebase fija una foto de los contadores y NO toca los totales: lo ocurrido "desde" el corte es la
// diferencia, y lo que pase después sigue sumando sobre el acumulado de por vida.
func TestUsageRebaseConservaTotalesYMideDesdeElCorte(t *testing.T) {
	db := newTestDB(t)
	repo := NewUsageRepo(db)
	ctx := context.Background()
	id := addMemoryForUsage(t, db, "memoria reescrita")

	for range 4 {
		repo.RecordInjected(ctx, []int64{id}, baseTime)
	}
	repo.RecordMentioned(ctx, []int64{id}, baseTime)

	corte := baseTime.Add(time.Hour)
	if err := repo.Rebase(ctx, []int64{id}, corte); err != nil {
		t.Fatalf("Rebase: %v", err)
	}
	got, _ := repo.Get(ctx, id)
	if got.InjectedCount != 4 || got.MentionedCount != 1 {
		t.Errorf("los totales no debían tocarse, fueron (%d, %d)", got.InjectedCount, got.MentionedCount)
	}
	if got.InjectedSince() != 0 || got.MentionedSince() != 0 {
		t.Errorf("justo tras el corte el 'desde' debe ser 0, fue (%d, %d)", got.InjectedSince(), got.MentionedSince())
	}
	if got.BaselinedAt == nil || !got.BaselinedAt.Equal(corte) {
		t.Errorf("baselined_at = %v, se esperaba %v", got.BaselinedAt, corte)
	}

	repo.RecordInjected(ctx, []int64{id, id}, baseTime)
	got, _ = repo.Get(ctx, id)
	if got.InjectedCount != 6 || got.InjectedSince() != 2 {
		t.Errorf("tras 2 inyecciones nuevas: total=%d desde=%d, se esperaba 6 y 2", got.InjectedCount, got.InjectedSince())
	}
}

// Rebasar una memoria sin fila (nunca inyectada) es válido: crea la fila en cero, sin error.
func TestUsageRebaseSinFilaPreviaCreaLaFila(t *testing.T) {
	db := newTestDB(t)
	repo := NewUsageRepo(db)
	ctx := context.Background()
	id := addMemoryForUsage(t, db, "nunca inyectada")

	if err := repo.Rebase(ctx, []int64{id}, baseTime); err != nil {
		t.Fatalf("Rebase sin fila previa: %v", err)
	}
	got, _ := repo.Get(ctx, id)
	if got.InjectedCount != 0 || got.BaselinedAt == nil {
		t.Errorf("se esperaba fila en cero con línea base, fue %+v", got)
	}
}

// Rebasar un id que no existe (clave foránea) no es un fallo: se salta, como en las inyecciones.
func TestUsageRebaseDeMemoriaInexistenteSeSalta(t *testing.T) {
	db := newTestDB(t)
	if err := NewUsageRepo(db).Rebase(context.Background(), []int64{99999}, baseTime); err != nil {
		t.Fatalf("Rebase de un id inexistente no debe fallar: %v", err)
	}
}

// La señal de poda se mide desde la línea base: una memoria ya revisada deja de figurar como dormida
// hasta que acumule de nuevo suficientes inyecciones sin mención. Es el punto de todo el cambio.
func TestUsageDormantSeMideDesdeLaLineaBase(t *testing.T) {
	db := newTestDB(t)
	repo := NewUsageRepo(db)
	ctx := context.Background()
	revisada := addMemoryForUsage(t, db, "memoria ya revisada")
	intacta := addMemoryForUsage(t, db, "memoria sin tocar")

	for range 6 {
		repo.RecordInjected(ctx, []int64{revisada, intacta}, baseTime)
	}
	if n, _ := repo.CountDormant(ctx, 5); n != 2 {
		t.Fatalf("antes del corte debían ser 2 dormidas, fueron %d", n)
	}

	repo.Rebase(ctx, []int64{revisada}, baseTime)
	ids, err := repo.DormantIDs(ctx, 5)
	if err != nil {
		t.Fatalf("DormantIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != intacta {
		t.Fatalf("tras rebasar solo debía quedar la intacta (%d), fue %v", intacta, ids)
	}
	if n, _ := repo.CountDormant(ctx, 5); n != 1 {
		t.Errorf("CountDormant debe coincidir con DormantIDs, fue %d", n)
	}

	// Vuelve a acumular inyecciones sin mención tras el corte: vuelve a ser dormida.
	for range 5 {
		repo.RecordInjected(ctx, []int64{revisada}, baseTime)
	}
	if n, _ := repo.CountDormant(ctx, 5); n != 2 {
		t.Errorf("tras 5 inyecciones nuevas debía volver a ser dormida (2 en total), fueron %d", n)
	}

	// Una mención posterior al corte la saca de la lista.
	repo.RecordMentioned(ctx, []int64{revisada}, baseTime)
	if n, _ := repo.CountDormant(ctx, 5); n != 1 {
		t.Errorf("una mención tras el corte debía sacarla de dormidas, fueron %d", n)
	}
}

// TopInjected ordena por lo ocurrido desde la línea base por defecto y por el acumulado con total.
func TestUsageTopInjectedOrdenaPorDesdeOPorTotal(t *testing.T) {
	db := newTestDB(t)
	repo := NewUsageRepo(db)
	ctx := context.Background()
	vieja := addMemoryForUsage(t, db, "muchas inyecciones antes del corte")
	nueva := addMemoryForUsage(t, db, "pocas pero recientes")

	for range 10 {
		repo.RecordInjected(ctx, []int64{vieja}, baseTime)
	}
	repo.Rebase(ctx, []int64{vieja}, baseTime)
	repo.RecordInjected(ctx, []int64{nueva, nueva, nueva}, baseTime)

	desde, _ := repo.TopInjected(ctx, 10, false)
	if len(desde) != 2 || desde[0].MemoryID != nueva {
		t.Errorf("por 'desde' debía ir primero %d, fue %+v", nueva, desde)
	}
	total, _ := repo.TopInjected(ctx, 10, true)
	if len(total) != 2 || total[0].MemoryID != vieja {
		t.Errorf("por total debía ir primero %d, fue %+v", vieja, total)
	}
}
