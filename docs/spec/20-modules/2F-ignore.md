# 2F · src/lib/ignore（gitignore 语义匹配）

> 状态：✅ 与代码一致
> 关联：[29-codegraph §4.1](29-codegraph.md) · [38-检索与索引](../30-function-points/38-检索与索引.md) · [61-消息一览 §3.6](../60-reference/61-消息一览.md) · [10-分层与依赖](../10-architecture/10-分层与依赖.md)
> 代码目录：`src/lib/ignore/`（单文件 `ignore.go` + `ignore_test.go`）

---

## 1. 职责与边界

- **一句话职责**：**gitignore(5) 语义的路径排除匹配**的**单一实现** —— 供 codegraph / vfts 两个索引引擎与 vfts 插件的清单扫描共用，三处过滤口径必然一致（引擎排除集与插件 `file_list` 清单不会出现两套判定）。
- **做**：规则解析（`Parse` / `FileRules`）、规则来源叠加与有序求值（`Match` / `ruleSet.decide`）、目录遍历（`WalkDir`）、单路径判定（`Options.Ignored`）、引擎配置读取器（`ConfigOptions`）。
- **不做**：不查 git 索引（已跟踪文件照样受忽略规则约束）；不解析 `core.excludesFile`（仅 XDG 默认位置）；不要求 workdir 是 git 仓库（勾选即生效）；不持库、无 MQ、无 exe。

---

## 2. 形态与部署

| 项 | 值 |
|----|----|
| 编译形态 | **纯 lib**（module `github.com/chonkpilot/chonkpilot-ignore`），无 `main`、无独立 exe |
| 运行位置 | 进程内（引擎遍历 / 插件扫描 / 数据面判定） |
| 依赖 | **仅标准库**（`errors` / `io/fs` / `os` / `path` / `path/filepath` / `regexp` / `strings`） |
| 并发 | `Options.rs`（惰性规则集缓存）**非并发安全**：同一 `Options` 的 `Ignored` 只在单 goroutine 内调用（`WalkDir` 各自新建 `ruleSet`，不共享） |

---

## 3. 输入输出

### 3.1 输入

| 入口 | 输入 | 说明 |
|------|------|------|
| `Options{StackGitignore, UserRules, Root}` | 遍历排除配置 | `StackGitignore=false` → 仅「内置强制 + 默认排除 + 用户输入」；`UserRules` = 用户输入规则（最高优先级） |
| `ConfigOptions(engine, get, workdir)` | 引擎名 + 配置读取器 + workdir | 由**引擎配置读取器**构造 `*Options`（键前缀 = 引擎名，见 §3.3） |
| `Parse(content, base)` | gitignore 文本 + 来源目录 | 规则列表（保序；空行 / 注释 / 无效行丢弃） |
| `FileRules(path, base)` | 文件路径 + 来源目录 | 读取并解析；文件不存在 / 不可读 → `nil`（静默） |

### 3.2 输出

| API | 输入 | 输出 |
|-----|------|------|
| `WalkDir(root, opts, fn)` | 根 + 配置 + 回调 | 深度优先遍历：**跳过被忽略目录（不下降）与文件**，其余交 `fn`；root 自身恒以 `rel=""` 调用 |
| `(*Options).Ignored(rel)` | 相对 `Root`、`/` 分隔的路径 | `bool`（自身命中 **或**任一祖先目录「目录剪枝」命中） |
| `Match(forced, ordered, rel, isDir)` | 按源分组的规则 + 路径 | `bool`（供单测与外部按源复用） |
| `BuiltinRules` / `DefaultRules` / `GlobalRules` / `ExcludeRules(root)` | — | `[]Rule` |

### 3.3 引擎配置键（`ConfigOptions`）

| 键 | 取值 | 效果 |
|----|------|------|
| `enable-<engine>` | `"true"` | `enabled=true` |
| `<engine>.skip-dirs` | 逗号 / 分号 / 换行分隔（保序不去重） | → `UserRules`（空串 = 无规则） |
| `<engine>.stack-gitignore` | `"true"` | `StackGitignore`（false → 不读任何 ignore 文件） |

---

## 4. 关键流程

### 4.1 规则来源与求值顺序（`decide`：**先命中者决定**，`!` = 取消忽略）

```text
① 内置强制排除 BuiltinRules（.git/ .svn/ .hg/ .chonkpilot/）—— **最先判定**：任一命中即忽略，不可被 '!' 反选
② 用户输入 Options.UserRules —— 最高可反选层（等价 git 命令行 --exclude）
③ 各级 .gitignore —— 目录越深优先级越高；同目录内行序在后覆盖在前（仅 StackGitignore=true 参与）
④ base 组（同一 lastMatch **反向扫描** → 组内优先级：默认排除 > info/exclude > 全局 ignore）
     · 默认排除 DefaultRules（node_modules/ __pycache__/ dist/ build/ vendor/ …；可被 '!' 反选）
     · .git/info/exclude ExcludeRules
     · 全局 ignore GlobalRules（$XDG_CONFIG_HOME/git/ignore 或 ~/.config/git/ignore）
```

> base 组内优先级来自实现：`newRuleSet` 按 `GlobalRules() ++ ExcludeRules() ++ DefaultRules()` 追加，`lastMatch` **从尾部反向取首个命中**（末位 = 默认排除，优先胜出）；详情见 §4.2。
> `GlobalRules` / `ExcludeRules` 与各级 `.gitignore` **仅在 `Options.StackGitignore=true` 时参与**（默认排除恒参与）。

### 4.2 求值（`ruleSet.decide`）

```text
decide(rel, isDir)
 ├─ matchesAny(forced, rel)          → true（强制排除：不可被 '!' 反选）
 ├─ lastMatch(user, rel)             → 命中则按其 negate 返回
 ├─ for dir := dirOf(rel); ; dir = dirOf(dir):
 │      loadOwn(dir)   // 惰性读取 dir 的 .gitignore（stack 关闭 / 已加载 → 跳过）
 │      lastMatch(own[dir], rel)      → 命中则按其 negate 返回（自深到浅，最深优先）
 │      dir == "" → break
 └─ lastMatch(base, rel)             // 全局 ignore + info/exclude + 默认排除
```

- `matchesAny`：forced 任一命中即忽略。
- `lastMatch`：从后往前取**首个命中**规则 → `!negate`；无命中 → `(false, false)`。
- `(*Options).Ignored(rel)`：先按 `ancestorDirs(rel)` 逐层判「目录剪枝」（祖先被排除 → 其后代恒不索引，**不可被后代 `!` 救回**），再判自身（目录性取磁盘真实类型，不存在 → 按文件）。

### 4.3 解析（`parseLine` / `compile` / `globSegment`）

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
Rule.match(rel, isDir)：dirOnly 且非目录 → false；base 非空 → 仅适用于 base 之下（去 base 前缀后匹配）
```

---

## 5. 失败态

| 场景 | 处置 |
|------|------|
| `ConfigOptions` 的 `workdir` 为空 | 返回 `err`（`opts` / `enabled` 仍按已读配置填充） |
| `workdir` 不存在 | **不报错**：逐级 `.gitignore` 读取本就静默容忍缺失 → 判定自然降级为「不排除」 |
| `.gitignore` / 全局 ignore / info/exclude 不可读 | `FileRules` → `nil`（静默，不中断索引） |
| 单行无效 | `parseLine` 丢弃该行（空行 / 注释 / 未闭合字符类按字面处理） |
| `Options.Root` 为空 | `rules()` → `nil` → `Ignored` 恒 `false` |
| `Ignored` 传入 `""` / `".."` / `"../…"` | 恒 `false`（根自身与越界路径不排除） |
| `WalkDir` 的 `fn` 收到 err | 原样转交 `fn`（由调用方决定是否中止） |

---

## 6. 依赖与边界

- **上游依赖**：仅标准库。
- **下游消费者**：
  - `src/plugins/codegraph/server`（索引遍历排除）· `src/plugins/plugin-codegraph`（`skip-dirs` / `stack-gitignore` 解析）；
  - `src/plugins/vfts/server`（索引遍历排除）· `src/plugins/plugin-vfts`（`vfts.go` / `manifest.go` 清单扫描）；
  - `src/lib/data/internal/indexignored`（数据面判定，服务 `data-index-ignored` 主题 §3.6 —— 前端文件树灰显被排除条目）。
- **边界**：**排除判定单一实现** —— 引擎 `WalkDir` 与插件 `file_list` 清单、数据面 `Ignored` 三处共用同一 `ruleSet`，故 `Ignored(p) ⇔ WalkDir 会跳过 p`（与 git 的已知差异见 §1）。

---

## 7. 现状与待办

- ✅ 已实施：gitignore 语义匹配单一实现；`ConfigOptions` / `Options.Ignored` 服务 `data-index-ignored`（§3.6）。
- 已知与 git 的差异（刻意，见 §1）：不查 git 索引；不解析 `core.excludesFile`；不要求 workdir 是 git 仓库。
- 遗留 / 规划：无。

---

## 8. 关联测试（测试落点）

**L1 白盒（`src/lib/ignore/ignore_test.go`，20 例）**：

- 解析与匹配：`TestParseSkipsCommentsAndBlanks` · `TestParseEscapes` · `TestGlobStarNotCrossSlash` · `TestDoubleStarPositions` · `TestCharClass` · `TestDirOnly` · `TestAnchoredVsNonAnchored` · `TestOrderedLastMatchWins` · `TestBuiltinForcedNotNegatable`。
- 遍历：`TestWalkDirStackGitignore` · `TestWalkDirNoStack` · `TestWalkDirParentIgnoredChildNegationUseless` · `TestWalkDirGitInfoExcludeAndGlobal` · `TestWalkDirUserRulesHighestPriority` · `TestWalkDirUserRuleFileLevel`。
- 配置与单路径判定：`TestConfigOptions` · `TestIgnoredStackGitignore` · `TestIgnoredNoStackReadsNoIgnoreFiles` · `TestIgnoredUserRulesAndBuiltinForced` · `TestIgnoredMatchesWalkDir`（`Ignored` ⇔ `WalkDir`）。
- **间接**：`src/lib/data/internal/indexignored` 的服务经 L2 `persist_index_ignored_test.go`；引擎侧经 `codegraph/server` / `vfts/server` 白盒与 L4 `run_index_gate` / `run_project_cfg`。
