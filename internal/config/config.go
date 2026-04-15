package config

type ServerConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

type Config struct {
	Server ServerConfig `yaml:"server"`
}
