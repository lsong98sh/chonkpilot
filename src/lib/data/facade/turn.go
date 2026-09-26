// 本文件是 turn 域（轮次：幂等建轮 / 摘要 / 终态 / 遗留清理 / 历史分页）的【数据传输面】（DTO）。
//
// ── 为什么是「领域字段」而不是存储结构 ───────────────────────────────
// 存储形状（内核事实）：turns 表一条记录 = turn_id / session_id / status / summary /
// finish_reason / created_at / updated_at；历史分页要把 turns 行 + messages 行（含 tool_pair
// 展开）拼成前端消费视图。门面 DTO 用领域形态：
//   - 轮次 = id / 归属会话 / 状态 / 摘要 / 结束原因 / 时间戳；
//   - 历史 = 轮次数组 + 消息视图数组 + 是否还有更早（has_more）。
//
// 「无该字段」与「字段为空串」用指针区分（存储侧是"有键/无键"，改键名或换存储不影响调用方）。
package facade

// Turn 是会话轮次（领域字段）。
type Turn struct {
	// ID 轮次 id。
	ID string `json:"id"`
	// SessionID 归属会话 id。
	SessionID string `json:"session_id,omitempty"`
	// Status 轮次状态（running / done / error / interrupted）。
	Status string `json:"status,omitempty"`
	// Summary 轮次摘要（有值 = 该轮已被压缩）；nil = 该轮无摘要字段。
	Summary *string `json:"summary,omitempty"`
	// FinishReason 终态原因；nil = 该轮尚未写终态。
	FinishReason *string `json:"finish_reason,omitempty"`
	// FullTokens 该轮**完整态** token 数（轮次结束时预存；nil = 该轮无预存值，消费方回退实时估算）。
	FullTokens *int `json:"full_tokens,omitempty"`
	// BriefTokens 该轮**简化态** token 数（= 结论态；无结论时等于完整态；nil = 无预存值）。
	BriefTokens *int `json:"brief_tokens,omitempty"`
	// CreatedAt 创建时间（RFC3339）。
	CreatedAt string `json:"created_at,omitempty"`
	// UpdatedAt 更新时间（RFC3339）。
	UpdatedAt string `json:"updated_at,omitempty"`
}

// ── 请求 / 响应 ────────────────────────────────────────────────────

// TurnToken 是轮次 token 的**伴随数组**项（P3，2026-09-25；只增通道）：随
// `data-session-context` 返回，供消费方按轮**直接取预存 token**、免重复实时估算；
// 缺值（历史轮无预存）= 对应字段省略 → 消费方回退实时估算（绝不当 0）。
type TurnToken struct {
	// TurnID 轮次 id（数组按轮次时间**升序**；消费方按**尾部对齐**取用，见 data.ResolveStoredTokens）。
	TurnID string `json:"turn_id"`
	// Full 该轮**完整态** token 数；nil = 无预存值。
	Full *int `json:"full,omitempty"`
	// Brief 该轮**简化态**（仅 text，见 data.BriefMessages）token 数；nil = 无预存值。
	Brief *int `json:"brief,omitempty"`
}

// TurnHistoryRequest 是历史分页读入参（按 before_turn_id 向前翻页）。
type TurnHistoryRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// SessionID 会话 id。
	SessionID string `json:"session_id"`
	// BeforeTurnID 从此轮次**之前**继续向前翻页（空 = 从最新往回取）。
	BeforeTurnID string `json:"before_turn_id,omitempty"`
	// TargetMessages 期望消息条数上限（0 = 实现侧缺省 50）。
	TargetMessages int `json:"target_messages,omitempty"`
	// TargetBytes 期望内容字节上限（0 = 实现侧缺省 200KB）。
	TargetBytes int `json:"target_bytes,omitempty"`
	// Scope 实例数据根（可选；语义同 SnapshotGetRequest.Scope）。
	Scope Scope `json:"scope,omitempty"`
}

// TurnHistoryResponse 是历史分页读出参（时间升序；消息含工具调用展开与思维链截断）。
type TurnHistoryResponse struct {
	// Turns 本页轮次（时间升序）。
	Turns []Turn `json:"turns"`
	// Messages 本页消息视图（时间升序）。
	Messages []MessageView `json:"messages"`
	// HasMore 是否还有更早的历史（前端据此显示"加载更多"）。
	HasMore bool `json:"has_more"`
}

// TurnEnsureRequest 是幂等建轮次入参（已存在复用，不覆盖）。
type TurnEnsureRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// TurnID 轮次 id（必填）。
	TurnID string `json:"turn_id"`
	// SessionID 归属会话 id。
	SessionID string `json:"session_id"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// TurnEnsureResponse 是幂等建轮次出参。
type TurnEnsureResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}

// TurnSetSummaryRequest 是轮次摘要写入参（压缩回写；有值 = 该轮已被压缩）。
type TurnSetSummaryRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// TurnID 轮次 id（必填）。
	TurnID string `json:"turn_id"`
	// Summary 摘要全文。
	Summary string `json:"summary"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// TurnSetSummaryResponse 是轮次摘要写出参。
type TurnSetSummaryResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}

// TurnCompleteRequest 是轮次终态写入参（保留原有字段，只更新状态侧）。
type TurnCompleteRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// TurnID 轮次 id（必填）。
	TurnID string `json:"turn_id"`
	// Status 终态（done / error / interrupted）。
	Status string `json:"status"`
	// FinishReason 结束原因（可选）。
	FinishReason string `json:"finish_reason,omitempty"`
	// FullTokens 该轮**完整态** token 数（可选，P3 预存；nil = 本次不写该字段）。
	FullTokens *int `json:"full_tokens,omitempty"`
	// BriefTokens 该轮**简化态** token 数（可选，P3 预存；nil = 本次不写该字段）。
	BriefTokens *int `json:"brief_tokens,omitempty"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// TurnCompleteResponse 是轮次终态写出参。
type TurnCompleteResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}

// TurnCleanupStaleRequest 是遗留 running 轮次清理入参（进程崩溃/重启后防同会话双开）。
type TurnCleanupStaleRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// TurnCleanupStaleResponse 是遗留 running 轮次清理出参。
type TurnCleanupStaleResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
	// Count 被标记为 interrupted 的轮次数。
	Count int `json:"count"`
}
