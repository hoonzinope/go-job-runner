package main

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/hoonzinope/go-job-runner/internal/api"
	"github.com/hoonzinope/go-job-runner/internal/config"
	"github.com/hoonzinope/go-job-runner/internal/scheduler"
	"github.com/hoonzinope/go-job-runner/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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

	st, err := store.Open(cfg.Store.SQLitePath)
	if err != nil {
		fmt.Printf("Error opening store: %v\n", err)
		return
	}
	defer func() {
		if err := st.Close(); err != nil {
			fmt.Printf("Error closing store: %v\n", err)
		}
	}()

	sch := scheduler.NewScheduler(cfg, st)
	go func() {
		if err := sch.Start(ctx); err != nil {
			fmt.Printf("Error starting scheduler: %v\n", err)
		}
	}()

	// start API server
	apiServer := api.NewAPIServer(cfg, st, sch)
	if err := apiServer.StartServer(ctx); err != nil {
		fmt.Printf("Error starting API server: %v\n", err)
	}
}
