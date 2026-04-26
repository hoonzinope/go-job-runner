package config

type ServerConfig struct {
	Port int    `yaml:"port" mapstructure:"port"`
	Host string `yaml:"host" mapstructure:"host"`
}

type StoreConfig struct {
	SQLitePath        string `yaml:"sqlite_path" mapstructure:"sqlite_path"`
	LogRoot           string `yaml:"log_root" mapstructure:"log_root"`
	ArtifactRoot      string `yaml:"artifact_root" mapstructure:"artifact_root"`
	LogPathPattern    string `yaml:"log_path_pattern" mapstructure:"log_path_pattern"`
	ResultPathPattern string `yaml:"result_path_pattern" mapstructure:"result_path_pattern"`
}

type SchedulerConfig struct {
	DueJobScanIntervalSec   int  `yaml:"due_job_scan_interval_sec" mapstructure:"due_job_scan_interval_sec"`
	DispatchScanIntervalSec int  `yaml:"dispatch_scan_interval_sec" mapstructure:"dispatch_scan_interval_sec"`
	MaxConcurrentRuns       int  `yaml:"max_concurrent_runs" mapstructure:"max_concurrent_runs"`
	DefaultTimeoutSec       int  `yaml:"default_timeout_sec" mapstructure:"default_timeout_sec"`
	MaxTimeoutSec           int  `yaml:"max_timeout_sec" mapstructure:"max_timeout_sec"`
	AllowUnlimitedTimeout   bool `yaml:"allow_unlimited_timeout" mapstructure:"allow_unlimited_timeout"`
}

type ImageRemoteConfig struct {
	Endpoint string `yaml:"endpoint" mapstructure:"endpoint"`
	Insecure bool   `yaml:"insecure" mapstructure:"insecure"`
}

type ImageConfig struct {
	AllowedSources  []string          `yaml:"allowed_sources" mapstructure:"allowed_sources"`
	DefaultSource   string            `yaml:"default_source" mapstructure:"default_source"`
	PullPolicy      string            `yaml:"pull_policy" mapstructure:"pull_policy"`
	AllowedPrefixes []string          `yaml:"allowed_prefixes" mapstructure:"allowed_prefixes"`
	Remote          ImageRemoteConfig `yaml:"remote" mapstructure:"remote"`
}

type ExecutorConfig struct {
	NetworkMode             string  `yaml:"network_mode" mapstructure:"network_mode"`
	ReadOnlyRootFS          bool    `yaml:"read_only_rootfs" mapstructure:"read_only_rootfs"`
	MemoryLimitMB           int     `yaml:"memory_limit_mb" mapstructure:"memory_limit_mb"`
	CPULimit                float64 `yaml:"cpu_limit" mapstructure:"cpu_limit"`
	CleanupContainers       bool    `yaml:"cleanup_containers" mapstructure:"cleanup_containers"`
	StopGracePeriodSec      int     `yaml:"stop_grace_period_sec" mapstructure:"stop_grace_period_sec"`
	OrphanRecoveryOnStartup bool    `yaml:"orphan_recovery_on_startup" mapstructure:"orphan_recovery_on_startup"`
}

type Config struct {
	Server    ServerConfig    `yaml:"server" mapstructure:"server"`
	Store     StoreConfig     `yaml:"store" mapstructure:"store"`
	Scheduler SchedulerConfig `yaml:"scheduler" mapstructure:"scheduler"`
	Image     ImageConfig     `yaml:"image" mapstructure:"image"`
	Executor  ExecutorConfig  `yaml:"executor" mapstructure:"executor"`
}
