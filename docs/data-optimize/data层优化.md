# data 层优化清单（审计 + 待办）

> 日期：2026-10-25 ｜ 状态：🟡 **部分已实施**（见 §0 进度）｜ 登记：[49 §2 `DATA-OPT-1`](../spec/40-roadmap/49-待实现项.md)
> 范围：`src/lib/data` 全模块（生产代码 97 文件 / 19,623 行；测试 46 文件 / 6,108 行）
> 方法：四分区**只读**审计（persist 信封层 / session+kernel / config+capfs 等领域 / 内核与门面），每条均以 `文件:行号` 取证；无法证实的标「待确认」，已列入「明确不做」的不再重复。
> 契约准则：[61-消息一览](../spec/60-reference/61-消息一览.md) —— 本清单除 D-5 外**不改任何主题或 payload 字段**。

---

## 0. 实施进度（2026-10-25）

| 批次 | 状态 | 说明 |
|---|---|---|
| D-1 migrate 删除 | ✅ | `migrate.go`/`seed.go`/`migrate_test.go` 删除；新增 `buckets.go`（`bucketsFor` + `ensureLayerBuckets`）；`OpenLayer` 只建桶 |
| P0-2 `autoContinue` | ✅ | 读侧补 `case "bool"`（`strconv.ParseBool`） |
| P0-3 前端白名单 | ✅ | `configIO.js` 补 3 个 `llm.*` 键 |
| P0-4 `CompactArgs` | ✅ | 流式 token 解码，按 JSON 原始键序取前 3 |
| P0-5 inbox 顶层字段 | ✅ | `commit` 的 `session_id/brief/tool_call_status` 增加顶层回落 |
| P0-6 畸形载荷 req_id | ✅ | 新增 `Service.failDecode`，12 处 handler 接入 |
| D-4 移除 `llm.analysis/decision` | ✅ | Go 键表 + 单测 + 前端页面/测试 + 64 文档 |
| P1 死代码 | ✅ | 净 −533 行生产（+ −134 测试） |
| P2 出参 DTO | ✅ | 25 处回执全部改具名 struct（新增 9 个通用 DTO） |
| D-2 旧布局退场 | ✅ | 2026-10-28 实施：删 `session.db.shard` 开关 / `ShardOn` / 旧布局分支 / `persist/shard.go` 搬运 / `sessionstore/migrate.go`；`dialog_id` 恒必填（根会话自举除外）。详见 [49 §2.5](../spec/40-roadmap/49-待实现项.md) |
| P3 结构收敛 | ✅ | 2026-10-28 实施 S-1～S-7（实测净 ≈ -200 行：`src/lib/data` -162 + `src/lib/llm/server` -38；未为凑行数硬删）。落地要点与逐项行数见 [49 §2.5](../spec/40-roadmap/49-待实现项.md)。**未做**：S-4 的 `more` 置真 / 游标 Seek（语义修复，非结构收敛） |
| P4 性能 | ✅ | 2026-10-28 实施 F-1～F-7，量化见 [49 §2.5](../spec/40-roadmap/49-待实现项.md) |
| D-6 MCP map↔DTO | 🟡 | **结构合并已做**（删 `llm/server` 的同名 `mcpEntry` 与两次 JSON 往返；`data/internal/mcp` 的 `mcpFromMap`/`mcpToMap` 为唯一转换）。**「文件未知字段保留」语义部分未做**（需改 61 §3.1 / 64 口径，待用户确认） |
| D-5 `prj-config` 批量 delete 广播 | ⬜ | **未做 —— 属消息面变更，待用户按 61 §变更口径确认** |

> 〔2026-10-28 订正（DATA-OPT-1 ②）〕旧布局已整体退场：本文 §1 D-2、§2 P0-1、§4 F-1 / F-7 中提到的 `shard` / `ShardOn` 分支代码**均已删除**，相关条目仅作历史审计记录。

---

## 1. 已拍板（用户 2026-10-25）

| # | 决定 | 说明 |
|---|------|------|
| D-1 | **删除 migrate 迁移框架** | 开发期不做存量迁移。删 `data/migrate.go` 的 `Migration`、`migrations` 列表、`_meta.schema_version`、`_migrations` 桶与版本校验；库结构 = `bucketsFor(layer)` 直接建 + 缺失桶按需 `CreateBucketIfNotExists`。v10 `index_queue` / v15 `drop_file_list` / v16 `drop_index_queue` 条目**一并删除**（不再保留版本号占位）。同步 [12-数据层 §6.1](../spec/10-architecture/12-数据层.md) 的迁移表与相关篇章。 |
| D-2 | **旧布局整体退场** | 删 `internal/sessionstore/migrate.go`（`sessions/<hex>/` → `dialogs/` 搬运）、`persist/shard.go` 启动搬运、`session.db.shard` 开关及 13 处旧布局分支，只保留一树一库 `dialogs/dialog-<hex>.db`。`sessionstore` 只留 `ListDialogs`/`GetDialog`/`PutDialog`/`Delete`。P0-1 的 `SessionActiveGet` 缺陷随之消失。 |
| D-3 | `refresh` 的 `list` **不做优化** | 保持每次写后全量 list（`prj-config` 扫表 / `scenario`、`mcp` 磁盘遍历）。 |
| D-4 | **去掉 `llm.analysis` / `llm.decision`** | 后端零消费方的预留键：删 `userConfigKeyKinds` 两行 + 前端 `SettingsLLMPage.vue` 两行 standby + 导入白名单相关同步（`docs/spec/60-reference/64-配置项一览.md` 标删除）。 |
| D-5 | `prj-config` **批量 delete 对齐单条广播** | 写侧已收敛为 1 条（带 `ids`）；delete 批量改为同样 1 条。**属消息面变更**（[61 §3.1](../spec/60-reference/61-消息一览.md) 需补 delete 的 `ids` 口径），实施前按 61 §变更口径走确认。 |
| D-6 | MCP map↔DTO 改**手写合并** | 去掉每 server 两次 JSON 往返；语义变化 = 文件里的未知字段**保留**（当前被丢弃），需在 [61 §3.1 `data-mcp`](../spec/60-reference/61-消息一览.md) 与 64 注明。 |

---

## 2. P0 · 确认缺陷（先修，改动小、收益明确）

| # | 位置 | 问题 | 建议 |
|---|------|------|------|
| P0-1 | `internal/session/session_facade.go:333-357` | `SessionActiveGet` 只查 prjusr `sessions` 表；`shard=on` 后新会话行在对话库 → 活动会话读不到，静默回落「最近更新对话」 | D-2 退场后天然消除；若先修则按 `dialog_id` 到对话库校验 |
| P0-2 | `internal/config/userconfig.go:40` + `:189-214` | `autoContinue` 登记 `bool`，读回 switch **无 `bool` 分支** → 同字段时而 bool 时而 string，消费方三处兼容 | 补 `case "bool": strconv.ParseBool(v)`（失败回落原串） |
| P0-3 | `src/frontend/src/utils/configIO.js:34-41` | 导入白名单缺 `llm.promptOptimise/memory/compress/analysis/decision` → 导出再导入**静默丢配置**（D-4 后只需补 3 个） | 白名单与 Go 注册表逐键比对 + 守护测试 |
| P0-4 | `internal/kernel/viewdata.go:430-442` | `CompactArgs` 用 `for k := range map` 取「前 3 个参数」→ 落库 `brief` / 视图 `simplified` **不确定**（每次可能不同） | 按 key 排序后取前 3 |
| P0-5 | `persist/persist.go:402-409` | 畸形载荷时各 handler 用自己的未解析 `p.ReqID` 回失败 → `req_id:""`，调用方严格匹配 → **只能等 3s 超时**且丢真实原因 | `handle` 已解析 `hdr.ReqID`，失败回包用它兜底（与 P2 的 `parsePayload[T]` 同批） |
| P0-6 | `persist/inbox.go:107-115` + `payload.go:324-336` | payload 声明 `session_id/brief/tool_call_status`（61 schema 也在顶层），handler 只读 msg 内同名 → **声明了却不读** | 改为「顶层优先、msg 内补缺」（契约不动），补 L1 用例 |

---

## 3. P1 · 零风险死代码（合计约 530 行，均有「0 生产调用」证据）

| 位置 | 内容 | 约行数 |
|------|------|--------|
| `facade/wire/wire.go:498-551` | `FlatPayload` / `RequestID` / `ConfigSaveEntries`（0 调用，且与 persist 实现口径重复） | ~60 |
| `token.go:17-43` · `brief.go:17` | 5 个纯转发：`TokenDivisor` / `Zones` / `EstimateTokensOfMessages` / `LocateFullTurnCount` / `BriefMessages`（实现已在 `chonkpilot-session`） | ~40 |
| `config.go:5-76` · `resolve.go:8-86` | `OpenConfig` / `Config` / `Resolver` / `NewResolver` / `SystemDefault` / `SystemLayer` / `ErrLayerUnavailable` 零生产调用，能力与 `internal/config` 重复 | ~160 |
| `internal/kernel/viewdata.go:370-422` 等 | `MessageView`、`ToolLLMContent`、`TurnIDOfMsg`、`SortSessionsByActivity` 零调用；`Sval` / `SvalOf` 逐字相同 | ~70 |
| `internal/sessionstore/store.go:23-190` | `Store.Fork` 整族 + `Open` / `CatalogRow` / `copyTable`（fork 已由 `component.ForkDialog` 接管）；D-2 后此文件仅剩 `ListDialogs`/`GetDialog`/`PutDialog`/`Delete` | ~145 |
| `internal/sessionstore/dialog.go:65-95` | `NewDialogID`（纯转发）、`DeleteDialog`（0 调用） | ~16 |
| `internal/capfs/capfs.go:60-67` · `capfs/scenario.go:40-41` | `PromptsRoot`、`DirTools/Resources/Skills`、`ScenariosDirName` 别名 | ~12 |
| `internal/config/validate.go:199` 等 | `_ = kind`、`internal/snapshot/store.go` 的 `sval`、`validMemoryCategoryName`/`kbProjectRoot`/`promptSystemFile` 三处纯转发 | ~20 |
| `seed.go:14-41` | `seedMap` 恒 `nil` → 整条 seed 链 no-op | ~25 |
| `persist.go:356-359`、`:473-490` | `lookupInstance` / `prjUsrDB` 生产 0 调用（仅测试） | 归位测试 |
| `persist/payload.go:251` | `SessionData.Mentions` 定义后从未被读 | -1 字段 |

> 注：`persist/compat.go` 的跨模块转发面（`AgentToolLevels`、`ToolStatusPending/Provisional` 暂 0 调用）**保留**，只补「暂无调用方」注释。

---

## 4. P2 · 出参 DTO 收尾（25 处 `map[string]any`）

上轮只做了 wire 层，persist 仍手写 map：

| 文件 | 处数 | 备注 |
|------|------|------|
| `persist/envelope.go` | 14 | config 四域 4 · user-config 5 · session fork 1 · append-message 1 · snapshot 3 |
| `persist/inbox.go` | 4（+ 行内行对象） | list 行需 `omitempty` 精确复刻「非空才带键」 |
| `persist/knowledge.go` | 3（+ import errors 行） | 顺带去掉 `paths` 的 `[]any` 拷贝 |
| `persist/turnevent.go` | 2 | `deleted` 键与 `CleanupPayload.count` 不同，不能复用 |
| `persist/ask.go` | 1 | |
| `persist/memory.go:81` | 1 | **已有** `wire.MemoryIDResult`，直接替换 |

配套重构（同批）：
- 15 处「`method` 拼串 → `var p XPayload` → `Unmarshal` → `failID`」→ 泛型 `parsePayload[T]`（-50 行，并修 P0-5）；
- 98 处 `failID`/`failf` 三段式 → `failOn(...) bool`（-120 行）；
- `payload.go` 5 份 `merged()` 样板 → `mergeData[T]`（-12 行）；
- `mergeFlat` 每次 6 次 JSON 编解码 → 单次 decode + 单次 unmarshal；`fields()` 下沉到需要的 case。

---

## 5. P3 · 结构收敛

| # | 位置 | 问题 | 收益 |
|---|------|------|------|
| S-1 | `internal/kernel/root.go:69-169` | 四份「实例绑定解析阶梯」（strict / quiet / 回退），两支逐字相同 | 抽 `resolveBinding(mode)`，-60 行；MQ/inline 等价由同一函数保证 |
| S-2 | `internal/session/*` | `SessionSubChats` 与 `All` 逐字相同（-40）· 四处跨库枚举未复用 `forEachDialogDB`（-45）· 四处「按 session_id 清表」（-50）· 15 处 `PrjUsrFor + contentDB + release` 样板（-45）· 数值/时间/首非空 helper 各 3 份（-60） | 合计约 **-300 行** |
| S-3 | `table.go` / `tx.go:40-109` | 写路径（索引维护 / `putRecord`）两份真相；四个扫桶实现同骨架 | 抽 `putRow`/`delRow`/`scanBucket`，-70 行 |
| S-4 | `query.go:253-390` | 全序规则写 3 份（`compare`/`compareValue`/`filterAfter`）；`OrderBy==""` 时先算索引再丢弃；提前退出 `more` 无条件置真（可能「下一页空」）；带 `Cursor` 时禁用提前退出 | 抽 `cmpValue` + 修 `more` + 游标 Seek，-35 行 |
| S-5 | 校验重复 | 绝对路径 2 份（`config/validate.go:377` ↔ `persist/persist.go:311`）· 标识符字符集 5 份 · `..`/空段 6 份 · 包含性判定 5 变体（knowledge 已在调用点手写「含 root 自身」）· 长度常量 64/512 三处 | 收敛到 `capfs`（`ValidateAbsPath`/`ValidIdent(opt)`/`CleanRelSegs`/`RealPathWithinOrSelf`），-80 行 |
| S-6 | config 表 | `{"v":...}` 读写 3 组实现且非字符串值口径不同（`GetConfig` 走 JSON、`ConfigGet` 走 `fmt.Sprint`）；user-config 键分类 4 处各写一遍 | 保留 `data.SetConfig` 为底层，kernel 三函数改薄封装；抽 `classifyUserConfigKey`（P0-2/P0-3 的结构性根源） |
| S-7 | `facade/wire/domains.go` | 7 个 Row 与 `facade` 同名类型**字段与 json tag 完全一致**（`MemoryCategoryRow`/`MemoryDocRow`/`MemoryExtractRow`/`KnowledgeDirRow`/`KnowledgeFileRow`/`GroupRoundTargetRow`） | 直接用 facade 类型，-60 行 struct + -40 行转换 |

---

## 6. P4 · 性能

| # | 位置 | 问题 | 建议 |
|---|------|------|------|
| F-1 | `internal/session/ask_facade.go:66-98` | `AskPending` 无 `Where` → 全表物化；`shard=on` 时每对话库各扫一遍 | 改 `ForEach` 流式 + 命中即收 |
| F-2 | `internal/session/inbox_facade.go:107-127` | 每次 `InboxAppend` 两条无索引全扫（幂等键 + 状态） | 幂等键改确定性主键；或加 `inbox_by_session` 索引（需同步 `indexes.go` + 建桶） |
| F-3 | `internal/session/session_facade.go:1202-1268` | `MessageContext` 先装载全部轮次，再判断是否走快照分支 | 装载下沉到两个分支 |
| F-4 | `session_facade.go:266-291` 等 4 处 | 逐行 `Delete` = 每行一个写事务 | `db.Update` 单事务批删 |
| F-5 | `internal/session/group.go:135-171` | 名单替换 3N 个事务 | 单事务 |
| F-6 | `internal/tasktree/tasktree.go:330-342` | 关闭单个**叶子**节点也全表 `ForEach` 建映射 | 先判有无子节点，无子则跳过 |
| F-7 | `internal/session/dialog_route.go:29-31` | 每个内容请求都 `ShardOn` → 打开/关闭 prj 库一次 | 按 instance 缓存开关（配置写路径失效） |

---

## 7. 明确不做（避免过度优化）

- `mergeFlat` 的双形兼容（`{data:{…}}` 与顶层扁平）——`llm/server` 三处生产调用方确实发 `{data:{…}}`。
- `LatestTopSession` 的 O(顶层会话数) 物化——索引排序列与选中口径不同，加 `Limit` 会错。
- `cleanupStaleToolPairs` 全扫 + 500 上限——已用单遍流式替代 3 次全扫。
- `refresh.list` 全量扫描（D-3 拍板不做）。
- 场景「跨级唯一」与 MCP「跨级覆盖」的语义差异——可抽 helper，**不能统一**。
- 安全校验的纵深防御（词法 `StrictlyWithin` + 实路径 `RealPathInside`）——只合并样板，不删校验。
- `dataDomains` 线性前缀匹配——域名含连字符，改 `Cut` 会引入正确性风险，收益 μs 级。

---

## 8. 执行顺序与验收

| 批次 | 内容 | 验收 |
|------|------|------|
| A | D-1 migrate 删除 + D-2 旧布局退场 + P0-1/2/4/6 | `chonkpilot-data` 全量 + `persist`/`session` 单测；`src/test/chonkpilot-data/unittest` 全绿 |
| B | P1 死代码清理 + P0-3 + D-4 | 同上 + 前端 `npm test` |
| C | P2 出参 DTO + P0-5 | 同上（`eqWire` 等价链保持） |
| D | P3 结构收敛（S-1～S-7） | 同上，逐项小步提交 |
| E | P4 性能（F-1～F-7） | 同上 + 必要索引变更同步 `12-数据层` |
| F | D-5（61 契约变更）· D-6（MCP 合并） | 按 61 §变更口径确认后实施 |

**收尾**：`.\build-desktop.ps1`；文档同步 [12-数据层](../spec/10-architecture/12-数据层.md) · [22-data-persist](../spec/20-modules/22-data-persist.md) · [64-配置项一览](../spec/60-reference/64-配置项一览.md) · [49 §2 `DATA-OPT-1`](../spec/40-roadmap/49-待实现项.md)。
