# 25 · src/lib/mcp-server（MCP 契约服务）

> 状态：✅ 与代码一致
> 关联：[10-分层与依赖](../10-architecture/10-分层与依赖.md) · [26-mcp-gateway](26-mcp-gateway.md) · [27-mcp-tools](27-mcp-tools.md)
> 代码目录：lib = `src/lib/mcp-server/`（`server/` + `contracts/`）；**exe 外壳 = `src/others/mcp-server/`**（`main.go`/`service.go`）
> ⚠️ **形态已收敛**：**无** `Server` 结构体 / `New(cfg)` / 分类端点路由 / 进程内注册表（`Register/Get`）/ `Instance/SkillsList/SkillsGet` / `NewInProcessTransport`。

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

---

## 3. 对外接口

### 3.1 契约格式（分区式，**非 frontmatter**）

```text
# <标题>                      # 仅作可读标题（不等于原语名；原语名 = 文件名去后缀）
[meta]
key=value                    # 每行一个 k=v（首个 = 分割）
[description]                # 多行
[parameters]                 # tool：YAML → JSON Schema
[arguments]                  # prompt：JSON Schema（required/properties）
[content]                    # 正文
```

| 后缀 | 解析 | 注册 |
|------|------|------|
| `.tool.md` | `ToolDoc`（meta + description + parameters） | `srv.AddTool` |
| `.skill.md` | `SkillDoc`（`[description]`+`[content]`，**`[meta]` 不参与**） | 转 `PromptDoc` + `_meta.type=skill` → `AddPrompt` |
| `.prompt.md` | `PromptDoc`（description + arguments + content，`{{arg}}` 占位） | `AddPrompt`（`_meta.type=prompt`） |
| `.resource.md` | `ResourceDoc`（meta `uri`/`mimetype` + description + content） | `AddResource`（uri 缺省 `file://<name>`） |

**tool meta 键**：`runtime` · `category` · `async`(auto\|always\|never\|manual，缺省 auto) · `async-threshold` · `timeout` · `args` · `output`(stdout\|code\|file，缺省 stdout) · `hot` · `title`（一句话标题；作为 MCP Tool.Title）。**`timeout`（执行硬上限）取值语义**：键**未设置**（缺失 / 空串）→ **回落**全局（executor 回落 prj `timeout_sec` / 内置 300s）；显式 **`0` / `-1` = 无上限**（`noLimit`，不设 `WithTimeout`，永远等、可取消）；正数 = 该值。**键显式声明即透出 `_meta.timeout`（含 `0`/`-1`）**，未设置则不写。**系统工具（`category=server` / `meta`）不单独设执行硬上限** → 契约写 `timeout=0`（**`0` = 无上限**，非"回落"）。权威口径见 [18 §3.8](../10-architecture/18-工具异步超时与取消.md)。
**已删除死 meta**：`runtime-version` · `version` · `find` —— 解析与 `_meta` 透出均已移除，契约文件不再书写；**`title` 保留**（H1 标题链路 = `Tool.Title`）。
仅兼容读：`entry`（已废弃，解释器类遗留）；`hot` 吸收旧 `llm`。
透出到工具 `_meta`：`hot` / `category` / `async` / `async-threshold`（仅 `>0`） / `timeout`（**键显式声明即透出，含 `0`/`-1` = 无上限**）（+ gateway 注入 `server`；`title` 走 `Tool.Title`，**不进 `_meta`**）。

> **目录资产注册 payload 的 `path`**：`.prompt.md` / `.resource.md` 解析出的 `PromptDoc` / `ResourceDoc` 仍是**静态内容**（`[content]`）；gateway 侧目录资产注册面 `prompts/register`（`{name, description, arguments?, content?, path?, asset_kind?}`）与 `resources/register`（`{name, uri?, description?, mimetype?, content?, path?}`）**新增可选 `path`** = 运行时内容来源路径，**有则该资产内容按需读盘、不驻留 `content`**（无 `path` 时仍用 `content` 兼容兜底，`catalogAsset.Path` + `assetContent()`）。**payload 明细以 [61 §5.1.1](../60-reference/61-消息一览.md) 为准**（本模块仅消费同一契约根扫描出的原语，5 字段均为 gateway `regMsg` 字段）。

> **文本占位**：`*.prompt.md` / `*.skill.md` 的 `[content]` 支持 `{{toolchain.<key>}}`（key ∈ `java`/`python`/`node`/`go`/`rust`/`c`/`chrome`）——`makePromptHandler` 渲染时按 `Config.Toolchain`（装配层注入的 usr `*Path` 值）替换；已知 key 未配置 → 空串、未知 key → 原样保留、不影响 `{{arg}}` 与 DSL `{{env.X}}`（实现 `server/toolchain.go ReplaceToolchain`，见 [21-llm-server](21-llm-server.md)）。

### 3.2 config.json

`root` · `timeout_sec`（默认 300）· `max_concurrency`（默认 16）· `defaults{skip_dirs, ignore_files, fileext}`（`skip_dirs` 默认 `.git/.svn/node_modules/.trae/.chonkpilot/__pycache__/.venv/venv/build/dist/.next/.nuxt`）；`Apply` 为浅合并（指针字段非空才覆盖）。

### 3.3 契约目录清单（`contracts/`）

- `prompts/core/code_review.prompt.md`
- `resources/{core/core-overview, go/webview2-frameless, ui/frontend-drag-resize}.resource.md`
- `skills/core/{debug, explore, sandbox-escape}.skill.md`
- **本目录无 `.tool.md`**（工具契约在 [27-mcp-tools](27-mcp-tools.md)，部署时合并到同一 `capability/`）。

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
- **调用上下文（R-11 二次升级）与子进程注入**：调用上下文（`instance_id`/`work_dir`/`data_dir`）经 MCP `tools/call` 的 **`params._meta`** 命名空间 `chonkpilot` 透传（`server.CallContext` / `callContextFromMeta` / `CallContextMeta`；**不进 tool arguments**）。**上下文存在但 instance 为空 → 工具执行失败（异常）；完全无上下文（外部直连 stdio/HTTP 客户端）→ 不注入、不报错**。spawn executor 时 `Config.executorEnv` 把 `CHONKPILOT_INSTANCE/WORKDIR/DATADIR/INTERPRETERS` 与 `CHONK_CHROME` 注入**子进程环境变量**（继承宿主环境前**剥离**残留 `CHONKPILOT_*`）。`defaultsMap` 只注入 `skip_dirs`/`ignore_files`/`fileext`（原 `_workdir`/`_datadir`/`_instance`/`_interpreters` 注入已移除）。`Config.SetContext` 已删除。
  - **来源边界**：本仓 mcp-server（内嵌 self / dir 节点 / standalone 自有部署）在 gateway 侧标注 `Origin=builtin` → 收到 `_meta["chonkpilot"]`；第三方（用户定义）server 为 `Origin=user`，gateway 不向其携带 `_meta`，故其永远拿不到 `CHONKPILOT_*`（判定见 [26-mcp-gateway](26-mcp-gateway.md) 来源标注段）。
- **agentbox 沙箱（executor 级）**：`Config` 增 `SecurityDirs []agentbox.Rule`（prj `security-*` 换算的「可读 / 可写目录（递归）」）与 `ToolSandbox map[string]bool`（usr `tool_sandbox`，**executor 级**开关：key = 类别 core/desktop/browser），装配层经 `SetSecurityDirs`/`SetToolSandbox` 注入（gateway `SetSandboxOverrides` 直写共享 Config）。`callTool` 按 **`td.Category`**（= 契约 `_meta.category`）查开关，命中即 `executorEnv(cx, policy)` **额外注入 `CHONKPILOT_SANDBOX`**（策略 JSON）→ 执行器侧 `src/lib/agentbox` 强制拦截越界读写，并记一行日志 `[mcp-server] agentbox 隔离生效：category=… tool=… dirs=…`。**未配置 = 不注入 = 不启用隔离**（默认兼容）；**旧形态（按工具暴露名）不再生效**。见 [14 §7](../10-architecture/14-安全域-agentbox.md) · [64 §3](../60-reference/64-配置项一览.md)。
- **执行硬上限与 ctx 语义（已落地，详见 [18-工具异步超时与取消](../10-architecture/18-工具异步超时与取消.md)）**：`resolveExecTimeout` 硬上限（usr `hard_timeout` 优先于契约 `timeout`，每级「未设置」才回落；显式 `0`/`-1` = 无上限 → 不设 `WithTimeout`）**保留**；`ctx2` 继承请求 ctx（B3）→ gateway 经 ctx 下沉 cancel 可被执行体侧消费（**kill 留在本模块 lib**，决策 42 §2 (241)）。

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
- **prompt 类型标注**：本模块 `setPromptType` 写入的 `_meta.type` **实际取值仅 `prompt` / `skill`**（`*.prompt.md`→`prompt`、`*.skill.md`→`skill`；`server.go:162-172`）；**`agent` 资产不属本模块扫描面**，由 llm server 侧 `//go:embed contracts/agents/*.agent.md` 经 gateway `prompts/register{asset_kind:"agent"}` 注入产出（见 [21-llm-server §3.4](21-llm-server.md)）。**`prompts/list` 仅返回 `type=prompt|skill`**（`.agent.md` 解析**不适用**）。
- **已删除死 meta**：`runtime-version` / `version` / `find`（解析与透出全删）；仅**兼容读**：`entry`（已废弃）。`title` 保留（H1 标题链路）。

---

## 10. 关联测试

`src/test/chonkpilot-mcp-server/unittest/server_test.go`：
`TestListToolsContracts` · `TestPromptSkillMeta` · `TestListResources`（经公开包 `mcp-server/server`，把本模块 skills/prompts/resources 与 mcp-tools 工具契约合并到临时根，官方 client in-memory transport 断言）。
