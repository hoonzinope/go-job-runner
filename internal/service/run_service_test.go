package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	logwriter "github.com/hoonzinope/go-job-runner/internal/log"
	"github.com/hoonzinope/go-job-runner/internal/model"
)

func TestRunServiceCancelRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		initial model.RunStatus
		want    model.RunStatus
	}{
		{name: "pending", initial: model.RunStatusPending, want: model.RunStatusCancelled},
		{name: "running", initial: model.RunStatusRunning, want: model.RunStatusCancelling},
		{name: "success no-op", initial: model.RunStatusSuccess, want: model.RunStatusSuccess},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st := openServiceTestStore(t)
			svc := NewRunService(st)

			job := baseIntervalJob("cancel-job-"+tt.name, 60)
			if _, err := st.Jobs.Create(context.Background(), job); err != nil {
				t.Fatalf("create job: %v", err)
			}

			run := &model.Run{
				JobID:       job.ID,
				ScheduledAt: time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC),
				Status:      tt.initial,
				Attempt:     0,
			}
			if _, err := st.Runs.Create(context.Background(), run); err != nil {
				t.Fatalf("create run: %v", err)
			}

			updated, err := svc.CancelRun(context.Background(), run.ID)
			if err != nil {
				t.Fatalf("cancel run: %v", err)
			}
			if updated.Status != tt.want {
				t.Fatalf("unexpected status: got %s want %s", updated.Status, tt.want)
			}

			stored, err := st.Runs.Get(context.Background(), run.ID)
			if err != nil {
				t.Fatalf("get run: %v", err)
			}
			if stored.Status != tt.want {
				t.Fatalf("stored status mismatch: got %s want %s", stored.Status, tt.want)
			}
		})
	}
}

func TestRunServiceReadLogsAndResultPath(t *testing.T) {
	t.Parallel()

	st := openServiceTestStore(t)
	svc := NewRunService(st)

	job := baseIntervalJob("artifact-job", 60)
	if _, err := st.Jobs.Create(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	logPath := filepath.Join(t.TempDir(), "run.log")
	if err := os.WriteFile(logPath, []byte("abcdef"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	resultPath := filepath.Join(t.TempDir(), "result.json")
	if err := os.WriteFile(resultPath, []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatalf("write result: %v", err)
	}
	run := &model.Run{
		JobID:       job.ID,
		ScheduledAt: time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC),
		Status:      model.RunStatusSuccess,
		Attempt:     0,
		LogPath:     &logPath,
		ResultPath:  &resultPath,
	}
	if _, err := st.Runs.Create(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	content, offset, size, err := svc.ReadLogs(context.Background(), run.ID, logwriter.NewReader(), 1, 4, 0)
	if err != nil {
		t.Fatalf("read logs: %v", err)
	}
	if content != "bcde" || offset != 1 || size != 4 {
		t.Fatalf("unexpected logs payload: content=%q offset=%d size=%d", content, offset, size)
	}

	gotPath, err := svc.ReadResultPath(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("read result path: %v", err)
	}
	if gotPath == nil || *gotPath != resultPath {
		t.Fatalf("unexpected result path: %+v", gotPath)
	}
}

func TestRunServiceReadLogsMissingPath(t *testing.T) {
	t.Parallel()

	st := openServiceTestStore(t)
	svc := NewRunService(st)

	job := baseIntervalJob("artifact-job-missing-log", 60)
	if _, err := st.Jobs.Create(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	run := &model.Run{
		JobID:       job.ID,
		ScheduledAt: time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC),
		Status:      model.RunStatusSuccess,
		Attempt:     0,
	}
	if _, err := st.Runs.Create(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if _, _, _, err := svc.ReadLogs(context.Background(), run.ID, logwriter.NewReader(), 0, 0, 0); err == nil {
		t.Fatal("expected missing log path error")
	}
}
