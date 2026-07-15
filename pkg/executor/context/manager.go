package context

import (
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/internal/models"
	"github.com/chonkpilot/chonkpilot/pkg/executor/llm"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler"
)

// Compile-time check: Manager implements the strictSQLDB interface.
var _ strictSQLDB = (*sql.DB)(nil)

// strictSQLDB is the minimal DB interface used by the Manager.
type strictSQLDB interface {
	QueryRow(query string, args ...interface{}) *sql.Row
	Exec(query string, args ...interface{}) (sql.Result, error)
}

// Manager handles conversation context, three-layer message compression,
// and summary generation/loading.
type Manager struct {
	workDir             string
	keepFullTurns       int // N: turns closest to current that stay fully raw
	keepSimplifiedTurns int // M: turns before that get simplified to thinking
	// minCompressToken is enforced externally; compression is skipped
	// when estimated tokens are below this threshold.
}

// NewManager creates a new context manager with default settings.
func NewManager(workDir string) *Manager {
	return &Manager{
		workDir:             workDir,
		keepFullTurns:       5,
		keepSimplifiedTurns: 15,
	}
}

// BuildLLMContext processes raw DB messages into the three-layer output:
//
//	Current turn (always fully included)
//	→ keepFullTurns:  raw (unchanged)
//	→ keepSimplifiedTurns: tool_call+tool_result pairs simplified to thinking text
//	→ older turns (if any): replaced with summary from DB (when available)
//
// The current turn is detected as the last "user" message.
func (m *Manager) BuildLLMContext(allMessages []*models.Message, currentSessionID string) []llm.Message {
	// 1. Group messages by turn (split at "user" role)
	turns := groupTurns(allMessages)
	if len(turns) == 0 {
		return nil
	}

	// 2. Classify turn groups
	totalTurns := len(turns)
	fullStart := totalTurns - m.keepFullTurns
	if fullStart < 0 {
		fullStart = 0
	}
	simplifiedStart := totalTurns - m.keepFullTurns - m.keepSimplifiedTurns
	if simplifiedStart < 0 {
		simplifiedStart = 0
	}

	var result []llm.Message
	var summaryLoaded bool

	for i, turn := range turns {
		switch {
		case i >= fullStart:
			// Layer 1: Full — keep as-is
			result = append(result, processFullTurn(turn)...)

		case i >= simplifiedStart:
			// Layer 2: Simplified — tool pairs → thinking
			result = append(result, processSimplifiedTurn(turn, m.workDir)...)

		default:
			// Layer 3: Summarized — replaced by summary text (once)
			if !summaryLoaded {
				summary := m.loadSummary(currentSessionID)
				if summary != "" {
					result = append(result, llm.Message{
						Role:    "system",
						Content: "[History Summary]\n" + summary,
					})
				}
				summaryLoaded = true
			}
		}
	}

	return result
}

// groupTurns splits a flat message list into turn groups.
// A new turn starts at each "user" role message.
func groupTurns(msgs []*models.Message) [][]*models.Message {
	var turns [][]*models.Message
	var current []*models.Message

	for _, m := range msgs {
		if m.Role == "user" && len(current) > 0 {
			turns = append(turns, current)
			current = nil
		}
		current = append(current, m)
	}
	if len(current) > 0 {
		turns = append(turns, current)
	}
	return turns
}

// processFullTurn converts a turn's messages into llm.Messages.
// Option A: orphan tool_call (no matching tool_result) is stripped to avoid
// API rejection ("assistant with tool_calls must be followed by tool messages").
func processFullTurn(turn []*models.Message) []llm.Message {
	// First pass: collect all tool_call IDs and tool_result IDs
	callIDs := make(map[string]bool)
	resultIDs := make(map[string]bool)
	for _, m := range turn {
		if m.Role == "assistant" && m.Type == "tool_call" {
			var tc models.ToolCallPayload
			if err := json.Unmarshal([]byte(m.Content), &tc); err == nil {
				callIDs[tc.ToolCallID] = true
			}
		} else if m.Role == "tool" {
			var tp models.ToolResultPayload
			if err := json.Unmarshal([]byte(m.Content), &tp); err == nil {
				resultIDs[tp.ToolCallID] = true
			}
		}
	}

	// Strip orphan tool_calls (no matching tool_result)
	var dropped int
	for id := range callIDs {
		if !resultIDs[id] {
			delete(callIDs, id)
			dropped++
		}
	}
	if dropped > 0 {
		// TODO: wire a logger here if needed
	}

	var result []llm.Message
	var curText strings.Builder
	var curReason strings.Builder
	var curToolCalls []llm.ToolCall

	flushAssistant := func() {
		if curText.Len() > 0 || curReason.Len() > 0 || len(curToolCalls) > 0 {
			msg := llm.Message{
				Role:    "assistant",
				Content: curText.String(),
			}
			if curReason.Len() > 0 {
				msg.ReasoningContent = curReason.String()
			}
			if len(curToolCalls) > 0 {
				msg.ToolCalls = curToolCalls
			}
			if msg.Content != "" || msg.ReasoningContent != "" || len(msg.ToolCalls) > 0 {
				result = append(result, msg)
			}
		}
		curText.Reset()
		curReason.Reset()
		curToolCalls = nil
	}

	for _, m := range turn {
		switch m.Role {
		case "user":
			result = append(result, llm.Message{Role: "user", Content: m.Content})

		case "assistant":
			switch m.Type {
			case "text":
				curText.WriteString(m.Content)
			case "reasoning":
				curReason.WriteString(m.Content)
			case "tool_call":
				var tc models.ToolCallPayload
				if err := json.Unmarshal([]byte(m.Content), &tc); err == nil {
					if !callIDs[tc.ToolCallID] {
						// Orphan tool_call: add cancellation note instead of silent drop
						curText.WriteString("\n[tool: " + tc.Name + " was cancelled]")
						continue
					}
					curToolCalls = append(curToolCalls, llm.ToolCall{
						ID:   tc.ToolCallID,
						Type: "function",
						Function: struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						}{
							Name:      tc.Name,
							Arguments: tc.Arguments,
						},
					})
				}
			}

		case "tool":
			flushAssistant()
			var tp models.ToolResultPayload
			toolCallID := ""
			resultContent := m.Content
			if err := json.Unmarshal([]byte(m.Content), &tp); err == nil {
				toolCallID = tp.ToolCallID
				resultContent = tp.Result
			}
			result = append(result, llm.Message{
				Role:       "tool",
				Content:    resultContent,
				ToolCallID: toolCallID,
			})
		}
	}
	flushAssistant()

	return result
}

// processSimplifiedTurn condenses tool_call + tool_result pairs into
// a single assistant thinking message, preserving user text and assistant reasoning.
func processSimplifiedTurn(turn []*models.Message, workDir string) []llm.Message {
	var result []llm.Message
	var thinkingParts []string

	// Track pending tool_call info for pairing
	type pendingCall struct {
		id   string
		name string
		args json.RawMessage
	}
	var pending *pendingCall

	for _, m := range turn {
		switch m.Role {
		case "user":
			result = append(result, llm.Message{Role: "user", Content: m.Content})

		case "assistant":
			switch m.Type {
			case "text":
				thinkingParts = append(thinkingParts, m.Content)
			case "reasoning":
				thinkingParts = append(thinkingParts, m.Content)
			case "tool_call":
				var tc models.ToolCallPayload
				if err := json.Unmarshal([]byte(m.Content), &tc); err == nil {
					// If previous pending call was orphaned, add cancellation note
					if pending != nil {
						thinkingParts = append(thinkingParts, "→ [tool: "+pending.name+" was cancelled]")
					}
					pending = &pendingCall{
						id:   tc.ToolCallID,
						name: tc.Name,
						args: json.RawMessage(tc.Arguments),
					}
				}
			}

		case "tool":
			if pending != nil {
				// Find the matching tool result for this tool_call
				var tp models.ToolResultPayload
				resultContent := m.Content
				if err := json.Unmarshal([]byte(m.Content), &tp); err == nil {
					if tp.ToolCallID == pending.id {
						resultContent = tp.Result
					}
				}
				// Simplify using the tool's simplifier
				summary := toolhandler.SimplifyToolCallWithWorkDir(pending.name, pending.args, resultContent, workDir)
				thinkingParts = append(thinkingParts, "→ "+summary)
				pending = nil
			}
		}
	}

	// Check for orphaned tool_call at end of turn
	if pending != nil {
		thinkingParts = append(thinkingParts, "→ [tool: "+pending.name+" was cancelled]")
	}

	// Emit as a single assistant message with reasoning content
	if len(thinkingParts) > 0 {
		result = append(result, llm.Message{
			Role:             "assistant",
			ReasoningContent: strings.Join(thinkingParts, "\n"),
		})
	}

	return result
}

// loadSummary reads the latest summary for a session from the DB.
// Returns empty string if no summary exists.
func (m *Manager) loadSummary(sessionID string) string {
	if m.workDir == "" || sessionID == "" {
		return ""
	}
	var summary string
	_ = db.WithDB(m.workDir, func(sqlDB *sql.DB) error {
		s, err := db.GetLatestSummary(sqlDB, sessionID)
		if err == nil {
			summary = s
		}
		return nil
	})
	return summary
}

// ShouldCompress checks whether the conversation context needs compression.
func (m *Manager) ShouldCompress(turnCount int, estimatedTokens int) bool {
	if estimatedTokens > 80000 {
		return true
	}
	if turnCount > m.keepFullTurns+m.keepSimplifiedTurns && estimatedTokens > 3000 {
		return true
	}
	return false
}

// SetKeepFullTurns sets the number of fully preserved turns (N).
func (m *Manager) SetKeepFullTurns(n int) {
	if n > 0 {
		m.keepFullTurns = n
	}
}

// SetKeepSimplifiedTurns sets the number of simplified turns (M).
func (m *Manager) SetKeepSimplifiedTurns(mVal int) {
	if mVal > 0 {
		m.keepSimplifiedTurns = mVal
	}
}
