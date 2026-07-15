package engine

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"go.uber.org/zap"
)

// ToolResult holds the result of a tool execution.
type ToolResult struct {
	Success    bool   `json:"success"`
	Output     string `json:"output"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"duration_ms"`
}

// Engine executes tool calls from the LLM.
type Engine struct {
	logger  *zap.Logger
	WorkDir string
}

// NewEngine creates a new tool execution engine.
// workDir is the working directory for executed commands.
func NewEngine(workDir string, logger *zap.Logger) *Engine {
	return &Engine{
		logger:  logger,
		WorkDir: workDir,
	}
}

// Execute runs a tool command and returns the result.
func (e *Engine) Execute(command string, args []string) *ToolResult {
	start := time.Now()

	cmd := exec.Command(command, args...)
	cmd.Dir = e.WorkDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		duration := time.Since(start).Milliseconds()
		e.logger.Warn("Tool execution failed",
			zap.String("command", command),
			zap.Error(err),
			zap.Int64("duration_ms", duration),
		)
		return &ToolResult{
			Success:    false,
			Output:     stdout.String(),
			Error:      fmt.Sprintf("%s: %s", err.Error(), stderr.String()),
			DurationMs: duration,
		}
	}

	duration := time.Since(start).Milliseconds()
	e.logger.Info("Tool execution succeeded",
		zap.String("command", command),
		zap.Int64("duration_ms", duration),
	)

	return &ToolResult{
		Success:    true,
		Output:     stdout.String(),
		DurationMs: duration,
	}
}

// ExecuteShell runs a shell command.
func (e *Engine) ExecuteShell(shellCmd string) *ToolResult {
	// Determine shell
	var command string
	var args []string

	if isWindows() {
		command = "cmd"
		args = []string{"/c", shellCmd}
	} else {
		command = "sh"
		args = []string{"-c", shellCmd}
	}

	return e.Execute(command, args)
}

func isWindows() bool {
	return runtime.GOOS == "windows"
}
