package toolhandler

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/internal/models"
	"github.com/chonkpilot/chonkpilot/pkg/executor/codeindex"
	"github.com/chonkpilot/chonkpilot/pkg/executor/engine"
	run "github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/task"
	"github.com/chonkpilot/chonkpilot/pkg/fileversions"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/batch_llm"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/browser"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/call_llm"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/file"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
	"go.uber.org/zap"
)

// Handler dispatches tool calls to their respective handlers.
type Handler struct {
	Logger          *zap.Logger
	WorkDir         string
	DBDir           string
	Session         string
	TurnID          string
	Engine          *engine.Engine
	MaxDepth        int           // max nested tool call depth
	OnProgress      func(map[string]interface{})
	WriteEvent      func(eventType string, payload map[string]interface{})
	// WaitToolResult blocks until the IDE sends a tool_result command with
	// the given UUID. Used by tools that need async IDE operations (e.g.
	// get_ide_filetree_status requesting a frontend snapshot).
	// If nil, tools that need it will return an error.
	WaitToolResult  func(uuid string) *types.ToolResult
	Browser         *browser.BrowserManager // browser automation (web_* tools)
	TaskMgr         *run.TaskManager        // async task management
	NoChrome        bool                    // true if Chrome is not available
	CodeIndexer     *codeindex.Indexer      // codebase indexer (nil if disabled)
	FileVersioner   *fileversions.Versioner // file version snapshot (nil if disabled)
	CancelCtx       context.Context         // cancellation context, derived from IDE-level CancelChat
	CancelFunc      context.CancelFunc      // cancel function for this handler's execution
	LLMProtocol     string                  // LLM protocol for sub-executors
	LLMModel        string                  // LLM model name
	LLMAPIKey       string                  // LLM API key
	LLMAPIURL       string                  // LLM API base URL
	Thinking        bool                    // reasoning enabled
	ReasoningEffort string                  // reasoning effort level
	// Configurable limits
	TaskPollIntervalMs int // polling interval for async tasks in ms (default 200)
	SearchMaxResults   int // max grep/glob results (default 200)
	FetchMaxBodySizeKB int // max HTTP fetch body size in KB (default 100)
	toolHandlers       map[string]types.ToolHandler // built-in tool dispatch table
}

// NewHandler creates a new tool handler.
// workDir: file operations root directory (the real project directory).
// dbDir:   DB operations directory (real .ide or temp .ide).
func NewHandler(workDir, dbDir, session, turnID string, logger *zap.Logger) *Handler {
	h := &Handler{
		Logger:            logger,
		WorkDir:           workDir,
		DBDir:             dbDir,
		Session:           session,
		TurnID:            turnID,
		Engine:            engine.NewEngine(workDir, logger),
		MaxDepth:          5,
		TaskPollIntervalMs: 200,
		SearchMaxResults:  200,
		FetchMaxBodySizeKB: 100,
		Browser:           browser.NewBrowserManager(),
		TaskMgr:           run.NewTaskManager(),
	}
	h.registerBuiltinTools()
	return h
}

// PropagateConfig propagates Handler config fields to subpackage-level variables.
// Must be called after all Handler fields have been set from UserConfig.
func (h *Handler) PropagateConfig() {
	file.GrepMaxResults = h.SearchMaxResults
	file.FetchDownloadMaxBytes = h.FetchMaxBodySizeKB * 1024
	if h.FetchMaxBodySizeKB > 0 {
		file.SetFetchTimeout(h.FetchMaxBodySizeKB * 10) // rough heuristic: 1KB → 10s timeout
	}
	if h.CancelCtx != nil {
		file.SetCancelCtx(h.CancelCtx)
		browser.SetCancelCtx(h.CancelCtx)
		batch_llm.SetCancelCtx(h.CancelCtx)
		call_llm.SetCancelCtx()
	}
}

// SetCancelContext sets the cancellation context and propagates it to subpackages.
// Called once per turn from executeTurn, before the tool loop starts.
func (h *Handler) SetCancelContext(ctx context.Context) {
	h.CancelCtx, h.CancelFunc = context.WithCancel(ctx)
	file.SetCancelCtx(h.CancelCtx)
	browser.SetCancelCtx(h.CancelCtx)
	batch_llm.SetCancelCtx(h.CancelCtx)
	call_llm.SetCancelCtx()
	if h.Engine != nil {
		h.Engine.SetCancelCtx(h.CancelCtx)
	}
}

// SetSecurityFromDB loads project_security entries from DB and sets the package-level checker.
// Always sets a checker (minimally includes workDir). If DB access fails, the previous checker persists.
func (h *Handler) SetSecurityFromDB() {
	sqlDB, err := db.Open(h.DBDir)
	if err != nil {
		return
	}
	defer db.Close(sqlDB)
	entries, err := db.GetProjectSecurity(sqlDB)
	if err != nil {
		return
	}
	checker := file.NewSecurityChecker(entries, h.WorkDir)
	file.SetSecurityChecker(checker)
	if len(entries) > 0 {
		h.Logger.Info("project security enabled",
			zap.Int("rules", len(entries)))
	} else {
		h.Logger.Debug("project security disabled (workDir only)")
	}
}

// SetNoChrome marks that Chrome is not available on this system.
// Web browser tools will return an error instead of attempting to use the browser.
func (h *Handler) SetNoChrome() {
	h.NoChrome = true
}

// SetCodeIndexer sets the codebase indexer for automatic file indexing.
// When set, write_file/replace will trigger async LLM-based indexing.
func (h *Handler) SetCodeIndexer(idx *codeindex.Indexer) {
	h.CodeIndexer = idx
}

// SetFileVersioner sets the file version snapshotter.
// When set, write_file/replace will snapshot the file before modification.
func (h *Handler) SetFileVersioner(v *fileversions.Versioner) {
	h.FileVersioner = v
}

// FlushCodeIndex batch-enqueues all files marked by MarkChanged to DB.
// Should be called at the end of each turn (tool loop) to batch-process file changes.
func (h *Handler) FlushCodeIndex() int {
	if h.CodeIndexer == nil {
		return 0
	}
	return h.CodeIndexer.FlushChangedFiles()
}

// SetOnProgress sets a callback for progress events (e.g., run_tasks item completion).
func (h *Handler) SetOnProgress(cb func(map[string]interface{})) {
	h.OnProgress = cb
}

// SetWriteEvent sets a callback for writing events upstream (stdout/pipe).
func (h *Handler) SetWriteEvent(cb func(eventType string, payload map[string]interface{})) {
	h.WriteEvent = cb
}

// Dispatch executes a tool call and returns the result.
func (h *Handler) Dispatch(toolName string, args map[string]interface{}, depth int) *types.ToolResult {
	h.Logger.Info("Dispatching tool",
		zap.String("tool", toolName),
		zap.Int("depth", depth),
		zap.Any("args", args),
	)

	// 1. Built-in tools (with Chrome availability check for web tools)
	if fn, ok := h.toolHandlers[toolName]; ok {
		if h.NoChrome && strings.HasPrefix(toolName, "web_") {
			return &types.ToolResult{Success: false, Error: "Chrome/Chromium browser not found on this system. Web browser tools are not available. Install Google Chrome or Microsoft Edge.", Tool: toolName}
		}
		return fn(args, depth)
	}

	// 2. Custom tools registered via add_tool (from DB)
	if tool := getCustomToolFromDB(h.DBDir, toolName); tool != nil {
		switch tool.Type {
		case "mcp":
			return h.handleMCPTool(*tool, args)
		default:
			return h.handleCustomTool(toolName, tool.Command, args)
		}
	}

	// 3. Auto-discovered MCP tools (mcp_<server_name> from global config)
	if strings.HasPrefix(toolName, "mcp_") {
		return h.handleAutoMCPTool(toolName, args)
	}

	return &types.ToolResult{
		Success: false,
		Output:  "",
		Error:   fmt.Sprintf("unknown tool: %s", toolName),
		Tool:    toolName,
	}
}

// getCustomToolFromDB loads a custom tool by name from the project_tools table.
func getCustomToolFromDB(dbDir, toolName string) *models.ToolConfig {
	sqlDB, err := db.Open(dbDir)
	if err != nil {
		return nil
	}
	defer db.Close(sqlDB)

	tools, err := db.GetProjectTools(sqlDB)
	if err != nil {
		return nil
	}

	for _, t := range tools {
		if t.Name == toolName {
			return &t
		}
	}
	return nil
}

// escapeShellArg escapes cmd.exe metacharacters in a string.
// Prevents shell injection when substituting parameter values into command templates.
// Covers: ^ & | < > ( ) % ! — % prevents environment variable expansion (e.g. %PATH%),
// and ! prevents delayed variable expansion in cmd.exe.
func escapeShellArg(s string) string {
	// In cmd.exe, these characters have special meaning outside quotes:
	// & | < > ( ) ^ and must be escaped with ^
	// % triggers environment variable expansion (e.g. %PATH%)
	// ! triggers delayed variable expansion
	special := []string{"^", "&", "|", "<", ">", "(", ")", "%", "!"}
	for _, c := range special {
		s = strings.ReplaceAll(s, c, "^"+c)
	}
	return s
}

// handleCustomTool dispatches a custom tool registered via add_tool.
func (h *Handler) handleCustomTool(name, command string, args map[string]interface{}) *types.ToolResult {
	// Build command string from template, substituting parameters
	cmdStr := command
	for k, v := range args {
		placeholder := fmt.Sprintf("{%s}", k)
		// Escape shell metacharacters to prevent injection
		escaped := escapeShellArg(fmt.Sprintf("%v", v))
		cmdStr = strings.ReplaceAll(cmdStr, placeholder, escaped)
	}

	result := h.Engine.ExecuteShell(cmdStr)
	out := result.Output
	if out == "" {
		out = result.Error
	}
	return &types.ToolResult{
		Success: result.Success,
		Output:  fmt.Sprintf("[%s] %s", name, strings.TrimSpace(out)),
		Tool:    name,
	}
}

// sanitizePath resolves a user-provided path and ensures it stays within workDir.
// On Windows, comparison is case-insensitive to handle drive letter case differences.
// Returns the resolved absolute path, or empty string with an error message.
func (h *Handler) sanitizePath(userPath string) (string, string) {
	if userPath == "" {
		return "", "path is required"
	}
	resolved := userPath
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(h.WorkDir, resolved)
	}
	resolved = filepath.Clean(resolved)

	workDirClean := filepath.Clean(h.WorkDir)
	prefix := workDirClean + string(filepath.Separator)

	var inside bool
	if runtime.GOOS == "windows" {
		inside = strings.HasPrefix(strings.ToLower(resolved), strings.ToLower(prefix)) ||
			strings.EqualFold(resolved, workDirClean)
	} else {
		inside = strings.HasPrefix(resolved, prefix) || resolved == workDirClean
	}
	if !inside {
		return "", fmt.Sprintf("path %s is outside workspace %s", userPath, h.WorkDir)
	}
	return resolved, ""
}

// handleExecuteCommand executes a shell command.
func (h *Handler) handleExecuteCommand(args map[string]interface{}) *types.ToolResult {
	cmdStr, _ := args["command"].(string)
	if cmdStr == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "command is required",
			Tool:    "execute_command",
			Output:  "❌ 缺少 command 参数",
			RawResult: map[string]interface{}{
				"error": "command is required",
			},
		}
	}

	async, _ := args["async"].(bool)

	// cwd: optional custom working directory (cross-platform, overrides project root)
	workDir := h.WorkDir
	if cwd, ok := args["cwd"].(string); ok && cwd != "" {
		workDir = cwd
	}

	// env: custom environment variables
	var env map[string]string
	if envRaw, ok := args["env"].(map[string]interface{}); ok && len(envRaw) > 0 {
		env = make(map[string]string, len(envRaw))
		for k, v := range envRaw {
			env[k] = fmt.Sprint(v)
		}
	}

	// timeout: seconds before auto-converting to async
	timeoutSec := 0
	if t, ok := args["timeout"].(float64); ok && t > 0 {
		timeoutSec = int(t)
	} else if t, ok := args["timeout"].(int); ok && t > 0 {
		timeoutSec = t
	}

	taskID := h.TaskMgr.StartCommand(workDir, cmdStr, env)

	if async {
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("⚙️ 命令已异步启动（task_id=%s）", taskID),
			Tool:    "execute_command",
			RawResult: map[string]interface{}{
				"task_id": taskID,
				"command": cmdStr,
				"status":  "running",
				"async":   true,
			},
		}
	}

	// Sync: poll TaskManager until done or cancelled
	// (runs in background so process_task_stop can kill the process)
	// If timeout is set, auto-convert to async when the deadline is reached.

	pollInterval := time.Duration(h.TaskPollIntervalMs) * time.Millisecond
	var timeoutCh <-chan time.Time
	if timeoutSec > 0 {
		timer := time.NewTimer(time.Duration(timeoutSec) * time.Second)
		defer timer.Stop()
		timeoutCh = timer.C
	}

	for {
		select {
		case <-timeoutCh:
			// Timeout reached: switch to async mode
			info, err := h.TaskMgr.GetStatus(taskID)
			partialOutput := ""
			elapsed := float64(timeoutSec)
			if err == nil {
				partialOutput = info.Output
				elapsed = float64(info.Elapsed)
			}
			timeoutResult := map[string]interface{}{
				"status":         "timeout",
				"task_id":        taskID,
				"partial_output": partialOutput,
				"elapsed":        elapsed,
				"message":        fmt.Sprintf("命令未在 %ds 内完成，已转异步。后续可用 process_task_status 查询，或 process_task_stop 终止", timeoutSec),
			}
			out, _ := json.Marshal(timeoutResult)
			return &types.ToolResult{
				Success:   true,
				Output:    string(out),
				Tool:      "execute_command",
				RawResult: timeoutResult,
			}

		default:
			info, err := h.TaskMgr.GetStatus(taskID)
			if err != nil {
				return &types.ToolResult{
					Success: false,
					Error:   err.Error(),
					Tool:    "execute_command",
					Output:  "❌ 查询任务状态失败",
					RawResult: map[string]interface{}{
						"error":   err.Error(),
						"task_id": taskID,
					},
				}
			}
			if info.Status != "running" {
				out := info.Output
				if out == "" {
					out = info.Error
				}
				success := info.Status == "done"
				emoji := "✅"
				if !success {
					emoji = "❌"
				}
				return &types.ToolResult{
					Success: success,
					Output:  fmt.Sprintf("%s 命令执行完成（task_id=%s, status=%s, duration=%ds）\n\n%s", emoji, taskID, info.Status, info.Elapsed, strings.TrimSpace(out)),
					Tool:    "execute_command",
					RawResult: map[string]interface{}{
						"task_id": taskID,
						"command": cmdStr,
						"stdout":  info.Output,
						"stderr":  info.Error,
						"status":  info.Status,
						"pid":     info.PID,
						"elapsed": info.Elapsed,
					},
				}
			}
			time.Sleep(pollInterval)
		}
	}
}

// ─── Tool Simplify Functions ───

func init() {
	types.RegisterSimplify("batch_llm", simplifyBatchLLM)
}

func simplifyBatchLLM(args json.RawMessage, result string) string {
	var a struct {
		Filename string `json:"filename"`
		Count    int    `json:"count"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "batch_llm"
	}
	count := a.Count
	if count <= 0 {
		count = 1
	}
	var tr types.ToolResult
	if err := json.Unmarshal([]byte(result), &tr); err != nil {
		return fmt.Sprintf("batch_llm(%s, count=%d)", types.TruncateStr(a.Filename, 40), count)
	}
	if tr.Success {
		return fmt.Sprintf("batch_llm(%s, count=%d): %s", types.TruncateStr(a.Filename, 30), count, types.TruncateStr(tr.Output, 60))
	}
	return fmt.Sprintf("batch_llm(%s, count=%d): %s", types.TruncateStr(a.Filename, 30), count, types.TruncateStr(tr.Error, 60))
}
