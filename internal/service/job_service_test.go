package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hoonzinope/go-job-runner/internal/model"
	"github.com/hoonzinope/go-job-runner/internal/store"
)

type testSchedulerNotifier struct {
	dueJobCount   int
	dispatchCount int
}

func (n *testSchedulerNotifier) NotifyDueJob() {
	n.dueJobCount++
}

func (n *testSchedulerNotifier) NotifyDispatch() {
	n.dispatchCount++
}

func TestValidationErrorError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  *ValidationError
		want string
	}{
		{name: "nil", err: nil, want: ""},
		{name: "field and message", err: &ValidationError{Field: "name", Message: "is required"}, want: "name: is required"},
		{name: "field only", err: &ValidationError{Field: "name"}, want: "name"},
		{name: "message only", err: &ValidationError{Message: "invalid"}, want: "invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ""
			if tt.err != nil {
				got = tt.err.Error()
			}
			if got != tt.want {
				t.Fatalf("unexpected error string: got %q want %q", got, tt.want)
			}
		})
	}
}

func TestComputeNextRunAt(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	interval := 90
	expr := "*/5 * * * *"

	tests := []struct {
		name      string
		job       *model.Job
		want      time.Time
		wantError string
	}{
		{
			name: "interval",
			job: &model.Job{
				ScheduleType: model.ScheduleTypeInterval,
				IntervalSec:  &interval,
			},
			want: from.Add(90 * time.Second),
		},
		{
			name: "cron",
			job: &model.Job{
				ScheduleType: model.ScheduleTypeCron,
				ScheduleExpr: &expr,
				Timezone:     "UTC",
			},
			want: time.Date(2026, 4, 17, 12, 5, 0, 0, time.UTC),
		},
		{
			name: "invalid cron expr",
			job: &model.Job{
				ScheduleType: model.ScheduleTypeCron,
				ScheduleExpr: ptrString("not-a-cron"),
			},
			wantError: "parse cron expression:",
		},
		{
			name: "unsupported type",
			job: &model.Job{
				ScheduleType: model.ScheduleType("other"),
			},
			wantError: "unsupported schedule type:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := computeNextRunAt(tt.job, from)
			if tt.wantError != "" {
				if err == nil || !containsString(err.Error(), tt.wantError) {
					t.Fatalf("expected error containing %q, got %v", tt.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("compute next run at: %v", err)
			}
			if !got.Equal(tt.want) {
				t.Fatalf("unexpected next run: got %v want %v", got, tt.want)
			}
		})
	}
}

func TestJobServiceCreateJob(t *testing.T) {
	t.Parallel()

	st := openServiceTestStore(t)
	notifier := &testSchedulerNotifier{}
	svc := NewJobService(st, notifier)
	svc.now = func() time.Time {
		return time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	}

	desc := "example job"
	params := `{"foo":"bar"}`
	input := JobInput{
		Name:              "create-job",
		Description:       &desc,
		Enabled:           true,
		SourceType:        model.JobSourceTypeLocal,
		ImageRef:          "jobs/example:latest",
		ScheduleType:      model.ScheduleTypeInterval,
		IntervalSec:       ptrInt(90),
		ParamsJSON:        &params,
		ConcurrencyPolicy: model.ConcurrencyPolicyForbid,
		RetryLimit:        2,
		TimeoutSec:        ptrInt(30),
	}

	created, err := svc.CreateJob(context.Background(), input)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	if notifier.dueJobCount != 1 || notifier.dispatchCount != 0 {
		t.Fatalf("unexpected notifier counts: %+v", notifier)
	}
	if created.ID <= 0 {
		t.Fatalf("expected job id, got %d", created.ID)
	}
	if created.Timezone != "UTC" {
		t.Fatalf("expected default timezone UTC, got %q", created.Timezone)
	}
	wantNext := time.Date(2026, 4, 17, 12, 1, 30, 0, time.UTC)
	if created.NextRunAt == nil || !created.NextRunAt.Equal(wantNext) {
		t.Fatalf("unexpected next run: %+v", created.NextRunAt)
	}
	wantNow := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	if !created.CreatedAt.Equal(wantNow) || !created.UpdatedAt.Equal(wantNow) {
		t.Fatalf("unexpected timestamps: %+v", created)
	}

	got, err := st.Jobs.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get created job: %v", err)
	}
	if got.Name != input.Name || got.ImageRef != input.ImageRef || got.RetryLimit != input.RetryLimit {
		t.Fatalf("unexpected stored job: %+v", got)
	}
}

func TestJobServiceCreateJobValidationErrors(t *testing.T) {
	t.Parallel()

	st := openServiceTestStore(t)
	notifier := &testSchedulerNotifier{}
	svc := NewJobService(st, notifier)

	tests := []struct {
		name  string
		input JobInput
		field string
	}{
		{
			name:  "missing name",
			input: validIntervalJobInput(),
			field: "name",
		},
		{
			name:  "missing interval",
			input: func() JobInput { v := validIntervalJobInput(); v.IntervalSec = nil; return v }(),
			field: "intervalSec",
		},
		{
			name:  "interval with cron expr",
			input: func() JobInput { v := validIntervalJobInput(); v.ScheduleExpr = ptrString("* * * * *"); return v }(),
			field: "scheduleExpr",
		},
	}

	tests[0].input.Name = ""

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := svc.CreateJob(context.Background(), tt.input)
			if err == nil {
				t.Fatal("expected validation error")
			}
			var vErr *ValidationError
			if !errors.As(err, &vErr) {
				t.Fatalf("expected ValidationError, got %T %v", err, err)
			}
			if vErr.Field != tt.field {
				t.Fatalf("unexpected validation field: got %q want %q", vErr.Field, tt.field)
			}
		})
	}
}

func TestJobServiceTimeoutPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		policy    TimeoutPolicy
		timeout   *int
		want      int
		wantField string
	}{
		{
			name: "omitted timeout uses default",
			policy: TimeoutPolicy{
				DefaultTimeoutSec: 120,
				MaxTimeoutSec:     300,
			},
			want: 120,
		},
		{
			name: "zero timeout rejected by default",
			policy: TimeoutPolicy{
				DefaultTimeoutSec: 120,
				MaxTimeoutSec:     300,
			},
			timeout:   ptrInt(0),
			wantField: "timeoutSec",
		},
		{
			name: "zero timeout allowed when enabled",
			policy: TimeoutPolicy{
				DefaultTimeoutSec:     120,
				MaxTimeoutSec:         300,
				AllowUnlimitedTimeout: true,
			},
			timeout: ptrInt(0),
			want:    0,
		},
		{
			name: "timeout above max rejected",
			policy: TimeoutPolicy{
				DefaultTimeoutSec: 120,
				MaxTimeoutSec:     300,
			},
			timeout:   ptrInt(301),
			wantField: "timeoutSec",
		},
		{
			name: "negative timeout rejected",
			policy: TimeoutPolicy{
				DefaultTimeoutSec: 120,
				MaxTimeoutSec:     300,
			},
			timeout:   ptrInt(-1),
			wantField: "timeoutSec",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st := openServiceTestStore(t)
			svc := NewJobServiceWithTimeoutPolicy(st, nil, tt.policy)
			input := validIntervalJobInput()
			input.TimeoutSec = tt.timeout

			created, err := svc.CreateJob(context.Background(), input)
			if tt.wantField != "" {
				if err == nil {
					t.Fatal("expected validation error")
				}
				var vErr *ValidationError
				if !errors.As(err, &vErr) {
					t.Fatalf("expected ValidationError, got %T %v", err, err)
				}
				if vErr.Field != tt.wantField {
					t.Fatalf("unexpected validation field: got %q want %q", vErr.Field, tt.wantField)
				}
				return
			}
			if err != nil {
				t.Fatalf("create job: %v", err)
			}
			if created.TimeoutSec != tt.want {
				t.Fatalf("unexpected timeout: got %d want %d", created.TimeoutSec, tt.want)
			}
		})
	}
}

func TestJobServiceUpdateJobPreservesExistingFields(t *testing.T) {
	t.Parallel()

	st := openServiceTestStore(t)
	notifier := &testSchedulerNotifier{}
	svc := NewJobService(st, notifier)
	svc.now = func() time.Time {
		return time.Date(2026, 4, 17, 15, 0, 0, 0, time.UTC)
	}

	createdAt := time.Date(2026, 4, 16, 11, 0, 0, 0, time.UTC)
	lastScheduledAt := time.Date(2026, 4, 17, 10, 0, 0, 0, time.UTC)
	nextRunAt := time.Date(2026, 4, 17, 11, 0, 0, 0, time.UTC)
	job := baseIntervalJob("update-job", 60)
	job.CreatedAt = createdAt
	job.UpdatedAt = createdAt
	job.LastScheduledAt = &lastScheduledAt
	job.NextRunAt = &nextRunAt
	if _, err := st.Jobs.Create(context.Background(), job); err != nil {
		t.Fatalf("create existing job: %v", err)
	}

	desc := "updated description"
	updated, err := svc.UpdateJob(context.Background(), job.ID, JobInput{
		Name:              "update-job-v2",
		Description:       &desc,
		Enabled:           false,
		SourceType:        model.JobSourceTypeLocal,
		ImageRef:          "jobs/example:v2",
		ScheduleType:      model.ScheduleTypeInterval,
		IntervalSec:       ptrInt(120),
		ConcurrencyPolicy: model.ConcurrencyPolicyAllow,
		RetryLimit:        3,
		TimeoutSec:        ptrInt(45),
	})
	if err != nil {
		t.Fatalf("update job: %v", err)
	}

	if notifier.dueJobCount != 1 {
		t.Fatalf("expected due job notification, got %+v", notifier)
	}
	if updated.ID != job.ID {
		t.Fatalf("unexpected job id: %d", updated.ID)
	}
	if updated.CreatedAt != createdAt {
		t.Fatalf("createdAt was not preserved: got %v want %v", updated.CreatedAt, createdAt)
	}
	if updated.LastScheduledAt == nil || !updated.LastScheduledAt.Equal(lastScheduledAt) {
		t.Fatalf("lastScheduledAt was not preserved: %+v", updated.LastScheduledAt)
	}
	wantNext := time.Date(2026, 4, 17, 15, 2, 0, 0, time.UTC)
	if updated.NextRunAt == nil || !updated.NextRunAt.Equal(wantNext) {
		t.Fatalf("unexpected next run: %+v", updated.NextRunAt)
	}

	stored, err := st.Jobs.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("get updated job: %v", err)
	}
	if stored.CreatedAt != createdAt {
		t.Fatalf("stored createdAt was not preserved: got %v want %v", stored.CreatedAt, createdAt)
	}
	if stored.LastScheduledAt == nil || !stored.LastScheduledAt.Equal(lastScheduledAt) {
		t.Fatalf("stored lastScheduledAt was not preserved: %+v", stored.LastScheduledAt)
	}
	if stored.NextRunAt == nil || !stored.NextRunAt.Equal(wantNext) {
		t.Fatalf("stored nextRunAt mismatch: %+v", stored.NextRunAt)
	}
	if stored.Name != "update-job-v2" || stored.Enabled {
		t.Fatalf("stored update did not apply: %+v", stored)
	}
}

func TestJobServiceTriggerJob(t *testing.T) {
	t.Parallel()

	st := openServiceTestStore(t)
	notifier := &testSchedulerNotifier{}
	svc := NewJobService(st, notifier)
	svc.now = func() time.Time {
		return time.Date(2026, 4, 17, 16, 0, 0, 0, time.UTC)
	}

	job := baseIntervalJob("trigger-job", 60)
	if _, err := st.Jobs.Create(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	reason := "manual trigger"
	run, err := svc.TriggerJob(context.Background(), job.ID, &reason)
	if err != nil {
		t.Fatalf("trigger job: %v", err)
	}

	if notifier.dispatchCount != 1 || notifier.dueJobCount != 0 {
		t.Fatalf("unexpected notifier counts: %+v", notifier)
	}
	if run.ID <= 0 {
		t.Fatalf("expected run id, got %d", run.ID)
	}
	if run.JobID != job.ID || run.Status != model.RunStatusPending || run.Attempt != 0 {
		t.Fatalf("unexpected run: %+v", run)
	}
	wantScheduled := time.Date(2026, 4, 17, 16, 0, 0, 0, time.UTC)
	if !run.ScheduledAt.Equal(wantScheduled) {
		t.Fatalf("unexpected scheduledAt: %v", run.ScheduledAt)
	}

	stored, err := st.Runs.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if stored.Status != model.RunStatusPending {
		t.Fatalf("unexpected stored run status: %+v", stored)
	}
	events, err := st.Events.ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 || events[0].EventType != model.RunEventTypeCreated {
		t.Fatalf("unexpected events: %+v", events)
	}
	if events[0].Message == nil || *events[0].Message != reason {
		t.Fatalf("unexpected event message: %+v", events[0].Message)
	}
}

func TestJobServiceListJobRuns(t *testing.T) {
	t.Parallel()

	st := openServiceTestStore(t)
	svc := NewJobService(st, nil)

	job := baseIntervalJob("runs-job", 60)
	if _, err := st.Jobs.Create(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	pending := &model.Run{JobID: job.ID, ScheduledAt: time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC), Status: model.RunStatusPending, Attempt: 0}
	success := &model.Run{JobID: job.ID, ScheduledAt: time.Date(2026, 4, 17, 13, 0, 0, 0, time.UTC), Status: model.RunStatusSuccess, Attempt: 0}
	if _, err := st.Runs.Create(context.Background(), pending); err != nil {
		t.Fatalf("create pending run: %v", err)
	}
	if _, err := st.Runs.Create(context.Background(), success); err != nil {
		t.Fatalf("create success run: %v", err)
	}

	status := model.RunStatusPending
	runs, total, err := svc.ListJobRuns(context.Background(), job.ID, &status, store.Page{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("list job runs: %v", err)
	}
	if total != 1 || len(runs) != 1 || runs[0].Status != model.RunStatusPending {
		t.Fatalf("unexpected job runs result: total=%d runs=%+v", total, runs)
	}
}

func openServiceTestStore(t *testing.T) *store.Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "db.sqlite")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return st
}

func baseIntervalJob(name string, interval int) *model.Job {
	return &model.Job{
		Name:              name,
		Enabled:           true,
		SourceType:        model.JobSourceTypeLocal,
		ImageRef:          "jobs/example:latest",
		ScheduleType:      model.ScheduleTypeInterval,
		IntervalSec:       ptrInt(interval),
		Timezone:          "UTC",
		ConcurrencyPolicy: model.ConcurrencyPolicyForbid,
		RetryLimit:        1,
		TimeoutSec:        30,
	}
}

func validIntervalJobInput() JobInput {
	return JobInput{
		Name:              "valid-job",
		Enabled:           true,
		SourceType:        model.JobSourceTypeLocal,
		ImageRef:          "jobs/example:latest",
		ScheduleType:      model.ScheduleTypeInterval,
		IntervalSec:       ptrInt(60),
		ConcurrencyPolicy: model.ConcurrencyPolicyForbid,
		RetryLimit:        1,
		TimeoutSec:        ptrInt(30),
	}
}

func ptrString(v string) *string {
	return &v
}

func ptrInt(v int) *int {
	return &v
}

func ptrTime(v time.Time) *time.Time {
	return &v
}

func containsString(s, substr string) bool {
	return len(substr) == 0 || (len(s) >= len(substr) && (stringIndex(s, substr) >= 0))
}

func stringIndex(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
