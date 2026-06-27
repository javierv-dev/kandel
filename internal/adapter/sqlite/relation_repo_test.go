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
