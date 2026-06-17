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
	kbSearchContextChars = 200 // characters of context around each match
	kbSearchMaxResults   = 50  // maximum number of match snippets returned
)

// KBSearchTool searches for a query string across all documents in the KB directory.
// It performs a case-insensitive substring search and returns a list of matching
// files with short context snippets so the LLM can decide which to read in full.
type KBSearchTool struct {
	KBDir  string // absolute or relative path to the KB directory
	Silent bool
}

func (t *KBSearchTool) Name() string { return "kb_search" }

func (t *KBSearchTool) Description() string {
	return "Search the local knowledge base for documents matching a query. " +
		"Returns file names and context snippets for each match. " +
		"Use this first to find relevant documents, then use kb_read to read them in full."
}

func (t *KBSearchTool) Schema() anthropic.ToolInputSchemaParam {
	return anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search term or phrase to look for in the knowledge base (case-insensitive)",
			},
		},
		Required: []string{"query"},
	}
}

func (t *KBSearchTool) Execute(_ context.Context, args map[string]any) (string, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return "", fmt.Errorf("query must be a non-empty string")
	}

	queryLower := strings.ToLower(query)

	type match struct {
		file    string
		snippet string
	}
	var matches []match

	err := filepath.WalkDir(t.KBDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable paths
		}
		if d.IsDir() {
			return nil
		}
		// Only search markdown and text files
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".md" && ext != ".txt" && ext != ".rst" {
			return nil
		}

		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil // skip unreadable files
		}

		contentStr := string(content)
		lower := strings.ToLower(contentStr)

		// Find all occurrences
		offset := 0
		found := 0
		for found < 3 { // at most 3 snippets per file
			idx := strings.Index(lower[offset:], queryLower)
			if idx < 0 {
				break
			}
			abs := offset + idx

			// Extract context window
			start := abs - kbSearchContextChars/2
			if start < 0 {
				start = 0
			}
			end := abs + len(queryLower) + kbSearchContextChars/2
			if end > len(contentStr) {
				end = len(contentStr)
			}
			snippet := strings.TrimSpace(contentStr[start:end])
			// Replace newlines with spaces for compact display
			snippet = strings.ReplaceAll(snippet, "\n", " ")
			if len(snippet) > kbSearchContextChars+50 {
				snippet = snippet[:kbSearchContextChars+50] + "…"
			}

			// Use relative path from KBDir for display
			relPath, _ := filepath.Rel(t.KBDir, path)
			matches = append(matches, match{file: relPath, snippet: snippet})

			offset = abs + len(queryLower)
			found++
			if len(matches) >= kbSearchMaxResults {
				return filepath.SkipAll
			}
		}
		return nil
	})

	if err != nil {
		return "", fmt.Errorf("error walking KB directory: %w", err)
	}

	if len(matches) == 0 {
		return fmt.Sprintf("No documents found matching %q in %s", query, t.KBDir), nil
	}

	// Build output: group by file
	type fileResult struct {
		file     string
		snippets []string
	}
	fileMap := make(map[string]*fileResult)
	var fileOrder []string
	for _, m := range matches {
		if _, exists := fileMap[m.file]; !exists {
			fileMap[m.file] = &fileResult{file: m.file}
			fileOrder = append(fileOrder, m.file)
		}
		fileMap[m.file].snippets = append(fileMap[m.file].snippets, m.snippet)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d match(es) across %d file(s) for query %q:\n\n", len(matches), len(fileOrder), query))
	for _, fname := range fileOrder {
		fr := fileMap[fname]
		sb.WriteString(fmt.Sprintf("## %s\n", fr.file))
		for i, snip := range fr.snippets {
			sb.WriteString(fmt.Sprintf("  [%d] …%s…\n", i+1, snip))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("Use kb_read with the file name to read a document in full.")
	return sb.String(), nil
}
