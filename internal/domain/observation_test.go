package domain

import (
	"errors"
	"testing"
	"time"
)

func baseObservation() Observation {
	return Observation{
		Title:   "Usamos Clean Architecture",
		Content: "What: capas. Why: testeable. Where: internal/.",
		Type:    TypeDecision,
		Scope:   ScopeProject,
	}
}

func TestObservationState(t *testing.T) {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		reviewAfter time.Time
		want        LifecycleState
	}{
		{"sin fecha está activa", time.Time{}, StateActive},
		{"fecha futura está activa", now.AddDate(0, 1, 0), StateActive},
		{"fecha pasada necesita revisión", now.AddDate(0, -1, 0), StateNeedsReview},
		{"fecha justo ahora no necesita revisión todavía", now, StateActive},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := baseObservation()
			o.ReviewAfter = tc.reviewAfter
			if got := o.State(now); got != tc.want {
				t.Fatalf("State = %q, se esperaba %q", got, tc.want)
			}
		})
	}
}

func TestReviewAfterFor(t *testing.T) {
	from := time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)

	got := ReviewAfterFor(TypeDecision, from)
	want := from.AddDate(0, 6, 0)
	if !got.Equal(want) {
		t.Errorf("decision: ReviewAfterFor = %v, se esperaba %v", got, want)
	}

	// Los tipos sin política de decaimiento devuelven el tiempo cero.
	if got := ReviewAfterFor(TypeLearning, from); !got.IsZero() {
		t.Errorf("learning no debería decaer, se obtuvo %v", got)
	}
}

func TestObservationValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Observation)
		wantErr error
	}{
		{"válida", func(*Observation) {}, nil},
		{"título vacío", func(o *Observation) { o.Title = "  " }, ErrEmptyTitle},
		{"contenido vacío", func(o *Observation) { o.Content = "" }, ErrEmptyContent},
		{"tipo inválido", func(o *Observation) { o.Type = "otro" }, ErrInvalidType},
		{"scope inválido", func(o *Observation) { o.Scope = "equipo" }, ErrInvalidScope},
		{"topic_key válido", func(o *Observation) { o.TopicKey = "architecture/auth" }, nil},
		{"topic_key inválido", func(o *Observation) { o.TopicKey = "MAL/Formato" }, ErrInvalidTopicKey},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := baseObservation()
			tc.mutate(&o)
			err := o.Validate()
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Validate = %v, se esperaba %v", err, tc.wantErr)
			}
		})
	}
}

func TestObservationIsDeleted(t *testing.T) {
	o := baseObservation()
	if o.IsDeleted() {
		t.Error("una observación sin DeletedAt no está borrada")
	}
	ts := time.Now()
	o.DeletedAt = &ts
	if !o.IsDeleted() {
		t.Error("una observación con DeletedAt está borrada")
	}
}
