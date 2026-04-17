package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/hoonzinope/go-job-runner/internal/model"
	"github.com/hoonzinope/go-job-runner/internal/store"
)

func (s *Scheduler) runWorker(ctx context.Context, runID int64) {
	run, err := s.store.Runs.Get(ctx, runID)
	if err != nil {
		fmt.Printf("scheduler worker load run error (run=%d): %v\n", runID, err)
		return
	}

	job, err := s.store.Jobs.Get(ctx, run.JobID)
	if err != nil {
		fmt.Printf("scheduler worker load job error (run=%d job=%d): %v\n", runID, run.JobID, err)
		return
	}

	startedAt := time.Now().UTC()
	shouldStop := false
	if err := s.store.WithinTx(ctx, func(tx *store.TxStore) error {
		freshRun, err := tx.Runs.Get(ctx, runID)
		if err != nil {
			return err
		}

		switch freshRun.Status {
		case model.RunStatusCancelled:
			shouldStop = true
			return nil
		case model.RunStatusCancelling:
			finishedAt := time.Now().UTC()
			if err := tx.Runs.UpdateStatus(ctx, runID, model.RunStatusCancelled, freshRun.StartedAt, &finishedAt, freshRun.ExitCode, freshRun.ErrorMessage); err != nil {
				return err
			}
			msg := "run cancelled before execution"
			_, err := tx.Events.Create(ctx, &model.RunEvent{
				RunID:     runID,
				EventType: model.RunEventTypeCancelled,
				Message:   &msg,
			})
			shouldStop = true
			return err
		case model.RunStatusPending:
			run.StartedAt = &startedAt
			if err := tx.Runs.UpdateStatus(ctx, runID, model.RunStatusRunning, &startedAt, nil, nil, nil); err != nil {
				return err
			}
			msg := "run started"
			_, err := tx.Events.Create(ctx, &model.RunEvent{
				RunID:     runID,
				EventType: model.RunEventTypeStarted,
				Message:   &msg,
			})
			return err
		case model.RunStatusRunning:
			run.StartedAt = freshRun.StartedAt
			return nil
		default:
			return nil
		}
	}); err != nil {
		log.Printf("scheduler worker start error (run=%d): %v", runID, err)
		return
	}

	if shouldStop {
		s.signalDispatch()
		return
	}

	if ctx.Err() != nil {
		finishedAt := time.Now().UTC()
		msg := "context cancelled"
		if err := s.store.WithinTx(context.Background(), func(tx *store.TxStore) error {
			if err := tx.Runs.UpdateStatus(context.Background(), runID, model.RunStatusCancelled, run.StartedAt, &finishedAt, run.ExitCode, run.ErrorMessage); err != nil {
				return err
			}
			_, err := tx.Events.Create(context.Background(), &model.RunEvent{
				RunID:     runID,
				EventType: model.RunEventTypeCancelled,
				Message:   &msg,
			})
			return err
		}); err != nil {
			log.Printf("scheduler worker cancel finalize error (run=%d): %v", runID, err)
		}
		s.signalDispatch()
		return
	}

	runCtx := ctx
	cancelRun := func() {}
	if job.TimeoutSec > 0 {
		runCtx, cancelRun = context.WithTimeout(ctx, time.Duration(job.TimeoutSec)*time.Second)
	}
	defer cancelRun()

	result, execErr := s.newExecutor().Execute(runCtx, job, run)
	finishedAt := time.Now().UTC()

	if result != nil && result.LogPath != "" {
		run.LogPath = &result.LogPath
	}
	if result != nil && result.ResultPath != "" {
		run.ResultPath = &result.ResultPath
	}

	if err := s.store.WithinTx(context.Background(), func(tx *store.TxStore) error {
		if result != nil {
			if err := tx.Runs.UpdateLogArtifacts(context.Background(), runID, run.LogPath, run.ResultPath); err != nil {
				return err
			}
		}

		switch {
		case execErr == nil:
			exitCode := 0
			msg := "run completed"
			if err := tx.Runs.UpdateStatus(context.Background(), runID, model.RunStatusSuccess, run.StartedAt, &finishedAt, &exitCode, nil); err != nil {
				return err
			}
			_, err := tx.Events.Create(context.Background(), &model.RunEvent{
				RunID:     runID,
				EventType: model.RunEventTypeCompleted,
				Message:   &msg,
			})
			return err
		case errors.Is(runCtx.Err(), context.DeadlineExceeded):
			exitCode := -1
			msg := "job timeout"
			if result != nil {
				exitCode = result.ExitCode
			}
			if err := tx.Runs.UpdateStatus(context.Background(), runID, model.RunStatusTimeout, run.StartedAt, &finishedAt, &exitCode, &msg); err != nil {
				return err
			}
			_, err := tx.Events.Create(context.Background(), &model.RunEvent{
				RunID:     runID,
				EventType: model.RunEventTypeTimeout,
				Message:   &msg,
			})
			if err != nil {
				return err
			}
			if retryRun, retryEvent, ok := buildRetryRun(job, run); ok {
				if _, err := tx.Runs.Create(context.Background(), retryRun); err != nil {
					return err
				}
				retryEvent.RunID = retryRun.ID
				if _, err := tx.Events.Create(context.Background(), retryEvent); err != nil {
					return err
				}
			}
			return err
		case errors.Is(runCtx.Err(), context.Canceled):
			msg := "context cancelled"
			if err := tx.Runs.UpdateStatus(context.Background(), runID, model.RunStatusCancelled, run.StartedAt, &finishedAt, nil, &msg); err != nil {
				return err
			}
			_, err := tx.Events.Create(context.Background(), &model.RunEvent{
				RunID:     runID,
				EventType: model.RunEventTypeCancelled,
				Message:   &msg,
			})
			return err
		default:
			exitCode := -1
			if result != nil {
				exitCode = result.ExitCode
			}
			msg := execErr.Error()
			if err := tx.Runs.UpdateStatus(context.Background(), runID, model.RunStatusFailed, run.StartedAt, &finishedAt, &exitCode, &msg); err != nil {
				return err
			}
			_, err := tx.Events.Create(context.Background(), &model.RunEvent{
				RunID:     runID,
				EventType: model.RunEventTypeFailed,
				Message:   &msg,
			})
			if err != nil {
				return err
			}
			if retryRun, retryEvent, ok := buildRetryRun(job, run); ok {
				if _, err := tx.Runs.Create(context.Background(), retryRun); err != nil {
					return err
				}
				retryEvent.RunID = retryRun.ID
				if _, err := tx.Events.Create(context.Background(), retryEvent); err != nil {
					return err
				}
			}
			return err
		}
	}); err != nil {
		log.Printf("scheduler worker finalize error (run=%d): %v", runID, err)
	}

	s.signalDispatch()
}

func buildRetryRun(job *model.Job, currentRun *model.Run) (*model.Run, *model.RunEvent, bool) {
	if job == nil || currentRun == nil {
		return nil, nil, false
	}
	if job.RetryLimit <= 0 {
		return nil, nil, false
	}
	if currentRun.Attempt >= job.RetryLimit {
		return nil, nil, false
	}

	retryRun := &model.Run{
		JobID:       job.ID,
		ScheduledAt: currentRun.ScheduledAt.UTC(),
		Status:      model.RunStatusPending,
		Attempt:     currentRun.Attempt + 1,
	}
	msg := fmt.Sprintf("retry scheduled from run %d attempt %d", currentRun.ID, currentRun.Attempt)
	retryEvent := &model.RunEvent{
		EventType: model.RunEventTypeCreated,
		Message:   &msg,
	}
	return retryRun, retryEvent, true
}
