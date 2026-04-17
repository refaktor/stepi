package colors

import (
	"os"
)

// ANSI color codes
const (
	Reset   = "\033[0m"
	Bold    = "\033[1m"
	Dim     = "\033[2m"
	
	// Text colors
	Red     = "\033[31m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Magenta = "\033[35m"
	Cyan    = "\033[36m"
	White   = "\033[37m"
	Gray    = "\033[90m"
	
	// Bright colors
	BrightRed     = "\033[91m"
	BrightGreen   = "\033[92m"
	BrightYellow  = "\033[93m"
	BrightBlue    = "\033[94m"
	BrightMagenta = "\033[95m"
	BrightCyan    = "\033[96m"
	BrightWhite   = "\033[97m"
)

var colorEnabled = true

// init checks if colors should be disabled
func init() {
	// Disable colors if NO_COLOR is set or if not a terminal
	if os.Getenv("NO_COLOR") != "" || !isTerminal() {
		colorEnabled = false
	}
}

// isTerminal checks if stderr is a terminal
func isTerminal() bool {
	fileInfo, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}

// colorize applies color to text if colors are enabled
func colorize(color, text string) string {
	if !colorEnabled || text == "" {
		return text
	}
	return color + text + Reset
}

// LLMText colors LLM response text
func LLMText(text string) string {
	return colorize(BrightWhite, text)
}

// ToolCall colors tool execution lines
func ToolCall(text string) string {
	return colorize(BrightCyan+Bold, text)
}

// Cost colors cost information
func Cost(text string) string {
	return colorize(BrightYellow, text)
}

// Info colors informational text
func Info(text string) string {
	return colorize(Gray, text)
}

// Error colors error text
func Error(text string) string {
	return colorize(BrightRed, text)
}

// Success colors success text
func Success(text string) string {
	return colorize(BrightGreen, text)
}

// Warning colors warning text
func Warning(text string) string {
	return colorize(Yellow, text)
}

// Header colors header text
func Header(text string) string {
	return colorize(BrightBlue+Bold, text)
}

// Disable turns off color output
func Disable() {
	colorEnabled = false
}

// Enable turns on color output
func Enable() {
	colorEnabled = true
}

// IsEnabled returns whether colors are enabled
func IsEnabled() bool {
	return colorEnabled
}