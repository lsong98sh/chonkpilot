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

	// Daemon mode (one long-lived executor process)
	daemonCmd   *exec.Cmd
	daemonStdin io.WriteCloser
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
	info.BasicLimitInformation.LimitFlags = 0 // Don't use KILL_ON_JOB_CLOSE — Chrome is a child process and would be killed when executor exits
	_ = jobObjectLimitKillOnJobClose // keep the constant for reference
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
						pType, _ := payload["type"].(string)
						content, _ := payload["content"].(string)
						cLen := len(content)
						cPreview := ""
						if cLen > 0 {
							if cLen > 50 {
								cPreview = content[:50] + "..."
							} else {
								cPreview = content
							}
						}
						em.logger.Debug("[EVTLOG] Executor→IDE(SSE)",
							zap.String("event_type", parts.eventType),
							zap.String("type", pType),
							zap.Int("clen", cLen),
							zap.String("preview", cPreview),
							zap.String("session_id", sessionID),
							zap.String("turn_id", turnID),
						)
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
			// Log raw executor event for diagnostic
			pType, _ := payload["type"].(string)
			content, _ := payload["content"].(string)
			cLen := len(content)
			cPreview := ""
			if cLen > 0 {
				if cLen > 50 {
					cPreview = content[:50] + "..."
				} else {
					cPreview = content
				}
			}
			em.logger.Debug("[EVTLOG] Executor→IDE",
				zap.String("event_type", eventType),
				zap.String("type", pType),
				zap.Int("clen", cLen),
				zap.String("preview", cPreview),
				zap.String("session_id", sessionID),
				zap.String("turn_id", turnID),
			)
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
	// Only inject session_id/turn_id if not already present.
	// Sub-executors (batch_llm, call_llm) set their own session_id/turn_id,
	// which must be preserved for correct frontend routing.
	if _, ok := payload["session_id"]; !ok {
		payload["session_id"] = sessionID
	}
	if _, ok := payload["turn_id"]; !ok {
		payload["turn_id"] = turnID
	}
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
	// Stop per-turn executors
	em.mu.Lock()
	for turnID, record := range em.records {
		if record.Cmd != nil && record.Cmd.Process != nil {
			if record.jobHandle != 0 {
				procTerminateJob.Call(record.jobHandle, 1)
			}
			record.Cmd.Process.Kill()
		}
		closeJobHandle(record.jobHandle)
		delete(em.records, turnID)
	}
	em.mu.Unlock()

	// Stop daemon executor (if running)
	em.stopDaemon()

	em.logger.Info("All executors shut down")
}

// ─── Daemon mode ──────────────────────────────────────────

// StartDaemon starts the long-lived executor daemon process.
// It spawns executor.exe with --internal and connects to its stdin/stdout.
func (em *ExecutorManager) StartDaemon(workDir string) error {
	em.mu.Lock()
	defer em.mu.Unlock()

	if em.daemonCmd != nil && em.daemonCmd.Process != nil {
		// Already running
		return nil
	}

	execPath := findExecutorPath()
	args := []string{
		"--internal",
		fmt.Sprintf("--work-dir=%s", workDir),
		"--output=json",
	}

	em.logger.Info("Starting executor daemon",
		zap.String("execPath", execPath),
		zap.String("workDir", workDir),
	)

	cmd := exec.Command(execPath, args...)
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CreationFlags: 0x08000000, // CREATE_NO_WINDOW
		}
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("daemon stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("daemon stdout pipe: %w", err)
	}

	// Capture stderr to log file
	stderrLog := filepath.Join(workDir, ".ide", "logs", "executor_daemon_stderr.log")
	_ = os.MkdirAll(filepath.Dir(stderrLog), 0755)
	stderrFile, err := os.OpenFile(stderrLog, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err == nil {
		cmd.Stderr = stderrFile
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		if stderrFile != nil {
			stderrFile.Close()
		}
		return fmt.Errorf("daemon start: %w", err)
	}

	em.daemonCmd = cmd
	em.daemonStdin = stdin

	em.logger.Info("Executor daemon started", zap.Int("pid", cmd.Process.Pid))

	// Read daemon stdout events in background
	go em.readDaemonEvents(stdout)

	// Monitor daemon exit in background
	go func() {
		err := cmd.Wait()
		em.mu.Lock()
		em.daemonCmd = nil
		em.daemonStdin = nil
		em.mu.Unlock()
		em.logger.Info("Executor daemon exited", zap.Error(err))
		if stderrFile != nil {
			stderrFile.Close()
		}
	}()

	return nil
}

// SendTurn sends a start_turn command to the daemon via stdin.
func (em *ExecutorManager) SendTurn(sessionID, turnID, llmName, thinkEnabled, effort string, extraArgs []string) error {
	em.mu.Lock()
	stdin := em.daemonStdin
	em.mu.Unlock()

	if stdin == nil {
		return fmt.Errorf("daemon not running")
	}

	cmd := map[string]interface{}{
		"cmd":        "start_turn",
		"session_id": sessionID,
		"turn_id":    turnID,
		"llm":        llmName,
		"think":      thinkEnabled,
		"effort":     effort,
		"extra_args": extraArgs,
	}

	em.logger.Debug("SendTurn to daemon",
		zap.String("session_id", sessionID),
		zap.String("turn_id", turnID),
	)

	em.mu.Lock()
	defer em.mu.Unlock()
	return json.NewEncoder(stdin).Encode(cmd)
}

// CancelDaemonSession sends a cancel_session command to the daemon.
func (em *ExecutorManager) CancelDaemonSession(sessionID string) error {
	em.mu.Lock()
	stdin := em.daemonStdin
	em.mu.Unlock()

	if stdin == nil {
		return nil // daemon not running, nothing to cancel
	}

	cmd := map[string]interface{}{
		"cmd":        "cancel_session",
		"session_id": sessionID,
	}

	em.logger.Info("CancelDaemonSession", zap.String("session_id", sessionID))

	em.mu.Lock()
	defer em.mu.Unlock()
	return json.NewEncoder(stdin).Encode(cmd)
}

// IsDaemonRunning returns true if the daemon process is alive.
func (em *ExecutorManager) IsDaemonRunning() bool {
	em.mu.Lock()
	defer em.mu.Unlock()
	return em.daemonCmd != nil && em.daemonCmd.Process != nil && em.daemonCmd.ProcessState == nil
}

// stopDaemon sends shutdown and waits for the daemon to exit.
// Called by Stop().
func (em *ExecutorManager) stopDaemon() {
	em.mu.Lock()
	stdin := em.daemonStdin
	cmd := em.daemonCmd
	em.mu.Unlock()

	if stdin == nil {
		return
	}

	// Send shutdown command
	shutdown := map[string]interface{}{"cmd": "shutdown"}
	em.mu.Lock()
	json.NewEncoder(stdin).Encode(shutdown)
	stdin.Close()
	em.mu.Unlock()

	// Wait for process to exit (with timeout)
	if cmd != nil {
		done := make(chan struct{})
		go func() {
			cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			em.logger.Warn("daemon did not exit in time, killing")
			cmd.Process.Kill()
		}
	}
}

// readDaemonEvents reads JSON event lines from daemon stdout.
func (em *ExecutorManager) readDaemonEvents(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}

		if em.onEvent == nil {
			continue
		}

		eventType, _ := raw["type"].(string)
		payload, ok := raw["payload"].(map[string]interface{})
		if !ok {
			payload = raw
		}

		// Log daemon executor event for diagnostic
		pType, _ := payload["type"].(string)
		content, _ := payload["content"].(string)
		cLen := len(content)
		cPreview := ""
		if cLen > 0 {
			if cLen > 50 {
				cPreview = content[:50] + "..."
			} else {
				cPreview = content
			}
		}
		sID, _ := payload["session_id"].(string)
		tID, _ := payload["turn_id"].(string)
		em.logger.Debug("[EVTLOG] Daemon→IDE",
			zap.String("event_type", eventType),
			zap.String("type", pType),
			zap.Int("clen", cLen),
			zap.String("preview", cPreview),
			zap.String("session_id", sID),
			zap.String("turn_id", tID),
		)

		em.onEvent(eventType, payload)
	}

	if err := scanner.Err(); err != nil {
		em.logger.Warn("Daemon stdout scanner error", zap.Error(err))
	}
}

// findExecutorPath finds the executor executable.
// Intentionally duplicated in pkg/executor/toolhandler/call_llm.go (different binaries).
func findExecutorPath() string {
	// First check ~/.chonkpilot/bin/executor.exe (extracted from embedded binary on startup)
	if home, err := os.UserHomeDir(); err == nil {
		binPath := filepath.Join(home, ".chonkpilot", "bin", "executor.exe")
		if _, err := os.Stat(binPath); err == nil {
			return binPath
		}
	}
	// Fallback: check same directory as the current executable
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
