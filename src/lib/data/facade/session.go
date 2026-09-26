// 本文件是 session 域（会话 CRUD / 活动态 / 幂等建会话）的【数据传输面】（DTO）。
//
// ── 为什么是「领域字段」而不是存储结构 ───────────────────────────────
// 存储形状（内核事实）：sessions 表一条记录 = 标题 / parent_id / created_at / updated_at
// 五列 + `history`（**塞在字符串里的消息数组 JSON**）+ `snapshot_turn`。
// 23 §7 · G-33 明确把 `history` / `snapshot_turn` 列为**消息面/存储形状**，故本 DTO 用领域
// 形态承载：
//   - 会话 = id / 标题 / 父会话 / 创建与更新时间 / 最近活动 + **快照**（messages + turn，
//     复用试点批 facade.Snapshot 的领域形态：消息已解析为结构化数组，不是 JSON 字符串）。
//
// 换存储（列名 / 分表 / 独立库）不会把改动漏到调用方。
//
// 字段覆盖 = 调用方的**全部合法需求**：列表 / 详情 / 最新 / 改名 / 删 / 活动态读写 / 幂等建。
package facade

// Session 是会话（领域字段；对话粒度 = 会话 → 轮次 → 消息，见 session/turn 两域）。
type Session struct {
	// ID 会话 id。
	ID string `json:"id"`
	// Title 会话标题。
	Title string `json:"title,omitempty"`
	// ParentID 父会话 id（空 = 顶层会话；子会话仅顶层会话进列表/最新）。
	ParentID string `json:"parent_id,omitempty"`
	// CreatedAt 创建时间（RFC3339）。
	CreatedAt string `json:"created_at,omitempty"`
	// UpdatedAt 更新时间（RFC3339）。
	UpdatedAt string `json:"updated_at,omitempty"`
	// LastActivity 最近活动时间（可选；空 = 取 UpdatedAt，与既有"活动排序"口径一致）。
	LastActivity string `json:"last_activity,omitempty"`
	// Snapshot 该会话的快照（领域形态：messages + turn）。
	// **nil = 该会话从未写过快照**（存储侧无 history/snapshot_turn 两列）——
	// 与"有快照但为空"（非 nil 且 Messages 为空）是两件不同的事，故用指针保真。
	Snapshot *Snapshot `json:"snapshot,omitempty"`
}

// ── 请求 / 响应 ────────────────────────────────────────────────────

// SessionListRequest 是会话列表读入参。
type SessionListRequest struct {
	// InstanceID 实例 id（数据根由门面按它解析，调用方不构造路径，见 23 §7）。
	InstanceID string `json:"instance_id"`
	// Scope 实例数据根（可选；语义同 SnapshotGetRequest.Scope：供本进程尚未登记该实例时自登记）。
	Scope Scope `json:"scope,omitempty"`
}

// SessionListResponse 是会话列表读出参（仅顶层会话，按最近活动降序）。
type SessionListResponse struct {
	// List 会话列表。
	List []Session `json:"list"`
}

// SessionGetRequest 是会话详情读入参。
type SessionGetRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// SessionID 会话 id。
	SessionID string `json:"session_id"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// SessionGetResponse 是会话详情读出参（会话不存在 → Found=false，**不是错误**）。
type SessionGetResponse struct {
	// Found 是否存在该会话。
	Found bool `json:"found"`
	// Session 会话内容（Found=false 时无意义）。
	Session Session `json:"session"`
}

// SessionLatestRequest 是最近活动顶层会话读入参。
type SessionLatestRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// SessionLatestResponse 是最近活动顶层会话读出参（无会话 → 空串）。
type SessionLatestResponse struct {
	// SessionID 最近活动顶层会话 id（无 → 空串）。
	SessionID string `json:"session_id"`
}

// SessionTitleRequest 是会话改名入参。
type SessionTitleRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// SessionID 会话 id。
	SessionID string `json:"session_id"`
	// Title 新标题。
	Title string `json:"title"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// SessionTitleResponse 是会话改名出参。
type SessionTitleResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}

// SessionDeleteRequest 是会话删除入参（连带删除该会话的轮次与消息；活动会话一并清活动态）。
type SessionDeleteRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// SessionID 会话 id。
	SessionID string `json:"session_id"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// SessionDeleteResponse 是会话删除出参（会话不存在 → OK=true，幂等）。
type SessionDeleteResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}

// SessionActiveSetRequest 是「当前活动会话」写入参。
type SessionActiveSetRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// SessionID 活动会话 id。
	SessionID string `json:"session_id"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// SessionActiveSetResponse 是「当前活动会话」写出参。
type SessionActiveSetResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}

// SessionActiveGetRequest 是「当前活动会话」读入参。
type SessionActiveGetRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// SessionActiveGetResponse 是「当前活动会话」读出参。
type SessionActiveGetResponse struct {
	// SessionID 活动会话 id（未设置 / 已失效 → 回落最近活动顶层会话；无会话 → 空串）。
	SessionID string `json:"session_id"`
}

// SessionEnsureRequest 是幂等建会话入参（已存在不覆盖）。
type SessionEnsureRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// SessionID 会话 id（必填）。
	SessionID string `json:"session_id"`
	// ParentSessionID 父会话 id（可选；非空 = 子会话）。
	ParentSessionID string `json:"parent_session_id,omitempty"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// SessionEnsureResponse 是幂等建会话出参。
type SessionEnsureResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}
