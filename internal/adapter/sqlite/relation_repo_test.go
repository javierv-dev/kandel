package sqlite

import (
	"context"
	"testing"

	"github.com/javierv-dev/kandel/internal/domain"
)

func sampleRelation() domain.MemoryRelation {
	return domain.MemoryRelation{
		SyncID:    "rel-1",
		SourceID:  "mem-a",
		TargetID:  "mem-b",
		Kind:      domain.RelationPending,
		Status:    domain.JudgmentPending,
		CreatedAt: baseTime,
		UpdatedAt: baseTime,
	}
}

func TestRelationInsertAndGet(t *testing.T) {
	db := newTestDB(t)
	repo := NewRelationRepo(db)
	ctx := context.Background()

	id, err := repo.Insert(ctx, sampleRelation())
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get devolvió nil para una relación existente")
	}
	if got.SourceID != "mem-a" || got.TargetID != "mem-b" {
		t.Errorf("source/target no coinciden: %+v", got)
	}
	if got.Kind != domain.RelationPending || got.Status != domain.JudgmentPending {
		t.Errorf("estado inicial debe ser pending: %+v", got)
	}
}

func TestRelationFindBySourceKind(t *testing.T) {
	db := newTestDB(t)
	repo := NewRelationRepo(db)
	ctx := context.Background()

	// Enlace `continues` juzgado de mem-c hacia mem-b.
	rel := sampleRelation()
	rel.SyncID = "rel-cont"
	rel.SourceID = "mem-c"
	rel.TargetID = "mem-b"
	rel.Kind = domain.RelationContinues
	rel.Status = domain.JudgmentJudged
	if _, err := repo.Insert(ctx, rel); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.FindBySourceKind(ctx, "mem-c", domain.RelationContinues)
	if err != nil {
		t.Fatalf("FindBySourceKind: %v", err)
	}
	if got == nil || got.TargetID != "mem-b" {
		t.Fatalf("debía hallar el enlace hacia mem-b, dio: %+v", got)
	}

	// Otra source o verbo distinto → nil.
	if other, _ := repo.FindBySourceKind(ctx, "mem-x", domain.RelationContinues); other != nil {
		t.Errorf("source inexistente debe dar nil, dio: %+v", other)
	}
	if other, _ := repo.FindBySourceKind(ctx, "mem-c", domain.RelationSupersedes); other != nil {
		t.Errorf("verbo distinto debe dar nil, dio: %+v", other)
	}
}

func TestRelationJudge(t *testing.T) {
	db := newTestDB(t)
	repo := NewRelationRepo(db)
	ctx := context.Background()

	id, _ := repo.Insert(ctx, sampleRelation())
	v := domain.Verdict{
		Relation:   domain.RelationConflictsWith,
		Confidence: 0.87,
		Reasoning:  "afirman cosas opuestas",
		Model:      "claude-haiku",
	}
	if err := repo.Judge(ctx, id, v); err != nil {
		t.Fatalf("Judge: %v", err)
	}

	got, _ := repo.Get(ctx, id)
	if got.Status != domain.JudgmentJudged {
		t.Errorf("tras Judge el estado debe ser judged, fue %v", got.Status)
	}
	if got.Kind != domain.RelationConflictsWith {
		t.Errorf("el verbo debe actualizarse a conflicts_with, fue %v", got.Kind)
	}
	if got.Confidence != 0.87 || got.Reason != "afirman cosas opuestas" {
		t.Errorf("confianza/motivo no coinciden: %+v", got)
	}
	if !got.IsConflict() {
		t.Error("una relación juzgada como conflicts_with debe ser conflicto")
	}
}

func TestRelationListByStatus(t *testing.T) {
	db := newTestDB(t)
	repo := NewRelationRepo(db)
	ctx := context.Background()

	pending := sampleRelation()
	idP, _ := repo.Insert(ctx, pending)

	judged := sampleRelation()
	judged.SyncID = "rel-2"
	idJ, _ := repo.Insert(ctx, judged)
	_ = repo.Judge(ctx, idJ, domain.Verdict{Relation: domain.RelationRelated, Confidence: 0.5})

	gotPending, err := repo.ListByStatus(ctx, domain.JudgmentPending, 10)
	if err != nil {
		t.Fatalf("ListByStatus pending: %v", err)
	}
	if len(gotPending) != 1 || gotPending[0].ID != idP {
		t.Errorf("solo la pendiente debe listarse como pending, fue %+v", gotPending)
	}

	gotJudged, _ := repo.ListByStatus(ctx, domain.JudgmentJudged, 10)
	if len(gotJudged) != 1 || gotJudged[0].ID != idJ {
		t.Errorf("solo la juzgada debe listarse como judged, fue %+v", gotJudged)
	}
}

func TestRelationGetMissing(t *testing.T) {
	db := newTestDB(t)
	repo := NewRelationRepo(db)
	got, err := repo.Get(context.Background(), 999)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Errorf("Get de relación inexistente debe ser nil, fue %+v", got)
	}
}

// judgedRelation arma una relación ya juzgada entre dos sync_id.
func judgedRelation(syncID, source, target string, kind domain.RelationType) domain.MemoryRelation {
	return domain.MemoryRelation{
		SyncID: syncID, SourceID: source, TargetID: target,
		Kind: kind, Status: domain.JudgmentJudged, CreatedAt: baseTime, UpdatedAt: baseTime,
	}
}

func TestRelationFindJudgedPair(t *testing.T) {
	db := newTestDB(t)
	repo := NewRelationRepo(db)
	ctx := context.Background()

	if _, err := repo.Insert(ctx, judgedRelation("rel-1", "mem-a", "mem-b", domain.RelationConflictsWith)); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	// Se encuentra en cualquier dirección.
	for _, dir := range [][2]string{{"mem-a", "mem-b"}, {"mem-b", "mem-a"}} {
		got, err := repo.FindJudgedPair(ctx, dir[0], dir[1])
		if err != nil {
			t.Fatalf("FindJudgedPair(%v): %v", dir, err)
		}
		if got == nil || got.SyncID != "rel-1" {
			t.Errorf("debe encontrar el par en dirección %v, fue %+v", dir, got)
		}
	}
	// Un par sin relación juzgada devuelve nil.
	if got, _ := repo.FindJudgedPair(ctx, "mem-a", "mem-z"); got != nil {
		t.Errorf("par inexistente debe ser nil, fue %+v", got)
	}
}

// TestRelationPairUniqueIndex verifica la red de seguridad a nivel de base: no puede haber dos
// relaciones juzgadas del mismo par, ni siquiera en direcciones opuestas.
func TestRelationPairUniqueIndex(t *testing.T) {
	db := newTestDB(t)
	repo := NewRelationRepo(db)
	ctx := context.Background()

	if _, err := repo.Insert(ctx, judgedRelation("rel-1", "mem-a", "mem-b", domain.RelationConflictsWith)); err != nil {
		t.Fatalf("primer Insert: %v", err)
	}
	// La dirección inversa colapsa a la misma clave canónica → viola el índice único parcial.
	if _, err := repo.Insert(ctx, judgedRelation("rel-2", "mem-b", "mem-a", domain.RelationConflictsWith)); err == nil {
		t.Error("insertar el par inverso ya juzgado debe fallar por el índice único")
	}
}

func TestCountOpenConflicts(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	mem := NewMemoryRepo(db)
	rel := NewRelationRepo(db)
	ctx := context.Background()

	add := func(syncID, project string, scope domain.Scope) {
		_, err := mem.Add(ctx, domain.Memory{
			SyncID: syncID, SessionID: "s1", Project: project, Scope: scope,
			Type: domain.TypeDecision, Title: syncID, Content: "x",
			RevisionCount: 1, DuplicateCount: 1, CreatedAt: baseTime, UpdatedAt: baseTime,
		})
		if err != nil {
			t.Fatalf("Add %s: %v", syncID, err)
		}
	}
	add("mem-a", "proj", domain.ScopeProject)
	add("mem-b", "proj", domain.ScopeProject)
	if _, err := rel.Insert(ctx, judgedRelation("rel-1", "mem-a", "mem-b", domain.RelationConflictsWith)); err != nil {
		t.Fatalf("Insert relación: %v", err)
	}

	if n, _ := rel.CountOpenConflicts(ctx, "proj"); n != 1 {
		t.Errorf("el proyecto debe tener 1 conflicto abierto, dio %d", n)
	}
	// Otro proyecto (sin memorias globales implicadas) no ve este conflicto.
	if n, _ := rel.CountOpenConflicts(ctx, "otro"); n != 0 {
		t.Errorf("otro proyecto no debe ver el conflicto, dio %d", n)
	}
	// Al borrar (lógico) una de las memorias, el conflicto deja de contarse.
	if id, _ := idOf(t, mem, "mem-a"); true {
		if err := mem.SoftDelete(ctx, id); err != nil {
			t.Fatalf("SoftDelete: %v", err)
		}
	}
	if n, _ := rel.CountOpenConflicts(ctx, "proj"); n != 0 {
		t.Errorf("con una memoria borrada el conflicto no debe contarse, dio %d", n)
	}

	// Un conflicto que involucra una memoria compartida ("yaku-servidor,yaku-web") se cuenta en ambos proyectos acoplados.
	add("mem-y1", "yaku-servidor,yaku-web", domain.ScopeProject)
	add("mem-y2", "yaku-web", domain.ScopeProject)
	if _, err := rel.Insert(ctx, judgedRelation("rel-y", "mem-y1", "mem-y2", domain.RelationConflictsWith)); err != nil {
		t.Fatalf("Insert rel-y: %v", err)
	}
	for _, p := range []string{"yaku-web", "yaku-servidor"} {
		if n, _ := rel.CountOpenConflicts(ctx, p); n != 1 {
			t.Errorf("CountOpenConflicts(%s) debe contar el conflicto compartido (1), dio %d", p, n)
		}
	}
	if n, _ := rel.CountOpenConflicts(ctx, "yaku"); n != 0 {
		t.Errorf("CountOpenConflicts(yaku) no debe coincidir por prefijo, dio %d", n)
	}
}

// idOf resuelve el id local de una memoria por su sync_id (helper de test).
func idOf(t *testing.T, mem *MemoryRepo, syncID string) (int64, bool) {
	t.Helper()
	m, err := mem.GetBySyncID(context.Background(), syncID)
	if err != nil || m == nil {
		t.Fatalf("GetBySyncID %s: %v", syncID, err)
	}
	return m.ID, true
}
