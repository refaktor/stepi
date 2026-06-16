package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/user/stepi/colors"
)

// WriteTool writes content to a file
type WriteTool struct {
	Cwd    string
	Silent bool
	// Desc overrides the default tool description when non-empty (set from a profile).
	Desc string
}

func (t *WriteTool) Name() string {
	return "write"
}

func (t *WriteTool) Description() string {
	if t.Desc != "" {
		return t.Desc
	}
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

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), pathArg))
	
	// Show file content preview unless silent
	if !t.Silent {
		result.WriteString("\n")
		result.WriteString(colors.EditFile(fmt.Sprintf("=== %s ===", pathArg)))
		
		// Show first few lines of content
		lines := strings.Split(content, "\n")
		maxLines := 10
		if len(lines) > maxLines {
			result.WriteString("\n" + colors.Info(fmt.Sprintf("Showing first %d lines of %d:", maxLines, len(lines))))
			for i := 0; i < maxLines; i++ {
				result.WriteString("\n" + colors.EditAdd(fmt.Sprintf("+ %s", lines[i])))
			}
			result.WriteString("\n" + colors.Info(fmt.Sprintf("... and %d more lines", len(lines)-maxLines)))
		} else {
			result.WriteString("\n" + colors.Info("Content:"))
			for _, line := range lines {
				result.WriteString("\n" + colors.EditAdd(fmt.Sprintf("+ %s", line)))
			}
		}
	}

	return result.String(), nil
}
