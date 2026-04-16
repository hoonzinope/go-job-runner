package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"

	"github.com/hoonzinope/go-job-runner/internal/model"
	"github.com/hoonzinope/go-job-runner/internal/store"
)

type JobHandler struct {
	store *store.Store
}

func NewJobHandler(st *store.Store) *JobHandler {
	return &JobHandler{store: st}
}

func (h *JobHandler) ListJobs(c *gin.Context) {
	page, size := parsePageQuery(c)

	var filter store.JobFilter
	if enabled, err := parseBoolPtr(c.Query("enabled")); err == nil {
		filter.Enabled = enabled
	} else {
		badRequest(c, fmt.Errorf("invalid enabled query: %w", err))
		return
	}
	if scheduleType := c.Query("scheduleType"); scheduleType != "" {
		v := model.ScheduleType(scheduleType)
		filter.ScheduleType = &v
	}
	filter.Name = c.Query("name")

	jobs, total, err := h.store.Jobs.List(c.Request.Context(), filter, store.Page{Page: page, Size: size})
	if err != nil {
		internalError(c, err)
		return
	}

	items := make([]jobResponse, 0, len(jobs))
	for i := range jobs {
		items = append(items, toJobResponse(&jobs[i]))
	}
	c.JSON(http.StatusOK, listResponse[jobResponse]{Items: items, Total: total, Page: page, Size: size})
}

func (h *JobHandler) GetJob(c *gin.Context) {
	jobID, err := parseIntParam(c, "jobId")
	if err != nil {
		badRequest(c, fmt.Errorf("invalid jobId: %w", err))
		return
	}

	job, err := h.store.Jobs.Get(c.Request.Context(), jobID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			notFound(c, fmt.Errorf("job %d not found", jobID))
			return
		}
		internalError(c, err)
		return
	}

	c.JSON(http.StatusOK, toJobResponse(job))
}

func (h *JobHandler) CreateJob(c *gin.Context) {
	var req jobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}

	job, params, err := jobFromRequest(req)
	if err != nil {
		badRequest(c, err)
		return
	}
	job.ParamsJSON = params

	nextRunAt, err := nextRunTime(job, time.Now().UTC())
	if err != nil {
		badRequest(c, err)
		return
	}
	job.NextRunAt = nextRunAt

	if _, err := h.store.Jobs.Create(c.Request.Context(), job); err != nil {
		internalError(c, err)
		return
	}

	c.JSON(http.StatusCreated, toJobResponse(job))
}

func (h *JobHandler) UpdateJob(c *gin.Context) {
	jobID, err := parseIntParam(c, "jobId")
	if err != nil {
		badRequest(c, fmt.Errorf("invalid jobId: %w", err))
		return
	}

	var req jobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}

	job, params, err := jobFromRequest(req)
	if err != nil {
		badRequest(c, err)
		return
	}
	job.ID = jobID
	job.ParamsJSON = params

	nextRunAt, err := nextRunTime(job, time.Now().UTC())
	if err != nil {
		badRequest(c, err)
		return
	}
	job.NextRunAt = nextRunAt

	if err := h.store.Jobs.Update(c.Request.Context(), job); err != nil {
		internalError(c, err)
		return
	}

	c.JSON(http.StatusOK, toJobResponse(job))
}

func (h *JobHandler) DeleteJob(c *gin.Context) {
	jobID, err := parseIntParam(c, "jobId")
	if err != nil {
		badRequest(c, fmt.Errorf("invalid jobId: %w", err))
		return
	}

	if err := h.store.Jobs.Delete(c.Request.Context(), jobID); err != nil {
		internalError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *JobHandler) TriggerJob(c *gin.Context) {
	jobID, err := parseIntParam(c, "jobId")
	if err != nil {
		badRequest(c, fmt.Errorf("invalid jobId: %w", err))
		return
	}

	job, err := h.store.Jobs.Get(c.Request.Context(), jobID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			notFound(c, fmt.Errorf("job %d not found", jobID))
			return
		}
		internalError(c, err)
		return
	}

	type triggerRequest struct {
		Reason *string `json:"reason"`
	}
	var req triggerRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, err)
			return
		}
	}

	now := time.Now().UTC()
	run := &model.Run{
		JobID:       job.ID,
		ScheduledAt: now,
		Status:      model.RunStatusPending,
		Attempt:     0,
	}

	if err := h.store.WithinTx(c.Request.Context(), func(tx *store.TxStore) error {
		if _, err := tx.Runs.Create(c.Request.Context(), run); err != nil {
			return err
		}
		event := &model.RunEvent{
			RunID:     run.ID,
			EventType: model.RunEventTypeCreated,
		}
		if req.Reason != nil && *req.Reason != "" {
			event.Message = req.Reason
		}
		if _, err := tx.Events.Create(c.Request.Context(), event); err != nil {
			return err
		}
		return nil
	}); err != nil {
		internalError(c, err)
		return
	}

	c.JSON(http.StatusCreated, toRunResponse(run))
}

func (h *JobHandler) ListJobRuns(c *gin.Context) {
	jobID, err := parseIntParam(c, "jobId")
	if err != nil {
		badRequest(c, fmt.Errorf("invalid jobId: %w", err))
		return
	}

	page, size := parsePageQuery(c)
	var filter store.RunFilter
	filter.JobID = &jobID
	if status := c.Query("status"); status != "" {
		v := model.RunStatus(status)
		filter.Status = &v
	}

	runs, total, err := h.store.Runs.List(c.Request.Context(), filter, store.Page{Page: page, Size: size})
	if err != nil {
		internalError(c, err)
		return
	}

	items := make([]runResponse, 0, len(runs))
	for i := range runs {
		items = append(items, toRunResponse(&runs[i]))
	}
	c.JSON(http.StatusOK, listResponse[runResponse]{Items: items, Total: total, Page: page, Size: size})
}

func jobFromRequest(req jobRequest) (*model.Job, *string, error) {
	if req.Name == "" {
		return nil, nil, fmt.Errorf("name is required")
	}
	if !req.SourceType.IsValid() {
		return nil, nil, fmt.Errorf("invalid sourceType: %q", req.SourceType)
	}
	if req.ImageRef == "" {
		return nil, nil, fmt.Errorf("imageRef is required")
	}
	if !req.ScheduleType.IsValid() {
		return nil, nil, fmt.Errorf("invalid scheduleType: %q", req.ScheduleType)
	}
	if !req.ConcurrencyPolicy.IsValid() {
		return nil, nil, fmt.Errorf("invalid concurrencyPolicy: %q", req.ConcurrencyPolicy)
	}
	if req.Timezone == "" {
		req.Timezone = "UTC"
	}
	if req.ScheduleType == model.ScheduleTypeInterval {
		if req.IntervalSec == nil || *req.IntervalSec <= 0 {
			return nil, nil, fmt.Errorf("intervalSec must be > 0 for interval jobs")
		}
		if req.ScheduleExpr != nil && *req.ScheduleExpr != "" {
			return nil, nil, fmt.Errorf("scheduleExpr must be empty for interval jobs")
		}
	}
	if req.ScheduleType == model.ScheduleTypeCron {
		if req.ScheduleExpr == nil || *req.ScheduleExpr == "" {
			return nil, nil, fmt.Errorf("scheduleExpr is required for cron jobs")
		}
	}

	job := &model.Job{
		Name:              req.Name,
		Description:       req.Description,
		Enabled:           req.Enabled,
		SourceType:        req.SourceType,
		ImageRef:          req.ImageRef,
		ImageDigest:       req.ImageDigest,
		ScheduleType:      req.ScheduleType,
		ScheduleExpr:      req.ScheduleExpr,
		IntervalSec:       req.IntervalSec,
		Timezone:          req.Timezone,
		ConcurrencyPolicy: req.ConcurrencyPolicy,
		RetryLimit:        req.RetryLimit,
		TimeoutSec:        req.TimeoutSec,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}

	var params *string
	if len(req.Params) > 0 {
		raw := string(req.Params)
		params = &raw
	}
	return job, params, nil
}

func nextRunTime(job *model.Job, from time.Time) (*time.Time, error) {
	switch job.ScheduleType {
	case model.ScheduleTypeInterval:
		if job.IntervalSec == nil {
			return nil, fmt.Errorf("intervalSec is required")
		}
		next := from.Add(time.Duration(*job.IntervalSec) * time.Second).UTC()
		return &next, nil
	case model.ScheduleTypeCron:
		if job.ScheduleExpr == nil || *job.ScheduleExpr == "" {
			return nil, fmt.Errorf("scheduleExpr is required")
		}
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
		sched, err := parser.Parse(*job.ScheduleExpr)
		if err != nil {
			return nil, fmt.Errorf("parse cron expression: %w", err)
		}
		loc := time.UTC
		if job.Timezone != "" {
			if loaded, err := time.LoadLocation(job.Timezone); err == nil {
				loc = loaded
			}
		}
		next := sched.Next(from.In(loc)).UTC()
		return &next, nil
	default:
		return nil, fmt.Errorf("unsupported schedule type: %q", job.ScheduleType)
	}
}

func toJobResponse(job *model.Job) jobResponse {
	return jobResponse{
		ID:                job.ID,
		Name:              job.Name,
		Description:       job.Description,
		Enabled:           job.Enabled,
		SourceType:        job.SourceType,
		ImageRef:          job.ImageRef,
		ImageDigest:       job.ImageDigest,
		ScheduleType:      job.ScheduleType,
		ScheduleExpr:      job.ScheduleExpr,
		IntervalSec:       job.IntervalSec,
		Params:            rawJSON(job.ParamsJSON),
		Timezone:          job.Timezone,
		ConcurrencyPolicy: job.ConcurrencyPolicy,
		RetryLimit:        job.RetryLimit,
		TimeoutSec:        job.TimeoutSec,
		NextRunAt:         job.NextRunAt,
		LastScheduledAt:   job.LastScheduledAt,
		CreatedAt:         job.CreatedAt,
		UpdatedAt:         job.UpdatedAt,
	}
}

func rawJSON(v *string) json.RawMessage {
	if v == nil || *v == "" {
		return nil
	}
	return json.RawMessage(*v)
}
