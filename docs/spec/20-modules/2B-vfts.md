# 2B · vfts（全文检索索引）

> 状态：✅ 与代码一致
> 关联：[28-plugins §3.4](28-plugins.md) · [29-codegraph](29-codegraph.md)（同构对照） · [2F-ignore](2F-ignore.md) · [38-检索与索引](../30-function-points/38-检索与索引.md)
> 代码目录：`src/plugins/vfts/`（引擎 exe，含 `server/`）· `src/plugins/plugin-vfts/`（宿主插件）

---

## 1. 职责与边界

- **一句话**：进程内 **zvec（C-API）FTS 全文索引**的**独立 console mcp-server**（引擎）+ server 内嵌插件（按 workdir 管理引擎子进程、编排增量/全量索引并向 gateway 注册查询工具）。
- **做**：纯文本（源码 + `.txt`/`.md` 等）与**文档类**（Office/PDF，经外部转换服务抽文本）的全文索引、按文件增量（文件清单驱动）、6 个引擎工具（5 管理 + 1 查询）+ 1 个查询工具注册、进度回写与空闲子进程回收、**jieba 分词（系统级词典）**。
- **不做**：不做向量语义检索（阶段 2，HNSW 后置）；不启动文档转换服务（由用户手动注册/启动）；不落 `chonkpilot.db`（索引独立目录）；引擎侧不做可见性门控（门控在插件）；**不做项目级/用户级词典覆盖**（词典只有系统级一份）。
- **与 codegraph 的定位差异**：vfts = **全文检索**（FTS，可命中自然语言/中文），codegraph = **代码语义索引**（符号/调用图）；两者引擎各自独立落盘、工具面互不重叠。

---

## 2. 形态与部署

| 项 | 引擎（vfts-mcp-server） | 插件（plugin-vfts） |
|----|--------------------------|---------------------|
| 形态 | 独立 exe（console，**CGO 允许**——链接 zvec C-API） | 内嵌 lib Hook（server 进程内） |
| 运行 | `--http[=<addr>]`（默认 `127.0.0.1:5702`，端点 `/mcp`）· `--stdio` · `-probe <dir>`（自检：建索引打印汇总，可附 `-match`/`-query`/`-topk`/`-stack-gitignore`） | stdio 子进程管理（懒建 client） |
| 构建 | `build-vfts.ps1`（`CGO_ENABLED=1`） | 随 server 内嵌（`build-desktop.ps1`） |
| 产物 | `dist/desktop/mcps/vfts/chonkpilot-vfts-mcp-server.exe` + `zvec_c_api.dll`（**同目录**，CGO 运行时必需） | — |
| 引擎定位 | `Options.Exe` > 环境变量 `VFTS_EXE` > **`<exeDir>/mcps/vfts/chonkpilot-vfts-mcp-server.exe`**（`resolveExe`，**不做多路径猜测**，缺失即 `Start` 失败） | — |

> 与主模块 CGO 禁令不冲突：**禁令仅针对 `src/gui`（`-H windowsgui` 单 exe）**；本组件是独立 console 组件，自由用 CGO（`github.com/zvec-ai/zvec-go`）。
> **不内嵌引擎**：与 codegraph 同构——引擎外置 exe，插件按需 lazily spawn `--stdio` 子进程。
> **jieba 基础词典 vendor + 内嵌**：`src/plugins/vfts/third_party/jieba/{jieba.dict.utf8,hmm_model.utf8}`（约 5.6MB，来源 cppjieba MIT，经 `github.com/yanyiwu/gojieba@v1.4.0` 取得；LICENSE 见同目录 `LICENSE.cppjieba`）经 `//go:embed` **内嵌进引擎 exe**（`third_party/jieba/dict.go`），**产物不新增词典文件**；运行时首次初始化 zvec 前物化到**系统级**缓存目录。**二进制词典不得用文本工具编辑**。

---

## 3. 对外接口

### 3.1 引擎工具（6 = 5 管理 + 1 查询）

| 工具 | hot | 说明 |
|------|:---:|------|
| `vfts_configure` | ✗ | 配置 workdir 工作区：`enabled` / `exts` / `skip_dirs`（用户排除规则，gitignore 语法，最高优先级）/ `stack_gitignore`，以及 `docs` 段（`docs`/`doc_endpoint`/`doc_token`/`doc_max_bytes`/`doc_text_max_bytes`/`doc_cache_dir`）。**文档字段为「键存在即覆盖（含空值）」**：`doc_endpoint` 下发空串 = 服务不可用（清旧值）。 |
| `vfts_index` | ✗ | 建/更新 FTS 索引（同步落盘 `<workDir>/.chonkpilot/vfts/`）。**不传 `files`/`remove` = 全量重建**（幂等）；传则按文件增量（先按 `doc_ids` 删旧块再重建）。返回 `mode`/计数 + 逐文件 `indexed[{path,key,doc_ids,chunks}]`。 |
| `vfts_status` | ✗ | 状态：`state`（`""`未初始化 / `indexing` / `ready` / `error`）+ 进度 + `indexedFiles`/`chunkCount`/`tokenizer`/`exts`；文档态 `docsEnabled`/`docsService(running\|absent)`/`docsSkipped`/`docsFailed`/`docsParserVersion`。 |
| `vfts_dict_get` | ✗ | 查看**系统级** jieba 词典：`dict_dir`（系统词典目录）/ `user_dict_path`（自定义词文件）/ `user_dict`（自定义词全文）/ `word_count`（词条数）/ `base_dicts`（内嵌基础词典文件名）。 |
| `vfts_dict_set` | ✗ | 编辑**系统级**自定义词（覆写 `user_dict.txt`；每行一词、`#` 注释行忽略）→ `{ok, word_count}`。**词典变更后必须重建索引**才生效。 |
| `vfts_query` | ✓ | 检索：`match`（自然语言匹配串）/ `query`（布尔表达式，**不支持 `content:` 字段前缀**）至少其一；`topK`（默认 20，文件级去重）；`path` 子串过滤。返回命中文件 + `snippet`/`line`/`score`/`loc`。 |

- 查询必备 `workdir`；`readyGate` 未就绪 → 结构化应答（`not_initialized`/`pending`(indexing)/`error`，**不算协议错误**）。`vfts_dict_get`/`vfts_dict_set` 为**系统级**（无 `workdir`；由插件 vfts.dict.* 面调用）。
- **引擎侧 `Configure` 的 `enabled` 仅记录/存档**，真正门控在插件（与 codegraph 同口径）。

### 3.2 插件注册到 gateway 的查询工具（1）

| 工具 | 契约 |
|------|------|
| `vfts_query` | 与引擎**同名同描述**，schema **去掉 `workdir`**（由插件按 `context.instance_id` 注入）；`handler_subject=vfts-tool-call`，`hot=true`，`category=vfts`，`owner=vfts`。 |

管理工具（`vfts_configure`/`vfts_index`/`vfts_status`/`vfts_dict_get`/`vfts_dict_set`）**不注册**给 gateway——插件直接调引擎。

### 3.3 插件订阅/发布的消息面（[61-消息一览](../60-reference/61-消息一览.md)）

- 订阅：`instance-register` / `instance-heartbeat` / `instance-exit` · `data-prj-config-refresh` · 工具回调 `vfts-tool-call` · **管理面 `vfts.dict.get` / `vfts.dict.set` / `vfts.reindex`**（UI ↔ 插件，点分相对主题、同主题 promise 写回 `v.Result`；插件分别直调引擎 `vfts_dict_get`/`vfts_dict_set` 与调度强制重建）。
- 发布：`mcp-tools-register` / `mcp-tools-unregister`（gateway 方法面）· `data-prj-config-load`/`save`（读写配置键）· `data-filelist-list`/`put`/`del`（文件清单）· 回写 `vfts.status`（prj-config 键，非主题）。

### 3.4 配置键（prj 库）

| 键 | 语义 |
|----|------|
| `enable-vfts` | 可见性门控（`"true"`/`"false"`；缺省 = 关闭） |
| `vfts.exts` | 参与索引的扩展名（逗号/分号/换行分隔；空 = 引擎默认集） |
| `vfts.skip-dirs` | 用户排除规则（gitignore 语法；`splitRules` **保序且保留重复项**） |
| `vfts.stack-gitignore` | 是否叠加各级 `.gitignore` / `.git/info/exclude` / 全局 ignore |
| `vfts.docs` | 是否索引文档类（Office/PDF，需转换服务；默认关） |
| `vfts.doc-max-mb` | 文档类单文件上限（整数 MB；缺省 50） |
| `vfts.status` | 引擎状态 JSON 文本（插件回写、UI 只读回显） |

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

- **文件收集（`collectFiles`）分组**：
  - **非文档类** = 生效 `exts`（配置集或 `defaultExts`），单文件上限 **8MB**（`maxFileBytes`）；二进制（前 8KB 有 NUL）跳过。
  - **文档类**（`docExts` = `.docx/.xlsx/.pptx/.pdf`）= **独立一组**，仅当 `docs` 开启时收集，单文件上限 `doc_max_bytes`（默认 50MiB）。**文档类恒从非文档集中剔除**（即便被写进 `vfts.exts`）→ 文档支持只由 `docs` 开关决定。
  - 排除走共享包 `github.com/chonkpilot/chonkpilot-ignore` 的 `ignore.WalkDir`（与 codegraph、vfts 插件清单扫描**同一实现**）；规则来源/优先级/语义见 [29 §4.1](29-codegraph.md)。
- **FTS 分词器（`tokenizer` = 固定 `jieba`）**：`server/workspace.go` 常量 `tokenizer = "jieba"`；`zvec.NewFTSIndexParams("jieba", ["lowercase"], extra_params)`（`server/store.go` `buildSchema`/`jiebaExtraParams`），`extra_params` = `{"jieba_dict_dir":<系统词典目录>,"user_dict_path":<自定义词文件>,"cut_mode":"search"}`。过滤器 `lowercase`（英文大小写归一；jieba 本身不对 ASCII 归一）。**中文按词切分**（提升中文召回）；实测判红：跨词相邻字串 `文检` **不**命中（`standard` 下会命中）。zvec 另支持 `ngram`/`whitespace` 分词器与 `ascii_folding`/`stemmer` 过滤器，当前**未启用**。
  - **系统级词典（`server/dict.go`，只有系统级一份）**：目录 = `<系统缓存根>/chonkpilot/vfts/jieba`（Windows = `%LocalAppData%\chonkpilot\vfts\jieba`；`os.UserCacheDir()` 不可得 → 系统临时目录）。内容 = **内嵌基础词典**（`jieba.dict.utf8`/`hmm_model.utf8`，首次初始化 zvec 前物化，按大小判等跳过；**只读**） + **自定义词** `user_dict.txt`（每行一词、`#` 注释；由 `vfts_dict_set` 覆写）。引擎初始化时先 `zvec.SetDefaultJiebaDictDir(dir)` 再 `zvec.Initialize`；建集合时再经 `extra_params` 显式下发同一目录。
  - **分词器/词典变更 → 必须重建索引**（分词结果落在索引里）：① 代码侧 `Meta.tokenizer` 与当前常量不一致（`server/workspace.go` `loadMeta`）→ 复位 `state=""`，插件据此走全量重建并换掉旧集合；② 自定义词变更（`vfts.dict.set`）→ 插件对该 workdir 调度强制重建；③ 配置页「重新索引」按钮（`vfts.reindex`）→ 手动全量重建。
  - **语义已有 L1 可判红自动断言**：`server/incremental_test.go` `TestTokenizerSemanticsJieba`（中文词命中 / 非词字串 `文检` 判红不命中 / `keyword` 命中 `Keyword` / `run` 不命中 `running`）+ `TestSystemDictMaterializeAndTools`（基础词典物化 + 系统级词典查看/编辑往返）。

### 4.2 引擎：按文件增量（`Incremental`）

```text
标 indexing → ensureCollLocked（内存有则复用 / 盘上有则打开 / 否则新建）
→ 收集待删旧块主键 = files[].doc_ids ∪ remove[].doc_ids（去重）→ deleteChunks
→ 逐文件重建块（文档类先并发转换）→ insertChunks（主键 = 单调递增纯数字串，避免与全量 1..N 冲突）
→ coll.Flush → 更新聚合计数（fileDelta/chunkDelta）+ NextPK → saveMeta
```

### 4.3 引擎：查询与就绪门控

- `EnsureReady` 单飞：内存无集合 → 盘上有则打开 → 回 `ready`/`indexing`/`error`/`not_initialized`；`ErrNotInitialized` 供 `readyGate` 结构化应答。
- `Query`：多取文档块（`topK×5`，上限 500）→ 文件级去重 → 取前 `topK` 个文件；`path` 子串过滤；返回命中拼回绝对路径。
- 片段定位 `snippetOf`：块内首个含检索词的行 → 返回（截断 240 字符, 行偏移）。

### 4.4 引擎：文档转换接入（`server/docs.go`）

- **约束（用户定稿）**：引擎**不启动**转换服务（`src/mcps/markitdown`，由用户在 MCP 配置页手动注册/启动）；引擎只按插件下发的 endpoint/token 直连其内部快通道 `POST {endpoint}/vfts/convert`（头 `X-Chonk-Token`）。
- **缓存**：`<workdir>/.chonkpilot/vfts/doc_text/`；缓存键 = `sha1(相对路径 | mtime | parser_version)`（mtime 或解析器版本变化即换键 → 天然失效）；命中不调 HTTP。
- **降级**：服务不可用 → **整批跳过**文档类（不写文件名 chunk，`Docs.Skipped`++）；单文件失败 → 降级为「仅索引文件名」（1 条 content = 相对路径的 chunk，`Docs.Failed`++）。任何失败都不中断整库索引。
- **并发**：转换阶段走 worker 池（`defaultDocWorkers=6`）；zvec 写入仍串行。
- **loc 映射**：文档定位（页码/sheet 名/slide 序号）按 UTF-8 字节偏移映射；转换文本按 `doc_text_max_bytes`（默认 2MiB）按字节截断（回退到有效 UTF-8 边界）。

### 4.5 插件：启用编排（`plugin-vfts/vfts.go` `ensureWorkspace`）

```text
（启用态且索引未就绪 → maybeEnsure 后台触发；或索引配置变更 → 去抖强制重建）
ensureWorkspace(force)：
  0) readIndexConfig（exts / skip_dirs / stack） + readDocsConfig（docs / doc-max-mb）
  0b) probeDocsService（索引前强制复探转换服务）
  1) vfts_configure（enabled/exts/skip_dirs/stack_gitignore + docs 字段**恒下发**；服务不可用 = 空 endpoint/token）
  2) vfts_status 快检 → ready？
     - 解析器版本变化 → 清文档缓存 + 强制全量
  3) ready 且非强制 → incrementalSync（扫描 → file_list diff → 按文件增量 → 回写清单）
  4) 否则全量：pushStatusPhase(index) + startProgressPoll（500ms 轮询引擎 meta.json）+ vfts_index → 重建清单
  末了 mergeSyncStatus 回写 vfts.status（added/updated/removed/skipped + docsPort）
```

- **文件清单驱动增量（`manifest.go`）**：清单 `file_list`（项目级 prj 库，经 persist 读写）记录每个文件的 `key(sha1(绝对路径)前8字节)`/`size`/`mtime`/`md5`/`doc_ids`/`chunks`/`indexed_at`。diff 判定（纯函数 `diffManifest`）：
  - ① 新文件（表里无 key）→ 待索引；
  - ② `size`+`mtime` 未变 → 跳过（不算 md5，省 IO）；
  - ③ 变化 → 算 md5：与表一致 → 仅更新 mtime；不一致 → 待索引（先按旧 `doc_ids` 删块）；
  - ④ 表里有、本次扫描没有 → 待删除（按 `doc_ids` 删块）。
  - **文档类行 md5 携带解析器版本**（`<md5hex>@<parserVersion>`，复用 md5 口径）：版本变化 → 该行失效重建 + 插件清缓存目录。
- **去抖（`rebuildDebouncer`，400ms）**：设置页一次保存写 `vfts.exts`+`vfts.skip-dirs`(+`vfts.stack-gitignore`) 多键 = 多次 `data-prj-config-refresh` → 逐键展开后经去抖合并为**一轮**重建；窗口外单键变更仍触发（不漏重建）。
- **进度推送（零新增主题）**：索引期间 `startProgressPoll` 每 500ms 读引擎落盘 `meta.json` 的 `done/total`，变化即回写 `vfts.status`（`{state:"indexing", phase:"configure"\|"index", progressDone, progressTotal}`）；收口写 `ready`/`error`。UI 只读回显该键。
- **转换服务「后启动」容错**：`tick` 内 `maybeDocServiceAppeared` —— 存在「启用中且 docs 开启」的 workdir 且探测到服务由不可用→可用（跳变）→ 经去抖调度一次重建（自动接上，无需重启）。
- **可见性收敛（`syncTools`，单飞）**：应注册 = 存在某 workdir `refs>0 && enabled=true`。v1 限制：gateway `tools/register` 无 scope → 同名工具全局只注册**一份**（归属排序后第一个 workdir），执行按 `context.instance_id` 路由。
- **空闲回收（`sweepIdleClient`，15s 扫描）**：无任何活跃实例（`refs==0`）且距 `lastActiveAt` ≥ `childIdleTimeout`（5min）→ 关闭**全局共享**引擎子进程（下次 `sharedClient()` 懒重建）。这与 codegraph 的**每 workdir 独立**子进程不同（vfts 仍为全局共享 client —— 与 28-plugins §3.4 一致）。
- **实例退出**：显式 `instance-exit` 恒有效；分离形态（`-tags split`）另由 `sweepInstances` 按 `instance.HeartbeatTimeout`（90s）判超时 → 走既有 `instanceGone`；**合并单进程形态（默认构建）为空实现**（见 `sweep_split.go` / `sweep_inprocess.go`）。

---

## 5. 数据结构与存储

| 数据 | 位置 |
|------|------|
| 全文索引集合（zvec：字段 `path`/`line`/`content`(FTS)/`loc`） | `<workDir>/.chonkpilot/vfts/collection/`（zvec C-API 目录） |
| 工作区元信息（`Meta`：enabled/state/进度/err/lastIndexedAt/skipDirs/stackGitignore/exts/fileCount/chunkCount/tokenizer/**nextPK**/docs） | `<workDir>/.chonkpilot/vfts/meta.json`（跨进程读取，进度推送/就绪门控用；**不含 token**） |
| 文档转换缓存（文本 + locs） | `<workDir>/.chonkpilot/vfts/doc_text/<sha1>.json`（`.chonkpilot` 强制排除） |
| 文件清单 `file_list`（key/size/mtime/md5/doc_ids/chunks/indexed_at） | **prj 库**（persist，`data-filelist-*` 面；插件读写） |
| 启用开关 / 索引配置 / 状态 | `enable-vfts`·`vfts.exts`·`vfts.skip-dirs`·`vfts.stack-gitignore`·`vfts.docs`·`vfts.doc-max-mb`·`vfts.status` → **prj 库 config**（团队共享；插件读写） |
| 转换服务 token | **仅内存**（`Workspace.docsToken`，绝不落盘/落日志） |
| **系统级 jieba 词典**（基础词典 + 自定义词） | `<系统缓存根>/chonkpilot/vfts/jieba/`（`os.UserCacheDir()` → Windows `%LocalAppData%`；**全机/全工作区共用一份**，非项目/用户级覆盖；基础词典内嵌物化、只读） |

- 索引内一律用 **'/' 分隔、相对 workdir** 的路径（可移植）；对外拼回绝对路径。
- `writeFileAtomic`（临时文件 + rename）落 `meta.json`；引擎进程退出前 `CloseAll()` 关闭集合 + `shutdownZvec()`。

---

## 6. 依赖

- 引擎：官方 `go-sdk` + **`github.com/zvec-ai/zvec-go` v0.7.0**（C-API，需 `zvec_c_api.dll`）+ 共享包 `github.com/chonkpilot/chonkpilot-ignore`。**CGO 来自 zvec**（见 §2 注）。
- 插件：`src/lib/core`（`msgkeys`/`mq`/`winproc`）· `src/plugins/plugin`（`plugin`/`instance`）· `chonkpilot-ignore`；由 server 内嵌。**不依赖** mcp-tools / gateway / data（清单经 persist 消息面间接）。
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
| 文件清单（file_list）在插件侧 | 引擎只接收「要索引/删除的文件清单」 | 增量判定（md5/mtime）与去抖编排留插件 |
| 管理工具 hot=false | 不发 LLM | 管理动作由插件调用 |
| gateway 工具全局一份（v1） | 执行按 `context.instance_id` 路由 | gateway `tools/register` 无 scope |

---

## 8. 场景与边界

- 未初始化时查询 → `readyGate` 结构化应答（不报协议错）。
- 非文档单文件 >8MB 跳过；文档类 >`doc_max_bytes` 跳过；二进制跳过。
- 索引可重建（`Initialize` 幂等）；`Incremental` 按文件增量自愈。
- 文档服务不可用 → 文档类整批跳过（清单同口径移除）；单文件失败 → 仅文件名。
- 多 workdir 并存 → 引擎子进程仍全局共享（一对多，按工具参数 `workdir` 路由）；工具面差异注册留待 v2。

---

## 9. 现状与待办

- ✅ **全文索引（FTS）已落地**：6 工具（5 管理 + 1 查询）+ gateway 注册 1 查询工具。
- ✅ **按文件增量（清单驱动）已落地**：`file_list` diff（新/跳过/mtime-only/内容变/删除）+ 主键单调递增；全量重建后重建清单。
- ✅ **文档类转换接入**：`docs`/`doc-max-mb` 配置 + 服务探测（pid 校验 + 探活，30s TTL）+ 转换缓存（键含 mtime/parser_version）+ 降级 + 后启动自动接上（去抖重建）。
- ✅ **进度推送 + 空闲回收已落地**：500ms 轮询引擎 `meta.json` → 回写 `vfts.status`（零新增主题）；`sweepIdleClient` 5min 回收共享子进程。
- ✅ **叠加 gitignore**：`vfts.skip-dirs`/`vfts.stack-gitignore` 原样下发引擎与插件清单扫描，共用 `chonkpilot-ignore` 单一实现。
- ✅ **jieba 分词 + 系统级词典 + 重新索引**：分词器固定 `jieba`（+`lowercase`）；基础词典 vendor 内嵌并物化到系统级目录；系统级自定义词查看/编辑（`vfts.dict.get`/`vfts.dict.set`）；配置页「重新索引」按钮（`vfts.reindex`）；分词器/词典变更 → 强制重建。
- ⚠️ **向量语义检索（HNSW）未实现**：阶段 2 后置（见 `server/workspace.go` 包注释）。
- ⚠️ **多 workdir 差异注册（按 workdir 区分工具名/可见性）留待 v2**：v1 全局仅注册一份（同 codegraph 限制）。
- ⚠️ **无 L2/L3 专门套件**：仅包内 L1（见 §10）。

---

## 10. 关联测试

- **引擎**：`src/plugins/vfts/server/{docs,incremental}_test.go`（文档类独立成组 / 服务不可用整批跳过 / 空 endpoint 清旧值 / 三态开关 / 转换+缓存命中 / 单文件降级 / 缓存键解析器版本失效 / loc 字节偏移映射 / 并发转换全覆盖 / loc 往返；`TestCollectFilesStackGitignore` / `TestIncrementalIndexSmoke` / `TestTokenizerSemanticsJieba` / `TestSystemDictMaterializeAndTools`）。
- **插件**：`src/plugins/plugin-vfts/`（`vfts_test.go`：查询工具定义/schema/回复/`strval`/`resolveExe`/空闲回收/进度 JSON/`readEngineProgress`/轮询序列/去抖合并/`onPrjConfigRefresh` 多键合并/`sweepIdleClient`/无心跳超时/显式退出注销/`splitRules`/清单扫描 gitignore 语义/`readIndexConfig`；`manage_test.go`：词典查看/编辑回执与载荷透传、`reindex` 按实例定位 + 调度目标选择；`manifest_test.go`：diff 用例/文档解析器版本/`md5@version` 往返/扫描文档组/`keyOf`/`absOf`；`docsprobe_test.go`：探测路径/absent/非法状态；`docs_regression_test.go`：重建清单剔除未索引文档；`sweep_split_test.go`：心跳超时注销）。
- **运行（CGO，需 `zvec_c_api.dll`）**：

```powershell
$env:GOROOT="e:\GoDev\go1.26"; $env:Path="e:\GoDev\go1.26\bin;"+$env:Path
$env:GOPROXY="https://goproxy.cn,direct"
cd src/plugins/vfts          ; go test ./server/ -v -count=1
cd src/plugins/plugin-vfts   ; go test -v -count=1
```

> **实跑证据**：对含中英文样本的临时目录执行
> `dist/plugins/vfts/chonkpilot-vfts-mcp-server.exe -probe <dir> -match <词>` → `files/chunks/state/tokenizer` 汇总 + `hits` 命中行（`tokenizer:jieba`）；据此确认索引建/查闭环与分词器行为（见 §4.1 末条）。**以上测试/自检均不涉及 MQ 消息收发**（纯函数 + 临时文件系统），天然满足 [61-消息一览](../60-reference/61-消息一览.md) 的测试准则。
