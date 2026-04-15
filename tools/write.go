package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/anthropics/anthropic-sdk-go"
)

// WriteTool writes content to a file
type WriteTool struct {
	Cwd string
}

func (t *WriteTool) Name() string {
	return "write"
}

func (t *WriteTool) Description() string {
	return "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories."
}

func (t *WriteTool) Schema() anthropic.ToolInputSchemaParam {
	return anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to write (relative or absolute)",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Content to write to the file",
			},
		},
		Required: []string{"path", "content"},
	}
}

func (t *WriteTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	pathArg, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path must be a string")
	}
	content, ok := args["content"].(string)
	if !ok {
		return "", fmt.Errorf("content must be a string")
	}

	// Resolve path
	path := pathArg
	if !filepath.IsAbs(path) {
		path = filepath.Join(t.Cwd, path)
	}

	// Create parent directories
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	// Write file
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	return fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), pathArg), nil
}
