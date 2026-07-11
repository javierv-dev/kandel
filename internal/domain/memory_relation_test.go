package domain

import "testing"

func TestRelationTypeValid(t *testing.T) {
	valid := []RelationType{
		RelationPending, RelationRelated, RelationCompatible, RelationScoped,
		RelationConflictsWith, RelationSupersedes, RelationDuplicate, RelationNotConflict,
		RelationContinues,
	}
	for _, k := range valid {
		if !k.Valid() {
			t.Errorf("%q debería ser válido", k)
		}
	}
	if RelationType("inventado").Valid() {
		t.Error("\"inventado\" no debería ser válido")
	}
}

func TestJudgmentStatusValid(t *testing.T) {
	valid := []JudgmentStatus{
		JudgmentPending, JudgmentJudged, JudgmentOrphaned, JudgmentIgnored,
	}
	for _, s := range valid {
		if !s.Valid() {
			t.Errorf("%q debería ser válido", s)
		}
	}
	if JudgmentStatus("inventado").Valid() {
		t.Error("\"inventado\" no debería ser válido")
	}
}

func TestRelationIsConflict(t *testing.T) {
	tests := []struct {
		name   string
		kind   RelationType
		status JudgmentStatus
		want   bool
	}{
		{"juzgado y en conflicto", RelationConflictsWith, JudgmentJudged, true},
		{"en conflicto pero sin juzgar", RelationConflictsWith, JudgmentPending, false},
		{"juzgado pero no es conflicto", RelationSupersedes, JudgmentJudged, false},
		{"placeholder pendiente", RelationPending, JudgmentPending, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := MemoryRelation{Kind: tc.kind, Status: tc.status}
			if got := r.IsConflict(); got != tc.want {
				t.Fatalf("IsConflict = %v, se esperaba %v", got, tc.want)
			}
		})
	}
}
