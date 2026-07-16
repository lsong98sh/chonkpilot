package toolhandler

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/internal/models"
	"github.com/chonkpilot/chonkpilot/pkg/executor/codeindex"
	"github.com/chonkpilot/chonkpilot/pkg/executor/engine"
	run "github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/task"
	"github.com/chonkpilot/chonkpilot/pkg/fileversions"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/browser"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
	"go.uber.org/zap"
)

// skipDirs are directories that grep and search_files skip by default.
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	".trae":        true,
	".chonkpilot":  true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
	"build":        true,
	"dist":         true,
	".next":        true,
	".nuxt":        true,
}

// Handler dispatches tool calls to their respective handlers.
type Handler struct {
	Logger          *zap.Logger
	WorkDir         string
	DBDir           string
	Session         string
	TurnID          string
	Engine          *engine.Engine
	MaxDepth        int
	OnProgress      func(map[string]interface{})
	WriteEvent      func(eventType string, payload map[string]interface{})
	Browser         *browser.BrowserManager // browser automation (web_* tools)
	TaskMgr         *run.TaskManager        // async task management
	NoChrome        bool                    // true if Chrome is not available
	CodeIndexer     *codeindex.Indexer      // codebase indexer (nil if disabled)
	FileVersioner   *fileversions.Versioner // file version snapshot (nil if disabled)
	LLMProtocol     string                  // LLM protocol for sub-executors
	LLMModel        string                  // LLM model name
	LLMAPIKey       string                  // LLM API key
	LLMAPIURL       string                  // LLM API base URL
	Thinking        bool                    // reasoning enabled
	ReasoningEffort string                  // reasoning effort level
	toolHandlers    map[string]types.ToolHandler // built-in tool dispatch table
}

// NewHandler creates a new tool handler.
// workDir: file operations root directory (the real project directory).
// dbDir:   DB operations directory (real .ide or temp .ide).
func NewHandler(workDir, dbDir, session, turnID string, logger *zap.Logger) *Handler {
	h := &Handler{
		Logger:   logger,
		WorkDir:  workDir,
		DBDir:    dbDir,
		Session:  session,
		TurnID:   turnID,
		Engine:   engine.NewEngine(workDir, logger),
		MaxDepth: 5,
		Browser:  browser.NewBrowserManager(),
		TaskMgr:  run.NewTaskManager(),
	}
	h.registerBuiltinTools()
	return h
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
func escapeShellArg(s string) string {
	// In cmd.exe, these characters have special meaning outside quotes:
	// & | < > ( ) ^ and must be escaped with ^
	special := []string{"^", "&", "|", "<", ">", "(", ")"}
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

	// Ensure resolved path is within workDir
	if !strings.HasPrefix(resolved, filepath.Clean(h.WorkDir)+string(filepath.Separator)) && resolved != filepath.Clean(h.WorkDir) {
		return "", fmt.Sprintf("path %s is outside workspace %s", userPath, h.WorkDir)
	}
	return resolved, ""
}

// handleExecuteCommand executes a shell command.
func (h *Handler) handleExecuteCommand(args map[string]interface{}) *types.ToolResult {
	cmdStr, _ := args["command"].(string)
	if cmdStr == "" {
		return &types.ToolResult{Success: false, Error: "command is required", Tool: "execute_command"}
	}

	async, _ := args["async"].(bool)
	taskID := h.TaskMgr.StartCommand(h.WorkDir, cmdStr)

	if async {
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("task_id=%s status=running", taskID),
			Tool:    "execute_command",
		}
	}

	// Sync: poll TaskManager until done or cancelled
	// (runs in background so process_task_stop can kill the process)
	for {
		info, err := h.TaskMgr.GetStatus(taskID)
		if err != nil {
			return &types.ToolResult{Success: false, Error: err.Error(), Tool: "execute_command"}
		}
		if info.Status != "running" {
			out := info.Output
			if out == "" {
				out = info.Error
			}
			success := info.Status == "done"
			return &types.ToolResult{
				Success: success,
				Output:  strings.TrimSpace(out),
				Tool:    "execute_command",
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// parseArgs extracts arguments from the args map using JSON marshal/unmarshal for type safety.
func parseArgs[T any](args map[string]interface{}, target *T) error {
	data, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("failed to marshal args: %w", err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("failed to unmarshal args: %w", err)
	}
	return nil
}

// ─── Tool Simplify Functions ───

type executeCommandArgs struct {
	Command string `json:"command"`
	Async   bool   `json:"async,omitempty"`
}

func simplifyExecuteCommand(argsJSON json.RawMessage, result string) string {
	var a executeCommandArgs
	if err := json.Unmarshal(argsJSON, &a); err != nil {
		return "execute_command"
	}
	var tr types.ToolResult
	if err := json.Unmarshal([]byte(result), &tr); err != nil {
		return fmt.Sprintf("execute_command(%s)", types.TruncateStr(a.Command, 80))
	}
	if a.Async {
		return fmt.Sprintf("execute_command(async): %s", tr.Output)
	}
	lines := 0
	if tr.Output != "" {
		lines = strings.Count(tr.Output, "\n")
	}
	if tr.Success {
		return fmt.Sprintf("execute_command(%s): ok, %d lines",
			types.TruncateStr(a.Command, 60), lines)
	}
	return fmt.Sprintf("execute_command(%s): failed, exit=%s",
		types.TruncateStr(a.Command, 60), types.TruncateStr(tr.Error, 80))
}

func init() {
	types.RegisterSimplify("execute_command", simplifyExecuteCommand)
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
