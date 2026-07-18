package sessionutil

import (
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/internal/models"
	"github.com/chonkpilot/chonkpilot/pkg/executor/llm"
)

// LoadSessionMessages loads all messages from a session and converts them
// to llm.Message format suitable for chat context. Messages are ordered
// by their DB creation time.
func LoadSessionMessages(sqlDB *sql.DB, sessionID string) ([]llm.Message, error) {
	rawMessages, err := db.GetMessagesBySession(sqlDB, sessionID)
	if err != nil {
		return nil, err
	}
	if len(rawMessages) == 0 {
		return nil, nil
	}

	return ConvertDBMessages(rawMessages), nil
}

// ConvertDBMessages converts DB messages to llm.Message format.
// It combines assistant text/reasoning/tool_calls into single assistant messages
// and pairs tool results with their tool call IDs.
func ConvertDBMessages(msgs []*models.Message) []llm.Message {
	var result []llm.Message
	var curText, curReason strings.Builder
	var curToolCalls []llm.ToolCall

	flushAssistant := func() {
		hasContent := curText.Len() > 0 || curReason.Len() > 0 || len(curToolCalls) > 0
		if !hasContent {
			return
		}
		msg := llm.Message{Role: "assistant"}
		if curText.Len() > 0 {
			msg.Content = curText.String()
		}
		if curReason.Len() > 0 {
			msg.ReasoningContent = curReason.String()
		}
		if len(curToolCalls) > 0 {
			msg.ToolCalls = curToolCalls
		}
		result = append(result, msg)
		curText.Reset()
		curReason.Reset()
		curToolCalls = nil
	}

	for _, m := range msgs {
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
			content := m.Content
			if err := json.Unmarshal([]byte(m.Content), &tp); err == nil {
				toolCallID = tp.ToolCallID
				content = tp.Result
			}
			result = append(result, llm.Message{
				Role:       "tool",
				Content:    content,
				ToolCallID: toolCallID,
			})
		}
	}
	flushAssistant()

	return result
}
