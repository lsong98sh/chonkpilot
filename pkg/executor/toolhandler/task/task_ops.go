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
					out := fmt.Sprintf("✅ process_wait 完成：status=%s elapsed=%ds", info.Status, info.Elapsed)
					if info.Output != "" {
						out += "\n" + info.Output
					}
					return &types.ToolResult{
						Success: true,
						Output:  out,
						Tool:    "process_wait",
						RawResult: map[string]interface{}{
							"task_id": taskID,
							"status":  info.Status,
							"elapsed": info.Elapsed,
						},
					}
				}
			}
			if item, err := tm.GetRunTaskSubItem(taskID); err == nil {
				if item.Status == "done" || item.Status == "error" {
					out := fmt.Sprintf("✅ process_wait 完成：item_id=%s status=%s", item.ItemID, item.Status)
					if item.Output != "" {
						out += "\n" + item.Output
					}
					return &types.ToolResult{
						Success: true,
						Output:  out,
						Tool:    "process_wait",
						RawResult: map[string]interface{}{
							"task_id":  taskID,
							"item_id":  item.ItemID,
							"status":   item.Status,
						},
					}
				}
			}
			if state, err := tm.GetRunTaskState(taskID); err == nil {
				if state.Status != "running" {
					return &types.ToolResult{
						Success: true,
						Output:  fmt.Sprintf("✅ process_wait 完成：status=%s completed=%d failed=%d total=%d elapsed=%s", state.Status, state.Completed, state.Failed, state.Total, state.Elapsed),
						Tool:    "process_wait",
						RawResult: map[string]interface{}{
							"task_id":   taskID,
							"status":    state.Status,
							"completed": state.Completed,
							"failed":    state.Failed,
							"total":     state.Total,
							"elapsed":   state.Elapsed,
						},
					}
				}
			}
			time.Sleep(1 * time.Second)
		}
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("⏱️ process_wait 超时：task %s 仍在运行（%.0fs）", taskID, timeout),
			Tool:    "process_wait",
			RawResult: map[string]interface{}{
				"task_id": taskID,
				"status":  "timeout",
			},
		}
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
		Output:  fmt.Sprintf("⏱️ 等待了 %.1f 秒", duration),
		Tool:    "process_wait",
		RawResult: map[string]interface{}{
			"duration": duration,
		},
	}
}

// HandleProcessTaskStatus handles the process_task_status tool.
// When follow=true, polls every 1 second returning new output incrementally
// until task completes or timeout (default 30s) is reached.
func HandleProcessTaskStatus(tm *TaskManager, args map[string]interface{}) *types.ToolResult {
	taskID, _ := args["task_id"].(string)
	if taskID == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "task_id is required",
			Tool:    "process_task_status",
			Output:  "❌ 缺少 task_id 参数",
			RawResult: map[string]interface{}{
				"error": "task_id is required",
			},
		}
	}

	follow, _ := args["follow"].(bool)
	timeoutSec := 30
	if t, ok := args["timeout"].(float64); ok && t > 0 {
		timeoutSec = int(t)
	}

	if !follow {
		return queryTaskStatusOnce(tm, taskID)
	}

	// Follow mode: poll every 1 second, return new output incrementally
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	startTime := time.Now()
	var lastOutputLen int
	var parts []string

	// Only supported for regular command tasks (GetStatus)
	if info, err := tm.GetStatus(taskID); err != nil {
		return queryTaskStatusOnce(tm, taskID)
	} else {
		// Initial output snapshot with >100KB truncation
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
		lastOutputLen = len(info.Output)

		if outputStr != "" {
			parts = append(parts, fmt.Sprintf("[初始输出快照]:\n%s", outputStr))
		}

		if info.Status != "running" {
			// Already done
			emoji := "✅"
			if info.Status == "error" {
				emoji = "❌"
			}
			out := fmt.Sprintf("%s 状态：status=%s elapsed=%ds", emoji, info.Status, info.Elapsed)
			if info.Error != "" {
				out += "\nerror: " + info.Error
			}
			if len(parts) > 0 {
				out += "\n\n" + strings.Join(parts, "\n\n")
			}
			return &types.ToolResult{
				Success: true,
				Output:  out,
				Tool:    "process_task_status",
				RawResult: map[string]interface{}{
					"task_id": taskID,
					"status":  info.Status,
					"elapsed": info.Elapsed,
				},
			}
		}
	}

	// Polling loop
	for {
		time.Sleep(1 * time.Second)

		if time.Now().After(deadline) {
			parts = append(parts, "[超时，后续可用 process_task_status 继续查询]")
			break
		}

		info, err := tm.GetStatus(taskID)
		if err != nil {
			break
		}

		// Check for new output (with bounds protection)
		newOutput := ""
		if len(info.Output) > lastOutputLen {
			newOutput = info.Output[lastOutputLen:]
		} else if len(info.Output) < lastOutputLen {
			// Output was truncated (e.g. buffer reset), return full output
			newOutput = info.Output
		}
		lastOutputLen = len(info.Output)

		if info.Status == "running" && newOutput != "" {
			elapsed := int(time.Since(startTime).Seconds())
			parts = append(parts, fmt.Sprintf("[等待 %ds 后新输出]:\n%s", elapsed, newOutput))
		}

		if info.Status != "running" {
			if newOutput != "" {
				elapsed := int(time.Since(startTime).Seconds())
				parts = append(parts, fmt.Sprintf("[等待 %ds 后新输出]:\n%s", elapsed, newOutput))
			}
			parts = append(parts, fmt.Sprintf("[任务已完成 - %s]", info.Status))
			emoji := "✅"
			if info.Status == "error" {
				emoji = "❌"
			}
			out := fmt.Sprintf("%s 状态：status=%s elapsed=%ds", emoji, info.Status, info.Elapsed)
			if info.Error != "" {
				out += "\nerror: " + info.Error
			}
			out += "\n\n" + strings.Join(parts, "\n\n")
			return &types.ToolResult{
				Success: true,
				Output:  out,
				Tool:    "process_task_status",
				RawResult: map[string]interface{}{
					"task_id": taskID,
					"status":  info.Status,
					"elapsed": info.Elapsed,
				},
			}
		}
	}

	// Timeout or error: return what we have
	info, err := tm.GetStatus(taskID)
	if err == nil {
		emoji := "✅"
		if info.Status == "error" {
			emoji = "❌"
		}
		out := fmt.Sprintf("%s 状态：status=%s elapsed=%ds", emoji, info.Status, info.Elapsed)
		if info.Error != "" {
			out += "\nerror: " + info.Error
		}
		if len(parts) > 0 {
			out += "\n\n" + strings.Join(parts, "\n\n")
		}
		return &types.ToolResult{
			Success: true,
			Output:  out,
			Tool:    "process_task_status",
			RawResult: map[string]interface{}{
				"task_id": taskID,
				"status":  info.Status,
				"elapsed": info.Elapsed,
			},
		}
	}
	return &types.ToolResult{
		Success: true,
		Output:  "⚠️ " + strings.Join(parts, "\n\n"),
		Tool:    "process_task_status",
		RawResult: map[string]interface{}{
			"task_id": taskID,
		},
	}
}

// queryTaskStatusOnce performs a single query of task status (non-follow mode).
// Handles regular tasks, sub-items, and run_tasks.
func queryTaskStatusOnce(tm *TaskManager, taskID string) *types.ToolResult {
	if info, err := tm.GetStatus(taskID); err == nil {
		emoji := "✅"
		if info.Status == "error" {
			emoji = "❌"
		}
		out := fmt.Sprintf("%s 状态：status=%s elapsed=%ds", emoji, info.Status, info.Elapsed)
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
		return &types.ToolResult{
			Success: true,
			Output:  out,
			Tool:    "process_task_status",
			RawResult: map[string]interface{}{
				"task_id": taskID,
				"status":  info.Status,
				"elapsed": info.Elapsed,
			},
		}
	}

	if item, err := tm.GetRunTaskSubItem(taskID); err == nil {
		emoji := "✅"
		if item.Status == "error" {
			emoji = "❌"
		}
		out := fmt.Sprintf("%s 状态：item_id=%s tool=%s status=%s", emoji, item.ItemID, item.Tool, item.Status)
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
		return &types.ToolResult{
			Success: true,
			Output:  out,
			Tool:    "process_task_status",
			RawResult: map[string]interface{}{
				"task_id": taskID,
				"item_id": item.ItemID,
				"status":  item.Status,
			},
		}
	}

	if state, err := tm.GetRunTaskState(taskID); err == nil {
		out := fmt.Sprintf("✅ 状态：status=%s completed=%d failed=%d total=%d elapsed=%s",
			state.Status, state.Completed, state.Failed, state.Total, state.Elapsed)
		return &types.ToolResult{
			Success: true,
			Output:  out,
			Tool:    "process_task_status",
			RawResult: map[string]interface{}{
				"task_id":   taskID,
				"status":    state.Status,
				"completed": state.Completed,
				"failed":    state.Failed,
				"total":     state.Total,
				"elapsed":   state.Elapsed,
			},
		}
	}

	return &types.ToolResult{
		Success: false,
		Error:   fmt.Sprintf("task not found: %s", taskID),
		Tool:    "process_task_status",
		Output:  fmt.Sprintf("❌ 任务未找到：%s", taskID),
		RawResult: map[string]interface{}{
			"error": fmt.Sprintf("task not found: %s", taskID),
		},
	}
}

// HandleProcessTaskStop handles the process_task_stop tool.
func HandleProcessTaskStop(tm *TaskManager, args map[string]interface{}) *types.ToolResult {
	taskID, _ := args["task_id"].(string)
	processID, _ := args["process_id"].(float64)

	if processID > 0 {
		pid := int(processID)
		proc, err := os.FindProcess(pid)
		if err != nil {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("process %d not found: %s", pid, err.Error()),
				Tool:    "process_task_stop",
				Output:  fmt.Sprintf("❌ 进程 %d 未找到", pid),
				RawResult: map[string]interface{}{
					"error": err.Error(),
					"pid":   pid,
				},
			}
		}
		if err := proc.Kill(); err != nil {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("failed to kill process %d: %s", pid, err.Error()),
				Tool:    "process_task_stop",
				Output:  fmt.Sprintf("❌ 终止进程 %d 失败", pid),
				RawResult: map[string]interface{}{
					"error": err.Error(),
					"pid":   pid,
				},
			}
		}
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("🛑 进程 %d 已终止", pid),
			Tool:    "process_task_stop",
			RawResult: map[string]interface{}{
				"pid":    pid,
				"status": "killed",
			},
		}
	}

	if taskID == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "task_id or process_id is required",
			Tool:    "process_task_stop",
			Output:  "❌ 缺少 task_id 或 process_id",
			RawResult: map[string]interface{}{
				"error": "task_id or process_id is required",
			},
		}
	}

	err := tm.Stop(taskID)
	if err == nil {
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("🛑 任务 %s 已停止", taskID),
			Tool:    "process_task_stop",
			RawResult: map[string]interface{}{
				"task_id": taskID,
				"status":  "stopped",
			},
		}
	}

	err = tm.StopRunTask(taskID)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   err.Error(),
			Tool:    "process_task_stop",
			Output:  fmt.Sprintf("❌ 停止任务 %s 失败", taskID),
			RawResult: map[string]interface{}{
				"task_id": taskID,
				"error":   err.Error(),
			},
		}
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("🛑 任务 %s 已停止", taskID),
		Tool:    "process_task_stop",
		RawResult: map[string]interface{}{
			"task_id": taskID,
			"status":  "stopped",
		},
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
		return &types.ToolResult{
			Success:   true,
			Output:    fmt.Sprintf("✅ 查询了 %d 个任务", len(taskIDs)),
			Tool:      "query_task",
			RawResult: map[string]interface{}{"tasks": results},
		}
	}

	taskID, _ := args["task_id"].(string)
	if taskID == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "task_id or task_ids is required",
			Tool:    "query_task",
			Output:  "❌ 缺少 task_id 或 task_ids",
			RawResult: map[string]interface{}{
				"error": "task_id or task_ids is required",
			},
		}
	}

	state, err := tm.GetRunTaskState(taskID)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   err.Error(),
			Tool:    "query_task",
			Output:  fmt.Sprintf("❌ 查询任务 %s 失败", taskID),
			RawResult: map[string]interface{}{
				"error": err.Error(),
			},
		}
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

	return &types.ToolResult{
		Success:   true,
		Output:    fmt.Sprintf("✅ 查询任务 %s：status=%s completed=%d/%d", taskID, state.Status, state.Completed, state.Total),
		Tool:      "query_task",
		RawResult: result,
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
