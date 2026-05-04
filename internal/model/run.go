package model

import "time"

type RunStatus string

const (
	RunStatusPending    RunStatus = "pending"
	RunStatusRunning    RunStatus = "running"
	RunStatusCancelling RunStatus = "cancelling"
	RunStatusSuccess    RunStatus = "success"
	RunStatusFailed     RunStatus = "failed"
	RunStatusTimeout    RunStatus = "timeout"
	RunStatusCancelled  RunStatus = "cancelled"
	RunStatusSkipped    RunStatus = "skipped"
)

func (s RunStatus) IsValid() bool {
	switch s {
	case RunStatusPending, RunStatusRunning, RunStatusCancelling, RunStatusSuccess, RunStatusFailed, RunStatusTimeout, RunStatusCancelled, RunStatusSkipped:
		return true
	default:
		return false
	}
}

type Run struct {
	ID int64 `json:"id"`

	JobID int64 `json:"jobId"`

	ScheduledAt time.Time  `json:"scheduledAt"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`

	Status  RunStatus `json:"status"`
	Attempt int       `json:"attempt"`

	ExitCode     *int    `json:"exitCode,omitempty"`
	ErrorMessage *string `json:"errorMessage,omitempty"`

	LogPath    *string `json:"logPath,omitempty"`
	ResultPath *string `json:"resultPath,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
