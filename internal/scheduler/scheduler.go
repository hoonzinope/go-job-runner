package scheduler

import (
	"context"
	"sync"
	"time"

	"github.com/hoonzinope/go-job-runner/internal/config"
	"github.com/hoonzinope/go-job-runner/internal/executor"
	"github.com/hoonzinope/go-job-runner/internal/model"
	"github.com/hoonzinope/go-job-runner/internal/store"
)

type Executor interface {
	Execute(ctx context.Context, job *model.Job, run *model.Run) (*executor.ExecutionResult, error)
}

type noopExecutor struct{}

func (noopExecutor) Execute(ctx context.Context, job *model.Job, run *model.Run) (*executor.ExecutionResult, error) {
	return &executor.ExecutionResult{
		ExitCode: 0,
	}, nil
}

type Scheduler struct {
	store *store.Store

	dueJobInterval    time.Duration
	dispatchInterval  time.Duration
	maxConcurrentRuns int

	dueWakeup      chan struct{}
	dispatchWakeup chan struct{}
	workerTokens   chan struct{}

	executor Executor
	wg       sync.WaitGroup
}

func NewScheduler(cfg *config.Config, st *store.Store) *Scheduler {
	return &Scheduler{
		store:             st,
		dueJobInterval:    time.Duration(cfg.Scheduler.DueJobScanIntervalSec) * time.Second,
		dispatchInterval:  time.Duration(cfg.Scheduler.DispatchScanIntervalSec) * time.Second,
		maxConcurrentRuns: cfg.Scheduler.MaxConcurrentRuns,
		dueWakeup:         make(chan struct{}, 1),
		dispatchWakeup:    make(chan struct{}, 1),
		workerTokens:      make(chan struct{}, cfg.Scheduler.MaxConcurrentRuns),
		executor:          executor.NewDockerExecutor(cfg.Store, cfg.Image, cfg.Executor),
	}
}

func (s *Scheduler) Start(ctx context.Context) error {
	s.wg.Add(2)
	go func() {
		defer s.wg.Done()
		s.dueJobLoop(ctx)
	}()
	go func() {
		defer s.wg.Done()
		s.dispatchLoop(ctx)
	}()

	<-ctx.Done()
	s.wg.Wait()
	return nil
}

func (s *Scheduler) NotifyDueJob() {
	select {
	case s.dueWakeup <- struct{}{}:
	default:
	}
}

func (s *Scheduler) NotifyDispatch() {
	select {
	case s.dispatchWakeup <- struct{}{}:
	default:
	}
}

func (s *Scheduler) signalDispatch() {
	s.NotifyDispatch()
}

func (s *Scheduler) signalDueJob() {
	s.NotifyDueJob()
}

func (s *Scheduler) newExecutor() Executor {
	if s.executor == nil {
		return noopExecutor{}
	}
	return s.executor
}

func (s *Scheduler) setExecutor(executor Executor) {
	if executor == nil {
		s.executor = noopExecutor{}
		return
	}
	s.executor = executor
}

func (s *Scheduler) maxWorkers() int {
	if s.maxConcurrentRuns <= 0 {
		return 1
	}
	return s.maxConcurrentRuns
}

func (s *Scheduler) acquireWorkerToken(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	select {
	case s.workerTokens <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Scheduler) releaseWorkerToken() {
	select {
	case <-s.workerTokens:
	default:
	}
}
