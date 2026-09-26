// 本文件是 snapshot 域的【数据传输面】（DTO）。
//
// ── 为什么是「领域字段」而不是存储结构 ───────────────────────────────
// 存储结构（DAO 内核）是 sessions 表的一条记录：
//
//	{"history": "<JSON 字符串>", "snapshot_turn": "<轮次>"}
//
// 即「两个列 + 一个塞在字符串里的 JSON」。把这种形状搬出门面（例如
// `{HistoryJSON string, SnapshotTurn string}`，或直接把 data.Record / data.Snapshot 原样透出）
// 只是**换了地方暴露格式**：调用方会被迫自己解析 JSON、自己知道"历史存在 history 列"，
// 将来换存储（列名 / 分表 / 独立库）就会把改动漏到每个调用方。
//
// 本文件的形状 = 领域模型：
//   - 快照 = 会话 + 轮次 + 消息数组（Messages，已解析的结构化消息，不是 JSON 字符串）；
//   - 消息 = 角色 / 类型 / 正文 / 工具调用 / 思维链 / 工具元信息（**平铺领域字段**）；
//   - 工具调用 = id / 类型 / **工具名** / **参数**——内核与 wire 里的 `function:{name,arguments}`
//     嵌套是协议格式的产物，领域里就是"名字 + 参数"两格，故此处**打平**
//     （副作用之一：生成器要求具名 struct，匿名嵌套结构它不接受，见 TECH-POC/facade §6）。
//
// 字段覆盖 = 调用方的**全部合法需求**：压缩插件是"读改写"，保留段原样回写，
// 因此消息的每个字段都必须过得了 DTO（漏一个就是静默丢数据，如 Meta/Reasoning）。
package facade

// Message 是会话消息（领域字段；对齐会话消息的语义，不映射 sessions 表的列）。
type Message struct {
	// Role 消息角色（system / user / assistant / tool）。
	Role string `json:"role"`
	// Kind 用户消息来源标记（text=真实提问 / notify=工具通知 / continue / resume；
	// 空 = 未标记）。压缩按「用户发起消息」定位轮次，故该标记必须过门面。
	Kind string `json:"kind,omitempty"`
	// Content 正文。
	Content string `json:"content"`
	// ToolCallID 工具结果消息所属的调用 id（role=tool 时携带）。
	ToolCallID string `json:"tool_call_id,omitempty"`
	// ToolCalls 该消息发起的工具调用（role=assistant 时携带）。
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// Reasoning 思维链（DeepSeek reasoning_content；落库保留、组装请求时按协议取舍）。
	Reasoning string `json:"reasoning,omitempty"`
	// Meta 该工具自身的元信息（工具契约 `_meta` 的续存字段；role=tool 时携带，
	// 前端据此本地判定裁决项）。**必须过门面**：压缩保留段原样回写，漏掉即丢字段。
	Meta map[string]any `json:"meta,omitempty"`
}

// ToolCall 是工具调用（领域字段平铺：工具名与参数各一格）。
type ToolCall struct {
	// ID 调用 id（与 ToolCallID 配对）。
	ID string `json:"id"`
	// Type 调用类型（现取值为 function）。
	Type string `json:"type"`
	// Name 工具名。
	Name string `json:"name"`
	// Arguments 参数（JSON 字符串，按契约原样）。
	Arguments string `json:"arguments"`
}

// Snapshot 是会话快照（领域字段：会话 + 轮次 + 消息数组）。
type Snapshot struct {
	// SessionID 会话 id（快照的归属；写回时按它定位）。
	SessionID string `json:"session_id"`
	// Turn 该快照已覆盖到的轮次（空 = 无覆盖轮，即全部轮次都在快照内）。
	Turn string `json:"turn"`
	// Messages 快照消息数组（已解析；快照可直接发送，压缩 = 对其做变换）。
	Messages []Message `json:"messages"`
}

// ── 请求 / 响应 ────────────────────────────────────────────────────

// SnapshotGetRequest 是快照读取入参。
type SnapshotGetRequest struct {
	// InstanceID 实例 id（数据根由门面按它解析，调用方不构造路径，见 23 §7）。
	InstanceID string `json:"instance_id"`
	// SessionID 会话 id。
	SessionID string `json:"session_id"`
	// Scope 实例数据根（可选）：调用方从事件载荷（61 §4.1 的 work_dir/data_dir 事实）带入，
	// 供本进程尚未登记该实例时自登记。空 = 仅按 InstanceID 解析。
	Scope Scope `json:"scope,omitempty"`
}

// SnapshotGetResponse 是快照读取出参。
type SnapshotGetResponse struct {
	// Found 是否存在快照（无快照 / 无记录 → false，且 Snapshot 为零值）。
	Found bool `json:"found"`
	// Snapshot 快照内容（Found=false 时无意义）。
	Snapshot Snapshot `json:"snapshot"`
}

// SnapshotSetRequest 是快照写回入参（会话由 Snapshot.SessionID 指定）。
type SnapshotSetRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// Snapshot 要写回的快照。
	Snapshot Snapshot `json:"snapshot"`
	// Scope 实例数据根（可选；语义同 SnapshotGetRequest.Scope）。
	Scope Scope `json:"scope,omitempty"`
}

// SnapshotSetResponse 是快照写回出参。
type SnapshotSetResponse struct {
	// OK 写入是否成功（失败走 error）。
	OK bool `json:"ok"`
}
