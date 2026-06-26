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
func (f *fakeSync) Ack(context.Context, string, []int64) error        { return nil }
func (f *fakeSync) RecordChunk(context.Context, string, string) error { return nil }
func (f *fakeSync) SyncedChunks(context.Context, string) (map[string]bool, error) {
	return map[string]bool{}, nil
}
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

func (f *fakeObs) HardDelete(_ context.Context, id int64) error {
	delete(f.items, id)
	return nil
}

func (f *fakeObs) Recent(_ context.Context, q ports.RecentQuery) ([]domain.Memory, error) {
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
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (f *fakeObs) Search(context.Context, ports.SearchQuery, []ports.Neighbor) ([]domain.SearchResult, error) {
	return nil, nil
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

func (f *fakeObs) LatestByTopicKey(_ context.Context, project string, scope domain.Scope, key domain.TopicKey) (*domain.Memory, error) {
	var best *domain.Memory
	for _, o := range f.items {
		if o.IsDeleted() || o.TopicKey != key || o.Project != project || o.Scope != scope {
			continue
		}
		c := o
		if best == nil || c.UpdatedAt.After(best.UpdatedAt) {
			best = &c
		}
	}
	return best, nil
}

func (f *fakeObs) SetPinned(_ context.Context, id int64, pinned bool) error {
	o, ok := f.items[id]
	if !ok {
		return nil
	}
	o.Pinned = pinned
	f.items[id] = o
	return nil
}

func (f *fakeObs) NeedingReview(_ context.Context, project string, limit int) ([]domain.Memory, error) {
	now := time.Now().UTC()
	var out []domain.Memory
	for _, o := range f.items {
		if o.IsDeleted() || (project != "" && o.Project != project) {
			continue
		}
		if !o.ReviewAfter.IsZero() && o.ReviewAfter.Before(now) {
			out = append(out, o)
		}
	}
	return out, nil
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

func (f *fakeSessions) End(_ context.Context, id, summary string) error {
	s, ok := f.items[id]
	if !ok {
		return nil
	}
	t := time.Now().UTC()
	s.EndedAt = &t
	s.Summary = summary
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

// Aserciones de que los fakes cumplen los contratos de ports.
var (
	_ ports.Clock       = fixedClock{}
	_ ports.UnitOfWork  = passthroughUoW{}
	_ ports.SyncRepo    = (*fakeSync)(nil)
	_ ports.MemoryRepo  = (*fakeObs)(nil)
	_ ports.SessionRepo = (*fakeSessions)(nil)
)
