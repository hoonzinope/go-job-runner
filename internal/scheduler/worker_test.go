package scheduler

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hoonzinope/go-job-runner/internal/executor"
	"github.com/hoonzinope/go-job-runner/internal/model"
	"github.com/hoonzinope/go-job-runner/internal/store"
)

type failingExecutor struct{}

func (failingExecutor) Execute(ctx context.Context, job *model.Job, run *model.Run) (*executor.ExecutionResult, error) {
	return nil, errors.New("boom")
}

func TestRunWorkerSchedulesRetryOnFailure(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	defer st.Close()

	job := createTestJob(t, st, 1)
	run := createTestRun(t, st, job.ID)

	s := &Scheduler{
		store:          st,
		executor:       failingExecutor{},
		dueWakeup:      make(chan struct{}, 1),
		dispatchWakeup: make(chan struct{}, 1),
	}

	s.runWorker(context.Background(), run.ID)

	updatedRun, err := st.Runs.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get original run: %v", err)
	}
	if updatedRun.Status != model.RunStatusFailed {
		t.Fatalf("expected original run to fail, got %s", updatedRun.Status)
	}

	pending, err := st.Runs.ListPending(context.Background(), 10)
	if err != nil {
		t.Fatalf("list pending runs: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 retry run, got %d", len(pending))
	}
	if pending[0].JobID != job.ID {
		t.Fatalf("expected retry run job id %d, got %d", job.ID, pending[0].JobID)
	}
	if pending[0].Attempt != 1 {
		t.Fatalf("expected retry attempt 1, got %d", pending[0].Attempt)
	}
	if !pending[0].ScheduledAt.Equal(run.ScheduledAt) {
		t.Fatalf("expected retry scheduled_at %v, got %v", run.ScheduledAt, pending[0].ScheduledAt)
	}
}

func TestRunWorkerDoesNotScheduleRetryWhenLimitIsZero(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	defer st.Close()

	job := createTestJob(t, st, 0)
	run := createTestRun(t, st, job.ID)

	s := &Scheduler{
		store:          st,
		executor:       failingExecutor{},
		dueWakeup:      make(chan struct{}, 1),
		dispatchWakeup: make(chan struct{}, 1),
	}

	s.runWorker(context.Background(), run.ID)

	pending, err := st.Runs.ListPending(context.Background(), 10)
	if err != nil {
		t.Fatalf("list pending runs: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected no retry run, got %d", len(pending))
	}
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return st
}

func createTestJob(t *testing.T, st *store.Store, retryLimit int) *model.Job {
	t.Helper()
	interval := 60
	job := &model.Job{
		Name:              "job-" + t.Name(),
		Enabled:           true,
		SourceType:        model.JobSourceTypeLocal,
		ImageRef:          "alpine:latest",
		ScheduleType:      model.ScheduleTypeInterval,
		IntervalSec:       &interval,
		Timezone:          "UTC",
		ConcurrencyPolicy: model.ConcurrencyPolicyAllow,
		TimeoutSec:        0,
		RetryLimit:        retryLimit,
		NextRunAt:         nil,
	}
	if _, err := st.Jobs.Create(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	return job
}

func createTestRun(t *testing.T, st *store.Store, jobID int64) *model.Run {
	t.Helper()
	scheduledAt := time.Now().UTC().Add(-time.Minute)
	run := &model.Run{
		JobID:       jobID,
		ScheduledAt: scheduledAt,
		Status:      model.RunStatusPending,
		Attempt:     0,
	}
	if _, err := st.Runs.Create(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	return run
}
