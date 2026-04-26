package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoonzinope/go-job-runner/internal/config"
	"github.com/hoonzinope/go-job-runner/internal/image"
	"github.com/hoonzinope/go-job-runner/internal/model"
	"github.com/hoonzinope/go-job-runner/internal/service"
	"github.com/hoonzinope/go-job-runner/internal/store"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestHandlerHelpers(t *testing.T) {
	t.Parallel()

	t.Run("parsePageQuery", func(t *testing.T) {
		t.Parallel()

		makeContext := func(rawURL string) *gin.Context {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, rawURL, nil)
			return c
		}

		page, size := parsePageQuery(makeContext("/?page=3&size=7"))
		if page != 3 || size != 7 {
			t.Fatalf("unexpected page query result: %d %d", page, size)
		}

		page, size = parsePageQuery(makeContext("/?page=-1&size=bad"))
		if page != 1 || size != 20 {
			t.Fatalf("unexpected fallback page query result: %d %d", page, size)
		}
	})

	t.Run("parseBoolPtr", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			value   string
			want    *bool
			wantErr bool
		}{
			{value: "", want: nil, wantErr: false},
			{value: "true", want: boolPtr(true), wantErr: false},
			{value: "false", want: boolPtr(false), wantErr: false},
			{value: "bad", want: nil, wantErr: true},
		}
		for _, tt := range tests {
			got, err := parseBoolPtr(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("unexpected error state for %q: got %v wantErr %v", tt.value, err, tt.wantErr)
			}
			if !boolPtrEqual(got, tt.want) {
				t.Fatalf("unexpected bool ptr for %q: got %v want %v", tt.value, got, tt.want)
			}
		}
	})

	t.Run("parseInt64Default", func(t *testing.T) {
		t.Parallel()

		if got := parseInt64Default("", 9); got != 9 {
			t.Fatalf("unexpected fallback: %d", got)
		}
		if got := parseInt64Default("11", 9); got != 11 {
			t.Fatalf("unexpected parsed value: %d", got)
		}
	})

	t.Run("jobInputFromRequest", func(t *testing.T) {
		t.Parallel()

		req := jobRequest{
			Name:              "job",
			Description:       stringPtr("desc"),
			Enabled:           true,
			SourceType:        model.JobSourceTypeLocal,
			ImageRef:          "jobs/example:latest",
			ScheduleType:      model.ScheduleTypeInterval,
			IntervalSec:       intPtr(60),
			Params:            json.RawMessage(`{"foo":"bar"}`),
			ConcurrencyPolicy: model.ConcurrencyPolicyForbid,
			RetryLimit:        2,
			TimeoutSec:        intPtr(30),
			Timezone:          "UTC",
		}
		input, err := jobInputFromRequest(req)
		if err != nil {
			t.Fatalf("job input: %v", err)
		}
		if input.ParamsJSON == nil || *input.ParamsJSON != `{"foo":"bar"}` {
			t.Fatalf("unexpected params json: %+v", input.ParamsJSON)
		}
	})

	t.Run("toJobResponseAndRunResponse", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
		job := &model.Job{
			ID:                1,
			Name:              "job",
			Enabled:           true,
			SourceType:        model.JobSourceTypeLocal,
			ImageRef:          "jobs/example:latest",
			ScheduleType:      model.ScheduleTypeCron,
			ConcurrencyPolicy: model.ConcurrencyPolicyForbid,
			Timezone:          "UTC",
			CreatedAt:         now,
			UpdatedAt:         now,
		}
		if got := toJobResponse(job); got.ID != job.ID || got.Name != job.Name || got.SourceType != job.SourceType {
			t.Fatalf("unexpected job response: %+v", got)
		}

		run := &model.Run{
			ID:          2,
			JobID:       1,
			ScheduledAt: now,
			Status:      model.RunStatusRunning,
			Attempt:     1,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if got := toRunResponse(run); got.ID != run.ID || got.JobID != run.JobID || got.Status != run.Status {
			t.Fatalf("unexpected run response: %+v", got)
		}

		event := &model.RunEvent{
			ID:        3,
			RunID:     2,
			EventType: model.RunEventTypeStarted,
			CreatedAt: now,
		}
		if got := runEventToResponse(event); got.ID != event.ID || got.EventType != event.EventType {
			t.Fatalf("unexpected event response: %+v", got)
		}
	})
}

func TestHealthzHandler(t *testing.T) {
	t.Parallel()

	router := gin.New()
	router.GET("/health", HealthzHandler)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", w.Code)
	}
	body := w.Body.String()
	assertContains(t, body, `"message":"API is healthy"`)
	assertContains(t, body, `"status":"ok"`)
	assertContains(t, body, `"timestamp":"`)
}

func TestJobHandlerCRUDAndRuns(t *testing.T) {
	t.Parallel()

	env := newHandlerTestEnv(t, nil)

	created := doAPIRequest(t, env.router, http.MethodPost, "/api/v1/jobs", bytes.NewBufferString(`{
		"name":"api-job",
		"description":"api desc",
		"enabled":true,
		"sourceType":"local",
		"imageRef":"jobs/api:latest",
		"scheduleType":"interval",
		"intervalSec":60,
		"params":{"foo":"bar"},
		"concurrencyPolicy":"forbid",
		"retryLimit":2,
		"timeoutSec":30,
		"timezone":"UTC"
	}`))
	if created.Code != http.StatusCreated {
		t.Fatalf("unexpected create status: %d body=%s", created.Code, created.Body.String())
	}
	var createdJob jobResponse
	decodeJSON(t, created.Body.Bytes(), &createdJob)
	if createdJob.ID <= 0 || createdJob.Name != "api-job" || string(createdJob.Params) != `{"foo":"bar"}` {
		t.Fatalf("unexpected created job: %+v", createdJob)
	}

	list := doAPIRequest(t, env.router, http.MethodGet, "/api/v1/jobs?name=api-job&enabled=true&page=1&size=10", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("unexpected list status: %d body=%s", list.Code, list.Body.String())
	}
	var jobs listResponse[jobResponse]
	decodeJSON(t, list.Body.Bytes(), &jobs)
	if jobs.Total != 1 || len(jobs.Items) != 1 || jobs.Items[0].ID != createdJob.ID {
		t.Fatalf("unexpected job list: %+v", jobs)
	}

	get := doAPIRequest(t, env.router, http.MethodGet, "/api/v1/jobs/"+intToString(createdJob.ID), nil)
	if get.Code != http.StatusOK {
		t.Fatalf("unexpected get status: %d body=%s", get.Code, get.Body.String())
	}
	var gotJob jobResponse
	decodeJSON(t, get.Body.Bytes(), &gotJob)
	if gotJob.Name != "api-job" || gotJob.Description == nil || *gotJob.Description != "api desc" {
		t.Fatalf("unexpected get job: %+v", gotJob)
	}

	updated := doAPIRequest(t, env.router, http.MethodPut, "/api/v1/jobs/"+intToString(createdJob.ID), bytes.NewBufferString(`{
		"name":"api-job-v2",
		"description":"updated desc",
		"enabled":false,
		"sourceType":"local",
		"imageRef":"jobs/api:v2",
		"scheduleType":"interval",
		"intervalSec":120,
		"concurrencyPolicy":"allow",
		"retryLimit":3,
		"timeoutSec":45,
		"timezone":"UTC"
	}`))
	if updated.Code != http.StatusOK {
		t.Fatalf("unexpected update status: %d body=%s", updated.Code, updated.Body.String())
	}
	var updatedJob jobResponse
	decodeJSON(t, updated.Body.Bytes(), &updatedJob)
	if updatedJob.Name != "api-job-v2" || updatedJob.Enabled {
		t.Fatalf("unexpected updated job: %+v", updatedJob)
	}

	trigger := doAPIRequest(t, env.router, http.MethodPost, "/api/v1/jobs/"+intToString(createdJob.ID)+"/trigger", bytes.NewBufferString(`{"reason":"manual trigger"}`))
	if trigger.Code != http.StatusCreated {
		t.Fatalf("unexpected trigger status: %d body=%s", trigger.Code, trigger.Body.String())
	}
	var triggered runResponse
	decodeJSON(t, trigger.Body.Bytes(), &triggered)
	if triggered.JobID != createdJob.ID || triggered.Status != model.RunStatusPending {
		t.Fatalf("unexpected triggered run: %+v", triggered)
	}

	runs := doAPIRequest(t, env.router, http.MethodGet, "/api/v1/jobs/"+intToString(createdJob.ID)+"/runs", nil)
	if runs.Code != http.StatusOK {
		t.Fatalf("unexpected runs status: %d body=%s", runs.Code, runs.Body.String())
	}
	var jobRuns listResponse[runResponse]
	decodeJSON(t, runs.Body.Bytes(), &jobRuns)
	if jobRuns.Total == 0 || len(jobRuns.Items) == 0 || jobRuns.Items[0].ID != triggered.ID {
		t.Fatalf("unexpected job run list: %+v", jobRuns)
	}

	del := doAPIRequest(t, env.router, http.MethodDelete, "/api/v1/jobs/"+intToString(createdJob.ID), nil)
	if del.Code != http.StatusNoContent {
		t.Fatalf("unexpected delete status: %d body=%s", del.Code, del.Body.String())
	}
	missing := doAPIRequest(t, env.router, http.MethodGet, "/api/v1/jobs/"+intToString(createdJob.ID), nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("expected missing job, got %d body=%s", missing.Code, missing.Body.String())
	}
}

func TestRunHandlerEndpoints(t *testing.T) {
	t.Parallel()

	env := newHandlerTestEnv(t, nil)
	job := createHandlerJob(t, env.store, "run-job")
	logPath := filepath.Join(t.TempDir(), "run.log")
	if err := os.WriteFile(logPath, []byte("abcdef"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	run := &model.Run{
		JobID:       job.ID,
		ScheduledAt: time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC),
		Status:      model.RunStatusRunning,
		Attempt:     1,
		LogPath:     &logPath,
	}
	if _, err := env.store.Runs.Create(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	resultPath := filepath.Join(t.TempDir(), "result.json")
	resultPayload := `{"jobId":` + intToString(job.ID) + `,"runId":` + intToString(run.ID) + `,"imageRef":"jobs/example:latest","pullRef":"jobs/example:latest","exitCode":0,"message":"done","finishedAt":"2026-04-17T12:30:00Z"}`
	if err := os.WriteFile(resultPath, []byte(resultPayload), 0o644); err != nil {
		t.Fatalf("write result: %v", err)
	}
	if err := env.store.Runs.UpdateLogArtifacts(context.Background(), run.ID, &logPath, &resultPath); err != nil {
		t.Fatalf("update run artifacts: %v", err)
	}
	msg := "started"
	if _, err := env.store.Events.Create(context.Background(), &model.RunEvent{
		RunID:     run.ID,
		EventType: model.RunEventTypeStarted,
		Message:   &msg,
	}); err != nil {
		t.Fatalf("create event: %v", err)
	}

	list := doAPIRequest(t, env.router, http.MethodGet, "/api/v1/runs?jobId="+intToString(job.ID)+"&status=running&page=1&size=10", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("unexpected list status: %d body=%s", list.Code, list.Body.String())
	}
	var runs listResponse[runResponse]
	decodeJSON(t, list.Body.Bytes(), &runs)
	if runs.Total != 1 || len(runs.Items) != 1 || runs.Items[0].ID != run.ID {
		t.Fatalf("unexpected run list: %+v", runs)
	}

	got := doAPIRequest(t, env.router, http.MethodGet, "/api/v1/runs/"+intToString(run.ID), nil)
	if got.Code != http.StatusOK {
		t.Fatalf("unexpected get status: %d body=%s", got.Code, got.Body.String())
	}
	var gotRun runResponse
	decodeJSON(t, got.Body.Bytes(), &gotRun)
	if gotRun.Status != model.RunStatusRunning || gotRun.JobID != job.ID {
		t.Fatalf("unexpected run response: %+v", gotRun)
	}

	events := doAPIRequest(t, env.router, http.MethodGet, "/api/v1/runs/"+intToString(run.ID)+"/events", nil)
	if events.Code != http.StatusOK {
		t.Fatalf("unexpected events status: %d body=%s", events.Code, events.Body.String())
	}
	var eventList listResponse[runEventResponse]
	decodeJSON(t, events.Body.Bytes(), &eventList)
	if eventList.Total != 1 || len(eventList.Items) != 1 || eventList.Items[0].EventType != model.RunEventTypeStarted {
		t.Fatalf("unexpected event list: %+v", eventList)
	}

	logs := doAPIRequest(t, env.router, http.MethodGet, "/api/v1/runs/"+intToString(run.ID)+"/logs?offset=1&limit=4", nil)
	if logs.Code != http.StatusOK {
		t.Fatalf("unexpected logs status: %d body=%s", logs.Code, logs.Body.String())
	}
	var logResp logResponse
	decodeJSON(t, logs.Body.Bytes(), &logResp)
	if logResp.Content != "bcde" || logResp.Offset != 1 || logResp.Size != 4 {
		t.Fatalf("unexpected log response: %+v", logResp)
	}

	result := doAPIRequest(t, env.router, http.MethodGet, "/api/v1/runs/"+intToString(run.ID)+"/result", nil)
	if result.Code != http.StatusOK {
		t.Fatalf("unexpected result status: %d body=%s", result.Code, result.Body.String())
	}
	var resultResp resultResponse
	decodeJSON(t, result.Body.Bytes(), &resultResp)
	if resultResp.RunID != run.ID || resultResp.JobID != job.ID || resultResp.Message != "done" {
		t.Fatalf("unexpected result response: %+v", resultResp)
	}

	cancelled := doAPIRequest(t, env.router, http.MethodPost, "/api/v1/runs/"+intToString(run.ID)+"/cancel", nil)
	if cancelled.Code != http.StatusOK {
		t.Fatalf("unexpected cancel status: %d body=%s", cancelled.Code, cancelled.Body.String())
	}
	var cancelledRun runResponse
	decodeJSON(t, cancelled.Body.Bytes(), &cancelledRun)
	if cancelledRun.Status != model.RunStatusCancelling {
		t.Fatalf("unexpected cancelled run: %+v", cancelledRun)
	}
}

func TestImageHandlerEndpoints(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/_catalog":
			if r.URL.Query().Get("page") == "2" {
				writeHandlerJSON(t, w, map[string]any{"repositories": []string{"jobs/side"}})
				return
			}
			w.Header().Set("Link", `</v2/_catalog?page=2>; rel="next"`)
			writeHandlerJSON(t, w, map[string]any{"repositories": []string{"jobs/app"}})
		case r.URL.Path == "/v2/jobs/app/tags/list":
			writeHandlerJSON(t, w, map[string]any{"tags": []string{"latest"}})
		case r.URL.Path == "/v2/jobs/side/tags/list":
			writeHandlerJSON(t, w, map[string]any{"tags": []string{"stable"}})
		case r.URL.Path == "/v2/jobs/app/manifests/latest":
			w.Header().Set("Docker-Content-Digest", "sha256:manifest-digest")
			writeHandlerJSON(t, w, map[string]any{"schemaVersion": 2})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	resolver := image.NewResolver(config.ImageConfig{
		AllowedSources: []string{"remote"},
		DefaultSource:  "remote",
		Remote: config.ImageRemoteConfig{
			Endpoint: srv.URL,
		},
	})

	env := newHandlerTestEnv(t, resolver)

	list := doAPIRequest(t, env.router, http.MethodGet, "/api/v1/images", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("unexpected image list status: %d body=%s", list.Code, list.Body.String())
	}
	var imageList struct {
		Items []image.Candidate `json:"items"`
		Total int               `json:"total"`
	}
	decodeJSON(t, list.Body.Bytes(), &imageList)
	if imageList.Total != 2 || len(imageList.Items) != 2 {
		t.Fatalf("unexpected image list: %+v", imageList)
	}

	resolve := doAPIRequest(t, env.router, http.MethodGet, "/api/v1/images/resolve?imageRef=jobs/app:latest", nil)
	if resolve.Code != http.StatusOK {
		t.Fatalf("unexpected resolve status: %d body=%s", resolve.Code, resolve.Body.String())
	}
	var candidate image.Candidate
	decodeJSON(t, resolve.Body.Bytes(), &candidate)
	if candidate.SourceType != "remote" || candidate.Digest == nil || *candidate.Digest != "sha256:manifest-digest" {
		t.Fatalf("unexpected resolved candidate: %+v", candidate)
	}
}

func newHandlerTestEnv(t *testing.T, resolver *image.Resolver) *handlerTestEnv {
	t.Helper()

	st := openHandlerTestStore(t)
	jobSvc := service.NewJobService(st, nil)
	runSvc := service.NewRunService(st)
	if resolver == nil {
		resolver = image.NewResolver(config.ImageConfig{
			AllowedSources: []string{"remote", "local"},
			DefaultSource:  "remote",
			Remote: config.ImageRemoteConfig{
				Endpoint: "http://example.invalid",
			},
		})
	}

	router := gin.New()
	router.Use(gin.Recovery())
	router.GET("/health", HealthzHandler)

	jobHandler := NewJobHandler(jobSvc)
	runHandler := NewRunHandler(runSvc, nil)
	imageHandler := NewImageHandler(resolver)

	api := router.Group("/api/v1")
	{
		api.GET("/jobs", jobHandler.ListJobs)
		api.GET("/jobs/:jobId", jobHandler.GetJob)
		api.POST("/jobs", jobHandler.CreateJob)
		api.PUT("/jobs/:jobId", jobHandler.UpdateJob)
		api.DELETE("/jobs/:jobId", jobHandler.DeleteJob)
		api.POST("/jobs/:jobId/trigger", jobHandler.TriggerJob)
		api.GET("/jobs/:jobId/runs", jobHandler.ListJobRuns)

		api.GET("/runs", runHandler.ListRuns)
		api.GET("/runs/:runId", runHandler.GetRun)
		api.POST("/runs/:runId/cancel", runHandler.CancelRun)
		api.GET("/runs/:runId/events", runHandler.ListRunEvents)
		api.GET("/runs/:runId/logs", runHandler.GetRunLogs)
		api.GET("/runs/:runId/result", runHandler.GetRunResult)

		api.GET("/images", imageHandler.ListImages)
		api.GET("/images/resolve", imageHandler.ResolveImage)
		api.GET("/images/:sourceType/candidates", imageHandler.ListImageCandidates)
	}

	return &handlerTestEnv{
		store:  st,
		router: router,
	}
}

type handlerTestEnv struct {
	store  *store.Store
	router *gin.Engine
}

func openHandlerTestStore(t *testing.T) *store.Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "db.sqlite")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return st
}

func createHandlerJob(t *testing.T, st *store.Store, name string) *model.Job {
	t.Helper()

	job := &model.Job{
		Name:              name,
		Enabled:           true,
		SourceType:        model.JobSourceTypeLocal,
		ImageRef:          "jobs/example:latest",
		ScheduleType:      model.ScheduleTypeInterval,
		IntervalSec:       intPtr(60),
		Timezone:          "UTC",
		ConcurrencyPolicy: model.ConcurrencyPolicyForbid,
		RetryLimit:        1,
		TimeoutSec:        30,
	}
	if _, err := st.Jobs.Create(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	return job
}

func createHandlerRun(t *testing.T, st *store.Store, jobID int64, status model.RunStatus) *model.Run {
	t.Helper()

	run := &model.Run{
		JobID:       jobID,
		ScheduledAt: time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC),
		Status:      status,
		Attempt:     0,
	}
	if _, err := st.Runs.Create(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	return run
}

func doAPIRequest(t *testing.T, router http.Handler, method, path string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func decodeJSON(t *testing.T, data []byte, dst any) {
	t.Helper()

	if err := json.Unmarshal(data, dst); err != nil {
		t.Fatalf("decode json: %v data=%s", err, string(data))
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func boolPtrEqual(a, b *bool) bool {
	switch {
	case a == nil || b == nil:
		return a == nil && b == nil
	default:
		return *a == *b
	}
}

func stringPtr(v string) *string {
	return &v
}

func intPtr(v int) *int {
	return &v
}

func intToString(v int64) string {
	return strconv.FormatInt(v, 10)
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()

	if !bytes.Contains([]byte(s), []byte(substr)) {
		t.Fatalf("expected %q to contain %q", s, substr)
	}
}

func writeHandlerJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("write json: %v", err)
	}
}
