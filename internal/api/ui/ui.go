package ui

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoonzinope/go-job-runner/internal/image"
	logwriter "github.com/hoonzinope/go-job-runner/internal/log"
	"github.com/hoonzinope/go-job-runner/internal/model"
	"github.com/hoonzinope/go-job-runner/internal/service"
	"github.com/hoonzinope/go-job-runner/internal/store"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

type UI struct {
	jobs   *service.JobService
	runs   *service.RunService
	images *image.Resolver
	reader *logwriter.Reader
	tpl    *template.Template
}

type pageMeta struct {
	Title           string
	Subtitle        string
	ActiveNav       string
	ContentTemplate string
}

type pagination struct {
	Page       int
	Size       int
	Total      int64
	TotalPages int
	HasPrev    bool
	HasNext    bool
	PrevPage   int
	NextPage   int
}

type jobsListPage struct {
	pageMeta
	Filter     jobListFilter
	Jobs       []model.Job
	Pagination pagination
}

type jobListFilter struct {
	Name         string
	Enabled      string
	ScheduleType string
}

type jobFormPage struct {
	pageMeta
	Mode        string
	Job         *model.Job
	Image       *jobImagePanel
	FieldErrors map[string]string
	Error       string
}

type jobImagePanel struct {
	SourceType string
	Query      string
	Prefix     string
	ImageRef   string
	Candidates []image.Candidate
	Resolved   *image.Candidate
	Error      string
}

type jobDetailPage struct {
	pageMeta
	Job        *model.Job
	RecentRuns []model.Run
	Pagination pagination
	HasRecent  bool
}

type runsListPage struct {
	pageMeta
	Filter     runListFilter
	Runs       []runListItem
	Pagination pagination
}

type runListFilter struct {
	JobID  string
	Status string
	From   string
	To     string
}

type runListItem struct {
	model.Run
	JobName   string
	CanCancel bool
}

type runDetailPage struct {
	pageMeta
	Run           *model.Run
	Job           *model.Job
	Events        []model.RunEvent
	LogContent    string
	LogOffset     int64
	LogSize       int
	LogError      string
	ResultContent string
	CanCancel     bool
}

func New(jobs *service.JobService, runs *service.RunService, resolver *image.Resolver, reader *logwriter.Reader) *UI {
	if reader == nil {
		reader = logwriter.NewReader()
	}

	tpl := template.Must(template.New("base").Funcs(template.FuncMap{
		"formatTime": func(t *time.Time) string {
			if t == nil || t.IsZero() {
				return "—"
			}
			return t.UTC().Format("2006-01-02 15:04:05 UTC")
		},
		"formatCompactTime": func(t *time.Time) string {
			if t == nil || t.IsZero() {
				return "—"
			}
			return t.UTC().Format("01-02 15:04")
		},
		"formatValue": func(v any) string {
			value := reflect.ValueOf(v)
			for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
				if value.IsNil() {
					return "—"
				}
				value = value.Elem()
			}
			if !value.IsValid() {
				return "—"
			}
			switch value.Kind() {
			case reflect.String:
				if strings.TrimSpace(value.String()) == "" {
					return "—"
				}
				return value.String()
			case reflect.Bool:
				if value.Bool() {
					return "true"
				}
				return "false"
			default:
				return fmt.Sprint(value.Interface())
			}
		},
		"inputString": func(v *string) string {
			if v == nil {
				return ""
			}
			return *v
		},
		"inputInt": func(v *int) string {
			if v == nil {
				return ""
			}
			return strconv.Itoa(*v)
		},
		"statusClass": func(status model.RunStatus) string {
			switch status {
			case model.RunStatusSuccess:
				return "status status-success"
			case model.RunStatusFailed, model.RunStatusTimeout:
				return "status status-failed"
			case model.RunStatusRunning, model.RunStatusPending, model.RunStatusCancelling:
				return "status status-warn"
			case model.RunStatusCancelled:
				return "status status-muted"
			default:
				return "status"
			}
		},
		"jobSourceLabel": func(v model.JobSourceType) string {
			return string(v)
		},
		"scheduleLabel": func(v model.ScheduleType) string {
			return string(v)
		},
		"jsonPretty": func(raw string) string {
			if strings.TrimSpace(raw) == "" {
				return "—"
			}
			var out bytes.Buffer
			if err := json.Indent(&out, []byte(raw), "", "  "); err != nil {
				return raw
			}
			return out.String()
		},
		"fieldError": func(fields map[string]string, name string) string {
			if fields == nil {
				return ""
			}
			return fields[name]
		},
	}).ParseFS(templatesFS, "templates/*.tmpl"))

	return &UI{
		jobs:   jobs,
		runs:   runs,
		images: resolver,
		reader: reader,
		tpl:    tpl,
	}
}

func (u *UI) RegisterRoutes(r gin.IRoutes) {
	r.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/jobs")
	})
	r.GET("/jobs", u.listJobs)
	r.GET("/jobs/new", u.newJob)
	r.POST("/jobs", u.createJob)
	r.GET("/jobs/:jobId", u.jobDetail)
	r.GET("/jobs/:jobId/edit", u.editJob)
	r.POST("/jobs/:jobId", u.updateJob)
	r.POST("/jobs/:jobId/delete", u.deleteJob)
	r.POST("/jobs/:jobId/trigger", u.triggerJob)
	r.GET("/runs", u.listRuns)
	r.GET("/runs/:runId", u.runDetail)
	r.POST("/runs/:runId/cancel", u.cancelRun)
}

func (u *UI) listJobs(c *gin.Context) {
	pageNum, size := parsePageQuery(c.Query("page"), c.Query("size"))
	filter := store.JobFilter{}
	if name := strings.TrimSpace(c.Query("name")); name != "" {
		filter.Name = name
	}
	if enabled, ok := parseBoolQuery(c.Query("enabled")); ok {
		filter.Enabled = enabled
	}
	if scheduleType := strings.TrimSpace(c.Query("scheduleType")); scheduleType != "" {
		v := model.ScheduleType(scheduleType)
		filter.ScheduleType = &v
	}

	jobs, total, err := u.jobs.ListJobs(c.Request.Context(), filter, store.Page{Page: pageNum, Size: size})
	if err != nil {
		u.renderError(c, http.StatusInternalServerError, "Jobs", err)
		return
	}

	if err := u.render(c, http.StatusOK, "base", jobsListPage{
		pageMeta: pageMeta{
			Title:           "Jobs",
			Subtitle:        "Registered jobs and their next run state",
			ActiveNav:       "jobs",
			ContentTemplate: "jobs_list_content",
		},
		Filter: jobListFilter{
			Name:         filter.Name,
			Enabled:      c.Query("enabled"),
			ScheduleType: c.Query("scheduleType"),
		},
		Jobs:       jobs,
		Pagination: newPagination(pageNum, size, total),
	}); err != nil {
		u.renderError(c, http.StatusInternalServerError, "Jobs", err)
	}
}

func (u *UI) newJob(c *gin.Context) {
	job := &model.Job{
		Enabled:           true,
		SourceType:        model.JobSourceTypeLocal,
		ScheduleType:      model.ScheduleTypeInterval,
		ConcurrencyPolicy: model.ConcurrencyPolicyForbid,
		Timezone:          "UTC",
	}
	if imageRef := strings.TrimSpace(c.Query("imageRef")); imageRef != "" {
		job.ImageRef = imageRef
	}
	if sourceType := strings.TrimSpace(c.Query("sourceType")); sourceType != "" {
		job.SourceType = model.JobSourceType(sourceType)
	}
	u.renderOrError(c, http.StatusOK, "base", jobFormPage{
		pageMeta: pageMeta{
			Title:           "New Job",
			Subtitle:        "Create a new scheduled task",
			ActiveNav:       "jobs",
			ContentTemplate: "job_form_content",
		},
		Mode:  "create",
		Job:   job,
		Image: u.buildImagePanel(c, job),
	})
}

func (u *UI) editJob(c *gin.Context) {
	jobID, err := parseID(c.Param("jobId"))
	if err != nil {
		u.renderError(c, http.StatusBadRequest, "Job edit", err)
		return
	}
	job, err := u.jobs.GetJob(c.Request.Context(), jobID)
	if err != nil {
		u.renderError(c, http.StatusNotFound, "Job edit", err)
		return
	}
	if imageRef := strings.TrimSpace(c.Query("imageRef")); imageRef != "" {
		job.ImageRef = imageRef
	}
	if sourceType := strings.TrimSpace(c.Query("sourceType")); sourceType != "" {
		job.SourceType = model.JobSourceType(sourceType)
	}
	u.renderOrError(c, http.StatusOK, "base", jobFormPage{
		pageMeta: pageMeta{
			Title:           fmt.Sprintf("Edit Job #%d", job.ID),
			Subtitle:        "Update the job configuration",
			ActiveNav:       "jobs",
			ContentTemplate: "job_form_content",
		},
		Mode:  "edit",
		Job:   job,
		Image: u.buildImagePanel(c, job),
	})
}

func (u *UI) createJob(c *gin.Context) {
	input, draft, err := jobInputFromForm(c)
	if err != nil {
		u.renderJobForm(c, http.StatusBadRequest, "New Job", "Create a new scheduled task", "create", draft, u.buildImagePanel(c, draft), jobValidationFields(err), err)
		return
	}

	job, err := u.jobs.CreateJob(c.Request.Context(), input)
	if err != nil {
		u.renderJobForm(c, http.StatusBadRequest, "New Job", "Create a new scheduled task", "create", draft, u.buildImagePanel(c, draft), jobValidationFields(err), err)
		return
	}

	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/jobs/%d", job.ID))
}

func (u *UI) updateJob(c *gin.Context) {
	jobID, err := parseID(c.Param("jobId"))
	if err != nil {
		u.renderError(c, http.StatusBadRequest, "Job edit", err)
		return
	}
	input, draft, err := jobInputFromForm(c)
	if err != nil {
		u.renderJobForm(c, http.StatusBadRequest, fmt.Sprintf("Edit Job #%d", jobID), "Update the job configuration", "edit", draft, u.buildImagePanel(c, draft), jobValidationFields(err), err)
		return
	}

	job, err := u.jobs.UpdateJob(c.Request.Context(), jobID, input)
	if err != nil {
		u.renderJobForm(c, http.StatusBadRequest, fmt.Sprintf("Edit Job #%d", jobID), "Update the job configuration", "edit", draft, u.buildImagePanel(c, draft), jobValidationFields(err), err)
		return
	}

	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/jobs/%d", job.ID))
}

func (u *UI) deleteJob(c *gin.Context) {
	jobID, err := parseID(c.Param("jobId"))
	if err != nil {
		u.renderError(c, http.StatusBadRequest, "Job delete", err)
		return
	}
	if err := u.jobs.DeleteJob(c.Request.Context(), jobID); err != nil {
		u.renderError(c, http.StatusInternalServerError, "Job delete", err)
		return
	}
	c.Redirect(http.StatusSeeOther, "/jobs")
}

func (u *UI) triggerJob(c *gin.Context) {
	jobID, err := parseID(c.Param("jobId"))
	if err != nil {
		u.renderError(c, http.StatusBadRequest, "Job trigger", err)
		return
	}
	var reason *string
	if v := strings.TrimSpace(c.PostForm("reason")); v != "" {
		reason = &v
	}
	run, err := u.jobs.TriggerJob(c.Request.Context(), jobID, reason)
	if err != nil {
		u.renderError(c, http.StatusInternalServerError, "Job trigger", err)
		return
	}
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/runs/%d", run.ID))
}

func (u *UI) jobDetail(c *gin.Context) {
	jobID, err := parseID(c.Param("jobId"))
	if err != nil {
		u.renderError(c, http.StatusBadRequest, "Job detail", err)
		return
	}
	job, err := u.jobs.GetJob(c.Request.Context(), jobID)
	if err != nil {
		u.renderError(c, http.StatusNotFound, "Job detail", err)
		return
	}
	runs, total, err := u.jobs.ListJobRuns(c.Request.Context(), jobID, nil, store.Page{Page: 1, Size: 10})
	if err != nil {
		u.renderError(c, http.StatusInternalServerError, "Job detail", err)
		return
	}
	u.renderOrError(c, http.StatusOK, "base", jobDetailPage{
		pageMeta: pageMeta{
			Title:           fmt.Sprintf("Job #%d", job.ID),
			Subtitle:        job.Name,
			ActiveNav:       "jobs",
			ContentTemplate: "job_detail_content",
		},
		Job:        job,
		RecentRuns: runs,
		Pagination: newPagination(1, 10, total),
		HasRecent:  len(runs) > 0,
	})
}

func (u *UI) listRuns(c *gin.Context) {
	pageNum, size := parsePageQuery(c.Query("page"), c.Query("size"))
	filter := store.RunFilter{}
	if jobID := strings.TrimSpace(c.Query("jobId")); jobID != "" {
		if n, err := parseID(jobID); err == nil {
			filter.JobID = &n
		}
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		v := model.RunStatus(status)
		filter.Status = &v
	}
	if from := strings.TrimSpace(c.Query("from")); from != "" {
		if t, err := time.Parse(time.RFC3339, from); err == nil {
			filter.From = &t
		}
	}
	if to := strings.TrimSpace(c.Query("to")); to != "" {
		if t, err := time.Parse(time.RFC3339, to); err == nil {
			filter.To = &t
		}
	}

	runs, total, err := u.runs.ListRuns(c.Request.Context(), filter, store.Page{Page: pageNum, Size: size})
	if err != nil {
		u.renderError(c, http.StatusInternalServerError, "Runs", err)
		return
	}

	items := make([]runListItem, 0, len(runs))
	for i := range runs {
		job, _ := u.jobs.GetJob(c.Request.Context(), runs[i].JobID)
		item := runListItem{
			Run:       runs[i],
			CanCancel: runs[i].Status == model.RunStatusPending || runs[i].Status == model.RunStatusRunning || runs[i].Status == model.RunStatusCancelling,
		}
		if job != nil {
			item.JobName = job.Name
		}
		items = append(items, item)
	}

	u.renderOrError(c, http.StatusOK, "base", runsListPage{
		pageMeta: pageMeta{
			Title:           "Runs",
			Subtitle:        "Recent run history, live execution state, and direct cancellation control",
			ActiveNav:       "runs",
			ContentTemplate: "runs_list_content",
		},
		Filter: runListFilter{
			JobID:  c.Query("jobId"),
			Status: c.Query("status"),
			From:   c.Query("from"),
			To:     c.Query("to"),
		},
		Runs:       items,
		Pagination: newPagination(pageNum, size, total),
	})
}

func (u *UI) runDetail(c *gin.Context) {
	runID, err := parseID(c.Param("runId"))
	if err != nil {
		u.renderError(c, http.StatusBadRequest, "Run detail", err)
		return
	}
	run, err := u.runs.GetRun(c.Request.Context(), runID)
	if err != nil {
		u.renderError(c, http.StatusNotFound, "Run detail", err)
		return
	}
	job, _ := u.jobs.GetJob(c.Request.Context(), run.JobID)
	events, err := u.runs.ListRunEvents(c.Request.Context(), runID)
	if err != nil {
		u.renderError(c, http.StatusInternalServerError, "Run detail", err)
		return
	}

	logContent := ""
	var logOffset int64
	var logSize int
	logError := ""
	if content, offset, size, err := u.runs.ReadLogs(c.Request.Context(), runID, u.reader, 0, 0, 8192); err == nil {
		logContent = content
		logOffset = offset
		logSize = size
	} else {
		logError = err.Error()
	}

	resultContent := ""
	if run.ResultPath != nil && *run.ResultPath != "" {
		if raw, err := jsonFromFile(*run.ResultPath); err == nil {
			resultContent = raw
		}
	}

	u.renderOrError(c, http.StatusOK, "base", runDetailPage{
		pageMeta: pageMeta{
			Title:           fmt.Sprintf("Run #%d", run.ID),
			Subtitle:        fmt.Sprintf("Job #%d", run.JobID),
			ActiveNav:       "runs",
			ContentTemplate: "run_detail_content",
		},
		Run:           run,
		Job:           job,
		Events:        events,
		LogContent:    logContent,
		LogOffset:     logOffset,
		LogSize:       logSize,
		LogError:      logError,
		ResultContent: resultContent,
		CanCancel:     run.Status == model.RunStatusPending || run.Status == model.RunStatusRunning || run.Status == model.RunStatusCancelling,
	})
}

func (u *UI) cancelRun(c *gin.Context) {
	runID, err := parseID(c.Param("runId"))
	if err != nil {
		u.renderError(c, http.StatusBadRequest, "Run cancel", err)
		return
	}
	run, err := u.runs.CancelRun(c.Request.Context(), runID)
	if err != nil {
		u.renderError(c, http.StatusInternalServerError, "Run cancel", err)
		return
	}
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/runs/%d", run.ID))
}

func (u *UI) render(c *gin.Context, status int, name string, data any) error {
	contentTemplate, ok := pageContentTemplateName(data)
	if !ok {
		return fmt.Errorf("missing content template name")
	}

	tpl, err := u.tpl.Clone()
	if err != nil {
		return err
	}
	if _, err := tpl.Parse(fmt.Sprintf(`{{ define "page_content" }}{{ template %q . }}{{ end }}`, contentTemplate)); err != nil {
		return err
	}

	c.Status(status)
	c.Header("Content-Type", "text/html; charset=utf-8")
	return tpl.ExecuteTemplate(c.Writer, name, data)
}

func (u *UI) renderOrError(c *gin.Context, status int, name string, data any) {
	if err := u.render(c, status, name, data); err != nil {
		u.renderError(c, http.StatusInternalServerError, "UI render", err)
	}
}

func (u *UI) renderError(c *gin.Context, status int, title string, err error) {
	_ = u.render(c, status, "base", errorPage{
		pageMeta: pageMeta{
			Title:           title,
			Subtitle:        "Unable to load page",
			ActiveNav:       "",
			ContentTemplate: "error_content",
		},
		Message: err.Error(),
	})
}

type errorPage struct {
	pageMeta
	Message string
}

func newPagination(page, size int, total int64) pagination {
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	totalPages := 0
	if total > 0 {
		totalPages = int((total + int64(size) - 1) / int64(size))
	}
	return pagination{
		Page:       page,
		Size:       size,
		Total:      total,
		TotalPages: totalPages,
		HasPrev:    page > 1,
		HasNext:    totalPages > 0 && page < totalPages,
		PrevPage:   max(1, page-1),
		NextPage:   page + 1,
	}
}

func parsePageQuery(pageValue, sizeValue string) (int, int) {
	page := 1
	size := 20
	if n, err := strconv.Atoi(pageValue); err == nil && n > 0 {
		page = n
	}
	if n, err := strconv.Atoi(sizeValue); err == nil && n > 0 {
		size = n
	}
	return page, size
}

func parseBoolQuery(value string) (*bool, bool) {
	if value == "" {
		return nil, false
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		v := true
		return &v, true
	case "0", "false", "no", "off":
		v := false
		return &v, true
	default:
		return nil, false
	}
}

func parseID(value string) (int64, error) {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid id: %w", err)
	}
	return n, nil
}

func jsonFromFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		return string(raw), nil
	}
	return out.String(), nil
}

func newJobDraft() *model.Job {
	return &model.Job{
		Enabled:           true,
		SourceType:        model.JobSourceTypeLocal,
		ScheduleType:      model.ScheduleTypeInterval,
		ConcurrencyPolicy: model.ConcurrencyPolicyForbid,
		Timezone:          "UTC",
	}
}

func jobInputFromForm(c *gin.Context) (service.JobInput, *model.Job, error) {
	params := strings.TrimSpace(c.PostForm("params"))
	var paramsJSON *string
	if params != "" {
		paramsJSON = &params
	}

	draft := newJobDraft()
	var (
		enabled, _ = parseBoolQuery(c.PostForm("enabled"))
		jobInput   = service.JobInput{
			Name:              strings.TrimSpace(c.PostForm("name")),
			ConcurrencyPolicy: model.ConcurrencyPolicy(strings.TrimSpace(c.PostForm("concurrencyPolicy"))),
			Timezone:          strings.TrimSpace(c.PostForm("timezone")),
			SourceType:        model.JobSourceType(strings.TrimSpace(c.PostForm("sourceType"))),
			ImageRef:          strings.TrimSpace(c.PostForm("imageRef")),
			ScheduleType:      model.ScheduleType(strings.TrimSpace(c.PostForm("scheduleType"))),
			RetryLimit:        parseIntFormDefault(c.PostForm("retryLimit"), 0),
			TimeoutSec:        parseIntFormPtr(c.PostForm("timeoutSec")),
			Description:       stringPtrOrNil(strings.TrimSpace(c.PostForm("description"))),
			Enabled:           enabled != nil && *enabled,
			ParamsJSON:        paramsJSON,
		}
	)
	draft.Name = jobInput.Name
	draft.Description = jobInput.Description
	draft.Enabled = jobInput.Enabled
	draft.SourceType = jobInput.SourceType
	draft.ImageRef = jobInput.ImageRef
	draft.ImageDigest = jobInput.ImageDigest
	draft.ScheduleType = jobInput.ScheduleType
	draft.ScheduleExpr = jobInput.ScheduleExpr
	draft.IntervalSec = jobInput.IntervalSec
	draft.Timezone = jobInput.Timezone
	draft.ConcurrencyPolicy = jobInput.ConcurrencyPolicy
	draft.RetryLimit = jobInput.RetryLimit
	if jobInput.TimeoutSec != nil {
		draft.TimeoutSec = *jobInput.TimeoutSec
	}
	draft.ParamsJSON = paramsJSON

	if interval := strings.TrimSpace(c.PostForm("intervalSec")); interval != "" {
		n, err := strconv.Atoi(interval)
		if err != nil {
			return jobInput, draft, &service.ValidationError{Field: "intervalSec", Message: "must be a number"}
		}
		jobInput.IntervalSec = &n
		draft.IntervalSec = &n
	}
	if expr := strings.TrimSpace(c.PostForm("scheduleExpr")); expr != "" {
		jobInput.ScheduleExpr = &expr
		draft.ScheduleExpr = &expr
	}
	if imgDigest := strings.TrimSpace(c.PostForm("imageDigest")); imgDigest != "" {
		jobInput.ImageDigest = &imgDigest
		draft.ImageDigest = &imgDigest
	}
	return jobInput, draft, nil
}

func parseIntFormDefault(value string, fallback int) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

func parseIntFormPtr(value string) *int {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		n = 0
	}
	return &n
}

func stringPtrOrNil(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func (u *UI) renderJobForm(c *gin.Context, status int, title, subtitle, mode string, job *model.Job, imagePanel *jobImagePanel, fieldErrors map[string]string, err error) {
	u.renderOrError(c, status, "base", jobFormPage{
		pageMeta: pageMeta{
			Title:           title,
			Subtitle:        subtitle,
			ActiveNav:       "jobs",
			ContentTemplate: "job_form_content",
		},
		Mode:        mode,
		Job:         job,
		Image:       imagePanel,
		FieldErrors: fieldErrors,
		Error:       err.Error(),
	})
}

func jobValidationFields(err error) map[string]string {
	if err == nil {
		return nil
	}
	var vErr *service.ValidationError
	if errors.As(err, &vErr) && vErr != nil {
		if vErr.Field != "" {
			return map[string]string{vErr.Field: vErr.Message}
		}
	}
	return nil
}

func (u *UI) buildImagePanel(c *gin.Context, job *model.Job) *jobImagePanel {
	if u.images == nil {
		return nil
	}

	sourceType := strings.TrimSpace(c.Query("sourceType"))
	if sourceType == "" && job != nil {
		sourceType = string(job.SourceType)
	}
	if sourceType == "" {
		sourceType = u.images.DefaultSource()
	}

	query := strings.TrimSpace(c.Query("q"))
	prefix := strings.TrimSpace(c.Query("prefix"))
	imageRef := strings.TrimSpace(c.Query("imageRef"))
	if imageRef == "" && job != nil {
		imageRef = strings.TrimSpace(job.ImageRef)
	}

	panel := &jobImagePanel{
		SourceType: sourceType,
		Query:      query,
		Prefix:     prefix,
		ImageRef:   imageRef,
	}

	if candidates, err := u.images.ListCandidates(c.Request.Context(), sourceType, query, prefix); err != nil {
		panel.Error = err.Error()
	} else {
		if len(candidates) > 20 {
			candidates = candidates[:20]
		}
		panel.Candidates = candidates
	}

	if imageRef != "" {
		resolved, err := u.images.Resolve(c.Request.Context(), sourceType, imageRef)
		if err != nil {
			if panel.Error == "" {
				panel.Error = err.Error()
			} else {
				panel.Error = panel.Error + "; " + err.Error()
			}
		} else {
			panel.Resolved = resolved
		}
	}

	return panel
}

func pageContentTemplateName(data any) (string, bool) {
	if data == nil {
		return "", false
	}
	v := reflect.ValueOf(data)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return "", false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return "", false
	}
	field := v.FieldByName("ContentTemplate")
	if !field.IsValid() || field.Kind() != reflect.String {
		return "", false
	}
	return field.String(), true
}
