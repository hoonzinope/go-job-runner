package retention

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hoonzinope/go-job-runner/internal/config"
	"github.com/hoonzinope/go-job-runner/internal/model"
	"github.com/hoonzinope/go-job-runner/internal/store"
)

func TestPrunerDeletesOnlyTerminalExpiredData(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg, st := newRetentionTestStore(t)
	cfg.Retention.RunHistoryDays = 30
	cfg.Retention.SuccessLogDays = 7
	cfg.Retention.FailedLogDays = 30
	cfg.Retention.ArtifactDays = 14
	cfg.Retention.MaxLogBytesPerRun = 0
	cfg.Retention.MaxTotalStorageBytes = 0

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	oldFinished := now.AddDate(0, 0, -40)
	recentFinished := now.AddDate(0, 0, -2)

	oldLog := writeRetentionFile(t, cfg.Store.LogRoot, "old.log", "old log")
	oldArtifact := writeRetentionFile(t, cfg.Store.ArtifactRoot, "old.json", "{}")
	oldRunID := createRetentionRun(t, ctx, st, model.RunStatusSuccess, oldFinished, &oldLog, &oldArtifact)
	if _, err := st.Events.Create(ctx, &model.RunEvent{RunID: oldRunID, EventType: model.RunEventTypeCreated}); err != nil {
		t.Fatalf("create old event: %v", err)
	}

	recentLog := writeRetentionFile(t, cfg.Store.LogRoot, "recent.log", "recent log")
	recentArtifact := writeRetentionFile(t, cfg.Store.ArtifactRoot, "recent.json", "{}")
	recentRunID := createRetentionRun(t, ctx, st, model.RunStatusRunning, recentFinished, &recentLog, &recentArtifact)

	pruner := NewPruner(cfg, st)
	pruner.now = func() time.Time { return now }
	report, err := pruner.Run(ctx)
	if err != nil {
		t.Fatalf("run pruner: %v", err)
	}
	if report.DeletedRuns != 1 || report.DeletedLogFiles != 1 || report.DeletedArtifacts != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if _, err := os.Stat(oldLog); !os.IsNotExist(err) {
		t.Fatalf("expected old log deleted, got %v", err)
	}
	if _, err := os.Stat(oldArtifact); !os.IsNotExist(err) {
		t.Fatalf("expected old artifact deleted, got %v", err)
	}
	if _, err := st.Runs.Get(ctx, oldRunID); err == nil {
		t.Fatal("expected old terminal run deleted")
	}
	if _, err := st.Runs.Get(ctx, recentRunID); err != nil {
		t.Fatalf("running run should remain: %v", err)
	}
	if _, err := os.Stat(recentLog); err != nil {
		t.Fatalf("running log should remain: %v", err)
	}
	if _, err := os.Stat(recentArtifact); err != nil {
		t.Fatalf("running artifact should remain: %v", err)
	}
}

func TestPrunerTruncatesOversizedTerminalLogs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg, st := newRetentionTestStore(t)
	cfg.Retention.RunHistoryDays = 0
	cfg.Retention.SuccessLogDays = 0
	cfg.Retention.FailedLogDays = 0
	cfg.Retention.ArtifactDays = 0
	cfg.Retention.MaxLogBytesPerRun = 4
	cfg.Retention.MaxTotalStorageBytes = 0

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	logPath := writeRetentionFile(t, cfg.Store.LogRoot, "large.log", "123456789")
	createRetentionRun(t, ctx, st, model.RunStatusFailed, now.Add(-time.Hour), &logPath, nil)

	pruner := NewPruner(cfg, st)
	pruner.now = func() time.Time { return now }
	report, err := pruner.Run(ctx)
	if err != nil {
		t.Fatalf("run pruner: %v", err)
	}
	if report.TruncatedLogFiles != 1 || report.FreedBytes != 5 {
		t.Fatalf("unexpected report: %+v", report)
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat truncated log: %v", err)
	}
	if info.Size() != 4 {
		t.Fatalf("unexpected truncated size: %d", info.Size())
	}
}

func TestPrunerRemovesFilesWhenHistoryIsDeletedFirst(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg, st := newRetentionTestStore(t)
	cfg.Retention.RunHistoryDays = 1
	cfg.Retention.SuccessLogDays = 30
	cfg.Retention.FailedLogDays = 30
	cfg.Retention.ArtifactDays = 30
	cfg.Retention.MaxLogBytesPerRun = 0
	cfg.Retention.MaxTotalStorageBytes = 0

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	logPath := writeRetentionFile(t, cfg.Store.LogRoot, "history.log", "log")
	artifactPath := writeRetentionFile(t, cfg.Store.ArtifactRoot, "history.json", "{}")
	createRetentionRun(t, ctx, st, model.RunStatusSuccess, now.AddDate(0, 0, -2), &logPath, &artifactPath)

	pruner := NewPruner(cfg, st)
	pruner.now = func() time.Time { return now }
	report, err := pruner.Run(ctx)
	if err != nil {
		t.Fatalf("run pruner: %v", err)
	}
	if report.DeletedRuns != 1 || report.DeletedLogFiles != 1 || report.DeletedArtifacts != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("expected log deleted with run history, got %v", err)
	}
	if _, err := os.Stat(artifactPath); !os.IsNotExist(err) {
		t.Fatalf("expected artifact deleted with run history, got %v", err)
	}
}

func newRetentionTestStore(t *testing.T) (config.Config, *store.Store) {
	t.Helper()

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	cfg := config.Config{
		Store: config.StoreConfig{
			LogRoot:      filepath.Join(dir, "logs"),
			ArtifactRoot: filepath.Join(dir, "artifacts"),
		},
		Retention: config.RetentionConfig{
			Enabled:          true,
			PruneIntervalSec: 3600,
		},
	}
	return cfg, st
}

func createRetentionRun(t *testing.T, ctx context.Context, st *store.Store, status model.RunStatus, finishedAt time.Time, logPath, resultPath *string) int64 {
	t.Helper()

	jobID, err := st.Jobs.Create(ctx, retentionTestJob("job-"+finishedAt.Format("20060102150405")))
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	run := &model.Run{
		JobID:       jobID,
		ScheduledAt: finishedAt.Add(-time.Minute),
		StartedAt:   ptrTime(finishedAt.Add(-30 * time.Second)),
		FinishedAt:  &finishedAt,
		Status:      status,
		LogPath:     logPath,
		ResultPath:  resultPath,
		CreatedAt:   finishedAt.Add(-time.Minute),
		UpdatedAt:   finishedAt,
	}
	id, err := st.Runs.Create(ctx, run)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	return id
}

func retentionTestJob(name string) *model.Job {
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	return &model.Job{
		Name:              name,
		Enabled:           true,
		SourceType:        model.JobSourceTypeLocal,
		ImageRef:          "jobs/example:latest",
		ScheduleType:      model.ScheduleTypeCron,
		ScheduleExpr:      ptrString("* * * * *"),
		Timezone:          "UTC",
		ConcurrencyPolicy: model.ConcurrencyPolicyForbid,
		TimeoutSec:        30,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func writeRetentionFile(t *testing.T, root, name, content string) string {
	t.Helper()

	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	return path
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func ptrString(v string) *string {
	return &v
}
