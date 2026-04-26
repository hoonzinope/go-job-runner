package handler

import (
	"encoding/json"
	"time"

	"github.com/hoonzinope/go-job-runner/internal/model"
)

type listResponse[T any] struct {
	Items []T   `json:"items"`
	Total int64 `json:"total"`
	Page  int   `json:"page"`
	Size  int   `json:"size"`
}

type jobRequest struct {
	Name              string                  `json:"name"`
	Description       *string                 `json:"description"`
	Enabled           bool                    `json:"enabled"`
	SourceType        model.JobSourceType     `json:"sourceType"`
	ImageRef          string                  `json:"imageRef"`
	ImageDigest       *string                 `json:"imageDigest"`
	ScheduleType      model.ScheduleType      `json:"scheduleType"`
	ScheduleExpr      *string                 `json:"scheduleExpr"`
	IntervalSec       *int                    `json:"intervalSec"`
	Params            json.RawMessage         `json:"params"`
	ConcurrencyPolicy model.ConcurrencyPolicy `json:"concurrencyPolicy"`
	RetryLimit        int                     `json:"retryLimit"`
	TimeoutSec        *int                    `json:"timeoutSec"`
	Timezone          string                  `json:"timezone"`
}

type jobResponse struct {
	ID int64 `json:"id"`

	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	Enabled     bool    `json:"enabled"`

	SourceType   model.JobSourceType `json:"sourceType"`
	ImageRef     string              `json:"imageRef"`
	ImageDigest  *string             `json:"imageDigest,omitempty"`
	ScheduleType model.ScheduleType  `json:"scheduleType"`
	ScheduleExpr *string             `json:"scheduleExpr,omitempty"`
	IntervalSec  *int                `json:"intervalSec,omitempty"`
	Params       json.RawMessage     `json:"params,omitempty"`

	Timezone          string                  `json:"timezone"`
	ConcurrencyPolicy model.ConcurrencyPolicy `json:"concurrencyPolicy"`
	RetryLimit        int                     `json:"retryLimit"`
	TimeoutSec        int                     `json:"timeoutSec"`

	NextRunAt       *time.Time `json:"nextRunAt,omitempty"`
	LastScheduledAt *time.Time `json:"lastScheduledAt,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type runResponse struct {
	ID int64 `json:"id"`

	JobID int64 `json:"jobId"`

	ScheduledAt time.Time  `json:"scheduledAt"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`

	Status  model.RunStatus `json:"status"`
	Attempt int             `json:"attempt"`

	ExitCode     *int    `json:"exitCode,omitempty"`
	ErrorMessage *string `json:"errorMessage,omitempty"`
	LogPath      *string `json:"logPath,omitempty"`
	ResultPath   *string `json:"resultPath,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type runEventResponse struct {
	ID int64 `json:"id"`

	RunID     int64              `json:"runId"`
	EventType model.RunEventType `json:"eventType"`
	Message   *string            `json:"message,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
}

type logResponse struct {
	RunID   int64  `json:"runId"`
	Offset  int64  `json:"offset"`
	Size    int    `json:"size"`
	Content string `json:"content"`
}

type resultResponse struct {
	JobID       int64     `json:"jobId"`
	RunID       int64     `json:"runId"`
	ImageRef    string    `json:"imageRef"`
	PullRef     string    `json:"pullRef"`
	ImageDigest *string   `json:"imageDigest,omitempty"`
	ExitCode    int       `json:"exitCode"`
	Message     string    `json:"message"`
	FinishedAt  time.Time `json:"finishedAt"`
}

type imageCandidateResponse struct {
	SourceType string  `json:"sourceType"`
	ImageRef   string  `json:"imageRef"`
	Digest     *string `json:"digest,omitempty"`
}

type imageResolveResponse struct {
	SourceType string  `json:"sourceType"`
	ImageRef   string  `json:"imageRef"`
	Digest     *string `json:"digest,omitempty"`
	Resolved   bool    `json:"resolved"`
}
