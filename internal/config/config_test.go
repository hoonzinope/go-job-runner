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
	if cfg.Scheduler.DueJobScanIntervalSec != 2 || cfg.Scheduler.DispatchScanIntervalSec != 1 || cfg.Scheduler.MaxConcurrentRuns != 3 {
		t.Fatalf("unexpected scheduler config: %+v", cfg.Scheduler)
	}
	if len(cfg.Image.AllowedSources) != 2 || cfg.Image.DefaultSource != "local" || cfg.Image.PullPolicy != "if_not_present" {
		t.Fatalf("unexpected image config: %+v", cfg.Image)
	}
	if cfg.Image.Remote.Endpoint != "http://registry:5000" || !cfg.Image.Remote.Insecure {
		t.Fatalf("unexpected remote config: %+v", cfg.Image.Remote)
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
	}
}

func writeConfigFile(t *testing.T, dir, body string) {
	t.Helper()

	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
