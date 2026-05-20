# Stepi

_Minimal file-based LLM coding agent._

A streamlined coding agent that works with files and pipes instead of complex UIs. Based on the well-performing Pi agent, stepi uses a single Go binary and focuses on Unix-style workflows.

> UI is temporary, but files are forever

## Quick Start

```bash
$ cd my-project
$ export ANTHROPIC_API_KEY=sk-...........

# Basic file mode - auto-generates output
$ echo "analyze the code in this project" > task.md
$ stepi task.md                    # Creates task.out.md

# Pipe mode for quick tasks
$ echo "what are the main files here?" | stepi

# Pipe mode with file saving
$ echo "create a README for this project" | stepi --name readme
# Creates readme.md and readme.out.md

# Multi-turn sessions
$ stepi --session-start myproject
$ echo "read main.go and explain it" | stepi --session myproject  
$ echo "now optimize the performance" | stepi --session myproject
$ stepi --session-end myproject
```

## Key Features

### Multiple LLM Providers
- **Anthropic Claude**: Full model family (claude-3-5-sonnet, haiku, etc.)
- **OpenAI**: GPT models and Codex for code generation
- **Google Gemini**: Gemini models with optional search capabilities  
- **Auto-detection**: Provider selected based on model name

```bash
$ stepi --model claude-3-5-haiku-20241022 task.md      # Fast and cheap
$ stepi --model gpt-4 task.md                          # OpenAI GPT-4  
$ stepi --model gemini-1.5-pro task.md                 # Google Gemini
$ stepi --model code-davinci-002 task.md               # Codex for coding
```

### Thinking Modes
Control the agent's reasoning depth:

```bash
$ stepi --thinking high complex-task.md    # Deep reasoning for complex problems
$ stepi --thinking low simple-task.md      # Quick responses for simple tasks
```

### Sessions for Multi-turn Conversations
Persistent conversations that remember context:

```bash
$ stepi --session-start analysis         # Start session  
$ echo "examine the database code" | stepi --session analysis
$ echo "find performance bottlenecks" | stepi --session analysis  
$ echo "suggest optimizations" | stepi --session analysis
$ stepi --session-end analysis          # Clean up
```

### Tool Integration
The agent has access to:
- **read**: Read any file in your project
- **write**: Create or overwrite files  
- **edit**: Make precise surgical edits
- **bash**: Execute shell commands

### Google Search with Gemini
Real-time information retrieval using Google's Gemini AI:

```bash
$ export GEMINI_API_KEY=your_api_key
$ stepi google "latest developments in AI"                    # Default model (pro)
$ stepi google --model gemini-1.5-flash "quick question"     # Faster model
$ stepi google --help                                         # Show detailed help
```

Get your Gemini API key from: https://makersuite.google.com/app/apikey

### Cost Tracking & Management
Automatic cost tracking with analysis tools:

```bash
$ stepi io costs                         # Show cost analysis by project
$ stepi io costs-csv                     # Generate unified CSV report
$ stepi io clean                         # Clean up generated files
```

## Build & Install

```bash
# Build from source (requires Go)
$ go build
$ cp stepi ~/.local/bin/  # or add to PATH
```

## Environment Variables

```bash
ANTHROPIC_API_KEY=sk-...        # Required for Claude models
OPENAI_API_KEY=sk-...           # Required for OpenAI models
GEMINI_API_KEY=...              # Required for Gemini models and google command
STEPI_MODEL=claude-sonnet-4     # Default model
STEPI_THINKING=medium           # Default thinking level
```

## File Organization

Stepi creates a clean file structure:
```
project/
├── task.md              # Your input
├── task.out.md         # Agent's response  
├── task.log            # Execution log
├── task.cost.csv       # Cost tracking
└── task.chatter        # Full LLM conversation
```

## Why Stepi?

- **File-based**: All inputs and outputs are files - no information lost in UIs
- **Automation-friendly**: Easy to integrate into scripts and workflows
- **Persistent**: Context and history preserved across sessions
- **Unix philosophy**: Compose with pipes, scripts, and other tools
- **Cost-conscious**: Track and analyze LLM usage costs
- **Multi-provider**: Use the best model for each task

Perfect for developers who prefer terminal workflows and want full control over their AI coding assistant interactions.
