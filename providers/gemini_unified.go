package providers

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/genai"
)

// GeminiUnifiedProvider implements the Provider interface using the new unified Google GenAI SDK
type GeminiUnifiedProvider struct {
	config ProviderConfig
	client *genai.Client
}

// NewGeminiUnifiedProvider creates a new Gemini provider using the unified SDK
func NewGeminiUnifiedProvider(config ProviderConfig) (Provider, error) {
	if config.GeminiAPIKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY is required for gemini provider")
	}

	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  config.GeminiAPIKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	return &GeminiUnifiedProvider{
		config: config,
		client: client,
	}, nil
}

// Name returns the provider name
func (g *GeminiUnifiedProvider) Name() string {
	return "gemini-unified"
}

// Models returns available Gemini models that support search grounding
func (g *GeminiUnifiedProvider) Models() []string {
	return []string{
		"gemini-3-flash-preview",
		"gemini-3-pro-preview", 
		"gemini-2.5-pro",
		"gemini-2.5-flash",
		"gemini-2.0-flash",
		"gemini-pro-latest",
		"gemini-flash-latest",
	}
}

// ValidateModel checks if a model is supported by this provider
func (g *GeminiUnifiedProvider) ValidateModel(model string) bool {
	for _, m := range g.Models() {
		if m == model {
			return true
		}
	}
	return false
}

// CalculateCost calculates USD cost for given token usage (placeholder for Gemini pricing)
func (g *GeminiUnifiedProvider) CalculateCost(model string, inputTokens, outputTokens int64) float64 {
	// Gemini pricing (approximate as of current knowledge)
	var inputRate, outputRate float64
	
	switch {
	case strings.HasPrefix(model, "gemini-3"):
		// Estimated pricing for newer models
		inputRate = 0.00125 / 1000   // $1.25 per 1M input tokens
		outputRate = 0.005 / 1000    // $5 per 1M output tokens
	case strings.Contains(model, "2.5"):
		inputRate = 0.001 / 1000     // $1 per 1M input tokens  
		outputRate = 0.004 / 1000    // $4 per 1M output tokens
	default:
		inputRate = 0.0005 / 1000    // $0.5 per 1M input tokens
		outputRate = 0.002 / 1000    // $2 per 1M output tokens
	}
	
	return float64(inputTokens)*inputRate + float64(outputTokens)*outputRate
}

// supportsSearchGrounding returns true for models that support Google Search grounding
func (g *GeminiUnifiedProvider) supportsSearchGrounding(model string) bool {
	supportedModels := map[string]bool{
		"gemini-3-flash-preview": true,
		"gemini-3-pro-preview":   true,
		"gemini-2.5-pro":         true,
		"gemini-2.5-flash":       true,
		"gemini-2.0-flash":       true,
		"gemini-pro-latest":      true,
		"gemini-flash-latest":    true,
	}
	return supportedModels[model]
}

// CreateChatCompletion implements the Provider interface with Google Search grounding support
func (g *GeminiUnifiedProvider) CreateChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	// Build the content array
	var contents []*genai.Content
	
	for _, msg := range req.Messages {
		var role genai.Role
		switch msg.Role {
		case RoleUser, RoleSystem:
			role = genai.RoleUser
		case RoleAssistant:
			role = genai.RoleModel
		default:
			role = genai.RoleUser
		}
		
		content := genai.NewContentFromText(msg.Content, role)
		contents = append(contents, content)
	}

	// Create generation config
	temp := float32(0.2)  // Good for factual queries
	genConfig := &genai.GenerateContentConfig{
		Temperature:     &temp,
		MaxOutputTokens: int32(req.MaxTokens),
	}

	// Add Google Search grounding if enabled and model supports it
	if g.config.GeminiSettings.SearchGrounding && g.supportsSearchGrounding(req.Model) {
		genConfig.Tools = []*genai.Tool{
			{
				GoogleSearch: &genai.GoogleSearch{},
			},
		}
	}

	// Generate content
	resp, err := g.client.Models.GenerateContent(ctx, req.Model, contents, genConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to generate content: %w", err)
	}

	if len(resp.Candidates) == 0 {
		return nil, fmt.Errorf("no candidates in response")
	}

	candidate := resp.Candidates[0]
	
	// Extract text from parts
	var responseText strings.Builder
	for _, part := range candidate.Content.Parts {
		if part.Text != "" {
			responseText.WriteString(part.Text)
		}
	}

	// Append grounding information if available
	if candidate.GroundingMetadata != nil {
		g.appendGroundingInfo(&responseText, candidate.GroundingMetadata)
	}

	// Calculate token usage
	var inputTokens, outputTokens int64
	if resp.UsageMetadata != nil {
		inputTokens = int64(resp.UsageMetadata.PromptTokenCount)
		outputTokens = int64(resp.UsageMetadata.CandidatesTokenCount)
	}
	
	cost := g.CalculateCost(req.Model, inputTokens, outputTokens)

	return &ChatCompletionResponse{
		Content: []ContentBlock{{
			Type: ContentTypeText,
			Text: responseText.String(),
		}},
		StopReason: StopReason(candidate.FinishReason),
		Usage: Usage{
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			Cost:         cost,
		},
	}, nil
}

// appendGroundingInfo adds grounding metadata to the response
func (g *GeminiUnifiedProvider) appendGroundingInfo(responseText *strings.Builder, metadata *genai.GroundingMetadata) {
	responseText.WriteString("\n\n---\n")
	responseText.WriteString("**Sources and Search Information:**\n\n")

	// Add search queries used
	if len(metadata.WebSearchQueries) > 0 {
		responseText.WriteString("**Search queries performed:**\n")
		for _, query := range metadata.WebSearchQueries {
			responseText.WriteString(fmt.Sprintf("- %s\n", query))
		}
		responseText.WriteString("\n")
	}

	// Add grounding chunks (sources)
	if len(metadata.GroundingChunks) > 0 {
		responseText.WriteString("**Sources:**\n")
		for i, chunk := range metadata.GroundingChunks {
			if chunk.Web != nil {
				responseText.WriteString(fmt.Sprintf("%d. [%s](%s)\n", 
					i+1, chunk.Web.Title, chunk.Web.URI))
			} else if chunk.RetrievedContext != nil {
				responseText.WriteString(fmt.Sprintf("%d. %s\n", 
					i+1, chunk.RetrievedContext.Title))
			}
		}
		responseText.WriteString("\n")
	}

	// Add support information if available
	if len(metadata.GroundingSupports) > 0 {
		responseText.WriteString("**Grounding support:**\n")
		for i, support := range metadata.GroundingSupports {
			if support.GroundingChunkIndices != nil && len(support.GroundingChunkIndices) > 0 {
				responseText.WriteString(fmt.Sprintf("*Support %d: sources %v*\n",
					i+1, support.GroundingChunkIndices))
			}
		}
		responseText.WriteString("\n")
	}

	responseText.WriteString("\n*Information is current as of the search date. Please verify with original sources for the most recent updates.*")
}