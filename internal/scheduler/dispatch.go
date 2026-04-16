package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/hoonzinope/go-job-runner/internal/model"
	"github.com/hoonzinope/go-job-runner/internal/store"
)

func (s *Scheduler) dispatchLoop(ctx context.Context) {
	ticker := time.NewTicker(s.dispatchInterval)
	defer ticker.Stop()

	s.processDispatch(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.processDispatch(ctx)
		case <-s.dispatchWakeup:
			s.processDispatch(ctx)
		}
	}
}

func (s *Scheduler) processDispatch(ctx context.Context) {
	for {
		available := cap(s.workerTokens) - len(s.workerTokens)
		if available <= 0 {
			return
		}

		pending, err := s.store.Runs.ListPending(ctx, available)
		if err != nil {
			fmt.Printf("scheduler dispatch scan error: %v\n", err)
			return
		}
		if len(pending) == 0 {
			return
		}

		for i := range pending {
			if !s.acquireWorkerToken(ctx) {
				return
			}
			run := pending[i]
			s.wg.Add(1)
			go func(runID int64) {
				defer s.wg.Done()
				defer s.releaseWorkerToken()
				s.runWorker(ctx, runID)
			}(run.ID)
		}
	}
}

func (s *Scheduler) markRunDispatch(ctx context.Context, tx *store.TxStore, runID int64) error {
	now := time.Now().UTC()
	msg := "run dispatched"
	if err := tx.Runs.UpdateStatus(ctx, runID, model.RunStatusRunning, &now, nil, nil, nil); err != nil {
		return err
	}
	if _, err := tx.Events.Create(ctx, &model.RunEvent{
		RunID:     runID,
		EventType: model.RunEventTypeDispatched,
		Message:   &msg,
	}); err != nil {
		return err
	}
	return nil
}
