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

type successExecutor struct {
	result *executor.ExecutionResult
}

func (e successExecutor) Execute(ctx context.Context, job *model.Job, run *model.Run) (*executor.ExecutionResult, error) {
	if e.result == nil {
		return &executor.ExecutionResult{ExitCode: 0}, nil
	}
	return e.result, nil
}

type cancelGuardExecutor struct {
	called bool
}

func (e *cancelGuardExecutor) Execute(ctx context.Context, job *model.Job, run *model.Run) (*executor.ExecutionResult, error) {
	e.called = true
	return nil, errors.New("executor should not be called")
}

func TestComputeNextRunAtInterval(t *testing.T) {
	t.Parallel()

	interval := 90
	from := time.Date(2026, time.April, 17, 12, 0, 0, 0, time.UTC)

	next, err := computeNextRunAt(&model.Job{
		ScheduleType: model.ScheduleTypeInterval,
		IntervalSec:  &interval,
		Timezone:     "UTC",
	}, from)
	if err != nil {
		t.Fatalf("compute next run at: %v", err)
	}

	want := from.Add(90 * time.Second)
	if !next.Equal(want) {
		t.Fatalf("expected %v, got %v", want, next)
	}
}

func TestComputeNextRunAtCron(t *testing.T) {
	t.Parallel()

	expr := "*/5 * * * *"
	from := time.Date(2026, time.April, 17, 12, 2, 0, 0, time.UTC)

	next, err := computeNextRunAt(&model.Job{
		ScheduleType: model.ScheduleTypeCron,
		ScheduleExpr: &expr,
		Timezone:     "UTC",
	}, from)
	if err != nil {
		t.Fatalf("compute next run at: %v", err)
	}

	want := time.Date(2026, time.April, 17, 12, 5, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("expected %v, got %v", want, next)
	}
}

func TestProcessDueJobCreatesRunAndAdvancesSchedule(t *testing.T) {
	t.Parallel()

	st := openSchedulerTestStore(t)
	job := createScheduledJob(t, st, 0, model.ConcurrencyPolicyAllow)
	now := time.Date(2026, time.April, 17, 12, 0, 0, 0, time.UTC)

	s := newTestScheduler(st)
	if err := s.processDueJob(context.Background(), job, now); err != nil {
		t.Fatalf("process due job: %v", err)
	}

	updatedJob, err := st.Jobs.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if updatedJob.NextRunAt == nil {
		t.Fatalf("expected next run time to be set")
	}
	wantNext := now.Add(90 * time.Second)
	if !updatedJob.NextRunAt.Equal(wantNext) {
		t.Fatalf("expected next run %v, got %v", wantNext, updatedJob.NextRunAt)
	}
	if updatedJob.LastScheduledAt == nil || !updatedJob.LastScheduledAt.Equal(*job.NextRunAt) {
		t.Fatalf("expected last scheduled at %v, got %v", job.NextRunAt, updatedJob.LastScheduledAt)
	}

	runs, total, err := st.Runs.List(context.Background(), store.RunFilter{JobID: &job.ID}, store.Page{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if total != 1 || len(runs) != 1 {
		t.Fatalf("expected 1 run, got total=%d len=%d", total, len(runs))
	}
	if runs[0].Attempt != 0 || runs[0].Status != model.RunStatusPending {
		t.Fatalf("unexpected run state: attempt=%d status=%s", runs[0].Attempt, runs[0].Status)
	}

	events, err := st.Events.ListByRun(context.Background(), runs[0].ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 || events[0].EventType != model.RunEventTypeCreated {
		t.Fatalf("expected created event, got %+v", events)
	}
}

func TestProcessDueJobSkipsRunningJobAndRecordsEvent(t *testing.T) {
	t.Parallel()

	st := openSchedulerTestStore(t)
	job := createScheduledJob(t, st, 0, model.ConcurrencyPolicyForbid)
	running := &model.Run{
		JobID:       job.ID,
		ScheduledAt: (*job.NextRunAt).Add(-90 * time.Second),
		Status:      model.RunStatusRunning,
		Attempt:     0,
	}
	if _, err := st.Runs.Create(context.Background(), running); err != nil {
		t.Fatalf("create running run: %v", err)
	}

	s := newTestScheduler(st)
	now := time.Date(2026, time.April, 17, 12, 0, 0, 0, time.UTC)
	if err := s.processDueJob(context.Background(), job, now); err != nil {
		t.Fatalf("process due job: %v", err)
	}

	runs, total, err := st.Runs.List(context.Background(), store.RunFilter{JobID: &job.ID}, store.Page{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if total != 2 || len(runs) != 2 {
		t.Fatalf("expected running run and skipped run, got total=%d len=%d", total, len(runs))
	}

	var skipped *model.Run
	for i := range runs {
		if runs[i].Status == model.RunStatusSkipped {
			skipped = &runs[i]
			break
		}
	}
	if skipped == nil {
		t.Fatalf("expected skipped run in recent runs: %+v", runs)
	}
	if skipped.FinishedAt == nil || skipped.ErrorMessage == nil || *skipped.ErrorMessage == "" {
		t.Fatalf("expected skipped run to carry finish time and reason: %+v", skipped)
	}

	events, err := st.Events.ListByRun(context.Background(), skipped.ID)
	if err != nil {
		t.Fatalf("list skipped run events: %v", err)
	}
	if len(events) != 1 || events[0].EventType != model.RunEventTypeSkipped {
		t.Fatalf("expected skipped event on skipped run, got %+v", events)
	}
	if events[0].Message == nil || *events[0].Message == "" {
		t.Fatalf("expected skipped event message, got %+v", events[0])
	}
}

func TestProcessDueJobSkipsRunWhenRunningAndConcurrencyForbid(t *testing.T) {
	t.Parallel()

	st := openSchedulerTestStore(t)
	job := createScheduledJob(t, st, 0, model.ConcurrencyPolicyForbid)
	running := &model.Run{
		JobID:       job.ID,
		ScheduledAt: (*job.NextRunAt).Add(-90 * time.Second),
		Status:      model.RunStatusRunning,
		Attempt:     0,
	}
	if _, err := st.Runs.Create(context.Background(), running); err != nil {
		t.Fatalf("create running run: %v", err)
	}

	s := newTestScheduler(st)
	if err := s.processDueJob(context.Background(), job, time.Date(2026, time.April, 17, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("process due job: %v", err)
	}

	runs, total, err := st.Runs.List(context.Background(), store.RunFilter{JobID: &job.ID}, store.Page{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if total != 2 || len(runs) != 2 {
		t.Fatalf("expected running run and skipped run, got total=%d len=%d", total, len(runs))
	}
	if runs[0].Status != model.RunStatusSkipped && runs[1].Status != model.RunStatusSkipped {
		t.Fatalf("expected skipped run in recent runs, got %+v", runs)
	}

	updatedJob, err := st.Jobs.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if updatedJob.NextRunAt == nil || updatedJob.LastScheduledAt == nil {
		t.Fatalf("expected scheduling fields to be updated")
	}
}

func TestProcessDueJobReclaimsStaleRunningRun(t *testing.T) {
	t.Parallel()

	st := openSchedulerTestStore(t)
	job := createScheduledJob(t, st, 0, model.ConcurrencyPolicyForbid)
	now := time.Date(2026, time.April, 17, 12, 0, 0, 0, time.UTC)
	staleStarted := now.Add(-2 * time.Hour)
	running := &model.Run{
		JobID:       job.ID,
		ScheduledAt: now.Add(-time.Minute),
		StartedAt:   &staleStarted,
		Status:      model.RunStatusRunning,
		Attempt:     0,
	}
	if _, err := st.Runs.Create(context.Background(), running); err != nil {
		t.Fatalf("create running run: %v", err)
	}

	s := newTestScheduler(st)
	if err := s.processDueJob(context.Background(), job, now); err != nil {
		t.Fatalf("process due job: %v", err)
	}

	runs, total, err := st.Runs.List(context.Background(), store.RunFilter{JobID: &job.ID}, store.Page{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if total != 2 || len(runs) != 2 {
		t.Fatalf("expected stale timeout and new pending run, got total=%d len=%d", total, len(runs))
	}

	var timedOut *model.Run
	var pending *model.Run
	for i := range runs {
		switch runs[i].Status {
		case model.RunStatusTimeout:
			timedOut = &runs[i]
		case model.RunStatusPending:
			pending = &runs[i]
		}
	}
	if timedOut == nil || pending == nil {
		t.Fatalf("expected timeout and pending runs, got %+v", runs)
	}
	if timedOut.FinishedAt == nil || timedOut.ErrorMessage == nil || *timedOut.ErrorMessage == "" {
		t.Fatalf("expected timed out run to be finalized, got %+v", timedOut)
	}
	if pending.Attempt != 0 {
		t.Fatalf("expected fresh pending run attempt 0, got %+v", pending)
	}

	events, err := st.Events.ListByRun(context.Background(), timedOut.ID)
	if err != nil {
		t.Fatalf("list timed out run events: %v", err)
	}
	if len(events) != 1 || events[0].EventType != model.RunEventTypeTimeout {
		t.Fatalf("expected timeout event on stale run, got %+v", events)
	}
}

func TestRunWorkerTransitionsSuccess(t *testing.T) {
	t.Parallel()

	st := openSchedulerTestStore(t)
	job := createTestJob(t, st, 0)
	run := createTestRun(t, st, job.ID)

	s := newTestScheduler(st)
	s.setExecutor(successExecutor{result: &executor.ExecutionResult{
		ExitCode:   0,
		LogPath:    "/tmp/run.log",
		ResultPath: "/tmp/result.json",
	}})

	s.runWorker(context.Background(), run.ID)

	updatedRun, err := st.Runs.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if updatedRun.Status != model.RunStatusSuccess {
		t.Fatalf("expected success, got %s", updatedRun.Status)
	}
	if updatedRun.StartedAt == nil || updatedRun.FinishedAt == nil {
		t.Fatalf("expected started and finished timestamps")
	}
	if updatedRun.LogPath == nil || *updatedRun.LogPath != "/tmp/run.log" {
		t.Fatalf("expected log path to be persisted")
	}
	if updatedRun.ResultPath == nil || *updatedRun.ResultPath != "/tmp/result.json" {
		t.Fatalf("expected result path to be persisted")
	}

	events, err := st.Events.ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected started + completed events, got %d", len(events))
	}
	if events[0].EventType != model.RunEventTypeStarted || events[1].EventType != model.RunEventTypeCompleted {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestRunWorkerSkipsAlreadyClaimedRun(t *testing.T) {
	t.Parallel()

	st := openSchedulerTestStore(t)
	job := createTestJob(t, st, 0)
	run := createTestRun(t, st, job.ID)

	startedAt := time.Date(2026, time.April, 17, 12, 5, 0, 0, time.UTC)
	ok, err := st.Runs.ClaimPending(context.Background(), run.ID, startedAt)
	if err != nil {
		t.Fatalf("claim run: %v", err)
	}
	if !ok {
		t.Fatal("expected claim to succeed")
	}

	execGuard := &cancelGuardExecutor{}
	s := newTestScheduler(st)
	s.setExecutor(execGuard)

	s.runWorker(context.Background(), run.ID)

	if execGuard.called {
		t.Fatal("executor should not be called for already claimed run")
	}

	updatedRun, err := st.Runs.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if updatedRun.Status != model.RunStatusRunning {
		t.Fatalf("expected run to stay running, got %s", updatedRun.Status)
	}
	if updatedRun.StartedAt == nil || !updatedRun.StartedAt.Equal(startedAt) {
		t.Fatalf("unexpected started_at: %+v", updatedRun.StartedAt)
	}

	events, err := st.Events.ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no events from skipped worker, got %+v", events)
	}
}

func TestRunWorkerTransitionsCancelledBeforeExecution(t *testing.T) {
	t.Parallel()

	st := openSchedulerTestStore(t)
	job := createTestJob(t, st, 0)
	run := createTestRunWithStatus(t, st, job.ID, model.RunStatusCancelling)

	execGuard := &cancelGuardExecutor{}
	s := newTestScheduler(st)
	s.setExecutor(execGuard)

	s.runWorker(context.Background(), run.ID)

	if execGuard.called {
		t.Fatalf("executor should not be called for cancelling run")
	}

	updatedRun, err := st.Runs.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if updatedRun.Status != model.RunStatusCancelled {
		t.Fatalf("expected cancelled, got %s", updatedRun.Status)
	}

	events, err := st.Events.ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 || events[0].EventType != model.RunEventTypeCancelled {
		t.Fatalf("expected cancelled event, got %+v", events)
	}
}

func TestProcessDispatchSkipsWhenNoPendingRuns(t *testing.T) {
	t.Parallel()

	st := openSchedulerTestStore(t)
	s := newTestScheduler(st)

	s.processDispatch(context.Background())
}

func TestProcessDispatchSkipsWhenWorkerTokensAreFull(t *testing.T) {
	t.Parallel()

	st := openSchedulerTestStore(t)
	job := createTestJob(t, st, 0)
	run := createTestRun(t, st, job.ID)

	s := &Scheduler{
		store:          st,
		workerTokens:   make(chan struct{}, 1),
		dueWakeup:      make(chan struct{}, 1),
		dispatchWakeup: make(chan struct{}, 1),
	}
	s.workerTokens <- struct{}{}

	s.processDispatch(context.Background())

	updatedRun, err := st.Runs.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if updatedRun.Status != model.RunStatusPending {
		t.Fatalf("expected run to remain pending, got %s", updatedRun.Status)
	}
	if len(s.workerTokens) != 1 {
		t.Fatalf("expected worker token to remain occupied")
	}
}

func TestAcquireWorkerTokenHonorsCancellation(t *testing.T) {
	t.Parallel()

	s := &Scheduler{
		workerTokens: make(chan struct{}, 1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if s.acquireWorkerToken(ctx) {
		t.Fatalf("expected acquireWorkerToken to fail on cancelled context")
	}
}

func newTestScheduler(st *store.Store) *Scheduler {
	return &Scheduler{
		store:          st,
		dueWakeup:      make(chan struct{}, 1),
		dispatchWakeup: make(chan struct{}, 1),
		workerTokens:   make(chan struct{}, 1),
	}
}

func openSchedulerTestStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return st
}

func createScheduledJob(t *testing.T, st *store.Store, retryLimit int, policy model.ConcurrencyPolicy) *model.Job {
	t.Helper()
	interval := 90
	nextRunAt := time.Now().UTC().Add(-time.Minute)
	job := &model.Job{
		Name:              "job-" + t.Name(),
		Enabled:           true,
		SourceType:        model.JobSourceTypeLocal,
		ImageRef:          "alpine:latest",
		ScheduleType:      model.ScheduleTypeInterval,
		IntervalSec:       &interval,
		Timezone:          "UTC",
		ConcurrencyPolicy: policy,
		TimeoutSec:        0,
		RetryLimit:        retryLimit,
		NextRunAt:         &nextRunAt,
	}
	if _, err := st.Jobs.Create(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	return job
}

func createTestRunWithStatus(t *testing.T, st *store.Store, jobID int64, status model.RunStatus) *model.Run {
	t.Helper()
	scheduledAt := time.Now().UTC().Add(-time.Minute)
	run := &model.Run{
		JobID:       jobID,
		ScheduledAt: scheduledAt,
		Status:      status,
		Attempt:     0,
	}
	if _, err := st.Runs.Create(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	return run
}
