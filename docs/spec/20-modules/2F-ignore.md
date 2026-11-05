# 2F · gitignore 语义匹配（`chonkpilot-util/ignore`）

> 状态：⚠️ **内核/外壳已实现**（2026-10-31 更新：I/O 外壳亦迁入 `chonkpilot-util/ignore`，`plugins/shared` 已整体删除；对外改为**实例 API** `ignore.New(root, opts)`；2026-11-04 核对：4 处表述已校正）
> 关联：[75-utility](../70-conventions/75-utility.md) · [29-codegraph §4.1](29-codegraph.md) · [38-检索与索引](../30-function-points/38-检索与索引.md) · [61-消息一览 §3.6](../60-reference/61-消息一览.md) · [10-分层与依赖](../10-architecture/10-分层与依赖.md)
> 代码目录：`chonkpilot-util/ignore/`（module `github.com/lsong98sh/chonkpilot-util`）：`ignore.go` = 纯内核；`io.go` = I/O 外壳 + `Matcher` 实例

---

## 1. 职责与边界

- **纯内核（同包 `ignore.go`）**：gitignore(5) 的解析与匹配 —— `Parse` / `Match` / `MatchesAny` / `LastMatch` / `Rule.Match` / `Rule.Negate` / `BuiltinRules` / `DefaultRules` / `NormalizeRel` / `AncestorDirs`。只做「文本/路径 → 布尔」，不读盘、不缓存、不依赖业务包。
- **I/O 外壳（同包 `io.go`）**：规则来源叠加与遍历 —— `FileRules` / `GlobalRules` / `ExcludeRules`、逐级 `.gitignore` 装载、`New` / `Matcher`（惰性规则缓存）、`Matcher.Ignored` / `Matcher.WalkDir`、`ConfigOptions`。
- **做**：codegraph / vfts 两个索引引擎共用同一套规则语义（纯内核唯一实现，外壳只负责取规则）；插件 `file_list.json` 清单由引擎 `indexed` 派生，**不直接调用 ignore**（2026-11-04 核对，`codegraph/src/manifest.go:1-28`）。
- **不做**：不查 git 索引（已跟踪文件照样受忽略规则约束）；不解析 `core.excludesFile`（仅 XDG 默认位置）；不要求 workdir 是 git 仓库（勾选即生效）；纯内核不持库、无 MQ、无 exe。

---

## 2. 形态与部署

| 项 | 值 |
|----|----|
| 编译形态 | **纯 lib**（`chonkpilot-util/ignore`；纯内核 + I/O 外壳同包） |
| 运行位置 | 进程内（引擎遍历 / 插件扫描） |
| 依赖 | 标准库 + `chonkpilot-util/strlist`（用户规则拆分） |
| 并发 | 纯内核无状态；`Matcher` 内部加锁（`own`/`loaded` 缓存），**同一实例并发安全** |

> 历史：`plugins/shared/ignore`（I/O 外壳）已于 2026-10-31 随 `plugins/shared` 整体删除；主仓 `src/lib/ignore` 已于 2026-10-29 删除，全仓零引用。

---

## 3. 输入输出

### 3.1 纯内核

| API | 输入 | 输出 |
|-----|------|------|
| `Parse(content, base)` | gitignore 文本 + 来源目录 | `[]Rule`（保序；空行 / 注释 / 无效行丢弃） |
| `Rule.Match(rel, isDir)` | 相对路径（`/` 分隔）+ 目录性 | 单条规则是否命中 |
| `Rule.Negate()` | — | 是否 `!` 取反规则 |
| `Match(forced, ordered, rel, isDir)` | 强制组 + 有序组 + 路径 | 是否忽略（最后一条命中决定） |
| `MatchesAny(rules, rel, isDir)` | 规则组 + 路径 | 任一命中（不做 `!` 语义，供「强制排除」） |
| `LastMatch(rules, rel, isDir)` | 规则组 + 路径 | `(ignored, matched)`：区分「未命中」与「命中且取反」 |
| `BuiltinRules` / `DefaultRules` | — | `[]Rule`（内置强制 / 默认排除） |
| `NormalizeRel` / `AncestorDirs` | 相对路径 | 归一路径 / 祖先目录列表 |

### 3.2 I/O 外壳

| 入口 / API | 输入 | 说明 |
|------|------|------|
| `Options{StackGitignore, UserRules}` | 遍历排除配置 | `StackGitignore=false` → 仅「内置强制 + 默认排除 + 用户输入」；`UserRules` = 用户输入规则（最高优先级） |
| `New(root, opts) *Matcher` | 规则根 + 配置 | **推荐用法**：创建一次实例后复用（逐级 `.gitignore` 只解析一次，O(paths)） |
| `(*Matcher).Ignored(rel)` | 相对实例根、`/` 分隔的路径 | `bool`（自身命中 **或**任一祖先目录「目录剪枝」命中） |
| `(*Matcher).WalkDir(fn)` | 回调 | 深度优先遍历实例根：**跳过被忽略目录（不下降）与文件**，其余交 `fn`；根自身恒以 `rel=""` 调用 |
| `WalkDir(root, opts, fn)` | 根 + 配置 + 回调 | 一次性实例的便捷入口（等价 `New(root, opts).WalkDir(fn)`） |
| `ConfigOptions(keys, get, workdir)` | 显式键名 + 配置读取器 + workdir | 构造 `*Options` 与 `enabled`；`workdir` 为空 → error。**当前仅测试使用，无生产调用方**（2026-11-04 核对） |
| `FileRules(path, base)` | 文件路径 + 来源目录 | 读取并解析；文件不存在 / 不可读 → `nil`（静默） |
| `GlobalRules()` / `ExcludeRules(root)` | — / 根目录 | 全局 ignore / `.git/info/exclude` 规则（缺失 → `nil`） |

### 3.3 引擎配置键（`ConfigOptions`）

`ConfigOptions` 不再内置引擎名拼接，由调用方给出显式键名。**注意**：本节是**预留/测试辅助**口径 —— 5 仓 grep 显示 `ConfigOptions` **零生产调用方**（定义 `io.go:63`，使用仅 `io_test.go:218`）；引擎实际自行构造 `ignore.Options`（`treesitter/server/index.go:118-124`、`vfts/server/index.go:104-110`；2026-11-04 核对）。

| `Keys` 字段 | 取值 | 效果 |
|----|------|------|
| `Enabled` | `"true"` | `enabled=true` |
| `SkipDirs` | 逗号 / 分号 / 换行分隔（保序不去重） | → `UserRules`（空串 = 无规则） |
| `StackGitignore` | `"true"` | `StackGitignore`（false → 不读任何 ignore 文件） |

codegraph / vfts 的实际键命名空间（2026-11-04 核对 `codegraph/src/config.go:39-52`）：codegraph = `plugins.codegraph.{enabled,exts,skip-dirs,stack-gitignore}`；vfts = **`plugins.codegraph.vfts.{enabled,exts,skip-dirs,stack-gitignore}`**（挂在 codegraph 插件下，不是 `plugins.vfts.*`）。当前生产调用方直接构造 `Options`，未走 `ConfigOptions`。

---

## 4. 关键流程

### 4.1 规则来源与求值顺序（`decide`：**先命中者决定**，`!` = 取消忽略）

```text
① 内置强制排除 BuiltinRules（.git/ .svn/ .hg/ .chonkpilot/）—— **最先判定**：任一命中即忽略，不可被 '!' 反选
② 用户输入 Options.UserRules —— 最高可反选层（等价 git 命令行 --exclude）
③ 各级 .gitignore —— 目录越深优先级越高；同目录内行序在后覆盖在前（仅 StackGitignore=true 参与）
④ base 组（同一 LastMatch **反向扫描** → 组内优先级：info/exclude > 全局 ignore > 默认排除）
     · .git/info/exclude ExcludeRules（最高）
     · 全局 ignore GlobalRules（$XDG_CONFIG_HOME/git/ignore 或 ~/.config/git/ignore）
     · 默认排除 DefaultRules（node_modules/ __pycache__/ .venv/ venv/ .trae/ dist/ build/ .next/ .nuxt/ out/ target/ vendor/；可被 '!' 反选，最低）
```

> base 组内优先级来自实现：`Matcher` 初始化时按 `DefaultRules() ++ GlobalRules() ++ ExcludeRules()` 追加，`LastMatch` **从尾部反向取首个命中**（末位 = info/exclude，优先胜出）；详情见 §4.2（2026-11-04 核对：无 `newRuleSet` 函数）。
> `GlobalRules` / `ExcludeRules` 与各级 `.gitignore` **仅在 `Options.StackGitignore=true` 时参与**（默认排除恒参与）。

### 4.2 求值（外壳 `Matcher.decide`）

```text
decide(rel, isDir)
 ├─ uignore.MatchesAny(forced, rel)   → true（强制排除：不可被 '!' 反选）
 ├─ uignore.LastMatch(user, rel)      → 命中则按其 negate 返回
 ├─ for dir := dirOf(rel); ; dir = dirOf(dir):
 │      loadOwn(dir)   // 惰性读取 dir 的 .gitignore（stack 关闭 / 已加载 → 跳过）
 │      uignore.LastMatch(own[dir], rel) → 命中则按其 negate 返回（自深到浅，最深优先）
 │      dir == "" → break
 └─ uignore.LastMatch(base, rel)      // 全局 ignore + info/exclude + 默认排除
```

- `MatchesAny`：forced 任一命中即忽略。
- `LastMatch`：从后往前取**首个命中**规则 → `!negate`；无命中 → `(false, false)`。
- `(*Matcher).Ignored(rel)`：先按 `AncestorDirs(rel)` 逐层判「目录剪枝」（祖先被排除 → 其后代恒不索引，**不可被后代 `!` 救回**），再判自身（目录性取磁盘真实类型，不存在 → 按文件）。

### 4.3 解析（纯内核 `parseLine` / `compile` / `globSegment`）

```text
parseLine(raw, base)
 ├─ 去行尾未转义空白（' ' / '\t'；`\ ` 保留字面空白）；去 \r
 ├─ `\#` → 字面 '#'；`#…` → 注释丢弃
 ├─ `\!` → 字面 '!'；`!…` → negate=true
 ├─ 尾 '/' → dirOnly=true
 ├─ 首 '/' 或含 '/' → anchored（相对 base 锚定）
 └─ compile(line, anchored) → Regexp
compile：`\A` [非锚定 → `(?:[^/]+/)*`] + 段拼接（`**` 特判：尾随/整条 → `.*`，前导/中间 → `(?:[^/]+/)*`）
globSegment：`*`→`[^/]*`、`?`→`[^/]`、`[...]`（`!`/`^` 取反，未闭合按字面 '['）、`\x`→字面 x
Rule.Match(rel, isDir)：dirOnly 且非目录 → false；base 非空 → 仅适用于 base 之下（去 base 前缀后匹配）
```

---

## 5. 失败态

| 场景 | 处置 |
|------|------|
| `ConfigOptions` 的 `workdir` 为空 | 返回 `err`（`opts` / `enabled` 仍按已读配置填充） |
| `workdir` 不存在 | **不报错**：逐级 `.gitignore` 读取本就静默容忍缺失 → 判定自然降级为「不排除」 |
| `.gitignore` / 全局 ignore / info/exclude 不可读 | `FileRules` → `nil`（静默，不中断索引） |
| 单行无效 | `parseLine` 丢弃该行（空行 / 注释 / 未闭合字符类按字面处理） |
| `New` 的 root 为空 | `Matcher.Ignored` 恒 `false`（不读 `.gitignore`） |
| `Ignored` 传入 `""` / `".."` / `"../…"` | 恒 `false`（根自身与越界路径不排除） |
| `WalkDir` 的 `fn` 收到 err | 原样转交 `fn`（由调用方决定是否中止） |

---

## 6. 依赖与边界

- **纯内核上游依赖**：仅标准库。
- **外壳上游依赖**：同包纯内核 + `chonkpilot-util/strlist`（用户规则拆分）。
- **下游消费者**：
  - `chonkpilot-plugins/codegraph/src/engines/treesitter/server`（索引遍历排除）· `chonkpilot-plugins/codegraph`（`skip-dirs` / `stack-gitignore` 解析）；
  - `chonkpilot-plugins/codegraph/src/engines/vfts/server`（索引遍历排除）· `chonkpilot-plugins/codegraph`（`vfts_query.go`；`manifest.go` 的清单由引擎 `indexed` 派生，不调用 ignore）。
  - ~~`src/lib/data/internal/indexignored`~~（2026-10-25 随 `data-index-ignored` 主题下线删除）。
- **边界（2026-11-04 核对）**：**排除判定单一实现**在**引擎遍历内部**成立 —— `Matcher.WalkDir` 与 `Matcher.Ignored` 共用同一实例，故对引擎遍历而言 `Ignored(p) ⇔ WalkDir 会跳过 p`。插件 `file_list.json` 清单来自引擎 `indexed` 派生（`codegraph/src/manifest.go:1-28,61-86`），**不另调 ignore**；与 git 的已知差异见 §1。

---

## 7. 现状与待办

- ✅ 2026-10-31：I/O 外壳并入 `chonkpilot-util/ignore`（实例 API `New` / `Matcher`）；`plugins/shared`（含 `ignore` / `pluginhost` / `pluginproto` / `winproc` / `trayconsole` / `indexkind` / `lockfile` / `pluginclient`）整体删除。
- ✅ 2026-10-29：纯内核迁入 `chonkpilot-util/ignore`；主仓孤儿 `src/lib/ignore` 删除、go.mod 引用清零。
- ✅ 已实施：gitignore 语义匹配单一实现；`Matcher.Ignored` / `WalkDir` 供引擎遍历（`ConfigOptions` 为预留/测试辅助，无生产调用方；2026-11-04 核对；~~数据面 `data-index-ignored`~~ 已于 2026-10-25 下线）。
- 已知与 git 的差异（刻意，见 §1）：不查 git 索引；不解析 `core.excludesFile`；不要求 workdir 是 git 仓库。
- 遗留 / 规划：无。

---

## 8. 关联测试（测试落点）

**L1 纯内核（`chonkpilot-util/ignore/ignore_test.go`）**：解析与匹配（注释 / 转义 / `*` 与 `?` 不跨 `/` / `**` 三位置 / 字符类 / dirOnly / 锚定 vs 非锚定 / 有序最后一条胜出 / 内置不可反选）、`MatchesAny` / `LastMatch` / `NormalizeRel` / `AncestorDirs`。

**L1 外壳（`chonkpilot-util/ignore/io_test.go`）**：`WalkDir`（stack 开关 / 父目录剪枝 / info/exclude + 全局 ignore / 用户规则最高优先级）、`ConfigOptions`、`Matcher` 实例复用、`Ignored` 与 `WalkDir` 等价性。

**间接**：引擎侧经 `codegraph/server` / `vfts/server` 白盒与 L4 `run_index_gate` / `run_project_cfg`；vfts 清单（由引擎 `indexed` 派生）经 `run_docs_gate`。
