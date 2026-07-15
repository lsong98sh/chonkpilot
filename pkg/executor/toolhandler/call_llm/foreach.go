package call_llm

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot/internal/models"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/task"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

var (
	defaultConcurrency = 5
	defaultMaxDepth    = 5
)

// SetConcurrency sets the global default concurrency for foreach.
// Must be 1-10; values outside range are clamped.
func SetConcurrency(n int) {
	if n < 1 {
		n = 1
	} else if n > 10 {
		n = 10
	}
	defaultConcurrency = n
}

// SetMaxDepth sets the global max nested depth for foreach.
// Must be 1-10; values outside range are clamped.
func SetMaxDepth(d int) {
	if d < 1 {
		d = 1
	} else if d > 10 {
		d = 10
	}
	defaultMaxDepth = d
}

// HandleForeach executes a list of tool calls.
// All execution goes through TaskManager so process_task_stop can cancel it.
//   - async=false (default): items run sequentially in background, function polls and blocks until done
//   - async=true: items run concurrently in background, function returns immediately with task_id
func HandleForeach(tm *task.TaskManager,
	dispatch func(string, map[string]interface{}, int) *types.ToolResult,
	onProgress func(map[string]interface{}), args map[string]interface{}, depth int) *types.ToolResult {

	if depth >= defaultMaxDepth {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("max depth %d exceeded", defaultMaxDepth),
			Tool:    "run_tasks",
		}
	}

	rawAsync, _ := args["async"].(bool)
	rawItems, ok := args["items"].([]interface{})
	if !ok || len(rawItems) == 0 {
		return &types.ToolResult{
			Success: false,
			Error:   "items array is required and must be non-empty",
			Tool:    "run_tasks",
		}
	}

	// Build ToolCallItems for TaskManager registration
	items := make([]models.ToolCallItem, 0, len(rawItems))
	for _, raw := range rawItems {
		if itemMap, ok := raw.(map[string]interface{}); ok {
			toolName, _ := itemMap["tool"].(string)
			argsMap, _ := itemMap["args"].(map[string]interface{})
			items = append(items, models.ToolCallItem{Tool: toolName, Args: argsMap})
		}
	}

	taskID := fmt.Sprintf("rt-%d", time.Now().UnixNano())
	tm.StoreRunTask(taskID, items, rawAsync)

	// Get cancel channel so background goroutine can detect cancellation
	cancelCh, _ := tm.GetRunTaskCancel(taskID)

	start := time.Now()

	// Launch background execution (managed by TaskManager for cancel support)
	go runForeachItems(tm, taskID, items, rawAsync, dispatch, onProgress, start, cancelCh, depth)

	if rawAsync {
		// Async: return immediately with task_id
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("task_id=%s status=running total=%d", taskID, len(items)),
			Tool:    "run_tasks",
			RawResult: map[string]interface{}{
				"task_id": taskID,
			},
		}
	}

	// Sync: poll TaskManager until done or cancelled
	for {
		state, err := tm.GetRunTaskState(taskID)
		if err != nil {
			return &types.ToolResult{Success: false, Error: err.Error(), Tool: "run_tasks"}
		}
		if state.Status != "running" {
			elapsed := time.Since(start)
			output := fmt.Sprintf("task_id=%s completed=%d failed=%d total=%d elapsed=%s",
				taskID, state.Completed, state.Failed, state.Total, elapsed)
			return &types.ToolResult{
				Success: state.Failed == 0,
				Output:  output,
				Tool:    "run_tasks",
				RawResult: map[string]interface{}{
					"task_id": taskID,
				},
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// runForeachItems executes the tool items in background.
// Checks cancel channel between items for graceful cancellation.
func runForeachItems(tm *task.TaskManager, taskID string, items []models.ToolCallItem,
	rawAsync bool,
	dispatch func(string, map[string]interface{}, int) *types.ToolResult,
	onProgress func(map[string]interface{}), start time.Time, cancelCh <-chan struct{}, depth int) {

	// Check cancelled before starting
	select {
	case <-cancelCh:
		return
	default:
	}

	if rawAsync {
		runForeachConcurrent(tm, taskID, items, dispatch, onProgress, start, cancelCh, depth)
	} else {
		runForeachSequential(tm, taskID, items, dispatch, onProgress, start, cancelCh, depth)
	}
}

// runForeachSequential executes items one by one, checking cancel between each.
func runForeachSequential(tm *task.TaskManager, taskID string, items []models.ToolCallItem,
	dispatch func(string, map[string]interface{}, int) *types.ToolResult,
	onProgress func(map[string]interface{}), start time.Time, cancelCh <-chan struct{}, depth int) {

	completed := 0
	failed := 0
	total := len(items)

	for i, item := range items {
		select {
		case <-cancelCh:
			for j := i; j < total; j++ {
				tm.UpdateRunTaskItem(taskID, j, "cancelled", "", "", "cancelled by user")
			}
			tm.MarkRunTaskDone(taskID, "cancelled")
			return
		default:
		}

		toolArgs := cloneArgs(item.Args)
		res := dispatch(item.Tool, toolArgs, depth+1)

		if res.Success {
			completed++
		} else {
			failed++
		}

		tm.UpdateRunTaskItem(taskID, i, statusFromResult(res), "", res.Output, res.Error)

		if onProgress != nil {
			elapsed := time.Since(start)
			p := map[string]interface{}{
				"type":       "tool_progress",
				"total":      total,
				"completed":  completed,
				"failed":     failed,
				"elapsed_ms": elapsed.Milliseconds(),
			}
			if completed > 0 {
				p["estimated_remaining"] = (elapsed / time.Duration(completed) * time.Duration(total-completed)).Round(time.Millisecond).String()
			}
			onProgress(p)
		}
	}

	status := "done"
	if failed > 0 && completed == 0 {
		status = "error"
	} else if failed > 0 {
		status = "partial"
	}
	tm.MarkRunTaskDone(taskID, status)
}

// runForeachConcurrent executes items concurrently with bounded workers.
// Each worker checks cancel before processing a job.
func runForeachConcurrent(tm *task.TaskManager, taskID string, items []models.ToolCallItem,
	dispatch func(string, map[string]interface{}, int) *types.ToolResult,
	onProgress func(map[string]interface{}), start time.Time, cancelCh <-chan struct{}, depth int) {

	total := len(items)
	type job struct {
		index int
		item  models.ToolCallItem
	}

	jobs := make(chan job, total)
	for i, item := range items {
		jobs <- job{index: i, item: item}
	}
	close(jobs)

	var mu sync.Mutex
	var completed, failed int
	var wg sync.WaitGroup

	for w := 0; w < defaultConcurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				select {
				case <-cancelCh:
					tm.UpdateRunTaskItem(taskID, j.index, "cancelled", "", "", "cancelled by user")
					continue
				default:
				}

				toolArgs := cloneArgs(j.item.Args)
				res := dispatch(j.item.Tool, toolArgs, depth+1)

				mu.Lock()
				if res.Success {
					completed++
				} else {
					failed++
				}
				tm.UpdateRunTaskItem(taskID, j.index, statusFromResult(res), "", res.Output, res.Error)
				mu.Unlock()

				if onProgress != nil {
					mu.Lock()
					c, f := completed, failed
					mu.Unlock()
					elapsed := time.Since(start)
					p := map[string]interface{}{
						"type":       "tool_progress",
						"total":      total,
						"completed":  c,
						"failed":     f,
						"elapsed_ms": elapsed.Milliseconds(),
					}
					if c > 0 {
						p["estimated_remaining"] = (elapsed / time.Duration(c) * time.Duration(total-c)).Round(time.Millisecond).String()
					}
					onProgress(p)
				}
			}
		}()
	}

	wg.Wait()

	status := "done"
	if failed > 0 && completed == 0 {
		status = "error"
	} else if failed > 0 {
		status = "partial"
	}
	tm.MarkRunTaskDone(taskID, status)
}

// cloneArgs copies args into a new map (avoids mutation of original).
func cloneArgs(src map[string]interface{}) map[string]interface{} {
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// statusFromResult returns "done" or "error" based on ToolResult.
func statusFromResult(res *types.ToolResult) string {
	if res.Success {
		return "done"
	}
	return "error"
}

func init() {
	types.RegisterSimplify("run_tasks", simplifyRunTasks)
}

func simplifyRunTasks(argsJSON json.RawMessage, result string) string {
	var tr types.ToolResult
	if err := json.Unmarshal([]byte(result), &tr); err != nil {
		return "run_tasks"
	}
	if tr.Success {
		return fmt.Sprintf("run_tasks: done, %d chars", len(tr.Output))
	}
	return fmt.Sprintf("run_tasks: failed, %s", types.TruncateStr(tr.Error, 80))
}
