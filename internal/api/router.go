package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoonzinope/go-job-runner/internal/api/handler"
	"github.com/hoonzinope/go-job-runner/internal/config"
	"github.com/hoonzinope/go-job-runner/internal/store"
)

type APIServer struct {
	Host  string
	Port  int
	Store *store.Store
}

func NewAPIServer(cfg *config.Config, st *store.Store) *APIServer {
	return &APIServer{
		Host:  cfg.Server.Host,
		Port:  cfg.Server.Port,
		Store: st,
	}
}

func (s *APIServer) setupRouter() *gin.Engine {
	router := gin.Default()
	router.GET("/health", handler.HealthzHandler)

	api := router.Group("/api/v1")
	{
		jobHandler := handler.NewJobHandler(s.Store)
		runHandler := handler.NewRunHandler(s.Store)
		imageHandler := handler.NewImageHandler()

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

		api.GET("/images", imageHandler.ListImages)
		api.GET("/images/resolve", imageHandler.ResolveImage)
		api.GET("/images/:sourceType/candidates", imageHandler.ListImageCandidates)
	}
	return router
}

func (s *APIServer) StartServer(ctx context.Context) error {
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

	// Wait for context cancellation
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	<-quit
	fmt.Println("Shutting down API server...")

	// Create a context with timeout for shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Shutdown the server gracefully
	if err := server.Shutdown(ctx); err != nil {
		return fmt.Errorf("API server shutdown error: %w", err)
	}

	fmt.Println("API server stopped")
	return nil
}
