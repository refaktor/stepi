package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/user/stepi/logging"
)

// askToContinue prompts the user to continue when turn limit is reached
func askToContinue(currentTurns int) bool {
	fmt.Fprintf(os.Stderr, "\n\nReached %d turns. Continue with additional turns? (y/N): ", currentTurns)
	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes"
}

// Run executes the agent loop with the given prompt
func Run(ctx context.Context, systemPrompt, userPrompt string, tools []Tool, cfg Config, logger *logging.Logger) Result {
	logger.Info("Starting agent execution")
	logger.Info("Model: %s", cfg.Model)
	
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
	
	// Create client
	opts := []option.RequestOption{}
	if cfg.ApiKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.ApiKey))
	}
	client := anthropic.NewClient(opts...)

	// Convert tools to Anthropic format
	anthropicTools := make([]anthropic.ToolUnionParam, len(tools))
	toolMap := make(map[string]Tool)
	for i, tool := range tools {
		anthropicTools[i] = anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        tool.Name(),
				Description: anthropic.String(tool.Description()),
				InputSchema: tool.Schema(),
			},
		}
		toolMap[tool.Name()] = tool
	}

	// Initialize messages
	messages := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock(userPrompt)),
	}

	// Build system prompt blocks
	var systemBlocks []anthropic.TextBlockParam
	if systemPrompt != "" {
		cacheControl := anthropic.NewCacheControlEphemeralParam()
		systemBlocks = []anthropic.TextBlockParam{
			{Text: systemPrompt, CacheControl: cacheControl},
		}
	}

	var finalResponse strings.Builder
	var fullComs strings.Builder
	var totalUsage Usage
	turns := 0

	// Log initial context if fullcoms enabled
	if cfg.FullComs {
		fullComs.WriteString("# Full Communication Log\n\n")
		fullComs.WriteString("## System Prompt\n\n```\n")
		fullComs.WriteString(systemPrompt)
		fullComs.WriteString("\n```\n\n")
		fullComs.WriteString("## Conversation\n\n")
		fullComs.WriteString("### User\n\n")
		fullComs.WriteString(userPrompt)
		fullComs.WriteString("\n\n")
	}

	logger.Info("User prompt: %s", userPrompt)
	logger.Info("Starting conversation loop")

	for {
		turns++
		logger.Info("Turn %d: Sending request to LLM", turns)
		
		if cfg.MaxTurns > 0 && turns > cfg.MaxTurns {
			logger.Info("Maximum turns (%d) exceeded, asking user to continue", cfg.MaxTurns)
			if !askToContinue(cfg.MaxTurns) {
				logger.Info("User chose to stop at %d turns", cfg.MaxTurns)
				return Result{
					Response:   finalResponse.String(),
					TotalTurns: turns - 1,
					FullComs:   fullComs.String(),
					Usage:      totalUsage,
				}
			} else {
				logger.Info("User chose to continue beyond %d turns", cfg.MaxTurns)
				// Reset the turn limit to allow continuing
				cfg.MaxTurns = 0 // Set to unlimited
			}
		}

		// Build request params
		params := anthropic.MessageNewParams{
			Model:     anthropic.Model(cfg.Model),
			MaxTokens: int64(cfg.MaxTokens),
			Messages:  messages,
			Tools:     anthropicTools,
		}
		if len(systemBlocks) > 0 {
			params.System = systemBlocks
		}

		// Add thinking/extended thinking if requested
		if cfg.Thinking != "off" && cfg.Thinking != "" {
			budget := getThinkingBudget(cfg.Thinking)
			if budget > 0 {
				params.Thinking = anthropic.ThinkingConfigParamOfEnabled(int64(budget))
			}
		}

		// Log the request
		logger.ChatRequest(cfg.Model, len(messages), systemPrompt)

		// Stream the response
		stream := client.Messages.NewStreaming(ctx, params)

		message := anthropic.Message{}
		for stream.Next() {
			event := stream.Current()
			if err := message.Accumulate(event); err != nil {
				return Result{Error: fmt.Errorf("accumulate error: %w", err), FullComs: fullComs.String(), Usage: totalUsage}
			}

			// Print streaming output and log it
			switch ev := event.AsAny().(type) {
			case anthropic.ContentBlockDeltaEvent:
				if ev.Delta.Text != "" {
					fmt.Fprint(os.Stderr, ev.Delta.Text)
					logger.StreamText(ev.Delta.Text) // Log the streaming text
				}
			}
		}

		if stream.Err() != nil {
			logger.Info("Stream error: %v", stream.Err())
			return Result{
				Response:   finalResponse.String(),
				TotalTurns: turns,
				Error:      stream.Err(),
				FullComs:   fullComs.String(),
				Usage:      totalUsage,
			}
		}

		// Update usage tracking
		totalUsage.InputTokens += message.Usage.InputTokens
		totalUsage.OutputTokens += message.Usage.OutputTokens
		totalUsage.CacheRead += message.Usage.CacheReadInputTokens
		totalUsage.CacheWrite += message.Usage.CacheCreationInputTokens
		turnCost := CalculateCost(cfg.Model, message.Usage.InputTokens, message.Usage.OutputTokens)
		totalUsage.Cost = CalculateCost(cfg.Model, totalUsage.InputTokens, totalUsage.OutputTokens)

		// Log this turn's cost
		if costLogger != nil {
			description := fmt.Sprintf("LLM turn %d", turns)
			if message.StopReason == "tool_use" {
				description += " (tool_use)"
			} else {
				description += " (complete)"
			}
			costLogger.LogStep(turnCost, "llm_call", description, message.Usage.InputTokens, message.Usage.OutputTokens, cfg.Model)
		}

		// Display running usage
		costMsg := fmt.Sprintf("\n[%.4f$] ", totalUsage.Cost)
		fmt.Fprint(os.Stderr, costMsg)
		logger.StreamText(costMsg)

		// Log the response
		responseContent := ""
		for _, block := range message.Content {
			if block.Type == "text" {
				responseContent += block.Text
			}
		}
		usageMap := map[string]int64{
			"input":  message.Usage.InputTokens,
			"output": message.Usage.OutputTokens,
		}
		logger.ChatResponse(responseContent, string(message.StopReason), usageMap)
		logger.Info("Received response from LLM, tokens: %d in, %d out", 
			message.Usage.InputTokens, message.Usage.OutputTokens)

		// Add assistant message to history
		messages = append(messages, message.ToParam())

		// Log assistant response if fullcoms enabled
		if cfg.FullComs {
			fullComs.WriteString(fmt.Sprintf("### Assistant (turn %d)\n\n", turns))
			for _, block := range message.Content {
				switch variant := block.AsAny().(type) {
				case anthropic.TextBlock:
					fullComs.WriteString(variant.Text)
					fullComs.WriteString("\n")
				case anthropic.ThinkingBlock:
					fullComs.WriteString("<thinking>\n")
					fullComs.WriteString(variant.Thinking)
					fullComs.WriteString("\n</thinking>\n\n")
				case anthropic.ToolUseBlock:
					fullComs.WriteString(fmt.Sprintf("\n**Tool Call: %s** (id: %s)\n```json\n", variant.Name, variant.ID))
					fullComs.WriteString(variant.JSON.Input.Raw())
					fullComs.WriteString("\n```\n")
				}
			}
			fullComs.WriteString("\n")
		}

		// Collect text content
		for _, block := range message.Content {
			if block.Type == "text" {
				finalResponse.WriteString(block.Text)
			}
		}

		// Check if we need to execute tools
		if message.StopReason != "tool_use" {
			// Done - no more tool calls
			logger.Info("Conversation complete, no more tool calls")
			return Result{
				Response:   finalResponse.String(),
				TotalTurns: turns,
				FullComs:   fullComs.String(),
				Usage:      totalUsage,
			}
		}

		logger.Info("LLM wants to execute tools")

		// Log tool results section if fullcoms enabled
		if cfg.FullComs {
			fullComs.WriteString("### Tool Results\n\n")
		}

		// Execute tool calls
		toolResults := []anthropic.ContentBlockParamUnion{}
		for _, block := range message.Content {
			switch variant := block.AsAny().(type) {
			case anthropic.ToolUseBlock:
				tool, ok := toolMap[block.Name]
				if !ok {
					toolResults = append(toolResults,
						anthropic.NewToolResultBlock(block.ID, fmt.Sprintf("Unknown tool: %s", block.Name), true))
					continue
				}

				// Parse arguments
				var args map[string]any
				if err := json.Unmarshal([]byte(variant.JSON.Input.Raw()), &args); err != nil {
					toolResults = append(toolResults,
						anthropic.NewToolResultBlock(block.ID, fmt.Sprintf("Invalid arguments: %v", err), true))
					continue
				}

				// Execute tool - show tool name and relevant info
				logToolExecution(block.Name, args, logger)
				result, err := tool.Execute(ctx, args)
				
				// Log tool execution
				logger.Command(block.Name, args, result, err)
				
				// Log tool cost tracking
				if costLogger != nil {
					costLogger.LogToolCall(block.Name, args, err == nil)
				}
				
				if err != nil {
					logger.Info("Tool %s failed: %v", block.Name, err)
					toolResults = append(toolResults,
						anthropic.NewToolResultBlock(block.ID, fmt.Sprintf("Error: %v", err), true))
					continue
				} else {
					logger.Info("Tool %s executed successfully", block.Name)
				}

				// Ensure non-empty result (API requires non-empty text blocks)
				if result == "" {
					result = "(empty output)"
				}

				// Log tool result if fullcoms enabled
				if cfg.FullComs {
					fullComs.WriteString(fmt.Sprintf("**%s** (id: %s)\n```\n", block.Name, block.ID))
					// Truncate very long results in log
					logResult := result
					if len(logResult) > 2000 {
						logResult = logResult[:2000] + "\n...[truncated]"
					}
					fullComs.WriteString(logResult)
					fullComs.WriteString("\n```\n\n")
				}

				toolResults = append(toolResults,
					anthropic.NewToolResultBlock(block.ID, result, false))
			}
		}

		// Add tool results to messages
		messages = append(messages, anthropic.NewUserMessage(toolResults...))
		fmt.Fprintln(os.Stderr)
		logger.StreamText("\n")
	}
}

func getThinkingBudget(level string) int {
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

// logToolExecution logs a tool execution to both stderr and the logger
func logToolExecution(toolName string, args map[string]any, logger *logging.Logger) {
	var toolMsg string
	switch toolName {
	case "bash":
		if cmd, ok := args["command"].(string); ok {
			toolMsg = fmt.Sprintf("\n[bash] %s\n", cmd)
		} else {
			toolMsg = "\n[bash]\n"
		}
	case "read":
		if path, ok := args["path"].(string); ok {
			toolMsg = fmt.Sprintf("\n[read] %s\n", path)
		} else {
			toolMsg = "\n[read]\n"
		}
	case "write":
		if path, ok := args["path"].(string); ok {
			toolMsg = fmt.Sprintf("\n[write] %s\n", path)
		} else {
			toolMsg = "\n[write]\n"
		}
	case "edit":
		if path, ok := args["path"].(string); ok {
			toolMsg = fmt.Sprintf("\n[edit] %s\n", path)
		} else {
			toolMsg = "\n[edit]\n"
		}
	default:
		toolMsg = fmt.Sprintf("\n[%s]\n", toolName)
	}
	fmt.Fprint(os.Stderr, toolMsg)
	logger.StreamText(toolMsg)
}

// RunWithHistory executes the agent loop with previous conversation history
func RunWithHistory(ctx context.Context, systemPrompt, userPrompt string, history []StoredMessage, tools []Tool, cfg Config, logger *logging.Logger) Result {
	logger.Info("Starting agent execution with session history")
	logger.Info("Model: %s, History messages: %d", cfg.Model, len(history))
	
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
	
	// Create client
	opts := []option.RequestOption{}
	if cfg.ApiKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.ApiKey))
	}
	client := anthropic.NewClient(opts...)

	// Convert tools to Anthropic format
	anthropicTools := make([]anthropic.ToolUnionParam, len(tools))
	toolMap := make(map[string]Tool)
	for i, tool := range tools {
		anthropicTools[i] = anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        tool.Name(),
				Description: anthropic.String(tool.Description()),
				InputSchema: tool.Schema(),
			},
		}
		toolMap[tool.Name()] = tool
	}

	// Build messages from history
	messages := []anthropic.MessageParam{}
	for _, msg := range history {
		switch msg.Role {
		case "user":
			messages = append(messages, anthropic.NewUserMessage(anthropic.NewTextBlock(msg.Content)))
		case "assistant":
			blocks := []anthropic.ContentBlockParamUnion{}
			if msg.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
			}
			for _, tc := range msg.ToolCalls {
				blocks = append(blocks, anthropic.ContentBlockParamUnion{
					OfToolUse: &anthropic.ToolUseBlockParam{
						ID:    tc.ID,
						Name:  tc.Name,
						Input: json.RawMessage(tc.Input),
					},
				})
			}
			messages = append(messages, anthropic.NewAssistantMessage(blocks...))
		case "tool_result":
			results := []anthropic.ContentBlockParamUnion{}
			for _, tr := range msg.ToolResults {
				results = append(results, anthropic.NewToolResultBlock(tr.ToolCallID, tr.Content, tr.IsError))
			}
			messages = append(messages, anthropic.NewUserMessage(results...))
		}
	}

	// Add the new user message
	messages = append(messages, anthropic.NewUserMessage(anthropic.NewTextBlock(userPrompt)))

	// Track new messages for session storage
	newMessages := []StoredMessage{
		{Role: "user", Content: userPrompt},
	}

	// Build system prompt blocks
	var systemBlocks []anthropic.TextBlockParam
	if systemPrompt != "" {
		cacheControl := anthropic.NewCacheControlEphemeralParam()
		systemBlocks = []anthropic.TextBlockParam{
			{Text: systemPrompt, CacheControl: cacheControl},
		}
	}

	var finalResponse strings.Builder
	var fullComs strings.Builder
	var totalUsage Usage
	turns := 0

	// Log initial context if fullcoms enabled
	if cfg.FullComs {
		fullComs.WriteString("# Full Communication Log\n\n")
		fullComs.WriteString("## System Prompt\n\n```\n")
		fullComs.WriteString(systemPrompt)
		fullComs.WriteString("\n```\n\n")
		fullComs.WriteString("## Conversation\n\n")
		fullComs.WriteString(fmt.Sprintf("(Session history: %d messages)\n\n", len(history)))
		fullComs.WriteString("### User (new)\n\n")
		fullComs.WriteString(userPrompt)
		fullComs.WriteString("\n\n")
	}

	logger.Info("User prompt: %s", userPrompt)
	logger.Info("Starting conversation loop")

	for {
		turns++
		logger.Info("Turn %d: Sending request to LLM", turns)
		
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
				// Reset the turn limit to allow continuing
				cfg.MaxTurns = 0 // Set to unlimited
			}
		}

		// Build request params
		params := anthropic.MessageNewParams{
			Model:     anthropic.Model(cfg.Model),
			MaxTokens: int64(cfg.MaxTokens),
			Messages:  messages,
			Tools:     anthropicTools,
		}
		if len(systemBlocks) > 0 {
			params.System = systemBlocks
		}

		// Add thinking if requested
		if cfg.Thinking != "off" && cfg.Thinking != "" {
			budget := getThinkingBudget(cfg.Thinking)
			if budget > 0 {
				params.Thinking = anthropic.ThinkingConfigParamOfEnabled(int64(budget))
			}
		}

		// Log the request
		logger.ChatRequest(cfg.Model, len(messages), systemPrompt)

		// Stream the response
		stream := client.Messages.NewStreaming(ctx, params)

		message := anthropic.Message{}
		for stream.Next() {
			event := stream.Current()
			if err := message.Accumulate(event); err != nil {
				return Result{Error: fmt.Errorf("accumulate error: %w", err), FullComs: fullComs.String(), NewMessages: newMessages, Usage: totalUsage}
			}

			switch ev := event.AsAny().(type) {
			case anthropic.ContentBlockDeltaEvent:
				if ev.Delta.Text != "" {
					fmt.Fprint(os.Stderr, ev.Delta.Text)
					logger.StreamText(ev.Delta.Text) // Log the streaming text
				}
			}
		}

		if stream.Err() != nil {
			return Result{
				Response:    finalResponse.String(),
				TotalTurns:  turns,
				Error:       stream.Err(),
				FullComs:    fullComs.String(),
				NewMessages: newMessages,
				Usage:       totalUsage,
			}
		}

		// Update usage tracking
		totalUsage.InputTokens += message.Usage.InputTokens
		totalUsage.OutputTokens += message.Usage.OutputTokens
		totalUsage.CacheRead += message.Usage.CacheReadInputTokens
		totalUsage.CacheWrite += message.Usage.CacheCreationInputTokens
		turnCost := CalculateCost(cfg.Model, message.Usage.InputTokens, message.Usage.OutputTokens)
		totalUsage.Cost = CalculateCost(cfg.Model, totalUsage.InputTokens, totalUsage.OutputTokens)

		// Log this turn's cost
		if costLogger != nil {
			description := fmt.Sprintf("LLM turn %d (session)", turns)
			if message.StopReason == "tool_use" {
				description += " (tool_use)"
			} else {
				description += " (complete)"
			}
			costLogger.LogStep(turnCost, "llm_call", description, message.Usage.InputTokens, message.Usage.OutputTokens, cfg.Model)
		}

		// Display running usage
		costMsg := fmt.Sprintf("\n[%.4f$] ", totalUsage.Cost)
		fmt.Fprint(os.Stderr, costMsg)
		logger.StreamText(costMsg)

		// Log the response
		responseContent := ""
		for _, block := range message.Content {
			if block.Type == "text" {
				responseContent += block.Text
			}
		}
		usageMap := map[string]int64{
			"input":  message.Usage.InputTokens,
			"output": message.Usage.OutputTokens,
		}
		logger.ChatResponse(responseContent, string(message.StopReason), usageMap)
		logger.Info("Received response from LLM, tokens: %d in, %d out", 
			message.Usage.InputTokens, message.Usage.OutputTokens)

		// Add assistant message to history
		messages = append(messages, message.ToParam())

		// Build stored message for session
		storedAssistant := StoredMessage{Role: "assistant"}
		var textContent strings.Builder
		var storedToolCalls []StoredToolCall

		// Log and collect content
		if cfg.FullComs {
			fullComs.WriteString(fmt.Sprintf("### Assistant (turn %d)\n\n", turns))
		}
		for _, block := range message.Content {
			switch variant := block.AsAny().(type) {
			case anthropic.TextBlock:
				textContent.WriteString(variant.Text)
				if cfg.FullComs {
					fullComs.WriteString(variant.Text)
					fullComs.WriteString("\n")
				}
			case anthropic.ThinkingBlock:
				if cfg.FullComs {
					fullComs.WriteString("<thinking>\n")
					fullComs.WriteString(variant.Thinking)
					fullComs.WriteString("\n</thinking>\n\n")
				}
			case anthropic.ToolUseBlock:
				storedToolCalls = append(storedToolCalls, StoredToolCall{
					ID:    variant.ID,
					Name:  variant.Name,
					Input: variant.JSON.Input.Raw(),
				})
				if cfg.FullComs {
					fullComs.WriteString(fmt.Sprintf("\n**Tool Call: %s** (id: %s)\n```json\n", variant.Name, variant.ID))
					fullComs.WriteString(variant.JSON.Input.Raw())
					fullComs.WriteString("\n```\n")
				}
			}
		}
		if cfg.FullComs {
			fullComs.WriteString("\n")
		}

		storedAssistant.Content = textContent.String()
		storedAssistant.ToolCalls = storedToolCalls
		newMessages = append(newMessages, storedAssistant)

		// Collect text for final response
		finalResponse.WriteString(textContent.String())

		// Check if we need to execute tools
		if message.StopReason != "tool_use" {
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
		toolResults := []anthropic.ContentBlockParamUnion{}
		storedResults := []StoredToolResult{}

		for _, block := range message.Content {
			switch variant := block.AsAny().(type) {
			case anthropic.ToolUseBlock:
				tool, ok := toolMap[block.Name]
				if !ok {
					errMsg := fmt.Sprintf("Unknown tool: %s", block.Name)
					toolResults = append(toolResults, anthropic.NewToolResultBlock(block.ID, errMsg, true))
					storedResults = append(storedResults, StoredToolResult{ToolCallID: block.ID, Content: errMsg, IsError: true})
					continue
				}

				var args map[string]any
				if err := json.Unmarshal([]byte(variant.JSON.Input.Raw()), &args); err != nil {
					errMsg := fmt.Sprintf("Invalid arguments: %v", err)
					toolResults = append(toolResults, anthropic.NewToolResultBlock(block.ID, errMsg, true))
					storedResults = append(storedResults, StoredToolResult{ToolCallID: block.ID, Content: errMsg, IsError: true})
					continue
				}

				// Execute tool - show tool name and relevant info
				logToolExecution(block.Name, args, logger)

				result, err := tool.Execute(ctx, args)
				
				// Log tool cost tracking
				if costLogger != nil {
					costLogger.LogToolCall(block.Name, args, err == nil)
				}
				
				if err != nil {
					errMsg := fmt.Sprintf("Error: %v", err)
					toolResults = append(toolResults, anthropic.NewToolResultBlock(block.ID, errMsg, true))
					storedResults = append(storedResults, StoredToolResult{ToolCallID: block.ID, Content: errMsg, IsError: true})
					continue
				}

				if result == "" {
					result = "(empty output)"
				}

				if cfg.FullComs {
					fullComs.WriteString(fmt.Sprintf("**%s** (id: %s)\n```\n", block.Name, block.ID))
					logResult := result
					if len(logResult) > 2000 {
						logResult = logResult[:2000] + "\n...[truncated]"
					}
					fullComs.WriteString(logResult)
					fullComs.WriteString("\n```\n\n")
				}

				toolResults = append(toolResults, anthropic.NewToolResultBlock(block.ID, result, false))
				storedResults = append(storedResults, StoredToolResult{ToolCallID: block.ID, Content: result, IsError: false})
			}
		}

		// Store tool results
		newMessages = append(newMessages, StoredMessage{Role: "tool_result", ToolResults: storedResults})

		// Add tool results to messages
		messages = append(messages, anthropic.NewUserMessage(toolResults...))
		fmt.Fprintln(os.Stderr)
		logger.StreamText("\n")
	}
}
