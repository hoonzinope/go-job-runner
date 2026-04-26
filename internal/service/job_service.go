package service

import (
	"context"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/hoonzinope/go-job-runner/internal/config"
	"github.com/hoonzinope/go-job-runner/internal/model"
	"github.com/hoonzinope/go-job-runner/internal/store"
)

type SchedulerNotifier interface {
	NotifyDueJob()
	NotifyDispatch()
}

type JobInput struct {
	Name              string
	Description       *string
	Enabled           bool
	SourceType        model.JobSourceType
	ImageRef          string
	ImageDigest       *string
	ScheduleType      model.ScheduleType
	ScheduleExpr      *string
	IntervalSec       *int
	ParamsJSON        *string
	ConcurrencyPolicy model.ConcurrencyPolicy
	RetryLimit        int
	TimeoutSec        *int
	Timezone          string
}

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	if e == nil {
		return ""
	}
	if e.Field == "" {
		return e.Message
	}
	if e.Message == "" {
		return e.Field
	}
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

type JobService struct {
	store     *store.Store
	scheduler SchedulerNotifier
	timeouts  TimeoutPolicy
	now       func() time.Time
}

type TimeoutPolicy struct {
	DefaultTimeoutSec     int
	MaxTimeoutSec         int
	AllowUnlimitedTimeout bool
}

func NewJobService(st *store.Store, scheduler SchedulerNotifier) *JobService {
	return NewJobServiceWithTimeoutPolicy(st, scheduler, TimeoutPolicy{
		DefaultTimeoutSec: 3600,
		MaxTimeoutSec:     86400,
	})
}

func NewJobServiceWithConfig(st *store.Store, scheduler SchedulerNotifier, cfg config.SchedulerConfig) *JobService {
	return NewJobServiceWithTimeoutPolicy(st, scheduler, TimeoutPolicy{
		DefaultTimeoutSec:     cfg.DefaultTimeoutSec,
		MaxTimeoutSec:         cfg.MaxTimeoutSec,
		AllowUnlimitedTimeout: cfg.AllowUnlimitedTimeout,
	})
}

func NewJobServiceWithTimeoutPolicy(st *store.Store, scheduler SchedulerNotifier, policy TimeoutPolicy) *JobService {
	if policy.MaxTimeoutSec <= 0 {
		policy.MaxTimeoutSec = 86400
	}
	if policy.DefaultTimeoutSec == 0 && !policy.AllowUnlimitedTimeout {
		policy.DefaultTimeoutSec = 3600
	}
	return &JobService{
		store:     st,
		scheduler: scheduler,
		timeouts:  policy,
		now:       time.Now,
	}
}

func (s *JobService) ListJobs(ctx context.Context, filter store.JobFilter, page store.Page) ([]model.Job, int64, error) {
	return s.store.Jobs.List(ctx, filter, page)
}

func (s *JobService) GetJob(ctx context.Context, id int64) (*model.Job, error) {
	return s.store.Jobs.Get(ctx, id)
}

func (s *JobService) CreateJob(ctx context.Context, input JobInput) (*model.Job, error) {
	job, err := s.buildJob(input)
	if err != nil {
		return nil, err
	}

	now := s.now().UTC()
	job.CreatedAt = now
	job.UpdatedAt = now

	nextRunAt, err := computeNextRunAt(job, now)
	if err != nil {
		return nil, err
	}
	job.NextRunAt = nextRunAt

	if _, err := s.store.Jobs.Create(ctx, job); err != nil {
		return nil, err
	}
	s.notifyDueJob()
	return job, nil
}

func (s *JobService) UpdateJob(ctx context.Context, id int64, input JobInput) (*model.Job, error) {
	job, err := s.buildJob(input)
	if err != nil {
		return nil, err
	}

	existing, err := s.store.Jobs.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	job.ID = id
	job.CreatedAt = existing.CreatedAt
	job.LastScheduledAt = existing.LastScheduledAt

	now := s.now().UTC()
	job.UpdatedAt = now

	nextRunAt, err := computeNextRunAt(job, now)
	if err != nil {
		return nil, err
	}
	job.NextRunAt = nextRunAt

	if err := s.store.Jobs.Update(ctx, job); err != nil {
		return nil, err
	}
	s.notifyDueJob()
	return job, nil
}

func (s *JobService) DeleteJob(ctx context.Context, id int64) error {
	return s.store.Jobs.Delete(ctx, id)
}

func (s *JobService) TriggerJob(ctx context.Context, jobID int64, reason *string) (*model.Run, error) {
	job, err := s.store.Jobs.Get(ctx, jobID)
	if err != nil {
		return nil, err
	}

	now := s.now().UTC()
	run := &model.Run{
		JobID:       job.ID,
		ScheduledAt: now,
		Status:      model.RunStatusPending,
		Attempt:     0,
	}

	if err := s.store.WithinTx(ctx, func(tx *store.TxStore) error {
		if _, err := tx.Runs.Create(ctx, run); err != nil {
			return err
		}
		event := &model.RunEvent{
			RunID:     run.ID,
			EventType: model.RunEventTypeCreated,
		}
		if reason != nil && *reason != "" {
			event.Message = reason
		}
		if _, err := tx.Events.Create(ctx, event); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return nil, err
	}

	s.notifyDispatch()
	return run, nil
}

func (s *JobService) ListJobRuns(ctx context.Context, jobID int64, status *model.RunStatus, page store.Page) ([]model.Run, int64, error) {
	filter := store.RunFilter{JobID: &jobID}
	if status != nil {
		filter.Status = status
	}
	return s.store.Runs.List(ctx, filter, page)
}

func (s *JobService) buildJob(input JobInput) (*model.Job, error) {
	if input.Name == "" {
		return nil, &ValidationError{Field: "name", Message: "is required"}
	}
	if !input.SourceType.IsValid() {
		return nil, &ValidationError{Field: "sourceType", Message: fmt.Sprintf("invalid value %q", input.SourceType)}
	}
	if input.ImageRef == "" {
		return nil, &ValidationError{Field: "imageRef", Message: "is required"}
	}
	if !input.ScheduleType.IsValid() {
		return nil, &ValidationError{Field: "scheduleType", Message: fmt.Sprintf("invalid value %q", input.ScheduleType)}
	}
	if !input.ConcurrencyPolicy.IsValid() {
		return nil, &ValidationError{Field: "concurrencyPolicy", Message: fmt.Sprintf("invalid value %q", input.ConcurrencyPolicy)}
	}
	if input.Timezone == "" {
		input.Timezone = "UTC"
	}
	timeoutSec, err := s.resolveTimeoutSec(input.TimeoutSec)
	if err != nil {
		return nil, err
	}
	switch input.ScheduleType {
	case model.ScheduleTypeInterval:
		if input.IntervalSec == nil || *input.IntervalSec <= 0 {
			return nil, &ValidationError{Field: "intervalSec", Message: "must be greater than 0 for interval jobs"}
		}
		if input.ScheduleExpr != nil && *input.ScheduleExpr != "" {
			return nil, &ValidationError{Field: "scheduleExpr", Message: "must be empty for interval jobs"}
		}
	case model.ScheduleTypeCron:
		if input.ScheduleExpr == nil || *input.ScheduleExpr == "" {
			return nil, &ValidationError{Field: "scheduleExpr", Message: "is required for cron jobs"}
		}
	default:
		return nil, &ValidationError{Field: "scheduleType", Message: fmt.Sprintf("unsupported value %q", input.ScheduleType)}
	}

	job := &model.Job{
		Name:              input.Name,
		Description:       input.Description,
		Enabled:           input.Enabled,
		SourceType:        input.SourceType,
		ImageRef:          input.ImageRef,
		ImageDigest:       input.ImageDigest,
		ScheduleType:      input.ScheduleType,
		ScheduleExpr:      input.ScheduleExpr,
		IntervalSec:       input.IntervalSec,
		Timezone:          input.Timezone,
		ConcurrencyPolicy: input.ConcurrencyPolicy,
		RetryLimit:        input.RetryLimit,
		TimeoutSec:        timeoutSec,
		ParamsJSON:        input.ParamsJSON,
	}
	return job, nil
}

func (s *JobService) resolveTimeoutSec(requested *int) (int, error) {
	if requested == nil {
		return s.timeouts.DefaultTimeoutSec, nil
	}
	timeoutSec := *requested
	if timeoutSec < 0 {
		return 0, &ValidationError{Field: "timeoutSec", Message: "must be greater than or equal to 0"}
	}
	if timeoutSec == 0 {
		if s.timeouts.AllowUnlimitedTimeout {
			return 0, nil
		}
		return 0, &ValidationError{Field: "timeoutSec", Message: "unlimited timeout is disabled"}
	}
	if timeoutSec > s.timeouts.MaxTimeoutSec {
		return 0, &ValidationError{Field: "timeoutSec", Message: fmt.Sprintf("must be less than or equal to %d", s.timeouts.MaxTimeoutSec)}
	}
	return timeoutSec, nil
}

func computeNextRunAt(job *model.Job, from time.Time) (*time.Time, error) {
	switch job.ScheduleType {
	case model.ScheduleTypeInterval:
		if job.IntervalSec == nil {
			return nil, fmt.Errorf("intervalSec is required")
		}
		next := from.Add(time.Duration(*job.IntervalSec) * time.Second).UTC()
		return &next, nil
	case model.ScheduleTypeCron:
		if job.ScheduleExpr == nil || *job.ScheduleExpr == "" {
			return nil, fmt.Errorf("scheduleExpr is required")
		}
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
		sched, err := parser.Parse(*job.ScheduleExpr)
		if err != nil {
			return nil, fmt.Errorf("parse cron expression: %w", err)
		}
		loc := time.UTC
		if job.Timezone != "" {
			if loaded, err := time.LoadLocation(job.Timezone); err == nil {
				loc = loaded
			}
		}
		next := sched.Next(from.In(loc)).UTC()
		return &next, nil
	default:
		return nil, fmt.Errorf("unsupported schedule type: %q", job.ScheduleType)
	}
}

func (s *JobService) notifyDueJob() {
	if s.scheduler != nil {
		s.scheduler.NotifyDueJob()
	}
}

func (s *JobService) notifyDispatch() {
	if s.scheduler != nil {
		s.scheduler.NotifyDispatch()
	}
}
