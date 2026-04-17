// Package agent provides provider-agnostic agent functionality
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/user/stepi/colors"
	"github.com/user/stepi/logging"
	"github.com/user/stepi/providers"
)

// RunWithProvider executes the agent loop using the provider system
func RunWithProvider(ctx context.Context, systemPrompt, userPrompt string, tools []Tool, provider providers.Provider, cfg Config, logger *logging.Logger) Result {
	logger.Info("Starting agent execution with provider: %s", provider.Name())
	logger.Info("Model: %s", cfg.Model)
	
	return runAgentLoop(ctx, systemPrompt, userPrompt, nil, tools, provider, cfg, logger)
}

// RunWithProviderAndHistory executes the agent loop with conversation history
func RunWithProviderAndHistory(ctx context.Context, systemPrompt, userPrompt string, history []StoredMessage, tools []Tool, provider providers.Provider, cfg Config, logger *logging.Logger) Result {
	logger.Info("Starting agent execution with provider: %s and history", provider.Name())
	logger.Info("Model: %s, History messages: %d", cfg.Model, len(history))
	
	return runAgentLoop(ctx, systemPrompt, userPrompt, history, tools, provider, cfg, logger)
}

// runAgentLoop is the core agent loop that works with any provider
func runAgentLoop(ctx context.Context, systemPrompt, userPrompt string, history []StoredMessage, tools []Tool, provider providers.Provider, cfg Config, logger *logging.Logger) Result {
	// Convert tools to provider format
	providerTools := make([]providers.Tool, len(tools))
	toolMap := make(map[string]Tool)
	for i, tool := range tools {
		providerTools[i] = providers.Tool{
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters:  convertSchemaToMap(tool.Schema()),
		}
		toolMap[tool.Name()] = tool
	}

	// Convert history to provider format
	var providerMessages []providers.ChatMessage
	for _, msg := range history {
		providerMsg := providers.ChatMessage{
			Role:    convertRole(msg.Role),
			Content: msg.Content,
		}
		
		// Convert tool calls
		for _, tc := range msg.ToolCalls {
			providerMsg.ToolCalls = append(providerMsg.ToolCalls, providers.ToolCall{
				ID:        tc.ID,
				Name:      tc.Name,
				Arguments: convertInputToMap(tc.Input),
			})
		}
		
		// Convert tool results
		for _, tr := range msg.ToolResults {
			providerMsg.ToolResults = append(providerMsg.ToolResults, providers.ToolResult{
				ToolCallID: tr.ToolCallID,
				Content:    tr.Content,
				IsError:    tr.IsError,
			})
		}
		
		providerMessages = append(providerMessages, providerMsg)
	}

	// Add user message
	providerMessages = append(providerMessages, providers.ChatMessage{
		Role:    providers.RoleUser,
		Content: userPrompt,
	})

	// Track new messages for session storage
	newMessages := []StoredMessage{
		{Role: "user", Content: userPrompt},
	}

	var finalResponse strings.Builder
	var fullComs strings.Builder
	var totalUsage Usage
	turns := 0

	// Create cost logger if cost tracking is enabled
	var costLogger *logging.CostLogger
	if cfg.CostTracking {
		var err error
		costLogger, err = logging.NewCostLogger(cfg.LogFile)
		if err != nil {
			logger.Info("Warning: failed to create cost logger: %v", err)
		}
		defer func() {
			if costLogger != nil {
				costLogger.Close()
			}
		}()
	}

	// Log initial context if fullcoms enabled
	if cfg.FullComs {
		fullComs.WriteString("# Full Communication Log\n\n")
		fullComs.WriteString("## Provider\n\n")
		fullComs.WriteString(fmt.Sprintf("Provider: %s\n", provider.Name()))
		fullComs.WriteString("## System Prompt\n\n```\n")
		fullComs.WriteString(systemPrompt)
		fullComs.WriteString("\n```\n\n")
		fullComs.WriteString("## Conversation\n\n")
		if len(history) > 0 {
			fullComs.WriteString(fmt.Sprintf("(Session history: %d messages)\n\n", len(history)))
		}
		fullComs.WriteString("### User (new)\n\n")
		fullComs.WriteString(userPrompt)
		fullComs.WriteString("\n\n")
	}

	logger.Info("User prompt: %s", userPrompt)
	logger.Info("Starting conversation loop")

	for {
		turns++
		logger.Info("Turn %d: Sending request to LLM via %s", turns, provider.Name())
		
		if cfg.MaxTurns > 0 && turns > cfg.MaxTurns {
			logger.Info("Maximum turns (%d) exceeded, asking user to continue", cfg.MaxTurns)
			if !askToContinue(cfg.MaxTurns) {
				logger.Info("User chose to stop at %d turns", cfg.MaxTurns)
				return Result{
					Response:    finalResponse.String(),
					TotalTurns:  turns - 1,
					FullComs:    fullComs.String(),
					NewMessages: newMessages,
					Usage:       totalUsage,
				}
			} else {
				logger.Info("User chose to continue beyond %d turns", cfg.MaxTurns)
				cfg.MaxTurns = 0 // Set to unlimited
			}
		}

		// Create stream handler
		streamHandler := func(event providers.StreamEvent) {
			switch event.Type {
			case providers.StreamEventTypeText:
				fmt.Fprint(os.Stderr, colors.LLMText(event.Text))
				logger.StreamText(event.Text)
			case providers.StreamEventTypeDone:
				// Stream completed
			}
		}

		// Build completion request
		req := providers.ChatCompletionRequest{
			Model:         cfg.Model,
			Messages:      providerMessages,
			Tools:         providerTools,
			MaxTokens:     cfg.MaxTokens,
			SystemPrompt:  systemPrompt,
			Thinking:      cfg.Thinking,
			Stream:        true,
			StreamHandler: streamHandler,
		}

		// Log the request
		logger.ChatRequest(cfg.Model, len(providerMessages), systemPrompt)

		// Execute completion
		response, err := provider.CreateChatCompletion(ctx, req)
		if err != nil {
			logger.Info("Provider error: %v", err)
			return Result{
				Response:    finalResponse.String(),
				TotalTurns:  turns,
				Error:       err,
				FullComs:    fullComs.String(),
				NewMessages: newMessages,
				Usage:       totalUsage,
			}
		}

		// Update usage tracking
		totalUsage.InputTokens += response.Usage.InputTokens
		totalUsage.OutputTokens += response.Usage.OutputTokens
		totalUsage.CacheRead += response.Usage.CacheRead
		totalUsage.CacheWrite += response.Usage.CacheWrite
		totalUsage.Cost += response.Usage.Cost

		// Log this turn's cost
		if costLogger != nil {
			description := fmt.Sprintf("LLM turn %d (%s)", turns, provider.Name())
			if response.StopReason == providers.StopReasonToolCall {
				description += " (tool_call)"
			} else {
				description += " (complete)"
			}
			costLogger.LogStep(response.Usage.Cost, "llm_call", description, 
				response.Usage.InputTokens, response.Usage.OutputTokens, cfg.Model)
		}

		// Display running usage
		costMsg := fmt.Sprintf("\n[%.4f$] ", totalUsage.Cost)
		fmt.Fprint(os.Stderr, colors.Cost(costMsg))
		logger.StreamText(costMsg)

		// Log the response
		responseContent := extractTextContent(response.Content)
		stopReasonStr := string(response.StopReason)
		usageMap := map[string]int64{
			"input":  response.Usage.InputTokens,
			"output": response.Usage.OutputTokens,
		}
		logger.ChatResponse(responseContent, stopReasonStr, usageMap)
		logger.Info("Received response from %s, tokens: %d in, %d out", 
			provider.Name(), response.Usage.InputTokens, response.Usage.OutputTokens)

		// Build stored message for session
		storedAssistant := StoredMessage{Role: "assistant"}
		var textContent strings.Builder
		var storedToolCalls []StoredToolCall

		// Process response content
		if cfg.FullComs {
			fullComs.WriteString(fmt.Sprintf("### Assistant (turn %d)\n\n", turns))
		}
		
		for _, block := range response.Content {
			switch block.Type {
			case providers.ContentTypeText:
				textContent.WriteString(block.Text)
				if cfg.FullComs {
					fullComs.WriteString(block.Text)
					fullComs.WriteString("\n")
				}
			case providers.ContentTypeThinking:
				if cfg.FullComs {
					fullComs.WriteString("<thinking>\n")
					fullComs.WriteString(block.Thinking)
					fullComs.WriteString("\n</thinking>\n\n")
				}
			case providers.ContentTypeToolCall:
				if block.ToolCall != nil {
					inputJSON, _ := json.Marshal(block.ToolCall.Arguments)
					storedToolCalls = append(storedToolCalls, StoredToolCall{
						ID:    block.ToolCall.ID,
						Name:  block.ToolCall.Name,
						Input: string(inputJSON),
					})
					if cfg.FullComs {
						fullComs.WriteString(fmt.Sprintf("\n**Tool Call: %s** (id: %s)\n```json\n", 
							block.ToolCall.Name, block.ToolCall.ID))
						argsJSON, _ := json.MarshalIndent(block.ToolCall.Arguments, "", "  ")
						fullComs.WriteString(string(argsJSON))
						fullComs.WriteString("\n```\n")
					}
				}
			}
		}
		
		if cfg.FullComs {
			fullComs.WriteString("\n")
		}

		storedAssistant.Content = textContent.String()
		storedAssistant.ToolCalls = storedToolCalls
		newMessages = append(newMessages, storedAssistant)

		// Add assistant message to conversation
		assistantMsg := providers.ChatMessage{
			Role:    providers.RoleAssistant,
			Content: textContent.String(),
		}
		for _, tc := range storedToolCalls {
			assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, providers.ToolCall{
				ID:        tc.ID,
				Name:      tc.Name,
				Arguments: convertInputToMap(tc.Input),
			})
		}
		providerMessages = append(providerMessages, assistantMsg)

		// Collect text for final response
		finalResponse.WriteString(textContent.String())

		// Check if we need to execute tools
		if response.StopReason != providers.StopReasonToolCall {
			return Result{
				Response:    finalResponse.String(),
				TotalTurns:  turns,
				FullComs:    fullComs.String(),
				NewMessages: newMessages,
				Usage:       totalUsage,
			}
		}

		// Log tool results section
		if cfg.FullComs {
			fullComs.WriteString("### Tool Results\n\n")
		}

		// Execute tool calls
		var storedResults []StoredToolResult
		var toolResults []providers.ToolResult

		for _, block := range response.Content {
			if block.Type == providers.ContentTypeToolCall && block.ToolCall != nil {
				tc := block.ToolCall
				tool, ok := toolMap[tc.Name]
				if !ok {
					errMsg := fmt.Sprintf("Unknown tool: %s", tc.Name)
					toolResults = append(toolResults, providers.ToolResult{
						ToolCallID: tc.ID,
						Content:    errMsg,
						IsError:    true,
					})
					storedResults = append(storedResults, StoredToolResult{
						ToolCallID: tc.ID,
						Content:    errMsg,
						IsError:    true,
					})
					continue
				}

				// Execute tool
				logToolExecution(tc.Name, tc.Arguments, logger)
				result, err := tool.Execute(ctx, tc.Arguments)
				
				// Log tool cost tracking
				if costLogger != nil {
					costLogger.LogToolCall(tc.Name, tc.Arguments, err == nil)
				}
				
				if err != nil {
					errMsg := fmt.Sprintf("Error: %v", err)
					toolResults = append(toolResults, providers.ToolResult{
						ToolCallID: tc.ID,
						Content:    errMsg,
						IsError:    true,
					})
					storedResults = append(storedResults, StoredToolResult{
						ToolCallID: tc.ID,
						Content:    errMsg,
						IsError:    true,
					})
					continue
				}

				if result == "" {
					result = "(empty output)"
				}

				if cfg.FullComs {
					fullComs.WriteString(fmt.Sprintf("**%s** (id: %s)\n```\n", tc.Name, tc.ID))
					logResult := result
					if len(logResult) > 2000 {
						logResult = logResult[:2000] + "\n...[truncated]"
					}
					fullComs.WriteString(logResult)
					fullComs.WriteString("\n```\n\n")
				}

				toolResults = append(toolResults, providers.ToolResult{
					ToolCallID: tc.ID,
					Content:    result,
					IsError:    false,
				})
				storedResults = append(storedResults, StoredToolResult{
					ToolCallID: tc.ID,
					Content:    result,
					IsError:    false,
				})
			}
		}

		// Store tool results
		newMessages = append(newMessages, StoredMessage{Role: "tool_result", ToolResults: storedResults})

		// Add tool results to conversation
		if len(toolResults) > 0 {
			toolMsg := providers.ChatMessage{
				Role:        providers.RoleUser, // Tool results are sent as user messages
				ToolResults: toolResults,
			}
			providerMessages = append(providerMessages, toolMsg)
		}

		fmt.Fprintln(os.Stderr)
		logger.StreamText("\n")
	}
}

// Helper functions for type conversion

// convertRole converts from agent role to provider role
func convertRole(role string) providers.MessageRole {
	switch role {
	case "user":
		return providers.RoleUser
	case "assistant":
		return providers.RoleAssistant
	case "system":
		return providers.RoleSystem
	case "tool_result", "tool":
		return providers.RoleTool
	default:
		return providers.RoleUser
	}
}

// convertSchemaToMap converts Anthropic schema to generic map
func convertSchemaToMap(schema anthropic.ToolInputSchemaParam) map[string]any {
	// This is a simplified conversion - in practice you might need more robust handling
	result := make(map[string]any)
	
	// Try to convert the schema parameter to a map
	// This may need adjustment based on the actual Anthropic SDK types
	schemaBytes, err := json.Marshal(schema)
	if err != nil {
		return result
	}
	
	json.Unmarshal(schemaBytes, &result)
	return result
}

// convertInputToMap converts JSON string input to map
func convertInputToMap(input string) map[string]any {
	var result map[string]any
	json.Unmarshal([]byte(input), &result)
	return result
}

// extractTextContent extracts text content from content blocks
func extractTextContent(blocks []providers.ContentBlock) string {
	var result strings.Builder
	for _, block := range blocks {
		if block.Type == providers.ContentTypeText {
			result.WriteString(block.Text)
		}
	}
	return result.String()
}

