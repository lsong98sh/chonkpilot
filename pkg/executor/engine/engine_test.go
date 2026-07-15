package engine

import (
	"testing"
	"go.uber.org/zap"
)

func TestNewEngine(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	e := NewEngine("", logger)
	if e == nil {
		t.Fatal("NewEngine() returned nil")
	}
}

func TestExecuteSuccess(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	e := NewEngine("", logger)

	result := e.Execute("go", []string{"version"})
	if !result.Success {
		t.Errorf("Execute() failed: %s", result.Error)
	}
	if result.Output == "" {
		t.Error("Execute() returned empty output")
	}
	if result.DurationMs <= 0 {
		t.Error("DurationMs should be positive")
	}
}

func TestExecuteFailure(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	e := NewEngine("", logger)

	result := e.Execute("nonexistent_command_xyz", []string{})
	if result.Success {
		t.Error("Execute() should fail for non-existent command")
	}
	if result.Error == "" {
		t.Error("Error should be non-empty for failed command")
	}
}

func TestToolResult(t *testing.T) {
	r := ToolResult{
		Success:    true,
		Output:     "hello",
		DurationMs: 100,
	}
	if !r.Success {
		t.Error("Success should be true")
	}
	if r.Output != "hello" {
		t.Errorf("Output = %q", r.Output)
	}
}
