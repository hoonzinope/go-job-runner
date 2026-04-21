package ui

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	logwriter "github.com/hoonzinope/go-job-runner/internal/log"
	"github.com/hoonzinope/go-job-runner/internal/model"
	"github.com/hoonzinope/go-job-runner/internal/store"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

type UI struct {
	store  *store.Store
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
	Mode  string
	Job   *model.Job
	Error string
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
	JobName string
}

type runDetailPage struct {
	pageMeta
	Run           *model.Run
	Job           *model.Job
	Events        []model.RunEvent
	LogContent    string
	ResultContent string
	CanCancel     bool
}

func New(st *store.Store, reader *logwriter.Reader) *UI {
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
		"formatValue": func(v any) string {
			switch x := v.(type) {
			case nil:
				return "—"
			case string:
				if strings.TrimSpace(x) == "" {
					return "—"
				}
				return x
			case *string:
				if x == nil || strings.TrimSpace(*x) == "" {
					return "—"
				}
				return *x
			case bool:
				if x {
					return "true"
				}
				return "false"
			default:
				return fmt.Sprint(x)
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
	}).ParseFS(templatesFS, "templates/*.tmpl"))

	return &UI{
		store:  st,
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
	r.GET("/jobs/:jobId", u.jobDetail)
	r.GET("/jobs/:jobId/edit", u.editJob)
	r.GET("/runs", u.listRuns)
	r.GET("/runs/:runId", u.runDetail)
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

	jobs, total, err := u.store.Jobs.List(c.Request.Context(), filter, store.Page{Page: pageNum, Size: size})
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
		Enabled:      true,
		SourceType:   model.JobSourceTypeLocal,
		ScheduleType: model.ScheduleTypeInterval,
		Timezone:     "UTC",
	}
	u.renderOrError(c, http.StatusOK, "base", jobFormPage{
		pageMeta: pageMeta{
			Title:           "New Job",
			Subtitle:        "Create a new scheduled task",
			ActiveNav:       "jobs",
			ContentTemplate: "job_form_content",
		},
		Mode: "create",
		Job:  job,
	})
}

func (u *UI) editJob(c *gin.Context) {
	jobID, err := parseID(c.Param("jobId"))
	if err != nil {
		u.renderError(c, http.StatusBadRequest, "Job edit", err)
		return
	}
	job, err := u.store.Jobs.Get(c.Request.Context(), jobID)
	if err != nil {
		u.renderError(c, http.StatusNotFound, "Job edit", err)
		return
	}
	u.renderOrError(c, http.StatusOK, "base", jobFormPage{
		pageMeta: pageMeta{
			Title:           fmt.Sprintf("Edit Job #%d", job.ID),
			Subtitle:        "Update the job configuration",
			ActiveNav:       "jobs",
			ContentTemplate: "job_form_content",
		},
		Mode: "edit",
		Job:  job,
	})
}

func (u *UI) jobDetail(c *gin.Context) {
	jobID, err := parseID(c.Param("jobId"))
	if err != nil {
		u.renderError(c, http.StatusBadRequest, "Job detail", err)
		return
	}
	job, err := u.store.Jobs.Get(c.Request.Context(), jobID)
	if err != nil {
		u.renderError(c, http.StatusNotFound, "Job detail", err)
		return
	}
	runs, total, err := u.store.Runs.ListByJob(c.Request.Context(), jobID, store.Page{Page: 1, Size: 10})
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

	runs, total, err := u.store.Runs.List(c.Request.Context(), filter, store.Page{Page: pageNum, Size: size})
	if err != nil {
		u.renderError(c, http.StatusInternalServerError, "Runs", err)
		return
	}

	items := make([]runListItem, 0, len(runs))
	for i := range runs {
		job, _ := u.store.Jobs.Get(c.Request.Context(), runs[i].JobID)
		item := runListItem{Run: runs[i]}
		if job != nil {
			item.JobName = job.Name
		}
		items = append(items, item)
	}

	u.renderOrError(c, http.StatusOK, "base", runsListPage{
		pageMeta: pageMeta{
			Title:           "Runs",
			Subtitle:        "Recent run history and execution state",
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
	run, err := u.store.Runs.Get(c.Request.Context(), runID)
	if err != nil {
		u.renderError(c, http.StatusNotFound, "Run detail", err)
		return
	}
	job, _ := u.store.Jobs.Get(c.Request.Context(), run.JobID)
	events, err := u.store.Events.ListByRun(c.Request.Context(), runID)
	if err != nil {
		u.renderError(c, http.StatusInternalServerError, "Run detail", err)
		return
	}

	logContent := ""
	if run.LogPath != nil && *run.LogPath != "" {
		if content, _, _, err := u.reader.ReadContent(*run.LogPath, 0, 0, 8192); err == nil {
			logContent = content
		}
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
		ResultContent: resultContent,
		CanCancel:     run.Status == model.RunStatusPending || run.Status == model.RunStatusRunning || run.Status == model.RunStatusCancelling,
	})
}

func (u *UI) render(c *gin.Context, status int, name string, data any) error {
	c.Status(status)
	c.Header("Content-Type", "text/html; charset=utf-8")
	return u.tpl.ExecuteTemplate(c.Writer, name, data)
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
