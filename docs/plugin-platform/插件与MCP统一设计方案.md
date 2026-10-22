# 插件与 MCP 统一设计方案（Plugin Platform v2）

> 状态：🔵 **设计稿 v2（2026-10-22 重新设计，待评审，未实现）** ｜ 待实施登记：[49 · PLUGIN-1](../spec/40-roadmap/49-待实现项.md)
> 本稿**整体替代** v1（2026-10 上旬）——v1 的 `managed-stdio` / JSON-Schema 表单 / `inproc` 插件三处方向已作废，原因见 §1.2。
> 前置事实：**PRODSIM-1 主体已完成（L1–L4，2026-10-22）** —— 唯一产物 `chonkpilot.exe`（WebView2 宿主 + 常驻 server），`gui` / `flash` / `browser` 形态与 `--plugin-http-*` 已删除，插件桥只保留 **UDS** 免认证前门（`transport` 仍支持显式 `tcp://`）。
> 关联：[28-plugins](../spec/20-modules/28-plugins.md) · [2C-UI插件](../spec/20-modules/2C-UI插件.md) · [25-mcp-server](../spec/20-modules/25-mcp-server.md) · [26-mcp-gateway](../spec/20-modules/26-mcp-gateway.md) · [27-mcp-tools](../spec/20-modules/27-mcp-tools.md) · [29-codegraph](../spec/20-modules/29-codegraph.md) · [2B-vfts](../spec/20-modules/2B-vfts.md) · [31-窗口与布局](../spec/30-function-points/31-窗口与布局.md) · [61-消息一览](../spec/60-reference/61-消息一览.md) · [71-编程规范 §3.3](../spec/70-conventions/71-编程规范.md)
> 代码面：`src/lib/llm/pluginbridge`（插件前门）· `src/lib/core/{transport,pluginclient}`（帧/UDS）· `src/lib/llm/pluginhost`（进程监管）· `src/lib/llm/server`（gateway 客户端/能力面）· `src/lib/gwadapter` + gateway 内核（`chonkpilot-mcp-gateway@v0.1.0`）· `src/frontend/src/{layout,layout-free,views/codeview}` · `src/lib/gui/{main.go,plugin_gui.go}` · `src/initdata/capability`

***

## 0. 结论速览

**一句话**：插件 = `capability/plugins/<id>/` 下的 **manifest + 可执行体 + 可选 webui**；宿主新增一个 `PluginManager` 负责发现 / 拉起 / 守护 / 注册；插件与宿主只走 **一条 UDS（可配 TCP）控制通道**；MCP 是插件**在该通道上向 gateway 登记的工具面**（插件内可自带 MCP server，但**不走 stdio**）；UI 是 manifest 声明的 **iframe view**，作为普通 view 参与布局、页签、抽屉、对话框与状态栏。

需求 → 设计落点：

| # | 你的口径 | 设计落点 |
|---|---------|---------|
| 1 | 插件是 server 启动后被 spawn 的 exe | §3.3 / §3.4：`PluginManager` 在 server 就绪、插件桥就绪之后按 manifest `runtime` spawn；退避重启；禁用即停 |
| 2 | MCP 按配置在 server 启动后注入 gateway | §5.7：沿用 `servers/register`（`mcp-servers-register`）注入；用户手写 `capability/mcps/*.json` 继续走既有对账 |
| 3 | 同一 exe 可含 MCP server；插件经 server 能力登记 MCP；**捆绑后不支持 stdio** | §5.7：`mcp.mode=http`（插件内 Streamable HTTP，回环 + Bearer）或 `mode=tools`（经通道直挂）；**显式禁 stdio**，理由见 §5.7.1 |
| 4 | vfts / codebase 可合二为一 | §9.1：`codebase` 单插件 = 单 exe（内嵌索引器 + MCP server + 9 工具） |
| 5-1 | 插件可注册配置页面 | §4.2 `ui.views[]` + §7.3 预览区 iframe 页 |
| 5-2 | server 维护「插件」一栏，view 展示插件；点配置 → preview 区 iframe | §7.4 插件管理页（`plugin-manager`）+ `preview-tab-open` 打开插件页 |
| 5-3 | 插件状态页经 statusbar 按钮 / 命令显示 | §7.5 状态栏条目注入（`ui.statusbar[]`） |
| 5-4 | UILayout 支持 iframe 页签 / 抽屉 / 对话框；**更优 = 注册 iframe view** | §7.1 评估：**采纳「注册 view」**（机制层零改动），预览区另设专用宿主；不再新增三条开 iframe 的命令 |
| 5-5 | 插件管理页可查看 view、可禁用插件 | §7.4 列表 + 「打开视图 / 配置 / 启停」 |
| 6 | manifest 放 `capability/plugins` | §4.1 四级 `capability/plugins/<id>/plugin.json` |
| 7 | 制定插件规范 + 插件↔server 交互规范 | §4（manifest）/ §5（通道）/ §6（能力面） |
| 8 | 仅设计规范，不管插件内部实现 | §1.3 非目标 |
| 9 | server 提供：业务数据读写 / hook / LLM / 会话 | §6.1–§6.4（全部落到既有 MQ / 门面能力，不新开物理面） |
| 10 | 产品插件 Go；用户插件任意语言；UDS/TCP 为主 | §5.1（语言无关的行分隔 JSON 帧）/ §3.5（SDK 分层） |
| 11 | websearch 样板：Key + 检索参数 + 注入 MCP | §8 |
| 12 | compress / vfts / codebase 改插件，配置各自 iframe 页（含数据读写） | §9（含 `codebase` 与 `compress`） |

***

## 1. 与 v1 的差异、边界

### 1.1 v2 保留的 v1 结论

- 插件包 = 目录（manifest + exe + webui），**新增插件不改主程序源码**。
- 工具契约来源 = 插件自己的工具声明（不落 `capability/tools/*.tool.md`）。
- 插件启用 = 一个开关同时管「UI 入口 + 工具面」。
- 密钥恒 usr 级、永不回传明文、不打日志。
- 状态机唯一写入口 + 事件链广播（前端零轮询、零 `watch`）。

### 1.2 v2 推翻的 v1 结论（附原因）

| v1 | v2 | 原因 |
|----|----|------|
| `runtime.mode = inproc`（内嵌 Hook 也算插件） | **废除**：插件平台只认「独立 exe」；`compress` 等显式改造为 exe；`memory` / `history` 暂留内嵌 Hook（不在本平台 v1 范围） | 你的口径 1 明确「插件是 server spawn 的 exe」；两套生命周期混在一张表会让「启用/守护/重启」语义分裂 |
| `mcp.transport = stdio` 托管 | **禁止**给捆绑 MCP 用 stdio | 见 §5.7.1：gateway 会**再 spawn 一份 exe** 并独立 kill，与插件进程/状态/单实例冲突 |
| 配置页 = JSON-Schema 表单（host 渲染） | **iframe 页面**（插件自带 webui） | 你的口径 5-1/12；且 codegraph/vfts 现有富交互页不必重写 |
| 插件工具经 `tools/register` 直挂（v1 只留这一条） | **两条**：`http`（标准 MCP server，默认）/ `tools`（通道直挂） | 你的口径 3；`http` 能保留标准 MCP 生态与精确工具名 |
| `ui-plugin-*` 消息族（v1 规划） | **作废**，改用 `plugin-*`（§10） | `ui-plugin-*` 从未实现、全仓零生产零消费；本稿一次性定名，避免两套命名并存 |

### 1.3 非目标

- 不做插件市场 / 在线安装 / 签名校验（本期只做本地目录发现）。
- 不做 JS bundle 型插件 UI（只做 iframe），不做 MCP-UI / MCP Apps。
- **不做进程级沙箱**：插件以当前用户身份运行，`permissions` 是**宿主能力面的准入控制**，不是 OS 隔离（诚实声明，见 §4.5）。
- 不重写插件内部业务逻辑（DeepSeek 请求细节、codegraph 索引算法等仍归插件）。
- 不迁移 `memory` / `history`（保留内嵌 Hook；后续可另立迁移项）。
- 不改 `layout.free` 文档结构（用户已保存布局零迁移）。
- 不新增「开 iframe 到页签/抽屉/对话框」三条命令（§7.1 评估结论）。

***

## 2. 现状核对（代码事实）

### 2.1 已存在、直接复用

| 能力 | 事实 | 落点 |
|------|------|------|
| 插件 UDS 前门 | `pluginbridge`：帧 `{k,id,name,payload,ok,result,errors}`、UDS 默认 `%LOCALAPPDATA%\chonkpilot\plugin.sock`、后台 fanout 总线事件、订阅过滤 | [server.go](file:///e:/BizWorks/chonkpilot/src/lib/llm/pluginbridge/server.go#L16-L29) · [transport.go](file:///e:/BizWorks/chonkpilot/src/lib/core/transport/transport.go#L34-L50) |
| 握手 / 注册 / 订阅 | `plugin.hello`（id / name / version / features）→ 分配 `plugin:<id>:<8hex>`；`plugin.register`（features + subscriptions）；`plugin.subscribe/unsubscribe` | [control.go](file:///e:/BizWorks/chonkpilot/src/lib/llm/pluginbridge/control.go#L15-L94) |
| 能力白名单 | `plugin/v1/*` ≈60 条 → 既有 MQ 主题（会话 / 任务 / 配置 / 文件 / 工具 / server / 运维） | [capabilities.go](file:///e:/BizWorks/chonkpilot/src/lib/llm/pluginbridge/capabilities.go#L18-L86) |
| 反向 RPC | 宿主 → 插件：`plugin.tool.call` / `plugin.filter.run` / `plugin.tool.cancel`；工具主题经 `plugin.tool.bind` 绑定后转 RPC | [tools.go](file:///e:/BizWorks/chonkpilot/src/lib/llm/pluginbridge/tools.go#L29-L102) |
| Filter / 前置钩子 | 4 个 stage（已接 `llm.before_start` / `llm.before_send` / `llm.before_simple` / `tool.before_call`），`pass/replace/drop`，fail-closed；工具 `pre_hook_subject` | [filter.go](file:///e:/BizWorks/chonkpilot/src/lib/llm/pluginbridge/filter.go#L10-L58) |
| 进程监管 | `pluginhost.Host/Manager`：单实例锁、env 注入、`1s→30s` 退避、最多 3 次 | [host.go](file:///e:/BizWorks/chonkpilot/src/lib/llm/pluginhost/host.go#L189-L228) |
| 索引队列 | `plugin.index.take/ack/pending/workdirs` → data 门面待索引队列；vfts/codegraph indexer 已在用 | [server.go](file:///e:/BizWorks/chonkpilot/src/server/main.go#L266-L299) |
| gateway 注册 | `servers/register`（`mcp-servers-register`）支持 `stdio` / `http`(=Streamable HTTP) / `sse`、`namespace`、`hot_tools`、`headers`、`origin`、`timeout`、`isolate` | [gateway_servers.go](file:///e:/BizWorks/chonkpilot/src/lib/llm/server/gateway_servers.go#L194-L227) |
| 调用上下文注入 | 仅 `origin=builtin` 的下游收到 `_meta.chonkpilot{instance_id,work_dir,data_dir,session,turn,tool_call_id,top_session,parent,purpose}` | gateway 内核 `provider.go` / `contextmeta.go` |
| 工具名规则 | 暴露名 = `namespace` 覆盖 > `<id>_`；`"-"` = 禁前缀；`origin=user` 且无前缀时强制来源前缀；**冲突 = 注册失败** | gateway 内核 `registry.go` |
| 前端布局 | 动态 view 解析走注入的 `views.definitions` / `views.resolve`；命令 `open-view{viewKey,instanceKey,target}` 已支持 `tabs/drawer/dialog` | [deps.js](file:///e:/BizWorks/chonkpilot/src/frontend/src/layout-free/deps.js#L111-L123) · [commands.js](file:///e:/BizWorks/chonkpilot/src/frontend/src/layout/commands.js#L143-L169) |
| 预览区 | `preview-tab-open{kind,title?,tab?}`；功能页 = `specialComponents` 固定 12 键（CodeView 自管页签） | [CodeView.vue](file:///e:/BizWorks/chonkpilot/src/frontend/src/views/codeview/CodeView.vue#L273-L343) |
| 插件 → 宿主命令 | `plugin/v1/gui.list|apply`，动作白名单含 `preview-tab-open` / `lf-layout-*` / `data-*` / `gui.*` | [plugin_gui.go](file:///e:/BizWorks/chonkpilot/src/lib/gui/plugin_gui.go#L15-L110) |
| 静态资源 | 每窗口 `appHandler` 的路由（`/publish`、`/api/log/tail`、`/show/`、静态 dist） | [main.go](file:///e:/BizWorks/chonkpilot/src/lib/gui/main.go#L352-L364) |

### 2.2 结构性缺口（本设计要补的）

1. **无插件发现/注册中心**：`DefaultPlugins` 是编译期清单（[adapters.go](file:///e:/BizWorks/chonkpilot/src/lib/appwire/adapters.go#L15-L28)），无 manifest / 无 id / 无启停。
2. **无 manifest 目录**：`capability/` 下只有 `tools/prompts/resources/skills/agents/scenarios/system` 与 `mcps/`，没有 `plugins/`。
3. **无 UI 注册面**：全仓 `ui-plugin*` / manifest / 动态 view 注册**零实现**；`VIEW_DEFINITIONS` / `VIEW_COMPONENTS` 是模块级静态表（[view-definitions.js](file:///e:/BizWorks/chonkpilot/src/frontend/src/layout/view-definitions.js#L14)、[views.registry.js](file:///e:/BizWorks/chonkpilot/src/frontend/src/layout/views.registry.js#L57-L90)）。
4. **无 iframe view**：机制层 `LfViewHost` 只按 viewKey 解析 Vue 组件，无 URL 分支；预览区 iframe 只服务文件 `/show/`（`rawUrl`）。
5. **无插件管理页**：前端只有 MCP 设置页 / 项目配置子页，无插件列表。
6. **状态栏 / 顶栏条目来自布局文档**，无运行时注入通道。
7. **插件能力面缺口**：数据写（快照、摘要、file_list、prj 配置）多数不在 `plugin/v1` 白名单；无 UI RPC / 无插件配置读写 / 无插件状态上报。

***

## 3. 总体架构

### 3.1 拓扑

```text
 capability/plugins/<id>/            ┌──────────── 宿主进程 chonkpilot.exe ─────────────┐
   plugin.json  ────────────────────▶│ PluginManager                                     │
   *.exe        ──(spawn)──────────▶ │   ├─ 发现/四级合并/校验                           │
   webui/                            │   ├─ 生命周期：spawn / health / backoff / stop     │
                                      │   ├─ 注册表：status / views / bar / mcp / config   │
                                      │   └─ 广播：plugin-*（MQ → 前端）                  │
                                      │                                                   │
                                      │  pluginbridge（UDS 前门，单一 socket）             │
                                      │      ▲ 帧 req/res/evt/ctl（行分隔 JSON）           │
                                      │      │                                            │
                                      │  llm-server ── servers/register ──▶ gateway        │
                                      │  data 门面 ◀── plugin/v1/data.*（业务级校验）      │
                                      │  llm-simple ◀── plugin/v1/llm.step                 │
                                      │  会话能力  ◀── plugin/v1/session.* / subchat.*      │
                                      └───────────────────────────────────────────────────┘
                                                     ▲
   插件进程（Go / Python / Node / Java / Rust）────────┘
     ├─ 控制通道客户端（本设计协议）
     ├─ 可选：内嵌 MCP server（Streamable HTTP，回环）
     └─ 可选：webui 静态页（由宿主经 /plugin-ui/<id>/ 提供，iframe 加载）
```

### 3.2 分层职责

| 层 | 职责 | 不做什么 |
|----|------|---------|
| manifest（插件侧） | 声明启动、MCP 暴露方式、UI 视图/状态栏、配置字段、权限 | 不写宿主代码；不描述内部业务逻辑 |
| PluginManager（宿主） | 四级发现、校验、spawn/守护、注册表、状态机、持久快照 | 不解析工具语义；不代理业务请求 |
| pluginbridge（宿主） | 帧、握手、订阅、能力白名单、反向 RPC、审计 | 不做 UI、不做 MCP 协议解析 |
| gateway | MCP 传输/工具聚合/命名冲突/调用路由/取消 | 不 spawn 插件（由 PluginManager 独占） |
| 前端插件宿主 | 动态 view、iframe 宿主、postMessage 桥、插件管理页、状态栏注入 | 不认识具体插件 |

### 3.3 生命周期状态机

```text
discovered ──▶ starting ──▶ registering ──▶ running
                  │              │             │
                  │              └─ 注册失败 ──▶ error（入口保留、灰点、可重试）
                  ├─ spawn 失败 / initialize 超时 → backoff(1s→30s, max 3) → error
                  └─ 健康检查连续 3 次失败 ──▶ restarting ──▶ …
running ──▶ stopping ──▶ stopped（禁用/宿主退出）
running ──▶ degraded（MCP 断开 / UI RPC 连续失败；仍可打开配置页重试）
```

- **`running` 判据** = 控制通道 `plugin.hello` + `plugin.register` 成功 **且** MCP 注册成功（`mcp.mode=none` 时只看前两者），不是「进程还在」。
- 状态唯一写入口 = `PluginManager`；每次变更广播 `plugin-status{id,status,detail?}`。
- 插件也可 `plugin.status.report` 自报 `degraded`（如上游 API Key 失效），宿主合并展示。

### 3.4 启动顺序（宿主）

```text
1  appwire.Build / App.Start（llm-server 就绪）
2  pluginbridge.Start（UDS 前门）→ server-ready
3  core-mcp-server / 用户 MCP 对账（既有）
4  PluginManager.Start：
     4.1 扫四级 capability/plugins/*/plugin.json → 校验 → 合并（最具体级整条覆盖）
     4.2 读启用表 + UI 快照 → 立即广播 plugin-registered（**菜单/视图先于进程就绪**）
     4.3 对 enabled 且 autostart 的插件 spawn（并发，受上限约束）
     4.4 每插件：hello/register → MCP 注册 → status=running
5  前端 `plugin-list` 拉快照（与事件链并存，幂等）
```

> **为什么 UI 先于进程**：配置页/管理页必须在插件崩溃时仍能打开（用来重试/看日志），所以视图与菜单来自 **manifest + 持久快照**，不依赖插件在线。

### 3.5 语言与 SDK 分层

| 层 | 内容 | 面向 |
|----|------|------|
| 协议（唯一契约） | §5 的帧 + 方法名 + payload（行分隔 JSON，UTF-8） | 所有语言 |
| Go SDK（产品插件） | `chonkpilot-plugin-sdk/go`：通道客户端、订阅、MCP HTTP 骨架、配置读取、日志 | 产品插件（websearch / codebase / compress） |
| 其他语言 SDK（后续） | Python / Node / Rust 薄封装（只包 §5，不引入新语义） | 用户插件 |

> 协议**不依赖** protobuf / gRPC：复用现有 `transport` 帧（一行一 JSON），任何语言 30 行可接入。UDS 在 Windows 由 Go 侧抽象（现有 `transport` 已同时支持 `uds://` 与 `tcp://`）。

***

## 4. 插件规范（manifest）

### 4.1 目录与四级

```text
<级别根>/capability/plugins/<id>/
  plugin.json          # 唯一入口（必填）
  <exe>                # runtime.mode=managed 时必填
  webui/               # 可选：iframe 页面静态资源（只读）
  README.md            # 可选
```

| 级别 | 路径 | 用途 |
|------|------|------|
| app | `<exeDir>/capability/plugins/<id>/` | 出厂插件（websearch / codebase / compress） |
| user | `~/.chonkpilot/capability/plugins/<id>/` | 用户安装 |
| project | `<workDir>/.chonkpilot/capability/plugins/<id>/` | 项目自带（团队共享） |
| prjusr | `<prjusr 数据根>/capability/plugins/<id>/` | 本机覆盖 + 密钥 + 运行快照（不入库） |

- 同名跨级 = **最具体级整条覆盖**（`prjusr > project > user > app`），与 `capability/mcps` 同语义。
- 插件私有数据：`<dataDir>/plugins/<id>/`（索引、缓存、日志），宿主建目录并注入路径。

### 4.2 manifest 字段

```jsonc
{
  "schema": "chonkpilot.plugin/v1",
  "id": "websearch",
  "name": "联网搜索",
  "version": "1.0.0",
  "description": "基于 DeepSeek Search 的联网检索",
  "icon": "search",
  "publisher": "chonkpilot",
  "product": true,                         // true = 产品插件（origin=builtin，受信）

  "runtime": {
    "mode": "managed",                     // managed（默认）| external（用户自行拉起，宿主不 spawn）
    "exe": "chonkpilot-websearch.exe",     // 相对插件目录；禁止绝对路径/.. 越界
    "args": [],
    "env": {},                             // 静态环境变量；密钥不写这里
    "autostart": true,
    "restart": { "backoff_ms": 1000, "max_backoff_ms": 30000, "max_fails": 3 },
    "health":  { "interval_ms": 15000, "timeout_ms": 3000, "fail_threshold": 3 },
    "stop":    { "grace_ms": 5000 }
  },

  "channel": {
    "transport": "uds",                    // uds（默认，用宿主前门地址）| tcp（显式 addr）
    "addr": ""                             // 空 = 宿主注入 CHONK_PLUGIN_ADDR
  },

  "mcp": {
    "mode": "http",                        // http（默认）| tools | none
    "namespace": "self_",                  // "-" = 原样；"self_" = 与既有 self_* 工具面一致
    "hot_tools": ["web_search"],           // 缺省 = "*"（全部进默认下发面）
    "timeout_sec": 120,
    "category": "search",
    "origin": "builtin",                   // builtin | user（user 不注入 _meta.chonkpilot）
    "tools": [                             // tools 模式的声明（http 模式可省略）
      { "name": "web_search", "description": "...", "schema": { "type": "object", "properties": {} },
        "hot": true, "idempotent": true }
    ]
  },

  "config": {
    "scope": "prjusr",                     // 非密配置落点：usr|prj|prjusr
    "fields": [
      { "key": "apiKey",     "type": "string",  "secret": true,  "required": true, "label": "DeepSeek API Key" },
      { "key": "baseUrl",    "type": "string",  "default": "https://api.deepseek.com" },
      { "key": "count",      "type": "integer", "default": 10, "min": 1, "max": 50 },
      { "key": "needContent","type": "boolean", "default": false },
      { "key": "timeoutSec", "type": "integer", "default": 30, "min": 5, "max": 120 }
    ]
  },

  "ui": {
    "views": [
      { "id": "config", "title": "联网搜索", "icon": "search",
        "entry": "webui/config.html",
        "containers": ["preview", "tab", "drawer", "dialog"],
        "default_size": { "width": 760, "height": 560 } },
      { "id": "status", "title": "联网搜索状态", "icon": "activity",
        "entry": "webui/status.html",
        "containers": ["preview", "tab", "drawer"] }
    ],
    "menus": [
      { "location": "plugin", "label": "联网搜索", "icon": "search", "view": "config" }
    ],
    "statusbar": [
      { "id": "websearch", "zone": "right", "icon": "search", "tooltip": "联网搜索",
        "command": { "target": "host", "action": "open-view", "view": "status" } }
    ],
    "rpc": ["test", "status"]              // 允许 UI 调用的插件方法（白名单）
  },

  "permissions": {
    "data":  ["storage.self"],
    "hooks": [],                            // websearch 只读检索，不订阅事件
    "llm":   {},                            // 不需要宿主 LLM（直接调 DeepSeek）
    "session": [],
    "ui":    { "containers": ["preview", "tab", "drawer", "dialog", "statusbar"] },
    "network": ["api.deepseek.com"]         // 声明用途（宿主代理请求时校验；插件直连无法强制）
  }
}
```

**字段口径**

- `mcp.namespace` 缺省取 **产品插件 `self_`**（与 `self_file_read` / `self_codegraph_*` 一致）、用户插件 `"-"`（gateway 会补 `<id>_`）。
- `ui.views[].entry` 必须是相对路径，只允许 `webui/` 前缀；宿主以 `/plugin-ui/<id>/<entry>` 提供。
- `ui.statusbar[].command` 二选一：`target:"host"`（§7.5 宿主命令）或 `target:"plugin"`（转发插件 `plugin.command`）。
- `config.fields[]` 是**唯一可写键**；未声明的键 `plugin-config-set` 直接拒绝。

### 4.3 校验规则（PluginManager 启动强制）

1. `id` 匹配 `^[a-z][a-z0-9-]{1,31}$`，且等于目录名。
2. `runtime.mode=managed` → `exe` 必填、存在、解析后仍在该插件目录内（拒绝绝对路径与 `..` 越界）。
3. `mcp.mode=http` → 插件上报的 `url` 必须 `http://127.0.0.1:<port>`（仅回环 + 随机端口），且 `token` 非空；非回环 → 拒绝注册并 `status=error`。
4. `mcp.mode=tools` → `mcp.tools[]` 非空，工具名全局唯一（与 gateway 冲突即注册失败，不静默改名）。
5. `mcp.mode` 不得为 `stdio` / `sse`（`stdio` 明确报错：见 §5.7.1；`sse` 本期不支持）。
6. `ui.views[].containers` ⊆ `{preview,tab,drawer,dialog,bar}`；`preview` 必须有 `entry`。
7. `config.fields[]` 键名 `^[A-Za-z][A-Za-z0-9_.-]{0,63}$`；`secret:true` 只允许 `string`。
8. `permissions.data` 每条必须命中 §6.1 资源表；未知资源 → 拒绝该条并告警（不静默放行）。
9. `product=false` 时禁止 `origin:"builtin"`、禁止 `permissions.data` 含 `config.usr.write` / `config.prj.write` 无限前缀。

### 4.4 权限模型（先说不做什么）

> **进程级不隔离**：插件 exe 以当前 OS 用户运行，能读写用户可访问的文件、能访问网络。`permissions` 只约束**插件经宿主能力面**能做什么（数据 / 钩子 / LLM / 会话 / UI），**不是 OS 沙箱**。若将来需要强隔离，另立「插件进程沙箱」专题（Job Object / 受限令牌 / AppContainer），不在本期。

- **声明 vs 强制**：`permissions.*` 由 PluginManager 在**每次能力调用**时校验；未声明 = 拒绝（fail-closed）。
- **数据权限**：`resource.op[:selector]`，selector 支持前缀通配（`compress.*`）与键名（`summary_prompt`）。
- **网络权限**：仅用于**宿主代理**（插件经宿主代理发起请求时）与审计；插件自行 `http.Client` 无法被宿主阻止 —— **如实告知**。
- **密钥**：`permissions` 不含密钥；插件读取的是宿主注入的 `secrets.json` 文件路径（同用户可读，属既有边界）。

***

## 5. 插件 ↔ Server 交互规范

### 5.1 传输与帧

| 项 | 规定 |
|----|------|
| 传输 | **UDS 默认**（宿主前门，`CHONK_PLUGIN_ADDR` 注入）；`tcp://` 仅显式配置（开发/跨机调试），PRODSIM-1 后**不默认开端口** |
| 编码 | 一行一个 JSON（UTF-8，`\n` 分隔），单帧上限 8 MiB |
| 帧 | `{"k":"req|res|evt|ctl","id":"<关联 id>","name":"<方法/主题>","payload":{},"ok":true,"result":{},"errors":[]}` |
| 请求 | 插件 → 宿主发 `req`；宿主回 `res`（`ok` + `result` 或 `errors:["<code>: <msg>"]`） |
| 事件 | 宿主 → 插件 `evt`（订阅命中的总线事件）；插件 → 宿主用 `plugin.publish`（白名单主题，见 §5.4） |
| 控制 | `ctl`：`plugin.shutdown` / `plugin.tool.cancel` / `plugin.command` / `plugin.ui.rpc` / `plugin.config.changed` |
| 并发 | 每插件一条连接；请求队列 64、事件队列 256，满则丢事件（不阻塞总线）、请求直接报错 |
| 超时 | 普通方法 30s；`plugin.tool.call` 11min（契约 `timeout=0/-1` 时无上限，仅靠取消收敛）；`plugin.ui.rpc` 60s |

### 5.2 握手与注册

```text
插件 → plugin.hello        { id, name, version, features }
宿主 →                     { plugin_id, instance_id:"plugin:<id>:<8hex>", version, methods:[...] }
插件 → plugin.register     { features, subscriptions:[...], ui?, mcp?, status? }
宿主 →                     { ok, subscriptions, capabilities:{granted:[...]}, config:{path,...} }
插件 → plugin.ready        {}                       // 初始化完成（可选）
宿主 → server-ready        {}                       // 晚连接补发（既有）
```

- `plugin.register` 的 `ui` / `mcp` 可**省略**（宿主用 manifest）；携带时用于**运行时覆盖**（第三方插件无 manifest 的场景）。
- `subscriptions[]` = `{ topic, filter?:{instance_id?,project_id?,session_id?,direction?}, mode:"async" }`；只支持 `async`（不阻塞总线）。
- 注册返回值里的 `capabilities.granted` = 实际生效的权限（可能少于 manifest 声明，便于插件自检）。

### 5.3 能力方法面（插件 → 宿主）

> 复用既有 `plugin/v1/*` 白名单（[capabilities.go](file:///e:/BizWorks/chonkpilot/src/lib/llm/pluginbridge/capabilities.go#L18-L86)），本设计**只增不改**。带 ★ = 本期新增。

| 分类 | 方法 | 说明 |
|------|------|------|
| 握手 | `plugin.hello` / `plugin.register` / `plugin.ready` / `plugin.exit` | 既有 |
| 订阅 | `plugin.subscribe` / `plugin.unsubscribe` | 既有 |
| 工具绑定 | `plugin.tool.bind` / `plugin.tool.unbind` | 既有（`tools` 模式必需） |
| 过滤器 | `plugin.filter.register` | 既有 |
| 索引队列 | `plugin.index.take|ack|pending|workdirs` | 既有（codebase / vfts 用） |
| 实例 | `plugin/v1/instance.claim|list` | 既有 |
| 会话 | `plugin/v1/session.start|send|cancel|list|get|history|content|snapshot|rename|delete|active` | 既有 |
| 插话 | `plugin/v1/session.inbox.add|list|discard|promote` | 既有 |
| 会话写 ★ | `plugin/v1/session.snapshot.set` / `session.summary.set` / `session.messages` / `session.context` | 新增（compress 需要） |
| 任务 | `plugin/v1/task.tree|tasks|stop|background|verify` / `tool.retry` / `ask.reply` | 既有 |
| 配置读 | `plugin/v1/config.user.get|project.get|filelist.list|memory.list|mcp.list|scenario.list|prompt.get` | 既有 |
| 配置写 ★ | `plugin/v1/config.project.set` / `config.project.delete` / `prompt.set` | 新增（按 `permissions.data` 的前缀/键校验） |
| 数据 ★ | `plugin/v1/data.read` / `plugin/v1/data.write` | 新增统一业务数据面（§6.1） |
| 文件 | `plugin/v1/file.list|read|write|mkdir|remove|rename|copy` | 既有（限 `work_dir`） |
| 工具 | `plugin/v1/tools.list|call|wait|timeout` | 既有 |
| 执行器 ★ | `plugin/v1/tools.exec` | 新增：宿主转发 core `/internal/exec`（单工具 executor，供 codebase 复用既有查询执行器） |
| MCP ★ | `plugin.mcp.register` / `plugin.mcp.unregister` | 新增（§5.7，替代插件直调 `servers.register`） |
| UI ★ | `plugin.ui.register` / `plugin.status.report` | 新增（§5.8 / §3.3） |
| 事件上报 ★ | `plugin.publish{topic,payload}` | 新增：仅允许 `plugin-ui-event`（推给自己的 iframe），**不开放任意 MQ 发布** |
| 命令 | `plugin/v1/gui.apply`（既有） | 插件 → 宿主窗口命令（白名单动作，§5.6） |
| LLM | `plugin/v1/llm.step` | 既有（单次调用，无会话/工具） |
| 运维 | `plugin/v1/memory.flush` / `vfts.reindex` / `vfts.dict.set` / `prompt.optimise` | 既有 |

> **收口原则**：插件**不得**直接调 `plugin/v1/servers.register` / `plugin/v1/tools.register`（白名单里保留是历史兼容）；manifest 插件统一走 `plugin.mcp.register`，由 PluginManager 校验后转发 gateway。第三方无 manifest 插件在过渡期仍可用旧方法，但会标记 `legacy:true`。

### 5.4 事件订阅与 hook 目录

**订阅**：`plugin.subscribe{subscriptions:[{topic, filter?, mode:"async"}]}`，宿主按 §0 的主题规则原样投递（主题名 = 相对主题，如 `session-compress`）。

| 类别 | 主题（订阅） | 触发 |
|------|-------------|------|
| 轮次 / 会话 | `session-turn-start` · `session-complete` · `session-compress` · `session-ask` · `session-interrupted` · `subchat-opened` · `subchat-state` | 既有事件 |
| 工具 | `tool-notify` · `mcp-tools-timeout` · `mcp-tasks-report` | 既有 |
| 文件 | `filesys.changed` | 既有（60ms 去抖后广播） |
| 配置 | `data-user-config-changed` · `data-<domain>-refresh` | 既有 |
| 实例 | `instance-register` · `instance-heartbeat` · `instance-exit` | 既有 |
| 生命周期 | `server-ready` | 宿主就绪（晚连接补发，既有） |

> 配置变更通知 `plugin.config.changed` **不是总线事件**，走控制帧（§5.5）；插件也可按 `CHONK_PLUGIN_CONFIG` 文件 mtime 自读。

**Hook（同步，可拦截）**

| Hook | 接入方式 | 语义 |
|------|---------|------|
| `llm.before_start` | `plugin.filter.register{stage}` | 新一轮开始前；可 `replace` / `drop` |
| `llm.before_send` | 同上 | 每次发给 LLM 前；可改写消息 |
| `llm.before_simple` | 同上 | `llm-simple` 前 |
| `tool.before_call` | 同上 + 工具 `pre_hook_subject` | 工具执行前；失败 = 拒绝本次调用（fail-closed） |
| `session-compress` | 事件订阅（异步） | 轮末压缩触发（compress 插件） |
| `filesys.changed` | 事件订阅 | 文件变化（codebase 增量索引） |

> Hook 的**唯一实现载体**仍是既有 filter chain / `pre_hook_subject`，本设计不新增第二种钩子机制。

### 5.5 宿主 → 插件（反向）

| 帧/方法 | 载荷 | 用途 |
|---------|------|------|
| `plugin.tool.call` | `{subject, payload:{tool,args,context:{instance_id,session,turn,tool_call_id,work_dir,data_dir,purpose}}}` | 工具调用（`tools` 模式） |
| `plugin.tool.cancel` | `{call_id}` | 取消（超时/用户取消） |
| `plugin.filter.run` | `{stage,payload}` → `{action:"pass|replace|drop",payload?}` | 同步钩子 |
| `plugin.ui.rpc` ★ | `{method, params, req_id}` → `{ok,result}` | 插件配置页/状态页调用插件自有方法（白名单 = `ui.rpc`） |
| `plugin.command` ★ | `{name, params}` → `{ok,result?}` | 状态栏「插件命令」/ 管理页按钮（重启、重建索引） |
| `plugin.config.changed` ★ | `{keys:[...]}` | 配置保存后通知（插件也可按 mtime 自读文件） |
| `plugin.shutdown` | `{grace_ms}` | 宿主退出/禁用时优雅停止 |

### 5.6 插件 → 宿主「发送指令」

统一走既有 `plugin/v1/gui.apply`（[gui.go](file:///e:/BizWorks/chonkpilot/src/lib/llm/pluginbridge/gui.go#L20-L45)）：

```jsonc
{ "action": "preview-tab-open", "payload": { "kind": "plugin-page", "plugin": "websearch", "page": "config" } }
{ "action": "lf-layout-open-drawer", "payload": { "drawerId": "drawer_task", "viewKey": "plugin:websearch:status" } }
{ "action": "data-prj-config-save", "payload": { "data": { "key": "websearch.count", "value": "20" } } }
```

- 动作白名单**只增**：本期为 `guiEventActions` 补 `plugin-page` 的 payload 字段（`plugin` / `page`）与 `plugin:<id>:<view>` viewKey（**不新增主题**）。
- 需要「在指定窗口打开」时带 `instance_id`；不带 = 广播（现有语义）。

### 5.7 MCP 注册

#### 5.7.1 为什么插件捆绑 MCP 时禁止 stdio（硬规则）

`servers/register` 的 `runtime` + `transport:"stdio"` 语义 = **gateway 自己 `exec.Command` 拉起 exe、自己 `Close()` kill**（gateway 内核 `provider.go`）。若该 exe 同时是插件进程：

1. **双进程**：PluginManager 已 spawn 一份，gateway 再 spawn 一份 → 两套索引/锁/状态，`zvec` 单写锁直接冲突。
2. **双所有者**：插件被禁用时 PluginManager 停 A，gateway 可能仍在跑 B；反之 gateway 重启会 kill B 而不通知插件。
3. **状态割裂**：stdio 的 stdin/stdout 被 MCP 独占，插件的控制通道（UDS）与日志被拆到另一个进程实例，`plugin.hello` 身份无法对应。
4. **无法沙箱化**：stdio 子进程可被 gateway 单独 kill / 设沙箱，但**插件主体**仍需长期存活并持有业务状态 —— 杀一个不等于停插件，语义矛盾。

→ **规则**：manifest `mcp.mode` 只允许 `http` / `tools` / `none`；出现 `stdio` 一律校验失败。

#### 5.7.2 三种模式

| mode | 插件侧 | 注册动作 | 工具名 | 适用 |
|------|--------|---------|--------|------|
| **http**（默认） | 进程内起 Streamable HTTP MCP server（`127.0.0.1:0`，随机端口，Bearer token） | `plugin.mcp.register{url,token,hot_tools?}` → 宿主 `servers/register{mcp_server:{url,transport:"http",headers:{Authorization:"Bearer <token>"},namespace,hot_tools,timeout,origin,category}}` | 由 `namespace` 决定（`self_` / `-`） | 产品插件；需要标准 MCP / 精确工具名 |
| **tools** | 不发 HTTP；声明工具 schema，绑定回调主题 | `plugin.mcp.register{mode:"tools",tools:[...]}` → 宿主逐条 `tools/register{handler_subject:"plugin:<id>:tool",...}` + `plugin.tool.bind` | `self_<name>`（gateway 固定前缀） | 极简插件 / 不想开端口 |
| **none** | 无工具面 | 不注册 | — | 纯 UI / 后台插件（compress） |

**http 模式细节**

- 端口由 OS 分配（`:0`），插件把实际 `url` 与 token 报给宿主；**不写配置文件**、不固定端口。
- token 每进程随机生成，只在内存与 `plugin.mcp.register` 帧中出现；host 不打日志。
- `origin:"builtin"` 时 gateway 在 `tools/call` 注入 `_meta.chonkpilot`（实例 / workdir / 会话 / turn / tool_call_id），**插件据此路由多项目**（codebase 必需）。
- `origin:"user"` 时不注入，且工具名强制来源前缀；第三方插件用此默认值。
- 调用取消：gateway 断连/取消 → HTTP 请求 ctx 取消；插件应在 handler 里尊重 `ctx`。

**tools 模式细节**

- 宿主注册前做三件事：名字唯一性检查（先查 `tools/list`）、schema 合法性（JSON Schema object）、`permissions.mcp` 校验。
- 工具执行 = `plugin.tool.call` 反 RPC，payload 含 `context`（同上字段）；11 分钟上限，`no_timeout` 由契约声明。
- 工具名固定 `self_<name>` —— 与 `codegraph_*` 现状一致（实际 LLM 可见名带 `self_`，契约名不带，见 [engineStatus.js](file:///e:/BizWorks/chonkpilot/src/frontend/src/utils/engineStatus.js#L14)）。

**注册/注销幂等**

- 同名 register = 覆盖（先 unregister 再 register）；`running` 状态下重复注册不报错。
- 禁用一个插件 = `servers/unregister`（http）或逐条 `tools/unregister`（tools）→ 停进程 → 撤 UI → 广播。
- MCP 注册失败：`status=error{detail:"tool conflict: xxx"}`，UI 入口保留并灰点（便于用户改名/禁用冲突方）。

### 5.8 UI 注册

manifest `ui.views[]` 是**主路径**；`plugin.ui.register` 用于运行时增补（第三方无 manifest、或按状态动态生成视图）：

```jsonc
{
  "views": [
    { "id": "status", "title": "联网搜索状态", "icon": "activity",
      "entry": "webui/status.html", "containers": ["preview","tab","drawer"] }
  ],
  "menus": [ /* 同 manifest */ ],
  "statusbar": [ /* 同 manifest */ ]
}
```

- 注册结果 = 视图 id `plugin:<pluginId>:<viewId>`（全局唯一）；同名重复注册 = 覆盖。
- 视图被移除 → 已打开的页签/抽屉显示「视图已不可用」占位（不静默空白），布局文档中的引用保留（插件恢复后自动复活）。
- 插件禁用 → 其视图从动态注册表移除、状态栏条目隐藏；**布局文档不改写**（用户布局零迁移）。

### 5.9 错误、超时、幂等

| 项 | 规定 |
|----|------|
| 错误码 | 沿用 `pluginbridge`：`args_invalid` / `plugin_unregistered` / `plugin_unauthorized` / `method_not_found` / `filter_dropped` / `internal` + 本期新增 `permission_denied` / `mcp_conflict` / `config_invalid` / `ui_rpc_denied` |
| 幂等 | `plugin.register` / `plugin.mcp.register` / `plugin.ui.register` / `plugin-config-set` 均幂等；`unregister` 对不存在项返回成功 |
| 重试 | 宿主**不自动重试**插件工具调用（除非 manifest 标 `idempotent:true`）；RPC 超时不重发（避免副作用翻倍） |
| 背压 | 单插件并发请求上限 32；超出直接 `internal: busy`（不排队到无限） |
| 审计 | 每次能力调用记一行：`plugin=<id> method=<m> ok=<bool> ms=<n>`（不含 payload / 密钥） |

***

## 6. Server 提供给插件的能力

### 6.1 业务数据读写（业务级，不是物理级）

**原则**：插件**永远不直接打开** `chonkpilot.db` / 不写 `capability/` 内部文件 / 不拿 bbolt 句柄；一律经 `plugin/v1/data.*`（内部转发既有 `data-*` 主题 + 门面），由宿主做**归属 + 字段 + 状态机**校验。

资源表（`permissions.data` 的合法值）：

| 资源.操作 | 对应既有面 | 额外校验 |
|-----------|-----------|---------|
| `session.read` | `data-session-list/get/history/content/latest/context/load-messages` | 只读；按 instance 路由 |
| `session.snapshot.read` / `.write` | `data-snapshot-get` / `data-snapshot-set` | 写只允许 `snapshot_turn` 单调不回退；`history` 必须是合法消息数组 |
| `session.summary.write` | `data-session-set-summary` | 只允许该插件处理过的 turn；长度上限（默认 8K 字符） |
| `session.write` | `data-session-title/delete/active-set` | 需显式声明；delete 前宿主 cancel 运行轮（既有语义） |
| `session.inbox.read` / `.write` | `llm-inbox-list` / `add/discard/promote` | 只对 `pending` 项；跨会话需声明 |
| `task.read` / `.control` | `data-tasktree-*` / `task-stop` / `task-background` | `.control` 需显式声明 |
| `config.usr.read` | `data-user-config`（只读） | 密钥字段**永不返回**（`apiKey` / `token` 等按字段名掩码） |
| `config.prj.read:<prefix>` | `data-prj-config` | 只返回声明前缀内的键 |
| `config.prj.write:<prefix>` | `data-prj-config-save/delete` | 只允许声明前缀；值长度/类型按 64 号表校验；禁写 `window.*` / `layout.*` / `security.*` |
| `prompt.read:<key>` / `.write:<key>` | `data-prompt-*` | 键白名单（如 `summary_prompt`） |
| `filelist.read` / `.write` | `data-filelist` | 写仅 `put/del`；路径必须是该 workdir 绝对路径 |
| `memory.read` / `.write` | `data-memory-*` | 写仅自定义类别；预置类别拒绝（既有语义） |
| `knowledge.read` | `data-knowledge-*` | 只读 |
| `filesys.read` / `.write` | `filesys.*` | 限 `work_dir`；拒绝 `.chonkpilot/` 内部路径（除 `plugins/<id>/`） |
| `storage.self` | 新增（宿主直管） | 插件私有 KV/文件：`<dataDir>/plugins/<id>/storage/`，单文件 ≤ 8 MiB、总配额 256 MiB |

**统一请求形状**

```jsonc
// plugin/v1/data.read
{ "resource": "session", "op": "history", "instance_id": "...", "params": { "session_id": "..." } }
// plugin/v1/data.write
{ "resource": "config.prj", "op": "set", "instance_id": "...", "params": { "key": "websearch.count", "value": "20" } }
```

- `instance_id` 必填（§0 硬规则）；缺失 → `args_invalid`。
- 写操作统一返回 `{ok:true, id?}`；失败 → 明确错误码（不返回空对象）。
- **不做通用 SQL / 任意 KV**：资源与操作是闭集，新增资源必须改宿主（这是「避免对数据随意修改」的代价，刻意为之）。

### 6.2 Hooks / 监听

见 §5.4。补充：

- 订阅是**静态声明优先**（manifest `permissions.hooks`），运行期 `plugin.subscribe` 只能订阅已声明集合；未声明 → `permission_denied`。
- 每个插件的事件投递独立队列；慢消费者丢事件（记一行审计），**不阻塞总线**。
- `filter` 是唯一的同步阻塞面（单 filter 200ms / 整链 1s，超时 = fail-closed）。

### 6.3 LLM 能力

| 能力 | 方法 | 说明 |
|------|------|------|
| 单次调用 | `plugin/v1/llm.step` → `llm-simple` | `{prompt, system?, instance_id?, llm?}` → `{text}`；60s 超时；无工具、无会话；支持 `{{toolchain.<key>}}` 替换 |
| 提示词优化 | `plugin/v1/prompt.optimise` | 既有 |
| 带工具 / 多轮（后续） | 🔵 `plugin/v1/llm.chat` | 本期不做；如需要，插件应通过「会话能力」起子会话（§6.4），避免在插件内复制工具循环 |

> 密钥**不经插件**：`llm.step` 由宿主选择已配置 LLM 并发起请求，插件拿不到 `apiKey`。

### 6.4 会话能力

| 场景 | 能力 | 说明 |
|------|------|------|
| 单聊（起一轮） | `plugin/v1/session.start` + `session.send` | 等价客户端 `llm-start` / `llm-send`；返回 `{accepted}`，结果走事件 |
| 多聊 / 子聊 | `plugin/v1/subchat.open` + `subchat.finish` | 既有 server 能力（`plugin_caps.go`）；建 `kind=sub_chat` 子会话并异步起首轮 |
| 群聊 | 🔵 `plugin/v1/group.open`（**新增，待实现**） | 本期只登记需求；实现前插件可用「多个 `session.start` + 自管调度」降级 |
| 插话 | `plugin/v1/session.inbox.add|list|discard|promote` | 对**当前轮**插话（`insert`）/ 排下一轮（`next_turn`）；语义同前端「插话」 |
| 取消 | `plugin/v1/session.cancel` + `tool.stop` | 取消轮次 / 取消单个工具 |
| 工具调用 | `plugin/v1/tools.call` | 走 gateway（含第三方 MCP、executor）；`async` 支持 |
| 单工具执行 ★ | `plugin/v1/tools.exec` | 宿主直转 core `/internal/exec`（不经 gateway 路由，避免递归） |
| 工具结果等待 | `plugin/v1/tools.wait` | 异步工具结果 |

- **会话归属**：插件起的会话 `kind` / `purpose` 必须显式声明；宿主据此在 UI 区分（如「由插件创建」）。
- **权限**：`permissions.session` 逐项声明；未声明不能起会话 / 插话 / 调工具。
- 插件起的轮次同样适用「受理即持久化」「非终态收口」等既有语义（见 §12）。

### 6.5 平台服务

| 服务 | 内容 |
|------|------|
| 配置 | `plugin-config-get/set`（UI 与插件共用）；非密落 `config.scope`，密钥恒 usr 级；原子写 + 广播 `plugin-config-changed` |
| 私有存储 | `storage.self`（§6.1）；路径 `<dataDir>/plugins/<id>/storage/` |
| 索引队列 | `plugin.index.*`（既有）；codebase / vfts 消费 |
| 日志 | 插件 stderr 由宿主捕获 → `<dataDir>/plugins/<id>/logs/plugin.log`（滚动 2MB×3）；插件也可 `plugin.log{level,msg}`（自动脱敏：拒绝含 `sk-` 的长串） |
| 实例视图 | `plugin/v1/instance.claim|list`；多项目 workdir 由插件自管 |

***

## 7. UI 设计

### 7.1 评估：三种 iframe 承载方式

| 方案 | 做法 | 优点 | 缺点 | 结论 |
|------|------|------|------|------|
| A. 新增三条命令 | `open-iframe-tab` / `open-iframe-drawer` / `open-iframe-dialog` | 直白 | ① iframe 没有 view 实体 → **不能进布局文档、不能被编辑器选中**；② 三套容器各自实现（页签/抽屉/对话框），单例、视图池、保活、尺寸全部重造；③ 与 `open-view{target}` 双轨，命令语义分裂 | ❌ |
| **B. 注册 iframe view（推荐）** | manifest 声明 view → 动态注册为 `plugin:<id>:<view>`（`containers` 含 `preview/tab/drawer/dialog/bar`）→ 用既有 `open-view{viewKey,target}` 打开 | ① **一个定义、四种容器**；② 复用 `LfViewHost` / `LfTabbedContainer` / `LfDrawerHost` / `DialogManager` / 视图池 / 单例；③ 布局编辑器可下拉选中、可持久化；④ **机制层零改动**（`views.definitions` / `views.resolve` 本就是注入的函数） | 需要在**适配层**补动态注册与 `containers` 判定；预览区（CodeView 自管页签）仍需专用宿主 | ✅ |
| C. 仅预览区嵌 iframe | `preview-tab-open{kind:'plugin-page',plugin,page}` | 满足「配置页在预览区」 | 抽屉/对话框/状态页无法承载 | 只作为 B 的**补充** |

**最终取舍**：**B 为主 + C 为补充**。

- 插件配置页：默认落 **预览区**（你的口径 5-2），同时因注册为 view，也可被用户拖到抽屉/对话框/自由页签。
- 插件状态页：注册为 view（`containers:["preview","tab","drawer"]`），由状态栏条目命令打开。
- 不新增任何「开 iframe」主题；只有 `preview-tab-open` 的 payload **只增** `plugin` / `page` 两字段。

### 7.2 动态 view 注册（适配层改造点）

现状：`viewDefinitions()` 返回注入的 `state.views.definitions`（每次调用都读），`resolveViewComponent()` 优先用 `views.resolve`（[deps.js](file:///e:/BizWorks/chonkpilot/src/frontend/src/layout-free/deps.js#L111-L123)）；`checkView` 用 `getView` 校验（[config.js](file:///e:/BizWorks/chonkpilot/src/frontend/src/layout-free/stores/config.js#L253-L263)）。→ **动态 view 注册本身只在适配层完成、机制层零改动**；唯一例外是状态栏注入需在 `LfBarZones` 增一个只读 `extraItems` prop（渲染顺序不变）。

| 改动 | 文件 | 内容 |
|------|------|------|
| ① 动态注册表 | `layout/views.registry.js` | 新增 `registerPluginViews(list)` / `unregisterPluginViews(id)`；内部维护 `dynamicDefs[]` + `dynamicComponents{}`；`createViewRegistry()` 返回对象的 `definitions` 用 **getter** 合并静态 + 动态，`resolve` 先查动态 |
| ② 组件包装 | `layout/pluginViews.js`（新） | `makePluginViewComponent(pluginId, viewId)` → `PluginPageHost`（§7.6），把 viewKey 捕获进闭包 |
| ③ 容器判定 | `layout/view-definitions.js` + `layout/commands.js` | 新增 `containersOf(viewKey)`；`resolveOpenView` 的 `dialog` / `drawer` 分支改为「`containers` 含该容器」而非 `host==='dialog'`；`host` 保留给旧静态 view |
| ④ 数据源 | `composables/usePlugins.js`（新） | `plugin-list` 快照 + `plugin-registered/unregistered/status/set-enabled` 事件链；零 `watch` |
| ⑤ 状态栏注入 | `layout/ChonkStatusBar.vue` / `ChonkTopBar.vue` + `LfBarZones.vue` | 宿主传入 `extraItems`（来自 `usePlugins`），渲染在配置条目之后；**不写布局文档**（插件禁用即消失） |
| ⑥ 预览区 | `views/codeview/CodeView.vue` | `specialComponents` 增 **一个** `plugin-page`；`handleTabOpen` 读 `plugin`/`page` 生成唯一 key（`plugin-page:<id>:<page>`）；组件内部用 `PluginPageHost` |

**view 定义形状（动态）**

```js
{
  key: 'plugin:websearch:config',
  label: '联网搜索',            // 来自 manifest ui.views[].title
  icon: 'search',
  mode: 'multi',               // 插件视图默认 multi（允许不同 plugin 页并存）
  host: 'layout',              // 派生：containers 含 tab/bar
  containers: ['preview','tab','drawer','dialog'],
  designable: false,           // 设计态只渲染骨架（iframe 不预加载）
  plugin: { id: 'websearch', view: 'config', entry: 'webui/config.html' },
}
```

### 7.3 预览区插件页

- 打开方式：插件管理页「配置」按钮 / 插件菜单 / 状态栏命令 → 宿主发 `preview-tab-open{kind:'plugin-page', plugin:'websearch', page:'config', title:'联网搜索'}`。
- CodeView：`kind === 'plugin-page'` → `plugin-page` 组件 + `plugin`/`page` props；页签 key = `plugin-page:<id>:<page>`（同插件同页去重）。
- 宿主组件 `PluginPageHost.vue`：
  - 从 `usePlugins()` 取 `entry` → 计算 `src = /plugin-ui/<id>/<entry>?v=<version>&p=<page>`；
  - 渲染 `<iframe sandbox="allow-scripts allow-forms" referrerpolicy="no-referrer">`（**不授予** `allow-same-origin`）；
  - 加载中骨架 / 失败占位（含「重试」「打开日志」）；
  - 卸载时清理 postMessage 监听与未完成请求。
- 插件被禁用：已开页签保留（显示「插件已禁用」占位 + 启用按钮），避免用户丢上下文。

### 7.4 插件管理页

- 新预览功能页 `plugin-manager`（`CodeView.specialComponents` 增一键；工具栏「插件」菜单项 → `preview-tab-open{kind:'plugin-manager'}`）。
- 布局：左侧插件列表（状态点 / 名称 / 版本 / 开关），右侧详情：

| 区块 | 内容 |
|------|------|
| 概览 | id、名称、版本、发布者、状态、PID、启动时间、最近错误 |
| 能力 | `capabilities` / `permissions` 摘要（只读） |
| 工具面 | 已注册工具名 + hot + 最近调用耗时（来自 `tools-list`，只读） |
| 视图 | `views[]` 列表，每项「在预览区打开」按钮（→ §7.3） |
| 配置 | 「打开配置页」按钮（→ §7.3 的 `config` view） |
| 操作 | 启用/禁用、启动/停止/重启、打开日志、刷新 manifest、复制插件目录 |
| 注册（三方） | 外部插件注册令牌（仅 `runtime.mode=external` 时需要） |

- **禁用** = `plugin-set-enabled{id,false}` → 停进程 + 注销 MCP + 隐藏状态栏/菜单（视图保留注册但置为禁用态）。
- 列表数据源 = `plugin-list`；状态变化经 `plugin-status` 事件链更新（不轮询）。

### 7.5 状态栏 / 顶栏条目

- manifest `ui.statusbar[]` → 动态条目（`kind:"button"` 或 `kind:"view"`），由宿主在渲染时合并到 `statusbar` 区（右侧默认）。
- 命令模型复用栏条目已有 `commandToMq`（[layout-free/utils/commands.js](file:///e:/BizWorks/chonkpilot/src/frontend/src/layout-free/utils/commands.js#L30-L64)）：

```jsonc
// 打开插件视图（含状态页）
{ "type": "open-view", "viewKey": "plugin:websearch:status", "target": "auto" }
// 打开预览区插件页
{ "type": "publish-mq", "topic": "preview-tab-open",
  "payload": { "kind": "plugin-page", "plugin": "websearch", "page": "config" } }
// 转发给插件执行
{ "type": "publish-mq", "topic": "plugin-command",
  "payload": { "id": "websearch", "name": "refresh", "params": {} } }
```

- 条目注入**不持久化**：布局文档里不出现插件条目；插件禁用/卸载即消失（用户布局不受影响）。
- 插件可自报状态点颜色（`plugin.status.report{state:"degraded"}`），条目显示黄点 + tooltip 原因。

### 7.6 iframe 宿主与 postMessage 桥

**加载**：`src = https://<appOrigin>/plugin-ui/<id>/<entry>?p=<view>&v=<manifest.version>`
（`appOrigin` = 宿主虚拟源；`appHandler` 已能拦截任意路径，见 [main.go](file:///e:/BizWorks/chonkpilot/src/lib/gui/main.go#L352-L364)。）

**桥协议**（iframe ↔ 父窗口）

```jsonc
// iframe → 父：请求
{ "ckPlugin": 1, "id": "r1", "type": "call", "method": "config.get", "params": {} }
// 父 → iframe：应答
{ "ckPlugin": 1, "id": "r1", "type": "result", "ok": true, "result": { "values": { "apiKey": "sk-****abcd" } } }
// 父 → iframe：推送（仅订阅的）
{ "ckPlugin": 1, "type": "event", "topic": "plugin-status", "payload": { "id": "websearch", "status": "running" } }
```

**允许的方法（闭集，父窗口校验）**

| method | 转发 | 说明 |
|--------|------|------|
| `config.get` | 父窗口 → `plugin-config-get`（server 直读） | 返回 `values`（密钥掩码）+ `fields` + `dirty` |
| `config.set` | 父窗口 → `plugin-config-set`（server 直写） | 只接受 manifest `config.fields[].key`；成功后广播 `plugin-config-changed` |
| `config.reset` | 父窗口 → 既有 `data-*` 删除覆盖（server 校验） | 回落默认值 |
| `data.read` / `data.write` | 父窗口 → `plugin-call{id,method:"data.read|write"}` → server §6.1 | 仍需过 `permissions.data` |
| `rpc` | 父窗口 → `plugin-call` → server → 插件 `plugin.ui.rpc` | `method` 必须在 `ui.rpc` 白名单 |
| `open-url` | 前端 `window.open`（WebView2 交系统浏览器） | 首次域名由用户确认，之后记入允许集 |
| `close` | 父窗口 | 关闭当前页签/抽屉/对话框 |
| `log` | 父窗口 → 插件日志文件 | 只写插件日志，限 4KB/条 |

**安全**

- 父窗口校验 `e.source === iframe.contentWindow`（每个 host 实例持有自己的 ref）；
- 不校验 `e.origin`（sandbox 无 `allow-same-origin` 时 origin 为 `null`），改用**实例绑定 + 请求白名单**；
- iframe **不暴露** `window.mq` / `/publish` / 任意 `data-*`；
- 页面资源只读、禁止 `../` 越界；`Cache-Control: no-store`。

### 7.7 静态资源服务

- 新路由：`GET /plugin-ui/<id>/<path...>` → `<pluginRoot>/webui/<path>`。
- 校验：id 必须在注册表；`path` 经 `filepath.Clean` 后仍在 `webui/` 内；拒绝 `.` 开头段与符号链接逃逸。
- MIME 按扩展名（html/js/css/svg/png/woff2/json）；无目录列表；`Cache-Control: no-cache, no-store, must-revalidate`。
- 共享 SDK：`GET /plugin-ui/_sdk/plugin.js`（宿主提供，版本随主程序；iframe 只需 `<script src="/plugin-ui/_sdk/plugin.js">`）。

### 7.8 布局持久化与单例

- 插件 view 的 `mode` 默认 `multi`（不同插件页可并存）；同 `plugin:<id>:<view>` + 同 `instanceKey` 仍按既有单例判定（`hasViewTarget`）。
- 布局文档只保存 `viewKey` + `instanceKey`；插件卸载后引用保留，`LfViewHost` 显示「视图不可用」（[LfViewHost.vue](file:///e:/BizWorks/chonkpilot/src/frontend/src/layout-free/views/LfViewHost.vue#L29-L35) 已有占位分支）。
- 抽屉 / 对话框目标要求 `containers` 含 `drawer` / `dialog`；否则命令层拒绝并提示（沿用 `command.reject.*` i18n）。

***

## 8. 样板插件：websearch（DeepSeek）

### 8.1 包结构

```text
src/initdata/capability/plugins/websearch/
  plugin.json
  chonkpilot-websearch.exe        # Go：通道客户端 + Streamable HTTP MCP（:0）+ 配置监听
  webui/config.html               # 配置页（Key + 检索参数 + 测试连接）
  webui/status.html               # 状态页（最近调用、错误、Key 状态）
```

### 8.2 配置项（`config.fields`）

| 键 | 类型 | 默认 | 说明 |
|----|------|------|------|
| `apiKey` | string / secret | — | DeepSeek API Key（必填，usr 级） |
| `baseUrl` | string | `https://api.deepseek.com` | 高级项；仅 https，host 必须在 `permissions.network` |
| `count` | integer | 10 | 默认返回条数（1–50） |
| `needContent` | boolean | false | 是否返回正文（影响上下文体积） |
| `timeoutSec` | integer | 30 | 请求超时（5–120） |

### 8.3 工具契约（插件自报）

`web_search`：

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `query` | string | ✅ | 1–128 字符；`site:` / `lang:` / `after:` / `before:` 原样透传 |
| `count` | integer | | 1–50，缺省取配置 |
| `need_content` | boolean | | 缺省取配置 |
| `ori_question` | string | | ≤128，供检索改写参考 |

返回（裁剪为模型可读）：

```json
{ "provider": "deepseek",
  "results": [{ "title": "", "url": "", "snippet": "", "summary": "",
                "content": "", "site_name": "", "date": "", "authority": 3, "score": 2.4 }],
  "count": 5 }
```

- `mcp.mode="http"`、`namespace="self_"` → LLM 可见名 `self_web_search`（契约名 `web_search`，与 `self_codegraph_*` 同口径）。
- `hot_tools:["web_search"]`、`idempotent:true`（只读，可安全重试）。
- **未配置 Key**：`tools/list` 仍暴露工具，调用返回可诊断错误「websearch 未配置 DeepSeek API Key：请在 插件 → 联网搜索 中填写」，状态栏黄点。

### 8.4 配置页交互

- `config.html` 首屏 `config.get` → 渲染表单（Key 掩码 `sk-****abcd`）。
- 「保存」→ `config.set`（只提交发生变化的键）→ 成功后提示「已保存，下一次调用生效」。
- 「测试连接」→ `rpc{method:"test"}` → 插件用 1 条 `query="DeepSeek"` 发真实检索（**UI 明确提示会计费**）→ 返回耗时 + 首条标题。
- 「恢复默认」→ `config.reset`。

### 8.5 DeepSeek 适配要点（插件内部，仅约束边界）

| 项 | 值 |
|----|----|
| Endpoint | `POST {baseUrl}/v1/tools/search` |
| 鉴权 | `Authorization: Bearer <apiKey>`；Key 只在内存与请求头出现 |
| 重试 | 仅 `408/429/5xx` 指数退避 2 次（1s/3s）；`400/401/402/403/404` 不重试 |
| 错误映射 | `401` → 「API Key 无效或未配置」；`402` → 「余额不足」；`429` → 「请求过于频繁」；超时/5xx → 「搜索服务暂不可用，已重试 N 次」 |
| 日志 | 只打 `status / elapsed / result_count`，**禁止**打印 query 全文与 Key |

***

## 9. 存量能力改造

### 9.1 `codebase`（codegraph + vfts 合二为一）

**目标形态**

| 现状 | 数量 | 目标 |
|------|:--:|------|
| `chonkpilot-codegraph-mcp-server.exe` + `chonkpilot-vfts-mcp-server.exe` | 2 | **1** 个 `chonkpilot-codebase.exe` 内的 MCP server（HTTP，9 工具） |
| `chonkpilot-codegraph-indexer.exe` + `chonkpilot-vfts-indexer.exe` | 2 | 同进程内 2 个索引协程（消费既有 `plugin.index.*` 队列） |
| 内嵌 hook `plugin-codegraph` + `plugin-vfts`（配置/注册/状态/去抖/文档探测） | 2 | 迁入插件；宿主不再编译这两个 hook |
| 9 个 executor exe | 9 | **迁移期保留**：codebase 可经新增 `plugin/v1/tools.exec`（宿主转发 core `/internal/exec`）复用「一工具一 executor」；或由内嵌引擎直接回答（二选一，见下） |
| 配置页 `CodegraphConfig.vue` + `VftsConfig.vue`（项目配置内两页签） | 2 | 1 个 `codebase` iframe 配置页（内部两区：代码语义 / 全文检索） |

**工具面不变**：`codegraph_symbol_search / get_symbol_info / callers / callees / get_dependency_graph / find_circular_deps / analyze_complexity / get_module_summary / vfts_query` 的**参数与返回**逐字保留；LLM 可见名保持 `self_codegraph_*` / `self_vfts_query`（`mcp.namespace:"self_"`）。

**关键设计**

- **工具执行路径（二选一，C3 时拍板）**：
  - **甲（推荐，改动最小）**：插件经 `plugin/v1/tools.exec` → 宿主 core `/internal/exec` 跑**单工具 executor**（与现状 `Deps.ToolExec` 同一链路；一次调用一个进程，取消即杀）。优点 = 与既有执行器同一实现、无需在插件内复制查询逻辑；缺点 = 每次调用仍有 executor 进程。
  - **乙**：插件内嵌引擎直接回答（不再经 executor）。优点 = 进程最少；缺点 = 查询实现要从 executor 抽到共享包，且要等 GATEWAY-2 L3（executor 合并）定型后再动。
  - 迁移期默认**甲**；L3 落地后再评估乙。
- **多 workdir 路由**：`origin:"builtin"` + HTTP → gateway 在 `tools/call` 注入 `_meta.chonkpilot.instance_id/work_dir`；插件按 `instance_id → workdir` 路由（沿用现有 codegraph per-workdir / vfts shared 两条会话池）。
- **索引**：插件消费 `plugin.index.take/ack/pending/workdirs`（既有），不再需要独立 indexer 进程与 `pluginhost` 监管。
- **zvec 单写锁**：合并为单进程后天然消解跨进程竞争；索引结束即释放写句柄（沿用现状）。
- **状态回写**：继续写既有 `codegraph.status` / `vfts.status` 键（前端 `IndexBadgeView` 等零改动）。
- **CGO 构建**：新 `build-plugins.ps1` 统一产出 `dist/desktop/capability/plugins/codebase/`（含 `zvec_c_api.dll`）。

**迁移阶段（每步可回退）**

| 阶段 | 内容 | 回退 |
|------|------|------|
| C1 | 加 `codebase` manifest + iframe 配置页；进程仍两套（菜单/页面聚合） | 删 manifest |
| C2 | 工具注册迁到插件（`mcp.mode="http"`），hook 只留配置/索引/状态 | 恢复 hook 注册 |
| C3 | 合进程：一个 `chonkpilot-codebase.exe`（MCP server + 双索引协程），删两个 indexer | `-legacy` 构建开关保留一个版本周期 |
| C4 | 删 `plugin-codegraph` / `plugin-vfts` hook 与两个引擎 exe | 随 C3 legacy 一起保留 |

### 9.2 `compress`

| 项 | 内容 |
|----|------|
| 形态 | `capability/plugins/compress/` + `chonkpilot-compress.exe`（`mcp.mode:"none"`，纯后台） |
| 订阅 | `session-compress`（既有触发事件，载荷含 `max_context_token` / `max_output_token`） |
| 能力 | `plugin/v1/session.snapshot.read|write`（新增写）、`llm.step`、`config.prj.read|write:compress.*`、`prompt.read|write:summary_prompt` |
| 算法 | 仍复用 `data.LocateZones` / `data.BriefFacadeMessages` / `EstimateTokens*`（Go SDK 直接 import 库，不复制算法） |
| 进度 | 继续用 `tool-notify{plugin:"compress"}`（既有），前端 `useCompressStatus` 零改动 |
| 配置页 | `webui/config.html`：`keep_full_max_turns` / `keep_full_max_tokens` / `compress_token_threshold` / `compress.subsession` + 摘要提示词编辑（读写 `capability/system/summary.md`）+ 队列状态 |
| 失败 | 摘要失败 = 不压缩（不降级）；上报 `tool-notify` 一次；行为与现状一致 |

> 迁移顺序建议放在 `codebase` 之后：compress 是每轮热路径，先让插件平台在 codebase（非热路径）上稳定。

### 9.3 构建与产物

- 新增 `build-plugins.ps1`：编译产品插件 → `dist/desktop/capability/plugins/<id>/`（exe + webui + plugin.json）；由 `build-desktop.ps1` 调用。
- `src/initdata/capability/plugins/` = 出厂源（manifest + webui；**exe 由构建产出，不入库**）。
- 卸载 = 删目录；内置插件目录随产物覆盖式同步（与 `capability/scenarios` 同口径）。

***

## 10. 消息与契约

### 10.1 插件通道（非 MQ）

§5 的帧协议**不走 MQ**（与 browser/desktop 插件的 `pluginproto` 并列）。因此**不触发 61 冻结**；但需新增 `docs/spec/60-reference/` 下的协议附录（建议并入 [62-命令行与参数](../spec/60-reference/62-命令行与参数.md) 或新建 `65-插件协议`）。

### 10.2 MQ 新增（前端 ↔ server）

> 现状：`ui-plugin-*` 仅在 [61 §预留主题族](../spec/60-reference/61-消息一览.md#L19-L21) 声明、**零实现**。本期改用 `plugin-*` 并删除 `ui-plugin-*` 预留族（等价于「新增主题 + 删除未实现预留」，**实施前需按 61 §变更口径报用户确认**）。

| 主题 | 方向 | payload | 说明 |
|------|------|---------|------|
| `plugin-list` | 前端 → server（call） | `{}` → `{plugins:[pluginView]}` | 初始快照 |
| `plugin-registered` | server → 前端（event） | `{plugin:pluginView}` | 注册/更新（含视图/状态栏） |
| `plugin-unregistered` | server → 前端（event） | `{id}` | 卸载 |
| `plugin-status` | server → 前端（event） | `{id,status,detail?}` | `discovered\|starting\|registering\|running\|degraded\|error\|stopping\|stopped\|disabled` |
| `plugin-set-enabled` | 前端 → server（call） | `{id,enabled}` → `{ok}` | 启停（同时摘/挂工具面） |
| `plugin-command` | 前端 → server（call） | `{id,name,params?}` → `{ok,result?}` | 状态栏/管理页命令 → 插件 |
| `plugin-config-get` | 前端 → server（call） | `{id}` → `{fields,values,dirty}` | 密钥掩码 |
| `plugin-config-set` | 前端 → server（call） | `{id,values}` → `{ok}` | 原子写 + 广播 |
| `plugin-config-changed` | server → 前端（event） | `{id,keys:[...]}` | 配置变更；同内容另经 `plugin.config.changed` 控制帧发插件 |
| `plugin-call` | 前端 → server（call） | `{id,method,params}` → `{ok,result}` | iframe 桥的数据/RPC 统一入口（§7.6） |
| `plugin-ui-event` | server → 前端（event） | `{id,topic,payload}` | 插件推送给自己的 UI |

`pluginView` 形状：

```jsonc
{ "id":"websearch", "name":"联网搜索", "version":"1.0.0", "icon":"search",
  "status":"running", "enabled":true, "product":true,
  "capabilities":["mcp","ui"], "detail":"",
  "views":[{"id":"config","title":"联网搜索","containers":["preview","tab","drawer","dialog"]}],
  "statusbar":[{"id":"websearch","zone":"right","icon":"search"}],
  "tools":["self_web_search"], "pid":12345, "started_at":"..." }
```

- `preview-tab-open`：**只增** payload 字段 `plugin` / `page`（`kind:'plugin-page'`）；主题不变。
- 禁用 = `plugin-set-enabled` → `plugin-status{disabled}` → 前端隐藏菜单/状态栏、MCP 注销。

### 10.3 实施时必须同步的文档

| 文档 | 同步内容 |
|------|---------|
| [61-消息一览](../spec/60-reference/61-消息一览.md) | 删除 `ui-plugin-*` 预留族；`plugin-*` 入正式表；`preview-tab-open` 增 `plugin`/`page`；`plugin-list` 等 payload 明细 |
| `61-messages.schema.json` + genmsg | 新主题与字段字典（`plugin_id` / `enabled` / `status`） |
| [2C-UI插件](../spec/20-modules/2C-UI插件.md) | 收敛为「本期实现 = iframe view + 预览页 + 管理页 + 状态栏」；bundle / JSON 装配移出 |
| [28-plugins](../spec/20-modules/28-plugins.md) | 新增「插件平台」章；内嵌 Hook 与 manifest 插件的关系；compress/codebase 迁移状态 |
| [25/26/27](../spec/20-modules/25-mcp-server.md) | 插件 MCP 注册（`plugin.mcp.register`）、`origin` 与 `_meta` 注入口径 |
| [31-窗口与布局](../spec/30-function-points/31-窗口与布局.md) | 动态 view / `containers` / 插件页签（WIN-0xx） |
| [64-配置项一览](../spec/60-reference/64-配置项一览.md) | `plugin.<id>.*` 配置键、`plugins.enabled` |
| [03-构建与部署](../spec/00-overview/03-构建与部署.md) | `build-plugins.ps1`、产物目录 |

***

## 11. 边界与异常

| 场景 | 行为 |
|------|------|
| manifest 非法 / exe 缺失 | 跳过该插件，`status=error` + 原因可见；不影响其他插件启动 |
| 同一 id 多级 | 最具体级整条覆盖（与 `capability/mcps` 一致） |
| 插件 spawn 失败 | 退避 1s→30s，最多 3 次 → `error`；入口保留（可手点重试/看日志） |
| 健康检查失败 | 连续 3 次 → 重启；重启成功后自动重注册；期间工具面摘除、菜单灰点 |
| MCP 工具名冲突 | 拒绝注册（保留先注册者），`status=error`，`detail` 含冲突工具名 |
| MCP 端口不是回环 | 拒绝注册（SSRF/越权面） |
| 配置写入非法 | 拒绝 + 字段级错误；旧值不变（原子写） |
| 密钥被写入非密配置 | manifest 校验期拒绝（`secret` 字段不进 `config.json`） |
| 插件 UI 加载失败 | 错误占位 + 重试 + 「打开日志」；不影响主应用 |
| iframe 发未授权方法 | 父窗口拒绝并记审计；不转发 |
| 插件崩溃 | 工具面摘除、UI 灰点；退避重启；`_meta` 路由的进行中调用失败（不重试） |
| 禁用插件时有进行中调用 | 先摘工具面（新调用失败），等 `stop.grace_ms` 后 kill；已发出的调用按失败返回 |
| 宿主退出 | `PluginManager.Stop` 按启动**逆序**：先禁新调用 → `plugin.shutdown` → 超时 kill；`external` 只注销不杀 |
| 插件目录被删 | 下次启动视为卸载：清注册表、广播 `plugin-unregistered`；布局引用保留 |

***

## 12. 重启恢复（71 §3.3 五问）

1. **状态落在哪**：manifest / 配置 / 密钥 / 启用表 / UI 快照 → 落盘（`capability/plugins/<id>/` + prjusr 快照）；进程 pid、连接、MCP 端口、会话池 → 内存（崩溃即丢，可重建）。
2. **是否受理即持久化**：配置保存**先原子落盘再应用**；启用/禁用先落启用表再执行；插件注册（视图/MCP）属于**运行态**，重启后由 manifest 重建（无需持久化）。
3. **非终态谁收口**：运行态不持久化 → 宿主启动后每个插件状态一律从 `starting` 重建（不存在「上次残留 running」）；每个 `starting` 受 `health.interval_ms × fail_threshold` 上限约束，超时 → `error`（可手点重试）；**不出现永久 `starting`**。
4. **重启后重建还是收敛**：`managed` 插件 **重建**（重新 spawn → hello/register → MCP 注册）；`external` 插件 **重连**，连不上则 `degraded`，UI 用快照保留入口；工具面只注册已连通的插件。
5. **副作用语义**：注册/注销幂等；配置写入幂等（同值不写）；工具调用默认**不自动重试**，仅 `idempotent:true`（如 `web_search`）允许自动重试；索引任务由 `plugin.index.*` 队列保证「至少一次」，插件按 `file_list` 幂等重算。

***

## 13. 落地阶段、测试、构建

| 阶段 | 内容 | 主要落点 |
|------|------|---------|
| P0 | manifest 模型 + 四级发现 + 校验 + `PluginManager`（复用 `pluginhost`）+ 启用表 | `src/lib/pluginmgr` · `src/lib/appwire` · `src/lib/gui/host` |
| P1 | 通道扩展：`plugin.mcp.register` / `plugin.ui.register` / `plugin.status.report` / `plugin.ui.rpc` / `plugin.command` / `plugin/data.*` / 配置读写 | `src/lib/llm/pluginbridge` · `src/lib/llm/server` |
| P2 | UI：`plugin-*` 消息 + `usePlugins` + 动态 view 注册 + `PluginPageHost` + 插件管理页 + 状态栏注入 + `/plugin-ui/` 静态路由 | `src/frontend/src/{composables,layout,views/codeview,views/plugins}` · `src/lib/gui` |
| P3 | websearch 样板（插件 + webui + MCP HTTP + 测试动作） | `src/initdata/capability/plugins/websearch` · `src/plugins/websearch` |
| P4 | `codebase` 迁移 C1–C4（含构建脚本 + 9 工具名快照比对） | `src/plugins/codebase` · `build-plugins.ps1` |
| P5 | `compress` 迁移（含 iframe 配置页 + 摘要提示词编辑） | `src/plugins/compress` |
| P6 | 文档收口 + 内嵌 Hook 只读展示（可选） | `docs/spec/*` |

**测试（全部 mq 驱动 + mq 断言；协议面可用桩 exe）**

1. manifest 校验 / 四级覆盖 / 非法目录跳过（L1）。
2. 桩插件（Go，UDS）注册 → `plugin-list` 含视图 + 状态栏；`tools-list` 含工具（L2）。
3. 禁用 → 菜单/状态栏消失 + MCP 注销 + 进程退出（L2）。
4. `plugin-config-set` → 落盘 → 重启后生效；密钥回显为掩码（L2）。
5. 崩溃 → 退避重启 → 工具面恢复（L2，含重启恢复用例）。
6. iframe 桥：非法方法被拒、`config.get` 掩码、`rpc` 白名单（前端单测 + L4）。
7. codebase：合并前后 9 个工具名与 schema **逐字一致**（L1 快照比对）。
8. websearch：stub DeepSeek 200/401/402/429 → 文案与重试次数（L1/L2）。
9. 动态 view：注册后 `viewKey` 可被 `open-view{tabs|drawer|dialog}` 打开（前端单测 + L4）。
10. 重启恢复：宿主重启后 UI 快照立即可用、`external` 插件标 `degraded`、重连后恢复（L2）。

**构建**

- 产品插件：`build-plugins.ps1`（由 `build-desktop.ps1` 调用）。
- 主程序改动：`.\build-desktop.ps1`（前端 + Go 一次）。
- 插件 MCP 契约无 `.tool.md`，不需要 `build-mcp-server.ps1`（除非改 core executor）。

### 13.1 与「组件独立化」的顺序（2026-10-22 定）

**结论：不先独立 `codegraph` / `vfts`。** §9.1 会把两者合并为**单个 `codebase` 插件**（形态由「MCP server module」变为「插件 exe + 插件协议」），先拆会导致**两次搬迁**且依赖边界全变。顺序如下：

| 序 | 动作 | 说明 |
|---|---|---|
| **S0**（可并行） | 发布 `chonkpilot-ignore`（零依赖，codebase 后续复用）· 清理空目录 `src/lib/cli` · 建 **9 工具名 / schema 快照基线** | 与本平台无冲突，先立「LLM 可见面」护栏 |
| **S1** | P0 → P3 | 平台 + 通道 + UI + `websearch` 样板；**末尾协议冻结** |
| **S2** | P4（`codebase` C1–C4） | 合并前后 9 工具名与 schema **逐字一致** |
| **S3** | 先发 **`chonkpilot-plugin`（SDK + 协议）v0.1.0**，再把 `codebase` 抽为独立仓（`require` SDK） | **此时才谈独立化**；避免 SDK 未定稿反复打 tag |
| **S4** | P5（`compress` 插件化）→ 再评估是否独立 | 方案建议放 codebase 之后 |
| **S5** | 工具面（`mcp-tools` + `mcp-server`）独立化再评估 | 动契约需单独设计 |

> 前置门槛：**主仓全量测试（L1/L2/L3 + e2e L4）全绿之后才开工 S1**（用户 2026-10-22 要求）。

***

## 14. 设计默认值（实施前确认）

以下 6 项已按默认值写进本稿；如无异议即按此实施（不阻塞开工），有异议再改。

| # | 事项 | 默认值 | 备选 |
|---|------|--------|------|
| 1 | 前端消息族命名 | `plugin-*`（删除未实现的 `ui-plugin-*` 预留族） | 保留 `ui-plugin-*`（需两套命名并存） |
| 2 | 插件捆绑 MCP 的默认方式 | `http`（回环 + Bearer，端口 0） | `tools`（无端口，但工具名固定 `self_`） |
| 3 | 产品插件工具名前缀 | `self_`（与现有 `self_*` 一致；契约名不带前缀） | `-`（原样，工具面出现裸名） |
| 4 | 插件配置页承载 | 预览区 iframe（主）+ 同页可注册为抽屉/对话框 view | 仅预览区 |
| 5 | compress 迁移时机 | codebase 稳定后（P5） | 与 codebase 并行 |
| 6 | 插件 UI 静态资源 | 宿主 `/plugin-ui/<id>/` 只读提供 | 插件自带 HTTP（多端口，不推荐） |

***

## 15. 关联文档

- [2C-UI插件](../spec/20-modules/2C-UI插件.md) · [28-plugins](../spec/20-modules/28-plugins.md) · [25-mcp-server](../spec/20-modules/25-mcp-server.md) · [26-mcp-gateway](../spec/20-modules/26-mcp-gateway.md) · [27-mcp-tools](../spec/20-modules/27-mcp-tools.md) · [29-codegraph](../spec/20-modules/29-codegraph.md) · [2B-vfts](../spec/20-modules/2B-vfts.md)
- [31-窗口与布局](../spec/30-function-points/31-窗口与布局.md) · [61-消息一览](../spec/60-reference/61-消息一览.md) · [64-配置项一览](../spec/60-reference/64-配置项一览.md) · [71-编程规范](../spec/70-conventions/71-编程规范.md)
- [工具面重构与Gateway二次优化设计方案](../tool-plane/工具面重构与Gateway二次优化设计方案.md)
- [DeepSeek Search 文档](https://api-docs.deepseek.com/zh-cn/guides/search/)（外部参考，不进 spec）
