package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/hoonzinope/go-job-runner/internal/model"
	"github.com/hoonzinope/go-job-runner/internal/service"
	"github.com/hoonzinope/go-job-runner/internal/store"
)

type JobHandler struct {
	service *service.JobService
}

func NewJobHandler(svc *service.JobService) *JobHandler {
	return &JobHandler{service: svc}
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

	jobs, total, err := h.service.ListJobs(c.Request.Context(), filter, store.Page{Page: page, Size: size})
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

	job, err := h.service.GetJob(c.Request.Context(), jobID)
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

	input, err := jobInputFromRequest(req)
	if err != nil {
		badRequest(c, err)
		return
	}

	created, err := h.service.CreateJob(c.Request.Context(), input)
	if err != nil {
		internalError(c, err)
		return
	}

	c.JSON(http.StatusCreated, toJobResponse(created))
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

	input, err := jobInputFromRequest(req)
	if err != nil {
		badRequest(c, err)
		return
	}

	updated, err := h.service.UpdateJob(c.Request.Context(), jobID, input)
	if err != nil {
		internalError(c, err)
		return
	}

	c.JSON(http.StatusOK, toJobResponse(updated))
}

func (h *JobHandler) DeleteJob(c *gin.Context) {
	jobID, err := parseIntParam(c, "jobId")
	if err != nil {
		badRequest(c, fmt.Errorf("invalid jobId: %w", err))
		return
	}

	if err := h.service.DeleteJob(c.Request.Context(), jobID); err != nil {
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

	run, err := h.service.TriggerJob(c.Request.Context(), jobID, req.Reason)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			notFound(c, fmt.Errorf("job %d not found", jobID))
			return
		}
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
	var status *model.RunStatus
	if statusValue := c.Query("status"); statusValue != "" {
		v := model.RunStatus(statusValue)
		status = &v
	}

	runs, total, err := h.service.ListJobRuns(c.Request.Context(), jobID, status, store.Page{Page: page, Size: size})
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

func jobInputFromRequest(req jobRequest) (service.JobInput, error) {
	if len(req.Params) == 0 {
		return service.JobInput{
			Name:              req.Name,
			Description:       req.Description,
			Enabled:           req.Enabled,
			SourceType:        req.SourceType,
			ImageRef:          req.ImageRef,
			ImageDigest:       req.ImageDigest,
			ScheduleType:      req.ScheduleType,
			ScheduleExpr:      req.ScheduleExpr,
			IntervalSec:       req.IntervalSec,
			ParamsJSON:        nil,
			ConcurrencyPolicy: req.ConcurrencyPolicy,
			RetryLimit:        req.RetryLimit,
			TimeoutSec:        req.TimeoutSec,
			Timezone:          req.Timezone,
		}, nil
	}
	raw := string(req.Params)
	return service.JobInput{
		Name:              req.Name,
		Description:       req.Description,
		Enabled:           req.Enabled,
		SourceType:        req.SourceType,
		ImageRef:          req.ImageRef,
		ImageDigest:       req.ImageDigest,
		ScheduleType:      req.ScheduleType,
		ScheduleExpr:      req.ScheduleExpr,
		IntervalSec:       req.IntervalSec,
		ParamsJSON:        &raw,
		ConcurrencyPolicy: req.ConcurrencyPolicy,
		RetryLimit:        req.RetryLimit,
		TimeoutSec:        req.TimeoutSec,
		Timezone:          req.Timezone,
	}, nil
}
