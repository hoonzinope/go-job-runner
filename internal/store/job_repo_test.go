package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/hoonzinope/go-job-runner/internal/model"
)

func TestJobRepoCrudHappyPath(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	ctx := context.Background()

	desc := "example job"
	imageDigest := "sha256:abc"
	params := `{"foo":"bar"}`
	nextRun := time.Date(2026, 4, 17, 13, 0, 0, 0, time.UTC)
	lastScheduled := time.Date(2026, 4, 17, 12, 30, 0, 0, time.UTC)
	job := testJob("job-crud")
	job.Description = &desc
	job.ImageDigest = &imageDigest
	job.ParamsJSON = &params
	job.NextRunAt = &nextRun
	job.LastScheduledAt = &lastScheduled
	job.IntervalSec = ptrInt(60)

	id, err := st.Jobs.Create(ctx, job)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id <= 0 || job.ID != id {
		t.Fatalf("unexpected job id: %d %d", id, job.ID)
	}

	got, err := st.Jobs.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != job.Name || got.ImageRef != job.ImageRef || got.RetryLimit != job.RetryLimit || got.TimeoutSec != job.TimeoutSec {
		t.Fatalf("unexpected job: %+v", got)
	}
	if got.Description == nil || *got.Description != desc {
		t.Fatalf("description mismatch: %+v", got.Description)
	}
	if got.ImageDigest == nil || *got.ImageDigest != imageDigest {
		t.Fatalf("image digest mismatch: %+v", got.ImageDigest)
	}
	if got.ParamsJSON == nil || *got.ParamsJSON != params {
		t.Fatalf("params mismatch: %+v", got.ParamsJSON)
	}
	if got.IntervalSec == nil || *got.IntervalSec != 60 {
		t.Fatalf("interval mismatch: %+v", got.IntervalSec)
	}
	if got.NextRunAt == nil || !got.NextRunAt.Equal(nextRun) {
		t.Fatalf("next run mismatch: %+v", got.NextRunAt)
	}
	if got.LastScheduledAt == nil || !got.LastScheduledAt.Equal(lastScheduled) {
		t.Fatalf("last scheduled mismatch: %+v", got.LastScheduledAt)
	}

	gotByName, err := st.Jobs.GetByName(ctx, job.Name)
	if err != nil {
		t.Fatalf("get by name: %v", err)
	}
	if gotByName.ID != id {
		t.Fatalf("get by name id mismatch: %d", gotByName.ID)
	}

	enabled := true
	scheduleType := model.ScheduleTypeCron
	items, total, err := st.Jobs.List(ctx, JobFilter{Enabled: &enabled, ScheduleType: &scheduleType, Name: "crud"}, Page{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("unexpected list result: total=%d len=%d", total, len(items))
	}

	desc2 := "updated job"
	job.Description = &desc2
	job.Enabled = false
	job.ImageRef = "jobs/example:v2"
	job.TimeoutSec = 90
	job.RetryLimit = 2
	if err := st.Jobs.Update(ctx, job); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err = st.Jobs.Get(ctx, id)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Enabled {
		t.Fatal("expected disabled job")
	}
	if got.TimeoutSec != 90 || got.RetryLimit != 2 || got.ImageRef != "jobs/example:v2" {
		t.Fatalf("update not applied: %+v", got)
	}

	newNextRun := time.Date(2026, 4, 17, 14, 0, 0, 0, time.UTC)
	if err := st.Jobs.UpdateScheduling(ctx, id, &newNextRun, &newNextRun); err != nil {
		t.Fatalf("update scheduling: %v", err)
	}

	got, err = st.Jobs.Get(ctx, id)
	if err != nil {
		t.Fatalf("get after scheduling update: %v", err)
	}
	if got.NextRunAt == nil || !got.NextRunAt.Equal(newNextRun) {
		t.Fatalf("next run mismatch: %+v", got.NextRunAt)
	}
	if got.LastScheduledAt == nil || !got.LastScheduledAt.Equal(newNextRun) {
		t.Fatalf("last scheduled mismatch: %+v", got.LastScheduledAt)
	}

	if err := st.Jobs.Delete(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.Jobs.Get(ctx, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected deleted job to be missing, got: %v", err)
	}
}

func TestJobRepoCreateValidationFailures(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	ctx := context.Background()

	t.Run("nil", func(t *testing.T) {
		if _, err := st.Jobs.Create(ctx, nil); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("invalid source", func(t *testing.T) {
		job := testJob("bad-source")
		job.SourceType = "invalid"
		if _, err := st.Jobs.Create(ctx, job); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("invalid schedule", func(t *testing.T) {
		job := testJob("bad-schedule")
		job.ScheduleType = "invalid"
		if _, err := st.Jobs.Create(ctx, job); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("invalid concurrency", func(t *testing.T) {
		job := testJob("bad-concurrency")
		job.ConcurrencyPolicy = "invalid"
		if _, err := st.Jobs.Create(ctx, job); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestJobRepoUniqueNameAndMissingRows(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	ctx := context.Background()

	job := testJob("duplicate-name")
	if _, err := st.Jobs.Create(ctx, job); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := st.Jobs.Create(ctx, testJob("duplicate-name")); err == nil {
		t.Fatal("expected unique constraint error")
	}

	if _, err := st.Jobs.Get(ctx, 9999); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected no rows, got: %v", err)
	}
	if _, err := st.Jobs.GetByName(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected no rows, got: %v", err)
	}
}

func TestJobRepoListDueAndCorruptRow(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	ctx := context.Background()

	dueA := testJob("due-a")
	dueA.NextRunAt = ptrTime(time.Date(2026, 4, 17, 10, 0, 0, 0, time.UTC))
	dueA.Enabled = true
	if _, err := st.Jobs.Create(ctx, dueA); err != nil {
		t.Fatalf("create dueA: %v", err)
	}
	dueB := testJob("due-b")
	dueB.NextRunAt = ptrTime(time.Date(2026, 4, 17, 9, 30, 0, 0, time.UTC))
	if _, err := st.Jobs.Create(ctx, dueB); err != nil {
		t.Fatalf("create dueB: %v", err)
	}
	disabled := testJob("disabled")
	disabled.Enabled = false
	disabled.NextRunAt = ptrTime(time.Date(2026, 4, 17, 8, 0, 0, 0, time.UTC))
	if _, err := st.Jobs.Create(ctx, disabled); err != nil {
		t.Fatalf("create disabled: %v", err)
	}
	future := testJob("future")
	future.NextRunAt = ptrTime(time.Date(2026, 4, 17, 18, 0, 0, 0, time.UTC))
	if _, err := st.Jobs.Create(ctx, future); err != nil {
		t.Fatalf("create future: %v", err)
	}

	items, err := st.Jobs.ListDue(ctx, time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("unexpected due count: %d", len(items))
	}
	if items[0].Name != "due-b" || items[1].Name != "due-a" {
		t.Fatalf("unexpected order: %+v", []string{items[0].Name, items[1].Name})
	}

	// Corrupt row to cover scan-time parsing failure.
	if _, err := st.DB.ExecContext(ctx, `
		INSERT INTO jobs (
			name, enabled, source_type, image_ref, schedule_type, timezone, concurrency_policy,
			timeout_sec, retry_limit, created_at, updated_at
		) VALUES (?, 1, ?, ?, ?, ?, ?, 0, 0, ?, ?)
	`, "corrupt-job", "local", "jobs/example:latest", "cron", "UTC", "forbid", "not-a-time", encodeTime(time.Now().UTC())); err != nil {
		t.Fatalf("insert corrupt row: %v", err)
	}
	if _, err := st.Jobs.GetByName(ctx, "corrupt-job"); err == nil {
		t.Fatal("expected scan error for corrupt time")
	}
}

func TestJobRepoListFiltersAndEmptyResult(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	ctx := context.Background()

	enabledCron := testJob("alpha-cron")
	enabledCron.Enabled = true
	enabledCron.ScheduleType = model.ScheduleTypeCron
	if _, err := st.Jobs.Create(ctx, enabledCron); err != nil {
		t.Fatalf("create enabled cron: %v", err)
	}

	disabledInterval := testJob("beta-interval")
	disabledInterval.Enabled = false
	disabledInterval.ScheduleType = model.ScheduleTypeInterval
	if _, err := st.Jobs.Create(ctx, disabledInterval); err != nil {
		t.Fatalf("create disabled interval: %v", err)
	}

	enabledInterval := testJob("gamma-interval")
	enabledInterval.Enabled = true
	enabledInterval.ScheduleType = model.ScheduleTypeInterval
	if _, err := st.Jobs.Create(ctx, enabledInterval); err != nil {
		t.Fatalf("create enabled interval: %v", err)
	}

	enabled, scheduleType := true, model.ScheduleTypeCron
	items, total, err := st.Jobs.List(ctx, JobFilter{Enabled: &enabled}, Page{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("list enabled: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("unexpected enabled result: total=%d len=%d", total, len(items))
	}

	items, total, err = st.Jobs.List(ctx, JobFilter{ScheduleType: &scheduleType}, Page{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("list cron: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].Name != "alpha-cron" {
		t.Fatalf("unexpected schedule result: total=%d items=%+v", total, items)
	}

	items, total, err = st.Jobs.List(ctx, JobFilter{Name: "interval"}, Page{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("list name: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("unexpected name result: total=%d len=%d", total, len(items))
	}

	enabledFalse := false
	items, total, err = st.Jobs.List(ctx, JobFilter{Enabled: &enabledFalse, ScheduleType: &scheduleType}, Page{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("list empty filter: %v", err)
	}
	if total != 0 || len(items) != 0 {
		t.Fatalf("expected empty result, got total=%d len=%d", total, len(items))
	}
}

func TestJobRepoUpdateSchedulingClearsNullableFields(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	ctx := context.Background()

	nextRun := time.Date(2026, 4, 17, 13, 0, 0, 0, time.UTC)
	lastScheduled := time.Date(2026, 4, 17, 12, 30, 0, 0, time.UTC)
	job := testJob("clear-scheduling")
	job.NextRunAt = &nextRun
	job.LastScheduledAt = &lastScheduled
	id, err := st.Jobs.Create(ctx, job)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := st.Jobs.UpdateScheduling(ctx, id, nil, nil); err != nil {
		t.Fatalf("clear scheduling: %v", err)
	}

	got, err := st.Jobs.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.NextRunAt != nil || got.LastScheduledAt != nil {
		t.Fatalf("expected cleared scheduling, got: %+v", got)
	}
}

func TestJobRepoUpdateValidationFailures(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	ctx := context.Background()

	if err := st.Jobs.Update(ctx, nil); err == nil {
		t.Fatal("expected error")
	}

	job := testJob("missing-id")
	if err := st.Jobs.Update(ctx, job); err == nil {
		t.Fatal("expected error")
	}

	job.ID = 1
	job.SourceType = "invalid"
	if err := st.Jobs.Update(ctx, job); err == nil {
		t.Fatal("expected error")
	}
}

func ptrInt(v int) *int {
	return &v
}

func ptrTime(v time.Time) *time.Time {
	return &v
}
