// Package router 是多 Provider（LLM 服务商）统一接入的 canonical 层：统一请求 / 响应 / 事件
// 类型（LR-1）+ 协议适配器注册与分派（LR-2）。见 docs/spec/40-roadmap/40-演进计划.md §LR。
//
// 边界（LR-12，术语见 docs/spec/60-reference/60-名词约定.md §4）：
//
//   - Router 只表达「**一次**」（call）= 一次 LLM 请求—响应（LLM 输出 tool-call 即该次结束）；
//   - Router **无状态**：**不得**出现轮次（turn）/ 会话（session）概念 —— 无历史管理、无上下文
//     拼接、无工具循环、无子 LLM 调度；这些全归 llm 层的编排（会话 / 轮次 / 工具循环）；
//   - Router **只做出网（LLM）**：工具执行 / 工具注册 / 工具清单归 gateway，router 只接收
//     []ToolDef；
//   - **边界含「发送」**：一次 `Call` = 编码 → 发送（HTTP）→ 流式读取 → 解析 → 出事件；本包
//     **持有连接生命周期**（2026-09-22 定案）；
//   - 重试（2026-09-22 定案，口径见 `docs/spec/40-roadmap/40-演进计划.md` §LR §1.1）：
//     **「透明重发」（对上层不可见、语义不变）归本包** —— 仅当**尚未向上层输出过任何事件**且属
//     连接层失败（`Kind: network`）时透明重发 **≤1 次**（毫秒级退避，见 `retry.go`）；
//     **「需要决策或可见的重试」全归 llm**（超时 / 已输出后失败 / 上报「重试中」/ cancel / 次数
//     用尽处置）；**判定与退避值**（`Error.Retryable` / `RetryAfter` / `RetryWait`）由本包出，
//     决策归 llm —— 总量归 llm 掌管（router ≤1 + llm `retryCount` = 总额上限，不放大）；
//   - 协议实现：内置适配器 `openai`（chat completions）/ `responses`（OpenAI Responses API，D-29）/
//     `anthropic`（`/v1/messages`）/ `echo`（**内置兜底**：无可用 provider 时启用，D-30）；
//   - 命名：本包用 Spec 表示「LLM 服务商」；**不引入 Provider 标识符** —— gateway.Provider 是
//     工具提供方（同名异义）。
//
// 形态：纯 lib（**无 facade / 无 MQ / 无 DB / 无 exe**，与 src/lib/core/dsl 同类）；
// **只 import 标准库**（外加本 module 自己的 `internal/*`：`internal/canon` = canonical 类型与
// 错误分类的**定义处**，根包以别名/常量/函数转发再导出；`internal/adaptor/**` = 协议实现），
// **不 import 任何其它 chonkpilot-* module**。
//
// 无环：`internal/adaptor/**` 只依赖 `internal/canon`，**不 import 根包**；根包反之依赖 adaptor
// 层装配适配器 —— 方向单向（`router → internal/adaptor → internal/canon`），无 import 环
// （取证：`go list -deps ./internal/adaptor/...` 不含 `github.com/chonkpilot/chonkpilot-router`）。
//
// 与 data 的关系（2026-09-22 用户定案：**转换层归 llm，router 不访问 data**）：
// llm 层从 data 读出 ChatMsg（拼接 / 组装）→ 转成 router canonical → 交给 router；
// router 只认自持的 canonical（canonical → 具体协议格式 → 发送 + 流式解析 → 回 Event）。
// 故 `data.ChatMsg` / `data.ToolCall` ↔ canonical 的薄转换**落在 llm 侧**
// （`src/lib/llm/server/routerconv.go`），本 module 由此保持纯标准库、不反向依赖 data。
package router
