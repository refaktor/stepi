package providers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// AnthropicProvider implements the Provider interface for Anthropic's Claude API
type AnthropicProvider struct {
	client anthropic.Client
	config ProviderConfig
}

// NewAnthropicProvider creates a new Anthropic provider
func NewAnthropicProvider(config ProviderConfig) (*AnthropicProvider, error) {
	opts := []option.RequestOption{}
	if config.AnthropicAPIKey != "" {
		opts = append(opts, option.WithAPIKey(config.AnthropicAPIKey))
	}
	
	client := anthropic.NewClient(opts...)
	
	return &AnthropicProvider{
		client: client,
		config: config,
	}, nil
}

// Name returns the provider name
func (p *AnthropicProvider) Name() string {
	return "anthropic"
}

// Models returns available Anthropic models
func (p *AnthropicProvider) Models() []string {
	return []string{
		"claude-sonnet-4-20250514",
		"claude-sonnet-4-5-20250929", 
		"claude-3-5-haiku-20241022",
		"claude-3-5-sonnet-20241022",
		"claude-opus-4-20250514",
	}
}

// ValidateModel checks if a model is supported by Anthropic
func (p *AnthropicProvider) ValidateModel(model string) bool {
	for _, m := range p.Models() {
		if m == model {
			return true
		}
	}
	return false
}

// CalculateCost calculates USD cost for given token usage
func (p *AnthropicProvider) CalculateCost(model string, inputTokens, outputTokens int64) float64 {
	// Model pricing (USD per million tokens)
	modelPricing := map[string]struct{ Input, Output float64 }{
		"claude-sonnet-4-20250514":     {3.0, 15.0},
		"claude-sonnet-4-5-20250929":   {3.0, 15.0},
		"claude-3-5-haiku-20241022":    {0.8, 4.0},
		"claude-3-5-sonnet-20241022":   {3.0, 15.0},
		"claude-opus-4-20250514":       {15.0, 75.0},
	}
	
	pricing, ok := modelPricing[model]
	if !ok {
		// Default to sonnet pricing
		pricing = struct{ Input, Output float64 }{3.0, 15.0}
	}
	
	return (float64(inputTokens) * pricing.Input / 1_000_000) +
		(float64(outputTokens) * pricing.Output / 1_000_000)
}

// CreateChatCompletion executes a chat completion request
func (p *AnthropicProvider) CreateChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	// Convert tools to Anthropic format
	anthropicTools := make([]anthropic.ToolUnionParam, len(req.Tools))
	for i, tool := range req.Tools {
		// Convert parameters map to proper schema format
		schemaParam := convertMapToSchema(tool.Parameters)
		anthropicTools[i] = anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        tool.Name,
				Description: anthropic.String(tool.Description),
				InputSchema: schemaParam,
			},
		}
	}

	// Convert messages to Anthropic format
	messages := make([]anthropic.MessageParam, 0)
	for _, msg := range req.Messages {
		switch msg.Role {
		case RoleUser:
			if len(msg.ToolResults) > 0 {
				// Tool results message
				blocks := make([]anthropic.ContentBlockParamUnion, len(msg.ToolResults))
				for i, result := range msg.ToolResults {
					blocks[i] = anthropic.NewToolResultBlock(result.ToolCallID, result.Content, result.IsError)
				}
				messages = append(messages, anthropic.NewUserMessage(blocks...))
			} else {
				// Regular user message
				messages = append(messages, anthropic.NewUserMessage(anthropic.NewTextBlock(msg.Content)))
			}
		case RoleAssistant:
			blocks := []anthropic.ContentBlockParamUnion{}
			if msg.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
			}
			// Add tool calls
			for _, tc := range msg.ToolCalls {
				inputJSON, _ := json.Marshal(tc.Arguments)
				blocks = append(blocks, anthropic.ContentBlockParamUnion{
					OfToolUse: &anthropic.ToolUseBlockParam{
						ID:    tc.ID,
						Name:  tc.Name,
						Input: json.RawMessage(inputJSON),
					},
				})
			}
			messages = append(messages, anthropic.NewAssistantMessage(blocks...))
		}
	}

	// Build system prompt blocks
	var systemBlocks []anthropic.TextBlockParam
	if req.SystemPrompt != "" {
		cacheControl := anthropic.NewCacheControlEphemeralParam()
		systemBlocks = []anthropic.TextBlockParam{
			{Text: req.SystemPrompt, CacheControl: cacheControl},
		}
	}

	// Build request params
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: int64(req.MaxTokens),
		Messages:  messages,
		Tools:     anthropicTools,
	}
	if len(systemBlocks) > 0 {
		params.System = systemBlocks
	}

	// Add thinking if requested
	if req.Thinking != "off" && req.Thinking != "" {
		budget := p.getThinkingBudget(req.Thinking)
		if budget > 0 {
			params.Thinking = anthropic.ThinkingConfigParamOfEnabled(int64(budget))
		}
	}

	if req.Stream {
		return p.createStreamingCompletion(ctx, params, req.StreamHandler)
	} else {
		return p.createRegularCompletion(ctx, params)
	}
}

// createRegularCompletion handles non-streaming completion
func (p *AnthropicProvider) createRegularCompletion(ctx context.Context, params anthropic.MessageNewParams) (*ChatCompletionResponse, error) {
	message, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("anthropic API error: %w", err)
	}

	return p.convertAnthropicResponse(message), nil
}

// createStreamingCompletion handles streaming completion
func (p *AnthropicProvider) createStreamingCompletion(ctx context.Context, params anthropic.MessageNewParams, streamHandler func(StreamEvent)) (*ChatCompletionResponse, error) {
	stream := p.client.Messages.NewStreaming(ctx, params)

	message := anthropic.Message{}
	for stream.Next() {
		event := stream.Current()
		if err := message.Accumulate(event); err != nil {
			return nil, fmt.Errorf("accumulate error: %w", err)
		}

		// Handle streaming events
		switch ev := event.AsAny().(type) {
		case anthropic.ContentBlockDeltaEvent:
			if ev.Delta.Text != "" && streamHandler != nil {
				streamHandler(StreamEvent{
					Type: StreamEventTypeText,
					Text: ev.Delta.Text,
				})
			}
		}
	}

	if stream.Err() != nil {
		return nil, fmt.Errorf("anthropic stream error: %w", stream.Err())
	}

	if streamHandler != nil {
		streamHandler(StreamEvent{
			Type: StreamEventTypeDone,
			Done: true,
		})
	}

	return p.convertAnthropicResponse(&message), nil
}

// convertAnthropicResponse converts Anthropic response to unified format
func (p *AnthropicProvider) convertAnthropicResponse(message *anthropic.Message) *ChatCompletionResponse {
	// Convert content blocks
	var contentBlocks []ContentBlock
	for _, block := range message.Content {
		switch variant := block.AsAny().(type) {
		case anthropic.TextBlock:
			contentBlocks = append(contentBlocks, ContentBlock{
				Type: ContentTypeText,
				Text: variant.Text,
			})
		case anthropic.ThinkingBlock:
			contentBlocks = append(contentBlocks, ContentBlock{
				Type:     ContentTypeThinking,
				Thinking: variant.Thinking,
			})
		case anthropic.ToolUseBlock:
			args := make(map[string]any)
			json.Unmarshal([]byte(variant.JSON.Input.Raw()), &args)
			
			contentBlocks = append(contentBlocks, ContentBlock{
				Type: ContentTypeToolCall,
				ToolCall: &ToolCall{
					ID:        variant.ID,
					Name:      variant.Name,
					Arguments: args,
				},
			})
		}
	}

	// Convert stop reason
	var stopReason StopReason
	switch message.StopReason {
	case "tool_use":
		stopReason = StopReasonToolCall
	case "end_turn":
		stopReason = StopReasonComplete
	case "max_tokens":
		stopReason = StopReasonLength
	default:
		stopReason = StopReasonComplete
	}

	// Convert usage
	usage := Usage{
		InputTokens:  message.Usage.InputTokens,
		OutputTokens: message.Usage.OutputTokens,
		CacheRead:    message.Usage.CacheReadInputTokens,
		CacheWrite:   message.Usage.CacheCreationInputTokens,
	}
	usage.Cost = p.CalculateCost(string(message.Model), usage.InputTokens, usage.OutputTokens)

	return &ChatCompletionResponse{
		Content:    contentBlocks,
		StopReason: stopReason,
		Usage:      usage,
		MessageID:  message.ID,
	}
}

// getThinkingBudget returns thinking token budget for given level
func (p *AnthropicProvider) getThinkingBudget(level string) int {
	switch level {
	case "low":
		return 1024
	case "medium":
		return 4096
	case "high":
		return 16384
	default:
		return 0
	}
}

// convertMapToSchema converts a map to Anthropic schema format
func convertMapToSchema(params map[string]any) anthropic.ToolInputSchemaParam {
	// Convert map to JSON bytes then back to the required schema type
	jsonBytes, _ := json.Marshal(params)
	var result anthropic.ToolInputSchemaParam
	json.Unmarshal(jsonBytes, &result)
	return result
}