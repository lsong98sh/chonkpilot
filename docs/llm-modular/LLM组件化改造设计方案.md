# LLM 组件化改造设计方案（独立稿）

> 状态：✅ **已完成（2026-10-21 收口；A4-P4b 删除旧执行路径，引擎成为唯一执行路径）**
> 日期：2026-10-19（定案）· 2026-10-20（开工）· 2026-10-21（收口）
> 关联（spec）：[49-待实现项 §2](../spec/40-roadmap/49-待实现项.md)（登记项 `MOD-1`）· [2D-router](../spec/20-modules/2D-router.md) · [21-llm-server](../spec/20-modules/21-llm-server.md) · [61-消息一览](../spec/60-reference/61-消息一览.md) · [71-编程规范 §3.3](../spec/70-conventions/71-编程规范.md)
> 前置：**`RETRY-2` 先落**（重试归属决定 `LLMClient` 端口形状与 turn 循环结构）
> 代码目录：`src/lib/llm/server/`（端口化 + 状态可序列化）· `src/lib/router/`（保持纯 lib）· 新增 `src/lib/llm/chat/`（独立 module，暂定名 `chonkpilot-chat`）
>
> **本档不修改 `docs/spec/` 任何现有文档**（spec 同步列入 §7 落地清单）。

---

## 0. 总纲：两条本质要求（高于其余所有条目）

> 本次改造只认两条。任何设计取舍与这两条冲突时，以这两条为准。

### 0.1 完美组件化的判定标准（可验收）

「完美组件」不是主观评价，而是下面 12 条**逐条可机器验证**的清单：

| # | 标准 | 验收方式 | 当前 |
|:--:|------|----------|:--:|
| C1 | **零 MQ**：包级依赖图无 `mq` / `msgkeys` | `go list -deps` 断言（CI） | ✅ |
| C2 | **零环境依赖**（router / chat 严格；gateway 的执行能力须经宿主注入的 `Executor`/`Runner`，不得自行 `os/exec`） | import 断言 + 构造注入 | ✅（router / chat；gateway 执行能力经宿主） |
| C3 | **零共享包**：三组件两两无 import，无公共契约包 | import 断言（L3） | ✅ |
| C4 | **消费方定义端口**：接口声明在消费方，实现在宿主 | 代码审查 + 接口位置检查 | ✅ |
| C5 | **类型安全**：端口签名无 `any` / `map[string]any` | 静态扫描（`chat/ports_ci_test.go`） | ✅（chat 端口签名扫描通过；类型里的 `_meta map[string]any` 保留，不进端口） |
| C6 | **可独立编译**：单独 `go build` / `go test` 不拉宿主 | 独立 module CI | ✅ |
| C7 | **可独立发布**：不要求使用者引入其他组件或共享包 | module 依赖图检查 | ✅ |
| C8 | **状态可序列化**：`TurnState` JSON 往返无损 | 快照往返测试 | ✅ |
| C9 | **可重放**：同事件序列 → 同 `StepRequest` + 同写入序列 | 重放等价测试（硬判据） | ✅ |
| C10 | **可恢复**：非终态可收口；ask **已答不丢**（答案即 tool result，回填即落库）；未答置 `expired`；帧栈可恢复 | 崩溃重启 L2 | ⚠️（收敛 / ask 已落；帧栈「继续」待生产切换） |
| C11 | **可扩展**：非核心能力经 `LifecycleManager + Hook` 挂载，不进内核 | 扩展点测试 | ✅ |
| C12 | **行为等价**：现有 L1/L2/L4 全绿 | 回归测试 | ✅（生产路径未切换，既有套件全绿） |

**一句话**：**能被单独拿走、塞进任何宿主、且行为不变**，才算完美组件。C6 + C7 是最终判据，其余是它的必要条件。

### 0.2 现有优势功能（一条都不能丢）

这是本项目的差异化能力，改造只能增强、不能削减：

| # | 优势能力 | 关键语义 | 验收 |
|:--:|----------|----------|------|
| A1 | **插话帧栈** | LIFO 帧栈 · 深度 5 · `insert` / `next_turn` 相位分流 · `discard` 撤回 · `promote` 提升 · 外层结果 `stash` · 一次性续接说明 | 现有 `inbox_test.go` 全绿 + L2 |
| A2 | **受理即落库** | 用户输入/插话/ask 回答先落库再推进 | L2 崩溃注入 |
| A3 | **双层重试** | router 透明重发 + 可见重试（含断连/连接分流） | RETRY-2 + L1 |
| A4 | **自动续写** | `Emitted>0` 中断 / `length` → 中断后 10 分钟宽限期、用户无操作才由后端直发「继续」，每次中断 1 次 | RETRY-2 + L2 |
| A5 | **ask_user** | 消息化 interrupt：卡片 / 回答回填 / 超时收口 / 任务节点 | L2 |
| A6 | **工具循环** | 单轮 20 次 · 异步任务回执 · 看门狗兜底 · 按 `tool_call_id` 幂等回填 | 现有工具链路测试 |
| A7 | **多协议 router** | openai chat / responses / anthropic / echo · SSE 流解析 | 现有 router 测试 |
| A8 | **流式事件** | `content` / `reasoning` / `tool_calls` / `done` / `err` | L2 |
| A9 | **双超时** | 首字节 120s + chunk 间隔 60s（可配） | L1 |
| A10 | **上下文压缩** | 保留轮次 / 快照 / token 预算 | 现有压缩测试 |
| A11 | **多形态宿主** | desktop / gui / browser / flash 同一套逻辑 | 各形态构建 + L4 |
| A12 | **消息契约** | 业务消息面**零变更**（基线 = `RETRY-2` 后的 61，已含 `session-interrupted`）；数据面新增 `data-inbox-commit` + `data-ask-pending`（§3，均须登记） | `audit:topics` + 61 §0 登记 |

**红线**：A1–A12 中任意一条在改造后行为退化，即视为改造失败，**不允许用「别的方式实现」作为降级理由**。

### 0.3 明确不追求（其他框架有、我们不要）

| 不追求 | 原因 |
|--------|------|
| LangGraph 式图编排 / 时间旅行全量回放 | 我们只需 turn 级重放，图模型会引入额外抽象 |
| Eino 式 Checkpoint 执行栈 | 我们的状态机是事件驱动，不需要执行栈快照 |
| 多 provider 智能路由 / 降级链 | 后续独立 scheduler，不塞进 router |
| 通用插件市场 / 动态加载 | 现有 filter chain 的扩展点已够用 |

### 0.4 变更治理（重构完成 + 测试全绿后生效）

> 用户 2026-10-20 拍板。

- 三个组件一旦达到 C1–C12 并测试全绿，**除 bug 修复外的任何修改**都必须**先向用户汇报**（与 `61-消息一览` 变更同等级）；
- 汇报必须说明：改什么 · 为什么 · **是否破坏组件边界**（C1–C12 任一条、端口签名、`go list -deps` 依赖图、模块拆分）；
- 未获确认**不得**单方面新增跨组件依赖 / 共享包 / 端口字段 / 环境依赖；
- 结论回写本档（§0.1 表 + 相关章节），保持与 `docs/spec/` 同批同步。

---

## 1. 问题与目标

### 1.1 现状

| 组件 | 现状 | 判定 |
|------|------|------|
| `chonkpilot-router` | 纯 lib：只 import 标准库 + `internal/*`，无 facade / MQ / DB；配置经 `Register`，事件经 channel | ✅ **已经是目标形态，不改内核** |
| `chonkpilot-mcp-gateway` | **内核 `gateway/` 依赖 `mq.Bus` 作为上游通道**（[mcpgateway.go](../../src/lib/gateway/gateway/mcpgateway.go)：`Bus` 必填、注释写明"上游通道 = 进程内内存 Bus，唯一通道"）；`meta_tools.go` / `subjects.go` 还 import `msgkeys`；`internal/facade` 为对外 MCP 外壳（依赖 mq） | ❌ **需 MQ 端口化** |
| `llm-server`（chat） | 直连 `mq.Bus`（`s.publish`）· 直连 `data-*`（`dataRequest`）· 内嵌 `data.ChatMsg` / `facade.InboxItem` 别名 · **直接 import `mcpgateway` 具体类型** | ❌ 宿主耦合 + 跨组件类型耦合 |
| turn 状态 | `pending` / `inflight` / `inserts` / `stash` / `resumeNote` 全在内存且就地修改（[turn.go](../../src/lib/llm/server/turn.go) `turnCtx`） | ❌ 不可序列化、不可重放、崩溃即丢 |
| ask 登记 | `askWaiter` 纯内存 map（[server.go](../../src/lib/llm/server/server.go)）；重启后回答被静默丢弃 | ❌ 数据丢失风险 |

**已有基础（不要重复造）**：`llm-server` 已有 RB-5 窄接口 `gwRuntime` / `execController` / `taskStateReader`（[ports.go](../../src/lib/llm/server/ports.go)），但签名仍渗出外部具体类型，且 `server.go` / `gateway_servers.go` / `gwclient.go` 仍直接引用 `mcpgateway.ServerEntry` / `Context` / `ExecSink` / `ExecState` / `Subject*` / `Origin*` / `Kind*` / `ToolAsyncOverride`。

**类型重复的现状（零共享方案下可接受，但字段契约必须一致）**：

| 类型 | 重复位置 |
|------|----------|
| `ExecState` | `gateway/gateway/tasks.go` 与 `task/exec.go` |
| `ToolAsyncOverride` | `gateway/gateway/tooloverride.go` 与 `mcp-server/server/config.go` |
| `Context`（工具调用上下文） | `gateway/gateway/subjects.go`、`llm/servertools/exec/exec.go`、`llm/dslrun/run.go` |
| `ServerEntry` | 仅 `gateway/gateway/serverslist.go`，但**被 llm-server 直接引用** |

> **重复本身不是问题**（零共享的必然代价）；真正的问题是 **llm-server 直接 import `mcpgateway.*`**，形成编译期耦合与版本绑定。

### 1.2 目标（可验收）

0. **零 MQ 依赖（最高优先级）**：三个组件的**包级依赖图**中不得出现 `chonkpilot-lib/mq` 与 `chonkpilot-lib/msgkeys`；组件内不出现 topic / payload / bus 概念，一切收发由宿主经端口实现。**验收 = `go list -deps` 断言 + L3 import 测试**。
1. **端口化**：`llm-server` 只依赖 4 组领域端口（`ContextProvider` / `TurnStore` / `EventSink` / `ToolInvoker`）+ `ToolCatalog` + `Clock` + 标准库 `*slog.Logger`，不直接 import `data` / `facade` / `mq` / `mcpgateway` 具体类型（在 RB-5 已有的 `gwRuntime` / `execController` / `taskStateReader` 上扩展，不重造）。
2. **零共享依赖**：三个组件之间**不共享任何包**；端口签名中的类型由**消费方（chat）**定义，gateway / router 保持自有类型，互转只在宿主装配层。
3. **类型自持**：chat 自有 `Message` / `InboxItem` / `Event`，`data` 与 MQ 的转换下沉到 adapter。
4. **状态可序列化**：`TurnState` 可 JSON 序列化；turn 内的状态变更走**输入事件 → 状态转移 → 输出事件**。
5. **可重放**：给定同一事件序列 + 同一 LLM 桩，**`StepRequest` 序列**与**落库写入序列**逐项等价。
6. **可恢复**：答案即 tool result（回填即落库）；重启后未回答的 ask 置灰、未知 `ask_id` 回执不静默；插入帧栈（栈结构 / stash / 续接说明）可恢复。
7. **拆包**：`chonkpilot-chat` 独立 module，仅依赖自身定义的端口类型，**不 import router / gateway 任何包**。
8. **零共享包**：chat ⇄ gateway ⇄ router 两两无 import，也不存在"必须共同引入"的契约包；三者各自可独立发布。
9. **行为零变化**：除第 6 项修复的数据丢失外，现有 L1/L2/L4 测试全部保持通过。
10. **契约不靠包，靠 spec + 测试**：跨进程/持久化的字段名与语义由 `docs/spec` 与契约测试保障（见 §3.2）。

### 1.3 非目标

- 不改重试策略（归 `RETRY-2`）。
- 不改**业务消息契约**（基线 = `RETRY-2` 完成后的 61，含 `session-interrupted`；本次 §1 / §2 / §4 零变更）。**唯一例外 = 数据面 §3 新增两条内部方法**：`data-inbox-commit`（`CommitInbox` 事务原子性）与 `data-ask-pending`（ask 启动扫描）；均须按 61 §0 登记并经用户确认。
- 不做智能调度 / 降级（归后续 `scheduler`，见 §6.3）。
- 不追求「逐 token 级」的续跑；恢复粒度 = turn 级。

---

## 2. 目标形态

```
┌──────────────┐   ┌──────────────┐   ┌──────────────────┐
│  router      │   │  gateway     │   │  chat            │
│  单次 step   │   │  工具执行     │   │  TurnState / 帧栈 │
│  纯 lib      │   │  纯 lib       │   │  独立 module     │
└──────▲───────┘   └──────▲───────┘   └────────▲─────────┘
       │                  │                    │
       │ router 适配器     │ gateway 适配器      │ 端口（由 chat 定义）
       └──────────────────┴────────────────────┘
                     宿主装配层（llm/server）
         · 实现 chat 的全部端口（内部调用 router / gateway）
         · 持有 data / mq / HTTP / OS / WebView
```

**三条铁律**：

1. 三个组件**两两无 import，且不共享任何包**——各自可独立发布；
2. 端口签名里的类型由**消费方（chat）**定义，gateway / router 保持自有类型，互转只在宿主；
3. 环境能力（data / HTTP / OS / WebView / **MQ**）只出现在**宿主装配层**；
4. **零 MQ（最高优先级）**：组件内核不得 import `chonkpilot-lib/mq` / `msgkeys`——gateway 的 `Bus` 改为 `Dispatcher` 端口，chat 的 `s.publish` / `dataRequest` 改为 `EventSink` / `TurnStore`。

### 2.1 实例模型（子会话 / 群聊 / DSL 子轮次都走同一路径）

**chat 不为「主会话 / 子会话 / 群聊」设计不同路径**。执行单元只有两个入口：

```go
rt := chat.New(deps)                 // deps = 端口集合（ContextProvider/TurnStore/EventSink/ToolInvoker/Clock/Logger）
rt.RunTurn(ctx, turnID)              // 会话内一轮（含帧栈 / 工具循环 / 续写）
rt.RunSubTurn(ctx, subTurnRequest)   // 无会话持久化的子轮次（DSL llm_run）
```

> 实例绑定一个 `sessionID`；群聊下每个参与者各有独立 `work_session`，因此**一个实例仍只对应一个 session**。

| 场景 | 宿主怎么做 | chat 侧 |
|---|---|---|
| 主会话 | 建一个 `Runtime` 实例，绑定主 session | `RunTurn` |
| **子会话（新窗口）** | **新建 session + 新建一个 `Runtime` 实例**，路径与主会话**完全相同** | `RunTurn` |
| **群聊** | 每个参与者 `work_session` 各一个 `Runtime` 实例；宿主调度器负责认领 / 收敛 | 各实例 `RunTurn` |
| **DSL `llm_run`** | 临时实例（不落会话、不发 UI 事件），跑完即弃 | `RunSubTurn` |

**实例规则**：

1. **一 session 一实例**（或一 turn 一实例，由宿主决定）——**chat 内部不排队**：`RunTurn` 可并发调用，实例间并发、同一实例内也可并发；同一 session 的并发 turn 由宿主做**单写者仲裁**（同一 session 的落库写串行化；冲突返回 `chat.ErrConflict`，由宿主决定重试或提示）；
2. 实例**不持有全局状态**：会话注册表、窗口、群聊名单、调度全在宿主；
3. 端口方法均带 `sessionID`，天然支持多实例共享同一组 adapter；
4. 实例可随时创建 / 销毁；崩溃恢复由宿主用同一 `sessionID` **重建实例**（`TurnState` 从 store 读）。

**结论**：chat 不认识「主会话 / 子会话 / 群聊」——它们只是宿主用不同方式**创建和管理实例**。

### 2.2 分层边界：router = 传输层，chat = 逻辑层

| | **router（传输层）** | **chat（逻辑层）** |
|---|---|---|
| 管什么 | 协议适配、SSE 流解析、连接/首字节/流超时、错误分类（`Kind` / `Phase` / `RetryAfter`）、**报文级重发**（同一请求原样重发、次数/预算/退避）、事件产出 | turn / step 状态机、消费 `ContextProvider` 给出的上下文、工具循环、插话帧栈、可见重试（`llm-retry` 上报）、续写、`EventSink` 编排、`TurnStore` 落库、`chat.Error` 对外错误 |
| 不管什么 | 业务语义、会话、用户可见状态、是否续写、工具执行 | 协议细节、连接重连、报文重发策略 |
| 类比 | **TCP**（含重传与 RTO 退避） | **应用层**（决定业务上要不要重来、要不要告诉用户） |

**一句话判据**：

> **同一份报文能不能原样再发一次** → router；**要不要让用户知道、要不要改变语义（续写 / 换模型 / 放弃）** → chat。

> 因此「报文级重试的次数与退避放在 router」并不违背分层——TCP 的重传与退避本就属于传输层；而「可见重试」「续写」属于应用层，仍在 chat。

**错误模型（chat 对外唯一错误类型）**：

```go
type Error struct {
    Kind      string // invalid_input / tool_failed / llm_failed / canceled / conflict
    Retryable bool
    Cause     error
}
```

- router 的 `canon.Error` 由宿主 adapter 映射为 `chat.Error`，chat 不 import router；
- 对外只暴露 `chat.Error`，宿主据此决定 UI 呈现与是否重试业务。

### 2.3 落地位置（目录与 module）

| 组件 / 层 | 目录 | module | 本次动作 |
|---|---|---|---|
| router | `src/lib/router/` | `chonkpilot-router` | **内核不改**（仅按 `RETRY-2` 扩展重试） |
| gateway | `src/lib/gateway/` | `chonkpilot-mcp-gateway` | 内核 `Bus` → `Dispatcher` 端口、去 `msgkeys`；`internal/facade` 仍为宿主适配外壳 |
| **chat（新）** | **`src/lib/chat/`** | **`chonkpilot-chat`（新建独立 module）** | 从 `src/lib/llm/server` 抽出的组件内核：`TurnState` / 帧栈 / 工具循环 / 4 组端口 / `chatest` |
| 宿主装配 | `src/lib/llm/server/`（**保留原名**） | `chonkpilot-llm` | 退化为装配层：实现端口 adapter → 拼装 router / gateway / chat；**`New` 与全部既有对外 API 不变**（gui / server / cli 入口零改动） |
| 类型互转 adapter | `src/lib/llm/server/`（沿用 `routerconv.go`） | `chonkpilot-llm` | gateway / router ↔ chat 的类型互转**统一在此**（`chonkpilot-assembly` 只管 gateway / mcp-server 装配，**不放 chat adapter**，避免两处实现） |

**约束**：

1. `chonkpilot-chat` 的 `go.mod` 只允许自身依赖 + 标准库（`go list -deps` 断言）；
2. `llm/server` 保留**包路径与导出符号**（`New` / 既有方法），确保 `src/desktop` / `src/server` / `src/lib/gui` 等消费方不改调用点；
3. `src/lib/llm/server` 内**不得**再出现 `turnCtx` 级状态机实现——同一逻辑只存在于 `chonkpilot-chat`。

---

## 3. 详细设计

### 3.1 端口（4 组 + `ToolCatalog` + `Clock` + 标准库日志）

> 起点是**现有 RB-5 窄接口**（`gwRuntime` / `execController` / `taskStateReader`），本次是「补全 + 去外部类型」，不是从零重造。

```go
// 上下文构建（宿主实现：读库 + 压缩 + system prompt 拼装）
// chat 不读库、不拼上下文、不压缩——只消费「已拼好的本轮上下文」
type ContextProvider interface {
    Build(ctx context.Context, req ContextRequest) (Context, error)
}
type ContextRequest struct {
    InstanceID string
    SessionID  string
    TurnID     string
}
type Context struct {
    SystemPrompt string    // 已拼好的 system（全局/目录/场景/agent/注入层）
    Messages     []Message // 已压缩的历史（快照 + 最近 N 轮）
}

// 写侧：消息归属按 sessionID；顺序与事务语义随端口声明
type TurnStore interface {
    SaveMessage(ctx context.Context, msg Message) error
    // 同一事务：落消息 + 置 inbox consumed（data 层新增 data-inbox-commit；幂等：已 consumed 直接返回）
    CommitInbox(ctx context.Context, inboxID string, msg Message) error
    // 事件日志（重放游标）；实现方可与 messages 同库不同表
    AppendEvent(ctx context.Context, ev TurnEvent) error
}

// 时钟（续写宽限期 / 超时 / 事件时间；测试可注入）
type Clock interface {
    Now() time.Time
    After(d time.Duration) <-chan time.Time
}

// 对外事件：只广播，不写库（消息落库走 TurnStore）
// 统一承载：tool_result / turn_complete / ask_user / llm_receive /
//           llm_retry / tool_progress / session_*；由 Event.Type 区分
type EventSink interface {
    Emit(ctx context.Context, ev Event) // 必须非阻塞
}

// 工具调用：只表达「发起」与「结果」，不引入 MCP task 协议类型
// 执行期间的阶段变化（preparing/running/…）经 EventSink（Type=tool_progress）输出，
// 不在 Dispatch 返回值里承载。
type ToolInvoker interface {
    // 统一入口：由实现方决定同步还是异步（chat 不做策略判断）
    Dispatch(ctx context.Context, call ToolCall) (ToolOutcome, error)
    Cancel(ctx context.Context, instanceID, opID string) (bool, error)
    Detach(ctx context.Context, instanceID, opID string) (string, error)
}

type ToolOutcome struct {
    Pending bool       // true = 稍后回填
    OpID    string     // Pending=true 时有效
    Result  ToolResult // Pending=false 时有效
}

// Pending=true 的回填通道（宿主 → chat，按 tool_call_id 幂等）：
//   chat.SubmitToolResult(ctx, toolCallID, result) error
// 宿主在工具真正完成时调用；崩溃恢复后，宿主重放「已产生但未投递」的结果。

// 目录视图（对应 gateway_servers.go 的用途）
// 返回 chat 自有类型（`ServerInfo` / `ToolSpec`），gateway `ServerEntry` 由宿主 adapter 转换
type ToolCatalog interface {
    Servers(ctx context.Context, instanceID string) ([]ServerInfo, error)
    Tools(ctx context.Context, instanceID string) ([]ToolSpec, error)
}

// gateway 侧（gateway 自己声明；宿主实现）—— 替代 mq.Bus
type Dispatcher interface {
    Call(ctx context.Context, op Op, payload []byte) ([]byte, error)
    Publish(ctx context.Context, op Op, payload []byte) error
}

// Op 是 gateway 自有的操作名（原 MQ 相对主题），不引用 msgkeys
type Op string
```

**关于 `Op` 的取舍**：保留一个字符串操作名（而不是为每个上游方法建 Go 方法）可以避免接口爆炸（tools/list、tools/call、servers/register… 数量很多）。它只是**路由键**，不是 MQ 主题——`Op → 实际主题` 的映射只在宿主 adapter；gateway 内核既不 import `mq` 也不 import `msgkeys`。

**工具协议约束**：

1. **不依赖 MCP task 协议类型**——`task/*`、`tasks/*`、`tasks-report` 等一律不出现在端口签名里；
2. 实现方可自由选择：标准 MCP `tools/call`（同步）、`goroutine` + 信号/回调、或异步 task，**均由宿主在实现内消化**；
3. **工具是否自动执行 / 异步策略 / 错误分类归 MCP Gateway**，chat 只负责发起与接收结果；
4. `ask_user` **不是 chat 的特例**：chat 只看到一次普通工具调用，"等待用户回答"由宿主实现（阻塞在 `Dispatch` 内，或返回 `Pending` 后回填）。现有 `isAskTool` 特判从 chat 内核移除。

**约束**：

1. `EventSink` 必须非阻塞（同 `RETRY-2` 的 `RetryObserver`）；**同一实例内按调用顺序投递**（实现可用有界有序队列，丢弃必须记录日志，禁止静默丢）。
2. `CommitInbox` 的原子性写进接口契约，不能拆成两个 hook（否则宿主实现错一次就产生重复窗口）。**实现 = data 层新增点分主题 `data-inbox-commit`**（单条消息内完成「写消息 + 置 consumed」，幂等），**须按 [61 §0](../../docs/spec/60-reference/61-消息一览.md) 登记**。
3. **端口签名禁止出现 `data.*` / `facade.*` / `mq.*` / `msgkeys.*` / `mcpgateway.*` / `tasklayer.*`**——只能用声明方自有的具体类型。
4. `ToolInvoker` 的实现方仍是 `*mcpgateway.Gateway`（或包一层的 adapter），但**只在装配层引用**。
5. **零 MQ 验收**：对**组件内核包路径**（`router`、`gateway/gateway`、`chat`/`chonkpilot-chat` 模块根）执行 `go list -deps`，断言依赖图中不含 `chonkpilot-lib/mq` / `msgkeys`；该断言固化进 CI（L3）。`gateway/internal/facade` 属宿主适配层，不在断言范围。

### 3.2 零共享：消费方定义类型 + 宿主适配

**原则**：组件之间**不共享任何包**。端口签名中的类型由**消费方（chat）**定义；gateway / router 保持自有内部类型；互转只在**宿主装配层**发生。

| chat 定义（消费方） | gateway / router 内部类型 | 转换位置 |
|---|---|---|
| `ToolCall` / `ToolResult` | gateway `CallReq` / `tools/call` 结果 | 宿主 adapter（`llm/server`） |
| `ToolSpec` | gateway `ServerEntry` + `tools/list` | 宿主 adapter（`llm/server`） |
| `ServerInfo`（MCP 服务列表展示） | gateway `ServerEntry`（注册表视图） | 宿主 adapter（`llm/server`） |
| `ToolProgress`（阶段/状态） | gateway `ExecState` / `ExecPhase` | 宿主 adapter（`llm/server`，事件订阅） |
| `CallContext`（InstanceID / Session / Turn / ToolCallID / Purpose / WorkDir / DataDir / TopSession / Parent） | gateway `Context` | 宿主 adapter（`llm/server`） |
| `StepRequest` / `StepEvent` | router `Request` / `Event` | 宿主 adapter（`llm/server`，沿用 `routerconv.go`） |

- **chat 自有**：`Message` / `InboxItem` / `Event` / `TurnState` / `ToolCall` / `ToolResult` / `ToolSpec` / `ServerInfo` / `ToolProgress` / `CallContext`。（**不含 `Snapshot`** —— 压缩快照归 `ContextProvider` / 宿主）
- **gateway 自有**：`Context` / `ServerEntry` / `ExecState` / `ToolAsyncOverride` / `Subject*` 等，**不对外暴露**。
- **adapter 必须放宿主**（`src/lib/llm/server`，沿用 `routerconv.go`；`chonkpilot-assembly` 不放 chat adapter），**禁止**放在 gateway 或 router——否则形成 `gateway → chat` 反向依赖。
- **端口签名禁止用 `any` / `map[string]any` 兜底**——那等于退回 MQ payload 模式、丢失编译期校验。签名必须使用**声明方的具体类型**，转换发生在实现方内部。
- **"类型不必一致"的准确含义**：不需要三方共用一套类型；接口签名固定为声明方（chat）的类型，实现方接收后转成自己的类型、返回前再转回来。**不是**"声明用 A 类型、实现用 B 类型"（Go 静态类型不允许）。

#### 载荷形态：含义一致，形状可不同

端口签名固定为声明方类型，但该类型的**载荷表示**可以是多种形态——由宿主按 `Kind` 判别后转换：

| 形态 | chat 侧载荷 | host 转换 |
|------|-------------|-----------|
| 结构化 | `StepRequest{Messages []Message, ...}` | 直接映射为 `router.Request` |
| JSON 信封 | `StepRequest{Kind:"json", Payload []byte}`（可经 plugin hook 改写） | 解码 → 校验 `SchemaVersion` → `router.Request` |
| 纯文本 | `StepRequest{Kind:"text", Text string}` | 包装为单条 user 消息 |
| 引用 | `StepRequest{Kind:"ref", Ref string}`（落库 ID / 块 ID） | 经 `TurnStore` 取内容 → 再按上述形态转换 |

**硬约束**：

1. **必须有判别字段**（`Kind` / `Type`），禁止靠字段是否存在去猜形态；
2. **必须带版本**（`SchemaVersion`），未知版本**显式失败**，不静默降级；
3. 每种形态都要有 **convertibility 测试**：同一 fixture 经 host 转换后，router / gateway 收到的请求逐字段符合预期；
4. **引用形态是「按引用传递」**：host 必须能访问 store，且 `Ref` 必须在有效期内（生命周期由 chat 保证，或显式带过期时间）；
5. 引用形态**不得跨进程**（ID 在另一进程无效），跨进程只允许 `json` / `text`。

> 组件化的目标是**语义契约稳定**，不是「类型相同」。类型相同只省一次转换，却把三方绑死；「含义一致 + 可转换」才是可独立发布的前提。

**依赖方向（单向，宿主是唯一同时 import 三方的包）**：

```
chat  ←── host ──→  router
           └─────→  gateway

gateway  ✗→ chat / router
router   ✗→ chat / gateway
```

**示例：chat 只声明抽象，host 实现并把结果转回来**

```go
// ---- chat 包内：只声明接口 + 自有类型 ----
type StepSender interface {
    Send(ctx context.Context, req StepRequest, onEvent func(StepEvent)) error
}

type ToolInvoker interface {
    Invoke(ctx context.Context, call ToolCall) (ToolResult, error)
}

// ---- host 包内：唯一同时 import chat 与 router/gateway ----
type stepSender struct{ rc *router.Client }

func (s *stepSender) Send(ctx context.Context, req chat.StepRequest, onEvent func(chat.StepEvent)) error {
    return s.rc.Call(ctx, toRouterRequest(req), func(ev router.Event) {
        onEvent(fromRouterEvent(ev)) // router 类型 → chat 类型
    })
}

type toolInvoker struct{ gw *gateway.Gateway }

func (t *toolInvoker) Invoke(ctx context.Context, call chat.ToolCall) (chat.ToolResult, error) {
    res, err := t.gw.Call(ctx, call.Name, call.Args, toGatewayContext(call.Context))
    return toChatResult(res), err
}
```

> **精确表述**：三者之间没有**共享包 / 共享类型**；唯一的"约定"是**消费方定义的接口签名**（单向、由实现方满足）。这就是 Ports & Adapters：`host` 是 composition root，也是唯一知道三方存在的地方。

**跨进程契约仍然必须约定**（Go 类型可各自定义，但字段名与语义必须一致）：

| 通道 | 承载 | 契约来源 |
|---|---|---|
| 环境变量 | `CHONKPILOT_INSTANCE / SESSION / TURN / TOOL_CALL_ID / PURPOSE / WORKDIR / DATADIR` | [61-消息一览](../spec/60-reference/61-消息一览.md) + 本节 |
| 进程间 JSON | `tools/call` 的 `context` 字段 | 同上 |
| 持久化行 | `messages` / `inbox_items` / `turn_events`（事件日志；**不建 ask 表**） | [21-llm-server](../spec/20-modules/21-llm-server.md) 数据模型 |

> **结论：可以没有共享库，但不能没有字段契约。** 共享包只是契约的一种载体；本方案改用「spec + 契约测试」承载——任一侧改字段名而另一侧未跟进，测试必须失败。

### 3.3 状态可序列化

```go
type TurnState struct {
    Version   int           `json:"version"`    // 快照结构版本（不兼容 → 收敛到终态并保留已落消息）
    SessionID string        `json:"session_id"`
    TurnID    string        `json:"turn_id"`
    Status    TurnStatus    `json:"status"`     // pending/running/awaiting_tool/...
    Seq       uint64        `json:"seq"`        // 事件序号（重放游标）
    Pending   []ToolCall    `json:"pending"`
    Inflight  []Inflight    `json:"inflight"`
    Frames    []FrameState  `json:"frames"`     // 插入帧栈（LIFO）
    Stash     []ToolResult  `json:"stash"`      // 外层工具结果暂存
    ResumeNote string       `json:"resume_note"`// 一次性续接说明
}
```

事件模型：

```go
type TurnEvent struct {
    Seq     uint64          `json:"seq"`
    Type    EventType       `json:"type"` // turn_start / step_start / step_done / llm_delta / llm_done /
                                          // tool_dispatch / tool_result / inbox_insert / inbox_consume /
                                          // continue / cancel / turn_end
    At      time.Time       `json:"at"`
    Payload json.RawMessage `json:"payload"`
}
```

**要求**：

- 所有状态变更只能发生在 `Apply(state, event)` 内，禁止就地修改；
- 事件必须有单调 `Seq`（并发回执、插话、取消的到达顺序必须显式化）；**`Seq` 作用域 = per-turn**，跨 turn 由 `TurnID` 区分；
- `FrameState` 含父帧引用与栈深，支持恢复时重建 LIFO 关系。

**事件日志分级（配置 `turnEventLog`）**：

| 级别 | 记录内容 | 用途 | 每轮量级 |
|---|---|---|---|
| `none` | 不写事件日志（仅终态 + `messages`） | 最省；**不可重放** | 0 |
| `minimal` | `turn_start` / `turn_end` | 只能收敛到终态 | 2 |
| **`outline`（默认）** | 状态转移：`turn_start` / `step_start` / `step_done` / `tool_dispatch` / `tool_result` / `inbox_insert` / `inbox_consume` / `continue` / `turn_end` | **满足重放判据** | 数十 |
| `verbose` | `outline` + `llm_delta`（每个流事件 / 增量） | 诊断、逐 token 回放 | 数千 |

- `M4` 重放验证固定以 `outline` 运行；`verbose` 仅用于诊断，不参与验收；
- `none` / `minimal` 下恢复只能收敛到终态，**不得声称支持重放**；
- 级别可在运行时切换（下个 turn 生效），已写入的事件不回溯。
- 配置项 `turnEventLog` 默认 `outline`，落在用户级个人偏好（概念上是系统级参数，因系统级无编辑入口而落用户级）。
- **保留与清理（71 §3.3 要求）**：终态 turn 的事件日志保留天数由用户配置 **`turnEventLogRetentionDays`**（默认 **7**，`0` = 永久）；`verbose` 同用该值；清理任务随既有 sweep 周期执行。
- **配置 UI**：`turnEventLog`（下拉 `none/minimal/outline/verbose`）与 `turnEventLogRetentionDays`（非负整数）放在「设置 → 参数 → **用户页签** → 会话」分组；需同步 `configIO` 白名单、校验、`SYSTEM_DEFAULTS` 与 `TestSystemDefaultParamsMirror`。

### 3.4 重放判据（验收硬标准）

给定：事件序列 `E[1..n]` + 确定性 LLM 桩（按请求返回固定流）。

断言三条**逐项相等**：

1. 发出的 `StepRequest` 序列（含模型、消息、工具集、重试次数）；
2. 对 `TurnStore` 的写入调用序列（含顺序与幂等键）；
3. 对 `EventSink` 的输出序列（UI 消息也是行为，不能漏比对）。

不满足 = 重放不可用，不允许进入拆包阶段。

### 3.5 恢复（本专题的实际收益）

**ask 回答不丢（不新增表）**：

- 答案的最终形态 = **tool result**；回填成功即落库 `messages`，已落库的答案重启后可直接重试（不重问）。
- **不新增 `asks` / `ask_answers` 表**：等待登记仍在内存；重启后按「任务节点 + 超时」收口。
- 未回答的 `ask_user` 任务 → 置 `expired`，前端卡片置灰；用户点「继续」时重新提问。
- **启动扫描（不建表的前提）**：新增数据面查询主题 **`data-ask-pending`**（查询「`tool_call.name = ask_user` 且无对应 tool result」的记录；**须按 [61 §0](../../docs/spec/60-reference/61-消息一览.md) 登记**）→ 按 `pending_task_timeout_sec` 超时后置 `expired`。
- 未知 `ask_id` 的回复**不再静默返回**，回一条「该问题已失效」。
- 已知残余窗口：回答到达 → tool result 落库之间崩溃，该题需重答一次（用户已确认接受）。
- **DSL 子轮次**：`denyTools` 硬拒 `ask_user`（无人值守，避免永久挂起）。

**帧栈恢复**：

- `TurnState` 快照 + 事件日志（粒度：每个事件一次或按批）；
- 启动：`Status` 非终态 → 重建 `TurnState` 继续；无法重建 → 收敛到终态并保留已落库消息；
- 恢复后**不重跑已完成工具**（`tool_call_id` 幂等回填）。

### 3.6 chat 与 router 解耦

```go
// chat 侧唯一依赖
type LLMClient interface {
    Step(ctx context.Context, req StepRequest, opts StepOptions) (<-chan StepEvent, error)
}
```

- `StepRequest` / `StepEvent` 为 chat 自持类型；
- router 侧新增 adapter：`routerclient.Step`（`StepRequest → router.Request`，`router.Event → StepEvent`），沿用现有 [routerconv.go](../../src/lib/llm/server/routerconv.go) 的转换逻辑；
- **禁止** chat import `chonkpilot-router`；反之 router 也不 import chat。

### 3.7 核心与扩展：插话在内核，其余走扩展点

**chat 内核（必须内置，不通过端口外置）**：

| 能力 | 为什么必须在内部 |
|---|---|
| **插话帧栈**（LIFO / stash / 续接说明） | 需与 turn 状态机、`pending` / `inflight` 同步演进；外置要复制一套状态并做双向同步，成本高且易错 |
| 工具循环 | 与帧栈、上下文拼装强耦合 |
| 续写**编排**（触发条件 / 次数 / 宽限期判定） | 与 turn 终态、`Emitted` 判定耦合；文案与投递由宿主决定 |

> 插话是高频能力，且状态就在 turn 内部——**外置实现代价大于内置**。
>
> **压缩不属于内核，也不在 Hook 里**：`ContextProvider.Build` 由宿主实现（读库 + 压缩 + 注入层 + system prompt 拼装），chat 只消费结果；`BeforeStep` Hook 仅用于临时改写请求，不承担压缩。**`Build` 只在 turn 开始时调用一次**；turn 内的 assistant / tool / 插话消息由 chat 在内存维护并经 `TurnStore` 落库。

**生命周期：Hook + LifecycleManager（宿主挂载，不属于 chat 内核）**

先澄清执行单位（**重要**）：

| 概念 | 定义 | 持久化单位 |
|---|---|:---:|
| **turn** | 会话层的一轮（用户输入 → 若干 step → 终态） | ✅ `messages.turn` |
| **frame** | turn 内的执行上下文（主帧 + 插入帧） | ❌ 内存结构 |
| **step** | 一次 LLM 请求—响应 | ❌ |

> **插话不产生新 turn**：`insertFrame` 没有独立 turnID（只有 inbox item id），插话消息经 `persistMessage` 落在**同一 turn**，按实际发生顺序写入 `messages`。因此上下文拼装天然正确，恢复也按同一 turn 进行。

三层职责：

| 层 | 职责 |
|---|---|
| `Hook` | 单个生命周期节点的回调 |
| `LifecycleManager` | 注册 / 排序 / 执行 / 失败策略 / **帧区分** / 清理 |
| `Extension`（可选） | 一组 Hook 打包，面向插件作者 |

```go
type Hook interface {
    BeforeTurn(ctx context.Context, t *TurnInfo) (Action, error)
    AfterTurn(ctx context.Context, r *TurnResult) error
    BeforeStep(ctx context.Context, c *ExecContext, req *StepRequest) (Action, error)
    AfterStep(ctx context.Context, c *ExecContext, res *StepResult) error
    BeforeTool(ctx context.Context, c *ExecContext, call *ToolCall) (Action, error)
    AfterTool(ctx context.Context, c *ExecContext, res *ToolResult) error
}

// ExecContext 描述「当前在哪个 turn 的哪个帧」
type ExecContext struct {
    TurnID    string // 同一 turn（插话不产生新 turn）
    FrameID   string // 主帧为空；插入帧 = inbox item id
    FrameKind string // "main" | "insert"
    Depth     int    // 插入深度（主帧 0）
}

type LifecycleManager interface {
    Register(h Hook, priority int, policy FailPolicy)
}
```

**触发规则（关键）**：

| 钩子 | 主帧 | 插入帧 |
|---|:---:|:---:|
| `BeforeTurn` / `AfterTurn` | ✅ 一次 | ❌ **不触发**（插话不是新 turn） |
| `BeforeStep` / `AfterStep` | ✅ | ✅（带 `FrameKind=insert`，宿主可自行过滤） |
| `BeforeTool` / `AfterTool` | ✅ | ✅ |

**为什么 step 级钩子必须带 `ExecContext`**：插话会在**同一 turn** 内再跑 step；记忆 / 计费 / 审计类扩展若不加区分会重复计数。turn 级钩子不受影响——这从另一面印证了「插话不是新 turn」。

**硬约束**：

1. 顺序按 priority 稳定排序；`FailPolicy` 默认 **fail-open**（Hook 失败不阻断主流程），`fail-closed` 必须显式声明；
2. 失败策略在注册时声明（fail-open / fail-closed）；
3. `After*` 必达：`Before*` 返回 Drop / Error 时也要走对应清理；
4. Hook 非阻塞；
5. 与既有 filter chain 对齐（`llm.before_start`→`BeforeTurn`、`llm.before_send`→`BeforeStep`、`llm.before_simple`→`BeforeStep(simple)`、`tool.before_call`→`BeforeTool`），**复用既有实现，不新造插件体系**；
6. Hook 由宿主实现，chat **不 import** `pluginbridge` / MQ。

### 3.8 单聊 / 群聊的差异不进 chat

**问题**：单聊（1 agent）与群聊（N agent）对 turn / step 的**后处理**不同，chat 是否按会话类型分支？

**答案：不。chat 只产出统一结果，差异全部由宿主策略承担。**

chat 统一输出：

```go
type TurnResult struct {
    SessionID string
    TurnID    string
    FinalText string           // 完整最终文本
    Messages  []Message        // 本轮落库消息（含插话）
    ToolCalls []ToolCallRecord
    Finish    string           // stop | length | error | canceled
    Usage     Usage
    Frames    int              // 插入帧数
}
```

| 群聊差异点 | 落点 | chat 侧 |
|---|---|---|
| 依次认领 / 不抢 | 宿主调度器 | 不认识 |
| `@` 定向投递 | 宿主解析 `FinalText` | 只给最终文本 |
| 本轮屏障 | 宿主调度器 | 不认识 |
| 判定与执行 = 同一次调用 | 宿主把判据写进 system prompt | 只执行 step |
| 「无关」落库不上屏 | 宿主在 `AfterTurn` / `EventSink` 处置 | 只发 `turn_end` 事件 |
| 只把结论上屏 | 宿主展示层 | 不变 |
| 收敛判定 / 到顶终审 | 宿主 | 不认识 |

**唯一允许的「类型感知」**：实例创建时注入的策略值（`DisplayName` / `Role` / 系统提示词），chat 只当普通请求参数，**不做分支判断**。

**反例（必须避免）**：给 chat 加 `SessionKind: single|group` 并按类型走不同后处理 → 调度、投屏、收敛逻辑渗入内核，独立发布与单测同时崩塌。

**若宿主需要业务判据**（如「有无实质推进」）：通过 `BeforeTurn` / `BeforeStep` Hook 注入，由宿主实现；chat 不内置任何业务判据。

### 3.9 群聊的上下文与压缩：会话已隔离，过滤在投递侧

**代码事实（2026-10-19 订正）**：

- 群聊 = **1 个 `group_chat` 会话（展示板）+ N 个 `work_session`**；后者每个参与者一个，`parent_id = 群聊会话`，跨轮复用（[facade/group.go](../../src/lib/data/facade/group.go#L18-L19) · [groupchat_sched_test.go](../../src/lib/llm/server/groupchat_sched_test.go#L378-L384)）。
- 每个 agent 的聊天历史**就在自己的 `work_session` 里** → `ContextProvider.Build` 只读到本 agent 可见内容。
- 群聊公共内容（名单 / 角色 / 目标 / 剧本 / 推进规则）**每轮以「注入层」进入 prompt**（[turn.go](../../src/lib/llm/server/turn.go#L738-L740)），**不写入历史、不参与压缩**。
- 定向投递（`from` 的 `@` / `to` 的 agent）发生在**投递侧**：宿主决定消息写入哪个 `work_session`、是否上展示板；**读取侧不再过滤**。

**因此**：

| 关注点 | 结论 |
|---|---|
| 压缩单位 | **sessionID**（每 session 一份摘要，天然隔离；压缩在 `ContextProvider` 内部完成） |
| 上下文可见性 | 由 **session 隔离**保证，chat 不做来源判断 |
| `@` / `to` 过滤 | 宿主在**发送时**解析并决定投递目标；读取侧无需再过滤 |
| chat 是否感知群聊 | ❌ 不感知；只按 `sessionID` 读写 |

- 端口契约：`ContextProvider.Build` 返回的 `Messages` 必须**稳定有序**（按落库序号）且**幂等**（同一 `ContextRequest` 重复调用结果一致）。

### 3.10 测试宿主（fake host）：组件内集成测试不依赖 MQ

端口化的直接收益：**可以为组件写一个内存宿主**，不引入 MQ / DB / 真实网络，就能跑接近系统级的集成测试。

```go
// 组件内提供（如 chat/chatest）
type Host struct{ /* 全内存实现 */ }
func NewHost() *Host
func (h *Host) Context() chat.ContextProvider  // 内存上下文（可脚本化压缩结果）
func (h *Host) Turns() chat.TurnStore        // 内存落库 + 事件日志
func (h *Host) Events() []chat.Event         // 事件录制（顺序可断言）
func (h *Host) Tools() *ScriptedInvoker      // 脚本化工具结果（含 Pending 回填）
func (h *Host) Clock() *FakeClock            // 手动推进时间（续写延迟 / 超时）
```

**能测**：turn 状态机（多 step / 工具循环 / 续写）· 插话帧栈（insert / stash / resumeNote / 深度上限）· ask 回填与失效回执（丢弃实例 → 重建 → 未知 `ask_id`）· 重放等价（事件序列 → `StepRequest` / 写入序列）· 事件输出顺序。

**不能测（必须由宿主仓 MQ systest 覆盖）**：消息形状与 61 契约 · 真实 data 落库（事务 / 字段）· 窗口与 UI 行为（L4）。

**与测试准则的关系**：测试宿主只用于组件自测（L1 / L2′），**不替代 mq 驱动的系统测试**；系统级仍按「发 mq → 监听断言」执行。

---

## 4. 规则与约定

| 项 | 结论 |
|----|------|
| router | **不改**，保持纯 lib（本次只增加 adapter，不改内核） |
| 端口数量 | chat：4 组（`ContextProvider` / `TurnStore` / `EventSink` / `ToolInvoker`）+ `ToolCatalog` + `Clock` + `*slog.Logger`；gateway：自有 `Dispatcher`；新增依赖必须先加端口，不得直连 |
| 类型 | 组件内 canonical；`data` / `facade` 只出现在 adapter |
| 状态修改 | 只能在 `Apply(state, event)` 内 |
| 事件序号 | 单调递增，不可复用；重放以 `Seq` 为游标 |
| 端口语义 | 顺序/事务/幂等写进接口注释，不只写实现 |
| 行为 | 除 ask 丢失修复外，零行为变化 |
| 拆包时机 | **重放判据通过后**才允许拆 `chonkpilot-chat` |
| **零 MQ** | 组件包级依赖图不得出现 `mq` / `msgkeys`；`go list -deps` + CI 断言 |
| **插话帧栈** | **chat 内核**，不通过端口外置（状态在 turn 内，外置需双向同步） |
| **工具协议** | 端口不出现 MCP task 类型；同步/异步由宿主实现内消化 |
| **ask_user** | 普通工具，chat 无特判；等待语义由宿主 `ToolInvoker` 消化 |
| **工具策略** | 自动执行 / 异步策略 / 错误分类归 MCP Gateway |
| **非核心功能** | 宿主以 `LifecycleManager + Hook` 挂载（登录 / 配置建议 / 群聊调度等），不进内核 |

---

## 5. 与 RETRY-2 的关系

- **顺序**：先 `RETRY-2`，后本专题。原因：重试归属（router vs chat）决定 `StepOptions` 是否需要携带 `MaxRetries/Budget/Observer`，先定重试再定端口，避免端口返工。
- **交集**：`RetryObserver` 的实现方最终是 chat（chat 把 router 回调翻译成 `Event`，由宿主经 `EventSink` 发 `llm-retry`）；因此 `EventSink` 必须在 `RETRY-2` 的 `Observer` 之上，不能反向依赖。**RETRY-2 阶段先在 `llm/server` 落地，MOD-1 再迁入 chat**（迁移只换实现位置，不改消息面）。
- **不重叠**：本专题不改重试次数、退避、判据。

---

## 6. 边界、异常与重启恢复

### 6.1 边界与异常

| 场景 | 处理 |
|------|------|
| 端口实现 panic | 组件内 recover，转终态错误，不崩宿主 |
| `EventSink` 阻塞 | 视为实现违约；组件侧带 ctx 超时并丢弃事件（不阻塞 turn） |
| 事件写盘失败 | 不进入下一状态；重试写或转终态（按 `TurnStore` 返回值） |
| 重放时遇到未知事件类型 | 显式失败（不静默跳过），避免"重放看似成功、实际丢步" |
| 旧版本快照 | `TurnState.Version` 校验；不兼容则收敛到终态并保留消息 |

### 6.2 重启恢复（对齐 [71 §3.3](../spec/70-conventions/71-编程规范.md) 五问）

1. **状态落在哪**：`TurnState` 快照 + 事件日志持久化；内存只保留运行副本。
2. **受理即持久化**：用户输入、ask 回答、插话消息均先落库再推进状态。
3. **非终态谁收口**：启动扫描 `Status != terminal` → 能重建则继续，不能则收敛并标记。
4. **重建 vs 收敛**：优先重建（有快照 + 事件日志且版本兼容）；否则收敛。
5. **副作用语义**：工具按 `tool_call_id` 幂等回填（至多一次写入结果）；重放不重派发已完成工具。

### 6.3 scheduler（非本期）

智能调度 / 降级链 / 预算优化应独立成层（chat 与 router 之间），**不得塞进 router**——否则 router 从「单次 step 传输层」变成「多次 step 优化器」，破坏单一职责与本专题的解耦目标。

---

## 7. 落地清单

| 阶段 | 任务 | 行为变更 | 验收 |
|------|------|:---:|------|
| M0 | 前置：`RETRY-2` 完成 | 有 | 见 RETRY-2 |
| M0.5 | ✅ **已落（2026-10-20）** **依赖盘点 + 零 MQ 基线（最高优先级）**：`go list -deps` 盘点三组件；gateway `Bus` → `Dispatcher` 端口 + 去 `msgkeys`（mq 适配下沉 `mqadapter`）；chat 各 `mq.Bus` 调用点收口到端口（待 M1a） | 无 | 三组件包级依赖图**零** `mq` / `msgkeys`；L3 import 断言入库（`gateway/deps_test.go` · `router/deps_test.go`） |
| M1a | ✅ **已落（2026-10-20）** **消费方类型 + 端口**：chat 定义 4 组端口（`ContextProvider` / `TurnStore` / `EventSink` / `ToolInvoker`）+ `ToolCatalog` + `Clock` + `*slog.Logger` 与 `ToolCall` / `ToolResult` / `ToolSpec` / `ToolProgress` / `CallContext`（在 RB-5 上补全）；模块 = `src/lib/chat`（`chonkpilot-chat`，仅标准库） | 无 | chat 零 import `mcpgateway` / `tasklayer` / `router`；L1/L2 全绿（`deps_test.go` + 端口契约哨兵） |
| M1b | ✅ **已落（2026-10-20）** **宿主 adapter + 契约测试**：`llm/server/chatadapter.go` 提供 chat ↔ data / facade / gateway / router 全字段互转（Message / ToolCall / InboxItem / CallContext / ToolSpec / ServerInfo / ToolResult）+ `chat.Clock` 系统实现；三条跨进程通道字段一致性断言（`chatadapter_test.go`：JSON = `gateway.Context` 键集；env = `mcp-server` 注入点字面守卫；DB = `data.ChatMsg` / `facade.InboxItem` 键集 + 往返）| 无 | 任一侧改字段名 → 契约测试失败（已验证）；`llm/server` 全量 `go test` 全绿 |
| M1c | ✅ **已落（2026-10-20）** **生命周期管理**：chat 定义 `Hook`（6 个 = 3 组 Before/After）+ `Action`（Continue/Drop）+ `FailPolicy`（FailOpen 默认 / FailClosed）+ `ExecContext`（`main`/`insert`）+ `TurnInfo`/`TurnResult`/`StepRequest`/`StepResult`/`Usage`/`ToolCallRecord` + `Lifecycle` 分发器（priority 稳定排序 / 失败策略 / **插入帧不触发 turn 级钩子**）；与既有 filter chain 映射写在文件头（`llm.before_start`→BeforeTurn 等）| 无 | chat 内核不含登录 / 配置建议 / 群聊调度等；Hook 零 MQ（`lifecycle.go` 仅标准库）；插入帧 turn 级钩子不触发（`lifecycle_test.go` 断言） |
| M2 | ✅ **已落（2026-10-20）** **组件自持类型**：`type ChatMsg = data.ChatMsg` / `type ToolCall = data.ToolCall` **别名已删除**，内部统一 `chat.Message` / `chat.ToolCall`（`llm.go`）；data 边界集中在 `chatadapter.go`（`ChatMessageFromData/ToData`、批量 `chatMessagesFromData/dataMessagesFromChat`、`ChatToolCalls*`），落库 / 快照 / 上下文读取 / token 估算 / router 转换全部经 adapter；`routerconv.go` 改 chat 类型；索引 `_meta` / `think` / `effort` 原样保留。EventSink 的 MQ 实现随 M5 引擎接线一并落地（M2 不含行为变更）| 无 | 全仓无 `data.ChatMsg` 别名（`grep` 断言）；落库 wire 与 `data.ChatMsg` 逐字段一致（`TestContractChatMessagePersistWireEquivalence`）；llm 全量 `go test ./...` 全绿 |
| M3 | ✅ **已落（2026-10-20）** **TurnState 提取 + `Apply(state,event)` + `Seq` 事件日志**：`chat/turnstate.go`（`TurnState`/`TurnStatus`/`Inflight`/`FrameState` + 12 类 `TurnEventType` + 纯函数 `Apply`：版本校验、Seq 单调、未知事件显式失败、插入帧 LIFO、终态守卫、入参不可变）；日志分级 `TurnEventLogLevel`（`none/minimal/outline/verbose` + `Records` + `ParseTurnEventLogLevel`）；**新建 `turn_events` 表**（data 迁移 v11）+ `data-turn-event-append`（幂等）/`data-turn-event-cleanup`（终态 + 保留天数）+ `TurnEventAPI`（persist/inline 双绑定）；配置 `turnEventLog`（默认 outline）/`turnEventLogRetentionDays`（默认 7，0=永久）+ 用户页签「会话」组 UI；首次使用 instance 时按配置清理 | 无 | 状态快照 JSON 往返；`Apply` 各转移单测（chat）；各级别写入量（chat）；清理按保留天数生效（data）；`chat` / `data` / `llm` 全量 `go test` 全绿；前端 `npm test` 703/703；genmsg 已再生 |
| M4 | ✅ **已落（2026-10-20）** **影子重放验证（只记录比对，不接管生产）**：`chat/trace.go`——`Recorder`（并发安全、只追加：`RecordStep` / `RecordWrite` / `Trace` / `Reset` / `Len`）、`ShadowTurnStore`（TurnStore 装饰器：先旁路记录、再原样委托 Inner；返回值/错误/顺序透传）、`Trace{Steps,Writes}`、`Diff`（canonical JSON 逐项、顺序敏感、缺失/多余标记）、`Verify`（汇总差异，最多展示 5 处） | 无 | 同一事件序列 + 同一桩 → `StepRequest` 与写入序列逐项相等（`trace_test.go`：等价 / steps 差异 / writes 差异 / 委托与错误透传 / nil 记录器 / Trace 隔离 / 空轨迹）；生产路径未接线（M5 起由宿主开关接入 `ShadowTurnStore`） |
| M5 | ✅ **内核已落（2026-10-20）** **chat 引擎内核**：独立 module `src/lib/chat`（M1a 建，7 个消费方 `replace` 已补）→ 新增 `chat/engine.go`：`New(Deps)` + `RunTurn(ctx,turnID)` + `RunSubTurn(ctx,req)`；唯一执行路径 `run`（上下文 → step → 工具循环 → 终态），`StepClient` 端口（`StepRequest`/`StepEvent`/`StepOptions` 自持），`RuntimeConfig.MaxSteps`/`EventLogLevel`；状态只经 `Apply`，事件日志受分级控制，`Shadow` 记录 `StepRequest`，生命周期 6 钩子在 4 个节点分发；Pending 工具 → 挂起返回（`Finish=pending` + `awaiting_tool`）。宿主接线见 M5b | 无（新代码未接入生产路径） | 独立 `go test` 全绿（单步 / 工具循环 / 挂起 / MaxSteps / 日志分级 / 影子等价 / 子轮次 / 钩子 Drop / 无 Done / LLM 错误 / Clock）；不 import router/gateway/data/facade/mq |
| M5b | ✅ **①② 已落（2026-10-20）；生产切换随 M6** **宿主接线**：① LLMClient + TurnStore + EventSink 三适配器 + 影子等价比对；② ToolCatalog + ToolInvoker 适配器（gateway 派发 / 同步·异步 / ask / 取消转后台）+ 工具环影子等价比对 | 无（全部影子，未接管生产） | ① 见下格；② 见下格；生产切换前置 = M6（插入帧 / ask·异步恢复 / 自动续写） |
| M5b-① | ✅ **已落（2026-10-20）** **三适配器 + 影子等价**：`llm/server/chatshadow.go`——`chatLLMClient`（`router.Spec` + `ChatOptions` → `Server.chat`；`StreamEvent` → `StepEvent`（Content→Delta / Reasoning→Reasoning / Err→Done.Err / Done→ToolCalls+Finish）；`Model`/`MaxRetries`/`RetryBudget`/`OnRetry` 由 `StepOptions` 覆盖，重试计数仍归 router）、`chatTurnStore`（per-turn：`SaveMessage`→`AppendFullKeyed`；`CommitInbox` 落消息 + `inbox consumed`（两步；**M6a 已切 `data-inbox-commit` 一步**）；`AppendEvent`→`data-turn-event-append`）、`chatEventSink`（`llm_delta`→`llm-receive`、`llm_retry`→`llm-retry`、`turn_complete`→`llm-complete`；**零新增主题**；`tool_result` 由落库消息桥转发、`tool_progress`/`ask_user` 归 M5b-② 宿主实现）。生产路径仍走 `turnCtx`（行为零变更） | 无 | `chatshadow_test.go`：① `TestChatLLMClientStep`（router step 文本 + Done）；② `TestChatTurnStoreWrites`（落库 + 事件日志同 seq 不同 type 冲突）；③ `TestChatEventSinkTopics`（相对域主题订阅断言）；④ `TestChatShadowParity`（引擎经三适配器跑两遍，`StepRequest` 序列 + 写入序列逐项相等，时钟固定）；`chonkpilot-llm/server` 全量 `go test` 92s 全绿；`chonkpilot-chat` 全绿 |
| M5b-② | ✅ **已落（2026-10-20）** **工具端口适配器 + 工具环影子等价**：`llm/server/chatshadow_tool.go`——`chatToolCatalog`（`Tools` = 宿主已过滤下发面 `[]ToolDef` → `chat.ToolSpec`（`Parameters`→`InputSchema` 原文、`_meta` 透传）；`Servers` 恒空 = MCP 服务目录属管理面 / UI，不进 chat 会话端口）；`chatToolInvoker`（per-turn 绑定 instance/session/turn/workDir/dataDir/topSession/parent；`Dispatch` = 策略硬拒→失败回喂 → ask_user 走宿主 `ask` 通道挂起 → `gc.Call`（`mcp-tools-call`）→ 同步结果 / `status=pending` 挂起（`OpID`=gateway task_id）/ `cancelled` 打 `_meta.status`；工具级错误（含 gateway 报错）编码为 `Result.IsError`，返回 error 仅表示适配器自身不可用；`Cancel`/`Detach` → `execSink`（空 instance 回落本轮））。**策略仍归宿主**：`args`（A5 非主会话强制 `async=never`）、`policy`（agent 白名单 + `mcp_invoke` 旁路加固）、`ask`（ask 通道）均为注入钩子，chat 不解释。生产路径仍走 `turnCtx`（行为零变更） | 无 | `chatshadow_tool_test.go`：`TestChatToolCatalogTools`（转换 / `_meta` / 空 schema / Servers 空）、`TestChatToolInvokerDispatch`（同步 / 策略拒绝 / 异步 pending / ask 挂起 / cancelled 标记 / 缺 instance 失败回喂）、`TestChatToolInvokerCancelDetach`（控制面转发 + instance 回落）、`TestChatToolLoopThroughAdapters`（引擎经真实四适配器跑完整工具环：2 次 step + assistant/tool 落库 + 两遍影子轨迹逐项相等）；`chonkpilot-llm/server` 全量 `go test .` 98.9s 全绿 + `go build ./...` 通过 |
| — | **生产切换（原 ② 后半）→ 并入 M6**：引擎当前只覆盖「上下文 → step → 工具循环 → 终态」，而生产 `turnCtx` 还承载**插入帧栈 / inbox 原子消费 / ask·异步回填续轮 / RETRY-2 自动续写 / 任务树节点与 `persistToolRunning/Result`**。这些能力迁入 chat 后才能切生产，否则属行为退化（违反 A1–A12 不丢）。 | 有 | 切换后 L1/L2/L4 全绿 + 同轮次影子轨迹等价 |
| M6 | 🟡 **M6a/M6b/M6c ✅ 已落（2026-10-20）；帧栈「继续」随生产切换** 恢复落地：**不建 ask 表**（答案即 tool result、回填即落库；重启后未回答的 ask 置灰、未知 `ask_id` 回执不静默）+ **新增 `data-ask-pending` 查询 + 启动扫描** + 帧栈恢复 + **移除 chat 内 `isAskTool` 特判**（等待语义下沉宿主 `ToolInvoker`；chat 内核本无该特判，现仅宿主适配器有，位置正确）+ **DSL 子轮次 `denyTools` 增 `ask_user`** + **data 层新增 `data-inbox-commit`（同事务：写消息 + 置 consumed，幂等），adapter 由「两步」切换为「一步」** | 有（消除静默丢弃 + 消除重复窗口） | ask 失效有回执；插入帧栈可继续；inbox 消费不重复；DSL 不弹 ask；启动扫描只命中未答 ask |
| M6a | ✅ **已落（2026-10-20）** **`data-inbox-commit` 同事务消费**：data 层新增 `data.Tx`（`DB.Update` + `Tx.Get/Exists/Insert/Put`，索引同事务维护）；`kernel.InboxMessageKey`（`M-INBOX-<inbox_id>` 确定性主键）→ 重试幂等；`facade.InboxAPI.InboxCommit` + session 实现（同事务落 messages + 置 consumed；discarded 拒绝；消息行增 `inbox_id` 仅本路径）；`persist` 信封 `commit` + 订阅；llm 适配器 `chatTurnStore.CommitInbox` 由两步切一步 | 有（消除「消息已落、inbox 未消费」的重复窗口） | `TestInboxCommit`（落库 + 索引可见 + 重试幂等 + discarded 拒绝）；`TestChatTurnStoreCommitInbox`（适配器一步消费 + 重试不重复）；`chonkpilot-data` 全量 `go test ./...` 全绿；`chonkpilot-llm/server` 全量 95.7s 全绿；61/schema/genmsg 已同步 |
| M6b | ✅ **已落（2026-10-20）** **ask 失效恢复（不建表）**：① ask_id 随发起落库——turn 循环先 `newID` 再 `persistToolRunning(..., askID)`（`messages.ask_id`，仅 ask 行；`askWithID` 由调用方给定，`ask()` 保持旧签名）；② `data-ask-pending`（`facade.AskAPI` + session 按 `messages.ask_id` 回查 + persist 信封/订阅 + 61/schema/genmsg）；`ask_id` 空 + `pending_only` = 启动扫描口径，`ask_id` 非空 = 精确回查（含终态）；③ 启动扫描（首次使用 instance、**遗留清理之前**）只留痕计数；④ `onAskUserReply` 未命中等待登记 → `replyExpiredAsk`：已终态（completed/failed/cancelled）→ 明确失败不重复开轮；非终态（running/interrupted）→ 按 61 §4.2「已超时关闭 → 转普通 user 消息 = 新 turn」开新轮喂答案；缺 `instance_id`/未落库 → 明确失败（不静默） | 有（消除静默丢弃；补充 ask_id 落库字段） | `TestAskPending`（未答过滤 / 精确回查含终态 / 未命中空 / question 解析）；`TestAskExpiredReply`（4 条失败路径 + 已回答不重复开轮 + 失效转新轮落库）；`chonkpilot-data` 全量全绿 |
| M6c | ✅ **已落（2026-10-20）** **DSL 子轮次硬拒 ask_user**：`Server.dslTurns`（`instKey(instance,turn)`）+ `mark/unmark/isDSLSubTurn`；`runDSLSubTurn` 起轮前登记、返回后注销；`newTurnCtx` 命中 → `denyTools{ask_user}`（下发面 + 调用处双拦，模型改走其他工具）。**帧栈恢复说明**：宿主侧「非终态 → 终态收敛 + 保留已落库消息」已由 `CleanupStaleTurns`/`cleanupStaleToolPairs` 承担；**帧栈「继续」**（插入帧执行）依赖 chat 引擎的插入帧能力，随生产切换实施（M6 后续 / M7 前） | 有（DSL 无人值守不再永久挂起） | `TestDSLSubTurnDeniesAskUser`（剥前缀两种口径 + 下发面不含 + 普通轮次不受影响）；`TestScenarioAgentToolsWhitelist` / `TestWorkSessionToolFaceExcludesSubchatFinish` 回归全绿 |
| M6d | ✅ **已落（2026-10-20）** **ContextProvider 宿主适配器**：`llm/server/chatshadow_context.go`——`chatContextProvider{s, scenarioID, agent, includeSnapshot}`（默认 `true`）；`Build` = 校验 `instance_id` / `session_id`（缺失明确报错）→ `sessionStore.BuildContextTokens(session, turn, includeSnapshot)`（已压缩历史：快照 + 最近 N 轮，`exclude_turn` 排除当前轮）→ `resolveTurnScenario`（五层系统提示词）→ `chat.Context{SystemPrompt, Messages}`（系统提示词**不重复塞进 Messages**）；压缩 / 拼装归宿主，`Build` 每轮只调用一次；`SetIncludeSnapshot(false)` 为同轮次继续（生产 `!req.Continue`）预留；`memoryGuide` / `assetGuide` 等 per-step 注入仍归宿主后续步骤。**生产路径未改**（`newTurnCtx` 原样），零新增 MQ 主题 | 无 | `TestChatContextProviderBuild`（历史含上一轮 `old`、当前轮 `now` 被 `exclude_turn` 排除、`SystemPrompt` 与 `resolveTurnScenario` 同源）+ `TestChatContextProviderRequiresIDs`（缺 instance / session 均报错）；`go test -run 'TestChatContext' -count=1 .` ok 2.635s；`go test -count=1 .` ok 93.650s；`go build ./...` 通过 |
| M6e | ✅ **已落（2026-10-20）** **引擎可见重试接线**：`chat/engine.go` 的 `RuntimeConfig` 增 `RetryCount *int` / `RetryBudget`（零值 = 旧行为）；调 `LLM.Step` 时构造 `StepOptions{MaxRetries, RetryBudget, OnRetry}`，`OnRetry(attempt, wait)` → `EventLLMRetry`（`RetryInfo{Attempt, Count=宿主配置值（nil → 0，仅供影子/诊断）, Wait, WaitMilli}`）→ 宿主 sink 发 `llm-retry`（payload 零变更）。报文级重试判据 / 退避仍归 router，引擎只做「回调 → 事件」折算；执行顺序与落库行为不变；**生产路径 `turnCtx` 未改**、零新增主题 | 无 | `TestEngineRetryEvent`（StepOptions 透传 + 事件字段 attempt=2/count=3/wait_ms=1500 + 终态 completed）+ `TestChatEnginePublishesRetry`（mq 驱动：500 → router 重试 → 收到 `llm-retry{retry_attempt≥1, retry_count=1}` → 文本 `retry-ok` / Finish `stop`）；`chonkpilot-chat` 全量 ok 0.986s；`llm/server` 全量 ok 95.496s；`src/lib/llm` `go build ./...` 通过 |
| M6f-1 | ✅ **已落（2026-10-20）** **引擎插话帧栈（Inbox 端口 + LIFO 插入帧 + 续接说明）**：`chat/ports.go` 新增 `Inbox` 端口（消费方定义、宿主实现；`Poll` 非阻塞）；`chat.Deps.Inbox`（nil = 无中途输入，行为与 M6e 逐字节一致）；`chat/types.go` 增 `InboxTypeInsert = "insert"` + `InsertResumeNote`（与生产 `turn.go` `insertResumeNote` 文案逐字一致）；`chat/engine.go` 把 step 循环抽成可重入单帧 `runFrame(ctx,o,depth,frameID,fr)`（`frameRun` 跨帧共享 messages / records / usage / 文本 / 全局 step 计数），帧栈唯一事实源 = `rt.state.Frames`、续接说明 = `rt.resumeNotes`（主帧键空串、一次性）；每个 step 结束后（下一 step 的 `BeforeStep` 之前 / 本帧收口之前）`pollInbox`：空内容丢弃 · 非 `insert` 忽略（M6f-2）· `len(Frames) >= 5` 留痕不消费 · 正常 → `CommitInbox`（同事务落消息 + 置 consumed）→ 记 `inbox_insert` → 递归插入帧；插入帧收口 / 全局 step 额度用尽 → 记 `inbox_consume` 出栈 + 给父帧标注续接说明；父帧下一次 `StepRequest` 最前面临时插 system 续接说明（一次性、不落库、不进 messages）；`MaxSteps` = 跨帧全局计数（插入帧超限只出栈不终结整轮，主帧 → `ErrMaxSteps`）。**只实现 `insert`**：`tool_result` / ask 回执续轮见 M6f-2（✅ 已落）、自动续写留 M6f-3 | 无（生产路径 `turnCtx` 未改；零消息面变更） | `TestEngineInsertFrame`（3 次 StepRequest 顺序 / 第 2 次含插入 user 消息 / 第 3 次首位 = `InsertResumeNote` 且仅 1 次 / `CommitInbox` 1 次 / `inbox_insert`→`inbox_consume` / `Frames=0` / `FinalText=part1answerdone`）· `TestEngineInsertDepthLimit`（6 条预置 → 消费 5 条、第 6 条不消费、completed）· `TestEngineInsertEmptyAndNonInsertIgnored`（空内容 / `next_turn` / `tool_result` 均不 Commit、不建帧）· `TestEngineNilInboxUnchanged`（Inbox=nil 事件序列与单步行为不变）；`chonkpilot-chat` 全量 `go test -count=1 ./...` ok 0.857s；`llm/server` `go test -run 'TestChat'` ok 7.751s；`src/lib/llm` `go build ./...` 通过 |
| M6f-2 | ✅ **已落（2026-10-20）** **异步工具回执续轮**：`chat/types.go` 增 `InboxItem.Result *ToolResult`（`Type=="tool_result"` 时 = 回执，AskID/OpID 由宿主映射到 CallID）+ `InboxTypeToolResult = "tool_result"`；`chat/engine.go` 的 `RuntimeConfig` 增 `PendingWait`（0 = 旧行为：Pending 立即返回；>0 = 轮内等待上限）与 `PollInterval`（<=0 → 50ms）。工具循环收到 `outcome.Pending` 且 `PendingWait>0` 且有 `Deps.Inbox` → 进入等待循环（`Clock.After` 做超时 / 间隔轮询，`ctx.Done` → canceled）：① `tool_result`：CallID 空则补当前调用，**只接受匹配当前待回填调用**的回执（不匹配 → 忽略并继续等），命中后按同步结果路径回填（抽出 `deliverToolResult`：落 tool 消息 → `EventToolResult` → `AfterTool` → `tool_result` 事件日志）→ 继续工具循环 / 下一 step（不再返回 pending）；② `insert`：走 M6f-1 路径（`CommitInbox` + 插入帧），插入帧执行时 `runOpts.skipInbox=true`（不抢本轮待消费回执），跑完**继续等**原回执；③ 超时：合成 `ToolResult{CallID, Content:"错误: 工具执行超时（等待回执 <dur>）", IsError:true}` 按同步路径回填后续轮（**不终结整轮**）+ `Logger.Warn`；等待期间**不重复 Dispatch**。`PendingWait<=0` / 无 Inbox → 与 M6f-1 逐字节一致（立即返回 pending）。**已知限制**：一次只等一个 Pending（一次 step 的多个异步调用按既有顺序串行等待，生产多路并发在飞 + 统一闸门留 M6f-3 / 生产切换对齐）；跨进程重建续轮未做（本轮内存态等待，进程崩溃仍走宿主收敛） | 无（生产路径 `turnCtx` 未改；零消息面变更） | `TestEnginePendingWaitResult`（1 次 Dispatch / step2 含 tool 消息 "ok" / completed / 有 `tool_result` 事件 / 无超时占位）· `TestEnginePendingWaitTimeout`（无回执 + 立即超时假时钟 → step2 见 `IsError` 超时文本 / 整轮 completed / 无 panic）· `TestEnginePendingWaitInsertThenResult`（等待中先 insert 再 tool_result：3 次 StepRequest / 插入帧含插话 / 续轮首位 = `InsertResumeNote` / `CommitInbox` 1 次 / 回执被消费 / 帧栈收敛）· `TestEnginePendingNoWaitKeepsOldBehavior`（`PendingWait=0` → 立即 `Finish=pending` / 只 1 次 step / 不轮询 inbox）；`chonkpilot-chat` 全量 `go test -count=1 ./...` ok 1.006s；`chonkpilot-llm/server` `go test -count=1 -run 'TestChat' .` ok 8.097s；`src/lib/llm` `go build ./...` 通过 |
| M6f-3 | ✅ **已落（2026-10-20）** **自动续写编排**：`chat/engine.go` 的 `RuntimeConfig` 增 `AutoContinue`（**false 零值 = 关闭 = 旧行为**）/ `AutoContinueDelay`（<=0 → 600s）/ `AutoContinueText`（空 → 「继续」）/ `AutoContinueMax`（<=0 → 1，每次中断最多续写次数，计数落 `frameRun.continues`）。**中断判定**（step 结束即判，工具循环之前）：`done.Err != nil` 且本轮已产生文本 → 已连接后断流（**不进入 failed**）；`done.Finish == "length"` → 输出截断（同样视为中断）；`done.Err != nil` 且无文本 → 保持既有 `fail`。中断先按既有路径落库部分 assistant 文本（非空时）→ `EventSessionInterrupted{Text}` → `AutoContinue` 开启时进入**宽限期等待** `awaitAutoContinue`：`Clock.After(AutoContinueDelay)` + `ctx.Done()` + 按 `PollInterval`（复用 M6f-2 字段，<=0 → 50ms）轮询 `Inbox` —— ① `insert` → 走 M6f-1 路径（`CommitInbox` + 插入帧，`skipInbox` 防抢输入）后**取消本次续写**；② `tool_result`（本步已结束、无待回填调用）→ 留痕并**取消续写**（不重复 Dispatch）；③ 到期且无输入 → `Store.SaveMessage(user/kind=continue/<AutoContinueText>)`（`persist=false` 只进内存）→ 内存追加 → `TurnEventContinue`（无载荷）→ 计数 +1 → 下一 step；④ `ctx` 取消 → canceled（`context.WithoutCancel` 收尾）。次数用尽 / 未开启 → 按中断类型收口（`length` → completed，断流 → failed）。**零新增消息 / 字段**：复用既有 `session-interrupted` 与 `kind=continue`、`continue` 事件；`AutoContinue=false` 时连 `session_interrupted` 都不发（逐字节旧行为）。**生产路径 `turnCtx` 未改**、不新增 MQ 主题、不改 61 / schema | 无（生产路径 `turnCtx` 未改；零消息面变更） | `continue_test.go`：`TestEngineAutoContinueOnLength`（step1 `length`+"part1" → step2 请求尾部 = user「继续」（Kind=`continue`）→ 续写消息落库 1 次 + 部分文本落库 → 终态 completed / `FinalText=part1done` / `continue` 事件 / `session_interrupted` 文本 "part1"）· `TestEngineAutoContinueOnce`（两次 `length` + `Max=1` → 2 次 StepRequest、`Finish=length`、completed、续写 1 次）· `TestEngineAutoContinueCancelledByInsert`（宽限期内 insert → 插入帧执行 / `CommitInbox` 1 次 / 三请求顺序含 `InsertResumeNote` / 零续写消息 / completed）· `TestEngineAutoContinueDisabled`（零值关闭 → 单请求、`Finish=length`、无 `session_interrupted`）· `TestEngineInterruptedWithPartialText`（`done.Err` + 有文本：enabled → 部分 assistant 落库 + `session_interrupted` + 续写 + completed；disabled → 错误透传 + failed）；`chonkpilot-chat` 全量 `go test -count=1 ./...` ok 1.089s；`chonkpilot-llm/server` `go test -count=1 -run 'TestChat' .` ok 8.663s；`src/lib/llm` `go build ./...` 通过 |
| M7 | ✅ **已落（2026-10-20；纯文档，零代码 / 零 schema / 零消息契约变更）** **spec 同步**：① [2D-router](../spec/20-modules/2D-router.md) §6.1（传输层边界与宿主 adapter：报文级职责 · `OnRetry` → `llm-retry` · 不感知 chat/gateway/MQ · `deps_test.go` 断言）；② [21-llm-server](../spec/20-modules/21-llm-server.md) §11（chat 引擎与端口，含 `Inbox`；执行路径 / 帧栈与续轮 / 自动续写 / 可见重试；生产仍走 `turnCtx`）+ §12（恢复：`turn_events` · `messages.ask_id` · `data-ask-pending` · `data-inbox-commit` · 启动扫描 · 失效回执 · DSL `denyTools` + **恢复五问**）；③ [12-数据层](../spec/10-architecture/12-数据层.md) §7.1（MOD-1 内部面：`ask_id` 仅 ask 行 / 不建 ask 表 · `data-inbox-commit` 同事务与确定性主键 · `data-ask-pending` 全桶低频口径）；④ [71-编程规范 §3.3.1](../spec/70-conventions/71-编程规范.md)（有状态能力恢复指向 21 §12，五问必须逐条回答）；⑤ [61-消息一览](../spec/60-reference/61-消息一览.md) **仅 §0 架构注**（`llm-receive/llm-retry/llm-complete` 由 `EventSink` 输出、字段不变；内部面已登记 §3，**payload / 字段零改动**）；⑥ [49-待实现项](../spec/40-roadmap/49-待实现项.md)（状态补 `M7 ✅` + 进度段）；⑦ 本档 §0.1「当前」列按事实刷新。（`64` 的 `turnEventLogRetentionDays` 已在 M3 同步，M7 无新增改动） | — | 与代码同批（逐处核对 `chatshadow*.go` / `chat/ports.go` / `ask_facade.go` / `inbox_facade.go` / `turnstate.go` / `deps_test.go`） |
| M8 | ✅ **M8：C1/C2/C3/C4 ✅（2026-10-20）** 测试：L1（Apply / 重放）· L2′（组件内测试宿主 `chatest`）· L2（崩溃恢复：ask / 帧栈）· L3（模块边界：禁 import 断言）。**C1 ✅** = `chat/replay.go` 的 `Replay` + `replay_test.go`（`TestReplayMatchesApply` / `TestReplayRejectsInvalidEvents` / `TestReplayEngineEvents`）；**C2 ✅** = `chat/chatest/`（`Context` / `Store` / `Sink` / `Clock` / `Inbox` / `LLM` / `Tools` / `Catalog` / `NewDeps`）+ `chatest_test.go`（`TestSingleStep` / `TestToolLoop` / `TestInsertFrameStack` / `TestReplayEquivalent` / `TestPendingToolResult`）；**C3 ✅** = 宿主仓 `llm/server/recovery_test.go`（`TestAskRecoveryAfterRestart` / `TestInboxCommitCrashRetryOnce`，mq 驱动 + data 面断言）；**C4 ✅** = `chat/deps_ci_test.go`（`go list -deps ./...` 前缀断言，含 `chatest`）+ `chat/ports_ci_test.go`（`ports.go` 接口方法签名无 `any` / `interface{}`） | — | C1/C2/C3/C4 全绿（`chat` 全量 `go test -count=1 ./...` 含 `deps_ci_test.go` / `ports_ci_test.go`；`llm/server` 全量 `go test -count=1 .`） |

| M9-P1 | ✅ **已落（2026-10-20）** **宿主接线（不接管）**：`llm/server/chatshadow_inbox.go` 的 `chatInbox`（per-turn 内存 FIFO；`Push` / 非阻塞 `Poll` / `Len`；数据面行复用 `ChatInboxFromFacade`）；`llm/server/chatshadow_tool_prod.go` 的 `chatToolInvokerFromTurn`（`args` = `tc.gatewayArgs`；`policy` = 白名单硬拒 + `mcp_invoke` 旁路加固，顺序与文案逐字对齐 `turn.go`；`ask` = `askWithID(tc.req.Turn, call.ID, args, "", "ask-"+newID())`，任务节点仍由生产循环创建 → P1 传空）；`chatshadow.go` 增 `session_interrupted` → `llm-interrupted`（与生产 `raiseInterrupted` 同形载荷：`instance_id/session/turn/code/retryable/auto_continue_delay_ms` + 可选 `message`）；`chat/types.go` 的 `Event` 增 `Code` / `Retryable` / `DelayMilli`（引擎在截断 / 断流时填充，续写触发逻辑不变）；**生产 `turnCtx` / `server.go` 零改动**、零新增 MQ 主题、61 / schema 未改 | 无 | `TestChatInboxAdapter` / `TestChatInboxItemFromFacade` / `TestChatToolInvokerFromTurnPolicy` / `TestChatEventSinkInterrupted`（`llm/server`）+ `TestEngineInterruptEventFields`（`chat`）；`chonkpilot-chat` 全量 `go test -count=1 ./...` ok 2.942s；`llm/server` 全量 `go test -count=1 .` ok 103.937s；`src/lib/llm` `go build ./...` 通过 |
| M9-P2 | ✅ **已落（2026-10-20）** **离线重放影子（A4-P2，默认关闭）**：`llm/server/chatshadow_replay.go` 的 `shadowReplayTurn(instanceID, sessionID, turnID)`——turn 结束后用**已落库消息 + 已落库工具结果**重跑 chat 引擎并比对消息序列（**零网络 / 零工具执行 / 零 DB 写 / 零 MQ**）。① **上下文** = 生产同一份 `chatContextProvider.Build`（本项修正语义：已压缩历史 + **本轮已落库用户输入**，**不含**本轮 assistant / tool）；② `replayLLM`（`chat.LLMClient`）按已落库 assistant 顺序脚本化 step（Reasoning → Delta → Done，Finish = `tool_calls` / `stop`；调用序号超出脚本 → 显式 `Done.Err`，不静默）；③ `replayToolInvoker`（`chat.ToolInvoker`）按 `tool_call_id` 返回已落库结果（`{call,result,async}` 取 `result.content` + `status` 判 `IsError`；读侧已归一为裸文本则回退原文；缺失 → `IsError` 明确失败）；④ `replayStore`（`chat.TurnStore`）只记内存（种子 = 本轮用户输入）；`Catalog` / `Sink` 空实现、`Clock` 系统时钟、`AutoContinue=false` / `PendingWait=0` / `MaxSteps=脚本条数+2`；⑤ **比对** = 生产 `LoadMessages` 与重放记录逐项 JSON（只取 `role` / `content` / `tool_call_id` / `tool_calls`，忽略 `created_at` / `id` / `kind` / `brief`），最多 5 条差异摘要（空 = 等价）。**触发点唯一** = `turn.go` 正常终态 `complete(...)` 之后 / `Close()` 之前，`CHONKPILOT_CHAT_SHADOW=1` 才起 goroutine（`recoverLog` 兜底、只经 `logf`、不发 MQ）；**env 未设 → 零调用**。**零消息面变更**（61 / schema / msgkeys 未改） | 无 | `TestReplayLLMScript`（2 条 assistant → 两次 Step 的文本/工具调用/Finish + 超脚本显式错误）· `TestReplayToolInvoker`（ToolContent JSON → 文本 / IsError；裸文本回退；缺失 → IsError）· `TestShadowReplayDiff`（等价 / 内容 / 缺失 / 多出 / 忽略字段）· `TestShadowReplayTextTurn`（mq 驱动纯文本轮 → `report==""`、无 error）· `TestShadowReplayToolTurn`（mq 驱动「tool call → 结果 → 文本」→ `report==""`）· `TestShadowReplayEnvOff`（env 未设 → 触发点零调用）+ `TestChatContextProviderBuild`（当前轮 user 应含、assistant/tool 不应含）；`llm/server` 全量 `go test -count=1 .` ok 101.529s（全绿）；`chonkpilot-chat` 全量 `go test -count=1 ./...` ok；`src/lib/llm` `go build ./...` 通过 |
| M9-P3a-0 | ✅ **已落（2026-10-20）** **生命周期接口抽象**：`turnSession` / `turnInput`；`s.turns` 改接口容器；旧路径类型化助手不变；行为零变更 | 无 | 全量测试 + `TestTurnSessionInterface` |
| M9-P3a | ✅ **已落（2026-10-20；A4-P4b 已删除该单步路径与开关）** **纯文本单步引擎接管**（历史阶段）：开关（P4b 已删除）；前置条件（无工具/无图片/非 continue/无续接说明）；等价性用例。P3b 起主会话整轮接管后本单步分支只对少数旧路径轮次生效，P4b 随旧路径一并删除 | 无 | `TestEngineTextParity` / `TestEngineTextToolsParity` / `TestEngineTextImageParity` / `TestEngineTextEmptyReply` / `TestEngineTextCancel` 全绿（当时口径：全轮接管；P4b 后图片 / 空回复改为引擎单跑覆盖，其余由 `TestEngineTurn*` 覆盖） |
| M9-P3b | ✅ **已落（2026-10-20）** **工具轮 / 异步 / ask 接管**：`chatOnce` 的单工具处理抽成 `turnCtx.dispatchToolCall`（逐字搬迁：策略硬拒 · running 落库 · 任务节点 · ask · 同步/异步 · 结果落库 · 任务终态；`pending=true` 表示回执异步到达），旧路径与引擎端口**共用**同一实现；`chatToolInvoker` 由 `chatshadow_tool_prod.go` 的装配改为直接调它（ask 的 `taskID` / `topSession` / `parent` 不再留空）；宿主端口装配 = `engine_turn.go`（Context=逐字 `tc.msgs()`、Store=生产落库（tool 跳过）、Sink=零新增主题、Catalog=`tc.llmTools()`、LLM=`newChatLLMClient`+`applyReasoningRule` 归一）；`RuntimeConfig.PendingWait = pending_task_timeout_sec`（缺省 2h）+ `PendingPlaceholder`（等待期占位 tool 消息，插话帧请求保持合法协议序列）+ 多路回执「后发先至」暂存（`chat/engine.go` 的 `heldResults`，不丢弃也不重复派发） | 无（env 默认关闭；旧路径保留可回滚） | `TestEngineTurnSyncTool` / `TestEngineTurnAsyncTool` / `TestEngineTurnAsk` / `TestEngineTurnText` / `TestEngineTurnSameTurnContinue`（env=0/1 双跑：落库消息 / role=tool 行终态 / 任务树事件 / `llm-receive` / `llm-complete` 逐项相等）+ `chat.TestEnginePendingPlaceholderAndHeldResult` |
| M9-P3c | ✅ **已落（2026-10-20）** **整轮接管（含插话 / 自动续写 / 终态）**：`loop()` 入口 `engineMode` 分支（仅主会话：无父链、`kind` 为空、非 DSL 子轮次；子会话 / 群聊 / 恢复轮仍走旧路径）；`runEngineLoop` = 首输入等待（复用 `tc.in` / `Kick` 语义）→ 输入泵（`tc.in` → `tool_result` / `insert`；`tc.inboxCh` → 相位 / 撤回 / 内容判据后建插入帧；`tc.asyncDone` → `handleAsyncDone(legacy=false)` 落库后投 `tool_result`）→ `RunTurn` → 终态收口（complete / incomplete / interrupted / error，`finishOnce` 仍唯一）；终态文本按生产 `answer` 口径（主帧、无 tool_calls 段）由 Lifecycle `AfterStep` 累积；`length` 在关闭续写时由宿主补发同形 `llm-interrupted` | 无（env 默认关闭；旧路径保留） | `TestEngineTurnInsert`（插话 + 回执恢复）· `TestEngineTurnLength`（incomplete + 中断提示 + 半截 assistant 落库）· `TestEngineTurnCancel` · `TestEngineTurnSubChatOldPath`（子会话零接管）· `TestEngineTurnModeGate`（主会话开；子聊天 / 群聊 / 干活子会话 / DSL 关）；`llm/server` 全量 `go test -count=1 .` 全绿（两次连续） |
| M9-P4-lite | ✅ **已落（2026-10-20；A4-P4b 已删除开关与旧路径）** **主会话默认开启引擎（旧路径保留，历史阶段）**：① `chatEngineEnabled()` = **env 未设 → true**；`1/true/on` → true；`0/false/off` → false（回滚旧路径，零引擎调用）。② 接管资格不变（`turnCtx.engineMode` = 开关 && 无父链 && 会话 `kind` 为空 && 非 DSL 子轮次）→ 子聊天 / 群聊 / 干活子会话 / DSL 子轮次 / 恢复轮**当时**走旧 `loop`。③ P3a 单步接管（`engineTextEligible`）**限定主会话判定面**：单步只回正文、无法承接工具轮，默认开启后在非主会话拦截会丢弃模型返回的 `tool_calls`（旧 `loop` 会照常派发）→ 回归修复项。④ 适配器对齐生产语义：`autoContinueDelaySec=0` = 立即续写（引擎零值表示「未设」→ 显式折成正的最小正时长）。⑤ 引擎嵌套插入帧回执：`skipInbox` 帧等待自身异步工具时**仍取件**，途中插话按序暂存交回外层循环（`heldInserts`），修复「嵌套插入帧工具只能等超时」。⑥ 当时**不删除任何旧代码**（P4 删除见 M9-P4b）。 | 无（61 / schema / msgkeys 未改，零新增 MQ 主题） | `TestEngineTurnModeGate`（当时 5 env × 形态矩阵，P4b 已删除）+ `chat.TestEngineNestedInsertPendingResult` + `TestInboxInsertNestedTool` / `TestAllSessionsToolCallForceAsyncMQ` / `TestTaskTreeSubSessionParent` / `TestAutoContinueAccumulatesAnswer` / `TestTurnLengthAutoContinue` 回归；`llm/server` 全量 `go test -count=1 .` 连续两次全绿；`chat` 全量 `go test -count=1 ./...` 全绿；`src/lib/llm` `go build ./...` 通过 |
| M9-P4b-0（P4a 后续步，见下一行 M9-P4a） | ✅ **已落（2026-10-21）** **恢复轮（含半途 assistant/tool）全量接入引擎**（P4b 删除旧路径的前置）：① **删除 `engineRecoverable()` 门槛** —— 恢复轮（`recoverTurnCtx`，`isRecovery=true`）**始终** `engineMode=true`，不再按消息形态回落旧 `loop`；旧 `loop` 自此只剩 `group_chat` 本体（不跑 LLM）/ 未知 `kind` 这一条不可达分支。② **消息种子口径** = `engineTurnContext.Build` 返回 `tc.msgs()`（hist + 本轮全部已落库消息，system/user/assistant/tool 顺序与旧 `loop` 的 `chatOnce` 一致；`forceFull=true` 保证全量拼接）→ 引擎只从历史继续下一次 LLM 调用，**不重复派发已完成工具**（引擎不会从历史 tool_calls 派发）。③ **悬空 tool_call**（assistant 有 tool_calls 但该轮缺 tool 行，启动清理 `cleanupStaleToolPairs` 未覆盖的极端情况）：`runEngineLoop` 在**收到首个输入之后、落首条输入之前**调 `engineSynthesizeDanglingToolResults()` —— 逐条合成 `role=tool` + `tool_call_status=interrupted`（文案 = 数据层 `kernel.toolPlaceholder("interrupted")` 逐字：「（工具已中断，未获得结果；如需重试请使用「重试」）」）并 `persistToolResult` 落库，同时 `logf` 记 **WARN**；顺序敏感（合成行必须紧跟 assistant.tool_calls，否则协议变成 assistant → user → tool）。已存在 tool 行（任何终态）不覆盖 = 幂等。④ **续写 / 子轮次规则**沿用 `engineAutoContinue()`（子轮次强制关闭、子聊天保留 usr 配置）；终态仍由 `Server.finish` 唯一收口，`tc.result` / `tc.turnErr` 与旧路径同源。⑤ **旧代码未删除**：`turnCtx` 旧路径完整保留（P4b 删除待本步验证后执行）；**61 / schema / msgkeys 未改、零新增 MQ 主题**。 | 无（旧路径保留可回滚） | `TestEngineTurnRecoveryAlwaysEngine`（仅 user / 含 assistant 的恢复轮均 engineMode=true 且引擎恰好 1 次，原 `TestEngineTurnRecoverySafeSubset` 改写）、`TestEngineTurnRecoveryMidTurn`（落库 user + assistant(tool_calls) + tool(completed) → 恢复轮引擎接管 → 下一步 LLM 收到这三条（按序）+「继续」；gateway `mcp-tools-call` 零调用 = 不重复派发；落库 / 终态与手动构造的「继续」逐项一致）、`TestEngineTurnRecoveryDanglingCall`（无 tool 行 → 合成 1 条 interrupted（状态 + 文案）→ 正常续跑，不 panic / 死循环 / 不派发工具）、`TestEngineTurnModeGate` 增恢复轮判定（= 开且进入引擎）。**验证（2026-10-21 实跑）**：见 [49](../spec/40-roadmap/49-待实现项.md) 进度注（定向 + 两次全量 + `chonkpilot-chat` 全量全绿）。 |
| M9-P4b | ✅ **已落（2026-10-21）** **删除旧 turn 执行路径与回滚开关（引擎唯一执行路径）**：① **删除回滚开关** —— `chatEngineEnabled()` / `turnCtx.engineMode` / `engineTurnSelectable()` / `engineAutoContinue` 内 env 判定全部移除；全仓（代码 / 测试 / spec / 本稿）**零引擎开关环境变量引用**。② **`loop()` 只保留引擎** —— 唯一执行体 = `runEngineLoop`（原 `if tc.engineMode` 分支与旧循环体删除）。③ **删除旧执行代码**：`chatOnce`（旧 LLM+工具循环；`dispatchToolCall` 保留并成为引擎 ToolInvoker 唯一实现）· `deliverInput` · `consumeInbox` · `reapStalePending` / `reapInflight`（旧看门狗）· `maybeResumeTop` / `finishInsertFrame` / `abortCurrentFrame` / `setResumeNote` / `takeResumeNote` · `resumePartial` / `unpersistedInputs` · P3a 单步接管 `engine_step.go`（`engineTextEligible` / `runEngineStep` / 单步端口；`engineStepLLM` 移入 `engine_turn.go` 继续承担思考链转发）· `turnCtx` 的 `iterations` / `answer` / `stash` / `resumeNote` / `persistedResultOK` / `engineMode` / `engineStepFinish` 字段与 `insertFrame.stash|answer|resumeNote`。④ **保留（引擎依赖）**：`turnCtx` / `newTurnCtx` / `msgs` / `persistMessage` / `dispatchToolCall` / `finish` / `complete` / `Close` / `finishOnce` / `llmTools` / `gatewayArgs` / `toolAllowed` / `markInflight` / `registerPending` / 任务树 / ask / `raiseInterrupted` / `publishInterrupted` / `runEngineLoop` / `enginePump` / `engineFirstInput` / `engineRouteInput` / `engineRouteInsert` / `engineFinish`。⑤ **测试清理**：删除 env=0/1 双跑基线（`TestEngineText*` 等价套件 · `TestEngineTurnModeGate` · `TestResumePartialExhaustedPersistsTail` · `TestReapInflightFeedsFailedPlaceholder` · `TestUnpersistedInputsUserDedup`）；其余改为「引擎唯一路径」单跑断言（纯文本 / 同步·异步工具 / ask / 插话 / length / 取消 / 同轮次继续 / 子聊天 / 恢复轮；新增图片 / 空回复两例承接原 `TestEngineText*` 覆盖）；`TestInboxDiscardBeforeConsume` 由 `consumeInbox` 改调 `engineRouteInsert`。⑥ **61 / schema / msgkeys 零变更、零新增 MQ 主题**；终态仍由 `Server.finish` 唯一收口。 | 无（引擎唯一路径，无回滚开关） | `go build ./...` / `go vet ./...` 通过；`llm/server` 全量 `go test -count=1 .` 连续两次 ok **108.6s / 108.8s**；`chonkpilot-chat` 全量 ok；`src/lib/llm` `go build ./...` 通过；`grep` 引擎开关环境变量 = **0**；`gofmt -l` 仅剩 3 个与本步无关的历史文件（`domainmcp_test.go` / `domainmd.go` / `groupchat_sched.go`） |
| M9-P4a | ✅ **已落（2026-10-20；A4-P4b 已删除本行所述选择器 `engineTurnSelectable` / `engineMode` 与旧 `loop` 回退，引擎为唯一路径）** **子会话 / 群聊 / DSL 子轮次接入引擎 + 恢复轮安全子集**：① **按路径选择** `Server.engineTurnSelectable(req, sc)` —— 主会话（`kind` 空）· 子聊天（`sub_chat`）· 子会话（`sub_session`，含 `runChildTurn`/`subllm`）· 群聊参与者干活子会话（`work_session`）· DSL 子轮次（`isDSLSubTurn`，父链非空或无父链皆可）→ 开；`group_chat` 本体（不跑 LLM）与未知 `kind` → **关**（保守走旧 loop）。② **`loop()` 分流**：`engineMode` 且（非恢复轮 或 `engineRecoverable()`）→ `runEngineLoop`；否则回落旧 `loop`（同时把 `engineMode` 置 false，使旧单步接管判定面保持原样）。`engineRecoverable()` = `LoadMessages(turn)` 中无 `assistant` / `tool`（只允许 system/user）——引擎无法从半途 assistant/tool 重建，重跑会重复工具调用。③ **恢复轮**在 `recoverTurnCtx` 打标 `isRecovery=true`，并按同口径判 `engineMode`（会话 `kind` 由会话行读侧归一提供）。④ **AutoContinue**：子轮次（父链非空 / `sub_session` / `work_session`）**强制关闭**（`engineAutoContinue()`），不计 `AutoContinueMax`；子聊天保留 usr 配置（其窗口可成为活动会话，与生产 `activeSessionOK` 一致）。`length` 且续写被关闭时仍由宿主补发同形 `llm-interrupted`。⑤ 首输入 / 输入泵 / 终态（`tc.result`/`tc.turnErr` 经 `Server.finish`）/ 任务树（`taskNodeBase`，parents→top_session/parent_id 不变）/ `denyTools`（Catalog 仍用 `tc.llmTools()`，含 `ask_user` / `subchat_finish` 硬拒）全部同源，不放宽工具面。⑥ **旧代码仍未删除**（P4 删除留待四路径 soak 稳定）。 | 无（61 / schema / msgkeys 未改；零新增 MQ 主题；旧路径保留可回滚） | 新增 `TestEngineTurnSubChatP4a`（`sub_chat` env=0/1 双跑：落库消息 / 终态等价 + env=1 引擎恰好 1 次、env=0 零调用）、`TestEngineTurnRecoverySafeSubset`（仅 user → 引擎接管；含 assistant → 回落旧 loop、引擎零调用；**P4b-0 已改写为 `TestEngineTurnRecoveryAlwaysEngine`（恢复轮恒引擎）**）；`TestEngineTurnModeGate` 扩为 7 形态 × 5 env（主 / 子聊天 / 子会话 / 干活子会话 / DSL = 可接管；群聊 / 未知 kind = 关闭）；逐路径等价另有既有 `TestSubChat*` / `TestGroupChat*` / `TestDSLSubTurnDeniesAskUser` / `TestRecoverTurnCtx*` 回归覆盖。**验证（2026-10-20 实跑）**：`gofmt` 干净（`turn.go` 已格式化）；定向 `-run 'TestEngineTurn|TestEngineStep|TestSubChat|TestGroupChat|TestDSL|TestRecover'` ok **33.3s**；全量 `go test -count=1 .` 连续两次 ok **123.6s / 120.4s**；`chonkpilot-chat` 全量 ok；`src/lib/llm` `go build ./...` 通过。修复 1 处测试清理顺序（`TestRecoverTurnCtxCarriesSystemPromptAndDirs`：`data.Reset` 晚于 `t.TempDir()` 注册，避免 Windows 上 DB 占用导致 `RemoveAll` 失败）。 |

> **M8-C1/C2/C3/C4 交付（2026-10-20；仅测试 + 文档；生产逻辑（`turn.go` / `askrecover.go` / `chatshadow*.go` / `engine.go`）与 61 / schema 未改）**
> - **C1 L1 重放 API**：`chat/replay.go` —— `Replay(sessionID, turnID string, events []TurnEvent) (TurnState, error)`：从 `NewTurnState` 起步逐条 `Apply`（同一转移函数 / 同一失败判据）；空事件 → 初始 `pending` 状态；未知事件 / `Seq` 非单调（重复、回退、0）/ 版本不符 / 载荷非法 → **返回错误，不静默跳过**；不修改入参切片。
> - **C2 L2′ 测试宿主**：`chat/chatest/`（`package chatest`，仅 import 标准库 + `chonkpilot-chat`）—— `Context`（固定系统提示 + 初始消息）· `Store`（`SaveMessage` 追加、`CommitInbox` 幂等、`AppendEvent` 按序原样保存；快照 `Messages()/Writes()/Events()/InboxCommits()`）· `Sink`（并发安全记录 `[]chat.Event`）· `Clock`（固定 `Now` + `Fire(d)` 让指定时长的 `After` 立即就绪，覆盖 `PendingWait` 与 `AutoContinue`）· `Inbox`（FIFO + 非阻塞 `Poll` + `Empty()`）· `LLM`（按调用序号脚本化 `Step`、可按下标返回 error、记录 `StepRequest`）· `Tools`（按 CallID / Name 脚本化 `Dispatch` + 记录调用）· `Catalog`（静态工具列表）· `NewDeps(...)` / `(*Deps).Engine(cfg)` 便捷装配。
> - **C3 L2 崩溃恢复（宿主仓，mq 驱动 + data 面断言）**：`llm/server/recovery_test.go` —— `TestAskRecoveryAfterRestart`（落库 user 输入 + ask running 行 → `data-ask-pending{pending_only}` 命中 1 条 → `CleanupStaleTurns` 后 pending 归 0 且精确回查 `interrupted` → `replyExpiredAsk` 转新 turn）· `TestInboxCommitCrashRetryOnce`（`data-inbox-commit` 连续两次 → 消息仅 1 条、inbox `consumed`、重试不报错）。**帧栈「继续」/ 事件日志可恢复**：宿主侧无事件读取接口（只有 append / cleanup），按约束不改消息面；由 `chat/chatest` 的 `TestReplayEquivalent`（C2）覆盖 → 本项不重复造测。
> - **C4 L3 模块边界**：`chat/deps_ci_test.go`（`go list -deps ./...` 逐行前缀断言：`chonkpilot-lib/mq` / `msgkeys` / `chonkpilot-router` / `chonkpilot-mcp-gateway` / `chonkpilot-data` / `facade` / `chonkpilot-llm`；`go` 不在 PATH → `t.Skip` 并打印原因）· `chat/ports_ci_test.go`（`go/parser` 解析 `ports.go`，断言接口方法签名不含 `any` / `interface{}` / `map[string]any`；若发现违规只 `t.Skipf` 记录，不改端口）。**补充关系**：router / gateway 已有源码级 `deps_test.go`，C1/C3 的 CI 断言以 chat 的 `deps_ci_test.go` 为补充；C5 扫描以 `ports_ci_test.go` 为准。
> - **测试**：`gofmt -w` 改动文件 · `chonkpilot-chat` `go test -count=1 ./...` ok（含 `/chatest`）· `llm/server` `go test -count=1 -run 'TestAskRecoveryAfterRestart|TestInboxCommitCrashRetryOnce|TestAskExpiredReply|TestChatTurnStoreCommitInbox' .` ok · `llm/server` 全量 `go test -count=1 .` ok · `src/lib/llm` `go build ./...` 通过。

---

## 8. 关联文档

- [49-待实现项 §2](../spec/40-roadmap/49-待实现项.md)（`MOD-1` · `RETRY-2`）
- [2D-router](../spec/20-modules/2D-router.md)
- [21-llm-server](../spec/20-modules/21-llm-server.md)
- [61-消息一览](../spec/60-reference/61-消息一览.md)
- [71-编程规范 §3.3](../spec/70-conventions/71-编程规范.md)
- [LLM重试分层设计方案](../llm-retry/LLM重试分层设计方案.md)（前置）
