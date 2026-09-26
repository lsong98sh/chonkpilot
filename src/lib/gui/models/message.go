package models

import (
	"encoding/json"
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

// ToolPairPayload 是 type="tool_pair" 消息的 content JSON 结构。
// 存储对齐前端消费格式（tool_pair），tool_call + tool_result 两条历史合并为一条。
// 合二为一：ToolCallID（LLM 生成）与 TaskID（executor 生成）落在同一条消息上，
// 两个索引（by_call / by_task）都指向它，tool_result 用任意一个都能查到。
type ToolPairPayload struct {
	ToolCallID string `json:"tool_call_id"`    // LLM 生成的 id（LLM 发起时才有）
	TaskID     string `json:"task_id"`         // executor 生成，必有（_tool_id / cancel / tool_result 用）
	Name       string `json:"name"`            // 工具名
	Args       any    `json:"args"`            // 参数
	Result     string `json:"result"`          // 结果内容（见生命周期：provisional 后永驻话术）
	Reply      string `json:"reply,omitempty"` // 转后台任务的正式完成结果（调用方已离开，不覆盖 result）
	Status     string `json:"status"`          // pending / running / provisional / completed / failed / cancelled
	Notify     bool   `json:"notify"`          // true=调用方已离开（wait 超时/async），结果写 reply；false=调用方还在等/已拿到
}

// NewToolPairPayload 构造一个 tool_pair content JSON 字符串。
func NewToolPairPayload(p *ToolPairPayload) (string, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return string(raw), nil
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
