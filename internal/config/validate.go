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
	return nil
}
