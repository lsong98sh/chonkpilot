// 跨模块共享契约类型（DAO 内核层声明；chonkpilot-server 经类型别名、plugin 系列
// 直接 import 引用——契约类型属于数据组件对外的最小共享面，不承载业务逻辑）。
//
// 2026-10-24（Persist Provider）：`ChatMsg` / `ToolCall` 的唯一定义上移到
// `chonkpilot-session/store`（组件与数据面共用同一契约类型），此处为别名；
// `Snapshot` 仍由数据面持有（history 字段使用同一 ChatMsg 别名，JSON 形状不变）。
//
// 会话历史快照（对齐 21-llm-server / 28-plugins）：
// sessions 表 (history, snapshot_turn) 两个字段；history = JSON []ChatMsg（含 kind）。
// 快照即消息数组（可直接发送），压缩 = 快照变换（摘要 + 保留段），恢复/组装零编辑。
package data

import "github.com/chonkpilot/chonkpilot-session/store"

// ChatMsg 是 chat 消息（对齐 OpenAI 协议；快照格式）。
type ChatMsg = store.ChatMsg

// ToolCall 是 LLM 请求的工具调用（快照格式）。
type ToolCall = store.ToolCall

// Snapshot 是会话快照（sessions 表 history + snapshot_turn 两字段）。
type Snapshot struct {
	History      []ChatMsg `json:"history"`
	SnapshotTurn string    `json:"snapshot_turn"`
}
