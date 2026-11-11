# 25 · src/lib/mcp-server（MCP 契约服务）

> 状态：⚠️ **部分过时（2026-11-04 核对）** —— 契约解析 / 注册 / builtin 进程内执行 / spawn executor / ctx 取消主线与代码一致；**「core 由 `coretools.Supervisor` 拉起」「agentbox 沙箱」「22 个 executor」三类描述已失效**，见 §0 现状订正
> 关联：[10-分层与依赖](../10-architecture/10-分层与依赖.md) · [26-mcp-gateway](26-mcp-gateway.md) · [27-mcp-tools](27-mcp-tools.md)（🗄 已退场，仅留档）
> 代码目录：lib = `src/lib/mcp-server/`（`server/` + `server/contracts/`；根 `contracts/` 为**空目录**，embed 副本在 `server/contracts/`）；**exe 外壳 = `src/others/mcp-server/`**（`main.go`/`service.go`）；**core exe = `src/lib/mcp-server/cmd/core-mcp-server/`**（`--stdio`/`--http`）
> ⚠️ **形态已收敛**：**无** `Server` 结构体 / `New(cfg)` / 分类端点路由 / 进程内注册表（`Register/Get`）/ `Instance/SkillsList/SkillsGet` / `NewInProcessTransport`。

---

## 0. 现状订正（2026-10-21，GATEWAY-2 L1 / L3 / C / L7；**2026-11-04 复核**）

> 本节为准，**与下文冲突时以本节为准**；下文未删除的旧描述（self / dir 节点、`dsl_run`、`agentbox`、一工具一 exe、22 executor）均为历史口径。

- **① DSL 已退役（L1）；出厂 executor 已全部退场（D-72，2026-10-31）**：`dsl_run` 契约、`src/lib/dsl`、`build-dsl-executor.ps1` 与 `filesys_run` / `browser_run` / `desktop_run` 三个容器工具**全部删除**；file 域 11 工具随 `file` 插件、`script_run` 迁为 task 插件 `task_run`、web 三工具随 `web` 插件。**〔2026-11-04 核对〕** 原先声明的 `chonkpilot-web-executor.exe` 也已随 `src/lib/mcp-tools` 整体退场（`build-mcp-server.ps1:127` 明写「executors 已全部退场，不产出」），**当前无任何出厂 executor 产物或源码**。browser / desktop 改为**常驻插件**（UDS，非 executor、不接 gateway）。
- **② 内置 server = 单进程 `core-mcp-server`（L3）；桌面单体已不拉起**：`chonkpilot-core-mcp-server.exe`（`src/lib/mcp-server/cmd/core-mcp-server`）仍是**独立部署 / capability 目录节点**使用的内置 MCP server；工具**不再一工具一 exe**：
  - **〔2026-11-04 核对〕** `src/lib/llm/coretools/` 现为**空目录**（无 `Supervisor` 实现）；桌面单体自 2026-11-02 起**不再拉起 core**：`src/lib/llm/server/boot.go:216`「进程外工具前门（core-mcp-server）已退役」，能力源改由 `src/lib/assembly/assemble.go` 以 in-memory `ClientSession` 作为普通 `ServerEntry{ID:"self"}` 接入 gateway。core 的现存用途 = 独立 gateway 的 `--capability` 下游（`src/others/mcp-gateway/main.go:80-99`，`dir:<root>`）与 `capability` 目录节点（`src/lib/llm/server/capnodes.go:252-275`，`core-mcp-server --stdio --root <dir>`）。
  - `runtime: builtin` 的契约（**meta 组 3 个** + 其他 `category` 的 builtin 契约）由 `dispatchBuiltin` 在 **core 进程内**执行（`server/builtin.go`）：`meta` 组走 `toolmeta.Run(GroupMeta)`，`server`（domain）组走 `toolmeta.Run(GroupDomain)`；每 handler 外层 `recover()` → panic 转 `isError`，绝不冒泡到主循环。**〔2026-11-08〕** domain 组契约由 5 收敛为 **0**：`ask_user` / `next_run` / `subchat_open` / `subchat_finish` 已迁 `chonkpilot-plugins/task`（插件自持契约 + 宿主 `plugin/v1/*` 能力）；`tool_result` 删除（结果读取统一走 `task_status`）。core 的 `capability/tools/server/` 不再投放任何契约。
  - `runtime: ../../executors/<x>.exe` 的契约**仍支持** spawn 单个 executor（`executor.go`，`exec.CommandContext` + `winproc` 隐藏窗口 + ctx 取消 → 杀该次进程树）；但**出厂已无此类契约与 exe**，仅保留为第三方/历史契约的兼容路径。
  - `internal: true` 契约只经 `POST /internal/exec` 执行，不进任何 MCP 工具面。
- **③ 契约来源三层 + embed（C）**：`server/contractsource.go` 的 `Source{Dir|FS}` 支持多层合并，**同名（同类型+同原语名）后者覆盖前者**并输出 `Shadowed` 审计。现行装配：**磁盘契约根存在 → 磁盘单层**；**缺失 → 内置 `//go:embed all:contracts`**（`server/contracts_embed.go`，构建期从 `src/initdata/capability` 覆盖同步）。更高层（user / project / prjusr capability 根）由各自 dir 下游经 gateway 前缀接入，不在本函数合并。**dir 节点走 `RegisterContractsWithTools`（forceTools=true）→ 不回落 embed**（用户/项目目录缺失 = 空能力面）。
- **④ 沙箱已下沉 gateway 进程级（L7）**：`agentbox` 包与 `Config.SecurityDirs` / `SetSecurityDirs` / `callTool` 内注入 `CHONKPILOT_SANDBOX` **全部删除**；`Config.ToolSandbox` 仅剩配置字段与解析，**执行期无消费**（见 §4.2）。写隔离由 **gateway 在 spawn stdio 下游时**以 **`chonkpilot-util/sandbox`**（Windows **Low IL 令牌 + Job Object**）实施，**只限写、不改 DACL、读不受限**，允许写目录须先 `EnsureLowIL` 打标（缺省未打标 = 子进程只能读 = fail-closed）。应用层路径黑名单随之移除（详见 [14](../10-architecture/14-安全域-agentbox.md) · G-75）。**〔2026-11-04 核对〕** 原写 `src/lib/core/sandbox`，该目录已不存在，现行实现 = 同级仓 `chonkpilot-util/sandbox`。
- **⑤ 子进程 env 收敛（2026-11-04 核对）**：`executorEnv` 只注入 `CHONKPILOT_INSTANCE/WORKDIR/DATADIR` + `CHONK_CHROME`（`server/config.go:594-597`）；`CHONKPILOT_SESSION/TURN/TOOL_CALL_ID/PURPOSE/INTERPRETERS` **不再注入**（`config_test.go` 明确断言）。

---

## 1. 职责与边界

- **一句话**：把「契约目录」（四原语 `.md`）注册到一个**调用方自建的官方 go-sdk `mcp.Server`** 上；工具调用时按契约 `runtime` 分派（`builtin` 进程内 / `runtime` spawn executor）。
- **做**：契约解析（四原语）、注册（tools→`AddTool`；prompts/skills→`AddPrompt`；resources→`AddResource`）、`tools/call` 执行链（builtin 分派 + spawn + 输出封装）、core exe 三种运行形态。
- **不做**：不建容器/实例/注册表/传输层（go-sdk `mcp.Server` 由调用方传入）；不做沙箱（下沉 gateway）；不持持久状态。
- **单体链路中的角色（2026-11-04 核对）**：`RegisterContracts` 由**装配方**在**自建的官方 server 实例**上调用 —— 现行唯一装配入口 = `src/lib/assembly/assemble.go`：先 `ms := mcp.NewServer(...)` 再 `mcpms.RegisterContracts(ms, root, cfg)` 扫描注册，然后把 `ms` 经内存传输连成标准 `ClientSession`，作为**普通下游** `ServerEntry{ID:"self", Origin:builtin}` 交给 gateway；**不再以 `Params.MCPServer` 交给 gateway**（该字段已删除，见 [26-mcp-gateway §0](26-mcp-gateway.md)）。另一路调用方 = 独立 gateway 的 capability 下游（`src/others/mcp-gateway/main.go:80-99` 以 `core-mcp-server --stdio --root <root>` 接入）与 llm server 的 dir 目录节点（`src/lib/llm/server/capnodes.go:252-275`）。**本模块因此以 lib 形态参与**（不自建 instance、不持有它）。装配链全貌见 [26-mcp-gateway §4.0](26-mcp-gateway.md)。

---

## 2. 形态与部署

| 项 | 值 |
|----|----|
| lib | `RegisterContracts(srv *mcp.Server, root string, cfg *Config) error` 与 `RegisterContractsWithTools(...)`（后者忽略 `cfg.SkipTools`，用于 dir 节点扫描）；二者为导出构造函数（2026-11-04 核对） |
| exe | `chonkpilot-mcp-server.exe`（console）；core 形态 `chonkpilot-core-mcp-server.exe` |
| 形态（必须显式指定） | `--http[=addr]`（默认 `127.0.0.1:5700`，端点 `/mcp`）· `--stdio` · `--service install\|remove\|run`（Windows 服务） |
| 其它 flag | `-root`（缺省 exe 目录/`capability`）· `-timeout` · `-config`（缺省 exe 同目录 `config.json`） |
| 构建 | `build-mcp-server.ps1` |

### 2.1 core-mcp-server（独立部署 / capability 目录节点；2026-11-04 核对）

| 项 | 值 |
|----|----|
| exe | `chonkpilot-core-mcp-server.exe`（源 `src/lib/mcp-server/cmd/core-mcp-server`，console、常驻） |
| 拉起（2026-11-04 核对） | **桌面单体不再拉起 core**（`src/lib/llm/server/boot.go:216`：进程外工具前门已退役）；core 现由**独立 gateway**（`--capability` → `dir:<root>` stdio 下游）或**目录节点**（`core-mcp-server --stdio --root <dir>`）按需 spawn。原「宿主经 `coretools.Supervisor` 拉起 + 固定 loopback 端口 + 1s→30s 退避重启」描述已失效（`src/lib/llm/coretools/` 为空目录） |
| 端口 | `--addr` 缺省 `127.0.0.1:0`（**随机端口**，`cmd/core-mcp-server/main.go:104`），就绪后向 ready-file 写 `{url,token}`；「固定端口」不再成立 |
| 部署 | `<exeDir>/mcps/core-mcp/chonkpilot-core-mcp-server.exe`（查找路径仍在，见 `capnodes.go:296-299`） |
| 接口 | MCP **Streamable HTTP**（`POST /mcp`）+ `GET /healthz` + `POST /internal/runtime`（运行期执行配置）+ `POST /internal/exec`（插件工具委托执行面，2026-10-19） |
| 鉴权 | `Authorization: Bearer <token>`（constant-time；token 仅宿主与 gateway 可见） |
| 调用上下文 | `X-Chonk-Instance-Id` / `X-Chonk-Session-Id` / `X-Chonk-Turn-Id` / `X-Chonk-Call-Id` / `X-Chonk-Work-Dir` / `X-Chonk-Data-Dir` / `X-Chonk-Purpose` / `X-Chonk-Scope` / `X-Chonk-Deadline-Ms` / `X-Chonk-No-Timeout`；协议 `_meta.chonkpilot` 为兼容回落（**MCP 会话建立后头不随每次请求更新，gateway 路径以 `_meta` 为准**） |
| 执行 | 按契约执行方式：**`runtime` 契约**每次 `tools/call` spawn 一个 executor 进程；**`builtin` 契约**（meta / server 域）在 core 进程内执行；ctx 取消/超时 → `winproc.KillTree` 只杀该次 executor 进程树，绝不杀 core 本身 |
| 工具面 | MCP `tools/list` 只暴露**非 internal** 契约，且只含 core `--root` 下实际存在的 `*.tool.md`。**当前出厂契约** = core **无**域工具契约（`capability/tools/server/` 不再投放；源 `src/lib/llm/contracts/` 包已删除）；`src/initdata/capability/tools/` 亦无出厂工具契约（file 11 / task / mcp / web / codegraph / vfts 等均随各自插件 MCP server 暴露）。`internal: true` 契约只进 core 内部执行表，不进 MCP 工具面。详见 [03 §3](../00-overview/03-构建与部署.md) |
| 契约标记 | `meta.internal: true` = 仅 `/internal/exec` 执行（不进 core `tools/list`）；`meta.core-only: true` = 仅 core 注册执行（进程内 mcp-server 跳过，避免与 core 重复注册） |
| 配置 | 启动参数 `--root/--work-dir/--data-dir/--config/--plugin-addr/--plugin-token`；usr/prj 热生效经 `POST /internal/runtime`（`mcpms.RuntimeConfig`：timeout/并发/解释器/Chrome/skip_dirs/tool_async/tool_sandbox/security_dirs） |
| gateway 接入（2026-11-04 核对） | **桌面单体**：`src/lib/assembly` 把进程内 `mcp.Server` 连成 `ClientSession`，作为 `ServerEntry{ID:"self", Origin:builtin}` 直接传入（**不经 `servers/register`、无 `name=core`**）。**独立 gateway / 目录节点**：经 `servers/register` 或直接构造 stdio 下游接入 `chonkpilot-core-mcp-server --stdio --root <dir>`；暴露名原样（`namespace "-"`，不加 `self_` 前缀）、`origin=builtin`。**取消语义**：core 为纯远程（proxied）下游 → gateway `Terminate` 为协作式 no-op（取消经执行池 ctx 送达 HTTP 请求），**不重建共享连接**（见 [26 §核心下游](26-mcp-gateway.md)） |

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

### 3.3 契约目录清单（实际路径 `server/contracts/`，2026-11-04 核对）

> 根 `src/lib/mcp-server/contracts/` 为**空目录**；embed 副本与唯一实际资产在 `src/lib/mcp-server/server/contracts/`（`server/contracts_embed.go`）。

- `prompts/core/code_review.prompt.md`（1 个）
- `resources/{core/core-overview, go/webview2-frameless, ui/frontend-drag-resize}.resource.md`（3 个）
- `skills/core/{agent-prompt-rule, debug, explore, platform-capability, sandbox-escape}.skill.md`（**5 个**）
- `skills/ux/*.skill.md`（15 个 UX/前端设计技能，随构建投放）
- `skills/se/*.skill.md`（14 个草稿；`build-mcp-server.ps1:112-114` 构建时删除，不随产物投放）
- **本目录无 `.tool.md`**；core 侧工具契约源已随 `src/lib/llm/contracts/` 一并删除（2026-11-08，server 域 = 0）。工具契约现行源 = 各插件 `chonkpilot-plugins/<plugin>/src/tools/<cat>/`。[27-mcp-tools](27-mcp-tools.md) 为**已退场**执行层，不再承载工具契约。

### 3.4 单一契约解析器（v2，2026-10-14）

> **背景（现状核查结论）**：v1 的方括号分区解析在仓库里存在 **4 份互不调用的重复实现**——data 层 `capfs`（`ParseDoc/BuildDoc`）、mcp-server `contract`（`splitSections/parseMeta/firstHeading`）、llm-server `domainmd`（`parseSectionMD`）、mcp-tools `cli`（`firstTitle`）——仅靠注释「与另一处保持一致」人工同步，边界行为（未知分区 / `arguments` vs `parameters` / 标题缺失）无统一保证。

- **唯一实现**：收敛为**单一契约解析包**（H1 分区解析 + 序列化 + 模板 + 「文件名 ↔ 类型」映射 + 隐式字段派生）。上述 4 处**全部改为调用它，旧实现一律删除**（无兼容分支）。
- **消费方**：data 层读写、mcp-server 注册扫描、llm-server 域工具契约读取、executor `--help`、gateway 目录资产加载，均复用同包。
- **前端**：`utils/agentRef.js` 等仅做「领域模型 ↔ 契约字段」映射，不自行解析分区；序列化以同包规则为准。
- **保存即刷新（保持现状，2026-10-14 裁定）**：保存后**由 fswatcher（capwatch，去抖 60ms）隐式热生效**——**不新增显式刷新消息**（见 §4.4/§7）。

---

## 4. 内部控制流

### 4.1 注册（`RegisterContracts`）

```text
root 缺失（dir 节点）→ 空注册返回 nil
root 缺失（非 dir 节点）→ 回落内置 embed 契约（server/contracts_embed.go）
sem = make(chan, cfg.MaxConcurrency)
tools   → loadTools → buildTool  → makeToolHandler(cat,name) → srv.AddTool
prompts → loadPrompts → buildPrompt + setPromptType("prompt") → srv.AddPrompt
skills  → loadSkills → 转 PromptDoc + setPromptType("skill")   → srv.AddPrompt
resources → loadResources → AddResource（uri 缺省补齐）
```

`makeToolHandler`：取信号量（`sem <- struct{}{}`）→ 反序列化 arguments → `callTool` → 统一 `{status,...}` 文本；失败置 `IsError`。
`makePromptHandler`：先按调用实参替换 `{{arg}}`，再替换 `{{toolchain.<key>}}`（能力原语文本里的工具链路径占位）。`makeResourceHandler`：静态内容。

**注册过滤（2026-10-27 订正；2026-11-04 核对）**：`RegisterContracts` 跳过三类契约 —— ① `category=server` 且**无 `runtime`**（无 executor 可执行）；② `core-only: true`（meta 3 / server 6，仅 core 注册执行；`server/server.go:192`）；③ `internal: true`（仅 `/internal/exec` 执行，不进任何 MCP 工具面）。有 `runtime` 的 `category=core` 作为 executor 加载（**当前出厂无此形态**；web/codegraph/vfts 等已改由插件进程内注册，主仓无对应 `.tool.md`）。

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
                否则"结果已保存至 <path>（N 字节），用 file_read 提取"
```

- **不设置 `cmd.Dir`**（子进程继承宿主 cwd；仅契约 runtime/entry 按契约目录相对解析）。
- **ctx 继承请求（B3 已落地）**：`callTool` 的 `ctx2` **派生自请求 ctx**（原为 `context.Background()`）——取消经 in-memory 传输的 `notifications/cancelled` 取消 handler 请求 ctx，`exec.CommandContext` 随之 kill 子进程（「收到取消 → 真停」写在执行体侧，见 [18 §3.7](../10-architecture/18-工具异步超时与取消.md)）。有硬上限时 `ctx2 = WithTimeout(请求 ctx)`；显式 `0`/`-1`（无上限）时 `ctx2 = 请求 ctx`（不设超时点，取消仍可达）。
- **调用上下文（R-11 二次升级）与子进程注入**：调用上下文（`instance_id`/`work_dir`/`data_dir`）经 MCP `tools/call` 的 **`params._meta`** 命名空间 `chonkpilot` 透传（`server.CallContext` / `callContextFromMeta` / `CallContextMeta`；**不进 tool arguments**）。**上下文存在但 instance 为空 → 工具执行失败（异常）；完全无上下文（外部直连 stdio/HTTP 客户端）→ 不注入、不报错**。spawn executor 时 `Config.executorEnv` 把 `CHONKPILOT_INSTANCE/WORKDIR/DATADIR` 与 `CHONK_CHROME` 注入**子进程环境变量**（继承宿主环境前**剥离**残留 `CHONKPILOT_*`）。**2026-10-28（P2-3）**：`CHONKPILOT_INTERPRETERS` 注入链已删除（`script_run` → task 插件 `task_run`，解释器配置归插件 `plugins.task.interpreter-*`）。`defaultsMap` 只注入 `skip_dirs`/`ignore_files`/`fileext`（原 `_workdir`/`_datadir`/`_instance`/`_interpreters` 注入已移除）。`Config.SetContext` 已删除。
  - **来源边界（2026-11-04 核对）**：装配方的进程内 self 会话、core 目录节点下游、standalone 自有部署在 gateway 侧均标注 `Origin=builtin` → 收到 `_meta["chonkpilot"]`；第三方（用户定义）server 为 `Origin=user`，gateway 不向其携带 `_meta`，故其永远拿不到 `CHONKPILOT_*`（判定见 [26-mcp-gateway](26-mcp-gateway.md) 来源标注段）。
- **沙箱：不在本模块（2026-11-04 核对）**：`agentbox` 包、`Config.SecurityDirs` / `SetSecurityDirs`、`callTool` 注入 `CHONKPILOT_SANDBOX` 均已删除；`Config.ToolSandbox` 仅保留配置字段与解析（`config.go:164-172,406-470`），**无执行期消费**。写隔离由 **gateway 在 spawn stdio 下游时**以 `chonkpilot-util/sandbox`（Low IL + Job Object，只限写）实施；详见 [14 §7](../10-architecture/14-安全域-agentbox.md) · [64 §3](../60-reference/64-配置项一览.md)。原「执行器侧 `src/lib/agentbox` 强制拦截 + `[mcp-server] agentbox 隔离生效` 日志」整段为历史口径。
- **执行硬上限与 ctx 语义（已落地，详见 [18-工具异步超时与取消](../10-architecture/18-工具异步超时与取消.md)）**：`resolveExecTimeout` 硬上限（usr `hard_timeout` 优先于契约 `timeout`，每级「未设置」才回落；显式 `0`/`-1` = 无上限 → 不设 `WithTimeout`）**保留**；`ctx2` 继承请求 ctx（B3）→ gateway 经 ctx 下沉 cancel 可被执行体侧消费（**kill 留在本模块 lib**，决策 42 §2 (241)）。
- **`POST /internal/exec`（core 内部执行面，2026-10-19）**：插件（codegraph / vfts）把 LLM 工具调用委托给 core 时使用 —— 载荷 `{tool, args, context{instance_id,session,turn,tool_call_id,work_dir,data_dir,purpose}, deadline_ms, no_timeout}`，`context` 与协议 `_meta.chonkpilot` 同字段名；core 走**与 `tools/call` 完全相同的 `ExecTool.Call`**（spawn 单个 executor、`KillTree` 取消），返回 `{text, isError}`；未知工具 404、上下文非法 400、请求 ctx 取消/超时 408。`internal: true` 的工具只能经此执行面调用（不进 MCP `tools/list`）。
- **调用上下文（2026-11-04 核对）**：`CallContext` 仍承载 `SessionID` / `TurnID` / `ToolCallID`（经 `_meta.chonkpilot` 透传，供进程内 handler/插件使用），但 **`executorEnv` 不再注入 `CHONKPILOT_SESSION` / `CHONKPILOT_TURN` / `CHONKPILOT_TOOL_CALL_ID` / `CHONKPILOT_PURPOSE`**；子进程环境仅 4 个键（见上条与 §0 ⑤）。原「2026-10-19 扩展注入」段落为历史口径。

---

## 5. 数据结构与存储

- 无持久存储；契约在构建/启动时加载进 `mcp.Server` 注册表（内存）。
- 临时文件：`ck-call-*.json`（入参）、`{RESULT-OUTPUT-FILE}`（输出）。

---

## 6. 依赖

- **上游**：`src/lib`（mq/winsvc/winlog/exedir）、官方 `modelcontextprotocol/go-sdk`。
- **下游（2026-11-04 核对）**：`src/lib/assembly`（进程内能力源，经 in-memory `ClientSession` 作为 `ServerEntry`）、独立 gateway / capability 目录节点（`core-mcp-server --stdio --root`）、独立部署。**不再是 gateway 的 `dirnode` 内嵌扫描**（`dirnode.go` 已删除）。

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
- **已删除死 meta**：`runtime-version` / `version` / `find`（解析与透出全删）；仅**兼容读**：`entry`（已废弃）。`title` **不是** `_meta` 键（v2 起走「基本信息」`title` → `Tool.Title`）。
- **🗄 历史口径（2026-11-04 核对：以下统计不再是现状）**：2026-10-19「一工具一 exe」（G-68 第二步）曾登记 app 根 5 + codegraph 8 + vfts 1 + meta 3 + domain 5 = **22 个 executor**（core 日志 `executors loaded: 22（exposed=13）`）。现无任何 executor 产物/源码：`src/lib/mcp-tools` 已退场（D-72，[27-mcp-tools](27-mcp-tools.md)），`build-mcp-server.ps1` 不产出 `capability/executors/`；codegraph/vfts 改为插件进程内注册（主仓无对应 `.tool.md`），meta/domain 由 core 进程内或 llm server `tools/register` 执行。`chonkpilot-server-tools.exe` 亦已删除（`src/lib/mcp-server/cmd/self-tools/` 为空目录）。
- **取消语义保障**：core 为 gateway 的纯远程（proxied）下游，`Terminate` = 协作式 no-op（见 [18 §6](../10-architecture/18-工具异步超时与取消.md)）；L4 `run_tool_async.py` 11/11（含 E/F/G/H/J：detach、转异步、待裁决恢复、层权威行、cancel_on_timeout 无孤儿）。

---

## 10. 关联测试

`src/test/chonkpilot-mcp-server/unittest/server_test.go`：
`TestListToolsContracts` · `TestPromptSkillMeta` · `TestListResources`（经公开包 `mcp-server/server`，把本模块 skills/prompts/resources 契约复制到临时根；**2026-11-04 核对**：core capability 根已无工具契约，`TestListToolsContracts` 断言工具集为**空集**，不再合并 27 的 mcp-tools 契约；官方 client in-memory transport 断言）。
