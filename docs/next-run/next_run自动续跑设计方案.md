# next_run 自动续跑设计方案（独立稿）

> 状态：✅ **已实施（2026-10-21）** —— 落地清单与验证记录见 [49-待实现项 `NEXTRUN-1`](../spec/40-roadmap/49-待实现项.md)
> 日期：2026-10-20
> 关联（spec）：[49-待实现项 §2](../spec/40-roadmap/49-待实现项.md)（登记项 `NEXTRUN-1`）· [21-llm-server](../spec/20-modules/21-llm-server.md) · [22-data-persist](../spec/20-modules/22-data-persist.md) · [3A-工具与编排](../spec/30-function-points/3A-工具与编排.md) · [61-消息一览](../spec/60-reference/61-消息一览.md) · [64-配置项一览](../spec/60-reference/64-配置项一览.md) · [71-编程规范 §3.3](../spec/70-conventions/71-编程规范.md) · [中途插话设计方案](../agent-chat/中途插话设计方案.md)
> 代码目录（规划）：`src/lib/llm/contracts/tools/next_run.tool.md`（新）· `src/lib/llm/server/nextrun.go`（新）· `src/lib/llm/server/{turn,inbox,server,domainmcp,plugin_caps}.go` · `src/lib/llm/cmd/next-run-exec/`（新）· `src/lib/llm/servertools/exec/exec.go` · `src/lib/data/{facade/api.go, internal/session/session_nextrun.go}`（新）· `src/lib/data/internal/config/userconfig.go` · `src/frontend/src/views/config/SettingsParamsPage.vue` · `build-mcp-server.ps1`

---

## 1. 问题与目标

### 1.1 要解决的问题

长任务 / 多阶段任务在**无用户在场**时无法自己接着跑：

| 现有机制 | 作用范围 | 缺口 |
|---|---|---|
| `autoContinue`（RETRY-2） | **同一 turn 内**追加「继续」 | 只处理 length 截断 / 断链，不产生新 turn，也不携带新任务指令 |
| INBOX `next_turn` | **用户**排队输入 | 只能由用户 / 前端发起；模型无法主动登记下一轮 |
| 工具循环上限 | 单 turn `maxToolIterations` | 到顶即终态，模型不能"分段交付" |

模型在输出 token 预算即将耗尽（约不足 5%）但任务未完成，或一个阶段已完成而明确还有下一阶段时，需要一个**结构化出口**把"下一轮要做什么"记下来，由系统在本轮结束后自动起新轮。

### 1.2 目标（可验收）

1. 新增 LLM 工具 **`next_run(prompt)`**：同步返回 `OK`，并把 `prompt` 登记为当前会话的**下一条排队输入**（`inbox_items.type=next_turn`）。
2. 当前 turn **正常结束**后，复用既有 `finish → consumeNextTurn` 自动起**新 turn**（新 turn id、`session-turn-start`、`role=user` 消息）。
3. **来源可识别**：inbox 行 `source=llm`、落库消息 `kind=next_run`、`llm-inbox-list` 回带 `source`。
4. **会话级计数器** `sessions.next_run_count`：用户发送即重置为 `0`；每次成功调用 `next_run` +1；`>= nextRunMax` 时返回 `已自动接续 N 次，需用户判断是否继续`，且不再登记。
5. `nextRunMax` **默认 30**、**设置页 UI 可配**；`0` = 关闭（工具不下发 + 调用兜底拒绝）。
6. **同一 turn 内多次调用只保留最后一条**成功受理的提示词（内容覆盖），但每次成功调用都按次 +1。
7. **零新增 MQ 主题**；仅内部数据面可选字段增补，既有事件形状不变。
8. **异常 / 重启不产生"僵尸自动轮"**，计数器持久化、不因重启清零。

### 1.3 非目标

- 不向模型注入"剩余输出 token 预算"（当前系统无该口径；"不足 5%"作为**工具描述的触发判据**，由模型自行判断）。如需真正按预算判定，另立专题。
- 不做跨会话 / 跨实例 / 跨项目的自动调度。
- 不改变 `autoContinue`（同 turn「继续」）的语义、次数与宽限期。
- 不支持子聊天 / 群聊 / `work_session` / `sub_session`（它们由各自调度器驱动，`next_turn` 不会被消费）。

---

## 2. 总体方案

```
turn N（模型输出接近耗尽 / 阶段完成）
   │  LLM tool_call: next_run("下一阶段提示词")
   ▼
runNextRun
   ├─ 校验：主会话 / prompt 非空 / nextRunMax>0
   ├─ sessions.next_run_count 原子 +1（并判上限）
   ├─ inbox_items 写入/覆盖：type=next_turn, source=llm, turn_id=turn N, content=prompt
   └─ 工具结果 = "OK"
   │
   ▼
turn N 正常终态 complete/incomplete
   │  finish → <-tc.done → consumeNextTurn（既有）
   ▼
turn N+1（新 turn id）：落 role=user, kind=next_run 的消息 → Kick → LLM 继续
   │
   └─ 若模型再次 next_run → 计数继续累加，直到 nextRunMax
```

设计要点：

1. **复用而不新造**：排队、消费、合并、落库全部走既有 INBOX `next_turn` + `consumeNextTurn`，不新增主题、不新增队列。
2. **计数器在会话行**：`sessions.next_run_count` 是唯一次数权威，用户发送即归零，天然表达"连续无用户输入的自动接续次数"。
3. **last-wins**：同一 turn 内重复调用只更新同一条 inbox 行（内容覆盖），避免产生多条排队项；计数仍按调用次数累加。
4. **与「继续」正交**：`next_run` 产生**新 turn**（类似排队输入），`autoContinue` 只在**同一 turn** 内追加「继续」。

---

## 3. 详细设计

### 3.1 工具契约

新增 `src/lib/llm/contracts/tools/next_run.tool.md`（文件名 = 工具名；embed 自动纳入）：

```yaml
name: next_run
title: next_run
args: next_run --input={RAW-INPUT-FILE}
async: never
category: server
core-only: true
hot: true
output: stdout
runtime: ../../executors/chonkpilot-next-run-executor.exe
timeout: 0
```

`[摘要]`（给 LLM 的触发判据）：

- 当**输出 token 预算即将耗尽（约不足 5%）但任务尚未完成**，或**一个阶段已完成且明确还有下一阶段**时调用；
- `prompt` = 下一轮要执行的**完整提示词**（自成一体，不依赖本轮未输出的内容）；
- 调用成功返回 `OK`，本轮请直接收尾，不要重复输出下一轮内容；
- 同一轮多次调用以**最后一次**为准；到达连续自动接续上限时返回 `已自动接续 N 次，需用户判断是否继续`，本轮请收尾并提示用户；
- 仅主会话可用。

`[参数]`：

```json
{
  "properties": {
    "prompt": {
      "description": "下一轮要执行的完整提示词（自成一体；包含目标、已知进展、约束与验收标准）",
      "type": "string"
    }
  },
  "required": ["prompt"],
  "type": "object"
}
```

**返回文本（唯一准则）**：

| 场景 | 返回 | isError |
|---|---|---|
| 受理成功（含覆盖上一条） | `OK` | false |
| 到达上限 | `已自动接续 {N} 次，需用户判断是否继续` | false |
| 功能关闭（`nextRunMax<=0`） | `next_run 未启用（自动接续上限为 0）` | false |
| 非主会话 | `next_run 仅支持主会话（子聊天 / 群聊 / 子会话由各自调度器驱动）` | true |
| `prompt` 为空 | `错误: prompt 必填` | true |
| 落库失败 | `错误: 登记下一轮失败（<原因>）` | true |

### 3.2 执行链路（两形态）

| 形态 | 路径 |
|---|---|
| **进程内**（单测 / CLI，未设 `CHONKPILOT_EXTERNAL_TOOLS`） | 契约 embed → `registerDomainTools` 注册进 gateway 工具面；`chatOnce` 工具循环新增 `next_run` **同步分支**（`persistToolRunning` 已落行 → `runNextRun` → `persistToolResult` → 回喂 `role=tool`）；`domainExecute` 增同名 case 作为直调兜底 |
| **进程外**（桌面单体 / server，`CHONKPILOT_EXTERNAL_TOOLS=1`） | core-mcp-server 按 `core-only` 契约注册 → spawn `chonkpilot-next-run-executor.exe`（`src/lib/llm/cmd/next-run-exec`）→ `plugin/v1/next.run` → server 内 `runNextRun` |

与 `ask_user` / `tool_*` / `subchat_*` 同构：执行实体始终在 llm-server（turn 状态唯一所有者），外部进程只做「工具 → 原子能力」适配。

### 3.3 会话级计数器（核心）

#### 3.3.1 存储

`sessions` 行新增可选列 **`next_run_count`（int）**：

- 默认 `0`；**仅 `> 0` 才写键** → 普通会话行形状与既有逐字节一致。
- 会话删除随行删除，无孤儿。
- 随会话持久化 → **重启不清零**。

#### 3.3.2 数据层 API（进程内门面直调，零新增 MQ 主题）

llm-server 已直调 `s.cfg`（如 `SessionGet`），沿用同一方式，在 `facade.SessionAPI` 增：

| 方法 | 语义 |
|---|---|
| `SessionNextRunCheckAndAdd(req{max})` | `UpdateIn` **单事务读改写**：当前 `count >= max` → 返回 `(count, allowed=false)` 且不增；否则 `count+1` 落库并返回 `(count, allowed=true)`。上限判定与自增原子完成 |
| `SessionNextRunReset()` | 置 `0`（单事务；会话不存在 → 幂等成功） |
| `SessionNextRunGet()` | 读当前值（测试 / 诊断） |

> 不改 `facade.Session` DTO、不改 `data-session-get` 载荷形状；计数器读取只经上述方法。

#### 3.3.3 计数规则

| 事件 | 动作 |
|---|---|
| `next_run` **成功受理**（含覆盖上一条） | `CheckAndAdd(nextRunMax)` → +1 |
| 用户 `llm-send{type:text-user}` | `Reset()` |
| 用户 `llm-inbox-add`（`insert` / `next_turn` 均算） | `Reset()` |
| 用户 `llm-inbox-promote` | `Reset()` |
| **外部** `llm-start`（用户发新轮 / 点「继续」） | `Reset()` |
| **内部** `acceptStart`（`consumeNextTurn` 起自动轮） | **不重置** |
| 到达上限 / 参数非法 / 非主会话 / 落库失败 | **不 +1** |

- 实现约束：`onLLMStart` 主体抽成 `accept(req, resetNextRun bool)`；MQ handler 传 `true`，`consumeNextTurn` 传 `false`。
- 到限文案固定：`已自动接续 {N} 次，需用户判断是否继续`（`N` = 本次判定时的上限值）。
- 计数语义 = **自上次用户输入以来，模型成功排期的自动接续次数**；即使自动轮最终未执行（如本轮 error），也保留计数，等用户下一次发送时归零（与用户口径一致）。

### 3.4 来源标识

| 层级 | 字段 | 取值 |
|---|---|---|
| `inbox_items` | `source` | `"llm"`；用户行为不写（缺省 = user） |
| messages | `kind` | `"next_run"`（`role=user`）——`isTurnBoundary` / 压缩侧 `isUserText` 默认把它当真实提问（**新 turn 边界**），无需改判定 |
| `llm-inbox-list` / `data-inbox-list` | items 增 `source?` | 前端可显示「自动接续」标识；服务端重启后回读判定也用它 |

### 3.5 同一 turn 多次调用 = 保留最后一条

- `runNextRun` 在 `turnCtx` 上记录本轮已登记的 inbox 行 id（`nextRunItemID`，仅内存、仅本轮有效）。
- 第二次及以后：先过一次上限判定（**超限时不破坏已登记项**），通过后**就地覆盖同一行的 `content`**（`data-inbox-update` 增可选 `content`），不新增行、不改幂等键（`next-run:<turn>`）。
- 每次成功调用都 `+1`（按用户口径「每调用一次 +1」）。
- 结果：本轮最多只有 1 条 LLM 排队项，内容 = **最后一次**成功受理的 `prompt`。

### 3.6 配置与 UI

| 项 | 结论 |
|---|---|
| 键 | `nextRunMax`（usr 标量，int） |
| 默认 | **30** |
| 语义 | `>0` = 连续自动接续上限；`0`/负数 = **关闭**（工具不下发 + 兜底拒绝） |
| 位置 | 设置 → 参数 → **用户**页签 →「会话」组（与 `autoContinue` 同组） |
| 前端参数 | min 0 / max 1000 / step 1 / `allowZero: true` |
| 镜像 | `SYSTEM_DEFAULTS.nextRunMax='30'` · `configIO.js` 的 `SNAPSHOT_SCALAR_KEYS` · zh-CN/en-US `config.json` · `TestSystemDefaultParamsMirror` 断言 |

### 3.7 与「继续」（autoContinue）的区别

| 维度 | `next_run` | `autoContinue` |
|---|---|---|
| 触发者 | 模型（工具调用） | 系统（length / 断链） |
| 轮次 | **新 turn**（新 turn id，`session-turn-start`） | **同一 turn** 内追加 user 消息（`kind=continue`） |
| 内容 | 模型提供的下一轮提示词 | 固定「继续」文案（`continueText`） |
| 队列 | 走 inbox `next_turn`（排队输入） | 直接喂给当前 turn 的输入队列 |
| 次数 | `next_run_count` / `nextRunMax`（默认 30） | `maxAutoContinue=3` |
| 用户可见 | inbox 项可列（`source=llm`） | 提示条 + 续写消息 |

两者互不影响，可同时开启。

---

## 4. 规则与约定

| 项 | 结论 | 理由 |
|---|---|---|
| 只支持主会话 | `parents` 空且会话 `kind==""` | 子聊天 / 群聊 / 子会话由各自调度器驱动，`next_turn` 不会被消费 |
| 受理顺序 | 先计数 `CheckAndAdd`，再写 inbox；写失败补偿 `-1` | 上限判定与自增原子，避免并发绕过 |
| last-wins 边界 | 仅覆盖**本轮**登记项；超限调用不影响已登记项 | 保证「最后一条成功受理」语义 |
| 终态收敛 | 本轮 `error` / `interrupted` / `cancelled` → 丢弃**本 turn** 的 LLM 排队项 | 防止下一轮用户结束后"僵尸提示词"自动触发 |
| 失效判定 | LLM 项的 `turn_id` 必须等于会话**最新 turn**，否则 `discarded` | 崩溃 / 被用户新轮抢占后不复活 |
| 来源标记 | inbox `source=llm` + 消息 `kind=next_run` | 历史 / 快照 / 前端可识别「非用户输入」 |
| 消息面 | **零新增主题**；仅 `data-inbox-append/list/update`、`llm-inbox-list` 可选字段 | 降低契约冻结面 |
| 工具面 | `nextRunMax=0` → 不下发该工具 | 关闭即不可见，减少工具槽位占用 |

---

## 5. 与既有机制的关系

### 5.1 消息面（61-消息一览 / schema）

| 主题 | 变更 |
|---|---|
| `data-inbox-append` | payload 增可选 `source`（`"llm"`；缺省不写 = user） |
| `data-inbox-update` | payload 增可选 `content`（last-wins 覆盖） |
| `data-inbox-list` | result.items 增可选 `source` |
| `llm-inbox-list` | result.items 增可选 `source` |

**无新增 / 删除主题**；`llm-start` / `llm-send` / `session-*` 形状不变。

### 5.2 数据层

- `sessions` 行可选列 `next_run_count`（写入 `src/lib/data/internal/session/session_nextrun.go`）。
- 新增门面方法（§3.3.2），不新增数据面消息。

### 5.3 模块 / 工具面

- [21-llm-server](../spec/20-modules/21-llm-server.md)：域工具 **5 → 6**（新增 `next_run`，`category=server` / `async=never` / `timeout=0` / `hot=true` / `core-only=true`）。
- [3A-工具与编排](../spec/30-function-points/3A-工具与编排.md)：新增工具功能点与场景（正常 / 到限 / 关闭 / 非主会话 / 覆盖 / 失败）。
- [03-构建与部署](../spec/00-overview/03-构建与部署.md)：域工具 executor **5 → 6**（新增 `chonkpilot-next-run-executor.exe`，`build-mcp-server.ps1` 产出）。
- [64-配置项一览](../spec/60-reference/64-配置项一览.md)：新增 `nextRunMax`（默认 30，`0` = 关闭）。
- [22-data-persist](../spec/20-modules/22-data-persist.md)：`sessions` 数据字典增 `next_run_count`。

---

## 6. 边界、异常与重启恢复

### 6.1 边界与异常

| 场景 | 行为 |
|---|---|
| 同一 turn 多次调用 | 计数逐次 +1；inbox 仅 1 行，内容 = 最后一次成功受理 |
| 第 N 次调用已达上限 | 返回 `已自动接续 N 次，需用户判断是否继续`；不覆盖、不新增、不 +1 |
| `nextRunMax=0` | 工具不下发；若被幻觉调用 → `next_run 未启用（自动接续上限为 0）` |
| 非主会话 / 子会话 | 拒绝（isError=true），不计数、不登记 |
| 用户插话（insert） | 计数归零；插话本身正常消费，LLM 项留在 inbox 等本轮结束 |
| 用户中途另起新轮 | 计数归零；旧 LLM 项 `turn_id != 最新 turn` → `discarded` |
| 本轮 error / interrupted / cancelled | 异步丢弃本 turn 的 LLM 项；计数保留，等用户下次发送归零 |
| inbox 落库 / 覆盖失败 | 补偿 `-1`，返回错误文本（模型可重试） |
| inbox pending 上限（50） | 落库失败 → 错误文本；计数补偿 |
| 多个排队项（用户 + LLM） | 沿用 `consumeNextTurn`：同一批合并为**一次**新 turn 请求 |
| 会话被删除 | 计数器随行删除；在飞 turn 自然失败 |

### 6.2 重启恢复（按 [71 §3.3](../spec/70-conventions/71-编程规范.md) 五问）

1. **状态落在哪**：计数 = `sessions.next_run_count`（持久化）；待执行提示词 = `inbox_items`（`pending`）；`nextRunItemID` 仅内存且只在本轮有效。崩溃最多丢「尚未落库的那一次调用」。
2. **受理即持久化**：`next_run` 先计数、再落 inbox（pending），成功后才返回 `OK`；不使用内存队列承载。
3. **非终态收口**：崩溃时 running turn 由既有 `data-session-cleanup-stale` 标 `interrupted`；其 LLM 排队项通过「`turn_id` 必须等于最新 turn」在下次消费时判为过期并 `discarded`，**不会自动复活**。
4. **重启后重建还是收敛**：不重建自动链、不主动起轮（沿用 inbox「需用户动作」语义）；计数保留，用户下一次发送归零。
5. **副作用语义**：at-least-once（先落项、后置 `consumed`）；幂等键 `next-run:<turn>`；重复回执 / 重放最多多发一次提示词，不产生重复消息。

---

## 7. 落地清单

| 步骤 | 内容 | 状态 | 关联代码 |
|---|---|:---:|---|
| 1 | 数据层：`sessions.next_run_count` + `SessionNextRunCheckAndAdd/Reset/Get`（单事务 RMW） | 🔵 | `src/lib/data/facade/api.go` · `src/lib/data/internal/session/session_nextrun.go` |
| 2 | 数据面字段：`data-inbox-append/list` 的 `source`、`data-inbox-update` 的 `content` | 🔵 | `src/lib/data/{facade/inbox.go,persist/inbox.go,internal/session/inbox_facade.go}` |
| 3 | 契约：`next_run.tool.md`（名字 / 参数 / 摘要 / 返回文案唯一准则） | 🔵 | `src/lib/llm/contracts/tools/next_run.tool.md` |
| 4 | server：`runNextRun`（校验 / 计数 / last-wins / 来源 / 到限文案） | 🔵 | `src/lib/llm/server/nextrun.go` |
| 5 | server 接线：`chatOnce` 同步分支 · `domainExecute` 兜底 · 用户输入重置 · 外部 `llm-start` 重置 · 终态丢弃 | 🔵 | `src/lib/llm/server/{turn,server,domainmcp}.go` |
| 6 | 消费收敛：`consumeNextTurn` 读 `source` + `turn_id` 失效判定；落消息 `kind=next_run` | 🔵 | `src/lib/llm/server/inbox.go` |
| 7 | 进程外：executor + 插件桥能力 `plugin/v1/next.run` + 构建脚本 | 🔵 | `src/lib/llm/cmd/next-run-exec/` · `src/lib/llm/servertools/exec/exec.go` · `src/lib/llm/server/plugin_caps.go` · `build-mcp-server.ps1` |
| 8 | 配置：`nextRunMax` 默认 30 + 设置页 UI + i18n + `configIO` + 镜像测试 | 🔵 | `src/lib/data/internal/config/userconfig.go` · `SettingsParamsPage.vue` · `configIO.js` · locales |
| 9 | 文档同步：21 / 22 / 3A / 61（md + schema）/ 64 / 03 | 🔵 | `docs/spec/**` |
| 10 | 测试：L1（计数器原子 / mq 驱动的 next_run 全矩阵）+ L2（`self_next_run` 注册 / 默认值镜像） | 🔵 | `src/lib/llm/server/next_run_test.go` · `src/lib/data/internal/session/session_nextrun_test.go` · `src/test/chonkpilot-llm/unittest/llm_test.go` |
| 11 | 收尾构建：`.\build-desktop.ps1` + `.\build-mcp-server.ps1` | 🔵 | 构建脚本 |

### 7.1 测试用例（L1，mq 驱动 + mq 断言）

1. 正常：`next_run` → 工具结果 `OK` → 本轮 `session-complete` → 自动 `session-turn-start`（turn 变化）→ 新轮 user 消息 `kind=next_run`；`next_run_count=1`。
2. 上限：`nextRunMax=2` → 自动新轮恰 2 次；第 3 次返回 `已自动接续 2 次，需用户判断是否继续`，不再起轮。
3. 用户发送重置：到 2 后用户插话 / 新轮 → 归零，之后可再用 2 次。
4. last-wins：同轮两次 `next_run` → inbox 仅 1 条 pending，内容 = 第二条，计数 = 2。
5. 终态收敛：本轮 `error` / `cancel` → 本 turn 的 LLM 项 `discarded`。
6. 抢占失效：pending LLM 项 `turn_id != 最新 turn` → `discarded`，不触发新轮。
7. 非主会话：子聊天 / 群聊调用 → 拒绝、不计数、不登记。
8. 关闭：`nextRunMax=0` → 工具不下发（工具面断言）+ 直调返回未启用文案。

---

## 8. 关联文档

- [49-待实现项](../spec/40-roadmap/49-待实现项.md)（`NEXTRUN-1`）
- [21-llm-server](../spec/20-modules/21-llm-server.md) · [22-data-persist](../spec/20-modules/22-data-persist.md)
- [3A-工具与编排](../spec/30-function-points/3A-工具与编排.md) · [33-对话](../spec/30-function-points/33-对话.md)（如需前端标识）
- [61-消息一览](../spec/60-reference/61-消息一览.md) + [61-messages.schema.json](../spec/60-reference/61-messages.schema.json)
- [64-配置项一览](../spec/60-reference/64-配置项一览.md)
- [中途插话设计方案](../agent-chat/中途插话设计方案.md)（INBOX `next_turn` 的既有语义）
- [LLM重试分层设计方案](../llm-retry/LLM重试分层设计方案.md)（`autoContinue`，与 `next_run` 正交）
