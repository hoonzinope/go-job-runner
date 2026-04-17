package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hoonzinope/go-job-runner/internal/executor"
	"github.com/hoonzinope/go-job-runner/internal/model"
	"github.com/hoonzinope/go-job-runner/internal/store"
)

type flipFlopExecutor struct {
	mu    sync.Mutex
	calls int
}

func (e *flipFlopExecutor) Execute(ctx context.Context, job *model.Job, run *model.Run) (*executor.ExecutionResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.calls == 0 {
		e.calls++
		return nil, errors.New("boom")
	}
	e.calls++
	return &executor.ExecutionResult{ExitCode: 0}, nil
}

type timeoutExecutor struct{}

func (timeoutExecutor) Execute(ctx context.Context, job *model.Job, run *model.Run) (*executor.ExecutionResult, error) {
	<-ctx.Done()
	return &executor.ExecutionResult{ExitCode: -1}, ctx.Err()
}

func TestSchedulerScenarioDueJobDispatchAndSuccess(t *testing.T) {
	t.Parallel()

	st := openSchedulerTestStore(t)
	job := createScheduledJob(t, st, 0, model.ConcurrencyPolicyAllow)

	s := newTestScheduler(st)
	s.setExecutor(successExecutor{result: &executor.ExecutionResult{
		ExitCode:   0,
		LogPath:    "/tmp/run.log",
		ResultPath: "/tmp/result.json",
	}})

	s.processDueJobs(context.Background())

	run := waitForSingleRun(t, st, job.ID, 1*time.Second)
	if run.Status != model.RunStatusPending {
		t.Fatalf("expected pending run after due-job processing, got %s", run.Status)
	}

	s.processDispatch(context.Background())

	updated := waitForRunStatus(t, st, run.ID, model.RunStatusSuccess, 2*time.Second)
	if updated.StartedAt == nil || updated.FinishedAt == nil {
		t.Fatalf("expected started and finished timestamps")
	}
	if updated.LogPath == nil || *updated.LogPath != "/tmp/run.log" {
		t.Fatalf("expected log path to be persisted")
	}
	if updated.ResultPath == nil || *updated.ResultPath != "/tmp/result.json" {
		t.Fatalf("expected result path to be persisted")
	}

	events, err := st.Events.ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	want := []model.RunEventType{
		model.RunEventTypeCreated,
		model.RunEventTypeStarted,
		model.RunEventTypeCompleted,
	}
	if len(events) != len(want) {
		t.Fatalf("expected %d events, got %d", len(want), len(events))
	}
	for i, event := range events {
		if event.EventType != want[i] {
			t.Fatalf("event %d: expected %s, got %s", i, want[i], event.EventType)
		}
	}
}

func TestSchedulerScenarioRetryAfterFailure(t *testing.T) {
	t.Parallel()

	st := openSchedulerTestStore(t)
	job := createScheduledJob(t, st, 1, model.ConcurrencyPolicyAllow)

	s := newTestScheduler(st)
	s.setExecutor(failingExecutor{})

	s.processDueJobs(context.Background())

	run := waitForSingleRun(t, st, job.ID, 1*time.Second)
	if run.Status != model.RunStatusPending {
		t.Fatalf("expected pending run after due-job processing, got %s", run.Status)
	}

	s.processDispatch(context.Background())

	failed := waitForRunStatus(t, st, run.ID, model.RunStatusFailed, 2*time.Second)
	if failed.ExitCode == nil {
		t.Fatalf("expected exit code on failed run")
	}

	pending := waitForPendingRuns(t, st, job.ID, 1, 2*time.Second)
	if len(pending) != 1 {
		t.Fatalf("expected one retry run, got %d", len(pending))
	}
	if pending[0].Attempt != 1 {
		t.Fatalf("expected retry attempt 1, got %d", pending[0].Attempt)
	}
	if !pending[0].ScheduledAt.Equal(run.ScheduledAt) {
		t.Fatalf("expected retry scheduled_at %v, got %v", run.ScheduledAt, pending[0].ScheduledAt)
	}

	events, err := st.Events.ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	want := []model.RunEventType{
		model.RunEventTypeCreated,
		model.RunEventTypeStarted,
		model.RunEventTypeFailed,
	}
	if len(events) != len(want) {
		t.Fatalf("expected %d events, got %d", len(want), len(events))
	}
	for i, event := range events {
		if event.EventType != want[i] {
			t.Fatalf("event %d: expected %s, got %s", i, want[i], event.EventType)
		}
	}
}

func TestSchedulerScenarioForbidConcurrencySkipsDueRun(t *testing.T) {
	t.Parallel()

	st := openSchedulerTestStore(t)
	job := createScheduledJob(t, st, 0, model.ConcurrencyPolicyForbid)
	running := &model.Run{
		JobID:       job.ID,
		ScheduledAt: *job.NextRunAt,
		Status:      model.RunStatusRunning,
		Attempt:     0,
	}
	if _, err := st.Runs.Create(context.Background(), running); err != nil {
		t.Fatalf("create running run: %v", err)
	}

	s := newTestScheduler(st)
	s.processDueJobs(context.Background())

	runs, total, err := st.Runs.List(context.Background(), store.RunFilter{JobID: &job.ID}, store.Page{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if total != 1 || len(runs) != 1 {
		t.Fatalf("expected only existing run, got total=%d len=%d", total, len(runs))
	}

	updatedJob, err := st.Jobs.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if updatedJob.NextRunAt == nil || updatedJob.LastScheduledAt == nil {
		t.Fatalf("expected scheduling fields to be updated")
	}
}

func TestSchedulerScenarioRetryRunEventuallySucceeds(t *testing.T) {
	t.Parallel()

	st := openSchedulerTestStore(t)
	job := createScheduledJob(t, st, 1, model.ConcurrencyPolicyAllow)

	s := newTestScheduler(st)
	s.setExecutor(&flipFlopExecutor{})

	s.processDueJobs(context.Background())

	run := waitForSingleRun(t, st, job.ID, 1*time.Second)
	s.processDispatch(context.Background())

	failed := waitForRunStatus(t, st, run.ID, model.RunStatusFailed, 2*time.Second)
	if failed.ExitCode == nil {
		t.Fatalf("expected exit code on failed run")
	}

	retry := waitForPendingRuns(t, st, job.ID, 1, 2*time.Second)
	if len(retry) != 1 {
		t.Fatalf("expected one retry run, got %d", len(retry))
	}

	s.processDispatch(context.Background())

	retryDone := waitForRunStatus(t, st, retry[0].ID, model.RunStatusSuccess, 2*time.Second)
	if retryDone.StartedAt == nil || retryDone.FinishedAt == nil {
		t.Fatalf("expected retry run to execute fully")
	}

	events, err := st.Events.ListByRun(context.Background(), retry[0].ID)
	if err != nil {
		t.Fatalf("list retry events: %v", err)
	}
	want := []model.RunEventType{
		model.RunEventTypeCreated,
		model.RunEventTypeStarted,
		model.RunEventTypeCompleted,
	}
	if len(events) != len(want) {
		t.Fatalf("expected %d events, got %d", len(want), len(events))
	}
	for i, event := range events {
		if event.EventType != want[i] {
			t.Fatalf("retry event %d: expected %s, got %s", i, want[i], event.EventType)
		}
	}
}

func TestSchedulerScenarioCancelledRunSkipsExecution(t *testing.T) {
	t.Parallel()

	st := openSchedulerTestStore(t)
	job := createScheduledJob(t, st, 0, model.ConcurrencyPolicyAllow)

	s := newTestScheduler(st)
	s.setExecutor(&cancelGuardExecutor{})

	s.processDueJobs(context.Background())

	run := waitForSingleRun(t, st, job.ID, 1*time.Second)
	if err := st.Runs.UpdateStatus(context.Background(), run.ID, model.RunStatusCancelling, nil, nil, nil, nil); err != nil {
		t.Fatalf("mark run cancelling: %v", err)
	}

	s.runWorker(context.Background(), run.ID)

	updated := waitForRunStatus(t, st, run.ID, model.RunStatusCancelled, 2*time.Second)
	if updated.FinishedAt == nil {
		t.Fatalf("expected cancelled run to have finished_at")
	}

	events, err := st.Events.ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	want := []model.RunEventType{
		model.RunEventTypeCreated,
		model.RunEventTypeCancelled,
	}
	if len(events) != len(want) {
		t.Fatalf("expected %d events, got %d", len(want), len(events))
	}
	for i, event := range events {
		if event.EventType != want[i] {
			t.Fatalf("event %d: expected %s, got %s", i, want[i], event.EventType)
		}
	}
}

func TestSchedulerScenarioTimeoutRunTransitionsToTimeout(t *testing.T) {
	t.Parallel()

	st := openSchedulerTestStore(t)
	job := createScheduledJob(t, st, 0, model.ConcurrencyPolicyAllow)
	job.TimeoutSec = 1
	if err := st.Jobs.Update(context.Background(), job); err != nil {
		t.Fatalf("update job timeout: %v", err)
	}

	s := newTestScheduler(st)
	s.setExecutor(timeoutExecutor{})

	s.processDueJobs(context.Background())

	run := waitForSingleRun(t, st, job.ID, 1*time.Second)
	s.processDispatch(context.Background())

	updated := waitForRunStatus(t, st, run.ID, model.RunStatusTimeout, 3*time.Second)
	if updated.FinishedAt == nil {
		t.Fatalf("expected timed out run to have finished_at")
	}
	if updated.ExitCode == nil {
		t.Fatalf("expected timeout exit code")
	}

	events, err := st.Events.ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	want := []model.RunEventType{
		model.RunEventTypeCreated,
		model.RunEventTypeStarted,
		model.RunEventTypeTimeout,
	}
	if len(events) != len(want) {
		t.Fatalf("expected %d events, got %d", len(want), len(events))
	}
	for i, event := range events {
		if event.EventType != want[i] {
			t.Fatalf("event %d: expected %s, got %s", i, want[i], event.EventType)
		}
	}
}

func waitForSingleRun(t *testing.T, st *store.Store, jobID int64, timeout time.Duration) *model.Run {
	t.Helper()
	runs := waitForRuns(t, st, jobID, 1, timeout)
	return &runs[0]
}

func waitForPendingRuns(t *testing.T, st *store.Store, jobID int64, count int, timeout time.Duration) []model.Run {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		runs, total, err := st.Runs.List(context.Background(), store.RunFilter{JobID: &jobID, Status: runStatusPtr(model.RunStatusPending)}, store.Page{Page: 1, Size: 50})
		if err == nil && int(total) == count && len(runs) == count {
			return runs
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d pending runs for job %d", count, jobID)
	return nil
}

func waitForRunStatus(t *testing.T, st *store.Store, runID int64, want model.RunStatus, timeout time.Duration) *model.Run {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		run, err := st.Runs.Get(context.Background(), runID)
		if err == nil && run.Status == want {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for run %d to reach %s", runID, want)
	return nil
}

func waitForRuns(t *testing.T, st *store.Store, jobID int64, count int, timeout time.Duration) []model.Run {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		runs, total, err := st.Runs.List(context.Background(), store.RunFilter{JobID: &jobID}, store.Page{Page: 1, Size: 50})
		if err == nil && int(total) == count && len(runs) == count {
			return runs
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d runs for job %d", count, jobID)
	return nil
}
