package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoonzinope/go-job-runner/internal/model"
)

func TestRunRepoCrudHappyPath(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	ctx := context.Background()

	jobID := createTestJobForRun(t, st)
	startedAt := time.Date(2026, 4, 17, 12, 10, 0, 0, time.UTC)
	finishedAt := time.Date(2026, 4, 17, 12, 20, 0, 0, time.UTC)
	exitCode := 7
	errMsg := "failed"
	logPath := "/tmp/run.log"
	resultPath := "/tmp/result.json"
	run := &model.Run{
		JobID:        jobID,
		ScheduledAt:  time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC),
		StartedAt:    &startedAt,
		FinishedAt:   &finishedAt,
		Status:       model.RunStatusRunning,
		Attempt:      1,
		ExitCode:     &exitCode,
		ErrorMessage: &errMsg,
		LogPath:      &logPath,
		ResultPath:   &resultPath,
	}

	id, err := st.Runs.Create(ctx, run)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id <= 0 || run.ID != id {
		t.Fatalf("unexpected run id: %d %d", id, run.ID)
	}

	got, err := st.Runs.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.JobID != jobID || got.Status != model.RunStatusRunning || got.Attempt != 1 {
		t.Fatalf("unexpected run: %+v", got)
	}
	if got.StartedAt == nil || !got.StartedAt.Equal(startedAt) || got.FinishedAt == nil || !got.FinishedAt.Equal(finishedAt) {
		t.Fatalf("time mismatch: %+v", got)
	}
	if got.ExitCode == nil || *got.ExitCode != exitCode || got.ErrorMessage == nil || *got.ErrorMessage != errMsg {
		t.Fatalf("payload mismatch: %+v", got)
	}
	if got.LogPath == nil || *got.LogPath != logPath || got.ResultPath == nil || *got.ResultPath != resultPath {
		t.Fatalf("path mismatch: %+v", got)
	}

	items, total, err := st.Runs.List(ctx, RunFilter{JobID: &jobID}, Page{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("unexpected list result: total=%d len=%d", total, len(items))
	}
	itemsByJob, totalByJob, err := st.Runs.ListByJob(ctx, jobID, Page{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("list by job: %v", err)
	}
	if totalByJob != 1 || len(itemsByJob) != 1 {
		t.Fatalf("unexpected list by job result: total=%d len=%d", totalByJob, len(itemsByJob))
	}

	newStarted := time.Date(2026, 4, 17, 12, 15, 0, 0, time.UTC)
	newFinished := time.Date(2026, 4, 17, 12, 25, 0, 0, time.UTC)
	newErr := "updated"
	newExit := 3
	if err := st.Runs.UpdateStatus(ctx, id, model.RunStatusFailed, &newStarted, &newFinished, &newExit, &newErr); err != nil {
		t.Fatalf("update status: %v", err)
	}
	newLog := "/tmp/updated.log"
	newResult := "/tmp/updated.json"
	if err := st.Runs.UpdateLogArtifacts(ctx, id, &newLog, &newResult); err != nil {
		t.Fatalf("update artifacts: %v", err)
	}

	got, err = st.Runs.Get(ctx, id)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Status != model.RunStatusFailed || got.ExitCode == nil || *got.ExitCode != newExit || got.ErrorMessage == nil || *got.ErrorMessage != newErr {
		t.Fatalf("updated payload mismatch: %+v", got)
	}
	if got.LogPath == nil || *got.LogPath != newLog || got.ResultPath == nil || *got.ResultPath != newResult {
		t.Fatalf("updated paths mismatch: %+v", got)
	}

	if err := st.Runs.Update(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
}

func TestRunRepoValidationFailures(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	ctx := context.Background()

	t.Run("nil create", func(t *testing.T) {
		if _, err := st.Runs.Create(ctx, nil); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("invalid status create", func(t *testing.T) {
		run := testRun(1)
		run.Status = "invalid"
		if _, err := st.Runs.Create(ctx, run); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("nil update", func(t *testing.T) {
		if err := st.Runs.Update(ctx, nil); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("missing id update", func(t *testing.T) {
		run := testRun(1)
		if err := st.Runs.Update(ctx, run); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("invalid status update", func(t *testing.T) {
		run := testRun(1)
		run.ID = 1
		run.Status = "invalid"
		if err := st.Runs.Update(ctx, run); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("invalid status update status fn", func(t *testing.T) {
		if err := st.Runs.UpdateStatus(ctx, 1, "invalid", nil, nil, nil, nil); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestRunRepoPendingAndMissingRows(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	ctx := context.Background()
	jobID := createTestJobForRun(t, st)

	oldest := time.Date(2026, 4, 17, 9, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 4, 17, 10, 0, 0, 0, time.UTC)
	for _, scheduledAt := range []time.Time{newer, oldest} {
		run := testRun(jobID)
		run.ScheduledAt = scheduledAt
		run.Status = model.RunStatusPending
		if _, err := st.Runs.Create(ctx, run); err != nil {
			t.Fatalf("create pending: %v", err)
		}
	}
	other := testRun(jobID)
	other.Status = model.RunStatusRunning
	if _, err := st.Runs.Create(ctx, other); err != nil {
		t.Fatalf("create running: %v", err)
	}

	items, err := st.Runs.ListPending(ctx, 0)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("unexpected pending count: %d", len(items))
	}
	if items[0].ScheduledAt.After(items[1].ScheduledAt) {
		t.Fatal("expected pending runs ordered by scheduled_at asc")
	}

	if _, err := st.Runs.Get(ctx, 9999); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected no rows, got: %v", err)
	}

	if _, err := st.DB.ExecContext(ctx, `
		INSERT INTO runs (
			job_id, scheduled_at, status, attempt, created_at, updated_at
		) VALUES (?, ?, ?, 0, ?, ?)
	`, jobID, "bad-time", string(model.RunStatusPending), encodeTime(time.Now().UTC()), encodeTime(time.Now().UTC())); err != nil {
		t.Fatalf("insert corrupt run: %v", err)
	}
	if _, _, err := st.Runs.List(ctx, RunFilter{}, Page{Page: 1, Size: 50}); err == nil {
		t.Fatal("expected scan error for corrupt run row")
	}
}

func TestRunRepoListFiltersAndEmptyResult(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	ctx := context.Background()
	jobA := createTestJobForRun(t, st)
	jobB := createTestJobForRun(t, st)

	runningAt := time.Date(2026, 4, 17, 9, 0, 0, 0, time.UTC)
	successAt := time.Date(2026, 4, 17, 10, 0, 0, 0, time.UTC)
	failedAt := time.Date(2026, 4, 17, 11, 0, 0, 0, time.UTC)

	createRun := func(jobID int64, status model.RunStatus, scheduled time.Time) {
		run := testRun(jobID)
		run.Status = status
		run.ScheduledAt = scheduled
		if _, err := st.Runs.Create(ctx, run); err != nil {
			t.Fatalf("create run: %v", err)
		}
	}
	createRun(jobA, model.RunStatusPending, runningAt)
	createRun(jobA, model.RunStatusRunning, successAt)
	createRun(jobB, model.RunStatusFailed, failedAt)

	items, total, err := st.Runs.List(ctx, RunFilter{JobID: &jobA}, Page{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("list by job: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("unexpected job filter result: total=%d len=%d", total, len(items))
	}

	status := model.RunStatusFailed
	items, total, err = st.Runs.List(ctx, RunFilter{Status: &status}, Page{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("list by status: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].Status != model.RunStatusFailed {
		t.Fatalf("unexpected status filter result: total=%d items=%+v", total, items)
	}

	from := time.Date(2026, 4, 17, 9, 30, 0, 0, time.UTC)
	to := time.Date(2026, 4, 17, 10, 30, 0, 0, time.UTC)
	items, total, err = st.Runs.List(ctx, RunFilter{From: &from, To: &to}, Page{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("list by window: %v", err)
	}
	if total != 1 || len(items) != 1 || !items[0].ScheduledAt.Equal(successAt) {
		t.Fatalf("unexpected time window result: total=%d items=%+v", total, items)
	}

	noneFrom := time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC)
	items, total, err = st.Runs.List(ctx, RunFilter{From: &noneFrom}, Page{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if total != 0 || len(items) != 0 {
		t.Fatalf("expected empty result, got total=%d len=%d", total, len(items))
	}
}

func TestRunRepoUpdateStatusClearsNullableFields(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	ctx := context.Background()
	jobID := createTestJobForRun(t, st)
	run := testRun(jobID)
	run.Status = model.RunStatusRunning
	started := time.Date(2026, 4, 17, 9, 0, 0, 0, time.UTC)
	finished := time.Date(2026, 4, 17, 9, 5, 0, 0, time.UTC)
	exitCode := 5
	msg := "boom"
	run.StartedAt = &started
	run.FinishedAt = &finished
	run.ExitCode = &exitCode
	run.ErrorMessage = &msg
	id, err := st.Runs.Create(ctx, run)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := st.Runs.UpdateStatus(ctx, id, model.RunStatusCancelled, nil, nil, nil, nil); err != nil {
		t.Fatalf("clear status fields: %v", err)
	}
	got, err := st.Runs.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != model.RunStatusCancelled || got.StartedAt != nil || got.FinishedAt != nil || got.ExitCode != nil || got.ErrorMessage != nil {
		t.Fatalf("expected cleared fields, got: %+v", got)
	}
}

func TestRunRepoUpdateLogArtifactsClearsNullableFields(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	ctx := context.Background()
	jobID := createTestJobForRun(t, st)
	run := testRun(jobID)
	logPath := "/tmp/a.log"
	resultPath := "/tmp/a.json"
	run.LogPath = &logPath
	run.ResultPath = &resultPath
	id, err := st.Runs.Create(ctx, run)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := st.Runs.UpdateLogArtifacts(ctx, id, nil, nil); err != nil {
		t.Fatalf("clear artifacts: %v", err)
	}
	got, err := st.Runs.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.LogPath != nil || got.ResultPath != nil {
		t.Fatalf("expected cleared artifacts, got: %+v", got)
	}
}

func createTestJobForRun(t *testing.T, st *Store) int64 {
	t.Helper()

	id, err := st.Jobs.Create(context.Background(), testJob(uniqueRunJobName(t.Name())))
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	return id
}

var runJobSeq atomic.Int64

func uniqueRunJobName(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UTC().UnixNano(), runJobSeq.Add(1))
}

func testRun(jobID int64) *model.Run {
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	return &model.Run{
		JobID:       jobID,
		ScheduledAt: now,
		Status:      model.RunStatusPending,
		Attempt:     0,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}
