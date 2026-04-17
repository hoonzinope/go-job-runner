package logwriter

import (
	"fmt"
	"os"
	"path/filepath"
)

const defaultLogPattern = "job-%d/run-%d/run.log"

type Writer struct {
	root    string
	pattern string
}

func NewWriter(root, pattern string) *Writer {
	if pattern == "" {
		pattern = defaultLogPattern
	}
	return &Writer{root: root, pattern: pattern}
}

func (w *Writer) Path(jobID, runID int64) string {
	return filepath.Join(w.root, fmt.Sprintf(w.pattern, jobID, runID))
}

func (w *Writer) Open(jobID, runID int64) (*os.File, string, error) {
	path := w.Path(jobID, runID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, "", fmt.Errorf("create log dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, "", fmt.Errorf("open log file: %w", err)
	}
	return file, path, nil
}
