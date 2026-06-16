// Package profile loads named stepi profiles from disk.
//
// A profile is a directory containing plain-text template files that define
// the strings stepi sends to the LLM. This allows users to customise
// behaviour (system prompt, tool descriptions, etc.) without recompiling.
//
// # Lookup order
//
// For a profile named "custom" stepi searches these locations in order,
// returning the first directory that exists:
//
//  1. .stepi/profiles/custom/   — repo-local override
//  2. ~/.config/stepi/profiles/custom/  — user-global override
//  3. <binary-dir>/profiles/custom/     — shipped alongside the binary
//
// The special name "default" always resolves to the bundled default profile.
//
// # Template files
//
// Each file in the profile directory is a plain UTF-8 text file.
// Missing files fall back to the built-in default strings so a custom
// profile only needs to contain the files it wants to override.
//
// | File               | Used for                                      |
// |--------------------|-----------------------------------------------|
// | system_prompt.md   | Core system prompt sent on every LLM call     |
// | tool_bash.md       | Description of the bash tool                  |
// | tool_read.md       | Description of the read tool                  |
// | tool_edit.md       | Description of the edit tool                  |
// | tool_write.md      | Description of the write tool                 |
// | readprev.md        | --readprev injected prefix (uses placeholders)|
// | search_prompt.md   | stepi google query wrapper (uses {QUERY})     |
package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Profile holds all template strings for a named profile.
type Profile struct {
	Name string

	// SystemPrompt is the core system prompt sent to the LLM.
	SystemPrompt string

	// Tool descriptions (human-readable, sent to LLM as tool definitions).
	ToolBash  string
	ToolRead  string
	ToolEdit  string
	ToolWrite string

	// ReadPrev is the --readprev prefix template.
	// Placeholders: {PREV_INPUT}, {PREV_OUTPUT}, {PREV_LOG}
	ReadPrev string

	// SearchPrompt is the google subcommand query wrapper.
	// Placeholder: {QUERY}
	SearchPrompt string
}

// Built-in default strings — identical to the previously hardcoded values.
// These are used when a profile file is missing or cannot be read.
const (
	defaultSystemPrompt = `You are an expert coding assistant. You help users by reading files, executing commands, editing code, and writing new files.

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

	defaultToolBash  = "Execute a bash command in the current working directory. Returns stdout and stderr. Output is truncated to last 50KB. Optionally provide a timeout in seconds."
	defaultToolRead  = "Read the contents of a file. Output is truncated to 2000 lines or 50KB (whichever is hit first). Use offset/limit for large files."
	defaultToolEdit  = "Edit a file by replacing exact text. The oldText must match exactly (including whitespace). Use this for precise, surgical edits."
	defaultToolWrite = "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories."

	// {PREV_INPUT}, {PREV_OUTPUT}, {PREV_LOG} are substituted by main.go
	defaultReadPrev = "Read what was the goal previously in {PREV_INPUT} and what you did previously by looking at files {PREV_OUTPUT} and {PREV_LOG}\n\n"

	// {QUERY} is substituted by the google subcommand
	defaultSearchPrompt = "Search for and provide current information about: {QUERY}\n\nPlease provide comprehensive, up-to-date information with specific details and context."
)

// Default returns a Profile populated entirely from built-in defaults.
func Default() *Profile {
	return &Profile{
		Name:         "default",
		SystemPrompt: defaultSystemPrompt,
		ToolBash:     defaultToolBash,
		ToolRead:     defaultToolRead,
		ToolEdit:     defaultToolEdit,
		ToolWrite:    defaultToolWrite,
		ReadPrev:     defaultReadPrev,
		SearchPrompt: defaultSearchPrompt,
	}
}

// Load returns the named profile, merging overrides from disk on top of the
// built-in defaults.  If name is "" or "default" the built-in defaults are
// returned directly (no disk lookup).
func Load(name string) (*Profile, error) {
	p := Default()
	if name == "" || name == "default" {
		return p, nil
	}
	p.Name = name

	dir, err := findProfileDir(name)
	if err != nil {
		return nil, fmt.Errorf("profile %q not found: %w", name, err)
	}

	// Load each file, falling back to the default value when absent.
	p.SystemPrompt = readFileOr(filepath.Join(dir, "system_prompt.md"), defaultSystemPrompt)
	p.ToolBash     = readFileOr(filepath.Join(dir, "tool_bash.md"),     defaultToolBash)
	p.ToolRead     = readFileOr(filepath.Join(dir, "tool_read.md"),     defaultToolRead)
	p.ToolEdit     = readFileOr(filepath.Join(dir, "tool_edit.md"),     defaultToolEdit)
	p.ToolWrite    = readFileOr(filepath.Join(dir, "tool_write.md"),    defaultToolWrite)
	p.ReadPrev     = readFileOr(filepath.Join(dir, "readprev.md"),      defaultReadPrev)
	p.SearchPrompt = readFileOr(filepath.Join(dir, "search_prompt.md"), defaultSearchPrompt)

	return p, nil
}

// ExpandReadPrev substitutes the three placeholders in the ReadPrev template.
func (p *Profile) ExpandReadPrev(prevInput, prevOutput, prevLog string) string {
	s := p.ReadPrev
	s = strings.ReplaceAll(s, "{PREV_INPUT}", prevInput)
	s = strings.ReplaceAll(s, "{PREV_OUTPUT}", prevOutput)
	s = strings.ReplaceAll(s, "{PREV_LOG}", prevLog)
	return s
}

// ExpandSearchPrompt substitutes the {QUERY} placeholder.
func (p *Profile) ExpandSearchPrompt(query string) string {
	return strings.ReplaceAll(p.SearchPrompt, "{QUERY}", query)
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// findProfileDir returns the first directory for the named profile that
// actually exists on disk.  It checks repo-local, user-global, and
// binary-adjacent locations.
func findProfileDir(name string) (string, error) {
	candidates := profileCandidates(name)
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir, nil
		}
	}
	return "", fmt.Errorf("searched: %s", strings.Join(candidates, ", "))
}

// profileCandidates returns the ordered list of directories to check for a
// named profile.
func profileCandidates(name string) []string {
	var candidates []string

	// 1. Repo-local: .stepi/profiles/<name>/
	candidates = append(candidates, filepath.Join(".stepi", "profiles", name))

	// 2. User-global: ~/.config/stepi/profiles/<name>/
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".config", "stepi", "profiles", name))
	}

	// 3. Binary-adjacent: profiles/<name>/  (relative to working dir, covers
	//    the case where stepi is run from its own source/build directory)
	candidates = append(candidates, filepath.Join("profiles", name))

	return candidates
}

// readFileOr reads the contents of path, trimming trailing whitespace.
// Returns fallback if the file does not exist or cannot be read.
func readFileOr(path, fallback string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return fallback
	}
	return strings.TrimRight(string(data), "\n\r ")
}
