package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// printQRToTerminal prints a QR code to the terminal using external tools
func printQRToTerminal(url string) error {
	// Try the custom bash script first (if available)
	if err := tryQRScript(url); err == nil {
		return nil
	}

	// Try different QR code generation tools
	tools := []string{"qrencode", "qr"}
	
	for _, tool := range tools {
		if err := tryQRTool(tool, url); err == nil {
			return nil
		}
	}
	
	// If no tools available, print ASCII QR as fallback
	return printASCIIQR(url)
}

// tryQRScript attempts to use the custom bash script
func tryQRScript(url string) error {
	// Look for the script in the same directory
	scriptPath := filepath.Join(filepath.Dir(os.Args[0]), "qr-tools.sh")
	
	// Check if script exists
	if _, err := os.Stat(scriptPath); err != nil {
		return fmt.Errorf("qr-tools.sh not found: %v", err)
	}
	
	// Make sure it's executable
	if err := os.Chmod(scriptPath, 0755); err != nil {
		return fmt.Errorf("failed to make script executable: %v", err)
	}
	
	// Run the script
	cmd := exec.Command("bash", scriptPath, "generate", url)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	
	return cmd.Run()
}

// tryQRTool attempts to use a specific QR code tool
func tryQRTool(tool, url string) error {
	var cmd *exec.Cmd
	
	switch tool {
	case "qrencode":
		// qrencode -t ANSI "$URL"
		cmd = exec.Command("qrencode", "-t", "ANSI", url)
	case "qr":
		// qr -t "$URL"
		cmd = exec.Command("qr", "-t", url)
	default:
		return fmt.Errorf("unknown tool: %s", tool)
	}
	
	// Check if tool exists
	if _, err := exec.LookPath(tool); err != nil {
		return fmt.Errorf("tool %s not found: %v", tool, err)
	}
	
	// Run the command
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	
	return cmd.Run()
}

// printASCIIQR prints a simple ASCII representation as fallback
func printASCIIQR(url string) error {
	fmt.Println("┌─────────────────────────────────────────────┐")
	fmt.Println("│         QR Code Tools Not Available        │")
	fmt.Println("│                                             │")
	fmt.Println("│  📱 To display QR codes in terminal:       │")
	fmt.Println("│                                             │")
	fmt.Println("│  🔧 Install qrencode:                      │")
	fmt.Println("│     • Ubuntu/Debian:                       │")
	fmt.Println("│       sudo apt install qrencode            │")
	fmt.Println("│     • macOS:                               │")
	fmt.Println("│       brew install qrencode                │")
	fmt.Println("│     • CentOS/RHEL:                         │")
	fmt.Println("│       sudo yum install qrencode            │")
	fmt.Println("│     • Fedora:                              │")
	fmt.Println("│       sudo dnf install qrencode            │")
	fmt.Println("│     • Arch Linux:                          │")
	fmt.Println("│       sudo pacman -S qrencode              │")
	fmt.Println("│                                             │")
	fmt.Println("│  🚀 Or use the enhanced bash script:       │")
	fmt.Println("│     ./qr-tools.sh install                  │")
	fmt.Println("│                                             │")
	fmt.Printf("│  🌐 URL: %-35s │\n", truncateString(url, 35))
	fmt.Println("│                                             │")
	fmt.Println("│  💡 For now, copy the URL above to         │")
	fmt.Println("│     share or create QR code online         │")
	fmt.Println("└─────────────────────────────────────────────┘")
	return nil
}

// truncateString truncates a string to the specified length
func truncateString(s string, length int) string {
	if len(s) <= length {
		return s
	}
	return s[:length-3] + "..."
}