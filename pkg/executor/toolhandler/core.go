package toolhandler

import (
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/agent"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/batch_llm"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/browser"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/call_llm"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/desktop"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/file"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/llmresult"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/note"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/restore"
	run "github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/task"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/tool"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/writeimage"
)

// registerBuiltinTools populates the dispatch table for all built-in tools.
func (h *Handler) registerBuiltinTools() {
	h.toolHandlers = map[string]types.ToolHandler{}
	h.registerFileTools()
	h.registerSearchTools()
	h.registerExecutionTools()
	h.registerNoteTools()
	h.registerCodebaseTools()
	h.registerFileOpsTools()
	h.registerAgentTools()
	h.registerCustomToolTools()
	h.registerDesktopTools()
	h.registerBrowserTools()
}

// registerFileTools registers read/write/patch file operations.
func (h *Handler) registerFileTools() {
	h.toolHandlers["read_file"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return file.HandleReadFile(h.WorkDir, args)
	})
	h.toolHandlers["write_file"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return file.HandleWriteFile(h.WorkDir, h.CodeIndexer, h.FileVersioner, h.TurnID, args)
	})
	h.toolHandlers["replace"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return file.HandleReplace(h.WorkDir, h.Engine, h.CodeIndexer, h.FileVersioner, h.TurnID, args)
	})
	h.toolHandlers["diff"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return file.HandleDiff(h.WorkDir, args)
	})
	h.toolHandlers["patch"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return file.HandlePatch(h.WorkDir, h.FileVersioner, h.TurnID, args)
	})
}

// registerSearchTools registers grep, search_files, list_directory, fetch.
func (h *Handler) registerSearchTools() {
	h.toolHandlers["grep"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return file.HandleGrep(h.WorkDir, args)
	})
	h.toolHandlers["search_files"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return file.HandleSearchFiles(h.WorkDir, args)
	})
	h.toolHandlers["list_directory"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return file.HandleListDirectory(h.WorkDir, args)
	})
	h.toolHandlers["fetch"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return file.HandleFetch(h.WorkDir, args, file.FetchConfig{
			MaxBodySize: h.FetchMaxBodySizeKB * 1024,
		})
	})
}

// registerExecutionTools registers execute_command, run_tasks, call_llm, ask_user, task management.
func (h *Handler) registerExecutionTools() {
	h.toolHandlers["execute_command"] = types.Wrap(h.handleExecuteCommand)
	h.toolHandlers["run_tasks"] = types.DepthAware(func(args map[string]interface{}, depth int) *types.ToolResult {
		return call_llm.HandleForeach(h.TaskMgr, h.Dispatch, h.OnProgress, args, depth)
	})
	h.toolHandlers["call_llm"] = types.DepthAware(func(args map[string]interface{}, depth int) *types.ToolResult {
		return call_llm.HandleCallLLM(h.Logger, h.Session, h.TurnID, h.WorkDir, h.DBDir,
			h.TaskMgr,
			h.LLMProtocol, h.LLMModel, h.LLMAPIKey, h.LLMAPIURL,
			h.Thinking, h.ReasoningEffort,
			h.WriteEvent, h.OnProgress, h.CodeIndexer, args, depth,
			h.Dispatch)
	})
	h.toolHandlers["ask_user"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return call_llm.HandleAskUser(h.WriteEvent, args)
	})
	h.toolHandlers["query_task"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return run.HandleQueryTask(h.TaskMgr, args)
	})
	h.toolHandlers["process_wait"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return run.HandleProcessWait(h.TaskMgr, args)
	})
	h.toolHandlers["process_task_status"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return run.HandleProcessTaskStatus(h.TaskMgr, args)
	})
	h.toolHandlers["process_task_stop"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return run.HandleProcessTaskStop(h.TaskMgr, args)
	})
}

// registerNoteTools registers note CRUD operations.
func (h *Handler) registerNoteTools() {
	h.toolHandlers["note_write"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return note.HandleNoteWrite(h.WorkDir, args)
	})
	h.toolHandlers["note_read"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return note.HandleNoteRead(h.WorkDir, args)
	})
	h.toolHandlers["note_list"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return note.HandleNoteList(h.WorkDir, args)
	})
	h.toolHandlers["note_delete"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return note.HandleNoteDelete(h.WorkDir, args)
	})
}

// registerCodebaseTools registers the codebase query tool.
func (h *Handler) registerCodebaseTools() {
	h.toolHandlers["query_codebase"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return file.HandleQueryCodebase(h.CodeIndexer, args)
	})
	h.toolHandlers["get_llm_result"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return llmresult.HandleGetLLMResult(h.DBDir, args)
	})
	h.toolHandlers["batch_llm"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return batch_llm.HandleBatchLLM(
			h.Logger, h.WorkDir, h.DBDir, h.Session,
			h.LLMProtocol, h.LLMModel, h.LLMAPIKey, h.LLMAPIURL,
			h.Thinking, h.ReasoningEffort,
			h.WriteEvent,
			func(toolName string, a map[string]interface{}, depth int) *types.ToolResult {
				return h.Dispatch(toolName, a, depth)
			},
			h.NoChrome,
			h.CodeIndexer,
			args,
		).ToToolResult()
	})
}

// registerFileOpsTools registers rollback, make_directory, remove, rename, write_image.
func (h *Handler) registerFileOpsTools() {
	h.toolHandlers["rollback"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return restore.HandleRollback(h.FileVersioner, h.TurnID, args)
	})
	h.toolHandlers["make_directory"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return file.HandleMakeDirectory(h.WorkDir, args)
	})
	h.toolHandlers["remove"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return file.HandleRemove(h.WorkDir, h.FileVersioner, h.TurnID, args)
	})
	h.toolHandlers["rename"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return file.HandleRename(h.WorkDir, h.FileVersioner, h.TurnID, args)
	})
	h.toolHandlers["write_image"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return writeimage.HandleWriteImage(h.WorkDir, h.NoChrome, args)
	})
}

// registerAgentTools registers add_agent, list_agent, delete_agent.
func (h *Handler) registerAgentTools() {
	h.toolHandlers["add_agent"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return agent.HandleAddAgent(h.DBDir, args)
	})
	h.toolHandlers["list_agent"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return agent.HandleListAgent(h.DBDir, args)
	})
	h.toolHandlers["delete_agent"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return agent.HandleDeleteAgent(h.DBDir, args)
	})
}

// registerCustomToolTools registers add_tool, list_tool, get_tool, delete_tool.
func (h *Handler) registerCustomToolTools() {
	h.toolHandlers["add_tool"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return tool.HandleAddTool(h.DBDir, args)
	})
	h.toolHandlers["list_tool"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return tool.HandleListTool(h.DBDir, args)
	})
	h.toolHandlers["get_tool"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return tool.HandleGetTool(h.DBDir, args)
	})
	h.toolHandlers["delete_tool"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return tool.HandleDeleteTool(h.DBDir, args)
	})
}

// registerDesktopTools registers screenshot, mouse, keyboard, window operations.
func (h *Handler) registerDesktopTools() {
	h.toolHandlers["screenshot"] = types.Wrap(desktop.HandleScreenshot)
	h.toolHandlers["mouse_click"] = types.Wrap(desktop.HandleMouseClick)
	h.toolHandlers["mouse_down"] = types.Wrap(desktop.HandleMouseDown)
	h.toolHandlers["mouse_up"] = types.Wrap(desktop.HandleMouseUp)
	h.toolHandlers["mouse_move"] = types.Wrap(desktop.HandleMouseMove)
	h.toolHandlers["scroll_wheel"] = types.Wrap(desktop.HandleScrollWheel)
	h.toolHandlers["type_text"] = types.Wrap(desktop.HandleTypeText)
	h.toolHandlers["key_press"] = types.Wrap(desktop.HandleKeyPress)
	h.toolHandlers["key_down"] = types.Wrap(desktop.HandleKeyDown)
	h.toolHandlers["key_up"] = types.Wrap(desktop.HandleKeyUp)
	h.toolHandlers["find_window"] = types.Wrap(desktop.HandleFindWindow)
	h.toolHandlers["list_windows"] = types.Wrap(desktop.HandleListWindows)
	h.toolHandlers["get_window_rect"] = types.Wrap(desktop.HandleGetWindowRectFn)
	h.toolHandlers["set_window_rect"] = types.Wrap(desktop.HandleSetWindowRect)
	h.toolHandlers["focus_window"] = types.Wrap(desktop.HandleFocusWindow)
	h.toolHandlers["minimize_window"] = types.Wrap(desktop.HandleMinimizeWindow)
	h.toolHandlers["maximize_window"] = types.Wrap(desktop.HandleMaximizeWindow)
	h.toolHandlers["restore_window"] = types.Wrap(desktop.HandleRestoreWindow)
}

// registerBrowserTools registers all web browser automation tools.
func (h *Handler) registerBrowserTools() {
	h.toolHandlers["web_start"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebStart(h.Browser, args)
	})
	h.toolHandlers["web_open"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebOpen(h.Browser, h.TaskMgr, h.Logger, args)
	})
	h.toolHandlers["web_screenshot"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebScreenshot(h.Browser, args)
	})
	h.toolHandlers["web_close"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebClose(h.Browser, args)
	})
	h.toolHandlers["web_click"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebClick(h.Browser, h.TaskMgr, args)
	})
	h.toolHandlers["web_type"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebType(h.Browser, h.TaskMgr, args)
	})
	h.toolHandlers["web_hover"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebHover(h.Browser, h.TaskMgr, args)
	})
	h.toolHandlers["web_drag"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebDrag(h.Browser, h.TaskMgr, args)
	})
	h.toolHandlers["web_mouse_down"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebMouseDown(h.Browser, args)
	})
	h.toolHandlers["web_mouse_up"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebMouseUp(h.Browser, args)
	})
	h.toolHandlers["web_mouse_move"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebMouseMove(h.Browser, args)
	})
	h.toolHandlers["web_scroll_wheel"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebScrollWheel(h.Browser, args)
	})
	h.toolHandlers["web_scroll_to"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebScrollTo(h.Browser, h.TaskMgr, args)
	})
	h.toolHandlers["web_evaluate"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebEvaluate(h.Browser, h.TaskMgr, args)
	})
	h.toolHandlers["web_get_text"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebGetText(h.Browser, args)
	})
	h.toolHandlers["web_get_html"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebGetHTML(h.Browser, args)
	})
	h.toolHandlers["web_get_style"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebGetStyle(h.Browser, args)
	})
	h.toolHandlers["web_get_url"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebGetURL(h.Browser, args)
	})
	h.toolHandlers["web_get_title"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebGetTitle(h.Browser, args)
	})
	h.toolHandlers["web_get_console"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebGetConsole(h.Browser, args)
	})
	h.toolHandlers["web_get_requests"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebGetRequests(h.Browser, args)
	})
	h.toolHandlers["web_wait_selector"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebWaitSelector(h.Browser, h.TaskMgr, args)
	})
	h.toolHandlers["web_wait_navigation"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebWaitNavigation(h.Browser, h.TaskMgr, args)
	})
	h.toolHandlers["web_set_viewport"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebSetViewport(h.Browser, args)
	})
	h.toolHandlers["web_set_geolocation"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebSetGeolocation(h.Browser, args)
	})
	h.toolHandlers["web_grant_permission"] = types.Wrap(func(args map[string]interface{}) *types.ToolResult {
		return browser.HandleWebGrantPermission(h.Browser, args)
	})
}
