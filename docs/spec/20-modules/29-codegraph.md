# 29 · codegraph（代码语义索引）

> 状态：✅ 与代码一致（引擎已实现；插件多 workdir 已落地，工具面差异注册留待 v2，见 §9）
> 关联：[28-plugins](28-plugins.md) · [12-数据层](../10-architecture/12-数据层.md)
> 代码目录：`src/plugins/codegraph/`（引擎 exe）· `src/plugins/plugin-codegraph/`（宿主插件）

---

## 1. 职责与边界

- **一句话**：进程内 **tree-sitter** 索引的**独立 console mcp-server**（引擎）+ server 内嵌插件（按 workdir 管理引擎子进程并向 gateway 注册查询工具）。
- **做**：多语言符号/依赖/复杂度/**调用图（caller/callee）**抽取、索引持久化与增量自愈、8 个查询工具 + 3 个管理工具。
- **不做**：不落 chonkpilot.db（索引独立目录）；管理工具不发 LLM（hot=false）；引擎侧不做可见性门控（门控在插件）。

---

## 2. 形态与部署

| 项 | 引擎（codegraph-mcp-server） | 插件（plugin-codegraph） |
|----|------------------------------|--------------------------|
| 形态 | 独立 exe（console，**CGO 允许**） | 内嵌 lib Hook（server 进程内） |
| 运行 | `--http[=addr]`（默认 `127.0.0.1:5701`，端点 `/mcp`）· `--stdio` · `-probe <dir>` · `-dump <dir>`（只读自检：打印落盘命中文件集合 JSON） | stdio 子进程管理（懒建 client） |
| 构建 | `build-codegraph.ps1`（`CGO_ENABLED=1`，PATH 前置 `msys64/ucrt64/bin`） | 随 server 内嵌（`build-desktop.ps1`） |
| 产物 | `dist/plugins/codegraph/chonkpilot-codegraph-mcp-server.exe`；**发行落点 = `<exeDir>/mcps/codebase/`**（插件 `resolveExe` 定位，见下注） | — |

> 插件 `resolveExe` 定位引擎：`Options.Exe` > 环境变量 `CODEGRAPH_EXE` > **`<exeDir>/mcps/codebase/chonkpilot-codegraph-mcp-server.exe`**（**不多路径猜测**，缺失即失败）。

> 与主模块 CGO 禁令不冲突：**禁令仅针对 `src/gui`（`-H windowsgui` 单 exe）**，本组件是独立 console 组件，自由用 CGO（官方 `go-tree-sitter`）。

---

## 3. 对外接口

### 3.1 工具清单（11 = 3 管理 + 8 查询）

| 工具 | hot | 说明 |
|------|:---:|------|
| `codegraph_configure` | ✗ | 配置 workdir：`enabled` / `exts` / `skip_dirs`（用户排除规则，gitignore 语法）/ `stack_gitignore`（是否叠加 gitignore 体系，引擎仅记录，门控在插件）；`mode=clear` 清索引产物 |
| `codegraph_initialize` | ✗ | 全量建索引（同步，落盘 `<workDir>/.chonkpilot/codegraph/`） |
| `codegraph_status` | ✗ | 状态：`disabled/未初始化/indexing/ready/error` + 进度 + 规模 |
| `codegraph_symbol_search` | ✓ | 符号搜索（name 子串/前缀，kind/file 过滤，limit） |
| `codegraph_get_symbol_info` | ✓ | 单符号详情（id 或 file+name）：kind/行号/复杂度/签名 |
| `codegraph_callers` | ✓ | **谁调用了它**：按被调名（`name` 或 `id` 定位目标；`file` 过滤调用方）返回调用方符号（含其 `calls`） |
| `codegraph_callees` | ✓ | **它调用了谁**：按 `id` 或 `file+name` 定位符号，返回其直接调用目标（含 `resolved` 与回填的 file/line） |
| `codegraph_get_dependency_graph` | ✓ | 文件级依赖（原始 import 串） |
| `codegraph_find_circular_deps` | ✓ | 循环依赖（DFS 找环） |
| `codegraph_analyze_complexity` | ✓ | 圈复杂度 topN（决策点启发式） |
| `codegraph_get_module_summary` | ✓ | 目录/项目摘要（文件数/语言分布/符号数/最高复杂度） |

- 查询工具必填 `workdir`；`readyGate` 未就绪 → 结构化应答（`pending/indexing/error/not_initialized`，**不算协议错误**）。
- 注册到 gateway 时（插件侧）去掉 `workdir`（由 `context.instance_id` 注入），`handler_subject=codegraph-tool-call`，hot=true，category=codegraph。
- **调用图语义与限制（重要，工具 description 同步声明）**：`calls` 存的是**调用点文本的最后一段名**（如 `a.b.Foo()` → `Foo`、`fmt.Println` → `Println`、`new Widget()` → `Widget`、`println!` → `println`），查询匹配 = 忽略大小写的**全等** 或 **后缀 `.name`**（故 `pkg.Foo` 与 `Foo` 视为同一目标）。这是**名字级启发式**：**无类型解析、无重载/接收者区分、无跨包唯一性判定**——`Callees` 的 `resolved=true` 仅表示该名在索引内**唯一命中**一个符号，非真实绑定；`Callers` 同理（同名不同定义会一并列出）。仅覆盖**已索引文件**内的**直接**调用；动态派发（`a["x"]()`、反射、`eval`）与未索引符号不参与。

### 3.2 支持语言（7）

`go`(.go) · `javascript`(.js/.jsx/.mjs/.cjs) · `typescript`(.ts) · `tsx`(.tsx) · `python`(.py/.pyw) · `rust`(.rs) · `java`(.java)。

---

## 4. 内部控制流

### 4.1 索引（`server/index.go`）

```text
Configure(enabled, exts, skip_dirs, stack_gitignore)   # 引擎仅记录/存档
Initialize：
  标 indexing → collectSourceFiles（ignore.WalkDir：gitignore 语义排除 + LangForExt + maxFileBytes=8MB）
  → newIndex 逐文件 parseEntry → 每 100 个落一次 meta 进度
  → 标 ready + LastIndexedAt → SaveIndex + saveMeta
Reconcile：基线快检（数量 + mtime/size）→ 差异增删改 → 落盘（未初始化 → ErrNotInitialized）
EnsureReady：单飞，返回 ready/indexing/not_initialized/error
```

**排除语义（完整 gitignore 语义，`server/index.go:excludeOptions` + `collectSourceFiles`）**：
遍历走共享包 `github.com/chonkpilot/chonkpilot-ignore` 的 `ignore.WalkDir`（与 vfts 引擎、vfts 插件清单扫描**同一实现**），
规则来源与优先级（低 → 高，最后一条匹配者决定，`!` = 取消忽略）：

```text
内置强制排除（.git/ .svn/ .hg/ .chonkpilot/，不可被任何 '!' 反选）
→ 默认排除（node_modules/ __pycache__/ .venv/ venv/ .trae/ dist/ build/ .next/ .nuxt/ out/ target/ vendor/，可被 '!' 反选）
→ 全局 ignore（$XDG_CONFIG_HOME/git/ignore 或 ~/.config/git/ignore；不解析 core.excludesFile）
→ <workdir>/.git/info/exclude
→ 各级 .gitignore（目录越深优先级越高，同目录内行序在后覆盖在前）
→ 用户输入（skip_dirs，最高优先级，等价 git 命令行 --exclude）
```

- `stack_gitignore=false` → **只应用「内置强制 + 默认排除 + 用户输入」**，不读任何 `.gitignore`/exclude/全局 ignore。
- 目录被忽略 = **不下降**（`SkipDir`；父目录命中忽略规则时其子级 `!` 规则无法救回）；文件命中即跳过（在扩展名判定**之前**）→ 支持**文件级排除**。
- 规则语法：`#` 注释（`\#` 转义）· `!` 取反（`\!` 转义）· 尾 `/` 仅目录 · 含 `/` 或首 `/` = 相对规则所在目录锚定，否则匹配任意层级同名条目 · `*`/`?` 不跨 `/` · `**`（前导/中间/尾随）· `[...]` 字符类 · `\` 转义 · 行尾空格忽略（`\ ` 保留）。
- 已知与 git 的差异：不查 git 索引（已跟踪文件同样受规则约束）；不解析 `core.excludesFile`；不要求 workdir 是 git 仓库（勾选即生效）。
- 测试：规则族 `src/lib/ignore/ignore_test.go`；引擎集成 `TestCollectSourceFilesStackGitignore`。

### 4.2 解析（`server/extract.go`）

tree-sitter `parser.Parse` → 递归 walk：`defKinds`（符号类别）· `nameKinds` · `decisionKinds`（复杂度分支节点）· `importKinds`（导入）→ 收集 symbols/imports/复杂度/签名。

**调用边提取（`callKinds` + `callsWithin`）**：

| 语言 | 调用节点（grammar 版本） | 被调名字段 |
|------|------------------------------|-----------|
| go | `call_expression` | `function` |
| javascript / typescript / tsx | `call_expression` · `new_expression` | `function` / `constructor` |
| python | `call` | `function` |
| rust | `call_expression` · `macro_invocation` | `function` / `macro` |
| java | `method_invocation` · `object_creation_expression` | `name` / `type` |

- **收集范围**：以函数/方法/构造器定义节点为根遍历子树（仅 `func/method/constructor` 带 `calls`；class/type/interface 等不收集）。
- **剪枝**：子树内遇到定义节点（`defKinds`）或匿名函数作用域（`nestedScopeKinds`：go `func_literal`；js/ts/tsx `function_expression`/`arrow_function`/`generator_function`；python `lambda`；rust `closure_expression`；java `lambda_expression`）即不进入——**内层函数/闭包的调用不计入外层符号**。
- **取名**：取被调子树「最后一段」——成员/选择/作用域表达式递归其最后一段字段（go `selector_expression.field`、js/ts `member_expression.property`、python `attribute.attribute`、rust `field_expression.field`/`scoped_identifier.name`、java `field_access.field`/`scoped_type_identifier`）；java 泛型 `new Base<Args>()` 剥离 `type_arguments` 取 `Base`；go 泛型实例化 `Foo[T](a,b)` 取 `index_expression.operand`（→ `Foo`）；括号表达式下钻；**无法静态取名**者丢弃——go `fns[0]()`（下标为字面量）、js/py `a["x"]()`（`subscript_expression`）。去重 + 字典序稳定排序。
- **限制**：纯 AST 文本口径，**无类型/重载/接收者解析**（见 §3.1）。另：Go 泛型实例化的**单参形式**（`Foo[T](x)`）在本 grammar 下被解析为 `type_conversion_expression`（类型转换）→ 不产生调用边；`m[key]()`（下标为非字面量）会按 `operand` 记为 `m`（假阳性，罕见）。

### 4.3 持久化与调用图数据结构（`server/graph.go` / `server/store.go`）

- 目录：`<workDir>/.chonkpilot/codegraph/`；**索引本体 = bbolt 单文件库 `index.db`**（纯 Go、无 CGO，依赖 `go.etcd.io/bbolt` v1.4.2）；工作区元信息 `meta.json`（`enabled/state/progressDone/progressTotal/err/lastIndexedAt/skipDirs/stackGitignore/exts`）仍为独立 JSON —— **不并入库**：插件需**跨进程**轮询索引进度（`startProgressPoll` 读 `meta.json`），而 bolt 写事务持独占锁 → 跨进程只读取 meta 不可行（见 §4.5）。
- **bucket 设计**（键一律为 store 相对 `/` 路径）：

  | bucket | 键 | 值 |
  |------|---|---|
  | `meta` | `schema` | 格式版本 `codegraph-index/1`（bucket 结构/值编码变更时递增） |
  | `files` | 相对路径 | 文件条目元数据 JSON（`lang/mtime/size/imports/hasErr`） |
  | `symbols` | 相对路径 | 该文件符号数组 JSON（`[]Symbol`，保序，含 `calls`） |

  不另设 `refs`/`deps` bucket：全部查询经内存 `Index`（`syms/byName/byCall`）派生，跨 bucket 拼装 `FileInfo` 徒增风险且无功能收益；**单文件条目即最小同步单元**。
- **写路径（原子性）**：`SaveIndex`（全量；Initialize 结束）= 单事务重建 bucket 后写入全部条目；`Reconcile` = `saveIndexDelta`（单事务**只写变更文件** + 删除已移除文件）。库连接**即开即关**（读 = 只读共享锁、写 = 独占锁），无 `.db` 锁残留（可立即重开 / 被其他进程只读）。
- **载入**：`LoadIndex` 按 `files` 键**升序**拼装 `FileInfo`（= 旧 JSON 按 Path 排序的数组序，保证 `Callers` 截断顺序不变）→ `Index.AddFile` 重建 `syms/byName/byCall`；`-dump <dir>` 只读自检输出该命中文件集合（JSON `{count,files}`），不重建、不写盘。
- **迁移口径（不自动迁移）**：检测到遗留 `index.json`（且无 `index.db`）→ 记日志 + **删除旧文件**（索引为派生数据，重建无损）+ 状态回「未初始化」→ 由插件编排执行一次**全量重建**。
- **损坏兜底**：`index.db` 损坏/截断（`bolt.ErrInvalid` 或读页 panic）→ 记日志 + 删除损坏库 + 状态回「未初始化」（可重建）；写者持锁的 `bolt.ErrTimeout` 视为**瞬时错误上抛**（不删除、不改状态）；空文件视为未初始化。
- **陈旧状态纠正**：`LoadIndex` 发现索引不可用（库缺失/损坏/旧 JSON）而元信息 `state=ready` → 一并回「未初始化」，使**插件侧就绪缓存失效**并触发全量重建（避免"元信息 ready + 无索引"的卡死态）。
- Store 用**相对 `/` 路径**，对外拼绝对。
- `symbolID = file:line:name:kind`；`wsReg` 进程内复用同一 workdir 的 Workspace。
- **`Symbol.Calls []string`（`json:"calls,omitempty"`）**：该符号内的直接被调名（最后一段，去重排序）；随 `index.db` 的 `symbols` bucket 自动往返（`Index` 的 `syms/byName/byCall` 在 `AddFile` 重建）。
- **`Index.byCall map[string][]int`**（小写被调名 → 调用方符号下标）：`Calls` 的**反向索引**，`AddFile` 维护、`RemoveFile` 重建，供 `Callers` 免全表扫描。
- **查询**（`server/graph.go`）：`Callers(target, file, limit) []Symbol`（按名匹配：忽略大小写全等 或 后缀 `.name`；`file` 过滤调用方文件；结果含 `calls`）；`Callees(id, file, name, limit) []CalleeRef`（`CalleeRef{name, resolved, kind, file, line, id}`；按 `id` 或 `file+name` 定位首个符号，逐个 `Calls` 经 `byName` 解析——**唯一命中**才回填 file/line/kind/id 且 `resolved=true`）。

### 4.4 依赖解析（`server/path.go`）

`moduleOf`（读 go.mod module 行，带缓存）· `resolveImport`（JS/TS 相对、Go module 前缀、Py/Java 包路径、Rust `crate::` 尽力解析）· `probeRelative`。

### 4.5 插件侧（`plugin-codegraph`）

见 [28-plugins §3.3](28-plugins.md)：订阅 `instance-*` + `data-prj-config-refresh` + `codegraph-tool-call`；`enable-codegraph` 门控；`syncTools` 单飞注册/注销；**每 workdir 独立引擎 client/子进程**（`p.clients[workdir]`，`clientFor(workdir)` 首调懒建；`engineCall` 按 `args["workdir"]` 取该 workdir 的 client；`onToolCall` 按 `r.workDir` 路由）（每 workdir 独立子进程，独立索引内存/独立串行、互不干扰）。

**索引编排（进度 / 重试 / 空闲回收）**：

- **进度推送（复用既有 `codegraph.status` 面，零新增主题）**：插件在 `configure` / `initialize` 前经 `pushStatusPhase` 写 `codegraph.status`（JSON `{state:"indexing", phase, progressDone, progressTotal}`，`phase=configure|index|…`）；索引期间 `startProgressPoll` 每 **500ms** 轮询引擎落盘 `<workDir>/.chonkpilot/codegraph/meta.json` 的 `done/total`，变化即回写（收口写 `ready`/`error`，失败原因写 `message`/`err`）。UI 只读回显该 key（**补 `phase` / `error.message` / 进度**）。
- **保存幂等 + 去抖（扩至 stack-gitignore）**：索引配置（`codegraph.exts` / `codegraph.skip-dirs` / `codegraph.stack-gitignore`）变更 → 经 `rebuildDebouncer`（一次保存写多键 = 多次 `data-prj-config-refresh`，短窗口合并为**一轮**强制重建；`readIndexConfig` 建基线 `cfgExts/cfgSkipDirs/cfgStack`，**新旧值相同（重复保存同一份配置）不排重建**）。前端 `CodegraphConfig.vue` 保存时三键一起写、`origExts/origSkipDirs/origStackGitignore` 为幂等基线。
- **叠加 gitignore 体系（完整 gitignore 语义）**：插件把 `codegraph.skip-dirs`（用户规则，`splitRules` 保序且保留重复项）与 `codegraph.stack-gitignore`（布尔）**原样下发**引擎（`codegraph_configure` / `codegraph_initialize` 的 `skip_dirs` + `stack_gitignore`）；**插件不再读/解析 `.gitignore`**。规则来源与优先级、匹配语义、与 git 的已知差异见 §4.1（实现 = 共享包 `github.com/chonkpilot/chonkpilot-ignore`，与 vfts 引擎/插件**同一实现**）。
- **失败重试**：`configure` / `initialize` 经 `callWithRetry` 失败重试，参数取 `Options.RetryAttempts`（**缺省 2，含首次**）/ `Options.RetryBackoff`（**缺省 2s**）；≤0 回落缺省。
- **空闲回收（按 workdir 独立）**：`childIdleTimeout=5min` 已接线 —— `loop` 每 `sweepInterval=15s` 调 `sweepIdleClient`：逐 workdir 判定，**该 workdir 无活跃实例**（`refs==0`）且距其 `lastUsed` ≥ 5min → `close()` **该 workdir 的**引擎子进程（下次调用 `clientFor(workdir)` 懒重建）；仍活跃的 workdir 在清扫中刷新其 `lastUsed`（某 workdir 被回收不影响其它 workdir 的子进程）。
- **「重建索引」入口**：设置页 `CodegraphConfig.vue` 顶部按钮（开关关闭时禁用）—— 经**既有 `data-prj-config` 面**让索引配置产生一次变更（删 `codegraph.exts` 键 + 非空则立即回写原值）→ 插件据此**强制全量重建**（零新增消息主题；插件侧对同一次操作的去抖合并只重建一轮）。
- **关闭开关遮蔽陈旧 status**：`cgEnabled=false` → UI 一律显示「已停用」，**不展示陈旧的 `codegraph.status`**（避免误判仍在索引/已就绪），进度/阶段/错误同样不在关闭态回显。

---

## 5. 数据结构与存储

| 数据 | 位置 |
|------|------|
| 索引（`Index{Files, syms, byName, byCall}`；`Symbol.Calls` 调用图；bbolt bucket `meta/files/symbols`） | `<workDir>/.chonkpilot/codegraph/index.db`（bbolt 单文件库） |
| 工作区元信息（`Meta`：enabled/state/进度/err/lastIndexedAt/skipDirs/stackGitignore/exts） | `<workDir>/.chonkpilot/codegraph/meta.json`（跨进程进度读取，见 §4.3） |
| 启用开关 `enable-codegraph` / 索引配置 `codegraph.exts`·`codegraph.skip-dirs`·`codegraph.stack-gitignore` | **prj 库** config（团队共享；插件读写） |
| 状态 `codegraph.status` | **prjusr 库** config（个人运行态；插件回写、UI 只读回显） |

---

## 6. 依赖

- 引擎：官方 `go-sdk` + `github.com/tree-sitter/go-tree-sitter` + 6 个 grammar（社区版）+ **`go.etcd.io/bbolt` v1.4.2（索引落盘，纯 Go、无 CGO）**。**CGO 仅来自 tree-sitter**（见 §2 注）。
- 插件：`src/lib/core` · `src/plugins/plugin`；由 server 内嵌。
- **不依赖** mcp-tools / gateway / data（索引独立）。

---

## 7. 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| 独立 console exe | 与主模块分离，允许 CGO | 主模块 CGO 禁令；tree-sitter 官方绑定需 CGO |
| 索引不入 chonkpilot.db | 独立目录 + 独立落盘 | 派生数据、体量大、可重建 |
| 索引落盘 = **bbolt 单文件库**（`index.db`） | bucket `meta/files/symbols`；事务原子 + 增量只写变更文件 | 旧 JSON「整体重写」非原子且 O(N)；bolt 提供原子事务/增量写/多读单写，纯 Go 不引入 CGO |
| 工作区元信息保留 `meta.json` | 与索引库分离 | 插件需**跨进程**读索引进度；bolt 写期独占锁 → 只读取 meta 不可行 |
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

- ✅ **索引落盘由 JSON 改为 bbolt**：索引本体 = `<workDir>/.chonkpilot/codegraph/index.db`（bucket `meta/files/symbols`；事务原子 + 增量只写变更）；`meta.json` 保留为工作区元信息（跨进程进度）；旧 `index.json` **不自动迁移**（检测到即删除 + 全量重建），损坏/截断库自动删除重建。引擎新增只读自检 `-dump <dir>`（L4 命中集合断言用）。详见 §4.3。
- ✅ **调用图（caller/callee）已落地**：新增 `codegraph_callers` / `codegraph_callees` 两个 hot 查询工具（引擎 11 工具 = 3 管理 + 8 查询；插件 gateway 同步注册 8 个）；`Symbol.Calls` + `Index.byCall` 反向索引；7 语言 AST 调用提取（口径与限制见 §3.1/§4.2）。**已知限制**：名字级启发式（无类型/重载/接收者解析、不区分跨包同名）、仅直接调用、动态派发与未索引符号不参与。
- ✅ **多 workdir 已落地**：引擎子进程与索引状态**已按 workdir 独立**（`p.clients[workdir]` 每 workdir 一个子进程，独立索引内存/独立串行；空闲按 workdir 独立回收）——`codegraph.status` 键结构与 JSON 字段**未变**（写到该 workdir 实例的 prjusr 库）；**工具面/工具名/schema 未变**（LLM 不感知 workdir，靠 `context.instance_id → workdir` 路由）。**工具面差异注册（按 workdir 区分工具名/可见性）留待 v2**（gateway `tools/register` 无 scope，且契约要求工具名 = 引擎名 → 同名只能一份注册，多 workdir 同时启用时仍全局一份、归属排序后第一个 workdir）。
- ⚠️ 引擎侧 `Configure` 的 `enabled` 仅记录，**真正门控在插件**（文档需以此为准）。
- ✅ **索引进度 + 失败重试已落地**：进度经**既有** `codegraph.status` 面回写 `phase`/`progressDone`/`progressTotal`（**无新主题**，轮询引擎 `meta.json`）；`configure`/`initialize` 失败按 `RetryAttempts`/`RetryBackoff`（缺省 2 / 2s）重试。详见 §4.5。
- ✅ **空闲回收已接线（按 workdir 独立）**：`childIdleTimeout`（5min）+ `sweepIdleClient`（15s 扫描）**逐 workdir**回收无引用且长期空闲的引擎子进程（`clientFor` 懒重建）。详见 §4.5。
- ✅ **心跳超时退出判定 = 编译开关（按 `-tags split` 编入）**：本插件 `heartbeatTimeout`（90s）判定**按编译形态分流**——`tick()`（`codegraph.go:259`）调 `sweepInstances`：**分离形态（`-tags split`）** 由 `sweep_split.go` 编入（实例最近注册/心跳超 `instance.HeartbeatTimeout`（90s）→ 视同退出，走**既有** `instanceGone`：解绑 + 递减所属 workdir 引用 → 引用归零按既有规则注销 gateway 工具面；引擎子进程随后由 `sweepIdleClient` 按既有空闲规则回收）；**合并单进程形态（默认构建）为空实现**（`sweep_inprocess.go`，实例失效以**显式 `instance-exit`** 清理）。`instance-heartbeat` 订阅保留（供分离形态）。**空闲基准 = 每 workdir 的 `lastUsed`**——由 `sweepIdleClient` 对活跃 workdir 刷新；`instRec.last` 仅供记录时刻，默认构建下不用于退出判定，split 下用于心跳超时判定。通用口径见 [28-plugins §2](28-plugins.md)。

---

## 10. 关联测试

- 引擎：`src/plugins/codegraph/server/server_test.go`（59 个测试：path / Index / Workspace 持久化 / 查询 / 语言 / 扫描-初始化-自愈 / 参数助手与 readyGate / **调用图** / **bbolt 索引落盘**）。
- 插件：`src/plugins/plugin-codegraph/codegraph_test.go`（工具定义 / schema / exe 路径解析）。

### 单元测试计划

**引擎测试**：`src/plugins/codegraph/server/server_test.go`（`package server` 自包含，依赖文件系统 + tree-sitter/CGO；用 `t.TempDir()` 建临时工作区，`dropWorkspace` 清理全局注册表）。

| 分组 | 测试函数 | 覆盖 |
|------|---------|------|
| 1 路径与导入解析（`path.go`） | `TestCleanPath` `TestRelOf` `TestJoinPath` `TestDirOf` `TestProbeRelative` `TestResolveImport` | `./`/`../`/空段规整、相对路径、拼接、目录提取、JS/TS 相对导入（含扩展名探测）、多语言导入（Go module 前缀 / JS·TS / Python 包路径 / Java / Rust `crate::`） |
| 2 符号索引（`graph.go` Index） | `TestNewIndex` `TestIndexAddFile` `TestIndexRemoveFile` `TestIndexAllSymbols` `TestIndexAddRemoveMultiple` | 初始化、加文件+符号（byName 搜）、删文件、返回副本、多文件交叉增删 |
| 3 工作区持久化（`graph.go` Workspace） | `TestStoreDir` `TestOpen` `TestOpenReuse` `TestSaveLoadMeta` `TestSaveLoadIndex` `TestConfigure` | 目录构造、创建/打开、同 workdir 返回同一实例、meta/索引持久化与恢复、enabled/skipDirs 存档 |
| 4 查询操作 | `TestSearchSymbol` `TestFindSymbol` `TestTopComplexity` `TestModuleSummary` `TestImports` `TestFindCycles` `TestFindCyclesNoCycle` | 按 name/kind/file 搜与排序/limit、按 id 或 file+name 定位、圈复杂度 topN+minCc、摘要统计、文件级依赖、环检测（含/无/多环） |
| 5 索引生命周期（`index.go`） | `TestLangForExt` `TestSupportedLangs` `TestCollectSourceFiles` `TestCollectSourceFilesSkipDirs` **`TestCollectSourceFilesStackGitignore`** `TestInitialize` `TestReconcileNoChange` `TestReconcileNewFile` `TestReconcileModifiedFile` `TestReconcileDeletedFile` `TestEnsureReady` `TestInitializeWithSkipDirs` | 扩展名→语言（7 种）、支持列表、文件扫描（默认排除 / 用户规则 / **gitignore 语义：stack 关不读 .gitignore、目录不下降 + 文件级排除、`!` 反选**）、全量建索引、reconcile 无变/新增/修改/删除、ready 门控、带 skipDirs 初始化 |
| 6 工具辅助函数（`server.go`） | `TestGetString` `TestGetInt` `TestGetBoolPtr` `TestGetStrings` `TestNeedWS` `TestReadyGate` | 参数提取（含默认值/布尔指针/字符串数组）、工作区打开、就绪门控 |
| 7 调用图（`extract.go` `callKinds`/`callsWithin` + `graph.go` `Callers`/`Callees` + `server.go` 新工具） | `TestCallGraphExtraction`（+`persist round-trip`）· `TestCallers` · `TestCallees` · `TestCallGraphTools` | **7 语言真实语料**的调用提取（普通/方法/选择表达式最后一段/跨文件/`new`/宏/泛型剥离/内层函数剪枝）与 `Calls` 落盘往返；查询（精确名/大小写/`pkg.Foo`↔`Foo`/file 过滤/limit/未命中/无索引/调用方自带 calls）；`Callees`（id 或 file+name 定位/唯一解析回填/歧义 `resolved=false`/limit/未找到）；工具层（正常/未就绪/参数缺失/未命中 + `TextToolNames` 自检） |
| 8 索引落盘（bbolt，`store.go`） | `TestIndexStoreUsesBoltDB` · `TestIndexReloadAfterClose` · `TestReconcileIncrementalPersists` · `TestLoadIndexCorruptDB`（garbage/truncated/empty）· `TestLegacyJSONNotMigrated` · `TestConcurrentReadIndex` | 落盘为 `index.db`（不再产出 `index.json`）；即开即关后新实例重开完整读回（符号 ID 重建/导入/基线字段）；增量（新增+修改+删除）只写变更并重开一致；损坏/截断/空库**兜底**（不 panic、视为未初始化可重建）；旧 JSON **不自动迁移**（删除 + 状态回未初始化 + 重建）；并发读安全（16 goroutine） |

**插件测试**：`src/plugins/plugin-codegraph/codegraph_test.go`（不依赖 MQ）：`TestQueryToolDefinitions`（8 个查询工具名称/描述/schema 不含 workdir）· `TestSchemaJSON` · `TestToolReply` · `TestStrval`（string/bool/nil 归一）· `TestResolveExe`（模拟 dist 布局，验证 exe 搜索优先级）· **索引配置**：`TestSplitRules`（规则保序且保留重复项）· `TestReadIndexConfig`（`skip_dirs` 原样透传 + `stack_gitignore` 透传 + 幂等基线播种）· **编排白盒**：`TestRunWithRetry`（重试/退避）· `TestIdleReclaimDue`（空闲回收判定）· `TestPhaseStatusJSON`（进度快照 `{state,phase,progressDone,progressTotal}`）· `TestReadEngineProgress` / `TestPollEngineProgressSequence`（读/轮询引擎 `meta.json`）· **多 workdir**：`TestMultiWorkdirIndexAndQueryIsolation`（两 workdir 各自 `files:3`/`files:7` 互不覆盖、状态各写各的 prj 库、查询不串台）· `TestClientForPerWorkdirIdentity`（同 workdir 复用、异 workdir 各自独立 client）· `TestSweepIdleClientPerWorkdir`（空闲回收按 workdir 独立判定/回收）。

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
