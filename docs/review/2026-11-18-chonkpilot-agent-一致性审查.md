# chonkpilot-agent 一致性审查（2026-11-18）

审查对象：`E:\BizWorks\chonkpilot-agent` 工作区当前代码（含未提交改动）。
范围：`chat/`、`gateway/`、`router/`、`session/`、`types/`、`winproc/`。
方法：四个只读并行审计 + 交叉验证 + `go vet` / 各模块 `go test ./...`。
基线：`go vet` 全绿；`types/chat/session/router/gateway` 测试全绿。

> **前端说明**：本仓没有前端代码（无 `package.json`、`.vue/.ts/.js/.html`）。
> 前端在 `E:\BizWorks\chonkpilot\src\frontend`，本次未纳入逐行审查；本报告只给出
> agent 对前端的出站契约位置与已发现的契约缺口。

## 总评

分层方向本身成立（chat 执行 / session 组装与持久化 / gateway 工具执行 / router 协议），
但存在四类系统性问题：

1. **同一概念多套词表与多个裁决点**：turn 终态、inbox 类型/状态、任务活动/终态。
2. **重复状态机/端口/API**：chat 与 runner、component 两套 turn API、gateway 多份取消终态化。
3. **业务错误被静默替换**：历史读失败当空历史、清理失败当 0 条、资产/结果读失败当空。
4. **兼容/兜底仍可达**：`_async/_timeout`、`awaiting/tools.wait`、隔离池回落共享连接、
   chat 可选端口、router Legacy 重试与保留名 `echo`。

这些不是风格问题，已经能造成：取消被报成失败、消息写错会话、终态被覆盖、
隔离调用跑到共享连接、异步结果被截断、SSE 损坏被当成功。

---

## P0：正确性 / 原子性 / 静默丢错

### 1. turn 终态有 5 套词表、3 个写入口

- chat：`completed/interrupted/failed/canceled`（`chat/src/turnstate.go:21-39`）。
- runner：`complete/incomplete/error`（`session/src/runner/terminal.go:55-95`）。
- component `CloseTurn`：`completed/aborted/interrupted`（`session/src/component/turn.go:15-20,235-242`）。
- component `TurnComplete`：`running/done/complete/incomplete/error/interrupted/cancelled`
  （`session/src/component/turn_ops.go:14-18`）。
- 数据面：`running/done/complete/incomplete/error/interrupted/cancelled`
  （`chonkpilot/src/lib/data/internal/session/validate.go:34-40`）。
- 结果：`CloseTurn("incomplete")` 非法，`TurnComplete("completed")` 也非法；
  chat 的 `completed` 与 runner/数据面的 `complete` 不是同一值，`canceled`/`cancelled` 混用。
- 修法：在 `types/src` 定义唯一 `TurnStatus` 常量与终态白名单；chat/runner/component/宿主统一引用。
  持久化只保留一个写入口。

### 2. 取消在流式阶段被报成 failed，且取消时仍发“可续写中断”

- `consumeStep` 把 `ctx.Err()` 写进 `done.Err`（`chat/src/engine.go:1210-1220`）。
- 流式路径有文本时把 `done.Err` 当“断流中断”（`:483-490`）；AutoContinue 关闭后
  返回错误 → `fail()` 落 `failed`（`:545-547,1292-1299`）。
- AutoContinue 开启时先发 `session_interrupted{retryable:true}`（`:521-533`），
  即使这是用户主动取消。
- 等待回执/宽限期路径则落 `canceled`（`:771-774,837-842`）。同一取消两种语义。
- 修法：进入中断分支前先判 `ctx.Err()`；取消统一 `finish(WithoutCancel, Canceled)`，
  不发 `session_interrupted`。

### 3. `waitInboxItem` 超时/取消会丢掉已从队列取出的输入

- Wait goroutine 与 timer/ctx 并列 select（`chat/src/engine.go:974-1004`）。
- timer 或 ctx 先命中时，Wait 可能已从 FIFO 弹出 item 并写入缓冲 channel，但函数直接返回，
  item 再无消费者 → 用户插话丢失。
- 修法：用 `context.WithTimeout` 同步 `Wait`；或超时后从 `ch` 非阻塞取回并回填。

### 4. `Composer.Build` 吞历史读库错误，`Result.Err` 是死契约

- `h, _ := c.History.Load(ctx)`（`session/src/compose.go:49-51`），读失败按空历史继续请求 LLM。
- `Result.Err` 定义为“硬错误，调用方必须走错误终态”（`session/src/types.go:104-108`），
  但全包无任何赋值点。
- 修法：`Build` 返回 `(Result, error)`，或填 `Result.Err` 并让宿主强制检查。

### 5. `CleanupStale` 吞查询/更新错误，并把候选当成功条数

- `Query`/`UpdateIn` 失败均不返回错误；行不存在时 `UpdateIn` 也返回 nil，仍 `n++`
  （`session/src/component/maintenance.go:56-82`）。
- 启动恢复失败被伪装成“清理 0 条”，running turn 永远不会被收敛。
- 修法：查询/更新错误上抛；回调内记录 found，found 才计数。

### 6. `EnsureSession` 绕过“主会话唯一”和父会话校验

- `EnsureSession` 只校验非空并插入（`session/src/component/session.go:110-141`），
  不检查 `parent_id==""` 是否已存在，也不检查父会话存在。
- `CreateSession` 有 `E_MAIN_LIMIT` 与父校验（`component/dialog.go:366-412`），两条入口不一致。
- 结果可造出多个根会话；`mainSession` 取第一个，删除/改名行为随扫描顺序漂移。
- 修法：同一事务内校验；抽公共 `insertSessionRow(tx, ...)`。

### 7. open/close turn 先查后写跨事务，终态可被覆盖

- `OpenTurn` 先 `findOpenTurn` 再 `Insert`（`session/src/component/turn.go:78-95`），
  并发可插入两个 running turn。
- `closeTurn` 先在事务外读 `status != running`（`:149-162`），事务内重新读到 cur 后
  **不再校验** `status`，可把 `interrupted` 覆盖成 `completed`，并双发事件（`:164-192`）。
- 修法：扫描+插入、读状态+写终态都放进同一个 `Update` 事务。

### 8. `TurnComplete` 行缺失返回成功，且允许终态改回 running

- 行不存在时回调 `return nil`（`session/src/component/turn_ops.go:84-100`）；测试固化
  `TurnID:"ghost"` 成功（`turn_ops_test.go:108`）。
- 白名单包含 `running`，可把 `complete → running` 重开（测试 `turn_ops_test.go:96` 固化）。
- 修法：未找到返回 `E_TURN_NOT_FOUND`；终态写入只接受终态集合；重开另设显式 API。

### 9. inbox 持久化层不执行 lane/状态规则，且可跨会话写消息

- `session/inbox` 的 `IsDurableType/Status*/Consumable` 只在测试使用
  （`session/src/inbox/inbox.go:52-82`）。
- `InboxAppend` 接受任意 `Type`，包括 ephemeral 的 `tool_result/notify/control/runtime`
  （`component/inbox.go:71-160`）。
- `InboxCommit` 允许调用方传 `SessionID`，把 A 会话 inbox 的消息写成 B 会话
  （`component/inbox.go:249-274`）。
- `InboxUpdate` 可任意改 `Status/Type`，把 consumed 改回 pending（`:308-343`）。
- 修法：Append 校验 durable 类型；Commit 只从 inbox 行取 `session_id` 并校验 turn；
  Update 做状态迁移白名单；component 引用 `inbox.Status*` 常量。

### 10. fork context 把快照读失败当“无快照”

- `snapErr != nil` 时置 `hasSnap=false`（`session/src/component/fork.go:283-288`），
  派生成功但上下文为空。
- 修法：读失败返回错误并清理半成品；只有 `err==nil && !has` 才不复制。

### 11. gateway 取消权威分裂，`call_id` 冲突后缀导致取消错对象

- `CancelExec` 会问层权威状态，`CancelTurn` 不问（`gateway/src/mcpgateway.go:703-733`）。
- `provider.registerCall` 冲突时登记 `base#2`（`gateway/src/provider.go:1374-1381`），
  但 `ExecTask.CallID` 保存基名；取消按基名查找，可能取消另一个调用，或找不到而静默返回成功
  （`provider.go:1442-1447`）。
- 修法：唯一 call id；保存实际登记 id；`CancelTurn` 也走同一层校验。

### 12. 取消阶梯失败只记日志，对外仍报“已取消”

- `CallCanceller.CancelCall` 返回错误只 `logf`（`gateway/src/mcpgateway.go:1328-1332`）；
  同函数 `Terminate` 分支会置 failed、通知、上报 error（`:1334-1350`）。
- 任务终态已先改为 cancelled 并回报，用户看不到底层仍在跑。
- 修法：取消失败与 Terminate 失败同口径；或在回报 cancelled 前确认阶梯结果。

### 13. 异步结果经 `mcp-tasks-report` 只保留首段文本

- `reportTaskDone` 只取第一个非空 `TextContent`（`gateway/src/mcpgateway.go:407-415`），
  丢弃 `structuredContent`、多段 text、image/resource；同步路径却完整保留（`:1460-1506`）。
- 修法：report 携带完整 content + structuredContent；同步/异步结果同形。

### 14. 隔离池建连失败静默回落共享连接

- `slotFor`/`callSlotFor` 在建连/握手失败、无 work_dir、closed/closing 时返回共享槽
  （`gateway/src/provider.go:941-971,1039-1056`）。
- 共享槽在 `workdir=""` 下构建，Low IL 可写目录与 `_meta.sandbox` 均缺失；
  隔离请求实际跑在错误隔离域，调用方收到成功。
- 修法：隔离/pool_key 模式建连失败一律报错；无 work_dir 视为参数错误。

### 15. `tools/register` 非法 schema 被吞

- `json.Unmarshal` 错误被 `_ =` 丢弃（`gateway/src/mcpgateway.go:1704-1711`），
  schema 被补成 `{"type":"object"}`，工具仍注册成功。
- 修法：检查错误并返回 `-32602`；补非法 schema 测试。

### 16. provider 健康状态双源，`status` 无锁并发读写

- `providerState.status` 与 `breaker.state` 双轨（`gateway/src/registry.go:107-115`）；
  `servers/list` 显示 connected，`tools/call` 可能因熔断失败。
- 多处读 `ps.status` 不在锁内（`mcpgateway.go:581,1684-1685,1959,1973,1980,2019`；
  `primitives.go:40`）→ data race。
- 修法：状态访问统一加锁/快照；对外健康由 breaker 派生或与 status 合并。

### 17. router：SSE 流内损坏 JSON 被无条件跳过

- OpenAI/Responses/Anthropic 对 `data:` 载荷 `json.Unmarshal` 失败一律 `return nil`
  （`router/src/internal/adaptor/openai/openai.go:105-108`；
  `openai/responses.go:125-128`；`anthropic/anthropic.go:100-103`）。
- 上游截断/损坏的事件被吞；若随后有 `[DONE]/message_stop/response.completed`，
  会静默走成功路径，产出空/半截回复。
- 修法：仅对首字符不是 `{`/`[` 的心跳跳过；JSON 起始符解析失败 → protocol 错误。

### 18. router：空工具调用被当完整调用投出

- `toolcall.Complete()` 只校验参数 JSON（`router/src/internal/adaptor/toolcall.go:89-113`），
  允许 `ID="" Name=""` 的调用通过，上层收到不可执行调用。
- 修法：`Name` 必须非空（ID 按协议要求），否则 protocol 错误。

### 19. router：保留名 `echo` 未被拒绝，用户配置被内置兜底覆盖

- `Register`/`Reconcile` 接受名为 `echo` 的普通 provider，`Call` 对非空 `echo`
  仍切到 `builtinFallback`（`router/src/router.go:24-28,86-95,139-143`）。
- 用户配置永远不可达，`Reconcile` 还把它计入 Added/Unchanged。
- 修法：`normalizeSpec` 拒绝保留名；或删除非空 `echo` 覆盖分支。

### 20. `TurnEventAppend` 幂等是“先查后插”，清理注释与实现不符

- `Get` 后 `Insert`（`session/src/component/turnevent.go:57-84`），并发同 `(turn,seq)`
  后者拿到 `ErrExists` → 返回 `E_BACKEND`，与“幂等”注释相反。
- `TurnEventCleanup` 扫描/删除分属两个事务（`:103-123`），注释“单事务批删”不成立；
  `ErrNotFound` 也计入 deleted。
- 修法：Append 放进 `Update`；Cleanup 同一事务内扫描+删除。

---

## P1：重复实现 / 兼容兜底 / 死代码

### chat

- `session/runner` 暴露 `Input/InputKind/TurnRequest/Hooks/TurnStore/EventSink/ToolInvoker/TurnResult/TurnExecutor`
  （`session/src/runner/runner.go:62-129`）无任何包外调用方；仅 `Key/Registry/Frames/Lifecycle/
  DecideFollowUp/DecideTerminal/AutoContinue` 被宿主使用。应删除未用契约。
- chat 可选端口兜底：`PendingToolStore` 未实现则“只进内存不报错”
  （`chat/src/ports.go:37-45`）；`InboxWaiter` 未实现则 250ms 轮询
  （`:65-70`，`engine.go:843,857-869`）。生产已实现两者，应改为必选端口。
- `Replay`/`ShadowTurnStore`/`Recorder` 仅测试使用；`turn_events` 生产只写不重放。
  要么接入恢复，要么删除这套“可重放”契约。
- 不可达状态：`TurnStatusInterrupted`、`TurnEventCancel`、`TurnState.ResumeNote`、
  `AwaitingUser` 分支（`chat/src/turnstate.go:25-29,53,68,95,180-181,251-259`）。
- `TurnResult` 无状态字段，挂起用 `Finish:"pending"` 表达（`chat/src/lifecycle.go:76-85`）；
  runner 会把 `pending` 归为 complete。
- AutoContinue 默认值与决策两套：chat `Max<=0→1 / delay<=0→600s`
  （`engine.go:36-42,161-169`）；runner `3 / 1ns`（`runner/autocontinue.go:6,25-42`）。
- 两套消息 DTO 与有损转换：`types.Message/ToolCall` 与 `session/store.ChatMsg`；
  `datamsg.go:15-57` 丢掉 Message 身份与 ToolCall.Context；组装主路径反复互转
  （`project.go:42,58,67,123`，`tokens.go:70,77`）。

### session / component

- component `OpenTurn/CloseTurn/Finish/Abort/TurnComplete/TurnSetSummary` 生产零调用，
  仅测试使用；与宿主 `facade.TurnEnsure/TurnComplete` 重复。应删除或合并。
- 旧三段压缩 API `Due/PromptMessages/SnapshotMessages/Commit/CompressOnce`
  生产零调用，仅测试（`session/src/compress.go:47-128,187-205`）；
  `ModeZones=""` 旧投影仅测试可达（`session/src/compose.go:20-28,60-70`）。
- 任务活动/终态不一致：`TaskStateInterrupted` 注释称终态，`TerminalState` 不含它
  （`session/src/tasks.go:47-54,136-139`）；`ActiveBySession` 不算活动（`:342-357`），
  但 `Start` 的活跃数用 `!TerminalState`（`:293-298`），interrupted 算活跃；
  `CleanupTerminal` 永不清理 interrupted（`:771-794`）。
- `store.Memory.Upsert` 自动补 `updated_at`（`session/src/store/memory.go:181-187`），
  生产 Store 没有该约定，测试掩盖字段丢失。
- `mustID` 把 ID 生成错误折叠为空串（`component/dialog.go:501-508`）；
  `OpenTurn` 可用空主键落库（`turn.go:83-93`）；`newTaskID` 忽略 rand 错误
  （`session/src/tasks.go:835-839`）。
- 兼容读侧：`history.go:70-78` 不存在的 before_turn 静默返回空页；
  `group.go:254-281` 非 JSON 字符串回落单元素；`matchInstance` 空 instance 不校验
  （`tasks.go:200-202`）。
- 双套 session 行写入（`dialog.go:452-478`、`session.go:146-159`、`fork.go:289-293`）
  与两套删对话路径（`dialog.go:307-335` vs `session.go:343-355`，后者不发事件）；
  子会话级联不删 tasktree/turn_events。
- `TurnEventAppend`/`Cleanup` 的并发与注释问题（见 P0-20）。

### gateway

- legacy `awaiting` / `tools/wait` 整条链生产不可达：
  `enterAwaitingDecision`/`awaitingResult` 零调用（`tasks.go:496-523`，
  `mcpgateway.go:1443-1458`），`tools/wait` 恒 error。应删除。
- `_async/_timeout` 兼容键仍读取并剥离（`mcpgateway.go:852-858`）；
  顶层 `url/transport/namespace` 与 `mcp_server` 双源（`:1588-1590,1624-1629,1658-1676`）。
- `self_` 前缀回退仍生效（`tooloverride.go:210-227`），需先迁移 DB 再删。**〔已删除：gateway 与宿主均改为精确匹配〕**
- instance 空键/跨桶 `CancelTurn`（`tasks.go:143-147,709-727`）；池键只有 workdir，
  `connSlot.instance` 只写不读。
- `servers.list` 解析字段不消费：`PoolSize/Writable/KillPolicy/TimeoutSec/Enabled`
  （`serverslist.go:104-105,134-137,154-160,312,337`）。
- 多份取消终态化（`tasks.go:566-605,626-661,709-760,766-816`）、detach 双发
  `detached`（`mcpgateway.go:693,1177-1180,1202-1204`）、`doCall` 四模式重复
  （`:1020-1070`）、`monitorSpawned` 重复 failed 收尾（`:1954-1977,2045-2052`）。
- `registerTool` 允许既无 handler 也无 subject（`mcpgateway.go:1739-1758`）；
  `Async` 注册值未归一（`:1725-1727`）。
- 方法面两张表 + 未知方法返回 `(nil,nil)`（`mcpgateway.go:314-323,441-470`）。
- `subjects.go:124-140` 忽略 marshal 错误；`resultJSON` 零调用。
- `assetContentBytes` 读盘失败返回 `(nil,nil)`（`registry.go:146-158`）；
  资产 list 与 get 未接线（`primitives.go:99-157,180-233`）。

### router

- Legacy / Configured 两套重试（`router/src/retry.go:150-217`）；`RetryWait` 第二套退避、
  生产零调用（`error.go:41-71`）。
- `Message.Kind`/wire `kind` 兼容直通，`Synthetic` 迁移未落地
  （`canon/types.go:36-37`，`openai/request.go:54-62,157-165`，`echo/echo.go:26-30,83-88`）。
- `Caps` 多份字面量、大量字段无读取（`canon/caps.go`；三协议 `Caps`）；
  `Capabilities("echo")` 返回零值。
- `ExtraParams` 静默丢弃核心键/非法值（`adaptor/extra.go:19-53`）。
- 未声明 SSE 的嗅探与“忽略 stream 回单发 JSON”历史兼容（`exchange.go:7-9,77-116`）。
- 三份 `finishReason`、三份文本/图片拆分、两份空 schema/空参数（见路由审计明细）。
- `fixture.go` 测试素材加载器编译进生产包（`adaptor/fixture.go`）。
- `EvToolCallDelta`/`ToolCallDelta` 无生产方、无消费方（`canon/types.go:221-244`）。
- `New` 用 `_ = registerAdapter(...)` 忽略错误（`router.go:58-62`）；
  `deps_test` 白名单与文档不一致；`MaxTokensParam` 仅测试可达。

### 跨仓（agent 契约迫使宿主重复）

- `CallContext.Purpose` 已作废但仍在共享 DTO（`types/src/types.go:45`）；
  宿主 `gateway.Context` 用 `display_name`。
- Anthropic thinking signature 由 router 产出，但 `StreamEvent/ChatMsg/data.Message`
  无签名字段，往返丢失（`router/.../anthropic.go:203-208`；宿主 `routercall.go:189-197`）。
- 宿主对空 baseUrl/空 model/未知协议做静默回落，可能把 A provider 的 key 发往全局端点
  （`chonkpilot/src/lib/llm/server/routercall.go:53-76,85-93`，`llmconfig.go:386-390`）。
  该问题在宿主仓，不在 agent，但由 agent 契约的严格性缺口放大。

---

## 建议修复顺序

### 批次 1：统一契约，先停止继续分叉

1. `types/src/types.go`：定义唯一 `TurnStatus`、`InboxType/Lane/Status`、`TaskActivity/Terminal` 常量。
2. chat / runner / component / gateway 全部引用；删除各自白名单与别名。
3. 明确权威：轮内状态归 chat；持久化行归 component；gateway 只拥有 exec。
4. 测试只允许引用常量，不允许裸字符串。

### 批次 2：原子性与吞错（P0 的 3-13、15-20）

1. component：OpenTurn/CloseTurn/TurnComplete/InboxCommit/InboxUpdate/TurnEvent 全部事务化。
2. session：`Build` 返回错误；`CleanupStale` 上抛；fork 快照失败上抛。
3. chat：取消统一；`waitInboxItem` 不丢项；`record` 先写日志再提交内存状态。
4. gateway：取消阶梯失败可见；call id 唯一；report 完整结果；schema 校验；隔离失败报错。
5. router：SSE 损坏 JSON 报 protocol；空工具调用报 protocol；保留名 echo 拒绝。

### 批次 3：删除兜底与死代码

1. runner 未用契约；component 死 turn API；旧三段压缩 API；`ModeZones`。
2. chat 可选端口与 Legacy 轮询；`Replay/Shadow` 要么接线要么删。
3. gateway `awaiting/tools.wait`、`_async/_timeout`、顶层 register 字段、不消费配置。
4. router Legacy 重试、`RetryWait`、`Kind/Synthetic`、`fixture` 生产化、死 Caps/事件。
5. 删除空目录 `sandbox/`、`internal/testbus/`。

### 批次 4：去重与手法统一

1. gateway `doCall` 四模式、取消终态化、failed 收尾、排队原语、方法表。
2. router 共享 parts/schema/args/finish helper。
3. session session-row 写入、删除路径、任务活动判定、时间/ID 生成。
4. 统一错误包装（`%w`/`CodedError`）与日志口径。

### 批次 5：前端契约

前端不在本仓。需要另开一轮审 `E:\BizWorks\chonkpilot\src\frontend`，重点核对：
turn 终态词表、`turn_complete`/`session-interrupted` 字段、任务树 `state`、
`llm-retry`/`llm-usage`/`mcp-tasks-report` 载荷。当前不建议在词表统一前改前端。

---

## 执行结果（批次 1-3，2026-11-18 收口）

> 本节记录审计后的实际处置，不是审计时的原始判断。原 P0/P1 描述保留作对照。

### 已修复（P0 全部 20 项）

| 项 | 处置 |
|---|---|
| P0-1 turn 终态词表 | 新增 `types/src/contracts.go`：`TurnStatus`/`InboxType`/`InboxLane`/`InboxStatus`/`TaskState` 唯一常量与终态/活动判定；chat、session、component、runner、gateway、宿主全部引用，删除各自白名单。终态统一为 `complete/incomplete/error/interrupted`，`canceled/cancelled/completed/aborted` 不再写入。 |
| P0-2 取消被报 failed | chat 在流式与等待路径先判 `ctx.Err()`；用户取消统一 `TurnStatusInterrupted` / `finish:"interrupted"`，不再发可续写中断。 |
| P0-3 `waitInboxItem` 丢项 | Inbox 端口新增 `Requeue`；ctx 取消竞态下把已出队未消费项放回队首；timer 命中仍等待 Wait goroutine 返回。 |
| P0-4 Composer 吞历史错误 | `Build` 在历史读取失败时返回 `Result.Err`；宿主 `assembly.go` 对 `full/simplified/compacted` 三路均强制检查。 |
| P0-5 `CleanupStale` 吞错 | 查询/更新错误上抛；仅 `found` 才计数，行缺失不再伪装成成功。 |
| P0-6 `EnsureSession` 绕过唯一性 | 同一事务内校验根会话唯一（`E_MAIN_LIMIT`）与父会话存在。 |
| P0-7/8 turn 事务与幽灵终态 | component 的 `OpenTurn/CloseTurn/Finish/Abort/TurnComplete` 及其文件删除（生产零调用、与数据层重复）；持久化收敛到宿主数据层单入口。 |
| P0-9 inbox 规则 | `InboxAppend` 拒绝非 durable 类型；`InboxCommit` 只从 inbox 行取 `session_id` 并校验 turn/会话；`InboxUpdate` 仅允许 pending→consumed/discarded；component 引用统一状态常量。 |
| P0-10 fork 快照错误 | 快照读失败直接返回并清理半成品；只有“确认无快照”才继续。 |
| P0-11 gateway call_id | `CallWithIDProvider` 回写实际登记 id；取消/查找使用实际 id；`CancelTurn` 与 `CancelExec` 同走层权威校验。 |
| P0-12 取消阶梯失败 | 阶梯失败不再只记日志；层权威终态与失败原因可见，`Terminate` 与 `CancelCall` 同口径。 |
| P0-13 异步结果截断 | `mcp-tasks-report` 携带完整 `content`/`structuredContent`/`isError`/`resultType`，与同步结果同形；61 schema 与消息一览同步更新。 |
| P0-14 隔离回落 | 无 `work_dir`、建连/握手失败、键关闭/关闭中一律返回错误，不再回落共享槽。 |
| P0-15 非法 schema | `tools/register` 的 schema `Unmarshal` 错误返回 `-32602`，不再补空 schema 后注册。 |
| P0-16 状态双源/竞态 | 提供方状态访问统一持锁快照；`replaceProvider` 原子替换路由，reload 失败保留旧路由并对外报错。 |
| P0-17 SSE 损坏 | 仅心跳/非 JSON 载荷跳过；JSON 起始载荷解析失败 → `protocol` 错误。三类流式适配器一致。 |
| P0-18 空工具调用 | `Name` 为空 → `protocol` 错误，不再投出不可执行调用。 |
| P0-19 保留名 echo | `Register/normalizeSpec` 拒绝注册 `echo`；显式 `echo` 调用仍解析到内置兜底（保留既有语义），宿主不再注册该名。 |
| P0-20 TurnEvent 并发/清理 | Append 放入单个 `Update` 事务；Cleanup 同事务扫描+删除；`ErrNotFound` 不计数。 |

### 已删除的重复/兜底/死代码（批次 3）

- session：runner 未用契约（`Input/TurnRequest/Hooks/TurnStore/EventSink/ToolInvoker/TurnResult/TurnExecutor`）、
  component 旧 turn API、`turn_ops.go`、旧三段压缩 API、`ModeZones` 投影、`store.Memory.Upsert` 自动补时间。
- chat：`InboxWaiter`、`PendingToolStore`、`PollInterval`、`TurnEventCancel`、`ResumeNote`、
  `AwaitingUser` 载荷路径；`Inbox`/`TurnStore` 改为必选端口。
- gateway：`_async/_timeout` 读取、顶层 `url/transport/namespace` 注册字段、`tools/wait` 之外的
  `awaitingResult` 死函数、`servers.list` 的 `PoolSize/KillPolicy/Writable`、未消费的 reload 状态等。
- 工具名 `self_` 回退：删除 gateway `asyncOverride`、宿主 `toolNameIn`/`canon`、
  mcp-server `toolAsyncOverride` 的前缀兼容；白名单 / `tool_async` 键 / 任务节点标题
  统一按当前暴露名（= 原名）精确匹配，相关测试与 21/26/27/36/61/64 文档同步。
- router：Legacy 重试与 `RetryWait`、`fixture` 生产包迁移至 `testutil`、未使用的
  `EvToolCallDelta/ToolCallDelta/ToolCall`、多份 Caps 字面量收敛为 `CapsFor` 表。
- 插件身份单一来源：SDK `Options` 只收 `Manifest`，id/name/version 仅从嵌入 manifest 派生；
  `hello` 只带 `id/features/manifest`；删除 `Options.ID`、顶层 `name/version`、`CHONKPILOT_PLUGIN_ID`；
  12 个插件与 demo-python 同步。

### 原「保留」6 项已全部处理（2026-11-18 后续批次）

1. **`tools/wait` / awaiting 裁决链**：已从 gateway 方法表、执行池、任务状态、宿主 `ExecSink`、数据面
   `awaiting` 视图、前端裁决条/`useAwaitingArbitration`/`clearAwaiting` 及测试中整体删除；
   `mcp-tools-wait` 主题从 61 schema 与消息一览移除，msgkeys 已重新生成。
2. **空 instance 键**：gateway `spawn/locate`、session `matchInstance`/`NodeIn`/`FindByGWTask`/
   `CancelByTurn` 等及宿主任务查询改为**精确归属**（空只命中空归属，不再全桶回退）；测试与调用方
   显式携带 `instance_id`。宿主 `runner.Key` 已返回 `<instance> <id>`。
3. **工具模式旧别名 `never/always`**：gateway、mcp-server、宿主装配/调用级与前端配置只接受
   `sync|async|auto|manual`；旧别名不再归一，非法值明确报错/回落 auto。
4. **旧 `mcps[].sandbox` / 旧类别形状**：gateway `ServerEntry.Sandbox`、`mergeLegacySandboxMCP`、
   宿主数据 DTO/wire 字段、默认配置与 UI 写入口全部删除；`ParseToolSandbox` 只接受
   `executors`/`mcps`，旧形状 `{"core":...}` 明确报错；前端 `parseToolSandboxMap` 同步删除迁移。
5. **router 非声明式 SSE 嗅探**：删除 `looksLikeSSE`/probe；未声明 `text/event-stream` 的响应一律按
   单发 JSON 处理，测试桩需显式声明 SSE。宿主 llm 黑盒 mock 已补 `Content-Type`。
6. **chat `Replay`/`ShadowTurnStore`/`Recorder`**：`replay.go`/`trace.go` 及对应测试、`Deps.Shadow`、
   `Shadow.RecordStep`、chatest/engine 的 Replay 用例删除；`fakeTurnStore` 迁入独立测试文件。

补充收口：session `mustID/newTaskID` 错误传播、`history.BeforeTurnID` 未命中返回
`CodeTurnNotFound`、`assetContentBytes` 读失败不再返回空内容、gateway `tools/register` 强制
`handler_subject` 且校验 async 模式、宿主任务查询测试显式 instance 归属。

### 验证（修复后）

- `go vet ./...`：agent 5 模块 + 宿主 `src/lib/llm` 全绿。
- `go test ./...`：agent `types/chat/session/router/gateway` 全绿；chat/gateway `-race` 全绿。
- 宿主：`src/lib/data`、`src/lib/llm`（246s）、`src/lib/gui`、`src/lib/assembly`、`gwadapter`、
  `mcp-server`、`task` 全绿；`src/test` 下 mcp-gateway、mcp-server、llm、data、gui、lib 测试模块全绿。
- 前端：854 项中 852 通过；2 项为既有失败（`64-配置项一览` 文档源码引用、
  `layoutAdapter` 诊断入口），与本轮 agent 改动无关。
- 插件：`build-plugins.ps1` 12 个 staging 产物构建成功。`dist/plugins` 部署在
  `browser/chonkpilot-plugin-browser.exe` 处被正在运行的宿主占用而中断；**关闭宿主后重跑
  `.\build-plugins.ps1` 完成部署**。当前 `dist` 中 web/codegraph 已替换，其余仍为旧产物，
  在完成部署前不要用新宿主启动旧插件。

### 未纳入本轮（批次 4-5）

- gateway `doCall` 四模式/取消终态化/方法表去重、router parts/schema/args 共享 helper、
  session 行写入/删除路径/ID 生成去重（批次 4）。
- 宿主对空 baseUrl/空 model/未知协议的静默回落（`routercall.go`/`llmconfig.go`），以及
  Anthropic thinking signature 往返丢失（宿主 + router 契约，批次 5）。
- 前端终态词表、`turn_complete`/`session-interrupted` 字段、任务树 state 对账（批次 5）。