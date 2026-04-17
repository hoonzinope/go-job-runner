package logwriter

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReaderReadContent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "run.log")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	reader := NewReader()
	content, start, size, err := reader.ReadContent(path, 6, 5, 0)
	if err != nil {
		t.Fatalf("read content: %v", err)
	}
	if start != 6 {
		t.Fatalf("start mismatch: got %d want %d", start, 6)
	}
	if size != 5 {
		t.Fatalf("size mismatch: got %d want %d", size, 5)
	}
	if content != "world" {
		t.Fatalf("content mismatch: got %q want %q", content, "world")
	}
}

func TestReaderReadJSON(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "result.json")
	want := ResultRecord{
		JobID:      10,
		RunID:      20,
		ImageRef:   "jobs/example:latest",
		PullRef:    "registry.local/jobs/example:latest",
		ExitCode:   0,
		Message:    "success",
		FinishedAt: time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC),
	}
	payload := []byte(`{
  "jobId": 10,
  "runId": 20,
  "imageRef": "jobs/example:latest",
  "pullRef": "registry.local/jobs/example:latest",
  "exitCode": 0,
  "message": "success",
  "finishedAt": "2026-04-17T12:00:00Z"
}`)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var got ResultRecord
	reader := NewReader()
	if err := reader.ReadJSON(path, &got); err != nil {
		t.Fatalf("read json: %v", err)
	}
	if got.JobID != want.JobID || got.RunID != want.RunID || got.ImageRef != want.ImageRef || got.PullRef != want.PullRef || got.ExitCode != want.ExitCode || got.Message != want.Message || !got.FinishedAt.Equal(want.FinishedAt) {
		t.Fatalf("record mismatch: got %+v want %+v", got, want)
	}
}
