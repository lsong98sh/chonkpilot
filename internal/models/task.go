package models

import "time"

// Task represents an executable task in the task tree.
type Task struct {
	TaskID       string `json:"task_id"`
	ParentTaskID string `json:"parent_task_id,omitempty"`
	TurnID       string `json:"turn_id"`
	SessionID    string `json:"session_id"`
	Name         string `json:"name"`
	Status       string `json:"status"` // pending / running / paused / completed / failed / cancelled
	Progress     int    `json:"progress"` // 0-100
	Result       string `json:"result,omitempty"`
	ExecutorPID  int    `json:"executor_pid,omitempty"`
	PipePath     string `json:"pipe_path,omitempty"`
	PromptFile   string `json:"prompt_file,omitempty"`
	Depth        int    `json:"depth"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// TaskStatus constants
const (
	TaskStatusPending    = "pending"
	TaskStatusRunning    = "running"
	TaskStatusPaused     = "paused"
	TaskStatusCompleted  = "completed"
	TaskStatusFailed     = "failed"
	TaskStatusCancelled  = "cancelled"
)

// NewTask creates a new Task with generated ID and timestamps.
func NewTask(taskID, parentTaskID, turnID, sessionID, name string, depth int) *Task {
	now := time.Now().UTC().Format(time.RFC3339)
	return &Task{
		TaskID:       taskID,
		ParentTaskID: parentTaskID,
		TurnID:       turnID,
		SessionID:    sessionID,
		Name:         name,
		Status:       TaskStatusPending,
		Progress:     0,
		Depth:        depth,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}
