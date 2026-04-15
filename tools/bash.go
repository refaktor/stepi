package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

const (
	defaultTimeout   = 120 * time.Second
	defaultMaxOutput = 50 * 1024 // 50KB
)

// BashTool executes shell commands
type BashTool struct {
	Cwd string
}

func (t *BashTool) Name() string {
	return "bash"
}

func (t *BashTool) Description() string {
	return "Execute a bash command in the current working directory. Returns stdout and stderr. Output is truncated to last 50KB. Optionally provide a timeout in seconds."
}

func (t *BashTool) Schema() anthropic.ToolInputSchemaParam {
	return anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "Bash command to execute",
			},
			"timeout": map[string]any{
				"type":        "number",
				"description": "Timeout in seconds (optional, default: 120)",
			},
		},
		Required: []string{"command"},
	}
}

func (t *BashTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	command, ok := args["command"].(string)
	if !ok {
		return "", fmt.Errorf("command must be a string")
	}

	timeout := defaultTimeout
	if timeoutArg, ok := args["timeout"].(float64); ok {
		timeout = time.Duration(timeoutArg) * time.Second
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = t.Cwd
	cmd.Env = append(cmd.Environ(),
		"PAGER=cat",
		"GIT_PAGER=cat",
		"NO_COLOR=1",
		"TERM=dumb",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// Combine output
	output := stdout.String() + stderr.String()

	// Truncate if needed
	if len(output) > defaultMaxOutput {
		output = "...[truncated]\n" + output[len(output)-defaultMaxOutput:]
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return output + fmt.Sprintf("\n\nCommand timed out after %v", timeout), nil
		}
		return output + fmt.Sprintf("\n\nCommand exited with error: %v", err), nil
	}

	return output, nil
}
