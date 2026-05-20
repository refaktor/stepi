package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/skip2/go-qrcode"
	"golang.ngrok.com/ngrok"
	"golang.ngrok.com/ngrok/config"
)

type Server struct {
	jobs        map[string]*Job
	jobsMux     sync.RWMutex
	upgrader    websocket.Upgrader
	workingDir  string
	stepiPairs  map[string]*StepiPair
	pairsMux    sync.RWMutex
	password    string
	authenticated map[string]bool
	authMux     sync.RWMutex
	ngrokURL    string
	qrCode      string
}

type StepiPair struct {
	Name       string    `json:"name"`
	InputFile  string    `json:"input_file"`
	OutputFile string    `json:"output_file"`
	HasInput   bool      `json:"has_input"`
	HasOutput  bool      `json:"has_output"`
	LastRun    time.Time `json:"last_run,omitempty"`
	Status     string    `json:"status"` // idle, running, completed, error
}

type Job struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"` // pending, running, completed, failed
	CreatedAt time.Time `json:"created_at"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	InputFile string    `json:"input_file"`
	OutputFile string   `json:"output_file,omitempty"`
	Error     string    `json:"error,omitempty"`
	PairName  string    `json:"pair_name,omitempty"`
	cmd       *exec.Cmd
	ctx       context.Context
	cancel    context.CancelFunc
	stdout    []string
	stdoutMux sync.RWMutex
}

func (j *Job) AddOutput(line string) {
	j.stdoutMux.Lock()
	defer j.stdoutMux.Unlock()
	j.stdout = append(j.stdout, line)
}

func (j *Job) GetOutput() []string {
	j.stdoutMux.RLock()
	defer j.stdoutMux.RUnlock()
	result := make([]string, len(j.stdout))
	copy(result, j.stdout)
	return result
}

func NewServer(workingDir, password string) *Server {
	return &Server{
		jobs:       make(map[string]*Job),
		workingDir: workingDir,
		stepiPairs: make(map[string]*StepiPair),
		password:   password,
		authenticated: make(map[string]bool),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow connections from any origin
			},
		},
	}
}

// Security middleware
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.password == "" {
			next(w, r)
			return
		}

		// Check for session cookie
		cookie, err := r.Cookie("stepi_session")
		if err == nil {
			s.authMux.RLock()
			authenticated := s.authenticated[cookie.Value]
			s.authMux.RUnlock()
			if authenticated {
				next(w, r)
				return
			}
		}

		// Check for login form submission
		if r.Method == "POST" && r.URL.Path == "/login" {
			password := r.FormValue("password")
			if subtle.ConstantTimeCompare([]byte(password), []byte(s.password)) == 1 {
				sessionID := fmt.Sprintf("session-%d", time.Now().Unix())
				s.authMux.Lock()
				s.authenticated[sessionID] = true
				s.authMux.Unlock()
				
				http.SetCookie(w, &http.Cookie{
					Name:     "stepi_session",
					Value:    sessionID,
					Path:     "/",
					HttpOnly: true,
					MaxAge:   86400, // 24 hours
				})
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
		}

		// Show login form
		s.handleLogin(w, r)
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	html := `<!DOCTYPE html>
<html>
<head>
    <title>Stepi Server - Login</title>
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <style>
        body { font-family: Arial, sans-serif; display: flex; justify-content: center; align-items: center; height: 100vh; margin: 0; background: #f5f5f5; }
        .login-form { background: white; padding: 40px; border-radius: 8px; box-shadow: 0 2px 10px rgba(0,0,0,0.1); max-width: 300px; width: 100%; }
        input { width: 100%; padding: 12px; margin: 10px 0; border: 1px solid #ddd; border-radius: 4px; box-sizing: border-box; }
        button { width: 100%; padding: 12px; background: #007bff; color: white; border: none; border-radius: 4px; cursor: pointer; }
        button:hover { background: #0056b3; }
        h2 { text-align: center; margin-bottom: 30px; }
    </style>
</head>
<body>
    <div class="login-form">
        <h2>Stepi Server</h2>
        <form method="POST" action="/login">
            <input type="password" name="password" placeholder="Enter password" required>
            <button type="submit">Login</button>
        </form>
    </div>
</body>
</html>`
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

func (s *Server) scanStepiFiles() error {
	s.pairsMux.Lock()
	defer s.pairsMux.Unlock()

	// Clear existing pairs
	s.stepiPairs = make(map[string]*StepiPair)

	// Regular expression to match stepi files - more specific patterns
	stepiRegex := regexp.MustCompile(`^stepi_(.+)\.md$`)
	stepiOutRegex := regexp.MustCompile(`^stepi_(.+)\.out\.md$`)

	files, err := os.ReadDir(s.workingDir)
	if err != nil {
		return fmt.Errorf("failed to read directory: %v", err)
	}

	// Track all stepi files with their modification times
	type fileInfo struct {
		path    string
		modTime time.Time
	}
	
	inputs := make(map[string]fileInfo)
	outputs := make(map[string]fileInfo)

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		name := file.Name()
		info, _ := file.Info()
		
		// Check for input files (but not .out.md files)
		if matches := stepiRegex.FindStringSubmatch(name); matches != nil && !strings.Contains(name, ".out.") {
			pairName := matches[1]
			inputs[pairName] = fileInfo{
				path:    filepath.Join(s.workingDir, name),
				modTime: info.ModTime(),
			}
		}
		
		// Check for output files
		if matches := stepiOutRegex.FindStringSubmatch(name); matches != nil {
			pairName := matches[1]
			outputs[pairName] = fileInfo{
				path:    filepath.Join(s.workingDir, name),
				modTime: info.ModTime(),
			}
		}
	}

	// Create pairs - collect all unique names
	allNames := make(map[string]bool)
	for name := range inputs {
		allNames[name] = true
	}
	for name := range outputs {
		allNames[name] = true
	}

	for name := range allNames {
		input, hasInput := inputs[name]
		output, hasOutput := outputs[name]
		
		pair := &StepiPair{
			Name:      name,
			HasInput:  hasInput,
			HasOutput: hasOutput,
			Status:    "idle",
		}

		if hasInput {
			pair.InputFile = input.path
		}
		if hasOutput {
			pair.OutputFile = output.path
			pair.LastRun = output.modTime
		}

		// Determine the most recent modification time for sorting
		var lastMod time.Time
		if hasInput && hasOutput {
			if input.modTime.After(output.modTime) {
				lastMod = input.modTime
			} else {
				lastMod = output.modTime
			}
		} else if hasInput {
			lastMod = input.modTime
		} else if hasOutput {
			lastMod = output.modTime
		}
		
		if !lastMod.IsZero() {
			pair.LastRun = lastMod
		}

		s.stepiPairs[name] = pair
	}

	return nil
}

func (s *Server) generateJobID() string {
	return fmt.Sprintf("job-%d", time.Now().Unix())
}

func (s *Server) generateOutputVersion(baseName string) string {
	// Find the next version number for output files
	for i := 1; i < 1000; i++ {
		var filename string
		if i == 1 {
			filename = fmt.Sprintf("stepi_%s.out.md", baseName)
		} else {
			filename = fmt.Sprintf("stepi_%s_v%d.out.md", baseName, i)
		}
		
		fullPath := filepath.Join(s.workingDir, filename)
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			return fullPath
		}
	}
	
	// Fallback with timestamp if too many versions
	timestamp := time.Now().Format("20060102_150405")
	return filepath.Join(s.workingDir, fmt.Sprintf("stepi_%s_%s.out.md", baseName, timestamp))
}

func (s *Server) isValidStepiName(name string) bool {
	// Allow alphanumeric, hyphens, and underscores
	matched, _ := regexp.MatchString(`^[a-zA-Z0-9_-]+$`, name)
	return matched && len(name) > 0 && len(name) <= 100
}

func (s *Server) isSafeFilePath(path string) bool {
	// Ensure path is within working directory and is a stepi file
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	
	workingAbs, err := filepath.Abs(s.workingDir)
	if err != nil {
		return false
	}
	
	// Must be within working directory
	if !strings.HasPrefix(abs, workingAbs) {
		return false
	}
	
	// Must be a stepi file
	name := filepath.Base(abs)
	matched, _ := regexp.MatchString(`^stepi_[a-zA-Z0-9_-]+\.(md|out\.md)$`, name)
	return matched
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	// API routes don't need auth middleware wrapper since they're handled here
	// But we still check auth for API requests
	if s.password != "" {
		cookie, err := r.Cookie("stepi_session")
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		
		s.authMux.RLock()
		authenticated := s.authenticated[cookie.Value]
		s.authMux.RUnlock()
		
		if !authenticated {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// Route API calls
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/pairs"):
		s.handlePairs(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/create"):
		s.handleCreate(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/run/"):
		s.handleRunPair(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/content/"):
		s.handleContent(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/jobs"):
		if strings.Contains(r.URL.Path, "/stream") {
			s.handleWebSocket(w, r)
		} else {
			s.handleJobs(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handlePairs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Refresh file scan
	if err := s.scanStepiFiles(); err != nil {
		http.Error(w, fmt.Sprintf("Failed to scan files: %v", err), http.StatusInternalServerError)
		return
	}

	s.pairsMux.RLock()
	pairs := make([]*StepiPair, 0, len(s.stepiPairs))
	for _, pair := range s.stepiPairs {
		pairs = append(pairs, pair)
	}
	s.pairsMux.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pairs)
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if !s.isValidStepiName(req.Name) {
		http.Error(w, "Invalid name format", http.StatusBadRequest)
		return
	}

	filename := fmt.Sprintf("stepi_%s.md", req.Name)
	filepath := filepath.Join(s.workingDir, filename)

	// Check if file already exists
	if _, err := os.Stat(filepath); err == nil {
		http.Error(w, "File already exists", http.StatusConflict)
		return
	}

	// Write file
	if err := os.WriteFile(filepath, []byte(req.Content), 0644); err != nil {
		http.Error(w, fmt.Sprintf("Failed to create file: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "created",
		"file":   filename,
	})
}

func (s *Server) handleContent(w http.ResponseWriter, r *http.Request) {
	// Extract pair name from URL: /api/content/{pairName}/{type}
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/content/"), "/")
	if len(pathParts) != 2 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	pairName := pathParts[0]
	fileType := pathParts[1] // "input" or "output"

	s.pairsMux.RLock()
	pair, exists := s.stepiPairs[pairName]
	s.pairsMux.RUnlock()

	if !exists {
		http.Error(w, "Pair not found", http.StatusNotFound)
		return
	}

	var filepath string
	switch fileType {
	case "input":
		if !pair.HasInput {
			http.Error(w, "Input file not found", http.StatusNotFound)
			return
		}
		filepath = pair.InputFile
	case "output":
		if !pair.HasOutput {
			http.Error(w, "Output file not found", http.StatusNotFound)
			return
		}
		filepath = pair.OutputFile
	default:
		http.Error(w, "Invalid file type", http.StatusBadRequest)
		return
	}

	if !s.isSafeFilePath(filepath) {
		http.Error(w, "Unsafe file path", http.StatusForbidden)
		return
	}

	if r.Method == http.MethodGet {
		// Read file content
		content, err := os.ReadFile(filepath)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to read file: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(content)
	} else if r.Method == http.MethodPut && fileType == "input" {
		// Update input file content (only if no output exists)
		if pair.HasOutput {
			http.Error(w, "Cannot edit input file when output exists", http.StatusForbidden)
			return
		}

		content, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}

		if err := os.WriteFile(filepath, content, 0644); err != nil {
			http.Error(w, fmt.Sprintf("Failed to write file: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
	} else {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleRunPair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pairName := strings.TrimPrefix(r.URL.Path, "/api/run/")

	s.pairsMux.RLock()
	pair, exists := s.stepiPairs[pairName]
	s.pairsMux.RUnlock()

	if !exists || !pair.HasInput {
		http.Error(w, "Pair or input file not found", http.StatusNotFound)
		return
	}

	// Check if already running
	if pair.Status == "running" {
		http.Error(w, "Job already running", http.StatusConflict)
		return
	}

	// Create output file path with versioning
	outputPath := s.generateOutputVersion(pairName)

	// Create job
	jobID := s.generateJobID()
	job := &Job{
		ID:        jobID,
		Status:    "pending",
		CreatedAt: time.Now(),
		InputFile: pair.InputFile,
		OutputFile: outputPath,
		PairName:  pairName,
		stdout:    make([]string, 0),
	}

	s.jobsMux.Lock()
	s.jobs[jobID] = job
	s.jobsMux.Unlock()

	// Update pair status
	s.pairsMux.Lock()
	pair.Status = "running"
	s.pairsMux.Unlock()

	// Start job
	go s.runStepiJob(job)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"job_id": jobID,
		"status": "started",
		"output_file": outputPath,
	})
}

func (s *Server) runStepiJob(job *Job) {
	ctx, cancel := context.WithCancel(context.Background())
	job.ctx = ctx
	job.cancel = cancel

	// Update job status
	s.jobsMux.Lock()
	job.Status = "running"
	now := time.Now()
	job.StartedAt = &now
	s.jobsMux.Unlock()

	// Find stepi binary
	stepiPath := s.findStepiBinary()
	if stepiPath == "" {
		s.finishJob(job, "failed", "stepi binary not found")
		return
	}

	// Run stepi command with just the filename (not full path)
	// since we're setting cmd.Dir to the working directory
	inputFileName := filepath.Base(job.InputFile)
	cmd := exec.CommandContext(ctx, stepiPath, inputFileName)
	job.cmd = cmd
	cmd.Dir = s.workingDir

	// Set up pipes
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.finishJob(job, "failed", fmt.Sprintf("Failed to create stdout pipe: %v", err))
		return
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		s.finishJob(job, "failed", fmt.Sprintf("Failed to create stderr pipe: %v", err))
		return
	}

	// Start command
	if err := cmd.Start(); err != nil {
		s.finishJob(job, "failed", fmt.Sprintf("Failed to start stepi: %v", err))
		return
	}

	// Read output
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			job.AddOutput(fmt.Sprintf("[OUT] %s", scanner.Text()))
		}
	}()

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			job.AddOutput(fmt.Sprintf("[ERR] %s", scanner.Text()))
		}
	}()

	// Wait for completion
	err = cmd.Wait()
	wg.Wait()

	if err != nil {
		s.finishJob(job, "failed", fmt.Sprintf("stepi execution failed: %v", err))
	} else {
		s.finishJob(job, "completed", "")
	}
}

func (s *Server) finishJob(job *Job, status, errorMsg string) {
	now := time.Now()
	
	s.jobsMux.Lock()
	job.Status = status
	job.EndedAt = &now
	if errorMsg != "" {
		job.Error = errorMsg
	}
	s.jobsMux.Unlock()

	// Update pair status
	if job.PairName != "" {
		s.pairsMux.Lock()
		if pair, exists := s.stepiPairs[job.PairName]; exists {
			if status == "completed" {
				pair.Status = "completed"
				pair.HasOutput = true
				pair.OutputFile = job.OutputFile
				pair.LastRun = now
			} else {
				pair.Status = "error"
			}
		}
		s.pairsMux.Unlock()
		
		// Refresh file scan to update pairs
		s.scanStepiFiles()
	}
}

func (s *Server) findStepiBinary() string {
	// First try to find stepi in PATH (globally available)
	if _, err := exec.LookPath("stepi"); err == nil {
		return "stepi" // Return just "stepi" to avoid path issues
	}

	// As a fallback, try local paths
	candidates := []string{
		"./stepi",
		"../stepi",
		"../../stepi",
		"/usr/local/bin/stepi",
		"/usr/bin/stepi",
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	return ""
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	s.jobsMux.RLock()
	defer s.jobsMux.RUnlock()

	jobs := make([]*Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		jobs = append(jobs, job)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jobs)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Extract job ID from URL
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 4 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	jobID := pathParts[3] // /api/jobs/{jobId}/stream

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	s.jobsMux.RLock()
	job, exists := s.jobs[jobID]
	s.jobsMux.RUnlock()

	if !exists {
		conn.WriteJSON(map[string]string{"error": "Job not found"})
		return
	}

	// Send existing output
	existingOutput := job.GetOutput()
	for _, line := range existingOutput {
		if err := conn.WriteJSON(map[string]string{"line": line}); err != nil {
			return
		}
	}

	// Stream new output
	lastLineCount := len(existingOutput)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			currentOutput := job.GetOutput()
			if len(currentOutput) > lastLineCount {
				for i := lastLineCount; i < len(currentOutput); i++ {
					if err := conn.WriteJSON(map[string]string{"line": currentOutput[i]}); err != nil {
						return
					}
				}
				lastLineCount = len(currentOutput)
			}

			s.jobsMux.RLock()
			status := job.Status
			s.jobsMux.RUnlock()

			if status == "completed" || status == "failed" {
				conn.WriteJSON(map[string]string{"status": status})
				return
			}
		}
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	// Write the HTML as a separate file and read it
	html := s.getIndexHTML()
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

func (s *Server) getIndexHTML() string {
	ngrokSection := ""
	if s.ngrokURL != "" {
		qrCodeImg := ""
		if s.qrCode != "" {
			qrCodeImg = fmt.Sprintf(`<div style="margin: 10px 0;"><img src="data:image/png;base64,%s" alt="QR Code" style="max-width: 200px; border: 1px solid #ddd; border-radius: 4px;"></div>`, s.qrCode)
		}
		ngrokSection = fmt.Sprintf(`
		<div class="section">
			<div class="section-header">🌐 Public Access</div>
			<div class="section-content">
				<p><strong>Ngrok URL:</strong> <a href="%s" target="_blank">%s</a></p>
				<p>Share this URL to access the server from anywhere!</p>
				%s
			</div>
		</div>`, s.ngrokURL, s.ngrokURL, qrCodeImg)
	}

	return `<!DOCTYPE html>
<html>
<head>
    <title>Stepi Server</title>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/codemirror/5.65.2/codemirror.min.css">
    <link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/codemirror/5.65.2/theme/default.min.css">
    <script src="https://cdnjs.cloudflare.com/ajax/libs/codemirror/5.65.2/codemirror.min.js"></script>
    <script src="https://cdnjs.cloudflare.com/ajax/libs/codemirror/5.65.2/mode/markdown/markdown.min.js"></script>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; margin: 0; padding: 20px; background: #f5f5f5; }
        .container { max-width: 1200px; margin: 0 auto; }
        .header { background: white; padding: 20px; border-radius: 8px; margin-bottom: 20px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
        .section { background: white; margin: 20px 0; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); overflow: hidden; }
        .section-header { background: #f8f9fa; padding: 15px 20px; border-bottom: 1px solid #dee2e6; font-weight: 600; }
        .section-content { padding: 20px; }
        
        /* File item styles */
        .file-item { border: 1px solid #dee2e6; border-radius: 6px; margin: 8px 0; padding: 12px 16px; background: #fff; transition: background-color 0.2s; }
        .file-item:hover { background: #f8f9fa; }
        .file-item.expanded { border-color: #007bff; }
        .file-header { display: flex; justify-content: space-between; align-items: center; cursor: pointer; }
        .file-name { font-weight: 600; font-size: 14px; color: #333; }
        .file-meta { font-size: 12px; color: #6c757d; margin-top: 4px; }
        .file-actions { display: flex; gap: 6px; align-items: center; }
        .file-status { padding: 2px 6px; border-radius: 3px; font-size: 11px; text-transform: uppercase; font-weight: 500; }
        .status-idle { background: #e9ecef; color: #495057; }
        .status-running { background: #fff3cd; color: #856404; }
        .status-completed { background: #d4edda; color: #155724; }
        .status-error { background: #f8d7da; color: #721c24; }
        .status-input-only { background: #e7f3ff; color: #0066cc; }
        
        /* File details (expanded view) */
        .file-details { margin-top: 16px; padding-top: 16px; border-top: 1px solid #dee2e6; display: none; }
        .file-details.active { display: block; }
        .details-tabs { display: flex; background: #f8f9fa; border-radius: 4px; margin-bottom: 12px; }
        .details-tab { padding: 8px 12px; cursor: pointer; border: none; background: none; font-size: 13px; border-radius: 4px; }
        .details-tab.active { background: white; color: #007bff; font-weight: 500; }
        .details-content { min-height: 250px; max-height: 350px; overflow-y: auto; }
        .file-content { white-space: pre-wrap; font-family: Monaco, 'Courier New', monospace; font-size: 13px; background: #f8f9fa; padding: 12px; border-radius: 4px; line-height: 1.4; }
        .stream-output { background: #000; color: #00ff00; padding: 12px; border-radius: 4px; height: 250px; overflow-y: auto; font-family: Monaco, monospace; font-size: 12px; line-height: 1.3; }
        .CodeMirror { height: 250px; border: 1px solid #ddd; border-radius: 4px; font-size: 13px; }
        
        .btn { padding: 6px 12px; border: 1px solid #ddd; border-radius: 4px; background: white; cursor: pointer; font-size: 12px; text-decoration: none; display: inline-block; color: #333; }
        .btn:hover { background: #f8f9fa; }
        .btn-primary { background: #007bff; color: white; border-color: #007bff; }
        .btn-primary:hover { background: #0056b3; }
        .btn-success { background: #28a745; color: white; border-color: #28a745; }
        .btn-success:hover { background: #1e7e34; }
        .btn-secondary { background: #6c757d; color: white; border-color: #6c757d; }
        .btn-secondary:hover { background: #545b62; }
        .btn-sm { padding: 4px 8px; font-size: 11px; }
        
        .create-form { display: none; padding: 20px; border: 1px solid #ddd; border-radius: 6px; margin: 20px 0; }
        .form-group { margin: 15px 0; }
        .form-group label { display: block; margin-bottom: 5px; font-weight: 600; }
        .form-group input { width: 100%; padding: 8px; border: 1px solid #ddd; border-radius: 4px; box-sizing: border-box; }
        
        .category-header { font-weight: 600; color: #666; font-size: 14px; margin: 16px 0 8px 0; padding-bottom: 4px; border-bottom: 2px solid #007bff; }
        .category-header:first-child { margin-top: 0; }
        
        .empty-state { text-align: center; padding: 40px 20px; color: #6c757d; }
        
        @media (max-width: 768px) {
            body { padding: 10px; }
            .file-header { flex-direction: column; align-items: flex-start; }
            .file-actions { margin-top: 8px; width: 100%; justify-content: flex-start; }
            .details-tabs { flex-wrap: wrap; }
            .details-tab { flex: 1; text-align: center; }
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>🚀 Stepi Server</h1>
            <p>Create, edit and run stepi markdown files</p>
            <button class="btn btn-primary" onclick="showCreateForm()">+ New File</button>
            <button class="btn btn-secondary" onclick="loadPairs()">↻ Refresh</button>
        </div>

        ` + ngrokSection + `

        <div id="createForm" class="create-form">
            <h3>Create New Stepi File</h3>
            <div class="form-group">
                <label>Name (will create stepi_[name].md):</label>
                <input type="text" id="newFileName" placeholder="my-task" pattern="[a-zA-Z0-9_-]+" required>
            </div>
            <div class="form-group">
                <label>Content:</label>
                <textarea id="newFileContent" style="display: none;"></textarea>
            </div>
            <button class="btn btn-success" onclick="createFile()">Create & Save</button>
            <button class="btn" onclick="hideCreateForm()">Cancel</button>
        </div>

        <div class="section">
            <div class="section-header">📁 Stepi Files</div>
            <div class="section-content">
                <div id="pairsList">Loading...</div>
            </div>
        </div>

        <div class="section">
            <div class="section-header">📊 Recent Jobs</div>
            <div class="section-content">
                <div id="jobsList">No jobs yet</div>
            </div>
        </div>
    </div>

    <script>
        let editor = null;
        let currentPairs = {};
        let activeStreams = {};
        let expandedFiles = new Set();

        // Initialize
        document.addEventListener('DOMContentLoaded', function() {
            loadPairs();
            loadJobs();
            setInterval(loadJobs, 5000);
        });

        function showCreateForm() {
            const form = document.getElementById('createForm');
            form.style.display = 'block';
            
            if (!editor) {
                editor = CodeMirror.fromTextArea(document.getElementById('newFileContent'), {
                    mode: 'markdown',
                    lineNumbers: true,
                    lineWrapping: true,
                    theme: 'default'
                });
            }
        }

        function hideCreateForm() {
            document.getElementById('createForm').style.display = 'none';
            document.getElementById('newFileName').value = '';
            if (editor) {
                editor.setValue('');
            }
        }

        function createFile() {
            const name = document.getElementById('newFileName').value.trim();
            const content = editor ? editor.getValue() : '';

            if (!name || !/^[a-zA-Z0-9_-]+$/.test(name)) {
                alert('Please enter a valid name (letters, numbers, hyphens, underscores only)');
                return;
            }

            fetch('/api/create', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ name: name, content: content })
            })
            .then(response => response.json())
            .then(data => {
                if (data.status === 'created') {
                    hideCreateForm();
                    loadPairs();
                } else {
                    alert('Failed to create file');
                }
            })
            .catch(error => {
                alert('Error: ' + error.message);
            });
        }

        function loadPairs() {
            fetch('/api/pairs')
            .then(response => response.json())
            .then(pairs => {
                currentPairs = {};
                pairs.forEach(pair => currentPairs[pair.name] = pair);
                renderPairs(pairs);
            })
            .catch(error => {
                document.getElementById('pairsList').innerHTML = '<p>Error loading pairs: ' + error + '</p>';
            });
        }

        function loadJobs() {
            fetch('/api/jobs')
            .then(response => response.json())
            .then(jobs => {
                renderJobs(jobs);
            })
            .catch(error => {
                console.log('Error loading jobs:', error);
            });
        }

        function renderPairs(pairs) {
            const container = document.getElementById('pairsList');
            
            if (pairs.length === 0) {
                container.innerHTML = '<div class="empty-state"><p>No stepi files found. Create one to get started!</p></div>';
                return;
            }

            // Separate files into categories and sort them
            const inputOnlyFiles = pairs.filter(p => p.has_input && !p.has_output).sort(sortByModTime);
            const pairedFiles = pairs.filter(p => p.has_input && p.has_output).sort(sortByModTime);
            const outputOnlyFiles = pairs.filter(p => !p.has_input && p.has_output).sort(sortByModTime);

            let html = '';
            
            if (inputOnlyFiles.length > 0) {
                html += '<div class="category-header">Ready to Run</div>';
                html += inputOnlyFiles.map(pair => renderFileItem(pair, 'input-only')).join('');
            }
            
            if (pairedFiles.length > 0) {
                html += '<div class="category-header">Input/Output Pairs</div>';
                html += pairedFiles.map(pair => renderFileItem(pair, 'paired')).join('');
            }
            
            if (outputOnlyFiles.length > 0) {
                html += '<div class="category-header">Output Only</div>';
                html += outputOnlyFiles.map(pair => renderFileItem(pair, 'output-only')).join('');
            }

            container.innerHTML = html;
        }

        function sortByModTime(a, b) {
            const aTime = a.last_run ? new Date(a.last_run).getTime() : 0;
            const bTime = b.last_run ? new Date(b.last_run).getTime() : 0;
            return bTime - aTime; // Most recent first
        }

        function renderFileItem(pair, category) {
            const statusClass = pair.status === 'running' ? 'status-running' : 
                              pair.status === 'error' ? 'status-error' :
                              pair.status === 'completed' ? 'status-completed' :
                              category === 'input-only' ? 'status-input-only' : 'status-idle';
            
            const statusText = category === 'input-only' ? 'ready' : pair.status;
            const lastRun = pair.last_run ? new Date(pair.last_run).toLocaleString() : 'Never';
            
            const runButton = pair.has_input && pair.status !== 'running' ? 
                '<button class="btn btn-primary btn-sm" onclick="runPair(\'' + pair.name + '\')">Run</button>' : '';
            
            const editButton = pair.has_input && !pair.has_output ?
                '<button class="btn btn-sm" onclick="toggleFileDetails(\'' + pair.name + '\', \'input\')">Edit</button>' : '';
            
            const viewButton = '<button class="btn btn-sm" onclick="toggleFileDetails(\'' + pair.name + '\')">View</button>';
            
            const isExpanded = expandedFiles.has(pair.name);
            const expandedClass = isExpanded ? 'expanded' : '';
            
            const inputBadge = pair.has_input ? '<span style="font-size: 10px; background: #28a745; color: white; padding: 1px 4px; border-radius: 2px; margin-right: 4px;">IN</span>' : '';
            const outputBadge = pair.has_output ? '<span style="font-size: 10px; background: #007bff; color: white; padding: 1px 4px; border-radius: 2px; margin-right: 4px;">OUT</span>' : '';
            
            return '<div class="file-item ' + expandedClass + '">' +
                '<div class="file-header" onclick="toggleFileDetails(\'' + pair.name + '\')">' +
                '<div>' +
                '<div class="file-name">' + inputBadge + outputBadge + 'stepi_' + pair.name + '</div>' +
                '<div class="file-meta">Last modified: ' + lastRun + '</div>' +
                '</div>' +
                '<div class="file-actions" onclick="event.stopPropagation()">' +
                '<span class="file-status ' + statusClass + '">' + statusText + '</span>' +
                runButton + editButton + viewButton +
                '</div>' +
                '</div>' +
                '<div id="details-' + pair.name + '" class="file-details' + (isExpanded ? ' active' : '') + '"></div>' +
                '</div>';
        }

        function toggleFileDetails(pairName, defaultTab) {
            const detailsDiv = document.getElementById('details-' + pairName);
            const fileItem = detailsDiv.parentElement;
            
            if (expandedFiles.has(pairName)) {
                expandedFiles.delete(pairName);
                detailsDiv.classList.remove('active');
                fileItem.classList.remove('expanded');
                return;
            }
            
            expandedFiles.add(pairName);
            detailsDiv.classList.add('active');
            fileItem.classList.add('expanded');
            
            // Load details content
            loadFileDetails(pairName, defaultTab || 'auto');
        }

        function loadFileDetails(pairName, defaultTab) {
            const detailsDiv = document.getElementById('details-' + pairName);
            const pair = currentPairs[pairName];
            
            if (!pair) return;
            
            const tabs = [];
            if (pair.has_input) tabs.push('input');
            if (pair.has_output) tabs.push('output');
            if (pair.status === 'running') tabs.push('live');
            
            const activeTab = defaultTab === 'auto' ? tabs[0] : defaultTab;
            
            let tabsHtml = '<div class="details-tabs">';
            if (pair.has_input) {
                const activeClass = activeTab === 'input' ? 'active' : '';
                tabsHtml += '<button class="details-tab ' + activeClass + '" onclick="showFileTab(\'' + pairName + '\', \'input\')">Input</button>';
            }
            if (pair.has_output) {
                const activeClass = activeTab === 'output' ? 'active' : '';
                tabsHtml += '<button class="details-tab ' + activeClass + '" onclick="showFileTab(\'' + pairName + '\', \'output\')">Output</button>';
            }
            if (pair.status === 'running') {
                const activeClass = activeTab === 'live' ? 'active' : '';
                tabsHtml += '<button class="details-tab ' + activeClass + '" onclick="showFileTab(\'' + pairName + '\', \'live\')">Live</button>';
            }
            tabsHtml += '</div>';
            
            detailsDiv.innerHTML = tabsHtml + '<div id="tab-content-' + pairName + '" class="details-content"></div>';
            
            // Show the default tab
            if (tabs.length > 0) {
                showFileTab(pairName, activeTab);
            }
        }

        function showFileTab(pairName, tabType) {
            const detailsDiv = document.getElementById('details-' + pairName);
            const contentDiv = document.getElementById('tab-content-' + pairName);
            
            // Update active tab
            detailsDiv.querySelectorAll('.details-tab').forEach(tab => tab.classList.remove('active'));
            const activeTab = Array.from(detailsDiv.querySelectorAll('.details-tab')).find(tab => 
                tab.getAttribute('onclick').includes(tabType));
            if (activeTab) activeTab.classList.add('active');
            
            // Load content
            if (tabType === 'live') {
                loadLiveContent(pairName, contentDiv);
            } else {
                loadFileContent(pairName, tabType, contentDiv);
            }
        }

        function loadFileContent(pairName, tabType, contentDiv) {
            if (contentDiv.dataset.loaded === tabType) return;

            fetch('/api/content/' + pairName + '/' + tabType)
            .then(response => response.text())
            .then(content => {
                const pair = currentPairs[pairName];
                
                if (tabType === 'input' && !pair?.has_output) {
                    // Editable input
                    const textarea = document.createElement('textarea');
                    textarea.value = content;
                    textarea.style.display = 'none';
                    contentDiv.innerHTML = '';
                    contentDiv.appendChild(textarea);
                    
                    const inputEditor = CodeMirror.fromTextArea(textarea, {
                        mode: 'markdown',
                        lineNumbers: true,
                        lineWrapping: true
                    });
                    
                    const saveBtn = document.createElement('button');
                    saveBtn.textContent = 'Save Changes';
                    saveBtn.className = 'btn btn-success';
                    saveBtn.style.marginBottom = '10px';
                    saveBtn.onclick = function() { saveInputContent(pairName, inputEditor); };
                    contentDiv.insertBefore(saveBtn, contentDiv.firstChild);
                } else {
                    // Read-only content
                    contentDiv.innerHTML = '<div class="file-content">' + escapeHtml(content) + '</div>';
                }
                
                contentDiv.dataset.loaded = tabType;
            })
            .catch(error => {
                contentDiv.innerHTML = '<p>Error loading content: ' + error + '</p>';
            });
        }

        function loadLiveContent(pairName, contentDiv) {
            fetch('/api/jobs')
            .then(response => response.json())
            .then(jobs => {
                const runningJob = jobs.find(job => job.pair_name === pairName && job.status === 'running');
                if (runningJob) {
                    startLiveStream(pairName, runningJob.id, contentDiv);
                } else {
                    contentDiv.innerHTML = '<p>No running job found for this file.</p>';
                }
            })
            .catch(error => {
                contentDiv.innerHTML = '<p>Error loading live content: ' + error + '</p>';
            });
        }

        function startLiveStream(pairName, jobId, contentDiv) {
            if (activeStreams[pairName]) {
                activeStreams[pairName].close();
            }

            contentDiv.innerHTML = '<div class="stream-output" id="stream-' + pairName + '">Connecting to live output...</div>';
            
            const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
            const ws = new WebSocket(protocol + '//' + window.location.host + '/api/jobs/' + jobId + '/stream');
            
            activeStreams[pairName] = ws;
            const outputDiv = document.getElementById('stream-' + pairName);
            
            ws.onmessage = function(event) {
                const data = JSON.parse(event.data);
                if (data.line) {
                    outputDiv.textContent += data.line + '\n';
                    outputDiv.scrollTop = outputDiv.scrollHeight;
                } else if (data.status) {
                    outputDiv.textContent += '\n=== Job ' + data.status + ' ===\n';
                    loadPairs();
                    ws.close();
                }
            };
            
            ws.onerror = function(error) {
                outputDiv.textContent += '\nWebSocket error: ' + error + '\n';
            };
        }

        function saveInputContent(pairName, editor) {
            const content = editor.getValue();
            
            fetch('/api/content/' + pairName + '/input', {
                method: 'PUT',
                headers: { 'Content-Type': 'text/plain' },
                body: content
            })
            .then(response => response.json())
            .then(data => {
                if (data.status === 'updated') {
                    alert('File saved successfully!');
                } else {
                    alert('Failed to save file');
                }
            })
            .catch(error => {
                alert('Error saving file: ' + error);
            });
        }

        function runPair(pairName) {
            fetch('/api/run/' + pairName, { method: 'POST' })
            .then(response => response.json())
            .then(data => {
                if (data.status === 'started') {
                    loadPairs();
                    // Auto-expand and show live tab
                    setTimeout(() => {
                        expandedFiles.add(pairName);
                        loadFileDetails(pairName, 'live');
                    }, 1000);
                }
            })
            .catch(error => {
                alert('Failed to start job: ' + error);
            });
        }

        function escapeHtml(text) {
            const div = document.createElement('div');
            div.textContent = text;
            return div.innerHTML;
        }

        function renderJobs(jobs) {
            const container = document.getElementById('jobsList');
            
            if (jobs.length === 0) {
                container.innerHTML = '<div class="empty-state"><p>No jobs yet</p></div>';
                return;
            }

            const recentJobs = jobs.slice(-10).reverse();
            
            container.innerHTML = recentJobs.map(function(job) {
                const createdAt = new Date(job.created_at).toLocaleString();
                const statusClass = job.status === 'completed' ? 'status-completed' : 
                                  job.status === 'running' ? 'status-running' : 
                                  job.status === 'failed' ? 'status-error' : 'status-idle';
                const errorMsg = job.error ? '<div style="color: red; font-size: 12px;">Error: ' + job.error + '</div>' : '';
                
                return '<div class="file-item">' +
                    '<div class="file-header">' +
                    '<div>' +
                    '<div class="file-name">' + (job.pair_name || job.id) + '</div>' +
                    '<div class="file-meta">Started: ' + createdAt + '</div>' +
                    '</div>' +
                    '<span class="file-status ' + statusClass + '">' + job.status + '</span>' +
                    '</div>' +
                    errorMsg +
                    '</div>';
            }).join('');
        }
    </script>
</body>
</html>`
}

func (s *Server) setupNgrok(ctx context.Context, router http.Handler, port string) error {
	// Start ngrok tunnel
	tun, err := ngrok.Listen(ctx, config.HTTPEndpoint(), ngrok.WithAuthtokenFromEnv())
	if err != nil {
		return fmt.Errorf("failed to start ngrok tunnel: %v", err)
	}

	// Get the URL
	s.ngrokURL = tun.URL()
	
	// Generate QR code for web interface
	qr, err := qrcode.Encode(s.ngrokURL, qrcode.Medium, 256)
	if err != nil {
		log.Printf("Warning: failed to generate QR code for web: %v", err)
		s.qrCode = ""
	} else {
		s.qrCode = base64.StdEncoding.EncodeToString(qr)
	}

	// Print QR code to terminal console
	fmt.Println("\n🌐 NGROK TUNNEL ESTABLISHED")
	fmt.Println("──────────────────────────────────")
	fmt.Printf("📱 Public URL: %s\n", s.ngrokURL)
	fmt.Println("\n📱 QR Code for Mobile Access:")
	
	if err := printQRToTerminal(s.ngrokURL); err != nil {
		log.Printf("Warning: Could not print QR to terminal: %v", err)
	}
	
	fmt.Println("\n📋 Access Information:")
	fmt.Printf("   🖥️  Local:  http://localhost:%s\n", port)
	fmt.Printf("   📱 Remote: %s\n", s.ngrokURL)
	fmt.Println("   💡 Scan QR code or share the remote URL for mobile access!")
	fmt.Println("──────────────────────────────────\n")

	// Start accepting connections in a goroutine with the same router
	go func() {
		log.Printf("Ngrok tunnel accepting connections...")
		if err := http.Serve(tun, router); err != nil {
			log.Printf("Ngrok tunnel error: %v", err)
		}
	}()

	return nil
}
func main() {
	var (
		port = flag.String("port", "8080", "Port to run server on")
		password = flag.String("password", "", "Password for authentication (optional)")
		workDir = flag.String("workdir", ".", "Working directory to serve files from")
		enableNgrok = flag.Bool("ngrok", false, "Enable ngrok tunnel for public access")
	)
	flag.Parse()

	// Get absolute path for working directory
	workingDir, err := filepath.Abs(*workDir)
	if err != nil {
		log.Fatal("Failed to get absolute working directory:", err)
	}

	server := NewServer(workingDir, *password)
	
	// Initial file scan
	if err := server.scanStepiFiles(); err != nil {
		log.Printf("Warning: Failed to scan stepi files: %v", err)
	}

	router := mux.NewRouter()

	// Authentication middleware for main routes
	authMiddleware := server.requireAuth

	// Main routes (with auth)
	router.HandleFunc("/", authMiddleware(server.handleIndex)).Methods("GET")
	router.HandleFunc("/login", authMiddleware(func(w http.ResponseWriter, r *http.Request) {})).Methods("GET", "POST")

	// API routes (auth checked inside handleAPI)
	router.PathPrefix("/api/").HandlerFunc(server.handleAPI)

	// Setup ngrok if requested
	ctx := context.Background()
	if *enableNgrok {
		if err := server.setupNgrok(ctx, router, *port); err != nil {
			log.Printf("Warning: Failed to setup ngrok: %v", err)
			log.Printf("Continuing without ngrok...")
		}
	}

	log.Printf("🚀 Starting stepi server on port %s", *port)
	log.Printf("📂 Working directory: %s", workingDir)
	if *password != "" {
		log.Printf("🔐 Authentication enabled - password required")
	}
	log.Printf("🖥️  Local access: http://localhost:%s", *port)
	if server.ngrokURL != "" {
		log.Printf("📱 Public access: %s", server.ngrokURL)
		log.Printf("📱 QR code displayed above for easy mobile access")
	} else if !*enableNgrok {
		log.Printf("💡 To expose via ngrok: add --ngrok flag (requires NGROK_AUTHTOKEN env var)")
		log.Printf("   Example: ./stepi-server --ngrok")
	}
	log.Printf("──────────────────────────────────")
	
	if err := http.ListenAndServe(":"+*port, router); err != nil {
		log.Fatal("Server failed:", err)
	}
}