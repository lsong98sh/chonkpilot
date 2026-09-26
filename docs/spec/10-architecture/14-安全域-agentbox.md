# 14 · 安全域（agentbox）

> 日期：2026-09-10 ｜ 状态：🟢 **部分实现**（2026-09-19 A 批：后端核心已落地，见 §7；配置面 UI 待做）
> 关联：[00-架构总纲](../00-overview/00-架构总纲.md) §6 · [02-配置层级](../00-overview/02-配置层级.md)（`security-*`）· [27-mcp-tools](../20-modules/27-mcp-tools.md) · [26-mcp-gateway](../20-modules/26-mcp-gateway.md)
> 定位：安全域 = **agentbox 沙箱**（根限定 `work_dir`），是工具执行与资源读取的统一约束层。
>
> **〔订正（2026-09-18，用户拍板，[42 §2 (104)](../40-roadmap/42-决策记录.md)）—— 定位更新（未实施）**：
> 1. **agentbox = in-memory MCP（内存 MCP）+ 执行层**——它本身是一个内存 MCP，位于**执行层**。
> 2. **作用对象 = 仅「扫描到的 runtime」**（scan 出的**执行器工具**）；**其它路径不用管**。
> 3. **mcp-gateway 的接入点 = spawn（上游 MCP server 进程）时用 agentbox 隔离**；其余不接管。
> 4. **明确不接管 `filesys` / `data`**——二者以 `workdir` 为管理单位、维持现状（见 [20-实例隔离与后端分离](20-实例隔离与后端分离.md) §4 缺口 6 订正）。
> 5. **限制依据 = `security-*`**（项目级 `{dir, writable}` 列表，**可读 / 可写目录、递归**）——**该目录集传给 agentbox 执行限制**；**原方案里「命令白名单」等未定项 = 不做**（对齐 [41 G-02](../40-roadmap/41-未决项登记.md) 的落点订正）。
> 6. **可隔离范围 = 仅 stdio 的 MCP server**（**http / sse 不隔离**）。
> 7. **配置面（规划，未实施）**：**mcp-server 配置页**增「**是否隔离**」开关（**仅 stdio 的项可勾选**）+ **新增页签**「**扫描到的工具**」（**逐个选择是否隔离**）——**配置键与 UI 均未实施**，仅登记方案（登记 [41 I-102](../40-roadmap/41-未决项登记.md)）。

---

## 1. 一句话现状

**agentbox 尚未实现**（无包、无类型、无函数、无配置项）；当前"安全域"仅由三层各自的局部行为拼凑：

| 面 | 现状 | 是否有强制拦截 |
|----|------|:---:|
| 文件服务 `src/filesys` | `absPath` + `withinWorkDir` 把路径限制在 `work_dir` 内，越界返回 `errForbidden("path outside work dir")` | ✅（唯一真实边界） |
| 工具执行层 `src/mcp-tools` | 路径一律全路径，**R-11 形式强校验**（所有输入/输出文件与目录参数，含 DSL 内 `#"..."` 引用与数据源读取，须绝对或 `~/` 开头，相对路径整体失败） | ⚠️（形式约束，非越界白名单） |
| 信任目录配置 `security-*` | 存储 + 前端 UI 完整，**零消费者** | ❌（纯展示） |

---

## 2. 现状证据

### 2.1 文件服务（唯一强制点）

`src/filesys/filesys.go`：`absPath`（`~359-371`）+ `withinWorkDir`（`~373-380`）——读/写/`remove`/`rename` 均校验路径落在**消息自带 `work_dir`** 内，越界 `errForbidden`（如 `:95/:114/:145`）。**与 `security-*` 白名单无关**。

### 2.2 工具执行层（无沙箱；R-11 形式约束）

代码注释明示设计意图：

- `internal/fileops/fileops.go:3`：`不做路径白名单校验（沙箱由上层 mcp-server agentbox 承担）`；文件操作参数须绝对或 `~/` 开头（R-11，`pathcheck.go`）
- `internal/fetch/fetch.go`：`save_as` / `form_files[].path` 受 **R-11** 强校验（须绝对或 `~/` 开头，相对路径在发请求前即失败）；其余无沙箱/白名单（agentbox 管控）
- `internal/scriptrun/exec.go:14`：`执行层（mcp-tools）无沙箱/无 datadir`；`file`/`workdir`/`interpreter` 均受 R-11 强校验（agentbox 管控越界；命令参数 `args`/`env` 与内置默认命令不受约束）
- `internal/browser/{handle,run}.go` / `internal/desktop/run.go`：`file`、`chrome_path`（browser）与 DSL 内**所有**文件句柄——落盘（browser：`SHT`/`DOM`/`DBG`/`UPF`、`dom_file`/`console_file`/`fail_shot`；desktop：`SHT`/`WIN … SHT`）、行尾 `=> #"file"` 目标、**核心语句数据源读取/访问器/`IF exist`**——受 **R-11** 强校验
- `internal/fileops/scriptfs.go`：`filesys_run` 核心语句 `#"path"` 句柄（LOOP/SET 数据源、访问器、`=> 目标`）的校验型文件系统（`ScriptFS`）

且 CLI 入口**根本不注入工作目录**（`internal/cli/cli.go:96-100`：`workDir := ""`，"工作目录概念已移除"）。**注意**：相对路径不注入 workDir 后本会相对进程 cwd（不可控），故对工具参数改由 **R-11 直接拒绝相对路径**（见 [16-路径解析规范](16-路径解析规范.md) §8）——**包括** `workdir` 等目录参数同样受约束；`paths.ResolveDir` 仅服务 CLI 命令行目录参数（`--work-dir`/`--data-dir`）。

### 2.3 信任目录 `security-*`（有存储无执行）

- 存储：`src/data/persist/persist.go`（`data-prj-security-*` → prj `config` 表 `security-<key>`）。**契约（2026-09-15 已通往返）**：**一条目录 = 一个 persist key**（key 由前端生成的不透明 id，与目录内容无关、条目保序），value = **JSON 字符串** `{"dir","writable"}`；save 逐条 upsert + 删除已移除条目（`data-prj-security-delete`），list 返回平铺 map（剥 `security-` 前缀）→ 前端还原为条目数组。
- UI：`src/frontend/src/views/settings/SecurityConfig.vue`（ProjectConfig「安全」页签），主题 `data-prj-security-{list,save,delete}` + `data-prj-security-refresh`；「选择目录」经 `openDirDialog()`（`gui.dir.open-dialog`，原 `PickFolder()` 已改调，2026-09-15）。〔2026-09-19 订正：原 `frontend/src/views/...`——前端工程已独立为 `src/frontend`，路径迁移，原文保留。〕
- **无任何 Go 代码读取该白名单做拦截/过滤**（除 persist 自身存取）。

### 2.4 路径解析规范

`src/lib/paths`：`ExpandHome` + `ResolveDir(raw, base)`（`~` 展开 → 绝对原样 `Clean` → 否则 `Clean(Join(base, raw))`）。
- `ResolveDir` **明确允许 `../` 跳出 base**（见 [16-路径解析规范](16-路径解析规范.md) §7「属有意行为，不做强制钳制」）。使用点：CLI/GUI 的**命令行目录参数**（`--work-dir`/`--data-dir`）、`src/data`（ProjectPath）、mcp-tools 的 `fileops.ValidateFilePath`（仅复用其**绝对路径归一化**，`base` 恒为空、不接受相对兜底；R-11）。**不再有工具直接用 `ResolveDir` 解析用户目录参数**。

---

## 3. 目标设计（🔵）

### 3.1 归属与职责

- **沙箱根 = 请求上下文 `work_dir`**（随消息传入，不依赖进程状态）。
- **执行层不自持沙箱**：executor 短命进程零状态、不校验路径；**沙箱责任上抛**，由上层（gateway/agentbox）统一承担。
- 既有设计表述：一处主张「执行层无沙箱，agentbox 归 mcp-gateway」，另一处主张「执行层不校验路径，沙箱责任上抛（未实现）」。
- ⏸ **归属延后（D-01，2026-09-11 拍板）**：落点**非单一**——需在**演进阶段按情形区别对待**（gateway / mcp-server / 其他），不预设单一归属层。
- ✅ **归属已定（2026-09-18，D-01 闭项，[41 D-01](../40-roadmap/41-未决项登记.md)）**：**agentbox = 内存 MCP + 执行层**；**gateway 在 spawn 上游 MCP server 进程时用 agentbox 隔离**；**仅对「扫描到的 runtime」施加限制**；**不接管 filesys / data**（后者以 `workdir` 为单位）。**上述 `work_dir` 沙箱根口径仍成立，但不再自称「统一约束层」。**

### 3.2 边界表（设计目标）

| 维度 | 目标行为 |
|------|---------|
| 文件访问 | 越界（`work_dir` 外）由沙箱拦截；不再做路径字符串白名单（`SanitizePath` 已弃） |
| 命令执行 | **不做命令白名单**（`script_run` 任意脚本） |
| 上下文注入 | 上下文（work-dir/data-dir）注入系统提示词，约束 LLM 使用全路径 |
| 名称隔离 | 工具/资源名按 instance 隔离（`scope`：global ∪ 归属 instance） |
| 写操作确认 | 🔵 规划：只读工具自动执行，**修改性工具**（`write`/`remove`/`rename` 等）需用户确认；当前**未实现**（全仓无 permission 判定消费方） |
| 下游信任 | 下游 server 标记 `trust: trusted|sandbox`（**预留**） |

> **〔订正（2026-09-18，[42 §2 (104)](../40-roadmap/42-决策记录.md)）—— 本表按新定位收敛**：**agentbox 只约束「扫描到的 runtime」（执行器工具）**；**gateway 侧仅在 spawn 上游 MCP server 进程时用 agentbox 隔离**，**其余路径不接管**（**不接管 filesys / data**）。**限制依据 = `security-*` = 可读 / 可写目录（递归）**，**该目录集传给 agentbox 执行限制**；**「命令执行 = 不做命令白名单」维持**（原方案里「命令白名单」等未定项 = **不做**）。**可隔离范围 = 仅 stdio 的 MCP server**（**http / sse 不隔离**）。

### 3.3 访问控制 / 限流 / 审计（规划）

- 认证：按 subject 授权。
- 按 instance 的工具白/黑名单。
- `inputSchema` 校验。
- 限流。
- 审计落 `DataDir`。

> 上述来自网关侧设计稿（2026-08/09，已整体归档）的「安全域」规划，均为规划态。

---

## 4. 与信任目录的关系

`security-*`（项目级，`{dir, writable}` 列表）的目标消费方 = agentbox：

- 白名单内目录 → 允许读（或读写，视 `writable`）。
- 白名单外 → 拒绝或降级（待定）。
- 当前状态：**配置与 UI 已就绪，等待 agentbox 接上**（见 [02-配置层级 §5](../00-overview/02-配置层级.md)）。

> **〔订正（2026-09-18，[42 §2 (104)](../40-roadmap/42-决策记录.md)）**：`security-*` 语义**定稿 = 「可读 / 可写目录」（递归）**，**消费方 = agentbox**（**执行层限制**：把该目录集**传给 agentbox** 即生效）；**不再保留「白名单外拒绝或降级（待定）」的悬空口径**。**G-02（`security-*` 无强制点）的落点订正 = agentbox**（排期保持 **P1-5**）。
> **实施状态**：**配置面与 agentbox 均未实施**（`security-*` 现仍仅存储 + UI、零消费者）。

---

## 5. 待落地清单

| # | 事项 | 状态 |
|---|------|:---:|
| 1 | agentbox 实现（**内存 MCP + 执行层**；**仅约束「扫描到的 runtime」**，沙箱根 = work_dir，越界拦截） | 🔵 未实施 |
| 2 | 归属确定（D-01） | ✅ **已定（2026-09-18）**：内存 MCP + 执行层；gateway **spawn 上游 MCP server 时**用 agentbox 隔离（[41 D-01](../40-roadmap/41-未决项登记.md)） |
| 3 | 接入 `security-*`（**可读 / 可写目录，递归**）**传给 agentbox** 执行限制 | 🔵 未实施（G-02 落点） |
| 4 | 可隔离范围限**仅 stdio 的 MCP server**（http / sse 不隔离） | 🔵 未实施 |
| 5 | 配置面（规划，未实施）：**mcp-server 配置页**「是否隔离」开关（仅 stdio）+ **新页签**「扫描到的工具」（逐个是否隔离） | 🔵 未实施（[41 I-102](../40-roadmap/41-未决项登记.md)） |
| 6 | 按 instance 工具白/黑名单 + inputSchema 校验 + 限流 | 🔵（**未定项，原方案保留**，非本次口径） |
| 7 | 审计落盘 `DataDir` | 🔵 |
| 8 | `trust: trusted\|sandbox` 下游标记 | 🔵 预留 |

> **〔订正（2026-09-18，[42 §2 (104)](../40-roadmap/42-决策记录.md)）**：原第 1 条「沙箱根 = work_dir，越界拦截」的**定位订正为「内存 MCP + 执行层，仅约束扫描到的 runtime」**；**新增**第 4/5 条（仅 stdio 可隔离 + 配置面）；**第 6 条（工具白/黑名单 + inputSchema + 限流）为原方案未定项，本次不改、保留**。**全部为 🔵 未实施**（不编造实现状态）。

---

## 6. 关联文档

- [26-mcp-gateway](../20-modules/26-mcp-gateway.md)（网关侧职责与传输）
- [27-mcp-tools](../20-modules/27-mcp-tools.md)（执行层路径与保留参数）
- [16-路径解析规范](16-路径解析规范.md) 目录解析规范
- [23-filesys](../20-modules/23-filesys.md)（文件服务限 `work_dir`）
- 相关功能点：[30-FP分级规范](../30-function-points/30-FP分级规范.md) §7「权限与安全」维度

---

## 7. 实施状态（2026-09-19，A 批 · 后端核心；决策 [42 §2 (109)](../40-roadmap/42-决策记录.md)）

> **范围**：本批只做**后端**（策略包 + 执行层强制 + 配置键读取/生效 + gateway stdio 下发）；**配置面 UI（mcp-server 页「是否隔离」+ 新页签「扫描到的工具」）由后续批次做**（[41 I-102](../40-roadmap/41-未决项登记.md)）。**〔订正（2026-09-26）**：配置面 UI **已实施**（MCP 对话框「运行信息」页签「沙箱」+ 页签「工具沙箱配置」），且工具沙箱已由**工具级改为 executor 级**（key = 类别 core/desktop/browser）；见下文 §7.1 #4/#6 与 §7.2 #5 同日订正。〕

### 7.1 已实现

| # | 事项 | 落点 | 备注 |
|---|------|------|------|
| 1 | **策略包（换算 / 判定 / 编解码）** | `src/lib/agentbox`（**新增包**） | `Rule{Dir,Writable}` / `Policy{New,Parse,Marshal,Allowed,Check}` / 进程级 `Set,Current,InitFromEnv,Check`；环境变量 **`CHONKPILOT_SANDBOX`**（JSON `[{dir,writable}]`）；错误 `*DeniedError{Path,Write,Allowed}` + `ErrDenied`（中文可诊断） |
| 2 | **策略来源 = `security-*`** | `src/llm/server/server.go` `securityRules(instanceID)`（读 `data-prj-security-list`，value = `{"dir","writable"}`）→ `mcp-server Config.SetSecurityDirs` | **递归语义**：允许目录下所有子路径；`writable` 决定可写（可写必然可读） |
| 3 | **执行层强制（真正拦截）** | `src/mcp-tools`：`internal/cli/cli.go`（启动时 `InitFromEnv`）+ `fileops`（`file_read`/`file_find`/`file_diff`/`filesys_run` 全动词/`ScriptFS` 句柄）+ `fetch`（`save_as`/`form_files`） | 越界 → **整体失败**（exit 1 + 明确文案），非仅告警；审计 = 拒绝时 stderr 一行 `[agentbox] …`（宿主捕获落日志） |
| 4 | **mcp-server 下发** | `Config.SecurityDirs` + `Config.ToolSandbox`（`SetSecurityDirs`/`SetToolSandbox`/`SandboxPolicyFor`）→ `callTool` 按 **executor 类别**（`td.Category` = core/desktop/browser，2026-09-26 起）查开关 → `executorEnv(cx, policy)` 注入 | 未配置 = 不注入（**默认兼容**） |
| 5 | **gateway 上游 spawn 下发（仅 stdio）** | `ServerEntry.Sandbox`/`SandboxDirs` + `SandboxPolicyJSON()` → `buildConn` 的 **stdio** 分支注入 | http/sse **不施加**；**上游是否遵守取决于其是否实现 agentbox 消费方**——第三方进程**仅透传，不构成强制** |
| 6 | **配置键** | usr `tool_sandbox`（**executor 级**开关：key = 类别 core/desktop/browser，persist 自由键已注册；2026-09-26 由工具级改 executor 级，旧形态失效）· usr `mcps[].sandbox`（server 级开关，仅 stdio&spawn）· register `mcp_server.sandbox`/`sandbox_dirs`（可选字段） | 均登记 [64 §3/§4](../60-reference/64-配置项一览.md)；**与既有 `isolate`（连接池隔离）语义独立、互不替代** |
| 7 | **保存即生效** | llm server 订阅**既有主题** `data-prj-security-refresh` → 重跑 `loadExecConfig` + `reconcileUserMCPs` | 零新增 MQ 主题；executor 侧**下次 spawn** 生效（每次调用重读 Config） |
| 8 | **验证** | 6 模块 × {默认, `-tags split`} build/vet/test 全绿；L2 新增 4 组（含**经 mcp-server spawn 真实 executor 的端到端**）；L3 基线 23/0/1 + 57/0/2；真实 executor 开/关两态真机 | 详见 [42 §2 (109)](../40-roadmap/42-决策记录.md) §G |

### 7.2 未实现（如实标注，不编造）

| # | 事项 | 说明 |
|---|------|------|
| 1 | **`script_run` 脚本体内的文件读写** | 脚本在**任意子进程**内执行，执行器**无法拦截**其文件操作（本批仅约束 `file` 参数为可读）。**隔离开启时 `script_run` 仍是绕过通道** → 登记 [41 I-105](../40-roadmap/41-未决项登记.md) |
| 2 | **`browser_run` / `desktop_run` 的落盘句柄** | `SHT`/`DOM`/`DBG`/`UPF` 等落盘未接线（本批未覆盖）→ [41 I-105](../40-roadmap/41-未决项登记.md) |
| 3 | **审计落 DataDir** | 14 §5 规划项；当前仅 stderr 一行（由宿主捕获落日志），**不写文件** |
| 4 | **符号链接解析** | 判定按字面路径 `Abs`+`Clean`，**不追软链**（`EvalSymlinks` 未用） |
| 5 | ~~**配置面 UI**~~ | **已实现（2026-09-19；2026-09-26 改 executor 级）**：MCP 对话框「运行信息」页签「沙箱」（仅 stdio，usr `mcps[].sandbox`）+ 页签「工具沙箱配置」（**三个 executor 行 core/desktop/browser + 只读工具清单 + 手动保存**，usr `tool_sandbox`）→ [41 I-102](../40-roadmap/41-未决项登记.md) |
| 6 | **§3.3 其余规划**（按 instance 工具白/黑名单 / `inputSchema` 校验 / 限流 / `trust` 标记） | 原方案未定项，**本批不做、保留**（同 (104) 口径） |

> **空允许集语义**：开关显式开启而 `security-*` 为空时——executor 侧 = **全拒**（严格）；gateway 上游 = **不下发**策略 + 记日志（上游非本仓进程，空策略可能被打成全拒）。
