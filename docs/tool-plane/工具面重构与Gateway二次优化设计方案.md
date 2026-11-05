# 工具面重构与 Gateway 二次优化设计方案

> 状态：🟡 **实施中 · 2026-11-04 核对** —— 实测状态：L1 PARTIAL、L2 IMPL、L3 PARTIAL、L4 PARTIAL、L5–L8 IMPL、L9/L10 PARTIAL、L11 核心 MISSING、L12/L13 PARTIAL（逐条见 §7）。**`49-待实现项` 已于 2026-11-04 清空，不再作为进度权威**；本文 §7 是唯一状态表。
> 关联：[MOD-1 LLM 组件化](../llm-modular/LLM组件化改造设计方案.md)（前置：Gateway 依赖纯净）· [插件桥接端口设计方案](../bridge-plugin/插件桥接端口设计方案.md)（能力外溢 · T14 配置快照）· [63-DSL语法](../spec/60-reference/63-DSL语法.md)（退役对象）· [25-mcp-server](../spec/20-modules/25-mcp-server.md) · [26-mcp-gateway](../spec/20-modules/26-mcp-gateway.md) · [02-配置层级](../spec/00-overview/02-配置层级.md) · [18-工具异步超时与取消](../spec/10-architecture/18-工具异步超时与取消.md)

> **〔2026-11-04 核对 · 与下文设计口径的偏差（以代码为准）〕**
> 1. **browser / desktop 已进入工具面**：`chonkpilot-plugins/browser/src/main.go:55-56` 注册 `browser_run` 并 `HotTool`，`desktop/src/main.go:55-56` 注册 `desktop_run`；manifest 写 `hot_tools:["browser_run"|"desktop_run"]`、`namespace:"-"`。本文 §2 / §3.9 / §3.10 / R16 / L1 / L9 / L10 的“不注册 MCP 工具、不接 gateway”只在**引擎内层**（`engine/main.go`，仍是 UDS 三端点）成立，不是现行整体事实。
> 2. **L1 不是“零残留”**：DSL 契约 / 引擎 / 构建脚本确已删除，但类型与展示层仍在（`chonkpilot-agent/session/src/tasks.go` 的 `TaskKindDslJob/Loop/Parallel/Step`、`DslStepRecord`；主仓 `data/facade/tasktree.go` / `task/task.go` 的 `dsl_job` / `loop_current` / `steps_json`；前端 `useDslView.js` / `DslStepsPanel.vue`）。
> 3. **契约已分源**：server 域 6 在主仓 `src/lib/llm/contracts/tools/*.tool.md`；file 11 / task 4 / crontab 6 在各自插件仓；mcp / web / browser / desktop / codegraph 等由代码 `p.Tool(...)` 注册、无 `.tool.md`。`src/initdata/capability/tools/` 为空。
> 4. **gateway 无 drift 校验**：全仓 grep `drift` 在 gateway 零命中；第三方 `tools/list` 不与权威清单比对。L4 的 elicitation 也未接线（`ElicitHandler` 恒 nil，客户端不声明能力）。
> 5. **L11 无 `asset_save`**：全仓无 `asset_save` / `asset_*` 工具；只有数据层 `KnowledgeSave` + `capwatch` 热生效 + `assetGuide` 提示词。“热生效”有，“LLM 可写的工具箱沉淀”没有。
> 6. **L3 是“进程收敛完成”，不是“合并为 1 个内置 server”**：file/task/mcp/web/browser/desktop/crontab/log 各自插件 server；server 域 6 由 llm-server 进程内注册；`core-mcp-server` 仅作 capability 目录节点的 `--stdio` 下游保留，桌面单体 `gui/host` 不再拉起。
> 7. **L12 的 `BootConfig` 边界与 §3.12 相反**：现行 `appwire.BootConfig` 只作启动期环境输入，不搬运 usr/prj 运行期配置、不开库不读配置；`src/server` 仅剩 exe，实际只有 desktop 单体形态。

---

## 1. 问题与目标

### 1.1 要解决的问题

| # | 问题 | 现状证据 |
|---|------|---------|
| P1 | **DSL 语法层已无存在意义** | server 已对外提供 5 类原子能力（文件监控 / DB 读写 / 配置读写 / LLM 调用 / MCP 调用），任何脚本（JS/Python/shell/scriptlet）都能直连 server；DSL 的 `READ`/`WRITE`/句柄/`{{env.*}}` 全是对已有能力的重复实现 |
| P2 | **工具实现内嵌在 gateway 进程** | `Params.MCPServer`（self 节点）、`ContractScanner`、资产注册表、`llmToolPick`（gateway 内部调 LLM）——gateway 同时承担路由、执行、资产、语义推荐、契约扫描五种职责 |
| P3 | **无池化，执行体一次性** | 每次 `tools/call` 都 spawn 执行体（`executor.go` `exec.CommandContext`）；browser/desktop 的会话状态无法跨调用保持，被迫把多步操作挤进一个 DSL 脚本 |
| P4 | **装配分散三处** | `assembly.Build`（静态装配）+ `llm/server/chatshadow*.go`（端口适配）+ `gui/host/main.go`（进程接线），改一条链路要看三处 |
| P5 | **配置无启动快照** | 组件各自懒读 DB，初始化顺序不可测，组件无法纯内存单测（违反 MOD-1 C2「零环境依赖」） |
| P6 | **取消语义无阶梯** | 只有"断连 / kill+respawn"两档，无协议内优雅取消，无杀进程连带语义 |

### 1.2 目标（可验收）

| # | 目标 | 验收判据 |
|---|------|---------|
| G1 | **DSL 语法零残留** | `dsl_run` 删除；`filesys_run` 拆分后删除；`browser_run` / `desktop_run` 整个删除；`src/lib/core/dsl` 删除；`grep -r "SET \|LOOP \|PARALLEL " src/` 零业务命中。**〔2026-11-04 核对〕部分不成立**：契约/引擎/构建脚本已删，但 DSL 类型与前端展示仍在；`browser_run` / `desktop_run` 以插件工具名回归 |
| G2 | **gateway 零工具实现** | `Params.MCPServer` / self 节点 / `ContractScanner` / 资产注册表 / `llmToolPick` 全部移除；gateway 不 import `chonkpilot-mcp-server`（**已实现**） |
| G3 | **工具集成一个内置 server + 全部 server 一视同仁** | 全部工具（file 11 / asset / meta 3 / `llm_run`）集成到**一个内置 mcp-server**（handler 进程内，删除一工具一 exe 的 9 个 `*-exec`）；与第三方走同一 `ServerEntry` + 同一 MCP 协议，无 `builtin` 特殊分支；browser/desktop 不是 MCP server，不在此列。**〔2026-11-04 核对〕** 前半句不成立：现行是插件化多 server + server 域 6 进程内注册；browser/desktop 已是 hot MCP 工具 |
| G4 | **进程池 + 连接池** | 按 transport 分别管；池键 / 池大小 / `maxConcurrent` 可配置；实例上下文经 `_meta` 透传，不靠池键区分 |
| G5 | **取消四阶梯（MCP 通道）** | L0 协议取消 → L1 宽限 → L2 断连 → L3 杀进程树；杀进程连带受影响调用明确上报；browser/desktop 走 UDS `cancel` 帧 + hard timeout（§3.7） |
| G6 | **配置快照同源** | 前后端同一份快照（同 revision / 同分区 / 同脱敏）；启动只 load 一次 |
| G7 | **装配集中** | 单一 wire 函数产出整棵组件树；`desktop` 形态与 `server` 形态共用，差异仅剩宿主生命周期。**〔2026-11-04 核对〕** `appwire` 已集中装配并被 `gui/host` 使用；但 `src/server` 仅剩 exe，现行只有 desktop 单体形态 |

### 1.3 非目标

- 不做工具级认证（沿用「开放清单 + 审计」，见能力外溢专题）
- 不做 `mcp_find` 的合并/拆分方案（另立课题）
- 不做旧 DSL 脚本兼容（不保留、不迁移、不解析）
- 不做 gateway 内部的多 provider 智能路由

---

## 2. 总体方案

**一句话**：原设计为“全部工具**集成到一个内置 MCP server**（多工具、handler 进程内，**不再一工具一 exe**）→ gateway 只保留路由/策略/调用/任务四件事 → 池化托管进程与连接 → 取消按阶梯收口 → 装配与配置快照集中到一处；browser / desktop 不做工具，降为脚本能力面（独立进程 + **UDS** + 协议文档）”。**〔2026-11-04 核对〕** 现行实现是“进程收敛 + 契约分源 + 插件化 server”，不再有“单一内置 server”；且 browser / desktop 外层已注册 `browser_run` / `desktop_run` hot MCP 工具（详见篇首偏差 1、6）。

```text
LLM turn
  ├─ chat（9 端口，零 MQ 零环境依赖，MOD-1）
  │    └─ ToolInvoker.Dispatch
  │         └─ gateway { Contracts · Policy · Dispatcher · TaskLayer }
  │              │  MCP client（唯一工具协议）
  │    ┌─────────┴─────────┐
  │    ▼                   ▼
  │  内置 mcp-server      第三方 MCP
  │  （全部工具，1 进程）  (stdio/http/sse)
  │   file×11 · asset · meta×3 · llm_run
  │
  └─ 脚本（script_run：python / js / shell …）
       ├─ UDS ──▶ browser 插件（start / stop / operation）
       └─ UDS ──▶ desktop 插件（start / stop / operation）
```

> **两条通道不重叠（原设计口径）**：gateway 只认 MCP server（工具面）；browser / desktop 走脚本能力面，不注册为工具。**〔2026-11-04 核对〕** 该口径已被外层插件推翻：引擎仍是 UDS 脚本能力面，但插件同时把 `*_run` 注册为 hot MCP 工具；现行准确表述 = “引擎 UDS + 每端一个 `*_run` 工具”。

**职责边界**：

| 组件 | 唯一职责 | 明确不做 |
|------|---------|---------|
| gateway | MCP 路由、调用管理、任务管理 | 不实现任何工具、不扫描契约、不持有资产、不调 LLM |
| chat | 轮次状态机、上下文编排、工具循环 | 不碰 MQ、不碰 DB 实现、不知道 router/gateway 类型 |
| router | 单次 LLM 出网（协议适配） | 无状态、无会话、无工具执行 |
| **内置 mcp-server**（原设计） | **全部工具 handler**（file 11 · asset · meta 3 · llm_run） | 不做路由/任务/编排；不放 browser/desktop。**〔2026-11-04 核对〕现行非单一内置 server，见 §3.3 注** |
| （工具分组） | file / asset / meta / llm 四组 = **同一进程内的代码分包** | 分组只是代码组织，**不是进程边界**（原设计；现行已按插件分进程） |
| browser 插件 | 浏览器 context 生命周期 + 原子操作 | 不做 MCP 工具、不接 gateway、不做编排。**〔2026-11-04 核对〕引擎遵守；外层 `main.go` 已注册 `browser_run`** |
| desktop 插件 | HWND 查询/操作 + 输入注入 + 截图 | 不做 MCP 工具、不接 gateway、不做编排。**〔2026-11-04 核对〕引擎遵守；外层 `main.go` 已注册 `desktop_run`** |

---

## 3. 详细设计

### 3.1 DSL 退役

**删除**：

| 删除对象 | 位置 |
|---------|------|
| `dsl_run` 契约 + handler | `src/initdata/capability/tools/core/dsl_run.tool.md`、对应 executor |
| DSL 解释器 | `src/lib/core/dsl/`（整套） |
| DSL 相关执行器 | `chonkpilot-dsl-executor.exe`（`build-dsl-executor.ps1` 一并删除） |
| 契约中 DSL 语法段 | `filesys_run` 的 DSL 指令表（拆分为 11 个原子工具后**整个契约删除**） |
| **`browser_run` 契约** | **整个删除**——浏览器不进工具面，改由 UDS + 协议文档（§3.9） |
| **`desktop_run` 契约** | **整个删除**——桌面不进工具面，改由 UDS + 协议文档（§3.10） |
| 相关配置键 | `foreachConcurrency` / `foreachMaxDepth` / `toolMaxDepth`（已在 02 §8 死配置清单） |

> **〔2026-11-04 核对〕** 表中“删除 `browser_run` / `desktop_run` 契约”只对**主仓契约文件**成立；现行 `chonkpilot-plugins/browser` / `desktop` 的外层 `main.go` 已重新注册同名 **`browser_run` / `desktop_run`** hot MCP 工具。故 L1 的准确结论是“DSL 契约与执行器已删；DSL 类型/展示残留；browser/desktop 以插件工具名回归”。

**能力迁移表**：

| 原 DSL 能力 | 迁移去向 |
|------------|---------|
| `SET` / `IF` / `LOOP` / `PARALLEL` 流控 | **删除**——LLM 直接按序/并行发多次工具调用，不再在脚本内编排 |
| `#"path"` 文件句柄 | `file_*` 11 个原子工具 |
| `@"db"` 数据库句柄 | server 的 data 能力（`data-*` 消息面 / MCP 工具） |
| `{{env.*}}` 环境注入 | 工具参数显式传入 / server 配置 |
| `$RETURN` 结果通道 | 工具返回值（`structuredContent`） |
| `FILE_*` 指令 | file server |
| `WEB_*` 指令 | browser 插件 |
| `PC_*` 指令 | desktop 插件（UDS + 协议文档） |
| `LLM` 动作 | llm server（`llm_run`） |
| 无参 `LOOP` 上限 50 | 删除（无流控即无上限概念） |

> **不保留兼容**：旧脚本不解析、不迁移、不报友好错误（DSL 语法不再被任何工具识别，自然落到普通参数校验失败）。

### 3.2 文件域 11 工具

**工具清单**：

| 工具 | 用途 | 关键参数 |
|------|------|---------|
| `file_read` | 读文件（文本/二进制元信息） | `targets[].path`、`encoding`、`maxBytes` |
| `file_write` | 写文件（覆盖/新建） | `targets[].path`、`content`、`encoding`、`bom` |
| `file_replace` | 精确替换 | `targets[].path`、`find`、`replace`、`all` |
| `file_patch` | 上下文补丁（unified diff 风格） | `targets[].path`、`patch` |
| `file_diff` | 两文件/两版本对比 | `targets[].left`、`right` |
| `file_delete` | 删除文件/目录 | `targets[].path`、`recursive` |
| `file_move` | 移动/重命名 | `targets[].from`、`to` |
| `file_copy` | 复制 | `targets[].from`、`to` |
| `file_find` | **四合一**：glob + grep + 列目录 + 树 | 见下 |
| `file_detect` | 编码/BOM/二进制检测 | `targets[].path`、`sampleBytes` |
| `file_iconv` | 显式编码转换 | `targets[].path`、`to`、`bom` |

**统一参数模型**：

```json
{
  "purpose": "说明本次批量意图（给 LLM 自述 + 审计）",
  "targets": [ { "path": "...", "...": "..." } ]
}
```

- 单文件操作 = `targets` 长度 1 的数组（**不设两套形状**）。
- `targets` 数组长度**硬上限 2000**（与 `file_find` 的 `limit` 同口径），超出 → 整体失败并提示分批。
- **参数级违规**（缺字段 / 类型错）= **整体失败**，不做部分执行。
- **运行期冲突**（目标不存在 / 权限拒绝）= **逐项 `failed`**，其余继续，返回逐项结果。

**`file_find` 四合一**：

| 维度 | 参数 | 说明 |
|------|------|------|
| glob | `glob` | 文件名模式（如 `**/*.go`） |
| grep | `pattern` + `regex` | 内容匹配，返回命中行号与行内容 |
| dir | `dir` 模式 | 只列直接子项（等价 `ls`） |
| tree | `output=tree` | 递归树形（受 `depth` 限制） |
| 输出 | `output=json\|tree\|list` | **新增 `json`**，结构化可编程消费 |
| 条数上限 | `limit` | **默认 2000，硬上限 2000**；超出按字典序截断（见下） |
| 元信息 | `meta=none\|basic\|hash` | `basic` = size/mtime；`hash` = md5，**只对已返回条目计算**，且受 `maxHashBytes`（默认 512MB）限制 |
| 确定性 | — | 结果**按相对路径字典序**排序，保证可重放与截断稳定 |

> **为什么要 `limit`**：`glob` 可以写得很宽（如 `**/*`），匹配数可能上万。不设上限时，结果会撑爆工具返回体（进 LLM 上下文是灾难），`meta=hash` 更会变成读整个磁盘。
>
> 规则：
> 1. `limit` **默认与硬上限都是 2000**；按相对路径字典序取前 N 条（排序保证截断可重放）。
> 2. 截断时返回 `truncated: true`；`matched_total` 在 `glob` / `dir` / `tree` 下是**精确值**（只遍历、不读内容），`grep` 下省略（必须读内容才知是否命中，改给 `scanned` = 已扫文件数）。
> 3. `meta=hash` 只对**已返回的**条目计算，再受 `maxHashBytes`（默认 512MB）限制——**两层预算：先控条数，再控字节**。
> 4. `grep` 单文件命中行数按 `maxHitsPerFile`（默认 100）截断，避免单个大日志刷屏。
>
> 要拿全量：**收窄 `glob` / 调整 `depth` 后多次调用**，而不是把 `limit` 调大（硬上限不可越过）。

> 有 `meta=hash` 即不需要单独的 `file_md5`；有 `grep` 命中行号即不需要 `file_lines`。

**`file_detect` 四层判定**：

1. **硬门槛**：BOM 存在 → 直接定编码；NUL 字节比例超阈 → 判二进制。
2. **字符集分布统计**：字节区间分布打分（UTF-8 合法性 / 双字节区间密度）。
3. **常用字覆盖**：按候选编码解码后，统计常用字（汉字/假名/谚文）占比。
4. **LLM 仲裁**（**不在工具内**）：工具只返回 `{candidates[], scores[], sample}`，由调用方（LLM）看样本判「乱码 vs 自然语言」。

关键规则：Shift-JIS 与 GBK 都能解出汉字，**必须靠"解出来是不是自然语言"区分**——半角片假名（0xA1–0xDF）在 Shift-JIS 中是合法单字节，在 GBK 中会与后继字节拼成生僻字，这是最强判据之一。

**`file_iconv`**：

- 检测复用 `fileops.DecodeText` 链（UTF-16 BOM → UTF-8 → GBK/ShiftJIS/Big5/EUCKR）。
- **低置信度拒绝自动转**：GBK / Big5 / Shift-JIS 互相撞车时返回 `encoding_undetermined` + 候选，要求调用方显式指定 `from`。
- `bom = keep | add | strip`。
- 幂等：已是目标编码 → `skipped`，不重写。
- **原子写**：临时文件 + rename，失败不留半截文件。

### 3.3 服务形态（一个内置 server + 第三方）

| server | 承载 | 隔离档 | 备注 |
|--------|------|-------|------|
| **内置 mcp-server**（**1 个进程**，原设计） | **全部工具**：<br>· `file` 组 ×11<br>· `asset` 组（资产 CRUD + prompts/resources 暴露）<br>· `meta` 组 ×3（`mcp_find` / `mcp_load` / `mcp_invoke`）<br>· `llm` 组 ×1（`llm_run`） | 共享常驻（`poolSize = 1`） | **原设计**：契约扫描在此 = 唯一权威；handler **进程内**，每次调用不再 spawn；组 = 代码分包，**不是进程边界**。**〔2026-11-04 核对〕现行已分源，见下表注** |
| **browser 插件** | `start` / `stop` / `operation` + context 池 | **不接 gateway**（UDS 服务，脚本直连） | 见 §3.9。**〔2026-11-04 核对〕引擎 UDS 成立；外层已注册 `browser_run`** |
| **desktop 插件** | `start` / `stop` / `operation`（HWND 查询/操作/截图） | **不接 gateway**（UDS 服务，脚本直连） | 见 §3.10。**〔2026-11-04 核对〕引擎 UDS 成立；外层已注册 `desktop_run`** |
| **第三方 MCP** | 用户配置 | 共享常驻 | stdio / http / sse |

> **〔2026-11-04 核对 · 现行 server 形态与契约来源〕**
> - 现行不是“1 个内置 server”：`file` / `task` / `mcp` / `web` / `browser` / `desktop` / `crontab` / `log` 等各自插件 MCP server；server 域 6 工具（`ask_user` / `next_run` / `subchat_finish` / `subchat_open` / `tool_result` / `tool_stop`）经 llm-server 进程内注册；`core-mcp-server` 仅作为 capability 目录节点的 `--stdio --root <folder>` 下游保留，桌面单体 `gui/host` 明确不再拉起它（`gui/host/main.go:224-225,555`）。
> - **契约分源**：server 域 6 = `src/lib/llm/contracts/tools/*.tool.md`；file 11 = `chonkpilot-plugins/file/src/tools/file/*.tool.md`；task 4 = `chonkpilot-plugins/task/src/tools/task/*.tool.md`；crontab 6 = `chonkpilot-plugins/crontab/src/tools/crontab/*.tool.md`；mcp / web / browser / desktop / codegraph 等由代码 `p.Tool(...)` 注册，**没有 `.tool.md`**。`src/initdata/capability/tools/` 为空。
> - 因此“契约扫描唯一权威 = 内置 mcp-server”不成立，现行是“每个 server 自报工具、gateway 汇总”。

**为什么集成一个 server（用户 2026-10-21 决策）**：

现状是**一工具一 exe**（`src/lib/llm/cmd/` 下 `meta-find-exec` / `meta-load-exec` / `meta-invoke-exec` / `ask-user-exec` / `tool-result-exec` / `tool-stop-exec` / `subchat-open-exec` / `subchat-finish-exec` / `next-run-exec`，加 `mcp-tools` 下的 filesys / browser / desktop executor）。**收敛为 1 个 server 进程**：

| 维度 | 一工具一 exe | 集成一个 server |
|---|---|---|
| 每次调用 | spawn 新进程（~10–50ms + 句柄开销） | 一对 JSON-RPC 报文（亚毫秒） |
| 进程数 | 工具数 × 并发 | **1** |
| 共享代码 | 每个 exe 各自链接 `fileops` / 协议栈 | 进程内函数调用 |
| 组合能力 | 跨进程，做不到 | `meta` 组可直接用同进程的 `asset` / `file` 实现 |
| 故障域 | 单工具崩只死自己 | **全部工具一起死**（补偿见下） |

**集成的代价与补偿（必须实现，否则不予合入）**：

1. **故障域合并** → 每个工具 handler **必须包 `recover()`**：panic 转成 `isError` 结果返回，绝不冒泡到 server 主循环。
2. **单工具超时** → 每个 handler 执行时带自己的 ctx 超时（默认取 gateway 下发值），超时只终止该调用，不影响同进程其他调用。
3. **并发饿死** → `maxConcurrent`（默认 5）是 **server 级**；慢工具（`llm_run` 可能跑几十秒）会占槽 → 给 `llm` 组单独配额（见 §3.6）。
4. **循环依赖** → server **不 import gateway**；`meta` 组的 `mcp_invoke` 与 `llm` 组的 `llm_run` 经**宿主能力面**（BRIDGE-2 的 5 类原子能力）回调，由宿主转给 gateway，形成 `server → 宿主 → gateway → server` 的单向链。

**契约扫描唯一权威 = 内置 mcp-server**（原设计）：启动时扫 `<级别>/capability/tools/` 并注册全部工具，对外 `tools/list` 即权威清单；第三方 server 的 `tools/list` 只用于**校验漂移**。

> **〔2026-11-04 核对〕** 现行 `src/initdata/capability/tools/` 为空，契约分散在主仓 `llm/contracts/tools/` 与 file/task/crontab 插件仓，另有一批工具由代码 `p.Tool(...)` 注册、无 `.tool.md`；gateway 中**没有 drift 校验代码**（grep `drift` 零命中），“第三方 tools/list 比对”未实现。

**`mcp_find` 能力映射**（`meta` 组内组合，不新增底层能力）：

| 组合工具 | 底层能力 |
|---------|---------|
| `mcp_find` | `tools.list`（带完整 inputSchema）+ `config.knowledge.list` + `llm.step`（语义排序） |
| `mcp_invoke` | `tools.call` / `tools.wait` |
| `llm_run` | `session.start` + `session.send` |

**护栏**：`mcp_invoke` 禁自指；`invoke_depth ≤ 3`；白名单在**执行面**判（不在 meta server 判）；上下文透传 `instance/session/turn/tool_call_id`。

### 3.4 MCP 协议 v1（执行体 = MCP server）

> **术语**：`executor` 是旧称（"一个工具一个可执行体"）；新架构里它就是 **MCP server 进程**（一个 server 承载同域多个工具）。本文其余处一律用 "server"。

**身份变更**：执行体**不再是"被 spawn 的参数化进程"**，而是**一个 MCP server**。stdio JSON-RPC 是唯一交互协议。

| 环节 | 约定 |
|------|------|
| 握手 | 标准 `initialize` / `initialized`；`capabilities.tools` 必开 |
| 契约绑定 | **原设计**：内置 server：契约即实现（启动扫描并注册，无漂移概念）。<br>**第三方 server**：`initialize` 后首次 `tools/list` 与内置 server 的权威清单比对——<br>· 声明**多于**契约 → **拒绝注册**<br>· 声明**少于**契约 → 记 `drift` 告警，该工具标记不可用<br>· 参数 schema 不一致 → 记 `drift` 告警（不阻断启动，执行面仍按 server 实现）。<br>**〔2026-11-04 核对〕** 现行契约分源（§3.3 注）；gateway 无 drift 校验，上述“多于/少于/schema 不一致”均未实现。 |
| 上下文 | `_meta.chonkpilot = {instance_id, session_id, turn_id, tool_call_id, work_dir, data_dir, purpose}`（已实现） |
| 进度 | `notifications/progress` + `progressToken`（token = `tool_call_id`）（已实现） |
| 交互 | `elicitation/create`（需用户回答）。**〔2026-11-04 核对〕未接线**：`ElicitHandler` 恒 nil、客户端不声明能力（见 26 §0 ⑤） |
| 取消 | `notifications/cancelled`（`requestId` = 该次调用 id）（已实现） |
| 结果 | 文本 + `structuredContent`（机器可读）；错误走 `isError` + 错误码 |
| 日志 | stdout **只允许** JSON-RPC 帧（原子整帧写）；日志一律 stderr |

> 依据：仓库锁定的 go-sdk v1.7.0 已支持 `GetProgressToken` / `NotifyProgress` / `elicitation/create` / `notifications/cancelled` / `AddSendingCustomMethod`，**无需自造 dispatch 协议**。

### 3.5 Gateway 纯化

**新结构**：

```text
gateway {
  Contracts   工具契约索引（内置 server 权威清单 + 第三方 tools/list 汇总与漂移校验）
  Policy      执行策略（自动执行 / 异步 / 超时裁决 / 错误分类）
  Dispatcher  传输与调用（MCP client，连接池 / 进程池）
  TaskLayer   任务层（执行池 / in-flight 注册表 / 取消阶梯）
}
```

> **〔2026-11-04 核对〕** 四层结构与 `chonkpilot-agent/gateway/src` 文件划分一致，但 `Contracts` 的“漂移校验”未实现（grep `drift` 零命中）；现行 gateway 只汇总各 server 自报工具。

**删除清单**（验收逐条对照）：

| 删除项 | 现状位置 |
|--------|---------|
| `Params.MCPServer`（self 节点） | `assembly.Build` 装配处 |
| `RegisterContracts` / `ContractScanner` 注入 | `assembly.Build` / `Stack.Scan` |
| 进程内 MCP（`NewInMemoryTransports`） | gateway 构造路径 |
| 资产注册表（`assetsByKind`） | `meta_tools.go` → 移 asset server |
| `llmToolPick`（gateway 内部调 LLM） | `meta_tools.go` → 移 meta server |
| 工具目录扫描 | 移 asset server |

**保留**：`mqadapter.Transport`（MOD-1 M0.5 已定，gateway 内核只认 `Transport` 端口）、`ExecSink`（任务层接入）、`Policy`。

**验收 CI 检查（6 条）**：

1. `grep -r "MCPServer" src/lib/gateway/` → 零命中
2. `grep -r "ContractScanner" src/lib/gateway/` → 零命中
3. `go list -deps ./src/lib/gateway/... | grep chonkpilot-mcp-server` → 空
4. `grep -r "InMemoryTransport" src/lib/gateway/` → 零命中
5. `grep -r "assetsByKind\|llmToolPick" src/lib/gateway/` → 零命中
6. gateway 包内无 `mcp.NewServer(` 调用

### 3.6 池化：进程池与连接池

> **〔2026-10-28 订正 · 已实施〕L6 改为「动态池 · 不保留 free 池」**（见 §5 L6 行 + [26 §9](../spec/20-modules/26-mcp-gateway.md)）：本节下面的 `poolKey` 两档 / `poolSize` 预建 / free 队列 + 空闲 TTL **均不实施**；实际实现 = 键 `(workdir, exe)` 的动态实例池（实例数按需 0..`maxConcurrent`、每键等待队列、调用结束 1s 交接、清理三条）。以下 `poolKey`/`poolSize` 段落仅作**历史设计记录**。

**按 transport 分模型**：

| transport | 进程池 | 连接池 | 关系 | 扩容语义 |
|-----------|-------|--------|------|---------|
| **stdio** | N 个同名 server 进程 | 每进程一条双向管道 = 一条会话 | **1:1** | 扩容 = 再 spawn 一个；有状态 server 严格禁止 |
| **http**（streamable） | 0（纯远程）或 N（本地 spawned） | 每端点 M 条会话（`Mcp-Session-Id` 区分）；底层另走 HTTP keep-alive | **1:M** | 进程不扩、连接可扩 |
| **sse**（旧式） | 0 或 N | 一个会话 = 一条常连 GET + POST 上行 | **1:M** | 常连数即并发上限 |

**注册项（`ServerEntry` 内嵌池配置）**：

```text
ServerEntry{
  name, transport,
  poolKey:       shared | call,              // 池键（决定命运共享边界）
  poolSize:      N,                          // stdio 默认 1
  maxConcurrent: N,                          // 默认 5，UI 可配
  graceMs:       2000,                       // 取消 L1 宽限
  killPolicy:    cascade-report,             // 杀进程连带语义
}
```

**池键语义（两档）**：

| 档位 | 池键 | 池大小 | 用途 |
|------|------|-------|------|
| 共享常驻 | `(server)` | `poolSize` 可 >1 | 默认；无 instance 私有状态的 server |
| per-call | `(server, call_id)` | 1，用完即删 | 无法保证优雅取消、需要强隔离的调用（旧 executor 模式的池化表达） |

> **per-(域, instance) 档已删除**：它原本只为 browser / desktop 而设；两者现已退出 gateway（§3.9 / §3.10），此档**零使用者**，保留即死代码。若将来出现确实持有 instance 私有状态的 **MCP server**，需重新评估后恢复该档。

**无 builtin 分支**：内置 server 与第三方走同一路径（用户口径「现在都是第三方，没必要区别」），`Origin=builtin` 仅作审计标记，不影响池/并发/取消行为。

**内置 server（1 个）的池与配额**：

- `poolSize = 1`（单进程，扩不出第二个——否则契约扫描与内存状态会分裂）。
- `maxConcurrent = 5`（server 级闸）。
- **`llm` 组单独配额**：`llm_run` 一次可能跑几十秒，若与 file 工具抢同一个 5 槽，会把文件操作饿死 → `llm` 组最多占 1 槽，且不阻塞其余工具（server 内部按组计数，不是全局互斥）。

**`maxConcurrent` 语义澄清**：上一个 response 返回 **不等于** server 内部空闲——server 可能还有后台线程/任务在跑。因此并发上限是**配置值**，不靠探测。

**现状差距（需改造）**：

- `connSlot` 把 `conn + cmd` 绑成一体（只对 stdio 成立）→ 拆 `procSlot` / `connSlot`。
- `inFlight` 只有计数 → 升级为 `call_id → inflight` 映射。
- `isolate bool` → 扩为 `poolKey + poolSize`。

### 3.7 取消阶梯

```text
cancel(call_id):
  L0  notifications/cancelled(call_id)      # 协议内优雅（stdio/http/sse 均支持）
  L1  等 graceMs（默认 2000）                # response 回来 → 结束
  L2  仍未回 → 关闭该调用所在 session/连接
        · http(streamable): DELETE session
        · sse:              断常连 GET
        · stdio:            关 stdin
  L3  spawned 进程仍活着 → kill 进程树
```

**远程 server 的 L3 不适用**：纯远程 http/sse 没有本地进程可杀，L3 回退为 **L2 断连**（用户口径「http、sse 对远程的 kill，是断开连接」）。

**非 MCP 通道（browser / desktop 引擎）不走本阶梯（原设计）**：引擎不接 gateway，没有 `call_id` 层面的协议取消。**〔2026-11-04 核对〕** 外层 `browser_run` / `desktop_run` 已是 MCP 工具，其调用取消走 gateway 本阶梯；下列 UDS `cancel` 帧仅适用于脚本直连引擎的路径。

- 脚本侧：关闭 UDS 连接，或发 `cancel` 帧（带 `request_id`）；
- 插件侧：立即中止当前 `operation`，返回 `cancelled`；
- 兜底：插件内 operation 自带 hard timeout（默认 60s）；
- **不杀进程**：context / session 相互独立，杀进程会连带同插件进程上其他脚本的会话，代价远大于收益；只有进程无响应（health 探测超时）才由 supervisor 重启。

**杀进程连带语义 = `cascade-report`（方案 A）**：

- stdio 单进程上可能同时有多个调用在飞 → kill 会**连带**取消它们。
- 被连带者**明确上报**：`error.kind = slot_killed`，附受影响 `call_id` 列表。
- **不选 B（先排空再杀）**：stdio 单连接上排空往往等不到，反而拖长取消时延；明确上报比假装干净更可靠。

**隔离摊薄**：可能被强杀的调用应放独占槽（`poolKey=call`），槽里只有它自己，杀它零牵连。

### 3.8 通知路由

池化后 gateway 与 server 之间多了调度层，**progress / elicitation 不能假设"当前串行的就是它"**：

```text
inflight{ call_id, slot_id, instance, tool, started_at, phase, cancelled }
```

- `progressToken` = `call_id`，server 回调 → 按 token 查 `inflight` → 投给对应调用方。
- `elicitation/create` 同理按 `call_id` 路由到等待中的交互面板。
- 未命中 token 的 progress = **丢弃并记 warn**（不猜、不广播）。

### 3.9 browser 插件（引擎 = UDS 脚本能力面；外层 = `browser_run` 工具）

**定位（原设计）**：browser **不做 MCP 工具、不接 gateway**。它是一个 UDS 常驻服务，LLM 编写测试/自动化脚本时按协议文档直接写 socket 调用（提示词内置骨架）。理由见 R16。
**〔2026-11-04 核对〕** 该定位只对**引擎**成立（`browser/src/engine/main.go` 仍写“独立 exe，不注册 MCP、不接 gateway”，三端点 UDS 未变）；外层 `browser/src/main.go:55-56` 已把引擎包成 **`browser_run` hot MCP 工具**，manifest `hot_tools:["browser_run"]`、`namespace:"-"`。

**形态**（沿用 indexer 既有模式）：

| 项 | 值 |
|----|----|
| 工程 | 独立 exe，**GUI 子系统 + 系统托盘**（同 `codegraph-indexer` 的 `trayconsole` 形态） |
| 生命周期 | 宿主 supervisor 拉起（同 `servertools.StartSupervisor`：1s→30s 退避重启，连续 3 次失败放弃） |
| 通信 | **自有 UDS 协议**（`net.Listen("unix", …)`，复用 `core/uds`；非 MCP、不走插件桥工具面） |
| 承载 | **WebView2**（替代原 Chrome/chromedp 路线，用系统 Edge 运行时，无需下载浏览器） |
| 端点 | `start` / `stop` / `operation` |
| 回收 | **context 空闲 10 分钟自动回收** |
| 单实例 | `SingleInstance: true`（同 indexer） |

**控制面与 socket 路径**：

- 控制面**不走插件桥**（不依赖 `server-ready` 事件）：宿主 supervisor 直接 spawn，`SingleInstance` 保证单进程；异常退出按 1s→30s 退避重启。
- socket 路径：`%LOCALAPPDATA%\chonkpilot\plugins\browser.sock`（**固定短路径**，不放进 dataDir）；多实例加 `-<instance 前 8 位>` 后缀。
- **Windows AF_UNIX 路径上限约 108 字节**——这是用固定短路径、而非 `<dataDir>/plugins/browser.sock` 的原因（后者可能超限）。
- 就绪判定：supervisor 探测 socket 可连即就绪；插件启动时先 `os.Remove` 旧文件再 listen（同 `core/uds` 口径）。

**交付物 = 协议文档 + 提示词骨架（Python 库暂不发布）**：

1. **协议文档**——三端点请求/响应帧（含错误码）；
2. **提示词骨架**——系统提示词 `assetGuide` 内置一段 ~25 行 Python 调用样板（UDS 连接 + 帧编解码），LLM 直接复制改写；
3. ~~Python 客户端库~~ **暂不发布**——协议稳定后再出 pip 包；当前「文档 + 骨架」足够 LLM 写对。

**WebView2 承载能力**（已核实同级仓 fork `chonkpilot-webview2`）：

| 能力 | 支持 | 落点 |
|------|:---:|------|
| **debug 模式** | ✅ | `Debug` + `DevToolsDisabled`（`PutAreDevToolsEnabled(Debug && !DevToolsDisabled)`）：屏蔽用户 F12/右键，同时保留宿主程序化 `OpenDevToolsWindow()`（FORK P5） |
| **profile** | ✅ | `DataPath`（FORK P10），每 context 一个独立用户数据目录；已有按 instance 隔离先例（`~/.chonkpilot/webview2/<id>`） |
| **console 输出** | ⚠️ 能力在、封装缺 | **C 方案先行**：`Eval` 劫持 `console.log/warn/error` → 转发到 consoleSink（零 native 改动）；**A 方案补全**：DevTools Protocol `Runtime.consoleAPICalled`，需补 `CallDevToolsProtocolMethod` / `GetDevToolsProtocolEventReceiver` 的 Go 封装（vtable 槽位已在 `corewebview2.go#L110/L116`） |

**三个端点**：

| 端点 | 输入 | 输出 | 语义 |
|------|------|------|------|
| `start` | `{purpose, headless, profile, debug, console}` | `{context_id, created_at}` | 创建 browser context；`console` = `none\|memory\|stream` |
| `stop` | `{context_id, keep_profile?}` | `{closed}` | 显式销毁（幂等）；`keep_profile` 默认 true，便于复现登录态 |
| `operation` | `{context_id, action, args}` | `{result, snapshot?, console?}` | 单个原子动作（navigate/click/fill/eval/screenshot/wait_for/**console_dump**…） |

**提示词内置骨架（LLM 直接复制，无需第三方库）**：

```python
import socket, json, os

SOCK = os.path.join(os.environ["LOCALAPPDATA"], r"chonkpilot\plugins\browser.sock")

def call(method, params):
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    s.connect(SOCK)
    s.sendall(json.dumps({"method": method, "params": params}, ensure_ascii=False).encode() + b"\n")
    buf = b""
    while not buf.endswith(b"\n"):
        buf += s.recv(65536)
    s.close()
    return json.loads(buf)

ctx = call("start", {"profile": "test-login", "debug": True, "console": "memory"})["context_id"]
call("operation", {"context_id": ctx, "action": "navigate", "args": {"url": "http://localhost:5668"}})
call("operation", {"context_id": ctx, "action": "fill", "args": {"selector": "#user", "value": "admin"}})
call("operation", {"context_id": ctx, "action": "click", "args": {"selector": "#login"}})
print(call("operation", {"context_id": ctx, "action": "console_dump", "args": {}}))
call("stop", {"context_id": ctx})
```

> 帧约定：**一行一个 JSON**（`\n` 结尾），与插件桥帧风格一致；`Python 3.9+ / Windows 10 1803+` 原生支持 `AF_UNIX`。
> `script_run` 已有 python runtime，**无需新增任何工具**。

**instance 问题闭环**：context 由插件进程持有，`context_id` 跨调用稳定；`operation` 带 `context_id` 即回到同一浏览器会话。**这解决了此前悬空的「浏览器 instance 谁持有 / 后续动作如何拿到同一实例」**——不靠 gateway 池，靠插件自身 context 池。

**对 gateway 的推论（原设计）**：browser **不进入 gateway 的 server 列表**，因此不占工具名、不占 `maxConcurrent`、不参与 MCP 池化。per-(域, instance) 隔离档**彻底不需要**——instance 语义完全下沉到插件内部的 context 池。gateway 池模型由此简化为两档（共享常驻 / per-call）。
**〔2026-11-04 核对〕** 现行 `browser_run` 是外层注册的普通 hot 工具，已进入 gateway 的 server 列表并占工具名 / `maxConcurrent`；instance 语义仍由插件内 context 池承担，但“不占池”不再成立。

**10 分钟回收**：

- 计时基准 = 最后一次 `operation` 完成时间。
- 回收 = 关闭 context（**不杀插件进程**）。
- 回收后旧 `context_id` 的 `operation` → 明确错误 `context_expired`（不静默重建，避免行为漂移）。
- 回收前若该 context 有 in-flight operation → 等其结束再回收；超 `graceMs` 仍未结束 → 强制关闭并上报 `context_force_closed`。

**原设计“不进工具面”的理由**：`start`/`stop`/`operation` 是**有状态分步**操作（找元素 → 判断 → 动作 → 再判断），拆成三个 MCP 工具会让 LLM 在工具面里做编排，既丢掉了脚本的判断能力，又让工具面出现一堆无法独立使用的原子。**脚本内组合才是正确形态**。**〔2026-11-04 核对〕** 现行外层用**单个** `browser_run` 工具承载整组动作（而非拆成三个工具），因此“原子工具诱导编排”的顾虑部分缓解，但“把分步操作搬进工具面”本身已发生。

### 3.10 desktop 插件（引擎 = UDS 脚本能力面；外层 = `desktop_run` 工具）

> 命名消歧：这里的 **desktop 插件** 指桌面自动化插件，与构建形态 `desktop`（桌面单体宿主）同名不同物。

**与 browser 同构（原设计）**：独立 UDS 服务 + 协议文档 + 提示词骨架，**不接 gateway、不做 MCP 工具**。
**〔2026-11-04 核对〕** 引擎仍同构（`desktop/src/engine/main.go` 三端点 UDS，动作集齐全）；外层 `desktop/src/main.go:55-56` 已注册 **`desktop_run` hot MCP 工具**，manifest `hot_tools:["desktop_run"]`、`namespace:"-"`。

**形态**：

| 项 | 值 |
|----|----|
| 工程 | 独立 exe，**GUI 子系统 + 系统托盘**（同 browser / indexer） |
| 生命周期 | 宿主 supervisor 直接 spawn（控制面不走插件桥），退避重启同 browser |
| 通信 | **自有 UDS 协议**（与 browser 同风格，复用 `core/uds`） |
| socket | `%LOCALAPPDATA%\chonkpilot\plugins\desktop.sock`（同 browser 用固定短路径） |
| 承载 | Win32 / UI Automation（**不需要 WebView2**） |
| 端点 | `start` / `stop` / `operation` |
| 回收 | **session 空闲 10 分钟自动回收**（只释放会话上下文，**不动目标窗口**） |
| 单实例 | `SingleInstance: true` |

**与 browser 的差异**（同形不同质，必须写清）：

| 维度 | browser | desktop |
|------|---------|---------|
| 承载 | WebView2（需 GUI 消息循环） | Win32 / UIA |
| 句柄 | `context_id`（插件私有，**跨进程无效**） | HWND（**系统全局，跨进程有效**） |
| profile | 每 context 独立用户数据目录 | **无** |
| console | 可捕获（WebView2 console） | **无**；对应物 = 截图 + 窗口树 dump |
| 回收对象 | context（连带页面状态丢失） | session（目标窗口不受影响） |

**三个端点**：

| 端点 | 输入 | 输出 | 语义 |
|------|------|------|------|
| `start` | `{purpose, interactive, target?}` | `{session_id}` | 建立桌面会话；`interactive` 校验当前是否交互会话；`target` 可预绑定窗口 |
| `stop` | `{session_id}` | `{closed}` | 释放会话（幂等），**不动窗口** |
| `operation` | `{session_id, action, args}` | `{result, snapshot?}` | 原子动作：`find_window` / `window_info` / `click` / `input` / `key` / `screenshot` / `wait_for` |

**提示词内置骨架**（与 browser 共用同一 `call()` 函数，仅换 `SOCK` 路径与动作名）：

```python
# SOCK = %LOCALAPPDATA%\chonkpilot\plugins\desktop.sock
s = call("start", {"interactive": True})["session_id"]
win = call("operation", {"session_id": s, "action": "find_window", "args": {"title": "记事本"}})
call("operation", {"session_id": s, "action": "input", "args": {"hwnd": win["hwnd"], "text": "hello"}})
call("operation", {"session_id": s, "action": "screenshot", "args": {"hwnd": win["hwnd"], "path": "shot.png"}})
call("stop", {"session_id": s})
```

**限制（不变，但校验提前）**：注入类操作受**交互会话**限制（服务账户 / 会话 0 不可交互）。`start(interactive=true)` 时**立即校验并明确失败**，不等到 `operation` 才报错。

**与 browser 插件的关系**：两个独立进程，**不合并**——承载不同（WebView2 vs Win32/UIA），合并会让只用其一的场景被迫加载另一方的依赖。两者的调用骨架**共用同一个 `call()` 函数**（仅 `SOCK` 路径与动作名不同）。

**原设计“不进工具面”的理由**：同 §3.9——窗口查找 → 判断 → 动作是多步状态流程，必须脚本内组合。**〔2026-11-04 核对〕** 现行外层已用单个 `desktop_run` 工具承载整组动作。

### 3.11 工具箱沉淀

**〔2026-11-04 核对〕核心入口 MISSING**：全仓无 `asset_save` / `asset_list` / `asset_get` / `asset_delete` 工具，也没有“asset server 的资产保存接口”。现行可用的只有数据层 `KnowledgeSave`（知识库 UI 写入）、`capwatch` 监听 app/user/project/prjusr capability 根并 hot reload（含 `tools/*.tool.md`）、以及 `assetGuide` 提示词；**LLM 把新工具沉淀到项目级 `tools/` 的写入口不存在**。

| 项 | 结论 |
|----|------|
| 落点 | **仅项目级** `<workDir>/.chonkpilot/capability/tools/`（非 usr、非 prjusr） |
| 写入口 | **原设计**：asset server 的资产保存接口 + `capwatch` 热生效。**现行**：无 `asset_save`；`capwatch` 热生效在，写入入口不在 |
| 触发 | ① 检索优先（`mcp_find` 硬规则：先查已有工具再写新脚本）；② 可观测信号（同类目标重复使用不同工具 / 用户显式要求） |
| 提示词落点 | 系统提示词的 `assetGuide`（知识库资产指引层） |
| 类型分类（扫描/检查/校验） | **后置**（本期不做） |
| 共享范围 | 单机每用户共享 / 服务器 Remote Desktop 共享 / client-server 项目目录共享 |

### 3.12 引导与装配

**五阶段**：

```text
Phase 0  参数            main()：旗标 → workdir 默认值 → projectDataDir → workdir 锁
Phase 1  配置快照        BootConfig.Load()：一次性读全量可缓存配置（**原设计；现行只读启动期环境输入，见下注**）
Phase 2  组件构造        wire.Build(snapshot)：instance 在此创建 → 组件树
Phase 3  宿主就绪        窗口 / webview2 / UDS / 托盘 / claim 握手 / MarkReady
Phase 4  运行            事件循环
```

**Phase 1 · `BootConfig` 边界（必须画死）**：

| 进快照 | 不进快照 |
|--------|---------|
| DB 键值配置（usr/prj/prjusr 三层 fallback 结果） | **可发现资产**（工具契约 / prompts / skills / resources 清单） |
| `llms` 表（含 apiKey，**内存中脱敏规则同 T14**） | 会话数据 / 任务树 / 运行态 |
| 目录路径（workdir / datadir / exedir / 各级 capability 根） | 运行期派生值（索引状态 / 缓存） |
| 旗标（形态 / 端口 / 调试开关） | 插件发现结果（运行期由插件桥上报） |
| 池配置（`poolKey` / `poolSize` / `maxConcurrent` / `graceMs` / `killPolicy`） | |
| 超时重试（`responseTimeout` / `streamTimeout` / `retryCount`） | |

> 资产**不进快照**的理由：工具契约由 asset server 运行期扫描，每次新增工具不该改快照结构；否则快照会膨胀成 god object。

> **〔2026-11-04 核对 · 与 §3.12 相反〕** 现行 `appwire.BootConfig` 明确“只是启动期环境输入的一次性快照（命令行、路径、旗标、LLM 缺省、认证域、外部下游、执行池参数）”，**不搬运 usr/prj 运行期配置、不开库、不读配置**（`src/lib/appwire/bootconfig.go:8-13`）；池参数仅 `Pool{CallTimeoutSeconds, MaxTasks, AsyncMode}`。上表“DB 键值配置 / `llms` 表 / 三层 fallback”不属于现行快照。另：`src/server` 仅剩 `chonkpilot-server.exe`，无源码，实际只有 desktop 单体形态，“两形态共用”已不适用。

**与 T14 的同源关系**（用户第 2 条指向的既有设计）：

| 维度 | T14（前端） | `BootConfig`（后端） |
|------|------------|---------------------|
| 消息面 | `config-snapshot` / `config-saved` / `config-deleted` | 无（进程内结构体） |
| 分区键 | `(scope, work_dir, exe_id)` | 同 |
| 层级 | system / user / prj / executor 四层 | 同 |
| 增量 | `revision` | 同（快照带 `revision`） |
| 加载 | 启动只 load 一次 | 同 |
| 脱敏 | 密钥脱敏 | 同（规则单源） |
| 消费 | `configStore` → 页面只读 | 值拷贝下发 → 组件只读 |

**统一原则**：**快照是一个东西、两条消费路径**。后端 `BootConfig` 产出后，前端 `config-snapshot` 从**同一份数据**序列化（同 `revision`），不允许各读各的 DB。

**快照不可变**：构造后只读；组件拿到的是**值拷贝**，不是可变引用。运行期变更走订阅（`WatchLogLevel` 模式推广），组件**禁止**构造后自行开库读配置。

**粘合集中**：

```text
src/lib/appwire/
  bootconfig.go   快照加载 + 三层合并
  wire.go         Build(snapshot) → AppStack{Router, Gateway, Chat, Servers...}
  adapters.go     端口适配（原 chatshadow*.go 纯函数搬移）
```

唯一装配点 `gui/host/main.go` 调 `wire.Build`，Phase 3 只做窗口接线（单形态 desktop）。

**wire 内的连线**（端口版，非回调赋值）：

```text
chat.ToolInvoker.Dispatch  → gateway.Call
chat.LLMClient             → router.Call
chat.ContextProvider.Build → persist/data 读
gateway 结果                → chat.Inbox
宿主 mq 订阅                → chat.Send
```

---

## 4. 规则与约定

| # | 项 | 结论 | 理由 |
|---|----|------|------|
| R1 | 工具协议 | **MCP 唯一（仅工具面）** | 不再自造 dispatch 协议；go-sdk v1.7.0 已覆盖 progress/elicitation/cancel。脚本能力面（browser/desktop）走自有 UDS，不属于工具协议 |
| R2 | server 聚合粒度 | **全部内置工具集成 1 个 server**（四组 = 代码分包，非进程边界） | 一工具一 exe = 每次调用 spawn + 进程爆炸 + 共享逻辑被迫复制；用户 2026-10-21 决策 |
| R3 | 常驻准入判据 | **无状态 + 可优雅取消** 才可进共享常驻 | 不可优雅取消者必须 per-call，杀进程不殃及他人 |
| R4 | 进程粒度 | **server 粒度（常驻共享）** | 一次调用 = 一对 JSON-RPC 报文，不 spawn |
| R5 | 并发上限 | `maxConcurrent` 默认 **5**，UI 可配 | 内置与第三方无差别（用户口径） |
| R6 | 取消 | L0→L1→L2→L3 阶梯 | 优先优雅，杀进程为兜底 |
| R7 | 远程取消 | **L3 回退 L2 断连** | 远程无本地进程可杀 |
| R8 | 杀进程连带 | `cascade-report`（A 方案） | 明确上报优于假装干净 |
| R9 | 通知路由 | `progressToken = call_id` → `inflight` | 池化后不可假设"当前串行即它" |
| R10 | 契约权威 | **asset server 唯一**（原设计） | 下游 `tools/list` 只校验漂移。**〔2026-11-04 核对〕现行契约分源，且 gateway 无 drift 校验；无 asset server** |
| R11 | 配置快照 | 启动只 load 一次；前后端同源 | 组件纯内存可测（MOD-1 C2） |
| R12 | 快照内容 | 只含**可缓存配置**，不含**可发现资产** | 防 god object |
| R13 | 装配 | 单一 `wire.Build`，两形态共用 | 消装配漂移 |
| R14 | 文件工具参数 | 统一 `{purpose, targets[]}` | 单文件也是长度 1 数组，不设两套形状 |
| R15 | DSL | **零兼容** | 不解析、不迁移、不兜底 |
| R16 | browser/desktop | **不接 gateway、不做 MCP 工具**；只提供 UDS + 协议文档（原设计） | 分步状态操作必须脚本组合；进工具面会诱导 LLM 在工具层乱编排。**〔2026-11-04 核对〕已推翻**：引擎仍 UDS；外层插件注册 `browser_run` / `desktop_run` hot MCP 工具 |

---

## 5. 与既有机制的关系

| 既有机制 | 影响 |
|---------|------|
| **MOD-1（LLM 组件化）** | 本专题是其后续：MOD-1 完成「依赖纯净」，本专题完成「职责纯净」。chat 九端口**零变更**；`SubTurnRequest`（DSL 子轮次预留）随 DSL 退役**删除** |
| **能力外溢（插件桥）** | 提供 5 类原子能力 + T14 配置快照；本专题复用其 **UDS 前门**（`core/uds`，UDS 优先 / TCP 仅 loopback 兜底）+ 托盘形态 |
| **61-消息一览** | **原设计：业务消息面零变更**（`instance-claim` / `server-ready` 均不动）。**〔2026-11-04 核对〕不成立**：61 仍保留 `dsl_run` 展示字段专段（`61-消息一览.md:379-380`）与旧 executor/资产面文字，L13 未完成 |
| **62-命令行与参数** | 删除 `chonkpilot-dsl-executor.exe` 相关条目；新增 browser / desktop 两个插件 exe（**已实现**：62 已更新为唯一 desktop 形态） |
| **63-DSL语法** | **整篇作废**（标记 🗄 历史），删除时一并移出 spec 目录或标注废弃（**已实现**：文件头已标“整体作废 2026-10-21”） |
| **25-mcp-server** | **原设计**：core-mcp-server 升级为唯一内置 server，承载 meta / server 组；file 域 11 工具已随 `file` 插件迁出（2026-10-25）。**〔2026-11-04 核对〕** 25 已加 §0 订正，但“唯一内置 server”不成立；现行 server 域 6 由 llm-server 进程内注册，`core-mcp-server` 仅作 capability 目录节点下游 |
| **26-mcp-gateway** | 按 §3.5 重写职责边界（**部分完成**：§0 已订正，正文仍保留 self/dirnode/`Params.MCPServer` 历史段） |
| **02-配置层级** | 配置分级不变；新增「启动快照」读取方式 |
| **18-工具异步超时与取消** | 取消阶梯按 §3.7 扩写，原两档作废 |
| **构建脚本** | 删 `build-dsl-executor.ps1`；browser / desktop / file 等外挂插件 exe 由 `build-plugins.ps1`（`build-desktop.ps1` 调用）产出（Python 库暂不发） |

---

## 6. 边界、异常与重启恢复

### 6.1 边界与异常

| 场景 | 行为 |
|------|------|
| server 进程意外退出 | `watchPooled` 观测到 → 池槽标记失效 → 该槽 in-flight 全部 `server_exited` → 下次调用重建 |
| 契约漂移（声明的工具不存在） | 原设计：注册期即拒（`initialize` 后首次 `tools/list` 校验）。**〔2026-11-04 核对〕未实现**：gateway 无 drift 比对 |
| progress token 未命中 | 丢弃 + `warn`（不猜、不广播） |
| `mcp_invoke` 自指 | 执行面拒绝（`invoke_depth` 计数 + 目标比对） |
| `invoke_depth > 3` | 拒绝，返回明确错误 |
| 快照 `revision` 落后（运行期） | 订阅事件驱动刷新；组件不主动重读 |
| 池满 | 排队（有界队列）；超时 → `pool_timeout` |
| browser context 过期 | `context_expired`（不静默重建） |
| 杀进程连带 | `slot_killed` + 受影响 `call_id` 列表 |
| 文件批量部分失败 | 逐项 `failed`，整体返回 `partial` 状态 |

### 6.2 重启恢复

**逐条回答 71 §3.3**：

| # | 问题 | 答案 |
|---|------|------|
| 1 | 状态落在哪 | **in-flight 全在内存**（`inflight` 映射 / 池槽 / 请求队列）；**任务表落 DB**（`tasktree` / `task_shadow`）；快照由 DB + 配置文件重建 |
| 2 | 用户输入是否受理即持久化 | **是**——`chat.Inbox` 提交即落库（MOD-1 已实现 `CommitInbox`）；工具调用入任务表后再下发 |
| 3 | 非终态由谁收口 | **启动清理**：扫描任务表中 `pending`/`running` 的记录 → 标记 `interrupted`（附"进程重启"原因）；池槽全丢弃重建 |
| 4 | 重建内存态还是收敛后继续 | **收敛**：工具调用**不重跑**（`tool_call_id` 幂等回填）；轮次按 `TurnState` 恢复继续（MOD-1 C10 已落）；browser context 与 desktop session **全部失效**（进程级状态，重启即无） |
| 5 | 副作用语义 | 工具调用 = **至少一次**（gateway 可能重发）；**幂等靠 `tool_call_id`**；写文件类工具用原子写（临时文件 + rename）保证至多一次生效 |

**browser / desktop 插件重启**：browser context 与 desktop session 全部失效 → 客户端 `operation` 返回 `context_expired` / `session_expired`；插件自身由 supervisor 按 1s→30s 退避重启。脚本侧应在连不上 socket 时重新 `start`（提示词骨架里的 `call()` 可加一次重连）。

**池重建**：启动时池为空，首次调用时按 `poolKey` 懒建（single-flight 防重复 spawn）。

---

## 7. 落地清单

> **执行顺序约束（2026-10-21 实施中订正）**：必须**先建后删**——`L2`（新文件工具）先于 `L1`；`L9/L10`（browser/desktop 插件）先于 `L1` 中删除 `browser_run`/`desktop_run` 的动作。否则删完没有替代实现，编译链断裂。
>
> 实际批次：**A `L2`**（新工具）→ **B `L9/L10`**（插件替代）→ **C `L1`**（删 DSL）→ **D `L3/L4`**（工具集成 + 协议）→ **E `L5–L8`**（gateway 纯化/池化/取消）→ **F `L11–L13`**（装配/文档）。
>
> **〔2026-11-04 核对〕** 本表按代码实测重写；`49-待实现项.md` 已清空（12 行、无 GATEWAY-2 条目），不再承载进度。状态符号：`✅ IMPL` = 已实现且一致；`⚠️ PARTIAL` = 部分实现/口径过时；`❌ MISSING` = 文档声称但代码不存在。

| # | 步骤 | 内容（2026-11-04 核对后的实测口径） | 状态 | 关联 |
|---|------|------|:---:|------|
| L1 | DSL 退役 | **删除动作已完成**：`dsl_run` / `filesys_run` / `browser_run` / `desktop_run` 四契约、`core/dsl`、`build-dsl-executor.ps1` 均不存在。**但不是“零残留”**：`chonkpilot-agent/session/src/tasks.go:41-44,59` 仍定义 `TaskKindDslJob/Loop/Parallel/Step` + `DslStepRecord`；`llm/server/tasks.go` 仍转发；`data/facade/tasktree.go` / `data/internal/tasktree` / `task/task.go` 保留 `dsl_job` / `loop_current` / `steps_json`；前端 `useDslView.js` / `views/dsl/DslStepsPanel.vue` / `layout/views/DslStepsSlot.vue` / `SessionChat.vue` 仍渲染 DSL 作业。准确表述 = **“语法执行零残留，历史展示类型保留”** | ⚠️ PARTIAL | §3.1；`chonkpilot-agent/session/src/tasks.go`；`chonkpilot/src/lib/data/facade/tasktree.go` |
| L2 | 文件域 11 工具 | **已实现**：11 个 `file_*.tool.md` 在 `chonkpilot-plugins/file/src/tools/file/`；`internal/filetool` 复用；`targets` 上限 2000；`file_find` `limit=2000`、`maxHitsPerFile=100`、`maxHashBytes=512MiB` 均在位；`cmd/tools-exec` 已删除。位置由“内置 server 的 file 组”变为 **`file` 插件 MCP server** | ✅ IMPL | §3.2；`filetool.go:27`；`query.go:21-22` |
| L3 | 工具集成 | **“进程收敛”已完成，“合并为 1 个内置 server”不成立**：一工具一 exe 已退场（`build-mcp-server.ps1` 记 D-72），但现行 file/task/mcp/web/browser/desktop/crontab/log 各自插件 server；server 域 6 经 llm-server 进程内注册；`core-mcp-server` 仅作 capability 目录节点的 `--stdio --root` 下游；桌面单体 `gui/host` 不再拉起它 | ⚠️ PARTIAL | §3.3；`gui/host/main.go:224-225,555`；`llm/server/capnodes.go:252-277` |
| L4 | MCP 协议 v1 | **大部分已实现**：MCP server 身份、`_meta.chonkpilot`、progress（`progressToken` = call_id）、`notifications/cancelled` 均在位。**未完成**：① elicitation 不接（`ElicitHandler` 恒 nil，客户端不声明能力）；② 第三方 `tools/list` 与权威清单比对 / drift 不存在（grep `drift` 零命中） | ⚠️ PARTIAL | §3.4；`contextmeta.go:13-51`；`provider.go:508`；`mcpgateway.go:61-66` |
| L5 | Gateway 纯化 | **已实现**：`Params` 无 `MCPServer` / `MCPConfig`；gateway 生产源无 `ContractScanner` / `InMemoryTransport` / `assetsByKind` / `llmToolPick` / `mcp.NewServer(`；`go.mod` 无 `chonkpilot-mcp-server`；`purity_test.go` 固化 6 条检查 | ✅ IMPL | §3.5；`mcpgateway.go:34-73`；`purity_test.go` |
| L6 | 池化 | **已实现**：`pool.go` 动态池（键 `(workdir, exe)`、按需 0..`maxConcurrent`、1s 交接、三条清理、不保留 free 池）；`ServerEntry.MaxConcurrent` 缺省 5、`Grace` 缺省 2000ms；`pool_test.go` 覆盖 | ✅ IMPL | §3.6；`chonkpilot-agent/gateway/src/pool.go` |
| L7 | 取消阶梯 | **已实现**：`CancelCall` L0→L3 + 远程回退 L2；`slot_killed` + `Cascaded []call_id`；非优雅收口退役槽 | ✅ IMPL | §3.7；`provider.go:1335-1402` |
| L8 | 通知路由 | **已实现**：`calls map[string]*inflightCall`；`progressToken` 写入 `CallToolParams`；`routeProgress` 按 token 归位、无 token 丢弃 + warn；`mqadapter` 发 `mcp-tools-progress` | ✅ IMPL | §3.8；`provider.go:292-350,1513-1545` |
| L9 | browser 插件 | **引擎已实现，外层已工具化**：`browser/src/engine/main.go` 仍是 UDS 三端点（`start` / `stop` / `operation`）；`browser/src/main.go:55-56` 注册 **`browser_run` hot MCP 工具**（manifest `hot_tools:["browser_run"]`、`namespace:"-"`）。因此“插件 UDS 成立、不接 gateway / 不进工具面不成立” | ⚠️ PARTIAL | §3.9；`browser/src/main.go:55-56` |
| L9a | WebView2 封装补全 | C 方案已落地（`Init` 注入 console 劫持 → `console_dump`）；A 方案（DevTools Protocol `Runtime.consoleAPICalled`）待补；`chonkpilot-webview2` 有 vtable 槽位但无 Go 封装调用 | 🟡 PARTIAL | §3.9；`browserplugin/engine_webview.go:418-438` |
| L10 | desktop 插件 | **引擎已实现，外层已工具化**：`desktop/src/engine/internal/desktopplugin/engine.go` 动作集齐全（find_window/list_windows/window_info/focus/click/input/key/screenshot/move/size/minimize/maximize/restore）；`desktop/src/main.go:55-56` 注册 **`desktop_run` hot MCP 工具** | ⚠️ PARTIAL | §3.10；`desktop/src/main.go:55-56` |
| L11 | 工具箱沉淀 | **核心入口 MISSING**：全仓无 `asset_save` / `asset_*` 工具，也无 asset server；现行只有数据层 `KnowledgeSave` + `capwatch` 热生效（监听 app/user/project/prjusr capability 根，含 `tools/*.tool.md`）+ `assetGuide` 提示词。“热生效”有，“LLM 可写的工具箱沉淀”没有 | ❌ MISSING（核心） | §3.11；`capwatch.go`；`data/facade/knowledge.go:118-131` |
| L12 | 引导与装配 | **已实现但有口径偏差**：`appwire/{bootconfig,wire,adapters}` 存在并被 `gui/host/main.go` 调用；`Build` 产出 Bus/Workspace/Facade/Filesys/Server。分歧：`BootConfig` 只作启动期环境输入（不搬运 usr/prj、不开库不读配置），与 §3.12“Phase 1 全量 DB 快照”相反；`src/server` 仅剩 exe，“两形态共用”已不适用 | ⚠️ PARTIAL | §3.12；`appwire/bootconfig.go:8-13`；`gui/host/main.go:217-238` |
| L13 | 文档同步 | **部分完成**：63 已标“整体作废”；62 已改唯一 desktop 形态；18 已补取消阶梯订正；25/26 已加 §0 订正但正文仍保留 self/dirnode/`Params.MCPServer`/agentbox 历史段；**61 仍保留 `dsl_run` 展示字段专段**（`61-消息一览.md:379-380`）与旧 executor/资产面文字 | ⚠️ PARTIAL | §5；`25-mcp-server.md` §0；`26-mcp-gateway.md` §0；`61-消息一览.md:379-380` |

---

## 8. 关联文档

- [MOD-1 LLM 组件化改造设计方案](../llm-modular/LLM组件化改造设计方案.md) — 前置专题（依赖纯净）
- [插件桥接端口设计方案](../bridge-plugin/插件桥接端口设计方案.md) — 能力外溢 + T14 配置快照
- [LLM 桥接端口设计方案](../llm-bridge/LLM桥接端口设计方案.md) — 脚本直连 LLM 的先例
- [63-DSL语法](../spec/60-reference/63-DSL语法.md) — 退役对象
- [25-mcp-server](../spec/20-modules/25-mcp-server.md) / [26-mcp-gateway](../spec/20-modules/26-mcp-gateway.md) — 已加 §0 订正，正文仍含历史段
- [18-工具异步超时与取消](../spec/10-architecture/18-工具异步超时与取消.md) — 取消阶梯已落地
- [02-配置层级](../spec/00-overview/02-配置层级.md) — 配置分级 + 快照读取
- [61-消息一览](../spec/60-reference/61-消息一览.md) — 仍含 `dsl_run` 展示字段历史段（待 L13 清理）
