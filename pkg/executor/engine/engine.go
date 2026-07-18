package engine

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// decodeWinConsole converts a byte slice from the Windows console code page to UTF-8.
// On English/other-language Windows, it falls through if the output is already valid UTF-8
// or if decoding as GBK/CP936 fails.
func decodeWinConsole(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	// Fast path: valid UTF-8, no conversion needed
	if utf8.Valid(raw) {
		return string(raw)
	}

	// Try GBK (CodePage 936, common on Chinese Windows)
	// Use simplifiedchinese.HZGB2312 / simplifiedchinese.GBK
	result, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), raw)
	if err == nil {
		return string(result)
	}

	// Fallback: try CodePage 850 (Western European) which is common on non-Chinese systems
	result, _, err = transform.Bytes(charmap.CodePage850.NewDecoder(), raw)
	if err == nil {
		return string(result)
	}

	// Last resort: return the original bytes as-is (might still have garbled chars)
	return string(raw)
}

// decodeWinStderr decodes stderr output; same as decodeWinConsole but may emit a warning.
func decodeWinStderr(raw []byte) string {
	return decodeWinConsole(raw)
}

// ToolResult holds the result of a tool execution.
type ToolResult struct {
	Success    bool   `json:"success"`
	Output     string `json:"output"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"duration_ms"`
}

// Engine executes tool calls from the LLM.
type Engine struct {
	logger         *zap.Logger
	WorkDir        string
	CommandTimeout time.Duration // max execution time per command; 0 = no limit
	cancelCtx      context.Context // cancellation context from Handler.SetCancelContext
}

// NewEngine creates a new tool execution engine.
// workDir is the working directory for executed commands.
func NewEngine(workDir string, logger *zap.Logger) *Engine {
	return &Engine{
		logger:  logger,
		WorkDir: workDir,
	}
}

// SetCancelCtx sets the cancellation context so ExecuteShell can be aborted
// when CancelChat is triggered (e.g., for custom tools / MCP tools).
func (e *Engine) SetCancelCtx(ctx context.Context) {
	e.cancelCtx = ctx
}

// Execute runs a tool command and returns the result.
func (e *Engine) Execute(command string, args []string) *ToolResult {
	start := time.Now()

	ctx := context.Background()
	if e.cancelCtx != nil {
		// Merge cancel context so CommandTimeout still applies independently
		var cancel context.CancelFunc
		if e.CommandTimeout > 0 {
			ctx, cancel = context.WithTimeout(e.cancelCtx, e.CommandTimeout)
		} else {
			ctx, cancel = context.WithCancel(e.cancelCtx)
		}
		defer cancel()
	} else if e.CommandTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.CommandTimeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, command, args...)
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
		stdoutStr := decodeWinConsole(stdout.Bytes())
		stderrStr := decodeWinStderr(stderr.Bytes())
		return &ToolResult{
			Success:    false,
			Output:     stdoutStr,
			Error:      fmt.Sprintf("%s: %s", err.Error(), stderrStr),
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
		Output:     decodeWinConsole(stdout.Bytes()),
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
