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
			startedAt := time.Now().UTC()
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
			return nil
		default:
			return nil
		}
	}); err != nil {
		fmt.Printf("scheduler worker start error (run=%d): %v\n", runID, err)
		return
	}

	if ctx.Err() != nil {
		_ = s.store.WithinTx(context.Background(), func(tx *store.TxStore) error {
			finishedAt := time.Now().UTC()
			msg := "context cancelled"
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

	if err := s.newExecutor().Execute(ctx, job.ID, run.ID); err != nil {
		finishedAt := time.Now().UTC()
		msg := err.Error()
		_ = s.store.WithinTx(context.Background(), func(tx *store.TxStore) error {
			if err := tx.Runs.UpdateStatus(context.Background(), runID, model.RunStatusFailed, run.StartedAt, &finishedAt, nil, &msg); err != nil {
				return err
			}
			_, err := tx.Events.Create(context.Background(), &model.RunEvent{
				RunID:     runID,
				EventType: model.RunEventTypeFailed,
				Message:   &msg,
			})
			return err
		})
		s.signalDispatch()
		return
	}

	finishedAt := time.Now().UTC()
	exitCode := 0
	msg := "run completed"
	_ = s.store.WithinTx(context.Background(), func(tx *store.TxStore) error {
		if err := tx.Runs.UpdateStatus(context.Background(), runID, model.RunStatusSuccess, run.StartedAt, &finishedAt, &exitCode, nil); err != nil {
			return err
		}
		_, err := tx.Events.Create(context.Background(), &model.RunEvent{
			RunID:     runID,
			EventType: model.RunEventTypeCompleted,
			Message:   &msg,
		})
		return err
	})
	s.signalDispatch()
}
