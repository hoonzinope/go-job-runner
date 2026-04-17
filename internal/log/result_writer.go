package logwriter

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type ResultRecord struct {
	JobID       int64     `json:"jobId"`
	RunID       int64     `json:"runId"`
	ImageRef    string    `json:"imageRef"`
	PullRef     string    `json:"pullRef"`
	ImageDigest *string   `json:"imageDigest,omitempty"`
	ExitCode    int       `json:"exitCode"`
	Message     string    `json:"message"`
	FinishedAt  time.Time `json:"finishedAt"`
}

type ResultWriter struct {
	root    string
	pattern string
}

const defaultResultPattern = "job-%d/run-%d/result.json"

func NewResultWriter(root, pattern string) *ResultWriter {
	if pattern == "" {
		pattern = defaultResultPattern
	}
	return &ResultWriter{root: root, pattern: pattern}
}

func (w *ResultWriter) Path(jobID, runID int64) string {
	return filepath.Join(w.root, fmt.Sprintf(w.pattern, jobID, runID))
}

func (w *ResultWriter) Open(jobID, runID int64) (*os.File, string, error) {
	path := w.Path(jobID, runID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, "", fmt.Errorf("create artifact dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, "", fmt.Errorf("open result file: %w", err)
	}
	return file, path, nil
}
