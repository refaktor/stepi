// Package agent provides the core agent loop and types
package agent

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go"
)

// Tool represents a tool that can be called by the LLM
type Tool interface {
	// Name returns the tool name (must match what LLM calls)
	Name() string
	// Description returns human-readable description
	Description() string
	// Schema returns JSON schema for parameters
	Schema() anthropic.ToolInputSchemaParam
	// Execute runs the tool with given arguments
	Execute(ctx context.Context, args map[string]any) (string, error)
}

// Config holds agent configuration
type Config struct {
	Model      string
	MaxTokens  int
	ApiKey     string
	Thinking   string // "off", "low", "medium", "high"
	MaxTurns   int    // Maximum LLM turns (0 = unlimited)
	FullComs   bool   // Save full communication log
	// New logging options
	LogFile     string // Base name for log files (if set, enables .log, .cmds, .chatter)
	CostTracking bool  // Enable cost tracking to CSV
}

// StoredMessage represents a message from session history
type StoredMessage struct {
	Role        string             // "user", "assistant", "tool_result"
	Content     string             // Text content
	ToolCalls   []StoredToolCall   // For assistant messages with tool calls
	ToolResults []StoredToolResult // For tool result messages
}

// StoredToolCall represents a stored tool call
type StoredToolCall struct {
	ID    string
	Name  string
	Input string // JSON string
}

// StoredToolResult represents a stored tool result
type StoredToolResult struct {
	ToolCallID string
	Content    string
	IsError    bool
}

// DefaultConfig returns default configuration
func DefaultConfig() Config {
	return Config{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 16384,
		Thinking:  "off",
		MaxTurns:  50,
	}
}

// Usage tracks token usage and cost
type Usage struct {
	InputTokens  int64
	OutputTokens int64
	CacheRead    int64
	CacheWrite   int64
	Cost         float64 // USD
}

// Result holds the final agent result
type Result struct {
	Response    string
	TotalTurns  int
	Error       error
	FullComs    string          // Full communication log (if enabled)
	NewMessages []StoredMessage // New messages from this run (for session persistence)
	Usage       Usage           // Token usage and cost
}

// Model pricing (USD per million tokens)
var modelPricing = map[string]struct{ Input, Output float64 }{
	"claude-sonnet-4-20250514":     {3.0, 15.0},
	"claude-sonnet-4-5-20250929":   {3.0, 15.0},
	"claude-3-5-haiku-20241022":    {0.8, 4.0},
	"claude-3-5-sonnet-20241022":   {3.0, 15.0},
	"claude-opus-4-20250514":       {15.0, 75.0},
}

// CalculateCost calculates USD cost for given token counts
func CalculateCost(model string, inputTokens, outputTokens int64) float64 {
	pricing, ok := modelPricing[model]
	if !ok {
		// Default to sonnet pricing
		pricing = struct{ Input, Output float64 }{3.0, 15.0}
	}
	return (float64(inputTokens) * pricing.Input / 1_000_000) +
		(float64(outputTokens) * pricing.Output / 1_000_000)
}
