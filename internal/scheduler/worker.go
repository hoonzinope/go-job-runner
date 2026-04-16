package scheduler

import (
	"context"
	"fmt"
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
	if err := s.store.WithinTx(ctx, func(tx *store.TxStore) error {
		freshRun, err := tx.Runs.Get(ctx, runID)
		if err != nil {
			return err
		}

		switch freshRun.Status {
		case model.RunStatusCancelled:
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
		fmt.Printf("scheduler worker start error (run=%d): %v\n", runID, err)
		return
	}

	if ctx.Err() != nil {
		finishedAt := time.Now().UTC()
		msg := "context cancelled"
		_ = s.store.WithinTx(context.Background(), func(tx *store.TxStore) error {
			if err := tx.Runs.UpdateStatus(context.Background(), runID, model.RunStatusCancelled, run.StartedAt, &finishedAt, run.ExitCode, run.ErrorMessage); err != nil {
				return err
			}
			_, err := tx.Events.Create(context.Background(), &model.RunEvent{
				RunID:     runID,
				EventType: model.RunEventTypeCancelled,
				Message:   &msg,
			})
			return err
		})
		s.signalDispatch()
		return
	}

	result, execErr := s.newExecutor().Execute(ctx, job, run)
	finishedAt := time.Now().UTC()

	if result != nil && result.LogPath != "" {
		run.LogPath = &result.LogPath
	}
	if result != nil && result.ResultPath != "" {
		run.ResultPath = &result.ResultPath
	}
	if result != nil && result.ImageDigest != nil {
		job.ImageDigest = result.ImageDigest
	}

	_ = s.store.WithinTx(context.Background(), func(tx *store.TxStore) error {
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
		case ctx.Err() != nil:
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
			status := model.RunStatusFailed
			if isTimeoutError(execErr) {
				status = model.RunStatusTimeout
			}
			if err := tx.Runs.UpdateStatus(context.Background(), runID, status, run.StartedAt, &finishedAt, &exitCode, &msg); err != nil {
				return err
			}
			eventType := model.RunEventTypeFailed
			if status == model.RunStatusTimeout {
				eventType = model.RunEventTypeTimeout
			}
			_, err := tx.Events.Create(context.Background(), &model.RunEvent{
				RunID:     runID,
				EventType: eventType,
				Message:   &msg,
			})
			return err
		}
	})

	s.signalDispatch()
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	return err == context.DeadlineExceeded
}
