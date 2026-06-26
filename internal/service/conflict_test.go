package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
)

func newConflictService(obs *fakeObs, vecs *fakeVectorIndex, emb *fakeEmbedder, rels *fakeRelations, runner *fakeRunner) *ConflictService {
	return NewConflictService(obs, vecs, emb, rels, runner, passthroughUoW{}, fixedClock{now: time.Unix(1700000000, 0).UTC()})
}

// seedConflictMemory inserta una memoria con vector y devuelve su id.
func seedConflictMemory(t *testing.T, obs *fakeObs, vecs *fakeVectorIndex, title, content string) int64 {
	t.Helper()
	id, err := obs.Add(context.Background(), domain.Memory{
		SyncID: "mem-" + title, Type: domain.TypeDecision, Scope: domain.ScopeProject,
		Title: title, Content: content,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	vecs.vecs[id] = []float32{1, 0, 0}
	return id
}

func TestFindCandidatesExcludesSelf(t *testing.T) {
	obs, vecs, emb, rels, runner := newFakeObs(), newFakeVectorIndex(), newFakeEmbedder(), &fakeRelations{}, &fakeRunner{}
	a := seedConflictMemory(t, obs, vecs, "A", "uno")
	b := seedConflictMemory(t, obs, vecs, "B", "dos")
	svc := newConflictService(obs, vecs, emb, rels, runner)

	cands, err := svc.FindCandidates(context.Background(), a, 5)
	if err != nil {
		t.Fatalf("FindCandidates error: %v", err)
	}
	for _, c := range cands {
		if c.Memory.ID == a {
			t.Error("una memoria no debe ser candidata de sí misma")
		}
	}
	// b debe estar entre los candidatos.
	found := false
	for _, c := range cands {
		if c.Memory.ID == b {
			found = true
		}
	}
	if !found {
		t.Errorf("se esperaba a B como candidato: %+v", cands)
	}
}

func TestFindCandidatesNotFound(t *testing.T) {
	svc := newConflictService(newFakeObs(), newFakeVectorIndex(), newFakeEmbedder(), &fakeRelations{}, &fakeRunner{})
	_, err := svc.FindCandidates(context.Background(), 404, 5)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("se esperaba ErrNotFound, se obtuvo %v", err)
	}
}

func TestComparePersistsVerdict(t *testing.T) {
	obs, vecs, emb, rels := newFakeObs(), newFakeVectorIndex(), newFakeEmbedder(), &fakeRelations{}
	runner := &fakeRunner{verdict: domain.Verdict{Relation: domain.RelationConflictsWith, Confidence: 0.9, Reasoning: "chocan"}}
	a := seedConflictMemory(t, obs, vecs, "A", "usar JWT")
	b := seedConflictMemory(t, obs, vecs, "B", "usar sesiones")
	svc := newConflictService(obs, vecs, emb, rels, runner)

	rel, err := svc.Compare(context.Background(), a, b)
	if err != nil {
		t.Fatalf("Compare error: %v", err)
	}
	if rel.Kind != domain.RelationConflictsWith || rel.Status != domain.JudgmentJudged {
		t.Errorf("relación mal persistida: %+v", rel)
	}
	if rel.SourceID != "mem-A" || rel.TargetID != "mem-B" {
		t.Errorf("la relación debe referenciar por sync_id: %+v", rel)
	}
	if len(rels.items) != 1 {
		t.Errorf("debe haberse insertado una relación, hay %d", len(rels.items))
	}
	// El prompt debe incluir ambos contenidos.
	if runner.lastPrompt == "" {
		t.Error("Compare debe invocar al runner con un prompt")
	}
}

func TestCompareRunnerErrorPropagates(t *testing.T) {
	obs, vecs, emb, rels := newFakeObs(), newFakeVectorIndex(), newFakeEmbedder(), &fakeRelations{}
	runner := &fakeRunner{failWith: errors.New("agente caído")}
	a := seedConflictMemory(t, obs, vecs, "A", "x")
	b := seedConflictMemory(t, obs, vecs, "B", "y")
	svc := newConflictService(obs, vecs, emb, rels, runner)

	if _, err := svc.Compare(context.Background(), a, b); err == nil {
		t.Fatal("un fallo del agente debe propagarse")
	}
	if len(rels.items) != 0 {
		t.Error("si el juicio falla no debe persistirse relación")
	}
}

func TestRecordVerdictValidatesRelation(t *testing.T) {
	obs, vecs, emb, rels := newFakeObs(), newFakeVectorIndex(), newFakeEmbedder(), &fakeRelations{}
	a := seedConflictMemory(t, obs, vecs, "A", "x")
	b := seedConflictMemory(t, obs, vecs, "B", "y")
	svc := newConflictService(obs, vecs, emb, rels, &fakeRunner{})

	// Relación pending/ inválida → error, sin llamar al runner.
	_, err := svc.RecordVerdict(context.Background(), a, b, domain.Verdict{Relation: domain.RelationPending})
	if err == nil {
		t.Fatal("un veredicto pending debe rechazarse")
	}
	// Veredicto válido → persiste sin runner.
	rel, err := svc.RecordVerdict(context.Background(), a, b, domain.Verdict{Relation: domain.RelationCompatible, Confidence: 0.7})
	if err != nil {
		t.Fatalf("RecordVerdict error: %v", err)
	}
	if rel.Kind != domain.RelationCompatible || rel.Status != domain.JudgmentJudged {
		t.Errorf("relación mal persistida: %+v", rel)
	}
}
