package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/hoonzinope/go-job-runner/internal/model"
	"github.com/hoonzinope/go-job-runner/internal/store"
)

func (s *Scheduler) dueJobLoop(ctx context.Context) {
	ticker := time.NewTicker(s.dueJobInterval)
	defer ticker.Stop()

	s.processDueJobs(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.processDueJobs(ctx)
		case <-s.dueWakeup:
			s.processDueJobs(ctx)
		}
	}
}

func (s *Scheduler) processDueJobs(ctx context.Context) {
	now := time.Now().UTC()
	jobs, err := s.store.Jobs.ListDue(ctx, now)
	if err != nil {
		fmt.Printf("scheduler due-job scan error: %v\n", err)
		return
	}

	for i := range jobs {
		if err := s.processDueJob(ctx, &jobs[i], now); err != nil {
			fmt.Printf("scheduler due-job process error (job=%d): %v\n", jobs[i].ID, err)
		}
	}
}

func (s *Scheduler) processDueJob(ctx context.Context, job *model.Job, now time.Time) error {
	if job.NextRunAt == nil {
		return nil
	}

	nextRunAt, err := computeNextRunAt(job, now)
	if err != nil {
		return err
	}
	scheduledAt := (*job.NextRunAt).UTC()
	scheduledAtPtr := &scheduledAt

	return s.store.WithinTx(ctx, func(tx *store.TxStore) error {
		if job.ConcurrencyPolicy == model.ConcurrencyPolicyForbid {
			running, _, err := tx.Runs.List(ctx, store.RunFilter{
				JobID:  &job.ID,
				Status: runStatusPtr(model.RunStatusRunning),
			}, store.Page{Page: 1, Size: 100})
			if err != nil {
				return err
			}

			activeRunning := make([]model.Run, 0, len(running))
			for i := range running {
				if s.isRunningRunStale(&running[i], now, job.TimeoutSec) {
					if err := s.finalizeStaleRunningRun(ctx, tx, &running[i], now, job.TimeoutSec); err != nil {
						return err
					}
					continue
				}
				activeRunning = append(activeRunning, running[i])
			}

			if len(activeRunning) > 0 {
				msg := fmt.Sprintf(
					"scheduled run skipped: concurrency policy forbid and run %d is still running; next run advanced to %s",
					activeRunning[0].ID,
					nextRunAt.UTC().Format(time.RFC3339),
				)
				log.Printf("scheduler due-job skipped job=%d running_run=%d next_run_at=%s", job.ID, activeRunning[0].ID, nextRunAt.UTC().Format(time.RFC3339))
				skippedRun := &model.Run{
					JobID:        job.ID,
					ScheduledAt:  scheduledAt,
					FinishedAt:   &now,
					Status:       model.RunStatusSkipped,
					Attempt:      0,
					ErrorMessage: &msg,
				}
				if _, err := tx.Runs.Create(ctx, skippedRun); err != nil {
					return err
				}
				if _, err := tx.Events.Create(ctx, &model.RunEvent{
					RunID:     skippedRun.ID,
					EventType: model.RunEventTypeSkipped,
					Message:   &msg,
				}); err != nil {
					return err
				}
				return tx.Jobs.UpdateScheduling(ctx, job.ID, &nextRunAt, scheduledAtPtr)
			}
		}

		run := &model.Run{
			JobID:       job.ID,
			ScheduledAt: scheduledAt,
			Status:      model.RunStatusPending,
			Attempt:     0,
		}
		if _, err := tx.Runs.Create(ctx, run); err != nil {
			return err
		}
		if _, err := tx.Events.Create(ctx, &model.RunEvent{
			RunID:     run.ID,
			EventType: model.RunEventTypeCreated,
		}); err != nil {
			return err
		}
		if err := tx.Jobs.UpdateScheduling(ctx, job.ID, &nextRunAt, scheduledAtPtr); err != nil {
			return err
		}
		return nil
	})
}

func computeNextRunAt(job *model.Job, from time.Time) (time.Time, error) {
	switch job.ScheduleType {
	case model.ScheduleTypeInterval:
		if job.IntervalSec == nil || *job.IntervalSec <= 0 {
			return time.Time{}, fmt.Errorf("invalid intervalSec for job %d", job.ID)
		}
		return from.Add(time.Duration(*job.IntervalSec) * time.Second).UTC(), nil
	case model.ScheduleTypeCron:
		if job.ScheduleExpr == nil || *job.ScheduleExpr == "" {
			return time.Time{}, fmt.Errorf("missing scheduleExpr for job %d", job.ID)
		}
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
		sched, err := parser.Parse(*job.ScheduleExpr)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse cron expr for job %d: %w", job.ID, err)
		}
		loc := time.UTC
		if job.Timezone != "" {
			if loaded, err := time.LoadLocation(job.Timezone); err == nil {
				loc = loaded
			}
		}
		return sched.Next(from.In(loc)).UTC(), nil
	default:
		return time.Time{}, fmt.Errorf("unsupported schedule type %q", job.ScheduleType)
	}
}

func runStatusPtr(status model.RunStatus) *model.RunStatus {
	return &status
}

func (s *Scheduler) isRunningRunStale(run *model.Run, now time.Time, timeoutSec int) bool {
	if run == nil {
		return false
	}
	timeoutSec = s.effectiveTimeoutSec(timeoutSec)
	if timeoutSec <= 0 {
		return false
	}

	startedAt := run.StartedAt
	if startedAt == nil || startedAt.IsZero() {
		if run.CreatedAt.IsZero() {
			return false
		}
		startedAt = &run.CreatedAt
	}

	return now.Sub(*startedAt) >= time.Duration(timeoutSec)*time.Second
}

func (s *Scheduler) finalizeStaleRunningRun(ctx context.Context, tx *store.TxStore, run *model.Run, now time.Time, timeoutSec int) error {
	if run == nil {
		return nil
	}
	timeoutSec = s.effectiveTimeoutSec(timeoutSec)
	startedAt := run.StartedAt
	if startedAt == nil || startedAt.IsZero() {
		startedAt = &run.CreatedAt
	}
	finishedAt := now.UTC()
	exitCode := -1
	msg := fmt.Sprintf(
		"stale running run discarded after %d seconds without a status update",
		timeoutSec,
	)
	log.Printf(
		"scheduler stale-running timeout job=%d run=%d started_at=%s timeout_sec=%d",
		run.JobID,
		run.ID,
		startedAt.UTC().Format(time.RFC3339),
		timeoutSec,
	)
	if err := tx.Runs.UpdateStatus(ctx, run.ID, model.RunStatusTimeout, startedAt, &finishedAt, &exitCode, &msg); err != nil {
		return err
	}
	_, err := tx.Events.Create(ctx, &model.RunEvent{
		RunID:     run.ID,
		EventType: model.RunEventTypeTimeout,
		Message:   &msg,
	})
	return err
}
