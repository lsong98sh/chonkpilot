// 本文件是 message 域（消息：落库 / 会话重建 / 上下文组装 / 按键取正文）的【数据传输面】（DTO）。
//
// ── 为什么是「领域字段」而不是存储结构 ───────────────────────────────
// 存储形状（内核事实）：messages 表一条记录 = role / kind / content / tool_call_id /
// tool_calls（**JSON 字符串**，形状 `[{id,type,function:{name,arguments}}]`）/ reasoning /
// brief / tool_call_status / `_meta`；role=tool 的 content 是 `{call,result,async}` JSON。
// 这些**都不出门面**（23 §7 · G-33 明列）：
//   - 消息 = 结构化字段（工具调用打平为「名字 + 参数」，与试点批 facade.Message 同源）；
//   - 历史视图 = 消息视图（工具调用/结果展开为工具卡片字段组），不是存储行。
//
// 「按键取正文」的键语义沿用既有消息面契约（61 §3.2：`message:<id>` / `tool_call:<id>` /
// `tool_result:<tool_call_id>`）——值形态 = **领域正文**（消息正文 / 工具参数 JSON /
// 工具结果载荷），门面只做键 → 正文的领域映射，不解释正文内部结构。
package facade

// MessageView 是消息视图（会话历史的展示面；工具调用/结果展开为前端消费字段）。
//
// 两种形态（**视图判别 = ToolPair**，不是消息面字段，故 json 不导出）：
//   - ToolPair=false：普通消息（正文 / 思维链 / 摘要 / 工具调用请求体）；
//   - ToolPair=true ：工具调用/结果卡片（工具名 / 参数 / 结果摘要 / 状态）。
type MessageView struct {
	// ToolPair 该条是工具调用/结果视图（决定下方面字段组；非消息面字段，见上）。
	ToolPair bool `json:"-"`

	// Role 消息角色（system / user / assistant / tool）。
	Role string `json:"role"`
	// Type 消息视图类型（工具卡片恒 tool_pair；其余取消息行自身标记）。
	Type string `json:"type,omitempty"`
	// MessageID 消息 id。
	MessageID string `json:"message_id"`
	// TurnID 归属轮次 id。
	TurnID string `json:"turn_id,omitempty"`
	// SessionID 归属会话 id（普通消息分支携带）。
	SessionID string `json:"session_id,omitempty"`

	// ── 普通消息分支 ──
	// Content 正文（截断：思维链类消息只给前 3 行 + HasMore）。
	Content string `json:"content,omitempty"`
	// Kind 用户消息来源标记（text=真实提问 / notify=工具通知 / continue / resume）。
	Kind string `json:"kind,omitempty"`
	// Brief 落库摘要（LLM 必填的调用理由 / 简化内容）。
	Brief string `json:"brief,omitempty"`
	// ToolCallID 工具结果消息所属的调用 id。
	ToolCallID string `json:"tool_call_id,omitempty"`
	// ToolCallStatus 工具调用生命周期状态（落库字段）。
	ToolCallStatus string `json:"tool_call_status,omitempty"`
	// Reasoning 思维链全文（截断见 Content）。
	Reasoning string `json:"reasoning,omitempty"`
	// ToolCalls 该消息发起的工具调用（领域平铺：名字 + 参数）。
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// Meta 该工具自身的元信息（工具契约 `_meta` 续存字段）。
	Meta map[string]any `json:"meta,omitempty"`
	// CreatedAt 创建时间（RFC3339）。
	CreatedAt string `json:"created_at,omitempty"`
	// HasMore 正文被截断（还有更多内容，前端据此提供"查看全文"）。
	HasMore *bool `json:"has_more,omitempty"`

	// ── 工具卡片分支（ToolPair=true）──
	// Tool 工具名。
	Tool string `json:"tool,omitempty"`
	// TaskID 后台任务 id（executor 生成；无 = 未转后台）。
	TaskID string `json:"task_id,omitempty"`
	// Arguments 工具参数 JSON 字符串（按契约原样）。
	Arguments string `json:"arguments,omitempty"`
	// Simplified 调用单行摘要（工具名 + 关键参数）。
	Simplified string `json:"simplified,omitempty"`
	// Result 工具结果（截断模式为空串 + HasMore）。
	Result string `json:"result,omitempty"`
	// ResultSimplified 结果单行摘要。
	ResultSimplified string `json:"result_simplified,omitempty"`
	// ResultSuccess 结果是否成功（= 状态为 completed）。
	ResultSuccess *bool `json:"result_success,omitempty"`
	// Status 工具调用生命周期状态（completed / failed / cancelled / interrupted / …）。
	Status string `json:"status,omitempty"`
}

// ── 请求 / 响应 ────────────────────────────────────────────────────

// MessageAppendRequest 是消息落库入参（LLM 产出逐条落库）。
type MessageAppendRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// TurnID 归属轮次 id（必填）。
	TurnID string `json:"turn_id"`
	// Message 消息（领域形态；role=tool 的正文由实现侧规整为工具载荷）。
	Message Message `json:"message"`
	// SessionID 归属会话 id（可选；空 = 由轮次记录回填）。
	SessionID string `json:"session_id,omitempty"`
	// Brief 摘要（可选；空 = 由实现侧尽力生成：工具=工具名+参数摘要 / 思维链=前 3 行）。
	Brief string `json:"brief,omitempty"`
	// ToolCallStatus 工具调用状态（可选；role=tool 时随结果载荷落库）。
	ToolCallStatus string `json:"tool_call_status,omitempty"`
	// Scope 实例数据根（可选；语义同 SnapshotGetRequest.Scope）。
	Scope Scope `json:"scope,omitempty"`
}

// MessageAppendResponse 是消息落库出参。
type MessageAppendResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}

// MessageLoadRequest 是读某轮次全部消息入参（LLM 会话重建）。
type MessageLoadRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// TurnID 轮次 id（必填）。
	TurnID string `json:"turn_id"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// MessageLoadResponse 是读某轮次全部消息出参（时间升序）。
type MessageLoadResponse struct {
	// Messages 消息数组（工具调用为结构化领域字段）。
	Messages []Message `json:"messages"`
}

// MessageContextRequest 是会话上下文组装入参（llm-start 恢复上下文一次到位）。
type MessageContextRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// SessionID 会话 id（必填）。
	SessionID string `json:"session_id"`
	// ExcludeTurn 排除该轮次（空 = 不排除）。
	ExcludeTurn string `json:"exclude_turn,omitempty"`
	// IncludeSnapshot 优先用快照前缀（快照存在且非空 → 快照 + 覆盖轮之后的历史）。
	IncludeSnapshot bool `json:"include_snapshot,omitempty"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// MessageContextResponse 是会话上下文组装出参。
type MessageContextResponse struct {
	// Messages 组装结果（快照前缀 + 其后轮次消息；或全量历史含 [历史摘要]）。
	Messages []Message `json:"messages"`
	// TurnTokens 轮次 token **伴随数组**（P3，2026-09-25，**只增**）：本会话各轮按时间升序
	// （排除 ExcludeTurn），每项 {turn_id, full, brief}；缺预存值 → 字段省略（消费方回退估算）。
	// 不改既有 Messages 语义，旧调用方忽略该键即逐字节等价。
	TurnTokens []TurnToken `json:"turn_tokens,omitempty"`
}

// MessageContentRequest 是按 key 批量取正文入参。
type MessageContentRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// SessionID 会话 id（必填）。
	SessionID string `json:"session_id"`
	// Keys 取值键（`message:<消息id>` / `tool_call:<调用id>` / `tool_result:<调用id>`）。
	Keys []string `json:"keys"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// MessageContentResponse 是按 key 批量取正文出参（未命中的 key 省略）。
type MessageContentResponse struct {
	// Contents 键 → 正文（消息正文 / 工具参数 JSON / 工具结果载荷）。
	Contents map[string]string `json:"contents"`
}
