package config

type ServerConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

type StoreConfig struct {
	SQLitePath   string `yaml:"sqlite_path"`
	LogRoot      string `yaml:"log_root"`
	ArtifactRoot string `yaml:"artifact_root"`
}

type SchedulerConfig struct {
	DueJobScanIntervalSec   int `yaml:"due_job_scan_interval_sec"`
	DispatchScanIntervalSec int `yaml:"dispatch_scan_interval_sec"`
	MaxConcurrentRuns       int `yaml:"max_concurrent_runs"`
}

type ImageRemoteConfig struct {
	Endpoint string `yaml:"endpoint"`
	Insecure bool   `yaml:"insecure"`
}

type ImageConfig struct {
	AllowedSources  []string          `yaml:"allowed_sources"`
	DefaultSource   string            `yaml:"default_source"`
	PullPolicy      string            `yaml:"pull_policy"`
	AllowedPrefixes []string          `yaml:"allowed_prefixes"`
	Remote          ImageRemoteConfig `yaml:"remote"`
}

type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Store     StoreConfig     `yaml:"store"`
	Scheduler SchedulerConfig `yaml:"scheduler"`
	Image     ImageConfig     `yaml:"image"`
}
