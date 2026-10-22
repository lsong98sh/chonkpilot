# 26 · src/lib/gateway（MCP 网关）

> 状态：✅ 与代码一致（2026-10-21 GATEWAY-2 L5/L6/L7/L8 同步；见 §0 现状订正）
> 关联：[10-分层与依赖](../10-architecture/10-分层与依赖.md) · [25-mcp-server](25-mcp-server.md) · [27-mcp-tools](27-mcp-tools.md)
> 代码目录：**内核 = `chonkpilot-mcp-gateway/`**（独立目录，与主仓同级：`gateway/` + 子包 `sandbox/`、`winproc/`，零本仓依赖）；**适配胶水层 = `src/lib/gwadapter/`**（`mqadapter/` + `facadeapi/` + `internal/facade/`，经 `require github.com/lsong98sh/chonkpilot-mcp-gateway v0.1.0` 引用内核 —— 2026-10-22 起不再 `replace` 本地目录，见 [10 §3](../10-architecture/10-分层与依赖.md)）；**exe 外壳 = `src/others/mcp-gateway/`**（`main.go` + `gateway_service.go`；跨 module 经 `facadeapi` 转发 `internal/facade`，保持 internal 隔离）

---

## 0. 现状订正（2026-10-21，GATEWAY-2 L5 / L6 / L7 / L8）

> 本节为准，**与下文冲突时以本节为准**。L5「纯化」后 gateway **只持四件事**：

| 组件 | 载体 | 职责 |
|------|------|------|
| **Contracts** | `registry.go` / `toolschema.go` / `serverslist.go` | 下游条目、工具暴露名 / 前缀 / 覆盖表、`_meta` 归一 |
| **Policy** | `purpose.go`·`subjects.go`·`breaker.go`·`tooloverride.go`·`primitives.go` | 调用准入（`tool.before_call` 钩子）、熔断、异步覆盖、统一 `{status}` 结果封装 |
| **Dispatcher** | `provider.go` / `registered_provider.go` | 三类下游 provider（`stdio` 自持子进程 / `proxied` 远程 http·sse / `registered` 注册回调）+ `_meta` 注入 |
| **TaskLayer** | `tasks.go` / `execsink` | 执行四态（同步 / 异步 / pending / awaiting）+ 取消 + 完成回报（**只发 server**） |

- **① 零工具实现（L5）**：`Params.MCPServer`、self 内存节点（`selfnode.go`）、dir 目录节点（`dirnode.go`）、`ContractScanner`、资产注册表（`mcp_find/mcp_load` 的目录资产面）、`llmToolPick` **全部删除**。`Params` 现为 `Transport` / `Servers` / `NsPrefix` / `CallTimeout` / `CBThreshold` / `CBCooldown` / `MaxTasks` / `AsyncMode` / `ManageAddr` / `ExecSink` / `ProgressHandler` / `ElicitHandler` / `Logf`（无 `MCPServer` / 无 `MCPConfig`）。
- **② 内置与第三方一视同仁（L5）**：全部下游都是**普通 provider**；`Origin=builtin` 仅决定是否注入 `_meta["chonkpilot"]`，**不再有 `builtin` 分支**。`tools/register` 的注册工具经 `registeredProvider` 参与路由（暴露名仍是 `self_<原名>`，**对外零变更**）。
- **③ 池化（L6）**：`procSlot`（进程，仅 stdio）与 `connSlot`（连接，http/sse）**拆分**；池键 `pool_key = shared | call`、`pool_size`、`max_concurrent`（缺省 **5**）、`call_timeout`、`grace_ms`（缺省 **2000**）、`kill_policy`（固定 **cascade-report**）由 `ServerEntry` 下发；`inFlight` 升级为 **`call_id → inflight`** 注册表（可查询单次调用是否结束、连带影响范围）。**stdio 不跨 workdir 复用**（沙箱按 workdir 打标）。
- **④ 取消阶梯（L7）**：`CancelCall`（`provider.go`）＝ **L0 协议取消 → L1 宽限 `grace_ms` → L2 关连接/会话 → L3 杀进程树**；远程（proxied）无进程可杀 → **L3 回退 L2 断连**；同槽在飞调用按 **`cascade-report`** 连带终止（`ErrKindSlotKilled` + `Cascaded []call_id`）。详见 [18 §3.9](../10-architecture/18-工具异步超时与取消.md)。
- **⑤ 通知路由（L8）**：下游 `progress` / `elicitation` 按 **`progressToken`（= `call_id`）** 归位到具体调用（`Params.ProgressHandler` / `ElicitHandler`）；未注入 = progress 丢弃 + warn、elicitation 不注册 handler（客户端不声明该能力）。
- **⑥ 沙箱（L7）**：仅 `stdio` 的 spawn 生效，`ServerEntry.Sandbox` / `SandboxDirs`（`prj security-*` 快照，类型 = `[]SandboxRule`）→ **本仓 `sandbox` 子包**（`src/lib/gateway/sandbox`，2026-10-21 随 gateway 从 `core/sandbox` 迁入；Windows **Low IL + Job Object**，只限写，允许目录须 `EnsureLowIL` 打标）。`SandboxRule` 类型与 `WritableRoots` 归 gateway 包；**配置的读取与持有仍在宿主**（llm/server 解析 `security-*` 后填入）。

---

## 1. 职责与边界

- **一句话**：**工具唯一入口**——聚合 self 能力 + 下游 server，做路由/前缀别名/scope 隔离/异步任务/熔断，并对外提供官方 MCP 门面（stdio/http/sse）与 `mcp-*` 消息方法面。
- **做**：19 个方法面（四原语 + servers/* + gateway/*；`tasks/*` 已移除，见 §3.1）、三种下游传输（stdio/http/sse）、dir 节点 / self 内存节点、工具路由与 hot、异步四态、熔断。
- **不做**：不 spawn executor（executor 只被 mcp-server 的 `callTool` spawn）；不感知 session/turn 语义（仅作执行上下文参数）。

---

## 2. 形态与部署

| 项 | 值 |
|----|----|
| 形态 | lib + **独立 exe**（`build-mcp-gateway.ps1`） |
| 参数 | `-transport stdio\|http://<addr>\|sse://<addr>`（默认 stdio）· `-service install\|remove\|run` · `-servers-file` · `-capability` · `-config` · `-call-timeout`(60) · `-max-tasks`(8) |
| 服务形态 | install 缺省 `--transport=http://127.0.0.1:5556`；服务名 `src/lib/gateway`；日志 `winlog` |
| 端点 | http：`/mcp` 与 `/`；sse：`/sse` 与 `/`（facade 门面） |
| 构建 | `build-mcp-gateway.ps1`（源 = `dist/mcp-server/capability`） |

`Params`：`Transport`(必填；上游通道端口 = 出站 `Call` + 入站 `Subscribe`，mq 实现 = `mqadapter.New(bus)`；MOD-1 M0.5 起内核零 `mq`/`msgkeys` 依赖) · **`Hooks`**（出站通知宿主回调 = `OnTaskReport` / `OnMCPChanged` / `OnToolsTimeout`；**主题名由宿主决定**，mq 实现 = `mqadapter.Hooks()`；`nil` = 该通知丢弃且 `Start` 打 Warn） · `MCPServer` · `MCPConfig` · `Servers` / `ServersFile` · `NsPrefix` · `CallTimeout`(60s) · `CBThreshold` / `CBCooldown`(30s) · `MaxTasks`(8) · `AsyncMode` · `ManageAddr` · `Logf`。

> **通知面归属（2026-10-21）**：内核**不再持有任何消息主题名**。`mcp-tasks-report` / `mcp-gateway-changed` / `mcp-tools-timeout` 三个出站通知改由 `Hooks` 表达语义事件，「发到哪个主题」与序列化全部在宿主（`mqadapter.Hooks()`）；订阅侧（llm/server、facade）直接用 `msgkeys` 主题常量。`Dispatcher` 因此只保留 `Call`（`Publish` 已删除）。

> **接入源三类**（gateway 无 DB，全部由装配方传入或按参数读取）：
> ① **`MCPServer`（self）** = 装配方自建的 go-sdk server，能力来自 **capability 契约扫描**（含 mcp-tools，见 [21-llm-server §2.1](21-llm-server.md)、[25-mcp-server](25-mcp-server.md)）（装配链见 §4.0）；
> ② **`Servers` 参数** = **宿主注入**的下游（llm 启动时把用户维护 MCP（usr `mcps` 表）转成 `ServerEntry` 传入；单体不读 exe 同目录 `config.json`。见 [21-llm-server §2.1](21-llm-server.md)、`src/lib/llm/server/gateway_servers.go`）；
> ③ **`ServersFile`**（`servers.list` 文件）= 部署形态来源；**无源（空路径 / 未给文件）→ 空列表（不报错）**，gateway 照常启动、工具面可为空（仅 self/meta）；**显式路径存在却读取失败、或内容非法 → 报错**（真错误，启动失败）。**无「内置默认」回退**（`defaultServersList` 已移除；无源不报错）。
> 运行时动态接入另走消息方法面 `servers/register`（§5.1.1，按 `scope` 隔离）。
> **core-mcp-server（2026-10-19）**：工具唯一前门由宿主拉起后，经 `servers/register` 登记为内置下游（`name=core`、`namespace=self_`、`origin=builtin`、`mcp_server={url,transport:"http",headers:{Authorization:"Bearer <token>"}}`），暴露名保持 `self_<tool>`；**core 暴露面 = 13 个**（app 根 5 + meta 3 + domain 5；codegraph/vfts 的 `internal` 工具不进 core 工具面，由插件按启用状态经 `tools/register` 注册）。取消/超时由 core 按 call 落实（该次请求 ctx 取消 → `KillTree` 本次 executor），gateway 不做进程级 kill。
>
> **纯远程（proxied）取消语义（2026-10-19 修正）**：`proxyProvider.Terminate` 对 `kind=proxied`（无自持子进程）为**协作式 no-op** —— 取消已由执行池 ctx 送达（MCP client 中止 HTTP 请求并发出 `notifications/cancelled`），对端进程可能继续运行 = 语义「尽力」；**不重建共享连接**。此前 proxied 走 `Invalidate`（断连 + `initialize` 握手）会在取消路径上同步阻塞 mq 同步派发线程（实测 30s ctx 打满 → `task-stop` / `/eval` 超时），并发取消还会把共享槽置空 → 后续调用报 `no transport connection`。`spawned`（自持子进程）仍按 `(instance, workdir)` kill + respawn。
>
> **`chonkpilot-server-tools.exe` 已撤销（2026-10-19）**：meta 3 / domain 5 改为一工具一 exe（`capability/executors/chonkpilot-{meta-*,ask-user,tool-result,tool-stop,subchat-*}-executor.exe`，契约 `core-only: true`），由 core 注册并经 `ExecTool.Call` 执行；server 侧 `tools.register/tools.call` 适配路径已删除。
>
> **来源标注与 `_meta` 内部上下文（R-11 边界）**：`ServerEntry.Origin` = `builtin`（本仓自有/内置）| `user`（用户定义/第三方），**缺省/未知 → `user`（安全默认）**。gateway 只向 `Origin=builtin` 的下游注入内部调用上下文 `_meta["chonkpilot"]{instance_id,work_dir,data_dir,session,turn,tool_call_id,purpose}`（`purpose` = 本次调用运行目的，C1 2026-10-15，见 §4.3）；`user` 一律不注入、也不产生 `CHONKPILOT_*`。判定**按来源（`IsBuiltin()`），与是否 in-process 无关**——内嵌 self/dir 节点经 `memNodeProvider`、standalone/独立部署的本仓下游经 `proxyProvider`，只要来源=builtin 均注入（`memNodeProvider.inject` / `proxyProvider.inject`）。标注点：self 节点、本仓 mcp-server、dir 节点 = `builtin`；usr `mcps`（第三方）= `user`；**`servers.list` 静态条目缺省 `builtin`**（部署方文件 → 本仓自有；第三方条目须显式 `<id>.origin=user`）；`servers/register` 动态接入按载荷 `origin`（缺省 user）。已废弃字段 `inject_context`（`servers.list` 键 + `mcp_server.inject_context`）已移除——注入判定统一由 `origin` 承载。

---

## 3. 对外接口

### 3.1 消息方法面（`mcp-*`，19 个，同主题请求-响应：`Handle` 返回结果 JSON）

> **说明**：`mcp-tasks-status|result|list|cancel` 与 `mcp-tools-background` **五个方法面已移除**（取消 / 转后台 / 状态 / 结果改由**任务层 + message 表 + 进程内 sink** 承担，见 §4.2 · §7）；**通知面 `mcp-tasks-report` 保留**。

| 组 | 方法 |
|----|------|
| tools | `tools/list` · `tools/call` · `tools/wait` · `tools/register` · `tools/unregister` |
| prompts | `prompts/list` · `prompts/get` · `prompts/register` · `prompts/unregister` |
| resources | `resources/list` · `resources/read` · `resources/register` · `resources/unregister` |
| servers | `servers/list` · `servers/get` · `servers/register` · `servers/unregister` |
| gateway | `gateway/check`（`{ok,tools,servers}`）· `gateway/reload`（只刷暴露缓存） |

主题 = `mcp-<组>-<动作>`（`/`→`-`）；`chonk.` 前缀由宿主 adapter（`mqadapter`）注入。
通知：`mcp-gateway-changed`（工具/资源/技能/提示词/agent/server 变化）· `mcp-tasks-report`（**只回报 server**；`result_summary` = 终态结果全文）。
管理 REST（`ManageAddr`）：`GET /mcp/list` · `/mcp/check`（`/mcp/tasks` 已删除）。
前端可经桥客户端主题 `tools-list`/`prompts-list`/`resources-list`（→ 相对主题 `mcp-tools-list`/`mcp-prompts-list`/`mcp-resources-list`）直取运行时能力面（见 [61-消息一览](../60-reference/61-消息一览.md) §4.5）。list 返回**全量**（含所有 instance），由**桥侧按实例过滤**后返回前端（仅 `scope=='' || scope==当前实例`）。

### 3.2 官方 MCP 门面（facade）

`internal/facade`：`Adapter` 持 `tools/prompts/resources` 三张已暴露表；`Start` 注册扩展方法 + 首轮同步 + 订阅 `mcp-gateway-changed` 差异同步。扩展方法经 `mcp.AddReceivingCustomMethod` 暴露（裸方法名：各 register/unregister、`servers/list|get`、`gateway/check|reload`；**`tasks/*` 已移除**），回包统一 `{data: <网关应答>}`。

---

## 4. 内部控制流

### 4.0 装配链（装配期 `New` → 启动期 `Start`）

> **结论（用户口径 4 步全部成立）**：① 装配方（单体 = `src/lib/llm` 的 `Server.New()`）用官方 go-sdk 建一个 **in-memory `mcp.Server` instance** → ② 对其执行 **capability 扫描 + 契约注册** → ③ 把**这个 instance 作为参数**交给 gateway 构建 → ④ gateway 在 **同一个 instance** 上注入 `mcp_find`/`mcp_load`/`mcp_invoke`。

**阶段 A · 装配期（`src/lib/llm/server/server.go:225-273`，仅在 `!opts.DisableMCP` 时执行）**：

| 步 | 事实 | 证据 |
|:--:|------|------|
| ① 建 instance | `root = opts.MCPServerRoot`，缺省 `<exeDir>/capability` | `server.go:228-235` |
| ① 建 instance | **`ms := mcp.NewServer(&mcp.Implementation{Name:"chonkpilot-server",Version:"1.0.0"}, nil)`** = 标准 go-sdk **in-memory server instance**（无传输、不 spawn） | `server.go:236` |
| ② 扫描 + 注册 | `s.mcpCfg = mcpms.DefaultConfig(); s.mcpCfg.Root = root` → **`mcpms.RegisterContracts(ms, root, s.mcpCfg)`**：扫描 capability 并把契约工具注册进**这个 `ms`**；失败仅打印告警、**空能力面继续**。**P6 外部模式**（`CHONKPILOT_EXTERNAL_TOOLS=1`）下 `SkipTools=true`：只注册 prompts/skills/resources，5 个可执行工具由 `chonkpilot-self-tools.exe` 经 `tools/register` 注入 | `server.go:239-243` |
| ② 留存 | `s.mcpServer = ms`（供运行期重扫/热生效） | `server.go:246` |
| ③ 交参数 | **`mcpgateway.New(mcpgateway.Params{Transport: mqadapter.New(bus), MCPServer: ms, Servers…})`** = 把该 instance 作为参数构建 gateway | `server.go:259-268` |

**阶段 B · 启动期（`Server.Start()` → `gw.Start()`）**：

- `Server.Start()` 中 `s.gw.Start(ctx)`（`src/lib/llm/server/server.go:286-290`）。
- `gw.New` **本身不建 server、不注入任何工具**：只保存 `params`、建 `regProv`/`reg`/`tm`（`gateway/mcpgateway.go:116-125`）。
- `gw.Start()`（`gateway/mcpgateway.go:129-137`）：
  1. `params.MCPServer != nil` → **`connectSelf()`**（`mcpgateway.go:178-204`）：`newMemNode("self", params.MCPServer)` 经官方 **`mcp.NewInMemoryTransports()`** 建同进程 client 会话（`gateway/memnode.go:25-41`）→ `ListTools` → `registerProvider` 成 self 节点（`selfKey = mem:global:self`，`Origin=builtin`）。
  2. 紧接着 **`registerMetaTools()`**（`mcpgateway.go:136`）→ **`g.params.MCPServer.AddTool(t, handler)`**（`gateway/meta_tools.go:70-77`）把 **3 个**工具注入**同一个 `ms`**：**`mcp_find` / `mcp_load` / `mcp_invoke`**（不是 2 个），`Meta = {"hot":true,"category":"meta"}`。
  3. 随后 `reconcileSelf()`（`meta_tools.go:80`）重拉 self list 并重建路由（`mcpgateway.go:206-220`），使注入工具进入 `tools/list` 与路由表。

**3 处必须记住的细节**：

1. **「装配」与「连接 + 注入」是两个阶段**：构建 gateway（`New`，仅参数交接）在**装配期**；连接 self 与注入 meta 工具在**启动期 `Start()`**。注入的是**同一个 `ms` 指针**（同一 instance 先 `Connect` 建会话、后 `AddTool` 追加工具、再 `reconcileSelf` 让新工具可见）。
2. **条件性**：`opts.DisableMCP` 为真 → 整段跳过（不建 instance、不建 gateway）；`Params.MCPServer == nil` → gateway **跳过 self 节点与 meta 工具注入**（`meta_tools.go:47-50`，注释明写「无 MCPServer 时跳过（**不自建**）」），此时工具面仅来自下游 `Servers`/`ServersFile`。
3. **例外：dir 目录节点是「gateway 自建 server」的另一条路**（不违反上条）——`@mcp` 等 dir 节点由 **gateway 自建**官方 server + `RegisterContracts` 扫描（`gateway/dirnode.go:25-41`：`mcp.NewServer(&mcp.Implementation{Name:"chonkpilot-gateway-dir"…})` → `ckmcpServer.RegisterContracts(ms, dir, cfg)`；注释见 `gateway/memnode.go:1-5`）。**准确表述**：**capability 主源 = 装配方传入的 instance**；**dir 节点 = gateway 自建**。不得写成「gateway 从不建 server」。

> **进程外模式（2026-10-19；BRIDGE-2 T7 ①）**：`CHONKPILOT_EXTERNAL_TOOLS=1`（桌面单体 / server 形态）时 **`registerMetaTools()` 直接返回**（不 `AddTool`、不 `reconcileSelf`）——`mcp_find` / `mcp_load` / `mcp_invoke` 改由 **`chonkpilot-server-tools.exe`** 经 `plugin/v1/tools.register{handler_subject:"meta-tool-call-ext"}` 注册，契约在 `src/lib/llm/cmd/server-tools/contracts/meta/`。进程内（单测 / CLI）仍走本节链路。见 [21 §3](21-llm-server.md)。

**由此推出的一条既有结论的「原因」（与 [18 §7 B5](../10-architecture/18-工具异步超时与取消.md) 呼应）**：进程内模式下 `mcp_find`/`mcp_load`/`mcp_invoke` 是 gateway 用 `AddTool` **注入到「装配方传入的 `ms`」里的本地 handler** →

- (a) **它们没有契约 md** —— `registerMetaTools` 注入时**硬编码 `Meta{hot:true, category:"meta", async:"never", timeout:0}`**（`timeout:0` = 无上限），`_meta.async` 随之透出；[18 §3.7 B](../10-architecture/18-工具异步超时与取消.md) 的「类① 只能同步」约束以**方案 B 落地**（`explicitNever` 锁死，调用级不可覆盖）。
- (b) 它们的 handler 在 **self 节点 server 侧（同进程）** 执行，gateway 调用要走 in-memory client 会话（`memNode.Call`，`gateway/memnode.go:61-63`）。**取消经 ctx 下沉可达 handler**（in-memory 传输的 `notifications/cancelled` 会取消请求 ctx，[18 §3.7](../10-architecture/18-工具异步超时与取消.md) 探针证「能传递」）；节点侧 `Terminate` 为**协作式 no-op**（`memnode.go`，真停在执行体侧；类① 取消由工具自身管理）。

### 4.1 启动

```text
New（默认值兜底；tm / instanceTracker）
Start：
  connectSelf（memNode("self", MCPServer)，selfKey=mem:self，scope=global，breaker disabled）
  registerMetaTools（3 个：mcp_find / mcp_load / mcp_invoke，AddTool 注入 Params.MCPServer 同一 instance，均 _meta.hot=true；详见 §4.0）
  # mcp_find 查 tool/skill/resource（type 收敛为 tool|skill|resource|all、**去 prompt/agent**，见 §4.3）：purpose 走 LLM 语义推荐（对所有 type 生效，候选按 type 收敛）
  # （agent 退出资产面 → "子 agent 发现"改由**场景层团队列表注入**承担，`mcp_find(type=agent)` 不再支持，见 §4.3）
  接入列表（Servers 优先，否则 loadServersList）→ 逐条 connectServer（disabled 跳过；失败记日志继续）
  subscribeAll（19 个方法经 Transport.Subscribe 登记；`tasks/*` 已移除）
  管理 REST
Stop：置 stopped → 退订方法面 → 关闭各 provider（spawned 收尾）→ 关管理 REST（**不关闭宿主通道**）
```

- **订阅关停短路（`subscribeAll`）**：`Stop` 置 `stopped` 后**拒绝新订阅**（返回 `gateway: stopped, refusing new subscription`）；保存订阅句柄时**双检 `stopped`**——注册期间 `Stop` 已并发退订完毕 → **立即退订并拒绝**（避免句柄泄漏、停服后仍接收/派发方法调用）。

### 4.2 工具调用与异步（`doCall`）

```text
入参归一：async/_async、timeout/_timeout 取出并从 args 删除
  → 调用展示名剥离：**剥离前**取值写入 req.Context.Purpose（C1 注入通道，见 §4.3），
    再 delete(args, `tool_call_display_name`)——gateway 注入的展示参数，不传下游（**剥离逻辑不变**）
    （**不叫 purpose**：避免与工具自有 purpose 冲突，如 mcp_find 的任务目标语义推荐）
熔断 ps.cb.allow()（open 且未过冷却 → -32601 "circuit open"）
  → 模式解析：契约 _meta.async（缺省 auto）→ Params.AsyncMode 全局覆盖 → 调用级 async/_async 覆盖（never 不可被覆盖）
  → 有效超时：**工具 `_meta.timeout` 显式「无上限」（`0`/`-1`）绝对优先、不可被覆盖**；否则 调用级(`>0`) > 条目 TimeoutSec(`>0`) > 工具 meta timeout(`>0`) > 全局；threshold 缺省 = timeout
  → always 立即转后台；auto 超阈值转后台；**never / manual 到点 → 发 `mcp-tools-timeout` 交用户裁决**（never 可选 `tools/wait` 继续等，manual 无此选项）
  → 配置⑤ 超时自动取消（usr `tool_async.<暴露名>.cancel_on_timeout > 0`）：never / manual 到点**直接取消**（`tm.cancelRef` → `onTaskCancel` 按执行线终止），**不发 `mcp-tools-timeout`、不等裁决**；**默认 0 = 不取消**；auto 语义不变（阈值命中仍转后台）
  → `never` / `manual` 超时**均不失败**，任务保持 `running` 待裁决
  → **例外：工具 `_meta.timeout` 显式 `0` / `-1` = 无上限** → **不设裁决点、不发 `mcp-tools-timeout`**，直接阻塞等待至完成或**用户取消**；且**调用级 / server 级 / 全局超时均不得覆盖**（「无上限」必须是绝对的，[18 §3.8](../10-architecture/18-工具异步超时与取消.md)）
  → 后台：执行池（`execPool`，原 `TaskManager`）goroutine（pending→running→done|error|cancelled）→ 完成回报 mcp-tasks-report
```

> **说明**：上文 `pending→running→done|error|cancelled` 是 **gateway 内部 goroutine 执行态**（`execPool` 内存池，随进程生灭），**不等于 Task 层的权威状态**。**控制面以层（`src/lib/task`）为权威**：层 `state` 共 **8 态**——除 `pending`/`running`/`done`/`error`/`cancelled`/`interrupted` 外，另含 **执行态** `detached`（已转后台）/ `awaiting`（超时到裁决点、等用户选择）；且**状态优先级 = 终态 > 执行态(`detached`/`awaiting`) > 粗粒度(`pending`/`running`)**，`exec_json` **只增不减（深合并）**。gateway 的 4 类执行态（`started`/`detached`/`awaiting`/`error`）经 **层内 API**上报落库（`ExecSink.OnExecState`，不新增 MQ 主题）。**状态总表（8 态 + `closed` 标记 + 执行态映射 + 优先级）见 [21 §5.3](../10-architecture/21-任务层设计方案.md)**。

- **取消后等待的兜底上限**：配置⑤ 超时自动取消（`cancel_on_timeout > 0`）路径中，`ep.cancel` 已置终态并 `markDone`（`doneCh` 立即关闭）→ 正常**零等待**；执行体极端不响应取消时，**`cancelDeliverWait`（5s）兜底超时后按 `cancelled` 交付**，避免永久占用 mq 同步派发线程。

- **调用上下文透传边界（R-11，按来源判定）**：`doCall` 的 `req.Context`（`instance_id`/`work_dir`/`data_dir`/`session`/`turn`/`tool_call_id`/`purpose`）经 `withTurnContext` 注入执行 ctx。是否把上下文以 MCP `tools/call` 的 **`params._meta`**（命名空间 `chonkpilot`，`callContextMeta`）透传，**只看来源 `Origin`**：`builtin`（`memNodeProvider`：self/dir 本仓 in-memory 节点；`proxyProvider`：standalone 本仓下游）→ 透传；`user`（第三方，用户定义）→ **不携带任何 `_meta`/`CHONKPILOT_*`**。**不以是否 in-process 为界**；上下文仍**不注入 tool arguments**；gateway 自持工具/域工具的 AddTool 包装经 `execCtxFromMeta` 从 `req.Params.Meta` 恢复 ctx。见 [16 §8.4/§8.8](../10-architecture/16-路径解析规范.md)。

- **执行层定位（任务编排分层）**：gateway 在任务面**只承担单次 `tools/call` 的执行层** —— spawn / 阈值计时 / `autoDetach` / `enterAwaiting`（`mcp-tools-timeout`）/ `tools/wait` / **执行池取消（`CancelExec`）与转后台（`DetachExec`）** → `tm.cancel` / `tm.detach` → `onTaskCancel` → `Provider.Terminate`（**按执行线分派**：spawned = kill + respawn；in-memory = 协作式 no-op；纯远程 = 仅断请求），终态经 `mcp-tasks-report` 回报 llm（`tasks.go` 注释原文：「tasks.done 仅回报 server，不对前端广播——**任务编排提升 server，gateway 纯执行层**」）；**任务树/父子/级联/批量/任务事件由 llm-server 编排**（`src/lib/llm/server/tasks.go`，注释原文「**gateway 是纯执行层，不对前端广播任务事件**」）。故**不得表述为「所有控制都在 gateway」**（应限定为**单次调用执行层**）。详见 [18 §3.6 A](../10-architecture/18-工具异步超时与取消.md)。

- **执行侧控制面**：取消 / 转后台的**传输面**从 MQ 方法面改为**进程内 sink** —— `ExecSink.CancelExec` / `ExecSink.DetachExec`（gateway 包内窄接口，装配方 `src/lib/llm/server` 实现并转调 gateway 公开方法 `(*Gateway).CancelExec` / `(*Gateway).DetachExec`，复用既有 `tm.cancelRef` / `tm.detach`；`CancelExec` 仍**先问层**：层判不可逆终态 → 拒绝）。语义与既有等价：取消 = 级联标终态 + `provider.Terminate` 真打断（含已 detached 后台任务）；转后台 = 解绑在飞调用 + 原同步调用返回 `pending{task_id}` + 终态走 `mcp-tasks-report`。**sink 未注入 / 层未就绪 → 调用方返回明确错误、不动作**（gateway 单体 exe 仍可独立运行，直接调公开方法）。**异步结果 / 状态不再由 gateway 提供**：结果读 message 表（`{call,result,async}`）、状态以任务层为准。

- **异步 / 超时 / 取消（已落地，详见 [18-工具异步超时与取消](../10-architecture/18-工具异步超时与取消.md)）**：gateway 是**唯一裁决点**（模式裁决 + 阈值/超时计时 + 裁决事件 + 取消编排）。取消**按 provider 执行线分派**（`onTaskCancel` → `Provider.Terminate`）：spawned/自持 = `InvalidateScoped`（kill + respawn）、in-memory（`memNode`/`registered`）= 协作式 no-op、纯远程 = 仅断请求；`execCtx` 经 `withTurnContext` 注入，其取消经 in-memory 传输送达执行体（§3.7 探针证「能传递」）→ 执行体侧真停（mcp-server `callTool` + `CommandContext`）。
  - **工具分类与异步模式约束** —— 分类按「**执行线**」共 **10 类**（**归属按提供方、处理按执行线**：类① gateway 自有 `mcp_find`/`mcp_load`/`mcp_invoke` · 类② `llm_run` · 类③ `tool_stop`/`tool_result` · 类④ `ask_user` · 类⑤ mcp-server 执行器 · 类⑥ stdio · 类⑦ http·spawned · 类⑧ http·remote · 类⑨ sse·spawned · 类⑩ sse·remote）；模式约束 = **类①③④ 仅同步 / 类② 仅异步 / 类⑤–⑩ 四档全支持**；配置⑤（超时自动取消 `cancel_on_timeout`，usr 键）**类①③④ 不定义**、**类⑤⑥⑦⑨ 支持**、**类⑧/⑩ 降级「尽力」**；组合数重算 = 10 类 × 5 配置 = 50 → **有效 34（按「不支持」口径 32）· 可用 28 · 缺失 6**。细节见 [18 §3.1/§3.2/§4](../10-architecture/18-工具异步超时与取消.md)。

### 4.3 路由与隔离（`registry.go`）
- `toolRoute{Name, Original, Provider, Tool, Hot, Scope}`；`routeKey = scope+"|"+name`。
- `route.Hot` 来源 = 工具 `_meta.hot` 真值（`registry.go` `isHot`：`ServerEntry.HotTools` 含 `*` = 全部 hot / 含原名 = 单工具 hot；写入工具 `_meta.hot`）。
- 暴露名 = alias 优先，否则前缀 `applyPrefix`（namespace `-` 禁前缀 / 空 → `<id>_` / 否则 `<ns>`）；dir 节点 = `NsPrefix + 原名`。
- **同名注册报错**（按 `(scope, 暴露名)`）；global 与各 instance scoped 为独立名字空间。
- `findFor`：instance scoped 命中**遮蔽** global，未命中回退 global；`allToolsFor` = global ∪ 归属 instance。
- 目录资产 `catalogAsset` 按 `(scope, kind, name)` 唯一；字段含 `Path`（运行时内容来源路径）与 `Content`（**兼容兜底**，仅无 `path` 的资产驻留）——**有 `path` → 内容按需读盘**（`assetContent()`，`registry.go:118-147`；`prompts/get` · `mcp_load` 同源）。`prompts/register`（`{name, description, arguments?, content?, path?, asset_kind?}`）/ `resources/register`（`{name, uri?, description?, mimetype?, content?, path?}`）的 payload **以 [61 §5.1.1](../60-reference/61-消息一览.md) 为准**（本行仅为描述对齐，不改 61）。
- `_meta` 组装（`tools/list`）：工具自身契约/注册 meta（`title/category/hot/async/…`）为基底，gateway 追加 `server`；写入**副本**，不污染 provider 侧工具对象。（`find`/`version` 死 meta 已删除。）
- **`_meta.server`**：`{alias, node, category, description[, url]}`（`registry.serverInfoOf`；alias = server 别名，url 仅 proxied/有端点时出现）。`tools/list` 每项 `_meta.server` 与 `mcp_find`/`mcp_load` 返回的 `server` 字段同源——UI/LLM 一眼识别工具归属哪台 server，不必再查 `servers/get`。
- **`mcp_find`/`mcp_load` 对 tool 必带 `inputSchema`（参数定义）+ `provider` + `server`**：仅 `_meta.hot=true` 的工具进 LLM 的 `tools` 参数，非 hot 工具 LLM 无法直接 tool-call，只能经 `mcp_invoke`（`mcp_invoke` 自身亦 `hot=true`）——故必须能从这里取到入参 schema 以组装 `arguments`。
- **`mcp_load` 对 `resource` 的内容承载**（2026-10-14）：经 `ReadResource` **实时**读取——内容 = 资源「正文」（markdown）或 meta `path` 指向的**外部内容文件**；**文本经 `content`、二进制经 `blob`（base64，对齐 MCP）**，`mimetype` 按文件后缀识别（见 [25 §3.1](25-mcp-server.md)）。
- **meta 工具 hot**：self 节点 `entry` 无 `HotTools`，故 `isHot` 不会自动补；`registerMetaTools` 显式置 `_meta.hot=true`（否则 LLM 连工具发现入口都拿不到）。
- **`mcp_find` 检索语义**：
  - `query`/`purpose` **均省略 → 返回该类型全部条目**（受 `limit`，默认 20）；schema 已去掉 `required:["query"]`（原实现缺参报错 `query or purpose is required`）。`type` 收敛为 `tool|skill|resource|all`（**去 `prompt`/`agent`**）→ `type=agent` 不再支持（agent 只"注入"不"注册"，原 server 域工具 `agent_list` 注册已移除）；`mcp_load` 的 `kind` 同步为 `tool|skill|resource`；prompt 移出 LLM 检索面。见 [25 §5](../10-architecture/25-MCP与场景分层模型.md)。
  - `purpose`（任务目标）→ **LLM 语义推荐，对*所有* `type` 生效**（非仅 tool/all）：候选清单按 `type` 收敛（`findTypeScope`，口径与本地检索一致），交无上下文单轮 LLM mq `llm-simple` 推荐（可返回组合，候选收敛为 **tool/skill/resource**（去 agent/prompt），见上条）；LLM 不可用（无订阅/超时/未知 type）→ 降级把 `purpose` 当关键词走本地。
  - `query` → 本地关键词检索（name/description 子串；工具按 `type`/category 过滤，资产按 kind）。
- **调用展示名参数的注入与剥离**：`injectDisplayName` 给**每个**工具注入展示参数 **`tool_call_display_name`**（≤20 字符，UI/CLI 展示调用名；写入 `properties` + `required`，除非契约已定义同名）；`doCall` 执行前**统一剥离**，不传下游工具。
  - **为何不叫 `purpose`**：`purpose` 是工具可自有的语义参数（`mcp_find` 的 `purpose` = 任务目标，驱动 LLM 语义推荐）。旧实现把注入参数命名为 `purpose`，导致 `doCall` 的剥离把 `mcp_find` 的语义参数一并吞掉；且旧实现需 `_meta.purpose_injected` 标记来区分两者。**更名后无需标记**（不再有名字冲突），剥离逻辑恢复为无条件。
  - **消费方与语义**：server `tasks.go taskDisplay`（任务节点 `name`/`simplified` 取该参数值；事件字段仍为 `purpose`/`simplified`，属消息面不变）。该参数**即 purpose（运行目的）**——代表本次工具调用/子 LLM 的目的，并**直接充当 tasktree 节点 label**；参数名不叫 `purpose` 的原因见上条（避免与工具自有 `purpose` 冲突）。
  - **C1 · 运行目的（`purpose`）注入通道（2026-10-15 · SUBCHAT-1）**：展示名 = 本次调用的**运行目的**（`purpose`）；原被剥离后下游任何 handler 都取不到（唯一消费方 = server `tasks.go taskDisplay` 的任务树 label，且单聊不进任务树 → 该消费方不存在）。故 `doCall` **在剥离前**取值写入调用上下文 `req.Context.Purpose`（`gateway/mcpgateway.go:856-865`；**`delete(args, …)` 剥离逻辑保持不变**，注入的是「上下文」、剥离的是「工具参数」，二者不冲突），随后经**三条通道**下发：
    - ① **进程内**（gateway 自持 handler）：`withTurnContext` → `ctxKeyPurpose` → 工具侧 `PurposeFromContext(ctx)`（`gateway/provider.go:843` · `:874-876` · `:906-908`）；
    - ② **域工具**（bus 回调）：`turnContextPayload` 增 `purpose` 键 → handler 读 `payload["context"]["purpose"]`（`gateway/registered_provider.go:111-132`）；
    - ③ **进程外**（spawn executor）：协议 `_meta.chonkpilot.purpose` 写入 / 还原（`gateway/memnode.go:186-190` · `:232-234`）→ mcp-server 解析（`server/server.go:31-56` · `:66-79`）→ spawn 时落环境变量 **`CHONKPILOT_PURPOSE`**（`server/config.go:659`；executor 侧 `mcp-tools/internal/fileops/toolenv.go:27` · `:59`）。
    - **空值不注入**：`purpose` 为空 → 不写 ctx key / payload 键 / `_meta` 键 / env（既有无 purpose 形态逐字节不变）；`Origin=user` 第三方仍不带 `_meta`/`CHONKPILOT_*`（§4.2 边界不变）。
    - **域工具侧消费（已闭环，SUBCHAT-2 阶段 A，2026-10-16）**：`llm/server/domainmcp.go` 的 `domainToolMsg.Context` 增 `purpose` 字段（`registered_provider.turnContextPayload` 写入）→ `onDomainToolCall` 取出后经 `domainExecute(..., purpose)` 送达 handler；本阶段用于 `domainNode` 对域工具任务节的 `purpose` 兜底（args 里的展示名已被 `doCall` 剥离），后续 `subchat_open` 等 handler 直接消费该参数。

### 4.3.1 命名与唯一性（in-memory 注册名）

- **in-memory 注册表内名字必须唯一**——同 `(scope, 暴露名)`（tool/路由）与同 `(scope, kind, name)`（资产）**重复注册一律拒绝并返回含来源的明确错误，不得静默覆盖**。约束落在**全部注册入口**：`registerProvider`（self/dir/下游工具）、`addDirRef`（dir 引用）、`putAsset`（skill/resource 资产）、`registeredProvider.RegisterTool`（域工具）、`registerServer`（server 接入 = `servers/register`）。
- **同类条目不重名**：**场景 / skill / resource / tool 一律不允许重名**（场景 id 全局唯一见 [25 §6](../10-architecture/25-MCP与场景分层模型.md)）；**agent 是唯一允许重名者**，其消歧见 [25 §6.1](../10-architecture/25-MCP与场景分层模型.md)（不在本面注册资产）。
- **第三方（下游 server，`Origin=user`）条目一律加别名（来源前缀）**：暴露名 = `entry.Aliases[原名]` 优先，否则 `applyPrefix`（namespace 覆盖默认；缺省 `<id>_`）；**即使显式 `namespace "-"` 也不得去前缀** → 强制回落 `<id>_`（`entrySourcePrefix`），以保证与内置 `self_*`、知识库条目**不撞名**。
- **名字往返一致**：`mcp_find` / `mcp_load` 返回的 `name` = `tools/call` / `mcp_invoke` 可直接使用的名字（同一暴露名口径，别名后亦然）。

### 4.4 下游传输（`provider.go`）

| transport | 判定 | 实现 |
|-----------|------|------|
| `stdio` | 仅 `runtime` | `exec.Command(runtime, args...)`（`spawnArgv`，逐个 exec 参数、**不做 shell/引号解析**；**`cwd` 非空 → `proc.Dir`**）+ `mcp.CommandTransport`（env 经 `processEnv` 追加） |
| `http` | URL 非空 | 官方 **`StreamableClientTransport`**（2025-03-26 规范）；headers 经 `headerRoundTripper` |
| `sse` | URL 非空 | 官方 **`SSEClientTransport`**（2024-11-05 旧式 SSE：GET 常连 + endpoint 事件 + POST 上行）——**与 `http` 必须各用其 transport** |
| `inprocess://` | 推断层识别 | ⚠️ **switch 无 case → 报 `unknown transport`**；同进程能力实由 `memNode` 承担；**已决删除该分支（见 §9）** |

- **显式 `http`/`sse` 无 `url` → 前置报错、不 spawn**（`transport %q needs url`）。
- **spawned 子进程 `env`**：基线 `os.Environ()` + `entry.Env`（`K=V`），值经 `expandVars` 展开 **`%VAR%` 与 `${VAR}`**（与 `servers.list` 字段同一展开函数；**未定义变量 → 空串**）。
- **agentbox 沙箱下发（仅 stdio）**：`ServerEntry` 增 `Sandbox *bool`（旧 usr `mcps[].sandbox`，三态：nil/false = 不隔离；**2026-10-12 起仅作「兼容写入口 + 读取时迁移」——开关来源 = usr `tool_sandbox.mcps[<mcp 名>]`，`connectServer` 见 `Sandbox==true` 经 `mergeLegacySandboxMCP` 并入 `tool_sandbox.mcps`**）与 `SandboxDirs []agentbox.Rule`（装配层按在线实例 `security-*` 并集注入的快照）；`buildConn` 的 **stdio** 分支额外追加环境变量 **`CHONKPILOT_SANDBOX`**（值 = `SandboxPolicyJSON()`，判据 = **该 MCP 在 `tool_sandbox.mcps` 中开启 + 传输 stdio + 允许目录非空**；http/sse **不施加**）。**上游进程是否遵守取决于其是否实现 agentbox 消费方**——本仓进程（mcp-server/executor）已实现；第三方进程**仅透传，不构成强制**。空允许目录 → **不下发** + 记日志（避免把上游打成全拒）。**与 `isolate`（连接池按 `(instance, workdir)` 隔离）是两条独立轴**。见 [14 §7.1](../10-architecture/14-安全域-agentbox.md) · [64 §6](../60-reference/64-配置项一览.md)。
- `needsSpawn = runtime != "" && (http|sse)`：spawn 进程 → `waitEndpointReady`（TCP 轮询 + 子进程提前退出检测）→ 连接。
> 实现细节见 `provider.go buildConn`。

- **每 server 单连接（共享槽）；`isolate=true` 时按 workdir 池化**：
  - **共享槽**（`p.shared`）：恒建、承担**控制面**（首连校验 / `ListTools` / 无 workdir 回落 / `Invalidate` 原地换新）。`isolate=false`（http/sse 缺省）→ **全 server 仅此一条连接**（与既有行为一致）。
  - **隔离槽**（`p.pool[workdir]`）：`isolate=true`（stdio 缺省）时**按 workdir 一槽**（各自管道 / 自持子进程，互不阻塞）。**key 来源 = 调用 ctx 的 `work_dir`**（`withTurnContext` → `ctxKeyWorkDir`，`WorkDirFromContext`；**零新增主题 / 零新增 payload**）。
  - **懒建与并发**：`slotFor` 首调懒建（`buildSlot(entry, workdir)` + `connect`）；**同池键并发首调由 per-key single-flight 表 `p.building`（`poolKey → *buildFlight`，`buildFlight.done` 为完成通道）保证仅一次建连**——首调者在 `p.mu` 内登记 `buildFlight` 后于锁外建连，其余同键调用者 `<-done` 等待其结果并复用入池槽（**不**各自 build，避免重复 spawn 造成 isolate 固定端口冲突）；**建连（spawn + 握手，可达数秒）在 `p.mu` **之外**进行**，不同池键并发建连互不阻塞。入池再于 `p.mu` 短临界区**双检**（`closing` → 关闭新槽、置 `flight.err`、回落共享槽；他方已抢先入池同键 → 关闭本次新建槽、复用在池槽）；`buildFlight` 的「移出 `building` + `close(done)`」在 `p.mu` 内**原子**完成（避免新来者看到空档重复建连）。**不同 workdir / instance 各自建连、互不阻塞**。建连 / 握手失败 → 置 `flight.err` + warn 并**回落共享槽**（保持调用可用，如 spawned http/sse 固定端口无法按 workdir 重复拉起时退化为共享）。
  - **缺 workdir**：`isolate=true` 但调用上下文无 `work_dir` → **回落共享槽 + 一次 warn**（`noWd` CAS，不报错）。
  - **空闲回收**：`sweepIdle` goroutine（仅 `isolate=true` 启动）按 TTL 回收空闲 workdir 槽——TTL **复用既有 `Params.CallTimeout`**（`newProxyProvider` 的 `callTimeout`，≤0 → 60s；**不新增配置键**），扫描间隔 = TTL/2（clamp `[100ms, 30s]`）；`reclaimIdle` 在 `mu` 内与「查池占用」互斥（**在飞调用 `inFlight>0` 的槽绝不回收**），空闲 ≥ TTL → 关闭并移出池（下次调用懒建）。
  - **作废覆盖全池**：`Invalidate`（作废 + 恒重建）/ `Close` 均先 `closePooled()`（关闭并移出池中**全部** workdir 槽）再处理共享槽；`watchPooled` 观测池化自持子进程（意外退出 → 移除出池 + warn，下次懒建）。
  - **作废/重建的串行、短路与单调代际（C-52）**：`Invalidate` 持 **`rebuildMu`** 串行化「作废 + 重建」（并发进入会各自 spawn、后写 `shared` 覆盖前者 → 前一子进程失句柄成孤儿）；**已 `Close` / `Stop`（`stopCh` 已关）→ `Invalidate` 短路**（返回 `provider closed`，不再 spawn/重连，避免 `Stop` 之后 respawn 出无人观测的孤儿子进程）；每次重建经 `genSeq` 递增**单调代际**，**重建/重连失败回落亦保留单调代际**（不复用零值 0）——`monitorSpawned` 以「捕获代际 ≠ 当前共享槽代际」判定某次退出是否已被重建路径认领（置 0 会让空槽与任意代际无法区分 → 代际误判、多一次假重建）。
  - 判定入口 = `ServerEntry.IsolateEnabled()`（`serverslist.go:76-81`：**显式 `Isolate` 优先；缺省按 transport 推断——stdio→true（单管道无法并发处理）、http/sse→false**）；`isolate` 配置键见 [64 §6](../60-reference/64-配置项一览.md)，注册载荷见 [61 §5.1.1](../60-reference/61-消息一览.md)。

### 4.5 熔断（`breaker.go`）

`threshold=5` / `cooldown=30s` / `disabled` 豁免（`ServerEntry.CBDisabled`）。状态机 `closed → open → half-open → closed`。

---

## 5. 数据结构与存储

- 无持久存储（纯运行态）：`reg`（提供方/路由/资产表）、`tm`（任务表）、`nodes`（dir 节点）、`self`（内存节点）。
- 执行池（`execPool`，原 `TaskManager`）上限 `MaxTasks`（默认 8）。
- 接入配置：`servers.list` 分组式（`mcp.alias=<id>` 开始，字段 `<id>.<field>`；支持 `%VAR%`/`${ENV}` 展开）+ `ServerEntry` 全字段。

---

## 6. 依赖

- **上游**：`src/lib`(mq/winsvc/winlog/exedir) · `src/lib/mcp-server`（`RegisterContracts`，用于 dir 节点扫描） · 官方 go-sdk。
- **下游**：server（会话层经 `gwClient` 调用）、独立 MCP 客户端。

---

## 7. 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| 单一 `mcp-*` 主题域 | 方法名 ↔ 相对主题一一对应；HTTP 侧统一由 facade 暴露于 `/mcp` | 统一、可映射 |
| 同主题 promise | 回执 = 同一次派发 `v.Result`，无 `-reply`/`req_id` | 与 MQ 门面一致 |
| self 走 memNode | 同进程用 `mcp.NewInMemoryTransports` | 零 HTTP；`inprocess://` 分支实际未接线 |
| scope 隔离 | `(scope,name)` 独立名字空间，scoped 遮蔽 global | instance 级注册不污染全局 |
| 完成回报只发 server | `mcp-tasks-report` 订阅方仅 server | 终态单源 |
| 熔断豁免 | `ServerEntry.CBDisabled` | 本地/受信下游无需熔断 |

---

## 8. 场景与边界

- 同名注册冲突 → 返回含来源的错误。
- 下游启动失败 → 仅记日志继续（不阻断网关）。
- 工具并发超 `MaxTasks` → 返回"并发任务数已达上限"。
- 终态任务不可取消。
- disabled 条目跳过接入。

---

## 9. 现状与待办

- ✅ **疑似死代码/未接线已清理完毕**（gateway 目录已无 `instanceTracker` / `TaskManager.{cancelByInstance,prune,reportProgress}` / `dirNodeCount` / `handleServersReload` / `handleRegister` / `handleUnregister` 等符号）。
- ✅ **工具异步 / 超时 / 取消统一议题已落地**（取消按执行线分派 `Provider.Terminate`、⑤ 超时自动取消 `cancel_on_timeout`、executor 真终止）——见 §4.2 末条与 [18-工具异步超时与取消](../10-architecture/18-工具异步超时与取消.md)（B1–B4 已实施）。
- ✅ **已决**：**删除 `inprocess://` 分支**——`newProxyProvider` 推断出 transport 但无 case → `unknown transport`；同进程能力一律走 `memNode`。待实施。
- ⚠️ heartbeat/instance-exit 不再消费（`subscribeAll` 不含）。
- 🔵 `callTimeout` 预留（不再设传输层超时）；**另复用为按 workdir 隔离槽的空闲回收 TTL**（`isolate=true`，见 §4.4；**不新增配置键**）。
- ✅ **MCP 下游按 workdir 隔离**：MCP 配置的**项目级 `isolate` 开关**（usr `mcps` 键 + `servers/register` 的 `mcp_server.isolate`，三态：未设置 → 按 transport 推断 stdio→true / http·sse→false）与**按 workdir 多连接池**（`pool[workdir]` + 每 workdir 建连锁 + 空闲 TTL 回收 + `Invalidate/Close` 覆盖全池 + 缺 workdir 回落共享）均已实现，见 §4.4。原「每 server 全局单管道、跨 workdir 串行」仅适用于 `isolate=false`。

---

## 10. 关联测试

`src/test/chonkpilot-mcp-gateway/unittest/gateway_test.go`（内存总线 + 自备能力源官方 server → New/Start → 总线驱动）：
`TestSyncCall` · `TestAsyncByMeta` · `TestAsyncByRequest` · `TestBackgroundManual` · `TestTaskReport` · `TestTaskCancel` · `TestTaskError` · `TestMCPFindAllNoQuery` · `TestMCPFindPurposeAllTypes`。

`src/lib/gateway/gateway/pool_test.go`（按 workdir 隔离连接池白盒）：
并发两 workdir → 两条独立管道且总耗时≈单次 · `isolate=false` → 同 pid（共享槽）· 空闲回收只回收对应 workdir · 缺 workdir 回落共享 + warn · `IsolateEnabled()` 真值表（显式优先 / stdio 缺省 true / http·sse 缺省 false）。

`src/lib/gateway/gateway/purpose_test.go`（C1 · 运行目的注入通道白盒，§4.3）：
`TestTurnContextPurpose`（进程内 `PurposeFromContext` + 域工具 `turnContextPayload["purpose"]`）· `TestExecCtxFromMetaPurpose`（协议 `_meta` 往返）· `TestDoCallInjectsPurposeBeforeStripping`（剥离前取值写入调用上下文，且 args 仍被剥离）。
配套（进程外通道）：`src/lib/mcp-server/server/config_test.go` 的 `TestCallContextFromMeta`（`_meta.purpose` 往返 / 空值不写键）与 `TestExecutorEnv`（`CHONKPILOT_PURPOSE` 注入 / 空值不注入）。

`src/test/chonkpilot-mcp-gateway/unittest/origin_test.go`（来源标注驱动 `_meta` 注入；下游 = httptest streamable HTTP MCP server 回显 `_meta`）：
`TestMetaInjectedByOrigin`（builtin 注入 / user 不注入 / 裸构造缺省=user）· `TestServersRegisterOrigin`（`servers/register` 缺省 user、显式 `origin:builtin` 注入、`mcp_server.origin` 亦可）· `TestServersListStaticOrigin`（`servers.list` 静态条目缺省 builtin、显式 `origin=user` 覆盖）· `TestOriginUnknownTreatedAsUser`。
