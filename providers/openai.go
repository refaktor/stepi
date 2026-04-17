package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"
)

// OpenAIProvider implements the Provider interface for OpenAI's API (including Codex)
type OpenAIProvider struct {
	client *openai.Client
	config ProviderConfig
}

// NewOpenAIProvider creates a new OpenAI provider
func NewOpenAIProvider(config ProviderConfig) (*OpenAIProvider, error) {
	client := openai.NewClient(config.OpenAIAPIKey)
	
	return &OpenAIProvider{
		client: client,
		config: config,
	}, nil
}

// Name returns the provider name
func (p *OpenAIProvider) Name() string {
	return "openai"
}

// Models returns available OpenAI models
func (p *OpenAIProvider) Models() []string {
	return []string{
		"gpt-4",
		"gpt-4-turbo",
		"gpt-4-turbo-preview",
		"gpt-4-0125-preview",
		"gpt-4-1106-preview", 
		"gpt-3.5-turbo",
		"gpt-3.5-turbo-1106",
		"code-davinci-002", // Codex model
		"text-davinci-003",
		// Note: Some Codex models may be deprecated, but keeping for compatibility
	}
}

// ValidateModel checks if a model is supported by OpenAI
func (p *OpenAIProvider) ValidateModel(model string) bool {
	for _, m := range p.Models() {
		if m == model {
			return true
		}
	}
	return false
}

// CalculateCost calculates USD cost for given token usage
func (p *OpenAIProvider) CalculateCost(model string, inputTokens, outputTokens int64) float64 {
	// Model pricing (USD per million tokens) - as of 2024
	modelPricing := map[string]struct{ Input, Output float64 }{
		"gpt-4":                    {30.0, 60.0},
		"gpt-4-turbo":              {10.0, 30.0},
		"gpt-4-turbo-preview":      {10.0, 30.0},
		"gpt-4-0125-preview":       {10.0, 30.0},
		"gpt-4-1106-preview":       {10.0, 30.0},
		"gpt-3.5-turbo":            {0.5, 1.5},
		"gpt-3.5-turbo-1106":       {1.0, 2.0},
		"code-davinci-002":         {2.0, 2.0}, // Codex pricing
		"text-davinci-003":         {20.0, 20.0},
	}
	
	pricing, ok := modelPricing[model]
	if !ok {
		// Default to GPT-4 pricing for unknown models
		pricing = struct{ Input, Output float64 }{10.0, 30.0}
	}
	
	return (float64(inputTokens) * pricing.Input / 1_000_000) +
		(float64(outputTokens) * pricing.Output / 1_000_000)
}

// CreateChatCompletion executes a chat completion request
func (p *OpenAIProvider) CreateChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	// Convert tools to OpenAI function format
	var functions []openai.FunctionDefinition
	for _, tool := range req.Tools {
		functions = append(functions, openai.FunctionDefinition{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.Parameters,
		})
	}

	// Convert messages to OpenAI format
	var messages []openai.ChatCompletionMessage
	
	// Add system message if present
	if req.SystemPrompt != "" {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: req.SystemPrompt,
		})
	}
	
	for _, msg := range req.Messages {
		switch msg.Role {
		case RoleUser:
			if len(msg.ToolResults) > 0 {
				// Tool results - convert to function call results
				for _, result := range msg.ToolResults {
					messages = append(messages, openai.ChatCompletionMessage{
						Role:         openai.ChatMessageRoleFunction,
						Name:         result.ToolCallID, // Use tool call ID as name
						Content:      result.Content,
					})
				}
			} else {
				// Regular user message
				messages = append(messages, openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleUser,
					Content: msg.Content,
				})
			}
		case RoleAssistant:
			openaiMsg := openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleAssistant,
				Content: msg.Content,
			}
			
			// Add function calls if present
			if len(msg.ToolCalls) > 0 {
				var functionCalls []openai.FunctionCall
				for _, tc := range msg.ToolCalls {
					argsJSON, _ := json.Marshal(tc.Arguments)
					functionCalls = append(functionCalls, openai.FunctionCall{
						Name:      tc.Name,
						Arguments: string(argsJSON),
					})
				}
				// Note: OpenAI API has evolved - may need to adapt based on version
				// This is for older function calling format
				if len(functionCalls) == 1 {
					openaiMsg.FunctionCall = &functionCalls[0]
				}
			}
			
			messages = append(messages, openaiMsg)
		}
	}

	// Build request
	chatReq := openai.ChatCompletionRequest{
		Model:     req.Model,
		Messages:  messages,
		MaxTokens: req.MaxTokens,
	}
	
	// Add functions if available
	if len(functions) > 0 {
		chatReq.Functions = functions
		chatReq.FunctionCall = "auto"
	}
	
	// Add OpenAI-specific settings
	if p.config.OpenAISettings.Temperature != nil {
		chatReq.Temperature = *p.config.OpenAISettings.Temperature
	}
	if p.config.OpenAISettings.TopP != nil {
		chatReq.TopP = *p.config.OpenAISettings.TopP
	}

	if req.Stream {
		return p.createStreamingCompletion(ctx, chatReq, req.StreamHandler)
	} else {
		return p.createRegularCompletion(ctx, chatReq)
	}
}

// createRegularCompletion handles non-streaming completion
func (p *OpenAIProvider) createRegularCompletion(ctx context.Context, req openai.ChatCompletionRequest) (*ChatCompletionResponse, error) {
	resp, err := p.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("openai API error: %w", err)
	}

	return p.convertOpenAIResponse(&resp), nil
}

// createStreamingCompletion handles streaming completion
func (p *OpenAIProvider) createStreamingCompletion(ctx context.Context, req openai.ChatCompletionRequest, streamHandler func(StreamEvent)) (*ChatCompletionResponse, error) {
	req.Stream = true
	stream, err := p.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("openai stream error: %w", err)
	}
	defer stream.Close()

	var fullResponse strings.Builder
	var usage openai.Usage
	var functionCall *openai.FunctionCall
	var finishReason string

	for {
		response, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("stream receive error: %w", err)
		}

		if len(response.Choices) > 0 {
			choice := response.Choices[0]
			
			// Handle content delta
			if choice.Delta.Content != "" {
				fullResponse.WriteString(choice.Delta.Content)
				if streamHandler != nil {
					streamHandler(StreamEvent{
						Type: StreamEventTypeText,
						Text: choice.Delta.Content,
					})
				}
			}
			
			// Handle function calls
			if choice.Delta.FunctionCall != nil {
				if functionCall == nil {
					functionCall = &openai.FunctionCall{}
				}
				if choice.Delta.FunctionCall.Name != "" {
					functionCall.Name = choice.Delta.FunctionCall.Name
				}
				if choice.Delta.FunctionCall.Arguments != "" {
					functionCall.Arguments += choice.Delta.FunctionCall.Arguments
				}
			}
			
			// Capture finish reason
			if choice.FinishReason != "" {
				finishReason = string(choice.FinishReason)
			}
		}

		// Capture usage if available
		if response.Usage != nil && response.Usage.TotalTokens > 0 {
			usage = *response.Usage
		}
	}

	if streamHandler != nil {
		streamHandler(StreamEvent{
			Type: StreamEventTypeDone,
			Done: true,
		})
	}

	// Build response object
	mockResp := openai.ChatCompletionResponse{
		ID:      "stream-response",
		Object:  "chat.completion",
		Model:   req.Model,
		Usage:   usage,
		Choices: []openai.ChatCompletionChoice{
			{
				Index: 0,
				Message: openai.ChatCompletionMessage{
					Role:         openai.ChatMessageRoleAssistant,
					Content:      fullResponse.String(),
					FunctionCall: functionCall,
				},
				FinishReason: openai.FinishReason(finishReason),
			},
		},
	}

	return p.convertOpenAIResponse(&mockResp), nil
}

// convertOpenAIResponse converts OpenAI response to unified format
func (p *OpenAIProvider) convertOpenAIResponse(resp *openai.ChatCompletionResponse) *ChatCompletionResponse {
	if len(resp.Choices) == 0 {
		return &ChatCompletionResponse{
			Content:    []ContentBlock{},
			StopReason: StopReasonComplete,
			Usage:      Usage{},
		}
	}

	choice := resp.Choices[0]
	var contentBlocks []ContentBlock

	// Add text content
	if choice.Message.Content != "" {
		contentBlocks = append(contentBlocks, ContentBlock{
			Type: ContentTypeText,
			Text: choice.Message.Content,
		})
	}

	// Add function call if present
	if choice.Message.FunctionCall != nil {
		args := make(map[string]any)
		json.Unmarshal([]byte(choice.Message.FunctionCall.Arguments), &args)
		
		contentBlocks = append(contentBlocks, ContentBlock{
			Type: ContentTypeToolCall,
			ToolCall: &ToolCall{
				ID:        generateToolCallID(), // OpenAI doesn't provide ID in older API
				Name:      choice.Message.FunctionCall.Name,
				Arguments: args,
			},
		})
	}

	// Convert stop reason
	var stopReason StopReason
	switch choice.FinishReason {
	case "function_call":
		stopReason = StopReasonToolCall
	case "stop":
		stopReason = StopReasonComplete
	case "length":
		stopReason = StopReasonLength
	default:
		stopReason = StopReasonComplete
	}

	// Convert usage
	usage := Usage{
		InputTokens:  int64(resp.Usage.PromptTokens),
		OutputTokens: int64(resp.Usage.CompletionTokens),
		CacheRead:    0, // OpenAI doesn't have cache tokens
		CacheWrite:   0,
	}
	usage.Cost = p.CalculateCost(resp.Model, usage.InputTokens, usage.OutputTokens)

	return &ChatCompletionResponse{
		Content:    contentBlocks,
		StopReason: stopReason,
		Usage:      usage,
		MessageID:  resp.ID,
	}
}

// generateToolCallID generates a unique tool call ID for OpenAI compatibility
func generateToolCallID() string {
	// Simple implementation - in production might want to use proper UUID
	return fmt.Sprintf("call_%d", time.Now().UnixNano())
}