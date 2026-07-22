package task

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chonkpilot/chonkpilot/internal/models"
)

// mapToEnvSlice converts a map[string]string to a slice of "KEY=VALUE" strings.
func mapToEnvSlice(env map[string]string) []string {
	s := make([]string, 0, len(env))
	for k, v := range env {
		s = append(s, fmt.Sprintf("%s=%s", k, v))
	}
	return s
}

// globalTaskID is a monotonic counter for generating unique task IDs.
var globalTaskID int64

// maxOutputBytes limits the total accumulated command output to prevent unbounded memory growth.
const maxOutputBytes = 1 * 1024 * 1024 // 1 MB

// writeOutput writes a line to the buffer, truncating if the limit is exceeded.
// Returns true if the line was written (or line was empty), false if limit exceeded.
func writeOutput(buf *bytes.Buffer, line string) bool {
	if buf.Len() >= maxOutputBytes {
		return false
	}
	n, _ := buf.WriteString(line + "\n")
	if n > 0 && buf.Len() >= maxOutputBytes {
		buf.WriteString("...(output truncated, limit exceeded)")
	}
	return true
}

// TaskInfo holds the state of an async task.
type TaskInfo struct {
	ID      string   `json:"task_id"`
	Status  string   `json:"status"` // "running", "done", "error", "stopped"
	Cmd     string   `json:"cmd"`
	PID     int      `json:"pid,omitempty"`
	Elapsed int64    `json:"elapsed_seconds"`
	Output  string   `json:"partial_output,omitempty"`
	Error   string   `json:"error,omitempty"`
	mu      sync.RWMutex
	cmd     *exec.Cmd
	cancel  chan struct{}
	buf     bytes.Buffer
	started time.Time
}

// RunTaskItem stores the per-item state within a run_tasks execution.
type RunTaskItem struct {
	ItemID    string                 `json:"item_id"`
	Tool      string                 `json:"tool"`
	Args      map[string]interface{} `json:"args"`
	Status    string                 `json:"status"`
	ProcessID string                 `json:"process_id,omitempty"`
	Output    string                 `json:"output,omitempty"`
	Error     string                 `json:"error,omitempty"`
}

// RunTaskState stores the full state of a run_tasks execution.
type RunTaskState struct {
	ID        string         `json:"task_id"`
	Status    string         `json:"status"` // running, done, cancelled
	Async     bool           `json:"async"`
	Total     int            `json:"total"`
	Completed int            `json:"completed"`
	Failed    int            `json:"failed"`
	Items     []RunTaskItem  `json:"items"`
	Elapsed   string         `json:"elapsed"`
	started   time.Time
	cancel    chan struct{}  // closed when StopRunTask is called
}

// maxTasks limits the number of completed tasks kept in memory to prevent unbounded growth.
const maxTasks = 200

// maxRunTasks limits the number of completed run_tasks states kept in memory.
const maxRunTasks = 50

// TaskManager manages async tasks.
type TaskManager struct {
	mu    sync.Mutex
	tasks map[string]*TaskInfo

	runTasksMu sync.Mutex
	runTasks   map[string]*RunTaskState

	// taskOrder tracks insertion order for FIFO eviction of completed tasks
	taskOrder    []string
	runTaskOrder []string
}

// NewTaskManager creates a new TaskManager.
func NewTaskManager() *TaskManager {
	return &TaskManager{
		tasks:    make(map[string]*TaskInfo),
		runTasks: make(map[string]*RunTaskState),
	}
}

// gcTasks evicts oldest completed tasks when the map exceeds maxTasks.
func (tm *TaskManager) gcTasks() {
	if len(tm.tasks) <= maxTasks {
		return
	}
	evict := len(tm.tasks) - maxTasks
	for i := 0; i < evict && i < len(tm.taskOrder); i++ {
		id := tm.taskOrder[i]
		if info, ok := tm.tasks[id]; ok {
			info.mu.RLock()
			status := info.Status
			info.mu.RUnlock()
			if status != "running" {
				delete(tm.tasks, id)
			}
		}
	}
	// Compact taskOrder: remove evicted entries
	keep := tm.taskOrder[:0]
	for _, id := range tm.taskOrder {
		if _, ok := tm.tasks[id]; ok {
			keep = append(keep, id)
		}
	}
	tm.taskOrder = keep
}

// gcRunTasks evicts oldest completed run_tasks when the map exceeds maxRunTasks.
func (tm *TaskManager) gcRunTasks() {
	if len(tm.runTasks) <= maxRunTasks {
		return
	}
	evict := len(tm.runTasks) - maxRunTasks
	for i := 0; i < evict && i < len(tm.runTaskOrder); i++ {
		id := tm.runTaskOrder[i]
		if rs, ok := tm.runTasks[id]; ok && rs.Status != "running" {
			delete(tm.runTasks, id)
		}
	}
	keep := tm.runTaskOrder[:0]
	for _, id := range tm.runTaskOrder {
		if _, ok := tm.runTasks[id]; ok {
			keep = append(keep, id)
		}
	}
	tm.runTaskOrder = keep
}

// StartCommand starts a shell command asynchronously and returns a task ID.
// env: optional custom environment variables merged with current process env (nil or empty map = inherit current env).
func (tm *TaskManager) StartCommand(workDir, cmdStr string, env map[string]string) string {
	id := fmt.Sprintf("cmd-%d", atomic.AddInt64(&globalTaskID, 1))

	info := &TaskInfo{
		ID:      id,
		Status:  "running",
		Cmd:     cmdStr,
		cancel:  make(chan struct{}),
		started: time.Now(),
	}

	tm.mu.Lock()
	tm.tasks[id] = info
	tm.taskOrder = append(tm.taskOrder, id)
	tm.gcTasks()
	tm.mu.Unlock()

	go tm.runCommand(info, workDir, cmdStr, env)
	return id
}

// StartOperation runs an arbitrary function asynchronously and returns a task ID.
func (tm *TaskManager) StartOperation(name string, fn func(cancel <-chan struct{}) (string, error)) string {
	id := fmt.Sprintf("op-%d", atomic.AddInt64(&globalTaskID, 1))

	info := &TaskInfo{
		ID:      id,
		Status:  "running",
		Cmd:     name,
		cancel:  make(chan struct{}),
		started: time.Now(),
	}

	tm.mu.Lock()
	tm.tasks[id] = info
	tm.taskOrder = append(tm.taskOrder, id)
	tm.gcTasks()
	tm.mu.Unlock()

	go func() {
		output, err := fn(info.cancel)
		info.mu.Lock()
		defer info.mu.Unlock()
		info.Elapsed = int64(time.Since(info.started).Seconds())
		info.Output = output
		if err != nil {
			info.Status = "error"
			info.Error = err.Error()
		} else {
			info.Status = "done"
		}
	}()

	return id
}

// SyncOperation runs a function synchronously and returns (taskID, output, error).
func (tm *TaskManager) SyncOperation(name string, fn func(cancel <-chan struct{}) (string, error)) (string, string, error) {
	id := fmt.Sprintf("op-%d", atomic.AddInt64(&globalTaskID, 1))

	info := &TaskInfo{
		ID:      id,
		Status:  "running",
		Cmd:     name,
		cancel:  make(chan struct{}),
		started: time.Now(),
	}

	tm.mu.Lock()
	tm.tasks[id] = info
	tm.taskOrder = append(tm.taskOrder, id)
	tm.gcTasks()
	tm.mu.Unlock()

	output, err := fn(info.cancel)
	info.mu.Lock()
	info.Elapsed = int64(time.Since(info.started).Seconds())
	if err != nil {
		info.Status = "error"
		info.Error = err.Error()
	} else {
		info.Status = "done"
		info.Output = output
	}
	info.mu.Unlock()
	return id, output, err
}

func (tm *TaskManager) runCommand(info *TaskInfo, workDir, cmdStr string, env map[string]string) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", cmdStr)
	} else {
		cmd = exec.Command("sh", "-c", cmdStr)
	}
	if workDir != "" {
		cmd.Dir = workDir
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), mapToEnvSlice(env)...)
	}

	info.mu.Lock()
	info.cmd = cmd
	info.mu.Unlock()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		info.mu.Lock()
		info.Status = "error"
		info.Error = fmt.Sprintf("stdout pipe failed: %s", err.Error())
		info.mu.Unlock()
		return
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		info.mu.Lock()
		info.Status = "error"
		info.Error = fmt.Sprintf("stderr pipe failed: %s", err.Error())
		info.mu.Unlock()
		return
	}

	if err := cmd.Start(); err != nil {
		info.mu.Lock()
		info.Status = "error"
		info.Error = err.Error()
		info.mu.Unlock()
		return
	}

	info.mu.Lock()
	info.PID = cmd.Process.Pid
	info.mu.Unlock()

	done := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			select {
			case <-info.cancel:
				return
			default:
			}
			line := scanner.Text()
			info.mu.Lock()
			writeOutput(&info.buf, line)
			info.mu.Unlock()
		}
	}()
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			select {
			case <-info.cancel:
				return
			default:
			}
			line := scanner.Text()
			info.mu.Lock()
			writeOutput(&info.buf, line)
			info.mu.Unlock()
		}
	}()

	go func() {
		err := cmd.Wait()
		close(done)
		info.mu.Lock()
		info.Elapsed = int64(time.Since(info.started).Seconds())
		info.Output = info.buf.String()
		if err != nil {
			if info.Status != "stopped" {
				info.Status = "error"
				info.Error = err.Error()
			}
		} else {
			info.Status = "done"
		}
		info.mu.Unlock()
	}()
}

// GetStatus returns task status info (thread-safe).
func (tm *TaskManager) GetStatus(id string) (*TaskInfo, error) {
	tm.mu.Lock()
	info, ok := tm.tasks[id]
	tm.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("task not found: %s", id)
	}

	info.mu.RLock()
	defer info.mu.RUnlock()
	elapsed := int64(time.Since(info.started).Seconds())
	if info.Status == "running" {
		elapsed = int64(time.Since(info.started).Seconds())
	}

	return &TaskInfo{
		ID:      info.ID,
		Status:  info.Status,
		Cmd:     info.Cmd,
		PID:     info.PID,
		Elapsed: elapsed,
		Output:  info.buf.String(),
		Error:   info.Error,
	}, nil
}

// Stop terminates a running task.
func (tm *TaskManager) Stop(id string) error {
	tm.mu.Lock()
	info, ok := tm.tasks[id]
	tm.mu.Unlock()
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}

	info.mu.Lock()
	defer info.mu.Unlock()

	if info.Status != "running" {
		return fmt.Errorf("task %s is not running (status: %s)", id, info.Status)
	}

	close(info.cancel)
	if info.cmd != nil && info.cmd.Process != nil {
		info.cmd.Process.Kill()
	}
	info.Status = "stopped"
	info.Elapsed = int64(time.Since(info.started).Seconds())
	info.Output = info.buf.String()
	return nil
}

// StoreRunTask initializes a run_tasks execution state.
func (tm *TaskManager) StoreRunTask(id string, items []models.ToolCallItem, async bool) {
	itemStates := make([]RunTaskItem, len(items))
	for i, item := range items {
		itemStates[i] = RunTaskItem{
			ItemID: fmt.Sprintf("%s/item-%d", id, i),
			Tool:   item.Tool,
			Args:   item.Args,
			Status: "pending",
		}
	}
	tm.runTasksMu.Lock()
	tm.runTasks[id] = &RunTaskState{
		ID:      id,
		Status:  "running",
		Async:   async,
		Total:   len(items),
		Items:   itemStates,
		started: time.Now(),
		cancel:  make(chan struct{}),
	}
	tm.runTaskOrder = append(tm.runTaskOrder, id)
	tm.gcRunTasks()
	tm.runTasksMu.Unlock()
}

// UpdateRunTaskItem updates a single item's state within a run_tasks execution.
func (tm *TaskManager) UpdateRunTaskItem(id string, idx int, status, processID, output, errStr string) {
	tm.runTasksMu.Lock()
	defer tm.runTasksMu.Unlock()
	rs, ok := tm.runTasks[id]
	if !ok {
		return
	}
	if idx < 0 || idx >= len(rs.Items) {
		return
	}
	rs.Items[idx].Status = status
	if processID != "" {
		rs.Items[idx].ProcessID = processID
	}
	if output != "" {
		rs.Items[idx].Output = output
	}
	if errStr != "" {
		rs.Items[idx].Error = errStr
	}

	rs.Completed = 0
	rs.Failed = 0
	for _, item := range rs.Items {
		if item.Status == "done" {
			rs.Completed++
		} else if item.Status == "error" {
			rs.Failed++
		}
	}
}

// MarkRunTaskDone marks the entire run_tasks as done/cancelled.
func (tm *TaskManager) MarkRunTaskDone(id string, status string) {
	tm.runTasksMu.Lock()
	defer tm.runTasksMu.Unlock()
	rs, ok := tm.runTasks[id]
	if !ok {
		return
	}
	rs.Status = status
	rs.Elapsed = time.Since(rs.started).Round(time.Millisecond).String()
}

// GetRunTaskState returns the full state of a run_tasks execution.
func (tm *TaskManager) GetRunTaskState(id string) (*RunTaskState, error) {
	tm.runTasksMu.Lock()
	defer tm.runTasksMu.Unlock()
	rs, ok := tm.runTasks[id]
	if !ok {
		return nil, fmt.Errorf("run task not found: %s", id)
	}
	copied := *rs
	copied.Items = make([]RunTaskItem, len(rs.Items))
	copy(copied.Items, rs.Items)
	if copied.Status == "running" {
		copied.Elapsed = time.Since(copied.started).Round(time.Millisecond).String()
	}
	return &copied, nil
}

// GetRunTaskCancel returns the cancel channel for a run_tasks task.
// Background goroutines select on this channel to detect cancellation.
func (tm *TaskManager) GetRunTaskCancel(id string) (<-chan struct{}, error) {
	tm.runTasksMu.Lock()
	defer tm.runTasksMu.Unlock()
	rs, ok := tm.runTasks[id]
	if !ok {
		return nil, fmt.Errorf("run task not found: %s", id)
	}
	return rs.cancel, nil
}

// GetRunTaskSubItem returns a single sub-item by its full ID.
func (tm *TaskManager) GetRunTaskSubItem(fullID string) (*RunTaskItem, error) {
	parts := strings.SplitN(fullID, "/item-", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid sub-item ID format: %s", fullID)
	}
	runTaskID := parts[0]
	var idx int
	if _, err := fmt.Sscanf(parts[1], "%d", &idx); err != nil {
		return nil, fmt.Errorf("invalid sub-item index in: %s", fullID)
	}

	tm.runTasksMu.Lock()
	defer tm.runTasksMu.Unlock()
	rs, ok := tm.runTasks[runTaskID]
	if !ok {
		return nil, fmt.Errorf("run task not found: %s", runTaskID)
	}
	if idx < 0 || idx >= len(rs.Items) {
		return nil, fmt.Errorf("sub-item index %d out of range (total: %d)", idx, len(rs.Items))
	}
	item := rs.Items[idx]
	return &item, nil
}

// StopRunTask cancels a running run_tasks execution.
// Closes the cancel channel to signal background goroutines to stop.
func (tm *TaskManager) StopRunTask(id string) error {
	tm.runTasksMu.Lock()
	defer tm.runTasksMu.Unlock()
	rs, ok := tm.runTasks[id]
	if !ok {
		return fmt.Errorf("run task not found: %s", id)
	}
	if rs.Status != "running" {
		return fmt.Errorf("run task %s is not running (status: %s)", id, rs.Status)
	}
	rs.Status = "cancelled"
	rs.Elapsed = time.Since(rs.started).Round(time.Millisecond).String()
	close(rs.cancel)
	return nil
}
