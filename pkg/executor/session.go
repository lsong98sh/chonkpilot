package executor

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/internal/models"
	"github.com/chonkpilot/chonkpilot/pkg/executor/context"
	"github.com/chonkpilot/chonkpilot/pkg/executor/llm"
	"github.com/chonkpilot/chonkpilot/pkg/executor/prompts"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type chunkType int

const (
	contentChunk chunkType = iota
	reasoningChunk
)

// writeSSEToStdout writes a DeepSeek-compatible SSE data line to stdout.
// format: data: {"choices":[{"delta":{"content":"..."},"finish_reason":null}]}
func writeSSEToStdout(content, reasoning string, cType chunkType) {
	delta := make(map[string]interface{})
	if cType == reasoningChunk && reasoning != "" {
		delta["reasoning_content"] = reasoning
	}
	if content != "" {
		delta["content"] = content
	}

	choice := map[string]interface{}{
		"delta":         delta,
		"finish_reason": nil,
		"index":         0,
	}

	payload := map[string]interface{}{
		"choices": []interface{}{choice},
	}

	data, _ := json.Marshal(payload)
	fmt.Fprintf(os.Stdout, "data: %s\n\n", string(data))
}

// writeSSEStop writes the stop and [DONE] SSE events.
func writeSSEStop() {
	choice := map[string]interface{}{
		"delta":         map[string]interface{}{},
		"finish_reason": "stop",
		"index":         0,
	}
	payload := map[string]interface{}{
		"choices": []interface{}{choice},
	}
	data, _ := json.Marshal(payload)
	fmt.Fprintf(os.Stdout, "data: %s\n\n", string(data))
	fmt.Fprintf(os.Stdout, "data: [DONE]\n\n")
}

// printOutgoingMessages prints the assembled request to stdout in verbose mode.
func printOutgoingMessages(ea *ExecutorArgs, messages []llm.Message, toolDefs []llm.ToolDefinition) {
	fmt.Fprintf(os.Stdout, "# outgoing (request):\n")
	req := map[string]interface{}{
		"model":    ea.LLMModel,
		"messages": messages,
		"stream":   true,
	}
	if len(toolDefs) > 0 {
		req["tools"] = toolDefs
	}
	if ea.Thinking {
		req["thinking"] = true
		if ea.ReasoningEffort != "" {
			req["reasoning_effort"] = ea.ReasoningEffort
		} else {
			req["reasoning_effort"] = "high"
		}
	}
	data, _ := json.MarshalIndent(req, "", "  ")
	fmt.Fprintf(os.Stdout, "%s\n", string(data))
	fmt.Fprintf(os.Stdout, "# incoming (response):\n")
}

// loadSessionHistory loads all previous messages in a session from the DB and
// processes them through the two-layer context manager:
//
//	Last keepFullTurns → raw (unchanged)
//	Older turns → tool_call+tool_result condensed to thinking text (with summary from DB when available)
func loadSessionHistory(ea *ExecutorArgs, sessionID string, sqlDB *sql.DB) ([]llm.Message, error) {
	if !hasIDEConfig(ea) {
		return nil, nil
	}
	if sqlDB == nil {
		var err error
		sqlDB, err = db.Open(ea.DBWorkDir())
		if err != nil {
			return nil, err
		}
		defer db.Close(sqlDB)
	}

	// Load raw messages from DB
	rawMessages, err := db.GetMessagesBySession(sqlDB, sessionID)
	if err != nil {
		return nil, err
	}

	// Context compression: CLI/global config takes precedence, fall back to DB config
	fullTurns := db.GetConfigInt(sqlDB, "keep_full_turns", 6)
	if ea.KeepFullTurns > 0 {
		fullTurns = ea.KeepFullTurns
	}

	// Create context manager and apply settings
	mgr := context.NewManager(ea.WorkDir, sqlDB)
	mgr.SetKeepFullTurns(fullTurns)

	// Build context
	result := mgr.BuildLLMContext(rawMessages, sessionID)

	return result, nil
}

// generateSessionSummary asynchronously updates the session summary after a turn completes.
// It checks compression thresholds and only runs when the simplified zone meets the token threshold.
// Runs in a background goroutine — errors are logged but never returned.
func generateSessionSummary(ea *ExecutorArgs, logger *zap.Logger) {
	if !hasIDEConfig(ea) || ea.SessionID == "" || ea.TurnID == "" {
		return
	}

	sqlDB, err := db.Open(ea.DBWorkDir())
	if err != nil {
		logger.Warn("summary: failed to open DB", zap.Error(err))
		return
	}
	defer db.Close(sqlDB)

	// ── 1. Check if compression is already in progress for this session ──
	lockKey := "compress_lock:" + ea.SessionID
	locked, err := db.GetConfig(sqlDB, lockKey)
	if err == nil && locked == "true" {
		logger.Debug("summary: skipped (compression already in progress)",
			zap.String("session_id", ea.SessionID))
		return
	}

	// ── 2. Read config values ──
	fullTurns := db.GetConfigInt(sqlDB, "keep_full_turns", 6)
	compressThreshold := db.GetConfigInt(sqlDB, "compress_token_threshold", 80000)
	if ea.CompressTokenThreshold > 0 {
		compressThreshold = ea.CompressTokenThreshold
	}

	turns, err := db.GetTurnsBySession(sqlDB, ea.SessionID)
	if err != nil || len(turns) <= fullTurns {
		return // Not enough turns — no simplified zone
	}

	// ── 3. Load old summary ──
	oldSummary, err := db.GetLatestSummary(sqlDB, ea.SessionID)
	if err != nil {
		logger.Warn("summary: failed to load old summary", zap.Error(err))
		oldSummary = "{}"
	}
	if oldSummary == "" {
		oldSummary = "{}"
	}

	// ── 4. Load all simplified zone turns (everything before the last N) ──
	simplifiedTurns := turns[:len(turns)-fullTurns]

	// 4a. Estimate simplified zone tokens (rough: len/4 per message)
	estimatedTokens := estimateTokensForTurns(sqlDB, simplifiedTurns, oldSummary)

	if estimatedTokens < compressThreshold {
		logger.Debug("summary: skipped (tokens below threshold)",
			zap.Int("estimated", estimatedTokens),
			zap.Int("threshold", compressThreshold),
			zap.Int("simplified_turns", len(simplifiedTurns)))
		return
	}

	// ── 5. Acquire compress lock ──
	if err := db.SetConfig(sqlDB, lockKey, "true"); err != nil {
		logger.Warn("summary: failed to acquire compress lock", zap.Error(err))
		return
	}
	defer db.SetConfig(sqlDB, lockKey, "") // release on exit

	// ── 6. Format all simplified zone turns into summary input ──
	var allLatestText strings.Builder
	for _, turn := range simplifiedTurns {
		turnMessages, err := db.GetMessagesByTurn(sqlDB, turn.TurnID)
		if err != nil {
			logger.Warn("summary: failed to load turn messages", zap.String("turn_id", turn.TurnID), zap.Error(err))
			return
		}
		text := formatTurnForSummary(turnMessages)
		if text != "" {
			allLatestText.WriteString(text)
			allLatestText.WriteString("\n---\n")
		}
	}

	summaryInput := allLatestText.String()
	if summaryInput == "" {
		logger.Debug("summary: all simplified turns are empty, nothing to compress")
		return
	}

	// ── 7. Build the summary prompt (fill template placeholders) ──
	summaryTemplate := prompts.DefaultSummaryPrompt
	if sp, err := db.GetProjectPrompt(sqlDB, "summary_prompt"); err == nil && sp != "" {
		summaryTemplate = sp
	} else if sp, err := db.GetConfig(sqlDB, "summary_prompt"); err == nil && sp != "" {
		summaryTemplate = sp
	}
	userContent := strings.NewReplacer(
		"{old_summary}", oldSummary,
		"{latest_turn}", summaryInput,
	).Replace(summaryTemplate)

	// ── 8. Call LLM (non-streaming, no tools, no retry) ──
	client := llm.NewClient(ea.LLMProtocol, ea.LLMModel, ea.LLMAPIKey, ea.LLMAPIURL, logger)
	ch, err := client.Chat([]llm.Message{
		{Role: "user", Content: userContent},
	}, llm.ChatOptions{
		Model:       ea.LLMModel,
		Temperature: 0.1, // deterministic
		MaxTokens:   resolveMaxTokens(ea),
	})
	if err != nil {
		logger.Warn("summary: LLM call failed", zap.Error(err))
		return
	}

	var result strings.Builder
	for evt := range ch {
		if evt.Error != nil {
			logger.Warn("summary: LLM stream error", zap.Error(evt.Error))
			return
		}
		result.WriteString(evt.Content)
	}

	summaryJSON := strings.TrimSpace(result.String())
	if summaryJSON == "" {
		logger.Warn("summary: empty response from LLM")
		return
	}

	// ── 9. Validate it's parseable JSON (basic check) ──
	if !json.Valid([]byte(summaryJSON)) {
		logger.Warn("summary: response is not valid JSON", zap.String("raw", truncateStr(summaryJSON, 200)))
		return
	}

	// ── 10. Save to DB ──
	if err := db.SaveSummary(sqlDB, ea.SessionID, summaryJSON, ea.TurnID); err != nil {
		logger.Warn("summary: failed to save", zap.Error(err))
		return
	}

	// ── 11. Update session title from overall_goals ──
	var summaryData struct {
		OverallGoals string `json:"overall_goals"`
	}
	if err := json.Unmarshal([]byte(summaryJSON), &summaryData); err == nil && summaryData.OverallGoals != "" {
		if err := db.UpdateSessionTitle(sqlDB, ea.SessionID, summaryData.OverallGoals); err != nil {
			logger.Warn("summary: failed to update session title", zap.Error(err))
		} else {
			logger.Debug("summary: session title updated", zap.String("title", summaryData.OverallGoals))
		}
	}

	logger.Info("summary: session summary updated",
		zap.Int("turn_count", len(turns)),
		zap.Int("simplified_turns_compressed", len(simplifiedTurns)),
		zap.Int("summary_bytes", len(summaryJSON)),
		zap.Int("estimated_tokens", estimatedTokens))
}

// estimateTokensForTurns roughly estimates the token count for a set of turns
// combined with the existing summary text. Uses len/4 as a rough approximation.
func estimateTokensForTurns(sqlDB *sql.DB, turns []*models.Turn, summary string) int {
	total := len(summary) / 4
	for _, turn := range turns {
		msgs, err := db.GetMessagesByTurn(sqlDB, turn.TurnID)
		if err != nil {
			continue
		}
		for _, m := range msgs {
			total += len(m.Content) / 4
		}
	}
	return total
}

// formatTurnForSummary formats a turn's DB messages into a readable conversation log.
func formatTurnForSummary(messages []*models.Message) string {
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
					b.WriteString(fmt.Sprintf("[tool: %s]\n", tc.Name))
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

// ensureSessionAndTurn creates session and turn records in DB as needed.
// Handles three scenarios:
//  1. IDE mode: turn already exists in DB → loads user message from DB
//  2. Create mode: turn doesn't exist → creates session+turn, writes user msg
//  3. Standalone continuation: --session-id set, no turn-id → auto-creates turn
func ensureSessionAndTurn(ea *ExecutorArgs, prompt string, logger *zap.Logger, sqlDB *sql.DB) {
	if !hasIDEConfig(ea) {
		return
	}
	if sqlDB == nil {
		var err error
		sqlDB, err = db.Open(ea.DBWorkDir())
		if err != nil {
			return
		}
		defer db.Close(sqlDB)
	}

	// Create session if not exists
	if ea.SessionID != "" {
		existing, _ := db.GetSession(sqlDB, ea.SessionID)
		if existing == nil {
			session := models.NewSession(ea.SessionID, "", ea.WorkDir, "")
			if err := db.CreateSession(sqlDB, session); err != nil {
				logger.Error("Failed to create session", zap.String("session_id", ea.SessionID), zap.Error(err))
			} else {
				logger.Info("Created new session", zap.String("session_id", ea.SessionID))
			}
		}
	}

	// Check if turn already exists
	if ea.TurnID != "" {
		existing, _ := db.GetTurn(sqlDB, ea.TurnID)
		if existing != nil {
			// IDE mode: turn exists, user message already written by IDE
			// Nothing to do here
			return
		}

		// Turn doesn't exist: create it (create mode or first-time standalone)
		turn := models.NewTurn(ea.TurnID, ea.SessionID)
		if err := db.CreateTurn(sqlDB, turn); err != nil {
			logger.Error("Failed to create turn", zap.String("turn_id", ea.TurnID), zap.Error(err))
		} else {
			logger.Info("Created new turn", zap.String("turn_id", ea.TurnID))
		}

		if prompt != "" {
			msg := models.NewMessage(uuid.New().String(), ea.TurnID, "user", "text", prompt)
			if err := db.AddMessage(sqlDB, msg); err != nil {
				logger.Error("Failed to write user message", zap.String("turn_id", ea.TurnID), zap.Error(err))
			} else {
				logger.Info("Wrote user message to turn")
			}
		}
		return
	}

	// No turn-id but has session-id: standalone continuation, auto-create turn
	if ea.SessionID != "" {
		ea.TurnID = uuid.New().String()
		turn := models.NewTurn(ea.TurnID, ea.SessionID)
		if err := db.CreateTurn(sqlDB, turn); err != nil {
			logger.Error("Failed to auto-create turn", zap.String("turn_id", ea.TurnID), zap.Error(err))
		} else {
			logger.Info("Auto-created turn for standalone continuation", zap.String("turn_id", ea.TurnID))
		}

		if prompt != "" {
			msg := models.NewMessage(uuid.New().String(), ea.TurnID, "user", "text", prompt)
			if err := db.AddMessage(sqlDB, msg); err != nil {
				logger.Error("Failed to write user message to auto-created turn", zap.String("turn_id", ea.TurnID), zap.Error(err))
			} else {
				logger.Info("Wrote user message to turn")
			}
		}
	}
}

// saveAssistantMessage persists an assistant message to the DB.
// It saves separate rows for reasoning, text, and each tool_call within the message.
// Returns the message IDs of saved tool_call messages.
func saveAssistantMessage(ea *ExecutorArgs, msg llm.Message, logger *zap.Logger, sqlDB *sql.DB) []string {
	var toolCallIDs []string
	if ea.WorkDir == "" || ea.TurnID == "" {
		return toolCallIDs
	}
	if sqlDB == nil {
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			sqlDB, err = db.Open(ea.DBWorkDir())
			if err == nil {
				break
			}
			time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
		}
		if err != nil {
			logger.Error("saveAssistantMessage: failed to open DB after retries", zap.Error(err))
			return toolCallIDs
		}
		defer db.Close(sqlDB)
	}

	// 1. Reasoning content (if any)
	if msg.ReasoningContent != "" {
		dbMsg := models.NewMessage(uuid.New().String(), ea.TurnID, "assistant", "reasoning", msg.ReasoningContent)
		if err := db.AddMessage(sqlDB, dbMsg); err != nil {
			logger.Error("saveAssistantMessage: failed to save reasoning", zap.Error(err))
		}
	}

	// 2. Text content (if any)
	if msg.Content != "" {
		dbMsg := models.NewMessage(uuid.New().String(), ea.TurnID, "assistant", "text", msg.Content)
		if err := db.AddMessage(sqlDB, dbMsg); err != nil {
			logger.Error("saveAssistantMessage: failed to save text", zap.Error(err))
		}
	}

	// 3. Tool calls (if any)
	for _, tc := range msg.ToolCalls {
		tcPayload, _ := json.Marshal(models.ToolCallPayload{
			ToolCallID: tc.ID,
			Name:       tc.Function.Name,
			Arguments:  tc.Function.Arguments,
		})
		dbMsg := models.NewMessage(uuid.New().String(), ea.TurnID, "assistant", "tool_call", string(tcPayload))
		if err := db.AddMessage(sqlDB, dbMsg); err != nil {
			logger.Error("saveAssistantMessage: failed to save tool_call", zap.String("tool", tc.Function.Name), zap.Error(err))
		} else {
			toolCallIDs = append(toolCallIDs, dbMsg.MessageID)
		}
	}

	return toolCallIDs
}

// saveToolMessage persists a tool result message to the DB.
func saveToolMessage(ea *ExecutorArgs, msg llm.Message, toolName string, logger *zap.Logger, sqlDB *sql.DB) {
	if ea.WorkDir == "" || ea.TurnID == "" {
		return
	}
	if sqlDB == nil {
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			sqlDB, err = db.Open(ea.DBWorkDir())
			if err == nil {
				break
			}
			time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
		}
		if err != nil {
			logger.Error("saveToolMessage: failed to open DB after retries", zap.Error(err))
			return
		}
		defer db.Close(sqlDB)
	}

	toolResult, _ := json.Marshal(models.ToolResultPayload{
		ToolCallID: msg.ToolCallID,
		Name:       toolName,
		Result:     msg.Content,
	})
	dbMsg := models.NewMessage(uuid.New().String(), ea.TurnID, "tool", "tool_result", string(toolResult))
	if err := db.AddMessage(sqlDB, dbMsg); err != nil {
		logger.Error("saveToolMessage: failed to save tool_result", zap.String("tool", toolName), zap.Error(err))
	}
}

// extractBrief returns the first 3 non-empty lines of a text.
// Used to create a concise thinking summary for tool_call messages.
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
