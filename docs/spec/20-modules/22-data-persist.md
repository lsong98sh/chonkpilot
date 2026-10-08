# 22 · data-persist（src/lib/data 数据面：存储内核 + persist 服务）

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
| session | `data-session-{list,get,history,latest,title,delete,active-set,active-get,content,ensure-session,ensure-turn,append-message,set-summary,complete-turn,cleanup-stale,load-messages,context}` | prj/prjusr |
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

---

## 5. 数据结构与存储

见 [12-数据层 §6](../10-architecture/12-数据层.md)：三层同构桶（`_meta/_migrations/config/llms/mcps/sessions/turns/messages/scenarios/permission/tasktree/meta` + 索引桶）；config 记录 `{"v": value}`。
persist 运行态：实例视图（订阅 `instance-register/heartbeat/exit`）、按路径连接缓存（`shared` + 引用计数）。

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
