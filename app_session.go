//go:build windows
// +build windows

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/internal/models"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ─── Session operations ────────────────────────────────────

// ListSessions returns all top-level sessions ordered by most recent activity.
func (a *App) ListSessions() (map[string]interface{}, error) {
	var sessions interface{}
	err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		var err error
		sessions, err = db.ListTopLevelSessions(sqlDB)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"sessions": sessions}, nil
}

// ListAllSessions returns all sessions including sub-sessions, ordered by created_at descending.
// Used by SessionTree to show sub-sessions for a given parent.
func (a *App) ListAllSessions() (map[string]interface{}, error) {
	var sessions interface{}
	err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		var err error
		sessions, err = db.ListSessions(sqlDB)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"sessions": sessions}, nil
}

type createSessionArgs struct {
	WorkDir string `json:"workDir"`
	Title   string `json:"title"`
}

// CreateSession creates a new session.
func (a *App) CreateSession(args createSessionArgs) (map[string]interface{}, error) {
	wd := args.WorkDir
	if wd == "" {
		wd = a.workDir
	}
	sessionID := uuid.New().String()
	session := models.NewSession(sessionID, "", wd, args.Title)
	err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		return db.CreateSession(sqlDB, session)
	})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"session_id": sessionID, "session": session}, nil
}

// GetSession returns a session by ID.
func (a *App) GetSession(id string) (map[string]interface{}, error) {
	var session interface{}
	err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		var err error
		session, err = db.GetSession(sqlDB, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"session": session}, nil
}

// DeleteSession deletes a session by ID.
func (a *App) DeleteSession(id string) error {
	return db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		return db.DeleteSession(sqlDB, id)
	})
}

// UpdateSessionTitle updates the title of a session.
func (a *App) UpdateSessionTitle(id, title string) error {
	return db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		return db.UpdateSessionTitle(sqlDB, id, title)
	})
}

// GetLatestSessionID returns the ID of the session with the most recent message.
func (a *App) GetLatestSessionID() (map[string]interface{}, error) {
	var sessionID string
	err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		var err error
		sessionID, err = db.GetLatestSessionID(sqlDB)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"session_id": sessionID}, nil
}

// SetActiveSession saves the active session ID to user config.
func (a *App) SetActiveSession(sessionID string) error {
	if a.userCfg == nil {
		return nil
	}
	cfg := a.userCfg.Get()
	cfg.ActiveSessionID = sessionID
	return a.userCfg.Update(cfg)
}

// GetTurnsBySession returns all turns and messages for a session.
// tool_call + tool_result messages are paired into tool_pair messages for
// compact frontend rendering.
// When brief=true, large content fields (reasoning content, tool_pair result)
// are omitted and can be fetched on demand via GetMessageContent.
func (a *App) GetTurnsBySession(sessionID string, brief bool) (map[string]interface{}, error) {
	var turns []*models.Turn
	var rawMessages []*models.Message
	err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		var err error
		turns, err = db.GetTurnsBySession(sqlDB, sessionID)
		if err != nil {
			return err
		}
		rawMessages, err = db.GetMessagesBySession(sqlDB, sessionID)
		return err
	})
	if err != nil {
		return nil, err
	}
	messages := pairToolMessages(rawMessages, brief)
	return map[string]interface{}{"turns": turns, "messages": messages}, nil
}

// GetMessageContent returns full content for messages that were
// loaded in brief mode. Supports "tool_call:<tool_call_id>" for
// tool_pair results and "message:<message_id>" for reasoning content.
func (a *App) GetMessageContent(sessionID string, itemKeys []string) (map[string]interface{}, error) {
	result := make(map[string]interface{})
	err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		for _, key := range itemKeys {
			if strings.HasPrefix(key, "tool_call:") {
				tcID := strings.TrimPrefix(key, "tool_call:")
				content, err := db.GetToolResultByCallID(sqlDB, sessionID, tcID)
				if err != nil {
					result[key] = nil
					a.logger.Debug("GetMessageContent: tool_call not found", zap.String("tool_call_id", tcID), zap.Error(err))
					continue
				}
				result[key] = content
			} else if strings.HasPrefix(key, "message:") {
				msgID := strings.TrimPrefix(key, "message:")
				msg, err := db.GetMessage(sqlDB, msgID)
				if err != nil {
					result[key] = nil
					a.logger.Debug("GetMessageContent: message not found", zap.String("message_id", msgID), zap.Error(err))
					continue
				}
				result[key] = msg.Content
			} else {
				result[key] = nil
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// toolPairInfo accumulates data for pairing tool_call + tool_result.
type toolPairInfo struct {
	toolCallID    string
	name          string
	arguments     string
	result        string
	resultSuccess bool
	createdAt     string
}

// truncateLines returns the first maxLines lines of s and a bool indicating
// whether the string was truncated.
func truncateLines(s string, maxLines int) (string, bool) {
	if s == "" {
		return "", false
	}
	lines := strings.SplitN(s, "\n", maxLines+1)
	if len(lines) <= maxLines {
		return s, false
	}
	return strings.Join(lines[:maxLines], "\n"), true
}

// pairToolMessages processes raw messages, pairing consecutive tool_call +
// tool_result into single tool_pair messages. Non-paired messages pass through.
// When brief=true, reasoning content is truncated to 3 lines and tool_pair
// results are omitted (use result_simplified as summary, load full on demand).
func pairToolMessages(msgs []*models.Message, brief bool) []interface{} {
	// First pass: collect tool_call IDs and their matching results
	pairs := make(map[string]*toolPairInfo)
	for _, m := range msgs {
		if m.Role == "assistant" && m.Type == "tool_call" {
			var tc models.ToolCallPayload
			if err := json.Unmarshal([]byte(m.Content), &tc); err == nil {
				if _, exists := pairs[tc.ToolCallID]; !exists {
					pairs[tc.ToolCallID] = &toolPairInfo{
						toolCallID:    tc.ToolCallID,
						name:          tc.Name,
						arguments:     tc.Arguments,
						resultSuccess: true,
						createdAt:     m.CreatedAt,
					}
				}
			}
		} else if m.Role == "tool" {
			var tp models.ToolResultPayload
			if err := json.Unmarshal([]byte(m.Content), &tp); err == nil {
				if info, exists := pairs[tp.ToolCallID]; exists {
					info.result = tp.Result
					info.resultSuccess = true
				}
			}
		}
	}

	// Second pass: emit tool_pair for matched pairs, skip consumed tool_results
	consumed := make(map[string]bool)
	var result []interface{}

	for _, m := range msgs {
		if m.Role == "assistant" && m.Type == "tool_call" {
			var tc models.ToolCallPayload
			if err := json.Unmarshal([]byte(m.Content), &tc); err == nil {
				if info, exists := pairs[tc.ToolCallID]; exists && !consumed[tc.ToolCallID] {
					consumed[tc.ToolCallID] = true

					// Build simplified call text (compact args summary)
					simplified := info.name + "(" + formatToolCallArgsCompact(info.arguments) + ")"
					// Build simplified result text (first line or truncated)
					resultSimplified := summarizeResult(info.result)

					status := "done"
					if info.result == "" {
						status = "failed"
					}

					pair := map[string]interface{}{
						"role":              "assistant",
						"type":              "tool_pair",
						"tool_call_id":      info.toolCallID,
						"tool":              info.name,
						"arguments":         info.arguments,
						"result_success":    info.resultSuccess,
						"simplified":        simplified,
						"result_simplified": resultSimplified,
						"status":            status,
						"created_at":        info.createdAt,
					}
					if brief {
						pair["result"] = ""
						pair["has_more"] = info.result != ""
					} else {
						pair["result"] = info.result
					}
					result = append(result, pair)
					continue
				}
			}
		} else if m.Role == "tool" {
			// Check if this tool_result was consumed by a tool_pair
			var tp models.ToolResultPayload
			if err := json.Unmarshal([]byte(m.Content), &tp); err == nil {
				if consumed[tp.ToolCallID] {
					continue // skip, already in tool_pair
				}
			}
		}
		// Pass through: user messages, assistant text/reasoning, orphan tool_calls
		if brief && m.Role == "assistant" && m.Type == "reasoning" {
			preview, hasMore := truncateLines(m.Content, 3)
			result = append(result, map[string]interface{}{
				"role":       m.Role,
				"type":       m.Type,
				"message_id": m.MessageID,
				"turn_id":    m.TurnID,
				"content":    preview,
				"has_more":   hasMore,
				"created_at": m.CreatedAt,
			})
		} else {
			result = append(result, m)
		}
	}

	return result
}

// formatToolCallArgsCompact produces a one-line summary of tool call arguments.
func formatToolCallArgsCompact(argsStr string) string {
	var argsMap map[string]interface{}
	if err := json.Unmarshal([]byte(argsStr), &argsMap); err != nil {
		return ""
	}
	var parts []string
	for k, v := range argsMap {
		if s, ok := v.(string); ok && len(s) > 80 {
			parts = append(parts, fmt.Sprintf("%s=...</%d chars>", k, len(s)))
		} else if s, ok := v.(string); ok {
			parts = append(parts, fmt.Sprintf("%s=%v", k, s))
		} else {
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
	}
	if len(parts) > 3 {
		parts = parts[:3]
		return strings.Join(parts, ", ") + ", ..."
	}
	return strings.Join(parts, ", ")
}

// summarizeResult produces a one-line summary of a tool result for display.
func summarizeResult(result string) string {
	if result == "" {
		return "failed"
	}
	// Take the first meaningful line
	for _, line := range strings.SplitN(result, "\n", 2) {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			if len(trimmed) > 100 {
				return trimmed[:100] + "..."
			}
			return trimmed
		}
	}
	return "ok"
}

// ─── Chat operations ───────────────────────────────────────

type sendChatArgs struct {
	SessionID string   `json:"session_id"`
	TurnID    string   `json:"turn_id"`
	Q         string   `json:"q"`
	Files     []string `json:"files"`
	LLM       string   `json:"llm"`
	Think     string   `json:"think"`
	Effort    string   `json:"effort"`
}

// SendChatMessage sends a chat message to the executor.
func (a *App) SendChatMessage(args sendChatArgs) (map[string]string, error) {
	if args.SessionID == "" {
		return nil, fmt.Errorf("session_id required")
	}

	turnID := uuid.New().String()

	// Persist turn + user message in a single DB session, propagating errors.
	if err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		// Auto-title from first user message (best-effort)
		if args.Q != "" {
			session, err := db.GetSession(sqlDB, args.SessionID)
			if err == nil && session.Title == "" {
				title := args.Q
				if len(title) > 40 {
					title = title[:40]
				}
				if uerr := db.UpdateSessionTitle(sqlDB, args.SessionID, title); uerr != nil {
					a.logger.Warn("failed to auto-title session", zap.String("session_id", args.SessionID), zap.Error(uerr))
				}
			}
		}

		turn := models.NewTurn(turnID, args.SessionID)
		if err := db.CreateTurn(sqlDB, turn); err != nil {
			return fmt.Errorf("create turn: %w", err)
		}
		msg := models.NewMessage(uuid.New().String(), turnID, "user", "text", args.Q)
		if err := db.AddMessage(sqlDB, msg); err != nil {
			return fmt.Errorf("add message: %w", err)
		}
		return nil
	}); err != nil {
		a.logger.Error("SendChatMessage: failed to persist turn/message", zap.Error(err))
		return nil, fmt.Errorf("failed to persist chat data: %w", err)
	}
	go func() {
		// Check if cancellation was requested before the goroutine started
		stop := make(chan struct{}, 1)
		a.pendingCancels.LoadOrStore(turnID, stop)
		select {
		case <-stop:
			a.pendingCancels.Delete(turnID)
			a.logger.Debug("StartChat goroutine aborted by CancelChat before executor started",
				zap.String("turn_id", turnID))
			return
		default:
		}

		cfg := a.userCfg.Get()
		var extraArgs []string
		if cfg.LogLevel != "" {
			extraArgs = append(extraArgs, "--log-level="+cfg.LogLevel)
		}
		if cfg.RetryCount > 0 {
			extraArgs = append(extraArgs, fmt.Sprintf("--retry-count=%d", cfg.RetryCount))
		}
		if cfg.RetryDelay > 0 {
			extraArgs = append(extraArgs, fmt.Sprintf("--retry-delay=%d", cfg.RetryDelay))
		}
		// KeepFullTurns / CompressTokenThreshold — read from ConfigManager cache, not disk
		if val, ok := a.cfg.Get("keep_full_turns"); ok && val != "" {
			if n, e := strconv.Atoi(val); e == nil && n > 0 {
				extraArgs = append(extraArgs, fmt.Sprintf("--keep-full-turns=%d", n))
			}
		}
		if val, ok := a.cfg.Get("compress_token_threshold"); ok && val != "" {
			if n, e := strconv.Atoi(val); e == nil && n > 0 {
				extraArgs = append(extraArgs, fmt.Sprintf("--compress-token-threshold=%d", n))
			}
		}
		if cfg.ForeachConcurrency > 0 {
			extraArgs = append(extraArgs, fmt.Sprintf("--foreach-concurrency=%d", cfg.ForeachConcurrency))
		}
		if cfg.ForeachMaxDepth > 0 {
			extraArgs = append(extraArgs, fmt.Sprintf("--foreach-max-depth=%d", cfg.ForeachMaxDepth))
		}
		if cfg.FetchTimeout > 0 {
			extraArgs = append(extraArgs, fmt.Sprintf("--fetch-timeout=%d", cfg.FetchTimeout))
		}
		if cfg.MCPTimeout > 0 {
			extraArgs = append(extraArgs, fmt.Sprintf("--mcp-timeout=%d", cfg.MCPTimeout))
		}
		if cfg.AskUserTimeout > 0 {
			extraArgs = append(extraArgs, fmt.Sprintf("--ask-user-timeout=%d", cfg.AskUserTimeout))
		}
		// Pass active scenario system prompt as env var for executor to read
		_ = db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
			if prompt, err := db.GetConfig(sqlDB, "scenario_system_prompt"); err == nil && prompt != "" {
				os.Setenv("CHONKPILOT_SCENARIO_PROMPT", prompt)
			} else {
				os.Unsetenv("CHONKPILOT_SCENARIO_PROMPT")
			}
			return nil
		})
		// Check cancellation again before the final SendTurn call
		select {
		case <-stop:
			a.pendingCancels.Delete(turnID)
			a.logger.Debug("StartChat goroutine aborted before SendTurn",
				zap.String("turn_id", turnID))
			return
		default:
		}

		// Send turn to daemon executor (long-lived process that persists browser state)
		if err := a.em.SendTurn(args.SessionID, turnID, args.LLM, args.Think, args.Effort, extraArgs); err != nil {
			// Fallback: start per-turn executor if daemon is not running
			a.logger.Warn("SendTurn failed, falling back to per-turn executor", zap.Error(err))
			a.em.StartExecutor(args.SessionID, turnID, args.LLM, args.Think, args.Effort, extraArgs...)
		}
		a.pendingCancels.Delete(turnID)
	}()
	return map[string]string{"turn_id": turnID}, nil
}

type cancelChatArgs struct {
	TurnID string `json:"turn_id"`
}

// CancelChat cancels a running chat.
func (a *App) CancelChat(args cancelChatArgs) error {
	if args.TurnID == "" {
		return fmt.Errorf("turn_id required")
	}

	// Signal the StartChat goroutine to abort before executor starts (if still in setup phase)
	if v, ok := a.pendingCancels.Load(args.TurnID); ok {
		if ch, ok := v.(chan struct{}); ok {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}

	// Try daemon cancel first (long-lived executor mode)
	if err := a.em.CancelDaemonSession(args.TurnID); err == nil {
		// Daemon found and cancelled by session
	} else if err := a.em.KillExecutor(args.TurnID); err != nil {
		// Look up session_id from DB for daemon cancel
		sessionID := ""
		_ = db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
			turn, err := db.GetTurn(sqlDB, args.TurnID)
			if err == nil && turn != nil {
				sessionID = turn.SessionID
			}
			return nil
		})
		if sessionID != "" {
			a.em.CancelDaemonSession(sessionID)
		}
		a.logger.Warn("kill executor failed", zap.String("turn_id", args.TurnID), zap.Error(err))
	}

	if err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		return db.UpdateTaskStatus(sqlDB, args.TurnID, models.TaskStatusCancelled, 0, "cancelled by user")
	}); err != nil {
		a.logger.Warn("update task status failed", zap.String("turn_id", args.TurnID), zap.Error(err))
	}
	return nil
}

// ─── ask_user operations ───────────────────────────────────

type askUserResponse struct {
	Answer   string `json:"answer"`
	Custom   string `json:"custom,omitempty"`
	PipeAddr string `json:"pipe_addr"`
}

// RespondAskUser sends a response to an ask_user prompt via named pipe.
func (a *App) RespondAskUser(args askUserResponse) (map[string]string, error) {
	if args.PipeAddr == "" {
		return nil, fmt.Errorf("pipe_addr required")
	}
	if args.Answer == "" {
		return nil, fmt.Errorf("answer required")
	}
	conn, err := net.Dial("tcp", args.PipeAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to ask_user pipe: %w", err)
	}
	defer conn.Close()
	payload := map[string]string{
		"answer": args.Answer,
	}
	if args.Custom != "" {
		payload["custom"] = args.Custom
	}
	if err := json.NewEncoder(conn).Encode(payload); err != nil {
		return nil, fmt.Errorf("failed to send answer to ask_user pipe: %w", err)
	}
	return map[string]string{"status": "sent"}, nil
}

// ─── Task operations ───────────────────────────────────────

// GetTasksByTurn returns all tasks for a turn.
func (a *App) GetTasksByTurn(turnID string) (map[string]interface{}, error) {
	var tasks interface{}
	err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		var err error
		tasks, err = db.GetTasksByTurn(sqlDB, turnID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"tasks": tasks}, nil
}

type updateTaskArgs struct {
	TaskID   string  `json:"task_id"`
	Status   string  `json:"status"`
	Progress float64 `json:"progress"`
	Result   string  `json:"result"`
}

// UpdateTaskStatus updates a task's status.
func (a *App) UpdateTaskStatus(args updateTaskArgs) error {
	return db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		return db.UpdateTaskStatus(sqlDB, args.TaskID, args.Status, int(args.Progress), args.Result)
	})
}
