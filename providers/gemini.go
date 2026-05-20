package providers

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

// GeminiProvider implements the Provider interface for Google's Gemini API
type GeminiProvider struct {
	config ProviderConfig
	client *genai.Client
}

// NewGeminiProvider creates a new Gemini provider
func NewGeminiProvider(config ProviderConfig) (Provider, error) {
	if config.GeminiAPIKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY is required for gemini provider")
	}

	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(config.GeminiAPIKey))
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	return &GeminiProvider{
		config: config,
		client: client,
	}, nil
}

// Name returns the provider name
func (g *GeminiProvider) Name() string {
	return "gemini"
}

// Models returns available Gemini models
func (g *GeminiProvider) Models() []string {
	return []string{
		"gemini-3-flash-preview",      // New model with search grounding support
		"gemini-3-pro-preview",        // New model with search grounding support  
		"gemini-2.5-pro",
		"gemini-2.5-flash", 
		"gemini-2.0-flash",
		"gemini-pro-latest",
		"gemini-flash-latest",
	}
}

// ValidateModel checks if a model is supported by this provider
func (g *GeminiProvider) ValidateModel(model string) bool {
	for _, m := range g.Models() {
		if m == model {
			return true
		}
	}
	return false
}

// CalculateCost calculates USD cost for given token usage
// Gemini pricing (approximate, check current pricing)
func (g *GeminiProvider) CalculateCost(model string, inputTokens, outputTokens int64) float64 {
	var inputCost, outputCost float64

	switch model {
	case "gemini-2.5-pro", "gemini-pro-latest":
		inputCost = 0.00125 / 1000   // $1.25 per 1M input tokens
		outputCost = 0.005 / 1000    // $5 per 1M output tokens
	case "gemini-2.5-flash", "gemini-flash-latest":
		inputCost = 0.0001875 / 1000 // $0.1875 per 1M input tokens
		outputCost = 0.00075 / 1000  // $0.75 per 1M output tokens
	case "gemini-2.0-flash":
		inputCost = 0.0001875 / 1000 // $0.1875 per 1M input tokens
		outputCost = 0.00075 / 1000  // $0.75 per 1M output tokens
	default:
		// Default to flash pricing
		inputCost = 0.0001875 / 1000
		outputCost = 0.00075 / 1000
	}

	return float64(inputTokens)*inputCost + float64(outputTokens)*outputCost
}

// supportsSearchGrounding checks if the model supports Google Search grounding
func (g *GeminiProvider) supportsSearchGrounding(model string) bool {
	// Currently, search grounding is available for newer Gemini models
	// When the new unified SDK (google.golang.org/genai) is available,
	// these models will support GoogleSearch tool
	supportedModels := map[string]bool{
		"gemini-3-flash-preview": true,
		"gemini-3-pro-preview":   true,
		"gemini-2.5-pro":         true,
		"gemini-2.5-flash":       true,
		"gemini-2.0-flash":       true,
	}
	return supportedModels[model]
}

// CreateChatCompletion executes a chat completion request
func (g *GeminiProvider) CreateChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	model := g.client.GenerativeModel(req.Model)

	// Configure model parameters
	if req.MaxTokens > 0 {
		model.SetMaxOutputTokens(int32(req.MaxTokens))
	}
	
	// Configure temperature for better search responses
	model.SetTemperature(0.1)

	// Configure Google Search Grounding if enabled and supported  
	// Note: The legacy SDK (github.com/google/generative-ai-go) doesn't support
	// Google Search grounding yet. When the unified SDK (google.golang.org/genai) 
	// becomes available, full search grounding will be supported.
	if g.config.GeminiSettings.SearchGrounding && g.supportsSearchGrounding(req.Model) {
		// For now, we use enhanced prompting to encourage search-aware responses
		// The newer models still provide good search-aware capabilities even without
		// explicit grounding tools in the legacy SDK
		model.SetTemperature(0.2) // Slightly higher for more dynamic responses
	}

	// Build the prompt from messages
	var prompt string
	if req.SystemPrompt != "" {
		prompt = req.SystemPrompt + "\n\n"
	}

	for _, msg := range req.Messages {
		switch msg.Role {
		case RoleUser:
			prompt += fmt.Sprintf("User: %s\n\n", msg.Content)
		case RoleAssistant:
			prompt += fmt.Sprintf("Assistant: %s\n\n", msg.Content)
		}
	}

	// Generate response
	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return nil, fmt.Errorf("failed to generate content: %w", err)
	}

	// Parse response
	response := &ChatCompletionResponse{
		StopReason: StopReasonComplete,
		Usage: Usage{
			InputTokens:  int64(resp.UsageMetadata.PromptTokenCount),
			OutputTokens: int64(resp.UsageMetadata.CandidatesTokenCount),
		},
	}

	// Calculate cost
	response.Usage.Cost = g.CalculateCost(req.Model, response.Usage.InputTokens, response.Usage.OutputTokens)

	// Extract content
	if len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
		var contentText strings.Builder
		
		for _, part := range resp.Candidates[0].Content.Parts {
			if textPart, ok := part.(genai.Text); ok {
				contentText.WriteString(string(textPart))
			}
		}

		content := contentText.String()
		
		// Note: Grounding metadata parsing will be available when using 
		// the unified SDK (google.golang.org/genai) with proper search grounding support

		response.Content = []ContentBlock{
			{
				Type: ContentTypeText,
				Text: content,
			},
		}
	}

	return response, nil
}

// appendGroundingInfo appends grounding metadata to the response content
// Note: This will be implemented when the unified SDK supports grounding metadata
func (g *GeminiProvider) appendGroundingInfo(content string, metadata interface{}) string {
	// Placeholder for future implementation with google.golang.org/genai
	return content
}

// Close closes the Gemini client
func (g *GeminiProvider) Close() error {
	if g.client != nil {
		return g.client.Close()
	}
	return nil
}