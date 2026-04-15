package config

import (
	"os"

	"github.com/user/stepi/agent"
)

// FromEnv loads configuration from environment variables
func FromEnv() agent.Config {
	cfg := agent.DefaultConfig()

	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		cfg.ApiKey = key
	}

	if model := os.Getenv("STEPI_MODEL"); model != "" {
		cfg.Model = model
	}

	if thinking := os.Getenv("STEPI_THINKING"); thinking != "" {
		cfg.Thinking = thinking
	}

	return cfg
}
