package logging

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"time"
)

// CostLogger handles logging costs to CSV file
type CostLogger struct {
	csvFile    *os.File
	csvWriter  *csv.Writer
	stepNumber int
}

// NewCostLogger creates a new cost logger that appends to filename.cost.csv
func NewCostLogger(baseFilename string) (*CostLogger, error) {
	// Extract base name for the cost file
	var filename string
	if baseFilename != "" && baseFilename != "<stdout>" && baseFilename != "<stdin>" {
		// Use baseFilename directly as the base name (it should already be simplified)
		filename = baseFilename + ".cost.csv"
	} else {
		// Default filename if no base provided
		filename = "stepi_default.cost.csv"
	}
	
	// Check if file exists to determine if we need headers
	needsHeader := false
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		needsHeader = true
	}
	
	// Open file for append/create
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open cost log file: %w", err)
	}
	
	writer := csv.NewWriter(file)
	
	cl := &CostLogger{
		csvFile:   file,
		csvWriter: writer,
	}
	
	// Write header if new file
	if needsHeader {
		err := cl.csvWriter.Write([]string{"timestamp", "step", "cost", "type", "description", "input_tokens", "output_tokens", "model"})
		if err != nil {
			file.Close()
			return nil, fmt.Errorf("failed to write CSV header: %w", err)
		}
		cl.csvWriter.Flush()
	}
	
	return cl, nil
}

// LogStep logs a step with cost information
func (cl *CostLogger) LogStep(cost float64, stepType, description string, inputTokens, outputTokens int64, model string) error {
	if cl.csvWriter == nil {
		return nil // No-op if logger not initialized
	}
	
	cl.stepNumber++
	
	record := []string{
		time.Now().Format("2006-01-02 15:04:05"),
		strconv.Itoa(cl.stepNumber),
		fmt.Sprintf("%.6f", cost),
		stepType,
		description,
		strconv.FormatInt(inputTokens, 10),
		strconv.FormatInt(outputTokens, 10),
		model,
	}
	
	err := cl.csvWriter.Write(record)
	if err != nil {
		return fmt.Errorf("failed to write cost record: %w", err)
	}
	
	cl.csvWriter.Flush()
	return nil
}

// LogToolCall logs a tool execution step
func (cl *CostLogger) LogToolCall(toolName string, args map[string]any, success bool) error {
	if cl.csvWriter == nil {
		return nil
	}
	
	// Format description based on tool type
	var description string
	switch toolName {
	case "bash":
		if cmd, ok := args["command"].(string); ok {
			description = fmt.Sprintf("bash: %s", cmd)
			if len(description) > 100 {
				description = description[:97] + "..."
			}
		} else {
			description = "bash: (unknown command)"
		}
	case "read":
		if path, ok := args["path"].(string); ok {
			description = fmt.Sprintf("read: %s", path)
		} else {
			description = "read: (unknown path)"
		}
	case "write":
		if path, ok := args["path"].(string); ok {
			description = fmt.Sprintf("write: %s", path)
		} else {
			description = "write: (unknown path)"
		}
	case "edit":
		if path, ok := args["path"].(string); ok {
			description = fmt.Sprintf("edit: %s", path)
		} else {
			description = "edit: (unknown path)"
		}
	default:
		description = fmt.Sprintf("%s: (tool call)", toolName)
	}
	
	if !success {
		description += " [FAILED]"
	}
	
	cl.stepNumber++
	
	record := []string{
		time.Now().Format("2006-01-02 15:04:05"),
		strconv.Itoa(cl.stepNumber),
		"0.000000", // Tool calls don't have direct cost
		"tool",
		description,
		"0",
		"0", 
		"",
	}
	
	err := cl.csvWriter.Write(record)
	if err != nil {
		return fmt.Errorf("failed to write tool cost record: %w", err)
	}
	
	cl.csvWriter.Flush()
	return nil
}

// Close closes the cost logger
func (cl *CostLogger) Close() error {
	if cl.csvWriter != nil {
		cl.csvWriter.Flush()
	}
	if cl.csvFile != nil {
		return cl.csvFile.Close()
	}
	return nil
}