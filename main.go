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
	"github.com/user/stepi/profile"
	"github.com/user/stepi/prompt"
	"github.com/user/stepi/providers"
	"github.com/user/stepi/session"
	"github.com/user/stepi/tools"
	"github.com/user/stepi/vars"
)

// extractStepNumber extracts step number from filename like ".stepi/step04.md" or "something_04.md"
func extractStepNumber(filename string) int {
	// Remove extension
	baseName := strings.TrimSuffix(filename, ".md")
	
	// Look for step pattern in .stepi/stepXX format
	if strings.Contains(baseName, ".stepi/step") {
		parts := strings.Split(baseName, "step")
		if len(parts) > 1 {
			stepStr := parts[len(parts)-1]
			if stepNum := parseStepNumber(stepStr); stepNum > 0 {
				return stepNum
			}
		}
	}
	
	// Look for _XX pattern at end
	parts := strings.Split(baseName, "_")
	if len(parts) > 1 {
		lastPart := parts[len(parts)-1]
		if stepNum := parseStepNumber(lastPart); stepNum > 0 {
			return stepNum
		}
	}
	
	return 0
}

// parseStepNumber parses a step number string like "04" or "4"
func parseStepNumber(s string) int {
	var num int
	if n, err := fmt.Sscanf(s, "%d", &num); err == nil && n == 1 && num > 0 {
		return num
	}
	return 0
}

// generateReadPrevPrefix generates the prefix instruction for reading previous step files.
// It uses the profile's ReadPrev template, substituting the three placeholders.
func generateReadPrevPrefix(currentFile, nameFlag string, prof *profile.Profile) string {
	var currentStep int
	var stepDir string
	
	if currentFile == "<stdin>" && nameFlag != "" {
		// Using pipe with --name flag
		currentStep = extractStepNumber(nameFlag)
		if strings.Contains(nameFlag, ".stepi/") {
			stepDir = ".stepi/"
		}
	} else if currentFile != "<stdin>" {
		// Using file input
		currentStep = extractStepNumber(currentFile)
		if strings.Contains(currentFile, ".stepi/") {
			stepDir = ".stepi/"
		}
	}
	
	if currentStep <= 1 {
		return "" // No previous step
	}
	
	prevStep := currentStep - 1
	prevStepStr := fmt.Sprintf("%02d", prevStep)
	
	var prevInput, prevOutput, prevLog string
	if stepDir != "" {
		// .stepi directory format
		prevInput  = fmt.Sprintf("%sstep%s.md",     stepDir, prevStepStr)
		prevOutput = fmt.Sprintf("%sstep%s.out.md", stepDir, prevStepStr)
		prevLog    = fmt.Sprintf("%sstep%s.log",    stepDir, prevStepStr)
	} else {
		// Generic format - extract base name pattern
		var baseName string
		if currentFile != "<stdin>" {
			baseName = strings.TrimSuffix(currentFile, fmt.Sprintf("%02d.md", currentStep))
		} else if nameFlag != "" {
			baseName = strings.TrimSuffix(nameFlag, fmt.Sprintf("%02d", currentStep))
		}
		if baseName == "" {
			return ""
		}
		prevInput  = fmt.Sprintf("%s%s.md",     baseName, prevStepStr)
		prevOutput = fmt.Sprintf("%s%s.out.md", baseName, prevStepStr)
		prevLog    = fmt.Sprintf("%s%s.log",    baseName, prevStepStr)
	}
	
	return prof.ExpandReadPrev(prevInput, prevOutput, prevLog)
}

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
		case "init":
			handleInitCommand()
			return
		case "google":
			if len(os.Args) < 3 {
				fmt.Fprintln(os.Stderr, "Error: google command requires a query argument")
				fmt.Fprintln(os.Stderr, "Usage: stepi google \"your question here\"")
				os.Exit(1)
			}
			handleGoogleCommand(os.Args[2:])
			return
		case "summarize":
			if len(os.Args) < 3 {
				fmt.Fprintln(os.Stderr, "Error: summarize command requires a name argument")
				fmt.Fprintln(os.Stderr, "Usage: stepi summarize <name>")
				os.Exit(1)
			}
			handleSummarizeCommand(os.Args[2])
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
	name := flag.String("name", "", "Name for file when using pipe input (creates <name>.md and generates <name>.out.md, etc.)")
	readprev := flag.Bool("readprev", false, "Prepend instruction to read previous step files (.stepi/stepXX.md and .stepi/stepXX.out.md)")
	silent := flag.Bool("silent", false, "Suppress tool output and edit details")
	profileName := flag.String("profile", "", "Profile name: load templates from profiles/<name>/ (default: built-in defaults)")
	help := flag.Bool("help", false, "Show help")
	flag.BoolVar(help, "h", false, "Show help")

	flag.Usage = func() {
		fmt.Println(`stepi - Minimal file-based LLM coding agent

Primary Usage:
  stepi [options] <input.md>              # Auto-generates <input>.out.md
  echo "prompt" | stepi [options]         # Pipe mode (output to stdout)
  echo "prompt" | stepi --name <name>     # Pipe mode (saves to <name>.md, generates <name>.out.md, etc.)
  stepi --session <name> [options]        # Multi-turn session mode

Legacy Usage:
  stepi [options] <input.md> <output.md>  # Explicit output (deprecated)

Commands:
  stepi list                              # List stepi files with metadata
  stepi models                            # Show available providers and models
  stepi google [--model <model>] [--name <name>] "question"       # Search using Gemini with Google Search grounding (supports --model gemini-3-flash-preview|gemini-3-pro-preview|gemini-2.5-pro|gemini-2.5-flash|gemini-2.0-flash|gemini-pro-latest|gemini-flash-latest)
  stepi io [options]                      # I/O operations
  stepi step [options]                    # Step-by-step execution
  stepi init                              # Initialize .stepi folder in current directory
  stepi summarize <name>                  # Generate summary of all files with given name

Options:
  --model <id>            Model ID (default: claude-sonnet-4-20250514)
  --provider <name>       LLM provider: anthropic, openai, gemini (auto-detected if not specified)
  --thinking <level>      Thinking level: off, low, medium, high (default: off)
  --fullcoms              Save full communication log to <output>.fullcoms.md
                          (Not available in session or pipe mode)
  --session <name>        Use existing session for multi-turn conversation
  --session-start <name>  Start a new session
  --session-end <name>    End (delete) a session
  --name <name>           Name for file when using pipe input (creates <name>.md and auxiliary files)
  --readprev              Prepend instruction to read previous step files (.stepi/stepXX.md and .stepi/stepXX.out.md and .stepi/stepXX.log)
  --silent                Suppress tool output and edit details
  --profile <name>        Use a named profile for system prompt and tool descriptions.
                          Looks for profiles/<name>/ in: .stepi/profiles/, ~/.config/stepi/profiles/, profiles/
                          Copy profiles/default/ to profiles/<name>/ and edit to customise.
  -h, --help              Show this help

Environment Variables:
  ANTHROPIC_API_KEY    Anthropic API key (required for Anthropic models)
  OPENAI_API_KEY       OpenAI API key (required for OpenAI/Codex models)
  GEMINI_API_KEY       Gemini API key (required for Gemini models and google command)
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
  echo "What is 2+2?" | stepi            # Pipe mode (output to stdout)
  echo "Analyze this code" | stepi --name analysis # Creates analysis.md, analysis.out.md, etc.
  stepi list                             # Show all stepi projects
  stepi init                             # Initialize .stepi folder
  stepi summarize myproject              # Generate summary of myproject files

Session examples:
  stepi --session-start myproject        # Start session
  echo "Read main.go" | stepi --session myproject
  echo "Explain it" | stepi --session myproject
  stepi --session-end myproject          # End session

File naming (simplified):
  Input: file.md generates:
  - file.out.md     (main output)
  - file.chatter    (LLM communication log)
  - file.cmds       (tool commands log)
  - file.log        (execution log)
  - file.cost.csv   (cost tracking)

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
		sessionProf, profErr := profile.Load(*profileName)
		if profErr != nil {
			fmt.Fprintf(os.Stderr, "Error loading profile %q: %v\n", *profileName, profErr)
			os.Exit(1)
		}
		systemPrompt := prompt.BuildWithPrompt(cwd, sessionProf.SystemPrompt)
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
	nameMode := *name != ""

	// Validate name flag usage
	if nameMode && !pipeMode {
		fmt.Fprintln(os.Stderr, "Error: --name flag can only be used with pipe input (echo \"...\" | stepi --name somename)")
		os.Exit(1)
	}
	if nameMode && sessionMode {
		fmt.Fprintln(os.Stderr, "Error: --name flag cannot be used with session mode")
		os.Exit(1)
	}

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
	} else if pipeMode && nameMode {
		// Pipe mode with name: read from stdin, save input as name.md, generate name.out.md
		inputFile = "<stdin>"
		outputFile = *name + ".out.md"
	} else if pipeMode {
		// Regular pipe mode: read from stdin, write to stdout
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

	// Set up logging if we have an output file (not pipe mode)
	var logBaseName string
	if !pipeMode && outputFile != "<stdout>" {
		// Calculate base name for auxiliary files: file.out.md -> file
		if strings.HasSuffix(outputFile, ".out.md") {
			logBaseName = strings.TrimSuffix(outputFile, ".out.md")
		} else if strings.HasSuffix(outputFile, ".md") {
			logBaseName = strings.TrimSuffix(outputFile, ".md")
		} else {
			logBaseName = outputFile
		}
	} else if nameMode && pipeMode {
		// For --name mode, use the name as base
		logBaseName = *name
	}

	// Load legacy config for agent compatibility
	cfg := config.FromEnv()
	cfg.Model = providerCfg.Model
	cfg.Thinking = providerCfg.Thinking
	cfg.FullComs = *fullcoms && !pipeMode && !sessionMode // Disable fullcoms in pipe mode and session mode
	cfg.Silent = *silent
	cfg.CostTracking = !sessionMode // Enable cost tracking by default, but disable for session mode

	// Set up log file for cost tracking with simplified naming
	if logBaseName != "" {
		cfg.LogFile = logBaseName
	} else if !pipeMode && outputFile != "<stdout>" {
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

	// Load profile (after flags are parsed so *profileName is available)
	prof, err := profile.Load(*profileName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading profile %q: %v\n", *profileName, err)
		os.Exit(1)
	}

	// Apply readprev prefix if flag is set
	if *readprev {
		prefix := generateReadPrevPrefix(inputFile, *name, prof)
		if prefix != "" {
			inputStr = prefix + inputStr
		}
	}

	// Expand template variables ({NAME}, {TASK}, {STEP}, {IN-N}, {OUT-N}, {LOG-N},
	// {INss:ee}, {OUTss:ee}, {LOGss:ee}) using the current filename or --name flag.
	{
		var expandPath string
		if inputFile != "<stdin>" {
			expandPath = inputFile
		} else if *name != "" {
			expandPath = *name
		}
		if expandPath != "" {
			expanded := vars.ExpandFromPath(inputStr, expandPath)
			if expanded != inputStr {
				fmt.Fprintln(os.Stderr, colors.Info("Variables expanded in input text"))
			}
			inputStr = expanded
		}
	}

	// Save input to file if using --name flag
	if nameMode && pipeMode {
		inputFileName := *name + ".md"
		if err := os.WriteFile(inputFileName, inputContent, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing input file %s: %v\n", inputFileName, err)
			os.Exit(1)
		}
		// Update inputFile for logging purposes
		inputFile = inputFileName
	}

	// Get working directory
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting working directory: %v\n", err)
		os.Exit(1)
	}

	// Build system prompt using the profile's system prompt template
	systemPrompt := prompt.BuildWithPrompt(cwd, prof.SystemPrompt)

	// Create tools, injecting per-profile descriptions
	agentTools := []agent.Tool{
		&tools.ReadTool{Cwd: cwd, Silent: cfg.Silent, Desc: prof.ToolRead},
		&tools.WriteTool{Cwd: cwd, Silent: cfg.Silent, Desc: prof.ToolWrite},
		&tools.EditTool{Cwd: cwd, Silent: cfg.Silent, Desc: prof.ToolEdit},
		&tools.BashTool{Cwd: cwd, Silent: cfg.Silent, Desc: prof.ToolBash},
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
	if logBaseName != "" {
		var err error
		logger, err = logging.NewLogger(logBaseName)
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
			// Generate fullcoms filename using simplified naming
			var fullcomsFile string
			if logBaseName != "" {
				fullcomsFile = logBaseName + ".fullcoms.md"
			} else if strings.HasSuffix(outputFile, ".out.md") {
				fullcomsFile = strings.TrimSuffix(outputFile, ".out.md") + ".fullcoms.md"
			} else if strings.HasSuffix(outputFile, ".md") {
				fullcomsFile = strings.TrimSuffix(outputFile, ".md") + ".fullcoms.md"
			} else {
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

// handleInitCommand creates a .stepi folder in the current directory
func handleInitCommand() {
	if err := os.MkdirAll(".stepi", 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating .stepi directory: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Initialized .stepi folder in current directory")
}

// handleSummarizeCommand generates a summary of all files with the given name
func handleSummarizeCommand(name string) {
	// Find all files matching the pattern
	var files []string
	
	// Pattern 1: .stepi/nameXX format
	stepiPattern := fmt.Sprintf(".stepi/%s*", name)
	stepiFiles, _ := filepath.Glob(stepiPattern)
	for _, file := range stepiFiles {
		if strings.HasSuffix(file, ".md") && !strings.HasSuffix(file, ".sum.md") {
			files = append(files, file)
		}
	}
	
	// Pattern 2: nameXX format
	pattern := fmt.Sprintf("%s*", name)
	nameFiles, _ := filepath.Glob(pattern)
	for _, file := range nameFiles {
		if strings.HasSuffix(file, ".md") && !strings.HasSuffix(file, ".sum.md") {
			files = append(files, file)
		}
	}
	
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "No files found matching pattern: %s\n", name)
		os.Exit(1)
	}
	
	// Sort files numerically
	sort.Slice(files, func(i, j int) bool {
		numI := extractStepNumber(files[i])
		numJ := extractStepNumber(files[j])
		if numI == 0 && numJ == 0 {
			return files[i] < files[j]
		}
		return numI < numJ
	})
	
	// Read all files and generate summary
	var summary strings.Builder
	summary.WriteString(fmt.Sprintf("# Summary: %s\n\n", name))
	
	// Extract overall goal from first file
	if len(files) > 0 {
		if content, err := os.ReadFile(files[0]); err == nil {
			lines := strings.Split(string(content), "\n")
			if len(lines) > 0 {
				firstLine := strings.TrimSpace(lines[0])
				if firstLine != "" {
					summary.WriteString("## Overall Goal\n\n")
					summary.WriteString(fmt.Sprintf("%s\n\n", firstLine))
				}
			}
		}
	}
	
	summary.WriteString("## Steps Summary\n\n")
	
	for i, file := range files {
		stepNum := extractStepNumber(file)
		if stepNum == 0 {
			stepNum = i + 1
		}
		
		content, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Could not read file %s: %v\n", file, err)
			continue
		}
		
		summary.WriteString(fmt.Sprintf("### Step %d: %s\n\n", stepNum, file))
		
		// Extract first few lines as step description
		lines := strings.Split(string(content), "\n")
		descLines := 0
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				summary.WriteString(fmt.Sprintf("- %s\n", line))
				descLines++
				if descLines >= 3 {
					break
				}
			}
		}
		
		// Look for corresponding .out.md file to see what was accomplished
		outFile := strings.TrimSuffix(file, ".md") + ".out.md"
		if outContent, err := os.ReadFile(outFile); err == nil {
			outLines := strings.Split(string(outContent), "\n")
			summary.WriteString("\n**What was done:**\n")
			
			// Extract key accomplishments (look for tool usage or key changes)
			toolUsed := false
			for _, line := range outLines {
				line = strings.TrimSpace(line)
				if strings.Contains(line, "bash") || strings.Contains(line, "write") || 
				   strings.Contains(line, "edit") || strings.Contains(line, "read") {
					if !toolUsed {
						summary.WriteString("- Used tools: ")
						toolUsed = true
					}
				}
				
				// Look for completion indicators
				if strings.Contains(strings.ToLower(line), "done") || 
				   strings.Contains(strings.ToLower(line), "completed") ||
				   strings.Contains(strings.ToLower(line), "successfully") {
					summary.WriteString(fmt.Sprintf("- %s\n", line))
					break
				}
			}
			
			if toolUsed {
				summary.WriteString("bash, read, write, edit\n")
			}
		}
		
		summary.WriteString("\n")
	}
	
	// Write summary to file
	summaryFile := fmt.Sprintf("%s.sum.md", name)
	if strings.Contains(name, ".stepi/") {
		summaryFile = fmt.Sprintf("%s.sum.md", name)
	}
	
	if err := os.WriteFile(summaryFile, []byte(summary.String()), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing summary file: %v\n", err)
		os.Exit(1)
	}
	
	fmt.Printf("Summary written to: %s\n", summaryFile)
	fmt.Printf("Analyzed %d files\n", len(files))
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

// handleGoogleCommand handles the google search command
func handleGoogleCommand(args []string) {
	// Check for help flag first
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			fmt.Println("stepi google - Search using Google's Gemini AI with Google Search Grounding")
			fmt.Println()
			fmt.Println("Usage:")
			fmt.Println("  stepi google [--model <model>] [--name <name>] \"your question here\"")
			fmt.Println()
			fmt.Println("Options:")
			fmt.Println("  --model <model>    Gemini model to use (default: gemini-3-flash-preview)")
			fmt.Println("                     Available: gemini-3-flash-preview, gemini-3-pro-preview,")
			fmt.Println("                     gemini-2.5-pro, gemini-2.5-flash, gemini-2.0-flash,") 
			fmt.Println("                     gemini-pro-latest, gemini-flash-latest")
			fmt.Println("  --name <name>      Save output to <name>.google.md with quoted question and horizontal rule")
			fmt.Println()
			fmt.Println("Google Search Grounding:")
			fmt.Println("  This command uses real Google Search grounding with supported models.")
			fmt.Println("  Responses include live search results, source citations, and search metadata.")
			fmt.Println("  All models support Google Search grounding via the unified GenAI SDK.")
			fmt.Println()
			fmt.Println("Environment Variables:")
			fmt.Println("  GEMINI_API_KEY     Required. Get from: https://makersuite.google.com/app/apikey")
			fmt.Println()
			fmt.Println("Examples:")
			fmt.Println("  stepi google \"latest developments in AI\"")
			fmt.Println("  stepi google --model gemini-3-flash-preview \"current news about Go programming\"")
			fmt.Println("  stepi google --name .stepi/question_bla \"what happened in the stock market today\"")
			return
		}
	}
	
	// Parse google command flags
	model := "gemini-3-flash-preview" // Default to newest model with best search capabilities
	var nameFlag string
	var profileName string
	var query string
	
	// Simple argument parsing for google command
	i := 0
	for i < len(args) {
		if args[i] == "--model" && i+1 < len(args) {
			model = args[i+1]
			// Remove model flag and its value from args
			args = append(args[:i], args[i+2:]...)
		} else if args[i] == "--name" && i+1 < len(args) {
			nameFlag = args[i+1]
			// Remove name flag and its value from args
			args = append(args[:i], args[i+2:]...)
		} else if args[i] == "--profile" && i+1 < len(args) {
			profileName = args[i+1]
			// Remove profile flag and its value from args
			args = append(args[:i], args[i+2:]...)
		} else {
			i++
		}
	}
	
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: No query provided")
		fmt.Fprintln(os.Stderr, "Usage: stepi google [--model <model>] [--name <name>] \"your question here\"")
		fmt.Fprintln(os.Stderr, "Available models: gemini-3-flash-preview, gemini-3-pro-preview, gemini-2.5-pro, gemini-2.5-flash, gemini-2.0-flash, gemini-pro-latest, gemini-flash-latest")
		fmt.Fprintln(os.Stderr, "Use: stepi google --help for more information")
		os.Exit(1)
	}

	// Join all arguments to form the query
	query = strings.Join(args, " ")

	// Get Gemini API key from environment
	geminiAPIKey := os.Getenv("GEMINI_API_KEY")
	if geminiAPIKey == "" {
		fmt.Fprintln(os.Stderr, "Error: GEMINI_API_KEY environment variable is required")
		fmt.Fprintln(os.Stderr, "Please set your Gemini API key: export GEMINI_API_KEY=your_api_key_here")
		fmt.Fprintln(os.Stderr, "Get your API key from: https://makersuite.google.com/app/apikey")
		os.Exit(1)
	}

	// Validate model
	validModels := []string{"gemini-3-flash-preview", "gemini-3-pro-preview", "gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.0-flash", "gemini-pro-latest", "gemini-flash-latest"}
	isValidModel := false
	for _, validModel := range validModels {
		if model == validModel {
			isValidModel = true
			break
		}
	}
	if !isValidModel {
		fmt.Fprintf(os.Stderr, "Error: Invalid model '%s'. Valid models are: %s\n", model, strings.Join(validModels, ", "))
		os.Exit(1)
	}

	// Create provider config with search grounding enabled
	config := providers.ProviderConfig{
		Provider:     "gemini",
		GeminiAPIKey: geminiAPIKey,
		GeminiSettings: providers.GeminiSettings{
			SearchGrounding: true, // Enable real Google Search grounding
		},
	}

	// Create Gemini provider (will use unified SDK due to SearchGrounding=true)
	provider, err := providers.NewProvider(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating provider: %v\n", err)
		os.Exit(1)
	}

	// Load profile to get the search prompt template
	googleProf, profErr := profile.Load(profileName)
	if profErr != nil {
		fmt.Fprintf(os.Stderr, "Error loading profile %q: %v\n", profileName, profErr)
		os.Exit(1)
	}
	// Build search prompt from the profile template
	searchPrompt := googleProf.ExpandSearchPrompt(query)

	// Set parameters for search queries
	maxTokens := 4096

	// Create chat completion request
	req := providers.ChatCompletionRequest{
		Model:       model,
		MaxTokens:   maxTokens,
		Messages: []providers.ChatMessage{
			{
				Role:    providers.RoleUser,
				Content: searchPrompt,
			},
		},
	}

	// Display search status
	fmt.Printf("🔍 Google Search + Gemini (%s) searching for: %s\n", model, query)
	fmt.Println("" + strings.Repeat("=", 80))

	// Execute the request with real Google Search grounding
	ctx := context.Background()
	resp, err := provider.CreateChatCompletion(ctx, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n❌ Error during search: %v\n", err)
		os.Exit(1)
	}

	// Prepare the output content
	var output strings.Builder
	
	// Add quoted question at the top
	output.WriteString(fmt.Sprintf("> %s\n\n", query))
	
	// Add the response
	if len(resp.Content) > 0 {
		output.WriteString(resp.Content[0].Text)
	}
	
	// Add horizontal rule at the end for future questions
	output.WriteString("\n\n---\n\n")

	// Handle output based on --name flag
	if nameFlag != "" {
		outputFile := nameFlag + ".google.md"
		
		// Check if file exists to determine if we should append
		var existingContent []byte
		var err error
		if _, statErr := os.Stat(outputFile); statErr == nil {
			// File exists, read existing content to append to it
			existingContent, err = os.ReadFile(outputFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error reading existing file %s: %v\n", outputFile, err)
				os.Exit(1)
			}
		}
		
		// Combine existing content with new content
		var finalContent string
		if len(existingContent) > 0 {
			finalContent = string(existingContent) + output.String()
		} else {
			finalContent = output.String()
		}
		
		// Write/append to file
		if err := os.WriteFile(outputFile, []byte(finalContent), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing to file %s: %v\n", outputFile, err)
			os.Exit(1)
		}
		
		fmt.Printf("✅ Results saved to: %s\n", outputFile)
	} else {
		// Print the response to stdout
		fmt.Print(output.String())
	}

	// Print usage information
	fmt.Println("" + strings.Repeat("=", 80))
	fmt.Printf("📊 Usage: %d input tokens, %d output tokens (Cost: $%.6f)\n", 
		resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.Cost)
	
	if nameFlag != "" {
		fmt.Println("✅ Search completed with real Google Search grounding and source citations.")
	} else {
		fmt.Println("✅ Search completed with real Google Search grounding and source citations.")
	}
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
