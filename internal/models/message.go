package models

import "time"

// Message represents a single message within a turn.
// Type field distinguishes the message variant: "text", "reasoning", "tool_call", "tool_result".
// Content holds the actual payload (plain text for text/reasoning, JSON for tool_call/tool_result).
type Message struct {
	MessageID string `json:"message_id"`
	TurnID    string `json:"turn_id"`
	Role      string `json:"role"` // system / user / assistant / tool
	Type      string `json:"type"` // text / reasoning / tool_call / tool_result
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

// NewMessage creates a new Message with generated ID and timestamp.
// type_ is one of: "text", "reasoning", "tool_call", "tool_result".
func NewMessage(messageID, turnID, role, type_, content string) *Message {
	return &Message{
		MessageID: messageID,
		TurnID:    turnID,
		Role:      role,
		Type:      type_,
		Content:   content,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// ToolCallPayload is the JSON content for type="tool_call" messages.
type ToolCallPayload struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Arguments  string `json:"arguments"`
}

// ToolResultPayload is the JSON content for type="tool_result" messages.
type ToolResultPayload struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Result     string `json:"result"`
}

// ReasoningPayload is the JSON content for type="reasoning" messages.
type ReasoningPayload struct {
	Content string `json:"content"`
}
