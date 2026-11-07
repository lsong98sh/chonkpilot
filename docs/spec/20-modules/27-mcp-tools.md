# 27 · src/lib/mcp-tools（执行层）

> 状态：🗄 **已退场（2026-10-31，D-72）** —— `src/lib/mcp-tools` 整体删除（无出厂契约绑定）；
> 本章保留**历史口径**与已迁出能力的索引，现行实现见各插件（`file` / `task` / `web` / `mcp`）。
> 关联：[10-分层与依赖](../10-architecture/10-分层与依赖.md) · [25-mcp-server](25-mcp-server.md) · [28-plugins](28-plugins.md) · [14-安全域](../10-architecture/14-安全域-agentbox.md)
> 代码目录：**已删除**（原 `src/lib/mcp-tools/` = `cmd/{filesys-exec,script-exec,web-exec}` + `internal/{cli,contracts,fetch,fileops,pluginadapter,scriptrun}`；**2026-11-04 核对**：文件数为 0，但上述**空目录树仍留在盘上**且未被 git 跟踪）。工具契约源（**有契约文件者**）= **`src/lib/llm/contracts/tools/`**（server 域）与各插件 **`chonkpilot-plugins/<plugin>/src/tools/<cat>/`**（**载体不一**：task 为 `//go:embed`，file 运行时 `contract.LoadDir` 磁盘读；web/codegraph 无契约文件、程序化注册）。

---

## 1. 职责与边界

- **一句话**：🗄 **该执行层已退场**（2026-10-31，D-72）——原 `chonkpilot-web-executor.exe` 无出厂契约绑定，`src/lib/mcp-tools` 整体删除；**所有工具能力现由插件 MCP server 提供**（`file` / `task` / `web` / `mcp` / `codegraph` …），core 域工具在 **core 部署形态**（独立 gateway / capability 目录节点）下由 `core-mcp-server` 进程内执行；桌面单体已改由 llm server `tools/register` 与 `mcp` 插件进程内提供。**〔2026-11-04 核对〕D-72 的正式记录见 [10-分层与依赖](../10-architecture/10-分层与依赖.md)（第 23/38/54/68/127 行）**；[41-未决项登记](../40-roadmap/41-未决项登记.md#2-已收口--历史只读不再待决) 已于 2026-11-04 清空历史条目，**不再含 D-72**。
- **已迁出本层的部分**：
  - **`script_run` → `task_run`（2026-10-28 P2-3）**：多 runtime 脚本执行器整包迁出。**〔2026-11-04 核对〕实现位于同级仓 `chonkpilot-util/scriptrun/`**（`exec.go` + `exec_test.go`），**不是** `chonkpilot-plugins/task/src/lib/scriptrun`（该路径不存在）；task 插件引用该包。`chonkpilot-script-executor.exe` 不再产出；解释器配置归 task 插件（`plugins.task.interpreter-*`），主仓不再注入 `CHONKPILOT_INTERPRETERS`。见 [49 §2.21](../40-roadmap/49-待实现项.md#1-引用迁移与历史索引2026-11-04)。
  - **file 域 11 工具**（2026-10-25）随 `file` 插件迁出主仓 → 执行体 = 插件自带 MCP server，契约 = `chonkpilot-plugins/file/src/tools/file/*.tool.md`；
  - **browser / desktop** 为常驻插件（UDS，不接 gateway、不注册 MCP 工具）；
  - **meta / server 域**（`meta_*` 3 + `ask_user` / `tool_result` / `next_run` / `subchat_open` / `subchat_finish` 5）由 `core-mcp-server` **进程内**执行，无独立 exe。**〔2026-11-04 核对〕** 该能力仍在 `cmd/core-mcp-server` + `runtime: builtin`；但**桌面单体已不拉起 core**（`src/lib/llm/server/boot.go:216`），domain 5 改由 llm server `tools/register` 进程内执行，meta 3 由 `mcp` 插件注册；core 仅用于独立 gateway / capability 目录节点。**`tool_stop` 已删除（2026-11-04，`task_stop` 为唯一停止入口）。**
  - `filesys_run` / `browser_run` / `desktop_run` / `dsl_run` 与 `chonkpilot-dsl-executor.exe` **已全部删除**（GATEWAY-2 L1 / PLUGIN-SPLIT-1）。
- **做**：脚本执行（多 runtime）、HTTP 抓取；统一输出层（`Ok`/`Err` + 200KB 落文件）。
- **不做（历史）**：**零主仓库依赖**（仅 `src/lib/paths`）；无 DB/NATS；**无路径白名单**（越界读写成败由沙箱承担，见 [14-安全域](../10-architecture/14-安全域-agentbox.md)）；零持久状态。**〔2026-11-04 核对〕** `agentbox` 已删除，现行写隔离 = `chonkpilot-util/sandbox`（gateway spawn stdio 下游时实施）与 `chonkpilot-util/scriptrun` 自带 Low IL/Job 约束。
- **工具名口径（P0a，2026-10-29 用户拍板）**：本层各工具经 gateway 暴露时**暴露名 = 契约名**——内置 `self` 节点（`tools/register` 的 server 域 / 插件注册工具）与 `namespace "-"` 的 core/插件 server 都不再加 `self_` 前缀；下文条目均以契约名书写，与模型可见的 `tools/list` 名称一致（`tool_async` 等存量 `self_<原名>` 配置键由后端兼容识别，见 [26 §4.3.1](26-mcp-gateway.md)）。
- **文件操作参数强约束（R-11）**：所有输入/输出文件与目录参数**必须为绝对路径或以 `~/` / `!/` 开头**，相对路径一律拒绝；规范见 [16-路径解析规范](../10-architecture/16-路径解析规范.md) §8。

---

## 2. 形态与部署

| 项 | 值 |
|----|----|
| 形态 | **无内置 executor**（2026-10-31 D-72 整体退场）；工具能力随插件 MCP server |
| 调用约定 | `<exe> <tool> --input=<参数 JSON 路径>`；`<exe> --help`；`<exe> --help <tool>` |
| 部署 | `build-mcp-server.ps1` **不再产出** `capability/executors/`（D-72）；插件工具契约随各插件投放。**2026-11-04 已修**：`build-mcp-gateway.ps1` 的 verify 改为只校验共享 `capability/` 目录存在，不再要求已停产的 `chonkpilot-tools-executor.exe`。 |
| work-dir | **概念已移除**（`workDir := ""`；不消费 `_work_dir`/`CHONK_WORK_DIR`）。文件参数须**绝对 / `~/` / `!/` 开头**（R-11） |
| 宿主注入 env | executor **从自身进程环境**读宿主注入的 `CHONKPILOT_*`（`CHONKPILOT_INSTANCE/WORKDIR/DATADIR`），经 `fileops.HostEnv`/`BuildToolEnv` 构造**只读** env：`CHONKPILOT_WORKDIR/DATADIR/TEMPDIR/EXEDIR/PROJECT`，并 `paths.SetTempRoot(CHONKPILOT_INSTANCE)` 定 `!/` 落地根。缺 `CHONKPILOT_INSTANCE` 且需要 `!/` 或 DSL env → 顶层失败（`ErrNoInstance`）。**（2026-10-28 P2-3：`CHONKPILOT_INTERPRETERS` 注入链已删除，解释器配置归 task 插件）** |

---

## 3. 对外接口

### 3.1 工具清单（本层已无出厂工具，2026-11-04 核对）

> **〔2026-10-31 现状；2026-11-04 核对〕** `script_run` 已迁为 task 插件 `task_run`（2026-10-28 P2-3）；`web_search` / `web_fetch` / `web_download` 随 `web` 插件迁出（2026-10-31）→ **本层无出厂工具契约**，**且 `src/lib/mcp-tools` 与 `chonkpilot-web-executor.exe` 已于 2026-10-31 整体删除**（D-72，见 [10-分层与依赖](../10-architecture/10-分层与依赖.md)）。**修正**：全新构建的 core 工具面**不是 0**——`build-mcp-server.ps1:116-122` 会把 `src/lib/llm/contracts/tools/` 的 **6 个 server 域契约**拷进 `capability/tools/server` 并由 core 暴露 6 个工具；「0 工具」只适用于**旧 mcp-tools 层**。file 域 11 工具随 `file` 插件（契约 `chonkpilot-plugins/file/src/tools/file/`，**运行时磁盘加载**）、task 4 随 `task` 插件（`src/tools/task/`，**go:embed**）、web 3 随 `web` 插件（**程序化注册，无 `.tool.md`**）、codegraph 8 / vfts 1 随各自插件（程序化注册）。

| 分类 | 工具 | meta 要点 |
|------|------|-----------|
| core | （无出厂契约） | web 工具随 `web` 插件（web_search / web_fetch / web_download） |

公共「基本信息」键：`args=<tool> --input={RAW-INPUT-FILE}`、`output=stdout`、`async=auto`、`async-threshold=30`。
其余工具：server 域 6（契约 `src/lib/llm/contracts/tools/`，core 进程内执行）· **task 插件 4（`task_run` / `task_llm` / `task_status` / `task_stop`；契约 `chonkpilot-plugins/task/src/tools/task/`）** · **web 域 3（`web` 插件：web_search / web_fetch / web_download）** · file 域 11（`file` 插件）· meta 3（`mcp_find` / `mcp_load` / `mcp_invoke`，由 `mcp` 插件 MCP server 暴露）。契约同步规则见 [03-构建与部署](../00-overview/03-构建与部署.md) §7。

> **task 插件口径（2026-11-04）**：`task_llm` = **全能力委派子代理**（独立子会话/子轮次 + 全工具面，可多层；宿主能力 `plugin/v1/agent.delegate`），不再是「单次 LLM 调用」；`task_status` 支持 `task_id` 单任务查询 + `waitSec` + 运行中返回**已产出的部分输出**（与 `tool_result` 同口径）；`task_stop` 经数据面 `task.stop` → 层级联取消并关闭委派子轮次。
> **完成语义（2026-11-06）**：委派链**先子后父** —— 子轮次全部终态后才判定父轮次终态；`task_llm` 只有 `state=done` + `text` 表示完整结果，`incomplete` → `error/LLM_INCOMPLETE`（`partial` 非终态），`interrupted/cancelled` → `state=cancelled`；`running` 不返回正文，须经 `task_status` 取终态。

### 3.2 各工具能力

> **〔2026-10-27 订正〕** 下列 `filesys_run` / `READ` / `FIND` / `DIFF` / `browser_run` / `desktop_run` 条目为**历史实现记录**——这些工具已删除（file 域改为 11 个 `file_*` 工具随 `file` 插件；browser/desktop 为常驻插件）。现行契约以 `chonkpilot-plugins/file/src/tools/file/*.tool.md` 与 [3A-工具与编排](../30-function-points/3A-工具与编排.md) 为准；`web_fetch` 已于 2026-10-31 随 `web` 插件迁出（其能力见 [28-plugins](28-plugins.md) / `chonkpilot-plugins/web`），`script_run` 已于 2026-10-28 迁为 task 插件 `task_run`（`chonkpilot-plugins/task/src/tools/task/task_run.tool.md`）。

> **可重试性声明位 `idempotent`**：每个 `*.tool.md` 的「基本信息」yaml 可声明 `idempotent`（`true` = 该工具**可安全自动重试**；缺省 / `false` = **安全默认 = 不可安全自动重试**），仅声明 `true` 时透出 `_meta.idempotent=true`。`web_download`（可能含 POST，随 `web` 插件）为 `false`；`task_run`（task 插件）亦为 `false`。见 [72 §2](../70-conventions/72-工具开发规范.md)。

- **`filesys_run`**：DSL 动词（**10 个 Raw：7 写 + 3 只读查询**）：写动作 `RPL`（全局替换，保行数）· `APD`（追加）· `PTC`（unified diff 增删改）· `INS`（建文件）· `DEL`（按 search 删行 / 无 search 删文件或目录）· `MOV`（移动，跨卷回退复制+删源）· `CPY`（文件/目录递归复制）；**只读查询动作 `READ` / `DIFF` / `FIND`（2026-10-12 收纳原 `file_read` / `file_diff` / `file_find`，实现 `internal/fileops/query_actions.go`；`dsl_run` 经前缀化为 `FILE_READ` / `FILE_DIFF` / `FILE_FIND`）**。`StopOnError=false`（记错继续）。顶层入参 `script` / `file` **二选一（均非必填）**（契约 `[parameters].required` 不再含 `script`）。**R7-C**：① 修改类动作（`modify`）单文件**尺寸上限 32MB**（超限拒改，避免整读整写）；② 文件复制（`CPY`/`MOV` 跨卷回退）/目录递归复制改**流式**（`io.Copy`，O(1) 内存）；③ `PTC` 支持**多 hunk（带 gap）补丁**——两 hunk 之间未被 context 覆盖的中间源行**原样保留**（此前会静默丢失）；④ 修改按**原文件编码**回写（UTF-16/GBK/Shift-JIS/Big5/EUC-KR 往返不改编码，不再静默转 UTF-8）。**路径（R-11）**：动词 `#"path"`、核心语句**所有文件句柄**（**LOOP/SET 数据源读取**、访问器 `.content/.lines/.array/.object/.range`、`IF exist`、`=> #"file"` 目标）、顶层 `file`、顶层 `md5` 键均须绝对 / `~/` / `!/`；字面相对路径在执行前被拒（整体失败、无副作用），`{{}}` 插值由 `ScriptFS` 执行时兜底校验。
  **返回条目**：`modified[]` / `created[]` 每条含 `{path, type, size, mtime(RFC3339), md5}`（`modified` 另含 `diff`）；目录条目 `type="dir"`、`size=0`、无 `md5`；`deleted[]` 仍为路径字符串数组（删除后无法 stat）。
- **`READ`（只读查询动作；原 `file_read`）**：`READ #"path" [start=N] [limit=N] [tail=N] [ranges=1:10,20:30] [info] [line_numbers]`。**返回语义**：**首行为元数据**（`path`/`md5`/`encoding`/`lines`/`size`[ `truncated=true`][ `total_lines=N`]），第二行起为内容；`info` 模式只返回元数据行。多文件 + `ranges`/`tail`/`start+limit` + 行号 + md5/encoding/size。**路径（R-11）**：`#"path"` 须绝对 / `~/` / `!/`。**R7-C-60**：>32MB（`maxReadBytes`）文件的 `tail` 语义**不建立在截断前缀上**（前缀尾 ≠ 文件尾）→ 改从**文件末尾反向分块**读最后 N 行（不整读大文件）。**R8-C-3**：反向分块读设**扫描窗口上限 8MiB**（`maxTailScan`）—— 对**无换行**超大文件（minified 单行）防止逐块读满退化为整读整文件。**R8-C-4**：非 EOF 的读取错误改为**项级失败**（不再静默吞，避免顶层误报 success）。**〔订正（2026-10-11）〕**：>32MB 文件**带 `ranges`/`start` 时显式报错**（行段语义建立在截断前缀上会与真实行号错位 → 不再静默按前缀解释，提示改用 `tail` 或 `info`）；**`tail` / `info` 语义不受影响**（`tail` 仍走文件末尾反向分块，见 R7-C-60）；**截断文件新增 `total_lines`（全文总行数，流式扫描 O(1) 内存；元数据 `lines` 仍为已读前缀行数）**，且 **`>32MB` 时 `tail` 配合 `line_numbers` 按全文行号回填**（行号基线 = 全文总行数 - 尾段行数 + 1，不再丢失行号或按前缀错行）。
- **`FIND`（只读查询动作；原 `file_find`）**：`FIND #"dir" [depth=N] [glob="*.go"] [grep="re"] [output=tree|file|summary] [skip_dirs="a,b"] [ignore_files="*.log"] [from=YYYY-MM-DD] [to=YYYY-MM-DD]`。**返回语义**：返回查找输出文本（按 `output` 渲染 tree/file/summary）。`output=tree|file|summary`，`depth` 剪枝、`glob`、`grep` 正则、200 行上限、时间过滤。**路径（R-11）**：`#"dir"` 须绝对 / `~/` / `!/`。**R7-C**：① `path` **必填**（缺失 → **顶层报错**，不再静默回落 `workDir`/`"."`）；② grep 单文件命中条数**封顶 200**（防逐行无界 append）；③ 整读按 `maxReadBytes` **上限截断**（`readFileCapped`）；④ 命中行 / 摘要按 **rune** 截断（中文不被按字节截出乱码）。
- **`DIFF`（只读查询动作；原 `file_diff`）**：`DIFF #"a" #"b" [#"c" #"d" ...]`（**须偶数个 `#"path"`**，逐对产 diff，多对以空行分隔）。**返回语义**：返回**统一 diff 文本**（unified diff）。三形态入参收敛为 `#"path"` 逐对。**路径（R-11）**：各 `#"path"` 须绝对 / `~/` / `!/`。**R7-C**：① 单侧文件**尺寸上限 8MB**（`maxDiffBytes`；diffmatchpatch 为 O(n²)，超限**直接报错**、不做全量 diff）；② 选项/标志不接受（`DIFF` 仅接受偶数个文件句柄）。
> **〔2026-10-12〕收纳与回显边界**：三查询动作经 `filesys_run` 工具调用时，**容器输出（`HandleFileManager`）不回显查询动作文本**（仅 modified/created/deleted/fails + 计数摘要，不含 DSL 引擎动作输出汇总）→ 经 `filesys_run` 的 `READ`/`DIFF`/`FIND` 须写 `=> 目标` 才有结果；`dsl_run` 路径正常回显（fallbackSummary）。历史未决项 `I-177`（41 已于 2026-11-04 清空，不再可查）。
- **`script_run`（已迁为 task 插件 `task_run`，2026-10-28；以下为迁移前实现记录）**：runtime 白名单 `shell/cmd/bash/python/js/powershell/vbs/java`；`script` XOR `file`；`requires` **仅 `python`（`pip install`）与 `js`（`npm install --no-save`）**，其它 runtime 传 `requires` → **整体失败**；powershell 加 `-ExecutionPolicy Bypass -File`。**写临时脚本** `ck_script_*`（用完删），扩展名按 runtime（`python=.py` / `js=.js` / `powershell=.ps1` / `vbs=.vbs` / `bash=.sh` / **`java=.java`**——JDK 单文件源码模式（JEP 330）要求 `.java`，其它扩展名会被 `java` 当**类名** → `ClassNotFoundException`；`public` 类名与文件名不必同名）。**路径（R-11）**：`file`、`workdir`（运行目录）、`interpreter`（解释器/可执行文件路径）均须绝对 / `~/` / `!/`；`args`/`env`/`filter` 等命令参数与 runtime 内置默认命令（`cmd`/`bash`/`sh`，走 PATH）不受约束。
- **`web_fetch`（2026-10-31 起随 `web` 插件；以下为迁出前实现记录）**：`method/body/form/form_files/headers/cookies/save_as/encoding/follow_redirect/readTimeout`；多编码转码（gbk/big5/shift-jis/euc-jp/euc-kr/iso-8859-1）；multipart 文件上传；`save_as` 直落文件（大文件不进上下文）；UA 缺省 `ChonkPilot/1.0`。**路径（R-11）**：`save_as`、`form_files[].path` 须绝对 / `~/` / `!/`（在发请求前即拒绝）。**现状**：本地直连抓取（返回原始内容 / 下载 + 可配重定向）现由 `web` 插件 `web_download` 提供，见 [28-plugins §3.0](28-plugins.md)。
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
**R-11 路径校验（与上述区分；历史实现记录）**：**所有输入/输出文件与目录参数**在**执行前**对字面路径预校验，相对路径 → **整体失败**（`cli.Err`，exit 1，消息含原值、位置与示例），不记 fails、无文件改动；含 `{{}}` 插值的路径在执行时兜底校验（仍按整体失败）。覆盖：`filesys_run`（动词 `#"path"`、核心语句**所有句柄**——**LOOP/SET 数据源**、访问器、`IF exist`、`=> #"file"` 目标、顶层 `file`、`md5` 键）· `script_run`（`file`/`workdir`/`interpreter`；**2026-10-28 起该工具已迁为 task 插件 `task_run`，路径约束随实现搬至 `chonkpilot-util/scriptrun`（2026-11-04 核对）**）· `web_fetch`（`save_as`/`form_files[].path`）· `browser_run`（`file`/`chrome_path`、DSL `SHT`/`DOM`/`DBG`/`UPF` 最后一个参数与核心语句句柄、`=> #"file"` 目标、`dom_file`/`console_file`/`fail_shot`）· `desktop_run`（`file`、DSL `SHT`/`WIN … SHT` 与核心语句句柄、`=> #"file"` 目标）。命令参数（`args`/`env`/`filter`/`requires`）与 runtime 内置默认命令不受约束。唯一允许相对处 = 宿主目录参数（`--work-dir`，`paths.ResolveDir`）。

---

## 5. 数据结构与存储

- **零持久状态**；临时文件仅 `ck_script_*`（脚本）、`ck-out-*.log`（超长输出）、锁文件 `.chonk.lock`。
- 契约**无内嵌副本**：executor 不再 `go:embed`（原 `internal/contracts/contracts.go` 已删）；**〔2026-11-04 核对〕`internal/contracts/` 目录仍为空壳留在盘上**（同 §1 空目录残留）。`--help` 从磁盘读 `<exeDir>/capability/tools/<cat>/`（`--root` 可覆盖）为历史行为。

---

## 6. 依赖

- **上游（历史）**：`src/lib/paths`（`ResolveDir`）、chromedp（browser）、`golang.org/x/text`（多编码）。**〔2026-11-04 核对〕** 这些依赖已随能力迁至 `chonkpilot-util` 与各插件（如 `chonkpilot-util/scriptrun`），本模块无现存依赖。
- **下游（历史）**：仅被 mcp-server 的 `callTool` spawn；**不依赖 mcp-server/gateway**。现行执行面 = 插件 MCP server 与 core `builtin` 分派。

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
- `task_run`（task 插件）未配置解释器 → 直接报错（**不探测 PATH**）；`web_download`（web 插件）无体积自限（`io.ReadAll` 全读），超限由 executor 统一层接管。
- 锁冲突 → 重试至 30s 后失败。

---

## 9. 现状与待办

- **🗄 退场收尾（2026-11-04 核对）**：
  - **空目录残留**：`src/lib/mcp-tools/` 下 `cmd/{filesys-exec,script-exec,web-exec}`、`internal/{cli,contracts,fetch,fileops,pluginadapter,scriptrun}`、`plugins` 均无文件但仍在盘上（`git ls-files` 0 条，未被跟踪），审计/搜索易误判；
  - **测试目录空**：`src/test/chonkpilot-mcp-tools/unittest/` 为空，仅 `systest/__pycache__/*.pyc` 残留；
  - **构建脚本未收尾**：`build-mcp-gateway.ps1:50-51` 仍要求旧 `chonkpilot-tools-executor.exe`（见 §2）；
  - **D-72 链接**：`41-未决项登记.md` 已于 2026-11-04 清空历史条目，D-72 不再可查；请以 [10-分层与依赖](../10-architecture/10-分层与依赖.md) 为准。
- 🗄 **已清理**：无入口接线的 `fileops/{write,remove,rename,dir,replace}.go` **整文件删除**；`patch.go`/`grep.go` 裁剪为仅保留被复用的 `applyUnifiedDiff`/`globMatch`（能力由 `filesys_run`（含 `READ`/`DIFF`/`FIND` 动作）承接，均为历史记录）。
- 🗄 **已订正**：`fetch.go` 头注释为「响应体不设自限（io.ReadAll 全读）；超长输出由 executor 统一层接管（>200KB 落临时文件）」。
- 🗄 **已修**：`internal/browser/tab.go` 有 `maxConsoleLines=2000` 滑动窗口（原地丢弃最旧），替代已删除的 `browserLogCap` 配置。
- ✅ **R-11 落地**：`internal/fileops/pathcheck.go` 新增 `ValidateFilePath`/`InvalidPathMessage`/`ValidateField`，`ResolvePath` 收敛为强校验（绝对或 `~/`，拒绝相对）；接入 `READ`（`#"path"`）· `FIND`（`#"dir"`）· `DIFF`（偶数个 `#"path"`）**（三查询动作，2026-10-12 收纳原 `file_read`/`file_find`/`file_diff` 工具）**· `filesys_run`（DSL `#"path"` 执行前预校验 + 顶层 `file`/`md5` 键）· `script_run`（`file`）。
- 📌 **〔2026-10-12〕工具面收敛**：`file_read` / `file_find` / `file_diff` 三工具契约（`tools/core/{file_read,file_find,file_diff}.tool.md`）与 handler 均已删除；只读查询实现留存为 `fileops/query_actions.go`，经 `filesys_run` 裸动词 / `dsl_run` 前缀动作暴露。
- ✅ **R-11 覆盖扩展**：`internal/fetch/fetch.go`（`save_as`/`form_files[].path`）· `internal/browser/{handle,run,actions}.go`（`file`、`dom_file`/`console_file`/`fail_shot`、DSL `SHT`/`DOM`/`DBG`/`UPF` 与 `=> #"file"` 目标）· `internal/desktop/run.go`（`file`、DSL `SHT`/`WIN … SHT` 与 `=> #"file"` 目标）接入同一 `fileops.ValidateFilePath`；browser/desktop 对 DSL 内字面落盘路径执行前预校验、`{{}}` 插值在执行时兜底校验。
- ✅ **R-11 范围升级**：新增 `src/lib/dsl/paths.go` 的**只读** `CollectHandleRefs`（收集 LOOP/SET 数据源、`IF exist`、`=> 目标`、访问器参数等全部 `#"path"` 引用；纯遍历、**不改变引擎行为**，`llm_run` 不受影响）；新增 `fileops/scriptfs.go` 校验型 `ScriptFS` 并注入 `filesys_run`（核心语句数据源读取/访问器/`=> 目标` 亦须绝对 / `~/` / `!/`，`{{}}` 插值执行时兜底校验）；`browser`/`desktop` 的 `scriptFS` 读路径同样运行时可校验并纳入数据源预校验；`script_run.workdir`/`interpreter`、`browser_run.chrome_path` 纳入「必须绝对 / `~/` / `!/`」。
- ✅ **R-11 二次升级**：`src/lib/paths` 新增**唯一 API** `ResolvePath(raw, base)`（规则序：空 → `!/` → `~/` → 绝对 → base 拼接 / 无 base 报错）、`SetTempRoot`/`TempRoot`（`!/` 映射 `<temp>/chonkpilot/<instance>/`）、`InvalidPathMessage`；`pathcheck.go` **降为薄封装**（删重复实现）。`scriptfs.go` 与 browser/desktop 的 `scriptFS`/落盘点改为**解析后使用绝对路径**（`!/`、`~/` 全路径可用）。executor 经新增 `fileops.HostEnv`/`BuildToolEnv`（读**自身进程环境** `CHONKPILOT_*` + 自身 exe 目录）`SetTempRoot` + 注入**只读** DSL `env`；`script_run` 同时把同名变量注入**子进程环境**（用户 `env` 同名优先）。
- ✅ **R-11 调用上下文改由 `_meta` + 子进程 env**：移除经 tool `arguments` 注入上下文 —— `mcp-server Config.SetContext`/`defaultsMap` 对 `_workdir`/`_datadir`/`_instance`/`_interpreters` 的注入**已删除**；上下文改由调用上下文（协议 `_meta`，键 `chonkpilot.{instance_id,work_dir,data_dir}`）从 gateway 透传，`mcp-server` spawn executor 时注入子进程环境 `CHONKPILOT_INSTANCE/WORKDIR/DATADIR/INTERPRETERS`（`Config.executorEnv`）。executor 从进程环境读（`cli.Run` + `fileops.HostEnv`）；**instance 为空 = 异常**（`paths.SetTempRoot/TempRoot` 不再回落 `default`，返回 `ErrNoInstance`）。DSL env 去掉兼容别名 `WORKDIR`，**只允许 `{{env.<NAME>}}`**。
- 构建产物：**无**（2026-10-31 D-72：`src/lib/mcp-tools` 整体删除，`build-mcp-server.ps1` 不再产出任何 executor）；历史产物 `chonkpilot-{script,tools,core,dsl}-executor.exe` 均已随工具迁出 / DSL 退役删除。

---

## 10. 关联测试

模块内：`fileops/manager_test.go`（DSL 创建/修改/追加/删行/删文件、md5、部分失败、MOV、PTC；**LOOP 数据源读取**；R-11 二次升级补 **`{{env.CHONKPILOT_WORKDIR}}` 数据源**与 **`!/` 落盘**）· `fileops/toolenv_test.go`（`BuildToolEnv`：读进程环境 `CHONKPILOT_*` / `TEMPDIR` 按 instance 分目录 / 根作用域只注入 `env`（无裸名别名）/ 缺 instance → 顶层错误）· `fileops/lock_test.go`（锁 / fileMD5 / md5 分支）· `fileops/pathcheck_test.go`（R-11 路径强校验：绝对/`~/`/`!/` 通过、相对拒绝、md5 键违规整体失败、md5 不一致仍记 fails；**`ScriptFS` 数据源**相对拒绝/绝对通过）· `scriptrun/exec_test.go`（**2026-11-04 核对：已随实现位于同级仓 `chonkpilot-util/scriptrun/exec_test.go`**：`file`/`cwd`/`interpreter` 路径强校验、runtime 白名单、超时杀进程树、Windows `shell`=PowerShell 的 argv 断言）· `fetch/fetch_test.go`（`save_as`/`form_files[].path` 相对拒绝、绝对 `save_as` 正常落盘）· `browser/pathcheck_test.go`（DSL `SHT`/`DOM`/`DBG`/`UPF`/`=> 目标` 字面相对拒绝、`!/` 与绝对预校验通过、`file`/`dom_file`/`chrome_path` 相对拒绝、**DSL 数据源**相对拒绝）· `desktop/pathcheck_test.go`（DSL `SHT`/`WIN SHT`/`=> 目标`/`IF exist`/`LOOP 数据源` 字面相对拒绝、`!/` 预校验通过、`file` 相对拒绝）· `browser/run_dsl_test.go` · `desktop/run_dsl_test.go` · `desktop/keyspec_test.go`。
共享引擎侧：`src/lib/dsl/paths_test.go`（`CollectHandleRefs` 覆盖 LOOP/SET 数据源、`IF exist`、`=> 目标`；纯只读、不影响执行）· `src/lib/dsl/vars_test.go`（`Options.Vars` 注入 `env` + `{{env.X}}` 插值 + `SET … => env` 只读拒绝）。

进程级黑盒：`src/test/chonkpilot-mcp-tools/unittest/`（**2026-11-04 核对：该目录已空**，原列 `find_test.go` + `path_constraint_test.go` 不存在；仅 `systest/__pycache__/*.pyc` 残留）。历史用例口径 = 断言退出码与 stdout 错误消息、上下文经子进程环境 `CHONKPILOT_*` 注入；依赖的 core executor 已退场，**无法按原方式重建**。现行 R-11 / 脚本执行测试见 `chonkpilot-util/scriptrun/exec_test.go`（script 路径与 runtime 白名单）、`chonkpilot-plugins/file` 与 `chonkpilot-plugins/task` 的测试。

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
