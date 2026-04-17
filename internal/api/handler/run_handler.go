package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	logwriter "github.com/hoonzinope/go-job-runner/internal/log"
	"github.com/hoonzinope/go-job-runner/internal/model"
	"github.com/hoonzinope/go-job-runner/internal/store"
)

type RunHandler struct {
	store  *store.Store
	reader *logwriter.Reader
}

func NewRunHandler(st *store.Store, reader *logwriter.Reader) *RunHandler {
	if reader == nil {
		reader = logwriter.NewReader()
	}
	return &RunHandler{store: st, reader: reader}
}

func (h *RunHandler) ListRuns(c *gin.Context) {
	page, size := parsePageQuery(c)
	var filter store.RunFilter
	if jobID := c.Query("jobId"); jobID != "" {
		n, err := strconv.ParseInt(jobID, 10, 64)
		if err != nil {
			badRequest(c, fmt.Errorf("invalid jobId: %w", err))
			return
		}
		filter.JobID = &n
	}
	if status := c.Query("status"); status != "" {
		v := model.RunStatus(status)
		filter.Status = &v
	}
	if from := c.Query("from"); from != "" {
		t, err := time.Parse(time.RFC3339, from)
		if err != nil {
			badRequest(c, fmt.Errorf("invalid from: %w", err))
			return
		}
		filter.From = &t
	}
	if to := c.Query("to"); to != "" {
		t, err := time.Parse(time.RFC3339, to)
		if err != nil {
			badRequest(c, fmt.Errorf("invalid to: %w", err))
			return
		}
		filter.To = &t
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

func (h *RunHandler) GetRun(c *gin.Context) {
	runID, err := parseIntParam(c, "runId")
	if err != nil {
		badRequest(c, fmt.Errorf("invalid runId: %w", err))
		return
	}

	run, err := h.store.Runs.Get(c.Request.Context(), runID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			notFound(c, fmt.Errorf("run %d not found", runID))
			return
		}
		internalError(c, err)
		return
	}

	c.JSON(http.StatusOK, toRunResponse(run))
}

func (h *RunHandler) CancelRun(c *gin.Context) {
	runID, err := parseIntParam(c, "runId")
	if err != nil {
		badRequest(c, fmt.Errorf("invalid runId: %w", err))
		return
	}

	run, err := h.store.Runs.Get(c.Request.Context(), runID)
	if err != nil {
		internalError(c, err)
		return
	}

	switch run.Status {
	case model.RunStatusPending:
		run.Status = model.RunStatusCancelled
	case model.RunStatusRunning, model.RunStatusCancelling:
		run.Status = model.RunStatusCancelling
	default:
		c.JSON(http.StatusOK, toRunResponse(run))
		return
	}

	if err := h.store.Runs.UpdateStatus(c.Request.Context(), run.ID, run.Status, run.StartedAt, run.FinishedAt, run.ExitCode, run.ErrorMessage); err != nil {
		internalError(c, err)
		return
	}
	run.UpdatedAt = time.Now().UTC()
	c.JSON(http.StatusOK, toRunResponse(run))
}

func (h *RunHandler) ListRunEvents(c *gin.Context) {
	runID, err := parseIntParam(c, "runId")
	if err != nil {
		badRequest(c, fmt.Errorf("invalid runId: %w", err))
		return
	}

	events, err := h.store.Events.ListByRun(c.Request.Context(), runID)
	if err != nil {
		internalError(c, err)
		return
	}

	items := make([]runEventResponse, 0, len(events))
	for i := range events {
		items = append(items, runEventToResponse(&events[i]))
	}
	c.JSON(http.StatusOK, listResponse[runEventResponse]{Items: items, Total: int64(len(items)), Page: 1, Size: len(items)})
}

func (h *RunHandler) GetRunLogs(c *gin.Context) {
	runID, err := parseIntParam(c, "runId")
	if err != nil {
		badRequest(c, fmt.Errorf("invalid runId: %w", err))
		return
	}

	run, err := h.store.Runs.Get(c.Request.Context(), runID)
	if err != nil {
		internalError(c, err)
		return
	}
	if run.LogPath == nil || *run.LogPath == "" {
		c.JSON(http.StatusOK, logResponse{RunID: runID, Offset: 0, Size: 0, Content: ""})
		return
	}

	offset := parseInt64Default(c.Query("offset"), 0)
	limit := parseInt64Default(c.Query("limit"), 0)
	tail := parseInt64Default(c.Query("tail"), 0)

	content, start, size, err := h.reader.ReadContent(*run.LogPath, offset, limit, tail)
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, logResponse{RunID: runID, Offset: start, Size: size, Content: content})
}

func (h *RunHandler) GetRunResult(c *gin.Context) {
	runID, err := parseIntParam(c, "runId")
	if err != nil {
		badRequest(c, fmt.Errorf("invalid runId: %w", err))
		return
	}

	run, err := h.store.Runs.Get(c.Request.Context(), runID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			notFound(c, fmt.Errorf("run %d not found", runID))
			return
		}
		internalError(c, err)
		return
	}
	if run.ResultPath == nil || *run.ResultPath == "" {
		c.JSON(http.StatusOK, resultResponse{})
		return
	}

	var payload resultResponse
	if err := h.reader.ReadJSON(*run.ResultPath, &payload); err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, payload)
}

func parseInt64Default(value string, fallback int64) int64 {
	if value == "" {
		return fallback
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

func toRunResponse(run *model.Run) runResponse {
	return runResponse{
		ID:           run.ID,
		JobID:        run.JobID,
		ScheduledAt:  run.ScheduledAt,
		StartedAt:    run.StartedAt,
		FinishedAt:   run.FinishedAt,
		Status:       run.Status,
		Attempt:      run.Attempt,
		ExitCode:     run.ExitCode,
		ErrorMessage: run.ErrorMessage,
		LogPath:      run.LogPath,
		ResultPath:   run.ResultPath,
		CreatedAt:    run.CreatedAt,
		UpdatedAt:    run.UpdatedAt,
	}
}

func runEventToResponse(event *model.RunEvent) runEventResponse {
	return runEventResponse{
		ID:        event.ID,
		RunID:     event.RunID,
		EventType: event.EventType,
		Message:   event.Message,
		CreatedAt: event.CreatedAt,
	}
}
