# 21 · src/lib/llm（会话 / LLM 服务层）

> 日期：2026-09-10 ｜ 状态：✅ 与代码一致
> 关联：[13-生命周期](../10-architecture/13-生命周期.md) · [01-端到端数据流](../00-overview/01-端到端数据流.md) · [12-数据层](../10-architecture/12-数据层.md) · [26-mcp-gateway](26-mcp-gateway.md)
> 代码目录（D-28）：`src/lib/llm/`（`server/` 包 + `httpapi/`）；**服务端 exe 外壳 = `src/server/`**（`chonkpilot-server.exe`，gui 与 browser 共用）

---

## 1. 职责与边界

- **一句话**：LLM 会话与任务编排服务层——受理轮次、构建上下文、驱动 LLM 流式对话与工具循环、回填 tool_pair、编排任务树并落库。
- **做**：轮次状态机、工具调用经 gateway 唯一入口、子会话与批处理编排（`llm_run` DSL）、任务树与取消级联、实例管理、会话锁、上下文压缩触发、提示词优化。
- **不做**：不直接执行工具（经 gateway）；不直连数据库（经 `data-*` 消息面 persist）；不持沙箱。

---

## 2. 形态与部署

| 项 | 值 |
|----|----|
| 编译形态 | lib + exe（console 常驻） |
| exe 命名 | `chonkpilot-server.exe`（GUI/CLI 以 lib 内嵌） |
| service 形态 | ❌ 无（main.go 无 `--service`，未 import `winsvc`） |
| 内嵌装配 | persist + gateway + mcp-server（`!DisableMCP`）+ 插件 compress→history→codegraph |
| 参数 | `--work-dir` · `--servers-file` · `--manage-addr` · `--llm-base`（默认 `http://127.0.0.1:8901/v1`）· `--llm-model`（默认 `mock`） |

`Options` 要点：`LLMBase` / `LLMModel` / `UsrPath` / `DisableMCP` / `MCPServerRoot` / `GatewayServers(File)` / `GatewayManageAddr` / **`AsyncMode`**（CLI 传 `never`） / `Plugins`。

### 2.1 用户维护 MCP 的启动注册（2026-09-11 已实现，T-25 / D-18）

> **目标**：用户在「MCP 配置页」维护的 server 定义（**四级文件化** `<级别>/capability/mcps/<名>.json`）在 **LLM 启动时**注入内嵌 gateway —— 此前该定义**只落库、无消费方**（见 [40-演进计划](../40-roadmap/40-演进计划.md) T-25）。
>
> **单体（src/gui / src/cli 内嵌本 llm server）的 MCP 配置一律取自四级文件化视图，不读 exe 同目录 `config.json`**（2026-09-14 去依赖）；exe 同目录 `config.json` 的 `mcpServers` 段属 mcp-server / mcp-gateway **独立 exe** 自身行为（见 [25-mcp-server](25-mcp-server.md) / [26-mcp-gateway](26-mcp-gateway.md)）。
>
> **〔订正（2026-10-01，[42 §2 (211)(212)](../40-roadmap/42-决策记录.md)）：配置来源 = 四级文件化视图（唯一来源）〕** MCP server 定义**载体** = **`<级别>/capability/mcps/<名>.json`**（四级 app/user/project/prjusr）。装配读点 `gateway_servers.go`：
> ① `loadMcpFileEntries` 经 data 门面 **`McpAPI.McpList`**（`s.cfg`）读**四级合并生效视图**（同名**最具体级优先、整条覆盖** `prjusr > project > user > app`）；
> ② 合并后交 `mergeGatewayServers(nil, …)` → `Params.Servers`（system 基底生产恒传 nil）。**同名跨级只生效一份 → 只 spawn 一份**（以生效定义为准）。
> **旧 usr KV `mcpServers`（专用表 `mcps`）已彻底废弃**（2026-10-01）：**代码零兼容、不再回落**，历史数据不迁移。
> **保存即生效** = 订阅 **`data-mcp-refresh`**（四级文件配置 save/delete）→ `reconcileUserMCPs` 增量对账（`servers/register|unregister`）。
> 下文表格为订正前（仅 usr `mcps`）口径，**字段映射 / 分流 / 约束仍适用**，来源描述以本条为准。

**读点与装配**（实现文件 `src/lib/llm/server/gateway_servers.go`，接线 `server.go` 的 `New`（gateway 构造前））：

| 步 | 动作 |
|:--:|------|
| 1 | **经 data 门面** `McpAPI.McpList`（`s.cfg`，inline 绑定 = 同进程直调；**不持库句柄**）读**四级文件化合并生效视图** → 定义数组（`loadMcpFileEntries`）（2026-09-21 订正：原「直开 usr 库 `data.OpenSharedLayer` + `db.Table("mcps")`」已删；2026-10-01：来源由 usr `mcps` 改为四级文件） |
| 2 | 过滤与分流：`enabled=false` 跳过（记日志）；**有 `runtime` → spawned（网关拉起，`ServerEntry.Runtime` + `Args`）；有 `url` → proxied（连接已运行 server）；两者皆空 → 跳过**（记日志）。其余规范字段（`transport`/`description`/`category`/`namespace`/`env`/`headers`/`cwd`/`hot_tools`/`timeout`）**原样透传**，不做子集裁剪（字段与校验以 gateway `servers.list`/`servers.register` 为唯一来源；2026-09-13 修正旧"仅 URL 型 proxied"限制，并将原 `command` 整条命令行字符串改为 `runtime`+`args`） |
| 3 | 映射 `[]mcpgateway.ServerEntry`：`ID←name`、`Name←name`、`URL←url`、`Description←description`、`Transport←transport`（`"direct"` 归一为空 → gateway 按 URL 推断 http/proxied）、`Enabled=true`、`Scope=""`（global）、**`Origin`**（`user`，见下） |
| 4 | 作为 `Params.Servers`（`= opts.GatewayServers ++ 本处装配`）传入内嵌 gateway，**优先于** `ServersFile` |
| 5 | gateway `Start` 逐条 `connectServer`：**单条失败记日志继续**（与 `servers.list` 同语义），不影响引擎启动 |

**时序方案（选型）**：在 `server.New` 构造 gateway 前**读**（非经总线 `data-user-config-load`）——persist 服务订阅在 `Start` 后才就绪，经总线读须把 gateway 构造整体推迟到 `Start`（牵动 `s.gw`/`s.mcpServer`/`s.mcpCfg` 生命周期）。**2026-09-21 订正**：读法由「直开 usr 库（`data.OpenSharedLayer`）」改为 **data 门面 `McpAPI.McpList`**（`s.cfg` = inline 绑定，同进程直调，构造序在 gateway 之前）——读点无时序问题且**库句柄不出 data 组件**（21 §1）。

**D-18 结论（2026-09-11 定）**：

- ① 注册 **`scope` = `global`**（`ServerEntry.Scope` 留空）。
- ② 启用标识 = 四级文件化 MCP 条目的 `enabled` 字段（数据层已按名整条合并 → 同名只生效最具体级一份）。
- ③ **保存即生效（2026-09-15 订正；2026-10-01 触发主题订正）**：订阅 **`data-mcp-refresh`** → `reconcileUserMCPs` 做**增量对账**（变更/停用/删除 → `servers/unregister`；新增/变更 → `servers/register`；未变更条目不动），**零新增主题**（`gateway_servers.go`）。
- ④ **来源（Origin，2026-09-12 新增；2026-09-14 收敛；2026-10-01 来源改文件）**：四级文件化 MCP 定义 → `Origin=user`（**外部/用户定义**：不注入 `_meta`/`CHONKPILOT_*`）。原「系统级 `<exeDir>/config.json` 的 `mcpServers` → `Origin=builtin`」已随**单体去 exe 目录 `config.json` 依赖**移除（`loadSystemMCPEntries` 已删；builtin 来源仍由 gateway self / dir 节点承载）。见 [26-mcp-gateway](26-mcp-gateway.md) 来源标注段。

**边界（设计约束）**：

- **不触消息面**：注册走进程内 `Params.Servers`，**不经 MQ**——[61-消息一览](../60-reference/61-消息一览.md) **不新增消息**；gateway 的 `servers/register` 方法面继续保留给**运行时**动态接入（第三方 / 测试 / 按 instance）。
- `-servers-file`（`servers.list`）保留为**部署形态**的补充来源（与用户配置并存，`Servers` 优先）。
- 与 capability 的关系：`MCPServer`（self）= exe 同级 `capability` 扫描出的**内置**能力；用户维护 MCP 属**下游 server**，两者在 gateway 内聚合（`scope` 语义见 [26-mcp-gateway §5.1.1](26-mcp-gateway.md)）。
- exe 同目录 `config.json`：gui（`bridge/builtins.go`）仅读其 `mcpServers` 段作**只读内置项展示**（原 `llms` 段展示已移除）；llm（运行时装配）**不再读取**（单体 MCP 配置 = 四级文件化 `<级别>/capability/mcps/`）。原"gui 与 llm 两侧各自独立读取"表述随 llm 侧读点移除作废。

---

## 3. 对外接口

### 3.1 订阅的方法面（`Start`）

| 相对主题 | handler | 说明 |
|---------|---------|------|
| `session-start`（llm-start） | `onLLMStart` | 受理轮次 |
| `session-send`（llm-send） | `onLLMSend` | 驱动：text-user / tool-result / text-notify |
| `session-cancel`（客户端 topic `llm-cancel`） | `onLLMCancel` | 取消 |
| `session-ask-reply`（客户端主题 `ask-user-reply`） | `onAskUserReply` | 提问应答 |
| `task-stop` | `onTaskStop` | 手动结束任务（级联） |
| `tool-retry` | `onToolRetry` | 重试最后一条 interrupted 工具 |
| `prompt-optimise` | `onPromptOptimise` | 提示词优化（流式） |
| `domain-tool-call` | `onDomainToolCall` | 域工具远程回调 |
| `llm-simple` | `onLLMSimple` | 单次无上下文 LLM（插件/摘要用） |
| `llm.test-connection` | `onLLMTestConnection` | **2026-09-20 新增（批 3 · ⑮，[42 §2 (133)](../40-roadmap/42-决策记录.md)）**：设置 → LLM「测试连接」的**只读探活**方法面（**点分相对主题**，不经 `frontMethodSubjects` 单字白名单 —— 走桥 / 服务端上行入口的「点分直通总线」分支）。实现 `server/llm_testconn.go`：**复用主路径同一条出网链路**（LR-11 起 = router `Call`：同一 Authorization / 协议分派 / 超时 / 错误分类）发一次最小请求（单条 `hi` + `max_tokens=1`、不下发工具），**首个非错误事件即判连通**并立即 cancel（`latency_ms` = 首包延迟），超时 = 15s 常量 `llmTestTimeout`。**只读、不落库**：用一次性 provider（唯一名 `__probe__*` 登记 → 探测后**注销**，**不改 usr `llms`、不影响运行中 provider**），**不写会话/轮次/消息/历史表**。载荷 `{instance_id, baseUrl, model, apiKey?, protocol?}`（`baseUrl`/`model` 必填，缺 → `error.kind=invalid`）→ 结果信封 `{ok, latency_ms, model_echo?, error{kind, message}}`（`kind` ∈ `network\|timeout\|rate_limit\|server\|auth\|protocol\|invalid`；handler 恒 `return nil`，结果走 `v.Result`）。**安全（硬要求）**：应答与日志一律经 `maskLLMSecrets` 脱敏（先整串替换本次 `apiKey`、再按通用密钥形态兜底）→ 上游回显的 Key 不外泄；payload/返回明细见 [61 §1.1](../60-reference/61-消息一览.md) |

### 3.2 订阅的事件面（`Subscribe`）

`instance-register` → `onInstanceRegister` · `instance-exit` → `onExit` · `mcp-tasks-report` → `onGatewayTaskDone` · `mcp-gateway-changed` → `onMCPChanged` · `task-deleted` → `onTaskTreeDeleted`。

### 3.3 广播（`domainTopics` 映射）

`session-receive`（reason/text/tool-call）· `session-complete`（唯一终态）· `session-compress` · `session-ask` · `session-turn-start` · `task-started`/`task-updated`/`task-done` · `server-starting` · `prompt-optimised`。

### 3.4 域工具与 agent 契约（`server/contracts/`）

- **tools（4）**：`ask_user`（hot, async=never）· `llm_run`（hot, async=always）· `tool_stop`（hot, async=never）· `tool_result`（async=never）。
  - **〔订正注（2026-09-17，[42 §2 (88)](../40-roadmap/42-决策记录.md)）**：这四个域工具在「工具异步」矩阵里**按执行线拆分并冻结模式** —— **类② = `llm_run`（仅异步）**、**类③ = `tool_stop`/`tool_result`（仅同步）**、**类④ = `ask_user`（仅同步 + 无超时裁决点，不纳入「⑤ 超时自动取消」）**；契约 meta 实证：`ask_user.tool.md:3-7` = `category=server`/`async=never`/**`timeout=0`**/`hot=true` · `llm_run.tool.md:3-6` = `category=server`/`async=always`/`hot=true`（**无 `timeout` 键**） · `tool_stop.tool.md:3-7` = `category=server`/`async=never`/**`timeout=0`**/`hot=true` · `tool_result.tool.md:3-6` = `category=server`/`async=never`/**`timeout=0`** → **四项契约与决策一致**。`ask_user` 的「不纳入 ⑤」为**我方建议，待用户确认**。上述工具的**提供方归属不变**（均由本模块经 `tools/register` 注册 = `registeredProvider`），仅**处理档位**按执行线归并；详见 [18 §3.1/§4](../10-architecture/18-工具异步超时与取消.md)。**〕**
  - **〔订正（2026-09-27，[18 §3.7 B](../10-architecture/18-工具异步超时与取消.md) · §3.8）**：**原 `tool_stop` 的 `timeout=10`、`tool_result` 的 `timeout=30` 已过期** —— 本批把四域工具的执行硬上限统一为 **`timeout=0`**（**系统工具 `category=server` 不单独设执行硬上限** → **`timeout=0` = 无上限**：`_meta.timeout=0` **显式透出**，gateway `limit<=0` **不设裁决点**、executor `noLimit` **不设执行硬上限**，永远等待、用户可取消；`tool_result` 的 `timeout` **入参**是轮询等待秒数、非执行硬上限）。同批 `[meta]` 已解析并透出：`loadDomainTools` 解析 `category`/`async`/`async-threshold`/`timeout` → `tools/register` 载荷携带 → gateway `registerTool` 写入工具 `_meta`（`async` 为 `auto`/空不显式透出；**`async-threshold` 仅 `>0` 透出**；**`timeout` 键显式声明即透出，含 `0`/`-1`**），故这四项的 `_meta.async` 现可在 `tools/list` 见到。**〔订正注（2026-09-27）**：本条此前写「`_meta.timeout` 缺失 → 执行侧回落全局默认」「阈值/超时仅 `>0` 透出」**作废** —— 超时语义已反转为「未设置 = 回落；`0`/`-1` = 无上限」，见 [18 §3.8](../10-architecture/18-工具异步超时与取消.md) **〕** **〕**
- `ask_user` **目标态（2026-09-11 定）**：入参 `questions[]`（一次 = 一表 N 问）；答复**增量写穿**（ask 节点 `content`，落点方案 A）；**全部题答齐才回一个 tool_result**；turn 等待态 `awaiting` 支持**重启续答**（详见 [34 §7](../30-function-points/34-任务.md) · [42 §2 (13)(14)](../40-roadmap/42-决策记录.md)）。
- **agents（7）**：`coder` / `executor` / `planner` / `reader` / `reviewer` / `worker` / `writer`（经 `prompts/register asset_kind=agent` 入目录，**供 `mcp_find(type=agent)` / `mcp_load` 发现与取全文**；不参与 tool-call）。〔**订正（2026-09-25，[25 §5](../10-architecture/25-MCP与场景分层模型.md) · [42 §2 (170)](../40-roadmap/42-决策记录.md)）**：**agent 退出资产面** —— 撤 `prompts/register asset_kind=agent` 与 `syncScenarioAgents`；`mcp_find` 的 `type` 收敛为 **`tool|skill|resource|all`**（**去 `prompt`/`agent`**）、`mcp_load` 的 `kind` 为 **`tool|skill|resource`**、**`prompts/list` 不得再列 agent**；LLM 改由**注入的团队列表**（场景层）知道可委派谁（🔵 待实施，见 [41 G-47](../40-roadmap/41-未决项登记.md)）〕
- 加载：`//go:embed contracts/tools/*.tool.md`（**2026-09-25 起 agent 不再 embed**，见上「agents」订正注）→ `domainmd.go` 分段解析（`[description]`/`[parameters]`/**`[meta]`** —— `[meta]`（每行 `key=value`）现**解析并携带** `category`/`async`/`async-threshold`/`timeout`）→ `domainmcp.go` 经 `tools/register` 注入 gateway：**载荷现携 `category`/`async`/`async_threshold`/`timeout`**（`hot` 保留），gateway `registerTool` 据此写入工具 `_meta`（`async` 为 `auto`/空不显式透出；**`async-threshold` 仅 `>0` 透出**；**`timeout` 键显式声明即透出，含 `0`/`-1` = 无上限**，见 [18 §3.8](../10-architecture/18-工具异步超时与取消.md)）。

---

## 4. 内部控制流

### 4.1 轮次状态机（`turn.go`）

```text
llm-start → newTurnCtx：BuildContext(include_snapshot) → 场景 system prompt 前插 → 子轮 persona → 载 LLM 参数 → go loop()
loop：select { done | asyncDone → onAsyncDone | in → chatOnce }
chatOnce：
  迭代上限 maxToolIterations=20
  → LLM 流式（reason/text/tool-call 广播）→ 空回复 EMPTY_REPLY / 断链有内容内部"继续"（≤maxAutoContinue=3）
  → finish_reason=length 且未超限 → 续写；否则 complete
  → 工具循环：逐 tool-call → 剥 self_ 前缀 → ask/llm/task/普通工具分派
       普通工具 gc.Call（异步 → pending + setGwTask；同步 → 喂回）
  → 有 pending 则 stash；否则递归续轮
```

常量：`maxToolIterations=20` · `llmRetryCount=2` · `retryDelay=5s` · `maxAutoContinue=3`。其中 **`llmRetryCount`/`retryDelay`（及超时 `ResponseTimeout`/`StreamTimeout`）为「回落默认」**——2026-09-13 起经 `loadLLMRuntimeConfig`（`server.go:1491`，`data-user-config-load` 读 usr 标量）加载，在 `newTurnCtx`/`recoverTurnCtx` 各一次，**下个新 turn 生效**；读取失败/键缺失/非正值时用上述常量。

**上下文拼接（`assemble.go`）**：全量历史按 turn（边界 = `isTurnBoundary`）拼接 —— **三段结构（2026-09-25 口径 X）**：**完整区**整轮**原文**（范围受 `keep_full_max_turns`(N) + `keep_full_max_tokens`(M) 双条件约束，均项目级配置，默认 10 / 24000；`loadKeepFullTurns`/`loadKeepFullTokens`，**旧键 `keep_full_turns` 读时兼容**）；**简化区**（再往前，**brief token 累计 ≤ `compress_token_threshold`(T)**）→ **【简化态原文】仅 text**（`data.BriefMessages`：去 reasoning / tool_call / tool_result；T 由 `loadBriefBudget` 读，默认 20000）；**摘要区**（更早）→ 已由压缩插件落为前导 system 摘要（原样透传），若残留同按简化态投影；重试/恢复轮强制全量（`forceFull`）。**边界算法唯一来源 = `data.LocateZones`**（**与压缩侧同一函数**：完整区 = `LocateFullTurnCount`（N/M 取先到，**本轮恒保留**，口径 V，下限 1）；简化区 = 自完整区向前逐轮累加 brief token ≤ T）。**预存 token 优先**（P3）：各轮 `full_tokens`/`brief_tokens` 随 `data-session-context` 的**只增**伴随数组 `turn_tokens` 带回（`BuildContextTokens` → `tc.turnTokens` → `splitTurnTokens`），**尾部对齐 + 缺值回退实时估算**（`data.ResolveStoredTokens`）→ 生产判定真正读取预存值。**取值语义（口径 W）**：`N==0`/`M==0` = 该条件不启用；**两者均 0 → 不压缩**（保留全量）。摘要压缩仍由压缩插件落快照（本处只做粒度收敛与简化态投影，不做摘要、不写快照）。

**兜底归并（口径 Y，2026-09-25）**：属压缩插件职责（本模块只经 `session-compress` 载荷透传真窗口 `max_context_token` + 输出预留 `max_output_token`，见 [28 §3.1](28-plugins.md)）——三段 + 输出预留超窗口 → 合并【摘要 + 简化区原内容】→ 重新摘要（目标 = 窗口 1/2）→ 简化区清空。

### 4.2 终态（`finish`）

`CompleteTurnTokens(status, finish_reason, full_tokens, brief_tokens)` → `SetSnapshot(session, hist+本轮消息, turn)` → 广播 `llm-complete` → 广播 `llm-compress`。`status ∈ complete | incomplete | interrupted | error`。

- **预存 token（2026-09-25，P3/P4）**：终态写轮次时一并预存该轮 `full_tokens`（**完整态** = 该轮全部消息，`data.EstimateTokensOfMessages`）/`brief_tokens`（**简化态** = **仅 text**，`data.EstimateTokensOfMessages(data.BriefMessages(turn))` —— 去 reasoning / tool_call / tool_result），经 `data-session-complete-turn` 的**只增可选字段**落 turns 行（详见 [61 §3.2a](../60-reference/61-消息一览.md)）。**口径与组装侧简化区投影 / 压缩侧简化区预算同一构造**（`turnTokenCounts` ↔ `assembleTurns` ↔ `data.BriefMessages`，P4 同源一致性）。读取侧：`data-session-context` 结果**只增**伴随数组 `turn_tokens`（见 [61 §3.2a](../60-reference/61-消息一览.md)）。
- **`llm-compress` 载荷（只增）**：唯一终态落库后随事件携带**可选** `max_context_token` / `max_output_token`（各自 = provider 对应字段 `maxContextToken` / `maxOutputToken`：**上下文窗口**（兜底归并窗口来源）/ **最大输出 token**（输出预留 + 摘要目标基数），nil/未配置则不带；见 [28 §3.1](28-plugins.md) 订正注）。**〔2026-09-25 口径 Z3/Z4〕**：替代上批以 provider `maxTokens`（输出上限）当窗口代理 + 常量 4096 预留的误用；**载荷字段命名统一 snake_case**（旧名 camelCase `maxContextToken`/`maxOutputToken` 与更早 `window` 由插件**只读兼容**）。

- **终态载荷** `{status, finish_reason, code?, message?, text?, **retryable?**}`；`finish(tc, status, finishReason, code, message, text, retryable *bool)` 为**单点终态**。
- **`retryable` 为可选字段（2026-09-16，S21 修复）**：非 nil 才随 `llm-complete` 携带。**仅错误终态携带**——`complete()`（正常/取消）传 nil **不带**；`llmError()`（`EMPTY_REPLY`/`TOOL_LOOP_LIMIT`/`DB_ERROR`，无 `*LLMError` 分类来源）**固定 `false`**；`llmErrorRetryable()`（`LLM_REQUEST_FAILED`/`LLM_STREAM_ERROR`）取 `*LLMError.Retryable`（`turn.go` `retryableErr`；超时/网络/429/5xx → `true`，鉴权/协议 → `false`）。契约详见 [61 §4.3](../60-reference/61-消息一览.md)。
- 消费方 = 前端：`retryable=true` → 静默自动续写；`false` → 不自动续写、显示错误气泡 +「继续」（手动）。

### 4.3 任务树（`tasks.go` / `tool_task.go`）

- `taskManager`：`TaskNode` 六态 `pending/running/done/error/cancelled/interrupted`；上限 `maxTaskNodes=200`、`maxActiveTasks=50`；终态 5 分钟淘汰；~~落库 `data-tasktree-upsert`~~。
  > **〔2026-09-18 订正（[42 §2 (96)](../40-roadmap/42-决策记录.md)，Task 层 P2 落地；依据 = 用户已确认「payload 可以增加必要的」+「依次完成 P1、P2 以及剩余未完成的决策」）〕**：**任务状态唯一权威 = Task 层（`src/task`，与 LLM/Data/Filesys 平级）**；本模块 `taskManager` **只保留运行态视图（内存 `nodes`）+ 事件发布**（`tasks.started`/`tasks.updated`/`tasks.done`，载荷增补 `workdir`），**不再自写库**——原文「落库 `data-tasktree-upsert`」的**落库调用已移除**（全仓唯一发送点 = 层 `src/task/store.go:103`）。**启动遗留清理 `cleanupStaleTasktree` 已并入层 `Recover`**（`src/llm/server/tasks.go` `recoverStaleTasktree` 只做转发：`tm.srv.taskLayer.Recover(instanceID)`；层把库中非终态 `pending/running/awaiting/detached` 一律标 `interrupted`、**不自动重跑**）；**控制面状态判定以层为权威**（`cancelSubtree` 经 `taskLayer.CancelSubtree` 判定可取消集合 + 层 `OnCancelExec` 回调执行侧 → 既有 `onTaskCancel`→`provider.Invalidate`）。内存 `nodes` 降级为**运行态视图**（DB 仍为权威）。见 [21-任务层设计方案](../10-architecture/21-任务层设计方案.md) §9.2。
- 任务型工具：`tool_stop`（级联 `cancelSubtree`）/ `tool_result`（取转后台结果）；两者经 gateway 唯一入口（域工具回调）同步执行。
- **任务编排分层（2026-09-17，[42 §2 (90)](../40-roadmap/42-决策记录.md)）**：**任务编排归本模块** —— 任务树/级联/批量/任务事件（`tasks.started`/`tasks.updated` 节流 ≤250ms/`tasks.done`）由 `server/tasks.go` 编排（节点 `kind` = `tool`/`llm`/`ask_user`，`parent_id` 挂父、`top_session` 归属；`tasks.go:1-6` 注释原文「**gateway 是纯执行层，不对前端广播任务事件**」）；**gateway 只做单次 `tools/call` 的执行层异步/裁决/取消**（`src/gateway/gateway/tasks.go:1-4` · `mcpgateway.go:640-651`），终态经 `mcp-tasks-report` 回报本模块。**取消链** = `cancelSubtree`（`server.go:1071-1085` `onTaskStop` / `tool_task.go:39-67` `runToolStop`）→ 子树登记的 gwID 逐个经**进程内 sink** `gc.TaskCancel` 交 gateway（**2026-09-18 起为 `s.execSink.CancelExec`**——`gwclient.TaskCancel` 已移除，[42 §2 (99)](../40-roadmap/42-决策记录.md)）；`process_id` 分支直接 OS kill（`tool_task.go:48-57`）。**既有缺口（未修）**：`turn.go:609` 仅在「已转异步」分支登记 `gwTaskID` → 在飞的 **manual / never** 任务打不到级联取消（[41 I-83](../40-roadmap/41-未决项登记.md)）。详见 [18 §3.6](../10-architecture/18-工具异步超时与取消.md)。
- `ask_user` 表单（**目标态**）：`questions[]` 一表 N 问；节点 `content` 承载表单态（questions/answers/state）；**增量答复写穿** → **全部题答齐才聚合为一个 tool_result**；turn 停在 `awaiting`（免被启动清理误标 `interrupted`），重开经 `recoverTurnCtx` 重播表单。

### 4.4 子会话与 DSL（`subllm.go` / `jobdsl.go`）

- `isLLMTool` 仅 `llm_run`（唯一注册 llm 型）→ `runSubJob` 执行 DSL 作业。`agent_list` 已下线（2026-09-11），**子 agent 发现改由 meta 工具 `mcp_find(type=agent)` 承担**（域 agent 经 `prompts/register asset_kind=agent` 入目录，`mcp_find{query|purpose}` 可检索/语义推荐）。**〔2026-09-25：`agent_list` 注册已从代码移除（`registerDomainTools` 现仅注入 4 个域工具契约），本条与代码一致；见 [42 §2 (167)](../40-roadmap/42-决策记录.md)〕**〔订正（2026-09-25，[25 §5](../10-architecture/25-MCP与场景分层模型.md) · [42 §2 (170)](../40-roadmap/42-决策记录.md)）：agent 退出资产面 → `mcp_find(type=agent)` 不再成立（`type` 收敛为 `tool|skill|resource|all`）；改由注入的团队列表（场景层）知道可委派谁。🔵 待实施〕
- **子轮次 agent 定义接线（AG-1/AG-2，2026-09-24 落地，[42 §2 (164)](../40-roadmap/42-决策记录.md)）**：`agents` 由 `map[string]struct{}` 改存 **agent 定义**（`domainmcp.go`：`loadScenario`/`scenarioAgentOf`/`mainScenarioAgent`/`childAgentSystem`/`resolveAgentLLMName`/`parseToolWhitelist`）；**四项生效** —— `prompt`/`delegateCond` → 子轮 system（`childAgentSystem`）；`tools` → 工具下发面（`turnCtx.allowedTools` + `llmTools()` 过滤）；`llmRef` → provider（子轮 = 被委派 agent，顶层 = 主 agent）。**`llmRef` 逐级回落**：`agent.llmRef` →（空）→ `llm.<子系统>` →（空）→ 继承通道（顶层 `llm-start.llm` / 子轮父轮 provider）→（空）→ `defaultLLM` →（空）→ 系统默认（经 `data.LLMRefName` 折算 int 索引）；**热生效** = `newTurnCtx` **每轮解析一次**、无进程级缓存（AG-C5）。**空值 = 不限制/不干预**（`tools` 空/`[]` = 不限制）。删除已无调用方的 `loadScenarioSystemPrompt`（主 agent = `mainScenarioAgent`）。**〔2026-09-25 T6（[25 §8.1 #8](../10-architecture/25-MCP与场景分层模型.md) · [42 §2 (170)](../40-roadmap/42-决策记录.md)）〕**：内置 agent 集**不再是代码内嵌契约**（原 `//go:embed contracts/agents/*.agent.md` 已撤）—— 统一为 **app 级场景**（`<exeDir>/scenarios/*/`；出厂内容由 **embed** 提供、app 初始化（首次 list）缺失即物化，**app 级可编辑**，25 §6），`registerDomainAgents` 经数据层门面 `facade.ScenarioAPI.ScenarioList` 读取（路径/三级根规则单源在数据层，本层不拼路径）；`registeredAgentDef` 的回落来源随之改为「app 级场景内的同名 agent」（全量链路见 §4.4 上方注）。**〔订正（2026-09-26）：`builtin-agents/` 已删除 —— 发布 `scenarios/` 仅 `default/`（「开发场景」）；「通用」= 无场景的通用模式（无目录）。`registerDomainAgents` 仍扫描全部 app 级场景（当前仅 1 个）〕**
- **场景子 agent 可发现 + 白名单硬拒（G-45 ②③，2026-09-25 落地，[42 §2 (166)](../40-roadmap/42-决策记录.md)）**：② 场景子 agent 的定义只活在场景记录（capability 扫描面**不含** `*.agent.md`）→ `newTurnCtx` **每轮**把当前场景的**子 agent**（非主 agent）经既有 `prompts/register`（`asset_kind=agent` + **instance 级 `scope`**，复用 [61 §5.1.1](../60-reference/61-消息一览.md) 可选字段、**零消息面变更**）对账进 gateway 资产目录 → `mcp_find(type=agent)` / `prompts/list` 可见（幂等 / 场景切换整批更替 / 实例退出 `exitInstance` 对称注销）；③ `turn.go` 在工具调用**分发前**按 `turnCtx.toolAllowed(name)` **硬拒**白名单外工具（**不落 gateway**、不建任务节点，以失败文本落 role=tool 回喂；名字口径 = LLM 工具面**暴露名**，与 `llmTools()` 同一名字空间）。〔订正（2026-09-25，[25 §5](../10-architecture/25-MCP与场景分层模型.md) · [42 §2 (170)](../40-roadmap/42-决策记录.md)）：② 的 `prompts/register asset_kind=agent` 注入撤销（agent 只注入 context、不注册资产）→ `mcp_find(type=agent)` / `prompts/list` 不再列 agent；③ 白名单调用处硬拒语义不变（[41 G-46](../40-roadmap/41-未决项登记.md) a 的 `mcp_invoke` 旁路加固同保留）。🔵 待实施〕
- `runChildTurn`：子轮**复用同一 turnCtx 状态机**，`Parents=父链+父session`，业务同主轮。
- `llm_run` DSL：`dsl.Parse` + 注入 `LLM "agent" "prompt" ["目的"] [=> 目标]` 动词（第三参可选 = purpose/展示名，即 tasktree 节点 label；缺省回退提示词截断）；`watchCancel` 200ms 轮询根节点取消。
- **路径收紧 + 只读 env（R-11 二次升级，2026-09-12）**：`jobScriptOf` 的 `file` 与 DSL 内所有 `#"path"` 须**绝对 / `~/` / `!/`**，相对路径 → **顶层失败**（`jobFile.abs()` 严格 `paths.ResolvePath(raw,"")`，不再按 workDir 静默 `Join`；执行前经 `dsl.CollectHandleRefs` 预校验字面句柄，含 `{{}}` 的跳过、运行时严格解析兜底）；同时把同一套**只读** `env` 注入 `dsl.Options.Vars`：`CHONKPILOT_WORKDIR`（= 轮次 `req.WorkDir`）/`DATADIR`（= `req.DataDir`）/`TEMPDIR`（= `paths.SetTempRoot(req.InstanceID)` 返回值，按 instance 分目录）/`EXEDIR`（server exe 目录）/`PROJECT`（= workdir）；**无裸名别名**（`WORKDIR` 已删除），LLM 用 `{{env.CHONKPILOT_WORKDIR}}` 显式拼项目内绝对路径（不再依赖隐式 workDir 兜底）。见 [16-路径解析规范](../10-architecture/16-路径解析规范.md) §8。
- **实例上下文改由调用上下文（`_meta`）承载（R-11 二次升级修订）**：`loadExecConfig(instanceID)` 仅注入 usr/prj 执行配置（解释器/chrome/超时/并发/skip_dirs）；实例上下文**不再经 `mcpCfg.SetContext` 注入 tool arguments**，改由每次工具调用经 `mcpgateway.Context`（`mcp-tools-call` payload）→ gateway → mcp-server `params._meta` 透传。`llm-start` 的 `InstanceID` 为空 → 顶层错误（`accepted:false`，不进 turn）；`gwClient.Call` 的调用上下文 `InstanceID` 为空 → 直接返回错误（不下发）。`llm_start` 的 `work_dir`/`data_dir` 随 `instance-register` 绑定，`gwClient.call` 现随 payload 携带 `data_dir`。

### 4.5 LLM 出网（`src/lib/router` 统一入口，LR-11，2026-09-23 起；原 `llm.go` 直连实现已删）

> **〔订正（2026-09-23，[40 §LR §5.1](../40-roadmap/40-演进计划.md)）〕**：llm 侧**不再直连各 provider** ——
> 「一次来回」（`step`）调用统一经 **`chonkpilot-router`**（`Server.chat` → `router.Call`，接线落 `server/routercall.go`）。**〔2026-09-25 术语统一（[60-名词约定 §4](../60-reference/60-名词约定.md)）：原「一次」/「一次调用」→「一次来回（`step`）」；代码标识符 `router.Call` / `Call` 不改名〕**
> 协议实现（openai chat / responses / anthropic / 内置兜底 echo）、SSE 解析、错误分类与连接池全部归 router；
> 本模块只保留**契约类型**（`ChatOptions` / `StreamEvent` / `ErrKind`）与**折算层**。

- **协议分派**：`router.Call` 按 provider `Spec.Protocol` 分派 —— `openai`（默认，`/chat/completions`）/ `responses`（`POST {base}/responses`）/ `anthropic`（`/v1/messages`；usr 配置暂不暴露）/ `echo`（内置兜底，不发 HTTP，见 §4.8）。
- **provider 配置**：命中 usr `llms`（或内置保留名 `echo`）时 `baseUrl`/`apiKey`/`model`/`temperature`/`maxOutputToken`/`thinking`/`reasoningEffort`/`protocol`（+ 2026-09-25 新增 `maxContextToken`）**以配置为准**；`apiKey` 非空 → 请求头 `Authorization: Bearer`；`baseUrl`/`model` 空 → 回落 exe flags（`server/routercall.go specFor`）。**〔2026-09-25 口径 Z1〕**：原 `maxTokens` 改名 `maxOutputToken`（= 请求体 `max_tokens`/`max_output_tokens`）；旧键 `maxTokens` / `max_tokens` **读时兼容**（只读不写）。
- **provider 注册**：usr `llms` 全量 + exe flags 隐含默认（保留名 `__default__`）经 `reconcileLLMProviders`（`Reconcile` 增量对账）进 router；`data-user-config-refresh` 触发**动态增减**（零新增主题）。调用路径另做幂等 `Register`（配置刚改、refresh 未到亦可用）。
- 超时分级：`ResponseTimeout=120s`（首字节）· `StreamTimeout=60s`（chunk 间隔）**经 `router.CallOptions` 下发**（router 侧同名默认）。**2026-09-13 起为回落默认**——优先取 usr 配置 `responseTimeout`/`streamTimeout`（`loadLLMRuntimeConfig`，下个 turn 生效）。
- 错误七类（router 口径）：`network`/`timeout`/`rate_limit`/`server`（可重试）· `auth`/`protocol`/`invalid`（不可重试）；llm 侧折算为六类（`invalid` → `protocol`，见 `routercall.go routerKindToLLM`）→ `*LLMError`。
- 流解析（归 router）：SSE 行解析 `data:`/`[DONE]`、按 `index` 聚合 tool_call（`src/lib/router/internal/adaptor/toolcall.go`）、responses 语义化 SSE（无 `[DONE]`）、Anthropic `content_block_delta`。
- **终止与断链判定（归 router，三协议统一）**：载荷自身表达流结束（chat `[DONE]` / responses 终态事件 / Anthropic `message_stop`）→ **立即收尾**（不等上游关连接，见 `adaptor.ErrStreamDone`）；流在事件边界 EOF 结束却**未收到终态标记**（chat：`[DONE]` 与 `finish_reason` **都未见**；responses / Anthropic：未见终态事件）→ `network`（可重试）→ llm 侧走**断链续写**（[35 §S6/S8](../30-function-points/35-错误处理与恢复.md)）。
- **已知线格式差异**（事件语义不变，见 `routercall.go` 文件头）：请求恒带 `stream_options.include_usage`；输出上限字段按模型名择一（`o1/o3/o4/gpt-5*` → `max_completion_tokens`）；非 2xx 错误体信息串按 JSON 取值。


### 4.6 LLM 请求参数与 systemPrompt 注入

> 本节由原数据流审计稿迁入并按现行代码校正（2026-09-11；**协议分派与 responses 分支 2026-09-15 补**）。

**请求参数完整透传**（`server/routercall.go buildRouterRequest` 按 `ChatOptions` 折算 `router.Request`；`llm.go` 仅留契约类型）：

| 参数 | 来源 / 规则 |
|------|-----------|
| `model` | **provider 配置 `model`**（命中且非空）；**空 → 回落 exe flag `-llm-model`**（不再用 `name` 当 model；`llm-start.llm` 仅作 provider 选择键） |
| `messages` / `stream` / `tools` | 上下文与工具集 |
| `reasoning_effort` | **`Effort` 优先，否则 `Think`**（映射 high/medium/low）；**`think=off` 或 provider `thinking=false` → 不发送**（`resolveThinkEffort`，`turn.go:205-219`） |
| `temperature` / `max_tokens` / `top_p` | 均为指针，**非 nil 才写**（`frequency_penalty`/`presence_penalty` **已从 `ChatOptions` 删除**，无消费方；`top_p` 保留） |

**`responses` 协议分支（归 router：`src/lib/router/internal/adaptor/openai/responses*.go`，`protocol=responses`）**：`POST {base}/responses`，请求体字段 —— `instructions`（历史最前连续 system 消息）/ `input`（字符串或输入 item 列表：`message` / `function_call` / `function_call_output` / `reasoning`）/ `stream` / `temperature` / `max_output_tokens`（= `maxTokens`）/ `top_p` / `reasoning{effort}`（思考力度，替代 chat 的顶层 `reasoning_effort`）/ `tools`（**扁平形态** `{type:function,name,description,parameters}`，无 `function` 嵌套）/ `tool_choice`。响应为**语义化 SSE 事件**（每个事件带 `type` + 递增 `sequence_number`，**无 `data: [DONE]`**）：`response.output_text.delta` / `response.reasoning_text.delta` / `response.output_item.added|done` / `response.function_call_arguments.delta|done` / `response.completed|incomplete|failed`；工具调用按 `item_id` 组装（`call_id`→`ToolCall.ID`），工具结果以 `function_call_output` 回灌；`incomplete`（`max_output_tokens`）→ 本仓 `finish_reason=length`（触发自动续写）；`failed` → `Err`（错误分类）。**白盒**：`src/lib/router/internal/adaptor/openai/responses_test.go`（原 llm 侧 `llm_responses_test.go` 已删，断言随之迁入 router）。

**参数来源与加载时机**：`server.go loadLLMProvider(instanceID, selectedModel)` → `userLLMProvider`（命中 usr `llms`）→ 未命中查**内置兜底保留名**（D-30 后为 `builtinFallbackProvider`，仅 `echo`；原「内置 provider 表」已删）→ 仍不命中回落 exe flags；读取 `protocol`/`baseUrl`/`apiKey`/`model`/`temperature`/`maxTokens`/`thinking`/`reasoningEffort`/`maxToolIterations`，在 `turn.go newTurnCtx` 时**一次加载**，子轮次复用主轮次 provider 与参数。同批由 `server.go loadLLMRuntimeConfig(instanceID)` 经 `data-user-config-load` 读 usr 标量 `responseTimeout`/`streamTimeout`/`retryCount`/`retryDelay`（`newTurnCtx`/`recoverTurnCtx` 各加载一次，**下个新 turn 生效**；`retryCount` 显式 0 = 不重试，其余三项非正值回落常量）。

**systemPrompt 注入链**：`server.go loadScenarioSystemPrompt(instanceID, scenarioID)`（按场景目录名/key 取该场景 `systemPrompt`）→ `turn.go` 在 `BuildContext` 之后将其**前插**为系统提示，随后注入子轮次 persona（`llm_run` 每条 LLM 指令的 agent 参数）。〔**订正（2026-09-25，AG）**：`loadScenarioSystemPrompt` **已删除**（[42 §2 (164)](../40-roadmap/42-决策记录.md)）—— **主 agent** 场景 systemPrompt 现由 `domainmcp.go mainScenarioAgent` 解析；**子轮 system** 由 `childAgentSystem`（含被委派 agent 的 `prompt` / `delegateCond`）承载，见 §4.4。**原文保留**为改前口径。〕

> **〔订正（2026-09-25，[25 §3](../10-architecture/25-MCP与场景分层模型.md) · [42 §2 (170)](../40-roadmap/42-决策记录.md)）—— systemPrompt 改三层拼接（🔵 待实施）〕**：按序拼接 = **全局层**（身份/运行环境，**代码写死**，不落文件/不 embed/不可配置，**属新增**）→ **场景层**（`scenario.description` + **代码按场景 agents 自动拼接的成员段**〔名字 + roleTag + 描述〕）→ **agent 层**（`*.agent.md`，主 agent = `main.agent.md`）。**无场景（通用模式）→ 只注入全局层**。`Scenario.SystemPrompt` **语义重定义 = 场景层**（原"派生 = 主 agent prompt"作废，主 agent prompt 归 agent 层）。现有 `memoryGuide`（门控 `memory.enabled=true`）/`assetGuide`（门控已接入 capability 节点）是**两条功能指引**，与"全局层"**不是一回事**（见 [41 G-47](../40-roadmap/41-未决项登记.md)）。

> 设计要点：模型与生成参数属**用户级配置**（随 LLM 条目），场景 systemPrompt 属**场景文件**（`<级别根>/capability/scenarios/<目录>/`〔**订正（2026-10-01，P1：[42 §2 (207)](../40-roadmap/42-决策记录.md)）**：场景根**移入 capability**（=`<capability>/scenarios/`，原「独立根 `scenarios/` 与 `capability/` 平级」作废）、级别 **三级 → 四级**（新增 prjusr）；**四级均可编辑**〕），两者均在轮次受理时解析并注入。
>
> **〔订正（2026-09-25，[25 §4](../10-architecture/25-MCP与场景分层模型.md) · [42 §2 (170)](../40-roadmap/42-决策记录.md)）—— 工具面语义（🔵 待实施）〕**：**直供**（进入 LLM `tools` 参数）= **`hot` 工具 ∪ gateway in-memory MCP 工具**（`mcp_find`/`mcp_load`/`mcp_invoke`）；⚠️ `hot` **两个来源都要** —— ① 下游 `HotTools`（内嵌 self 节点为 `"*"` = 全部）② **meta 工具显式 `_meta.hot=true`**（self 节点 entry **无 `HotTools`**，`isHot` 不会自动补）。**「通用模式」（无场景）工具面 = 全部 hot（含 core/browser/desktop）+ meta**（此前"仅 core 工具"**已否定**）。**白名单（`agent.tools`）语义修正**：**非空 → 下发面 = 白名单里的工具（不论是否 hot）**；**空 → hot 集**。**既有缺陷**：现 `llmTools()` = `toolsForLLM()`（**只含 hot**）再按白名单**取交集** → **白名单中的非 hot 工具被吞掉**（用户配了却**永远传不到 LLM**），须修（[41 G-47](../40-roadmap/41-未决项登记.md) **T1**）。**执行面**：空白名单 = **不限制**；白名单非空 = **仅白名单内**（含 `mcp_invoke` 目标工具亦须在白名单内）。

### 4.7 prompt 占位符 `{{toolchain.<key>}}`（2026-09-14）

**一句话**：prompt / system 文本里的 `{{toolchain.<key>}}` 按**实例 usr 配置**的工具链路径替换（用户口径："占位符：所有 prompt。名称 toolchain.xxx 包括 java,python 等"）。

| 项 | 规格 |
|----|------|
| key | `java` / `python` / `node` / `go` / `rust` / `c` / `chrome`（`mcpms.ToolchainKeys`） |
| 取值 | usr 标量 `javaPath`/`pythonPath`/`nodePath`/`goPath`/`rustPath`/`cCompilerPath`/`chromePath`（经 `data-user-config-load`，同 `loadExecConfig` 读法；`src/llm/server/toolchain.go toolchainVars`） |
| 替换语义 | 已配置 → 路径；已知 key **未配置 → 空串**；**未知 key → 原样保留**；仅匹配 `{{toolchain.x}}` 整串，**不影响 `{{arg}}`（MCP GetPrompt 实参）与 DSL `{{env.X}}`**；文本无占位符 → **零成本直返**（不发配置读取） |
| 单实现 | `src/mcp-server/server/toolchain.go ReplaceToolchain`（两模块共用） |

**覆盖落点（5 处）**：

| 落点 | 读点 / 装配 |
|------|------|
| 场景 systemPrompt | `mainScenarioAgent`（`domainmcp.go`）→ `replaceToolchain`〔**订正（2026-09-25，AG）**：原 `loadScenarioSystemPrompt`（`server.go:1539`）**已删除**，见 §4.6〕 |
| 记忆库带出指引 | `memoryGuide`（`memory_guide.go:104`）→ `replaceToolchain`；**门控（2026-09-15）**：`memory.enabled` 非 true → **不注入指引、也不请求 `data-memory-list`**；`memory.category.<X>` = false → 该类不列入 |
| 发 LLM 的工具契约描述 | `toolsForLLM`（`server.go:1443`；仅当描述含占位符才懒读配置，替换只改本次下发副本、不动 gateway 缓存） |
| `llm-simple`（含压缩摘要 `summary.prompt.md`） | `onLLMSimple`（`server.go:358`；`replaceToolchain` 调用 `server.go:370`）替换 `system` 与 `prompt` |
| capability `*.prompt.md` / `*.skill.md` | `loadExecConfig` → `mcpCfg.SetToolchain`（`server.go:1677`）→ mcp-server `makePromptHandler` 的 GetPrompt 渲染（见 [25-mcp-server §3.1](25-mcp-server.md)） |

**说明承载（2026-09-15 订正）**：占位符的**用法示例与替换规则说明由场景 system prompt 承载**（默认场景主 agent，`capability/prompts/default/main.agent.md`）〔**订正（2026-09-25，[25 §6/§8.1 #8](../10-architecture/25-MCP与场景分层模型.md) · [42 §2 (171)](../40-roadmap/42-决策记录.md) / T6）**：`persist_scenario.go defaultScenarioAgents` 与「出厂默认物化」**已撤**；出厂默认场景现为 **app 级文件** `src/initdata/capability/scenarios/default/` → 投放 `<exeDir>/scenarios/default/`（与 `capability/` **平级**；2026-09-29：源唯一 = `src/initdata/capability/scenarios/`，不再 embed/物化），**主 agent = `scenarios/default/main.agent.md`**〕，**不单独设指南契约文件**——原拟的 `src/mcp-server/contracts/prompts/core/工具使用指南.prompt.md` **已撤销**（用户口径：「工具使用指南理论上应放在系统的 prompt 里」）。

> **机制边界**：只做文本替换，**不改消息面结构/主题**（[61-消息一览](../60-reference/61-消息一览.md) 未改）；与 DSL 的 `{{env.X}}`（[63-DSL语法](../60-reference/63-DSL语法.md)）互不影响。

### 4.8 内置兜底 `echo` provider（`protocol=echo`，2026-09-15；**D-30 后不再作为可配置项**）

**一句话**：**内置兜底**（代码写死；**D-30（2026-09-22 拍板）后不再作为「可配置 provider」暴露** —— 不进 usr `llms`、不出现在 LLM 设置页 / 聊天选择器）的占位 provider —— **不发任何 HTTP、不注册/不下发任何工具**，取**本轮最后一条真实用户消息**填入**固定文案**后回一条（`收到<用户输入>，目前无法回复，请设置LLM。`；`finish_reason=stop`，单段 content，流式语义同既有 `StreamEvent`）。**「无任何可用 provider」的自动兜底归 router**（`src/lib/router` 的 `builtinFallback`，见 [40 §LR](../40-roadmap/40-演进计划.md)；llm 侧接线归 LR-11）。

| 项 | 规格 |
|----|------|
| 定义 | `name=echo` / `protocol=echo` / `model=echo`；无 `baseUrl`/`apiKey`（`server.go` 的 `builtinFallbackName` + `builtinFallbackProvider`；原「内置 provider 表」已删） |
| 触发 | `llm-start.llm = "echo"`（`loadLLMProvider` 未命中 usr `llms` → 命中**保留名兜底**；其余名 → 回落 exe flags） |
| 行为 | `lastUserText`：倒序取 `role=user` 且 `isTurnBoundary` 的消息（**排除系统注入**：`Kind=notify` 工具通知 / `continue` 续写 / `resume` 恢复）；**回复文本 = 固定文案**（2026-09-24 用户口径）：`replyText(用户文本)` = `收到` + 该条用户消息内容 + `，目前无法回复，请设置LLM。`（**唯一文案源** = `internal/adaptor/echo` 常量 `replyPrefix`/`replySuffix`，**不是**原文回显）；无真实用户消息 → 不发 content 段；**实现 = router `internal/adaptor/echo`（LR-11 起；原 llm 侧 `chatEcho`/`lastUserText` 已删）** |
| 只读行 | **2026-09-26 起无任何内置 / 系统默认只读行**：`gui.system.builtins` 不再下发 `builtinLLMs`（原「系统默认（启动参数）」行随之移除）；LLM 设置页改 2 页签（一览 / 默认模型），「主对话」默认 = usr `defaultLLM`。**〔订正历史（D-30，2026-09-23）**：「系统内置」（echo）只读行先已不再存在。〕 |
| 归一 | `echo` **不**被归一成 `openai`（`NormalizeLLMProtocol`）；白盒 `src/lib/router/internal/adaptor/echo/echo_test.go`（含 `TestReplyTextFixedTemplate` 固定文案逐字断言）+ `router_test.go`（内置兜底文案）+ llm 侧 `llm_echo_test.go`（无 HTTP + 固定文案 / 系统注入不入文案 / 保留名兜底不可配置 / 近似名回落） |

### 4.9 图片（多模态）输入（P2-8，2026-09-15）

**一句话**：图片仍以既有**文本引用** `![名](绝对路径)` 承载（`InputBox.serialize`；**无新 MQ 主题、无 payload/`ChatMsg` 字段变更**），在**构造 LLM 请求体时**（`src/llm/server/llm_images.go`）就地展开为多模态内容块。

| 项 | 规格 |
|----|------|
| chat（`openai` 兼容） | 该条 `content` 由 **string 变数组** `[{type:"text",text}, {type:"image_url",image_url:{url}}]`；`url` = **base64 data URL**（`data:<mime>;base64,…`）。取舍：provider `baseUrl` 多为远端、无法访问本地路径，故自带载荷；代价 = 请求体随图片线性放大 + 首字节略慢（由 `responseTimeout` 覆盖） |
| `responses` 协议 | `input` 的 message item 的 `content` 数组追加 `{type:"input_image", image_url:"data:<mime>;base64,…"}` |
| 源与范围 | 仅 `role=user`；仅**落在上传根**（`<prjusr 数据根>/tmp/uploads`，`imageUploadDir` → `data.PrjUsrDir`）内的引用按图片处理；**根外本地路径不读取、原样保留为文本**（避免用户手写 markdown 被当附件）。**〔订正（2026-09-25，[24 §3.2](24-多窗口模型设计方案.md) MW-8）〕** 上传根与桥 `gui.upload` 落点同源：`data_dir` 非空 → 该目录；`data_dir` 空（desktop 缺省）→ `~/.chonkpilot/data/<prj-id>/tmp/uploads`。~~`data_dir` 空 → 回落 `<work_dir>/.chonkpilot`~~ / ~~`<data_dir>/tmp/uploads`~~（原文保留） |
| 历史 / 压缩规则 | 仅**最近 3 轮**（`defaultKeepImageTurns`，轮边界 = `isTurnBoundary`，与 `assemble.go` 同源）保留原图；更早轮次的引用**降级为文本占位** `[图片: 名]`；`plugin-compress` **无需改动**（早前轮次整轮被摘要替换） |
| 上限 | 类型白名单 `png/jpeg/webp/gif`（扩展名 + **魔数**双重校验，mime **以魔数为准**）；**单图 5MB / 单消息 4 张 / 单请求 8 张** |
| 错误语义 | 文件缺失/不可读/类型不支持/内容不可识别/超限 → `*LLMError{Kind: ErrProtocol, Retryable:false}` 且**不发起 HTTP 请求**（随 `llm-error` 可见，**不静默丢**）；`UploadDir` 为空 → 引用原样作文本发送（纯文本路径不回归） |
| 前端 | 上传中 chip/状态、失败提示；消息内图片经 `/show/` 缩略图（**无新浮层**） |

---

## 5. 数据结构与存储

- 无本地存储：全部经 `data-*` 消息面（`session.go` 的 `sessionStore` 方法 ↔ 主题一一对应）：`ensure-session`/`ensure-turn`/`append-message`/`load-messages`/`context`/`complete-turn`/`cleanup-stale`/`set-summary` + `data-snapshot-set`。
- 内存运行态：`turns map[turn]*turnCtx` · `busy map[session]turn` · `asks map[askID]*askWaiter` · `cleaned map[instance]bool` · `taskManager.nodes`。（**目标态**：ask 等待期 turn 运行态 = `awaiting`（非 `running`）；`asks` 在重开时由落库 ask 节点 `content` 重建 —— 见 §4.3）
- 会话锁：`lockSession`（Windows 文件锁；非 Windows 为 stub，恒失败）。

---

## 6. 依赖

- **上游**：`src/lib`(mq) · `src/data`(persist) · `src/gateway` · `src/mcp-server` · `src/plugin` + `-compress`/`-history`/`-codegraph`。
- **下游**：GUI（内嵌）、CLI（内嵌）、gateway（方法面调用）。

---

## 7. 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| 方法结果用同主题 promise | 无 `.reply`、payload 无 `req_id` | 与 MQ 门面语义统一 |
| tool_pair 归会话层 | gateway 不感知 | 回合产物，创建/回填/续轮在此 |
| `finish` 单点终态 | 正常/错误/取消都收敛 `llm-complete` | 消除 `llm-error` 多通道 |
| 子轮复用 turnCtx | `runChildTurn` 用同一状态机 | 行为一致、代码合一 |
| `AsyncMode` 透传 gateway | CLI 传 `never` 强制同步 | CLI 无后台消费方 |
| 首次使用清理 stale | 多 workdir 服务无法启动时预知数据根 | 改为首个 `llm-start` 触发 |

---

## 8. 场景与边界

- **busy 拒绝**：同 session 已有活动 turn → `{accepted:false, error:"session busy: <turn>"}`。
- **锁失败**：跨进程锁被占 → `session busy (locked)`。
- **实例未注册** → `{accepted:false, error:"instance not registered"}`。
- **用户/项目/项目私有级 capability 动态接入（T-29，2026-09-11；四级 2026-10-01 P4 补 prjusr）**：`onInstanceRegister` 经 `capNodeSpecs` 解析**存在的**用户级 `~/.chonkpilot/capability`（`persist.CapUserRoot`）、项目级 `<workDir>/.chonkpilot/capability`（`persist.CapProjectRoot`）与**项目私有级**（prjusr；根由门面 `KnowledgeRoot(kind=prjusr)` 解析，与知识库/场景写读同源）→ gateway `servers/register`（dir 节点，`scope=instance_id`，节点名 `<id>-user`/`<id>-project`/`<id>-prjusr`）；目录缺失跳过、重复注册幂等（`dirNodes` 记账）、`onExit` 对称 `servers/unregister`。注册后主动 `s.gc.ListTools` 刷新工具缓存（gateway dir 节点注册不发 `mcp-gateway-changed`），使 `hot==true` 的契约进 `toolsForLLM`。**原 `@mcp` 旧名已废弃**（项目根随 [02-配置层级 §7.2](../00-overview/02-配置层级.md) 迁移为 `.chonkpilot/capability`；系统级 `<exeDir>/capability` 仍由 self 节点承载）。
- **agent 工具白名单级别矩阵（P4，2026-10-01）**：`newTurnCtx` 读场景级别（`loadScenario` 增返回 `level`）→ `filterWhitelistByLevel` 按"同级或更高级"矩阵**静默剔除**越权 / 不存在（不在本实例可见工具面）的工具名（工具级别由 `_meta.server.node` → `capfs.LevelOfNode` 判定；场景级别不可判定 / 工具级别无法判定 → 放行；空白名单 = 不限制语义不变）。矩阵单源 = `capfs.AgentToolLevels / LevelAllowed`（`persist` 转发）。详见 [25 §4.5](../10-architecture/25-MCP与场景分层模型.md)。
- **原语保存热生效（capwatch，T-21，2026-09-15 三级根全覆盖；2026-10-01 P4 纳入 prjusr）**：`src/lib/llm/server/capwatch.go` 对**用户级 / 项目级 / 项目私有级** capability 根加 fsnotify 监听（60ms 去抖合并 create/write/chmod），`fire` 按脏标记分流——① **用户级/项目级/项目私有级** → `reconcileCapabilityNodes`（复用 `servers/unregister`+`register` 重建 dir 节点）；② **系统级 app 根**（`<exeDir>/capability`，内嵌 self 节点）→ `Server.reloadAppContracts`：重跑 `RegisterContracts` 进同一内嵌 go-sdk server（同名覆盖更新 / 新增注入）+ `RemoveTools` 回收已删/改名工具 → 复用既有方法面 `gateway/reload` 让 self 节点重拉 list。**增/改/删数十毫秒级生效、app 根只读语义不变（UI 不可改）、零新增消息面主题**。**已知边界**：app 根目前**仅对 `*.tool.md` 做删除回收**（prompt/skill/resource 的删除不回收，保留至重启）；`gateway/reload` 会连带重列外部下游。监听失败不致命（退化为"需重注册/重启生效"的既有行为）。
- **取消**：`llm-cancel` 无 turn 时按 session 回退运行中轮次。

---

## 9. 现状与待办

- ✅ **已决（P0-4）**：`curInstance` 改 **context 承载**（`withInstance`/`instanceFromCtx`；`emitGateway` 从 ctx 取 `instance_id`）。
- ✅ **不支持非 Windows（D-14，2026-09-11）**：非 Windows `lockSession` stub 保持，文档标注"仅 Windows"，不再作待办。
- ⚠️ `Start` 中契约注册失败仅打印并"空能力面继续"；内嵌 gateway 失败同样仅打印。
- 🗄 `server/items.json` 已删除（P0-4，无引用）。
- ✅ **域工具面收敛为 4 个（2026-09-11）**：`ask_user` / `llm_run` / `tool_stop` / `tool_result`。废弃工具 `tasks_run` / `tool_get_result` 删除，`tool_get_result` 更名 `tool_result`（内部判定统一，D-08 落地）；`agent_list` 删除但**能力无损**——子 agent 发现由 gateway meta 工具 `mcp_find(type=agent)` 承接；legacy llm 别名（`llm_job`/`call_llm`/`llm_call`/`batch_llm`/`llm_batch`）一并删除。**〔2026-09-25：`agent_list` 注册已从代码移除（`registerDomainTools` 仅注入 4 个域工具契约），见 §4.4 / [42 §2 (167)](../40-roadmap/42-决策记录.md)〕**
- ✅ **llm_run 路径收紧 + env（2026-09-12，R-11 二次升级）**：`file` 与 DSL 内 `#"path"` 拒绝相对路径（顶层失败，带行号/用途）；`jobFile.abs()` 由「workDir 兜底 Join」改为严格 `paths.ResolvePath(raw,"")`；注入只读 `env`（5 变量 + `WORKDIR` 别名）供 `{{env.CHONKPILOT_WORKDIR}}` 拼绝对路径；同批更新 `llm_run.tool.md`。测试 `jobdsl_test.go` 相对用例改绝对/env，并补相对失败与 env 成功两例。

---

## 10. 关联测试

`src/llm/server/*_test.go`：`llm_test`（流解析/错误分类）· `assemble_test`（**三段拼接**：完整区原文 / 简化区**仅 text**（`TestAssembleTurnsBriefZoneTextOnly`/`TestAssembleTurnsThreeZones`）/ token 边界与预存优先（`TestAssembleTurnsTokenBound`）；**2026-09-25 口径 X**）· `llm_testconn_test`（**2026-09-20 新增**：`llm.test-connection` 只读探活 —— `TestLLMTestConnectionOK` / `...AuthMaskedKey`（Key 脱敏）/ `...Network` / `...Timeout` / `...InvalidParams` / `...ResponsesProtocol`（Responses 协议映射 `max_output_tokens`）/ `TestMaskLLMSecrets`，见 §3.1）· `llm_images_test`（图片多模态：`TestChatImageContentParts`/`TestResponsesImageInputItem`/`TestChatImageErrors`（6 子例，**HTTP hits=0**）/`TestImageRecentTurnsRule`/`TestImagesDisabledPlainText`/`TestTurnSendsImagesEndToEnd`，见 §4.9）· `turn_test`/`turn_cover_test`（续写/空回复/auth/异步/压缩事件/取消）· `tool_task_test`（tool_stop 级联/tool_result 取结果）· `tool_retry_test`（定位/重启恢复）· `tasks_test` · `session_test`（含 **`TestBuildContextTokensReadsPrestored`**：P3 读通道真取预存值） · `sessionlock_test` · `prompt_test` · `plugins_test` · `jobdsl_test` · `domainmd_test` · `domainmcp_test`。
黑盒：`src/test/chonkpilot-llm/unittest/llm_test.go`（仅经公开主题驱动）。
