package model

import "time"

type RunEventType string

const (
	RunEventTypeCreated    RunEventType = "created"
	RunEventTypeDispatched RunEventType = "dispatched"
	RunEventTypeStarted    RunEventType = "started"
	RunEventTypeSkipped    RunEventType = "skipped"
	RunEventTypeCompleted  RunEventType = "completed"
	RunEventTypeFailed     RunEventType = "failed"
	RunEventTypeTimeout    RunEventType = "timeout"
	RunEventTypeCancelled  RunEventType = "cancelled"
)

func (t RunEventType) IsValid() bool {
	switch t {
	case RunEventTypeCreated, RunEventTypeDispatched, RunEventTypeStarted, RunEventTypeSkipped, RunEventTypeCompleted, RunEventTypeFailed, RunEventTypeTimeout, RunEventTypeCancelled:
		return true
	default:
		return false
	}
}

type RunEvent struct {
	ID int64 `json:"id"`

	RunID     int64        `json:"runId"`
	EventType RunEventType `json:"eventType"`
	Message   *string      `json:"message,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
}
