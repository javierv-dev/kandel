package sqlite

import (
	"context"
	"testing"
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

	rows, err := repo.TopInjected(ctx, 10)
	if err != nil {
		t.Fatalf("TopInjected: %v", err)
	}
	if len(rows) != 1 || rows[0].MemoryID != viva {
		t.Errorf("se esperaba solo la memoria viva (%d), fue %+v", viva, rows)
	}
}
