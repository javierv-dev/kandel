package domain

import (
	"errors"
	"testing"
	"time"
)

func baseMemory() Memory {
	return Memory{
		Title:   "Usamos Clean Architecture",
		Content: "What: capas. Why: testeable. Where: internal/.",
		Type:    TypeDecision,
		Scope:   ScopeProject,
	}
}

func TestMemoryIsPending(t *testing.T) {
	tests := []struct {
		name       string
		resolution Resolution
		want       bool
	}{
		{"open es un pendiente", ResolutionOpen, true},
		{"vacío no es un pendiente", ResolutionNone, false},
		{"resolved no es un pendiente", ResolutionResolved, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := baseMemory()
			o.Resolution = tc.resolution
			if got := o.IsPending(); got != tc.want {
				t.Fatalf("IsPending = %v, se esperaba %v", got, tc.want)
			}
		})
	}
}

func TestMemoryTypeIsNormative(t *testing.T) {
	tests := []struct {
		typ  MemoryType
		want bool
	}{
		{TypeDecision, true},
		{TypePreference, true},
		{TypeArchitecture, false},
		{TypeBugfix, false},
		{TypeDiscovery, false},
	}
	for _, tc := range tests {
		if got := tc.typ.IsNormative(); got != tc.want {
			t.Errorf("%q.IsNormative() = %v, se esperaba %v", tc.typ, got, tc.want)
		}
	}
}

func TestMemoryValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Memory)
		wantErr error
	}{
		{"válida", func(*Memory) {}, nil},
		{"título vacío", func(o *Memory) { o.Title = "  " }, ErrEmptyTitle},
		{"contenido vacío", func(o *Memory) { o.Content = "" }, ErrEmptyContent},
		{"tipo inválido", func(o *Memory) { o.Type = "otro" }, ErrInvalidType},
		{"scope inválido", func(o *Memory) { o.Scope = "equipo" }, ErrInvalidScope},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := baseMemory()
			tc.mutate(&o)
			err := o.Validate()
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Validate = %v, se esperaba %v", err, tc.wantErr)
			}
		})
	}
}

func TestMemoryIsDeleted(t *testing.T) {
	o := baseMemory()
	if o.IsDeleted() {
		t.Error("una memoria sin DeletedAt no está borrada")
	}
	ts := time.Now()
	o.DeletedAt = &ts
	if !o.IsDeleted() {
		t.Error("una memoria con DeletedAt está borrada")
	}
}
