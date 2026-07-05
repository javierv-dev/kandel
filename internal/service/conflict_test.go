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

// seedNormative inserta una memoria normativa con vector y los campos dados; devuelve su id.
func seedNormative(t *testing.T, obs *fakeObs, vecs *fakeVectorIndex, m domain.Memory) int64 {
	t.Helper()
	id, err := obs.Add(context.Background(), m)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	vecs.vecs[id] = []float32{1, 0, 0}
	return id
}

func TestSweepDetectionSameScopeConflict(t *testing.T) {
	obs, vecs, emb, rels := newFakeObs(), newFakeVectorIndex(), newFakeEmbedder(), &fakeRelations{}
	runner := &fakeRunner{verdict: domain.Verdict{Relation: domain.RelationConflictsWith, Confidence: 0.9, Reasoning: "chocan"}}
	a := seedConflictMemory(t, obs, vecs, "A", "usar JWT")
	b := seedConflictMemory(t, obs, vecs, "B", "usar sesiones")
	// B ya verificada: el barrido solo debe procesar A (evita el doble juicio del par).
	_ = obs.SetConflictChecked(context.Background(), b)
	svc := newConflictService(obs, vecs, emb, rels, runner)

	n, err := svc.SweepDetection(context.Background())
	if err != nil {
		t.Fatalf("SweepDetection error: %v", err)
	}
	if n != 1 {
		t.Fatalf("esperaba 1 normativa verificada, obtuve %d", n)
	}
	if len(rels.items) != 1 {
		t.Fatalf("esperaba 1 relación, hay %d", len(rels.items))
	}
	rel := rels.items[0]
	if rel.Kind != domain.RelationConflictsWith || rel.Status != domain.JudgmentJudged {
		t.Errorf("mismo scope debe dar conflicts_with juzgado: %+v", rel)
	}
	if rel.SourceID != "mem-A" || rel.TargetID != "mem-B" {
		t.Errorf("source debe ser la recién verificada (A): %+v", rel)
	}
	if ma, _ := obs.Get(context.Background(), a); ma == nil || !ma.ConflictChecked {
		t.Error("A debe quedar marcada como verificada")
	}
}

func TestSweepDetectionCrossScopeCascade(t *testing.T) {
	obs, vecs, emb, rels := newFakeObs(), newFakeVectorIndex(), newFakeEmbedder(), &fakeRelations{}
	runner := &fakeRunner{verdict: domain.Verdict{Relation: domain.RelationConflictsWith, Confidence: 0.8, Reasoning: "chocan"}}
	global := seedNormative(t, obs, vecs, domain.Memory{
		SyncID: "mem-G", Type: domain.TypeDecision, Scope: domain.ScopeGlobal, Title: "G", Content: "regla global",
	})
	proj := seedNormative(t, obs, vecs, domain.Memory{
		SyncID: "mem-P", Type: domain.TypeDecision, Scope: domain.ScopeProject, Project: "proj", Title: "P", Content: "regla de proyecto",
	})
	_ = obs.SetConflictChecked(context.Background(), proj) // se detecta la global
	svc := newConflictService(obs, vecs, emb, rels, runner)

	if _, err := svc.SweepDetection(context.Background()); err != nil {
		t.Fatalf("SweepDetection error: %v", err)
	}
	if len(rels.items) != 1 {
		t.Fatalf("esperaba 1 relación, hay %d", len(rels.items))
	}
	rel := rels.items[0]
	// Cross-scope: la de proyecto reemplaza a la global (cascada project > global).
	if rel.Kind != domain.RelationSupersedes || rel.SourceID != "mem-P" || rel.TargetID != "mem-G" {
		t.Errorf("cross-scope debe dar supersedes proyecto→global: %+v", rel)
	}
	_ = global
}

func TestSweepDetectionSupersedesByRecency(t *testing.T) {
	obs, vecs, emb, rels := newFakeObs(), newFakeVectorIndex(), newFakeEmbedder(), &fakeRelations{}
	runner := &fakeRunner{verdict: domain.Verdict{Relation: domain.RelationSupersedes, Confidence: 0.9}}
	older := seedNormative(t, obs, vecs, domain.Memory{
		SyncID: "mem-Old", Type: domain.TypeDecision, Scope: domain.ScopeProject, Project: "proj",
		Title: "Old", Content: "regla vieja", CreatedAt: time.Unix(1000, 0).UTC(),
	})
	newer := seedNormative(t, obs, vecs, domain.Memory{
		SyncID: "mem-New", Type: domain.TypeDecision, Scope: domain.ScopeProject, Project: "proj",
		Title: "New", Content: "regla nueva", CreatedAt: time.Unix(2000, 0).UTC(),
	})
	_ = obs.SetConflictChecked(context.Background(), newer) // se detecta la vieja
	svc := newConflictService(obs, vecs, emb, rels, runner)

	if _, err := svc.SweepDetection(context.Background()); err != nil {
		t.Fatalf("SweepDetection error: %v", err)
	}
	if len(rels.items) != 1 {
		t.Fatalf("esperaba 1 relación, hay %d", len(rels.items))
	}
	rel := rels.items[0]
	// Mismo scope + supersedes: la más reciente reemplaza a la más antigua.
	if rel.Kind != domain.RelationSupersedes || rel.SourceID != "mem-New" || rel.TargetID != "mem-Old" {
		t.Errorf("supersedes debe dirigir reciente→antigua: %+v", rel)
	}
	_ = older
}

func TestSweepDetectionNonConflictMarksCheckedWithoutRelation(t *testing.T) {
	obs, vecs, emb, rels := newFakeObs(), newFakeVectorIndex(), newFakeEmbedder(), &fakeRelations{}
	runner := &fakeRunner{verdict: domain.Verdict{Relation: domain.RelationCompatible, Confidence: 0.7}}
	a := seedConflictMemory(t, obs, vecs, "A", "x")
	b := seedConflictMemory(t, obs, vecs, "B", "y")
	_ = obs.SetConflictChecked(context.Background(), b)
	svc := newConflictService(obs, vecs, emb, rels, runner)

	n, err := svc.SweepDetection(context.Background())
	if err != nil {
		t.Fatalf("SweepDetection error: %v", err)
	}
	if n != 1 {
		t.Fatalf("esperaba 1 verificada, obtuve %d", n)
	}
	if len(rels.items) != 0 {
		t.Errorf("un veredicto no conflictivo no debe persistir relación: %+v", rels.items)
	}
	if ma, _ := obs.Get(context.Background(), a); ma == nil || !ma.ConflictChecked {
		t.Error("A debe quedar verificada aunque no haya conflicto")
	}
}

func TestSweepDetectionFactualSupersede(t *testing.T) {
	obs, vecs, emb, rels := newFakeObs(), newFakeVectorIndex(), newFakeEmbedder(), &fakeRelations{}
	runner := &fakeRunner{verdict: domain.Verdict{Relation: domain.RelationSupersedes, Confidence: 0.9}}
	// Dos descubrimientos (factuales) del mismo asunto: el hecho nuevo reemplaza al viejo (§20.E-bis).
	older := seedNormative(t, obs, vecs, domain.Memory{
		SyncID: "mem-Old", Type: domain.TypeDiscovery, Scope: domain.ScopeProject, Project: "proj",
		Title: "Old", Content: "config vieja", CreatedAt: time.Unix(1000, 0).UTC(),
	})
	newer := seedNormative(t, obs, vecs, domain.Memory{
		SyncID: "mem-New", Type: domain.TypeDiscovery, Scope: domain.ScopeProject, Project: "proj",
		Title: "New", Content: "config nueva", CreatedAt: time.Unix(2000, 0).UTC(),
	})
	_ = obs.SetConflictChecked(context.Background(), newer) // se procesa la vieja, que detecta la nueva
	svc := newConflictService(obs, vecs, emb, rels, runner)

	n, err := svc.SweepDetection(context.Background())
	if err != nil {
		t.Fatalf("SweepDetection error: %v", err)
	}
	if n != 1 {
		t.Fatalf("esperaba 1 factual verificado, obtuve %d", n)
	}
	if len(rels.items) != 1 {
		t.Fatalf("esperaba 1 relación, hay %d", len(rels.items))
	}
	rel := rels.items[0]
	// El más reciente reemplaza al más antiguo; el retrieval oculta el target (el hecho caduco).
	if rel.Kind != domain.RelationSupersedes || rel.SourceID != "mem-New" || rel.TargetID != "mem-Old" {
		t.Errorf("supersede factual debe dirigir reciente→antiguo: %+v", rel)
	}
	if mo, _ := obs.Get(context.Background(), older); mo == nil || !mo.ConflictChecked {
		t.Error("el factual procesado debe quedar verificado")
	}
}

func TestSweepDetectionFactualDropsNonSupersede(t *testing.T) {
	obs, vecs, emb, rels := newFakeObs(), newFakeVectorIndex(), newFakeEmbedder(), &fakeRelations{}
	// Para un hecho, una contradicción que NO es reemplazo (conflicts_with) no dispara banner: se
	// descarta sin persistir, pero la memoria queda verificada igual.
	runner := &fakeRunner{verdict: domain.Verdict{Relation: domain.RelationConflictsWith, Confidence: 0.8, Reasoning: "chocan"}}
	a := seedNormative(t, obs, vecs, domain.Memory{
		SyncID: "mem-A", Type: domain.TypeDiscovery, Scope: domain.ScopeProject, Project: "proj", Title: "A", Content: "hecho A",
	})
	b := seedNormative(t, obs, vecs, domain.Memory{
		SyncID: "mem-B", Type: domain.TypeDiscovery, Scope: domain.ScopeProject, Project: "proj", Title: "B", Content: "hecho B",
	})
	_ = obs.SetConflictChecked(context.Background(), b)
	svc := newConflictService(obs, vecs, emb, rels, runner)

	n, err := svc.SweepDetection(context.Background())
	if err != nil {
		t.Fatalf("SweepDetection error: %v", err)
	}
	if n != 1 {
		t.Fatalf("esperaba 1 factual verificado, obtuve %d", n)
	}
	if len(rels.items) != 0 {
		t.Errorf("un veredicto factual que no es supersede no debe persistirse: %+v", rels.items)
	}
	if ma, _ := obs.Get(context.Background(), a); ma == nil || !ma.ConflictChecked {
		t.Error("el factual debe quedar verificado aunque no se persista relación")
	}
}

func TestSweepDetectionFactualLoneMarksCheckedWithoutJudge(t *testing.T) {
	obs, vecs, emb, rels, runner := newFakeObs(), newFakeVectorIndex(), newFakeEmbedder(), &fakeRelations{}, &fakeRunner{}
	// Un factual solo (sin vecinos del mismo asunto) se marca verificado sin invocar al juez.
	d := seedNormative(t, obs, vecs, domain.Memory{
		SyncID: "mem-D", Type: domain.TypeDiscovery, Scope: domain.ScopeProject, Project: "proj", Title: "D", Content: "un hecho",
	})
	svc := newConflictService(obs, vecs, emb, rels, runner)

	n, err := svc.SweepDetection(context.Background())
	if err != nil {
		t.Fatalf("SweepDetection error: %v", err)
	}
	if n != 1 {
		t.Errorf("un factual solo debe verificarse igual, verificó %d", n)
	}
	if runner.lastPrompt != "" {
		t.Error("sin candidata comparable no debe invocarse el juez")
	}
	if md, _ := obs.Get(context.Background(), d); md == nil || !md.ConflictChecked {
		t.Error("el factual debe quedar verificado")
	}
}

// Un casi-duplicado con confianza alta se persiste como 'duplicate' y sobrevive la de mayor CALIDAD
// (más contenido), no la más reciente: el retrieval oculta la de menor calidad (target).
func TestSweepDetectionDuplicateHidesLowerQuality(t *testing.T) {
	obs, vecs, emb, rels := newFakeObs(), newFakeVectorIndex(), newFakeEmbedder(), &fakeRelations{}
	runner := &fakeRunner{verdict: domain.Verdict{Relation: domain.RelationDuplicate, Confidence: 0.95, Reasoning: "misma cosa"}}
	// 'rich' es más nueva pero MÁS COMPLETA; 'poor' es más vieja y escueta. Gana 'rich' por calidad.
	rich := seedNormative(t, obs, vecs, domain.Memory{
		SyncID: "mem-Rich", Type: domain.TypeArchitecture, Scope: domain.ScopeProject, Project: "proj",
		Title: "Arquitectura hexagonal", Content: "descripción larga y detallada de la arquitectura por capas",
		CreatedAt: time.Unix(2000, 0).UTC(),
	})
	poor := seedNormative(t, obs, vecs, domain.Memory{
		SyncID: "mem-Poor", Type: domain.TypeArchitecture, Scope: domain.ScopeProject, Project: "proj",
		Title: "Arquitectura hexagonal", Content: "capas", CreatedAt: time.Unix(1000, 0).UTC(),
	})
	_ = obs.SetConflictChecked(context.Background(), poor) // se procesa 'rich', que detecta a 'poor'
	svc := newConflictService(obs, vecs, emb, rels, runner)

	if _, err := svc.SweepDetection(context.Background()); err != nil {
		t.Fatalf("SweepDetection error: %v", err)
	}
	if len(rels.items) != 1 {
		t.Fatalf("esperaba 1 relación duplicate, hay %d", len(rels.items))
	}
	rel := rels.items[0]
	if rel.Kind != domain.RelationDuplicate || rel.SourceID != "mem-Rich" || rel.TargetID != "mem-Poor" {
		t.Errorf("duplicate debe conservar la de mayor calidad (source=Rich) y ocultar la pobre (target=Poor): %+v", rel)
	}
	_ = rich
}

// Un casi-duplicado por debajo del umbral de confianza NO se persiste (queda para revisión humana),
// pero la memoria se marca verificada igual.
func TestSweepDetectionDuplicateBelowConfidenceNotPersisted(t *testing.T) {
	obs, vecs, emb, rels := newFakeObs(), newFakeVectorIndex(), newFakeEmbedder(), &fakeRelations{}
	runner := &fakeRunner{verdict: domain.Verdict{Relation: domain.RelationDuplicate, Confidence: 0.5, Reasoning: "quizá"}}
	a := seedNormative(t, obs, vecs, domain.Memory{
		SyncID: "mem-A", Type: domain.TypeArchitecture, Scope: domain.ScopeProject, Project: "proj", Title: "Tema", Content: "una descripción",
	})
	b := seedNormative(t, obs, vecs, domain.Memory{
		SyncID: "mem-B", Type: domain.TypeArchitecture, Scope: domain.ScopeProject, Project: "proj", Title: "Tema", Content: "otra descripción",
	})
	_ = obs.SetConflictChecked(context.Background(), b)
	svc := newConflictService(obs, vecs, emb, rels, runner)

	if _, err := svc.SweepDetection(context.Background()); err != nil {
		t.Fatalf("SweepDetection error: %v", err)
	}
	if len(rels.items) != 0 {
		t.Errorf("un duplicate de baja confianza no debe persistirse: %+v", rels.items)
	}
	if ma, _ := obs.Get(context.Background(), a); ma == nil || !ma.ConflictChecked {
		t.Error("la memoria debe quedar verificada aunque no se persista relación")
	}
}

// El portón por título rescata una candidata que el coseno de CONTENIDO pierde: contenido por debajo
// del corte (no sería candidata) pero título por encima del corte de título. Es el caso 1↔47.
func TestSweepDetectionTitleGateSurfacesCandidate(t *testing.T) {
	obs, emb, rels := newFakeObs(), newFakeEmbedder(), &fakeRelations{}
	runner := &fakeRunner{verdict: domain.Verdict{Relation: domain.RelationDuplicate, Confidence: 0.95, Reasoning: "misma cosa reformulada"}}
	ctx := context.Background()

	processed, err := obs.Add(ctx, domain.Memory{
		SyncID: "mem-Proc", Type: domain.TypeDecision, Scope: domain.ScopeProject, Project: "proj",
		Title: "Arquitectura hexagonal", Content: "descripción larga del diseño por capas y sus fronteras",
	})
	if err != nil {
		t.Fatalf("add processed: %v", err)
	}
	cand, err := obs.Add(ctx, domain.Memory{
		SyncID: "mem-Cand", Type: domain.TypeDecision, Scope: domain.ScopeProject, Project: "proj",
		Title: "Arquitectura hexagonal", Content: "capas",
	})
	if err != nil {
		t.Fatalf("add cand: %v", err)
	}
	_ = obs.SetConflictChecked(ctx, cand) // solo se procesa 'processed', que debe descubrir a 'cand'

	// Contenido: la candidata puntúa 0.80, por debajo del corte normativo (0.85) → el gate de contenido la pierde.
	contentIdx := &fakeScoredIndex{scores: map[int64]float64{processed: 1.0, cand: 0.80}}
	// Título: la candidata puntúa 0.95, por encima del corte de título (0.88) → el gate de título la rescata.
	titleIdx := &fakeScoredIndex{scores: map[int64]float64{processed: 1.0, cand: 0.95}}

	svc := NewConflictService(obs, contentIdx, emb, rels, runner, passthroughUoW{}, fixedClock{now: time.Unix(1700000000, 0).UTC()},
		WithTitleGate(titleIdx, 0))

	if _, err := svc.SweepDetection(ctx); err != nil {
		t.Fatalf("SweepDetection error: %v", err)
	}
	if len(rels.items) != 1 || rels.items[0].Kind != domain.RelationDuplicate {
		t.Fatalf("el portón por título debía surface la candidata y persistir un duplicate: %+v", rels.items)
	}
}

func TestSweepDetectionAbortsWhenEmbedderDown(t *testing.T) {
	obs, vecs, emb, rels, runner := newFakeObs(), newFakeVectorIndex(), newFakeEmbedder(), &fakeRelations{}, &fakeRunner{}
	emb.failWith = errors.New("ollama caído")
	a := seedConflictMemory(t, obs, vecs, "A", "x")
	svc := newConflictService(obs, vecs, emb, rels, runner)

	if _, err := svc.SweepDetection(context.Background()); err == nil {
		t.Fatal("un embebedor caído debe abortar el barrido")
	}
	if ma, _ := obs.Get(context.Background(), a); ma == nil || ma.ConflictChecked {
		t.Error("A debe quedar SIN verificar para reintentar")
	}
}

func TestSweepDetectionAbortsWhenRunnerDown(t *testing.T) {
	obs, vecs, emb, rels := newFakeObs(), newFakeVectorIndex(), newFakeEmbedder(), &fakeRelations{}
	runner := &fakeRunner{failWith: errors.New("agente caído")}
	a := seedConflictMemory(t, obs, vecs, "A", "x")
	b := seedConflictMemory(t, obs, vecs, "B", "y")
	_ = obs.SetConflictChecked(context.Background(), b)
	svc := newConflictService(obs, vecs, emb, rels, runner)

	if _, err := svc.SweepDetection(context.Background()); err == nil {
		t.Fatal("un juez caído debe abortar el barrido")
	}
	if len(rels.items) != 0 {
		t.Error("no debe persistir relación si el juicio falla")
	}
	if ma, _ := obs.Get(context.Background(), a); ma == nil || ma.ConflictChecked {
		t.Error("A debe quedar SIN verificar para reintentar")
	}
}

func TestSweepDetectionCoalesces(t *testing.T) {
	obs, vecs, emb, rels, runner := newFakeObs(), newFakeVectorIndex(), newFakeEmbedder(), &fakeRelations{}, &fakeRunner{}
	svc := newConflictService(obs, vecs, emb, rels, runner)
	svc.mu.Lock() // simula un barrido en curso
	defer svc.mu.Unlock()

	n, err := svc.SweepDetection(context.Background())
	if err != nil {
		t.Fatalf("SweepDetection no debe fallar al coalescer: %v", err)
	}
	if n != 0 {
		t.Errorf("si ya hay un barrido en curso debe omitirse (0), dio %d", n)
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

// TestSweepDetectionBatchNoDuplicate cubre el bug del punto 2: en inserción en lote (dos normativas en
// conflicto, ambas sin verificar) el barrido juzgaba el par desde ambos lados y creaba dos filas
// simétricas. Con el salto-si-ya-juzgado + idempotencia debe quedar UNA sola relación.
func TestSweepDetectionBatchNoDuplicate(t *testing.T) {
	obs, vecs, emb, rels := newFakeObs(), newFakeVectorIndex(), newFakeEmbedder(), &fakeRelations{}
	runner := &fakeRunner{verdict: domain.Verdict{Relation: domain.RelationConflictsWith, Confidence: 0.9, Reasoning: "chocan"}}
	seedConflictMemory(t, obs, vecs, "A", "usar tabs")
	seedConflictMemory(t, obs, vecs, "B", "usar espacios")
	svc := newConflictService(obs, vecs, emb, rels, runner)

	n, err := svc.SweepDetection(context.Background())
	if err != nil {
		t.Fatalf("SweepDetection error: %v", err)
	}
	if n != 2 {
		t.Fatalf("esperaba 2 normativas verificadas, obtuve %d", n)
	}
	if len(rels.items) != 1 {
		t.Fatalf("el par debe producir UNA sola relación (sin duplicado simétrico), hay %d", len(rels.items))
	}
}

// TestCompareCrossScopeResolvesToSupersedes cubre el punto 1: mem_compare ahora comparte la lógica del
// barrido, así que un cruce project/global se resuelve como supersedes (proyecto→global) y dispara la
// cascada, en vez de quedar como un conflicts_with crudo que no resuelve nada.
func TestCompareCrossScopeResolvesToSupersedes(t *testing.T) {
	obs, vecs, emb, rels := newFakeObs(), newFakeVectorIndex(), newFakeEmbedder(), &fakeRelations{}
	runner := &fakeRunner{verdict: domain.Verdict{Relation: domain.RelationConflictsWith, Confidence: 0.9, Reasoning: "chocan"}}
	glob := seedNormative(t, obs, vecs, domain.Memory{
		SyncID: "mem-G", Type: domain.TypeDecision, Scope: domain.ScopeGlobal, Title: "G", Content: "regla global",
	})
	proj := seedNormative(t, obs, vecs, domain.Memory{
		SyncID: "mem-P", Type: domain.TypeDecision, Scope: domain.ScopeProject, Project: "proj", Title: "P", Content: "regla proyecto",
	})
	svc := newConflictService(obs, vecs, emb, rels, runner)

	rel, err := svc.Compare(context.Background(), proj, glob)
	if err != nil {
		t.Fatalf("Compare error: %v", err)
	}
	if rel.Kind != domain.RelationSupersedes || rel.SourceID != "mem-P" || rel.TargetID != "mem-G" {
		t.Errorf("cross-scope debe resolverse a supersedes proyecto→global: %+v", rel)
	}
	if len(rels.items) != 1 {
		t.Errorf("debe persistirse una relación, hay %d", len(rels.items))
	}
}

// TestCompareNonConflictNotPersisted verifica que un veredicto no conflictivo devuelto por mem_compare
// se informa a quien llama pero NO se persiste (antes ensuciaba la tabla con filas muertas que nadie lee).
func TestCompareNonConflictNotPersisted(t *testing.T) {
	obs, vecs, emb, rels := newFakeObs(), newFakeVectorIndex(), newFakeEmbedder(), &fakeRelations{}
	runner := &fakeRunner{verdict: domain.Verdict{Relation: domain.RelationRelated, Confidence: 0.7, Reasoning: "mismo tema"}}
	a := seedConflictMemory(t, obs, vecs, "A", "uno")
	b := seedConflictMemory(t, obs, vecs, "B", "dos")
	svc := newConflictService(obs, vecs, emb, rels, runner)

	rel, err := svc.Compare(context.Background(), a, b)
	if err != nil {
		t.Fatalf("Compare error: %v", err)
	}
	if rel.Kind != domain.RelationRelated {
		t.Errorf("debe devolver el veredicto crudo al llamante: %+v", rel)
	}
	if len(rels.items) != 0 {
		t.Errorf("un veredicto no conflictivo no debe persistirse, hay %d", len(rels.items))
	}
}

// TestComparePairIdempotent verifica que comparar el mismo par dos veces (en ambos sentidos) mantiene
// UNA sola relación: la segunda actualiza en sitio en vez de duplicar.
func TestComparePairIdempotent(t *testing.T) {
	obs, vecs, emb, rels := newFakeObs(), newFakeVectorIndex(), newFakeEmbedder(), &fakeRelations{}
	runner := &fakeRunner{verdict: domain.Verdict{Relation: domain.RelationConflictsWith, Confidence: 0.9, Reasoning: "chocan"}}
	a := seedConflictMemory(t, obs, vecs, "A", "usar tabs")
	b := seedConflictMemory(t, obs, vecs, "B", "usar espacios")
	svc := newConflictService(obs, vecs, emb, rels, runner)

	if _, err := svc.Compare(context.Background(), a, b); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Compare(context.Background(), b, a); err != nil {
		t.Fatal(err)
	}
	if len(rels.items) != 1 {
		t.Fatalf("comparar el par dos veces debe mantener UNA relación, hay %d", len(rels.items))
	}
}
