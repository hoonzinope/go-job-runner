package scheduler

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/hoonzinope/go-job-runner/internal/config"
	"github.com/hoonzinope/go-job-runner/internal/executor"
	"github.com/hoonzinope/go-job-runner/internal/model"
	"github.com/hoonzinope/go-job-runner/internal/retention"
	"github.com/hoonzinope/go-job-runner/internal/store"
)

type Executor interface {
	Execute(ctx context.Context, job *model.Job, run *model.Run) (*executor.ExecutionResult, error)
}

type orphanRecoverer interface {
	RecoverOrphans(context.Context) error
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
	defaultTimeoutSec int
	maxTimeoutSec     int
	allowUnlimited    bool
	retentionEnabled  bool
	retentionInterval time.Duration

	dueWakeup      chan struct{}
	dispatchWakeup chan struct{}
	workerTokens   chan struct{}

	executor Executor
	pruner   *retention.Pruner
	wg       sync.WaitGroup
}

func NewScheduler(cfg *config.Config, st *store.Store) *Scheduler {
	return &Scheduler{
		store:             st,
		dueJobInterval:    time.Duration(cfg.Scheduler.DueJobScanIntervalSec) * time.Second,
		dispatchInterval:  time.Duration(cfg.Scheduler.DispatchScanIntervalSec) * time.Second,
		maxConcurrentRuns: cfg.Scheduler.MaxConcurrentRuns,
		defaultTimeoutSec: cfg.Scheduler.DefaultTimeoutSec,
		maxTimeoutSec:     cfg.Scheduler.MaxTimeoutSec,
		allowUnlimited:    cfg.Scheduler.AllowUnlimitedTimeout,
		retentionEnabled:  cfg.Retention.Enabled,
		retentionInterval: time.Duration(cfg.Retention.PruneIntervalSec) * time.Second,
		dueWakeup:         make(chan struct{}, 1),
		dispatchWakeup:    make(chan struct{}, 1),
		workerTokens:      make(chan struct{}, cfg.Scheduler.MaxConcurrentRuns),
		executor:          executor.NewDockerExecutor(cfg.Store, cfg.Image, cfg.Executor),
		pruner:            retention.NewPruner(*cfg, st),
	}
}

func (s *Scheduler) Start(ctx context.Context) error {
	if recoverer, ok := s.executor.(orphanRecoverer); ok {
		if err := recoverer.RecoverOrphans(ctx); err != nil {
			return err
		}
	}

	if s.retentionEnabled && s.pruner != nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.retentionLoop(ctx)
		}()
	}

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

func (s *Scheduler) setPruner(pruner *retention.Pruner) {
	s.pruner = pruner
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

func (s *Scheduler) retentionLoop(ctx context.Context) {
	s.runRetention(ctx)
	interval := s.retentionInterval
	if interval <= 0 {
		interval = time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runRetention(ctx)
		}
	}
}

func (s *Scheduler) runRetention(ctx context.Context) {
	report, err := s.pruner.Run(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("retention prune failed: %v", err)
		}
		return
	}
	if report.DeletedRuns > 0 || report.DeletedLogFiles > 0 || report.DeletedArtifacts > 0 || report.TruncatedLogFiles > 0 || report.FreedBytes > 0 {
		log.Printf("retention pruned runs=%d logs=%d artifacts=%d truncated_logs=%d freed_bytes=%d",
			report.DeletedRuns, report.DeletedLogFiles, report.DeletedArtifacts, report.TruncatedLogFiles, report.FreedBytes)
	}
}
