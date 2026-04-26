package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeConfigFile(t, dir, `
server:
  host: 0.0.0.0
  port: 8080

store:
  sqlite_path: /data/app.db
  log_root: /data/logs
  artifact_root: /data/artifacts
  log_path_pattern: job-%d/run-%d/run.log
  result_path_pattern: job-%d/run-%d/result.json

scheduler:
  due_job_scan_interval_sec: 2
  dispatch_scan_interval_sec: 1
  max_concurrent_runs: 3
  default_timeout_sec: 3600
  max_timeout_sec: 86400
  allow_unlimited_timeout: false

image:
  allowed_sources:
    - local
    - remote
  default_source: local
  pull_policy: if_not_present
  allowed_prefixes:
    - jobs/
  remote:
    endpoint: http://registry:5000
    insecure: true

executor:
  network_mode: bridge
  read_only_rootfs: true
  memory_limit_mb: 512
  cpu_limit: 1.5
  cleanup_containers: true
  stop_grace_period_sec: 10
  orphan_recovery_on_startup: true

retention:
  enabled: true
  prune_interval_sec: 600
  run_history_days: 30
  success_log_days: 7
  failed_log_days: 30
  artifact_days: 14
  max_log_bytes_per_run: 10485760
  max_total_storage_bytes: 10737418240
`)

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Server.Host != "0.0.0.0" || cfg.Server.Port != 8080 {
		t.Fatalf("unexpected server config: %+v", cfg.Server)
	}
	if cfg.Store.SQLitePath != "/data/app.db" || cfg.Store.LogRoot != "/data/logs" || cfg.Store.ArtifactRoot != "/data/artifacts" {
		t.Fatalf("unexpected store config: %+v", cfg.Store)
	}
	if cfg.Scheduler.DueJobScanIntervalSec != 2 || cfg.Scheduler.DispatchScanIntervalSec != 1 || cfg.Scheduler.MaxConcurrentRuns != 3 || cfg.Scheduler.DefaultTimeoutSec != 3600 || cfg.Scheduler.MaxTimeoutSec != 86400 || cfg.Scheduler.AllowUnlimitedTimeout {
		t.Fatalf("unexpected scheduler config: %+v", cfg.Scheduler)
	}
	if len(cfg.Image.AllowedSources) != 2 || cfg.Image.DefaultSource != "local" || cfg.Image.PullPolicy != "if_not_present" {
		t.Fatalf("unexpected image config: %+v", cfg.Image)
	}
	if cfg.Image.Remote.Endpoint != "http://registry:5000" || !cfg.Image.Remote.Insecure {
		t.Fatalf("unexpected remote config: %+v", cfg.Image.Remote)
	}
	if cfg.Executor.NetworkMode != "bridge" || !cfg.Executor.ReadOnlyRootFS || cfg.Executor.MemoryLimitMB != 512 || cfg.Executor.CPULimit != 1.5 || !cfg.Executor.CleanupContainers || cfg.Executor.StopGracePeriodSec != 10 || !cfg.Executor.OrphanRecoveryOnStartup {
		t.Fatalf("unexpected executor config: %+v", cfg.Executor)
	}
	if !cfg.Retention.Enabled || cfg.Retention.PruneIntervalSec != 600 || cfg.Retention.RunHistoryDays != 30 || cfg.Retention.SuccessLogDays != 7 || cfg.Retention.FailedLogDays != 30 || cfg.Retention.ArtifactDays != 14 || cfg.Retention.MaxLogBytesPerRun != 10485760 || cfg.Retention.MaxTotalStorageBytes != 10737418240 {
		t.Fatalf("unexpected retention config: %+v", cfg.Retention)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	t.Parallel()

	if _, err := LoadConfig(t.TempDir()); err == nil {
		t.Fatal("expected load error for missing config")
	}
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "happy path",
		},
		{
			name: "invalid port",
			mutate: func(c *Config) {
				c.Server.Port = 0
			},
			wantErr: "invalid server port: 0",
		},
		{
			name: "missing host",
			mutate: func(c *Config) {
				c.Server.Host = ""
			},
			wantErr: "server host is required",
		},
		{
			name: "missing sqlite path",
			mutate: func(c *Config) {
				c.Store.SQLitePath = ""
			},
			wantErr: "store sqlite_path is required",
		},
		{
			name: "missing log root",
			mutate: func(c *Config) {
				c.Store.LogRoot = ""
			},
			wantErr: "store log_root is required",
		},
		{
			name: "missing artifact root",
			mutate: func(c *Config) {
				c.Store.ArtifactRoot = ""
			},
			wantErr: "store artifact_root is required",
		},
		{
			name: "missing due interval",
			mutate: func(c *Config) {
				c.Scheduler.DueJobScanIntervalSec = 0
			},
			wantErr: "scheduler due_job_scan_interval_sec must be > 0",
		},
		{
			name: "missing dispatch interval",
			mutate: func(c *Config) {
				c.Scheduler.DispatchScanIntervalSec = 0
			},
			wantErr: "scheduler dispatch_scan_interval_sec must be > 0",
		},
		{
			name: "missing max concurrent",
			mutate: func(c *Config) {
				c.Scheduler.MaxConcurrentRuns = 0
			},
			wantErr: "scheduler max_concurrent_runs must be > 0",
		},
		{
			name: "negative default timeout",
			mutate: func(c *Config) {
				c.Scheduler.DefaultTimeoutSec = -1
			},
			wantErr: "scheduler default_timeout_sec must be >= 0",
		},
		{
			name: "zero default timeout requires unlimited opt in",
			mutate: func(c *Config) {
				c.Scheduler.DefaultTimeoutSec = 0
			},
			wantErr: "scheduler default_timeout_sec must be > 0 unless allow_unlimited_timeout is true",
		},
		{
			name: "missing max timeout",
			mutate: func(c *Config) {
				c.Scheduler.MaxTimeoutSec = 0
			},
			wantErr: "scheduler max_timeout_sec must be > 0",
		},
		{
			name: "default timeout above max",
			mutate: func(c *Config) {
				c.Scheduler.DefaultTimeoutSec = 100
				c.Scheduler.MaxTimeoutSec = 99
			},
			wantErr: "scheduler default_timeout_sec must be <= max_timeout_sec",
		},
		{
			name: "missing allowed sources",
			mutate: func(c *Config) {
				c.Image.AllowedSources = nil
			},
			wantErr: "image allowed_sources is required",
		},
		{
			name: "missing default source",
			mutate: func(c *Config) {
				c.Image.DefaultSource = ""
			},
			wantErr: "image default_source is required",
		},
		{
			name: "missing pull policy",
			mutate: func(c *Config) {
				c.Image.PullPolicy = ""
			},
			wantErr: "image pull_policy is required",
		},
		{
			name: "unsupported allowed source",
			mutate: func(c *Config) {
				c.Image.AllowedSources = []string{"local", "bogus"}
			},
			wantErr: `unsupported image allowed_source: "bogus"`,
		},
		{
			name: "unsupported default source",
			mutate: func(c *Config) {
				c.Image.DefaultSource = "bogus"
			},
			wantErr: `unsupported image default_source: "bogus"`,
		},
		{
			name: "default source not allowed",
			mutate: func(c *Config) {
				c.Image.AllowedSources = []string{"local"}
				c.Image.DefaultSource = "remote"
			},
			wantErr: `default_source "remote" must be included in allowed_sources`,
		},
		{
			name: "invalid pull policy",
			mutate: func(c *Config) {
				c.Image.PullPolicy = "sometimes"
			},
			wantErr: `unsupported image pull_policy: "sometimes"`,
		},
		{
			name: "remote requires endpoint",
			mutate: func(c *Config) {
				c.Image.AllowedSources = []string{"local", "remote"}
				c.Image.DefaultSource = "remote"
				c.Image.Remote.Endpoint = ""
			},
			wantErr: "image remote.endpoint is required when remote source is enabled",
		},
		{
			name: "unsupported executor network mode",
			mutate: func(c *Config) {
				c.Executor.NetworkMode = "host"
			},
			wantErr: `unsupported executor network_mode: "host"`,
		},
		{
			name: "negative executor memory limit",
			mutate: func(c *Config) {
				c.Executor.MemoryLimitMB = -1
			},
			wantErr: "executor memory_limit_mb must be >= 0",
		},
		{
			name: "negative executor cpu limit",
			mutate: func(c *Config) {
				c.Executor.CPULimit = -0.5
			},
			wantErr: "executor cpu_limit must be >= 0",
		},
		{
			name: "negative executor stop grace period",
			mutate: func(c *Config) {
				c.Executor.StopGracePeriodSec = -1
			},
			wantErr: "executor stop_grace_period_sec must be >= 0",
		},
		{
			name: "missing retention prune interval",
			mutate: func(c *Config) {
				c.Retention.PruneIntervalSec = 0
			},
			wantErr: "retention prune_interval_sec must be > 0",
		},
		{
			name: "negative retention run history days",
			mutate: func(c *Config) {
				c.Retention.RunHistoryDays = -1
			},
			wantErr: "retention run_history_days must be >= 0",
		},
		{
			name: "negative retention max log bytes",
			mutate: func(c *Config) {
				c.Retention.MaxLogBytesPerRun = -1
			},
			wantErr: "retention max_log_bytes_per_run must be >= 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := validConfig()
			if tt.mutate != nil {
				tt.mutate(&cfg)
			}

			err := cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tt.wantErr)
			}
			if err.Error() != tt.wantErr {
				t.Fatalf("unexpected error: got %q want %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestRequiresExternalProtection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		host string
		want bool
	}{
		{name: "localhost", host: "localhost", want: false},
		{name: "ipv4 loopback", host: "127.0.0.1", want: false},
		{name: "ipv6 loopback", host: "::1", want: false},
		{name: "unspecified ipv4", host: "0.0.0.0", want: true},
		{name: "unspecified ipv6", host: "::", want: true},
		{name: "private ipv4", host: "192.168.1.10", want: true},
		{name: "hostname", host: "runner.local", want: true},
		{name: "bracketed ipv6 loopback", host: "[::1]", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := RequiresExternalProtection(tt.host); got != tt.want {
				t.Fatalf("RequiresExternalProtection(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func validConfig() Config {
	return Config{
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 8080,
		},
		Store: StoreConfig{
			SQLitePath:        "/data/app.db",
			LogRoot:           "/data/logs",
			ArtifactRoot:      "/data/artifacts",
			LogPathPattern:    "job-%d/run-%d/run.log",
			ResultPathPattern: "job-%d/run-%d/result.json",
		},
		Scheduler: SchedulerConfig{
			DueJobScanIntervalSec:   2,
			DispatchScanIntervalSec: 1,
			MaxConcurrentRuns:       3,
			DefaultTimeoutSec:       3600,
			MaxTimeoutSec:           86400,
		},
		Image: ImageConfig{
			AllowedSources:  []string{"local", "remote"},
			DefaultSource:   "local",
			PullPolicy:      "if_not_present",
			AllowedPrefixes: []string{"jobs/"},
			Remote: ImageRemoteConfig{
				Endpoint: "http://registry:5000",
				Insecure: true,
			},
		},
		Executor: ExecutorConfig{
			NetworkMode:             "bridge",
			ReadOnlyRootFS:          true,
			MemoryLimitMB:           512,
			CPULimit:                1.5,
			CleanupContainers:       true,
			StopGracePeriodSec:      10,
			OrphanRecoveryOnStartup: true,
		},
		Retention: RetentionConfig{
			Enabled:              true,
			PruneIntervalSec:     3600,
			RunHistoryDays:       30,
			SuccessLogDays:       7,
			FailedLogDays:        30,
			ArtifactDays:         14,
			MaxLogBytesPerRun:    10485760,
			MaxTotalStorageBytes: 10737418240,
		},
	}
}

func writeConfigFile(t *testing.T, dir, body string) {
	t.Helper()

	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
