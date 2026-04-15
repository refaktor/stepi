# Stepi

_Micromal file and step based LLM coding agent._

Based on well performing and _minimal_ coding agent Pi, but has no UI, and a single Go binary.

UnIx instead of UI, just files and some pipes, just Anthropic API for now.

## QUICK Start

```bash
$ cd my-project
$ export ANTHROPIC_API_KEY=sk-...........

$ cat > stepi_01.md
analyze the code in this project
Ctrl-c

$ stepi stepi_01.md                 # Output is written in stepi_01.out.md

$ echo "use analysis in stepi_01.out.md and try to build the project" | stepi  # Pipe mode
```

## Build

Requires Go for building. Get the source from github.

```bash
go build
```

## Install

Move the `stepi` binary to ~/.local/bin or equivalent or add to PATH.

## Why

* I wanted to see how it works on the inside (might integrate Ryelang into this)
* With inputs and outputs in UI you are basically throwing information away, files stay there
* It's easier to automate, integrate, extend the use - it's just files and pipes
* it's more persistent and more and-adhoc at the same time, always there, just a linux command
