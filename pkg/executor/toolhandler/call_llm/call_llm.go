package call_llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/internal/models"
	"github.com/chonkpilot/chonkpilot/pkg/executor/codeindex"
	"github.com/chonkpilot/chonkpilot/pkg/executor/discover"
	"github.com/chonkpilot/chonkpilot/pkg/executor/llm"
	"github.com/chonkpilot/chonkpilot/pkg/executor/sessionutil"
	run "github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/task"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// Package-level cancellation — set by toolhandler.PropagateConfig.
var callCancelCtx context.Context
var callCancelFunc context.CancelFunc

// SetCancelCtx sets the cancellation context for call_llm operations.
func SetCancelCtx() {
	callCancelCtx, callCancelFunc = context.WithCancel(context.Background())
}

// Cancel triggers cancellation for call_llm operations.
func Cancel() {
	if callCancelFunc != nil {
		callCancelFunc()
	}
}

// HandleCallLLM runs a sub-task in the current process using TurnRunner,
// with its own session/turn in DB and independent LLM tool loop.
// dispatch is the Handler's tool dispatch function, used for tool execution.
// codeIndexer is the project's codebase indexer (nil if disabled) — changes made
// by this sub-turn are flushed to the index queue on completion.
func HandleCallLLM(logger *zap.Logger, session, turnID string, workDir string, dbDir string,
	tm *run.TaskManager,
	llmProtocol, llmModel, llmAPIKey, llmAPIURL string,
	thinking bool, reasoningEffort string,
	writeEvent func(string, map[string]interface{}), onProgress func(map[string]interface{}),
	codeIndexer *codeindex.Indexer,
	args map[string]interface{}, depth int,
	dispatch func(toolName string, args map[string]interface{}, depth int) *types.ToolResult) *types.ToolResult {

	if depth >= 5 {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("call_llm max depth %d exceeded", 5),
			Tool:    "call_llm",
			Output:  fmt.Sprintf("❌ 嵌套深度 %d 超过上限 %d", depth, 5),
			RawResult: map[string]interface{}{
				"error": fmt.Sprintf("max depth %d exceeded", 5),
			},
		}
	}

	// ── Required: title ──
	title, _ := args["title"].(string)
	if title == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "'title' is required — provide a human-readable description for this sub-task",
			Tool:    "call_llm",
			Output:  "❌ 缺少 title 参数",
			RawResult: map[string]interface{}{
				"error": "'title' is required",
			},
		}
	}

	promptFile, _ := args["prompt-file"].(string)
	var promptText string
	if promptFile == "" {
		promptText, _ = args["prompt"].(string)
		if promptText == "" {
			return &types.ToolResult{
				Success: false,
				Error:   "either 'prompt' (text) or 'prompt-file' (path) is required",
				Tool:    "call_llm",
				Output:  "❌ 缺少 prompt 或 prompt-file 参数",
				RawResult: map[string]interface{}{
					"error": "either 'prompt' or 'prompt-file' is required",
				},
			}
		}
	} else {
		// Read prompt from file
		content, err := os.ReadFile(promptFile)
		if err != nil {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("failed to read prompt file: %s", err.Error()),
				Tool:    "call_llm",
				Output:  fmt.Sprintf("❌ 读取 prompt 文件失败：%s", promptFile),
				RawResult: map[string]interface{}{
					"error": err.Error(),
				},
			}
		}
		promptText = string(content)
	}

	systemPrompt, _ := args["system-prompt"].(string)
	agent, _ := args["agent"].(string)

	// Determine sub-session: use provided session_id for continuation, or create new
	subSessionID, _ := args["session_id"].(string)
	if subSessionID == "" {
		subSessionID = uuid.New().String()
	}
	subTurnID := uuid.New().String()

	// Create sub-session in DB
	if session != "" {
		var sessErr error
		for attempt := 0; attempt < 3; attempt++ {
			sqlDB, err := db.Open(workDir)
			if err != nil {
				sessErr = err
				time.Sleep(time.Duration(attempt+1) * 300 * time.Millisecond)
				continue
			}
			subSession := models.NewSession(subSessionID, session, workDir, title)
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

	// Create sub-turn + save user message
	if workDir != "" {
		sqlDB, err := db.Open(workDir)
		if err == nil {
			turn := models.NewTurn(subTurnID, subSessionID)
			_ = db.CreateTurn(sqlDB, turn)
			msg := models.NewMessage(uuid.New().String(), subTurnID, "user", "text", promptText)
			_ = db.AddMessage(sqlDB, msg)
			db.Close(sqlDB)
		}
	}

	// ── Load session history (if continuing an existing session) ──
	var historyMessages []llm.Message
	if subSessionID != "" && args["session_id"] != nil {
		sqlDB, err := db.Open(dbDir)
		if err == nil {
			historyMessages, _ = sessionutil.LoadSessionMessages(sqlDB, subSessionID)
			db.Close(sqlDB)
		}
	}

	// ── Build messages ──
	var messages []llm.Message
	// Prepend history (loaded messages come first for context)
	messages = append(messages, historyMessages...)
	// Add current prompt
	messages = append(messages, llm.Message{Role: "user", Content: promptText})
	// System prompts first
	if systemPrompt != "" {
		messages = append([]llm.Message{{Role: "system", Content: systemPrompt}}, messages...)
	}
	if agent != "" {
		messages = append([]llm.Message{{Role: "system", Content: "You are " + agent + ". Follow the persona strictly."}}, messages...)
	}

	// ── Create LLM client ──
	client := llm.NewClient(llmProtocol, llmModel, llmAPIKey, llmAPIURL, logger)

	// ── Build tool definitions ──
	allTools := discover.NewDiscoverer().ListBuiltinTools()
	toolDefs := make([]llm.ToolDefinition, 0, len(allTools))
	for _, t := range allTools {
		paramsJSON, _ := json.Marshal(t.Parameters)
		toolDefs = append(toolDefs, llm.ToolDefinition{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  json.RawMessage(paramsJSON),
		})
	}

	async, _ := args["async"].(bool)

	// ── Launch via TaskManager ──
	taskID := tm.StartOperation(fmt.Sprintf("call_llm(%s)", title), func(cancel <-chan struct{}) (string, error) {
		// Create a context that cancels when either the task is stopped or global cancel fires
		done := make(chan struct{}, 1)
		go func() {
			select {
			case <-cancel:
				Cancel()
			case <-done:
			}
		}()
		defer func() { done <- struct{}{} }()

		runner := &llm.TurnRunner{
			Client:   client,
			Logger:   logger,
			CancelCx: callCancelCtx,
			Dispatch: func(toolName string, args map[string]interface{}, depth int) (string, bool, error) {
				result := dispatch(toolName, args, depth)
				resultStr := types.FormatToolResultJSON(toolName, result)
				return resultStr, result.Success, nil
			},
			Callbacks: llm.TurnCallbacks{
				OnChunk: func(chunk llm.StreamEvent) {
					if writeEvent != nil {
						payload := map[string]interface{}{
							"session_id": subSessionID,
							"sub_turn_id":    subTurnID,
							"depth":          depth + 1,
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
						writeEvent("message_chunk", payload)
					}
				},
				OnAssistantMsg: func(msg llm.Message) ([]string, error) {
					if workDir == "" {
						return nil, nil
					}
					sqlDB, err := db.Open(workDir)
					if err != nil {
						return nil, nil
					}
					defer db.Close(sqlDB)

					var toolCallIDs []string

					// Save reasoning
					if msg.ReasoningContent != "" {
						dbMsg := models.NewMessage(uuid.New().String(), subTurnID, "assistant", "reasoning", msg.ReasoningContent)
						_ = db.AddMessage(sqlDB, dbMsg)
					}
					// Save text
					if msg.Content != "" {
						dbMsg := models.NewMessage(uuid.New().String(), subTurnID, "assistant", "text", msg.Content)
						_ = db.AddMessage(sqlDB, dbMsg)
					}
					// Save tool calls (for brief tracking)
					if len(msg.ToolCalls) > 0 {
						for _, tc := range msg.ToolCalls {
							tcPayload, _ := json.Marshal(models.ToolCallPayload{
								ToolCallID: tc.ID,
								Name:       tc.Function.Name,
								Arguments:  tc.Function.Arguments,
							})
							dbMsg := models.NewMessage(uuid.New().String(), subTurnID, "assistant", "tool_call", string(tcPayload))
							if err := db.AddMessage(sqlDB, dbMsg); err == nil {
								toolCallIDs = append(toolCallIDs, dbMsg.MessageID)
							}
						}
					}
					return toolCallIDs, nil
				},
				OnToolCall: func(tc llm.ToolCall, args map[string]interface{}) (string, error) {
					if writeEvent != nil {
						writeEvent("tool_call", map[string]interface{}{
							"session_id": subSessionID,
							"sub_turn_id":    subTurnID,
							"depth":          depth + 1,
							"tool":           tc.Function.Name,
							"tool_call_id":   tc.ID,
							"arguments":      tc.Function.Arguments,
						})
					}
					return "", nil
				},
				OnToolResult: func(tc llm.ToolCall, resultStr string, success bool) error {
					if writeEvent != nil {
						writeEvent("tool_result", map[string]interface{}{
							"session_id": subSessionID,
							"sub_turn_id":    subTurnID,
							"depth":          depth + 1,
							"tool":           tc.Function.Name,
							"tool_call_id":   tc.ID,
							"success":        success,
							"result":         resultStr,
						})
					}
					// Save tool result message to DB
					if workDir != "" {
						sqlDB, err := db.Open(workDir)
						if err == nil {
							toolResultPayload, _ := json.Marshal(models.ToolResultPayload{
								ToolCallID: tc.ID,
								Name:       tc.Function.Name,
								Result:     resultStr,
							})
							dbMsg := models.NewMessage(uuid.New().String(), subTurnID, "tool", "tool_result", string(toolResultPayload))
							_ = db.AddMessage(sqlDB, dbMsg)
							db.Close(sqlDB)
						}
					}
					return nil
				},

				OnIterationEnd: func(iter int, reasoningContent string, toolCallMsgIDs []string) {
					if reasoningContent == "" || len(toolCallMsgIDs) == 0 || workDir == "" {
						return
					}
					brief := extractBrief(reasoningContent)
					sqlDB, err := db.Open(workDir)
					if err != nil {
						return
					}
					defer db.Close(sqlDB)
					for _, msgID := range toolCallMsgIDs {
						_ = db.UpdateMessageBrief(sqlDB, msgID, brief)
					}
				},

				OnLLMError: func(code, message string, retryable bool, attempt, maxAttempts int) {
					if writeEvent != nil {
						writeEvent("llm_error", map[string]interface{}{
							"session_id": subSessionID,
							"sub_turn_id":    subTurnID,
							"depth":          depth + 1,
							"code":           code,
							"message":        message,
							"retryable":      retryable,
							"retry_attempt":  attempt,
							"retry_count":    maxAttempts,
						})
					}
				},

				OnLLMRetry: func(attempt, maxAttempts int, waitSeconds int) {
					if writeEvent != nil {
						writeEvent("llm_retry", map[string]interface{}{
							"session_id": subSessionID,
							"sub_turn_id":    subTurnID,
							"depth":          depth + 1,
							"retry_attempt":  attempt,
							"retry_count":    maxAttempts,
							"wait_seconds":   waitSeconds,
						})
					}
				},

				OnComplete: func(result *llm.TurnResult) {
					if writeEvent != nil {
						writeEvent("complete", map[string]interface{}{
							"session_id": subSessionID,
							"sub_turn_id":    subTurnID,
							"depth":          depth + 1,
							"result":         result.Content,
							"cancelled":      result.Cancelled,
						})
					}
				},
			},
		}

		runResult, err := runner.Run(llm.TurnConfig{
			Messages:          messages,
			Tools:             toolDefs,
			MaxIter:           100,
			MaxAttempts:       3,
			RetryDelaySeconds: 5,
			MaxTokens:         65535,
			Temperature:       0.7,
			Thinking:          thinking,
			ReasoningEffort:   reasoningEffort,
		})

		if err != nil {
			return "", err
		}
		if runResult.Cancelled {
			return "", fmt.Errorf("cancelled")
		}

		// ── Flush code index changes made by this sub-turn ──
		if codeIndexer != nil {
			n := codeIndexer.FlushChangedFiles()
			if n > 0 {
				logger.Info("call_llm: flushed code index changes",
					zap.String("sub_session", subSessionID),
					zap.Int("count", n))
			}
		}

		// ── Generate sub-session summary (async) ──
		if dbDir != "" && subSessionID != "" && subTurnID != "" {
			go generateSubSessionSummary(logger, llmProtocol, llmModel, llmAPIKey, llmAPIURL,
				dbDir, subSessionID, subTurnID)
		}

		return runResult.Content, nil
	})

	if async {
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("⚙️ call_llm 已异步启动（task_id=%s, session_id=%s）", taskID, subSessionID),
			Tool:    "call_llm",
			RawResult: map[string]interface{}{
				"session_id":  subSessionID,
				"sub_turn_id": subTurnID,
				"task_id":     taskID,
				"status":      "running",
				"async":       true,
			},
		}
	}

	// Sync: poll TaskManager until done or cancelled
	for {
		info, err := tm.GetStatus(taskID)
		if err != nil {
			return &types.ToolResult{
				Success: false,
				Error:   err.Error(),
				Tool:    "call_llm",
				Output:  "❌ 查询任务状态失败",
				RawResult: map[string]interface{}{
					"error":    err.Error(),
					"task_id":  taskID,
				},
			}
		}
		if info.Status != "running" {
			if info.Status == "done" {
				return &types.ToolResult{
					Success: true,
					Output:  fmt.Sprintf("✅ call_llm 执行成功（%s）\n\n%s", subSessionID, info.Output),
					Tool:    "call_llm",
					RawResult: map[string]interface{}{
						"session_id":   subSessionID,
						"sub_turn_id":  subTurnID,
						"status":       "done",
					},
				}
			}
			return &types.ToolResult{
				Success: false,
				Error:   info.Error,
				Tool:    "call_llm",
				Output:  fmt.Sprintf("❌ call_llm 执行失败（%s）", subSessionID),
				RawResult: map[string]interface{}{
					"session_id":  subSessionID,
					"sub_turn_id": subTurnID,
					"status":      "error",
					"error":       info.Error,
				},
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// generateSubSessionSummary generates and saves a session summary for a sub-session.
// Runs asynchronously; errors are logged but never returned.
func generateSubSessionSummary(logger *zap.Logger, llmProtocol, llmModel, llmAPIKey, llmAPIURL string,
	dbDir, sessionID, turnID string) {

	sqlDB, err := db.Open(dbDir)
	if err != nil {
		logger.Warn("call_llm summary: failed to open DB", zap.Error(err))
		return
	}
	defer db.Close(sqlDB)

	// Load current turn's messages and format as readable conversation
	turnMessages, err := db.GetMessagesByTurn(sqlDB, turnID)
	if err != nil {
		logger.Warn("call_llm summary: failed to load turn messages", zap.Error(err))
		return
	}
	latestTurnText := formatTurnSummary(turnMessages)
	if latestTurnText == "" {
		return
	}

	// Load old summary (if any)
	oldSummary, err := db.GetLatestSummary(sqlDB, sessionID)
	if err != nil || oldSummary == "" {
		oldSummary = "{}"
	}

	// Build the summary prompt
	userContent := "Summarize the following conversation turn and update the existing summary.\n\n" +
		"Existing summary:\n" + oldSummary + "\n\n" +
		"New turn:\n" + latestTurnText + "\n\n" +
		"Output a JSON object with keys: goals, completed, current_state, next_steps. Keep it concise (2-3 sentences per field)."

	// Call LLM (non-streaming, no tools)
	client := llm.NewClient(llmProtocol, llmModel, llmAPIKey, llmAPIURL, logger)
	ch, err := client.Chat([]llm.Message{
		{Role: "user", Content: userContent},
	}, llm.ChatOptions{
		Model:       llmModel,
		Temperature: 0.1,
		MaxTokens:   4096,
	})
	if err != nil {
		logger.Warn("call_llm summary: LLM call failed", zap.Error(err))
		return
	}

	var result strings.Builder
	for evt := range ch {
		if evt.Error != nil {
			logger.Warn("call_llm summary: LLM stream error", zap.Error(evt.Error))
			return
		}
		result.WriteString(evt.Content)
	}

	summaryJSON := strings.TrimSpace(result.String())
	if summaryJSON == "" {
		logger.Warn("call_llm summary: empty response from LLM")
		return
	}

	// Validate JSON
	if !json.Valid([]byte(summaryJSON)) {
		logger.Warn("call_llm summary: response is not valid JSON",
			zap.String("raw", truncateStr(summaryJSON, 200)))
		return
	}

	// Save to DB
	if err := db.SaveSummary(sqlDB, sessionID, summaryJSON, turnID); err != nil {
		logger.Warn("call_llm summary: failed to save", zap.Error(err))
		return
	}

	logger.Info("call_llm summary: sub-session summary updated",
		zap.String("session_id", sessionID),
		zap.Int("summary_bytes", len(summaryJSON)))
}

// formatTurnSummary formats a turn's DB messages into readable text for summary generation.
func formatTurnSummary(messages []*models.Message) string {
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

// extractBrief returns the first 3 non-empty lines of a text.
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

// truncateStr truncates a string to maxLen chars, appending "..." if truncated.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func init() {
	types.RegisterSimplify("call_llm", types.SimpleAction("call_llm"))
}
