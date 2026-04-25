package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoonzinope/go-job-runner/internal/api/handler"
	webui "github.com/hoonzinope/go-job-runner/internal/api/ui"
	"github.com/hoonzinope/go-job-runner/internal/config"
	"github.com/hoonzinope/go-job-runner/internal/image"
	logwriter "github.com/hoonzinope/go-job-runner/internal/log"
	"github.com/hoonzinope/go-job-runner/internal/scheduler"
	"github.com/hoonzinope/go-job-runner/internal/service"
	"github.com/hoonzinope/go-job-runner/internal/store"
)

type APIServer struct {
	Host          string
	Port          int
	Store         *store.Store
	Scheduler     *scheduler.Scheduler
	ImageResolver *image.Resolver
}

func NewAPIServer(cfg *config.Config, st *store.Store, sch *scheduler.Scheduler) *APIServer {
	return &APIServer{
		Host:          cfg.Server.Host,
		Port:          cfg.Server.Port,
		Store:         st,
		Scheduler:     sch,
		ImageResolver: image.NewResolver(cfg.Image),
	}
}

func (s *APIServer) setupRouter() *gin.Engine {
	router := gin.Default()
	router.GET("/health", handler.HealthzHandler)
	jobService := service.NewJobService(s.Store, s.Scheduler)
	runService := service.NewRunService(s.Store)
	ui := webui.New(jobService, runService, s.ImageResolver, logwriter.NewReader())
	ui.RegisterRoutes(router)

	api := router.Group("/api/v1")
	{
		jobHandler := handler.NewJobHandler(jobService)
		runHandler := handler.NewRunHandler(runService, logwriter.NewReader())
		imageHandler := handler.NewImageHandler(s.ImageResolver)

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
	return router
}

func (s *APIServer) StartServer(ctx context.Context) error {
	if config.RequiresExternalProtection(s.Host) {
		log.Printf("WARNING: server.host=%q binds the Web UI and REST API on a non-loopback interface. This project does not provide built-in authentication; protect it with a reverse proxy, VPN, or IP allowlist before exposing it externally.", s.Host)
	}

	router := s.setupRouter()
	server := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", s.Host, s.Port),
		Handler: router,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("API server error: %v\n", err)
		}
	}()

	fmt.Printf("API server started on %s:%d\n", s.Host, s.Port)

	<-ctx.Done()
	fmt.Println("Shutting down API server...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("API server shutdown error: %w", err)
	}

	fmt.Println("API server stopped")
	return nil
}
