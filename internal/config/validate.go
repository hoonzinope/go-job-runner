package config

import (
	"fmt"
	"log"
)

func (c *Config) Validate() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		log.Printf("Invalid server port: %d", c.Server.Port)
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}
	if c.Server.Host == "" {
		log.Printf("Server host is required")
		return fmt.Errorf("server host is required")
	}
	if c.Store.SQLitePath == "" {
		log.Printf("Store sqlite_path is required")
		return fmt.Errorf("store sqlite_path is required")
	}
	if c.Store.LogRoot == "" {
		log.Printf("Store log_root is required")
		return fmt.Errorf("store log_root is required")
	}
	if c.Store.ArtifactRoot == "" {
		log.Printf("Store artifact_root is required")
		return fmt.Errorf("store artifact_root is required")
	}
	if c.Scheduler.DueJobScanIntervalSec <= 0 {
		log.Printf("Scheduler due_job_scan_interval_sec must be > 0")
		return fmt.Errorf("scheduler due_job_scan_interval_sec must be > 0")
	}
	if c.Scheduler.DispatchScanIntervalSec <= 0 {
		log.Printf("Scheduler dispatch_scan_interval_sec must be > 0")
		return fmt.Errorf("scheduler dispatch_scan_interval_sec must be > 0")
	}
	if c.Scheduler.MaxConcurrentRuns <= 0 {
		log.Printf("Scheduler max_concurrent_runs must be > 0")
		return fmt.Errorf("scheduler max_concurrent_runs must be > 0")
	}
	return nil
}
