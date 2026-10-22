# LLM 重试分层设计方案（独立稿）

> 状态：✅ **已实施（2026-10-20；router 报文级重试 + llm 续写/提示条 + 配置 UI + 61/64 同步 + 测试全绿）**
> 日期：2026-10-19（定案）· 2026-10-20（实施）
> 关联（spec）：[49-待实现项 §2](../spec/40-roadmap/49-待实现项.md)（登记项 `RETRY-2`）· [2D-router](../spec/20-modules/2D-router.md) · [21-llm-server](../spec/20-modules/21-llm-server.md) · [61-消息一览 §4.3](../spec/60-reference/61-消息一览.md) · [64-配置项一览](../spec/60-reference/64-配置项一览.md) · [35-错误处理与恢复](../spec/30-function-points/35-错误处理与恢复.md) · [RETRY-1](../spec/40-roadmap/49-待实现项.md)（可见重试，已实施）
> 代码目录：`src/lib/router/`（重试循环 + 策略端口）· `src/lib/llm/server/`（次数配置 + 上报 + 自动续写）· `src/frontend/src/views/chat/ChatPanel.vue`（提示条）· `src/frontend/src/views/config/SettingsParamsPage.vue`（配置 UI）
>
> **spec 已同步（2026-10-20）**：2D-router / 21-llm-server / 61 §§4.3 / 64 / 49 随代码一并落。

---

## 1. 问题与目标

### 1.1 要解决的问题

当前重试逻辑**分散在两层且边界模糊**：

| 现象 | 位置 | 问题 |
|------|------|------|
| 透明重发 ≤1 次（仅 `network`） | `src/lib/router/retry.go` | 次数上限硬编码，连接类失败无法多次重试 |
| 可见重试 `retryCount` | `src/lib/llm/server/turn.go` | 与 router 各持一套次数，存在乘积放大风险 |
| 自动续写 `maxAutoContinue` | `src/lib/llm/server/turn.go` | 与重试混在同一循环，语义未分离 |
| 重试判据 | `turn.go`：`out.Len() == 0` | 判据在 llm 层，router 内部的「已输出」信息未利用 |

同时 `canon.Error` 只有 `Kind`，**无法区分「连接从未建立」与「已连接后流中断」**，导致两类失败只能共用同一策略。

### 1.2 目标（可验收）

1. **判据唯一**：`Emitted == 0`（已向上层输出的事件数为 0）→ router 重试；`Emitted > 0` → router 永久退出，交 llm/chat 处理。
2. **router 只做报文级重试**：同一请求原样重发，不改变请求体、不插入对话消息。
3. **两类报文级失败分流**：
   - 连接类（`dial` / `first_byte`）：可重试至配置上限（默认 **5** 次），指数退避；
   - 断连类（`stream` 中断且 `Emitted == 0`）：**最多 1 次**。
4. **次数单一所有者**：`retryCount` 归 router；llm 层重试循环删除，不再二次计数。
5. **可见重试**：重试期间前端显示「错误，正在重试 n/N（等待 s 秒）」，复用 RETRY-1 的 `llm-retry` 消息与提示条（`session-retry` 形状不变）。
6. **语义级续写**：`Emitted > 0` 后中断或 `finish_reason=length` → 立即发**新增** `session-interrupted`（前端 `llm-interrupted`），前端显示提示条「服务器错误，是否继续。」+「继续」**链接**；同时后端启动 `autoContinueDelaySec`（默认 600s = 10 分钟）宽限期，**用户仍在原会话且无任何操作**才在**同一 turn 内追加**一条续写消息（默认「继续」），**不填输入框、不结束 turn**；**每次中断最多自动续写 1 次，续写后再失败不再自动写**；报文级重试耗尽**不续写**。
7. **消息面唯一新增**：`session-interrupted`（前端 `llm-interrupted`，payload = `{session_id, turn_id, code, message, retryable, auto_continue_delay_ms}`）；`session-retry` / `session-complete` / `llm-error` **形状零变更**。新增主题**须按 [61 §0](../spec/60-reference/61-消息一览.md) 登记**。

### 1.3 非目标

- 不做跨 provider 降级 / 智能调度（归后续 scheduler 层）。
- 不做「已输出内容的去重续写」（即从半个 token 继续），只做「追加一条续写消息」。
- 不在消息面暴露失败阶段（`Phase` 仅内部使用；如后续需要显示原因，另走 61 确认）。
- 不改变 `llm-simple`（无 session/turn 的一次来回）的重试语义。

---

## 2. 总体方案

```
                    Emitted == 0                      Emitted > 0
失败 ──┬──────────────────────────────────┬──────────────────────────────
       │                                  │
       ▼                                  ▼
┌──────────────────────┐        ┌──────────────────────────────┐
│ router（报文级重试）  │        │ llm/chat（语义级）            │
│ · dial   ≤ retryCount│        │ · 发 session-interrupted      │
│ · first_byte ≤ 同上  │        │   （提示条：服务器错误，是否继续）│
│ · stream 断连 ≤ 1 次 │        │ · 10 分钟无操作 → 自动「继续」 │
│ · 429/5xx 按策略     │        │ · 用户操作/切会话 → 取消自动   │
└──────────┬───────────┘        └──────────────────────────────┘
           │ RetryObserver（非阻塞回调）
           ▼
    llm-server → publish `llm-retry`（既有主题/payload）
           ▼
    ChatPanel 提示条「错误，正在重试 n/N（等待 s 秒）」
```

一句话：**router 负责「还能不能再发一次」，chat 负责「要不要告诉用户、要不要再多花一次 token」。**

---

## 3. 详细设计

### 3.1 判据：`Emitted`

`Emitted` = 一次 `Call` 内已成功写入上层事件通道的事件数（`content` / `reasoning` / `tool_call` 增量均计）。

- `Emitted == 0`：可重发（上层无感知，不会重复内容）；
- `Emitted > 0`：**永久禁止重发**（已可见内容不可撤回）；
- 判据由 router **自行计数**，不接受上游传入（避免口径漂移）。

> 对齐现状：`retry.go` 的 `attemptOnce` 已返回 `emitted`，本次只是把它上升为唯一判据并用于两类分流。

### 3.2 失败阶段：`Error.Phase`

`canon.Error` 新增内部字段：

```go
type Phase string

const (
    PhaseDial      Phase = "dial"       // DNS / TCP / TLS 未建立
    PhaseFirstByte Phase = "first_byte" // 请求已发出，未收到首字节（含首字节超时）
    PhaseStream    Phase = "stream"     // 已开始收流（已收到响应头/首字节）
)
```

落点：`src/lib/router/internal/canon/error.go`（定义）+ 适配器（`internal/adaptor/**`）在错误构造处赋值。该字段**不进消息面**，仅用于重试分流。

### 3.3 重试矩阵

| 阶段 | Emitted | 是否重试 | 次数上限 | 退避 |
|------|---------|----------|----------|------|
| `dial`（连接未建立） | 0 | ✅ | `retryCount`（默认 5） | 预算归一化（见下） |
| `first_byte`（首字节超时 / 无响应） | 0 | ✅ | `retryCount` | 同上 |
| `stream`（断连 / 半包 / EOF） | 0 | ✅ | **1**（硬帽，占用 `retryCount` 名额） | 同上 |
| `stream` | > 0 | ❌ | — | 交 §3.5 续写 |
| `rate_limit`（429） | 0 | ✅ | `retryCount` | `Retry-After` 优先，否则指数退避 |
| `server`（5xx） | 0 | ✅ | `retryCount` | 指数退避 |
| `auth` / `protocol` / `invalid` | 任意 | ❌ | — | 直接终态 |

**总预算与退避曲线（指数权重归一化）**：新增 `retryBudgetSec`（默认 60s，可配）。

预算**不是闸门，而是曲线的总量**：按 2 倍指数的**权重**把预算分配到每一次等待，使 `retryCount` 次等待之和恰好等于预算：

```
N = MaxRetries（默认 5 = retryCount，**不含首次请求** → 总请求 ≤ 6 次）
weights[i] = 2^(i-1)        // 1, 2, 4, 8, 16
S = Σ weights[1..N]         // 31
k = Budget / S              // 60 / 31 ≈ 1.94s
wait[i] = k * weights[i]    // 1.9s, 3.9s, 7.7s, 15.5s, 31.0s → 合计 60s
```

- **起算点** = 首次可重试失败发生时；`k` 在首次失败时按**初始预算**计算一次，后续**不重算**；
- 只累加**退避等待**，不含请求执行时长（连接建立、首字节等待、流传输）；
- **`Retry-After` 优先**：`wait[i] = Retry-After`（>0 时），从同一预算**扣减**；若 `Retry-After > remainingBudget` → 不重试，直接终态；
- **不设单次等待上限**（用户 2026-10-19 明确）；仅当 `retryBudgetSec <= 0`（显式关闭预算）时回退到固定 `1s·2s·4s·8s·16s`；
- 次数与预算**先到者停**；
- **`N` 会影响单次等待**：`N` 越小、单次越长（如 `N=3` → `k = 60/7 ≈ 8.6s`，三次为 8.6 / 17.1 / 34.3s）。这是配置者可见的取舍。

**不采用算术均分（`Budget / N`）**：那会让曲线变成等距，失去指数退避的意义；本方案是**按指数权重归一化**，形状不变、总量可控。

**禁止叠加**：断连重发**占用一次重试名额**（与连接类共用 `retryCount`），同时**自身另有「最多 1 次」的硬帽**——即总重试次数 ≤ `retryCount`（总请求 ≤ `retryCount + 1`），且因断连最多只多试 1 次。

**决策顺序（避免 `Kind` 与 `Phase` 冲突）**：先按 `Kind` 判定（`auth` / `protocol` / `invalid` 直接终态；`rate_limit` / `server` 按次数重试）；`Kind` 为 `network` / `timeout` 时再按 `Phase` 分流（`dial` / `first_byte` → `retryCount`；`stream` 且 `Emitted == 0` → 最多 1 次）。

### 3.3.1 与现有超时的关系（不改）

- `responseTimeout`（首字节 120s）与 `streamTimeout`（chunk 间隔 60s）**语义完全不变**，本专题不触碰；
- 本专题只改两件事：**退避合计总长**（新增 `retryBudgetSec`）与**重试次数**（`retryCount` 原已有，仅改默认值与归属）；
- 超时产生的 `timeout` 错误照常进入重试矩阵，按 `Emitted` 分流；
- **未传 `MaxRetries` 的调用方**（`llm-simple` / `prompt-optimise` / `llmOnceSpec`）**行为不变**：仍只有现有 `network` 透明重发 1 次，不启用配置化的 5 次重试与预算；`MaxRetries` 用**指针**正是为了区分「未设置」与「显式 0（不重试）」。

### 3.4 router 侧端口

```go
// 策略（纯函数；nil = DefaultPolicy）
type RetryPolicy interface {
    Decide(ctx context.Context, rc RetryContext) (retry bool, wait time.Duration)
}

type RetryContext struct {
    Attempt int           // 第几次（从 1 起）
    Phase   Phase
    Err     *Error
    Emitted int
    Budget  time.Duration // 剩余总预算
}

// 上报（非阻塞；nil = 不上报）
type RetryObserver interface {
    OnRetry(ctx context.Context, rc RetryContext, wait time.Duration)
    OnExhausted(ctx context.Context, rc RetryContext, err *Error)
}

// Call 选项新增
type CallOptions struct {
    // ...既有字段不变
    MaxRetries *int           // nil = **保持现状**（仅 `network` 透明重发 1 次，保 llm-simple 语义）；
                              // &0 = 明确不重试（配置 retryCount=0）；&n = 重试 n 次（不含首次请求）
    Budget      time.Duration // 0 → 默认 60s；仅在设置了 MaxRetries 时生效
    Policy      RetryPolicy
    Observer    RetryObserver
    Meta        map[string]any // 宿主透传（session/turn），router 不解释
}
```

**约束**：

1. `Observer` 必须**非阻塞**（实现方自行 `go` 出去或带 ctx 超时）；router 不等待其返回。
2. `Observer` 失败（panic / 超时）**不得影响**重试决策；router 内部 recover 并忽略。
3. router 仍**不认识** session/turn，上下文只经 `Meta` / `ctx` 透传。
4. `DefaultPolicy` 计算退避：`Retry-After` 优先 → 预算归一化指数（`k × 2^(i-1)`）；无预算时回退固定指数；**不设单次上限**。

### 3.5 语义级续写（chat/llm）

**触发**：

- `Emitted > 0` 且流中断 / 半包；
- `finish_reason=length`（含现有 `incomplete` 路径）；
- **不触发**：
  - 报文级重试**耗尽**（终态错误，不再续写 —— 用户 2026-10-19 明确）；
  - 完整响应但 content 为空（`EMPTY_REPLY`，属独立语义）。

**行为**：

1. **不结束当前 turn**（不发 `session-complete`），在**同一 turn 内**继续；
2. **后端直接追加**一条 `user` 消息并继续 step 循环（不等前端、不填输入框、不产生草稿）：
   - 落一条 `user` 消息（`kind=text`，不新增 `Kind` 枚举，避免压缩口径变更）；
   - 消息的 `turn` = **当前 turn**（与插话一致：不新开 turn）；
3. 文案可配：`继续` / `continue` / `完成了吗`（用户级配置，默认「继续」）；
4. **中断即显示提示条**「服务器错误，是否继续。」——「继续」为**链接**，点击 = **模拟发送一条用户消息**（内容 = `continueText`，走正常输入路径、落库，等同于用户手动发送）；
5. **延迟触发**：中断后等 `autoContinueDelaySec`（默认 **600s = 10 分钟**）作为宽限期；到期**用户仍在该会话且无任何操作** → 自动追加 1 次；
6. **取消自动续写（限定 session 内）**：
   - 用户在该 session 发送**任意消息**（含手动输入「继续」、点击提示条「继续」链接）→ 取消；
   - **关闭该会话窗口 / 切换到其他会话** → 取消；
   - 上述操作**只取消自动续写**，**提示条「服务器错误，是否继续。」保留**（回到该会话仍可点链接手动继续）；
   - 其他 session 的操作**不影响**本 session 的自动续写；
7. **手动继续不受限**（用户输入 / 点击链接不检查宽限期与次数）；
8. **每次中断最多自动续写 1 次**；自动续写后又失败 → **不再自动写**（终态报错，等用户手动继续）；`maxAutoContinue`（现有 = 3）保留为同一 turn 内的硬上限兜底。
9. **开关关闭（`autoContinue=false`）**：中断即终态（发 `session-complete{status:error}`），**不进入宽限期**；提示条**照旧显示**「服务器错误，是否继续。」+ 链接，点击 = 发送 `continueText` 作为新消息。

> **「仍在原会话」的判定（复用既有消息，零新增）**：到期时读既有 `active_session_id`（`data-session-active-set` / `data-session-active-get`）——不等于本 session → **不自动续写**；前端切会话写该键、关窗置空即可满足「切会话/关窗取消」。

**展示**：

- 中断瞬间：发 `session-interrupted` → 前端提示条「服务器错误，是否继续。」+「继续」链接（**不结束 turn**，输入框可继续输入）；
- 续写消息**落库**，因此下次加载历史可见；实时上屏沿用既有 `session-receive`（不新增消息）；
- 点击链接 = 前端按正常发送路径发一条 `continueText` 用户消息（落库 + 取消后端自动续写）。

### 3.6 UI 呈现

| 场景 | 位置 | 文案 |
|------|------|------|
| 报文级重试中 | 聊天区底部、输入框上方（既有 `.llm-fail-hint` 位） | `错误，正在重试 2/5（3 秒后）` |
| 重试成功 | 同上 | 提示条消失（不残留） |
| 重试耗尽 | 同上 | 终态错误 + 「已重试 n/N 次」+「查看错误信息」（ERRUX-1 既有形态） |
| 已输出后中断（宽限期内） | 聊天区底部、输入框上方（既有 `.llm-fail-hint` 位） | **`服务器错误，是否继续。`** + 「继续」**链接**（点击 = 模拟发送 `continueText`）；到期无操作 → 自动发送 1 次 |
| 已输出后中断（已自动续写 / 已取消） | 同上 | 提示条保留，链接仍可点；**自动续写不再触发** |
| 报文级重试耗尽 | 同上（提示条） | 终态错误；**不续写** |

延迟显示：首次重试若 <1s 内成功则不显示（避免闪烁）。

### 3.7 配置 UI（LLM Router）

现有 `SettingsParamsPage.vue` 的「超时重试」分组（`responseTimeout` / `streamTimeout` / `retryCount`）与「服务参数」（`timeout_sec` / `max_concurrency` / `skip_dirs`）混在同一页。本次收敛为**独立的 LLM Router 配置组**：

| 配置组 | 键 | 默认 | 迁移 |
|---|---|---|---|
| **LLM Router**（新） | `responseTimeout` | 120s | 从「超时重试」迁入 |
| | `streamTimeout` | 60s | 迁入 |
| | `retryCount` | **5**（原 2） | 迁入 + 默认值变更 |
| | `retryBudgetSec` | **60s** | **新增** |
| **会话**（同一「参数」页的**用户页签**，新增分组） | `autoContinue` / `autoContinueDelaySec` / `continueText` | true / **600** / 「继续」 | **新增**（概念上是系统级参数，系统级无编辑入口，因此落用户级个人偏好） |
| 服务参数（不动） | `timeout_sec` / `max_concurrency` / `skip_dirs` | — | 保持原位（MCP 服务能力，不属于 router） |

**不新增页面**：两类配置都放在现有 `SettingsParamsPage.vue` 的**用户页签**，页内分成「LLM Router」与「会话」两个分组。

**前端改动清单**：

1. `SettingsParamsPage.vue`：拆出 `routerFields`（4 项）与 `sessionFields`（3 项）；`SYSTEM_DEFAULTS` 增 `retryBudgetSec: '60'`、`retryCount` 改 `'5'`、`autoContinueDelaySec: '600'`；
2. i18n（`zh-CN` / `en-US` 的 `config.json`）：`config.llmRouter`、`config.session`、`config.retryBudgetSec`、`config.autoContinue*`；
3. `configIO.js`：键白名单增 `retryBudgetSec` 与续写三项；
4. 校验：`retryBudgetSec` 正整数、`retryCount` 允许 0（沿用 `allowZero`）、`autoContinueDelaySec` 非负整数；
5. 系统默认镜像测试 `TestSystemDefaultParamsMirror` 同步（后端 `userconfig.go` + 前端 `SYSTEM_DEFAULTS`）。

**已定（UI 形态）**：A —— 在「设置 → 参数」的**用户页签**内新增两个分组（「LLM Router」与「会话」），**不新增页面、不动项目级页签**。

---

## 4. 规则与约定

| 项 | 结论 | 理由 |
|----|------|------|
| 重试判据 | `Emitted == 0` | 已输出内容不可撤回，重发会造成重复 |
| router 职责 | 仅报文级（原样重发） | 不改变请求体、不插入对话消息 |
| 次数所有者 | 单一：`retryCount` 归 router | 双层各算会叠成乘积 |
| 断连次数 | 最多 1 次，且计入总重试次数 | 断连重复发有重复计费风险 |
| 总预算 | 默认 60s，按 2 倍指数权重归一化分配到各次等待（5 次合计 = 预算）；次数与预算先到者停 | 曲线形状不变、总量可控 |
| 退避 | `Retry-After` 优先 → 预算归一化（`k × 2^(i-1)`）；无预算回退固定指数、不设单次上限 | 与现状的差异 = 曲线按预算缩放 |
| 已输出后 | 永不重发，只续写 | 续写是语义操作，需对话上下文 |
| 重试耗尽 | 不续写，直接终态 | 传输层已不可用，续写没有意义（用户 2026-10-19） |
| 续写形态 | **同一 turn 内后端直接追加**（落 user 消息，`kind=text`），不填输入框 | 用户不需确认；由后端保证上下文连续 |
| 续写触发 | **中断后等 10 分钟**（`autoContinueDelaySec`），用户无操作才自动发 **1 次**；再失败即停 | 给用户自己发「继续」的宽限期；也避免离席时任务干挂 |
| 续写限次 | **每次中断最多 1 次**；`maxAutoContinue`（3）为同 turn 硬上限 | 自动续写再失败说明链路不可用，继续写没有意义 |
| 中断提示 | 提示条「服务器错误，是否继续。」+「继续」**链接**（点击 = 模拟发送 `continueText`） | 用户始终有手动出口；不预填输入框 |
| 取消自动续写 | **session 内**：发任意消息（含点链接/手动「继续」）· 关窗 · 切会话 → 取消；**提示条保留** | 只取消"自动"，不剥夺手动入口；其他 session 不受影响 |
| 消息面 | **仅新增 `session-interrupted`**（须 61 §0 登记）；`session-retry` / `session-complete` 形状不变 | 提示条需要一个「已中断、等待继续」的信号；不能复用"重试中" |
| 失败阶段 | 仅内部 `Error.Phase` | 前端不需要区分原因，避免冻结契约变更 |

---

## 5. 与既有机制的关系

### 5.1 消息面（61）

- **新增 1 条**：`session-interrupted` → 前端 `llm-interrupted`，payload `{session_id, turn_id, code, message, retryable, auto_continue_delay_ms}`；由 llm 层在「`Emitted > 0` 中断 / `finish_reason=length`」时发布（**同一 turn 不结束**）；**须按 61 §0 登记**。
- `session-retry`（前端 `llm-retry`）`retryable / retry_attempt / retry_count / retry_wait_ms` **零变更**；
- `retry_count` 的**取值来源**由「llm 层 `retryCount`」改为「router `MaxRetries`（同源于配置）」，字段语义（允许上限）不变；
- `session-complete{status:error}` 的 `retry_attempt` / `retry_count` 同样改由 router 结果回填，形状不变；**续写场景不再发它**（turn 未结束）。
- 前端收到 `llm-interrupted` → 显示提示条 + 链接；收到该 session 的后续 `llm-receive` / `llm-complete` → 清除提示条。

### 5.2 配置（64）

| 键 | 现状 | 变更 |
|----|------|------|
| `retryCount` | llm 层可见重试次数（默认 2） | 语义改为 **router 报文级重试次数**（不含首次请求；默认 **5**） |
| `retryBudgetSec` | — | **新增**，退避合计总长（默认 60s）；按 2 倍指数权重归一化分配到各次等待 |
| `autoContinue` | — | **新增**，后端自动续写开关（默认 **true**） |
| `autoContinueDelaySec` | — | **新增**，中断后等待用户手动继续的宽限期秒（默认 **600**）；到期无操作才自动续写 |
| `continueText` | — | **新增**，续写文案（默认「继续」；可配 `continue` / `完成了吗`） |
| `maxAutoContinue` | 常量 3 | 保留为**同一 turn 内的硬上限** |

**默认值迁移**：`retryCount` 的默认值只影响**未显式配置**的用户（新装 / 键缺失）；已存在的 `usr` 配置值（如 2）**保持原值，不强制迁移**——避免静默改变用户已确认的行为。

### 5.3 代码落点

| 文件 | 变更 |
|------|------|
| `src/lib/router/internal/canon/error.go` | `Error.Phase` |
| `src/lib/router/internal/adaptor/**` | 错误构造处赋 `Phase` |
| `src/lib/router/retry.go` | 判据改为 `Emitted`、两类分流、预算、Policy/Observer |
| `src/lib/router/types.go` / `router.go` | `CallOptions` 新字段 |
| `src/lib/llm/server/turn.go` | 删除可见重试循环与 `probeBeforeRetry`（TCP 预检由 router 的 `dial` 分流取代）；`retryCount` 传入 router；Observer → `emitRetry` |
| `src/lib/llm/server/routercall.go` | 事件折算处接 `Meta` / Observer |
| `src/lib/llm/server/server.go` | 配置读取（`retryBudgetSec` / `autoContinue` / `autoContinueDelaySec` / `continueText`）；`llmRetryCount` 回落常量 2 → **5**；发布 `session-interrupted` + 宽限期计时 / 活动会话校验 |
| `src/lib/data/internal/config/userconfig.go` | 系统默认 `retryCount` 2 → **5**（与前端 `SYSTEM_DEFAULTS` 镜像，`TestSystemDefaultParamsMirror` 同步） |
| `src/frontend/src/views/chat/ChatPanel.vue` | 提示条「服务器错误，是否继续。」+「继续」链接（点击 = 发送 `continueText`）；收到 `llm-interrupted` 显示、收到后续 `llm-receive` / `llm-complete` 清除 |

### 5.4 与 RETRY-1 的关系

RETRY-1（可见重试）已实施，提供 `llm-retry` 主题与前端提示条。本专题**复用**其重试消息与提示条，只把「谁来决定重试、次数从哪来、退避在哪算」从 llm 层迁到 router，并新增**中断提示消息** `session-interrupted` 与续写分流。

---

## 6. 边界、异常与重启恢复

### 6.1 边界与异常

| 场景 | 处理 |
|------|------|
| `Observer` 阻塞 / panic | router recover + 忽略；不影响重试 |
| 重试期间用户取消（`llm-cancel`） | 退避等待可被 ctx 中断（沿用 `sleepCtx`） |
| `Retry-After` 超过总预算 | 不等待，直接终态（避免长挂起） |
| 429 与 5xx 混合出现 | 每次按当次错误重新决策，共享同一 attempt 预算 |
| 多模态 / 工具调用进行中 | 重试只作用于当前 step，不影响已派发的工具任务 |
| 半包工具调用（`Emitted > 0` 且 `tool_call` 参数未收完） | **不派发**（丢弃未完成的 `tool_call`），按 §3.5 走续写 |
| 续写后又失败 | 每次中断只自动续写 1 次；再次失败即终态（手动继续不受限） |
| 空回复（`EMPTY_REPLY`） | 不重试、不续写，按既有语义提示 |
| 适配器未赋 `Phase` | 回落 `PhaseStream`（保守：不放大重试） |

### 6.2 重启恢复

1. **状态落在哪**：重试预算、attempt 计数、`Emitted` 全部在**单次 `Call` 的内存**中，无新增持久化状态；续写宽限期计时器与同 turn 计数随 `turnCtx` 在内存，重启后该 turn 收口即失效。
2. **输入是否受理即持久化**：自动续写由**后端直接发送**，发送即落 `user` 消息（`kind=text`），不存在"草稿未发送"状态；**中断提示条不落新状态**——由已落库的 assistant 部分消息 + 终态错误派生，重启后照常显示，可手动点「继续」。
3. **非终态谁收口**：进程崩溃时该 step 随 turn 终止；turn 的非终态由既有 sweep / `reapInflight` 收口，本次**不新增**非终态。**重启后自动续写不再进行**（计时器随内存消失），仅保留手动入口。
4. **重启后重建还是收敛**：收敛。重试不跨进程续跑（重试是传输层瞬时行为，恢复后重新发起新一轮请求即可）。
5. **副作用语义**：报文级重发对 LLM 请求为「至多一次可见输出」，但**可能重复计费**（上游已生成、连接中断）；因此断连严格限制 1 次、总预算封顶。

---

## 7. 落地清单

| 步骤 | 内容 | 状态 | 关联代码 |
|------|------|:---:|---------|
| 1 | `canon.Error` 增 `Phase`，适配器赋值 | 🔵 | `src/lib/router/internal/canon/error.go` |
| 2 | `CallOptions` 增 `MaxRetries` / `Budget` / `Policy` / `Observer` / `Meta` | 🔵 | `src/lib/router/types.go` |
| 3 | 重试循环改造（`Emitted` 判据 + 两类分流 + 预算 + 非阻塞回调） | 🔵 | `src/lib/router/retry.go` |
| 4 | llm 层删除重试循环与 `probeBeforeRetry`，传次数 + Observer → `emitRetry` | 🔵 | `src/lib/llm/server/turn.go`、`routercall.go` |
| 5 | 配置读取：`retryCount`（默认 5）· `retryBudgetSec` · `autoContinue` · `autoContinueDelaySec` · `continueText` | 🔵 | `src/lib/llm/server/server.go` |
| 6 | 后端自动续写（中断后 10 分钟无操作 → 自动 1 次；再失败即停）；前端提示条「服务器错误，是否继续。」+「继续」链接（点击 = 发送 `continueText`，不预填输入框） | 🔵 | `src/lib/llm/server/turn.go`、`server.go`、`ChatPanel.vue` |
| 7 | **消息面**：新增 `session-interrupted`（前端 `llm-interrupted`）→ 61 §0 登记 + `msgkeys` 重生成 + `61-messages.schema.json` + 桥映射 + browser 前缀；spec 同步：2D-router（重试章节）· 21-llm-server · 61 §4.3 注（`retry_count` 来源变更 + 新主题）· 64（配置项）· 35 | 🔵 | `docs/spec/**`、`src/lib/core/msgkeys/`、`src/frontend/src/events/` |
| 8 | 测试：L1（router 分流/预算/判据）· L2（mq 驱动：可见重试 + 自动续写（宽限期）+ 取消语义）· L4（提示条视觉 + 链接点击） | 🔵 | `src/lib/router/*_test.go`、`src/test/chonkpilot-llm/unittest/`、`src/test/chonkpilot-gui/systest/` |

---

## 8. 关联文档

- [49-待实现项 §2](../spec/40-roadmap/49-待实现项.md)（`RETRY-2`）
- [2D-router](../spec/20-modules/2D-router.md)
- [21-llm-server](../spec/20-modules/21-llm-server.md)
- [61-消息一览 §4.3](../spec/60-reference/61-消息一览.md)（`session-retry` / `llm-retry`）
- [64-配置项一览](../spec/60-reference/64-配置项一览.md)
- [35-错误处理与恢复](../spec/30-function-points/35-错误处理与恢复.md)
- [71-编程规范 §3.3](../spec/70-conventions/71-编程规范.md)（异常与重启恢复）
