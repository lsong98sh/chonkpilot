package batch_llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/internal/models"
	"github.com/chonkpilot/chonkpilot/pkg/executor/codeindex"
	"github.com/chonkpilot/chonkpilot/pkg/executor/discover"
	"github.com/chonkpilot/chonkpilot/pkg/executor/llm"
	"github.com/chonkpilot/chonkpilot/pkg/executor/sessionutil"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ─── Pipeline JSON structures ────────────────────────────────────────────────

type PipelineTask struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Prompt       string `json:"prompt"`
	SubmitPrompt string `json:"submit_prompt,omitempty"`
	Status       string `json:"status"` // pending / running / done / error
	Retry        int    `json:"retry,omitempty"`
	RetryCount   int    `json:"retry_count,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
	TurnID       string `json:"turn_id,omitempty"`
}

type Pipeline struct {
	SystemPrompt string         `json:"system_prompt"`
	SubmitPrompt string         `json:"submit_prompt,omitempty"`
	LastTaskID   int            `json:"last_task_id"`
	Tasks        []PipelineTask `json:"tasks"`
}

// ─── Args ────────────────────────────────────────────────────────────────────

type BatchLLMArgs struct {
	Filename string `json:"filename"`
	Count    int    `json:"count,omitempty"`
	PoolSize int    `json:"pool_size,omitempty"` // sub-session pool reuse; default = count (max 10)
}

// ─── Dispatch callback (avoids circular import of toolhandler) ─────────────

type dispatchFunc func(toolName string, args map[string]interface{}, depth int) *types.ToolResult

// ─── Handler context ────────────────────────────────────────────────────────

type batchCtx struct {
	logger          *zap.Logger
	workDir         string
	dbDir           string
	parentSession   string // main session ID (parent for sub-sessions)
	llmProtocol     string
	llmModel        string
	llmAPIKey       string
	llmAPIURL       string
	thinking        bool
	reasoningEffort string
	writeEvent      func(eventType string, payload map[string]interface{})
	dispatch        dispatchFunc
	noChrome        bool
	codeIndexer     *codeindex.Indexer
}

// Package-level cancellation — set by toolhandler.PropagateConfig.
var batchCancelCtx context.Context

// SetCancelCtx sets the cancellation context for batch_llm operations.
func SetCancelCtx(ctx context.Context) {
	batchCancelCtx = ctx
}

// checkCancel returns the cancellation error if cancelled.
func checkCancel() error {
	if batchCancelCtx != nil {
		select {
		case <-batchCancelCtx.Done():
			return batchCancelCtx.Err()
		default:
		}
	}
	return nil
}

// ─── Entry point ─────────────────────────────────────────────────────────────

func HandleBatchLLM(
	logger *zap.Logger,
	workDir, dbDir, session string,
	llmProtocol, llmModel, llmAPIKey, llmAPIURL string,
	thinking bool, reasoningEffort string,
	writeEvent func(string, map[string]interface{}),
	dispatch dispatchFunc,
	noChrome bool,
	codeIndexer *codeindex.Indexer,
	args map[string]interface{},
) *BatchResult {

	var a BatchLLMArgs
	if err := parseArgs(args, &a); err != nil {
		return fail("args", fmt.Sprintf("invalid arguments: %v", err))
	}
	if a.Filename == "" {
		return fail("args", "filename is required")
	}
	if a.Count <= 0 {
		a.Count = 1
	}
	if a.Count > 10 {
		a.Count = 10
	}
	// Pool size defaults to count; cap at 10.
	poolSize := a.PoolSize
	if poolSize <= 0 {
		poolSize = a.Count
	}
	if poolSize > 10 {
		poolSize = 10
	}

	// Resolve pipeline path
	pipelinePath := a.Filename
	if !filepath.IsAbs(pipelinePath) {
		pipelinePath = filepath.Join(workDir, pipelinePath)
	}
	pipelinePath = filepath.Clean(pipelinePath)

	// Read pipeline JSON
	pipeline, err := readPipeline(pipelinePath)
	if err != nil {
		return fail("read", fmt.Sprintf("failed to read pipeline file %s: %v", a.Filename, err))
	}

	// Find pending tasks
	pending := findPending(pipeline.Tasks)
	if len(pending) == 0 {
		return &BatchResult{
			Success:  true,
			Done:     true,
			Output:   "所有任务已完成，请验收。",
			Filename: a.Filename,
		}
	}

	ctx := &batchCtx{
		logger:          logger,
		workDir:         workDir,
		dbDir:           dbDir,
		parentSession:   session,
		llmProtocol:     llmProtocol,
		llmModel:        llmModel,
		llmAPIKey:       llmAPIKey,
		llmAPIURL:       llmAPIURL,
		thinking:        thinking,
		reasoningEffort: reasoningEffort,
		writeEvent:      writeEvent,
		dispatch:        dispatch,
		noChrome:        noChrome,
		codeIndexer:     codeIndexer,
	}

	// Count how many we'll run this time
	runCount := a.Count
	if runCount > len(pending) {
		runCount = len(pending)
	}

	// Check if this batch has a submit_prompt to return
	// count=1 AND (global submit_prompt OR task submit_prompt) → return after one task
	hasSubmit := false
	var submitText string
	if a.Count == 1 {
		if pending[0].SubmitPrompt != "" {
			hasSubmit = true
			submitText = pending[0].SubmitPrompt
		} else if pipeline.SubmitPrompt != "" {
			hasSubmit = true
			submitText = pipeline.SubmitPrompt
		}
	}

	if hasSubmit {
		// Single task with submit_prompt → create sub-session, run it, return result
		subSession := uuid.New().String()
		createSubSessions(ctx, []string{subSession}, pipeline.SystemPrompt[:min(80, len(pipeline.SystemPrompt))])
		if ctx.writeEvent != nil {
			ctx.writeEvent("progress", map[string]interface{}{
				"session_id": subSession,
				"message":        "started",
			})
		}

		task := &pending[0]
		result := executeTask(ctx, pipeline, task, subSession)

		if result.err != "" {
			markTaskDone(pipelinePath, pipeline, task.ID, "error", pipeline.LastTaskID, subSession, result.turnID)
			if ctx.writeEvent != nil {
				ctx.writeEvent("error", map[string]interface{}{
					"session_id": subSession,
					"error":          result.err,
				})
			}
			return fail("execute", fmt.Sprintf("task #%d (%s) failed: %s", task.ID, task.Name, result.err))
		}
		markTaskDone(pipelinePath, pipeline, task.ID, "done", task.ID, subSession, result.turnID)

		return &BatchResult{
			Success:      true,
			Done:         allDone(pipeline.Tasks),
			Output:       fmt.Sprintf("任务 #%d (%s) 已完成。\n\n%s\n\n=== 请检查结果 ===\n%s", task.ID, task.Name, result.output, submitText),
			Filename:     a.Filename,
			SubmitPrompt: submitText,
		}
	}

	// Auto-loop: run batch in background, wait for all tasks.
	// Create pool sessions upfront for reuse across tasks.
	poolSessions := make([]string, poolSize)
	for i := 0; i < poolSize; i++ {
		sid := uuid.New().String()
		poolSessions[i] = sid
	}
	createSubSessions(ctx, poolSessions, pipeline.SystemPrompt[:min(80, len(pipeline.SystemPrompt))])

	// Sliding window execution
	var mu sync.Mutex
	var wg sync.WaitGroup
	nextIdx := 0
	activeCount := 0
	done := false

	for !done {
		// Check cancellation at top of each loop iteration
		if err := checkCancel(); err != nil {
			// Mark running tasks as cancelled and return partial results
			mu.Lock()
			for i := nextIdx; i < len(pipeline.Tasks); i++ {
				markTaskDone(pipelinePath, pipeline, pipeline.Tasks[i].ID, "error", pipeline.LastTaskID, "", "cancelled")
			}
			mu.Unlock()
			return &BatchResult{
				Success:  false,
				Done:     false,
				Output:   "batch_llm cancelled",
				Filename: a.Filename,
			}
		}
		mu.Lock()
		// Fill available slots
		//
		// Locking pattern:
		//   mu.Lock() at line 259 / entry.
		//   For pending tasks: unlock → launch goroutine → re-lock for next iteration check.
		//   For `continue` (non-pending): lock stays held (nextIdx is only accessed here).
		//   After loop: mu.Unlock() releases the entry lock (or the last re-acquired lock).
		//
		// This ensures the goroutine (activeCount--) can proceed between Unlock and
		// re-Lock, while the rest of the loop runs under the lock.
		for activeCount < a.Count && nextIdx < len(pipeline.Tasks) {
			if pipeline.Tasks[nextIdx].Status != "pending" {
				nextIdx++
				continue
			}
			task := &pipeline.Tasks[nextIdx]
			task.Status = "running"
			activeCount++
			nextIdx++   // increment before releasing the lock
			mu.Unlock() // release so goroutines can modify activeCount

			// Each task reuses a session from the pool by task ID.
			wg.Add(1)
			go func(t *PipelineTask) {
				defer wg.Done()

				// Pick session from pool by task ID (stable mapping)
				poolIdx := (t.ID - 1) % len(poolSessions)
				if poolIdx < 0 {
					poolIdx = 0
				}
				subSession := poolSessions[poolIdx]

				if ctx.writeEvent != nil {
					ctx.writeEvent("progress", map[string]interface{}{
						"session_id": subSession,
						"message":        fmt.Sprintf("Task #%d (%s)", t.ID, t.Name),
					})
				}
				r := executeTask(ctx, pipeline, t, subSession)

				if r.err != "" {
					// Retry logic
					if t.RetryCount < t.Retry {
						t.RetryCount++
						t.Status = "pending"
						mu.Lock()
						activeCount--
						mu.Unlock()
						if ctx.writeEvent != nil {
							ctx.writeEvent("progress", map[string]interface{}{
								"session_id": subSession,
								"message":        fmt.Sprintf("retry %d/%d", t.RetryCount, t.Retry),
							})
						}
						return
					}
					markTaskDone(pipelinePath, pipeline, t.ID, "error", pipeline.LastTaskID, subSession, r.turnID)
					if ctx.writeEvent != nil {
						ctx.writeEvent("error", map[string]interface{}{
							"session_id": subSession,
							"error":          r.err,
						})
					}
				} else {
					markTaskDone(pipelinePath, pipeline, t.ID, "done", t.ID, subSession, r.turnID)
				}
				mu.Lock()
				activeCount--
				mu.Unlock()
			}(task)

			// Re-acquire lock for the next iteration's condition check / continue.
			// This also lets the for-loop exit with the lock held, so the
			// mu.Unlock() below is the single matching release.
			mu.Lock()
		}
		mu.Unlock()

		mu.Lock()
		if activeCount == 0 && nextIdx >= len(pipeline.Tasks) {
			done = true
		}
		mu.Unlock()

		time.Sleep(200 * time.Millisecond)
	}

	wg.Wait()

	// All done — re-read pipeline to get final status
	finalPipeline, _ := readPipeline(pipelinePath)
	allComplete := allDone(finalPipeline.Tasks)

	return &BatchResult{
		Success:  true,
		Done:     allComplete,
		Output:   "所有任务已完成，请验收。",
		Filename: a.Filename,
	}
}

// ─── Task execution (full tool loop) ─────────────────────────────────────────

type taskResult struct {
	output string
	err    string
	turnID string
}

func executeTask(ctx *batchCtx, pipeline *Pipeline, task *PipelineTask, subSession string) taskResult {
	turnID := uuid.New().String()

	// Create turn in DB and save user message — single connection
	if ctx.dbDir != "" {
		sqlDB, err := db.Open(ctx.dbDir)
		if err == nil {
			turn := models.NewTurn(turnID, subSession)
			_ = db.CreateTurn(sqlDB, turn)

			msg := models.NewMessage(uuid.New().String(), turnID, "user", "text", task.Prompt)
			_ = db.AddMessage(sqlDB, msg)

			db.Close(sqlDB)
		}
	}

	// ── Load session history (for retry tasks with existing session) ──
	var historyMessages []llm.Message
	if task.SessionID != "" && ctx.dbDir != "" {
		sqlDB, err := db.Open(ctx.dbDir)
		if err == nil {
			historyMessages, _ = sessionutil.LoadSessionMessages(sqlDB, task.SessionID)
			db.Close(sqlDB)
		}
	}

	// Build LLM messages
	var messages []llm.Message
	// History first (if any — from previous retry attempt)
	messages = append(messages, historyMessages...)
	// System prompt
	messages = append(messages, llm.Message{Role: "system", Content: pipeline.SystemPrompt})
	// Current task prompt
	messages = append(messages, llm.Message{Role: "user", Content: task.Prompt})

	// Create LLM client
	client := llm.NewClient(ctx.llmProtocol, ctx.llmModel, ctx.llmAPIKey, ctx.llmAPIURL, ctx.logger)

	// Build tool definitions: filter web tools if no Chrome
	allTools := discover.NewDiscoverer().ListBuiltinTools()
	var filtered []discover.ToolDefinition
	for _, t := range allTools {
		if ctx.noChrome && t.Category == "web" {
			continue
		}
		filtered = append(filtered, t)
	}
	toolDefs := toLLMToolDefs(filtered)

	runner := &llm.TurnRunner{
		Client:   client,
		Logger:   ctx.logger,
		CancelCx: batchCancelCtx,
		Callbacks: llm.TurnCallbacks{
			OnChunk: func(chunk llm.StreamEvent) {
				if ctx.writeEvent == nil {
					return
				}
				payload := map[string]interface{}{
					"session_id": subSession,
					"task_id":        task.ID,
					"task_name":      task.Name,
				}
				if chunk.ReasoningContent != "" {
					payload["content"] = chunk.ReasoningContent
					payload["type"] = "reasoning"
					payload["index"] = chunk.Index
				}
				if chunk.Content != "" {
					payload["content"] = chunk.Content
					payload["type"] = "text"
					payload["index"] = chunk.Index
				}
				if chunk.Error != nil {
					payload["error"] = chunk.Error.Error()
				}
				ctx.writeEvent("message_chunk", payload)
			},

			OnAssistantMsg: func(msg llm.Message) ([]string, error) {
				toolCallIDs := saveAssistantMsg(ctx, turnID, msg)
				return toolCallIDs, nil
			},

			OnToolCall: func(tc llm.ToolCall, args map[string]interface{}) (string, error) {
				ctx.logger.Info("batch_llm: dispatching tool",
					zap.String("tool", tc.Function.Name),
					zap.String("tool_call_id", tc.ID),
					zap.Any("args", args),
				)
				if ctx.writeEvent != nil {
					ctx.writeEvent("tool_call", map[string]interface{}{
						"session_id": subSession,
						"task_id":        task.ID,
						"task_name":      task.Name,
						"tool":           tc.Function.Name,
						"tool_call_id":   tc.ID,
						"arguments":      tc.Function.Arguments,
					})
				}
				return "", nil
			},

			OnToolResult: func(tc llm.ToolCall, resultStr string, success bool) error {
				if ctx.writeEvent != nil {
					ctx.writeEvent("tool_result", map[string]interface{}{
						"session_id": subSession,
						"task_id":        task.ID,
						"task_name":      task.Name,
						"tool":           tc.Function.Name,
						"tool_call_id":   tc.ID,
						"success":        success,
						"result":         resultStr,
					})
				}
				saveToolMsg(ctx, turnID, llm.Message{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    resultStr,
				}, tc.Function.Name)
				return nil
			},

			OnIterationEnd: func(iter int, reasoningContent string, toolCallMsgIDs []string) {
				if reasoningContent != "" && len(toolCallMsgIDs) > 0 {
					brief := extractBrief(reasoningContent)
					sqlDB, err := db.Open(ctx.dbDir)
					if err != nil {
						return
					}
					defer db.Close(sqlDB)
					for _, msgID := range toolCallMsgIDs {
						_ = db.UpdateMessageBrief(sqlDB, msgID, brief)
					}
				}
			},

			OnLLMError: func(code, message string, retryable bool, attempt, maxAttempts int) {
				if ctx.writeEvent == nil {
					return
				}
				ctx.writeEvent("llm_error", map[string]interface{}{
					"session_id": subSession,
					"task_id":        task.ID,
					"task_name":      task.Name,
					"code":           code,
					"message":        message,
					"retryable":      retryable,
					"retry_attempt":  attempt,
					"retry_count":    maxAttempts,
				})
			},

			OnLLMRetry: func(attempt, maxAttempts int, waitSeconds int) {
				if ctx.writeEvent == nil {
					return
				}
				ctx.writeEvent("llm_retry", map[string]interface{}{
					"session_id": subSession,
					"task_id":        task.ID,
					"task_name":      task.Name,
					"retry_attempt":  attempt,
					"retry_count":    maxAttempts,
					"wait_seconds":   waitSeconds,
				})
			},
		},
		Dispatch: func(toolName string, args map[string]interface{}, depth int) (string, bool, error) {
			result := ctx.dispatch(toolName, args, depth)
			resultStr := types.FormatToolResultJSON(toolName, result)
			return resultStr, result.Success, nil
		},
	}

	result, err := runner.Run(llm.TurnConfig{
		Messages:          messages,
		Tools:             toolDefs,
		MaxIter:           100,
		MaxAttempts:       3,
		RetryDelaySeconds: 5,
		MaxTokens:         65535,
		Temperature:       0.7,
		Thinking:          ctx.thinking,
		ReasoningEffort:   ctx.reasoningEffort,
	})

	output := ""
	if result != nil {
		output = result.Content
	}

	errorStr := ""
	if err != nil {
		errorStr = err.Error()
	} else if result != nil && result.Cancelled {
		errorStr = "cancelled"
	}

	// Emit complete event so frontend marks sub-session as done
	if ctx.writeEvent != nil {
		ctx.writeEvent("complete", map[string]interface{}{
			"session_id": subSession,
			"task_id":        task.ID,
			"task_name":      task.Name,
			"output":         output,
		})
	}

	// ── Flush code index changes made by this task ──
	if ctx.codeIndexer != nil {
		n := ctx.codeIndexer.FlushChangedFiles()
		if n > 0 {
			ctx.logger.Info("batch_llm: flushed code index changes",
				zap.String("sub_session", subSession),
				zap.Int("task_id", task.ID),
				zap.Int("count", n))
		}
	}

	// ── Generate sub-session summary (async) ──
	if ctx.dbDir != "" && subSession != "" && turnID != "" {
		go generateSubSessionSummary(ctx.logger, ctx.llmProtocol, ctx.llmModel,
			ctx.llmAPIKey, ctx.llmAPIURL, ctx.dbDir, subSession, turnID)
	}

	return taskResult{output: output, err: errorStr, turnID: turnID}
}

// ─── DB helpers ──────────────────────────────────────────────────────────────

// extractBrief returns the first 3 non-empty lines of text.
func extractBrief(text string) string {
	lines := strings.SplitN(text, "\n", 4)
	var brief strings.Builder
	count := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if count > 0 {
			brief.WriteString("\n")
		}
		brief.WriteString(trimmed)
		count++
		if count >= 3 {
			break
		}
	}
	return brief.String()
}

func saveAssistantMsg(ctx *batchCtx, turnID string, msg llm.Message) []string {
	if ctx.dbDir == "" {
		return nil
	}
	sqlDB, err := db.Open(ctx.dbDir)
	if err != nil {
		return nil
	}
	defer db.Close(sqlDB)

	var toolCallIDs []string

	// 1. Reasoning content (if any)
	if msg.ReasoningContent != "" {
		dbMsg := models.NewMessage(uuid.New().String(), turnID, "assistant", "reasoning", msg.ReasoningContent)
		_ = db.AddMessage(sqlDB, dbMsg)
	}

	// 2. Text content (if any)
	if msg.Content != "" {
		dbMsg := models.NewMessage(uuid.New().String(), turnID, "assistant", "text", msg.Content)
		_ = db.AddMessage(sqlDB, dbMsg)
	}

	// 3. Tool calls (if any)
	if len(msg.ToolCalls) > 0 {
		for _, tc := range msg.ToolCalls {
			tcPayload, err := json.Marshal(models.ToolCallPayload{
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
				Arguments:  tc.Function.Arguments,
			})
			if err != nil {
				ctx.logger.Warn("saveAssistantMsg: marshal tool call failed", zap.Error(err))
				continue
			}
			dbMsg := models.NewMessage(uuid.New().String(), turnID, "assistant", "tool_call", string(tcPayload))
			if err := db.AddMessage(sqlDB, dbMsg); err != nil {
				ctx.logger.Warn("saveAssistantMsg: save tool call failed", zap.Error(err))
				continue
			}
			toolCallIDs = append(toolCallIDs, dbMsg.MessageID)
		}
	}

	return toolCallIDs
}

func saveToolMsg(ctx *batchCtx, turnID string, msg llm.Message, toolName string) {
	if ctx.dbDir == "" {
		return
	}
	sqlDB, err := db.Open(ctx.dbDir)
	if err != nil {
		return
	}
	defer db.Close(sqlDB)
	toolResult, err := json.Marshal(models.ToolResultPayload{
		ToolCallID: msg.ToolCallID,
		Name:       toolName,
		Result:     msg.Content,
	})
	if err != nil {
		return
	}
	dbMsg := models.NewMessage(uuid.New().String(), turnID, msg.Role, "tool_result", string(toolResult))
	_ = db.AddMessage(sqlDB, dbMsg)
}

// ─── Tool definition conversion ──────────────────────────────────────────────

func toLLMToolDefs(discTools []discover.ToolDefinition) []llm.ToolDefinition {
	result := make([]llm.ToolDefinition, 0, len(discTools))
	for _, t := range discTools {
		paramsData, _ := json.Marshal(t.Parameters)
		result = append(result, llm.ToolDefinition{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  json.RawMessage(paramsData),
		})
	}
	return result
}

// ─── Pipeline file helpers ───────────────────────────────────────────────────

func readPipeline(path string) (*Pipeline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Pipeline
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	// Set defaults
	for i := range p.Tasks {
		if p.Tasks[i].Retry <= 0 {
			p.Tasks[i].Retry = 3 // default retry count
		}
		if p.Tasks[i].Status == "" {
			p.Tasks[i].Status = "pending"
		}
	}
	return &p, nil
}

func writePipeline(path string, p *Pipeline) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func markTaskDone(path string, p *Pipeline, taskID int, status string, lastTaskID int, sessionID, turnID string) {
	for i := range p.Tasks {
		if p.Tasks[i].ID == taskID {
			p.Tasks[i].Status = status
			p.Tasks[i].SessionID = sessionID
			p.Tasks[i].TurnID = turnID
			break
		}
	}
	if lastTaskID > p.LastTaskID {
		p.LastTaskID = lastTaskID
	}
	_ = writePipeline(path, p)
}

func findPending(tasks []PipelineTask) []PipelineTask {
	var pending []PipelineTask
	for _, t := range tasks {
		if t.Status == "pending" {
			pending = append(pending, t)
		}
	}
	return pending
}

func allDone(tasks []PipelineTask) bool {
	for _, t := range tasks {
		if t.Status != "done" && t.Status != "error" {
			return false
		}
	}
	return true
}

// ─── Sub-session helpers ────────────────────────────────────────────────────

func createSubSessions(ctx *batchCtx, sessionIDs []string, title string) {
	if ctx.dbDir == "" || ctx.parentSession == "" {
		return
	}
	sqlDB, err := db.Open(ctx.dbDir)
	if err != nil {
		ctx.logger.Warn("batch_llm: failed to open DB for sub-sessions", zap.Error(err))
		return
	}
	defer db.Close(sqlDB)

	for _, sid := range sessionIDs {
		subSession := models.NewSession(sid, ctx.parentSession, ctx.workDir, title)
		if err := db.CreateSession(sqlDB, subSession); err != nil {
			ctx.logger.Warn("batch_llm: failed to create sub-session",
				zap.String("session_id", sid), zap.Error(err))
		}
	}
}

// ─── Return type ─────────────────────────────────────────────────────────────

// BatchResult is returned to the calling tool dispatch.
type BatchResult struct {
	Success      bool   `json:"success"`
	Done         bool   `json:"done"`
	Output       string `json:"output"`
	Error        string `json:"error,omitempty"`
	Filename     string `json:"filename"`
	SubmitPrompt string `json:"submit_prompt,omitempty"`
}

func fail(reason, msg string) *BatchResult {
	return &BatchResult{
		Success: false,
		Error:   fmt.Sprintf("[%s] %s", reason, msg),
	}
}

// ToToolResult converts BatchResult to *types.ToolResult for the dispatch system.
func (r *BatchResult) ToToolResult() *types.ToolResult {
	if r.Success {
		emoji := "✅"
		if !r.Done {
			emoji = "⚙️"
		}
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("%s %s", emoji, r.Output),
			Tool:    "batch_llm",
			RawResult: map[string]interface{}{
				"done":          r.Done,
				"output":        r.Output,
				"filename":      r.Filename,
				"submit_prompt": r.SubmitPrompt,
			},
		}
	}
	return &types.ToolResult{
		Success: false,
		Error:   r.Error,
		Tool:    "batch_llm",
		Output:  fmt.Sprintf("❌ %s", r.Output),
		RawResult: map[string]interface{}{
			"error":    r.Error,
			"output":   r.Output,
			"filename": r.Filename,
		},
	}
}

func parseArgs(args map[string]interface{}, target interface{}) error {
	data, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

// ─── Sub-session summary helpers ────────────────────────────────────────────

// generateSubSessionSummary generates and saves a session summary for a sub-session.
func generateSubSessionSummary(logger *zap.Logger, llmProtocol, llmModel, llmAPIKey, llmAPIURL string,
	dbDir, sessionID, turnID string) {

	sqlDB, err := db.Open(dbDir)
	if err != nil {
		logger.Warn("batch_llm summary: failed to open DB", zap.Error(err))
		return
	}
	defer db.Close(sqlDB)

	turnMessages, err := db.GetMessagesByTurn(sqlDB, turnID)
	if err != nil {
		logger.Warn("batch_llm summary: failed to load turn messages", zap.Error(err))
		return
	}
	latestTurnText := batchFormatTurnSummary(turnMessages)
	if latestTurnText == "" {
		return
	}

	oldSummary, err := db.GetLatestSummary(sqlDB, sessionID)
	if err != nil || oldSummary == "" {
		oldSummary = "{}"
	}

	userContent := "Summarize the following conversation turn and update the existing summary.\n\n" +
		"Existing summary:\n" + oldSummary + "\n\n" +
		"New turn:\n" + latestTurnText + "\n\n" +
		"Output a JSON object with keys: goals, completed, current_state, next_steps. Keep it concise (2-3 sentences per field)."

	client := llm.NewClient(llmProtocol, llmModel, llmAPIKey, llmAPIURL, logger)
	ch, err := client.Chat([]llm.Message{
		{Role: "user", Content: userContent},
	}, llm.ChatOptions{
		Model:       llmModel,
		Temperature: 0.1,
		MaxTokens:   4096,
	})
	if err != nil {
		logger.Warn("batch_llm summary: LLM call failed", zap.Error(err))
		return
	}

	var result strings.Builder
	for evt := range ch {
		if evt.Error != nil {
			logger.Warn("batch_llm summary: LLM stream error", zap.Error(evt.Error))
			return
		}
		result.WriteString(evt.Content)
	}

	summaryJSON := strings.TrimSpace(result.String())
	if summaryJSON == "" {
		logger.Warn("batch_llm summary: empty response from LLM")
		return
	}

	if !json.Valid([]byte(summaryJSON)) {
		logger.Warn("batch_llm summary: response is not valid JSON",
			zap.String("raw", batchTruncateStr(summaryJSON, 200)))
		return
	}

	if err := db.SaveSummary(sqlDB, sessionID, summaryJSON, turnID); err != nil {
		logger.Warn("batch_llm summary: failed to save", zap.Error(err))
		return
	}

	logger.Info("batch_llm summary: sub-session summary updated",
		zap.String("session_id", sessionID),
		zap.Int("summary_bytes", len(summaryJSON)))
}

func batchFormatTurnSummary(messages []*models.Message) string {
	var b strings.Builder
	for i, m := range messages {
		if i > 0 {
			b.WriteString("\n")
		}
		switch m.Role {
		case "user":
			b.WriteString("User:\n")
			b.WriteString(m.Content)
		case "assistant":
			switch m.Type {
			case "text":
				b.WriteString("Assistant:\n")
				b.WriteString(m.Content)
			case "reasoning":
				b.WriteString("[thinking]\n")
				b.WriteString(m.Content)
			case "tool_call":
				var tc models.ToolCallPayload
				if err := json.Unmarshal([]byte(m.Content), &tc); err == nil {
					b.WriteString("[tool: ")
					b.WriteString(tc.Name)
					b.WriteString("]\n")
					b.WriteString(tc.Arguments)
				}
			}
		case "tool":
			var tp models.ToolResultPayload
			if err := json.Unmarshal([]byte(m.Content), &tp); err == nil {
				b.WriteString("[result]\n")
				b.WriteString(tp.Result)
			} else {
				b.WriteString("[result]\n")
				b.WriteString(m.Content)
			}
		}
	}
	return b.String()
}

func batchTruncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
