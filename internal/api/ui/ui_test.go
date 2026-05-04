package ui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoonzinope/go-job-runner/internal/model"
	"github.com/hoonzinope/go-job-runner/internal/service"
	"github.com/hoonzinope/go-job-runner/internal/store"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestUIHelpers(t *testing.T) {
	t.Parallel()

	t.Run("parsePageQuery", func(t *testing.T) {
		t.Parallel()

		if page, size := parsePageQuery("3", "7"); page != 3 || size != 7 {
			t.Fatalf("unexpected page query result: %d %d", page, size)
		}
		if page, size := parsePageQuery("-1", "bad"); page != 1 || size != 20 {
			t.Fatalf("unexpected fallback page query result: %d %d", page, size)
		}
	})

	t.Run("parseBoolQuery", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			value string
			want  *bool
			ok    bool
		}{
			{value: "", want: nil, ok: false},
			{value: "true", want: boolPtr(true), ok: true},
			{value: "yes", want: boolPtr(true), ok: true},
			{value: "false", want: boolPtr(false), ok: true},
			{value: "no", want: boolPtr(false), ok: true},
			{value: "bad", want: nil, ok: false},
		}
		for _, tt := range tests {
			got, ok := parseBoolQuery(tt.value)
			if ok != tt.ok {
				t.Fatalf("unexpected ok for %q: got %v want %v", tt.value, ok, tt.ok)
			}
			if !boolPtrEqual(got, tt.want) {
				t.Fatalf("unexpected bool ptr for %q: got %v want %v", tt.value, got, tt.want)
			}
		}
	})

	t.Run("newPagination", func(t *testing.T) {
		t.Parallel()

		p := newPagination(2, 5, 13)
		if p.Page != 2 || p.Size != 5 || p.TotalPages != 3 || !p.HasPrev || !p.HasNext {
			t.Fatalf("unexpected pagination: %+v", p)
		}
		if p.PrevPage != 1 || p.NextPage != 3 {
			t.Fatalf("unexpected pagination navigation: %+v", p)
		}
	})

	t.Run("jsonFromFile", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "payload.json")
		if err := os.WriteFile(path, []byte(`{"ok":true}`), 0o644); err != nil {
			t.Fatalf("write json: %v", err)
		}
		raw, err := jsonFromFile(path)
		if err != nil {
			t.Fatalf("jsonFromFile: %v", err)
		}
		assertContains(t, raw, `"ok": true`)
	})

	t.Run("jobValidationFields", func(t *testing.T) {
		t.Parallel()

		fields := jobValidationFields(&service.ValidationError{Field: "intervalSec", Message: "must be a number"})
		if len(fields) != 1 || fields["intervalSec"] != "must be a number" {
			t.Fatalf("unexpected validation fields: %+v", fields)
		}
		if jobValidationFields(io.EOF) != nil {
			t.Fatal("expected non-validation error to be ignored")
		}
	})
}

func TestUIJobRoutes(t *testing.T) {
	t.Parallel()

	env := newUITestEnv(t)

	newJob := doUIRequest(t, env.router, http.MethodGet, "/jobs/new", "", "")
	if newJob.Code != http.StatusOK {
		t.Fatalf("unexpected new job status: %d body=%s", newJob.Code, newJob.Body.String())
	}
	assertContains(t, newJob.Body.String(), "Create a new scheduled task")

	form := url.Values{}
	form.Set("name", "ui-job")
	form.Set("description", "ui desc")
	form.Set("enabled", "true")
	form.Set("sourceType", "local")
	form.Set("imageRef", "jobs/ui:latest")
	form.Set("scheduleType", "interval")
	form.Set("intervalSec", "60")
	form.Set("concurrencyPolicy", "forbid")
	form.Set("retryLimit", "2")
	form.Set("timeoutSec", "30")
	form.Set("timezone", "UTC")
	form.Set("params", `{"foo":"bar"}`)

	created := doUIRequest(t, env.router, http.MethodPost, "/jobs", form.Encode(), "application/x-www-form-urlencoded")
	if created.Code != http.StatusSeeOther {
		t.Fatalf("unexpected create redirect: %d body=%s", created.Code, created.Body.String())
	}
	jobLocation := created.Header().Get("Location")
	if jobLocation == "" {
		t.Fatal("expected job redirect location")
	}

	jobPage := doUIRequest(t, env.router, http.MethodGet, jobLocation, "", "")
	if jobPage.Code != http.StatusOK {
		t.Fatalf("unexpected job detail status: %d body=%s", jobPage.Code, jobPage.Body.String())
	}
	assertContains(t, jobPage.Body.String(), "ui-job")
	assertContains(t, jobPage.Body.String(), "Recent Runs")

	jobID := mustParseID(t, jobLocation)
	triggered := doUIRequest(t, env.router, http.MethodPost, "/jobs/"+intToString(jobID)+"/trigger", "reason=manual", "application/x-www-form-urlencoded")
	if triggered.Code != http.StatusSeeOther {
		t.Fatalf("unexpected trigger redirect: %d body=%s", triggered.Code, triggered.Body.String())
	}
	runLocation := triggered.Header().Get("Location")
	if runLocation == "" {
		t.Fatal("expected run redirect location")
	}

	runPage := doUIRequest(t, env.router, http.MethodGet, runLocation, "", "")
	if runPage.Code != http.StatusOK {
		t.Fatalf("unexpected run detail status: %d body=%s", runPage.Code, runPage.Body.String())
	}
	assertContains(t, runPage.Body.String(), "Run ID")
	assertContains(t, runPage.Body.String(), "ui-job")

	listPage := doUIRequest(t, env.router, http.MethodGet, "/jobs", "", "")
	if listPage.Code != http.StatusOK {
		t.Fatalf("unexpected jobs list status: %d body=%s", listPage.Code, listPage.Body.String())
	}
	assertContains(t, listPage.Body.String(), "ui-job")
}

func TestUIRunRoutes(t *testing.T) {
	t.Parallel()

	env := newUITestEnv(t)
	job := seedUIJob(t, env.store, "run-job")
	logPath := filepath.Join(t.TempDir(), "run.log")
	if err := os.WriteFile(logPath, []byte("abcdef"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	resultPath := filepath.Join(t.TempDir(), "result.json")
	resultPayload := `{"jobId":` + intToString(job.ID) + `,"runId":1,"imageRef":"jobs/example:latest","pullRef":"jobs/example:latest","exitCode":0,"message":"done","finishedAt":"2026-04-17T12:30:00Z"}`
	if err := os.WriteFile(resultPath, []byte(resultPayload), 0o644); err != nil {
		t.Fatalf("write result: %v", err)
	}
	run := &model.Run{
		JobID:       job.ID,
		ScheduledAt: time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC),
		Status:      model.RunStatusRunning,
		Attempt:     1,
		LogPath:     &logPath,
		ResultPath:  &resultPath,
	}
	if _, err := env.store.Runs.Create(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	resultPayload = `{"jobId":` + intToString(job.ID) + `,"runId":` + intToString(run.ID) + `,"imageRef":"jobs/example:latest","pullRef":"jobs/example:latest","exitCode":0,"message":"done","finishedAt":"2026-04-17T12:30:00Z"}`
	if err := os.WriteFile(resultPath, []byte(resultPayload), 0o644); err != nil {
		t.Fatalf("rewrite result: %v", err)
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

	listPage := doUIRequest(t, env.router, http.MethodGet, "/runs", "", "")
	if listPage.Code != http.StatusOK {
		t.Fatalf("unexpected runs list status: %d body=%s", listPage.Code, listPage.Body.String())
	}
	assertContains(t, listPage.Body.String(), "run-job")

	detail := doUIRequest(t, env.router, http.MethodGet, "/runs/"+intToString(run.ID), "", "")
	if detail.Code != http.StatusOK {
		t.Fatalf("unexpected run detail status: %d body=%s", detail.Code, detail.Body.String())
	}
	assertContains(t, detail.Body.String(), "abcdef")
	assertContains(t, detail.Body.String(), "&#34;message&#34;: &#34;done&#34;")

	cancelled := doUIRequest(t, env.router, http.MethodPost, "/runs/"+intToString(run.ID)+"/cancel", "", "")
	if cancelled.Code != http.StatusSeeOther {
		t.Fatalf("unexpected cancel redirect: %d body=%s", cancelled.Code, cancelled.Body.String())
	}

	after := doUIRequest(t, env.router, http.MethodGet, "/runs/"+intToString(run.ID), "", "")
	if after.Code != http.StatusOK {
		t.Fatalf("unexpected run detail after cancel: %d body=%s", after.Code, after.Body.String())
	}
	assertContains(t, after.Body.String(), "cancelling")
}

func TestUIRunDetailShowsLogErrorWhenMissing(t *testing.T) {
	t.Parallel()

	env := newUITestEnv(t)
	job := seedUIJob(t, env.store, "missing-log-job")
	run := &model.Run{
		JobID:       job.ID,
		ScheduledAt: time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC),
		Status:      model.RunStatusSuccess,
		Attempt:     0,
	}
	if _, err := env.store.Runs.Create(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	detail := doUIRequest(t, env.router, http.MethodGet, "/runs/"+intToString(run.ID), "", "")
	if detail.Code != http.StatusOK {
		t.Fatalf("unexpected run detail status: %d body=%s", detail.Code, detail.Body.String())
	}
	assertContains(t, detail.Body.String(), "Log unavailable:")
	assertContains(t, detail.Body.String(), "no log path recorded")
}

func TestUIJobDetailShowsSkippedRunInRecentRuns(t *testing.T) {
	t.Parallel()

	env := newUITestEnv(t)
	job := seedUIJob(t, env.store, "skipped-job")
	finishedAt := time.Date(2026, 4, 17, 12, 5, 0, 0, time.UTC)
	run := &model.Run{
		JobID:        job.ID,
		ScheduledAt:  finishedAt.Add(-time.Minute),
		FinishedAt:   &finishedAt,
		Status:       model.RunStatusSkipped,
		Attempt:      0,
		ErrorMessage: stringPtr("scheduled run skipped: concurrency policy forbid and run 1 is still running"),
	}
	if _, err := env.store.Runs.Create(context.Background(), run); err != nil {
		t.Fatalf("create skipped run: %v", err)
	}

	page := doUIRequest(t, env.router, http.MethodGet, "/jobs/"+intToString(job.ID), "", "")
	if page.Code != http.StatusOK {
		t.Fatalf("unexpected job detail status: %d body=%s", page.Code, page.Body.String())
	}
	assertContains(t, page.Body.String(), "skipped")
}

func newUITestEnv(t *testing.T) *uiTestEnv {
	t.Helper()

	st := openUITestStore(t)
	jobSvc := service.NewJobService(st, nil)
	runSvc := service.NewRunService(st)
	ui := New(jobSvc, runSvc, nil, nil)

	router := gin.New()
	router.Use(gin.Recovery())
	ui.RegisterRoutes(router)

	return &uiTestEnv{
		store:  st,
		router: router,
	}
}

type uiTestEnv struct {
	store  *store.Store
	router *gin.Engine
}

func openUITestStore(t *testing.T) *store.Store {
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

func seedUIJob(t *testing.T, st *store.Store, name string) *model.Job {
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

func doUIRequest(t *testing.T, router http.Handler, method, path, body string, contentType string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func mustParseID(t *testing.T, path string) int64 {
	t.Helper()

	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 {
		t.Fatalf("invalid path: %q", path)
	}
	id, err := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	if err != nil {
		t.Fatalf("parse id from %q: %v", path, err)
	}
	return id
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

func intPtr(v int) *int {
	return &v
}

func stringPtr(v string) *string {
	return &v
}

func intToString(v int64) string {
	return strconv.FormatInt(v, 10)
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()

	if !strings.Contains(s, substr) {
		t.Fatalf("expected %q to contain %q", s, substr)
	}
}
