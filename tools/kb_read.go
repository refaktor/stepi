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
	kbReadMaxLines = 2000
	kbReadMaxBytes = 50 * 1024 // 50 KB
)

// KBReadTool reads a single document from the KB directory in full.
// It constrains reads to within KBDir so the LLM cannot escape the sandbox.
type KBReadTool struct {
	KBDir  string // absolute or relative path to the KB directory
	Silent bool
}

func (t *KBReadTool) Name() string { return "kb_read" }

func (t *KBReadTool) Description() string {
	return "Read a document from the local knowledge base in full. " +
		"Provide the file name (relative to the KB root) as returned by kb_search or kb_list. " +
		"Output is truncated to 2000 lines or 50 KB."
}

func (t *KBReadTool) Schema() anthropic.ToolInputSchemaParam {
	return anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"file": map[string]any{
				"type":        "string",
				"description": "File name (relative to KB root) to read, e.g. \"topic-auth.md\"",
			},
			"offset": map[string]any{
				"type":        "number",
				"description": "Line number to start reading from (1-indexed, optional)",
			},
			"limit": map[string]any{
				"type":        "number",
				"description": "Maximum number of lines to read (optional)",
			},
		},
		Required: []string{"file"},
	}
}

func (t *KBReadTool) Execute(_ context.Context, args map[string]any) (string, error) {
	fileArg, ok := args["file"].(string)
	if !ok || fileArg == "" {
		return "", fmt.Errorf("file must be a non-empty string")
	}

	// Resolve and sandbox to KBDir
	absKBDir, err := filepath.Abs(t.KBDir)
	if err != nil {
		return "", fmt.Errorf("cannot resolve KB directory: %w", err)
	}

	// Join and clean to prevent path traversal
	candidate := filepath.Clean(filepath.Join(absKBDir, fileArg))
	if !strings.HasPrefix(candidate, absKBDir+string(filepath.Separator)) && candidate != absKBDir {
		return "", fmt.Errorf("access denied: %q is outside the knowledge base directory", fileArg)
	}

	content, err := os.ReadFile(candidate)
	if err != nil {
		return "", fmt.Errorf("failed to read %q: %w", fileArg, err)
	}

	// Parse offset and limit
	offset := 1
	if v, ok := args["offset"].(float64); ok && v > 0 {
		offset = int(v)
	}
	limit := kbReadMaxLines
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

	lines := strings.Split(string(content), "\n")
	if offset > 1 {
		if offset > len(lines) {
			return "", fmt.Errorf("offset %d exceeds file length %d lines", offset, len(lines))
		}
		lines = lines[offset-1:]
	}
	if len(lines) > limit {
		lines = lines[:limit]
	}

	result := strings.Join(lines, "\n")
	if len(result) > kbReadMaxBytes {
		result = result[:kbReadMaxBytes] + "\n…[truncated]"
	}

	return result, nil
}
