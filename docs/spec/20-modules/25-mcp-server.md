# 25 · src/lib/mcp-server（MCP 契约服务）

> 状态：✅ 与代码一致（2026-10-21 GATEWAY-2 L1/L3/C 同步；见 §0 现状订正）
> 关联：[10-分层与依赖](../10-architecture/10-分层与依赖.md) · [26-mcp-gateway](26-mcp-gateway.md) · [27-mcp-tools](27-mcp-tools.md)
> 代码目录：lib = `src/lib/mcp-server/`（`server/` + `contracts/`）；**exe 外壳 = `src/others/mcp-server/`**（`main.go`/`service.go`）
> ⚠️ **形态已收敛**：**无** `Server` 结构体 / `New(cfg)` / 分类端点路由 / 进程内注册表（`Register/Get`）/ `Instance/SkillsList/SkillsGet` / `NewInProcessTransport`。

---

## 0. 现状订正（2026-10-21，GATEWAY-2 L1 / L3 / C / L7）

> 本节为准，**与下文冲突时以本节为准**；下文未删除的旧描述（self / dir 节点、`dsl_run`、`agentbox`、一工具一 exe）均为历史口径。

- **① DSL 已退役（L1）**：`dsl_run` 契约、`src/lib/dsl`、`build-dsl-executor.ps1` 与 `filesys_run` / `browser_run` / `desktop_run` 三个容器工具**全部删除**；执行器集合 = `chonkpilot-tools-executor.exe`（file 域）+ `chonkpilot-script-executor.exe`（`script_run`）+ `chonkpilot-web-executor.exe`（`web_fetch`）；browser / desktop 改为**常驻插件**（UDS，非 executor、不接 gateway）。
- **② 内置 server = 单进程 `core-mcp-server`（L3）**：`chonkpilot-core-mcp-server.exe`（`cmd/core-mcp-server`）是**唯一内置 MCP server**，由宿主经 `coretools.Supervisor` 拉起；工具**不再一工具一 exe**：
  - `runtime: builtin` 的契约（**file 组 11 个** + **meta 组 3 个** + 其他 `category` 的 builtin 契约）由 `dispatchBuiltin` 在 **core 进程内**执行（`server/builtin.go`）：文件域走 `mcp-tools/toolset`，`meta` 组走 `toolmeta.Run(GroupMeta)`，`server`（domain）组走 `toolmeta.Run(GroupDomain)`；每 handler 外层 `recover()` → panic 转 `isError`，绝不冒泡到主循环。
  - `runtime: ../../executors/<x>.exe` 的契约仍 **spawn 单个 executor**（`executor.go`，`exec.CommandContext` + `winproc` 隐藏窗口 + ctx 取消 → 杀该次进程树）。
  - `internal: true`（codegraph 8 / vfts 1）只经 `POST /internal/exec` 执行，不进任何 MCP 工具面。
- **③ 契约来源三层 + embed（C）**：`server/contractsource.go` 的 `Source{Dir|FS}` 支持多层合并，**同名（同类型+同原语名）后者覆盖前者**并输出 `Shadowed` 审计。现行装配：**磁盘契约根存在 → 磁盘单层**；**缺失 → 内置 `//go:embed all:contracts`**（构建期从 `src/initdata/capability` 覆盖同步）。更高层（user / project / prjusr capability 根）由各自 dir 下游经 gateway 前缀接入，不在本函数合并。
- **④ 沙箱已下沉 gateway 进程级（L7）**：`agentbox` 包与 `Config.SecurityDirs` / `ToolSandbox` / `callTool` 内注入 `CHONKPILOT_SANDBOX` **全部删除**；写隔离由 **gateway 在 spawn stdio 下游时**以 `src/lib/core/sandbox`（Windows **Low IL 令牌 + Job Object**）实施，**只限写、不改 DACL、读不受限**，允许写目录须先 `EnsureLowIL` 打标（缺省未打标 = 子进程只能读 = fail-closed）。应用层路径黑名单随之移除（详见 [14](../10-architecture/14-安全域-agentbox.md) · G-75）。

---

## 1. 职责与边界

- **一句话**：把「契约目录」（四原语 `.md`）注册到一个**装配方自建的官方 go-sdk `mcp.Server`** 上；工具调用时 spawn executor exe 执行。
- **做**：契约解析（四原语）、注册（tools→`AddTool`；prompts/skills→`AddPrompt`；resources→`AddResource`）、`tools/call` 执行链（spawn + 输出封装）、独立 exe 三种运行形态。
- **不做**：不建容器/实例/注册表/传输层（go-sdk `mcp.Server` 由装配方传入）；不做沙箱；不持持久状态。
- **单体链路中的角色**：`RegisterContracts` 由**装配方**在**自建的官方 server 实例**上调用 —— 单体链路 = `src/lib/llm` 的 `Server.New()` 先 `ms := mcp.NewServer(...)`（`src/lib/llm/server/server.go:236`）再 `mcpms.RegisterContracts(ms, root, cfg)` 扫描注册（`server.go:239-243`），随后该 `ms` 作为 `Params.MCPServer` 交 gateway；本模块因此以 **lib 形态**参与（不自建 instance、不持有它）。另一路装配方 = gateway 的 **dir 目录节点**自建 server 后同法调用（`src/lib/gateway/gateway/dirnode.go:38-41`）。装配链全貌与阶段划分见 [26-mcp-gateway §4.0](26-mcp-gateway.md)。

---

## 2. 形态与部署

| 项 | 值 |
|----|----|
| lib | `RegisterContracts(srv *mcp.Server, root string, cfg *Config) error`（唯一导出构造函数） |
| exe | `chonkpilot-mcp-server.exe`（console） |
| 形态（必须显式指定） | `--http[=addr]`（默认 `127.0.0.1:5700`，端点 `/mcp`）· `--stdio` · `--service install\|remove\|run`（Windows 服务） |
| 其它 flag | `-root`（缺省 exe 目录/`capability`）· `-timeout` · `-config`（缺省 exe 同目录 `config.json`） |
| 构建 | `build-mcp-server.ps1` |

### 2.1 core-mcp-server（2026-10-19，工具唯一前门）

| 项 | 值 |
|----|----|
| exe | `chonkpilot-core-mcp-server.exe`（源 `src/lib/mcp-server/cmd/core-mcp-server`，console、常驻） |
| 拉起 | 由 `chonkpilot-server` 在**插件桥就绪后**拉起（desktop 宿主 / server 形态共用 `coretools.Supervisor`）；固定 loopback 端口 + 随机 Bearer token；异常退出 1s→30s 退避重启，连续 3 次失败放弃 |
| 部署 | `<exeDir>/mcps/core-mcp/chonkpilot-core-mcp-server.exe`（desktop / server 同布局） |
| 接口 | MCP **Streamable HTTP**（`POST /mcp`）+ `GET /healthz` + `POST /internal/runtime`（运行期执行配置）+ `POST /internal/exec`（插件工具委托执行面，2026-10-19） |
| 鉴权 | `Authorization: Bearer <token>`（constant-time；token 仅宿主与 gateway 可见） |
| 调用上下文 | `X-Chonk-Instance-Id` / `X-Chonk-Session-Id` / `X-Chonk-Turn-Id` / `X-Chonk-Call-Id` / `X-Chonk-Work-Dir` / `X-Chonk-Data-Dir` / `X-Chonk-Purpose` / `X-Chonk-Scope` / `X-Chonk-Deadline-Ms` / `X-Chonk-No-Timeout`；协议 `_meta.chonkpilot` 为兼容回落（**MCP 会话建立后头不随每次请求更新，gateway 路径以 `_meta` 为准**） |
| 执行 | **每次 tools/call spawn 一个 executor 进程**（一工具一 exe）；ctx 取消/超时 → `winproc.KillTree` 只杀该次 executor 进程树，绝不杀 core 本身 |
| 工具面 | MCP `tools/list` 只暴露**非 internal** 契约（当前 13 个 = app 根 5 + meta 3 + domain 5）；`internal: true`（codegraph 8 / vfts 1）只进 core 内部执行表，由插件按启用状态注册给 gateway，不进 MCP 工具面 |
| 契约标记 | `meta.internal: true` = 仅 `/internal/exec` 执行（不进 core `tools/list`）；`meta.core-only: true` = 仅 core 注册执行（进程内 mcp-server 跳过，避免与 core 重复注册） |
| 配置 | 启动参数 `--root/--work-dir/--data-dir/--config/--plugin-addr/--plugin-token`；usr/prj 热生效经 `POST /internal/runtime`（`mcpms.RuntimeConfig`：timeout/并发/解释器/Chrome/skip_dirs/tool_async/tool_sandbox/security_dirs） |
| gateway 接入 | 经既有方法面 `servers/register`（`mcp-servers-register`）登记为内置 HTTP 下游：`name=core`、`namespace=self_`、`origin=builtin`、`mcp_server.headers.Authorization`；暴露名保持 `self_<tool>`，**不新增 MQ 主题**。**取消语义**：core 为纯远程（proxied）下游 → gateway `Terminate` 为协作式 no-op（取消经执行池 ctx 送达 HTTP 请求），**不重建共享连接**（见 [26 §核心下游](26-mcp-gateway.md)） |

---

## 3. 对外接口

### 3.1 契约格式（H1 分区 + 代码块，**v2**，2026-10-14）

> **变更**：由旧「`# 标题` + `[meta]`/`[description]`/`[parameters]`/`[arguments]`/`[content]` 方括号分区」**全面切换**为「**H1 分区 + yaml/json 代码块**」。**旧格式不再兼容读**（一次性迁移全部出厂资产与工具契约）。**单一解析源**：全仓仅保留一份解析/序列化实现（见 §3.4），旧的多处重复实现全部删除。

````text
# 基本信息
```yaml
name: <原语名>          # 唯一标识（= 文件名去后缀；保存时校验同级同类型唯一）
title: <可读标题>        # 原「首行 # 标题」
<类型专属键>             # 见下
```

# 摘要
<description 文本>        # 原 [description]

# 参数                    # 仅 tool / prompt
```json
{"type":"object","properties":{},"required":[]}
```

# 参考                    # 仅 skill / agent
```yaml
skills:
  - <name>
tools:
  - <name>
```

# 委托                    # 仅 agent（原 meta delegate）
<委派条件文本>

# 正文
<markdown 正文>          # 原 [content]；必须是最后一个分区，其后整段原样归属正文
````

| 后缀 | 解析分区 | 注册 |
|------|---------|------|
| `.tool.md` | 基本信息 + 摘要 + 参数 | `srv.AddTool` |
| `.skill.md` | 基本信息 + 摘要 + 参考 + 正文 | 转 `PromptDoc` + `_meta.type=skill` → `AddPrompt` |
| `.prompt.md` | 基本信息 + 摘要 + 参数 + 正文 | `AddPrompt`（`_meta.type=prompt`） |
| `.resource.md` | 基本信息 + 摘要 + 正文（或 meta `path` 外部内容文件） | `AddResource`（`uri` **隐式派生**；`mimetype` 有 `path` → 按该文件后缀，否则 `text/markdown`） |

**分区规则**

- 分区名固定集合 = `基本信息 / 摘要 / 参数 / 参考 / 委托 / 正文`；按序出现。**`正文` 必须是最后一个分区**——其后出现的任何 `#` 行均属正文文本，不再视为分区（兼容正文 markdown 自带 H1）。
- 除 `正文` 外，出现非固定分区名的 H1 → **解析报错**（不静默吞并）。
- 分区缺失 = 对应字段为空（`摘要`/`参数`/`参考`/`委托` 可缺省；`基本信息` 必存在且含 `name`）。

**基本信息（yaml）类型专属键**

- `tool` → `runtime`·`category`·`async`·`async-threshold`·`timeout`·`args`·`output`·`hot`（字段语义同旧 `[meta]`，见 [72 §2](../70-conventions/72-工具开发规范.md)）
- `agent` → `roletag`·`ismain`·`llm`；**名单类引用入「# 参考」分区**（`skills` / `tools` / `resources`，每项 = 同类型原语的 `name`），**委派条件入「# 委托」分区**（2026-10-14：不再落「基本信息」）。🆕 **2026-10-15（D-58）**：**场景主 agent**（内联）经 `data-scenario-save/load` 的 `agents[]` 落盘，其 `skills` / `resources`（**可选 `string[]`**）与 agent 原语**同形**（同落「# 参考」分区 `refs.skills` / `refs.resources`）；`agents[]` 为**字段增补**，`data-scenario-*` 主题零增删。
- `skill` → 无必填键；`prompt` → 无（参数走「# 参数」）；`resource` → 无（`uri` 隐式）；**可选 `path`** = 指向**外部内容文件**（相对契约文件目录或绝对路径）——有则资源**无「正文」**、内容取自该文件、`mimetype` 按其后缀识别

**治理规则**

- **`name` 必填**：`基本信息` 至少保留 `name` 一个键，**不可删**；其余键按类型可增删。**唯一性 = 同一可见集（app ∪ usr ∪ prj ∪ prjusr）内、同类型不重名**（`app` 已有 `abc` → 其下 `usr/prj/prjusr` 不得再有 `abc`；不同项目之间可同名）。
- **隐式字段**（**不写盘、界面不显示、仅在注册 MCP 时提供给消费端**）：`resource.uri` = `mcp://chonkpilot/resources/<name>`；`mimetype`：**有 meta `path`（外部内容文件）→ 按该文件后缀识别**（`MimetypeOfFile`，确定性表，未收录 → `application/octet-stream`），否则 = `text/markdown`。**外部文件为二进制时**资源内容经 MCP `blob`（base64）返回，文本经 `text`（2026-10-14）。

**tool「基本信息」键**（v2：写在 `# 基本信息` 的 yaml 块，非旧 `[meta]`）：`runtime` · `category` · `async`(auto\|always\|never\|manual，缺省 auto) · `async-threshold` · `timeout` · `args` · `output`(stdout\|code\|file，缺省 stdout) · `hot`。**工具标题** = 「基本信息」的 `title` 键（v2；原「首行 H1」→ `Tool.Title`，见 [72 §2](../70-conventions/72-工具开发规范.md)）。**`timeout`（执行硬上限）取值语义**：键**未设置**（缺失 / 空串）→ **回落**全局（executor 回落 prj `timeout_sec` / 内置 300s）；显式 **`0` / `-1` = 无上限**（`noLimit`，不设 `WithTimeout`，永远等、可取消）；正数 = 该值。**键显式声明即透出 `_meta.timeout`（含 `0`/`-1`）**，未设置则不写。**系统工具（`category=server` / `meta`）不单独设执行硬上限** → 契约写 `timeout=0`（**`0` = 无上限**，非"回落"）。权威口径见 [18 §3.8](../10-architecture/18-工具异步超时与取消.md)。
**已删除死 meta**：`runtime-version` · `version` · `find` —— 解析与 `_meta` 透出均已移除，契约文件不再书写；**`title` 亦为死 meta 键**（不解析、不书写），工具标题链路 = **「基本信息」`title` → `Tool.Title`**。
仅兼容读：`entry`（已废弃，解释器类遗留）；`hot` 吸收旧 `llm`。
透出到工具 `_meta`：`hot` / `category` / `async` / `async-threshold`（仅 `>0`） / `timeout`（**键显式声明即透出，含 `0`/`-1` = 无上限**）（+ gateway 注入 `server`；`title` 走 `Tool.Title`，**不进 `_meta`**）。

> **目录资产注册 payload 的 `path`**：`.prompt.md` / `.resource.md` 解析出的 `PromptDoc` / `ResourceDoc` 仍是**静态内容**（`[content]`）；gateway 侧目录资产注册面 `prompts/register`（`{name, description, arguments?, content?, path?, asset_kind?}`）与 `resources/register`（`{name, uri?, description?, mimetype?, content?, path?}`）**新增可选 `path`** = 运行时内容来源路径，**有则该资产内容按需读盘、不驻留 `content`**（无 `path` 时仍用 `content` 兼容兜底，`catalogAsset.Path` + `assetContent()`）。**payload 明细以 [61 §5.1.1](../60-reference/61-消息一览.md) 为准**（本模块仅消费同一契约根扫描出的原语，5 字段均为 gateway `regMsg` 字段）。

> **文本占位**：`*.prompt.md` / `*.skill.md` 的「# 正文」支持 `{{toolchain.<key>}}`（key ∈ `java`/`python`/`node`/`go`/`rust`/`c`/`chrome`）——`makePromptHandler` 渲染时按 `Config.Toolchain`（装配层注入的 usr `*Path` 值）替换；已知 key 未配置 → 空串、未知 key → 原样保留、不影响 `{{arg}}` 与 DSL `{{env.X}}`（实现 `server/toolchain.go ReplaceToolchain`，见 [21-llm-server](21-llm-server.md)）。

### 3.2 config.json

`root` · `timeout_sec`（默认 300）· `max_concurrency`（默认 16）· `defaults{skip_dirs, ignore_files, fileext}`（`skip_dirs` 默认 `.git/.svn/node_modules/.trae/.chonkpilot/__pycache__/.venv/venv/build/dist/.next/.nuxt`）；`Apply` 为浅合并（指针字段非空才覆盖）。

### 3.3 契约目录清单（`contracts/`）

- `prompts/core/code_review.prompt.md`
- `resources/{core/core-overview, go/webview2-frameless, ui/frontend-drag-resize}.resource.md`
- `skills/core/{agent-prompt-rule, debug, explore, sandbox-escape}.skill.md`
- **本目录无 `.tool.md`**（工具契约在 [27-mcp-tools](27-mcp-tools.md)，部署时合并到同一 `capability/`）。

### 3.4 单一契约解析器（v2，2026-10-14）

> **背景（现状核查结论）**：v1 的方括号分区解析在仓库里存在 **4 份互不调用的重复实现**——data 层 `capfs`（`ParseDoc/BuildDoc`）、mcp-server `contract`（`splitSections/parseMeta/firstHeading`）、llm-server `domainmd`（`parseSectionMD`）、mcp-tools `cli`（`firstTitle`）——仅靠注释「与另一处保持一致」人工同步，边界行为（未知分区 / `arguments` vs `parameters` / 标题缺失）无统一保证。

- **唯一实现**：收敛为**单一契约解析包**（H1 分区解析 + 序列化 + 模板 + 「文件名 ↔ 类型」映射 + 隐式字段派生）。上述 4 处**全部改为调用它，旧实现一律删除**（无兼容分支）。
- **消费方**：data 层读写、mcp-server 注册扫描、llm-server 域工具契约读取、mcp-tools CLI `--help`、gateway 目录资产加载，均复用同包。
- **前端**：`utils/agentRef.js` 等仅做「领域模型 ↔ 契约字段」映射，不自行解析分区；序列化以同包规则为准。
- **保存即刷新（保持现状，2026-10-14 裁定）**：保存后**由 fswatcher（capwatch，去抖 60ms）隐式热生效**——**不新增显式刷新消息**（见 §4.4/§7）。

---

## 4. 内部控制流

### 4.1 注册（`RegisterContracts`）

```text
root 缺失 → 空注册返回 nil
sem = make(chan, cfg.MaxConcurrency)
tools   → loadTools → buildTool  → makeToolHandler(cat,name) → srv.AddTool
prompts → loadPrompts → buildPrompt + setPromptType("prompt") → srv.AddPrompt
skills  → loadSkills → 转 PromptDoc + setPromptType("skill")   → srv.AddPrompt
resources → loadResources → AddResource（uri 缺省补齐）
```

`makeToolHandler`：取信号量（`sem <- struct{}{}`）→ 反序列化 arguments → `callTool` → 统一 `{status,...}` 文本；失败置 `IsError`。
`makePromptHandler`：先按调用实参替换 `{{arg}}`，再替换 `{{toolchain.<key>}}`（能力原语文本里的工具链路径占位）。`makeResourceHandler`：静态内容。

**注册过滤（2026-10-19）**：`RegisterContracts` 跳过三类契约 —— ① `category=server` 且**无 `runtime`**（无 executor 可执行；**当前无此类契约** —— `dsl_run` 已于 2026-10-19 外移为 `runtime` executor）；② `core-only: true`（meta 3 / domain 5 / **`dsl_run`**，仅 core 注册执行）；③ `internal: true`（codegraph 8 / vfts 1，仅 `/internal/exec` 执行，不进任何 MCP 工具面）。有 `runtime` 的 `category=server`（domain + `dsl_run`）仍作为 executor 加载。

### 4.2 执行（`callTool`）

```text
defaults 与 args 合并（客户端显式覆盖默认）
  → {RAW-INPUT-FILE} 占位 → 建临时 JSON 文件 ck-call-*.json
  → {RESULT-OUTPUT-FILE} 占位（仅 output=file）→ 建输出文件
  → buildArgv：interpreterArgv（python/node/bash/sh/ruby/perl/cmd/cscript/java…）
               resolveEntry / resolveRuntime（绝对 → ~ → 相对契约文件目录 td.Dir）
               tokenizeArgs（支持引号分组）
  → exec.CommandContext(ctx2, argv...)   # ctx2 = **请求 ctx**（继承；有硬上限时 WithTimeout 包一层）
  → cmd.SysProcAttr = winproc.SysProcAttr()  # Windows：HideWindow + CREATE_NO_WINDOW（不建控制台窗口，见 24-lib）
  → stdout 按 output 模式封装：
        stdout：JSON 原样透传；纯文本封装 {status,output}
        code  ：按退出码判定
        file  ：fileThreshold=200KB；≤阈值且合法 UTF-8 且无 NUL → 直返内容，
                否则"结果已保存至 <path>（N 字节），用 filesys_run READ 提取"
```

- **不设置 `cmd.Dir`**（子进程继承宿主 cwd；仅契约 runtime/entry 按契约目录相对解析）。
- **ctx 继承请求（B3 已落地）**：`callTool` 的 `ctx2` **派生自请求 ctx**（原为 `context.Background()`）——取消经 in-memory 传输的 `notifications/cancelled` 取消 handler 请求 ctx，`exec.CommandContext` 随之 kill 子进程（「收到取消 → 真停」写在执行体侧，见 [18 §3.7](../10-architecture/18-工具异步超时与取消.md)）。有硬上限时 `ctx2 = WithTimeout(请求 ctx)`；显式 `0`/`-1`（无上限）时 `ctx2 = 请求 ctx`（不设超时点，取消仍可达）。
- **调用上下文（R-11 二次升级）与子进程注入**：调用上下文（`instance_id`/`work_dir`/`data_dir`）经 MCP `tools/call` 的 **`params._meta`** 命名空间 `chonkpilot` 透传（`server.CallContext` / `callContextFromMeta` / `CallContextMeta`；**不进 tool arguments**）。**上下文存在但 instance 为空 → 工具执行失败（异常）；完全无上下文（外部直连 stdio/HTTP 客户端）→ 不注入、不报错**。spawn executor 时 `Config.executorEnv` 把 `CHONKPILOT_INSTANCE/WORKDIR/DATADIR/INTERPRETERS` 与 `CHONK_CHROME` 注入**子进程环境变量**（继承宿主环境前**剥离**残留 `CHONKPILOT_*`）。`defaultsMap` 只注入 `skip_dirs`/`ignore_files`/`fileext`（原 `_workdir`/`_datadir`/`_instance`/`_interpreters` 注入已移除）。`Config.SetContext` 已删除。
  - **来源边界**：本仓 mcp-server（内嵌 self / dir 节点 / standalone 自有部署）在 gateway 侧标注 `Origin=builtin` → 收到 `_meta["chonkpilot"]`；第三方（用户定义）server 为 `Origin=user`，gateway 不向其携带 `_meta`，故其永远拿不到 `CHONKPILOT_*`（判定见 [26-mcp-gateway](26-mcp-gateway.md) 来源标注段）。
- **agentbox 沙箱（被 spawn 执行体级）**：`Config` 增 `SecurityDirs []agentbox.Rule`（prj `security-*` 换算的「可读 / 可写目录（递归）」）与 `ToolSandbox`（usr `tool_sandbox`，**管理单位 = 被 spawn 的执行体**：内置 executor 按 **exe 基名**、外部 MCP 按 **mcp 名**；**2026-10-12 由 executor 类别 core/desktop/browser 重整**，旧形状读取时迁移），装配层经 `SetSecurityDirs`/`SetToolSandbox` 注入（gateway `SetSandboxOverrides` 直写共享 Config）。`callTool` 按 **被 spawn 执行体的 exe 基名**（解析后 runtime `argv[0]` 的 `filepath.Base`）查开关，命中即 `executorEnv(cx, policy)` **额外注入 `CHONKPILOT_SANDBOX`**（策略 JSON）→ 执行器侧 `src/lib/agentbox` 强制拦截越界读写，并记一行日志 `[mcp-server] agentbox 隔离生效：unit=… tool=… dirs=…`。**未配置 = 不注入 = 不启用隔离**（默认兼容）；**旧工具暴露名形态不再生效**。见 [14 §7](../10-architecture/14-安全域-agentbox.md) · [64 §3](../60-reference/64-配置项一览.md)。
- **执行硬上限与 ctx 语义（已落地，详见 [18-工具异步超时与取消](../10-architecture/18-工具异步超时与取消.md)）**：`resolveExecTimeout` 硬上限（usr `hard_timeout` 优先于契约 `timeout`，每级「未设置」才回落；显式 `0`/`-1` = 无上限 → 不设 `WithTimeout`）**保留**；`ctx2` 继承请求 ctx（B3）→ gateway 经 ctx 下沉 cancel 可被执行体侧消费（**kill 留在本模块 lib**，决策 42 §2 (241)）。
- **`POST /internal/exec`（core 内部执行面，2026-10-19）**：插件（codegraph / vfts）把 LLM 工具调用委托给 core 时使用 —— 载荷 `{tool, args, context{instance_id,session,turn,tool_call_id,work_dir,data_dir,purpose}, deadline_ms, no_timeout}`，`context` 与协议 `_meta.chonkpilot` 同字段名；core 走**与 `tools/call` 完全相同的 `ExecTool.Call`**（spawn 单个 executor、`KillTree` 取消），返回 `{text, isError}`；未知工具 404、上下文非法 400、请求 ctx 取消/超时 408。`internal: true` 的工具只能经此执行面调用（不进 MCP `tools/list`）。
- **调用上下文扩展（2026-10-19）**：`CallContext` 增 `SessionID` / `TurnID` / `ToolCallID`（`_meta.chonkpilot.{session,turn,tool_call_id}`；`CallContextMetaFull` 与 `MergeCallContext` 合并）；`executorEnv` 注入 `CHONKPILOT_SESSION` / `CHONKPILOT_TURN` / `CHONKPILOT_TOOL_CALL_ID`（供 domain executor 经插件桥回报任务/工具结果）。

---

## 5. 数据结构与存储

- 无持久存储；契约在构建/启动时加载进 `mcp.Server` 注册表（内存）。
- 临时文件：`ck-call-*.json`（入参）、`{RESULT-OUTPUT-FILE}`（输出）。

---

## 6. 依赖

- **上游**：`src/lib`（mq/winsvc/winlog/exedir）、官方 `modelcontextprotocol/go-sdk`。
- **下游**：server（内嵌 `RegisterContracts`）、gateway（`dirnode` 扫描 dir 契约根）、独立部署。

---

## 7. 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| 不持容器/注册表 | 只提供 `RegisterContracts`，`mcp.Server` 由装配方传入 | 装配方（server/gateway）决定单/多 server 与生命周期 |
| skills 复用 prompt 通道 | `_meta.type` 区分 | skills 是扩展方法，go-sdk `HandleMessage` 无注册点 |
| `category` 仅 meta | 不是端点路由 | 单 `/mcp` 端点 |
| 执行 ctx 继承请求 | `ctx2` 派生自请求 ctx（有上限时 `WithTimeout`；**显式 `0`/`-1` 无上限 → `ctx2` = 请求 ctx，不设超时点**）；取消经 ctx 下沉 kill 子进程（B3） | 「收到取消 → 真停」写在执行体侧，kill 通道留在本模块 lib（决策 42 §2 (241)） |
| 超长结果落文件 | 200KB 阈值 | 避免撑爆 LLM 上下文 |

---

## 8. 场景与边界

- `root` 缺失 → 空注册（不报错）。
- output=file 但 args 无 `{RESULT-OUTPUT-FILE}` → 直接报错。
- 超时 → `timeout after %ds`；取消 → `cancelled`；**显式 `0`/`-1`（无上限）→ 超时分支不触发，子进程运行至结束，或随请求 ctx 取消被 kill（→ `cancelled`）**。
- 服务形态：`--service run` 需 `winsvc.IsWindowsService()`；日志切 Windows 事件日志（`winlog`）；HTTP 前台 Ctrl+C 优雅关闭（5s）。

---

## 9. 现状与待办

- `.agent.md` 后缀常量已摘除（`*.agent.md` 扫描分支从未实现）。
- **prompt 类型标注**：本模块 `setPromptType` 写入的 `_meta.type` **实际取值仅 `prompt` / `skill`**（`*.prompt.md`→`prompt`、`*.skill.md`→`skill`；`server.go:162-172`）；**`agent` 资产不属本模块扫描面**，且 **agent 只「注入」不「注册」** —— llm server 侧从 **app 级场景**（`<exeDir>/capability/scenarios/*/`，经数据层门面读取）取其 agent，**仅登记到内存表**（`registerDomainAgents`）供校验与子轮提示词回落，**不经** gateway `prompts/register{asset_kind:"agent"}` 注册为资产，**亦不再** `//go:embed contracts/agents/*.agent.md`（原 embed 读法已撤，见 `src/lib/llm/server/domainmcp.go`）。**`prompts/list` 仅返回 `type=prompt|skill`**（`.agent.md` 解析**不适用**）。
- **已删除死 meta**：`runtime-version` / `version` / `find`（解析与透出全删）；仅**兼容读**：`entry`（已废弃）。`title` 保留（H1 标题链路）。
- **一工具一 exe 全量完成（2026-10-19，G-68 第二步）**：app 根 5 + codegraph 8 查询 + vfts_query + meta 3 + domain 5 = **22 个 executor**（core 日志 `executors loaded: 22（exposed=13）`）。`chonkpilot-server-tools.exe` 已删除；domain/meta 契约迁至 `src/initdata/capability/tools/{meta,server}/` 并标 `core-only`；codegraph/vfts 查询契约标 `internal`，由插件 `tools.register` 按启用状态暴露、执行委托 `POST /internal/exec`。
- **取消语义保障**：core 为 gateway 的纯远程（proxied）下游，`Terminate` = 协作式 no-op（见 [18 §6](../10-architecture/18-工具异步超时与取消.md)）；L4 `run_tool_async.py` 11/11（含 E/F/G/H/J：detach、转异步、待裁决恢复、层权威行、cancel_on_timeout 无孤儿）。

---

## 10. 关联测试

`src/test/chonkpilot-mcp-server/unittest/server_test.go`：
`TestListToolsContracts` · `TestPromptSkillMeta` · `TestListResources`（经公开包 `mcp-server/server`，把本模块 skills/prompts/resources 与 mcp-tools 工具契约合并到临时根，官方 client in-memory transport 断言）。
