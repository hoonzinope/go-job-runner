package main

import (
	"context"
	"fmt"

	"github.com/hoonzinope/go-job-runner/internal/api"
	"github.com/hoonzinope/go-job-runner/internal/config"
)

var (
	ctx context.Context
)

func main() {
	// create root context
	ctx = context.Background()

	// load & validate config
	cfg, err := config.LoadConfig(".")
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		return
	}
	if err := cfg.Validate(); err != nil {
		fmt.Printf("Error validating config: %v\n", err)
		return
	}

	// start API server
	apiServer := api.NewAPIServer(cfg)
	if err := apiServer.StartServer(ctx); err != nil {
		fmt.Printf("Error starting API server: %v\n", err)
	}
}
