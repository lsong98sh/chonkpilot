# 22 · data-persist（src/lib/data 数据面：存储内核 + persist 服务）

> **〔🆕 2026-10-24 · `DATAROOT-1` 已实施〕** `projectid.go` **已删除**（上行列出的 `projectid.go` 不再存在）；prjusr 根 = `<workDir>/.chonkpilot/users/<OS 用户名>/`；凡本文提及「`project-id` 生成/复用/删除」的测试与描述**一律作废**，改为「同一 workDir 的路径稳定性」。

> 状态：✅ 与代码一致
> 关联：[12-数据层](../10-architecture/12-数据层.md)（层级/fallback 规则）· [02-配置层级](../00-overview/02-配置层级.md)
> 代码目录：`src/lib/data/`（内核：`db.go` `table.go` `query.go` `configkv.go` `shared.go` `migrate.go` `seed.go` `types.go` `config.go` `projectid.go` `resolve.go` 等；服务面：`persist/`）

---

## 1. 职责与边界

- **一句话**：统一数据层——**存储内核**（三级 chonkpilot.db + 通用 DAO + 迁移/seed + 连接缓存）与 **persist 服务面**（`data-<domain>-<op>` 消息应答 + `refresh` 广播）。
- **做**：Table/Query DAO、config 键值、路径解析、instance 绑定、schema 迁移、seed、数据域消息处理。
- **不做**：不参与逐 key fallback（fallback 由消费方/上层按 [02-配置层级](../00-overview/02-配置层级.md) 规则解析）；不存派生内容（codegraph 索引独立目录）。

---

## 2. 形态与部署

| 项 | 值 |
|----|----|
| 形态 | lib（内核 + 服务面同 module） |
| 启动 | 由 server / gui / cli 以同一 Bus 启动（`persist.New(bus, Options{UsrPath})` + `Start`） |
| 依赖 | `src/lib/paths` · `go.etcd.io/bbolt` |

---

## 3. 对外接口

### 3.1 消息面（`data-<domain>-<op>`，同主题回复）

| 域 | 主题组 | 存储 |
|----|--------|------|
| user-config | `data-user-config-{list,load,save,delete}` | usr config（**标量逐 key** + 专用表 `llms`/`mcps`） |
| prj-config | `data-prj-config-{list,load,save,delete}` | prj config（无前缀；个人运行态 key 自动路由 prjusr） |
| prompt | `data-prompt-{list,load,save,delete}` | prj config（`prompt-` 前缀；`summary_prompt` 已文件化到 **系统文档** `capability/system/summary.md`（纯文本；读序 = 项目 → 用户 → 系统文件 → embed 内置））。**`summary_prompt` 区分「继承 / 覆盖」**——load 回读项目文件 → 用户文件 → 系统文件 → embed 内置；save 时**内容为空 或 与继承值相同 → 删项目级文件（保持继承、不写覆盖）**，不同才写项目级；delete = 删项目级文件回落继承（前端另有「恢复默认」入口，`src/lib/data/internal/config/prompt.go`） |
| prj-security | `data-prj-security-{list,load,save,delete}` | prj config（`security-` 前缀） |
| scenario | `data-scenario-{list,load,save,delete}` | **场景文件树（capability 根下 `scenarios/`）**：`<级别根>/capability/scenarios/<场景目录>/`（**四级** app/user/project/prjusr；id/key = 目录名；**四级均可编辑**；**无覆盖**、不允许同名场景（跨四级唯一）；app 级**可编辑**，出厂内容 = **磁盘目录**、唯一源 `src/initdata/capability/scenarios/`；`restore` 动作**已删除**；payload 不变） |
| session | `data-session-{list,get,history,latest,title,delete,active-set,active-get,content,ensure-session,ensure-turn,append-message,set-summary,complete-turn,cleanup-stale,load-messages,context,group-participants-load,group-participants-save,group-round-targets-load,group-round-targets-save}` | prj/prjusr（末四者 = 群聊两表，prjusr；SUBCHAT-3 阶段 E，**不广播 -refresh**） |
| snapshot | `data-snapshot-{get,set}` | sessions 表 `history`/`snapshot_turn` |
| tasktree | `data-tasktree-{list,tasks,delete,upsert}` | prjusr tasktree 表 |
| knowledge | `data-knowledge-{root,list,read,save,create,delete,rename,mkdir,rmdir,rename-dir}` | capability 文件树（直接文件读写） |

- 回复：同主题 `publish` `{ok, result}` / `{ok, error}`（**无 `.reply` 后缀**）；桥内部另有 `req_id` 关联字段用于请求-响应配对，**不属对外契约 payload**（见 [61-消息一览](../60-reference/61-消息一览.md) §0.1）。
- 防环：`handle` 先跳过已含 `ok` 字段的应答消息（请求与应答同主题）。
- 广播：`save`/`delete` 后发 `data-<domain>-refresh`（**只发布不订阅**）。

### 3.2 内核 API（lib）

`data.{OpenLayer, Table, Query, GetConfig/SetConfig/DeleteConfig, Register/Unregister, Prj/PrjUsr/Usr, OpenSharedLayer, UserPath/DataRoot/PrjUsrPath/PrjUsrDBPath/ProjectPath}`。

> ⚠️ **收窄在途**：上列「交出库句柄」的内核 API（`OpenLayer` / `Table` / `GetConfig`
> / `SetConfig` / `DeleteConfig` / `Register` / `Unregister` / `Prj` / `PrjUsr` / `Usr` /
> `OpenSharedLayer` / `BindOf`）**跨 module 生产调用已清零**（读点改走门面：
> llm server 的 usr `mcps` 读 = `ConfigAPI.UserConfigMCPs`；CLI 数据根准备 =
> `data.CopyConfigTables` + `data.ReadProjectIDPath`，两者起**不暴露句柄**）。
> 剩余消费方 = module 内（`internal/*` / `persist`）与测试；把这些符号移入 `internal`
> 尚待拍板（测试面影响）。

### 3.3 门面域（`facade.API`，inline 绑定）

> `src/lib/data/facade/` 是 data 层门面的**唯一定义**（接口 + DTO，单一真相）；宿主装配期选绑定（本期唯一落地 = **inline 同进程直调**，见 [23 §7](../10-architecture/23-工程与部署拓扑.md)）。门面只放**领域字段**、不交路径规则；写入广播由实现侧在其后发出（不在请求-响应签名内）。

| 门面面 | 域 | 消息面（若有） |
|--------|----|--------------|
| `SnapshotAPI` / `ConfigAPI` / `SessionAPI` / `TurnAPI` / `MessageAPI` / `TasktreeAPI` / `KnowledgeAPI` / `FileListAPI` / `ScenarioAPI` / `MemoryAPI` / `McpAPI` | snapshot / config / session / turn / message / tasktree / knowledge / filelist / scenario / memory / mcp | 对应 `data-<domain>-*`（§3.1 · §3.2 · §3.3 · §3.4 · §3.6） |
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
handle：跳过 ok 应答 → parseDataReq → 按 domain 分派
  user-config→handleUserConfig；prj-config/prompt/prj-security→handleConfigKV(前缀)
  scenario→handleScenario；session→handleSession；snapshot→handleSnapshot
  knowledge→handleKnowledge；tasktree→handleTasktree
reply/fail：publish 同主题 {req_id, ok, result|error}
```

### 4.2 数据根解析

```text
prjDB(instanceID)：查实例视图 → data.Prj(instanceID)（未登记 → error）
usr 库：data.OpenSharedLayer(UserPath, LayerUsr)（UsrPath 可注入，测试避免污染用户配置）
```

### 4.3 打开与迁移（内核）

`OpenLayer(path, layer)` → 建目录 `0700` → `bolt.Open(0600)` → 事务内：`readSchemaVersion` → 跑未应用 migration（记录 `_migrations`）→ `seedDefaults(tx, layer)`；失败 `Close` 并中止。

> **v12（🆕 2026-10-24 · SESSION-COMP-1 P0）**：新增 `dialogs`（prjusr 对话目录）与索引 `messages_by_client_msg`（`(session_id, client_msg_id)`），并回填既有 `messages` 索引项（幂等）。对话库 = `<prjusr>/dialogs/dialog-<hex(dialog_id)>.db`（一树一库，`data.DialogDBPath` / `OpenDialog` / `DeleteDialog`）；旧布局 `<prjusr>/sessions/<hex(session_id)>/` 仍在用（**P3 才切读**）。搬运与启动对账 = `internal/sessionstore.Migrate` / `Reconcile`，由 prj 键 **`session.db.shard`（`on`/`off`，缺省 `off`）** 控制，在 `instance-register` 后异步执行一次（每 prjusr 路径去重，失败告警可重试）。详见 [2G §9](../20-modules/2G-会话组件.md) / [49 `SESSION-COMP-1`](../40-roadmap/49-待实现项.md)。

---

## 5. 数据结构与存储

见 [12-数据层 §6](../10-architecture/12-数据层.md)：三层同构桶（`_meta/_migrations/config/llms/mcps/sessions/turns/messages/scenarios/permission/tasktree/meta` + 索引桶）；config 记录 `{"v": value}`。
persist 运行态：实例视图（订阅 `instance-register/heartbeat/exit`）、按路径连接缓存（`shared` + 引用计数）。

**`sessions` 行（prj/prjusr）**：`session_id` / `title` / `parent_id` / `created_at` / `updated_at`（+ 快照列 `history` / `snapshot_turn`）+ **子聊天固定参数可选列** `kind` / `lifecycle` / `agent` / `scenario_id` / `llm` / `purpose`（SUBCHAT-2 阶段 A/B-2，2026-10-16；**仅非空才写键** → 普通顶层会话行形状与既有逐字节一致）+ **`next_run_count`**（NEXTRUN-1，2026-10-21；`next_run` 连续自动接续计数，**非零才写键 / 归零即删键**；单事务读改写 `SessionNextRunCheckAndAdd|Reset|Get|Dec`，见 [21 §3.4](../20-modules/21-llm-server.md)）。
- `kind`：`""` = 普通顶层会话 · `sub_chat` = 子聊天（`subchat_open` 创建）· `group_chat` = 群聊（N 个 agent、人只旁观）· `work_session` = **参与者的干活子会话**（群聊专属，`parent_id` = 群聊会话；SUBCHAT-3 阶段 E/D23，**与 `sub_session` 不同物**）· `sub_session` = `dsl_run` 的 LLM 步骤派生的子会话；不变量 `kind ∈ {sub_session, sub_chat, group_chat, work_session} ⟺ parent_id != ""`。
- `lifecycle`：仅子聊天（`kind ∈ {sub_chat, group_chat}`）有，三态 `chatting` / `ready` / `finished`。
- `purpose`：仅子聊天有，= 本次子聊天的**运行目的 / 展示名**（gateway 注入的 `tool_call_display_name`，单聊 §5.4）。
- **读侧回退**：行缺 `kind` 且 `parent_id` 非空（2026-10-15 前落库的历史子会话行）⇒ 视为 `sub_session`（`facade/wire/wire.go SessionFromWire`）。
- 顶层列表（`data-session-list` / `latest`）仍按 `parent_id == ""` 过滤（`sessions_by_top` 索引），**只返回顶层会话**。

**`messages` 行群聊可选列（SUBCHAT-3 阶段 E，2026-10-16）**：`sender_agent`（群聊发言者标识）/ `mentions`（发言末行 `@` 名单，JSON 数组）—— **仅非空才写键** → 普通会话 / 单聊消息行形状与既有逐字节一致；随 `data-session-append-message` 落库（payload 增 `sender_agent?` / `mentions?`）、随 `data-session-history` 的**消息视图**回带（[61 §3.2/§3.2a](../60-reference/61-消息一览.md)）。**不进内核 `ChatMsg`**（= 不进 LLM 请求；展示与调度元数据）。

**群聊两表（SUBCHAT-3 阶段 E，2026-10-16；prjusr 专用表）**：`group_participants`（参与者名单 + 角色；主键 `<session_id>\x00<agent>`，列 `{session_id, agent, role, seq, created_at, updated_at}`；`seq` = 名单固定顺序）· `group_round_targets`（每【大轮次】的投递对象；主键 `<session_id>\x00<round 6 位零填充>`，列 `{session_id, round, agents[], created_at, updated_at}`）。两表**逻辑外键 = `session_id`**（关联 `sessions.kind == group_chat`）；读写经 `data-session-group-*`（**不广播 -refresh**）；**无二级索引**（单前缀 Seek）。设计见 [群聊会话设计方案 §5 · D44](../../agent-chat/群聊会话设计方案.md) · [12-数据层 §6.1](../10-architecture/12-数据层.md)。

---

## 6. 依赖

- **上游**：`src/lib`(paths) · bbolt。
- **下游**：server（`sessionStore`/`taskManager`）· gui 桥（`dataViaPersist`）· 各插件。

---

## 7. 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| 长连接缓存 + 引用计数 | 按**最终 db 路径**缓存，计数归零才 Close | bbolt 单文件独占；不同 data_dir 可能同文件 |
| 请求与应答同主题 | 无 `.reply`；靠 `ok` 字段防环 | 减少主题数量 |
| refresh 只发不订阅 | 结构上消除自环 | 替代旧"通配 + ok 过滤" |
| knowledge 域直接文件读写 | capability 文件树不落库 | 原语本质是文件 |
| 无历史迁移 | 旧库/旧 config.json 不导入 | v6 明确 |

---

## 8. 场景与边界

- 未注册 instance → `ErrLayerUnavailable` / "instance not registered"。
- 跨进程：**work-dir 占用锁**（`src/lib/core/lockfile`；启动 / 选目录入口非阻塞校验，I-74）拒绝同用户同项目双开 GUI；prj / prjusr 库的 bbolt 独占 flock 为底层兜底。
- CLI：`dataDir` 非空时 prj 与 prjusr 同文件（`<dataDir>/chonkpilot.db`）。
- 删除本层 key（`DeleteConfig`）= 恢复继承（不存在视为成功）。

---

## 9. 现状与待办

- ✅ v6 骨架落地：三级路径、`project-id`、同构 migration、逐 key config。
- ✅ S2–S7 完成：死配置清理、专用表 `llms`/`mcps` 迁移、prjusr 落地（会话/轮次/消息/任务树/快照 + 个人运行态 key 路由）、capability 文件树（场景/知识库 + `summary.prompt.md`）、UI 改造、契约对齐——见 [02-配置层级 §10](../00-overview/02-配置层级.md)。
- ✅ 场景已文件化：`<级别根>/capability/scenarios/<场景目录>/`（四级；原 `scenario_list` / `scenarios` 桶不再作为主数据）。

---

## 10. 关联测试

`src/test/chonkpilot-data/unittest/`：路径解析、fallback 逐级覆盖与回落、`project-id` 生成/复用/删除、Query 过滤/排序/游标。
