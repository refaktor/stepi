# Stepi

_Minimal file-based LLM coding agent._

A streamlined coding agent that works with files and pipes instead of complex UIs. Based on the well-performing Pi agent, stepi uses a single Go binary and focuses on Unix-style workflows.

> UI is temporary, but files are forever

## Basic use

Create a .md file for input, get .out.md as response. Echo input directly, get response and use `stepi` instead of Google.

```bash
$ cd my-project
$ export ANTHROPIC_API_KEY=sk-...........

# Basic file mode - auto-generates output
$ echo "analyze the code in this project" > task.md
$ stepi task.md                    # Creates task.out.md and additional files

# Pipe mode for quick tasks
$ echo "what are the main files here?" | stepi

# Gemini grounded with Google Search
$ stepi google "what are the top tech news of today?"
```

## Conventions for work on projects

On bigger project we usually create a .stepi folder.

```bash
$ cd another-project
$ export ANTHROPIC_API_KEY=sk-...........

# create a .stepi/ folder
$ stepi init

# cat problem to a file
$ cat > .stepi/task01.md
analyze the source code and make a index or map of modules into
<Ctrl-c>

$ stepi .stepi/task01.md
# shows what it's doing, creates files:
# * .stepi/task01.out.md -- as task output
# * .stepi/task01.log    -- stores what it printed out (log of what it was doing)
# * .stepi/task01.cmds   -- log of all tool commands
# * .stepi/task01.chatter -- log of raw communication with llm model

# Pipe mode with file saving
$ echo "create a README for this project" | stepi --name .stepi/task02
# creates .stepi/task02.md , .stepi/task02.out.md and other files
```

## Key Features

### Multiple LLM Providers
- **Anthropic Claude**: Full model family (claude-3-5-sonnet, haiku, etc.)
- **OpenAI**: GPT models and Codex for code generation
- **Google Gemini**: Gemini models with optional search capabilities  
- **Auto-detection**: Provider selected based on model name

### Thinking Modes

Control the agent's reasoning depth:

```bash
$ stepi --thinking high complex-task.md    # Deep reasoning for complex problems
$ stepi --thinking low simple-task.md      # Quick responses for simple tasks
```

### Google Search with Gemini

Real-time information retrieval using Google's Gemini AI:

```bash
$ export GEMINI_API_KEY=your_api_key
$ stepi google "latest developments in AI"                    # Default model (pro)
$ stepi google --help                                         # Show detailed help
```

Get your Gemini API key from: https://makersuite.google.com/app/apikey

## Get & Install

Download the latest release for your system:

https://github.com/refaktor/stepi/releases/tag/v0.2

Or build from source:

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

## Why Stepi?

- **File-based**: All inputs and outputs are files - no information lost in UIs
- **Automation-friendly**: Easy to integrate into scripts and workflows
- **Persistent**: Context and history preserved across sessions
- **Unix philosophy**: Compose with pipes, scripts, and other tools
- **Cost-conscious**: Track and analyze LLM usage costs
- **Multi-provider**: Use the best model for each task

Perfect for developers who prefer terminal workflows and want full control over their AI coding assistant interactions.

----

# Advanced usage

## VARIABLES

When you create a step you can now refer to previous input, output or log files by these:

```bash
$ ( git diff && echo "analyze the diff above and describe the changes being made" | stepi -name .stepi/diff01

$ echo "read {OUT-1} and check if any changes are critical or introduce risk" | stepi -name .stepi/diff02

$ echo "add tests for changes described in files: {OUT01:02} " | stepi -name .stepi/diff03
```
## PROFILES

Experimental: all the texts for communicating with llm-s was extracted to profiles/default/* . You can make your ownd profiles/ subfolder and tune them and then run the agent with your profile

```bash
$ cd profiles

$ cp -r default short

# look at the search prompt of default profile
$ cat profiles/default/search_prompt.md 
Search for and provide current information about: {QUERY}

Please provide comprehensive, up-to-date information with specific details and context.

# set a different prompt for out short profile
$ cat > short/search_prompt.md
Search for and provide current information about: {QUERY}

Please provide up-to-date information and summarize it to 5 lines.
(ctrl-c)

$ stepi  google "what is ryelang and does it make any sense to learn it" --profile short
... result in 5 lines ...
```
