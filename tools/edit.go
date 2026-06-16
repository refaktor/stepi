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

// min returns the smaller of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// max returns the larger of two integers
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// EditTool edits a file by replacing exact text
type EditTool struct {
	Cwd    string
	Silent bool
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

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Successfully replaced text in %s", pathArg))
	
	// Show detailed edit changes unless silent
	if !t.Silent {
		result.WriteString("\n")
		result.WriteString(colors.EditFile(fmt.Sprintf("=== %s ===", pathArg)))
		result.WriteString("\n")
		result.WriteString(colors.EditFrom("- " + strings.ReplaceAll(oldText, "\n", "\\n")))
		result.WriteString("\n")
		result.WriteString(colors.EditTo("+ " + strings.ReplaceAll(newText, "\n", "\\n")))
		
		// Show context lines if the change is small
		if len(oldText) <= 200 && len(newText) <= 200 {
			lines := strings.Split(string(content), "\n")
			for i, line := range lines {
				if strings.Contains(line, oldText) {
					start := max(0, i-2)
					end := min(len(lines), i+3)
					result.WriteString("\n\n" + colors.Info("Context:"))
					for j := start; j < end; j++ {
						prefix := fmt.Sprintf("%3d: ", j+1)
						if j == i {
							// This is the changed line
							changedLine := strings.Replace(lines[j], oldText, newText, 1)
							result.WriteString("\n" + colors.Info(prefix) + colors.EditTo(changedLine))
						} else {
							result.WriteString("\n" + colors.Info(prefix) + colors.Info(lines[j]))
						}
					}
					break
				}
			}
		}
	}

	return result.String(), nil
}
