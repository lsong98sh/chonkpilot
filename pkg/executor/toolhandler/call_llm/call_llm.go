package call_llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/internal/models"
	"github.com/chonkpilot/chonkpilot/pkg/executor/engine"
	run "github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/task"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// HandleCallLLM reads a prompt from a temp file and launches a sub-executor process.
// All execution goes through TaskManager so process_task_stop can cancel it.
func HandleCallLLM(logger *zap.Logger, session, turnID string, eng *engine.Engine,
	tm *run.TaskManager,
	llmProvider, llmModel, llmAPIKey, llmAPIURL string,
	thinking bool, reasoningEffort string,
	writeEvent func(string, map[string]interface{}), onProgress func(map[string]interface{}),
	args map[string]interface{}, depth int) *types.ToolResult {

	if depth >= 5 {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("call_llm max depth %d exceeded", 5),
			Tool:    "call_llm",
		}
	}

	// ── Required: title ──
	title, _ := args["title"].(string)
	if title == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "'title' is required — provide a human-readable description for this sub-task",
			Tool:    "call_llm",
		}
	}

	promptFile, _ := args["prompt-file"].(string)

	// If no prompt-file but prompt text is given, write it to a temp file
	if promptFile == "" {
		promptText, _ := args["prompt"].(string)
		if promptText == "" {
			return &types.ToolResult{
				Success: false,
				Error:   "either 'prompt' (text) or 'prompt-file' (path) is required",
				Tool:    "call_llm",
			}
		}
		tmpFile, err := os.CreateTemp("", "call_llm_prompt_*.txt")
		if err != nil {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("failed to create temp file: %s", err.Error()),
				Tool:    "call_llm",
			}
		}
		if _, err := tmpFile.WriteString(promptText); err != nil {
			os.Remove(tmpFile.Name())
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("failed to write temp file: %s", err.Error()),
				Tool:    "call_llm",
			}
		}
		tmpFile.Close()
		promptFile = tmpFile.Name()
		defer os.Remove(promptFile)
	}

	if !filepath.IsAbs(promptFile) {
		promptFile = filepath.Join(eng.WorkDir, promptFile)
	}

	if _, err := os.Stat(promptFile); os.IsNotExist(err) {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("prompt file not found: %s", promptFile),
			Tool:    "call_llm",
		}
	}

	systemPrompt, _ := args["system-prompt"].(string)
	agent, _ := args["agent"].(string)
	showWindow, _ := args["show_window"].(bool)

	// Determine sub-session: use provided session_id for continuation, or create new
	subSessionID, _ := args["session_id"].(string)
	if subSessionID == "" {
		subSessionID = uuid.New().String()
	}
	subTurnID := uuid.New().String()

	// Create sub-session in DB with retry (can fail under concurrent sub-executor access)
	if session != "" {
		var sessErr error
		for attempt := 0; attempt < 3; attempt++ {
			sqlDB, err := db.Open(eng.WorkDir)
			if err != nil {
				sessErr = err
				time.Sleep(time.Duration(attempt+1) * 300 * time.Millisecond)
				continue
			}
			subSession := models.NewSession(subSessionID, session, eng.WorkDir, title)
			sessErr = db.CreateSession(sqlDB, subSession)
			db.Close(sqlDB)
			if sessErr == nil {
				break
			}
			time.Sleep(time.Duration(attempt+1) * 300 * time.Millisecond)
		}
		if sessErr != nil {
			logger.Warn("call_llm: failed to create sub-session in DB after retries", zap.Error(sessErr))
		}
	}

	// ── Create parent-side TCP listener for child events ──
	pipeAddr := ""
	var subListener net.Listener
	if writeEvent != nil {
		subListener, _ = net.Listen("tcp", "127.0.0.1:0")
		if subListener != nil {
			pipeAddr = subListener.Addr().String()
		}
	}

	// ── Build sub-executor args ───────────────────────────
	subArgs := []string{
		fmt.Sprintf("--work-dir=%s", eng.WorkDir),
		fmt.Sprintf("--prompt-file=%s", promptFile),
		fmt.Sprintf("--session-id=%s", subSessionID),
		fmt.Sprintf("--turn-id=%s", subTurnID),
		"--output=json",
	}
	if pipeAddr != "" {
		subArgs = append(subArgs, fmt.Sprintf("--pipe-addr=%s", pipeAddr))
	}
	if systemPrompt != "" {
		subArgs = append(subArgs, fmt.Sprintf("--system-prompt=%s", systemPrompt))
	}
	if agent != "" {
		subArgs = append(subArgs, fmt.Sprintf("--agent=%s", agent))
	}

	// Pass LLM config to sub-executor so it uses the same provider/api key
	if llmProvider != "" {
		subArgs = append(subArgs, fmt.Sprintf("--llm-provider=%s", llmProvider))
	}
	if llmModel != "" {
		subArgs = append(subArgs, fmt.Sprintf("--llm-model=%s", llmModel))
	}
	if llmAPIKey != "" {
		subArgs = append(subArgs, fmt.Sprintf("--llm-api-key=%s", llmAPIKey))
	}
	if llmAPIURL != "" {
		subArgs = append(subArgs, fmt.Sprintf("--llm-api-url=%s", llmAPIURL))
	}
	if thinking {
		subArgs = append(subArgs, "--think=on")
	} else {
		subArgs = append(subArgs, "--think=off")
	}
	if reasoningEffort != "" {
		subArgs = append(subArgs, fmt.Sprintf("--effort=%s", reasoningEffort))
	}

	async, _ := args["async"].(bool)
	logMode := "sync"
	if async {
		logMode = "async"
	}

	// ── Launch via TaskManager ────────────────────────────
	// All sub-executor processes go through TM so they are cancelable
	// via process_task_stop and their results are queryable via query_task.
	taskID := tm.StartOperation(fmt.Sprintf("call_llm(%s)", title), func(cancel <-chan struct{}) (string, error) {
		// Manage listener lifecycle inside the operation
		if subListener != nil {
			defer subListener.Close()
			go forwardSubPipe(logger, subListener, subSessionID, subTurnID, depth, writeEvent)
		}

		execPath := findExecutorPath()
		cmd := exec.Command(execPath, subArgs...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if showWindow && runtime.GOOS == "windows" {
			cmd.SysProcAttr = &syscall.SysProcAttr{
				CreationFlags: 0x00000010, // CREATE_NEW_CONSOLE
			}
		}

		logger.Info("CallLLM launching sub-executor",
			zap.String("mode", logMode),
			zap.String("sub_turn_id", subTurnID),
			zap.Bool("show_window", showWindow),
		)

		if err := cmd.Start(); err != nil {
			return "", fmt.Errorf("failed to start sub-executor: %w", err)
		}

		// Wait for completion or cancellation
		done := make(chan error, 1)
		go func() {
			done <- cmd.Wait()
		}()

		select {
		case <-cancel:
			logger.Warn("CallLLM cancelled", zap.String("sub_turn_id", subTurnID))
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
			return "", fmt.Errorf("cancelled")
		case err := <-done:
			if err != nil {
				logger.Warn("CallLLM sub-executor failed",
					zap.Error(err),
					zap.String("stderr", stderr.String()),
				)
				return "", fmt.Errorf("sub-executor failed: %s\nstderr: %s", err.Error(), stderr.String())
			}
			// Read result from DB instead of parsing stdout — more reliable
			resultContent := readTurnResult(eng.WorkDir, subTurnID)
			if resultContent == "" {
				resultContent = parseLLMOutput(stdout.Bytes())
			}
			logger.Info("CallLLM completed",
				zap.String("sub_turn_id", subTurnID),
			)
			return resultContent, nil
		}
	})

	if async {
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("task_id=%s status=running", taskID),
			Tool:    "call_llm",
			RawResult: map[string]interface{}{
				"sub_session_id": subSessionID,
				"sub_turn_id":    subTurnID,
			},
		}
	}

	// Sync: poll TaskManager until done or cancelled
	for {
		info, err := tm.GetStatus(taskID)
		if err != nil {
			return &types.ToolResult{Success: false, Error: err.Error(), Tool: "call_llm"}
		}
		if info.Status != "running" {
			if info.Status == "done" {
				return &types.ToolResult{
					Success: true,
					Output:  info.Output,
					Tool:    "call_llm",
					RawResult: map[string]interface{}{
						"sub_session_id": subSessionID,
						"sub_turn_id":    subTurnID,
					},
				}
			}
			// error or stopped
			return &types.ToolResult{
				Success: false,
				Error:   info.Error,
				Tool:    "call_llm",
				RawResult: map[string]interface{}{
					"sub_session_id": subSessionID,
					"sub_turn_id":    subTurnID,
				},
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// forwardSubPipe listens on a TCP connection for child executor events
// and forwards them upstream via writeEvent with sub-session metadata injected.
func forwardSubPipe(logger *zap.Logger, listener net.Listener, subSessionID, subTurnID string, depth int, writeEvent func(string, map[string]interface{})) {
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	for {
		var event struct {
			Type    string                 `json:"type"`
			Payload map[string]interface{} `json:"payload"`
		}
		if err := decoder.Decode(&event); err != nil {
			break
		}

		if event.Payload == nil {
			event.Payload = make(map[string]interface{})
		}
		event.Payload["sub_session_id"] = subSessionID
		event.Payload["sub_turn_id"] = subTurnID
		event.Payload["depth"] = depth + 1

		if writeEvent != nil {
			writeEvent(event.Type, event.Payload)
		}
	}
}

// readTurnResult reads the assistant's final reply from DB for a given turn.
// Falls back to empty string if DB access fails or no assistant message found.
func readTurnResult(workDir, turnID string) string {
	sqlDB, err := db.Open(workDir)
	if err != nil {
		return ""
	}
	defer db.Close(sqlDB)

	messages, err := db.GetMessagesByTurn(sqlDB, turnID)
	if err != nil {
		return ""
	}

	// Return the last non-empty assistant message
	var last string
	for _, msg := range messages {
		if msg.Role == "assistant" && msg.Content != "" {
			last = msg.Content
		}
	}
	return last
}

// parseLLMOutput parses the JSON event output from a sub-executor and extracts the result.
func parseLLMOutput(stdout []byte) string {
	s := bufio.NewScanner(bytes.NewReader(stdout))
	for s.Scan() {
		line := s.Text()
		if line == "" {
			continue
		}
		var event struct {
			Type    string                 `json:"type"`
			Payload map[string]interface{} `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.Type == "complete" {
			if r, ok := event.Payload["result"]; ok {
				return fmt.Sprintf("%v", r)
			}
		}
	}
	return string(stdout)
}

// findExecutorPath finds the executor executable.
func findExecutorPath() string {
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		executorPath := filepath.Join(dir, "executor.exe")
		if _, err := os.Stat(executorPath); err == nil {
			return executorPath
		}
		executorPath = filepath.Join(dir, "executor")
		if _, err := os.Stat(executorPath); err == nil {
			return executorPath
		}
	}
	return "executor"
}

func init() {
	types.RegisterSimplify("call_llm", types.SimpleAction("call_llm"))
}
