# 22 · data-persist（src/lib/data 数据面：存储内核 + persist 服务）

> **数据根（当前口径，2026-11-04 核对）**：项目身份 = 归一化 `work_dir`，prjusr 根 = `<workDir>/.chonkpilot/users/<OS 用户名>/`。`projectid.go` 已删除、无项目 ID 路径来源；但 prj-config 写校验仍识别 `project-id` 键（`internal/config/validate.go`，历史配置键，非路径来源）。无迁移、无兼容分支。

> 状态：⚠️ 主体实现；2026-11-04 核对已订正文件清单 / 数据落层 / 主题域 / 跨 module 结论
> 关联：[12-数据层](../10-architecture/12-数据层.md)（层级/fallback 规则）· [02-配置层级](../00-overview/02-配置层级.md)
> 代码目录（2026-11-04 核对）：`src/lib/data/`（内核：`db.go` `table.go` `query.go` `configkv.go` `shared.go` `types.go` `buckets.go` `indexes.go` `tx.go` `dialog.go` `systemdoc.go` `session.go` `backup.go` `brief.go` `facade_conv.go` `llmref.go` `token.go` 等；内部实现：`internal/{capfs,config,kernel,knowledge,mcp,memory,project,scenario,session,sessionstore,snapshot,storeadapter,systemfs,tasktree}`；服务面：`persist/`；门面：`facade/`）。`migrate.go` / `seed.go` / `config.go` / `projectid.go` / `resolve.go` **不存在**。

---

## 1. 职责与边界

- **一句话**：统一数据层——**存储内核**（usr / prj 主库 + 一树一库对话库 + 通用 DAO + 连接缓存）与 **persist 服务面**（`data-<domain>-<op>` 消息应答 + `refresh` 广播）。
- **做**：Table/Query DAO、config 键值、路径解析、instance 绑定、对话库（一树一库）/分片、数据域消息处理。
- **不做**：不参与逐 key fallback（fallback 由消费方/上层按 [02-配置层级](../00-overview/02-配置层级.md) 规则解析）；不存派生内容（codegraph 索引独立目录）；无迁移体系、无 seed。

---

## 2. 形态与部署

| 项 | 值 |
|----|----|
| 形态 | lib（内核 + 服务面同 module） |
| 启动 | 由 server / gui / cli 以同一 Bus 启动（`persist.New(bus, Options{UsrPath})` + `Start`） |
| 依赖 | `chonkpilot-framework/host/core/paths`（原 `src/lib/paths` 已不存在）· `go.etcd.io/bbolt` |

---

## 3. 对外接口

### 3.1 消息面（`data-<domain>-<op>`，同主题回复）

| 域 | 主题组 | 存储 |
|----|--------|------|
| user-config | `data-user-config-{list,load,save,delete}` | usr config（**标量逐 key** + 专用表 `llms`；MCP 已文件化到四级 `capability/mcps/*.json`，**无 `mcps` 表**） |
| prj-config | `data-prj-config-{list,load,save,delete}` | prj config（无前缀；个人运行态 key 自动路由 prjusr） |
| prompt | `data-prompt-{list,load,save,delete}` | prj config（`prompt-` 前缀；`summary_prompt` 已文件化到 **系统文档** `capability/system/summary.md`（纯文本；读序 = 项目 → 用户 → 系统文件 → embed 内置））。**`summary_prompt` 区分「继承 / 覆盖」**——load 回读项目文件 → 用户文件 → 系统文件 → embed 内置；save 时**内容为空 或 与继承值相同 → 删项目级文件（保持继承、不写覆盖）**，不同才写项目级；delete = 删项目级文件回落继承（前端另有「恢复默认」入口，`src/lib/data/internal/config/prompt.go`） |
| prj-security | `data-prj-security-{list,load,save,delete}` | prj config（`security-` 前缀） |
| scenario | `data-scenario-{list,load,save,delete}` | **场景文件树（capability 根下 `scenarios/`）**：`<级别根>/capability/scenarios/<场景目录>/`（**四级** app/user/project/prjusr；id/key = 目录名；**四级均可编辑**；**无覆盖**、不允许同名场景（跨四级唯一）；app 级**可编辑**，出厂内容 = **磁盘目录**、唯一源 `src/initdata/capability/scenarios/`；`restore` 动作**已删除**；payload 不变） |
| mcp | `data-mcp-{list,load,save,delete}` | 四级文件 `<级别根>/capability/mcps/<名>.json`（2026-11-04 核对：域在 `persist.go` 已实现，旧文档漏列；save/delete 后广播 `data-mcp-refresh`） |
| session | `data-session-{list,get,history,latest,title,delete,fork,active-set,active-get,content,ensure-session,ensure-turn,append-message,set-summary,complete-turn,cleanup-stale,load-messages,context,group-participants-load,group-participants-save,group-round-targets-load,group-round-targets-save}` | **对话库（一树一库）** `<prjusr>/dialogs/dialog-<hex(dialog_id)>.db`；末四者 = 群聊两表，落 **prjusr 主库**（SUBCHAT-3 阶段 E，**不广播 -refresh**）。`fork` 为 2026-11-04 核对补列 |
| memory | `data-memory-{list,read,save,delete}` + `data-memory-extract-{load,save,delete}` | 记忆库文件（save/delete 广播 `data-memory-refresh`）· 提取进度表 `memory_extract`（prjusr） |
| inbox | `data-inbox-{append,list,update,commit}` | **对话库** `inbox_items`（按 `dialog_id`；内部面，只由 llm-server 读写）〔2026-11-04 核对：`buckets.go` 已将其归入 `shardBuckets`〕 |
| turn-event | `data-turn-event-{append,cleanup}` | prjusr `turn_events`（内部事件日志） |
| ask | `data-ask-pending` | 只读回查**对话库** `messages` 中带 `ask_id` 的行（**不建 ask 表**；内部面，详 [61](../60-reference/61-消息一览.md)） |
| snapshot | `data-snapshot-{get,set}` | **对话库** `sessions` 表 `history` / `snapshot_turn` |
| tasktree | `data-tasktree-{list,tasks,delete,upsert}` | **对话库** `tasktree`（+ `task_shadow`）；一树一库，按 `dialog_id` 落库，缺 `dialog_id` 时全实例聚合（61 §0.6 例外） |
| knowledge | `data-knowledge-{root,list,read,save,create,delete,rename,mkdir,rmdir,rename-dir}` | capability 文件树（直接文件读写） |

- 回复：同主题 `publish` `{ok, result}` / `{ok, error}`（**无 `.reply` 后缀**）；桥内部另有 `req_id` 关联字段用于请求-响应配对，**不属对外契约 payload**（见 [61-消息一览](../60-reference/61-消息一览.md) §0.1）。
- **载荷定死**（2026-10-25）：每个主题由一份 Go struct（`persist/payload.go`）解析，字段名/类型按 61 声明，内嵌 `Header{req_id,instance_id}`；兼容 `{data:{…}}` 与顶层扁平两形（`data` 优先、顶层补缺）。原通用宽松 `map` 解析（`dataReq`/`parseDataReq`）已删除。
- **出参定死**（2026-10-25）：应答 `result` 同样由具名 struct 组装（`facade/wire/{wire.go,domains.go}` 的 `XxxPayload`，json tag = 61 契约字段名；「非空才写键」语义用 `omitempty` 承接），不再是 `map[string]any`。门面路径与 MQ 路径共用同一组构造函数，两路径应答逐字一致由测试（`eqWire`）守。收尾新增通用 DTO：`OKIDPayload` / `OKDeletedPayload` / `ListPayload` / `DataPayload` / `PathPayload` / `SnapshotPayload`（**无 omitempty**，无快照仍输出 `null`）/ `InboxItemsPayload` / `SessionForkPayload` / `KnowledgeImportPayload`；persist 层已无手写回执 map（嵌套行 `items[]` / `errors[]` 仍为行 map，键集随数据变化）。
  > **〔2026-10-28 · DATA-OPT-1 P3 / S-7〕行 DTO 去重**：与门面类型**字段 + json tag 逐字一致**的 6 个行 struct（`KnowledgeDirRow` / `KnowledgeFileRow` / `MemoryCategoryRow` / `MemoryDocRow` / `MemoryExtractRow` / `GroupRoundTargetRow`）已删除，payload 直接持有 `facade.*`（载荷形状不变；`nil` 仍归一为 `[]`）。`GroupParticipantRow` **保留**——`facade.GroupParticipant.Role` 带 `omitempty`，直接复用会把 `role:""` 从载荷省掉。
- 防环：`handle` 入口 `isLoopbackReply` 判定——**顶层 `ok` + 应答专属信封键 `result` / `error` 二者同时成立**才跳过（仅凭 `ok` 会把恰好含 `ok` 的业务载荷误判为应答）。实现见 `persist.go`。
- 广播：`save`/`delete` 后发 `data-<domain>-refresh`（**只发布不订阅**）。2026-11-04 核对：仅配置类 / 场景 / MCP / 记忆有 `-refresh`（`data-{user-config,prj-config,prj-security,prompt,scenario,mcp,memory}-refresh`）；session / tasktree / knowledge / snapshot 无 `-refresh`（session 另发 `session-new` / `data-session-title-changed` 等业务事件）。

### 3.2 内核 API（lib）

`data.{OpenLayer, Table, Query, GetConfig/SetConfig/DeleteConfig, Register/Unregister, Prj/PrjUsr/Usr, OpenShared/OpenSharedLayer, UserPath/DataRoot/PrjUsrPath/PrjUsrDBPath/ProjectPath, DialogDBPath/OpenDialog/DeleteDialog, InstancesByWorkDir/DataDirByWorkDir, BindOf}`。
（2026-11-04 核对：`Table` / `Query` 是**类型**，不是函数；`Dialog*` 与 `InstancesByWorkDir/DataDirByWorkDir` 为实际存在但旧文档未列。）

> ⚠️ **收窄未完成（2026-11-04 核对订正）**：上列「交出库句柄」的内核 API **仍有跨 module 生产调用**——`src/lib/gui/host/pluginconfigstore.go`（独立 gui module）直接 `data.OpenSharedLayer` + `data.UserPath` + `db.Table` 读写插件配置桶。原「已清零 / 只剩 module 内与测试」的结论**不成立**，待后续迁移到门面。
> - llm server 的 usr 配置读取（含 mcps）= `ConfigAPI.UserConfigGet`（`s.cfg.UserConfigGet`：`bridge.go` / `domainmcp.go` / `llmconfig.go`）；`ConfigAPI.UserConfigMCPs` **不存在**。
> - 数据根准备 = `data.SetDataHome` / `data.UserPath`（`persist.New` 注入 `UsrPath` 时调用）；`data.CopyConfigTables` / `data.ReadProjectIDPath` **全仓无定义**。
> - 剩余消费方 = module 内（`internal/*` / `persist`）、`gui/host/pluginconfigstore.go` 与测试；把这些符号移入 `internal` 尚待拍板（测试面影响）。

### 3.3 门面域（`facade.API`，inline 绑定）

> `src/lib/data/facade/` 是 data 层门面的**唯一定义**（接口 + DTO，单一真相）；宿主装配期选绑定（本期唯一落地 = **inline 同进程直调**，见 [23 §7](../10-architecture/23-工程与部署拓扑.md)）。门面只放**领域字段**、不交路径规则；写入广播由实现侧在其后发出（不在请求-响应签名内）。
>
> **〔2026-10-25 双绑定删除〕** GUI 桥（`src/lib/gui/bridge`）**不再** inline 直调门面：`data-<domain>-*` 一律走总线由 `persist` 服务处理（`dataViaPersist` 为唯一入口，原 `facade_domains.go` / `facade_session.go` / `SetFacade` 已删）。inline 绑定现仅服务**进程内后端调用方**（llm server 的 `s.cfg`、插件桥宿主侧等）。

| 门面面 | 域 | 消息面（若有） |
|--------|----|--------------|
| `SnapshotAPI` / `ConfigAPI` / `SessionAPI` / `TurnAPI` / `MessageAPI` / `TasktreeAPI` / `KnowledgeAPI` / `ScenarioAPI` / `MemoryAPI` / `McpAPI` | snapshot / config / session / turn / message / tasktree / knowledge / scenario / memory / mcp | 对应 `data-<domain>-*`（§3.1 · §3.2 · §3.3 · §3.4）。〔~~`FileListAPI`~~ 2026-10-25 退场：vfts 清单改插件自持本地文件，原 `data-filelist-*` 三主题同步移除〕 |
| `TurnEventAPI` / `AskAPI` / `InboxAPI` / `GroupAPI` | turn-event / ask / inbox / group | 2026-11-04 核对补列（`facade/api.go` 实际成员；前三者对应内部面，`GroupAPI` 无消息面、由群聊调度器 inline 直调） |
| **`ProjectAPI`** | **project**（项目初始化：**工程规格文件** + 工作目录**只读探测** + **项目级 agent 落文件**） | **无消息面** —— 由 server **inline 直调**（「场景向导」用，[37 SCEN-012](../30-function-points/37-场景.md)）：`ProjectSpecExists` / `ProjectSpecRead` / `ProjectSpecWrite`（落点 `<workDir>/.chonkpilot/project_spec.md`）+ `ProjectProbe`（**只读**：`empty` / `has_code` / `has_git` / `has_readme` / `file_count` / `languages[]` / `frameworks[]` / `package_manager` / `build_tool` / `test_tool` / `lint_tool` / `top_dirs[]`）+ `ProjectAgentWrite`（把向导合成的 agent 提示词落 `<workDir>/.chonkpilot/capability/agents/<名>.agent.md`（契约分区文本），回引用串 `${workDir}/.chonkpilot/capability/agents/<名>.agent.md`；**供场景以引用承载子 agent**）。定义 = `facade/project.go`、实现 = `internal/project/`（`project.go` + `probe.go`）。本域**无变更广播**（消费者只有向导自身）。 |

> 🆕 **session 域新增能力（SUBCHAT-2 阶段 A/B-2，2026-10-16；均**无消息面**——由 llm-server **inline 直调** `s.cfg`）**：
> - `SessionEnsure` 支持**一次性写入**子聊天固定参数（`kind` / `lifecycle` / `agent` / `scenario_id` / `llm` / `purpose`）——**仅非空才写键**（普通顶层会话行形状与既有逐字节一致）；
> - 新增 `SessionLifecycleSet`（子聊天三态 `chatting` / `ready` / `finished` 更新；非法值报错不落库；行不存在报错不建残行）；
> - 新增 `SessionSubChats`（按父会话列举子聊天 `kind ∈ {sub_chat, group_chat}`，**含 finished**）与 `SessionSubChatsAll`（**全局**列举，跨父会话；单聊 §3.4.7 启动提醒取数）；
> - **`purpose`（阶段 B-2）**：`sessions` 行的子聊天**运行目的 / 展示名**（= gateway 注入的 `tool_call_display_name`，由 `subchat_open` handler 经注入通道读得后落库；单聊 §5.4），随 `subchat-opened` / `subchat-list` 下发；
> - **对外消息面（`subchat-*` 4 条）已登记**（阶段 B-1）·**handler/订阅已落**（阶段 B-2，见 [21-llm-server](21-llm-server.md)）；`data-session-list` 语义**不变**（仍只返回顶层会话，见 [61 §3.2](../60-reference/61-消息一览.md)）。

---

## 4. 内部控制流

### 4.1 消息分派

```text
route(subject)：剥 "data-" → 匹配 dataDomains 前缀 → handle(domain, op, payload)
handle（2026-11-04 核对）：isLoopbackReply 跳过应答 → 取 Header（instance_id 白名单校验）→ 按 domain switch 分派
  user-config→handleUserConfig；prj-config/prompt/prj-security→handleConfigKV(前缀)
  scenario→handleScenario；mcp→handleMCP；session→handleSession
  inbox→handleInbox；turn-event→handleTurnEvent；ask→handleAsk
  snapshot→handleSnapshot；knowledge→handleKnowledge；tasktree→handleTasktree；memory→handleMemory
  （载荷已按域定死 struct 解析，**无 `dataReq`/`parseDataReq`**）
reply/fail：publish 同主题 {req_id, ok, result|error}
```

### 4.2 数据根解析

```text
prjUsrDB(instanceID)：查实例视图 → PrjUsrByInst(instanceID, info)（未登记 → error）
sessionDBBy(instanceID)：同源解析 prjusr 主库（信封 handler 用）
usr 库：data.OpenSharedLayer(UserPath, LayerUsr)（UsrPath 可注入，测试避免污染用户配置）
对话库：data.OpenDialog / DialogDBPath（<prjusr>/dialogs/dialog-<hex(dialog_id)>.db，一树一库）
```

### 4.3 打开与迁移（内核）

`OpenLayer(path, layer)` → 建目录 `0700` → `bolt.Open(0600)` → 事务内 `ensureLayerBuckets`（按 `bucketsFor(layer)` 直建，幂等）；失败 `Close` 并中止。**无迁移体系**（无 `readSchemaVersion` / `_migrations` / `Migration`；2026-10-25 决策，2026-10-28 复核已无残留）。

> **v12（🆕 2026-10-24 · SESSION-COMP-1 P0）**：新增 `dialogs`（prjusr 对话目录）与索引 `messages_by_client_msg`（`(session_id, client_msg_id)`），并回填既有 `messages` 索引项（幂等）。对话库 = `<prjusr>/dialogs/dialog-<hex(dialog_id)>.db`（一树一库，`data.DialogDBPath` / `OpenDialog` / `DeleteDialog`）。〔2026-11-04 核对：`messages_by_client_msg` 现属**对话库** `shardBuckets`（`buckets.go`）；prjusr 主库只剩 `dialogs` / `turn_events` / `memory_extract` / `group_participants` / `group_round_targets`〕〔2026-10-28 订正（DATA-OPT-1 ②）：旧布局 `<prjusr>/sessions/<hex(session_id)>/`、`sessionstore.Migrate` 搬运与 `session.db.shard` 开关**已整体退场**；只保留 `sessionstore.Reconcile` 目录对账，`instance-register` 后异步执行一次（每 prjusr 路径去重，失败告警可重试）〕详见 [2G §9](2G-会话组件.md) / [49 `SESSION-COMP-1`](../40-roadmap/49-待实现项.md#1-引用迁移与历史索引2026-11-04)。

---

## 5. 数据结构与存储

见 [12-数据层 §6](../10-architecture/12-数据层.md)：按层建桶（`bucketsFor` 直建，**无** `_meta` / `_migrations`）；config 记录 `{"v": value}`。
persist 运行态：实例视图（订阅 `instance-register/heartbeat/exit`）、按路径连接缓存（`shared` + 引用计数）。

**`sessions` 行（对话库，一树一库；2026-11-04 核对）**：`session_id` / `title` / `parent_id` / `created_at` / `updated_at`（+ 快照列 `history` / `snapshot_turn`）+ **子聊天固定参数可选列** `kind` / `lifecycle` / `agent` / `scenario_id` / `llm` / `purpose`（SUBCHAT-2 阶段 A/B-2，2026-10-16；**仅非空才写键** → 普通顶层会话行形状与既有逐字节一致）+ **`next_run_count`**（NEXTRUN-1，2026-10-21；`next_run` 连续自动接续计数，**非零才写键 / 归零即删键**；单事务读改写 `SessionNextRunCheckAndAdd|Reset|Get|Dec`，见 [21 §3.4](../20-modules/21-llm-server.md)）+ **`goal` / `playbook` / `convergence` / `origin`**（2026-11-04 核对补列；`session_facade.go` 行写入）。
- `kind`：`""` = 普通顶层会话 · `sub_chat` = 子聊天（`subchat_open` 创建）· `group_chat` = 群聊（N 个 agent、人只旁观）· `work_session` = **参与者的干活子会话**（群聊专属，`parent_id` = 群聊会话；SUBCHAT-3 阶段 E/D23，**与 `sub_session` 不同物**）· `sub_session` = 子轮次派生的子会话（历史口径：原 `dsl_run` 的 LLM 步骤）；**例外**：`crontab` = 顶层定时任务根会话（`parent_id == ""`），其子会话 `crontab_task`（2026-11-04 核对补列，`facade/session.go`）。不变量 `kind ∈ {sub_session, sub_chat, group_chat, work_session} ⟺ parent_id != ""`（`crontab` 为顶层例外）。
- `lifecycle`：仅子聊天（`kind ∈ {sub_chat, group_chat}`）有，三态 `chatting` / `ready` / `finished`。
- `purpose`：仅子聊天有，= 本次子聊天的**运行目的 / 展示名**（gateway 注入的 `tool_call_display_name`，单聊 §5.4）。
- **读侧回退**：行缺 `kind` 且 `parent_id` 非空（2026-10-15 前落库的历史子会话行）⇒ 视为 `sub_session`（`facade/wire/wire.go SessionFromWire`）。
- 顶层列表（`data-session-list` / `latest`）仍按 `parent_id == ""` 过滤（`sessions_by_top` 索引），**只返回顶层会话**。

**`messages` 行群聊可选列（SUBCHAT-3 阶段 E，2026-10-16）**：`sender_agent`（群聊发言者标识）/ `mentions`（发言末行 `@` 名单，JSON 数组）—— **仅非空才写键** → 普通会话 / 单聊消息行形状与既有逐字节一致；随 `data-session-append-message` 落库（payload 增 `sender_agent?` / `mentions?`）、随 `data-session-history` 的**消息视图**回带（[61 §3.2/§3.2a](../60-reference/61-消息一览.md)）。**不进内核 `ChatMsg`**（= 不进 LLM 请求；展示与调度元数据）。

**群聊两表（SUBCHAT-3 阶段 E，2026-10-16；prjusr 专用表）**：`group_participants`（参与者名单 + 角色；主键 `<session_id>\x00<agent>`，列 `{session_id, agent, role, seq, created_at, updated_at}`；`seq` = 名单固定顺序）· `group_round_targets`（每【大轮次】的投递对象；主键 `<session_id>\x00<round 6 位零填充>`，列 `{session_id, round, agents[], created_at, updated_at}`）。两表**逻辑外键 = `session_id`**（关联 `sessions.kind == group_chat`）；读写经 `data-session-group-*`（**不广播 -refresh**）；**无二级索引**（单前缀 Seek）。设计见 [群聊会话设计方案 §5 · D44](../../agent-chat/群聊会话设计方案.md) · [12-数据层 §6.1](../10-architecture/12-数据层.md)。

---

## 6. 依赖

- **上游**：`chonkpilot-framework/host/core`（`paths` `mq` `msgkeys` 等）· `chonkpilot-util` · bbolt。
- **下游**：server（`sessionStore`/`taskManager`）· gui 桥（`dataViaPersist`；`gui/host/pluginconfigstore.go` 仍直用内核 API，见 §3.2）· 各插件。

---

## 7. 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| 长连接缓存 + 引用计数 | 按**最终 db 路径**缓存，计数归零才 Close | bbolt 单文件独占；不同 data_dir 可能同文件 |
| 请求与应答同主题 | 无 `.reply`；`ok` + `result\|error` 双条件防环 | 减少主题数量；单凭 `ok` 会误杀含 `ok` 的业务载荷（2026-11-04 核对） |
| refresh 只发不订阅 | 结构上消除自环 | 替代旧"通配 + ok 过滤" |
| knowledge 域直接文件读写 | capability 文件树不落库 | 原语本质是文件 |
| 无历史迁移 | 旧库/旧 config.json 不导入 | v6 明确 |

---

## 8. 场景与边界

- 未注册 instance → `ErrLayerUnavailable` / "instance not registered"。
- 跨进程：**桌面进程单例** = `framework/host/singleinstance`（数据根 UDS 独占 listen，`src/desktop/main.go`）；`framework/host/core/lockfile` 的 `AcquireWorkDir` / `AcquirePrjUsr` **已无生产调用点**（2026-11-04 核对；仅测试与常量使用）。prj / prjusr 库的 bbolt 独占 flock 为底层兜底。
- desktop：`dataDir` 恒为空串 → prj 与 prjusr 分离（prj 项目内、prjusr `<workDir>/.chonkpilot/users/<OS 用户名>/`）。
- 删除本层 key（`DeleteConfig`）= 恢复继承（不存在视为成功）。

---

## 9. 现状与待办

- ✅ v6 骨架落地：三级路径、同构建桶（无迁移体系）、逐 key config。
- ✅ S2–S7 完成：死配置清理、专用表 `llms` 迁移（MCP 已改四级文件，无 `mcps` 表）、prjusr 落地（会话/轮次/消息/任务树/快照原为 prjusr，2026-11-03 起已迁「一树一库」对话库；个人运行态 key 仍路由 prjusr）、capability 文件树（场景/知识库 + `capability/system/summary.md`）、UI 改造、契约对齐——见 [02-配置层级 §10](../00-overview/02-配置层级.md)。
- ✅ 场景已文件化：`<级别根>/capability/scenarios/<场景目录>/`（四级；原 `scenario_list` / `scenarios` 桶不再作为主数据）。

---

## 10. 关联测试

`src/test/chonkpilot-data/unittest/`（2026-11-04 核对：`data_test.go` / `data_index_test.go` / `llmref_test.go` + `persist/*`）：路径解析、fallback 逐级覆盖与回落、同一 workDir 的路径稳定性、Query 过滤/排序/游标、对话库一树一库、persist 各域契约。
