package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/user/stepi/agent"
	"github.com/user/stepi/colors"
	"github.com/user/stepi/config"
	"github.com/user/stepi/logging"
	"github.com/user/stepi/prompt"
	"github.com/user/stepi/providers"
	"github.com/user/stepi/session"
	"github.com/user/stepi/tools"
)

// isPiped returns true if stdin is piped (not a terminal)
func isPiped() bool {
	stat, _ := os.Stdin.Stat()
	return (stat.Mode() & os.ModeCharDevice) == 0
}

func main() {
	// Check if first argument is a subcommand
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "list":
			handleListCommand()
			return
		case "io":
			handleIOCommand(os.Args[2:])
			return
		case "step":
			handleStepCommand(os.Args[2:])
			return
		case "models":
			providers.PrintProvidersInfo()
			return
		}
	}

	// Parse flags
	model := flag.String("model", "", "Model ID (default: claude-sonnet-4-20250514)")
	provider := flag.String("provider", "", "LLM provider: anthropic, openai (auto-detected from model if not specified)")
	thinking := flag.String("thinking", "", "Thinking level: off, low, medium, high (default: off)")
	fullcoms := flag.Bool("fullcoms", false, "Save full communication log to <output>.fullcoms.md")
	sessionName := flag.String("session", "", "Use existing session for multi-turn conversation")
	sessionStart := flag.String("session-start", "", "Start a new session with given name")
	sessionEnd := flag.String("session-end", "", "End (delete) a session")
	help := flag.Bool("help", false, "Show help")
	flag.BoolVar(help, "h", false, "Show help")

	flag.Usage = func() {
		fmt.Println(`stepi - Minimal file-based LLM coding agent

Primary Usage:
  stepi [options] <input.md>              # Auto-generates <input>.out.md
  echo "prompt" | stepi [options]         # Pipe mode (output to stdout)
  stepi --session <name> [options]        # Multi-turn session mode

Legacy Usage:
  stepi [options] <input.md> <output.md>  # Explicit output (deprecated)

Subcommands:
  stepi list                              # List stepi files with metadata
  stepi models                            # Show available providers and models
  stepi io [options]                      # I/O operations
  stepi step [options]                    # Step-by-step execution

Options:
  --model <id>            Model ID (default: claude-sonnet-4-20250514)
  --provider <name>       LLM provider: anthropic, openai (auto-detected if not specified)
  --thinking <level>      Thinking level: off, low, medium, high (default: off)
  --fullcoms              Save full communication log to <output>.fullcoms.md
                          (Not available in session or pipe mode)
  --session <name>        Use existing session for multi-turn conversation
  --session-start <name>  Start a new session
  --session-end <name>    End (delete) a session
  -h, --help              Show this help

Environment Variables:
  ANTHROPIC_API_KEY    Anthropic API key (required for Anthropic models)
  OPENAI_API_KEY       OpenAI API key (required for OpenAI/Codex models)
  STEPI_MODEL          Default model
  STEPI_PROVIDER       Default provider
  STEPI_THINKING       Default thinking level
  OPENAI_TEMPERATURE   OpenAI temperature (0.0-2.0)
  OPENAI_TOP_P         OpenAI top_p (0.0-1.0)

Examples:
  stepi prompt.md                        # Auto-generates prompt.out.md
  stepi stepi_some_01.md                 # Auto-generates stepi_some_01.out.md
  stepi --model claude-3-5-haiku-20241022 input.md
  stepi --model gpt-4 input.md           # Use OpenAI GPT-4
  stepi --model code-davinci-002 input.md # Use OpenAI Codex
  stepi --provider openai --model gpt-3.5-turbo input.md
  stepi --thinking high complex-task.md
  stepi --fullcoms task.md               # Also saves task.out.fullcoms.md
  echo "What is 2+2?" | stepi            # Pipe mode
  stepi list                             # Show all stepi projects

Session examples:
  stepi --session-start myproject        # Start session
  echo "Read main.go" | stepi --session myproject
  echo "Explain it" | stepi --session myproject
  stepi --session-end myproject          # End session

`)
	}

	flag.Parse()

	if *help {
		flag.Usage()
		os.Exit(0)
	}

	// Handle session-end first (no other args needed)
	if *sessionEnd != "" {
		if err := session.Delete(*sessionEnd); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Session '%s' ended\n", *sessionEnd)
		os.Exit(0)
	}

	// Handle session-start
	if *sessionStart != "" {
		// Load provider config for session creation
		sessionCfg := config.FromEnvProvider()
		if *model != "" {
			sessionCfg.Model = *model
			if *provider == "" {
				sessionCfg.Provider = providers.GetProviderForModel(*model)
			}
		}
		if *provider != "" {
			sessionCfg.Provider = *provider
		}
		
		// Validate provider and model
		sessionProvider, err := providers.NewProvider(sessionCfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if !sessionProvider.ValidateModel(sessionCfg.Model) {
			fmt.Fprintf(os.Stderr, "Error: model '%s' is not supported by provider '%s'\n", 
				sessionCfg.Model, sessionProvider.Name())
			os.Exit(1)
		}
		
		cwd, _ := os.Getwd()
		systemPrompt := prompt.Build(cwd)
		if err := session.Create(*sessionStart, systemPrompt, sessionCfg.Model); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Session '%s' started (provider: %s, model: %s)\n", 
			*sessionStart, sessionProvider.Name(), sessionCfg.Model)
		os.Exit(0)
	}

	args := flag.Args()
	pipeMode := isPiped()
	sessionMode := *sessionName != ""

	var inputFile, outputFile string
	if sessionMode && pipeMode {
		// Session + pipe mode: read from stdin, write to stdout
		inputFile = "<stdin>"
		outputFile = "<stdout>"
	} else if sessionMode {
		// Session + file mode: input required, output optional
		if len(args) < 1 {
			// No args - must use pipe
			fmt.Fprintln(os.Stderr, "Error: session mode requires input (file or pipe)")
			os.Exit(1)
		}
		inputFile = args[0]
		if len(args) >= 2 {
			outputFile = args[1]
		} else {
			outputFile = "<stdout>"
		}
	} else if pipeMode {
		// Pipe mode: read from stdin, write to stdout
		inputFile = "<stdin>"
		outputFile = "<stdout>"
	} else {
		// File mode: require input file, auto-generate output filename
		if len(args) == 1 {
			// Single argument mode - auto-generate output filename
			inputFile = args[0]
			// Generate output filename: input.md -> input.out.md
			if strings.HasSuffix(inputFile, ".md") {
				outputFile = strings.TrimSuffix(inputFile, ".md") + ".out.md"
			} else {
				outputFile = inputFile + ".out"
			}
		} else if len(args) == 2 {
			// Legacy mode: accept both input and output files explicitly
			fmt.Fprintln(os.Stderr, "Warning: Two-argument mode is deprecated. Use: stepi input.md (auto-generates input.out.md)")
			inputFile = args[0]
			outputFile = args[1]
		} else {
			flag.Usage()
			os.Exit(1)
		}
	}

	// Load provider config from env
	providerCfg := config.FromEnvProvider()

	// Override with flags
	if *model != "" {
		providerCfg.Model = *model
		// Auto-detect provider from model if not explicitly set
		if *provider == "" {
			providerCfg.Provider = providers.GetProviderForModel(*model)
		}
	}
	if *provider != "" {
		providerCfg.Provider = *provider
	}
	if *thinking != "" {
		providerCfg.Thinking = *thinking
	}

	// Create provider
	llmProvider, err := providers.NewProvider(providerCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating provider: %v\n", err)
		os.Exit(1)
	}

	// Validate model
	if !llmProvider.ValidateModel(providerCfg.Model) {
		fmt.Fprintf(os.Stderr, "Error: model '%s' is not supported by provider '%s'\n", 
			providerCfg.Model, llmProvider.Name())
		fmt.Fprintf(os.Stderr, "Supported models for %s: %s\n", 
			llmProvider.Name(), strings.Join(llmProvider.Models(), ", "))
		os.Exit(1)
	}

	// Load legacy config for agent compatibility
	cfg := config.FromEnv()
	cfg.Model = providerCfg.Model
	cfg.Thinking = providerCfg.Thinking
	cfg.FullComs = *fullcoms && !pipeMode && !sessionMode // Disable fullcoms in pipe mode and session mode
	cfg.CostTracking = !sessionMode // Enable cost tracking by default, but disable for session mode

	// Set up logging if we have an output file (not pipe mode)
	if !pipeMode && outputFile != "<stdout>" {
		cfg.LogFile = outputFile
	}



	// Read input
	var inputContent []byte
	if pipeMode {
		inputContent, err = io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
			os.Exit(1)
		}
	} else {
		inputContent, err = os.ReadFile(inputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading input file: %v\n", err)
			os.Exit(1)
		}
	}

	// Check for empty input
	inputStr := strings.TrimSpace(string(inputContent))
	if inputStr == "" {
		fmt.Fprintln(os.Stderr, "Error: input is empty")
		os.Exit(1)
	}

	// Get working directory
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting working directory: %v\n", err)
		os.Exit(1)
	}

	// Build system prompt
	systemPrompt := prompt.Build(cwd)

	// Create tools
	agentTools := []agent.Tool{
		&tools.ReadTool{Cwd: cwd},
		&tools.WriteTool{Cwd: cwd},
		&tools.EditTool{Cwd: cwd},
		&tools.BashTool{Cwd: cwd},
	}

	// Setup context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\nInterrupted")
		cancel()
	}()

	// Print info (only in file mode, not session+pipe)
	if !pipeMode && !sessionMode {
		fmt.Fprintf(os.Stderr, colors.Info("Provider: %s\n"), llmProvider.Name())
		fmt.Fprintf(os.Stderr, colors.Info("Model: %s\n"), cfg.Model)
		fmt.Fprintf(os.Stderr, colors.Info("Input: %s\n"), inputFile)
		fmt.Fprintf(os.Stderr, colors.Info("Output: %s\n"), outputFile)
		fmt.Fprintln(os.Stderr, colors.Info("---"))
	} else if sessionMode && !pipeMode {
		fmt.Fprintf(os.Stderr, colors.Info("Session: %s\n"), *sessionName)
		fmt.Fprintf(os.Stderr, colors.Info("Provider: %s\n"), llmProvider.Name())
		fmt.Fprintln(os.Stderr, colors.Info("---"))
	}

	// Set up logger
	var logger *logging.Logger
	if !pipeMode && outputFile != "<stdout>" {
		var err error
		logger, err = logging.NewLogger(outputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to create logger: %v\n", err)
			logger = &logging.Logger{} // No-op logger
		}
		defer logger.Close()
	} else {
		logger = &logging.Logger{} // No-op logger
	}

	// Run agent
	var result agent.Result
	if sessionMode {
		// Load session
		sess, err := session.Load(*sessionName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		// Warn if overriding session model
		if *model != "" && *model != sess.Model {
			fmt.Fprintf(os.Stderr, "Warning: Overriding session model (%s) with %s\n", sess.Model, *model)
		}

		// Convert session messages to agent format
		history := make([]agent.StoredMessage, len(sess.Messages))
		for i, msg := range sess.Messages {
			history[i] = agent.StoredMessage{
				Role:    msg.Role,
				Content: msg.Content,
			}
			for _, tc := range msg.ToolCalls {
				history[i].ToolCalls = append(history[i].ToolCalls, agent.StoredToolCall{
					ID: tc.ID, Name: tc.Name, Input: tc.Input,
				})
			}
			for _, tr := range msg.ToolResults {
				history[i].ToolResults = append(history[i].ToolResults, agent.StoredToolResult{
					ToolCallID: tr.ToolCallID, Content: tr.Content, IsError: tr.IsError,
				})
			}
		}

		// Use session's system prompt with the provider system
		result = agent.RunWithProviderAndHistory(ctx, sess.SystemPrompt, inputStr, history, agentTools, llmProvider, cfg, logger)

		// Save new messages to session
		for _, msg := range result.NewMessages {
			switch msg.Role {
			case "user":
				sess.AddUserMessage(msg.Content)
			case "assistant":
				toolCalls := make([]session.ToolCall, len(msg.ToolCalls))
				for i, tc := range msg.ToolCalls {
					toolCalls[i] = session.ToolCall{ID: tc.ID, Name: tc.Name, Input: tc.Input}
				}
				sess.AddAssistantMessage(msg.Content, toolCalls)
			case "tool_result":
				results := make([]session.ToolResult, len(msg.ToolResults))
				for i, tr := range msg.ToolResults {
					results[i] = session.ToolResult{ToolCallID: tr.ToolCallID, Content: tr.Content, IsError: tr.IsError}
				}
				sess.AddToolResults(results)
			}
		}

		if err := sess.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to save session: %v\n", err)
		}
	} else {
		result = agent.RunWithProvider(ctx, systemPrompt, inputStr, agentTools, llmProvider, cfg, logger)
	}

	if !pipeMode && !sessionMode {
		fmt.Fprintln(os.Stderr, "\n---")
	}

	if result.Error != nil {
		fmt.Fprintf(os.Stderr, colors.Error("Error: %v\n"), result.Error)
	}

	// Write output
	if pipeMode || (sessionMode && outputFile == "<stdout>") {
		// Pipe mode or session without output file: write to stdout
		fmt.Print(result.Response)
	} else {
		// File mode: write to file
		if err := os.WriteFile(outputFile, []byte(result.Response), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing output file: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, colors.Success("Written to: %s\n"), outputFile)

		// Write fullcoms if enabled
		if *fullcoms && result.FullComs != "" {
			// Generate fullcoms filename: output.md -> output.fullcoms.md
			fullcomsFile := strings.TrimSuffix(outputFile, ".md") + ".fullcoms.md"
			if !strings.HasSuffix(outputFile, ".md") {
				fullcomsFile = outputFile + ".fullcoms.md"
			}
			if err := os.WriteFile(fullcomsFile, []byte(result.FullComs), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing fullcoms file: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "Full communication log: %s\n", fullcomsFile)
			}
		}
		fmt.Fprintf(os.Stderr, colors.Info("Turns: %d | Tokens: %d in, %d out | "), 
			result.TotalTurns, result.Usage.InputTokens, result.Usage.OutputTokens)
		fmt.Fprintf(os.Stderr, colors.Cost("Cost: $%.4f\n"), result.Usage.Cost)
	}

	if result.Error != nil {
		os.Exit(1)
	}
}



type StepiFileInfo struct {
	ProjectName string
	Steps       int
	FirstDate   time.Time
	LastDate    time.Time
	TotalCost   float64
	FilePath    string
}

// handleListCommand lists stepi files with metadata
func handleListCommand() {
	files, err := filepath.Glob("*.md")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding stepi files: %v\n", err)
		os.Exit(1)
	}

	var stepiFiles []StepiFileInfo
	
	for _, file := range files {
		// Look for stepi files with pattern like "project_name_XX.md"
		if strings.Contains(file, "_v2-") || strings.Contains(file, "_") {
			info := analyzeStepiFile(file)
			if info != nil {
				stepiFiles = append(stepiFiles, *info)
			}
		}
	}

	// Sort by project name
	sort.Slice(stepiFiles, func(i, j int) bool {
		return stepiFiles[i].ProjectName < stepiFiles[j].ProjectName
	})

	fmt.Printf("%-20s %-6s %-12s %-12s %-10s %s\n", "PROJECT", "STEPS", "FIRST", "LAST", "COST", "FILE")
	fmt.Println(strings.Repeat("-", 80))
	
	for _, info := range stepiFiles {
		fmt.Printf("%-20s %-6d %-12s %-12s $%-9.4f %s\n",
			info.ProjectName,
			info.Steps,
			info.FirstDate.Format("2006-01-02"),
			info.LastDate.Format("2006-01-02"),
			info.TotalCost,
			info.FilePath)
	}
}

// analyzeStepiFile analyzes a stepi file to extract metadata
func analyzeStepiFile(filename string) *StepiFileInfo {
	// Extract project name from filename
	baseName := strings.TrimSuffix(filename, ".md")
	projectName := extractProjectName(baseName)
	
	if projectName == "" {
		return nil
	}

	info := &StepiFileInfo{
		ProjectName: projectName,
		FilePath:    filename,
		Steps:       0,
		TotalCost:   0.0,
	}

	// Look for corresponding cost.csv file
	costFile := strings.TrimSuffix(filename, ".md") + ".cost.csv"
	if _, err := os.Stat(costFile); err == nil {
		analyzeCostFile(costFile, info)
	}

	// Look for .out.md file to get dates
	outFile := strings.TrimSuffix(filename, ".md") + ".out.md"
	if _, err := os.Stat(outFile); err == nil {
		analyzeDates(outFile, info)
	}

	return info
}

// extractProjectName extracts project name from filename
func extractProjectName(baseName string) string {
	// Handle patterns like "stepi_v2-02" -> "stepi"
	if strings.Contains(baseName, "_v2-") {
		parts := strings.Split(baseName, "_v2-")
		if len(parts) > 0 {
			return parts[0]
		}
	}
	
	// Handle other patterns with underscores
	parts := strings.Split(baseName, "_")
	if len(parts) > 1 {
		return parts[0]
	}
	
	return baseName
}

// analyzeCostFile reads cost CSV and extracts steps and total cost
func analyzeCostFile(filename string, info *StepiFileInfo) {
	file, err := os.Open(filename)
	if err != nil {
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return
	}

	stepSet := make(map[string]bool)
	totalCost := 0.0
	var firstTime, lastTime time.Time

	for i, record := range records {
		if i == 0 { // Skip header
			continue
		}
		if len(record) >= 3 {
			stepSet[record[1]] = true // Track unique steps
			if cost := parseFloat(record[2]); cost > 0 {
				totalCost += cost
			}
			
			// Parse timestamp
			if len(record) >= 1 {
				if t, err := time.Parse("2006-01-02 15:04:05", record[0]); err == nil {
					if firstTime.IsZero() || t.Before(firstTime) {
						firstTime = t
					}
					if lastTime.IsZero() || t.After(lastTime) {
						lastTime = t
					}
				}
			}
		}
	}

	info.Steps = len(stepSet)
	info.TotalCost = totalCost
	if !firstTime.IsZero() {
		info.FirstDate = firstTime
	}
	if !lastTime.IsZero() {
		info.LastDate = lastTime
	}
}

// analyzeDates extracts dates from output file if cost file doesn't have them
func analyzeDates(filename string, info *StepiFileInfo) {
	if !info.FirstDate.IsZero() && !info.LastDate.IsZero() {
		return // Already have dates from cost file
	}

	fileInfo, err := os.Stat(filename)
	if err != nil {
		return
	}

	modTime := fileInfo.ModTime()
	if info.FirstDate.IsZero() {
		info.FirstDate = modTime
	}
	if info.LastDate.IsZero() {
		info.LastDate = modTime
	}
}

// parseFloat safely parses a float string
func parseFloat(s string) float64 {
	if f, err := fmt.Sscanf(s, "%f", new(float64)); err == nil && f == 1 {
		var result float64
		fmt.Sscanf(s, "%f", &result)
		return result
	}
	return 0.0
}

// handleIOCommand handles I/O operations
func handleIOCommand(args []string) {
	if len(args) == 0 {
		fmt.Println(`stepi io - Input/Output operations

Usage:
  stepi io list-inputs     # List available input files
  stepi io list-outputs    # List generated output files  
  stepi io clean           # Clean generated files
  stepi io costs           # Show cost analysis
  stepi io costs-csv       # Generate unified costs CSV`)
		return
	}

	switch args[0] {
	case "list-inputs":
		listInputFiles()
	case "list-outputs": 
		listOutputFiles()
	case "clean":
		cleanGeneratedFiles()
	case "costs":
		showCostAnalysis()
	case "costs-csv":
		generateUnifiedCostsCSV()
	default:
		fmt.Fprintf(os.Stderr, "Unknown io command: %s\n", args[0])
		os.Exit(1)
	}
}

// handleStepCommand handles step-by-step execution
func handleStepCommand(args []string) {
	if len(args) == 0 {
		fmt.Println(`stepi step - Step-by-step execution

Usage:
  stepi step run <input.md>     # Run with step-by-step prompts
  stepi step continue <file>    # Continue from last step
  stepi step status <file>      # Show execution status`)
		return
	}

	switch args[0] {
	case "run":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Error: input file required\n")
			os.Exit(1)
		}
		runStepByStep(args[1])
	case "continue":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Error: file required\n")
			os.Exit(1)
		}
		continueStepByStep(args[1])
	case "status":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Error: file required\n")
			os.Exit(1)
		}
		showStepStatus(args[1])
	default:
		fmt.Fprintf(os.Stderr, "Unknown step command: %s\n", args[0])
		os.Exit(1)
	}
}

// I/O command implementations
func listInputFiles() {
	files, _ := filepath.Glob("*.md")
	fmt.Println("Input files (.md):")
	for _, file := range files {
		if !strings.HasSuffix(file, ".out.md") {
			fmt.Printf("  %s\n", file)
		}
	}
}

func listOutputFiles() {
	files, _ := filepath.Glob("*.out.md")
	fmt.Println("Output files:")
	for _, file := range files {
		fmt.Printf("  %s\n", file)
	}

	logs, _ := filepath.Glob("*.log")
	if len(logs) > 0 {
		fmt.Println("\nLog files:")
		for _, file := range logs {
			fmt.Printf("  %s\n", file)
		}
	}

	costs, _ := filepath.Glob("*.cost.csv")
	if len(costs) > 0 {
		fmt.Println("\nCost files:")
		for _, file := range costs {
			fmt.Printf("  %s\n", file)
		}
	}
}

func cleanGeneratedFiles() {
	patterns := []string{"*.out.md", "*.log", "*.cmds", "*.chatter", "*.cost.csv", "*.fullcoms.md"}
	
	for _, pattern := range patterns {
		files, _ := filepath.Glob(pattern)
		for _, file := range files {
			if err := os.Remove(file); err == nil {
				fmt.Printf("Removed: %s\n", file)
			}
		}
	}
}

func showCostAnalysis() {
	costs, _ := filepath.Glob("*.cost.csv")
	if len(costs) == 0 {
		fmt.Println("No cost files found.")
		return
	}

	totalCost := 0.0
	totalSteps := 0
	
	fmt.Println("Cost Analysis by Project:")
	fmt.Println(strings.Repeat("-", 50))
	
	for _, costFile := range costs {
		projectName := strings.TrimSuffix(costFile, ".cost.csv")
		cost, steps := analyzeSingleCostFile(costFile)
		totalCost += cost
		totalSteps += steps
		fmt.Printf("%-30s $%8.4f (%d steps)\n", projectName, cost, steps)
	}
	
	fmt.Println(strings.Repeat("-", 50))
	fmt.Printf("%-30s $%8.4f (%d steps)\n", "TOTAL", totalCost, totalSteps)
}

func analyzeSingleCostFile(filename string) (float64, int) {
	file, err := os.Open(filename)
	if err != nil {
		return 0, 0
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return 0, 0
	}

	totalCost := 0.0
	stepCount := 0
	
	for i, record := range records {
		if i == 0 { // Skip header
			continue
		}
		if len(record) >= 3 {
			stepCount++
			totalCost += parseFloat(record[2])
		}
	}

	return totalCost, stepCount
}

func generateUnifiedCostsCSV() {
	costs, _ := filepath.Glob("*.cost.csv")
	if len(costs) == 0 {
		fmt.Println("No cost files found.")
		return
	}

	outputFile := "stepi_unified_costs.csv"
	file, err := os.Create(outputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating unified CSV: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write header with project column
	writer.Write([]string{"project", "timestamp", "step", "cost", "type", "description", "input_tokens", "output_tokens", "model"})

	for _, costFile := range costs {
		projectName := extractProjectName(strings.TrimSuffix(costFile, ".cost.csv"))
		
		costFileHandle, err := os.Open(costFile)
		if err != nil {
			continue
		}
		
		reader := csv.NewReader(costFileHandle)
		records, err := reader.ReadAll()
		costFileHandle.Close()
		
		if err != nil {
			continue
		}

		for i, record := range records {
			if i == 0 { // Skip header
				continue
			}
			// Add project name as first column
			newRecord := append([]string{projectName}, record...)
			writer.Write(newRecord)
		}
	}

	fmt.Printf("Unified costs written to: %s\n", outputFile)
}

// Step command implementations
func runStepByStep(inputFile string) {
	fmt.Printf("Step-by-step execution of %s not yet implemented\n", inputFile)
	// TODO: Implement interactive step-by-step execution
}

func continueStepByStep(file string) {
	fmt.Printf("Continue step-by-step execution of %s not yet implemented\n", file)
	// TODO: Implement continuation from last step
}

func showStepStatus(file string) {
	fmt.Printf("Status of %s not yet implemented\n", file)
	// TODO: Show execution status and progress
}
