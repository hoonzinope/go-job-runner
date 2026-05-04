package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestJobSourceTypeIsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value JobSourceType
		want  bool
	}{
		{name: "local", value: JobSourceTypeLocal, want: true},
		{name: "remote", value: JobSourceTypeRemote, want: true},
		{name: "invalid", value: JobSourceType("bad"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.value.IsValid(); got != tt.want {
				t.Fatalf("unexpected validity: got %v want %v", got, tt.want)
			}
		})
	}
}

func TestScheduleTypeIsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value ScheduleType
		want  bool
	}{
		{name: "cron", value: ScheduleTypeCron, want: true},
		{name: "interval", value: ScheduleTypeInterval, want: true},
		{name: "invalid", value: ScheduleType("bad"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.value.IsValid(); got != tt.want {
				t.Fatalf("unexpected validity: got %v want %v", got, tt.want)
			}
		})
	}
}

func TestConcurrencyPolicyIsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value ConcurrencyPolicy
		want  bool
	}{
		{name: "allow", value: ConcurrencyPolicyAllow, want: true},
		{name: "forbid", value: ConcurrencyPolicyForbid, want: true},
		{name: "invalid", value: ConcurrencyPolicy("bad"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.value.IsValid(); got != tt.want {
				t.Fatalf("unexpected validity: got %v want %v", got, tt.want)
			}
		})
	}
}

func TestRunStatusIsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value RunStatus
		want  bool
	}{
		{name: "pending", value: RunStatusPending, want: true},
		{name: "running", value: RunStatusRunning, want: true},
		{name: "cancelling", value: RunStatusCancelling, want: true},
		{name: "success", value: RunStatusSuccess, want: true},
		{name: "failed", value: RunStatusFailed, want: true},
		{name: "timeout", value: RunStatusTimeout, want: true},
		{name: "cancelled", value: RunStatusCancelled, want: true},
		{name: "skipped", value: RunStatusSkipped, want: true},
		{name: "invalid", value: RunStatus("bad"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.value.IsValid(); got != tt.want {
				t.Fatalf("unexpected validity: got %v want %v", got, tt.want)
			}
		})
	}
}

func TestRunEventTypeIsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value RunEventType
		want  bool
	}{
		{name: "created", value: RunEventTypeCreated, want: true},
		{name: "dispatched", value: RunEventTypeDispatched, want: true},
		{name: "started", value: RunEventTypeStarted, want: true},
		{name: "skipped", value: RunEventTypeSkipped, want: true},
		{name: "completed", value: RunEventTypeCompleted, want: true},
		{name: "failed", value: RunEventTypeFailed, want: true},
		{name: "timeout", value: RunEventTypeTimeout, want: true},
		{name: "cancelled", value: RunEventTypeCancelled, want: true},
		{name: "invalid", value: RunEventType("bad"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.value.IsValid(); got != tt.want {
				t.Fatalf("unexpected validity: got %v want %v", got, tt.want)
			}
		})
	}
}

func TestJobJSONEncoding(t *testing.T) {
	t.Parallel()

	desc := "job description"
	digest := "sha256:abc"
	expr := "*/5 * * * *"
	interval := 60
	params := `{"foo":"bar"}`
	nextRunAt := time.Date(2026, 4, 17, 12, 1, 0, 0, time.UTC)
	lastScheduledAt := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	job := Job{
		ID:                7,
		Name:              "example",
		Description:       &desc,
		Enabled:           true,
		SourceType:        JobSourceTypeLocal,
		ImageRef:          "jobs/example:latest",
		ImageDigest:       &digest,
		ScheduleType:      ScheduleTypeCron,
		ScheduleExpr:      &expr,
		IntervalSec:       &interval,
		Timezone:          "UTC",
		ConcurrencyPolicy: ConcurrencyPolicyForbid,
		TimeoutSec:        30,
		RetryLimit:        2,
		NextRunAt:         &nextRunAt,
		LastScheduledAt:   &lastScheduledAt,
		ParamsJSON:        &params,
		CreatedAt:         time.Date(2026, 4, 17, 11, 0, 0, 0, time.UTC),
		UpdatedAt:         time.Date(2026, 4, 17, 11, 30, 0, 0, time.UTC),
	}

	data, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("marshal job: %v", err)
	}
	assertJSONContains(t, data, `"sourceType":"local"`)
	assertJSONContains(t, data, `"scheduleType":"cron"`)
	assertJSONContains(t, data, `"concurrencyPolicy":"forbid"`)
	assertJSONContains(t, data, `"paramsJson":"{\"foo\":\"bar\"}"`)
	assertJSONContains(t, data, `"lastScheduledAt":"2026-04-17T12:00:00Z"`)
}

func TestRunJSONEncoding(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 4, 17, 12, 10, 0, 0, time.UTC)
	finishedAt := time.Date(2026, 4, 17, 12, 20, 0, 0, time.UTC)
	exitCode := 0
	msg := "done"
	logPath := "/tmp/run.log"
	resultPath := "/tmp/result.json"
	run := Run{
		ID:           10,
		JobID:        7,
		ScheduledAt:  time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC),
		StartedAt:    &startedAt,
		FinishedAt:   &finishedAt,
		Status:       RunStatusSuccess,
		Attempt:      1,
		ExitCode:     &exitCode,
		ErrorMessage: &msg,
		LogPath:      &logPath,
		ResultPath:   &resultPath,
		CreatedAt:    time.Date(2026, 4, 17, 11, 0, 0, 0, time.UTC),
		UpdatedAt:    time.Date(2026, 4, 17, 11, 30, 0, 0, time.UTC),
	}

	data, err := json.Marshal(run)
	if err != nil {
		t.Fatalf("marshal run: %v", err)
	}
	assertJSONContains(t, data, `"jobId":7`)
	assertJSONContains(t, data, `"status":"success"`)
	assertJSONContains(t, data, `"errorMessage":"done"`)
	assertJSONContains(t, data, `"resultPath":"/tmp/result.json"`)
}

func TestRunEventJSONEncoding(t *testing.T) {
	t.Parallel()

	msg := "created"
	event := RunEvent{
		ID:        3,
		RunID:     10,
		EventType: RunEventTypeCreated,
		Message:   &msg,
		CreatedAt: time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	assertJSONContains(t, data, `"runId":10`)
	assertJSONContains(t, data, `"eventType":"created"`)
	assertJSONContains(t, data, `"message":"created"`)
}

func TestOptionalFieldsOmittedWhenNil(t *testing.T) {
	t.Parallel()

	job := Job{
		ID:                1,
		Name:              "minimal",
		Enabled:           true,
		SourceType:        JobSourceTypeLocal,
		ImageRef:          "jobs/example:latest",
		ScheduleType:      ScheduleTypeInterval,
		Timezone:          "UTC",
		ConcurrencyPolicy: ConcurrencyPolicyAllow,
		TimeoutSec:        1,
		RetryLimit:        0,
		CreatedAt:         time.Date(2026, 4, 17, 11, 0, 0, 0, time.UTC),
		UpdatedAt:         time.Date(2026, 4, 17, 11, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("marshal job: %v", err)
	}
	assertJSONNotContains(t, data, "description")
	assertJSONNotContains(t, data, "imageDigest")
	assertJSONNotContains(t, data, "scheduleExpr")
	assertJSONNotContains(t, data, "intervalSec")
	assertJSONNotContains(t, data, "nextRunAt")
	assertJSONNotContains(t, data, "lastScheduledAt")
	assertJSONNotContains(t, data, "paramsJson")
}

func assertJSONContains(t *testing.T, data []byte, substr string) {
	t.Helper()

	if !containsBytes(data, []byte(substr)) {
		t.Fatalf("expected JSON to contain %s, got %s", substr, string(data))
	}
}

func assertJSONNotContains(t *testing.T, data []byte, substr string) {
	t.Helper()

	if containsBytes(data, []byte(substr)) {
		t.Fatalf("expected JSON to not contain %s, got %s", substr, string(data))
	}
}

func containsBytes(data, substr []byte) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i+len(substr) <= len(data); i++ {
		if string(data[i:i+len(substr)]) == string(substr) {
			return true
		}
	}
	return false
}
