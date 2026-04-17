package logwriter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriterPath(t *testing.T) {
	t.Parallel()

	w := NewWriter("/tmp/logs", "")
	got := w.Path(12, 34)
	want := filepath.Join("/tmp/logs", "job-12", "run-34", "run.log")
	if got != want {
		t.Fatalf("path mismatch: got %q want %q", got, want)
	}
}

func TestWriterPathUsesPattern(t *testing.T) {
	t.Parallel()

	w := NewWriter("/tmp/logs", "runs/%d/%d/stdout.log")
	got := w.Path(12, 34)
	want := filepath.Join("/tmp/logs", "runs", "12", "34", "stdout.log")
	if got != want {
		t.Fatalf("path mismatch: got %q want %q", got, want)
	}
}

func TestWriterOpenCreatesFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	w := NewWriter(root, "")

	file, path, err := w.Open(7, 9)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer file.Close()

	wantPath := filepath.Join(root, "job-7", "run-9", "run.log")
	if path != wantPath {
		t.Fatalf("path mismatch: got %q want %q", path, wantPath)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat: %v", err)
	}

	if _, err := file.WriteString("hello"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := string(data); got != "hello" {
		t.Fatalf("content mismatch: got %q want %q", got, "hello")
	}
}
