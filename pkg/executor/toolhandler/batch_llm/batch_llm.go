package batch_llm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/internal/models"
	"github.com/chonkpilot/chonkpilot/pkg/executor/discover"
	"github.com/chonkpilot/chonkpilot/pkg/executor/llm"
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
}

// ─── Dispatch callback (avoids circular import of toolhandler) ─────────────

type dispatchFunc func(toolName string, args map[string]interface{}, depth int) *types.ToolResult

// ─── Handler context ────────────────────────────────────────────────────────

type batchCtx struct {
	logger          *zap.Logger
	workDir         string
	dbDir           string
	parentSession   string // main session ID (parent for sub-sessions)
	llmProvider     string
	llmModel        string
	llmAPIKey       string
	llmAPIURL       string
	thinking        bool
	reasoningEffort string
	writeEvent      func(eventType string, payload map[string]interface{})
	dispatch        dispatchFunc
	noChrome        bool
}

// ─── Entry point ─────────────────────────────────────────────────────────────

func HandleBatchLLM(
	logger *zap.Logger,
	workDir, dbDir, session string,
	llmProvider, llmModel, llmAPIKey, llmAPIURL string,
	thinking bool, reasoningEffort string,
	writeEvent func(string, map[string]interface{}),
	dispatch dispatchFunc,
	noChrome bool,
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
		llmProvider:     llmProvider,
		llmModel:        llmModel,
		llmAPIKey:       llmAPIKey,
		llmAPIURL:       llmAPIURL,
		thinking:        thinking,
		reasoningEffort: reasoningEffort,
		writeEvent:      writeEvent,
		dispatch:        dispatch,
		noChrome:        noChrome,
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
				"sub_session_id": subSession,
				"message":        "started",
			})
		}

		task := &pending[0]
		result := executeTask(ctx, pipeline, task, subSession)

		if result.err != "" {
			markTaskDone(pipelinePath, pipeline, task.ID, "error", pipeline.LastTaskID, subSession, result.turnID)
			if ctx.writeEvent != nil {
				ctx.writeEvent("error", map[string]interface{}{
					"sub_session_id": subSession,
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

	// Auto-loop: run batch in background, wait for all tasks
	workerSubSessions := make([]string, a.Count)
	for i := 0; i < a.Count; i++ {
		workerSubSessions[i] = uuid.New().String()
	}
	createSubSessions(ctx, workerSubSessions, pipeline.SystemPrompt[:min(80, len(pipeline.SystemPrompt))])

	// Notify frontend about new sub-sessions so Task panel refreshes
	for _, subSess := range workerSubSessions {
		if ctx.writeEvent != nil {
			ctx.writeEvent("progress", map[string]interface{}{
				"sub_session_id": subSess,
				"message":        "pending",
			})
		}
	}

	// Sliding window execution
	var mu sync.Mutex
	var wg sync.WaitGroup
	nextIdx := 0
	activeCount := 0
	done := false

	for !done {
		mu.Lock()
		// Fill available slots
		for activeCount < a.Count && nextIdx < len(pipeline.Tasks) {
			if pipeline.Tasks[nextIdx].Status != "pending" {
				nextIdx++
				continue
			}
			task := &pipeline.Tasks[nextIdx]
			task.Status = "running"
			workerID := activeCount
			subSess := workerSubSessions[workerID]
			activeCount++
			mu.Unlock()

			wg.Add(1)
			go func(t *PipelineTask, subSession string) {
				defer wg.Done()
				if ctx.writeEvent != nil {
					ctx.writeEvent("progress", map[string]interface{}{
						"sub_session_id": subSession,
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
								"sub_session_id": subSession,
								"message":        fmt.Sprintf("retry %d/%d", t.RetryCount, t.Retry),
							})
						}
						return
					}
					markTaskDone(pipelinePath, pipeline, t.ID, "error", pipeline.LastTaskID, subSession, r.turnID)
					if ctx.writeEvent != nil {
						ctx.writeEvent("error", map[string]interface{}{
							"sub_session_id": subSession,
							"error":          r.err,
						})
					}
				} else {
					markTaskDone(pipelinePath, pipeline, t.ID, "done", t.ID, subSession, r.turnID)
				}
				mu.Lock()
				activeCount--
				mu.Unlock()
			}(task, subSess)

			mu.Lock()
			nextIdx++
			mu.Unlock()
		}
		mu.Unlock()

		if activeCount == 0 && nextIdx >= len(pipeline.Tasks) {
			done = true
		}

		time.Sleep(200 * time.Millisecond)
	}

	wg.Wait()

	// All done — re-read pipeline to get final status
	finalPipeline, _ := readPipeline(pipelinePath)
	allComplete := allDone(finalPipeline.Tasks)

	// Emit completion for any sub-sessions still showing "pending"
	for _, subSess := range workerSubSessions {
		if ctx.writeEvent != nil {
			ctx.writeEvent("complete", map[string]interface{}{
				"sub_session_id": subSess,
				"output":         "batch completed",
			})
		}
	}

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

	// Create turn in DB — use sub-session ID so Task panel can find it
	if ctx.dbDir != "" {
		sqlDB, err := db.Open(ctx.dbDir)
		if err == nil {
			turn := models.NewTurn(turnID, subSession)
			_ = db.CreateTurn(sqlDB, turn)
			db.Close(sqlDB)
		}
	}

	// Save user message to DB
	if ctx.dbDir != "" {
		sqlDB, err := db.Open(ctx.dbDir)
		if err == nil {
			msg := models.NewMessage(uuid.New().String(), turnID, "user", "text", task.Prompt)
			_ = db.AddMessage(sqlDB, msg)
			db.Close(sqlDB)
		}
	}

	// Build LLM messages (no history — just system_prompt + task.prompt)
	messages := []llm.Message{
		{Role: "system", Content: pipeline.SystemPrompt},
		{Role: "user", Content: task.Prompt},
	}

	// Create LLM client
	client := llm.NewClient(ctx.llmProvider, ctx.llmModel, ctx.llmAPIKey, ctx.llmAPIURL, ctx.logger)

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

	// Tool loop
	maxIter := 100
	var fullContent strings.Builder

	for iter := 0; iter < maxIter; iter++ {
		// Call LLM
		stream, err := client.Chat(messages, llm.ChatOptions{
			Stream:          true,
			Tools:           toolDefs,
			Model:           ctx.llmModel,
			Temperature:     0.7,
			MaxTokens:       65535,
			Thinking:        ctx.thinking,
			ReasoningEffort: ctx.reasoningEffort,
		})
		if err != nil {
			return taskResult{err: fmt.Sprintf("LLM call failed: %v", err)}
		}

		// Read stream, collect text + tool calls
		var toolCalls []*llm.ToolCall
		var textContent, reasoningContent strings.Builder

		for chunk := range stream {
			if chunk.Error != nil {
				return taskResult{err: fmt.Sprintf("LLM stream error: %v", chunk.Error)}
			}
			if chunk.ReasoningContent != "" {
				reasoningContent.WriteString(chunk.ReasoningContent)
			}
			if chunk.Content != "" {
				textContent.WriteString(chunk.Content)
			}
			if chunk.ToolCall != nil {
				toolCalls = append(toolCalls, chunk.ToolCall)
			}
		}

		// Build assistant message
		assistantMsg := llm.Message{
			Role:             "assistant",
			Content:          textContent.String(),
			ReasoningContent: reasoningContent.String(),
		}
		if len(toolCalls) > 0 {
			assistantMsg.ToolCalls = make([]llm.ToolCall, len(toolCalls))
			for i, tc := range toolCalls {
				assistantMsg.ToolCalls[i] = *tc
			}
		}
		messages = append(messages, assistantMsg)
		fullContent.WriteString(textContent.String())

		// Save assistant message to DB
		saveAssistantMsg(ctx, turnID, assistantMsg)

		// No tool calls → done
		if len(toolCalls) == 0 {
			break
		}

		// Process each tool call
		for _, tc := range toolCalls {
			// Parse arguments
			var args map[string]interface{}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				ctx.logger.Warn("batch_llm: failed to parse tool call arguments", zap.Error(err), zap.String("raw", tc.Function.Arguments))
				errMsg := llm.Message{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    fmt.Sprintf(`{"error":"failed to parse arguments: %s"}`, err.Error()),
				}
				messages = append(messages, errMsg)
				continue
			}

			ctx.logger.Info("batch_llm: dispatching tool",
				zap.String("tool", tc.Function.Name),
				zap.String("tool_call_id", tc.ID),
				zap.Any("args", args),
			)

			// Emit tool call event for IDE Task panel tracking
			if ctx.writeEvent != nil {
				ctx.writeEvent("tool_call", map[string]interface{}{
					"sub_session_id": subSession,
					"task_id":        task.ID,
					"task_name":      task.Name,
					"tool":           tc.Function.Name,
					"arguments":      tc.Function.Arguments,
				})
			}

			// Dispatch tool via callback (routes to Handler.Dispatch)
			result := ctx.dispatch(tc.Function.Name, args, 0)

			// Format result
			resultStr := types.FormatToolResultJSON(tc.Function.Name, result)

			// Emit tool result event
			if ctx.writeEvent != nil {
				ctx.writeEvent("tool_result", map[string]interface{}{
					"sub_session_id": subSession,
					"task_id":        task.ID,
					"task_name":      task.Name,
					"tool":           tc.Function.Name,
					"success":        result.Success,
					"result":         resultStr,
				})
			}

			// Add and persist tool result message
			toolResultMsg := llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    resultStr,
			}
			messages = append(messages, toolResultMsg)
			saveToolMsg(ctx, turnID, toolResultMsg, tc.Function.Name)
		}
	}

	output := fullContent.String()

	// Emit complete event so frontend marks sub-session as done
	if ctx.writeEvent != nil {
		ctx.writeEvent("complete", map[string]interface{}{
			"sub_session_id": subSession,
			"task_id":        task.ID,
			"task_name":      task.Name,
			"output":         output,
		})
	}

	return taskResult{output: output, turnID: turnID}
}

// ─── DB helpers ──────────────────────────────────────────────────────────────

func saveAssistantMsg(ctx *batchCtx, turnID string, msg llm.Message) {
	if ctx.dbDir == "" {
		return
	}
	sqlDB, err := db.Open(ctx.dbDir)
	if err != nil {
		return
	}
	defer db.Close(sqlDB)
	dbMsg := models.NewMessage(uuid.New().String(), turnID, "assistant", "text", msg.Content)
	_ = db.AddMessage(sqlDB, dbMsg)
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
	msgType := "tool"
	dbMsg := models.NewMessage(uuid.New().String(), turnID, msg.Role, msgType, msg.Content)
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
		return &types.ToolResult{
			Success: true,
			Output:  r.Output,
			Tool:    "batch_llm",
		}
	}
	return &types.ToolResult{
		Success: false,
		Error:   r.Error,
		Tool:    "batch_llm",
		Output:  r.Output,
	}
}

func parseArgs(args map[string]interface{}, target interface{}) error {
	data, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
