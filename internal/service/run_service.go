package service

import (
	"context"
	"fmt"
	"time"

	logwriter "github.com/hoonzinope/go-job-runner/internal/log"
	"github.com/hoonzinope/go-job-runner/internal/model"
	"github.com/hoonzinope/go-job-runner/internal/store"
)

type RunService struct {
	store *store.Store
}

func NewRunService(st *store.Store) *RunService {
	return &RunService{store: st}
}

func (s *RunService) ListRuns(ctx context.Context, filter store.RunFilter, page store.Page) ([]model.Run, int64, error) {
	return s.store.Runs.List(ctx, filter, page)
}

func (s *RunService) GetRun(ctx context.Context, id int64) (*model.Run, error) {
	return s.store.Runs.Get(ctx, id)
}

func (s *RunService) CancelRun(ctx context.Context, id int64) (*model.Run, error) {
	run, err := s.store.Runs.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	switch run.Status {
	case model.RunStatusPending:
		run.Status = model.RunStatusCancelled
	case model.RunStatusRunning, model.RunStatusCancelling:
		run.Status = model.RunStatusCancelling
	default:
		return run, nil
	}

	if err := s.store.Runs.UpdateStatus(ctx, run.ID, run.Status, run.StartedAt, run.FinishedAt, run.ExitCode, run.ErrorMessage); err != nil {
		return nil, err
	}
	run.UpdatedAt = time.Now().UTC()
	return run, nil
}

func (s *RunService) ListRunEvents(ctx context.Context, runID int64) ([]model.RunEvent, error) {
	return s.store.Events.ListByRun(ctx, runID)
}

func (s *RunService) ReadLogs(ctx context.Context, runID int64, reader *logwriter.Reader, offset, limit, tail int64) (string, int64, int, error) {
	run, err := s.store.Runs.Get(ctx, runID)
	if err != nil {
		return "", 0, 0, err
	}
	if run.LogPath == nil || *run.LogPath == "" {
		return "", 0, 0, fmt.Errorf("run %d has no log path recorded", runID)
	}
	return reader.ReadContent(*run.LogPath, offset, limit, tail)
}

func (s *RunService) ReadResultPath(ctx context.Context, runID int64) (*string, error) {
	run, err := s.store.Runs.Get(ctx, runID)
	if err != nil {
		return nil, err
	}
	return run.ResultPath, nil
}
