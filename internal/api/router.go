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
)

type APIServer struct {
	Host string
	Port int
}

func NewAPIServer(cfg *config.Config) *APIServer {
	return &APIServer{
		Host: cfg.Server.Host,
		Port: cfg.Server.Port,
	}
}

func (s *APIServer) setupRouter() *gin.Engine {
	router := gin.Default()
	router.GET("/health", handler.HealthzHandler)
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
