package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

const (
	defaultMaxLines = 2000
	defaultMaxBytes = 50 * 1024 // 50KB
)

// ReadTool reads file contents
type ReadTool struct {
	Cwd string
}

func (t *ReadTool) Name() string {
	return "read"
}

func (t *ReadTool) Description() string {
	return "Read the contents of a file. Output is truncated to 2000 lines or 50KB (whichever is hit first). Use offset/limit for large files."
}

func (t *ReadTool) Schema() anthropic.ToolInputSchemaParam {
	return anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to read (relative or absolute)",
			},
			"offset": map[string]any{
				"type":        "number",
				"description": "Line number to start reading from (1-indexed)",
			},
			"limit": map[string]any{
				"type":        "number",
				"description": "Maximum number of lines to read",
			},
		},
		Required: []string{"path"},
	}
}

func (t *ReadTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	pathArg, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path must be a string")
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

	// Parse offset and limit
	offset := 1
	if offsetArg, ok := args["offset"].(float64); ok {
		offset = int(offsetArg)
	}
	limit := defaultMaxLines
	if limitArg, ok := args["limit"].(float64); ok {
		limit = int(limitArg)
	}

	// Split into lines
	lines := strings.Split(string(content), "\n")

	// Apply offset (1-indexed)
	if offset > 1 {
		if offset > len(lines) {
			return "", fmt.Errorf("offset %d exceeds file length %d", offset, len(lines))
		}
		lines = lines[offset-1:]
	}

	// Apply limit
	if len(lines) > limit {
		lines = lines[:limit]
	}

	result := strings.Join(lines, "\n")

	// Truncate by bytes if needed
	if len(result) > defaultMaxBytes {
		result = result[:defaultMaxBytes] + "\n...[truncated]"
	}

	return result, nil
}
