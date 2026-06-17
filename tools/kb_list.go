package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// KBListTool lists all documents available in the KB directory.
// Useful for the LLM to orient itself when it doesn't know what to search for.
type KBListTool struct {
	KBDir  string // absolute or relative path to the KB directory
	Silent bool
}

func (t *KBListTool) Name() string { return "kb_list" }

func (t *KBListTool) Description() string {
	return "List all documents available in the local knowledge base. " +
		"Returns file names with sizes. Use this to orient yourself before searching, " +
		"or when you want to browse available topics."
}

func (t *KBListTool) Schema() anthropic.ToolInputSchemaParam {
	return anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"subdir": map[string]any{
				"type":        "string",
				"description": "Optional subdirectory within the KB to list (leave empty to list all)",
			},
		},
		Required: []string{},
	}
}

func (t *KBListTool) Execute(_ context.Context, args map[string]any) (string, error) {
	root := t.KBDir
	if subdir, ok := args["subdir"].(string); ok && subdir != "" {
		// Sandbox: make sure subdir stays within KBDir
		absKBDir, _ := filepath.Abs(t.KBDir)
		candidate := filepath.Clean(filepath.Join(absKBDir, subdir))
		if !strings.HasPrefix(candidate, absKBDir+string(filepath.Separator)) {
			return "", fmt.Errorf("access denied: %q is outside the knowledge base directory", subdir)
		}
		root = candidate
	}

	type entry struct {
		path string
		size int64
	}
	var entries []entry

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".md" && ext != ".txt" && ext != ".rst" {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		relPath, _ := filepath.Rel(t.KBDir, path)
		entries = append(entries, entry{path: relPath, size: info.Size()})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("error listing KB directory: %w", err)
	}

	if len(entries) == 0 {
		return fmt.Sprintf("Knowledge base at %s is empty (no .md/.txt/.rst files found).", t.KBDir), nil
	}

	// Sort alphabetically
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Knowledge base: %d document(s) in %s\n\n", len(entries), t.KBDir))
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("  %-50s  %s\n", e.path, formatSize(e.size)))
	}
	sb.WriteString("\nUse kb_search to find documents by content, or kb_read to read a specific file.")
	return sb.String(), nil
}

func formatSize(bytes int64) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%dB", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
	}
}
