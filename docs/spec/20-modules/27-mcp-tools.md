# 27 · src/lib/mcp-tools（执行层）

> 状态：✅ 与代码一致
> 关联：[10-分层与依赖](../10-architecture/10-分层与依赖.md) · [25-mcp-server](25-mcp-server.md) · [14-安全域](../10-architecture/14-安全域-agentbox.md)
> 代码目录：`src/lib/mcp-tools/`（`core/` `desktop/` `browser/` + `internal/*`）；**契约唯一源已迁出** → `src/initdata/capability/tools/`（executor 不再内嵌，`--help` 从磁盘读）。

---

## 1. 职责与边界

- **一句话**：**执行层**——工具分类 executor 3 个（core / desktop / browser）+ **`dsl_run` 工具执行体**（`chonkpilot-dsl-executor.exe`，2026-10-20 起作业与引擎同进程）**共 4**，被 mcp-server `callTool` **spawn-on-call**，自持实现具体工具动作，用完即退。
- **做**：文件操作 DSL、脚本执行、HTTP 抓取、浏览器 DSL、桌面 DSL；统一输出层（`Ok`/`Err` + 200KB 落文件）。
- **不做**：**零主仓库依赖**（仅 `src/lib/paths`）；无 DB/NATS；**无路径白名单**（越界读写成败由 agentbox 承担，见 [14-安全域](../10-architecture/14-安全域-agentbox.md)）——agentbox 沙箱在执行器**启用隔离时**真正拦截越界读写（`agentbox.InitFromEnv()` + 各 choke point，见 [14 §7.1](../10-architecture/14-安全域-agentbox.md)）；未注入 `CHONKPILOT_SANDBOX` = 不启用（默认兼容）；零持久状态。
- **文件操作参数强约束（R-11）**：**所有输入/输出文件与目录参数**（含 **DSL 内所有 `#"..."` 引用**，含**数据源读取**）**必须为绝对路径或以 `~/` 开头的用户目录路径**，相对路径一律拒绝（参数级违规 → 整体失败，错误消息含原值、位置与示例）；规范见 [16-路径解析规范](../10-architecture/16-路径解析规范.md) §8。注意：`paths.ResolveDir` 仍服务 CLI 目录参数且允许相对（下同；是唯一允许相对处）。

---

## 2. 形态与部署

| 项 | 值 |
|----|----|
| 形态 | 3 个工具分类 executor（`chonkpilot-core-executor.exe` / `-desktop-` / `-browser-`）+ **`dsl_run` 工具执行体**（`chonkpilot-dsl-executor.exe`）= 共 4；**另有** meta 3 / domain 5 的一工具一 exe（`chonkpilot-{meta-find,meta-load,meta-invoke,ask-user,tool-result,tool-stop,subchat-open,subchat-finish}-executor.exe`，见 [21 §3](21-llm-server.md)）与 codegraph/vfts 查询 executor（见 [29](29-codegraph.md) / [2B](2B-vfts.md)） |
| 调用约定 | `<exe> <tool> --input=<参数 JSON 路径>`；`<exe> --help`；`<exe> --help <tool>`（一工具一 exe 亦沿用同一外壳约定） |
| 部署 | `build-mcp-server.ps1` 编到 `capability/executors/`（扁平 `chonkpilot-<cat>-executor.exe`；meta/domain 同目录扁平）；契约 `tools/<cat>/*.tool.md` 的 runtime 相对 md 写 `../../executors/<cat>.exe`（用户自建 executor 写全路径） |
| work-dir | **概念已移除**（`workDir := ""`；不消费 `_work_dir`/`CHONK_WORK_DIR`）。文件操作参数须**绝对路径 / `~/` 开头 / `!/` 开头**（R-11），不再按 workDir 兜底解析相对路径 |
| 宿主注入 env | executor **从自身进程环境**读宿主注入的 `CHONKPILOT_*`（`CHONKPILOT_INSTANCE/WORKDIR/DATADIR/INTERPRETERS`；`mcp-server` spawn 时注入子进程环境，**不再经 `tools/call` 保留参数 `_instance`/`_workdir`/`_datadir`**），经 `fileops.HostEnv`/`BuildToolEnv` 构造**只读** DSL `env`：`CHONKPILOT_WORKDIR/DATADIR/TEMPDIR/EXEDIR/PROJECT`，并 `paths.SetTempRoot(CHONKPILOT_INSTANCE)` 定 `!/` 落地根；`script_run` 另注入子进程环境（用户 `env` 同名优先）并读 `CHONKPILOT_INTERPRETERS` 解析解释器。缺 `CHONKPILOT_INSTANCE` 且需要 `!/` 或 DSL env → 顶层失败（`ErrNoInstance`）；纯绝对路径非 DSL 调用不受影响 |

---

## 3. 对外接口

### 3.1 工具清单（5 个，`src/initdata/capability/tools/`）

> **〔2026-10-12：`file_read` / `file_find` / `file_diff` 三个独立工具已彻底删除（契约与 handler 均移除）〕**，能力**收纳为 `filesys_run` 的裸动词动作 `READ` / `DIFF` / `FIND`**（同一实现 `internal/fileops/query_actions.go`）；`dsl_run` 经前缀化得到 `FILE_READ` / `FILE_DIFF` / `FILE_FIND`。core 分类由六工具收敛为三工具（`filesys_run` / `script_run` / `web_fetch`）。

| 分类 | 工具 | meta 要点 |
|------|------|-----------|
| core | `filesys_run` | hot，timeout=60（DSL 文件操作，含只读查询动作 `READ`/`DIFF`/`FIND`） |
| core | `script_run` | hot，**timeout=300** |
| core | `web_fetch` | hot，timeout=120 |
| desktop | `desktop_run` | hot，threshold=60，timeout=300 |
| browser | `browser_run` | hot，threshold=60，timeout=300 |

公共「基本信息」键：`args=<tool> --input={RAW-INPUT-FILE}`、`output=stdout`、`async=auto`、`async-threshold=30`（core）。
契约**单一数据源** = `src/initdata/capability/tools`（`capability/` 只是 `build-mcp-server.ps1` 铺出的**部署副本**）；同步规则与**副本漂移恢复**见 [03-构建与部署](../00-overview/03-构建与部署.md) §7（`.\sync-contracts.ps1`，勿手改部署目录）。

### 3.2 各工具能力

> **可重试性声明位 `idempotent`**：每个 `*.tool.md` 的「基本信息」yaml 新增 `idempotent`（`true` = 该工具**可安全自动重试**；缺省 / `false` = **安全默认 = 不可安全自动重试**），仅声明 `true` 时透出暴露 `_meta.idempotent=true`。声明结果：写/副作用 `filesys_run`（`StopOnError=false`，含 `APD`/`INS`/`PTC`）/`script_run`/`web_fetch`（POST）/`browser_run`/`desktop_run` = `false`。**〔2026-10-12〕** 原三个只读工具 `file_read`/`file_find`/`file_diff` 已删除（`idempotent=true` 随之移除）；其能力收纳为 `filesys_run` 的只读动作 `READ`/`DIFF`/`FIND`（工具整体仍为 `false`，因含写动作）。**DSL 运行器现不重试**；该位为将来加重试提供判据。见 [72 §2](../70-conventions/72-工具开发规范.md)。

- **`filesys_run`**：DSL 动词（**10 个 Raw：7 写 + 3 只读查询**）：写动作 `RPL`（全局替换，保行数）· `APD`（追加）· `PTC`（unified diff 增删改）· `INS`（建文件）· `DEL`（按 search 删行 / 无 search 删文件或目录）· `MOV`（移动，跨卷回退复制+删源）· `CPY`（文件/目录递归复制）；**只读查询动作 `READ` / `DIFF` / `FIND`（2026-10-12 收纳原 `file_read` / `file_diff` / `file_find`，实现 `internal/fileops/query_actions.go`；`dsl_run` 经前缀化为 `FILE_READ` / `FILE_DIFF` / `FILE_FIND`）**。`StopOnError=false`（记错继续）。顶层入参 `script` / `file` **二选一（均非必填）**（契约 `[parameters].required` 不再含 `script`）。**R7-C**：① 修改类动作（`modify`）单文件**尺寸上限 32MB**（超限拒改，避免整读整写）；② 文件复制（`CPY`/`MOV` 跨卷回退）/目录递归复制改**流式**（`io.Copy`，O(1) 内存）；③ `PTC` 支持**多 hunk（带 gap）补丁**——两 hunk 之间未被 context 覆盖的中间源行**原样保留**（此前会静默丢失）；④ 修改按**原文件编码**回写（UTF-16/GBK/Shift-JIS/Big5/EUC-KR 往返不改编码，不再静默转 UTF-8）。**路径（R-11）**：动词 `#"path"`、核心语句**所有文件句柄**（**LOOP/SET 数据源读取**、访问器 `.content/.lines/.array/.object/.range`、`IF exist`、`=> #"file"` 目标）、顶层 `file`、顶层 `md5` 键均须绝对 / `~/` / `!/`；字面相对路径在执行前被拒（整体失败、无副作用），`{{}}` 插值由 `ScriptFS` 执行时兜底校验。
  **返回条目**：`modified[]` / `created[]` 每条含 `{path, type, size, mtime(RFC3339), md5}`（`modified` 另含 `diff`）；目录条目 `type="dir"`、`size=0`、无 `md5`；`deleted[]` 仍为路径字符串数组（删除后无法 stat）。
- **`READ`（只读查询动作；原 `file_read`）**：`READ #"path" [start=N] [limit=N] [tail=N] [ranges=1:10,20:30] [info] [line_numbers]`。**返回语义**：**首行为元数据**（`path`/`md5`/`encoding`/`lines`/`size`[ `truncated=true`][ `total_lines=N`]），第二行起为内容；`info` 模式只返回元数据行。多文件 + `ranges`/`tail`/`start+limit` + 行号 + md5/encoding/size。**路径（R-11）**：`#"path"` 须绝对 / `~/` / `!/`。**R7-C-60**：>32MB（`maxReadBytes`）文件的 `tail` 语义**不建立在截断前缀上**（前缀尾 ≠ 文件尾）→ 改从**文件末尾反向分块**读最后 N 行（不整读大文件）。**R8-C-3**：反向分块读设**扫描窗口上限 8MiB**（`maxTailScan`）—— 对**无换行**超大文件（minified 单行）防止逐块读满退化为整读整文件。**R8-C-4**：非 EOF 的读取错误改为**项级失败**（不再静默吞，避免顶层误报 success）。**〔订正（2026-10-11）〕**：>32MB 文件**带 `ranges`/`start` 时显式报错**（行段语义建立在截断前缀上会与真实行号错位 → 不再静默按前缀解释，提示改用 `tail` 或 `info`）；**`tail` / `info` 语义不受影响**（`tail` 仍走文件末尾反向分块，见 R7-C-60）；**截断文件新增 `total_lines`（全文总行数，流式扫描 O(1) 内存；元数据 `lines` 仍为已读前缀行数）**，且 **`>32MB` 时 `tail` 配合 `line_numbers` 按全文行号回填**（行号基线 = 全文总行数 - 尾段行数 + 1，不再丢失行号或按前缀错行）。
- **`FIND`（只读查询动作；原 `file_find`）**：`FIND #"dir" [depth=N] [glob="*.go"] [grep="re"] [output=tree|file|summary] [skip_dirs="a,b"] [ignore_files="*.log"] [from=YYYY-MM-DD] [to=YYYY-MM-DD]`。**返回语义**：返回查找输出文本（按 `output` 渲染 tree/file/summary）。`output=tree|file|summary`，`depth` 剪枝、`glob`、`grep` 正则、200 行上限、时间过滤。**路径（R-11）**：`#"dir"` 须绝对 / `~/` / `!/`。**R7-C**：① `path` **必填**（缺失 → **顶层报错**，不再静默回落 `workDir`/`"."`）；② grep 单文件命中条数**封顶 200**（防逐行无界 append）；③ 整读按 `maxReadBytes` **上限截断**（`readFileCapped`）；④ 命中行 / 摘要按 **rune** 截断（中文不被按字节截出乱码）。
- **`DIFF`（只读查询动作；原 `file_diff`）**：`DIFF #"a" #"b" [#"c" #"d" ...]`（**须偶数个 `#"path"`**，逐对产 diff，多对以空行分隔）。**返回语义**：返回**统一 diff 文本**（unified diff）。三形态入参收敛为 `#"path"` 逐对。**路径（R-11）**：各 `#"path"` 须绝对 / `~/` / `!/`。**R7-C**：① 单侧文件**尺寸上限 8MB**（`maxDiffBytes`；diffmatchpatch 为 O(n²)，超限**直接报错**、不做全量 diff）；② 选项/标志不接受（`DIFF` 仅接受偶数个文件句柄）。
> **〔2026-10-12〕收纳与回显边界**：三查询动作经 `filesys_run` 工具调用时，**容器输出（`HandleFileManager`）不回显查询动作文本**（仅 modified/created/deleted/fails + 计数摘要，不含 DSL 引擎动作输出汇总）→ 经 `filesys_run` 的 `READ`/`DIFF`/`FIND` 须写 `=> 目标` 才有结果；`dsl_run` 路径正常回显（fallbackSummary）。未决项见 [41 `I-177`](../40-roadmap/41-未决项登记.md)。
- **`script_run`**：runtime 白名单 `shell/cmd/bash/python/js/powershell/vbs/java`；`script` XOR `file`；`requires` **仅 `python`（`pip install`）与 `js`（`npm install --no-save`）**，其它 runtime 传 `requires` → **整体失败**；powershell 加 `-ExecutionPolicy Bypass -File`。**写临时脚本** `ck_script_*`（用完删），扩展名按 runtime（`python=.py` / `js=.js` / `powershell=.ps1` / `vbs=.vbs` / `bash=.sh` / **`java=.java`**——JDK 单文件源码模式（JEP 330）要求 `.java`，其它扩展名会被 `java` 当**类名** → `ClassNotFoundException`；`public` 类名与文件名不必同名）。**路径（R-11）**：`file`、`workdir`（运行目录）、`interpreter`（解释器/可执行文件路径）均须绝对 / `~/` / `!/`；`args`/`env`/`filter` 等命令参数与 runtime 内置默认命令（`cmd`/`bash`/`sh`，走 PATH）不受约束。
- **`web_fetch`**：`method/body/form/form_files/headers/cookies/save_as/encoding/follow_redirect/readTimeout`；多编码转码（gbk/big5/shift-jis/euc-jp/euc-kr/iso-8859-1）；multipart 文件上传；`save_as` 直落文件（大文件不进上下文）；UA 缺省 `ChonkPilot/1.0`。**路径（R-11）**：`save_as`、`form_files[].path` 须绝对 / `~/` / `!/`（在发请求前即拒绝）。
- **`browser_run`**（chromedp，23 动词 Raw）：`OPN WAT CLK DBL RCL HOV CHK UCHK FILL SELO KPR KDN KUP EXP EVL SHT DOM DBG SCL DRG UPF TAB SLP`；locator JS（`__ckResolve` 支持 `>>` 链式）。**〔订正（2026-10-11）〕`OPN` 导航 scheme 白名单 = 仅放行 `http`/`https`/`file` 三种**（`file:` 走沙箱读校验），**其它 scheme 显式拒绝**（如 `view-source:` · **`about:`** · **`data:`** 等）；**`url.Parse` 失败 / 空 scheme 亦显式拒绝**——**相对 / 协议相对 URL**（`/path`、`//host/path`，解析后 scheme 为空）**同样不支持**（报 `syntax` 错，避免解析失败或空 scheme 时整段白名单被跳过、`file:` 读校验被绕过，见 `internal/browser/run.go` `stepOPN`）。**路径（R-11）**：`file`、`chrome_path`（Chrome/Edge 可执行文件）、DSL 内 `SHT`/`DOM`/`DBG`/`UPF`（最后一个参数）、核心语句文件句柄（**LOOP/SET 数据源读取**、访问器、`IF exist`）、行尾 `=> #"file"` 目标、落盘参数 `dom_file`/`console_file`/`fail_shot` 须绝对 / `~/` / `!/`；字面路径在启动浏览器前预校验。
- **`desktop_run`**（Win32，23 动词 Raw）：`WIN MOV CLK DBL CLKR CLKM DBLR DBLM LMD LMU RMD RMU MMD MMU DRG WHL INP KPR KDN KUP IME SHT SLP`；`WIN` 后 `$X/$Y/$W/$H/$CX...` 同步进作用域；`runtime.LockOSThread()`。**路径（R-11）**：`file`、DSL 内 `SHT`、`WIN … SHT`、核心语句文件句柄（**LOOP/SET 数据源读取**、访问器、`IF exist #"…"`/`IF exist "…"`）、行尾 `=> #"file"` 目标须绝对 / `~/` / `!/`；字面路径在执行前预校验。

---

## 4. 内部控制流

```text
<exe> <tool> --input=<file>
  → cli.Run：解析 args；--help 优先 → helpList/helpShow（读 embed md 原文）
  → 读 --input JSON（容忍 UTF-8 BOM）→ dispatch(tool, args)
  → handler 执行 → cli.Result{Success, Output, Error, Tool, RawResult}
  → cli.emit：原样透出 Output（判定权在 mcp-server）
       仅"纯文本 且 >200KB 且 !json.Valid" → 写 %TEMP%/ck-out-*.log + 提示用 filesys_run READ 读取
  → stdout 结果 JSON；stderr 轻量日志 [executor] tool=... status=... elapsed=...
  → 退出码 0 成功 / 1 失败 / 2 参数错误
```

**一致性规则（`filesys_run`）**：修改/删除已有文件前加**跨进程锁**（`<file>.chonk.lock`，`O_CREATE|O_EXCL`，30s 陈旧突破；100ms×300 重试）；顶层 `md5` 键在锁内首触校验，不一致 → 该文件静默跳过并记一次 fails；**运行时冲突**（md5 不一致、lock 超时、未找到匹配等）单条失败不整体失败。
**R-11 路径校验（与上述区分）**：**所有输入/输出文件与目录参数**在**执行前**对字面路径预校验，相对路径 → **整体失败**（`cli.Err`，exit 1，消息含原值、位置与示例），不记 fails、无文件改动；含 `{{}}` 插值的路径在执行时兜底校验（仍按整体失败）。覆盖：`filesys_run`（动词 `#"path"`、核心语句**所有句柄**——**LOOP/SET 数据源**、访问器、`IF exist`、`=> #"file"` 目标、顶层 `file`、`md5` 键）· `script_run`（`file`/`workdir`/`interpreter`）· `web_fetch`（`save_as`/`form_files[].path`）· `browser_run`（`file`/`chrome_path`、DSL `SHT`/`DOM`/`DBG`/`UPF` 最后一个参数与核心语句句柄、`=> #"file"` 目标、`dom_file`/`console_file`/`fail_shot`）· `desktop_run`（`file`、DSL `SHT`/`WIN … SHT` 与核心语句句柄、`=> #"file"` 目标）。命令参数（`args`/`env`/`filter`/`requires`）与 runtime 内置默认命令不受约束。唯一允许相对处 = CLI 目录参数（`--work-dir`/`--data-dir`，`paths.ResolveDir`）。

---

## 5. 数据结构与存储

- **零持久状态**；临时文件仅 `ck_script_*`（脚本）、`ck-out-*.log`（超长输出）、锁文件 `.chonk.lock`。
- 契约**无内嵌副本**：executor 不再 `go:embed`（原 `internal/contracts/contracts.go` 已删，`internal/contracts/` 目录已随迁移移除）；`--help` 从磁盘读 `<exeDir>/capability/tools/<cat>/`（`--root` 可覆盖）。

---

## 6. 依赖

- **上游**：`src/lib/paths`（`ResolveDir`）、chromedp（browser）、`golang.org/x/text`（多编码）。
- **下游**：仅被 mcp-server 的 `callTool` spawn；**不依赖 mcp-server/gateway**。

---

## 7. 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| 分类 exe | core/desktop/browser 三个 | 隔离重依赖（chromedp/桌面 API）与启动成本 |
| spawn-on-call | 用完即退、零状态 | 免常驻内存、免状态一致性 |
| 全路径、无白名单 | 不注入隐式 work_dir；文件操作参数**须绝对 / `~/` / `!/` 开头**（R-11），不做越界白名单；DSL 内用 `{{env.CHONKPILOT_WORKDIR}}` 显式拼项目内路径（方案 A，不依赖隐式兜底） | 沙箱责任上抛 agentbox（[14-安全域](../10-architecture/14-安全域-agentbox.md)）；路径形式强约束消除相对路径歧义（[16 §8](../10-architecture/16-路径解析规范.md)） |
| 输出原样透出 | 不在 executor 做 JSON 封装 | 判定权归 mcp-server |
| 纯文本超 200KB 落文件 | 阈值硬编码 | 防撑爆 LLM 上下文 |
| `StopOnError` 分工具 | filesys_run=false；desktop/browser=true | 文件批处理容错；交互 DSL 首错即停 |

---

## 8. 场景与边界

- 未知工具 → `unknown tool`（退出码 1）。
- `--input` 缺失/参数不足 → stderr + 退出码 2。
- 文件操作参数为相对路径（R-11）→ 整体失败（exit 1 + 错误消息含原值与示例）；与「运行时冲突记 fails、整体成功」区分（§4）；`!/` 前缀按 `<temp>/chonkpilot/<instance>/` 解析（按 instance 分目录）。
- 跨卷 `MOV` → 回退复制+删源；目录 MOV 残留进 fails。
- `script_run` 未配置解释器 → 直接报错（**不探测 PATH**）。
- `web_fetch` 无体积自限（`io.ReadAll` 全读），超限由 executor 统一层接管。
- 锁冲突 → 重试至 30s 后失败。

---

## 9. 现状与待办

- 🗄 **已清理**：无入口接线的 `fileops/{write,remove,rename,dir,replace}.go` **整文件删除**；`patch.go`/`grep.go` 裁剪为仅保留被复用的 `applyUnifiedDiff`/`globMatch`（能力由 `filesys_run`（含 `READ`/`DIFF`/`FIND` 动作）承接）。
- 🗄 **已订正**：`fetch.go` 头注释为「响应体不设自限（io.ReadAll 全读）；超长输出由 executor 统一层接管（>200KB 落临时文件）」。
- 🗄 **已修**：`internal/browser/tab.go` 有 `maxConsoleLines=2000` 滑动窗口（原地丢弃最旧），替代已删除的 `browserLogCap` 配置。
- ✅ **R-11 落地**：`internal/fileops/pathcheck.go` 新增 `ValidateFilePath`/`InvalidPathMessage`/`ValidateField`，`ResolvePath` 收敛为强校验（绝对或 `~/`，拒绝相对）；接入 `READ`（`#"path"`）· `FIND`（`#"dir"`）· `DIFF`（偶数个 `#"path"`）**（三查询动作，2026-10-12 收纳原 `file_read`/`file_find`/`file_diff` 工具）**· `filesys_run`（DSL `#"path"` 执行前预校验 + 顶层 `file`/`md5` 键）· `script_run`（`file`）。
- 📌 **〔2026-10-12〕工具面收敛**：`file_read` / `file_find` / `file_diff` 三工具契约（`tools/core/{file_read,file_find,file_diff}.tool.md`）与 handler 均已删除；只读查询实现留存为 `fileops/query_actions.go`，经 `filesys_run` 裸动词 / `dsl_run` 前缀动作暴露。
- ✅ **R-11 覆盖扩展**：`internal/fetch/fetch.go`（`save_as`/`form_files[].path`）· `internal/browser/{handle,run,actions}.go`（`file`、`dom_file`/`console_file`/`fail_shot`、DSL `SHT`/`DOM`/`DBG`/`UPF` 与 `=> #"file"` 目标）· `internal/desktop/run.go`（`file`、DSL `SHT`/`WIN … SHT` 与 `=> #"file"` 目标）接入同一 `fileops.ValidateFilePath`；browser/desktop 对 DSL 内字面落盘路径执行前预校验、`{{}}` 插值在执行时兜底校验。
- ✅ **R-11 范围升级**：新增 `src/lib/dsl/paths.go` 的**只读** `CollectHandleRefs`（收集 LOOP/SET 数据源、`IF exist`、`=> 目标`、访问器参数等全部 `#"path"` 引用；纯遍历、**不改变引擎行为**，`llm_run` 不受影响）；新增 `fileops/scriptfs.go` 校验型 `ScriptFS` 并注入 `filesys_run`（核心语句数据源读取/访问器/`=> 目标` 亦须绝对 / `~/` / `!/`，`{{}}` 插值执行时兜底校验）；`browser`/`desktop` 的 `scriptFS` 读路径同样运行时可校验并纳入数据源预校验；`script_run.workdir`/`interpreter`、`browser_run.chrome_path` 纳入「必须绝对 / `~/` / `!/`」。
- ✅ **R-11 二次升级**：`src/lib/paths` 新增**唯一 API** `ResolvePath(raw, base)`（规则序：空 → `!/` → `~/` → 绝对 → base 拼接 / 无 base 报错）、`SetTempRoot`/`TempRoot`（`!/` 映射 `<temp>/chonkpilot/<instance>/`）、`InvalidPathMessage`；`pathcheck.go` **降为薄封装**（删重复实现）。`scriptfs.go` 与 browser/desktop 的 `scriptFS`/落盘点改为**解析后使用绝对路径**（`!/`、`~/` 全路径可用）。executor 经新增 `fileops.HostEnv`/`BuildToolEnv`（读**自身进程环境** `CHONKPILOT_*` + 自身 exe 目录）`SetTempRoot` + 注入**只读** DSL `env`；`script_run` 同时把同名变量注入**子进程环境**（用户 `env` 同名优先）。
- ✅ **R-11 调用上下文改由 `_meta` + 子进程 env**：移除经 tool `arguments` 注入上下文 —— `mcp-server Config.SetContext`/`defaultsMap` 对 `_workdir`/`_datadir`/`_instance`/`_interpreters` 的注入**已删除**；上下文改由调用上下文（协议 `_meta`，键 `chonkpilot.{instance_id,work_dir,data_dir}`）从 gateway 透传，`mcp-server` spawn executor 时注入子进程环境 `CHONKPILOT_INSTANCE/WORKDIR/DATADIR/INTERPRETERS`（`Config.executorEnv`）。executor 从进程环境读（`cli.Run` + `fileops.HostEnv`）；**instance 为空 = 异常**（`paths.SetTempRoot/TempRoot` 不再回落 `default`，返回 `ErrNoInstance`）。DSL env 去掉兼容别名 `WORKDIR`，**只允许 `{{env.<NAME>}}`**。
- 构建产物当前 `chonkpilot-core-executor.exe` + `chonkpilot-dsl-executor.exe`（`dslexec`，**`dsl_run` 工具执行体**，`build-dsl-executor.ps1` 产出）落盘；desktop/browser 按需 `build-mcp-server.ps1` 产出。

---

## 10. 关联测试

模块内：`fileops/manager_test.go`（DSL 创建/修改/追加/删行/删文件、md5、部分失败、MOV、PTC；**LOOP 数据源读取**；R-11 二次升级补 **`{{env.CHONKPILOT_WORKDIR}}` 数据源**与 **`!/` 落盘**）· `fileops/toolenv_test.go`（`BuildToolEnv`：读进程环境 `CHONKPILOT_*` / `TEMPDIR` 按 instance 分目录 / 根作用域只注入 `env`（无裸名别名）/ 缺 instance → 顶层错误）· `fileops/lock_test.go`（锁 / fileMD5 / md5 分支）· `fileops/pathcheck_test.go`（R-11 路径强校验：绝对/`~/`/`!/` 通过、相对拒绝、md5 键违规整体失败、md5 不一致仍记 fails；**`ScriptFS` 数据源**相对拒绝/绝对通过）· `scriptrun/exec_test.go`（`file`/`workdir`/`interpreter` 路径强校验）· `fetch/fetch_test.go`（`save_as`/`form_files[].path` 相对拒绝、绝对 `save_as` 正常落盘）· `browser/pathcheck_test.go`（DSL `SHT`/`DOM`/`DBG`/`UPF`/`=> 目标` 字面相对拒绝、`!/` 与绝对预校验通过、`file`/`dom_file`/`chrome_path` 相对拒绝、**DSL 数据源**相对拒绝）· `desktop/pathcheck_test.go`（DSL `SHT`/`WIN SHT`/`=> 目标`/`IF exist`/`LOOP 数据源` 字面相对拒绝、`!/` 预校验通过、`file` 相对拒绝）· `browser/run_dsl_test.go` · `desktop/run_dsl_test.go` · `desktop/keyspec_test.go`。
共享引擎侧：`src/lib/dsl/paths_test.go`（`CollectHandleRefs` 覆盖 LOOP/SET 数据源、`IF exist`、`=> 目标`；纯只读、不影响执行）· `src/lib/dsl/vars_test.go`（`Options.Vars` 注入 `env` + `{{env.X}}` 插值 + `SET … => env` 只读拒绝）。

进程级黑盒：`src/test/chonkpilot-mcp-tools/unittest/`（`find_test.go` + `path_constraint_test.go`，断言退出码与 stdout 错误消息；上下文经**子进程环境 `CHONKPILOT_*`** 注入，`!/` 与 `{{env.CHONKPILOT_WORKDIR}}` 用例 + 缺 instance 报错用例；须先 `build-mcp-server.ps1` 重建 core executor）。

---

## 11. 实现约定（浏览器与执行层）

> 本节为**强制约定**。

### 11.1 Chrome 自动发现（行为规则）

- 系统未安装 Chrome/Chromium → `web_*` 工具被**过滤**，且 noChrome 守卫阻止执行。
- 前端工具栏显示警告图标（点击提示安装 Chrome/Edge）。
- Chrome 路径由配置项 `chromePath`（usr `config` 表）经装配层 `loadExecConfig` 注入环境变量 `CHONK_CHROME`（见 [64-配置项一览](../60-reference/64-配置项一览.md) §3）；`~/.chonkpilot/config.json` 缓存形态**已废弃（生产 Go 无读无写）**。

### 11.2 chromedp API 注意事项（迁移自 rod）

- `runtime.Evaluate(expr).Do(ctx)` 返回 **3 个值**：`*runtime.RemoteObject, *runtime.ExceptionDetails, error`。
- `input.DispatchKeyEvent` 只接受 `input.KeyType`，修饰键用 `.WithText()` 等链式方法。
- 权限授权用 `browser.GrantPermissions(perms).WithOrigin("").Do(ctx)`（旧 `network.SetPermission` 已移除）。

### 11.3 日志规范（executor）

- 使用 `log/slog`，输出到 **stderr**，由宿主进程捕获并写入日志。
- executor **不直接写文件**。
