// Package logging provides structured logging for the agent
package logging

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Logger handles multiple log files
type Logger struct {
	baseName string
	logFile  *os.File // .log - informational text
	cmdFile  *os.File // .cmds - tool commands  
	chatFile *os.File // .chatter - raw LLM communication
	mu       sync.Mutex
}

// NewLogger creates a new logger with the given base filename
// Creates files: baseName.log, baseName.cmds, baseName.chatter
func NewLogger(baseName string) (*Logger, error) {
	if baseName == "" {
		return &Logger{}, nil // No-op logger
	}

	l := &Logger{baseName: baseName}
	
	// Open log files
	var err error
	l.logFile, err = os.OpenFile(baseName+".log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}

	l.cmdFile, err = os.OpenFile(baseName+".cmds", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		l.logFile.Close()
		return nil, fmt.Errorf("failed to create commands file: %w", err)
	}

	l.chatFile, err = os.OpenFile(baseName+".chatter", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		l.logFile.Close()
		l.cmdFile.Close()
		return nil, fmt.Errorf("failed to create chatter file: %w", err)
	}

	// Write headers
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	
	fmt.Fprintf(l.logFile, "# Agent Log - %s\n# Started at %s\n\n", baseName, timestamp)
	fmt.Fprintf(l.cmdFile, "# Tool Commands Log - %s\n# Started at %s\n\n", baseName, timestamp)
	fmt.Fprintf(l.chatFile, "# LLM Communication Log - %s\n# Started at %s\n\n", baseName, timestamp)

	return l, nil
}

// Info logs informational text (what user sees in stderr)
func (l *Logger) Info(format string, args ...interface{}) {
	if l.logFile == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	
	timestamp := time.Now().Format("15:04:05")
	fmt.Fprintf(l.logFile, "[%s] ", timestamp)
	fmt.Fprintf(l.logFile, format, args...)
	if format[len(format)-1] != '\n' {
		fmt.Fprint(l.logFile, "\n")
	}
}

// StreamText logs text as it's streamed from the AI (the descriptive text)
func (l *Logger) StreamText(text string) {
	if l.logFile == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	
	// Write the text directly without timestamp for continuous streaming
	fmt.Fprint(l.logFile, text)
}

// Command logs a tool command execution
func (l *Logger) Command(toolName string, args map[string]interface{}, result string, err error) {
	if l.cmdFile == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	
	timestamp := time.Now().Format("15:04:05")
	fmt.Fprintf(l.cmdFile, "[%s] TOOL: %s\n", timestamp, toolName)
	
	// Log arguments
	fmt.Fprint(l.cmdFile, "ARGS:\n")
	for k, v := range args {
		fmt.Fprintf(l.cmdFile, "  %s: %v\n", k, v)
	}
	
	// Log result
	if err != nil {
		fmt.Fprintf(l.cmdFile, "ERROR: %v\n", err)
	} else {
		fmt.Fprint(l.cmdFile, "RESULT:\n")
		// Truncate very long results
		if len(result) > 1000 {
			fmt.Fprintf(l.cmdFile, "%s\n...[truncated %d chars]\n", result[:1000], len(result)-1000)
		} else {
			fmt.Fprintf(l.cmdFile, "%s\n", result)
		}
	}
	fmt.Fprint(l.cmdFile, "---\n\n")
}

// Chat logs raw LLM communication  
func (l *Logger) Chat(direction, content string) {
	if l.chatFile == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	
	timestamp := time.Now().Format("15:04:05")
	fmt.Fprintf(l.chatFile, "[%s] %s:\n", timestamp, direction)
	fmt.Fprintf(l.chatFile, "%s\n", content)
	fmt.Fprint(l.chatFile, "---\n\n")
}

// ChatRequest logs an LLM request
func (l *Logger) ChatRequest(model string, messages int, systemPrompt string) {
	if l.chatFile == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	
	timestamp := time.Now().Format("15:04:05")
	fmt.Fprintf(l.chatFile, "[%s] REQUEST TO LLM:\n", timestamp)
	fmt.Fprintf(l.chatFile, "Model: %s\n", model)
	fmt.Fprintf(l.chatFile, "Messages: %d\n", messages)
	if systemPrompt != "" {
		fmt.Fprintf(l.chatFile, "System Prompt:\n%s\n", systemPrompt)
	}
	fmt.Fprint(l.chatFile, "---\n\n")
}

// ChatResponse logs an LLM response
func (l *Logger) ChatResponse(content string, stopReason string, usage map[string]int64) {
	if l.chatFile == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	
	timestamp := time.Now().Format("15:04:05")
	fmt.Fprintf(l.chatFile, "[%s] RESPONSE FROM LLM:\n", timestamp)
	fmt.Fprintf(l.chatFile, "Stop Reason: %s\n", stopReason)
	if len(usage) > 0 {
		fmt.Fprint(l.chatFile, "Usage: ")
		for k, v := range usage {
			fmt.Fprintf(l.chatFile, "%s=%d ", k, v)
		}
		fmt.Fprint(l.chatFile, "\n")
	}
	fmt.Fprintf(l.chatFile, "Content:\n%s\n", content)
	fmt.Fprint(l.chatFile, "---\n\n")
}

// Close closes all log files
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	
	var errs []error
	if l.logFile != nil {
		if err := l.logFile.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if l.cmdFile != nil {
		if err := l.cmdFile.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if l.chatFile != nil {
		if err := l.chatFile.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	
	if len(errs) > 0 {
		return fmt.Errorf("errors closing log files: %v", errs)
	}
	return nil
}

// IsEnabled returns true if logging is enabled
func (l *Logger) IsEnabled() bool {
	return l.baseName != ""
}