package task

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// HandleProcessWait handles the process_wait tool.
func HandleProcessWait(tm *TaskManager, args map[string]interface{}) *types.ToolResult {
	if taskID, ok := args["task_id"].(string); ok && taskID != "" {
		timeout := 30.0
		if v, ok := args["timeout"].(float64); ok && v > 0 {
			timeout = v
		}
		if timeout > 300 {
			timeout = 300
		}
		start := time.Now()
		for time.Since(start).Seconds() < timeout {
			if info, err := tm.GetStatus(taskID); err == nil {
				if info.Status != "running" {
					out := fmt.Sprintf("status=%s elapsed=%ds", info.Status, info.Elapsed)
					if info.Output != "" {
						out += "\n" + info.Output
					}
					return &types.ToolResult{Success: true, Output: out, Tool: "process_wait"}
				}
			}
			if item, err := tm.GetRunTaskSubItem(taskID); err == nil {
				if item.Status == "done" || item.Status == "error" {
					out := fmt.Sprintf("item_id=%s status=%s", item.ItemID, item.Status)
					if item.Output != "" {
						out += "\n" + item.Output
					}
					return &types.ToolResult{Success: true, Output: out, Tool: "process_wait"}
				}
			}
			if state, err := tm.GetRunTaskState(taskID); err == nil {
				if state.Status != "running" {
					return &types.ToolResult{Success: true, Output: fmt.Sprintf("status=%s completed=%d failed=%d total=%d elapsed=%s",
						state.Status, state.Completed, state.Failed, state.Total, state.Elapsed), Tool: "process_wait"}
				}
			}
			time.Sleep(1 * time.Second)
		}
		return &types.ToolResult{Success: true, Output: fmt.Sprintf("timeout: task %s still running after %.0fs", taskID, timeout), Tool: "process_wait"}
	}

	duration := 1.0
	if v, ok := args["duration"].(float64); ok && v > 0 {
		duration = v
	}
	if duration > 60 {
		duration = 60
	}
	time.Sleep(time.Duration(duration * float64(time.Second)))
	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("waited %.1f seconds", duration),
		Tool:    "process_wait",
	}
}

// HandleProcessTaskStatus handles the process_task_status tool.
func HandleProcessTaskStatus(tm *TaskManager, args map[string]interface{}) *types.ToolResult {
	taskID, _ := args["task_id"].(string)
	if taskID == "" {
		return &types.ToolResult{Success: false, Error: "task_id is required", Tool: "process_task_status"}
	}

	if info, err := tm.GetStatus(taskID); err == nil {
		out := fmt.Sprintf("status=%s elapsed=%ds", info.Status, info.Elapsed)
		if info.Output != "" {
			outputStr := info.Output
			if len(outputStr) > 102400 {
				outPath := filepath.Join(".ide", "tmp", fmt.Sprintf("process_task_status_%s_%d.out", taskID, time.Now().UnixMilli()))
				if err := os.MkdirAll(filepath.Dir(outPath), 0755); err == nil {
					os.WriteFile(outPath, []byte(outputStr), 0644)
					outputStr = fmt.Sprintf("(output %d bytes > 100KB, written to %s)", len(outputStr), outPath)
				} else {
					outputStr = outputStr[:102400] + "\n...(truncated)"
				}
			}
			out += "\n" + outputStr
		}
		if info.Error != "" {
			out += "\nerror: " + info.Error
		}
		return &types.ToolResult{Success: true, Output: out, Tool: "process_task_status"}
	}

	if item, err := tm.GetRunTaskSubItem(taskID); err == nil {
		out := fmt.Sprintf("item_id=%s tool=%s status=%s", item.ItemID, item.Tool, item.Status)
		if item.Output != "" {
			outputStr := item.Output
			if len(outputStr) > 102400 {
				outPath := filepath.Join(".ide", "tmp", fmt.Sprintf("process_task_status_sub_%s_%d.out", taskID, time.Now().UnixMilli()))
				if err := os.MkdirAll(filepath.Dir(outPath), 0755); err == nil {
					os.WriteFile(outPath, []byte(outputStr), 0644)
					outputStr = fmt.Sprintf("(output %d bytes > 100KB, written to %s)", len(outputStr), outPath)
				} else {
					outputStr = outputStr[:102400] + "\n...(truncated)"
				}
			}
			out += "\n" + outputStr
		}
		if item.Error != "" {
			out += "\nerror: " + item.Error
		}
		return &types.ToolResult{Success: true, Output: out, Tool: "process_task_status"}
	}

	if state, err := tm.GetRunTaskState(taskID); err == nil {
		out := fmt.Sprintf("status=%s completed=%d failed=%d total=%d elapsed=%s",
			state.Status, state.Completed, state.Failed, state.Total, state.Elapsed)
		return &types.ToolResult{Success: true, Output: out, Tool: "process_task_status"}
	}

	return &types.ToolResult{Success: false, Error: fmt.Sprintf("task not found: %s", taskID), Tool: "process_task_status"}
}

// HandleProcessTaskStop handles the process_task_stop tool.
func HandleProcessTaskStop(tm *TaskManager, args map[string]interface{}) *types.ToolResult {
	taskID, _ := args["task_id"].(string)
	processID, _ := args["process_id"].(float64)

	if processID > 0 {
		pid := int(processID)
		proc, err := os.FindProcess(pid)
		if err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("process %d not found: %s", pid, err.Error()), Tool: "process_task_stop"}
		}
		if err := proc.Kill(); err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to kill process %d: %s", pid, err.Error()), Tool: "process_task_stop"}
		}
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("process %d killed", pid),
			Tool:    "process_task_stop",
		}
	}

	if taskID == "" {
		return &types.ToolResult{Success: false, Error: "task_id or process_id is required", Tool: "process_task_stop"}
	}

	err := tm.Stop(taskID)
	if err == nil {
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("task %s stopped", taskID),
			Tool:    "process_task_stop",
		}
	}

	err = tm.StopRunTask(taskID)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "process_task_stop"}
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("task %s stopped", taskID),
		Tool:    "process_task_stop",
	}
}

// HandleQueryTask handles the query_task tool.
func HandleQueryTask(tm *TaskManager, args map[string]interface{}) *types.ToolResult {
	if taskIDs, ok := args["task_ids"].([]interface{}); ok && len(taskIDs) > 0 {
		results := make(map[string]interface{})
		for _, tid := range taskIDs {
			id, _ := tid.(string)
			if id == "" {
				continue
			}
			if state, err := tm.GetRunTaskState(id); err == nil {
				results[id] = map[string]interface{}{
					"status":    state.Status,
					"total":     state.Total,
					"completed": state.Completed,
					"failed":    state.Failed,
					"elapsed":   state.Elapsed,
				}
			} else if info, err := tm.GetStatus(id); err == nil {
				results[id] = map[string]interface{}{
					"status":  info.Status,
					"elapsed": info.Elapsed,
					"output":  info.Output,
				}
			} else {
				results[id] = map[string]interface{}{"error": "not found"}
			}
		}
		resultJSON, _ := json.Marshal(map[string]interface{}{"tasks": results})
		return &types.ToolResult{Success: true, Output: string(resultJSON), Tool: "query_task"}
	}

	taskID, _ := args["task_id"].(string)
	if taskID == "" {
		return &types.ToolResult{Success: false, Error: "task_id or task_ids is required", Tool: "query_task"}
	}

	state, err := tm.GetRunTaskState(taskID)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "query_task"}
	}

	querySubTask := false
	if v, ok := args["query_sub_task"].(bool); ok {
		querySubTask = v
	}

	var result interface{}
	if querySubTask {
		result = state
	} else {
		result = map[string]interface{}{
			"task_id":   state.ID,
			"status":    state.Status,
			"total":     state.Total,
			"completed": state.Completed,
			"failed":    state.Failed,
			"elapsed":   state.Elapsed,
		}
	}

	resultJSON, _ := json.Marshal(result)
	return &types.ToolResult{
		Success: true,
		Output:  string(resultJSON),
		Tool:    "query_task",
	}
}

func init() {
	types.RegisterSimplify("process_wait", simplifyProcessWait)
	types.RegisterSimplify("process_task_status", types.SimpleAction("process_task_status"))
	types.RegisterSimplify("process_task_stop", types.SimpleAction("process_task_stop"))
	types.RegisterSimplify("query_task", simplifyQueryTask)
}

func simplifyProcessWait(argsJSON json.RawMessage, result string) string {
	var tr types.ToolResult
	if err := json.Unmarshal([]byte(result), &tr); err != nil {
		return "process_wait"
	}
	if tr.Success {
		return fmt.Sprintf("process_wait: done, %d chars", len(tr.Output))
	}
	return fmt.Sprintf("process_wait: failed, %s", types.TruncateStr(tr.Error, 80))
}

func simplifyQueryTask(argsJSON json.RawMessage, result string) string {
	var tr types.ToolResult
	if err := json.Unmarshal([]byte(result), &tr); err != nil {
		return "query_task"
	}
	if !tr.Success {
		return fmt.Sprintf("query_task: failed, %s", types.TruncateStr(tr.Error, 80))
	}
	// tr.Output contains the result JSON; try to extract status info
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(tr.Output), &data); err == nil {
		parts := []string{"query_task"}
		if s, _ := data["status"].(string); s != "" {
			parts = append(parts, s)
		}
		if c, _ := data["completed"].(float64); c > 0 {
			t, _ := data["total"].(float64)
			parts = append(parts, fmt.Sprintf("%.0f/%.0f", c, t))
		}
		return strings.Join(parts, " ")
	}
	return "query_task"
}
