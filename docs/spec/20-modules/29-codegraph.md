# 29 · codegraph（代码语义索引）

> 日期：2026-09-10 ｜ 状态：✅ 与代码一致（引擎已实现；插件多 workdir 已落地，工具面差异注册留待 v2，见 §9）
> 关联：[28-plugins](28-plugins.md) · [12-数据层](../10-architecture/12-数据层.md)
> 代码目录（D-28：`src/codegraph/` → `src/plugins/codegraph/`、`src/plugin-codegraph/` → `src/plugins/plugin-codegraph/`）：`src/plugins/codegraph/`（引擎 exe）· `src/plugins/plugin-codegraph/`（宿主插件）

---

## 1. 职责与边界

- **一句话**：进程内 **tree-sitter** 索引的**独立 console mcp-server**（引擎）+ server 内嵌插件（按 workdir 管理引擎子进程并向 gateway 注册查询工具）。
- **做**：多语言符号/依赖/复杂度抽取、索引持久化与增量自愈、6 个查询工具 + 3 个管理工具。
- **不做**：不落 chonkpilot.db（索引独立目录）；管理工具不发 LLM（hot=false）；引擎侧不做可见性门控（门控在插件）。

---

## 2. 形态与部署

| 项 | 引擎（codegraph-mcp-server） | 插件（plugin-codegraph） |
|----|------------------------------|--------------------------|
| 形态 | 独立 exe（console，**CGO 允许**） | 内嵌 lib Hook（server 进程内） |
| 运行 | `--http[=addr]`（默认 `127.0.0.1:5701`，端点 `/mcp`）· `--stdio` · `-probe <dir>` | stdio 子进程管理（懒建 client） |
| 构建 | `build-codegraph.ps1`（`CGO_ENABLED=1`，PATH 前置 `msys64/ucrt64/bin`） | 随 server 内嵌（`build-desktop.ps1`） |
| 产物 | `dist/codegraph/chonkpilot-codegraph-mcp-server.exe` | — |

> 与主模块 CGO 禁令不冲突：**禁令仅针对 `src/gui`（`-H windowsgui` 单 exe）**，本组件是独立 console 组件，自由用 CGO（官方 `go-tree-sitter`）。

---

## 3. 对外接口

### 3.1 工具清单（9 = 3 管理 + 6 查询）

| 工具 | hot | 说明 |
|------|:---:|------|
| `codegraph_configure` | ✗ | 配置 workdir：`enabled` / `skip_dirs`（引擎仅记录，门控在插件） |
| `codegraph_initialize` | ✗ | 全量建索引（同步，落盘 `<workDir>/.chonkpilot/codegraph/`） |
| `codegraph_status` | ✗ | 状态：`disabled/未初始化/indexing/ready/error` + 进度 + 规模 |
| `codegraph_symbol_search` | ✓ | 符号搜索（name 子串/前缀，kind/file 过滤，limit） |
| `codegraph_get_symbol_info` | ✓ | 单符号详情（id 或 file+name）：kind/行号/复杂度/签名 |
| `codegraph_get_dependency_graph` | ✓ | 文件级依赖（原始 import 串） |
| `codegraph_find_circular_deps` | ✓ | 循环依赖（DFS 找环） |
| `codegraph_analyze_complexity` | ✓ | 圈复杂度 topN（决策点启发式） |
| `codegraph_get_module_summary` | ✓ | 目录/项目摘要（文件数/语言分布/符号数/最高复杂度） |

- 查询工具必填 `workdir`；`readyGate` 未就绪 → 结构化应答（`pending/indexing/error/not_initialized`，**不算协议错误**）。
- 注册到 gateway 时（插件侧）去掉 `workdir`（由 `context.instance_id` 注入），`handler_subject=codegraph-tool-call`，hot=true，category=codegraph。

### 3.2 支持语言（7）

`go`(.go) · `javascript`(.js/.jsx/.mjs/.cjs) · `typescript`(.ts) · `tsx`(.tsx) · `python`(.py/.pyw) · `rust`(.rs) · `java`(.java)。

---

## 4. 内部控制流

### 4.1 索引（`server/index.go`）

```text
Configure(enabled, skip_dirs)   # 引擎仅记录/存档
Initialize：
  标 indexing → collectSourceFiles（WalkDir + 跳目录 + LangForExt + maxFileBytes=8MB）
  → newIndex 逐文件 parseEntry → 每 100 个落一次 meta 进度
  → 标 ready + LastIndexedAt → SaveIndex + saveMeta
Reconcile：基线快检（数量 + mtime/size）→ 差异增删改 → 落盘（未初始化 → ErrNotInitialized）
EnsureReady：单飞，返回 ready/indexing/not_initialized/error
```

- `defaultSkipDirs`：`.git/.svn/.hg/node_modules/__pycache__/.venv/venv/.trae/.chonkpilot/dist/build/.next/.nuxt/out/target/vendor`。

### 4.2 解析（`server/extract.go`）

tree-sitter `parser.Parse` → 递归 walk：`defKinds`（符号类别）· `nameKinds` · `decisionKinds`（复杂度分支节点）· `importKinds`（导入）→ 收集 symbols/imports/复杂度/签名。

### 4.3 持久化（`server/graph.go`）

- 目录：`<workDir>/.chonkpilot/codegraph/`，文件 `meta.json`（`enabled/state/progressDone/progressTotal/err/lastIndexedAt/skipDirs`）+ `index.json`。
- `SaveIndex`：按 Path 排序 → 临时文件 + **rename 原子落盘**。
- Store 用**相对 `/` 路径**，对外拼绝对。
- `symbolID = file:line:name:kind`；`wsReg` 进程内复用同一 workdir 的 Workspace。

### 4.4 依赖解析（`server/path.go`）

`moduleOf`（读 go.mod module 行，带缓存）· `resolveImport`（JS/TS 相对、Go module 前缀、Py/Java 包路径、Rust `crate::` 尽力解析）· `probeRelative`。

### 4.5 插件侧（`plugin-codegraph`）

见 [28-plugins §3.3](28-plugins.md)：订阅 `instance-*` + `data-prj-config-refresh` + `codegraph-tool-call`；`enable-codegraph` 门控；`syncTools` 单飞注册/注销；**每 workdir 独立引擎 client/子进程**（`p.clients[workdir]`，`clientFor(workdir)` 首调懒建；`engineCall` 按 `args["workdir"]` 取该 workdir 的 client；`onToolCall` 按 `r.workDir` 路由）（2026-09-15 后：原「**全局共享 client**」表述作废——T-12/P2-4 已改为每 workdir 独立子进程，独立索引内存/独立串行、互不干扰）。

**索引编排（进度 / 重试 / 空闲回收，T-11/T-18，2026-09-13 后）**：

- **进度推送（T-11，复用既有 `codegraph.status` 面，零新增主题）**：插件在 `configure` / `initialize` 前经 `pushStatusPhase` 写 `codegraph.status`（JSON `{state:"indexing", phase, progressDone, progressTotal}`，`phase=configure|index|…`）；索引期间 `startProgressPoll` 每 **500ms** 轮询引擎落盘 `<workDir>/.chonkpilot/codegraph/meta.json` 的 `done/total`，变化即回写（收口写 `ready`/`error`，失败原因写 `message`/`err`）。UI 只读回显该 key（**补 `phase` / `error.message` / 进度**）。
- **保存幂等 + 去抖（2026-09-15）**：索引配置（`codegraph.exts` / `codegraph.skip-dirs`）变更 → 经 `rebuildDebouncer`（一次保存写两键 = 两次 `data-prj-config-refresh`，短窗口合并为**一轮**强制重建；`readIndexConfig` 建基线，**新旧值相同（重复保存同一份配置）不排重建**）。前端 `CodegraphConfig.vue` 保存时两键一起写、`origExts/origSkipDirs` 为幂等基线。
- **失败重试（T-11）**：`configure` / `initialize` 经 `callWithRetry` 失败重试，参数取 `Options.RetryAttempts`（**缺省 2，含首次**）/ `Options.RetryBackoff`（**缺省 2s**）；≤0 回落缺省。
- **空闲回收（T-18，按 workdir 独立，2026-09-15 后）**：`childIdleTimeout=5min` 已接线 —— `loop` 每 `sweepInterval=15s` 调 `sweepIdleClient`：逐 workdir 判定，**该 workdir 无活跃实例**（`refs==0`）且距其 `lastUsed` ≥ 5min → `close()` **该 workdir 的**引擎子进程（下次调用 `clientFor(workdir)` 懒重建）；仍活跃的 workdir 在清扫中刷新其 `lastUsed`（某 workdir 被回收不影响其它 workdir 的子进程）。（原「`close()` 共享引擎子进程 / `sharedClient` 懒重建」表述随 T-12 作废。）
- **「重建索引」入口（2026-09-15）**：设置页 `CodegraphConfig.vue` 顶部按钮（开关关闭时禁用）—— 经**既有 `data-prj-config` 面**让索引配置产生一次变更（删 `codegraph.exts` 键 + 非空则立即回写原值）→ 插件据此**强制全量重建**（零新增消息主题；插件侧对同一次操作的去抖合并只重建一轮）。
- **关闭开关遮蔽陈旧 status（2026-09-15）**：`cgEnabled=false` → UI 一律显示「已停用」，**不展示陈旧的 `codegraph.status`**（避免误判仍在索引/已就绪），进度/阶段/错误同样不在关闭态回显。

---

## 5. 数据结构与存储

| 数据 | 位置 |
|------|------|
| 索引（`Index{Files, syms, byName}` + `Meta`） | `<workDir>/.chonkpilot/codegraph/{index.json, meta.json}` |
| 启用开关 `enable-codegraph` / 索引配置 `codegraph.exts`·`codegraph.skip-dirs` | **prj 库** config（团队共享；插件读写） |
| 状态 `codegraph.status` | **prjusr 库** config（个人运行态；插件回写、UI 只读回显。**2026-09-15 订正：原记「prj 库」有误**） |

---

## 6. 依赖

- 引擎：官方 `go-sdk` + `github.com/tree-sitter/go-tree-sitter` + 6 个 grammar（社区版）。**CGO**。
- 插件：`src/lib` · `src/plugin`；由 server 内嵌。
- **不依赖** mcp-tools / gateway / data（索引独立）。

---

## 7. 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| 独立 console exe | 与主模块分离，允许 CGO | 主模块 CGO 禁令；tree-sitter 官方绑定需 CGO |
| 索引不入 chonkpilot.db | 独立目录 + 原子落盘 | 派生数据、体量大、可重建 |
| 管理工具 hot=false | 不发 LLM | 管理动作由插件调用 |
| workdir 在插件侧剥离 | gateway 工具去 `workdir` | 由 `context.instance_id` 路由 |
| 就绪门控在插件 | 引擎不判可见性 | 职责单一 |

---

## 8. 场景与边界

- 未初始化时查询 → `readyGate` 结构化应答（不报协议错）。
- 文件 >8MB 跳过；二进制/未知扩展名不索引。
- 索引可重建（`initialize` 幂等）；`Reconcile` 增量自愈。
- 循环依赖仅统计**可解析为仓库内文件**的导入边。

---

## 9. 现状与待办

- ✅ **多 workdir（T-12 / P2-4 / D-11，2026-09-15 后已落地）**：引擎子进程与索引状态**已按 workdir 独立**（`p.clients[workdir]` 每 workdir 一个子进程，独立索引内存/独立串行；空闲按 workdir 独立回收）——`codegraph.status` 键结构与 JSON 字段**未变**（写到该 workdir 实例的 prjusr 库）；**工具面/工具名/schema 未变**（LLM 不感知 workdir，靠 `context.instance_id → workdir` 路由）。**工具面差异注册（按 workdir 区分工具名/可见性）留待 v2**（gateway `tools/register` 无 scope，且契约要求工具名 = 引擎名 → 同名只能一份注册，多 workdir 同时启用时仍全局一份、归属排序后第一个 workdir）。
- ⚠️ 引擎侧 `Configure` 的 `enabled` 仅记录，**真正门控在插件**（文档需以此为准）。
- ✅ **索引进度 + 失败重试已落地（T-11，2026-09-13 后）**：进度经**既有** `codegraph.status` 面回写 `phase`/`progressDone`/`progressTotal`（**无新主题**，轮询引擎 `meta.json`）；`configure`/`initialize` 失败按 `RetryAttempts`/`RetryBackoff`（缺省 2 / 2s）重试。详见 §4.5。
- ✅ **空闲回收已接线（T-18，2026-09-13 后；按 workdir 独立，2026-09-15 后）**：`childIdleTimeout`（5min）+ `sweepIdleClient`（15s 扫描）**逐 workdir**回收无引用且长期空闲的引擎子进程（`clientFor` 懒重建）。详见 §4.5。
- ✅ **心跳超时退出判定 = 编译开关（2026-09-15 移除 → 2026-09-16 改由 `-tags split` 编入）**：本插件原 `heartbeatTimeout`（90s）判定随用户口径移除后，改为**按编译形态分流**——`tick()`（`codegraph.go:259`）调 `sweepInstances`：**分离形态（`-tags split`）** 由 `sweep_split.go` 编入（实例最近注册/心跳超 `instance.HeartbeatTimeout`（90s）→ 视同退出，走**既有** `instanceGone`：解绑 + 递减所属 workdir 引用 → 引用归零按既有规则注销 gateway 工具面；引擎子进程随后由 `sweepIdleClient` 按既有空闲规则回收）；**合并单进程形态（默认构建）为空实现**（`sweep_inprocess.go`，实例失效以**显式 `instance-exit`** 清理）。`instance-heartbeat` 订阅保留（供分离形态）。**（2026-09-15 后：空闲基准改为每 workdir 的 `lastUsed`——由 `sweepIdleClient` 对活跃 workdir 刷新；`instRec.last` 仅供记录时刻，默认构建下不用于退出判定，split 下用于心跳超时判定）** 通用口径见 [28-plugins §2](28-plugins.md)。
- 🔵 早期「两个 mcp-server」设计方案（2026-09-09，已归档）为对齐稿；本文以**现有单引擎 + 插件**实现为准。

---

## 10. 关联测试

- 引擎：`src/plugins/codegraph/server/server_test.go`（46 个测试：path / Index / Workspace 持久化 / 查询 / 语言 / 扫描-初始化-自愈 / 参数助手与 readyGate）。
- 插件：`src/plugins/plugin-codegraph/codegraph_test.go`（工具定义 / schema / exe 路径解析）。

### 单元测试计划（本节由原测试计划整体迁入，2026-09-11）

**引擎测试**：`src/plugins/codegraph/server/server_test.go`（`package server` 自包含，依赖文件系统 + tree-sitter/CGO；用 `t.TempDir()` 建临时工作区，`dropWorkspace` 清理全局注册表）。

| 分组 | 测试函数 | 覆盖 |
|------|---------|------|
| 1 路径与导入解析（`path.go`） | `TestCleanPath` `TestRelOf` `TestJoinPath` `TestDirOf` `TestProbeRelative` `TestResolveImport` | `./`/`../`/空段规整、相对路径、拼接、目录提取、JS/TS 相对导入（含扩展名探测）、多语言导入（Go module 前缀 / JS·TS / Python 包路径 / Java / Rust `crate::`） |
| 2 符号索引（`graph.go` Index） | `TestNewIndex` `TestIndexAddFile` `TestIndexRemoveFile` `TestIndexAllSymbols` `TestIndexAddRemoveMultiple` | 初始化、加文件+符号（byName 搜）、删文件、返回副本、多文件交叉增删 |
| 3 工作区持久化（`graph.go` Workspace） | `TestStoreDir` `TestOpen` `TestOpenReuse` `TestSaveLoadMeta` `TestSaveLoadIndex` `TestConfigure` | 目录构造、创建/打开、同 workdir 返回同一实例、meta/索引持久化与恢复、enabled/skipDirs 存档 |
| 4 查询操作 | `TestSearchSymbol` `TestFindSymbol` `TestTopComplexity` `TestModuleSummary` `TestImports` `TestFindCycles` `TestFindCyclesNoCycle` | 按 name/kind/file 搜与排序/limit、按 id 或 file+name 定位、圈复杂度 topN+minCc、摘要统计、文件级依赖、环检测（含/无/多环） |
| 5 索引生命周期（`index.go`） | `TestLangForExt` `TestSupportedLangs` `TestCollectSourceFiles` `TestInitialize` `TestReconcileNoChange` `TestReconcileNewFile` `TestReconcileModifiedFile` `TestReconcileDeletedFile` `TestEnsureReady` `TestInitializeWithSkipDirs` | 扩展名→语言（7 种）、支持列表、文件扫描（skipDirs）、全量建索引、reconcile 无变/新增/修改/删除、ready 门控、带 skipDirs 初始化 |
| 6 工具辅助函数（`server.go`） | `TestGetString` `TestGetInt` `TestGetBoolPtr` `TestGetStrings` `TestNeedWS` `TestReadyGate` | 参数提取（含默认值/布尔指针/字符串数组）、工作区打开、就绪门控 |

**插件测试**：`src/plugins/plugin-codegraph/codegraph_test.go`（不依赖 MQ）：`TestQueryToolDefinitions`（6 个查询工具名称/描述/schema 不含 workdir）· `TestSchemaJSON` · `TestToolReply` · `TestStrval`（string/bool/nil 归一）· `TestResolveExe`（模拟 dist 布局，验证 exe 搜索优先级）· **编排白盒（T-11/T-18）**：`TestRunWithRetry`（重试/退避）· `TestIdleReclaimDue`（空闲回收判定）· `TestPhaseStatusJSON`（进度快照 `{state,phase,progressDone,progressTotal}`）· `TestReadEngineProgress` / `TestPollEngineProgressSequence`（读/轮询引擎 `meta.json`）· **多 workdir（T-12/P2-4，2026-09-15 后）**：`TestMultiWorkdirIndexAndQueryIsolation`（两 workdir 各自 `files:3`/`files:7` 互不覆盖、状态各写各的 prj 库、查询不串台）· `TestClientForPerWorkdirIdentity`（同 workdir 复用、异 workdir 各自独立 client）· `TestSweepIdleClientPerWorkdir`（空闲回收按 workdir 独立判定/回收）。

**测试数据**：分组 5 需真实源码——`t.TempDir()` 建 `testproj/`（`main.go` 含 `func main`/`type Config`、`utils/helper.go` 含 `func Helper`/`type Result`、`go.mod`）。

**构建与运行**（CGO）：

```powershell
$env:GOROOT="e:\GoDev\go1.26"; $env:Path = "e:\GoDev\go1.26\bin;" + $env:Path
$env:GOPROXY="https://goproxy.cn,direct"
$env:Path = "e:\GoDev\msys64\ucrt64\bin;" + $env:Path   # gcc
cd src/plugins/codegraph ; go test ./server/ -v -count=1
cd src/plugins/plugin-codegraph      ; go test -v -count=1
```

**验证标准**：引擎/插件测试全通过且无 CGO 链接错误；覆盖索引生命周期、查询、导入解析、工作区持久化；全局态（`wsReg`）测试间正确隔离（`defer dropWorkspace`）。

> 合规说明：以上测试均**不涉及 MQ 消息收发**（纯函数 + 临时文件系统），不发送/确认任何消息，天然满足 [61-消息一览](../60-reference/61-消息一览.md) 的测试准则。
