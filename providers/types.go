// Package providers defines the LLM provider abstraction layer
package providers

import (
	"context"
)

// Provider defines the interface for LLM providers
type Provider interface {
	// Name returns the provider name (e.g., "anthropic", "openai")
	Name() string
	
	// Models returns available model IDs for this provider
	Models() []string
	
	// ValidateModel checks if a model is supported by this provider
	ValidateModel(model string) bool
	
	// CalculateCost calculates USD cost for given token usage
	CalculateCost(model string, inputTokens, outputTokens int64) float64
	
	// CreateChatCompletion executes a chat completion request
	CreateChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error)
}

// ChatCompletionRequest represents a unified chat completion request
type ChatCompletionRequest struct {
	Model        string
	Messages     []ChatMessage
	Tools        []Tool
	MaxTokens    int
	SystemPrompt string
	Thinking     string // Anthropic-specific: "off", "low", "medium", "high"
	Stream       bool
	StreamHandler func(StreamEvent)
}

// ChatCompletionResponse represents a unified chat completion response
type ChatCompletionResponse struct {
	Content     []ContentBlock
	StopReason  StopReason
	Usage       Usage
	MessageID   string // Provider-specific message ID
}

// ChatMessage represents a message in the conversation
type ChatMessage struct {
	Role         MessageRole
	Content      string
	ToolCalls    []ToolCall
	ToolResults  []ToolResult
}

// MessageRole represents the role of a message
type MessageRole string

const (
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant" 
	RoleSystem    MessageRole = "system"
	RoleTool      MessageRole = "tool"
)

// ContentBlock represents different types of content
type ContentBlock struct {
	Type      ContentType
	Text      string
	Thinking  string    // Anthropic thinking content
	ToolCall  *ToolCall // Tool call content
}

// ContentType represents the type of content
type ContentType string

const (
	ContentTypeText     ContentType = "text"
	ContentTypeThinking ContentType = "thinking"
	ContentTypeToolCall ContentType = "tool_call"
)

// ToolCall represents a tool call
type ToolCall struct {
	ID       string
	Name     string
	Arguments map[string]any
}

// ToolResult represents the result of a tool call
type ToolResult struct {
	ToolCallID string
	Content    string
	IsError    bool
}

// Tool represents a tool that can be called
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON schema
}

// StopReason represents why the completion stopped
type StopReason string

const (
	StopReasonComplete StopReason = "complete"
	StopReasonToolCall StopReason = "tool_call"
	StopReasonLength   StopReason = "length"
	StopReasonStop     StopReason = "stop"
)

// Usage represents token usage and cost information
type Usage struct {
	InputTokens  int64
	OutputTokens int64
	CacheRead    int64  // Anthropic-specific
	CacheWrite   int64  // Anthropic-specific
	Cost         float64
}

// StreamEvent represents a streaming event
type StreamEvent struct {
	Type StreamEventType
	Text string
	Done bool
}

// StreamEventType represents the type of streaming event
type StreamEventType string

const (
	StreamEventTypeText StreamEventType = "text"
	StreamEventTypeDone StreamEventType = "done"
)

// ProviderConfig holds provider-specific configuration
type ProviderConfig struct {
	// Common config
	Provider    string
	Model       string
	MaxTokens   int
	Thinking    string
	MaxTurns    int
	
	// API Keys
	AnthropicAPIKey string
	OpenAIAPIKey    string
	GeminiAPIKey    string
	
	// Provider-specific settings
	AnthropicSettings AnthropicSettings
	OpenAISettings    OpenAISettings
	GeminiSettings    GeminiSettings
}

// AnthropicSettings holds Anthropic-specific settings
type AnthropicSettings struct {
	// Additional Anthropic-specific configuration can go here
}

// OpenAISettings holds OpenAI-specific settings  
type OpenAISettings struct {
	// OpenAI-specific configuration
	Temperature      *float32
	TopP            *float32
	FrequencyPenalty *float32
	PresencePenalty  *float32
}

// GeminiSettings holds Gemini-specific settings
type GeminiSettings struct {
	// Gemini-specific configuration
	SearchGrounding bool // Enable Google Search integration
}