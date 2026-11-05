# 14 · 安全域（agentbox）

> 状态：🗄 **历史篇名（`agentbox` 包已删除）**；现行口径 = **进程级 Low IL 写隔离 + 插件侧可写白名单**（2026-11-04 核对）
> 关联：[00-架构总纲](../00-overview/00-架构总纲.md) §6 · [02-配置层级](../00-overview/02-配置层级.md)（`security-*`）· [16-路径解析规范](16-路径解析规范.md) · [18-工具异步超时与取消](18-工具异步超时与取消.md) · [26-mcp-gateway](../20-modules/26-mcp-gateway.md) · [28-plugins](../20-modules/28-plugins.md) · [23-filesys](../20-modules/23-filesys.md)
> 定位：本文记录「安全域」的现行实现与历史沿革。**正文不再以 agentbox 为现行实现**；`agentbox` 相关段落统一标 🗄。

---

## 0. 现行实现（2026-11-04 核对）

### 0.1 配置：`security-*` = 只写白名单

- prj `security-*` 的条目值**只写 `{"dir"}`**；旧值 `{dir,writable}` 仍可读，`writable` **读取时忽略、不报错**（`chonkpilot/src/lib/data/internal/config/validate.go` 的 `validateSecurityEntry`；`src/lib/llm/server/execconfig.go` 的 `securityRules` 强制 `Writable=true`）。
- 语义：列出的目录**可写（递归）**；**读不受限**（Low IL 只限制写）。空目录/不存在目录在保存时校验失败。
- 前端只提交 `{dir}`（`src/frontend/src/api/config.js`；`views/settings/SecurityConfig.vue`）。
- 「选择目录」仍经 `gui.select-dir` → `gui.selected-dir` 两段式（`src/frontend/src/api/config.js`、`composables/useDirPicker.js`）。

### 0.2 下发通道：`_meta.chonkpilot.sandbox`（不是 HTTP header）

- gateway 每次 `tools/call` 向下游插件/内置 server 下发 **`_meta.chonkpilot.sandbox = {enabled, writable_dirs}`**（`chonkpilot-agent/gateway/src/provider.go`；插件侧读取见 `chonkpilot-framework/plugin/src/sdk/plugin.go` 与 `chonkpilot-plugins/file/src/sandbox.go`）。
- 插件 MCP 是共享 http 长连接，白名单按项目变化，故不能放 HTTP header。
- 未启用（无该字段 / `enabled=false`）→ 插件**不校验**（保持旧行为）。
- `ServerEntry.SandboxPolicyJSON` 仍保留在 `chonkpilot-agent/gateway/src/serverslist.go`，但**生产代码无调用者**（当前实际走 `ServerEntry.SandboxDirs` / 实时回调）；`SetSecurityDirs` / `SandboxPolicyFor` / `CHONKPILOT_SANDBOX` 注入均**不存在**。

### 0.3 强制点（仅三处）

| 执行体 | 机制 | 落点 | 越界语义 |
|---|---|---|---|
| `file_*` **7 个写工具**（`file_write` / `file_patch` / `file_replace` / `file_copy` / `file_move` / `file_delete` / `file_iconv`） | 调用前路径预检（`targets[].path` / `from` / `to`） | `chonkpilot-plugins/file/src/sandbox.go`；统一入口 `file/src/main.go` | **整次调用拒绝、未做任何写入** |
| `task_run` | `scriptrun` + **Low IL 令牌 + Job Object**（`KILL_ON_JOB_CLOSE`）；执行前给白名单目录与 `cwd` 打 Low IL 标签 | `chonkpilot-plugins/task/src/sandbox.go`；`chonkpilot-util/scriptrun/exec.go`；`chonkpilot-util/sandbox` | OS 级拒绝写入（脚本运行时自行算出的路径也能拦住） |
| `web_download` | 按**最终落盘路径**校验 | `chonkpilot-plugins/web/src/download.go` · `web/src/sandbox.go` | 整次调用拒绝 |

- `file_*` 只读工具（`read` / `find` / `diff` / `detect`）不校验；`task_run` 的 `file` / `cwd` / `interpreter` 另受 R-11 路径强校验（[16](16-路径解析规范.md) §8）。
- **未覆盖**：`browser` / `desktop` 插件的截图与落盘句柄**没有**写白名单校验（两仓 `src` 内 `sandbox`/`AllowsPath` 零命中）。

### 0.4 Low IL 的两种用法（关键区别）

- **`sandbox.Prepare`**：只把 Low IL 令牌挂到 `cmd`，**不建 Job**。gateway spawn **stdio** 下游时只用它（`chonkpilot-agent/gateway/src/provider.go`）。
- **`sandbox.Start`**：以 Low IL 启动并把进程放进 **Job Object**（进程树 / 内存 / CPU 约束 + `KILL_ON_JOB_CLOSE`）。仅 `scriptrun` 使用 → **`task_run` 才有 Job**。
- 因此「gateway stdio 下游 = Low IL + Job Object / KILL_ON_JOB_CLOSE」**不成立**：只有 Low IL，没有 Job。
- http / sse 下游不做沙箱（同上 provider 仅 stdio 分支调用 `Prepare`）。

### 0.5 白名单实际内容与空集语义

- 宿主合并的**内置默认项**：`work_dir` + `<work_dir>/.chonkpilot/tmp`（不落库）。
- gateway 的**实时白名单回调**还会追加：prj `security-*` 条目、data 根、prjusr 根、**`os.TempDir()`（系统 temp）**（`src/lib/llm/server/execconfig.go` 的 `gatewaySandboxRules`）。即插件实际可写范围**宽于**「work_dir + tmp」。
- **空集语义分两套**：
  - 路径预检（`file` / `web`）复用 `chonkpilot-util/sandbox.Policy`：`Enabled && len(Writable)==0` 时 `AllowsPath` **返回 true（fail-open，放行）**。
  - 进程隔离（`task_run`）：`Enabled=true` 但无目录可打标 → 子进程对任何路径都不可写（OS 级 fail-closed）。
  - 「显式开启而 `security-*` 为空 → executor 侧全拒」**只对进程隔离成立**，对路径预检**不成立**。

### 0.6 保存即生效与配置面

- 保存 prj `security-*` 后，llm server 订阅既有主题 `data-prj-security-refresh` → 重跑 `loadExecConfig` + `reconcileUserMCPs`（`src/lib/llm/server/subscribe.go`、`execconfig.go`、`boot.go`）；**零新增 MQ 主题**。
- 外部 MCP 的沙箱开关变化 → `Gateway.SetSandboxOverrides` 异步作废/重连（`chonkpilot-agent/gateway/src/tooloverride.go`）；仅 stdio 下游。
- usr `tool_sandbox` 仍按 `{"executors":{...},"mcps":{...}}` 解析，但**内置 executor 行已全部退场**（`src/frontend/src/composables/useToolSandbox.js` 的 `EXECUTOR_UNITS = []`），当前只剩 `mcps` 维度；旧形状读取时迁移。usr `mcps[].sandbox` 仅为写入口兼容，开关来源已收敛到 `tool_sandbox.mcps`。

### 0.7 符号链接与审计

- 写白名单判定用 `sandbox.RealPath`（`filepath.EvalSymlinks`，父目录可解析则拼回文件名），**无 64 次上限 / 无 fail-closed**。
- 文件服务（filesys）另有逐段 `os.Readlink` 真实路径校验（含 Windows junction；>64 次替换 fail-closed），位于 `chonkpilot/src/lib/workspace/layout/layout.go`，与写白名单判定**不是同一实现**。
- 拒绝时**不写审计文件**；全仓无 `[agentbox]` 审计行。仅返回错误文案 / 日志。

---

## 1. 🗄 历史：agentbox 设计（2026-10-21 删除）

> 以下为历史留档，不再代表现行实现。

- 原定位：agentbox = 内存 MCP + 执行层，约束「扫描到的 runtime」；gateway 仅在 spawn 上游 MCP server 进程时用 agentbox 隔离；不接管 filesys / data。
- 强制范围：仅 stdio 的 MCP server（http / sse 不隔离）。
- 配置消费链：`securityRules` → `mcp-server Config.SetSecurityDirs` → `SandboxPolicyFor`（按 executor exe 基名）→ 子进程 `CHONKPILOT_SANDBOX`。
- 执行层落点：`src/lib/mcp-tools` 的 cli / fileops / fetch / browser / desktop / dslfs / dslexec。
- 审计：拒绝时 stderr 一行 `[agentbox] …`。
- 上述实现**均已不存在或已改道**：
  - `src/lib/mcp-tools` 目录在，**文件数 = 0**（[27-mcp-tools](../20-modules/27-mcp-tools.md) 已标 🗄 退场）。
  - `agentbox` 包、`SetSecurityDirs`、`SandboxPolicyFor`、`CHONKPILOT_SANDBOX`、`DenyRead`、`SanitizePath` 全仓 0 命中。
  - `[agentbox]` 审计 0 命中。
  - 软链实现 `src/lib/core/agentbox/agentbox.go` 不存在。
- 历史设计中的「符号链接两侧逐段解析 + 自环/过深 fail-closed」只保留在 filesys 侧；写白名单侧未采用。

## 2. 🗄 历史修订记录（保留时间线）

| 日期 | 事项 | 现行影响 |
|---|---|---|
| 2026-10-08 | browser/desktop 落盘接线「已修复」 | 新插件形态下**未覆盖**（browser/desktop 无 sandbox 校验），该结论作废 |
| 2026-10-11 | 符号链接解析「已实现」 | 落点已删；写白名单改为 `EvalSymlinks`，无 fail-closed |
| 2026-10-12 | 工具沙箱单位改为「被 spawn 的执行体 exe 基名」 | 内置 executor 已全部退场，`EXECUTOR_UNITS=[]`；仅 `mcps` 生效 |
| 2026-10-21 | agentbox 删除，改 gateway 进程级写隔离 | 成立；但 Job Object 只用于 `scriptrun`，gateway stdio 仅 Low IL |
| 2026-10-28 | `script_run` → `task_run` | 「task_run 是绕过通道」已作废：C5 已接入 Low IL + Job |
| 2026-11-03 | 写隔离落地（只写白名单 + `_meta` + file/task/web） | **现行口径，见 §0** |

## 3. 🔵 规划 / 待用户拍板

| 项 | 状态 | 说明 |
|---|---|---|
| 审计落盘 `DataDir` | 🔵 未实现 | 当前只返回错误文案 / 日志 |
| 按 instance 工具白/黑名单、`inputSchema` 校验、限流 | 🔵 未实现 | 原方案未定项，保留 |
| 下游 `trust: trusted\|sandbox` 标记 | 🔵 预留 | 无消费方 |
| browser / desktop 写白名单 | ⚠️ 缺口 | 两插件落盘未校验；是否接入需拍板 |
| gateway stdio 下游是否补 Job Object | ⚠️ 待拍板 | 当前只有 Low IL；补 Job 会影响第三方 stdio 兼容性 |
| 空白名单语义统一（fail-open vs fail-closed） | ⚠️ 待拍板 | 路径预检 fail-open、进程隔离 fail-closed，两套口径并存 |

## 4. 关联文档

- [26-mcp-gateway](../20-modules/26-mcp-gateway.md)（网关侧职责与传输）
- [27-mcp-tools](../20-modules/27-mcp-tools.md)（🗄 已退场，留档）
- [28-plugins](../20-modules/28-plugins.md)（file / task / web / browser / desktop 插件）
- [16-路径解析规范](16-路径解析规范.md)（R-11 路径强校验）
- [23-filesys](../20-modules/23-filesys.md)（文件服务限 `work_dir`）
- [18-工具异步超时与取消](18-工具异步超时与取消.md)（沙箱订正块）
- 相关功能点：[30-FP分级规范](../30-function-points/30-FP分级规范.md) §7「权限与安全」维度