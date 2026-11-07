# AskUser · Ask User 专题设计方案

> 状态：🟢 **Step 1（S1-1～S1-6）已实施（2026-10-29）**；🟢 **Ask 中心独立窗口（S2-1～S2-6）已实施（2026-11-04）**；🟢 **决策落地（S3-1～S3-7，2026-11-05）：超时自动回复 / 全局 Ask 中心 / 后端唯一分发 / owner_instance / context 已实施**；🟢 **S4 非阻塞 ask（2026-11-06）：ask_user 立即返回占位结果（快速转异步）、同会话答复全部到齐后一次性投递**；🟢 **S5 聊一聊内嵌面板（2026-11-06）：面板内讨论、完成只回写 tool-result 且不通知 LLM**
> 日期：2026-10-28（Step 1 落地 2026-10-29；Ask 中心落地 2026-11-04；S3 决策落地 2026-11-05；S4 非阻塞改造 / S5 内嵌面板 2026-11-06）
> 关联：[61-消息一览 §0.6 / §4.2 / §4.3](../spec/60-reference/61-消息一览.md) · [49 ASK-1](../spec/40-roadmap/49-待实现项.md) · [群聊会话设计方案 §13 D42](../agent-chat/群聊会话设计方案.md) · [单聊会话设计方案 §12.4 B4](../agent-chat/单聊会话设计方案.md) · [24-多窗口模型 §1.1 I-1](../spec/10-architecture/24-多窗口模型设计方案.md) · [71-编程规范 §3.3](../spec/70-conventions/71-编程规范.md)
> 定位：本文是 **Ask User 的唯一专题口径**。消息面以 61 为准；§5 的 MQ 投递模型已于 S3 实施。

---

## 1. 问题与目标

### 1.1 要解决的问题

1. **“需要先讨论”的提问无解**：`ask_user` 曾是阻塞式工具调用（S4 起改为快速转异步），卡片只有「答 / 跳过 / 取消」；用户要解释、要看上下文时无处可去。「跳过」仅是前端本地重排，后端无感知。
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
- 不做独立待办窗口（**Step 1 期**；S2 已于 2026-11-04 独立窗口落地，见 §4.7）、不做子会话 composer。
- 不改 MQ 投递模型（另立专题，见 §5）。

---

## 2. 术语

| 术语 | 定义 |
|---|---|
| 归属窗口 | 跑该会话 turn 的窗口（`session → instance` 反查：主窗口 / 单聊窗口 / 群聊窗口）；`session-ask.instance_id` 指向它 |
| Ask 中心 | 集中展示与作答的界面：左列会话、右侧问题与选项。**目标形态 = 独立待办窗口**；Step 1 = 主窗口内承载；**S2 已实施独立待办窗口（2026-11-04）** |
| 会话标识 | `(dialog_id, session)`；`dialog_id` 按 61 §0.6 **必带** |

---

## 3. 总体方案

保持 ask 的**定向窄播**不变（不广播到全项目），把“作答”从“只能选”扩展为“可先讨论”，并把可讨论范围**先收敛到主窗口**。本期只做前端行为收敛 + 两个 payload 补字段，不做窗口与布局重构。

---

## 4. 详细设计

### 4.1 投递：后端唯一分发 + 全局 Ask 中心（S3，2026-11-05）

- ask 仍先投**归属窗口**（`session-ask.instance_id` = 该会话所在窗口实例）；归属窗口收到后调
  `gui.window.open-ask {ask}` 打开/激活 Ask 中心（宿主不支持时回落弹窗，见 §4.7）。
- **Ask 中心全局唯一**：每进程一个 `#ask` 窗口（跨项目），不绑定会话、不占对话窗口额度；
  标题固定「待办」。窗口 id 使用**进程级序号**（与项目对话窗口共用命名空间，避免 `w1` 撞号串窗）。
- **后端唯一分发**：server 维护 ask 登记（`ask_id` 全局唯一）与 Ask 中心订阅登记；
  订阅按 `user`（该实例 OS 用户）作用域，可选 `work_dir` 收窄；同一 user 只保留一个订阅实例
  （后订阅覆盖，避免多端重复分发）。
- **有序队列**：每个 workdir 一个单调 `seq`；`ask-center-subscribe` 快照按 `workdir + seq`
  升序返回；新增/摘除经 `ask-center-add` / `ask-center-remove` 定向推到订阅实例。
- 宿主侧待办表降级为**进程级缓存**（供 `gui.ask.list` / 关窗重开兜底）；server 是权威。
- `session-ask.instance_id` 语义不变；新增 `session-ask.owner_instance` = 发起 turn 的窗口实例
  （跨窗口作答归属判定）。

### 4.2 Ask 中心布局（**S2 已实施；S3 全局化**）

- 左右两栏：左 = 待办列表，右 = 选中项详情。
- 左列每项：`workdir 末级目录名`（不显示完整路径）+ 会话摘要（server 下发的 `session_title`，
  未就绪回落 `data-session-get` 标题，再回落 `#session 前 8 位`）+ 时间/状态（`received_at`、`已超时`）。
- 右侧：问题全文、`context` 背景说明（可选）、选项（含推荐项高亮）、自定义输入；作答即提交；
  底部「聊一聊 / 跳过 / 取消 / 发送回答」。
- 空态：「暂无待办」；未选中：「从左侧选择一条待办」。

> **S2 由独立待办窗口承载**（`#ask`；原生标题栏；不占对话窗口额度）；**S3 起全局**（跨项目单例）。

### 4.3 四个出口

| 出口 | 语义 | 状态 |
|---|---|---|
| 选择 / 填写 | 正常作答；记入会话批次，同会话**全部答完后**作为一条用户消息投递；同时把原 `ask_user` tool-result 就地更新为答案（S4） | ✅ 保持 |
| 跳过 | 暂挂到队尾（前端本地语义，后端无感知；S2 = 留在左列并移到队尾；不计入「已回答」） | ✅ 保持 |
| 取消 | 以「用户选择了其他，可能需要进一步的信息才能确定」文案作答（S3 定稿；计入本批答复，并回写 tool-result） | ✅ 保持 |
| 聊一聊 | 切换为**内嵌聊天面板**（覆盖 ask 区，加载源会话既有内容，可讨论）；ask **保持未解除**；点「完成」→ 只回写 tool-result（「参考后续的聊天记录」）+ 任务 done + 摘除待办，**不通知 LLM**；点其他 ask / 返回 → 仅退出面板，不解除 | ✅ S2 起在 Ask 中心提供；S5（2026-11-06）改为内嵌面板 |

- 「聊一聊」是 **UI 动作**，不是 LLM 工具；不改工具契约、不进工具面。
- S2 起「聊一聊」不再受「仅主窗口」限制：Ask 中心可按 ask 的 `(dialog, session, work_dir)` 定位
  源会话窗口（S3 起跨项目：宿主按 `work_dir` 路由到对应项目窗口表；项目未打开 → 明确失败）。
- **超时（S3 / S4）**：`ask_user.timeout` 未设置 = 用项目全局 `ask_timeout_sec`（全局未配置则一直等）；`timeout=0` 同全局；`timeout=-1` 明确不超时；`timeout>0` 覆盖全局。到点未答 → server 自动回复：
  有 `recommended` 取推荐（单选取首个，多选按「、」拼接），否则「用户未在时限内回复，请按推荐做法继续。」；
  自动回复**计入会话批次**，与用户答复一样在「同批全部答完」后作为用户消息投递；同时下发 `ask-center-remove{reason:timeout}`。
- **非阻塞（S4）**：`ask_user` 立即返回占位结果（见 §4.8），本轮可继续或收尾；答复后续以用户消息送达。
- **焦点（S3）**：Ask 中心已是前景窗口 → 新 ask 只入列，**不抢焦点**；否则自动打开/激活。

### 4.4 Step 1 实施范围

1. 主窗口 ask 卡片提供「聊一聊」；其他窗口不渲染该按钮。
2. 点击行为：定位 ask 的 `(dialog, session)` → 切过去 → 以 defer 语义回填 → 把问题引用放进输入框。
3. 补齐 `session-ask` / `ask-user-reply` 的 `dialog_id`。
4. ask 弹窗居中于屏幕工作区（见 §4.6）。
5. 上述之外一律不动。

### 4.5 契约影响（以 61 为准）

| 主题 | 变更 | 依据 |
|---|---|---|
| `session-ask` | 增 `dialog_id`（必带）、`owner_instance`（必带）、`context`/`session_title`（可选）；`expires_at` 改为**可选**（无超时不下发） | 61 §0.6 / S3 |
| `ask-user-reply` | 增 `dialog_id`（必带）、`owner_instance`（可选回带）；**S4：答复不再回填当前轮 tool-result**，按 `(instance, session)` 聚合，同会话全部答完后以一条用户消息投递；**S4：答复到达后就地更新 DB 原 tool-result** | 61 §0.6 / S3 / S4 |
| `ask_user` 工具结果 | **S4：立即返回占位文案**（快速转异步），真实答复后续以用户消息送达；任务节点在答复到达时才 `done`；答复 / 超时 / 完成时就地更新 tool-result | S4 / S5 |
| `ask-resolve` | **新增**（S5）：「聊一聊」后点「完成」→ 更新 tool-result + 任务 done + 摘除待办；**不向 LLM 投递** | 61 §4.2 / S5 |
| `ask-center-subscribe` / `ask-center-unsubscribe` | 新增（后端唯一分发订阅登记 / 退订；scope 缺省 user，可选 work_dir） | S3 |
| `ask-center-add` / `ask-center-remove` | `ask-center-add` 增 `seq`（可选）；`ask-center-remove` 增 `reason`（可选） | S3 |
| `gui.chat.insert-text` | 增 `work_dir`（可选，跨项目路由） | S3 |
| `gui.window.open-ask` / `gui.ask.list` / `gui.ask.remove` | 语义收紧为**全局**窗口 / 进程级缓存 | S3 |

> `ask_user` 工具契约（S3/S4）：参数增 `context`（可选背景说明）与 `timeout`（整数秒；未设置 = 用全局
> `ask_timeout_sec`，全局未配置则一直等；0 = 用全局；-1 = 明确不超时；>0 = 覆盖全局）。
> `on_timeout` 不再作为独立选项：到点统一“自动回复 + 本轮继续”。**S4 起工具本身非阻塞**：
> 调用立即返回占位结果，答复后续作为用户消息投递。

### 4.6 弹出位置（新增）

- **一律居中于整个桌面（屏幕工作区）的正中**，**不允许**相对父窗口、记忆上次拖拽位置或随机偏移。
- 「工作区」= 屏幕去掉任务栏的可用区域。
- 多显示器：取**弹窗所在窗口当前显示器**的工作区；无法判定时回落主显示器。
- 每次弹出都重新居中（拖拽只影响本次，不持久化）。
- 同一时刻只显示一张 ask 卡片（沿用现有队列），避免多张叠放导致偏移。

> 现状：`DialogShell` 默认按 `window.innerWidth/innerHeight` 居中（窗口内居中，非屏幕居中）。本项要求改为按屏幕工作区计算，并在视口内做 clamp 兜底。

### 4.7 S2 实施记录：Ask 中心独立窗口（2026-11-04）

**形态**

- 每项目一个 `#ask` 窗口（`?work-dir=<wd>#ask`），宿主原生标题栏；**单例**（再开 = 激活）；**不占对话窗口额度**（≤5 不变）；Ask 中心实例是**子实例**（`parent_instance` = 来源窗口）。
- 归属窗口（主窗 / 对话窗 / 群聊窗）收到 `ask-user` → `gui.window.open-ask {ask}`；宿主把 ask 并入**项目级内存待办表**（`ask_id` 去重），已开窗直推 `ask-center-add`。
- Ask 中心挂载/重载/关窗重开后调用 `gui.ask.list` 回填左列；作答/取消后 `gui.ask.remove` 摘除。

**为什么不由 server 直接投递给 Ask 中心**

- 保持 `session-ask` 的**定向窄播**与 `instance_id` 语义不变（不引入 ask 中心实例注册/角色，不改 bridge 过滤）。
- Ask 中心不承载会话，无法用 `InstanceForSession` 反查；由归属窗口转交最直接，且宿主天然知道本项目有哪些窗口。
- 回退友好：宿主不支持（browser / 旧宿主 / 建窗失败）→ 归属窗口回落既有 dialog，turn 不空等。

**作答路由**

- Ask 中心直接发既有 `ask-user-reply {ask_id, answer, custom, dialog_id}`；server `takeAskLocked` 增加**按 ask_id 全局兜底**（先 instance 精确、再 `deliverInstance`、最后 ask_id；ask_id 全局唯一、取走即销）。
- **超时后作答**（等待登记已摘除）：`replyExpiredAsk` 的新轮投递实例改为 `expiredReplyTarget` = 源会话窗口 → 同 workdir 顶层实例（主窗口）→ 调用方；避免把新轮挂到 Ask 中心实例（无 chat 面板）。

**聊一聊**

- **S5（2026-11-06）**：Ask 中心「聊一聊」进入**内嵌聊天面板**（`AskChatPanel`，覆盖 ask 区，加载源会话 `MessageList`），直接以 `owner_instance` 实例发送 `message-send`；不切窗口、不注入输入框、不自动作答。点「完成」→ 发 `ask-resolve`（只结案：tool-result「参考后续的聊天记录」+ 任务 done + 摘除待办，**不投递 LLM**）；点其他 ask → 仅退出面板、不解除。只有用户实际作答或点「完成」才解除 ask。

**已知边界（如实登记）**

- 宿主待办表**不落库**：窗口关闭/重开保留（进程内），**进程重启即清空**；server 侧未答 ask 由启动扫描收敛 `interrupted`，不再可答 —— 故**不**按 `data-ask-pending` 重建为可操作待办（与 §6.2 目标态的差异在此收口）。
- `ask-center-remove{reason}` 已覆盖超时/轮次收尾/实例退出/项目关闭的即时摘除；不再单列 `ask-closed` 主题。
- MQ 投递模型（后端唯一分发 / 订阅登记 / 每 wd 有序队列）**S3 已实施**；宿主待办表降级为进程级缓存，server 为权威。

### 4.8 S4 实施记录：非阻塞 ask（快速转异步，2026-11-06）

**口径（用户确定，必须按 1–5 执行）**

1. LLM 正常发起 `ask_user` 工具调用。
2. server 收到后把提问放入待办（内存登记 + `kind=ask_user` 任务节点 + Ask 中心队列）。
3. **立即返回占位工具结果**（快速转异步），不阻塞本轮：
   「已向用户提问，正在等待回复。你可以继续完成其他可独立完成的任务；如果暂时没有其他可做的，
   请直接回复文本「等待用户回复」并结束本轮对话。用户答复到达后会作为后续消息送达。」
   —— 该占位结果**不代表提问完成**：任务节点保持 `running`，提问登记与 Ask 中心待办继续存活。
4. 用户回复后，答复传给 LLM：作为**用户消息**投递（同一 `tool_call_id` 当前轮已拿过占位结果，
   不能二次回填；答复到达后把 DB 原 tool-result **就地更新**为答案，供历史/重载）。若原轮仍在运行，
   则在该轮结束后起新一轮；若原轮已结束，直接起新一轮。
5. **同一会话多个问题**：server 按 `(instance, session)` 聚合成一个批次；每条答复先收着，
   **全部回答后合并为一条**用户消息一次性投递（按提问 `seq` 排序，含问题原文与答复）。
6. **聊一聊（S5，2026-11-06）**：点击后进入 Ask 中心内嵌聊天面板（覆盖 ask 区，加载源会话内容，
   可讨论），**ask 保持未解除**；点「完成」→ 只回写 tool-result（「参考后续的聊天记录」）+ 任务 done
   + 摘除待办，**不向 LLM 发消息**；点其他 ask / 返回 → 仅退出面板，不解除。用户仍可回到回复界面直接作答。

**实现要点**

- `dispatchToolCall` 的 ask 分支：登记提问 → `persistToolResultAsk(占位文案, completed)` →
  返回同步 tool 消息；**不再 `markInflight`**，引擎不进入 `PendingWait`。
- 轮次**正常结束不清理** ask 登记与批次（提问允许跨轮存活）；显式 `llm-cancel` 与
  `instance-exit` 才清理（批次内已收到的答复一并丢弃）。
- 主会话答复投递走持久化 INBOX 的 `next_turn`（有运行轮则轮末消费；无运行轮立即起新轮），
  避免相位竞态丢输入；群聊 / 子会话沿用「有运行轮直接喂入，否则起新轮」。
- 超时自动回复与用户答复走同一记录路径：记入批次 + 就地更新 tool-result，同批全部答完才投递。
- **直接回答**：`updateAskToolResult` 把原 tool-result 就地更新为答案（同一行、`status=completed`、
  `ask_id` 保留）；答复本身按批次投递。
- **聊一聊 / 完成（S5）**：前端 `AskCenterView` 切到 `AskChatPanel`（`MessageList` 内嵌加载会话，
  输入经 `llm-inbox-add` **以源会话实例**发送，避免 owner conflict）；「完成」发 `ask-resolve`：
  后端 `onAskResolve` → `updateAskToolResult(w, "参考后续的聊天记录")` + `tasks.done` +
  `ask-center-remove{reason:resolved}`；**不调用 `deliverAskAnswers`**（除非同批还有已暂存的真实答复）。
  切换 ask / 返回只退出面板，不产生后端动作。
- `data-ask-pending` 的「未答扫描」不再识别这些提问（占位结果已把 tool 行收口为 completed）；
  进程重启后由任务层 `recoverStaleTasktree` 把遗留 `ask_user` 节点收敛为 `interrupted`，
  Ask 中心进程缓存同时清空（与 §6.2 一致）。

---

## 5. 与既有机制的关系

| 机制 | 关系 |
|---|---|
| 61 消息面 | Step 1 补 `dialog_id`；S2 新增 gui 本地面 4 主题 + 2 下行事件；S3 增 `ask-center-subscribe` / `ask-center-unsubscribe`、`ask-center-add.seq` / `ask-center-remove.reason`、`session-ask.owner_instance/context/session_title`（`expires_at` 改可选）、`gui.chat.insert-text.work_dir` |
| 24 I-1（一会话一窗口） | Ask 中心**不绑定会话**，不参与 I-1；S3 起为**全局单例**（每进程一个，跨项目） |
| 群聊 D42 / 单聊 B4 | 投递口径沿用“按 instance 定向”；归属窗口再转交全局 Ask 中心 |
| MQ 投递模型 | **S3 已实施**：后端唯一分发 + 订阅登记 + `user` 作用域（可选 `work_dir`）+ 每 wd 有序队列/`seq`；宿主待办表降级为进程级缓存 |
| 引擎异步等待（INBOX/PendingWait） | **S4 起 ask 不参与**：ask_user 立即返回占位结果，不 `markInflight`、不进入 `PendingWait`；答复走 INBOX `next_turn`（主会话）后续投递 |
| 会话输入 / 归属（INBOX + owner） | **S5 内嵌面板**复用既有 `llm-inbox-add`：以 `ask.owner_instance` 发送（不新建 owner）；ask 未解除时任务节点 running → 源窗口 owner 保持活动，面板不会触发 owner conflict |

---

## 6. 边界、异常与重启恢复

### 6.1 边界与异常

| 场景 | 行为 |
|---|---|
| 显式超时未答 | `ask_user.timeout>0`（或全局 `ask_timeout_sec>0`）到点 → 自动回复（推荐值优先）记入批次 + 就地更新 tool-result；同批全部答完后作为用户消息投递；`ask-center-remove{reason:timeout}`。**不**转新 turn、不中断轮次 |
| 无超时 | `timeout` 缺省/-1 且全局未配置 → 一直等待（不听 30 分钟旧 TTL；`expires_at` 不下发） |
| 轮次正常结束 | **ask 登记与批次继续存活**（S4）：LLM 可在提问后直接收尾；答复到达后起新一轮（主会话经 INBOX `next_turn`） |
| 轮次被取消 | `llm-cancel` → 显式摘除本轮 ask 登记与 Ask 中心待办（`turn_closed`）；批次内已收答复一并丢弃 |
| 归属窗口关闭 | `instance-exit` 取消该实例 running turn → 该实例全部 ask 与批次一并清除 |
| 聊一聊（内嵌面板） | 进入面板**不解除** ask、不发后端消息；面板内讨论以**源会话实例**发送（否则 owner conflict）；点其他 ask / 返回 → 仅退出面板，不解除、不产生后端动作 |
| 点「完成」 | `ask-resolve` → 就地更新 tool-result=「参考后续的聊天记录」+ 任务 `done` + `ask-center-remove{reason:resolved}`；**不向 LLM 投递**；同批其他真实答复若已齐仍按原口径一次性投递；重复点击幂等 |
| 聊天期间 LLM 已继续 | 不取消聊天轮；「完成」只回写历史与任务状态，不影响正在跑的轮次 |
| 非归属窗口作答 | 按 `ask_id` 命中（先 `deliverInstance`，S2 起再按 ask_id 全局兜底）；未命中 → 转新 turn |
| Ask 中心（全局形态）关闭 | 不影响 ask 登记与 turn；宿主进程缓存保留 + server 登记保留 → 下次 `gui.window.open-ask` 重开时 `ask-center-subscribe` 快照 + `gui.ask.list` 回填 |
| 本期主窗口关闭 | 等于归属实例退出 → turn 取消、ask 消失（server `instance_exit` 摘除 + 向 Ask 中心推 `ask-center-remove`；宿主缓存按 work_dir 清） |
| 重复作答 | 第二次必然“登记已摘除” → 转新 turn；前端提交后禁用防重 |
| 多视图同会话 | 各视图均按 `(dialog, session)` 渲染，作答按 `ask_id` 路由 |
| Ask 中心与归属窗口并存 | 归属窗口不再弹 dialog（宿主接管）；宿主不支持时回落弹窗（见 §4.7） |

### 6.2 重启恢复

1. **状态落在哪**：ask 无独立表；`ask_id` 随发起时的 `role=tool running` 行落在 `messages.ask_id`，未答登记仅在内存。
2. **是否受理即持久化**：提问行随工具调用落库；作答经 `ask-user-reply` 由 server 权威路由（活跃回填 / 失效转新 turn）。
3. **非终态谁收口**：显式超时由 `expireAsk` 定时器主动结算（默认无超时）；进程重启由启动扫描 `data-ask-pending` 识别，随后 `cleanupStaleToolPairs` 统一置 `interrupted`（当前无 `awaiting` 态）。旧 `askTTL` 30 分钟惰性回收已由“显式 timeout + 到期自动回复”取代。
4. **重启后重建还是收敛**：**S2/S3 实际口径** = 宿主待办缓存仅进程内，**进程重启即清空**；未答 ask 收敛为 `interrupted`，不再自动弹卡片。**不**按 `data-ask-pending` 重建为可操作待办（等待登记本就在内存，重建出来也无法作答；与“重启即失效”的 61 语义一致）。**S4 补充**：占位结果已把 tool 行收口为 `completed`，`data-ask-pending` 不再识别这些提问；遗留 `ask_user` 任务节点由任务层 `recoverStaleTasktree` 收敛为 `interrupted`，Ask 中心进程缓存同步清空。
5. **副作用语义**：「聊一聊」**不再发 defer 回填**（S5）。只有用户实际作答（`ask-user-reply`）或点「完成」（`ask-resolve`）才解除 ask；`ask-resolve` 只就地写 tool-result 供历史/重载，**不投递 LLM**；作答按批次投递并就地回填。`ask_id` 唯一、取走即销，重放会被判为“已摘除”，不产生双份副作用。

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
| S2-1 | 宿主：每项目唯一 Ask 中心窗口（`#ask` / 单例 / 不占对话额度 / 子实例） | ✅ | `chonkpilot-framework/host/src/gui/askcenter.go`（`openAsk` / `removeAskWindow` / `placeAskWindow`）、`registry.go`（`askHost` + 字段 + `gui.window.list.ask_window_id`）、`window.go`（`roleAsk` / 尺寸） |
| S2-2 | 宿主：项目级内存待办表 + 推送/摘除/快照 | ✅ | `askcenter.go`（`upsertAskLocked` / `listAsks` / `removeAsk` / `pushAsk`）；主题 `gui.ask.list` / `gui.ask.remove` / `ask-center-add` / `ask-center-remove` |
| S2-3 | 归属窗口路由：`ask-user` → `gui.window.open-ask`；宿主不支持才回落弹窗 | ✅ | `src/frontend/src/views/chat/AskUserDialog.vue`（`routeToAskCenter`）；`event-names.js`；l4 `run_ask_ui.py`（A1） |
| S2-4 | Ask 中心前端：左列待办 + 右侧详情 + 四出口 + 关窗重开回填 | ✅ | `src/frontend/src/views/ask/AskCenterView.vue`、`utils/viewRoute.js`（`#ask`）、`App.vue`；测试 `src/frontend/test/askCenter.test.js` + `run_ask_ui.py`（A1～A15） |
| S2-5 | 作答路由：按 ask_id 全局兜底；超时新轮投回源窗口 | ✅ | `src/lib/llm/server/instancescope.go`（`takeAskLocked`）、`handler_ask.go`（`expiredReplyTarget`）、`askrecover.go`（`replyExpiredAsk`） |
| S2-6 | 聊一聊跨窗口：defer 回填 + 宿主导入输入框（已有窗口推事件 / 未开走 URL draft） | ✅ | `gui.chat.insert-text`（`registry.go insertText`）、`chatWindowURL` 的 `draft`、`ChatOnlyView.vue`；l4 `run_ask_ui.py`（A13/A14） |
| S3-1 | `ask_user` 增 `timeout` / `context`；超时自动回复（推荐值优先）+ 本轮继续 | ✅ | `src/lib/llm/contracts/tools/ask_user.tool.md`；`handler_ask.go`（`askTimeoutSeconds` / `askAutoAnswer` / `expireAsk`）；配置 `ask_timeout_sec`（prj）+ 设置页；l4 `run_llm.py` **C14b** |
| S3-2 | 取消文案改为「用户选择了其他，可能需要进一步的信息才能确定」 | ✅ | locales `chat.ask_user_cancel_answer`（zh/en）；l4 `run_ask_ui.py` **A9** |
| S3-3 | Ask 窗口焦点策略：已激活不抢焦点，否则打开/激活 | ✅ | `window.go`（`windowIsActive`）、`askcenter.go`（`openAsk`）；l4 `run_ask_ui.py` A1/A14（连续 ask 不串窗） |
| S3-4 | 全局 Ask 中心（跨项目单例）+ 进程级窗口 id | ✅ | `askcenter.go`（`env.windows` 全局表 / `askWindowURL` 无 work-dir）、`window.go`（`nextWindowID`）；l4 `run_ask_ui.py` A15、`run_mw.py` MW-T31 |
| S3-5 | 后端唯一分发 + 订阅登记（user/可选 workdir 作用域、每 wd seq 有序队列） | ✅ | `handler_ask.go`（`onAskCenterSubscribe` / `onAskCenterUnsubscribe` / `askCenterSnapshot` / `pushAskCenter`）、`subscribe.go`；L1 `askcenter_test.go`；契约 `ask-center-subscribe` / `ask-center-unsubscribe` |
| S3-6 | `session-ask.owner_instance`（必带）/ `context`（可选）/ `session_title`（可选）；`expires_at` 改可选 | ✅ | `handler_ask.go`（payload + `askSessionTitle`）；`61-messages.schema.json`；前端 `AskUserDialog` / `AskCenterView` / `AskUserContent` |
| S3-7 | 全局 Ask 中心「聊一聊」跨项目：`gui.chat.insert-text.work_dir` 路由到目标项目窗口表 | ✅ | `askcenter.go`（`insertText` workDir → `env.lookupProject`）、`registry.go`；l4 `run_ask_ui.py` A13/A14 |
| S4-1 | `ask_user` 立即返回占位结果（快速转异步），不再 inflight/PendingWait | ✅ | `src/lib/llm/server/turn.go`（ask 分支：登记 + `persistToolResultAsk(占位, completed)` + 同步返回）、`handler_ask.go`（`askAsyncHandoffResult`）；L1 `engine_turn_test.go` **TestEngineTurnAsk** |
| S4-2 | 答复不再同步续轮；同会话全部答完后一次性作为用户消息投递；答复到达时就地回填 tool-result（S5-4 补齐） | ✅ | `handler_ask.go`（`askBatch` / `recordAskAnswer` / `formatAskAnswers` / `deliverAskAnswers` / `updateAskToolResult`）；L1 `askbatch_test.go` |
| S4-3 | 轮次正常结束保留 ask 登记与批次；取消 / 实例退出清理 | ✅ | `turn.go`（loop 收尾不再清 ask）、`handler_session.go`（`onLLMCancel` 显式清理）、`instancescope.go`（`forgetAskLocked`）；L1 `isolation_test.go` **TestAskSurvivesTurnClose** |
| S4-4 | 主会话答复走持久化 INBOX `next_turn`（有运行轮轮末消费 / 无运行轮立即起新轮） | ✅ | `handler_ask.go`（`deliverAskAnswers` → `onInboxAdd`）；L1 TestEngineTurnAsk（后续轮消息断言） |
| S4-5 | 工具契约 / 技能提示词改为非阻塞语义，允许同会话多问 | ✅ | `src/lib/llm/contracts/tools/ask_user.tool.md`、`src/initdata/capability/skills/ux/discovery-questions.skill.md` |
| S5-1 | 聊一聊改为 Ask 中心**内嵌聊天面板**（覆盖 ask 区、加载会话、可讨论） | ✅ | `src/frontend/src/views/ask/{AskCenterView.vue,AskChatPanel.vue}`（`MessageList` 内嵌 + 简易 composer） |
| S5-2 | 面板输入以**源会话实例**发送（避免 owner conflict） | ✅ | `AskChatPanel.vue`（`owner_instance \|\| instance_id` → `messageSend.instanceId`）、`api/chat.js`（`publishInboxAdd(..., instanceId)`）、`MessageList.vue` 透传 |
| S5-3 | 点「完成」发 `ask-resolve`：只回写 tool-result/task/摘除待办，不通知 LLM；切走 / 返回不解除 | ✅ | `AskChatPanel.vue`、`AskCenterView.vue`（`onChatDone` / `selectAsk` / `closeChat`）；后端 `handler_ask.go`（`onAskResolve` / `resolveAskBatchLocked`）、`subscribe.go`、桥白名单 |
| S5-4 | 直接回答 / 超时自动回复回写原 tool-result；契约 `ask-resolve` + genmsg | ✅ | `handler_ask.go`（`updateAskToolResult`）、`61-messages.schema.json`、genmsg 生成物 |

---

## 8. 未决项

> **2026-11-05 决策收口**：以下 1–6 均已拍板并落地（见 §7 S3-1～S3-7），不再作为阻塞项。
> 仍保留为“未做”的只有：宿主待办缓存/进程重启恢复（按 §6.2 明确“重启即失效”）与
> `ask-closed` 独立主题（已由 `ask-center-remove{reason}` 覆盖，不再单列主题）。

1. ~~`owner_instance`~~ → **已加**（`session-ask` 必带）。
2. ~~`ask-closed`~~ → 以 `ask-center-remove{reason}` 覆盖（answered/timeout/resolved/turn_closed/instance_exit/project_closed）。
3. ~~`timeout` / `on_timeout`~~ → `timeout` 已加；`on_timeout` 统一为“自动回复 + 本轮继续”。
4. ~~`context`~~ → **已加**（可选，Ask 中心详情展示）。
5. ~~项目级/全局~~ → **全局**（每进程单例，跨项目）。
6. ~~MQ 投递模型~~ → **已实施**（后端唯一分发 + 订阅登记 + user/可选 workdir 作用域 + 每 wd seq）。

---

## 9. 测试要点

> 准则：[50-测试体系](../spec/50-testing/50-测试体系.md) · [51-FP与测试映射](../spec/50-testing/51-FP与测试映射.md)。全部用例必须「发送 mq 消息」驱动、「监听 mq 消息」断言。

| 用例 | 断言 |
|---|---|
| 主窗口作答 | `ask-user-reply` → tool-result **就地回填** → 答复作为后续用户消息投递（同会话批次聚合） |
| 其他窗口只有选项 | 卡片无「聊一聊」节点 |
| 聊一聊 | 内嵌聊天面板覆盖 ask 区并加载源会话；点「完成」→ `ask-resolve` → tool-result「参考后续的聊天记录」+ 待办移除，**不投递 LLM** |
| 超时后作答 | 转新 turn，历史含原问题上下文 |
| 重启 | 宿主待办表仅进程内 → 重启即清空；未答 ask 由启动扫描收敛 `interrupted`（**不**按 `data-ask-pending` 重建为可操作待办，见 §6.2-4） |
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
> | 重启 | 未答 ask 由启动扫描 `data-ask-pending` + `cleanupStaleToolPairs` 收敛 `interrupted`（现状，本 Step 未改动）；**S2 已明确不按 `data-ask-pending` 重建可操作待办**（等待登记本就在内存，重建也无法作答；见 §6.2-4）→ 无新增自动化 |
> | 关窗 | L1 `isolation_test.go`（`TestAskClearedOnInstanceExit` / `TestAskClearedOnTurnClose`：实例退出一律摘除该实例全部 ask 登记）；本 Step 未改动该链路 |
> | 弹窗位置 | L4 `run_ask_ui.py` **C16**（屏幕工作区居中 ±2px；拖拽后再弹出的新卡片重新居中）；纯函数 `dialogPosition.test.js` |

> **S2 逐条对账（2026-11-04 落地；`run_ask_ui.py` 已按 Ask 中心重写为 A1～A15，旧 C1～C16 面板/弹窗用例由下表取代）**
>
> | 用例 | 实现位置 |
> |---|---|
> | 自动打开 + 左列一览 + 右侧详情 | L4 `run_ask_ui.py` **A1**（首次 ask → `gui.window.list.ask_window_id`；`.ask-list-item`/`.question-content`/选项/推荐）；L2 `askCenter.test.js` |
> | 作答回链（含 dialog_id） | L4 `run_ask_ui.py` **A1/A3/A4/A11/A12**（ask 窗口内捕获 `ask-user-reply`）；L2 `askCenter.test.js`（`sendAskReply` 共用） |
> | 多待办聚合 | L4 **A5**（三条 ask 同列、逐条作答移除）；**A15**（关窗重开后 `gui.ask.list` 回填） |
> | 跳过 / 取消 | L4 **A8**（跳过不回复、不移除）/ **A9**（取消文案 + 移除） |
> | 超时标记 | L4 **A7**（`.ask-list-item.expired` + 仍可作答）；server 超时转新轮仍由 L1 `askrecover_test.go` 覆盖 |
> | 聊一聊（内嵌面板，S5） | L4 **A13**（`.ask-chat-panel` 出现、无 defer、无独立窗口、ask 保留 → 完成 → `ask-resolve` → 移除）/ **A13b**（切其他 ask 仅退面板、不解除）/ **A14**（缺会话提示且待办保留） |
> | 多窗口同列 / 单例 | L4 `run_mw.py` **MW-T31**（对话窗与主窗注入 → 同一 Ask 中心、不重复建窗、来源窗口无 dialog） |
> | 归属窗口不弹 dialog | L4 `test_task.py` **T13～T15**；`run_tools.py` ask 多选；`run_server_deps.py` **B6**；`run_llm.py` **C14** |
> | 作答路由 / 超时投递源窗口 | L1 `src/lib/llm/server/instancescope.go`（按 ask_id 全局兜底）+ `handler_ask.go`（`expiredReplyTarget`）+ `askrecover.go`；`askrecover_test.go` 回归 |
> | 契约 | L2 `askCenter.test.js`（9 主题，含 `ask-resolve` + `ask_window_id` schema/msgkeys 对账） |

> **S3 逐条对账（2026-11-05 落地）**
>
> | 用例 | 实现位置 |
> |---|---|
> | 超时自动回复（推荐值 + 本轮继续） | L1 `askcenter_test.go`（`TestAskTimeoutDefaults`）；L4 `run_llm.py` **C14b**（mock 显式 `timeout:2` + `recommended:["B"]` → 不点作答 → `llm-complete` + 待办移除） |
> | 取消文案 | L4 `run_ask_ui.py` **A9**（「用户选择了其他…」原样回链） |
> | 焦点策略 / 全局单例 | L4 `run_ask_ui.py` **A1/A14/A15**（连续 ask 不串窗、关窗重开同窗口 id 命名空间）；宿主 `openchat_dialog_test.go`（全局 URL/标题） |
> | 后端唯一分发 / 订阅快照 / 有序 seq | L1 `askcenter_test.go`（`TestAskCenterSnapshotOrder` / `TestAskCenterSubscribeSnapshot`）；前端 `ask-center-subscribe` 快照与宿主缓存合并 |
> | owner_instance / context / session_title / expires_at 可选 | L1 `tasks_test.go`（缺省无 `expires_at` + `owner_instance`）；前端 `askCenter.test.js` 契约断言；L4 A10（无 `expires_at` 不转 busy） |
> | 跨项目聊一聊 | 宿主 `insertText` 以 `work_dir` 路由目标项目窗口表；L4 A13/A14（同项目回归） |

> **S4 逐条对账（2026-11-06 落地）**
>
> | 用例 | 实现位置 |
> |---|---|
> | 立即返回占位结果、本轮不阻塞 | L1 `engine_turn_test.go` **TestEngineTurnAsk**：答复前 tool 行内容 = 占位文案；不等待答复即 `llm-complete` |
> | 答复后续作为用户消息投递 + tool-result 回写答案 | L1 TestEngineTurnAsk：轮后新 turn 的用户消息含「问：确认继续？」「答：y」；原 tool 行就地更新为 `y` |
> | 「完成」只回写 tool-result、不通知 LLM | L1 `engine_turn_test.go` **TestEngineTurnAskResolve**：tool 行更新为「参考后续的聊天记录」、task done、ask/批次摘除；无新轮 / 无 user 消息 |
> | 同会话多问全部答完一次性投递 | L1 `askbatch_test.go` **TestAskBatchAllAnswersOnce**（首答暂存、末答合并一条、顺序按 seq） |
> | 「完成」最后一条时投递已暂存的真实答复 | L1 `askbatch_test.go` **TestAskResolveBatchFlushesRealAnswers** |
> | 不同会话互不等待 | L1 `askbatch_test.go` **TestAskBatchIsolatedPerSession** |
> | 轮次结束保留 / 取消或退出清理 | L1 `isolation_test.go` **TestAskSurvivesTurnClose** / **TestAskClearedOnInstanceExit** |
> | 超时自动回复走同一条批次路径 + tool-result 回写 | L1 `isolation_test.go` **TestAskTimeoutDefaults**（折算/文案）+ `askcenter_test.go` |
> | 内嵌面板 / ask-resolve 契约 / 源实例发送 | L2 `src/frontend/test/askCenter.test.js`（AskChatPanel、askResolve、九个主题、`publishInboxAdd.instanceId`） |

---

## 10. 关联文档

- [49-待实现项](../spec/40-roadmap/49-待实现项.md)（ASK-1 登记）
- [61-消息一览](../spec/60-reference/61-消息一览.md)（§0.6 会话标识、§4.2/§4.3 ask 两主题）
- [24-多窗口模型设计方案](../spec/10-architecture/24-多窗口模型设计方案.md)
- [群聊会话设计方案](../agent-chat/群聊会话设计方案.md) §13 / [单聊会话设计方案](../agent-chat/单聊会话设计方案.md) §12.4
