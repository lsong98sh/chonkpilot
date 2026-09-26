package models

import "time"

// Turn represents a single turn in a session. Acts as a grouping container
// for messages belonging to one user↔assistant interaction cycle.
// Actual message content is stored in the messages table.
// ScenarioID/ScenarioName are soft references (no FK) for history display
// only — scenario content itself is passed by value to the executor (D3).
type Turn struct {
	TurnID       string `json:"turn_id"`
	SessionID    string `json:"session_id"`
	Score        int    `json:"score"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	ScenarioID   string `json:"scenario_id"`
	ScenarioName string `json:"scenario_name"`
	// CurrentTurn 本轮新增 token 估算（新增 user/tool + completion，历史不计）。
	// TurnTotal 本 turn 全部发送（含历史）+ completion。均为 len/4 估算，仅展示。
	CurrentTurn int `json:"current_turn,omitempty"`
	TurnTotal   int `json:"turn_total,omitempty"`
	// Summary 上下文压缩摘要（写在"覆盖末轮"= 简化区最后一轮，倒序找第一个非空即边界）。
	Summary string `json:"summary,omitempty"`
	// CompressedTokens 该次压缩产出的摘要 token 数（与 Summary 同轮）。
	CompressedTokens int `json:"compressed_tokens,omitempty"`
	// FinishReason 该 turn 最后一次 LLM 响应的 finish_reason（stop/length/tool_calls/
	// content_filter/insufficient_system_resource）。前端据此识别"最后回复未完成"，
	// 决定是否显示/自动触发"继续"（length / insufficient_system_resource = 未写完）。
	FinishReason string `json:"finish_reason,omitempty"`
}

// TurnResult holds the result of executing a turn.
type TurnResult struct {
	TurnID string `json:"turn_id"`
	Answer string `json:"answer"`
	Score  int    `json:"score"`
}

// NewTurn creates a new Turn with generated ID and timestamps.
func NewTurn(turnID, sessionID string) *Turn {
	now := time.Now().UTC().Format(time.RFC3339)
	return &Turn{
		TurnID:    turnID,
		SessionID: sessionID,
		Score:     0,
		CreatedAt: now,
		UpdatedAt: now,
	}
}
