package config

import (
	"fmt"
	"log"
	"strings"
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
	if c.Scheduler.DefaultTimeoutSec < 0 {
		return fmt.Errorf("scheduler default_timeout_sec must be >= 0")
	}
	if c.Scheduler.DefaultTimeoutSec == 0 && !c.Scheduler.AllowUnlimitedTimeout {
		return fmt.Errorf("scheduler default_timeout_sec must be > 0 unless allow_unlimited_timeout is true")
	}
	if c.Scheduler.MaxTimeoutSec <= 0 {
		return fmt.Errorf("scheduler max_timeout_sec must be > 0")
	}
	if c.Scheduler.DefaultTimeoutSec > c.Scheduler.MaxTimeoutSec {
		return fmt.Errorf("scheduler default_timeout_sec must be <= max_timeout_sec")
	}
	if len(c.Image.AllowedSources) == 0 {
		log.Printf("Image allowed_sources is required")
		return fmt.Errorf("image allowed_sources is required")
	}
	if c.Image.DefaultSource == "" {
		log.Printf("Image default_source is required")
		return fmt.Errorf("image default_source is required")
	}
	if c.Image.PullPolicy == "" {
		log.Printf("Image pull_policy is required")
		return fmt.Errorf("image pull_policy is required")
	}

	validSource := map[string]struct{}{
		"local":  {},
		"remote": {},
	}
	for _, source := range c.Image.AllowedSources {
		if _, ok := validSource[source]; !ok {
			return fmt.Errorf("unsupported image allowed_source: %q", source)
		}
	}
	if _, ok := validSource[c.Image.DefaultSource]; !ok {
		return fmt.Errorf("unsupported image default_source: %q", c.Image.DefaultSource)
	}
	if !containsString(c.Image.AllowedSources, c.Image.DefaultSource) {
		return fmt.Errorf("default_source %q must be included in allowed_sources", c.Image.DefaultSource)
	}
	validPullPolicy := map[string]struct{}{
		"always":         {},
		"if_not_present": {},
		"never":          {},
	}
	if _, ok := validPullPolicy[strings.ToLower(c.Image.PullPolicy)]; !ok {
		return fmt.Errorf("unsupported image pull_policy: %q", c.Image.PullPolicy)
	}
	remoteEnabled := containsString(c.Image.AllowedSources, "remote") || c.Image.DefaultSource == "remote"
	if remoteEnabled && c.Image.Remote.Endpoint == "" {
		return fmt.Errorf("image remote.endpoint is required when remote source is enabled")
	}

	switch strings.ToLower(strings.TrimSpace(c.Executor.NetworkMode)) {
	case "", "bridge", "none":
	default:
		return fmt.Errorf("unsupported executor network_mode: %q", c.Executor.NetworkMode)
	}
	if c.Executor.MemoryLimitMB < 0 {
		return fmt.Errorf("executor memory_limit_mb must be >= 0")
	}
	if c.Executor.CPULimit < 0 {
		return fmt.Errorf("executor cpu_limit must be >= 0")
	}
	if c.Executor.StopGracePeriodSec < 0 {
		return fmt.Errorf("executor stop_grace_period_sec must be >= 0")
	}
	return nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
