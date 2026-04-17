package providers

import (
	"fmt"
	"strings"
)

// NewProvider creates a new provider based on the provider name
func NewProvider(config ProviderConfig) (Provider, error) {
	switch strings.ToLower(config.Provider) {
	case "anthropic":
		if config.AnthropicAPIKey == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY is required for anthropic provider")
		}
		return NewAnthropicProvider(config)
	case "openai":
		if config.OpenAIAPIKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY is required for openai provider")
		}
		return NewOpenAIProvider(config)
	default:
		return nil, fmt.Errorf("unsupported provider: %s (supported: anthropic, openai)", config.Provider)
	}
}

// GetProviderForModel determines the appropriate provider for a given model
func GetProviderForModel(model string) string {
	// Anthropic models
	if strings.HasPrefix(model, "claude-") {
		return "anthropic"
	}
	
	// OpenAI models
	if strings.HasPrefix(model, "gpt-") || 
	   strings.HasPrefix(model, "code-") || 
	   strings.Contains(model, "davinci") ||
	   strings.Contains(model, "codex") {
		return "openai"
	}
	
	// Default to anthropic for backward compatibility
	return "anthropic"
}

// ListAvailableModels returns all available models across providers
func ListAvailableModels() map[string][]string {
	return map[string][]string{
		"anthropic": {
			"claude-sonnet-4-20250514",
			"claude-sonnet-4-5-20250929", 
			"claude-3-5-haiku-20241022",
			"claude-3-5-sonnet-20241022",
			"claude-opus-4-20250514",
		},
		"openai": {
			"gpt-4",
			"gpt-4-turbo",
			"gpt-4-turbo-preview",
			"gpt-3.5-turbo",
			"code-davinci-002", // Codex
			"text-davinci-003",
			"gpt-4-code",       // Hypothetical future model
		},
	}
}