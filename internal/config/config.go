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

type Config struct {
	Server ServerConfig `yaml:"server"`
	Store  StoreConfig  `yaml:"store"`
}
