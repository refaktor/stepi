package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/user/stepi/colors"
)

const (
	defaultTimeout   = 120 * time.Second
	defaultMaxOutput = 50 * 1024 // 50KB
)

// BashTool executes shell commands
type BashTool struct {
	Cwd         string
	Silent      bool
	// Desc overrides the default tool description when non-empty (set from a profile).
	Desc string
}

func (t *BashTool) Name() string {
	return "bash"
}

func (t *BashTool) Description() string {
	if t.Desc != "" {
		return t.Desc
	}
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

	var result strings.Builder
	
	// Show command output with colors unless silent
	if !t.Silent {
		result.WriteString(colors.Info(fmt.Sprintf("$ %s", command)))
		result.WriteString("\n")
		
		if output != "" {
			// Color stdout and stderr differently
			if stdout.Len() > 0 {
				stdoutLines := strings.Split(stdout.String(), "\n")
				for _, line := range stdoutLines {
					if line != "" {
						result.WriteString(colors.CommandOutput(line) + "\n")
					}
				}
			}
			
			if stderr.Len() > 0 {
				stderrLines := strings.Split(stderr.String(), "\n")
				for _, line := range stderrLines {
					if line != "" {
						result.WriteString(colors.CommandError(line) + "\n")
					}
				}
			}
		}
	} else {
		// Silent mode: just return the raw output
		result.WriteString(output)
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.WriteString(fmt.Sprintf("\nCommand timed out after %v", timeout))
			return result.String(), nil
		}
		result.WriteString(fmt.Sprintf("\nCommand exited with error: %v", err))
		return result.String(), nil
	}

	return result.String(), nil
}
