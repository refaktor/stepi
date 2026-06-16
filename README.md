# Stepi

_Minimal file-based LLM coding agent._

A streamlined coding agent that works with files and pipes instead of complex UIs. Based on the well-performing Pi agent, stepi uses a single Go binary and focuses on Unix-style workflows.

> UI is temporary, but files are forever

## Why Stepi?

- **File-based**: All inputs and outputs are files - no information lost in UIs
- **Automation-friendly**: Easy to integrate into scripts and workflows
- **Persistent**: Context and history preserved across sessions
- **Unix philosophy**: Compose with pipes, scripts, and other tools
- **Cost-conscious**: Track and analyze LLM usage costs
- **Multi-provider**: Use the best model for each task

Perfect for developers who prefer terminal workflows and want full control over their AI coding assistant interactions.

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
$ cd my-project

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
$ echo "use {OUT-1} and create a README for this project" | stepi --name .stepi/task02
# creates .stepi/task02.md , .stepi/task02.out.md and other files
```

## Google Search with Gemini

Real-time information retrieval using Google's Gemini AI:

```bash
$ export GEMINI_API_KEY=your_api_key
$ stepi google "latest developments in AI"                    # Default model (pro)
$ stepi google --help                                         # Show detailed help
```

Get your Gemini API key from: https://makersuite.google.com/app/apikey

## Get & Install

Download the latest release for your system:

**https://github.com/refaktor/stepi/releases/**

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

----

# Advanced usage

## VARIABLES

When you create a step you can now refer to previous input, output or log files by these:

```bash
$ ( git diff && echo "analyze the diff above and describe the changes being made" ) | stepi -name .stepi/diff01

$ echo "read {OUT-1} and check if any changes are critical or introduce risk" | stepi -name .stepi/diff02

$ echo "add tests for changes described in files: {OUT01:02} " | stepi -name .stepi/diff03
```

| Variable | Example result | Notes |
|----------|---------------|-------|
| `{STEP}` | `03` | Current step, zero-padded |
| `{IN-1}` | `sometask02.md` | Input file N steps back |
| `{OUT-1}` | `sometask02.out.md` | Output file N steps back |
| `{LOG-1}` | `sometask02.log` | Log file N steps back |
| `{IN-2}`, `{OUT-2}`, … | — | Any N ≥ 1 |
| `{IN01:03}` | `sometask01.md`<br>`sometask02.md`<br>`sometask03.md` | Range of input files |
| `{OUT02:04}` | — | Range of output files |
| `{LOG03:04}` | — | Range of log files |


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

$ stepi google -profile short "what is ryelang and does it make any sense to learn it"
... result in 5 lines ...
```

# Cookbook

Just some useful oneliners / examples:

```bash
# see the tasks you were working on on current project
ls -ltr .stepi

# find a particular keyword you were working on
grep -r "multilang" .stepi

# summarize a task with multiple steps
stepi summarize .stepi/task
# summary is written to .stepi/task.sum.md

# continue from where you were interrupted
echo "I asked you to do {IN-1} but interrupted you to say, don't do X, just Y. Here is where you were at {LOG-1}. Continue" |stepi -name .stepi/task04 
```

# CLI options

```
stepi - Minimal file-based LLM coding agent

Primary Usage:
  stepi [options] <input.md>              # Auto-generates <input>.out.md
  echo "prompt" | stepi [options]         # Pipe mode (output to stdout)
  echo "prompt" | stepi --name <name>     # Pipe mode (saves to <name>.md, generates <name>.out.md, etc.)
  
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

File naming (simplified):
  Input: file.md generates:
  - file.out.md     (main output)
  - file.chatter    (LLM communication log)
  - file.cmds       (tool commands log)
  - file.log        (execution log)
  - file.cost.csv   (cost tracking)
```
