# TURNRUN-1 执行提示词（历史归档；**不可直接执行**）

> ⛔ **历史执行提示词，不可直接执行（2026-11-04 核对）**：`TURNRUN-1` 的**批次 1–6 已全部完成**（`docs/spec/20-modules/2H-轮次运行器与职责边界.md:3` 状态「批次 1–6 已落（2026-10-28）」）。本文件仅作**历史执行稿 / 交接记录**保留；照做会重复改造并与现行代码冲突。现行事实以 `2H` / `49 TURNRUN-1` / 代码为准。
>
> **已失效的关键指令**：
> - **批次 1 的 `CreateMutexW` 单例**已于 **2026-10-29** 被 **UDS 独占 listen 单例**（`singleinstance.Acquire`）取代（`2H:279,288` 明确）；`src/lib/gui/singleton_windows.go` **从未存在于当前树**，实际入口是 `src/desktop/main.go`。
> - **「61 零变更 / `git diff` 为空」验收**在当前工作树不可用：61、schema、msgkeys 等已有与本任务无关的未提交改动。
> - **代码坐标已漂移**：`engine_turn.go:82-109` 已不是 `chat.New`（`chat.New` 在 `boot.go:46`）；批次 3 的 `TurnParams` 实现**不含 `Deps`**（身份与端口分离）。
>
> 用法（仅作历史参考）：把本文件全文粘到新会话作为第一条消息。仓库根 = `e:\BizWorks\chonkpilot`；同级仓 = `e:\BizWorks\chonkpilot-agent\chat`、`e:\BizWorks\chonkpilot-agent\session`、`e:\BizWorks\chonkpilot-agent\router`、`e:\BizWorks\chonkpilot-agent\gateway`、`e:\BizWorks\chonkpilot-agent\types`。
> 本提示词只覆盖**代码改造**；文档已在上一轮收敛完毕，不要重做。

---

## 0. 任务（**已完成，仅归档**）

执行 `TURNRUN-1` 的**批次 1–6**：把 `llm/server` 里的轮次调度与输入策略下沉到 `chonkpilot-agent/session`，chat 改为无状态单例，补进程单例，新建共享数据类型仓。
**消息面（[61-消息一览](../../docs/spec/60-reference/61-消息一览.md)）零变更**——topic / payload / 字段一个都不许增删改。

> **执行状态（2026-11-04）**：批次 1–6 全部 ✅（`2H` 批次表 1–6 全绿）；唯一的机制替换 = 批次 1 的 `CreateMutexW` → UDS 单例 `singleinstance.Acquire`。下文「改动 / 验收」为历史任务书，**不要据此重新实施**。

## 1. 开工前必读（唯一口径）

| 文档 | 用途 |
|---|---|
| `docs/spec/20-modules/2H-轮次运行器与职责边界.md` | 本任务设计稿（§3 契约 / §4 一致性 / §5 单例 / §8 批次 / §11 异常恢复） |
| `docs/spec/40-roadmap/49-待实现项.md` 的 `TURNRUN-1` 行 | 任务登记与状态 |
| `docs/spec/60-reference/61-消息一览.md` | 消息契约（只读，禁改） |
| `.trae/rules/项目规则.md` | 构建 / 插件边界 / 日志 / 异常恢复硬约束 |
| `docs/spec/10-architecture/12-数据层.md` §5、`16-路径解析规范.md` | 数据根与路径口径 |

## 2. 已定决策（不要重新讨论）

1. **session 不 import chat**：session 自定义 `Message / ToolCall / Context / Event`，由 server 做边界转换。
2. **chat 保持每轮一个实例**，但实例必须**无跨轮状态**；`Deps` 中的 `InstanceID / SessionID` 外提到 `RunTurn` 入参。
3. **插入的边界**：能不能插入 / 插不进降级 `next_turn` / 帧深上限 → **决策归 session**；帧执行（嵌套跑帧）→ **留 chat**。
4. **工具列表与知识库/索引列表 = 活读 + 读写锁，不拉快照**；仅配置与 LLM spec 在本轮开始时快照。
5. ~~**单例只做 `CreateMutexW`**（`Local\chonkpilot-desktop`），入口第一件事；不做 UDS 单例、不做 window class 单例；随后**删除 session 文件锁**。~~ **〔2026-11-04 核对：已反转〕现行 = UDS 独占 listen 单例（`singleinstance.Acquire`），`CreateMutexW` 已于 2026-10-29 废止；session 文件锁已删除。**
6. **数据层保持短读短开**，不改常开。
7. **唯一形态 = desktop**：不要引入 gui / browser / flash / CLI 任何概念或分支。
8. **共享契约仓** `chonkpilot-agent/types` 第一批只放 6 个纯数据类型：`Message / ToolCall / ToolSpec / ToolResult / CallContext / Usage`；不放 router 的 `Part/Message`，不放 chat 的 `Event / InboxItem / TurnState`。

## 3. 已完成（不要重做）

- 批次 0 已收口：`src/server/` 已删除；`docs/spec` 全面改为单形态；`.trae/rules/项目规则.md` 已同步。
- `2H §8` 的批次表已从 **1** 开始。

---

## 4. 分批任务

### 批次 1 · 进程单例 + 删 session 文件锁（✅ 已完成；机制已被 UDS 单例取代）

> **2026-11-04 核对**：`src/lib/gui/singleton_windows.go` / `singleton_other.go` **不存在**，`CreateMutexW` 方案已废弃；现行入口 `src/desktop/main.go` 经 `singleinstance.Acquire`（UDS 独占 listen）实现单实例。`sessionlock_{windows,other}.go` / `_test.go` 已删除（`handler_session.go:203` 注释确认）。以下为历史任务书。

**改动**
- ~~新增 `src/lib/gui/singleton_windows.go`：`AcquireSingleInstance() (bool, error)`，`CreateMutex(nil,false,\`Local\chonkpilot-desktop\`)`；`GetLastError()==ERROR_ALREADY_EXISTS` → `false`；句柄保持到进程结束（不提前 Close）。~~
- ~~新增 `src/lib/gui/singleton_other.go`（`//go:build !windows`，恒返回 `true`）。~~
- ~~`src/lib/gui/host/main.go`：**函数第一段**调用；返回 false → 直接退出（不建窗、不打开数据根）。~~
- ✅ 删除 `src/lib/llm/server/sessionlock_windows.go`、`sessionlock_other.go`、`sessionlock_test.go`。
- ✅ 删除 `src/lib/llm/server/handler_session.go` 中 session 文件锁的获取/释放调用点（约 196–205 行），保留**内存 `busy`** 语义不变。

**验收**
- 双开实测：第二实例退出，首实例不受影响。
- `go test ./...`（`src/lib/llm`、`src/lib/gui`）+ L4 全量。

---

### 批次 2 · 新建 `chonkpilot-agent/types`（✅ 已完成，2026-10-27）

**改动**
- 新仓 `e:\BizWorks\chonkpilot-agent\types`，module 名与现有仓风格一致；只依赖 stdlib。
- 首批 6 个结构体，字段与现有 JSON tag **逐字一致**（以 `chonkpilot-agent/chat/src/types.go` 为源）。
- `chonkpilot-agent/chat`、`chonkpilot-agent/session` 内改为**类型别名过渡**（`type Message = llmtypes.Message`）；**不动 router**（它的 `Message{Content []Part}` 是另一层块模型）。
- `chonkpilot-agent/session/src/deps_test.go`：白名单改为 `stdlib + chonkpilot-agent/types`；黑名单保持 `data / llm / gui / mq / msgkeys / router / mcp-gateway`。

**验收**
- 现有键集断言全绿（`assertKeys` / `v2contract_fixture` / `chatadapter_test`）。
- 序列化字节对比：改造前后同一 payload 的 JSON 完全一致。
- 两仓 `go test ./...` 全绿。

---

### 批次 3 · chat 身份外提（不改单例）（✅ 已完成，2026-10-27）

**改动（`chonkpilot-agent/chat`）**
- `Deps` 去掉 `InstanceID / SessionID`；新增 `TurnParams{ InstanceID, SessionID, TurnID string; Deps }`。
- `RunTurn(ctx context.Context, p TurnParams) (TurnResult, error)`；`turn` 结构持有 `p` 与全部可变状态（`state / heldResults / heldInserts / resumeNotes / seq`）。
- 更新测试构造点（约 24 处）。

**验收**
- `go test -race ./...`（同 Engine 并发不同 session 不同 turn）。
- 宿主侧适配：`llm/server` 调用点传本轮 `instance/session/turn`。

> **实施记录（2026-10-27）**：`Deps` 已删身份字段；新增独立 `TurnParams{InstanceID,SessionID,TurnID}` + `Runtime.RunTurnParams`（**未**把 `Deps` 内嵌进 `TurnParams` —— 端口/配置仍是实例级依赖，每轮只传身份，避免无谓复制）。过渡期保留 `NewForTurn` + 旧 `RunTurn(ctx, turnID)`（批次 6 删）；宿主 `engine_turn.go` 已切 `RunTurnParams`；`go test ./... -race` 全绿。落地口径见 [2H §3.4](../spec/20-modules/2H-轮次运行器与职责边界.md) 与 [49 §2.12](../spec/40-roadmap/49-待实现项.md#1-引用迁移与历史索引2026-11-04)。

---

### 批次 4 · `session/inbox` 策略下沉（✅ 已完成，2026-10-27）

**改动**
- 新包 `chonkpilot-agent/session/src/inbox`（纯函数 + 状态快照入参）：`Insertable(frameOpen bool, depth int) bool`、`Classify(input) (insert | next_turn)`、`Degrade(item)`、`Promote(item)`、`MaxDepth`。
- `src/lib/llm/server/turn.go` 的 `insertable()`（约 1170 行）、`engine_turn.go` 的 `engineRouteInsert`（约 240–273 行）、`inbox.go` 的 `onInboxAdd / promote / discarded`（约 183–242、313–360、396–400 行）改为**调用该包**，行为零变更。
- `inbox` 落库仍走 `TurnStore` 端口（`ListBySession / Add / UpdateStatus / Promote / Lookup / CommitInbox`）。

**验收**
- 平移现有 L1 单测；L4 全量（插话 / 降级 / next_turn / 群聊屏障）。

---

### 批次 5 · `session/runner` 下沉（核心）（✅ 已完成，2026-10-27）

**改动**
- 新包 `chonkpilot-agent/session/src/runner`：
  - `TurnRequest{ InstanceID, SessionID, TurnID, LLMSpec, RuntimeConfig, ToolList }`（**配置/工具在轮开始时快照**）；
  - `Hooks{ Store, Events, Tools, Inbox }`；
  - `TurnExecutor interface { RunTurn(ctx, TurnRequest, Hooks) (TurnResult, error) }`；
  - 运行态注册表：`turns map[instKey(instance,turn)]`、`busy map[instKey(instance,session)]`、`asks`，全部进程内单例，随实例退出清理。
  - 归属：turn 生命周期、帧栈决策、自动续写开关/宽限、终态映射（finish reason）。
- `llm/server` 只留适配器：MQ 订阅/应答、payload 编解码、instance 视图、data facade、gateway 调用、文件锁（workdir 占用锁）、**领域事件 → 61 主题**映射。
- 群聊 / 子聊天 / `next_run` **本批不下沉**（保持现状，长程再做 M3）。

**验收**
- 61 零变更（`git diff` 检查 `61-消息一览.md`、`61-messages.schema.json`、`msgkeys*` 为空）。
- L1 平移 + L4 全量；`-race`。

---

### 批次 6 · chat `Engine` 单例化（✅ 已完成，2026-10-27）

**改动**
- `chonkpilot-agent/chat`：`Engine` 只保留 `logger / clock`；`RunTurn` 内部创建 `turn`；`New()` 不再需要 per-turn 参数。
- 宿主侧持有单例 `Engine`，每次传 `TurnParams`。

**验收**
- `go test -race ./...`：同一 Engine 并发跑 ≥8 轮（不同 session）无数据竞争。
- L4 全量。

---

## 5. 全局硬约束

1. **61 零变更**：不得增删改任何 topic / payload / 字段；需要变更先停下问用户。**〔2026-11-04 核对：该验收已失效——当前工作树 61 / schema / msgkeys 已有与本任务无关的未提交改动，`git diff` 不会为空〕**
2. **测试必须 MQ 驱动 + MQ 断言**（见 `docs/spec/50-testing/50-测试体系.md` §1）。
3. **构建只走脚本**：`.\build-desktop.ps1`；不得裸跑 `go build` / `npm run build`；前端产物落 `src/desktop/frontend/dist`。
4. **文件编辑只用 Write / SearchReplace / Edit**；不得用 PowerShell 重定向 / `-replace` 改文件。
5. **异常与重启恢复**：每批都要回答五问（状态落点 / 受理即持久化 / 非终态收口 / 重启收敛 / 副作用幂等），并至少一条 L1/L2 恢复测试。
6. **不要放宽断言来变绿**；测试与契约冲突时，改代码或登记 docGap，禁止改断言掩盖。
7. **每批完成后回改**：`2H`（若契约有调整）+ `49 TURNRUN-1` 状态；不得改动 `42` 历史。
8. **不做破坏性操作**；不 `git add -f`；不提交 git（除非用户明确要求）。

## 6. 每批交付格式

1. 改动文件清单（新增 / 修改 / 删除）。
2. 关键决策点：与 2H 的偏差（若有，先说明理由并等确认）。
3. 验证：执行的命令 + 结果（通过数 / 失败项原文）。
4. 未决项：做不到 / 需拍板的事项，写入 `49` 或 `41`，不要口头略过。

## 7. 关键代码坐标（起点；**2026-11-04 核对：行号已漂移，不可作为执行起点**）

> 仅作历史定位参考。`engine_turn.go:82-109` 现为 lifecycle / `RunTurnParamsWith`；`chat.New(chat.Deps{...})` 已不在 `engine_turn.go`（在 `boot.go:46`）。其余文件仍存在，但行号需重新 Grep。

| 位置 | 内容 |
|---|---|
| `src/lib/llm/server/handler_session.go` ~102–256 | `llm-start` 受理：校验 / busy / 落库 / `newTurnCtx` / 应答 |
| `src/lib/llm/server/turn.go` ~499–502 | `newTurnCtx` 末尾 `go tc.loop()` |
| `src/lib/llm/server/turn.go` ~1038–1063 | `loop → runEngineLoop` |
| `src/lib/llm/server/turn.go` ~1170–1174 | `insertable()` 相位判定 |
| `src/lib/llm/server/engine_turn.go` ~82–109 | `chat.New(chat.Deps{...})` |
| `src/lib/llm/server/engine_turn.go` ~151–189 | 自动续写组装 |
| `src/lib/llm/server/engine_turn.go` ~240–273 | `engineRouteInsert`（insert / 降级） |
| `src/lib/llm/server/engine_turn.go` ~278 | `engineFinish` 终态映射 |
| `src/lib/llm/server/inbox.go` ~183–242 / 313–360 / 396–400 | inbox 分类 / 提升 / 降级 |
| `src/lib/llm/server/finish.go` ~114 | `consumeNextTurn` 起新一轮 |
| `src/lib/llm/server/groupchat_sched.go` / `subchat.go` ~164–194 / `nextrun.go` | 长程 M3，本任务不动 |
| `chonkpilot-agent/chat/src/engine.go` ~78–147 / 836–864 | `Deps / Runtime / New` 与插入帧 |
| `chonkpilot-agent/chat/src/lifecycle.go` ~101–129 | `StepRequest` 与 6 个 Hook |
| `chonkpilot-agent/session/src/types.go` ~24–34 / `compose.go` ~51 | 现有 3 行 chat 类型别名 |
| `chonkpilot-agent/session/src/deps_test.go` ~17–23 / 77–85 | 依赖白名单 / 黑名单 |

## 8. 验收命令（每批至少跑对应项）

```powershell
# 构建（改了 Go / 前端才需要）
.\build-desktop.ps1

# 单元测试（按仓）
cd e:\BizWorks\chonkpilot\src\lib\llm; go test -race ./...
cd e:\BizWorks\chonkpilot\src\lib\gui; go test -race ./...
cd e:\BizWorks\chonkpilot-agent\chat;     go test -race ./...
cd e:\BizWorks\chonkpilot-agent\session;  go test -race ./...

# L4 系统测试（MQ 驱动）
python e:\BizWorks\chonkpilot\src\test\chonkpilot-gui\systest\run_all.py
```

> 失败处置：先止血（阻塞性错误立即停），再收敛（单点失败记录后跑完），修完按「失败用例 → 受影响组 → 全量」三层重跑。

## 9. 完成定义（**已达成，2026-11-04 核对**）

- 批次 1–6 全部落地（✅），L4 已由后续批次覆盖；61 在 TURNRUN-1 范围内零变更。
- `2H` 与 `49 TURNRUN-1` 状态已更新为已完成；未落实的（M3 群聊/子聊天下沉、`llm/server` 改名 `glue`）留在 `49` / `41`。
- 本文件自此转为**历史归档**，不再作为执行输入。
