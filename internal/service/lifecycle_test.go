package service

import (
	"context"
	"testing"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
)

func TestLifecyclePinUnpin(t *testing.T) {
	obs := newFakeObs()
	svc := NewLifecycleService(obs)
	ctx := context.Background()

	id, _ := obs.Add(ctx, domain.Observation{Project: "proj", Title: "t", Content: "c"})

	if err := svc.Pin(ctx, id); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if !obs.items[id].Pinned {
		t.Error("Pin debe fijar la memoria")
	}
	if err := svc.Unpin(ctx, id); err != nil {
		t.Fatalf("Unpin: %v", err)
	}
	if obs.items[id].Pinned {
		t.Error("Unpin debe desfijar la memoria")
	}
}

func TestLifecycleNeedingReview(t *testing.T) {
	obs := newFakeObs()
	svc := NewLifecycleService(obs)
	ctx := context.Background()

	_, _ = obs.Add(ctx, domain.Observation{Project: "proj", Title: "vencida", Content: "c",
		ReviewAfter: time.Now().UTC().Add(-time.Hour)})
	_, _ = obs.Add(ctx, domain.Observation{Project: "proj", Title: "futura", Content: "c",
		ReviewAfter: time.Now().UTC().Add(time.Hour)})
	_, _ = obs.Add(ctx, domain.Observation{Project: "proj", Title: "sin decay", Content: "c"})

	got, err := svc.NeedingReview(ctx, "Proj", 10)
	if err != nil {
		t.Fatalf("NeedingReview: %v", err)
	}
	if len(got) != 1 || got[0].Title != "vencida" {
		t.Errorf("solo la vencida debe necesitar revisión, fue %+v", got)
	}
}
