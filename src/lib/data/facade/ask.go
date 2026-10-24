package facade

// AskAPI 是 ask 未答 / 失效回查门面（MOD-1 M6b）。
//
// **不建 ask 表**：ask_id 随发起时的 `role=tool` running 行落到 `messages.ask_id`，
// 本查询按该列回查。仅「启动扫描」与「失效回执」两条低频路径使用（全表扫描，
// 走 messages 既有桶，不新增索引）。
type AskAPI interface {
	// AskPending 回查 ask 行：
	//   - AskID 空 + PendingOnly → 全部未答 ask（启动扫描）；
	//   - AskID 非空 → 该 ask 精确回查（含已终态；失效回执据此把答案转新轮）。
	AskPending(req AskPendingRequest) (AskPendingResponse, error)
}

// AskPendingRequest 回查入参。
type AskPendingRequest struct {
	InstanceID string `json:"instance_id"`
	// DialogID 对话 id（非空 → 只查该对话库；空 + 一树一库生效 → 跨对话库扫描）。
	DialogID string `json:"dialog_id,omitempty"`
	// AskID 非空 = 按 ask 精确回查（忽略 PendingOnly）。
	AskID string `json:"ask_id,omitempty"`
	// PendingOnly true = 只回非终态（pending / running / provisional）。
	PendingOnly bool  `json:"pending_only,omitempty"`
	Scope       Scope `json:"scope,omitempty"`
}

// AskPendingItem 一条 ask 行（来源 = messages 行）。
type AskPendingItem struct {
	AskID string `json:"ask_id"`
	// DialogID 该 ask 所在对话（一树一库时回带；旧布局不写键）。
	DialogID   string `json:"dialog_id,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	TurnID     string `json:"turn_id,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	// Status = messages.tool_call_status（running / interrupted / completed …）。
	Status    string `json:"status,omitempty"`
	Question  string `json:"question,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// AskPendingResponse 回查结果。
type AskPendingResponse struct {
	OK    bool             `json:"ok"`
	Items []AskPendingItem `json:"items,omitempty"`
}
