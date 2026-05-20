package config

import (
	"fmt"
	"os"

	"github.com/user/stepi/agent"
	"github.com/user/stepi/providers"
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

// FromEnvProvider loads provider configuration from environment variables
func FromEnvProvider() providers.ProviderConfig {
	cfg := providers.ProviderConfig{
		MaxTokens: 16384,
		Thinking:  "off",
		MaxTurns:  50,
	}

	// API Keys
	cfg.AnthropicAPIKey = os.Getenv("ANTHROPIC_API_KEY")
	cfg.OpenAIAPIKey = os.Getenv("OPENAI_API_KEY")
	cfg.GeminiAPIKey = os.Getenv("GEMINI_API_KEY")

	// Model and provider
	if model := os.Getenv("STEPI_MODEL"); model != "" {
		cfg.Model = model
		cfg.Provider = providers.GetProviderForModel(model)
	} else {
		// Auto-select provider and default model based on available API keys
		cfg.Provider, cfg.Model = autoSelectProviderAndModel(cfg.AnthropicAPIKey, cfg.OpenAIAPIKey, cfg.GeminiAPIKey)
	}

	// Explicit provider override
	if provider := os.Getenv("STEPI_PROVIDER"); provider != "" {
		cfg.Provider = provider
		// Update model to default for the explicitly selected provider
		if cfg.Model == "" || providers.GetProviderForModel(cfg.Model) != cfg.Provider {
			cfg.Model = getDefaultModelForProvider(cfg.Provider)
		}
	}

	// Thinking level
	if thinking := os.Getenv("STEPI_THINKING"); thinking != "" {
		cfg.Thinking = thinking
	}

	// OpenAI-specific settings
	if temp := os.Getenv("OPENAI_TEMPERATURE"); temp != "" {
		if f := parseFloat32(temp); f != nil {
			cfg.OpenAISettings.Temperature = f
		}
	}

	if topP := os.Getenv("OPENAI_TOP_P"); topP != "" {
		if f := parseFloat32(topP); f != nil {
			cfg.OpenAISettings.TopP = f
		}
	}

	return cfg
}

// parseFloat32 safely parses a float32 string
func parseFloat32(s string) *float32 {
	var f float32
	if n, err := fmt.Sscanf(s, "%f", &f); err == nil && n == 1 {
		return &f
	}
	return nil
}

// autoSelectProviderAndModel automatically selects provider and model based on available API keys
func autoSelectProviderAndModel(anthropicKey, openaiKey, geminiKey string) (provider, model string) {
	// If both keys are available, prefer Anthropic (current default behavior)
	if anthropicKey != "" {
		return "anthropic", getDefaultModelForProvider("anthropic")
	}
	
	// If only OpenAI key is available, use OpenAI
	if openaiKey != "" {
		return "openai", getDefaultModelForProvider("openai")
	}
	
	// If only Gemini key is available, use Gemini
	if geminiKey != "" {
		return "gemini", getDefaultModelForProvider("gemini")
	}
	
	// If no keys are available, default to Anthropic (will error later if key is missing)
	return "anthropic", getDefaultModelForProvider("anthropic")
}

// getDefaultModelForProvider returns the default model for a given provider
func getDefaultModelForProvider(provider string) string {
	switch provider {
	case "anthropic":
		return "claude-sonnet-4-20250514"
	case "openai":
		return "gpt-4"
	case "gemini":
		return "gemini-1.5-pro"
	default:
		return "claude-sonnet-4-20250514" // fallback to Anthropic default
	}
}
