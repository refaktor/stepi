package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// EditTool edits a file by replacing exact text
type EditTool struct {
	Cwd string
}

func (t *EditTool) Name() string {
	return "edit"
}

func (t *EditTool) Description() string {
	return "Edit a file by replacing exact text. The oldText must match exactly (including whitespace). Use this for precise, surgical edits."
}

func (t *EditTool) Schema() anthropic.ToolInputSchemaParam {
	return anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to edit (relative or absolute)",
			},
			"oldText": map[string]any{
				"type":        "string",
				"description": "Exact text to find and replace (must match exactly)",
			},
			"newText": map[string]any{
				"type":        "string",
				"description": "New text to replace the old text with",
			},
		},
		Required: []string{"path", "oldText", "newText"},
	}
}

func (t *EditTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	pathArg, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path must be a string")
	}
	oldText, ok := args["oldText"].(string)
	if !ok {
		return "", fmt.Errorf("oldText must be a string")
	}
	newText, ok := args["newText"].(string)
	if !ok {
		return "", fmt.Errorf("newText must be a string")
	}

	// Resolve path
	path := pathArg
	if !filepath.IsAbs(path) {
		path = filepath.Join(t.Cwd, path)
	}

	// Read file
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	// Check if oldText exists
	if !strings.Contains(string(content), oldText) {
		return "", fmt.Errorf("oldText not found in file")
	}

	// Count occurrences
	count := strings.Count(string(content), oldText)
	if count > 1 {
		return "", fmt.Errorf("oldText found %d times, must be unique", count)
	}

	// Replace
	newContent := strings.Replace(string(content), oldText, newText, 1)

	// Write back
	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	return fmt.Sprintf("Successfully replaced text in %s", pathArg), nil
}
