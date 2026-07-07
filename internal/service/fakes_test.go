package service

import (
	"context"
	"sort"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// fixedClock es un reloj inmóvil para tests deterministas.
type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

// passthroughUoW ejecuta fn sin transacción real: los fakes no necesitan atomicidad para probar
// la lógica de aplicación. (La atomicidad real se prueba en el adaptador SQLite.)
type passthroughUoW struct{}

func (passthroughUoW) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

// fakeSync registra las mutaciones encoladas; el resto de métodos son inertes (no se ejercitan
// en los tests de servicio).
type fakeSync struct {
	enqueued []domain.SyncMutation
}

func (f *fakeSync) EnqueueMutation(ctx context.Context, m domain.SyncMutation) error {
	f.enqueued = append(f.enqueued, m)
	return nil
}
func (f *fakeSync) ListPending(context.Context, string, int64, int) ([]domain.SyncMutation, error) {
	return nil, nil
}
func (f *fakeSync) Ack(context.Context, string, []int64) error { return nil }
func (f *fakeSync) GetCursor(context.Context, string) (int64, error) {
	return 0, nil
}
func (f *fakeSync) SetCursor(context.Context, string, int64) error { return nil }
func (f *fakeSync) AcquireLease(context.Context, string, string, time.Duration) (bool, error) {
	return true, nil
}
func (f *fakeSync) ReleaseLease(context.Context, string, string) error { return nil }

// fakeObs es un MemoryRepo en memoria.
type fakeObs struct {
	items  map[int64]domain.Memory
	nextID int64
}

func newFakeObs() *fakeObs {
	return &fakeObs{items: map[int64]domain.Memory{}}
}

func (f *fakeObs) Add(_ context.Context, o domain.Memory) (int64, error) {
	f.nextID++
	o.ID = f.nextID
	f.items[o.ID] = o
	return o.ID, nil
}

func (f *fakeObs) Get(_ context.Context, id int64) (*domain.Memory, error) {
	o, ok := f.items[id]
	if !ok || o.IsDeleted() {
		return nil, nil
	}
	c := o
	return &c, nil
}

func (f *fakeObs) GetBySyncID(_ context.Context, syncID string) (*domain.Memory, error) {
	for _, o := range f.items {
		if o.SyncID == syncID && !o.IsDeleted() {
			c := o
			return &c, nil
		}
	}
	return nil, nil
}

func (f *fakeObs) Update(_ context.Context, id int64, o domain.Memory) (*domain.Memory, error) {
	if _, ok := f.items[id]; !ok {
		return nil, nil
	}
	o.ID = id
	f.items[id] = o
	c := o
	return &c, nil
}

func (f *fakeObs) SoftDelete(_ context.Context, id int64) error {
	o, ok := f.items[id]
	if !ok {
		return nil
	}
	t := time.Now().UTC()
	o.DeletedAt = &t
	f.items[id] = o
	return nil
}

func (f *fakeObs) Restore(_ context.Context, id int64) error {
	o, ok := f.items[id]
	if !ok || o.DeletedAt == nil {
		return domain.ErrNotFound
	}
	o.DeletedAt = nil
	f.items[id] = o
	return nil
}

func (f *fakeObs) Recent(_ context.Context, q ports.RecentQuery) ([]domain.Memory, error) {
	// La exclusión por supersede es un JOIN contra memory_relations que el fake no modela; se prueba
	// a nivel del adaptador SQLite. Aquí se honran el resto de filtros.
	var out []domain.Memory
	for _, o := range f.items {
		if o.IsDeleted() {
			continue
		}
		if q.Project != "" && o.Project != q.Project {
			continue
		}
		if q.Scope != "" && o.Scope != q.Scope {
			continue
		}
		if len(q.Types) > 0 && !containsType(q.Types, o.Type) {
			continue
		}
		if q.ExcludeResolved && o.Resolution == domain.ResolutionResolved {
			continue
		}
		if q.Resolution != "" && o.Resolution != q.Resolution {
			continue
		}
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// containsType indica si t está en el conjunto types.
func containsType(types []domain.MemoryType, t domain.MemoryType) bool {
	for _, x := range types {
		if x == t {
			return true
		}
	}
	return false
}

func (f *fakeObs) Search(_ context.Context, q ports.SearchQuery, neighbors []ports.Neighbor) ([]domain.SearchResult, error) {
	var out []domain.SearchResult
	for _, n := range neighbors {
		o, ok := f.items[n.MemoryID]
		if !ok || o.IsDeleted() {
			continue
		}
		if q.Project != "" && o.Project != q.Project {
			continue
		}
		if q.Scope != "" && o.Scope != q.Scope {
			continue
		}
		if q.Type != "" && o.Type != q.Type {
			continue
		}
		out = append(out, domain.SearchResult{Memory: o, Score: n.Score})
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out, nil
}

func (f *fakeObs) FindByHash(_ context.Context, project string, scope domain.Scope, hash string) (*domain.Memory, error) {
	var best *domain.Memory
	for _, o := range f.items {
		if o.IsDeleted() || o.NormalizedHash != hash || o.Project != project || o.Scope != scope {
			continue
		}
		c := o
		if best == nil || c.CreatedAt.After(best.CreatedAt) {
			best = &c
		}
	}
	return best, nil
}

func (f *fakeObs) PendingResolution(_ context.Context, project string, limit int) ([]domain.Memory, error) {
	var out []domain.Memory
	for _, o := range f.items {
		if o.IsDeleted() || (project != "" && o.Project != project) {
			continue
		}
		// La bitácora abierta no es un pendiente por resolver: se excluye (igual que el repo real).
		if o.Type == domain.TypeJournal {
			continue
		}
		if o.Resolution == domain.ResolutionOpen {
			out = append(out, o)
		}
	}
	return out, nil
}

func (f *fakeObs) UncheckedNormatives(_ context.Context, limit int) ([]domain.Memory, error) {
	var out []domain.Memory
	for _, o := range f.items {
		if o.IsDeleted() || o.ConflictChecked || !o.Type.IsNormative() {
			continue
		}
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeObs) UncheckedFactuals(_ context.Context, limit int) ([]domain.Memory, error) {
	var out []domain.Memory
	for _, o := range f.items {
		if o.IsDeleted() || o.ConflictChecked || !o.Type.IsFactual() {
			continue
		}
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeObs) SetConflictChecked(_ context.Context, id int64) error {
	if o, ok := f.items[id]; ok {
		o.ConflictChecked = true
		f.items[id] = o
	}
	return nil
}

func (f *fakeObs) UpdateProject(_ context.Context, oldProject, newProject string) (int64, error) {
	var n int64
	for id, o := range f.items {
		if o.IsDeleted() || o.Project != oldProject {
			continue
		}
		o.Project = newProject
		f.items[id] = o
		n++
	}
	return n, nil
}

func (f *fakeObs) Projects(_ context.Context) ([]string, error) {
	seen := map[string]struct{}{}
	var projects []string
	for _, o := range f.items {
		if o.IsDeleted() || o.Project == "" {
			continue
		}
		if _, ok := seen[o.Project]; ok {
			continue
		}
		seen[o.Project] = struct{}{}
		projects = append(projects, o.Project)
	}
	sort.Strings(projects)
	return projects, nil
}

// fakePrompts es un PromptRepo en memoria.
type fakePrompts struct {
	items  []domain.Prompt
	nextID int64
}

func newFakePrompts() *fakePrompts { return &fakePrompts{} }

func (f *fakePrompts) Add(_ context.Context, p domain.Prompt) (int64, error) {
	f.nextID++
	p.ID = f.nextID
	f.items = append(f.items, p)
	return p.ID, nil
}

func (f *fakePrompts) AddIfMissing(_ context.Context, p domain.Prompt) (int64, bool, error) {
	for _, existing := range f.items {
		if existing.SessionID == p.SessionID && existing.Project == p.Project && existing.Content == p.Content {
			return existing.ID, false, nil
		}
	}
	id, _ := f.Add(context.Background(), p)
	return id, true, nil
}

func (f *fakePrompts) Recent(_ context.Context, project string, limit int) ([]domain.Prompt, error) {
	var out []domain.Prompt
	for _, p := range f.items {
		if project == "" || p.Project == project {
			out = append(out, p)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakePrompts) Delete(_ context.Context, id int64) error {
	for i, p := range f.items {
		if p.ID == id {
			f.items = append(f.items[:i], f.items[i+1:]...)
			return nil
		}
	}
	return nil
}

func (f *fakePrompts) UpdateProject(_ context.Context, oldProject, newProject string) (int64, error) {
	var n int64
	for i := range f.items {
		if f.items[i].Project == oldProject {
			f.items[i].Project = newProject
			n++
		}
	}
	return n, nil
}

// fakeSessions es un SessionRepo en memoria.
type fakeSessions struct {
	items map[string]domain.Session
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{items: map[string]domain.Session{}}
}

func (f *fakeSessions) Create(_ context.Context, s domain.Session) error {
	f.items[s.ID] = s
	return nil
}

func (f *fakeSessions) End(_ context.Context, id string) error {
	s, ok := f.items[id]
	if !ok {
		return nil
	}
	t := time.Now().UTC()
	s.EndedAt = &t
	f.items[id] = s
	return nil
}

func (f *fakeSessions) Get(_ context.Context, id string) (*domain.Session, error) {
	s, ok := f.items[id]
	if !ok {
		return nil, nil
	}
	c := s
	return &c, nil
}

func (f *fakeSessions) Recent(_ context.Context, project string, limit int) ([]domain.Session, error) {
	var out []domain.Session
	for _, s := range f.items {
		if project == "" || s.Project == project {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeSessions) UpdateProject(_ context.Context, oldProject, newProject string) (int64, error) {
	var n int64
	for id, s := range f.items {
		if s.Project == oldProject {
			s.Project = newProject
			f.items[id] = s
			n++
		}
	}
	return n, nil
}

func (f *fakeSessions) MostRecentActive(_ context.Context, project string) (string, bool, error) {
	var best *domain.Session
	for _, s := range f.items {
		if s.Project != project || !s.IsOpen() {
			continue
		}
		c := s
		if best == nil || c.StartedAt.After(best.StartedAt) {
			best = &c
		}
	}
	if best == nil {
		return "", false, nil
	}
	return best.ID, true, nil
}

// fakeEmbedder devuelve un vector constante; basta para probar el cableado del servicio (la
// corrección del coseno/ranking se prueba en el adaptador SQLite). failWith fuerza un error.
type fakeEmbedder struct {
	vec      []float32
	failWith error
	calls    int
}

func newFakeEmbedder() *fakeEmbedder {
	return &fakeEmbedder{vec: []float32{1, 0, 0}}
}

func (e *fakeEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	e.calls++
	if e.failWith != nil {
		return nil, e.failWith
	}
	return e.vec, nil
}
func (e *fakeEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	e.calls++
	if e.failWith != nil {
		return nil, e.failWith
	}
	vectors := make([][]float32, len(texts))
	for i := range texts {
		vectors[i] = e.vec
	}
	return vectors, nil
}
func (e *fakeEmbedder) Dimensions() int { return len(e.vec) }
func (e *fakeEmbedder) Model() string   { return "fake" }

// fakeVectorIndex guarda vectores en memoria. Nearest devuelve los ids guardados (ordenados por
// id, score 1.0): suficiente para probar el cableado de Search; el ranking real se prueba aparte.
type fakeVectorIndex struct {
	vecs map[int64][]float32
}

func newFakeVectorIndex() *fakeVectorIndex {
	return &fakeVectorIndex{vecs: map[int64][]float32{}}
}

func (v *fakeVectorIndex) Upsert(_ context.Context, memoryID int64, vector []float32, _ string) error {
	v.vecs[memoryID] = vector
	return nil
}

func (v *fakeVectorIndex) Nearest(_ context.Context, _ []float32, limit int) ([]ports.Neighbor, error) {
	ids := make([]int64, 0, len(v.vecs))
	for id := range v.vecs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var out []ports.Neighbor
	for _, id := range ids {
		out = append(out, ports.Neighbor{MemoryID: id, Score: 1.0})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (v *fakeVectorIndex) Delete(_ context.Context, memoryID int64) error {
	delete(v.vecs, memoryID)
	return nil
}

// PendingEmbedding no se ejercita en los tests de MemoryService (el backfill tiene sus propios
// dobles en embedding_test.go); aquí basta con cumplir el contrato.
func (v *fakeVectorIndex) PendingEmbedding(_ context.Context, _ string, _ int) ([]int64, error) {
	return nil, nil
}

// fakeScoredIndex es un VectorIndex cuyo Nearest devuelve scores fijos por memoryID (ignora la
// consulta). Sirve para probar el portón por título: modelar que una candidata puntúa BAJO por
// contenido pero ALTO por título.
type fakeScoredIndex struct {
	scores map[int64]float64
}

func (f *fakeScoredIndex) Upsert(_ context.Context, memoryID int64, _ []float32, _ string) error {
	return nil
}

func (f *fakeScoredIndex) Nearest(_ context.Context, _ []float32, limit int) ([]ports.Neighbor, error) {
	ids := make([]int64, 0, len(f.scores))
	for id := range f.scores {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return f.scores[ids[i]] > f.scores[ids[j]] })
	var out []ports.Neighbor
	for _, id := range ids {
		out = append(out, ports.Neighbor{MemoryID: id, Score: f.scores[id]})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeScoredIndex) Delete(_ context.Context, memoryID int64) error {
	delete(f.scores, memoryID)
	return nil
}

func (f *fakeScoredIndex) PendingEmbedding(_ context.Context, _ string, _ int) ([]int64, error) {
	return nil, nil
}

// fakeRelations es un RelationRepo en memoria.
type fakeRelations struct {
	items  []domain.MemoryRelation
	nextID int64
}

func (f *fakeRelations) Insert(_ context.Context, r domain.MemoryRelation) (int64, error) {
	f.nextID++
	r.ID = f.nextID
	f.items = append(f.items, r)
	return r.ID, nil
}

func (f *fakeRelations) Get(_ context.Context, id int64) (*domain.MemoryRelation, error) {
	for i := range f.items {
		if f.items[i].ID == id {
			c := f.items[i]
			return &c, nil
		}
	}
	return nil, nil
}

func (f *fakeRelations) Judge(_ context.Context, id int64, v domain.Verdict) error {
	for i := range f.items {
		if f.items[i].ID == id {
			f.items[i].Kind = v.Relation
			f.items[i].Reason = v.Reasoning
			f.items[i].Confidence = v.Confidence
			f.items[i].Status = domain.JudgmentJudged
			return nil
		}
	}
	return nil
}

func (f *fakeRelations) ListByStatus(_ context.Context, status domain.JudgmentStatus, limit int) ([]domain.MemoryRelation, error) {
	var out []domain.MemoryRelation
	for _, r := range f.items {
		if r.Status == status {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeRelations) FindJudgedPair(_ context.Context, a, b string) (*domain.MemoryRelation, error) {
	for i := range f.items {
		r := f.items[i]
		if r.Status != domain.JudgmentJudged {
			continue
		}
		if (r.SourceID == a && r.TargetID == b) || (r.SourceID == b && r.TargetID == a) {
			c := r
			return &c, nil
		}
	}
	return nil, nil
}

// CountOpenConflicts cuenta relaciones conflicts_with juzgadas. El fake no modela el proyecto de las
// memorias (relaciones referencian sync_ids), así que ignora el filtro; el filtrado por proyecto se
// prueba a nivel del adaptador SQLite.
func (f *fakeRelations) CountOpenConflicts(_ context.Context, _ string) (int, error) {
	n := 0
	for _, r := range f.items {
		if r.Status == domain.JudgmentJudged && r.Kind == domain.RelationConflictsWith {
			n++
		}
	}
	return n, nil
}

// fakeRunner es un AgentRunner que devuelve un veredicto fijo (o un error).
type fakeRunner struct {
	verdict    domain.Verdict
	failWith   error
	lastPrompt string
}

func (f *fakeRunner) Judge(_ context.Context, prompt string) (domain.Verdict, error) {
	f.lastPrompt = prompt
	if f.failWith != nil {
		return domain.Verdict{}, f.failWith
	}
	return f.verdict, nil
}

// Aserciones de que los fakes cumplen los contratos de ports.
var (
	_ ports.Clock        = fixedClock{}
	_ ports.UnitOfWork   = passthroughUoW{}
	_ ports.SyncRepo     = (*fakeSync)(nil)
	_ ports.MemoryRepo   = (*fakeObs)(nil)
	_ ports.PromptRepo   = (*fakePrompts)(nil)
	_ ports.SessionRepo  = (*fakeSessions)(nil)
	_ ports.Embedder     = (*fakeEmbedder)(nil)
	_ ports.VectorIndex  = (*fakeVectorIndex)(nil)
	_ ports.RelationRepo = (*fakeRelations)(nil)
	_ ports.AgentRunner  = (*fakeRunner)(nil)
)
