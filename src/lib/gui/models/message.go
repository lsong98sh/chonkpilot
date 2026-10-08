package models

import (
	"time"
)

// Message represents a single message within a turn.
// Type field distinguishes the message variant: "text", "reasoning", "tool_pair".
// Content holds the actual payload (plain text for text/reasoning, JSON for tool_pair).
// ToolCallID/TaskID/Status 是 tool_pair 消息的冗余字段（与 content JSON 保持一致），
// 供 bbolt 索引桶（tool_pair_by_call/by_task/by_status）维护与反查，omitempty 保持旧 JSON 兼容。
type Message struct {
	MessageID string `json:"message_id"`
	TurnID    string `json:"turn_id"`
	Role      string `json:"role"` // system / user / assistant / tool
	Type      string `json:"type"` // text / reasoning / tool_pair
	Content   string `json:"content"`
	Brief     string `json:"brief,omitempty"` // first 3 lines of reasoning for tool_call messages
	CreatedAt string `json:"created_at"`
	// tool_pair 冗余字段
	ToolCallID string `json:"tool_call_id,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
	Status     string `json:"status,omitempty"`
}

// NewMessage creates a new Message with generated ID and timestamp.
// type_ is one of: "text", "reasoning", "tool_pair".
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

// ToolPair status 常量（tool_pair 消息生命周期）。
const (
	ToolStatusPending     = "pending"
	ToolStatusRunning     = "running"
	ToolStatusProvisional = "provisional"
	ToolStatusCompleted   = "completed"
	ToolStatusFailed      = "failed"
	ToolStatusCancelled   = "cancelled"   // 用户主动取消（同步工具被强杀 / 后台任务被 task-stop）
	ToolStatusInterrupted = "interrupted" // 进程异常/应用关闭中断（abort），可手动重试
)

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
