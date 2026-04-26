package config

import (
	"fmt"

	"github.com/spf13/viper"
)

func LoadConfig(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigType("yaml")
	v.SetConfigName("config")
	v.AddConfigPath(path)
	v.SetDefault("scheduler.default_timeout_sec", 3600)
	v.SetDefault("scheduler.max_timeout_sec", 86400)
	v.SetDefault("scheduler.allow_unlimited_timeout", false)
	v.SetDefault("executor.cleanup_containers", true)
	v.SetDefault("executor.stop_grace_period_sec", 10)
	v.SetDefault("executor.orphan_recovery_on_startup", true)
	v.SetDefault("retention.enabled", true)
	v.SetDefault("retention.prune_interval_sec", 3600)

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	return &cfg, nil
}
