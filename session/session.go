// Package session provides session persistence for multi-turn conversations
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Message represents a stored message in the session
type Message struct {
	Role    string `json:"role"` // "user", "assistant", "tool_result"
	Content string `json:"content"`
	// For tool calls
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// For tool results
	ToolResults []ToolResult `json:"tool_results,omitempty"`
}

// ToolCall represents a tool call from the assistant
type ToolCall struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input string `json:"input"` // JSON string
}

// ToolResult represents a tool result
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error"`
}

// Session holds the conversation state
type Session struct {
	Name         string    `json:"name"`
	SystemPrompt string    `json:"system_prompt"`
	Messages     []Message `json:"messages"`
	Model        string    `json:"model"`
}

// sessionDir returns the directory for session files
func sessionDir() string {
	// Use current directory
	return "."
}

// sessionFile returns the path to a session file
func sessionFile(name string) string {
	return filepath.Join(sessionDir(), fmt.Sprintf(".stepi-session-%s.json", name))
}

// Exists checks if a session exists
func Exists(name string) bool {
	_, err := os.Stat(sessionFile(name))
	return err == nil
}

// Create creates a new session
func Create(name, systemPrompt, model string) error {
	if Exists(name) {
		return fmt.Errorf("session '%s' already exists", name)
	}

	session := Session{
		Name:         name,
		SystemPrompt: systemPrompt,
		Messages:     []Message{},
		Model:        model,
	}

	return save(name, &session)
}

// Load loads an existing session
func Load(name string) (*Session, error) {
	data, err := os.ReadFile(sessionFile(name))
	if err != nil {
		return nil, fmt.Errorf("session '%s' not found", name)
	}

	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("invalid session file: %w", err)
	}

	return &session, nil
}

// save saves the session to disk
func save(name string, session *Session) error {
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(sessionFile(name), data, 0644)
}

// Save saves the session
func (s *Session) Save() error {
	return save(s.Name, s)
}

// AddUserMessage adds a user message to the session
func (s *Session) AddUserMessage(content string) {
	s.Messages = append(s.Messages, Message{
		Role:    "user",
		Content: content,
	})
}

// AddAssistantMessage adds an assistant message to the session
func (s *Session) AddAssistantMessage(content string, toolCalls []ToolCall) {
	s.Messages = append(s.Messages, Message{
		Role:      "assistant",
		Content:   content,
		ToolCalls: toolCalls,
	})
}

// AddToolResults adds tool results to the session
func (s *Session) AddToolResults(results []ToolResult) {
	s.Messages = append(s.Messages, Message{
		Role:        "tool_result",
		ToolResults: results,
	})
}

// Delete deletes a session
func Delete(name string) error {
	path := sessionFile(name)
	if !Exists(name) {
		return fmt.Errorf("session '%s' not found", name)
	}
	return os.Remove(path)
}
