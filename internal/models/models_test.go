package models

import (
	"testing"
	"time"
)

func TestNewSession(t *testing.T) {
	sessionID := "test-session-1"
	workDir := "/tmp/test"
	title := "Test Session"

	s := NewSession(sessionID, "", workDir, title)
	if s == nil {
		t.Fatal("NewSession returned nil")
	}
	if s.SessionID != sessionID {
		t.Errorf("SessionID = %q, want %q", s.SessionID, sessionID)
	}
	if s.WorkDir != workDir {
		t.Errorf("WorkDir = %q, want %q", s.WorkDir, workDir)
	}
	if s.Title != title {
		t.Errorf("Title = %q, want %q", s.Title, title)
	}
	if s.CreatedAt == "" {
		t.Error("CreatedAt should not be empty")
	}
	if s.UpdatedAt == "" {
		t.Error("UpdatedAt should not be empty")
	}
	// Verify timestamps are valid RFC3339
	_, err := time.Parse(time.RFC3339, s.CreatedAt)
	if err != nil {
		t.Errorf("CreatedAt is not valid RFC3339: %v", err)
	}
}

func TestNewTurn(t *testing.T) {
	turnID := "test-turn-1"
	sessionID := "test-session-1"

	turn := NewTurn(turnID, sessionID)
	if turn == nil {
		t.Fatal("NewTurn returned nil")
	}
	if turn.TurnID != turnID {
		t.Errorf("TurnID = %q, want %q", turn.TurnID, turnID)
	}
	if turn.SessionID != sessionID {
		t.Errorf("SessionID = %q, want %q", turn.SessionID, sessionID)
	}
	if turn.Score != 0 {
		t.Errorf("Score = %d, want 0", turn.Score)
	}
}

func TestNewTurnResult(t *testing.T) {
	r := TurnResult{
		TurnID: "test-turn-1",
		A:      "Go is a programming language",
		Score:  5,
	}
	if r.TurnID != "test-turn-1" {
		t.Errorf("TurnResult.TurnID = %q, want %q", r.TurnID, "test-turn-1")
	}
}

func TestNewMessage(t *testing.T) {
	msgID := "msg-1"
	turnID := "turn-1"
	role := "user"
	content := "Hello"

	msg := NewMessage(msgID, turnID, role, "text", content)
	if msg == nil {
		t.Fatal("NewMessage returned nil")
	}
	if msg.MessageID != msgID {
		t.Errorf("MessageID = %q, want %q", msg.MessageID, msgID)
	}
	if msg.TurnID != turnID {
		t.Errorf("TurnID = %q, want %q", msg.TurnID, turnID)
	}
	if msg.Role != role {
		t.Errorf("Role = %q, want %q", msg.Role, role)
	}
	if msg.Content != content {
		t.Errorf("Content = %q, want %q", msg.Content, content)
	}
}

func TestNewTask(t *testing.T) {
	taskID := "task-1"
	parentID := ""
	turnID := "turn-1"
	sessionID := "session-1"
	name := "Build project"
	depth := 0

	task := NewTask(taskID, parentID, turnID, sessionID, name, depth)
	if task == nil {
		t.Fatal("NewTask returned nil")
	}
	if task.TaskID != taskID {
		t.Errorf("TaskID = %q, want %q", task.TaskID, taskID)
	}
	if task.TurnID != turnID {
		t.Errorf("TurnID = %q, want %q", task.TurnID, turnID)
	}
	if task.Name != name {
		t.Errorf("Name = %q, want %q", task.Name, name)
	}
	if task.Status != TaskStatusPending {
		t.Errorf("Status = %q, want %q", task.Status, TaskStatusPending)
	}
	if task.Progress != 0 {
		t.Errorf("Progress = %d, want 0", task.Progress)
	}
	if task.Depth != depth {
		t.Errorf("Depth = %d, want %d", task.Depth, depth)
	}
}

func TestNewTaskWithParent(t *testing.T) {
	parentID := "parent-task-1"
	task := NewTask("task-2", parentID, "turn-1", "session-1", "Sub task", 1)
	if task.ParentTaskID != parentID {
		t.Errorf("ParentTaskID = %q, want %q", task.ParentTaskID, parentID)
	}
	if task.Depth != 1 {
		t.Errorf("Depth = %d, want 1", task.Depth)
	}
}

func TestTaskStatusConstants(t *testing.T) {
	tests := []struct {
		constant string
		expected string
	}{
		{TaskStatusPending, "pending"},
		{TaskStatusRunning, "running"},
		{TaskStatusPaused, "paused"},
		{TaskStatusCompleted, "completed"},
		{TaskStatusFailed, "failed"},
		{TaskStatusCancelled, "cancelled"},
	}
	for _, tc := range tests {
		if tc.constant != tc.expected {
			t.Errorf("constant = %q, want %q", tc.constant, tc.expected)
		}
	}
}

func TestNewConfig(t *testing.T) {
	key := "test-key"
	value := `{"provider":"openai","model":"gpt-4"}`

	c := NewConfig(key, value)
	if c == nil {
		t.Fatal("NewConfig returned nil")
	}
	if c.Key != key {
		t.Errorf("Key = %q, want %q", c.Key, key)
	}
	if c.Value != value {
		t.Errorf("Value = %q, want %q", c.Value, value)
	}
}

func TestTrustedDirConfig(t *testing.T) {
	dir := TrustedDirConfig{
		Path:  "/home/user/project",
		Perms: "read",
		IsRef: false,
	}
	if dir.Path != "/home/user/project" {
		t.Errorf("Path = %q", dir.Path)
	}
	if dir.Perms != "read" {
		t.Errorf("Perms = %q", dir.Perms)
	}
}

func TestToolDefinition(t *testing.T) {
	tool := ToolDefinition{
		Name:        "read_file",
		Description: "Read a file",
		IsBuiltin:   true,
		Parameters: ToolParameterSchema{
			Type:     "object",
			Required: []string{"path"},
			Properties: map[string]ToolParameter{
				"path": {Type: "string", Description: "File path"},
			},
		},
	}
	if tool.Name != "read_file" {
		t.Errorf("Name = %q", tool.Name)
	}
	if !tool.IsBuiltin {
		t.Error("IsBuiltin should be true")
	}
	if len(tool.Parameters.Required) != 1 {
		t.Errorf("Required params count = %d, want 1", len(tool.Parameters.Required))
	}
}

func TestScenario(t *testing.T) {
	s := Scenario{
		ID:          "web-app",
		Name:        "Web Application",
		Description: "Full-stack web app",
		Category:    "scenario",
	}
	if s.ID != "web-app" {
		t.Errorf("ID = %q", s.ID)
	}
}

func TestRule(t *testing.T) {
	r := Rule{
		Name:     "use-go-style",
		Category: "language",
		Content:  "Follow Go conventions",
	}
	if r.Name != "use-go-style" {
		t.Errorf("Name = %q", r.Name)
	}
}
