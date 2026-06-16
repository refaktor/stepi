package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

const basePrompt = `You are an expert coding assistant. You help users by reading files, executing commands, editing code, and writing new files.

Available tools:
- read: Read file contents
- bash: Execute bash commands
- edit: Make surgical edits to files (find exact text and replace)
- write: Create or overwrite files

Guidelines:
- Use bash for file operations like ls, grep, find
- Use read to examine files before editing
- Use edit for precise changes (old text must match exactly)
- Use write only for new files or complete rewrites
- Be concise in your responses
- Show file paths clearly when working with files`

// Build constructs the system prompt using the built-in base prompt,
// loading AGENTS.md if present. Use BuildWithPrompt to supply a custom
// system prompt from a profile.
func Build(cwd string) string {
	return BuildWithPrompt(cwd, basePrompt)
}

// BuildWithPrompt constructs the system prompt from the given systemPrompt
// string, then appends date/time, working directory, and AGENTS.md (if
// present). Pass profile.Profile.SystemPrompt here when using a custom
// profile; pass the empty string to use the built-in default.
func BuildWithPrompt(cwd, systemPrompt string) string {
	if systemPrompt == "" {
		systemPrompt = basePrompt
	}

	var sb strings.Builder

	sb.WriteString(systemPrompt)
	sb.WriteString("\n\n")

	// Add date/time
	sb.WriteString("Current date and time: ")
	sb.WriteString(time.Now().Format("Monday, January 2, 2006 at 3:04:05 PM MST"))
	sb.WriteString("\n")

	// Add working directory
	sb.WriteString("Current working directory: ")
	sb.WriteString(cwd)
	sb.WriteString("\n")

	// Try to load AGENTS.md
	agentsPath := filepath.Join(cwd, "AGENTS.md")
	if content, err := os.ReadFile(agentsPath); err == nil {
		sb.WriteString("\n# Project Context\n\n")
		sb.WriteString("Project-specific instructions and guidelines:\n\n")
		sb.WriteString("## ")
		sb.WriteString(agentsPath)
		sb.WriteString("\n\n")
		sb.WriteString(string(content))
	}

	return sb.String()
}
