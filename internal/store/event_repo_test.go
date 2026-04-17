package store

import (
	"context"
	"testing"
	"time"

	"github.com/hoonzinope/go-job-runner/internal/model"
)

func TestEventRepoCrudHappyPath(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	ctx := context.Background()
	jobID := createTestJobForRun(t, st)
	run := testRun(jobID)
	run.Status = model.RunStatusRunning
	runID, err := st.Runs.Create(ctx, run)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	msg1 := "created"
	msg2 := "started"
	events := []*model.RunEvent{
		{RunID: runID, EventType: model.RunEventTypeCreated, Message: &msg1},
		{RunID: runID, EventType: model.RunEventTypeStarted, Message: &msg2},
	}
	for _, event := range events {
		if _, err := st.Events.Create(ctx, event); err != nil {
			t.Fatalf("create event: %v", err)
		}
	}

	got, err := st.Events.ListByRun(ctx, runID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("unexpected event count: %d", len(got))
	}
	if got[0].EventType != model.RunEventTypeCreated || got[1].EventType != model.RunEventTypeStarted {
		t.Fatalf("unexpected order: %+v", got)
	}
	if got[0].Message == nil || *got[0].Message != msg1 || got[1].Message == nil || *got[1].Message != msg2 {
		t.Fatalf("message mismatch: %+v", got)
	}
}

func TestEventRepoValidationAndFkFailures(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	ctx := context.Background()

	t.Run("nil", func(t *testing.T) {
		if _, err := st.Events.Create(ctx, nil); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("invalid type", func(t *testing.T) {
		event := &model.RunEvent{RunID: 1, EventType: "invalid"}
		if _, err := st.Events.Create(ctx, event); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("fk missing run", func(t *testing.T) {
		event := &model.RunEvent{RunID: 9999, EventType: model.RunEventTypeCreated, CreatedAt: time.Now().UTC()}
		if _, err := st.Events.Create(ctx, event); err == nil {
			t.Fatal("expected fk error")
		}
	})
}

func TestEventRepoListMissingRun(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	ctx := context.Background()

	items, err := st.Events.ListByRun(ctx, 9999)
	if err != nil {
		t.Fatalf("list missing run should not error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no events, got %d", len(items))
	}
}
