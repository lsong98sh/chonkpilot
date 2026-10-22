// Package chat 是 LLM 对话组件内核（MOD-1 M1a）：帧栈 / 工具循环 / 续写 / TurnState。
//
// 边界（硬约束，L3 断言见 deps_test.go）：
//   - 只依赖标准库；**不 import** mq / msgkeys / router / gateway / data / facade；
//   - 环境能力（读库、拼上下文、落库、事件广播、工具执行、目录）全部经本包声明的端口注入；
//   - 端口签名只使用本包自有类型，宿主 adapter 负责与 data / gateway / router 互转；
//   - 不区分「主会话 / 子会话 / 群聊」：执行单元只有 RunTurn / RunSubTurn（M5 起）。
//
// 端口集合（M1a）：ContextProvider · TurnStore · EventSink · ToolInvoker + ToolCatalog + Clock；
// 日志用标准库 *slog.Logger，不另设端口。
package chat
