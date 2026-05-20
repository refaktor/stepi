package agent

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/user/stepi/colors"
)

// InterruptManager handles interrupt signals and input injection
type InterruptManager struct {
	enabled    bool
	inputChan  chan string
	ctx        context.Context
	cancel     context.CancelFunc
	logger     Logger
	mutex      sync.RWMutex
	isWaiting  bool
	sigChan    chan os.Signal
}

// Logger interface for interrupt logging
type Logger interface {
	Info(format string, args ...interface{})
}

// NewInterruptManager creates a new interrupt manager
func NewInterruptManager(enabled bool, logger Logger) *InterruptManager {
	if !enabled {
		return &InterruptManager{enabled: false}
	}

	// Check if we have a proper TTY for interrupt functionality
	if !isatty() {
		fmt.Fprintf(os.Stderr, colors.Warning("[INTERRUPT] Warning: No TTY detected. Interrupt functionality may not work properly.\n"))
		fmt.Fprintf(os.Stderr, colors.Info("[INTERRUPT] Run in a real terminal for full interrupt support.\n"))
	}

	ctx, cancel := context.WithCancel(context.Background())
	
	// Create signal channel for Ctrl+\ (SIGQUIT)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGQUIT)
	
	manager := &InterruptManager{
		enabled:   true,
		inputChan: make(chan string, 10), // Buffer for multiple injections
		ctx:       ctx,
		cancel:    cancel,
		logger:    logger,
		sigChan:   sigChan,
	}

	// Start the interrupt listener
	go manager.listenForInterrupts()

	return manager
}

// isatty checks if we have a proper terminal
func isatty() bool {
	// Check if stdin, stdout, and stderr are all TTY
	file := os.Stdin
	fi, err := file.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// listenForInterrupts listens for Ctrl+\ and prompts for input injection
func (im *InterruptManager) listenForInterrupts() {
	if !im.enabled {
		return
	}

	for {
		select {
		case <-im.ctx.Done():
			return
		case <-im.sigChan:
			// Ctrl+\ was pressed - prompt for input
			im.handleInterrupt()
		}
	}
}

// handleInterrupt handles a Ctrl+\ interrupt by prompting for input
func (im *InterruptManager) handleInterrupt() {
	// Check if we have proper TTY before attempting to read
	if !isatty() {
		fmt.Fprintf(os.Stderr, colors.Error("[INTERRUPT] Error: No TTY available for input. Run in a real terminal.\n"))
		return
	}

	// Clear the interrupt signal and show prompt
	fmt.Fprintf(os.Stderr, colors.Info("\n[INTERRUPT] Enter message to inject (or press Enter to cancel): "))
	
	// Read input directly from stdin
	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, colors.Error("[INTERRUPT] Error reading input: %v\n"), err)
		fmt.Fprintf(os.Stderr, colors.Info("[INTERRUPT] Make sure you're running in a proper terminal.\n"))
		return
	}

	input = strings.TrimSpace(input)
	if input == "" {
		fmt.Fprintf(os.Stderr, colors.Info("[INTERRUPT] Cancelled\n"))
		return
	}

	// Handle special commands
	switch input {
	case "status":
		im.printStatus()
		return
	case "help", "?":
		im.printHelp()
		return
	}

	// Inject the message
	im.logger.Info("Interrupt: Injecting message: %s", input)
	fmt.Fprintf(os.Stderr, colors.Info("[INTERRUPT] Injecting: %s\n"), input)
	
	select {
	case im.inputChan <- input:
		fmt.Fprintf(os.Stderr, colors.Success("[INTERRUPT] Message queued successfully\n"))
	default:
		fmt.Fprintf(os.Stderr, colors.Error("[INTERRUPT] Warning: Queue full, message dropped\n"))
	}
}

// printStatus prints the current interrupt manager status
func (im *InterruptManager) printStatus() {
	im.mutex.RLock()
	defer im.mutex.RUnlock()
	
	if !im.enabled {
		fmt.Fprintf(os.Stderr, colors.Info("[INTERRUPT] Status: Disabled\n"))
		return
	}

	queueSize := len(im.inputChan)
	waitingStr := ""
	if im.isWaiting {
		waitingStr = " (waiting for LLM response)"
	}
	
	fmt.Fprintf(os.Stderr, colors.Info("[INTERRUPT] Status: Active%s | Queued: %d\n"), waitingStr, queueSize)
}

// printHelp prints available interrupt commands
func (im *InterruptManager) printHelp() {
	fmt.Fprintf(os.Stderr, colors.Info(`[INTERRUPT] Usage:
- Press Ctrl+\ during agent execution to interrupt and inject additional context
- Special commands when prompted:
  status  - Show interrupt manager status  
  help    - Show this help
  ?       - Show this help

Example workflow:
1. Run: stepi --interrupts task.md
2. While agent is working, press Ctrl+\
3. Enter: "Please use React instead of Vue for this component"
4. Press Enter to inject the message into the conversation
`))
}

// CheckForInput checks if there are any pending interrupt inputs
func (im *InterruptManager) CheckForInput() string {
	if !im.enabled {
		return ""
	}

	select {
	case input := <-im.inputChan:
		return input
	default:
		return ""
	}
}

// SetWaiting sets the waiting state for status display
func (im *InterruptManager) SetWaiting(waiting bool) {
	if !im.enabled {
		return
	}
	
	im.mutex.Lock()
	im.isWaiting = waiting
	im.mutex.Unlock()
}

// Close shuts down the interrupt manager
func (im *InterruptManager) Close() {
	if !im.enabled {
		return
	}
	
	// Stop signal notifications
	signal.Stop(im.sigChan)
	close(im.sigChan)
	
	im.cancel()
	close(im.inputChan)
}

// InterruptResult represents the result of an interrupt injection
type InterruptResult struct {
	Message     string
	Timestamp   time.Time
	Acknowledged bool
}

// InjectInput manually injects an input (for testing or programmatic use)
func (im *InterruptManager) InjectInput(message string) bool {
	if !im.enabled {
		return false
	}

	select {
	case im.inputChan <- message:
		im.logger.Info("Programmatically injected: %s", message)
		return true
	default:
		return false // Queue full
	}
}

// IsEnabled returns whether interrupts are enabled
func (im *InterruptManager) IsEnabled() bool {
	return im.enabled
}

// QueueSize returns the number of queued interrupt messages
func (im *InterruptManager) QueueSize() int {
	if !im.enabled {
		return 0
	}
	return len(im.inputChan)
}