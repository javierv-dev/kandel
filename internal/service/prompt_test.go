package service

import (
	"context"
	"testing"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
)

func newPromptService(prompts *fakePrompts, sync *fakeSync) *PromptService {
	return NewPromptService(prompts, sync, passthroughUoW{}, fixedClock{now: time.Unix(1700000000, 0).UTC()})
}

func TestPromptSaveInsertsAndEnqueues(t *testing.T) {
	prompts := newFakePrompts()
	sync := &fakeSync{}
	svc := newPromptService(prompts, sync)

	res, err := svc.Save(context.Background(), SavePromptInput{
		SessionID: "s1", Project: "Kandel", Content: "  arregla el login  ",
	})
	if err != nil {
		t.Fatalf("Save error: %v", err)
	}
	if !res.Inserted {
		t.Error("el primer guardado debe insertar")
	}
	if res.Prompt.Content != "arregla el login" {
		t.Errorf("contenido sin recortar: %q", res.Prompt.Content)
	}
	if res.Prompt.Project != "kandel" {
		t.Errorf("proyecto sin normalizar: %q", res.Prompt.Project)
	}
	if len(sync.enqueued) != 1 || sync.enqueued[0].Entity != domain.SyncEntityPrompt {
		t.Errorf("debe encolar una mutación de prompt: %+v", sync.enqueued)
	}
}

func TestPromptSaveIdempotent(t *testing.T) {
	prompts := newFakePrompts()
	sync := &fakeSync{}
	svc := newPromptService(prompts, sync)

	in := SavePromptInput{SessionID: "s1", Project: "kandel", Content: "lo mismo"}
	if _, err := svc.Save(context.Background(), in); err != nil {
		t.Fatalf("primer Save: %v", err)
	}
	res, err := svc.Save(context.Background(), in)
	if err != nil {
		t.Fatalf("segundo Save: %v", err)
	}
	if res.Inserted {
		t.Error("un prompt idéntico no debe reinsertarse")
	}
	if len(prompts.items) != 1 {
		t.Errorf("no debe haber duplicados, hay %d", len(prompts.items))
	}
	if len(sync.enqueued) != 1 {
		t.Errorf("el duplicado no debe encolar otra mutación: %d", len(sync.enqueued))
	}
}

func TestPromptSaveEmptyFails(t *testing.T) {
	svc := newPromptService(newFakePrompts(), &fakeSync{})
	_, err := svc.Save(context.Background(), SavePromptInput{SessionID: "s1", Content: "   "})
	if err != domain.ErrEmptyContent {
		t.Fatalf("se esperaba ErrEmptyContent, se obtuvo %v", err)
	}
}
