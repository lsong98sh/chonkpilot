// 跨模块共享契约类型（DAO 内核层声明；chonkpilot-server 经类型别名、plugin 系列
// 直接 import 引用——契约类型属于数据组件对外的最小共享面，不承载业务逻辑）。
//
// 会话历史快照（对齐 21-llm-server / 28-plugins）：
// sessions 表 (history, snapshot_turn) 两个字段；history = JSON []ChatMsg（含 kind）。
// 快照即消息数组（可直接发送），压缩 = 快照变换（摘要 + 保留段），恢复/组装零编辑。
package data

// ChatMsg 是 chat 消息（对齐 OpenAI 协议；快照格式）。
// Kind 标记 user 消息来源：text=用户真实提问（text-user）/ notify=工具完成通知（text-notify）。
// 压缩定位 N 轮只数 Kind=text，排除 notify；assistant/tool 消息无 Kind。
// Reasoning = 思维链（DeepSeek reasoning_content）：落库/快照持久化保留；组装请求时按协议
// 规则决定回传（仅"带 tool_calls 的 assistant"回传，见 chonkpilot-llm/server 组装侧）。
type ChatMsg struct {
	Role       string     `json:"role"`
	Kind       string     `json:"kind,omitempty"`
	Content    string     `json:"content"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	Reasoning  string     `json:"reasoning,omitempty"`
	// Meta = 该工具自身的 meta 子集（gateway tools/list `_meta` 原文：async/timeout/category/
	// server/hot 等；JSON 名 `_meta`）。仅 role=tool 工具结果消息携带（一个 tool-call 一条记录），
	// 随消息落库并回带前端，使前端无需依赖一次性 mcp-tools-timeout 事件即可本地判断裁决项
	// （I-60）。不参与 LLM 请求（toWireMessages 显式白名单字段）。
	Meta map[string]any `json:"_meta,omitempty"`
}

// ToolCall 是 LLM 请求的工具调用（快照格式）。
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Snapshot 是会话快照（sessions 表 history + snapshot_turn 两字段）。
type Snapshot struct {
	History      []ChatMsg `json:"history"`
	SnapshotTurn string    `json:"snapshot_turn"`
}
