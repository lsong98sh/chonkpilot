# AskUser · Ask User 专题设计方案

> 状态：🟢 Step 1（S1-1～S1-6）**已实施（2026-10-29）**；后续阶段（Ask 中心独立窗口 / MQ 投递模型）仍为设计草案
> 日期：2026-10-28（Step 1 落地 2026-10-29）
> 关联：[61-消息一览 §0.6 / §4.2 / §4.3](../spec/60-reference/61-消息一览.md) · [49 ASK-1](../spec/40-roadmap/49-待实现项.md) · [群聊会话设计方案 §13 D42](../agent-chat/群聊会话设计方案.md) · [单聊会话设计方案 §12.4 B4](../agent-chat/单聊会话设计方案.md) · [24-多窗口模型 §1.1 I-1](../spec/10-architecture/24-多窗口模型设计方案.md) · [71-编程规范 §3.3](../spec/70-conventions/71-编程规范.md)
> 定位：本文是 **Ask User 的唯一专题口径**。消息面以 61 为准；本文只定义 Ask 域的承载、交互与分期，**不重复定义 MQ 投递模型**（见 §5）。

---

## 1. 问题与目标

### 1.1 要解决的问题

1. **“需要先讨论”的提问无解**：`ask_user` 是阻塞式工具调用，卡片只有「答 / 跳过 / 取消」；用户要解释、要看上下文时无处可去。「跳过」仅是前端本地重排，后端无感知。
2. **后台不可见**：卡片按 `session → instance` 定向投给归属窗口；窗口在后台或最小化时无角标、无通知，turn 空等到 30 分钟超时。
3. **子会话无落点**：`work_session` / `llm_run` 子会话没有自己的窗口（结构上复用父轮次实例），有 ask 时没有可输入的界面。
4. **来源不清**：`session-ask` 未带 `dialog_id`（与 61 §0.6 冲突）；群聊里看不出“是哪个 agent 问的”。

### 1.2 目标（可验收）

- 主窗口的 ask 卡片可「聊一聊」：自动切到发起会话、以 defer 语义回填、问题引用进输入框。
- 其他窗口的 ask 卡片只保留选项/自定义/取消，不提供「聊一聊」。
- `session-ask` / `ask-user-reply` 补齐 `dialog_id`（61 §0.6 已定，属实现缺口）。
- 不改动工具契约、不改动 chat 显示/输入结构。

### 1.3 非目标（本期）

- 不做 chat 显示区与输入区的拆分。
- 不做场景选择下移、输入框 mini/max 布局改造。
- 不做独立待办窗口、不做子会话 composer。
- 不改 MQ 投递模型（另立专题，见 §5）。

---

## 2. 术语

| 术语 | 定义 |
|---|---|
| 归属窗口 | 跑该会话 turn 的窗口（`session → instance` 反查：主窗口 / 单聊窗口 / 群聊窗口）；`session-ask.instance_id` 指向它 |
| Ask 中心 | 集中展示与作答的界面：左列会话、右侧问题与选项。**目标形态 = 独立待办窗口；本期 = 主窗口内承载** |
| 会话标识 | `(dialog_id, session)`；`dialog_id` 按 61 §0.6 **必带** |

---

## 3. 总体方案

保持 ask 的**定向窄播**不变（不广播到全项目），把“作答”从“只能选”扩展为“可先讨论”，并把可讨论范围**先收敛到主窗口**。本期只做前端行为收敛 + 两个 payload 补字段，不做窗口与布局重构。

---

## 4. 详细设计

### 4.1 投递：保持定向窄播

- ask 的目标 = 归属窗口（现状）；Ask 中心上线后 = 归属窗口 + Ask 中心（窄播，非全项目）。
- 订阅窄化：普通窗口只订自己承载的 `(dialog, session)`；Ask 中心订 ask 聚合。
- `session-ask.instance_id` 语义不变（该会话所在窗口实例）。

### 4.2 Ask 中心布局（目标形态）

- 左右两栏：左 = 待办列表，右 = 选中项详情。
- 左列每项：`workdir 末级目录名`（不显示完整路径）+ 会话摘要（最新摘要）+ 时间/状态。
- 右侧：问题全文、选项（含推荐项高亮）、自定义输入；作答即提交。
- 空态：「暂无待办」。

> 本期由主窗口承载同等布局；独立窗口形态推迟。

### 4.3 四个出口

| 出口 | 语义 | 本期 |
|---|---|---|
| 选择 / 填写 | 正常作答，回填 tool_result | ✅ 保持 |
| 跳过 | 暂挂到队尾（前端本地语义，后端无感知） | ✅ 保持 |
| 取消 | 以「终止任务待讨论」文案作答 | ✅ 保持 |
| 聊一聊 | 切到发起会话 → defer 回填 → 问题引用进输入框 | 🆕 仅主窗口 |

- 「聊一聊」是 **UI 动作**，不是 LLM 工具；不改工具契约、不进工具面。

### 4.4 Step 1 实施范围

1. 主窗口 ask 卡片提供「聊一聊」；其他窗口不渲染该按钮。
2. 点击行为：定位 ask 的 `(dialog, session)` → 切过去 → 以 defer 语义回填 → 把问题引用放进输入框。
3. 补齐 `session-ask` / `ask-user-reply` 的 `dialog_id`。
4. ask 弹窗居中于屏幕工作区（见 §4.6）。
5. 上述之外一律不动。

### 4.5 契约影响（以 61 为准）

| 主题 | 变更 | 依据 |
|---|---|---|
| `session-ask` | 增 `dialog_id`（必带） | 61 §0.6 |
| `ask-user-reply` | 增 `dialog_id`（必带） | 61 §0.6 |
| 其余主题 | 无 | — |

> `owner_instance` / `ask-closed` / `timeout` / `context` 均为**未决字段**（§8），确认前不落 61。

### 4.6 弹出位置（新增）

- **一律居中于整个桌面（屏幕工作区）的正中**，**不允许**相对父窗口、记忆上次拖拽位置或随机偏移。
- 「工作区」= 屏幕去掉任务栏的可用区域。
- 多显示器：取**弹窗所在窗口当前显示器**的工作区；无法判定时回落主显示器。
- 每次弹出都重新居中（拖拽只影响本次，不持久化）。
- 同一时刻只显示一张 ask 卡片（沿用现有队列），避免多张叠放导致偏移。

> 现状：`DialogShell` 默认按 `window.innerWidth/innerHeight` 居中（窗口内居中，非屏幕居中）。本项要求改为按屏幕工作区计算，并在视口内做 clamp 兜底。

---

## 5. 与既有机制的关系

| 机制 | 关系 |
|---|---|
| 61 消息面 | 仅两处补 `dialog_id`；不改既有键名与语义 |
| 24 I-1（一会话一窗口） | 本期不新增窗口，不触碰 I-1 |
| 群聊 D42 / 单聊 B4 | 投递口径沿用“按 instance 定向” |
| MQ 投递模型 | **另立专题**（后端唯一分发 + 订阅登记 + `workdir+user` 作用域 + 每 wd 有序队列/seq）。本期 Step 1 **不依赖**；Ask 中心阶段依赖 |

---

## 6. 边界、异常与重启恢复

### 6.1 边界与异常

| 场景 | 行为 |
|---|---|
| 30 分钟未答 | 等待登记过期；再答 → 按 61 语义转新 turn（`replyExpiredAsk`） |
| 归属窗口关闭 | `instance-exit` 取消该实例 running turn → ask 随之消失（现状） |
| 非归属窗口作答 | 按 `ask_id` 命中（`deliverInstance` 兜底）；未命中 → 转新 turn |
| Ask 中心（独立形态）关闭 | 不影响 ask 登记与 turn；重开按 `data-ask-pending` 拉回 |
| 本期主窗口关闭 | 等于归属实例退出 → turn 取消、ask 消失（现状，无新增语义） |
| 重复作答 | 第二次必然“登记已摘除” → 转新 turn；前端提交后禁用防重 |
| 多视图同会话 | 各视图均按 `(dialog, session)` 渲染，作答按 `ask_id` 路由 |

### 6.2 重启恢复

1. **状态落在哪**：ask 无独立表；`ask_id` 随发起时的 `role=tool running` 行落在 `messages.ask_id`，未答登记仅在内存。
2. **是否受理即持久化**：提问行随工具调用落库；作答经 `ask-user-reply` 由 server 权威路由（活跃回填 / 失效转新 turn）。
3. **非终态谁收口**：内存登记由 `askTTL`（30 分钟）惰性回收；进程重启由启动扫描 `data-ask-pending` 识别，随后 `cleanupStaleToolPairs` 统一置 `interrupted`（当前无 `awaiting` 态）。
4. **重启后重建还是收敛**：Ask 中心重开时按 `data-ask-pending` 重建列表；未答 ask 收敛为 `interrupted`，不再自动弹卡片。
5. **副作用语义**：「聊一聊」的 defer 回填是**至少一次**语义的 tool result；因 `ask_id` 唯一、登记取走即销，重放会被判为“已摘除”并转新 turn，不产生双份 tool result。

---

## 7. 落地清单

| 步骤 | 内容 | 状态 | 关联代码 |
|------|------|:---:|---------|
| S1-1 | 主窗口渲染「聊一聊」，其他窗口不渲染 | ✅ | `src/frontend/src/views/chat/AskUserContent.vue`（`canChat` 开关 + `.chat-btn`）、`src/frontend/src/utils/askUserManager.js`（`get canChat()` 复用 `utils/viewRoute.js` 视图分派）；测试 `src/frontend/test/askUserChat.test.js` + `src/test/chonkpilot-gui/systest/run_mw.py`（MW-T31） |
| S1-2 | 点击：切会话 + defer 回填 + 问题引用入输入框 | ✅ | `src/frontend/src/utils/askUserManager.js`（`chatAbout` / `revealSession`；`chat-insert-text` 入输入框，不自动发送；defer 语义由 `answer` 文案表达、**不新增字段**）、locales `chat.ask_user_chat*`；测试 `run_ask_ui.py`（C14/C15） |
| S1-3 | `session-ask` 补 `dialog_id` | ✅ | `src/lib/llm/server/handler_ask.go`（`askWithID` → `ensureDialogIndex` 解析，取不到记 Warn 发空串）、`server_types.go`（`askWaiter.dialogID`）；契约 `61-messages.schema.json` + `61-消息一览.md`；测试 `src/lib/llm/server/ask_dialog_test.go` |
| S1-4 | `ask-user-reply` 补 `dialog_id` | ✅ | 前端回带：`askUserManager.submitAnswer`（`dialog_id: item.dialogId`）、`views/chat/AskUserDialog.vue`；后端留痕：`handler_ask.go onAskUserReply`（缺失/不一致记 Warn，路由权威仍是 `ask_id`）；测试 `ask_dialog_test.go` + `run_ask_ui.py`（C14） |
| S1-5 | 测试：mq 驱动 + mq 断言（见 §9） | ✅ | L1：`src/lib/llm/server/ask_dialog_test.go`（双向 `dialog_id`）；L2/前端：`src/frontend/test/askUserChat.test.js` + `dialogPosition.test.js`；L4：`run_ask_ui.py`（C14/C15/C16）、`run_mw.py`（MW-T31）。逐条对账见 §9 表后注 |
| S1-6 | ask 弹窗居中于屏幕工作区（非窗口内居中、不记忆位置） | ✅ | `src/frontend/src/components/dialog/DialogShell.vue`（`centerOnScreen` 分支）、`src/frontend/src/types/dialog.js`（开关缺省 false）、`src/frontend/src/utils/dialogPosition.js`（纯函数）；测试 `dialogPosition.test.js` + `run_ask_ui.py`（C16，±2px） |

---

## 8. 未决项

1. `session-ask` 是否增 `owner_instance`（解决跨窗口作答的归属判定）。
2. 是否新增 `ask-closed{ask_id}`（答完全局消卡，替代轮询 `data-ask-pending`）。
3. `ask_user` 是否增 `timeout` / `on_timeout`（abort / default / hang）。
4. 是否增 `context` 字段（为什么问、影响什么、选项差别）。
5. Ask 中心是**项目级**还是全局（建议项目级；全局需 user 级聚合订阅）。
6. MQ 投递模型专题的立项与排期。

---

## 9. 测试要点

> 准则：[50-测试体系](../spec/50-testing/50-测试体系.md) · [51-FP与测试映射](../spec/50-testing/51-FP与测试映射.md)。全部用例必须「发送 mq 消息」驱动、「监听 mq 消息」断言。

| 用例 | 断言 |
|---|---|
| 主窗口作答 | `ask-user-reply` → tool_result 回填 → 本轮续跑 |
| 其他窗口只有选项 | 卡片无「聊一聊」节点 |
| 聊一聊 | 先切到发起会话 → defer 回填 → 输入框出现问题引用 |
| 超时后作答 | 转新 turn，历史含原问题上下文 |
| 重启 | 待办列表按 `data-ask-pending` 重建；未答收敛 `interrupted` |
| 关窗 | `instance-exit` → turn 取消 → ask 消失 |
| 弹窗位置 | 卡片中心 = 屏幕工作区中心（容差 ±2px）；窗口未最大化时仍为屏幕居中；重复弹出不沿用上次拖拽位置 |

> **Step 1 逐条对账（2026-10-29 落地）**
>
> | 用例 | 实现位置 |
> |---|---|
> | 主窗口作答 | L1 `src/lib/llm/server/tasks_test.go`（`TestTaskAskUserMultiRecommended`）+ L4 `run_tools.py`（`ask_user` 多选 → `ask-user-reply` → tool_result → `llm-complete`） |
> | 其他窗口只有选项 | L4 `run_mw.py` **MW-T31**（对话窗 `.chat-btn` = 0、主窗 = 1）；源级 `askUserChat.test.js` |
> | 聊一聊 | L4 `run_ask_ui.py` **C14**（`session-changed` → 发起会话；`ask-user-reply` = defer 文案且 `dialog_id` 一致；问题引用出现在 `.richtext-input` 且未自动发送）；边界 **C15**（会话已删 → 可见提示 + 卡片保留） |
> | 超时后作答 | L1 `askrecover_test.go`（`TestAskExpiredReply`：非终态 → 转新 turn，历史含原问题上下文；已终态 → 明确失败）。**L4 不可自动化**：等待登记 TTL = 30 分钟（`askTTL`），真机等待成本不可接受 |
> | 重启 | 未答 ask 由启动扫描 `data-ask-pending` + `cleanupStaleToolPairs` 收敛 `interrupted`（现状，本 Step 未改动）；**待办列表按 `data-ask-pending` 重建属 Ask 中心（独立窗口形态）**，本期推迟 → 无新增自动化 |
> | 关窗 | L1 `isolation_test.go`（`TestAskClearedOnInstanceExit` / `TestAskClearedOnTurnClose`：实例退出一律摘除该实例全部 ask 登记）；本 Step 未改动该链路 |
> | 弹窗位置 | L4 `run_ask_ui.py` **C16**（屏幕工作区居中 ±2px；拖拽后再弹出的新卡片重新居中）；纯函数 `dialogPosition.test.js` |

---

## 10. 关联文档

- [49-待实现项](../spec/40-roadmap/49-待实现项.md)（ASK-1 登记）
- [61-消息一览](../spec/60-reference/61-消息一览.md)（§0.6 会话标识、§4.2/§4.3 ask 两主题）
- [24-多窗口模型设计方案](../spec/10-architecture/24-多窗口模型设计方案.md)
- [群聊会话设计方案](../agent-chat/群聊会话设计方案.md) §13 / [单聊会话设计方案](../agent-chat/单聊会话设计方案.md) §12.4
