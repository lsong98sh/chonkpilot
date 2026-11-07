# 2B · vfts（全文检索索引）

> 状态：⚠️ 部分与代码一致（2026-11-04 核对：引擎 FTS/会话索引/文档转换/编码真实且测试充分；插件编排、工具计数、网关注册、状态键已修正）
> 关联：[28-plugins §3.4](28-plugins.md) · [29-codegraph](29-codegraph.md)（同构对照） · [2F-ignore](2F-ignore.md) · [38-检索与索引](../30-function-points/38-检索与索引.md)
> 代码目录：`chonkpilot-plugins/codegraph/src/engines/vfts/`（引擎源码，含 `server/`；**以库形式编入 `codegraph` 插件进程**，独立 exe 仅构建自检；2026-10-24 随插件整体迁出主仓）· 主仓已无 `src/plugins/plugin-vfts/`

---

## 1. 职责与边界

- **一句话**：**zvec（C-API）FTS 全文索引引擎**（可独立 console 运行自检）+ **`codegraph` 进程外 exe 插件**（codegraph + vfts 合一）。**引擎以库形式编入插件进程**（`go.mod` require + `replace`，直接 import），运行期**不 spawn stdio 子进程**；原 `plugin-vfts` 进程内 Hook 已于 2026-10-27 删除，无兼容形态。**（2026-11-04 核对）**
- **做**：纯文本（源码 + `.txt`/`.md` 等）与**文档类**（Office/PDF，经外部转换服务抽文本）的全文索引、**9 个引擎工具**（5 管理 + 3 会话管理 + 1 查询）与 1 个查询工具注册、**jieba 分词（系统级词典）**、会话内容索引与后台对账。引擎支持按文件增量（`vfts_index {files,remove}`），但**插件当前只做全量重建**。
- **不做**：不做向量语义检索（阶段 2，HNSW 后置）；不启动文档转换服务（由用户手动注册/启动）；不落 `chonkpilot.db`（索引独立目录）；引擎侧不做可见性门控；**不做项目级/用户级词典覆盖**（词典只有系统级一份）。**插件侧无 `vfts.dict.*` UI 动作面**（词典仅经引擎管理工具/直接调用维护）。
- **与 codegraph 的定位差异**：vfts = **全文检索**（FTS，可命中自然语言/中文），codegraph = **代码语义索引**（符号/调用图）；两者引擎各自独立落盘、工具面互不重叠。

---

## 2. 形态与部署

| 项 | 引擎（vfts-mcp-server） | 插件（`codegraph` 内的 vfts 实现） |
|----|--------------------------|---------------------|
| 形态 | 独立 exe（console，**CGO 允许**——链接 zvec C-API），用于单独运行/自检 | **`codegraph` exe 插件；vfts 引擎库内联**（`codegraph/src/go.mod:16-17,37-39`）；无进程内 Hook |
| 运行 | `--http[=<addr>]`（默认 `127.0.0.1:5702`，端点 `/mcp`）· `--stdio` · `-probe <dir>`（自检：建索引打印汇总，可附 `-match`/`-query`/`-topk`/`-stack-gitignore`） | **进程内直调引擎库**（`main.go:23-24`），**无 stdio 子进程 / 无懒建 client** |
| 构建 | `build-vfts.ps1`（`CGO_ENABLED=1`） | `build-plugins.ps1` 产出 `chonkpilot-plugin-codegraph.exe`；引擎经 `require` + `replace` 编入 |
| 产物 | `dist/plugins/vfts/chonkpilot-vfts-mcp-server.exe` + 同目录 `zvec_c_api.dll`（独立自检用）；**发行默认不投放 `dist/desktop/mcps/vfts/`** | `dist/plugins/codegraph/chonkpilot-plugin-codegraph.exe` + `zvec_c_api.dll` |
| 引擎定位 | ~~`Options.Exe` > `VFTS_EXE` > `<exeDir>/mcps/vfts/...`~~ **不存在**（无 `resolveExe` / `VFTS_EXE` / `os/exec`） | — |

> 与主模块 CGO 禁令不冲突：**禁令仅针对 `src/desktop`（`-H windowsgui` 单 exe）**；引擎库编入的 `codegraph` 插件自由用 CGO（`github.com/zvec-ai/zvec-go`）。
> **引擎库内联（不是「不内嵌」）**：vfts 引擎以 Go module 直接编入 `codegraph` 插件进程（`require` + `replace` + 直接 import），运行期不 spawn 子进程。**（2026-11-04 核对）**
> **jieba 基础词典 vendor + 内嵌**：`chonkpilot-plugins/codegraph/src/engines/vfts/third_party/jieba/{jieba.dict.utf8,hmm_model.utf8}`（约 5.6MB，来源 cppjieba MIT，经 `github.com/yanyiwu/gojieba@v1.4.0` 取得；LICENSE 见同目录 `LICENSE.cppjieba`）经 `//go:embed` **内嵌进引擎 exe**（`third_party/jieba/dict.go`），**产物不新增词典文件**；运行时首次初始化 zvec 前物化到**系统级**缓存目录。**二进制词典不得用文本工具编辑**。

---

## 3. 对外接口

### 3.1 引擎工具（9 = 5 管理 + 3 会话管理 + 1 查询）

| 工具 | hot | 说明 |
|------|:---:|------|
| `vfts_configure` | ✗ | 配置 workdir 工作区：`enabled` / `exts` / `skip_dirs`（用户排除规则，gitignore 语法，最高优先级）/ `stack_gitignore`，以及 `docs` 段（`docs`/`doc_endpoint`/`doc_token`/`doc_max_bytes`/`doc_text_max_bytes`/`doc_cache_dir`）。**文档字段为「键存在即覆盖（含空值）」**：`doc_endpoint` 下发空串 = 服务不可用（清旧值）。 |
| `vfts_index` | ✗ | 建/更新 FTS 索引（同步落盘 `<workDir>/.chonkpilot/vfts/`）。**不传 `files`/`remove` = 全量重建**（幂等）；传则按文件增量（先按 `doc_ids` 删旧块再重建）。返回 `mode`/计数 + 逐文件 `indexed[{path,key,doc_ids,chunks}]`。 |
| `vfts_status` | ✗ | 状态：`state`（`""`未初始化 / `indexing` / `ready` / `error`）+ 进度 + `indexedFiles`/`chunkCount`/`tokenizer`/`exts`；文档态 `docsEnabled`/`docsService(running\|absent)`/`docsSkipped`/`docsFailed`/`docsParserVersion`。 |
| `vfts_dict_get` | ✗ | 查看**系统级** jieba 词典：`dict_dir`（系统词典目录）/ `user_dict_path`（自定义词文件）/ `user_dict`（自定义词全文）/ `word_count`（词条数）/ `base_dicts`（内嵌基础词典文件名）。 |
| `vfts_dict_set` | ✗ | 编辑**系统级**自定义词（覆写 `user_dict.txt`；每行一词、`#` 注释行忽略）→ `{ok, word_count}`。**词典变更后必须重建索引**才生效。 |
| `vfts_session_index` | ✗ | 写入/删除**会话内容索引**：`docs[{session_id,dialog_id?,turn_id?,message_id,role,ts?,text}]` / `remove[...]`；按 `message_id` 先删后插（幂等）→ `{added,removed,total}` |
| `vfts_session_clear` | ✗ | 清空会话索引：给 `session_id` 只清该会话，缺省清空整个集合（含落盘目录）→ `{ok,removed}` |
| `vfts_session_status` | ✗ | 会话索引状态：`state`（`""` / `ready` / `error`）、`sessions`、`docs`、`lastIndexedAt` |
| `vfts_query` | ✓ | 检索：`match`（自然语言匹配串）/ `query`（布尔表达式，**不支持 `content:` 字段前缀**）至少其一；`topK`（默认 20，文件级去重）；`path` 子串过滤；`scope=file\|session`。返回命中文件 + `snippet`/`line`/`score`/`loc`（会话范围返回消息级命中）。 |

- 查询必备 `workdir`（引擎 schema）；`readyGate` 未就绪 → 结构化应答（`not_initialized`/`pending`(indexing)/`error`，**不算协议错误**）。`vfts_dict_get`/`vfts_dict_set` 为**系统级**（无 `workdir`）。**（2026-11-04 核对）插件侧没有 `vfts.dict.*` 动作/订阅/配置页入口**（宿主 `bridge/capabilities.go` 仍有 `plugin/v1/vfts.dict.set → vfts.dict.set` 映射，但无消费者，属死能力）。
- ~~**resident indexer 完整性门（2026-10-19）**：`<workdir>/.chonkpilot/vfts/status.json` …~~ **〔2026-10-25 退场，D-71〕**：`status.json` / `readyGate` / `indexer.lock` / `manifest-indexer.json` 随 resident indexer 一并删除；状态面曾只剩 `vfts.status`（prjusr 派生键，插件回写），**该键也已于 2026-10-28（PLUGIN-UI-2）退场**，状态经插件动作 `engine-status` / `badge` 现取；清单 = `<workdir>/.chonkpilot/vfts/file_list.json`（插件自持）。**（2026-11-04 核对）**
- ~~**resident indexer（2026-10-19，P4b；同日改单实例多 workdir + 托盘 + 并发口径）**……~~ **〔2026-10-25 退场，D-71〕**：`chonkpilot-vfts-indexer.exe`、`src/lib/llm/vftsindexer`、`queuehooks`、`plugin.index.*` 四帧与宿主 `index_queue` **全部删除**。索引改由 **vfts 插件进程内自管**：订阅 `filesys.changed` / `session-complete`（2026-10-31 G-84 恢复）→ 按 workdir **5s 去抖 → 全量重建**；**无 `ensureWorkspace` / 清单 diff / 增量同步**，**不回写 `vfts.status`**。**（2026-11-04 核对）**
- **查询侧 LOCK 处理（历史；2026-11-04 核对已无调用点）**：原查询 executor 的 zvec 占用有界重试（200ms×15 ≈ **3s**）实现仍遗留于 `engines/vfts/execx/execx.go`，但该 executor **已随 PLUGIN-EDGE-1（2026-10-28）退场**，插件进程内直调引擎，**不再有该重试路径**；`readyGate` 的 `error` 仍保留原始错误文本。
- **引擎侧 `Configure` 的 `enabled` 仅记录/存档**，真正门控在插件（与 codegraph 同口径）。

### 3.2 插件对外注册的查询工具（1）

| 工具 | 契约 |
|------|------|
| `vfts_query` | 与引擎**同名同描述**，schema **去掉 `workdir`**（由插件从 `_meta.chonkpilot.work_dir` 注入）；经**插件自带 MCP server** `hot=true` 注册，manifest `category=codegraph`、`origin=builtin`；**无 `handler_subject` / `owner`**。**（2026-11-04 核对）** |

管理工具（`vfts_configure`/`vfts_index`/`vfts_status`/`vfts_dict_*`/`vfts_session_*`）**以 hot=false 注册进插件自带 MCP server**（不进 `hot_tools` → LLM 不可见）——插件/配置页直接调引擎；**不经 gateway `tools/register`**。**（2026-11-04 核对）**

**查询 executor 已退场（PLUGIN-EDGE-1，2026-10-28）**：`vfts_query` 现由 `codegraph` 插件进程内的引擎直挂执行（引擎库内联，不 spawn stdio 子进程）；`chonkpilot-vfts-query-executor.exe` 与 `src/initdata/capability/tools/vfts/vfts_query.tool.md` 均不再存在/不再构建，插件 `onToolCall` 也不再委托 core。下文「先委托 core / 回落本地引擎」为历史口径（G-68 第二步，已撤）。

### 3.3 插件订阅/发布的消息面（[61-消息一览](../60-reference/61-消息一览.md)）

- 订阅：`instance-register` / `instance-exit` · `data-prj-config-refresh` · `server-ready` · **`filesys.changed` / `session-complete`**（2026-10-31 G-84 恢复：按 workdir 去抖 **5s → 全量重建**）。**`vfts.reindex` / `vfts.session.query` / `vfts.session.reindex` 是 UI `OnRequest` 动作，不是总线订阅**；**无 `instance-heartbeat`、无 `vfts-tool-call`、无 `vfts.dict.get/set` 订阅**。**（2026-11-04 核对）**
- 发布：`mcp-tools-register` / `mcp-tools-unregister`（gateway 方法面）· `data-prj-config-load`/`save`（读写配置键）。**运行态不回写 prj 键**：`vfts.status` 已随 PLUGIN-UI-2（2026-10-28）整体退场，状态经插件动作 `engine-status` / `badge` 现取。**文件清单不经消息面**（2026-10-25：插件自持 `<workdir>/.chonkpilot/vfts/file_list.json`，D-71；原 `data-filelist-*` 三主题已退场）。

### 3.4 配置键（prj 库）

| 键 | 语义 |
|----|------|
| `plugins.codegraph.vfts.enabled` | 可见性门控（`"true"`/`"false"`；缺省 = 关闭） |
| `plugins.codegraph.vfts.exts` | 参与索引的扩展名（逗号/分号/换行分隔；空 = 引擎默认集） |
| `plugins.codegraph.vfts.skip-dirs` | 用户排除规则（gitignore 语法；`splitRules` **保序且保留重复项**） |
| `plugins.codegraph.vfts.stack-gitignore` | 是否叠加各级 `.gitignore` / `.git/info/exclude` / 全局 ignore |
| `plugins.codegraph.vfts.docs` | 是否索引文档类（Office/PDF，需转换服务；默认关） |
| `plugins.codegraph.vfts.doc-max-mb` | 文档类单文件上限（整数 MB；缺省 50） |
| `plugins.codegraph.vfts.encoding` | 遗留文本编码（D-73）：`""`/`auto` = 自动识别；可指定 `utf-8`/`utf-16le`/`utf-16be`/`gbk`/`gb18030`/`shift_jis`/`big5`/`euc-kr` |
| `plugins.codegraph.vfts.sessions` | 会话内容索引开关（`"true"`/`"false"`，缺省关闭；见 §3.5） |
| ~~`vfts.status`~~ | **已退场**（PLUGIN-UI-2，2026-10-28）：插件不回写、宿主不读；状态经 `engine-status` / `badge` 现取。**（2026-11-04 核对）** |

### 3.5 会话内容索引（SESSION-FTS，2026-11-01 新增）

- **定位**：在 vfts 内新增**会话正文 FTS 集合**，与文件集合并列、互不影响（文件全量重建不得清空会话索引）。
- **存储**：`<workDir>/.chonkpilot/vfts-session/`（`collection/` + `meta.json` + `state.json`）。一条消息 = 一个 doc；对外 id = `s:<session_id>:<message_id>`；**zvec 主键不允许 `:`** → 落盘主键 = `s_<base64url(session_id|message_id)>`（单射，`remove` 仍接受对外 id 并内部转换）；`path` 字段写 `sessions/<session_id>/<message_id>`（复用既有 `path` 子串过滤）；另存 `session_id` / `dialog_id` / `turn_id` / `message_id` / `role` / `ts`（消息创建时间，int64）。
- **索引范围**：**仅主会话**（`session-complete.parents` 为空且会话行 `parent_id` 为空）；`role ∈ {user, assistant}` 且消息 `type=text`；工具调用 / 工具结果 / 思维链 / system 不入索引。
- **写入时机**：订阅 `session-complete`（已有主题，**零新增字段**）→ 只取 `session / turn / dialog_id / work_dir` 标识 → 按轮拉正文 → 增量写。一轮结束后一次性写入该轮全部可索引消息。**正文来源 = `data-session-history`（按 `turn_id` 过滤）**：`data-session-load-messages` 返回的 `ChatMsg` 不含 `message_id/created_at/type`，无法满足主键与 `ts` 升序，故统一走 history 页（消息面上不新增任何主题）。
- **删除收口**：开关关闭 → 清空该 workdir 的会话集合；索引目录丢失/损坏 → 查询遇 `not_initialized` 时**自动后台全量重建**；**没有「会话删除」事件** —— 删除/编辑的回收由下文「后台对账」周期完成（旧 doc 不可达时也能被清掉）。
- **全量重建**：扫 `dialogs/`（`data-session-list` + `data-session-history` 分页 `before_turn_id`），逐会话重建；`state.json` 记每会话 `indexed_until_turn` 支持**断点续建**；范围 = **当前项目全部会话**（不跨项目）。后台执行、可被新的重建覆盖（不主动清理已删除消息的旧 doc）。
- **后台对账（D-74，2026-11-04）**：**零新增 MQ 主题** —— 插件内**单协程低优先级**对账回路，插件启动后延迟 3min 首轮、之后每 12min 一轮（常量，不新增配置项）。每轮对每个已登记 workdir（`plugins.codegraph.vfts.sessions` 为开时）用 `data-session-list` / `data-session-history` 与引擎已索引集合比对，**当场修四类差异**：
  - **漏索引**（数据面有、`state.json` 无游标 / 无指纹）→ 该会话全量补索引；
  - **内容变更**（指纹不一致 = 正文编辑 / 消息增删）→ 先 `vfts_session_clear{session_id}` 清旧 doc 再全量写入（先删后插，幂等）；
  - **已删除记录**（引擎有、`data-session-list` 已无）→ `vfts_session_clear{session_id}` 清掉该会话；
  - **时间**：`state.json` 每会话 `indexedAt`（UnixMilli，UTC 语义）+ `fingerprint`，仅用于日志 / 新鲜度，不参与正确性判定。
  - **指纹算法**：消息 `message_id` + `role` + 正文 逐行拼接、按行排序后算 **sha256**（Go 标准库），与消息顺序 / 分页无关；一致则**不重建**（只刷新 `indexedAt`）。
  - **让行（低优先级）**：同一 workdir 单飞；该 workdir 有增量待处理 / 全量重建在跑 / 引擎 `vfts_session_status` 非 `ready` → 本轮跳过；每处理完一个会话 `runtime.Gosched()` + 120ms 让路。
  - **失败与重启**：失败只记日志 + 进度 `error`（沿用 `sessionProgress`），不阻断增量、不 disable；`state.json` 的游标 / 指纹是唯一权威，插件重启后首轮对账自然覆盖（重建幂等，至多重复一次）。
  - **通知**：有差异修复时发既有 `tool-notify`（`notice=session-index-reconciled`，非新主题）。
- **进度与通知**：进度为**插件内存态**（全量重建期间 `state=indexing`），插件动作 `engine-status` / `badge` 的 vfts 段新增 `session{state,doneSessions,totalSessions,doneTurns,totalTurns}`；设置页画进度条；完成/失败发 `tool-notify`（`notice=session-index-done` / `session-index-failed`）。`state.json` 只记 `""/ready/error` + 逐会话游标（`indexed_until_turn` / `fingerprint` / `indexedAt`，后两者供后台对账）。
- **查询工具**：`vfts_query` 新增可选 `scope`（`file` 默认 \| `session`）+ `session_id?`（缺省 = `_meta.chonkpilot.session`，回退 `top_session`）+ `role?`；`scope=session` 返回 `hits[{session_id, dialog_id, turn_id, message_id, role, ts, snippet, score}]`（**必须带 `snippet` 正文片段**，只回 id 不可用）。
- **工具表面不带 `workdir`**（修正历史偏差）：`vfts_query` 的 `workdir` 由插件从 `_meta.chonkpilot.work_dir` 注入（gateway `contextmeta.go` 已透传），缺失回退「当前唯一实例」，仍缺失则明确失败；管理工具（`vfts_configure`/`vfts_index`/`vfts_status`）仍带 `workdir`（LLM 不可见）。
- **配置键**：`plugins.codegraph.vfts.sessions`（`"true"`/`"false"`，缺省关闭）。
- **UI 入口通道（2026-11-01 E2E 订正）**：会话内查找（`vfts.session.query`）与全量重建（`vfts.session.reindex`）在 codegraph 插件里以 `OnRequest` 注册 → 实际走**插件 UI 动作面**（前端 `mq.emit('plugin-call',{id:'codegraph',method:'rpc',params:{method,params}})` → 宿主 `plugin.ui.rpc` → 插件 `OnRequest` 同名方法）。**总线无同名订阅**（原「点分主题」写法会得到 `null` 应答）。`vfts.reindex` 语义不变（仅文件索引）。**2026-11-05**：两个方法已补进 codegraph `manifest.json` 的 `ui.rpc` 白名单（`plugin-call` 未声明即拒绝）。

---

## 4. 内部控制流

### 4.1 引擎：全量重建（`server/index.go` `Initialize`）

```text
Configure(enabled, exts, skip_dirs, stack_gitignore, docs)   # 引擎仅记录/存档
Initialize：
  标 indexing → collectFiles（ignore.WalkDir：gitignore 语义排除 + 扩展名分组 + 单文件上限）
  → preConvertDocs（文档类 worker 池并发转换；zvec 写入仍串行）
  → 关闭旧集合 + 清空 collection 目录 + createCollection
  → 逐文件切块（splitChunks，40 行/1000 字符上限）→ insertChunks（每 64 块一批）
  → coll.Flush → 标 ready + 计数 + NextPK=seq → saveMeta
```

- **单飞（`recMu`）**：`Initialize` 全程持 `recMu`（与 `Incremental` / `EnsureReady` **共用同一把锁**）——同一 workdir 的「就绪 / 重建 / 增量」串行执行，避免与并发查询的自愈/重建互踩。

- **文件收集（`collectFiles`）分组**：
  - **非文档类** = 生效 `exts`（配置集或 `defaultExts`），单文件上限 **8MB**（`maxFileBytes`）；二进制（前 8KB 有 NUL 且非 UTF-16 BOM）跳过。
  - **编码识别（2026-10-31 起）**：读原始字节后先识别编码并统一转 UTF-8 再切块分词——解码/识别实现 = **`chonkpilot-util/encodingx`（单一实现）**，引擎 `server/charenc.go` 只保留索引侧策略：① UTF-16 LE/BE BOM（`UseBOM` 解码，BOM 不入文本）；② UTF-8 BOM 剥离；③ 严格合法 UTF-8 原样；④ GB18030（含 GBK/GB2312）解码，**仅当结果「像中文」**（`encodingx.LooksLikeChinese`：汉字中命中高频常用字的占比 ≥0.7 且命中数 ≥3）时采纳；⑤ 其余一律保持原字节。**不猜测 Shift-JIS / Big5 / EUC-KR**（与 GBK 字节区间重叠，误码会造出假中文污染检索；需索引时先用 file 域 `file_iconv` 显式转码）。
  - **文档类**（`docExts` = `.docx/.xlsx/.pptx/.pdf`）= **独立一组**，仅当 `docs` 开启时收集，单文件上限 `doc_max_bytes`（默认 50MiB）。**文档类恒从非文档集中剔除**（即便被写进 `plugins.codegraph.vfts.exts`）→ 文档支持只由 `docs` 开关决定。
  - 排除走 `chonkpilot-util/ignore` 的 `ignore.New(...).WalkDir` / `ignore.WalkDir`（2026-10-31 起内核 + I/O 同包，与 codegraph、vfts 插件清单扫描**同一实现**）；规则来源/优先级/语义见 [29 §4.1](29-codegraph.md)。
- **FTS 分词器（`tokenizer` = 固定 `jieba`）**：`server/workspace.go` 常量 `tokenizer = "jieba"`；`zvec.NewFTSIndexParams("jieba", ["lowercase"], extra_params)`（`server/store.go` `buildSchema`/`jiebaExtraParams`），`extra_params` = `{"jieba_dict_dir":<系统词典目录>,"user_dict_path":<自定义词文件>,"cut_mode":"search"}`。过滤器 `lowercase`（英文大小写归一；jieba 本身不对 ASCII 归一）。**中文按词切分**（提升中文召回）；实测判红：跨词相邻字串 `文检` **不**命中（`standard` 下会命中）。zvec 另支持 `ngram`/`whitespace` 分词器与 `ascii_folding`/`stemmer` 过滤器，当前**未启用**。
  - **系统级词典（`server/dict.go`，只有系统级一份）**：目录 = `<系统缓存根>/chonkpilot/vfts/jieba`（Windows = `%LocalAppData%\chonkpilot\vfts\jieba`；`os.UserCacheDir()` 不可得 → 系统临时目录）。内容 = **内嵌基础词典**（`jieba.dict.utf8`/`hmm_model.utf8`，首次初始化 zvec 前物化，按大小判等跳过；**只读**） + **自定义词** `user_dict.txt`（每行一词、`#` 注释；由 `vfts_dict_set` 覆写）。引擎初始化时先 `zvec.SetDefaultJiebaDictDir(dir)` 再 `zvec.Initialize`；建集合时再经 `extra_params` 显式下发同一目录。
  - **分词器/词典变更 → 必须重建索引**（分词结果落在索引里）：① 代码侧 `Meta.tokenizer` 与当前常量不一致（`server/workspace.go` `loadMeta`）→ 复位 `state=""`，插件据此走全量重建并换掉旧集合；② 自定义词变更（引擎 `vfts_dict_set`）→ **需手动重建**（插件无 `vfts.dict.*` 动作，不会自动触发）；③ 配置页「重新索引」按钮（`vfts.reindex`，`OnRequest`）→ 手动全量重建。**（2026-11-04 核对）**
  - **语义已有 L1 可判红自动断言**：`server/incremental_test.go` `TestTokenizerSemanticsJieba`（中文词命中 / 非词字串 `文检` 判红不命中 / `keyword` 命中 `Keyword` / `run` 不命中 `running`）+ `TestSystemDictMaterializeAndTools`（基础词典物化 + 系统级词典查看/编辑往返）。

### 4.2 引擎：按文件增量（`Incremental`）

```text
标 indexing → ensureCollLocked（内存有则复用 / 盘上有则打开 / 否则新建）
→ 收集待删旧块主键 = files[].doc_ids ∪ remove[].doc_ids（去重）→ deleteChunks
→ 逐文件重建块（文档类先并发转换）→ insertChunks（主键 = 单调递增纯数字串，避免与全量 1..N 冲突）
→ coll.Flush → 更新聚合计数（fileDelta/chunkDelta）+ NextPK → saveMeta
```

### 4.3 引擎：查询与就绪门控

- `EnsureReady` 单飞：内存无集合 → 盘上有则打开 → 回 `ready`/`indexing`/`error`/`not_initialized`；`ErrNotInitialized` 供 `readyGate` 结构化应答。`EnsureReady` 持 `recMu`（与 `Initialize`/`Incremental` 单飞）；**载入态读 `coll` 单原子指针（不取 `zmu`）**——已载入则免锁返回，避免与 `Initialize` 的整段长 `zmu` 临界区耦合而阻塞查询门（仅在未载入时用短 `zmu` 懒加载/校验落盘集合；`coll` 由 `zmu` 临界区内的 `setColl` 唯一写入，「是否已载入」= `coll.Load() != nil`（`isLoaded()`）——单原子写入消除「先改句柄再改标志」两段式瞬时误报）。
- `Query`：多取文档块（`topK×5`，上限 500）→ 文件级去重 → 取前 `topK` 个文件；`path` 子串过滤；返回命中拼回绝对路径。
- 片段定位 `snippetOf`：块内首个含检索词的行 → 返回（截断 240 字符, 行偏移）。

### 4.4 引擎：文档转换接入（`server/docs.go`）

- **约束（用户定稿）**：引擎**不启动**转换服务（`src/mcps/markitdown`，由用户在 MCP 配置页手动注册/启动）；引擎只按插件下发的 endpoint/token 直连其内部快通道 `POST {endpoint}/vfts/convert`（头 `X-Chonk-Token`；请求体 `{path, max_bytes?, root}`，`root` = 本 workspace 根，供服务端校验 `path` 不越界）。
- **缓存**：`<workdir>/.chonkpilot/vfts/doc_text/`；缓存键 = `sha1(相对路径 | mtime | parser_version)`（mtime 或解析器版本变化即换键 → 天然失效）；命中不调 HTTP。
- **降级**：服务不可用 → **整批跳过**文档类（不写文件名 chunk，`Docs.Skipped`++）；单文件失败 → 降级为「仅索引文件名」（1 条 content = 相对路径的 chunk，`Docs.Failed`++）。任何失败都不中断整库索引。
- **并发**：转换阶段走 worker 池（`defaultDocWorkers=6`）；zvec 写入仍串行。
- **loc 映射**：文档定位（页码/sheet 名/slide 序号）按 UTF-8 字节偏移映射；转换文本按 `doc_text_max_bytes`（默认 2MiB）按字节截断（回退到有效 UTF-8 边界）。

### 4.5 插件：索引编排（`codegraph` 插件）

**实际实现（2026-11-04 核对）**：插件**没有** `ensureWorkspace` / `maybeEnsure` / `incrementalSync` / `diffManifest` / `startProgressPoll` / `maybeDocServiceAppeared` / `syncTools` / `sweepIdleClient`。实际路径：

```text
配置保存（一次多键 Set）或 filesys.changed / session-complete
  → 400ms（配置）/ 5s（事件）去抖
  → apply：probeDocsService（下发前强制复探）
  → vfts_configure（enabled/exts/skip_dirs/stack_gitignore + docs 字段恒下发；服务不可用 = 空 endpoint/token）
  → vfts_index（不传 files/remove = 全量重建）
  → 按引擎 indexed 重写 file_list.json
```

- **无增量编排**：引擎支持 `vfts_index {files,remove}` 与 `Reconcile`，但插件**没有任何增量调用方**（配置/事件一律全量 `vfts_index`）；`config.go` 注释明确「清单 md5 增量 diff 已随内嵌 hook 退场，暂不重做」。
- **文件清单（`manifest.go`）只写不扫**：`<workDir>/.chonkpilot/vfts/file_list.json`（原子写 temp+rename；2026-10-25 D-71 起不落 prj 库、不经消息面）记录 `key(sha1(绝对路径)前16 hex)`/`path`/`size`/`mtime`/`md5`/`doc_ids`/`chunks`/`indexed_at`；`md5` **留空**，无 `scanFiles` / `diffManifest`（增量应答直接跳过清单更新，`manifest.go:44-45`）。
- **去抖**：配置变更 **400ms**、工作区事件 **5s**；一次保存是**一个**多键 `data-prj-config-save` 请求（非「多键 = 多次 refresh」），apply 侧做幂等（新旧值相同不重建），**无 dirty 键过滤**。
- **进度/状态**：**无 500ms 轮询、无 `vfts.status` 回写**；进度经 `engine-status` / `badge` 现取（`state` / `progressDone` / `progressTotal` / `docsPort` 同源）。
- **转换服务后启动**：无自动「服务出现 → 增量接上」逻辑；每次 apply 前强制复探，服务不可用时文档类整批跳过，后续**重建**时自然纳入。
- **可见性**：**静态 MCP hot 注册**，无 `refs>0` / `syncTools`；v1 多 workdir 全局一份，执行按 `_meta.chonkpilot.work_dir` / `context.instance_id` 路由到进程内 Workspace。
- **空闲回收 / 实例退出**：无引擎子进程、无 `sharedClient` / `sweepIdleClient`；实例失效只走显式 `instance-exit`（不订阅 `instance-heartbeat`）。

---

## 5. 数据结构与存储

| 数据 | 位置 |
|------|------|
| 全文索引集合（zvec：字段 `path`/`line`/`content`(FTS)/`loc`） | `<workDir>/.chonkpilot/vfts/collection/`（zvec C-API 目录） |
| 工作区元信息（`Meta`：enabled/state/进度/err/lastIndexedAt/skipDirs/stackGitignore/exts/fileCount/chunkCount/tokenizer/**nextPK**/docs） | `<workDir>/.chonkpilot/vfts/meta.json`（供独立引擎/自检读取；**不含 token**；插件进程内直接取状态，无跨进程轮询） |
| 文档转换缓存（文本 + locs） | `<workDir>/.chonkpilot/vfts/doc_text/<sha1>.json`（`.chonkpilot` 强制排除） |
| 文件清单 `file_list.json`（key/size/mtime/md5/doc_ids/chunks/indexed_at） | **插件自持本地文件** `<workDir>/.chonkpilot/vfts/file_list.json`（原子写；2026-10-25 起不再落 prj 库、不经消息面） |
| 启用开关 / 索引配置 | `plugins.codegraph.vfts.enabled`·`.exts`·`.skip-dirs`·`.stack-gitignore`·`.docs`·`.doc-max-mb`·`.encoding`·`.sessions` → **prj 库 config**（团队共享；插件读写）。**`vfts.status` 已退场**（状态经 `engine-status`/`badge` 现取）。**（2026-11-04 核对）** |
| 转换服务 token | **仅内存**（`Workspace.docsToken`，绝不落盘/落日志） |
| **系统级 jieba 词典**（基础词典 + 自定义词） | `<系统缓存根>/chonkpilot/vfts/jieba/`（`os.UserCacheDir()` → Windows `%LocalAppData%`；**全机/全工作区共用一份**，非项目/用户级覆盖；基础词典内嵌物化、只读） |

- 索引内一律用 **'/' 分隔、相对 workdir** 的路径（可移植）；对外拼回绝对路径。
- `writeFileAtomic`（临时文件 + rename）落 `meta.json`；引擎进程退出前 `CloseAll()` 关闭集合 + `shutdownZvec()`。

---

## 6. 依赖

- 引擎：官方 `go-sdk` + **`github.com/zvec-ai/zvec-go` v0.7.0**（C-API，需 `zvec_c_api.dll`）+ `chonkpilot-util/ignore`（内核 + I/O 同包）。**CGO 来自 zvec**（见 §2 注）。
- 插件：`chonkpilot-plugin-framework`（SDK）· `chonkpilot-util`；实现 = `chonkpilot-plugins/codegraph`，由宿主 `host/mgr` 拉起。**不依赖** mcp-tools / gateway / data（配置键经插件桥 `config.project.*` 读写；清单为本地文件）。**（2026-11-04 核对）**
- 转换服务：`src/mcps/markitdown`（Python，**外部**，不在编译依赖内；插件只探测其状态文件 + 探活）。

---

## 7. 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| 独立 console exe | 与主模块分离，允许 CGO | 主模块 CGO 禁令；zvec C-API 需 CGO |
| 索引独立目录 | `<workDir>/.chonkpilot/vfts/` | 派生数据、体量大、可重建 |
| FTS 优先、向量后置 | 阶段 1 只做 FTS（零 embedding 依赖） | 降低首版依赖；HNSW 阶段 2 |
| 分词器固定 `jieba` + 系统级词典 | 基础词典 **vendor 内嵌**（`third_party/jieba`，go:embed）+ 物化到系统级缓存目录；**只做系统级**、无项目/用户级覆盖 | 提升中文召回；zvec jieba **无内置词典**（须提供词典目录）；系统级一份实现最简、口径统一（仅系统级） |
| 文档类独立成组 | 仅 `docs` 开关驱动（不并入 `exts`） | 避免用户自定义 exts 丢文档支持 |
| 引擎不启动转换服务 | 插件只探测（30s TTL）+ 复探 | 转换服务由用户掌控生命周期；token 仅内存 |
| 文档字段「键存在即覆盖（含空值）」 | 服务不可用 = 空 endpoint/token | 否则引擎残留旧 endpoint → 误判可用 |
| 全量重建 zvec 集合目录 | 清空 + `CreateAndOpen` | zvec 覆盖语义；幂等 |
| 增量主键 = 单调递增数字串 | 避免与全量 `1..N` 冲突 | `NextPK` 跨调用单调 |
| 文件清单（file_list）在插件侧 | 引擎只接收「要索引的文件」应答；插件按 `indexed` 回写清单 | 清单是派生缓存；**当前仅全量回写，md5 留空，无 diff** |
| 管理工具 hot=false | 不发 LLM | 管理动作由插件调用 |
| gateway 工具全局一份（v1） | 执行按 `context.instance_id` 路由 | gateway `tools/register` 无 scope |

---

## 8. 场景与边界

- 未初始化时查询 → `readyGate` 结构化应答（不报协议错）。
- 非文档单文件 >8MB 跳过；文档类 >`doc_max_bytes` 跳过；二进制（前 8KB 有 NUL 且非 UTF-16 BOM）跳过；非中文遗留编码（Shift-JIS/Big5/EUC-KR）不猜测，按原字节索引。
- 索引可重建（`Initialize` 幂等）；`Incremental` 按文件增量自愈。
- 文档服务不可用 → 文档类整批跳过（清单同口径移除）；单文件失败 → 仅文件名。
- 多 workdir 并存 → **进程内按 workdir 隔离 Workspace**（无子进程；按工具参数/`_meta.work_dir` 路由）；工具面差异注册留待 v2。**（2026-11-04 核对）**

---

## 9. 现状与待办

- ✅ **全文索引（FTS）已落地**：引擎 **9 工具（5 管理 + 3 会话管理 + 1 查询）**；插件向 LLM 注册 **1** 个查询工具 `vfts_query`（管理工具 hot=false，LLM 不可见）。**（2026-11-04 核对）**
- ⚠️ **按文件增量（引擎能力）已落地，插件侧未编排**：引擎 `vfts_index {files,remove}` / `Incremental` 可用，但插件**只做全量重建**，清单只写不扫（`md5` 留空），无 `diffManifest` / `incrementalSync`。见 §4.5。**（2026-11-04 核对）**
- ✅ **文档类转换接入**：`docs`/`doc-max-mb` 配置 + 服务探测（pid 校验 + 探活，30s TTL）+ 转换缓存（键含 mtime/parser_version）+ 降级。**「后启动自动接上增量」未实现**（无 `maybeDocServiceAppeared`）。
- ⚠️ **进度回写 + 空闲回收未实现**：无 500ms 轮询、不回写 `vfts.status`；无 `sharedClient` / `sweepIdleClient`（进程内引擎）。状态经 `engine-status` / `badge` 现取。**（2026-11-04 核对）**
- ✅ **叠加 gitignore**：`plugins.codegraph.vfts.skip-dirs`/`.stack-gitignore` 原样下发引擎，共用 `chonkpilot-util/ignore`（内核 + I/O 同包）单一实现。
- ✅ **jieba 分词 + 系统级词典**：分词器固定 `jieba`（+`lowercase`）；基础词典 vendor 内嵌并物化到系统级目录；引擎管理工具可查看/编辑系统词；配置页「重新索引」经 `vfts.reindex`（`OnRequest`）触发。**插件侧无 `vfts.dict.*` UI 动作面**。**（2026-11-04 核对）**
- ✅ **编码识别（2026-10-31）**：文本类文件索引前识别编码并转 UTF-8（UTF-16 LE/BE BOM、UTF-8 BOM、GB18030/GBK「像中文」判据）；新增 `plugins.codegraph.vfts.encoding` 可指定遗留编码。
- ⚠️ **向量语义检索（HNSW）未实现**：阶段 2 后置（见 `server/workspace.go` 包注释）。
- ⚠️ **多 workdir 差异注册（按 workdir 区分工具名/可见性）留待 v2**：v1 全局仅注册一份（同 codegraph 限制）。
- ⚠️ **无 L2/L3 专门套件**：仅包内 L1（见 §10）。

---

## 10. 关联测试

- **引擎**：`chonkpilot-plugins/codegraph/src/engines/vfts/server/{charenc,docs,incremental}_test.go`（编码策略：UTF-8/BOM/UTF-16LE-BE/GBK 转码与 SJIS-Big5-EUC-KR 不猜测（`TestDecodeIndexText`）+ 真索引检索（`TestIndexLegacyEncodedText`）；通用识别/解码/中文判据的单测在 `chonkpilot-util/encodingx`（`TestDecodeAutoAndAutoName` / `TestCandidatesAndConfidence` / `TestLooksLikeChinese` 等）；文档类独立成组 / 服务不可用整批跳过 / 空 endpoint 清旧值 / 三态开关 / 转换+缓存命中 / 单文件降级 / 缓存键解析器版本失效 / loc 字节偏移映射 / 并发转换全覆盖 / loc 往返；`TestCollectFilesStackGitignore` / `TestIncrementalIndexSmoke` / `TestTokenizerSemanticsJieba` / `TestSystemDictMaterializeAndTools`）。
- **插件**：`chonkpilot-plugins/codegraph/src/config_test.go`（实测：`TestMigrate*` / `TestBadgeText` / `TestHandleBadge*` / `TestHandleStatus` / `TestHandleClearNoStatusMirror` / `TestHandleSave*` / `TestNormalizeValueAccepts` / `TestHandleLoadDefaults` / `TestOnPrjConfigRefresh*` / `TestOnInstanceRegisterMigratesAndApplies` / `TestConfigPageUsesAPISave`）+ `sessionindex_test.go` / `sessionreconcile_test.go` / `webui_asset_test.go`。**原列举的 `resolveExe` / 空闲回收 / 进度轮询 / `sweepIdleClient` / 清单扫描等测试在插件仓 0 命中，已删除。**（2026-11-04 核对）
- **运行（CGO，需 `zvec_c_api.dll`）**：

```powershell
$env:GOROOT="e:\GoDev\go1.26"; $env:Path="e:\GoDev\go1.26\bin;"+$env:Path
$env:GOPROXY="https://goproxy.cn,direct"
cd ../chonkpilot-plugins/codegraph/src/engines/vfts ; go test ./server/ -v -count=1
cd e:\BizWorks\chonkpilot-plugins\codegraph\engines\vfts   ; go test -v -count=1
```

> **实跑证据**：对含中英文样本的临时目录执行
> `dist/plugins/vfts/chonkpilot-vfts-mcp-server.exe -probe <dir> -match <词>` → `files/chunks/state/tokenizer` 汇总 + `hits` 命中行（`tokenizer:jieba`）；据此确认索引建/查闭环与分词器行为（见 §4.1 末条）。**以上测试/自检均不涉及 MQ 消息收发**（纯函数 + 临时文件系统），天然满足 [61-消息一览](../60-reference/61-消息一览.md) 的测试准则。
