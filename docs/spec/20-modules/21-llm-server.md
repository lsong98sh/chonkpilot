# 21 · src/lib/llm（会话 / LLM 服务层）

> 状态：✅ 与代码一致
> 关联：[13-生命周期](../10-architecture/13-生命周期.md) · [01-端到端数据流](../00-overview/01-端到端数据流.md) · [12-数据层](../10-architecture/12-数据层.md) · [26-mcp-gateway](26-mcp-gateway.md)
> 代码目录：`src/lib/llm/`（`server/` 包 + `httpapi/`）；**服务端 exe 外壳 = `src/server/`**（`chonkpilot-server.exe`，gui 与 browser 共用）

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

### 2.1 用户维护 MCP 的启动注册

> **目标**：用户在「MCP 配置页」维护的 server 定义（**四级文件化** `<级别>/capability/mcps/<名>.json`）在 **LLM 启动时**注入内嵌 gateway。
>
> **单体（src/lib/gui / src/desktop/cli 内嵌本 llm server）的 MCP 配置一律取自四级文件化视图，不读 exe 同目录 `config.json`**；exe 同目录 `config.json` 的 `mcpServers` 段属 mcp-server / mcp-gateway **独立 exe** 自身行为（见 [25-mcp-server](25-mcp-server.md) / [26-mcp-gateway](26-mcp-gateway.md)）。
>
> **配置来源 = 四级文件化视图（唯一来源）**：MCP server 定义**载体** = **`<级别>/capability/mcps/<名>.json`**（四级 app/user/project/prjusr）。装配读点 `gateway_servers.go`：
> ① `loadMcpFileEntries` 经 data 门面 **`McpAPI.McpList`**（`s.cfg`）读**四级合并生效视图**（同名**最具体级优先、整条覆盖** `prjusr > project > user > app`）；
> ② 合并后交 `mergeGatewayServers(nil, …)` → `Params.Servers`（system 基底生产恒传 nil）。**同名跨级只生效一份 → 只 spawn 一份**（以生效定义为准）。
> **旧 usr KV `mcpServers`（专用表 `mcps`）已彻底废弃**：**代码零兼容、不再回落**，历史数据不迁移。
> **保存即生效** = 订阅 **`data-mcp-refresh`**（四级文件配置 save/delete）→ `reconcileUserMCPs` 增量对账（`servers/register|unregister`）。

**读点与装配**（实现文件 `src/lib/llm/server/gateway_servers.go`，接线 `server.go` 的 `New`（gateway 构造前））：

| 步 | 动作 |
|:--:|------|
| 1 | **经 data 门面** `McpAPI.McpList`（`s.cfg`，inline 绑定 = 同进程直调；**不持库句柄**）读**四级文件化合并生效视图** → 定义数组（`loadMcpFileEntries`） |
| 2 | 过滤与分流：`enabled=false` 跳过（记日志）；**有 `runtime` → spawned（网关拉起，`ServerEntry.Runtime` + `Args`）；有 `url` → proxied（连接已运行 server）；两者皆空 → 跳过**（记日志）。其余规范字段（`transport`/`description`/`category`/`namespace`/`env`/`headers`/`cwd`/`hot_tools`/`timeout`）**原样透传**，不做子集裁剪（字段与校验以 gateway `servers.list`/`servers.register` 为唯一来源） |
| 3 | 映射 `[]mcpgateway.ServerEntry`：`ID←name`、`Name←name`、`URL←url`、`Description←description`、`Transport←transport`（`"direct"` 归一为空 → gateway 按 URL 推断 http/proxied）、`Enabled=true`、`Scope=""`（global）、**`Origin`**（`user`，见下） |
| 4 | 作为 `Params.Servers`（`= opts.GatewayServers ++ 本处装配`）传入内嵌 gateway，**优先于** `ServersFile` |
| 5 | gateway `Start` 逐条 `connectServer`：**单条失败记日志继续**（与 `servers.list` 同语义），不影响引擎启动 |

**时序方案（选型）**：在 `server.New` 构造 gateway 前**读**（非经总线 `data-user-config-load`）——persist 服务订阅在 `Start` 后才就绪，经总线读须把 gateway 构造整体推迟到 `Start`（牵动 `s.gw`/`s.mcpServer`/`s.mcpCfg` 生命周期）。读法 = **data 门面 `McpAPI.McpList`**（`s.cfg` = inline 绑定，同进程直调，构造序在 gateway 之前）——读点无时序问题且**库句柄不出 data 组件**（21 §1）。

**结论**：

- ① 注册 **`scope` = `global`**（`ServerEntry.Scope` 留空）。
- ② 启用标识 = 四级文件化 MCP 条目的 `enabled` 字段（数据层已按名整条合并 → 同名只生效最具体级一份）。
- ③ **保存即生效**：订阅 **`data-mcp-refresh`** → `reconcileUserMCPs` 做**增量对账**（变更/停用/删除 → `servers/unregister`；新增/变更 → `servers/register`；未变更条目不动），**零新增主题**（`gateway_servers.go`）。
- ④ **来源（Origin）**：四级文件化 MCP 定义 → `Origin=user`（**外部/用户定义**：不注入 `_meta`/`CHONKPILOT_*`）。builtin 来源由 gateway self / dir 节点承载。见 [26-mcp-gateway](26-mcp-gateway.md) 来源标注段。

**边界（设计约束）**：

- **不触消息面**：注册走进程内 `Params.Servers`，**不经 MQ**——[61-消息一览](../60-reference/61-消息一览.md) **不新增消息**；gateway 的 `servers/register` 方法面继续保留给**运行时**动态接入（第三方 / 测试 / 按 instance）。
- `-servers-file`（`servers.list`）保留为**部署形态**的补充来源（与用户配置并存，`Servers` 优先）。
- 与 capability 的关系：`MCPServer`（self）= exe 同级 `capability` 扫描出的**内置**能力；用户维护 MCP 属**下游 server**，两者在 gateway 内聚合（`scope` 语义见 [26-mcp-gateway §5.1.1](26-mcp-gateway.md)）。
- exe 同目录 `config.json`：gui（`bridge/builtins.go`）仅读其 `mcpServers` 段作**只读内置项展示**（不含 `llms` 段）；llm（运行时装配）**不读取**（单体 MCP 配置 = 四级文件化 `<级别>/capability/mcps/`）。

---

## 3. 对外接口

### 3.1 订阅的方法面（`Start`）

| 相对主题 | handler | 说明 |
|---------|---------|------|
| `session-start`（llm-start） | `onLLMStart` | 受理轮次 |
| `session-send`（llm-send） | `onLLMSend` | 驱动：text-user / tool-result / text-notify |
| `inbox-add`（客户端 topic `llm-inbox-add`） | `onInboxAdd` | **INBOX 统一发送入口**：按当前相位分类 `insert`（工具等待期立即插入）/ `next_turn`，落 prjusr `inbox_items` |
| `inbox-list`（`llm-inbox-list`） | `onInboxList` | 读回 pending 项（队列指示器 / 重启拉回） |
| `inbox-discard`（`llm-inbox-discard`） | `onInboxDiscard` | 撤回 / 删除 inbox 项（置 `discarded`，不入 messages） |
| `inbox-promote`（`llm-inbox-promote`） | `onInboxPromote` | `next_turn → insert`「立刻插入」；无运行轮时 = 重启后「继续」消费 |
| `session-cancel`（客户端 topic `llm-cancel`） | `onLLMCancel` | 取消 |
| `session-ask-reply`（客户端主题 `ask-user-reply`） | `onAskUserReply` | 提问应答 |
| `task-stop` | `onTaskStop` | 手动结束任务（级联） |
| `tool-retry` | `onToolRetry` | 重试最后一条 interrupted 工具 |
| `prompt-optimise` | `onPromptOptimise` | 提示词优化（流式） |
| `domain-tool-call` | `onDomainToolCall` | 域工具远程回调 |
| `llm-simple` | `onLLMSimple` | 单次无上下文 LLM（插件/摘要用） |
| `llm.test-connection` | `onLLMTestConnection` | 设置 → LLM「测试连接」的**只读探活**方法面（**点分相对主题**，不经 `frontMethodSubjects` 单字白名单 —— 走桥 / 服务端上行入口的「点分直通总线」分支）。实现 `server/llm_testconn.go`：**复用主路径同一条出网链路**（= router `Call`：同一 Authorization / 协议分派 / 超时 / 错误分类）发一次最小请求（单条 `hi` + `max_tokens=1`、不下发工具），**首个非错误事件即判连通**并立即 cancel（`latency_ms` = 首包延迟），超时 = 15s 常量 `llmTestTimeout`。**只读、不落库**：用一次性 provider（唯一名 `__probe__*` 登记 → 探测后**注销**，**不改 usr `llms`、不影响运行中 provider**），**不写会话/轮次/消息/历史表**。载荷 `{instance_id, baseUrl, model, apiKey?, protocol?}`（`baseUrl`/`model` 必填，缺 → `error.kind=invalid`）→ 结果信封 `{ok, latency_ms, model_echo?, error{kind, message}}`（`kind` ∈ `network\|timeout\|rate_limit\|server\|auth\|protocol\|invalid`；handler 恒 `return nil`，结果走 `v.Result`）。**安全（硬要求）**：应答与日志一律经 `maskLLMSecrets` 脱敏（先整串替换本次 `apiKey`、再按通用密钥形态兜底）→ 上游回显的 Key 不外泄；payload/返回明细见 [61 §1.1](../60-reference/61-消息一览.md) |
| `config.suggest` | `onConfigSuggest` | **配置建议**方法面（AICFG-1，2026-10-16；**点分相对主题**，同走「点分直通总线」分支）。实现 `server/configsuggest.go`：受理由配置页（LLM / MCP）/ 工具原语编辑页发起的「AI 智能填充 / AI 生成」—— 用户自然语言 → LLM 产出**结构化字段建议** → 只回填表单**待保存态**（不落库，落库仍由既有【保存】触发）。载荷 `{instance_id?, kind, text, current?}`（`kind ∈ llm\|mcp\|tool` 决定白名单与指令；`kind` 未知 / `text` 空 → `error.kind=invalid`）。**provider 口径统一**：与 `prompt-optimise` 一致**现读 `llm.promptOptimise`**（缺失/空串由数据层回落 `defaultLLM`；**热生效**，不缓存到进程级）→ **一次性、非流式**（`llmOnceSpec`，自带 60s 超时）→ **严格 `json.Unmarshal`** + **服务端硬过滤白名单** `configSuggestWhitelist`（三套：llm/mcp/tool）→ 结果信封 `{ok, fields, notes?, error?{kind, message}}`（handler 恒 `return nil`，结果走 `v.Result`）。**白名单（硬要求）**：`fields` **只含该 kind 白名单字段**——**`apiKey` / `level` / `enabled` / `isolate` / `sandbox` / `hot_tools` 等禁填字段即便模型输出也丢弃**（`current` 上送时同样剔除，不外送禁填项）。**错误码**：`invalid`（入参不合法）· `parse_failed`（非 JSON / 无可填字段）· 及复用 LLM 出网分类 `timeout\|network\|rate_limit\|server\|auth\|protocol`（**不新增错误码体系**）。**安全（硬要求）**：应答与日志经 `maskLLMSecrets` 脱敏（无 key）。browser 形态上行白名单 `allowedDottedPrefixes` 已放行；payload/返回明细见 [61 §1.1](../60-reference/61-消息一览.md)，FP 见 [36 `CFG-018`](../30-function-points/36-配置.md) |

### 3.2 订阅的事件面（`Subscribe`）

`instance-register` → `onInstanceRegister` · `instance-exit` → `onExit` · `mcp-tasks-report` → `onGatewayTaskDone` · `mcp-gateway-changed` → `onMCPChanged` · `task-deleted` → `onTaskTreeDeleted`。

### 3.3 广播（`domainTopics` 映射）

`session-receive`（reason/text/tool-call）· `session-complete`（唯一终态）· `session-compress` · `session-ask` · `session-turn-start` · `task-started`/`task-updated`/`task-done` · `server-starting` · `prompt-optimised`。

### 3.4 域工具与 agent 契约（`llm/contracts/`）

- **tools（6）**：`ask_user`（hot, async=never）· `subchat_open`（hot, async=never）· `subchat_finish`（hot, async=never）· `tool_stop`（hot, async=never）· `tool_result`（async=never）· `next_run`（hot, async=never；**NEXTRUN-1**，见 §4.10）。
  - **`next_run`（NEXTRUN-1）**：模型主动登记**下一轮提示词**（inbox `next_turn`、`source=llm`），本轮正常结束后由 `finish → consumeNextTurn` 自动起新 turn；**仅主会话**（子聊天 / 群聊 / 子会话 / DSL 子轮次经 `denyTools` 不下发且调用处拒绝）；会话级计数 `sessions.next_run_count` + usr `nextRunMax`（默认 30，`0` = 关闭）——用户发送（`llm-start` / `llm-send{type:text-user}` / `llm-inbox-add` / `llm-inbox-promote`）重置 0，每次成功受理 +1，达上限返回固定文案且不登记；同轮多次调用 last-wins，本轮 `error`/`interrupted`/`cancelled` 终态丢弃本 turn 的 LLM 排队项；**零新增 MQ 主题**。
  - **DSL 编排入口已统一为 `dsl_run`**：2026-10-19 起由 **core-mcp-server 的独立 executor**承接，**执行体即 `chonkpilot-dsl-executor.exe`**（源 `src/lib/mcp-tools/dslexec`、`async=always`；2026-10-20 合并，作业与 DSL 引擎同进程）；LLM 步骤与展示上报经插件桥调用本模块原子能力 `plugin/v1/dsl.llm`（子轮次）/ `plugin/v1/dsl.report`（→ `task-*`）。**不存在 `llm_run` 域工具**（契约/注册/dispatch 均已删除）；gateway 不再自持 DSL 节点。
  - **工具异步档位**：域工具按执行线拆分并冻结模式 —— `tool_stop`/`tool_result`（仅同步）· `ask_user`/`subchat_open`/`subchat_finish`（仅同步 + 无超时裁决点，不纳入「⑤ 超时自动取消」）。契约「基本信息」yaml 键：`category=server`/`async=never`/**`timeout=0`**/`hot=true`。**域工具执行硬上限统一 `timeout=0`**（系统工具 `category=server` 不单独设执行硬上限 → **`timeout=0` = 无上限**：`_meta.timeout=0` **显式透出**，gateway `limit<=0` **不设裁决点**、executor `noLimit` **不设执行硬上限**，永远等待、用户可取消；`tool_result` 的 `timeout` **入参**是轮询等待秒数、非执行硬上限）。「基本信息」已解析并透出：`loadDomainTools` 解析 `category`/`async`/`async-threshold`/`timeout` → `tools/register` 载荷携带 → gateway `registerTool` 写入工具 `_meta`（`async` 为 `auto`/空不显式透出；**`async-threshold` 仅 `>0` 透出**；**`timeout` 键显式声明即透出，含 `0`/`-1` = 无上限**），故这些工具的 `_meta.async` 可在 `tools/list` 见到。上述工具的**提供方归属不变**（均由本模块经 `tools/register` 注册 = `registeredProvider`），仅**处理档位**按执行线归并；`ask_user` 的「不纳入 ⑤」为**我方建议，待用户确认**。详见 [18 §3.1/§4](../10-architecture/18-工具异步超时与取消.md) · [18 §3.8](../10-architecture/18-工具异步超时与取消.md)。
- `ask_user` **实现现状 + 目标态**：**入参 = 扁平单问**（`question`/`options`/`multi`/`custom`/`recommended`；一次工具调用 = 一问，N 问由前端 300ms 合并窗口聚合成「第几个/共几个」批次展示），答复经 `ask-user-reply`（`{ask_id, answer, custom?}`）回填该 turn 的 tool_result。**目标态（未落地）**：`questions[]` 一表 N 问 / 答复增量写穿（ask 节点 `content`，落点方案 A）/ 全部题答齐才回一个 tool_result / turn 等待态 `awaiting` 支持重启续答（详见 [34 §7](../30-function-points/34-任务.md)）。**2026-10-11** 由 [61 §4.3](../60-reference/61-消息一览.md) 口径收敛（扁平单问与实现一致）。
- **agents（7）**：`coder` / `executor` / `planner` / `reader` / `reviewer` / `worker` / `writer` —— **agent 退出资产面**（不注册进 gateway 资产目录、不落 `syncScenarioAgents`）：`mcp_find` 的 `type` 收敛为 **`tool|skill|resource|all`**（**去 `prompt`/`agent`**）、`mcp_load` 的 `kind` 为 **`tool|skill|resource`**、**`prompts/list` 不得再列 agent**；LLM 改由**注入的团队列表**（场景层）知道可委派谁。见 [25 §5](../10-architecture/25-MCP与场景分层模型.md)。🔵 待实施。
- 加载：`//go:embed tools/*.tool.md`（`src/lib/llm/contracts/embed.go` 的 `contracts.FS`；**agent 不再 embed**）→ `domainmd.go` 分段解析（v2 H1 分区：`# 摘要`/`# 参数`/**`# 基本信息`** —— 基本信息 yaml 现**解析并携带** `category`/`async`/`async-threshold`/`timeout`）→ `domainmcp.go` 经 `tools/register` 注入 gateway：**载荷现携 `category`/`async`/`async_threshold`/`timeout`**（`hot` 保留），gateway `registerTool` 据此写入工具 `_meta`（`async` 为 `auto`/空不显式透出；**`async-threshold` 仅 `>0` 透出**；**`timeout` 键显式声明即透出，含 `0`/`-1` = 无上限**，见 [18 §3.8](../10-architecture/18-工具异步超时与取消.md)）。该 embed 为**唯一副本**（`build-mcp-server.ps1` 另覆盖式同步到 `capability/tools/server/`，供 core 按 `core-only` 契约注册与 executor 执行；不再各存手工副本）。
- **BRIDGE-2 T7（2026-10-18）—— 域工具进程外实现**：桌面单体 / server 形态下，`ask_user` / `tool_stop` / `tool_result` / `subchat_open` / `subchat_finish` 由 **`chonkpilot-server-tools.exe`** 经 `plugin/v1/tools.register`（`handler_subject=domain-tool-call-ext`）注册——**server 零域工具**（进程内 `registerDomainTools` 在 `CHONKPILOT_EXTERNAL_TOOLS=1` 时跳过）。server 只保留**原子能力**（`plugin/v1/turn.context` · `task.node.start|done` · `task.result` · `tool.stop` · `ask.user` · `subchat.open|finish`，实现见 `plugin_caps.go`），任务树唯一写者仍是 server。单测 / CLI 不设开关 → 保持原进程内全量注册。两条回调主题分离（进程内 `domain-tool-call` 与外部 `domain-tool-call-ext`），避免同主题双订阅导致重复执行 / 结果双写。外移进程由宿主持有生命周期（`src/lib/llm/servertools`，spawn / Kill），失败不阻断核心服务（工具面降级）。
- **BRIDGE-2 T7 增补（2026-10-19）—— 元工具进程外实现**：`mcp_find` / `mcp_load` / `mcp_invoke` 同样外移：契约 = `src/lib/llm/cmd/server-tools/contracts/meta/*.tool.md`，实现 = `cmd/server-tools/meta.go`，由 server-tools 以 `handler_subject=meta-tool-call-ext` 注册；取数走插件桥能力面（`plugin/v1/tools.list|call`、`prompts.list|get`、`resources.list|read`、`llm.step`）。`CHONKPILOT_EXTERNAL_TOOLS=1` 时 gateway `registerMetaTools` 直接跳过（进程内模式仍用于单测 / CLI）。**契约「基本信息」**：`category=meta` / `async=never` / `hot=true` / `timeout=0`（无执行硬上限）。跨进程取消经 `plugin.tool.cancel{call_id}` 传播；`timeout=0/-1` → 调用载荷 `no_timeout:true`（pluginbridge 不套 11 分钟兜底）。验收 L4 `run_meta_tools.py` 4/4。
- **一工具一 exe（2026-10-19，G-68 第二步，取代上两条的 server-tools）**：`chonkpilot-server-tools.exe` 已删除 —— domain 6（`ask_user`/`tool_result`/`tool_stop`/`subchat_open`/`subchat_finish`/`next_run`）/ meta 3 各为独立 executor（`src/lib/llm/cmd/*-exec`，公共外壳 `execmain`），契约标 `core-only: true` + `runtime`（domain 契约源仍为本模块 `contracts/tools/`，构建同步到 `capability/tools/server/`；meta 契约源 `src/initdata/capability/tools/meta/`），由 core-mcp-server 注册并经 `ExecTool.Call` 执行。`CHONKPILOT_EXTERNAL_TOOLS=1` 的语义 = 由 core 接管（server 零域工具）；原子能力面（`plugin_caps.go`）不变。`next_run` 的跨进程路径 = 该 executor → `plugin/v1/next.run`。验收 L4 `run_meta_tools.py` 4/4 · `run_sem_tool_result.py` 3/3 · `run_tool_async.py` 11/11。

---

## 4. 内部控制流

### 4.1 轮次状态机（`turn.go`）

```text
llm-start → newTurnCtx：BuildContextTokens(include_snapshot) → 系统提示词分层前插（全局层 + 目录层 + 场景层 + agent 层，25 §3）→ 子轮 persona → 载 LLM 参数 → go loop()
loop：select { done | asyncDone → onAsyncDone | in → chatOnce }
chatOnce：
  迭代上限 maxToolIterations=20
  → LLM 流式（reason/text/tool-call 广播）→ 空回复 EMPTY_REPLY / 断链有内容内部"继续"（≤maxAutoContinue=3）
  → finish_reason=length 且未超限 → 续写；否则 complete
  → 工具循环：逐 tool-call → 剥 self_ 前缀 → ask/llm/task/普通工具分派
       普通工具 gc.Call（异步 → pending + setGwTask；同步 → 喂回）
  → 有 pending 则 stash；否则递归续轮
```

常量：`maxToolIterations=20` · `llmRetryCount=5` · `llmRetryBudget=60s` · `maxAutoContinue=3`。其中 **`llmRetryCount` / `llmRetryBudget`（及超时 `ResponseTimeout`/`StreamTimeout`）为「回落默认」**——经 `loadLLMRuntimeConfig`（`data-user-config-load` 读 usr 标量）加载，在 `newTurnCtx`/`recoverTurnCtx` 各一次，**下个新 turn 生效**；读取失败/键缺失/非正值时用上述常量。**RETRY-2（2026-10-20）：报文级重试整体下沉 router** —— turn 把 `MaxRetries=&retryCount` / `RetryBudget` / `RetryObserver(tc)` 传给 `ChatOptions`→`router.CallOptions`；`retryCount` 不含首次请求，显式 0 = 不重试；退避由 router 按预算归一化，llm 侧**不再自持重试循环 / `probeBeforeRetry` / `retryDelay`**。

- **pending / inflight 回执兜底（watchdog，`turn.go`）**：异步任务（`task_id`）回执丢失 / 未登记时 `pending` 永不归零 → 整轮永久挂起（无超时、无 reaper）；`ask_user` 的在飞登记（`inflight`）同理。`loop` 每 **`pendingSweepInterval`（30s）** 扫描一次，对超过 **`pendingTaskTimeout`** 仍未回执的 pending 与 inflight **按 `failed` 占位回填并续轮**（复用 `onAsyncDone` 同一路径）。`pendingTaskTimeout` **由项目级键 `pending_task_timeout_sec` 配置**（缺省 `defaultPendingTaskTimeout = 2h`；键缺失 / 非正整数 → 回落缺省，见 [64 §3](../60-reference/64-配置项一览.md)）。取值保守：长时异步工具仍可正常完成，仅在**回执确实丢失**时兜底。
- **恢复轮去重（`recoverTurnCtx`）**：同 `{instance, turn}` 已有**存活轮次** → **直接复用该 tc**（不另起 `loop`）；此前去重命中仍返回**未登记**的新 tc（其 loop 亦在跑）→ 同 `{session, turn}` 双执行，已修。
- **工具强制异步（INBOX，2026-10-17）**：`turnCtx.gatewayArgs` 对**普通工具一律附调用级 `async=always`**（含契约显式 `never`；gateway `doCall` 读取后剥离 → 不改 `mcp-tools-call` 载荷与工具入参）——派发即返回 `task_id`、主循环让出到 `select`，插话才可能被消费。**例外 = 本进程域工具**（`ask_user` / `subchat_open` / `subchat_finish` / `tool_stop` / `tool_result`）：由 llm-server 同步编排（毫秒级），异步化会把返回体改成 pending、改变其契约语义且对插话无收益。**第二类例外 = 契约显式声明超时裁决点（`_meta.timeout > 0`）的工具**：保留原 `never`/`manual` 档位，使既有 `mcp-tools-timeout` 裁决 UI 仍可达。**第三类例外 = 调用级已显式给出 `async` / `_async`**（LLM 或夹具明确要求 `never`/`manual`）：尊重原值不覆盖 —— `mcp-tools-timeout` 裁决与 `tool_async.<tool>.cancel_on_timeout` 都依赖同步等待语义，覆盖会让既有能力不可达（2026-10-18 实跑 `run_tool_async.py` D/I 验证）。原 A5「非主会话强制同步（`async=never`）」**已废止**（否则子会话 / 群聊插话不成立）；「收口点亮 / 本轮屏障」改判 `pending` 清空 + turn 终态。详见 §4.10。

**上下文拼接（`assemble.go`）**：全量历史按 turn（边界 = `isTurnBoundary`）拼接 —— **三段结构（口径 X）**：**完整区**整轮**原文**（范围受 `keep_full_max_turns`(N) + `keep_full_max_tokens`(M) 双条件约束，均项目级配置，默认 10 / 24000；`loadKeepFullTurns`/`loadKeepFullTokens`）；**简化区**（再往前，**brief token 累计 ≤ `compress_token_threshold`(T)**）→ **【简化态原文】仅 text**（`data.BriefMessages`：去 reasoning / tool_call / tool_result；T 由 `loadBriefBudget` 读，默认 20000）；**摘要区**（更早）→ 已由压缩插件落为前导 system 摘要（原样透传），若残留同按简化态投影；重试/恢复轮强制全量（`forceFull`）。**边界算法唯一来源 = `data.LocateZones`**（**与压缩侧同一函数**：完整区 = `LocateFullTurnCount`（N/M 取先到，**本轮恒保留**，口径 V，下限 1）；简化区 = 自完整区向前逐轮累加 brief token ≤ T）。**预存 token 优先**：各轮 `full_tokens`/`brief_tokens` 随 `data-session-context` 的**只增**伴随数组 `turn_tokens` 带回（`BuildContextTokens` → `tc.turnTokens` → `splitTurnTokens`），**尾部对齐 + 缺值回退实时估算**（`data.ResolveStoredTokens`）→ 生产判定真正读取预存值。**取值语义（口径 W）**：`N==0`/`M==0` = 该条件不启用；**两者均 0 → 不压缩**（保留全量）。摘要压缩仍由压缩插件落快照（本处只做粒度收敛与简化态投影，不做摘要、不写快照）。

**兜底归并（口径 Y）**：属压缩插件职责（本模块只经 `session-compress` 载荷透传真窗口 `max_context_token` + 输出预留 `max_output_token`，见 [28 §3.1](28-plugins.md)）——三段 + 输出预留超窗口 → 合并【摘要 + 简化区原内容】→ 重新摘要（目标 = 窗口 1/2）→ 简化区清空。

### 4.2 终态（`finish`）

`CompleteTurnTokens(status, finish_reason, full_tokens, brief_tokens)` → `SetSnapshot(session, hist+本轮消息, turn)` → 广播 `llm-complete` → 广播 `llm-compress`。`status ∈ complete | incomplete | interrupted | error`。

- **预存 token**：终态写轮次时一并预存该轮 `full_tokens`（**完整态** = 该轮全部消息，`data.EstimateTokensOfMessages`）/`brief_tokens`（**简化态** = **仅 text**，`data.EstimateTokensOfMessages(data.BriefMessages(turn))` —— 去 reasoning / tool_call / tool_result），经 `data-session-complete-turn` 的**只增可选字段**落 turns 行（详见 [61 §3.2a](../60-reference/61-消息一览.md)）。**口径与组装侧简化区投影 / 压缩侧简化区预算同一构造**（`turnTokenCounts` ↔ `assembleTurns` ↔ `data.BriefMessages`，同源一致性）。读取侧：`data-session-context` 结果**只增**伴随数组 `turn_tokens`（见 [61 §3.2a](../60-reference/61-消息一览.md)）。
- **`llm-compress` 载荷（只增）**：唯一终态落库后随事件携带**可选** `max_context_token` / `max_output_token`（各自 = provider 对应字段 `maxContextToken` / `maxOutputToken`：**上下文窗口**（兜底归并窗口来源）/ **最大输出 token**（输出预留 + 摘要目标基数），nil/未配置则不带；见 [28 §3.1](28-plugins.md)）。**载荷字段命名统一 snake_case**（口径 Z3/Z4）。
- **压缩处置非阻塞（OP-03）**：`llm-compress` 广播后，compress 插件**只入队即返回**（每会话串行 worker 异步压缩）——`finish` 不再等待压缩完成，发终态的 turn 收尾 goroutine 及时 `Close()`、会话锁及时释放。**载荷与主题零变更**；详见 [28 §3.1](28-plugins.md)。

- **终态载荷** `{status, finish_reason, code?, message?, text?, **retryable?**, **retry_attempt?**, **retry_count?**, **retry_wait_ms?**}`；`finish(tc, status, finishReason, code, message, text, retryable *bool)` 为**单点终态**。
- **`retryable` 为可选字段**：非 nil 才随 `llm-complete` 携带。**仅错误终态携带**——`complete()`（正常/取消）传 nil **不带**；`llmError()`（`EMPTY_REPLY`/`TOOL_LOOP_LIMIT`/`DB_ERROR`，无 `*LLMError` 分类来源）**固定 `false`**；`llmErrorRetryable()`（`LLM_REQUEST_FAILED`/`LLM_STREAM_ERROR`）取 `*LLMError.Retryable`（`turn.go` `retryableErr`；超时/网络/429/5xx → `true`，鉴权/协议 → `false`）。契约详见 [61 §4.3](../60-reference/61-消息一览.md)。
- **可见重试（RETRY-1 / RETRY-2）**：重试由 **router** 执行，turn 实现 `router.RetryObserver.OnRetry` → 每次重发前发布**中间态** `session-retry{instance_id, session, turn, retryable:true, retry_attempt, retry_count, retry_wait_ms}`（`turn.go#OnRetry`，`retry_wait_ms` 由 router 传入）；错误终态携带**真实** `retry_attempt`（已执行重试次数）/`retry_count`（usr `retryCount`，0 = 不重试）/`retry_wait_ms=0`。主轮次与子轮次/群聊 `work_session` 同走 `chatOnce`，行为一致。
- 消费方 = 前端：重试中间态 → 下方提示条「正在重试 n/N（等待 s 秒）」；**终态错误 → 唯一错误面 = 下方提示条**（摘要 + 「已重试 n/N 次」+「查看错误信息」），聊天流内不再渲染错误气泡（ERRUX-1）。前端**不再**静默自动续写（自动续写由后端按宽限期承担，见下条）。
- **`next_run` 终态收敛（NEXTRUN-1）**：`finish` 落定后若本轮为 `error` / `interrupted` / `cancelled`（非 `complete`/`incomplete`），按 `turn_id` 丢弃本 turn 的 LLM 排队项（`source=llm` 的 `pending` → `discarded`），避免"僵尸提示词"在用户结束本轮后又自动起轮；计数 `next_run_count` 不回滚（上限仍按用户视角累计，用户发送即重置）。
- **语义级中断 + 自动续写（RETRY-2，2026-10-20）**：`Emitted > 0` 中断 / `finish_reason=length` → 发 `session-interrupted`（前端 `llm-interrupted`）并**不结束 turn**；前端显示「服务器错误，是否继续。」+「继续」链接（点击 = 发送 `continueText` 用户消息）。usr `autoContinue=true` 时排程宽限期 `autoContinueDelaySec`（默认 600s），到期且**仍停在原会话、无任何用户操作** → 同 turn 内追加一条 `continueText`（`Kind=continue`）；用户在该 session 发任意消息 / 插话 / 切会话 / 关窗 → 取消（`continueSeq` 代次 + `data-session-active-get` 校验）；每次中断最多 1 次，`maxAutoContinue=3` 为同 turn 硬上限；再失败即终态（提示条保留、手动可继续）。`autoContinue=false` → 中断即终态（不发宽限期），提示条照旧。

### 4.3 任务树（`tasks.go` / `tool_task.go`）

- `taskManager`：`TaskNode` 六态 `pending/running/done/error/cancelled/interrupted`；上限 `maxTaskNodes=200`、`maxActiveTasks=50`；终态 5 分钟淘汰。**任务状态唯一权威 = Task 层（`src/lib/task`，与 LLM/Data/Filesys 平级）**；本模块 `taskManager` **只保留运行态视图（内存 `nodes`）+ 事件发布**（`tasks.started`/`tasks.updated`/`tasks.done`，载荷增补 `workdir`），**不自写库**（全仓唯一落库发送点 = 层 `src/lib/task/store.go:103`）。**启动遗留清理** 并入层 `Recover`（`src/lib/llm/server/tasks.go` `recoverStaleTasktree` 只做转发：`tm.srv.taskLayer.Recover(instanceID)`；层把库中非终态 `pending/running/awaiting/detached` 一律标 `interrupted`、**不自动重跑**）；**控制面状态判定以层为权威**（`cancelSubtree` 经 `taskLayer.CancelSubtree` 判定可取消集合 + 层 `OnCancelExec` 回调执行侧 → 既有 `onTaskCancel`→`provider.Invalidate`）。内存 `nodes` 为**运行态视图**（DB 仍为权威）。见 [21-任务层设计方案](../10-architecture/21-任务层设计方案.md) §9.2。
- 任务型工具：`tool_stop`（级联 `cancelSubtree`）/ `tool_result`（取转后台结果）；两者经 gateway 唯一入口（域工具回调）同步执行。
- **任务编排分层**：**任务编排归本模块** —— 任务树/级联/批量/任务事件（`tasks.started`/`tasks.updated` 节流 ≤250ms/`tasks.done`）由 `server/tasks.go` 编排（节点 `kind` = `tool`/`llm`/`ask_user`，`parent_id` 挂父、`top_session` 归属；`tasks.go:1-6` 注释原文「**gateway 是纯执行层，不对前端广播任务事件**」）；**gateway 只做单次 `tools/call` 的执行层异步/裁决/取消**（`src/lib/gateway/gateway/tasks.go:1-4` · `mcpgateway.go:640-651`），终态经 `mcp-tasks-report` 回报本模块。**取消链** = `cancelSubtree`（`server.go:1071-1085` `onTaskStop` / `tool_task.go:39-67` `runToolStop`）→ 子树登记的 gwID 逐个经**进程内 sink** `s.execSink.CancelExec` 交 gateway（`gwclient.TaskCancel` 已移除）；`process_id` 分支直接 OS kill（`tool_task.go:48-57`）。**既有缺口（未修）**：`turn.go:609` 仅在「已转异步」分支登记 `gwTaskID` → 在飞的 **manual / never** 任务打不到级联取消。详见 [18 §3.6](../10-architecture/18-工具异步超时与取消.md)。
- `ask_user` **实现现状 + 目标态**：**扁平单问**（一次工具调用 = 一问，`question`/`options`/`multi`/`custom`/`recommended`）；答复经 `ask-user-reply` 回填该 turn 的 tool_result。**目标态（未落地）**：`questions[]` 一表 N 问；节点 `content` 承载表单态（questions/answers/state）；**增量答复写穿** → **全部题答齐才聚合为一个 tool_result**；turn 停在 `awaiting`（免被启动清理误标 `interrupted`），重开经 `recoverTurnCtx` 重播表单。

### 4.4 子会话与 DSL（`dsl_caps.go` / `plugin_caps.go`）

- **DSL 编排不在本模块的工具面**：`llm_run` 已下线；`dsl_run` 已于 2026-10-19 外移为 core-mcp-server 的独立 executor（见 §3.4）。本模块保留其**原子能力**（`dslLLM` / `dslReport`）：`plugin/v1/dsl.llm` 执行 LLM 子轮次（`session-start`/`session-send`/`session-complete`），`plugin/v1/dsl.report` 把 tree/step/result 交给 `server/dsl_report.go` 的 `dslJobReporter` → `task-*`（**任务层唯一写者仍是 server**）。本模块不再有 `isLLMTool` / `runSubJob` 分支，也不建 `dsl_job` 节点。`agent_list` 已下线（`registerDomainTools` 现仅注入 5 个域工具契约）；**子 agent 发现改由注入的团队列表（场景层）承担**（`mcp_find(type=agent)` 不再成立 —— `type` 收敛为 `tool|skill|resource|all`）。🔵 待实施，见 [25 §5](../10-architecture/25-MCP与场景分层模型.md)。
- **子轮次 agent 定义接线**：`agents` 由 `map[string]struct{}` 改存 **agent 定义**（`domainmcp.go`：`loadScenario`/`scenarioAgentOf`/`mainScenarioAgent`/`childAgentSystem`/`resolveAgentLLMName`/`parseToolWhitelist`）；**四项生效** —— `prompt`/`delegateCond` → 子轮 system（`childAgentSystem`）；`tools` → 工具下发面（`turnCtx.allowedTools` + `llmTools()` 过滤）；`llmRef` → provider（子轮 = 被委派 agent，顶层 = 主 agent）。**`llmRef` 逐级回落**：`agent.llmRef` →（空）→ `llm.<子系统>` →（空）→ 继承通道（顶层 `llm-start.llm` / 子轮父轮 provider）→（空）→ `defaultLLM` →（空）→ 系统默认（经 `data.LLMRefName` 折算 int 索引）；**热生效** = `newTurnCtx` **每轮解析一次**、无进程级缓存。**空值 = 不限制/不干预**（`tools` 空/`[]` = 不限制）。删除已无调用方的 `loadScenarioSystemPrompt`（主 agent = `mainScenarioAgent`）。内置 agent 集**不是代码内嵌契约**（不 `//go:embed contracts/agents/*.agent.md`）—— 统一为 **app 级场景**（`<exeDir>/scenarios/*/`；出厂内容由 **embed** 提供、app 初始化（首次 list）缺失即物化，**app 级可编辑**，25 §6），`registerDomainAgents` 经数据层门面 `facade.ScenarioAPI.ScenarioList` 读取（路径/三级根规则单源在数据层，本层不拼路径）；`registeredAgentDef` 回落来源 = 「app 级场景内的同名 agent」（全量链路见 §4.4 上方注）。`builtin-agents/` 已删除 —— 发布 `scenarios/` 仅 `default/`（「开发场景」）；「通用」= 无场景的通用模式（无目录）；`registerDomainAgents` 扫描全部 app 级场景（当前仅 1 个）。
- **场景子 agent 白名单硬拒**：`turn.go` 在工具调用**分发前**按 `turnCtx.toolAllowed(name)` **硬拒**白名单外工具（**不落 gateway**、不建任务节点，以失败文本落 role=tool 回喂；名字口径 = LLM 工具面**暴露名**，与 `llmTools()` 同一名字空间）。agent 只注入 context、不注册资产（`mcp_find(type=agent)` / `prompts/list` 不再列 agent）；`mcp_invoke` 旁路加固同保留。🔵 待实施。见 [25 §5](../10-architecture/25-MCP与场景分层模型.md)。
- **子作业/子轮次 panic 兜底**：入口 `recover` **不仅回收 goroutine** —— panic 路径亦**落任务失败终态（`TaskStateError`）并回填父轮**（`parent.FeedToolResultStatus(toolCallID, …, "failed")`），否则子作业节点悬挂、父轮永久等待该 tool_call（无终态、无回执）；用「已终态则跳过」守卫避免与正常收尾重复回填（`tasks.done` 幂等；父轮 `FeedToolResultStatus` 非幂等）。
- `runChildTurn`：子轮**复用同一 turnCtx 状态机**，`Parents=父链+父session`，业务同主轮。
- **DSL 语法与执行**：`dsl_run` 接受 `dsl.Parse` 脚本（`LLM "agent" "prompt" "目的" [=> 目标]` 三参必填：agent/提示词/目的 = 步骤展示名，软约束 10–20 字）；执行体 = `chonkpilot-dsl-executor.exe`（一工具一 exe，一作业一进程，引擎与编排同进程）；LLM 步骤经插件桥 `plugin/v1/dsl.llm` → 本模块起子轮次并等 `session-complete`。见 [3A §TOOL-009](../30-function-points/3A-工具与编排.md) · [63-DSL语法](../60-reference/63-DSL语法.md) · 契约 [dsl_run.tool.md](../../../src/initdata/capability/tools/core/dsl_run.tool.md)。
- **路径收紧 + 只读 env（R-11 二次升级）**：`dsl_run` 的 `file` 由 **executor（`dslexec.execTool`）** 严格 `paths.ResolvePathFor(instance, file, "")` 校验后在本进程读取；DSL 内 `#"path"` 句柄由引擎在运行时按同一严格口径解析（相对路径即该步失败）。解释器注入**只读** `env`（`dslexec.envVars`）：`CHONKPILOT_WORKDIR` / `DATADIR` / `TEMPDIR`（按 instance 分目录）/ `EXEDIR` / `PROJECT`；LLM 用 `{{env.CHONKPILOT_WORKDIR}}` 显式拼项目内绝对路径（不再依赖隐式 workDir 兜底）。见 [16-路径解析规范](../10-architecture/16-路径解析规范.md) §8。
- **工具执行全进程外（P6，2026-10-19）**：`CHONKPILOT_EXTERNAL_TOOLS=1`（桌面单体 / server 默认）时，系统级 app 根只注册 **prompts / skills / resources 资产**（`Config.SkipTools=true`），5 个可执行工具（`filesys_run` / `script_run` / `web_fetch` / `browser_run` / `desktop_run`）改由 `chonkpilot-self-tools.exe` 经插件桥 `plugin/v1/tools.register` 注册（`handler_subject=self-tool-call-ext`），gateway 注入 self 节点后仍以 `self_<name>` 暴露；执行 = 外部进程 spawn executor。app 根工具契约热变更由 self-tools 自身 fswatcher 重扫（unregister→register）。非外部模式（单测 / CLI）保持进程内 `RegisterContracts`。
- **实例上下文改由调用上下文（`_meta`）承载（R-11 二次升级修订）**：`loadExecConfig(instanceID)` 仅注入 usr/prj 执行配置（解释器/chrome/超时/并发/skip_dirs）；实例上下文**不再经 `mcpCfg.SetContext` 注入 tool arguments**，改由每次工具调用经 `mcpgateway.Context`（`mcp-tools-call` payload）→ gateway → mcp-server `params._meta` 透传。`llm-start` 的 `InstanceID` 为空 → 顶层错误（`accepted:false`，不进 turn）；`gwClient.Call` 的调用上下文 `InstanceID` 为空 → 直接返回错误（不下发）。`llm_start` 的 `work_dir`/`data_dir` 随 `instance-register` 绑定，`gwClient.call` 现随 payload 携带 `data_dir`。

### 4.5 LLM 出网（`src/lib/router` 统一入口）

> llm 侧**不直连各 provider** ——
> 「一次来回」（`step`）调用统一经 **`chonkpilot-router`**（`Server.chat` → `router.Call`，接线落 `server/routercall.go`）；术语见 [60-名词约定 §4](../60-reference/60-名词约定.md)（代码标识符 `router.Call` / `Call` 不改名）。
> 协议实现（openai chat / responses / anthropic / 内置兜底 echo）、SSE 解析、错误分类与连接池全部归 router；
> 本模块只保留**契约类型**（`ChatOptions` / `StreamEvent` / `ErrKind`）与**折算层**。

- **协议分派**：`router.Call` 按 provider `Spec.Protocol` 分派 —— `openai`（默认，`/chat/completions`）/ `responses`（`POST {base}/responses`）/ `anthropic`（`/v1/messages`；usr 配置暂不暴露）/ `echo`（内置兜底，不发 HTTP，见 §4.8）。
- **provider 配置**：命中 usr `llms`（或内置保留名 `echo`）时 `baseUrl`/`apiKey`/`model`/`temperature`/`maxOutputToken`/`thinking`/`reasoningEffort`/`protocol`（+ `maxContextToken`）**以配置为准**；`apiKey` 非空 → 请求头 `Authorization: Bearer`；`baseUrl`/`model` 空 → 回落 exe flags（`server/routercall.go specFor`）。**（口径 Z1）**：`maxOutputToken` = 请求体 `max_tokens`/`max_output_tokens`；旧键 `maxTokens` / `max_tokens` **读时兼容**（只读不写）。
- **provider 注册**：usr `llms` 全量 + exe flags 隐含默认（保留名 `__default__`）经 `reconcileLLMProviders`（`Reconcile` 增量对账）进 router；`data-user-config-refresh` 触发**动态增减**（零新增主题）。调用路径另做幂等 `Register`（配置刚改、refresh 未到亦可用）。
- 超时分级：`ResponseTimeout=120s`（首字节）· `StreamTimeout=60s`（chunk 间隔）**经 `router.CallOptions` 下发**（router 侧同名默认）。**为回落默认**——优先取 usr 配置 `responseTimeout`/`streamTimeout`（`loadLLMRuntimeConfig`，下个 turn 生效）。
- 错误七类（router 口径）：`network`/`timeout`/`rate_limit`/`server`（可重试）· `auth`/`protocol`/`invalid`（不可重试）；llm 侧折算为六类（`invalid` → `protocol`，见 `routercall.go routerKindToLLM`）→ `*LLMError`。
- 流解析（归 router）：SSE 行解析 `data:`/`[DONE]`、按 `index` 聚合 tool_call（`src/lib/router/internal/adaptor/toolcall.go`）、responses 语义化 SSE（无 `[DONE]`）、Anthropic `content_block_delta`。
- **终止与断链判定（归 router，三协议统一）**：载荷自身表达流结束（chat `[DONE]` / responses 终态事件 / Anthropic `message_stop`）→ **立即收尾**（不等上游关连接，见 `adaptor.ErrStreamDone`）；流在事件边界 EOF 结束却**未收到终态标记**（chat：`[DONE]` 与 `finish_reason` **都未见**；responses / Anthropic：未见终态事件）→ `network`（可重试）→ llm 侧走**断链续写**（[35 §S6/S8](../30-function-points/35-错误处理与恢复.md)）。
- **已知线格式差异**（事件语义不变，见 `routercall.go` 文件头）：请求恒带 `stream_options.include_usage`；输出上限字段按模型名择一（`o1/o3/o4/gpt-5*` → `max_completion_tokens`）；非 2xx 错误体信息串按 JSON 取值。


### 4.6 LLM 请求参数与 systemPrompt 注入

> 本节按现行代码口径整理（协议分派见 §4.5，responses 分支见下）。

**请求参数完整透传**（`server/routercall.go buildRouterRequest` 按 `ChatOptions` 折算 `router.Request`；`llm.go` 仅留契约类型）：

| 参数 | 来源 / 规则 |
|------|-----------|
| `model` | **provider 配置 `model`**（命中且非空）；**空 → 回落 exe flag `-llm-model`**（不再用 `name` 当 model；`llm-start.llm` 仅作 provider 选择键） |
| `messages` / `stream` / `tools` | 上下文与工具集 |
| `reasoning_effort` | **`Effort` 优先，否则 `Think`**（映射 high/medium/low）；**`think=off` 或 provider `thinking=false` → 不发送**（`resolveThinkEffort`，`turn.go:205-219`） |
| `temperature` / `max_tokens` / `top_p` | 均为指针，**非 nil 才写**（`frequency_penalty`/`presence_penalty` **已从 `ChatOptions` 删除**，无消费方；`top_p` 保留） |

**`responses` 协议分支（归 router：`src/lib/router/internal/adaptor/openai/responses*.go`，`protocol=responses`）**：`POST {base}/responses`，请求体字段 —— `instructions`（历史最前连续 system 消息）/ `input`（字符串或输入 item 列表：`message` / `function_call` / `function_call_output` / `reasoning`）/ `stream` / `temperature` / `max_output_tokens`（= `maxTokens`）/ `top_p` / `reasoning{effort}`（思考力度，替代 chat 的顶层 `reasoning_effort`）/ `tools`（**扁平形态** `{type:function,name,description,parameters}`，无 `function` 嵌套）/ `tool_choice`。响应为**语义化 SSE 事件**（每个事件带 `type` + 递增 `sequence_number`，**无 `data: [DONE]`**）：`response.output_text.delta` / `response.reasoning_text.delta` / `response.output_item.added|done` / `response.function_call_arguments.delta|done` / `response.completed|incomplete|failed`；工具调用按 `item_id` 组装（`call_id`→`ToolCall.ID`），工具结果以 `function_call_output` 回灌；`incomplete`（`max_output_tokens`）→ 本仓 `finish_reason=length`（触发自动续写）；`failed` → `Err`（错误分类）。**白盒**：`src/lib/router/internal/adaptor/openai/responses_test.go`。

**参数来源与加载时机**：`server.go loadLLMProvider(instanceID, selectedModel)` → `userLLMProvider`（命中 usr `llms`）→ 未命中查**内置兜底保留名**（`builtinFallbackProvider`，仅 `echo`）→ 仍不命中回落 exe flags；读取 `protocol`/`baseUrl`/`apiKey`/`model`/`temperature`/`maxTokens`/`thinking`/`reasoningEffort`/`maxToolIterations`，在 `turn.go newTurnCtx` 时**一次加载**，子轮次复用主轮次 provider 与参数。`server.go loadLLMRuntimeConfig(instanceID)` 经 `data-user-config-load` 读 usr 标量 `responseTimeout`/`streamTimeout`/`retryCount`（`newTurnCtx`/`recoverTurnCtx` 各加载一次，**下个新 turn 生效**；`retryCount` 显式 0 = 不重试，两项超时非正值回落常量）。**退避间隔不自持**：经 `router.RetryWait`（`Retry-After` 优先 + 指数退避），`retryDelay` 配置项已移除。

**systemPrompt 注入链**：**主 agent** 场景 systemPrompt 由 `domainmcp.go mainScenarioAgent` 解析；**子轮 system** 由 `childAgentSystem`（含被委派 agent 的 `prompt` / `delegateCond`）承载，见 §4.4。`turn.go` 在 `BuildContextTokens` 之后将其**前插**为系统提示，随后注入子轮次 persona（`llm_run` 每条 LLM 指令的 agent 参数）。

**systemPrompt 五层拼接 + 会话固定参数（SUBCHAT-2 阶段 A，2026-10-16）**：唯一组装点 = `turn.go resolveTurnScenario(instanceID, sessionID, scenarioID, agent)` —— 拼接顺序 **全局 → 目录 → 场景 → agent → 会话注入层**（第五层，见 [25 §3](../10-architecture/25-MCP与场景分层模型.md)）。
- **参数优先级 = 会话固定参数 > 请求参数**：先按 `sessionID` 读会话行的 `agent` / `scenario_id`（仅子聊天有；见 [22 §5](22-data-persist.md)），非空即覆盖入参；
- **新轮与恢复轮同口径**：`recoverTurnCtx` 虽只有 `{instance, session}`（请求里无 scenario/agent），仍经同一构造从会话行取回 → **重启后人格与第五层不丢**（此前恢复轮退化为"仅全局层 + 目录层"）；
- **第五层注入条件 = 会话行 `kind ∈ {sub_chat, group_chat}`**，文本代码内建常量 `subChatLayerText`。
- **群聊扩展（SUBCHAT-3 阶段 F，2026-10-16）**：`kind ∈ {group_chat, work_session}` → 注入**群聊注入层**（`chatInjectionLayer` → `groupChatInjectionLayer`）：参与者名单 + 各自角色（读 `group_participants`）+ **本次目标 / 剧本 / 收敛与退出条件**（会话行可选列 `goal` / `playbook` / `convergence`）+ 推进规则（末行 `@` 决定下一个叫谁）+ 收口协议；`work_session` 取其**父群聊会话**的注入内容。文本代码内建（`groupChatProtocolText` / `groupChatReplyRules`）。

**单聊（子聊天）域工具与方法面（SUBCHAT-2 阶段 B-2，2026-10-16 · `src/lib/llm/server/subchat.go`）**：

| 项 | 落点 / 行为 |
|----|------------|
| `subchat_open`（域工具，`category=server`/`async:never`） | 建**固定参数**子聊天会话（`kind=sub_chat` + `parent_id` + `agent`/`scenario_id`/`llm` + `lifecycle=chatting` + `purpose`，经 data 门面 `s.cfg` **inline 直调** `SessionEnsure`）→ 交接内容**以用户口吻**写为首条 `user` 消息 → 发 `subchat-opened`（父窗口自动开窗）→ **异步**发起子会话首轮 → 返回 `{ok, session_id}`（**主轮不挂起**；**每个 tool-call 一条消息只落一次**） |
| `subchat_finish`（域工具，同上） | 把**本 turn 所属**子聊天会话置 `lifecycle=ready`（**建议态，不等于结束**）+ 发 `subchat-state`；不关窗、不回流 |
| 子会话首轮（D30「子 agent 自动先开口」） | `startSubChatFirstTurn`：建 `turnCtx`（**不带 parents**——子聊天不进任务执行树）→ 登记 `busy`/`turns` → 广播 `turn-start` → `turnCtx.Kick()`：以**库内既有上下文**跑一次 `chatOnce()`（无新输入、不重复落库；`turn.go` loop 新增 `kick` 分支，保证 `chatOnce` 恒在 loop goroutine 内串行执行） |
| 生命周期回写（§3.7） | **子聊天新一轮开始处**（`newTurnCtx`，用 `resolveTurnScenario` 同一次会话行读取带回的 `kind`/`lifecycle` 判定）：`lifecycle == ready` → 置回 `chatting` + 发 `subchat-state`。`recoverTurnCtx` 不走（它是既有轮次的恢复，非新一轮） |
| `subchat-list` / `subchat-finish`（client→server 方法面） | `Start` 订阅（`s.bus.On`，主题与前端 type **同名**）：清单（`parent_id` 有值 → `SessionSubChats` 全部含 finished；缺省 → `SessionSubChatsAll` 过滤未结束）+ 补 `parent_session_title`；结束（`SessionLifecycleSet(finished)` + `subchat-state` + `{ok}`） |
| `purpose` 来源 | **注入通道**（`payload.context.purpose` = gateway 剥离前取出的 `tool_call_display_name`）→ 落 `sessions.purpose` → 随 `subchat-opened` 下发（**不从 args 读**，见 [26 §4.3](26-mcp-gateway.md)） |
| `subchat-state` 投递 | **广播**（不带 `instance_id`），前端按 `parent_session_id` 过滤 —— **有意设计 = 「父 + 子都要」分支**（父窗口状态条 + 子窗口都需知）。**`ask_user` 则走定向**（见下「F-2」行）：41 **D-62 已闭环（2026-10-16，方案 A：补 session→instance 反查）** |
| 非主会话强制同步 | 子聊天（`kind` 非空）内**每次**工具调用附调用级 `async=never`（`turnCtx.forceSyncTools`/`gatewayArgs`，阶段 A 已铺；收口点亮与轮次屏障依赖轮次跑完） |

> **工具分派**：`subchat_open`/`subchat_finish` 归 `isTaskTool`（chatOnce 走"task 型"分支，节点由 `domainNode` 建、**不重复建节点**）；`domainExecute` 新增 `isSubChatTool` 分支**同步**执行（结果即终态，§3.4.3）——与 `tool_stop`/`tool_result` 同族。工具契约 = `contracts/tools/{subchat_open,subchat_finish}.tool.md`（embed，阶段 B-1/B-2）。

**群聊「依次认领」调度器（SUBCHAT-3 阶段 F，2026-10-16 · `src/lib/llm/server/groupchat_sched.go`；设计见 [群聊会话设计方案](../../agent-chat/群聊会话设计方案.md) §2/§3/§5/§6/§7/§18）**：

| 项 | 落点 / 行为 |
|----|------------|
| 发起（扩展 `subchat_open`） | `participants` **≥ 2** → 建 `kind=group_chat` 会话（`lifecycle=chatting` + `parent_id` + `agent`=主 agent + `goal`/`playbook`/`convergence` 落**会话可选列**）+ 写 `group_participants`（含 `seq` 固定顺序）+ 布置任务为首条展示板消息（`role=user`，主 agent ≡ 用户）+ `subchat-opened` → **异步**起调度器；**省略 / 1 项 → 单聊**（既有路径逐字节不变） |
| 大轮次循环（`groupSched.run`） | 按 `seq` **依次投递**（不抢、串行）；每人一轮 = `work_session` 里**一次** LLM 调用（判定 + 执行同一次）；「有关」→ 结论**立即上屏**（追加群聊展示板消息，带 `sender_agent`/`mentions`）+ 入下一轮；「无关」→ **不上屏、不入下一轮**；本轮走完（**屏障只挡下一轮启动**）→ 由发言 `@` 算下一轮投递对象并写 `group_round_targets`；**无人认领 → 主 agent 兜底** |
| `work_session`（D12/D23） | 每个参与者一个、**跨大轮次复用**（确定性 id `work-<群聊>-<净化agent>-<hash>` + 幂等建会话）；继承群聊上下文（逐轮喂入合并发言）；工具交互全在这里；**不进子聊天清单** |
| 参与者状态 | `subchat-participant-state`：轮到 → `thinking`、判无关 → `idle`、产出结论 → `spoken`（广播，前端按 `parent_session_id` 归属过滤） |
| 收敛 / 终止 | 主 agent 结论文本含**单独一行**「【已收敛】」→ `lifecycle=ready` + `subchat-state`（复用既有消息）；用户 `subchat-finish` → `finished`；**硬上限** `group_chat.max_rounds`（默认 20）到顶 → 主 agent **终审**（「【已收敛】」→ 点亮；否则 **强制 `finished`** + 提示「因达上限终止，可能未完成」）；用户 `subchat-continue` → **原线程续跑**（另给一轮预算） |
| 机读约定（不得新增消息主题） | 「无关」= work_session 最终 assistant 文本**首个非空行**恰为「无关」；「收敛」= 主 agent 文本**单独一行**「【已收敛】」；`@` 名单 = 发言**末行**的 `@xxx`（`@全体`/`@所有人` → 公开条）。详见设计稿 §18.6 |

**群聊 F-2（ask 定向投递 / 驱动归属 / 关窗即停 / 断点补跑 / 工具面收窄；SUBCHAT-3 阶段 F-2，2026-10-16 · 群聊设计 §13/§15）**：

| 项 | 落点 / 行为 |
|----|------------|
| `ask_user` **定向投递**（F2-1 · §13 D42；闭环 41 D-62） | 写侧 `instance-register` 增**可选 `session_id`**（对话/群聊窗口随桥上报承载会话，`src/lib/gui/{window,bridge}.go`）；读侧 `instance.Manager.InstanceForSession` 反查 → `ask()`（`askTargetInstance`）把目标填进 **`session-ask.instance_id`**：`work_session` → 其父 `group_chat` → 群聊窗口；`group_chat`/`sub_chat` 本体 → 承载窗口；反查不到 → 回落发起轮次实例。**`ask_user` 形态与前端弹窗零改动**；应答来自投递目标窗口时按 `askWaiter.deliverInstance` 兜底命中（`takeAskLocked`） |
| 群聊**驱动归属** = 群聊窗口的 instance（F2-2 · §15.4 N23） | 调度器每次叫号解析 `driverInstance()`（`InstanceForSession(groupID)` 命中 → 该窗口实例；未命中 → 回落发起窗口），子轮次以它执行（**不再裸 `context.Background()`**，父 ctx = `sc.ctx`）→ ① `ask_user` 落对窗口；② **关窗即停**：该实例 `instance-exit` → `Server.exitInstance` → `stopGroupRunsForInstance` 取消运行 ctx（停机不收口） |
| **暂停 / 终止 / 强关**（F2-3 · §15 · N22/N25） | **暂停** = 进程内停机（`sc.ctx` 取消；**不落库、无 `paused`**，`lifecycle` 保持原值）；**终止** = 既有 `subchat-finish` → `finished` + `stopGroupRun`（停掉在跑调度器）；**强关应用** = 复用既有 `interrupt`（`TurnCleanupStale` + `cleanupStaleToolPairs`，**未新增态**）。**关窗选择弹窗属阶段 G** |
| 「继续」= **断点补跑**（F2-4 · §15.4 D45） | `groupResumePlan`：读最后一个大轮次的 `group_round_targets` → 逐个比对「本轮是否已产结论」（展示板结论计数 vs 该 agent 累计被投递次数）→ **有结论跳过、只补无结论者**（`start` 不前进、不覆盖该轮记录）→ 补齐后本轮收口、续下一轮；**本轮已齐** → 从下一轮起（F-1 到顶续跑语义）。**续法 = (甲) 从断点续**：`work_session` 跨轮复用 → 历史即断点（被中断的工具由启动清理标 `interrupted`）。**续跑计划抽取为纯函数 `groupResumePlan` 便于单测** |
| `work_session` **工具面收窄**（F2-5 · §7/D12） | `kind == work_session` → `turnCtx.denyTools` 含 `subchat_finish`（**下发面 `llmTools` + 调用处 `toolAllowed` 双拦**，按契约名/暴露名两种口径匹配）；**单聊（`sub_chat`）不受影响**（收口仍靠 `subchat_finish`） |

> **systemPrompt 分层拼接**：按序拼接 = **全局层**（身份/运行环境，**代码写死**，不落文件/不 embed/不可配置）→ **目录层**（四级数据根 + `capability/` 子目录 + DSL env 说明；出厂文件 `capability/system/system-directory.md`，**每轮读取、不入快照、不受场景门控**，OP-10 增）→ **场景层**（`scenario.description` + **代码按场景 agents 自动拼接的成员段**〔名字 + roleTag + 描述〕）→ **agent 层**（`*.agent.md`，主 agent = `main.agent.md`）→ **会话注入层**（**第五层**，`kind ∈ {sub_chat, group_chat, work_session}`：单聊 → 收口协议骨架；群聊本体 / 其 `work_session` → **群聊注入层**〔名单/角色/目标/剧本/收敛条件，`work_session` 取其父群聊会话内容〕；代码内建常量）。**无场景（通用模式）→ 只注入全局层 + 目录层**。数据层 `Scenario.SystemPrompt` 字段**已删除**（facade DTO / wire / `ReadScenarioDir` 均不再有）；场景层提示词恒由本侧各层自拼。现 `memoryGuide`（门控 `memory.enabled=true`）/`assetGuide`（门控已接入 capability 节点）是**两条功能指引**，与"全局层"**不是一回事**。

> 设计要点：模型与生成参数属**用户级配置**（随 LLM 条目），场景 systemPrompt 属**场景文件**（`<级别根>/capability/scenarios/<目录>/`，级别四级 app/user/project/prjusr，**四级均可编辑**），两者均在轮次受理时解析并注入。
>
> **工具面语义（🔵 待实施）**：**直供**（进入 LLM `tools` 参数）= **`hot` 工具 ∪ gateway in-memory MCP 工具**（`mcp_find`/`mcp_load`/`mcp_invoke`）；⚠️ `hot` **两个来源都要** —— ① 下游 `HotTools`（内嵌 self 节点为 `"*"` = 全部）② **meta 工具显式 `_meta.hot=true`**（self 节点 entry **无 `HotTools`**，`isHot` 不会自动补）。**「通用模式」（无场景）工具面 = 全部 hot（含 core/browser/desktop）+ meta**（此前"仅 core 工具"**已否定**）。**白名单（`agent.tools`）语义修正**：**非空 → 下发面 = 白名单里的工具（不论是否 hot）**；**空 → hot 集**。**既有缺陷**：现 `llmTools()` = `toolsForLLM()`（**只含 hot**）再按白名单**取交集** → **白名单中的非 hot 工具被吞掉**（用户配了却**永远传不到 LLM**），须修（**T1**）。**执行面**：空白名单 = **不限制**；白名单非空 = **仅白名单内**（含 `mcp_invoke` 目标工具亦须在白名单内）。

### 4.7 prompt 占位符 `{{toolchain.<key>}}` / `{{path.<key>}}`

**一句话**：prompt / system 文本里的 `{{toolchain.<key>}}` 按**实例 usr 配置**的工具链路径替换（用户口径："占位符：所有 prompt。名称 toolchain.xxx 包括 java,python 等"）。

| 项 | 规格 |
|----|------|
| key | `java` / `python` / `node` / `go` / `rust` / `c` / `chrome`（`mcpms.ToolchainKeys`） |
| 取值 | usr 标量 `javaPath`/`pythonPath`/`nodePath`/`goPath`/`rustPath`/`cCompilerPath`/`chromePath`（经 `data-user-config-load`，同 `loadExecConfig` 读法；`src/lib/llm/server/toolchain.go toolchainVars`） |
| 替换语义 | 已配置 → 路径；已知 key **未配置 → 空串**；**未知 key → 原样保留**；仅匹配 `{{toolchain.x}}` 整串，**不影响 `{{arg}}`（MCP GetPrompt 实参）与 DSL `{{env.X}}`**；文本无占位符 → **零成本直返**（不发配置读取） |
| 单实现 | `src/lib/mcp-server/server/toolchain.go ReplaceToolchain`（两模块共用） |

**覆盖落点（5 处）**：

| 落点 | 读点 / 装配 |
|------|------|
| 场景 systemPrompt | `mainScenarioAgent`（`domainmcp.go`）→ `replaceToolchain`（见 §4.6） |
| 记忆库带出指引 | `memoryGuide`（`memory_guide.go:104`）→ `replaceToolchain`；**门控**：`memory.enabled` 非 true → **不注入指引、也不请求 `data-memory-list`**；`memory.category.<X>` = false → 该类不列入 |
| 发 LLM 的工具契约描述 | `toolsForLLM`（`server.go:1443`；仅当描述含占位符才懒读配置，替换只改本次下发副本、不动 gateway 缓存） |
| `llm-simple`（含压缩摘要 `capability/system/summary.md`） | `onLLMSimple`（`server.go:358`；`replaceToolchain` 调用 `server.go:370`）替换 `system` 与 `prompt` |
| capability `*.prompt.md` / `*.skill.md` | `loadExecConfig` → `mcpCfg.SetToolchain`（`server.go:1677`）→ mcp-server `makePromptHandler` 的 GetPrompt 渲染（见 [25-mcp-server §3.1](25-mcp-server.md)） |

**说明承载**：占位符的**用法示例与替换规则说明由场景 system prompt 承载**（默认场景主 agent = `scenarios/default/main.agent.md`），**不单独设指南契约文件**（用户口径：「工具使用指南理论上应放在系统的 prompt 里」）。出厂默认场景 = **app 级文件** `src/initdata/capability/scenarios/default/` → 投放 `<exeDir>/scenarios/default/`（与 `capability/` **平级**，源唯一 = `src/initdata/capability/scenarios/`）。

**`{{path.<key>}}`**：agent「# 正文」（技能 / 规范）里的 `{{path.<key>}}` 在**系统提示词分层拼接后**替换为四级根路径的**绝对路径**，指示 LLM 用文件工具自读（LLM 经 `mcp_find`/`mcp_load` 读不到能力面 `capability/skills/*`）。**目录层**（`capability/system/system-directory.md`，OP-10）也含 `{{path.*}}` → 同样在此步替换为真实绝对路径。

| 项 | 规格 |
|----|------|
| key | `exeDir`（`<exeDir>`；app 根 = `<exeDir>/capability`）· `userDir`（`~/.chonkpilot`；user 根 = `<userDir>/capability`）· `dataDir`（`~/.chonkpilot/data/<project-id>`；prjusr 根 = `<dataDir>/capability`）· `workDir`（`<workDir>`；project 根 = `<workDir>/.chonkpilot/capability`） |
| 取值 | `exeDir` = `exedir.Dir()`；`userDir` = `filepath.Dir(persist.CapUserRoot(opts.UsrPath))`；`workDir` = 实例绑定 `scope.WorkDir`；`dataDir` = 门面 `KnowledgeRoot(kind=prjusr)` 的父目录（`src/lib/llm/server/toolchain.go pathVars`） |
| 替换语义 | 同 `{{toolchain.*}}`：已知 key 解析不出 → **空串**；**未知 key → 原样保留**；文本无占位符 → **零成本直返**（不发门面请求） |
| 落点 | **系统提示词分层拼接后整段**（`turn.go newTurnCtx`：`replaceToolchain` 之后调 `replacePaths`；覆盖任一层，含目录层） |
| 单实现 | `src/lib/llm/server/toolchain.go replacePaths` / `pathVars` |

> **机制边界**：只做文本替换，**不改消息面结构/主题**（[61-消息一览](../60-reference/61-消息一览.md) 未改）；与 DSL 的 `{{env.X}}`（[63-DSL语法](../60-reference/63-DSL语法.md)）互不影响。

### 4.8 内置兜底 `echo` provider（`protocol=echo`；**不再作为可配置项**）

**一句话**：**内置兜底**（代码写死；**不再作为「可配置 provider」暴露** —— 不进 usr `llms`、不出现在 LLM 设置页 / 聊天选择器）的占位 provider —— **不发任何 HTTP、不注册/不下发任何工具**，取**本轮最后一条真实用户消息**填入**固定文案**后回一条（`收到<用户输入>，目前无法回复，请设置LLM。`；`finish_reason=stop`，单段 content，流式语义同既有 `StreamEvent`）。**「无任何可用 provider」的自动兜底归 router**（`src/lib/router` 的 `builtinFallback`）。

| 项 | 规格 |
|----|------|
| 定义 | `name=echo` / `protocol=echo` / `model=echo`；无 `baseUrl`/`apiKey`（`server.go` 的 `builtinFallbackName` + `builtinFallbackProvider`） |
| 触发 | `llm-start.llm = "echo"`（`loadLLMProvider` 未命中 usr `llms` → 命中**保留名兜底**；其余名 → 回落 exe flags） |
| 行为 | `lastUserText`：倒序取 `role=user` 且 `isTurnBoundary` 的消息（**排除系统注入**：`Kind=notify` 工具通知 / `continue` 续写 / `resume` 恢复）；**回复文本 = 固定文案**：`replyText(用户文本)` = `收到` + 该条用户消息内容 + `，目前无法回复，请设置LLM。`（**唯一文案源** = `internal/adaptor/echo` 常量 `replyPrefix`/`replySuffix`，**不是**原文回显）；无真实用户消息 → 不发 content 段；**实现 = router `internal/adaptor/echo`** |
| 只读行 | **无任何内置 / 系统默认只读行**：`gui.system.builtins` 不再下发 `builtinLLMs`；LLM 设置页改 2 页签（一览 / 默认模型），「主对话」默认 = usr `defaultLLM`。 |
| 归一 | `echo` **不**被归一成 `openai`（`NormalizeLLMProtocol`）；白盒 `src/lib/router/internal/adaptor/echo/echo_test.go`（含 `TestReplyTextFixedTemplate` 固定文案逐字断言）+ `router_test.go`（内置兜底文案）+ llm 侧 `llm_echo_test.go`（无 HTTP + 固定文案 / 系统注入不入文案 / 保留名兜底不可配置 / 近似名回落） |

### 4.9 图片（多模态）输入

**一句话**：图片仍以既有**文本引用** `![名](绝对路径)` 承载（`InputBox.serialize`；**无新 MQ 主题、无 payload/`ChatMsg` 字段变更**），在**构造 LLM 请求体时**（`src/lib/llm/server/llm_images.go`）就地展开为多模态内容块。

| 项 | 规格 |
|----|------|
| chat（`openai` 兼容） | 该条 `content` 由 **string 变数组** `[{type:"text",text}, {type:"image_url",image_url:{url}}]`；`url` = **base64 data URL**（`data:<mime>;base64,…`）。取舍：provider `baseUrl` 多为远端、无法访问本地路径，故自带载荷；代价 = 请求体随图片线性放大 + 首字节略慢（由 `responseTimeout` 覆盖） |
| `responses` 协议 | `input` 的 message item 的 `content` 数组追加 `{type:"input_image", image_url:"data:<mime>;base64,…"}` |
| 源与范围 | 仅 `role=user`；消息内引用为**相对数据根**的 `dir/name`（如 `tmp/uploads/<uuid>.png` / `screenshot/<uuid>.png`，见 [61 §1 gui.upload](../60-reference/61-消息一览.md)），仅**落在数据根**（`imageDataRoot` → `data.PrjUsrDir`）内的引用按图片处理；**根外路径不读取、原样保留为文本**（避免用户手写 markdown 被当附件）。数据根与桥 `gui.upload` 落点同源：`data_dir` 非空 → 该目录；`data_dir` 空（desktop 缺省）→ `~/.chonkpilot/data/<prj-id>`。（兼容历史消息的绝对路径引用） |
| 历史 / 压缩规则 | 仅**最近 3 轮**（`defaultKeepImageTurns`，轮边界 = `isTurnBoundary`，与 `assemble.go` 同源）保留原图；更早轮次的引用**降级为文本占位** `[图片: 名]`；`plugin-compress` **无需改动**（早前轮次整轮被摘要替换） |
| 上限 | 类型白名单 `png/jpeg/webp/gif`（扩展名 + **魔数**双重校验，mime **以魔数为准**）；**单图 5MB / 单消息 4 张 / 单请求 8 张** |
| 错误语义 | 文件缺失/不可读/类型不支持/内容不可识别/超限 → `*LLMError{Kind: ErrProtocol, Retryable:false}` 且**不发起 HTTP 请求**（随 `llm-error` 可见，**不静默丢**）；`UploadDir` 为空 → 引用原样作文本发送（纯文本路径不回归） |
| 前端 | 上传中 chip/状态、失败提示；消息内图片经 `/show/` 缩略图（**无新浮层**） |

---

### 4.10 中途插话（INBOX 帧栈，2026-10-17）

> 设计权威：[中途插话设计方案](../../agent-chat/中途插话设计方案.md)。消息面见 [61 §4.3](../60-reference/61-消息一览.md)。

**目标**：工具执行期间（`toolPending`）允许用户插入一段对话并**优先处理**；流式输出期（reason/text/tool-call）不允许打断，消息先入 inbox；插入轮自身可再调工具（嵌套）；插入结束后按"外层工具是否已完成"自动续轮。

**执行模型**

- 工具派发即异步（§4.1 强制异步）→ `chatOnce` 返回、`loop` 回到 `select`，`inboxCh` 才可能被消费。
- 帧（frame）= 一次进行中的 LLM 轮次及其工具等待集合；**LIFO 栈**，栈顶帧是唯一可发起 LLM 请求者。主帧状态仍由 `turnCtx` 的 `pending`/`inflight`/`stash`/`answer` 承载，插入帧为 `insertFrame`。
- 恢复判定（唯一入口 `maybeResumeTop`，双条件 AND）：**帧 == 栈顶** ∧ **本帧 `pending`/`inflight` 均空**；回执只清 `pending`，不直接续轮 → S4（插入先结束）/ S5（回执先到）/ S4.5（等待期反复插入）都退化为同一判定，且只续一次。
- 插入帧终结**不发 `session-complete`、不关 turn**（`session-complete` 仍只在整个栈清空时发一次）；插入轮产生的全部消息写**主 turn 的 `turn_id`**。
- 插入轮恢复外层时临时注入「续接说明」system 文本（不落库 / 不进快照 / 不参与压缩）。
- 未完成 / 空结果工具在 LLM 投影中按 `tool_call_status` 给明确占位（`ToolLLMContentStatus`：执行中 / 已中断 / 已取消 / 失败 / 完成无输出），**不得为空串**。

**发送入口（inbox 优先，含空闲首条）**

| 当前相位 | `llm-inbox-add` 分类 | 后续 |
|---|---|---|
| `toolPending` / `awaitJoin` | `insert` | 立即落 `messages` 并 push 插入帧 |
| `streaming` / 无运行轮 | `next_turn` | 等本轮结束；主会话由 `finish` 后 `consumeNextTurn` 起新一轮（同批多条**合并为一次**请求、逐条落库）；**无运行轮 → 立即消费起轮**，turn id 由服务端分配并经 `turn-start` 回填前端；子会话 / 群聊由各自调度器驱动 |

> 同轮次「继续」（`llm-start{continue:true}`）复用既有 turn，不走 inbox 队列（非新轮语义）。

- inbox 持久化 = prjusr `inbox_items`（`data-inbox-append/list/update`），每会话 pending ≤ 50；同 `idempotency_key` 幂等。
- 消费顺序（Q6）：inbox 行先落库 → 写 `messages` → **立即**置 `consumed`（崩溃重复窗口 = 一次落库）；`insert` 落库失败/相位变化保持 `pending`；`next_turn` 批次在起轮前与逐条写库前**二次 pending 复核**；`discard` 仅对 `pending` 生效（已 `consumed` 拒绝；撤回后不得再落 message）。
- 撤回 = `discarded`；`next_turn → insert` 显式提升（`llm-inbox-promote`）；无运行轮时 promote = 重启后「继续」消费。
- 重启：帧栈**不重建**；非终态工具由 `TurnCleanupStale` 收敛 `interrupted`；inbox pending 拉回、**不自动发送**。
- 收尾（G-65）：`TurnCleanupStale` 同一启动触发点顺带清理**终态 inbox 行**（`consumed`/`discarded` 保留 7 天，`pending` 永不自动删）；`SessionDelete` 级联删除该会话全部 `inbox_items`（含幂等删除不存在会话时的历史孤儿行）。
- **LLM 自登记排队项（NEXTRUN-1）**：`next_run(prompt)` 写 `next_turn` 行（`source=llm`、`turn_id=当前轮`、幂等键 `next-run:<turn>`），同轮多次调用就地覆盖 `content`（last-wins）；`consumeNextTurn` 消费时**过期判据** = 该行 `turn_id` 不等于会话最新轮 → 置 `discarded`（不落消息），合法项落消息 `kind=next_run`。`source` 字段仅允许 `llm`（用户排队项为空）。计数器 / 上限 / 重置与终态收敛见 §3.4、§4.2。

**边界**：插入帧 LLM 失败 → 仅丢弃该帧并恢复外层（主轮不受影响）；帧深上限 5；`llm-cancel` = 全局终止（所有帧 + 在飞工具，恰一次 `interrupted`，不回到原任务）。

---

## 5. 数据结构与存储

- 无本地存储：全部经 `data-*` 消息面（`session.go` 的 `sessionStore` 方法 ↔ 主题一一对应）：`ensure-session`/`ensure-turn`/`append-message`/`load-messages`/`context`/`complete-turn`/`cleanup-stale`/`set-summary` + `data-snapshot-set`。
- 内存运行态：`turns map[turn]*turnCtx` · `busy map[session]turn` · `asks map[askID]*askWaiter` · `cleaned map[instance]bool` · `taskManager.nodes`。（**目标态**：ask 等待期 turn 运行态 = `awaiting`（非 `running`）；`asks` 在重开时由落库 ask 节点 `content` 重建 —— 见 §4.3）
- 会话锁：`lockSession`（Windows 文件锁；非 Windows 为 stub，恒失败）。

---

## 6. 依赖

- **上游**：`src/lib/core`(mq) · `src/lib/data`(persist) · `src/lib/gateway` · `src/lib/mcp-server` · `src/plugins/plugin` + `-compress`/`-history`/`-codegraph`。
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
- **用户/项目/项目私有级 capability 动态接入**：`onInstanceRegister` 经 `capNodeSpecs` 解析**存在的**用户级 `~/.chonkpilot/capability`（`persist.CapUserRoot`）、项目级 `<workDir>/.chonkpilot/capability`（`persist.CapProjectRoot`）与**项目私有级**（prjusr；根由门面 `KnowledgeRoot(kind=prjusr)` 解析，与知识库/场景写读同源）→ gateway `servers/register`（dir 节点，`scope=instance_id`，节点名 `<id>-user`/`<id>-project`/`<id>-prjusr`）；目录缺失跳过、重复注册幂等（`dirNodes` 记账）、`onExit` 对称 `servers/unregister`。注册后主动 `s.gc.ListTools` 刷新工具缓存（gateway dir 节点注册不发 `mcp-gateway-changed`），使 `hot==true` 的契约进 `toolsForLLM`。**`@mcp` 旧名已废弃**（项目根为 [02-配置层级 §7.2](../00-overview/02-配置层级.md) 的 `.chonkpilot/capability`；系统级 `<exeDir>/capability` 仍由 self 节点承载）。
- **agent 工具白名单级别矩阵**：`newTurnCtx` 读场景级别（`loadScenario` 增返回 `level`）→ `filterWhitelistByLevel` 按"同级或更高级"矩阵**静默剔除**越权 / 不存在（不在本实例可见工具面）的工具名（工具级别由 `_meta.server.node` → `capfs.LevelOfNode` 判定；场景级别不可判定 / 工具级别无法判定 → 放行；空白名单 = 不限制语义不变）。矩阵单源 = `capfs.AgentToolLevels / LevelAllowed`（`persist` 转发）。详见 [25 §4.5](../10-architecture/25-MCP与场景分层模型.md)。
- **原语保存热生效（capwatch）**：`src/lib/llm/server/capwatch.go` 对**用户级 / 项目级 / 项目私有级** capability 根加 fswatcher 监听（60ms 去抖合并 create/write/chmod；`WithCooldown(0)` + 自管定时器），`fire` 按脏标记分流——① **用户级/项目级/项目私有级** → `reconcileCapabilityNodes`（复用 `servers/unregister`+`register` 重建 dir 节点）；② **系统级 app 根**（`<exeDir>/capability`，内嵌 self 节点）→ `Server.reloadAppContracts`：重跑 `RegisterContracts` 进同一内嵌 go-sdk server（同名覆盖更新 / 新增注入）+ `RemoveTools` 回收已删/改名工具 → 复用既有方法面 `gateway/reload` 让 self 节点重拉 list。**增/改/删数十毫秒级生效、app 根只读语义不变（UI 不可改）、零新增消息面主题**。**已知边界**：app 根目前**仅对 `*.tool.md` 做删除回收**（prompt/skill/resource 的删除不回收，保留至重启）；`gateway/reload` 会连带重列外部下游。监听失败不致命（退化为"需重注册/重启生效"的既有行为）。**递归监听由 fswatcher 单句柄承担**：根存在 → `AddPath(root, WatchNested)`；根不存在 → `AddPath(nearestExistingDir(root), WatchTopLevel)`，根创建后 `sync` 收敛；不再逐子目录 `Add`（原 `expanded/addTreeLocked` 机制已移除）。
- **取消**：`llm-cancel` 无 turn 时按 session 回退运行中轮次。

---

## 9. 现状与待办

- ✅ **已决**：`curInstance` 改 **context 承载**（`withInstance`/`instanceFromCtx`；`emitGateway` 从 ctx 取 `instance_id`）。
- ✅ **不支持非 Windows**：非 Windows `lockSession` stub 保持，文档标注"仅 Windows"，不再作待办。
- ⚠️ `Start` 中契约注册失败仅打印并"空能力面继续"；内嵌 gateway 失败同样仅打印。
- 🗄 `server/items.json` 已删除（无引用）。
- ✅ **域工具面收敛为 4 个**：`ask_user` / `llm_run` / `tool_stop` / `tool_result`。废弃工具 `tasks_run` / `tool_get_result` 删除，`tool_get_result` 更名 `tool_result`（内部判定统一）；`agent_list` 删除但**能力无损**——子 agent 发现由 gateway meta 工具 `mcp_find(type=agent)` 承接；legacy llm 别名（`llm_job`/`call_llm`/`llm_call`/`batch_llm`/`llm_batch`）一并删除。`agent_list` 注册已从代码移除（`registerDomainTools` 仅注入 4 个域工具契约），见 §4.4。
- ✅ **llm_run 路径收紧 + env（R-11）**：`file` 与 DSL 内 `#"path"` 拒绝相对路径（顶层失败，带行号/用途）；`jobFile.abs()` 为严格 `paths.ResolvePath(raw,"")`；注入只读 `env`（5 变量）供 `{{env.CHONKPILOT_WORKDIR}}` 拼绝对路径。

---

## 10. 关联测试

`src/lib/llm/server/*_test.go`：`llm_test`（流解析/错误分类）· `assemble_test`（**三段拼接**：完整区原文 / 简化区**仅 text**（`TestAssembleTurnsBriefZoneTextOnly`/`TestAssembleTurnsThreeZones`）/ token 边界与预存优先（`TestAssembleTurnsTokenBound`）；口径 X）· `llm_testconn_test`（`llm.test-connection` 只读探活 —— `TestLLMTestConnectionOK` / `...AuthMaskedKey`（Key 脱敏）/ `...Network` / `...Timeout` / `...InvalidParams` / `...ResponsesProtocol`（Responses 协议映射 `max_output_tokens`）/ `TestMaskLLMSecrets`，见 §3.1）· `llm_images_test`（图片多模态：`TestChatImageContentParts`/`TestResponsesImageInputItem`/`TestChatImageErrors`（6 子例，**HTTP hits=0**）/`TestImageRecentTurnsRule`/`TestImagesDisabledPlainText`/`TestTurnSendsImagesEndToEnd`，见 §4.9）· `turn_test`/`turn_cover_test`（续写/空回复/auth/异步/压缩事件/取消）· `tool_task_test`（tool_stop 级联/tool_result 取结果）· `tool_retry_test`（定位/重启恢复）· **`inbox_test`**（中途插话：S1 插入 / S3 嵌套工具 / S4 awaitJoin / S5 回执先到 / S4.5 重入 / 重复回执幂等 / 流式期 next_turn 与合并 / promote / 全局取消 / 上限 50 / **Q6 撤回优先 + 已消费不可撤回 + 批次 pending 复核** —— `TestInbox*`，见 §4.10）· `tasks_test` · `session_test`（含 **`TestBuildContextTokensReadsPrestored`**：P3 读通道真取预存值） · `sessionlock_test` · `prompt_test` · `plugins_test` · `jobdsl_test` · `domainmd_test` · `domainmcp_test`。
黑盒：`src/test/chonkpilot-llm/unittest/llm_test.go`（仅经公开主题驱动）；L1 `src/lib/llm/server/inbox_test.go`（S01–S07 + Q6 顺序/撤回：撤回后不落 message、已消费不可撤回、批次逐条 pending 复核）；`src/test/chonkpilot-data/unittest/persist/facade_inbox_test.go`（inbox 持久化 / 幂等 / 上限 / 重启保留 / 会话级联）；`src/lib/data/internal/kernel/viewdata_inbox_test.go`（工具状态投影占位）。L4：`src/test/chonkpilot-gui/systest/run_inbox.py`（真实 exe + mock LLM：工具执行中 `llm-inbox-add` → 插话先出流、主轮不提前终态）。

---

## 11. chat 引擎与端口（MOD-1，2026-10-20）

> 组件内核 = **`src/lib/chat/`**（独立 module `chonkpilot-chat`，`go.mod` 仅标准库）；宿主适配器 = `src/lib/llm/server/chatshadow{,_tool,_context}.go` + 接管装配 `engine_turn.go`。
> **生产唯一执行路径 = 引擎（A4-P4b，2026-10-21）**：所有轮次（主会话 / 子聊天 / 子会话 / 干活子会话 / DSL 子轮次 / 恢复轮）一律由 `runEngineLoop`（`loop()` 唯一执行体）执行；**无回滚开关**（不读任何环境变量），旧 `chatOnce` / P3a 单步接管 / 旧输入等待路径已删除。终态仍由 `Server.finish` 唯一收口。本节不改任何消息契约。

### 11.1 端口（消费方 = chat 定义，实现方 = 宿主）

| 端口 | 定义 | 宿主实现 | 语义要点 |
|------|------|----------|----------|
| `ContextProvider` | `Build(ctx, ContextRequest) (Context, error)` | `chatshadow_context.go`（`BuildContextTokens` + `chatTurnUserInputs` + `resolveTurnScenario`） | 读库 / 压缩 / 五层 system prompt 拼装全归宿主；**每轮只调一次**；`SystemPrompt` 不重复塞进 `Messages`；**`Build` 含本轮已落库用户输入**（A4-P2：历史 `exclude_turn` 排除当前轮 + 本轮 `role=user`（kind `""/text/insert/continue`）按 created_at 顺序追加；本轮 assistant / tool 由引擎重新生成，不进上下文） |
| `TurnStore` | `SaveMessage` · `CommitInbox` · `AppendEvent` | `chatshadow.go`（`AppendFullKeyed` · `data-inbox-commit` · `data-turn-event-append`） | `CommitInbox` = **同一事务**落消息 + 置 `consumed`（`M-INBOX-<inbox_id>` 确定性主键幂等） |
| `EventSink` | `Emit(ctx, Event)` | `chatshadow.go` 的 `chatEventSink` | **必须非阻塞**；`llm_delta`→`llm-receive` · `llm_retry`→`llm-retry` · `turn_complete`→`llm-complete`（**零新增主题**） |
| `ToolInvoker` | `Dispatch` / `Cancel` / `Detach` | `chatshadow_tool.go` | `Dispatch` = 策略硬拒 → `ask_user` 走宿主 ask 通道 → `mcp-tools-call` → 同步结果 / `status=pending` 挂起（`OpID` = gateway task_id）；工具级错误编码为 `ToolResult.IsError` |
| `ToolCatalog` | `Servers` / `Tools` | `chatshadow_tool.go` | `Tools` = 宿主**已过滤**的下发面（`[]ToolDef` → `ToolSpec`，`_meta` 透传）；`Servers` 恒空（服务目录属管理面） |
| `Clock` | `Now` / `After` | `chatadapter.go`（系统时钟） | 续写宽限期 / 轮询 / 超时；测试可注入假时钟 |
| `LLMClient` | `Step(ctx, StepRequest, StepOptions)` | `chatLLMClient`（`chatshadow.go`） | chat 与 router 的唯一接触面；协议 / 流解析 / **报文级重试**归 router |
| `Inbox` | `Poll(ctx) (InboxItem, bool, error)`（**非阻塞**） | 宿主队列（`llm-inbox-*` / 工具回执） | `insert` → 同事务消费 + 插入帧；`tool_result` → 仅轮内等待中消费；其余类型引擎忽略（宿主保留/丢弃） |

### 11.2 执行路径

```text
RunTurn(ctx, turnID)  /  RunSubTurn(ctx, req)        // engine.go
 └─ run → runFrame（可重入，一帧一次）
      ├─ ContextProvider.Build（每轮一次）
      ├─ step：LLMClient.Step(StepRequest, StepOptions)
      │    ├─ OnRetry → EventLLMRetry（可见重试）
      │    └─ 事件经 Apply 推进 TurnState + TurnStore.AppendEvent（受 turnEventLog 分级）
      ├─ 工具循环：Dispatch → 同步结果 / Pending（按 PendingWait 等待回执或立即返回）
      └─ 终态：completed / failed / canceled / pending（`awaiting_tool`）
```

- 状态变更**只经** `chat.Apply(state, event)`（`turnstate.go`：`Seq` 单调、未知事件显式失败、插入帧 LIFO、终态守卫）；路由状态 `TurnState` 可 JSON 往返。
- 插话帧栈（`engine.go`）：`maxInsertDepth = 5`；插入帧收口给出**一次性续接说明** `InsertResumeNote`（仅下一次 `StepRequest` 首位临时插入 system，不落库、不进快照）；全局 step 计数跨帧（`defaultMaxSteps = 32`）。
- 异步回执续轮：`RuntimeConfig.PendingWait > 0` 时轮内等待（`PollInterval`，缺省 50ms），命中匹配 `CallID` 的 `tool_result` 后按同步路径回填（`deliverToolResult`，**不重复 Dispatch**）；超时合成 `IsError` 占位续轮、不终结整轮。
- 自动续写：`AutoContinue`（零值关闭）/ `AutoContinueDelay`（缺省 600s）/ `AutoContinueText`（缺省「继续」）/ `AutoContinueMax`（缺省 1）。中断判定 = 已产生文本后的断流 或 `finish_reason=length`；宽限期内有插话 / 回执 → 取消续写；到期无输入才追加 `kind=continue` 的 user 消息。
- 可见重试：`RetryCount` / `RetryBudget` 由宿主配置注入并原样透传给 `StepOptions`；**次数与退避的单一所有者仍是 router**，引擎只把 `OnRetry` 折成 `EventLLMRetry`。

### 11.3 事件日志

- 表 `turn_events`（prjusr，schema v11）；写入 = `data-turn-event-append`（同 `turn_id` + `seq` 幂等、`type` 冲突失败），清理 = `data-turn-event-cleanup`。
- 分级 `turnEventLog`（`none` / `minimal` / `outline` / `verbose`，默认 `outline`）；`none` / `minimal` **不可重放**。
- 保留期 `turnEventLogRetentionDays`（默认 7，`0` = 永久）；`running` 永不按时间删。

### 11.4 影子与旧路径删除（如实口径）

- 已落：`chat/trace.go` 的 `Recorder` / `ShadowTurnStore` / `Trace` / `Diff` / `Verify` —— 同一事件序列 + 同一 LLM 桩下，`StepRequest` 序列与写入序列**逐项相等**（`TestChatShadowParity` / `TestChatToolLoopThroughAdapters`）。
- **旧路径已删除（A4-P4b，2026-10-21）**：`chatOnce`（旧 LLM+工具循环）· `deliverInput` · `consumeInbox` · `reapStalePending` / `reapInflight`（旧看门狗）· `maybeResumeTop` / `finishInsertFrame` / `abortCurrentFrame` / `setResumeNote` / `takeResumeNote`（旧插入帧续接）· `resumePartial` / `unpersistedInputs` · P3a 单步接管（`engine_step.go`：`engineTextEligible` / `runEngineStep`）；`turnCtx` 的 `engineMode` / `iterations` / `answer` / `stash` / `resumeNote` / `persistedResultOK` 字段一并删除。
- **保留（引擎依赖）**：`turnCtx` 结构 / `newTurnCtx` / `msgs` / `persistMessage` / `dispatchToolCall` / `finish` / `complete` / `Close` / `finishOnce` / `llmTools` / `gatewayArgs` / `toolAllowed` / `markInflight` / `registerPending` / 任务树 / ask / `raiseInterrupted` / `publishInterrupted` / `runEngineLoop` / `enginePump` / `engineFirstInput` / `engineRouteInput` / `engineRouteInsert` / `engineFinish`。
- **无回滚开关**：`chatEngineEnabled` / `engineTurnSelectable` 与相关环境变量判定已删除 → 引擎是唯一执行路径。
- 跨进程恢复（进程崩溃 → 宿主收敛 / 重建实例继续）语义不变：收敛到终态 + 保留已落库消息；恢复轮由引擎从历史继续。

### 11.5 生产执行：整轮引擎（唯一路径；A4-P4b 收口 2026-10-21）

- **开关**：无（引擎为唯一执行路径，不读取任何环境变量；不存在回滚旧路径的开关）。
- **覆盖范围**：全部轮次 → 主会话（会话行 `kind` 空）· 子聊天（`sub_chat`）· 子会话（`sub_session`，含 `runChildTurn` 的 llm 委派 / `subllm`）· 群聊参与者干活子会话（`work_session`）· DSL 子轮次（`isDSLSubTurn`）· **恢复轮**（`recoverTurnCtx`，`isRecovery=true`）。`group_chat` 本体不跑 LLM（无 turn 执行）。
  - **恢复轮**：上下文种子 = `tc.msgs()`（hist + 本轮**全部已落库消息**，system/user/assistant/tool 顺序一致，`forceFull=true` 保证全量）→ 引擎**只从历史继续下一次 LLM 调用**，不重复派发已完成工具。断链（assistant 有 `tool_calls` 但该轮缺 tool 行，启动清理未覆盖的极端情况）→ `runEngineLoop` 在**收到首个输入之后、落首条输入之前**调 `engineSynthesizeDanglingToolResults()`：逐条合成 `role=tool` + `tool_call_status=interrupted`（文案 = 数据层 `interrupted` 占位「（工具已中断，未获得结果；如需重试请使用「重试」）」）并落库 + 记 WARN；已存在的 tool 行（任何终态）不覆盖（幂等）；合成行紧跟 `assistant.tool_calls`（保证协议顺序）。
- **子轮次 AutoContinue**：父链非空 / `sub_session` / `work_session` → **强制关闭**（`engineAutoContinue()`），不计 `AutoContinueMax`；子聊天（`sub_chat`）保留 usr 配置（其窗口可成为活动会话，与生产 `activeSessionOK` 一致）。`length` 且续写关闭时仍由宿主补发同形 `llm-interrupted`（`auto_continue_delay_ms=0`）。
- **装配**（`engine_turn.go`）：
  - `ContextProvider` = `tc.msgs()`（生产同一份上下文：系统提示词 + 引导 + 本轮已落库消息）；
  - `TurnStore.SaveMessage` → `tc.persistMessage`（**role=tool 跳过**：结果由 `dispatchToolCall` / 异步回执路径落库）；`CommitInbox` = 落消息 + 置 consumed；`AppendEvent` no-op（生产不写 `turn_events`）；
  - `EventSink`：`llm_delta`→`llm-receive{type:text}` · `llm_retry`→`llm-retry` · `session_interrupted`→`llm-interrupted`（code 折回生产口径 `LLM_INCOMPLETE` / `LLM_STREAM_ERROR`）；`turn_complete` / 工具事件**有意不发**（终态仍由 `Server.finish` 唯一收口）；
  - `ToolInvoker` = **生产 `dispatchToolCall`**（白名单硬拒 / mcp_invoke 旁路加固 / running+result 同一行 / 任务树 / ask / 异步登记全部同源）；`Pending` → 引擎 `PendingWait`；
  - `ToolCatalog` = `tc.llmTools()`（生产下发面）；`LLM` = `newChatLLMClient`（同 ChatOptions，请求前按 `applyReasoningRule` 归一思维链回传）；
  - `RuntimeConfig`：`MaxSteps = maxToolIterations` · `EventLogLevel = none` · `PendingWait = pending_task_timeout_sec`（缺省 2h）· `PollInterval = 50ms` · `AutoContinue* = usr`（`autoContinue` / `autoContinueDelaySec` / `continueText`，上限 = `maxAutoContinue`）· `PendingPlaceholder` = 数据层 running 行投影文案。
- **输入漏斗（唯一消费者 = 引擎）**：`tc.in`（工具结果 → `tool_result`；用户文本 → `insert`）· `tc.inboxCh`（相位 / 撤回 / 空内容判据 → 交引擎建插入帧）· `tc.asyncDone`（`handleAsyncDone` 落库后投 `tool_result`）。
- **终态映射**：正常 → `complete`；`length` → `incomplete`（未开启续写时宿主补发 `llm-interrupted`，与生产同形）；`ctx` 取消 → 静默（取消路径已发 `interrupted`）；错误 → `LLM_REQUEST_FAILED`（retryable 取 `*LLMError`）；`ErrMaxSteps` → `TOOL_LOOP_LIMIT`；空回复 → `EMPTY_REPLY`。
- **行为证据**（`llm/server/engine_turn_test.go`，引擎唯一路径单跑断言落库消息 / role=tool 行终态 / 任务树事件 / `llm-receive` 文本 / `llm-complete`）：纯文本 · 同步工具 · 异步工具 · ask · 插话 · `length` · 取消 · 同轮次继续 · 图片 · 空回复；**子聊天**（`TestEngineTurnSubChat`）；**恢复轮**：始终引擎（`TestEngineTurnRecoveryAlwaysEngine`）+ 半途恢复（`TestEngineTurnRecoveryMidTurn`：user/assistant(tool_calls)/tool(completed) 三条进入下一步请求、gateway 零调用 = 不重复派发、落库 / 终态与新建轮「继续」一致）+ 断链合成（`TestEngineTurnRecoveryDanglingCall`）；逐路径覆盖另由 `TestSubChat*` / `TestGroupChat*` / `TestDSLSubTurnDeniesAskUser` / `TestRecoverTurnCtx*` 回归承担。
- **已知边界（如实登记）**：① 异步回执等待由引擎 `PendingWait` 兜底（同阈值），**超时占位结果不落库**（仅回填续轮）；② 同一 step 的多路异步调用**串行派发**（结果 / 落库 / 任务树终态一致，时序不同）；③ 断流错误（已产出文本后）收敛为 `LLM_REQUEST_FAILED`（`retryable` 与文案保持生产口径）；④ 自动续写用尽后的第 N+1 次中断提示 `auto_continue_delay_ms` 取配置值（生产为 0）；⑤ 插入帧深超限时引擎留 pending；⑥ 中途 `text-notify` 输入引擎不消费（留痕；主会话仅在轮次首输入前出现 `continue`）；⑦ **子轮次（父链 / `sub_session` / `work_session`）中断提示** `auto_continue_delay_ms=0`（生产按 usr 配置值展示倒计时，但因 `activeSessionOK` 不成立同样不会真正续写）。


---

## 12. 恢复（MOD-1，2026-10-20）

### 12.1 落点

| 数据 | 落点 | 写入方 | 用途 |
|------|------|--------|------|
| turn 事件日志 | prjusr `turn_events`（`data-turn-event-append` / `-cleanup`） | llm-server | 重放游标 / 收口依据；按保留天数清理 |
| ask 发起记录 | `messages.ask_id`（**仅 `ask_user` 的 role=tool 运行行**；**不建 ask 表**） | llm-server（`persistToolRunning(..., askID)`） | 重启 / 超时后回查「这题答过没有」 |
| 待答 ask 查询 | `data-ask-pending` | persist（`facade.AskAPI`） | `ask_id` 空 + `pending_only` = 启动扫描；`ask_id` 非空 = 精确回查（含终态） |
| inbox 原子消费 | `data-inbox-commit`（消息 + `consumed` 同事务；主键 `M-INBOX-<inbox_id>`） | persist | 消除「消息已落、inbox 未消费」重复窗口；重试幂等 |
| 失效回执 | `askrecover.go` 的 `replyExpiredAsk` | llm-server | 未命中等待登记时不静默：已终态 → 明确失败；非终态 → 转普通 user 消息开新 turn |

### 12.2 行为

- **启动扫描**（首次使用 instance、遗留清理**之前**）：`data-ask-pending{pending_only:true}` 只统计未答 ask 并留痕；随后既有 `CleanupStaleTurns`（`session.go`）把非终态 turn 收敛为 `interrupted`，`cleanupStaleToolPairs` 收敛未完成 tool 对。
- **DSL 子轮次**：`runDSLSubTurn` 起轮前 `markDSLSubTurn`，`newTurnCtx` 命中 → `denyTools{ask_user}`（下发面 + 调用处双拦），避免无人值守永久挂起。
- **已回答不重问**：答案的最终形态 = tool result；回填即落库，重启后无需重问。
- **待生产切换**：帧栈「继续」（插入帧执行）在引擎侧已实现（`maxInsertDepth=5`），但生产 `turnCtx` 仍不重建帧栈；**跨进程重建**（崩溃后按 `TurnState` + 事件日志重建实例）亦随生产切换。当前崩溃语义 = 收敛到终态 + 保留已落库消息。

### 12.3 恢复五问（[71 §3.3](../70-conventions/71-编程规范.md)）

| # | 问题 | 现状回答 |
|:--:|------|----------|
| 1 | 状态落在哪 | 内存 = `turnCtx`（生产）/ `TurnState` + `frameRun`（引擎）；持久化 = `messages`（含 `ask_id` / tool 状态）、`inbox_items`、`turn_events`。崩溃即丢的只有**运行中帧栈与在飞登记** |
| 2 | 受理即持久化 | 用户输入 / 插话 / ask 回答 / 工具结果**先落库再推进**；inbox 走 `data-inbox-commit` 同事务（消息 + `consumed`） |
| 3 | 非终态谁收口 | 启动 `CleanupStaleTurns` → `interrupted`；`cleanupStaleToolPairs` → 未完成 tool 对；`pendingSweepInterval`（30s）看门狗按 `pending_task_timeout_sec` 兜底回填 |
| 4 | 重启后重建 vs 收敛 | 当前 = **收敛到终态并保留已落库消息**；引擎具备 `TurnState` 快照 + 事件日志重建能力，**跨进程重建随生产切换启用** |
| 5 | 副作用语义 | inbox 消费 = **至多一次**（确定性主键 + 同事务 + 已 consumed 直接返回）；ask 答案 = tool result（回填落库后不重问）；工具按 `tool_call_id` 幂等回填，不重派发已完成工具；启动清理幂等可重跑 |
