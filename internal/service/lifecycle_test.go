package service

import (
	"context"
	"testing"

	"github.com/javierv-dev/kandel/internal/domain"
)

func TestLifecyclePendingResolution(t *testing.T) {
	obs := newFakeObs()
	svc := NewLifecycleService(obs)
	ctx := context.Background()

	_, _ = obs.Add(ctx, domain.Memory{Project: "proj", Title: "pendiente", Content: "c",
		Resolution: domain.ResolutionOpen})
	_, _ = obs.Add(ctx, domain.Memory{Project: "proj", Title: "resuelta", Content: "c",
		Resolution: domain.ResolutionResolved})
	_, _ = obs.Add(ctx, domain.Memory{Project: "proj", Title: "normal", Content: "c"})

	got, err := svc.PendingResolution(ctx, "Proj", 10)
	if err != nil {
		t.Fatalf("PendingResolution: %v", err)
	}
	if len(got) != 1 || got[0].Title != "pendiente" {
		t.Errorf("solo la pendiente (open) debe aparecer, fue %+v", got)
	}
}
