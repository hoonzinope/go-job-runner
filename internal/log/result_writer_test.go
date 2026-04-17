package logwriter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResultWriterPath(t *testing.T) {
	t.Parallel()

	w := NewResultWriter("/tmp/artifacts", "")
	got := w.Path(12, 34)
	want := filepath.Join("/tmp/artifacts", "job-12", "run-34", "result.json")
	if got != want {
		t.Fatalf("path mismatch: got %q want %q", got, want)
	}
}

func TestResultWriterPathUsesPattern(t *testing.T) {
	t.Parallel()

	w := NewResultWriter("/tmp/artifacts", "runs/%d/%d/result.ndjson")
	got := w.Path(12, 34)
	want := filepath.Join("/tmp/artifacts", "runs", "12", "34", "result.ndjson")
	if got != want {
		t.Fatalf("path mismatch: got %q want %q", got, want)
	}
}

func TestResultWriterOpenCreatesFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	w := NewResultWriter(root, "")

	file, path, err := w.Open(7, 9)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer file.Close()

	wantPath := filepath.Join(root, "job-7", "run-9", "result.json")
	if path != wantPath {
		t.Fatalf("path mismatch: got %q want %q", path, wantPath)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat: %v", err)
	}
}
