// 本文件是 `next_run` 自动续跑计数器（`sessions.next_run_count`）的【数据传输面】（DTO）。
//
// 口径（docs/next-run/next_run自动续跑设计方案.md §3.3）：
//   - 计数器 = 会话行可选列 `next_run_count`（**仅 > 0 才写键** → 普通会话行形状不变）；
//   - 用户发送即重置 0；每次 `next_run` 成功受理 +1（含覆盖上一条）；
//   - 上限判定与自增在同一事务内完成（单事务 RMW，避免并发绕过）。
//
// **零新增 MQ 主题**：三个方法由 llm-server 经门面直调（与 SessionGet 同路径）。
package facade

// SessionNextRunCheckAndAddRequest 是「判上限 + 自增」入参。
type SessionNextRunCheckAndAddRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// SessionID 会话 id（必填）。
	SessionID string `json:"session_id"`
	// Max 连续自动接续上限（<= 0 = 关闭；此处恒按拒绝处理，不增）。
	Max int `json:"max"`
	// Scope 实例数据根（可选；语义同其它 Session 请求）。
	Scope Scope `json:"scope,omitempty"`
}

// SessionNextRunCheckAndAddResponse 是「判上限 + 自增」出参。
type SessionNextRunCheckAndAddResponse struct {
	// Count 本次判定后的计数（allowed=true = 自增后的值；false = 当前已达上限的值）。
	Count int `json:"count"`
	// Allowed 是否受理（true 时已自增并落库）。
	Allowed bool `json:"allowed"`
}

// SessionNextRunResetRequest 是计数器归零入参。
type SessionNextRunResetRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// SessionID 会话 id（必填）。
	SessionID string `json:"session_id"`
	// Scope 实例数据根（可选）。
	Scope Scope `json:"scope,omitempty"`
}

// SessionNextRunResetResponse 是计数器归零出参。
type SessionNextRunResetResponse struct {
	// OK 是否成功（会话不存在 → 幂等成功）。
	OK bool `json:"ok"`
}

// SessionNextRunGetRequest 是计数器读入参（测试 / 诊断）。
type SessionNextRunGetRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// SessionID 会话 id（必填）。
	SessionID string `json:"session_id"`
	// Scope 实例数据根（可选）。
	Scope Scope `json:"scope,omitempty"`
}

// SessionNextRunGetResponse 是计数器读出参。
type SessionNextRunGetResponse struct {
	// Count 当前计数（会话不存在 / 未写键 → 0）。
	Count int `json:"count"`
}

// SessionNextRunDecRequest 是计数器补偿 -1 入参（`next_run` 落 inbox 失败时回滚本次自增）。
type SessionNextRunDecRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// SessionID 会话 id（必填）。
	SessionID string `json:"session_id"`
	// Scope 实例数据根（可选）。
	Scope Scope `json:"scope,omitempty"`
}

// SessionNextRunDecResponse 是计数器补偿出参。
type SessionNextRunDecResponse struct {
	// Count 补偿后的计数（不小于 0）。
	Count int `json:"count"`
}
