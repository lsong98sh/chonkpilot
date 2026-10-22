// INBOX（中途插话 / 等下一轮）数据面契约：prjusr 级 `inbox_items` 表。
//
// 设计见 `docs/agent-chat/中途插话设计方案.md` §6.2.3。表行语义：
//   - 所有发送先落本表（status=pending），实际插入 / 下一轮开始时才写 `messages`；
//   - type = insert（立即插入）| next_turn（等下一轮）；
//   - 消费 = 落 messages + 置 consumed（须原子 / 幂等键去重）。
package facade

// InboxItem 是 inbox 行（数据面形态；读回给 llm-server）。
type InboxItem struct {
	ID             string `json:"id"`
	SessionID      string `json:"session_id,omitempty"`
	TurnID         string `json:"turn_id,omitempty"`
	Type           string `json:"type"`
	LLM            string `json:"llm,omitempty"`
	Content        string `json:"content"`
	Think          any    `json:"think,omitempty"`
	Effort         any    `json:"effort,omitempty"`
	ScenarioID     string `json:"scenario_id,omitempty"`
	Status         string `json:"status"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
	// Source 来源标识（NEXTRUN-1 §3.4）：`"llm"` = 模型经 `next_run` 登记；
	// 缺省（空）= 用户输入。**仅非空才写键** —— 用户行形状与既有逐字节一致。
	Source string `json:"source,omitempty"`
}

// InboxAppendRequest 写入一条待处理发送。
type InboxAppendRequest struct {
	InstanceID     string `json:"instance_id"`
	SessionID      string `json:"session_id"`
	TurnID         string `json:"turn_id,omitempty"`
	Type           string `json:"type"`
	LLM            string `json:"llm,omitempty"`
	Content        string `json:"content"`
	Think          any    `json:"think,omitempty"`
	Effort         any    `json:"effort,omitempty"`
	ScenarioID     string `json:"scenario_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	// Source 来源标识（可选；`"llm"` = 模型 `next_run` 登记；空 = 用户输入）。
	Source string `json:"source,omitempty"`
	Scope  Scope  `json:"scope,omitempty"`
}

// InboxAppendResponse 返回落库 id（幂等命中时返回既有 id）。
type InboxAppendResponse struct {
	OK bool   `json:"ok"`
	ID string `json:"id,omitempty"`
}

// InboxListRequest 按会话读 inbox 项（status 缺省 = pending，按 created_at 升序）。
type InboxListRequest struct {
	InstanceID string `json:"instance_id"`
	SessionID  string `json:"session_id"`
	Status     string `json:"status,omitempty"`
	Scope      Scope  `json:"scope,omitempty"`
}

// InboxListResponse 返回 inbox 项集合。
type InboxListResponse struct {
	OK    bool        `json:"ok"`
	Items []InboxItem `json:"items"`
}

// InboxUpdateRequest 更新 inbox 项；空字段 = 不改。
type InboxUpdateRequest struct {
	InstanceID string `json:"instance_id"`
	ID         string `json:"id"`
	Status     string `json:"status,omitempty"`
	Type       string `json:"type,omitempty"`
	TurnID     string `json:"turn_id,omitempty"`
	// Content 新正文（可选；NEXTRUN-1 §3.5「同轮多次调用只保留最后一条」的就地覆盖；
	// 空 = 不改）。
	Content string `json:"content,omitempty"`
	Scope   Scope  `json:"scope,omitempty"`
}

// InboxUpdateResponse 统一 ok。
type InboxUpdateResponse struct {
	OK bool `json:"ok"`
}

// InboxCommitRequest 原子消费一条 inbox（MOD-1 M6）：**同一事务内**落 messages + 置 consumed。
// 幂等：同一 InboxID 重复提交 → 返回既有消息主键（不重复落库）。
type InboxCommitRequest struct {
	InstanceID string `json:"instance_id"`
	InboxID    string `json:"inbox_id"`
	TurnID     string `json:"turn_id"`
	// SessionID 可选；空 = 取 inbox 行的 session_id。
	SessionID string `json:"session_id,omitempty"`
	// Message 消息（领域形态；通常 role=user，Kind 标明来源如 insert / continue）。
	Message Message `json:"message"`
	// Brief 摘要（可选；空 = 由实现侧尽力生成）。
	Brief          string `json:"brief,omitempty"`
	ToolCallStatus string `json:"tool_call_status,omitempty"`
	Scope          Scope  `json:"scope,omitempty"`
}

// InboxCommitResponse 返回落库消息主键（幂等命中时返回既有主键）。
type InboxCommitResponse struct {
	OK bool   `json:"ok"`
	ID string `json:"id,omitempty"`
}
