package model

import "time"

type JobSourceType string

const (
	JobSourceTypeLocal  JobSourceType = "local"
	JobSourceTypeRemote JobSourceType = "remote"
)

func (t JobSourceType) IsValid() bool {
	switch t {
	case JobSourceTypeLocal, JobSourceTypeRemote:
		return true
	default:
		return false
	}
}

type ScheduleType string

const (
	ScheduleTypeCron     ScheduleType = "cron"
	ScheduleTypeInterval ScheduleType = "interval"
)

func (t ScheduleType) IsValid() bool {
	switch t {
	case ScheduleTypeCron, ScheduleTypeInterval:
		return true
	default:
		return false
	}
}

type ConcurrencyPolicy string

const (
	ConcurrencyPolicyAllow  ConcurrencyPolicy = "allow"
	ConcurrencyPolicyForbid ConcurrencyPolicy = "forbid"
)

func (p ConcurrencyPolicy) IsValid() bool {
	switch p {
	case ConcurrencyPolicyAllow, ConcurrencyPolicyForbid:
		return true
	default:
		return false
	}
}

type Job struct {
	ID int64 `json:"id"`

	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	Enabled     bool    `json:"enabled"`

	SourceType   JobSourceType `json:"sourceType"`
	ImageRef     string        `json:"imageRef"`
	ImageDigest  *string       `json:"imageDigest,omitempty"`
	ScheduleType ScheduleType  `json:"scheduleType"`
	ScheduleExpr *string       `json:"scheduleExpr,omitempty"`
	IntervalSec  *int          `json:"intervalSec,omitempty"`
	Timezone     string        `json:"timezone"`

	ConcurrencyPolicy ConcurrencyPolicy `json:"concurrencyPolicy"`
	TimeoutSec        int               `json:"timeoutSec"`
	RetryLimit        int               `json:"retryLimit"`

	NextRunAt       *time.Time `json:"nextRunAt,omitempty"`
	LastScheduledAt *time.Time `json:"lastScheduledAt,omitempty"`

	ParamsJSON *string `json:"paramsJson,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
