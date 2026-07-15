package process

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"go.uber.org/zap"
)

// EventCallback is called when an executor produces a JSON event.
type EventCallback func(eventType string, payload map[string]interface{})

// ExecutorRecord tracks a running executor process.
type ExecutorRecord struct {
	ProcessID int
	SessionID string
	TurnID    string
	PipePath  string
	Cmd       *exec.Cmd
	StartedAt time.Time
	// Windows only: job object handle for child process cleanup
	jobHandle uintptr
}

// ExecutorManager manages executor process lifecycle.
type ExecutorManager struct {
	workDir string
	logger  *zap.Logger
	mu      sync.Mutex
	records map[string]*ExecutorRecord // keyed by turn_id

	onEvent EventCallback
}

// NewExecutorManager creates a new ExecutorManager.
func NewExecutorManager(workDir string, logger *zap.Logger) *ExecutorManager {
	return &ExecutorManager{
		workDir: workDir,
		logger:  logger,
		records: make(map[string]*ExecutorRecord),
	}
}

// SetOnEvent sets the event callback for executor events.
func (em *ExecutorManager) SetOnEvent(cb EventCallback) {
	em.onEvent = cb
}

// --- Windows Job Object helpers ---

const (
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x00002000
	jobObjectMsgEndOfJobTime          = 1
	jobObjectMsgActiveProcessLimit    = 3
	jobObjectMsgActiveProcessZero     = 4
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procCreateJob    = kernel32.NewProc("CreateJobObjectW")
	procAssignJob    = kernel32.NewProc("AssignProcessToJobObject")
	procSetInfoJob   = kernel32.NewProc("SetInformationJobObject")
	procCloseHandle  = kernel32.NewProc("CloseHandle")
	procTerminateJob = kernel32.NewProc("TerminateJobObject")
)

type jobObjectExtendedLimitInfo struct {
	BasicLimitInformation struct {
		PerProcessUserTimeLimit int64
		PerJobUserTimeLimit     int64
		LimitFlags              uint32
		MinimumWorkingSetSize   uintptr
		MaximumWorkingSetSize   uintptr
		ActiveProcessLimit      uint32
		Affinity                uintptr
		ChildProcessRate        uint32
		_                       [4]byte
	}
	IoInfo struct {
		ReadOperationCount  int64
		WriteOperationCount int64
		OtherOperationCount int64
		ReadTransferCount   int64
		WriteTransferCount  int64
		OtherTransferCount  int64
	}
	ProcessMemoryLimitInBytes     uintptr
	JobMemoryLimitInBytes         uintptr
	PeakProcessMemoryUsedInBytes  uintptr
	PeakJobMemoryUsedInBytes      uintptr
}

// startExecutorWithJob assigns the process to a Windows Job Object so that
// when we kill the job, all child processes are terminated.
func startExecutorWithJob(cmd *exec.Cmd) (uintptr, error) {
	if runtime.GOOS != "windows" {
		return 0, nil
	}
	jobName := fmt.Sprintf("chonkpilot-executor-%d", cmd.Process.Pid)
	job, _, err := procCreateJob.Call(0, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(jobName))))
	if job == 0 {
		return 0, fmt.Errorf("CreateJobObject failed: %w", err)
	}
	info := &jobObjectExtendedLimitInfo{}
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	infoSize := unsafe.Sizeof(*info)
	ret, _, err := procSetInfoJob.Call(job, jobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(info)), infoSize)
	if ret == 0 {
		procCloseHandle.Call(job)
		return 0, fmt.Errorf("SetInformationJobObject failed: %w", err)
	}
	ret, _, err = procAssignJob.Call(job, uintptr(cmd.Process.Pid))
	if ret == 0 {
		procCloseHandle.Call(job)
		return 0, fmt.Errorf("AssignProcessToJobObject failed: %w", err)
	}
	return job, nil
}

func closeJobHandle(job uintptr) {
	if job != 0 {
		procCloseHandle.Call(job)
	}
}

// StartExecutor starts a new executor process for a turn.
// IDE has already written the user message to DB, so executor only needs --turn-id
// to look up the turn, derive session_id, and load all messages.
// llmName/thinkEnabled/effort are runtime overrides from the chat page controls.
// extraArgs can pass --log-level, --retry-count, --retry-delay from global config.
// Executor stdout is captured and parsed as JSON event lines, then forwarded via onEvent.
func (em *ExecutorManager) StartExecutor(sessionID, turnID, llmName, thinkEnabled, effort string, extraArgs ...string) (*ExecutorRecord, error) {
	// Find executor executable path
	execPath := findExecutorPath()

	em.logger.Info("StartExecutor: launching executor",
		zap.String("execPath", execPath),
		zap.String("sessionID", sessionID),
		zap.String("turnID", turnID),
		zap.String("llmName", llmName),
		zap.String("thinkEnabled", thinkEnabled),
		zap.String("effort", effort),
		zap.String("workDir", em.workDir),
	)

	// Build command args: IDE mode uses --turn-id (executor looks up the turn in DB
	// to get session_id, load messages, system prompt, tools, and retry config)
	args := []string{
		fmt.Sprintf("--work-dir=%s", em.workDir),
		fmt.Sprintf("--turn-id=%s", turnID),
		"--output=json",
	}

	// Runtime overrides from chat page (only pass non-empty values)
	if llmName != "" {
		args = append(args, fmt.Sprintf("--llm=%s", llmName))
	}
	if thinkEnabled != "" {
		args = append(args, fmt.Sprintf("--think=%s", thinkEnabled))
	}
	if effort != "" {
		args = append(args, fmt.Sprintf("--effort=%s", effort))
	}
	// Global config overrides (log level, retry policy from user config)
	args = append(args, extraArgs...)

	cmd := exec.Command(execPath, args...)

	// Hide the executor's console window (CREATE_NO_WINDOW = 0x08000000)
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CreationFlags: 0x08000000, // CREATE_NO_WINDOW
		}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	// Capture stderr to a log file for debugging
	stderrLog := filepath.Join(em.workDir, ".ide", "logs", "executor_stderr.log")
	_ = os.MkdirAll(filepath.Dir(stderrLog), 0755)
	stderrFile, err := os.OpenFile(stderrLog, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	var stderrCleanup func()
	if err == nil {
		cmd.Stderr = stderrFile
		stderrCleanup = func() { stderrFile.Close() }
	} else {
		cmd.Stderr = os.Stderr
		stderrCleanup = func() {}
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start executor: %w", err)
	}

	// Create Windows Job Object to ensure child processes are cleaned up on kill/exit
	jobHandle, _ := startExecutorWithJob(cmd)
	if jobHandle != 0 {
		em.logger.Debug("Created job object for executor child process cleanup",
			zap.Int("pid", cmd.Process.Pid),
		)
	}

	record := &ExecutorRecord{
		ProcessID: cmd.Process.Pid,
		SessionID: sessionID,
		TurnID:    turnID,
		Cmd:       cmd,
		StartedAt: time.Now(),
		jobHandle: jobHandle,
	}

	em.mu.Lock()
	em.records[turnID] = record
	em.mu.Unlock()

	em.logger.Info("Executor started",
		zap.Int("pid", cmd.Process.Pid),
		zap.String("turn_id", turnID),
		zap.String("session_id", sessionID),
	)

	// Read JSON events from stdout in background
	go em.readExecutorEvents(stdout, sessionID, turnID)

	// Wait in background
	go func() {
		err := cmd.Wait()
		stderrCleanup()

		// Close job handle on normal exit too (kills any lingering children)
		closeJobHandle(jobHandle)

		em.mu.Lock()
		delete(em.records, turnID)
		em.mu.Unlock()

		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
		}
		em.logger.Info("Executor finished",
			zap.Int("pid", cmd.Process.Pid),
			zap.String("turn_id", turnID),
			zap.Int("exit_code", exitCode),
			zap.Error(err),
		)

		// Send done event
		if em.onEvent != nil {
			em.onEvent("executor_done", map[string]interface{}{
				"turn_id":    turnID,
				"session_id": sessionID,
				"exit_code":  exitCode,
			})
		}
	}()
	return record, nil
}

// readExecutorEvents reads JSON event lines from executor stdout.
func (em *ExecutorManager) readExecutorEvents(reader io.Reader, sessionID, turnID string) {
	scanner := bufio.NewScanner(reader)
	// Use a large buffer for long JSON lines (e.g. tool results with file content)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Try to parse the line as JSON event
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			// SSE format: "event: thinking ..." or "data: {...}"
			if len(line) > 6 && line[:5] == "event" {
				// Extract event type and payload
				parts := splitSSELine(line)
				if parts.eventType != "" && parts.payload != "" && em.onEvent != nil {
					var payload map[string]interface{}
					if err := json.Unmarshal([]byte(parts.payload), &payload); err == nil {
						em.onEvent(parts.eventType, mergeContext(payload, sessionID, turnID))
					}
				}
			}
			continue
		}

		// Direct JSON lines: extract event type and payload from the JSON
		if em.onEvent != nil {
			eventType, _ := raw["type"].(string)
			if eventType == "" {
				eventType = "executor_output"
			}
			payload, ok := raw["payload"].(map[string]interface{})
			if !ok {
				payload = raw
			}
			em.onEvent(eventType, mergeContext(payload, sessionID, turnID))
		}
	}

	if err := scanner.Err(); err != nil {
		em.logger.Warn("Executor stdout scanner error",
			zap.String("turn_id", turnID),
			zap.Error(err),
		)
	}
}

type sseParts struct {
	eventType string
	payload   string
}

func splitSSELine(line string) sseParts {
	// Format: "event: type {\"key\":\"val\"}"
	// Or: "data: {\"key\":\"val\"}"
	var result sseParts
	if len(line) < 7 {
		return result
	}
	// Find the first space after "event:" or "data:"
	colonIdx := -1
	for i, c := range line {
		if c == ':' {
			colonIdx = i
			break
		}
	}
	if colonIdx < 0 {
		return result
	}
	prefix := line[:colonIdx]
	rest := stringsTrimLeft(line[colonIdx+1:])
	if prefix == "event" {
		// "event: type payload"
		spaceIdx := -1
		for i, c := range rest {
			if c == ' ' {
				spaceIdx = i
				break
			}
		}
		if spaceIdx >= 0 {
			result.eventType = rest[:spaceIdx]
			result.payload = stringsTrimLeft(rest[spaceIdx+1:])
		} else {
			result.eventType = rest
		}
	} else if prefix == "data" {
		// Try to determine event type from payload content
		result.payload = rest
		// Default event type for data:
		var payload map[string]interface{}
		if json.Unmarshal([]byte(rest), &payload) == nil {
			if t, ok := payload["type"].(string); ok {
				result.eventType = t
			} else {
				result.eventType = "data"
			}
		} else {
			result.eventType = "data"
		}
	}
	return result
}

func stringsTrimLeft(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	return s
}

func mergeContext(payload map[string]interface{}, sessionID, turnID string) map[string]interface{} {
	if payload == nil {
		payload = make(map[string]interface{})
	}
	payload["session_id"] = sessionID
	payload["turn_id"] = turnID
	return payload
}

// KillExecutor kills a running executor process.
func (em *ExecutorManager) KillExecutor(turnID string) error {
	em.mu.Lock()
	record, ok := em.records[turnID]
	em.mu.Unlock()

	if !ok {
		return fmt.Errorf("executor for turn %s not found", turnID)
	}

	if record.Cmd != nil && record.Cmd.Process != nil {
		// Kill via Windows job object first (kills all children)
		if record.jobHandle != 0 {
			procTerminateJob.Call(record.jobHandle, 1)
		}
		if err := record.Cmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill executor for turn %s: %w", turnID, err)
		}
	}

	em.mu.Lock()
	delete(em.records, turnID)
	em.mu.Unlock()
	return nil
}

// Stop kills all running executors and cleans up.
func (em *ExecutorManager) Stop() {
	em.mu.Lock()
	defer em.mu.Unlock()

	for turnID, record := range em.records {
		if record.Cmd != nil && record.Cmd.Process != nil {
			// Kill via job object (handles children)
			if record.jobHandle != 0 {
				procTerminateJob.Call(record.jobHandle, 1)
			}
			record.Cmd.Process.Kill()
		}
		closeJobHandle(record.jobHandle)
		delete(em.records, turnID)
	}
	em.logger.Info("All executors shut down")
}

// findExecutorPath finds the executor executable.
// Intentionally duplicated in pkg/executor/toolhandler/call_llm.go (different binaries).
func findExecutorPath() string {
	// First check same directory as the current executable
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		executorPath := filepath.Join(dir, "executor.exe")
		if _, err := os.Stat(executorPath); err == nil {
			fmt.Fprintf(os.Stderr, "[findExecutorPath] found at %s (same dir as exe %s)\n", executorPath, exe)
			return executorPath
		}
		executorPath = filepath.Join(dir, "executor")
		if _, err := os.Stat(executorPath); err == nil {
			fmt.Fprintf(os.Stderr, "[findExecutorPath] found at %s (unix, same dir as exe %s)\n", executorPath, exe)
			return executorPath
		}
		fmt.Fprintf(os.Stderr, "[findExecutorPath] NOT found next to exe %s (dir=%s)\n", exe, dir)
	} else {
		fmt.Fprintf(os.Stderr, "[findExecutorPath] os.Executable() error: %v\n", err)
	}
	// Fallback: check PATH
	fmt.Fprintf(os.Stderr, "[findExecutorPath] falling back to PATH lookup for 'executor'\n")
	return "executor"
}
