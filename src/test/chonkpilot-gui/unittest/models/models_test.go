package models_test

import (
	"testing"

	"github.com/chonkpilot/chonkpilot-gui/models"
)

func TestNewTask(t *testing.T) {
	taskID := "task-1"
	parentID := ""
	turnID := "turn-1"
	sessionID := "session-1"
	name := "Build project"
	depth := 0

	task := models.NewTask(taskID, parentID, turnID, sessionID, name, depth)
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
	if task.Status != models.TaskStatusPending {
		t.Errorf("Status = %q, want %q", task.Status, models.TaskStatusPending)
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
	task := models.NewTask("task-2", parentID, "turn-1", "session-1", "Sub task", 1)
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
		{models.TaskStatusPending, "pending"},
		{models.TaskStatusRunning, "running"},
		{models.TaskStatusPaused, "paused"},
		{models.TaskStatusCompleted, "completed"},
		{models.TaskStatusFailed, "failed"},
		{models.TaskStatusCancelled, "cancelled"},
	}
	for _, tc := range tests {
		if tc.constant != tc.expected {
			t.Errorf("constant = %q, want %q", tc.constant, tc.expected)
		}
	}
}
