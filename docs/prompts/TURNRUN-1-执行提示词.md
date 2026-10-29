# TURNRUN-1 执行提示词（新会话首条消息）

> 用法：把本文件全文粘到新会话作为第一条消息。仓库根 = `e:\BizWorks\chonkpilot`；同级仓 = `e:\BizWorks\chonkpilot-chat`、`e:\BizWorks\chonkpilot-session`、`e:\BizWorks\chonkpilot-router`、`e:\BizWorks\chonkpilot-mcp-gateway`。
> 本提示词只覆盖**代码改造**；文档已在上一轮收敛完毕，不要重做。

---

## 0. 任务

执行 `TURNRUN-1` 的**批次 1–6**：把 `llm/server` 里的轮次调度与输入策略下沉到 `chonkpilot-session`，chat 改为无状态单例，补进程单例，新建共享数据类型仓。
**消息面（[61-消息一览](../../docs/spec/60-reference/61-消息一览.md)）零变更**——topic / payload / 字段一个都不许增删改。

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
5. **单例只做 `CreateMutexW`**（`Local\chonkpilot-desktop`），入口第一件事；不做 UDS 单例、不做 window class 单例；随后**删除 session 文件锁**。
6. **数据层保持短读短开**，不改常开。
7. **唯一形态 = desktop**：不要引入 gui / browser / flash / CLI 任何概念或分支。
8. **共享契约仓** `chonkpilot-llm-types` 第一批只放 6 个纯数据类型：`Message / ToolCall / ToolSpec / ToolResult / CallContext / Usage`；不放 router 的 `Part/Message`，不放 chat 的 `Event / InboxItem / TurnState`。

## 3. 已完成（不要重做）

- 批次 0 已收口：`src/server/` 已删除；`docs/spec` 全面改为单形态；`.trae/rules/项目规则.md` 已同步。
- `2H §8` 的批次表已从 **1** 开始。

---

## 4. 分批任务

### 批次 1 · 进程单例 + 删 session 文件锁

**改动**
- 新增 `src/lib/gui/singleton_windows.go`：`AcquireSingleInstance() (bool, error)`，`CreateMutex(nil,false,\`Local\chonkpilot-desktop\`)`；`GetLastError()==ERROR_ALREADY_EXISTS` → `false`；句柄保持到进程结束（不提前 Close）。
- 新增 `src/lib/gui/singleton_other.go`（`//go:build !windows`，恒返回 `true`）。
- `src/lib/gui/host/main.go`：**函数第一段**调用；返回 false → 直接退出（不建窗、不打开数据根）。
- 删除 `src/lib/llm/server/sessionlock_windows.go`、`sessionlock_other.go`、`sessionlock_test.go`。
- 删除 `src/lib/llm/server/handler_session.go` 中 session 文件锁的获取/释放调用点（约 196–205 行），保留**内存 `busy`** 语义不变。

**验收**
- 双开实测：第二实例退出，首实例不受影响。
- `go test ./...`（`src/lib/llm`、`src/lib/gui`）+ L4 全量。

---

### 批次 2 · 新建 `chonkpilot-llm-types`

**改动**
- 新仓 `e:\BizWorks\chonkpilot-llm-types`，module 名与现有仓风格一致；只依赖 stdlib。
- 首批 6 个结构体，字段与现有 JSON tag **逐字一致**（以 `chonkpilot-chat/types.go` 为源）。
- `chonkpilot-chat`、`chonkpilot-session` 内改为**类型别名过渡**（`type Message = llmtypes.Message`）；**不动 router**（它的 `Message{Content []Part}` 是另一层块模型）。
- `chonkpilot-session/deps_test.go`：白名单改为 `stdlib + chonkpilot-llm-types`；黑名单保持 `data / llm / gui / mq / msgkeys / router / mcp-gateway`。

**验收**
- 现有键集断言全绿（`assertKeys` / `v2contract_fixture` / `chatadapter_test`）。
- 序列化字节对比：改造前后同一 payload 的 JSON 完全一致。
- 两仓 `go test ./...` 全绿。

---

### 批次 3 · chat 身份外提（不改单例）

**改动（`chonkpilot-chat`）**
- `Deps` 去掉 `InstanceID / SessionID`；新增 `TurnParams{ InstanceID, SessionID, TurnID string; Deps }`。
- `RunTurn(ctx context.Context, p TurnParams) (TurnResult, error)`；`turn` 结构持有 `p` 与全部可变状态（`state / heldResults / heldInserts / resumeNotes / seq`）。
- 更新测试构造点（约 24 处）。

**验收**
- `go test -race ./...`（同 Engine 并发不同 session 不同 turn）。
- 宿主侧适配：`llm/server` 调用点传本轮 `instance/session/turn`。

> **实施记录（2026-10-27）**：`Deps` 已删身份字段；新增独立 `TurnParams{InstanceID,SessionID,TurnID}` + `Runtime.RunTurnParams`（**未**把 `Deps` 内嵌进 `TurnParams` —— 端口/配置仍是实例级依赖，每轮只传身份，避免无谓复制）。过渡期保留 `NewForTurn` + 旧 `RunTurn(ctx, turnID)`（批次 6 删）；宿主 `engine_turn.go` 已切 `RunTurnParams`；`go test ./... -race` 全绿。落地口径见 [2H §3.4](../spec/20-modules/2H-轮次运行器与职责边界.md) 与 [49 §2.12](../spec/40-roadmap/49-待实现项.md)。

---

### 批次 4 · `session/inbox` 策略下沉

**改动**
- 新包 `chonkpilot-session/inbox`（纯函数 + 状态快照入参）：`Insertable(frameOpen bool, depth int) bool`、`Classify(input) (insert | next_turn)`、`Degrade(item)`、`Promote(item)`、`MaxDepth`。
- `src/lib/llm/server/turn.go` 的 `insertable()`（约 1170 行）、`engine_turn.go` 的 `engineRouteInsert`（约 240–273 行）、`inbox.go` 的 `onInboxAdd / promote / discarded`（约 183–242、313–360、396–400 行）改为**调用该包**，行为零变更。
- `inbox` 落库仍走 `TurnStore` 端口（`ListBySession / Add / UpdateStatus / Promote / Lookup / CommitInbox`）。

**验收**
- 平移现有 L1 单测；L4 全量（插话 / 降级 / next_turn / 群聊屏障）。

---

### 批次 5 · `session/runner` 下沉（核心）

**改动**
- 新包 `chonkpilot-session/runner`：
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

### 批次 6 · chat `Engine` 单例化

**改动**
- `chonkpilot-chat`：`Engine` 只保留 `logger / clock`；`RunTurn` 内部创建 `turn`；`New()` 不再需要 per-turn 参数。
- 宿主侧持有单例 `Engine`，每次传 `TurnParams`。

**验收**
- `go test -race ./...`：同一 Engine 并发跑 ≥8 轮（不同 session）无数据竞争。
- L4 全量。

---

## 5. 全局硬约束

1. **61 零变更**：不得增删改任何 topic / payload / 字段；需要变更先停下问用户。
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

## 7. 关键代码坐标（起点）

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
| `chonkpilot-chat/engine.go` ~78–147 / 836–864 | `Deps / Runtime / New` 与插入帧 |
| `chonkpilot-chat/lifecycle.go` ~101–129 | `StepRequest` 与 6 个 Hook |
| `chonkpilot-session/types.go` ~24–34 / `compose.go` ~51 | 现有 3 行 chat 类型别名 |
| `chonkpilot-session/deps_test.go` ~17–23 / 77–85 | 依赖白名单 / 黑名单 |

## 8. 验收命令（每批至少跑对应项）

```powershell
# 构建（改了 Go / 前端才需要）
.\build-desktop.ps1

# 单元测试（按仓）
cd e:\BizWorks\chonkpilot\src\lib\llm; go test -race ./...
cd e:\BizWorks\chonkpilot\src\lib\gui; go test -race ./...
cd e:\BizWorks\chonkpilot-chat;     go test -race ./...
cd e:\BizWorks\chonkpilot-session;  go test -race ./...

# L4 系统测试（MQ 驱动）
python e:\BizWorks\chonkpilot\src\test\chonkpilot-gui\systest\run_all.py
```

> 失败处置：先止血（阻塞性错误立即停），再收敛（单点失败记录后跑完），修完按「失败用例 → 受影响组 → 全量」三层重跑。

## 9. 完成定义

- 批次 1–6 全部落地，61 零变更，L4 全绿。
- `2H` 与 `49 TURNRUN-1` 状态更新为已完成；未落实的（M3 群聊/子聊天下沉、`llm/server` 改名 `glue`）留在 `49` / `41`。
- 最终构建由 `.\build-desktop.ps1` 后台发起，不等待。
