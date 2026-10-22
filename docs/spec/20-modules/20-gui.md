# 20 · src/lib/gui（宿主实现）+ src/desktop / src/gui（两个宿主壳）

> 状态：✅ 与代码一致
> 关联：[10-分层与依赖](../10-architecture/10-分层与依赖.md) · [11-MQ与消息](../10-architecture/11-MQ与消息.md) · [23-filesys](23-filesys.md)
> 代码目录（结构重整 + 🆕 2026-10-10 拆包）：**客户端能力** = `src/lib/gui/`（窗口工厂 / `bridge/` / registry / testserver / 远程薄壳 `RunRemote` / 日志 + `internal/` + `models/`）；**单体装配宿主** = `src/lib/gui/host/`（内嵌 server→persist→filesys / 插件 / 桥接线；入口 `host.Main(distFS, Options{Form})`，依赖方向 `host → gui` **单向**）。**壳**（只嵌前端 dist + 传形态 + 调 `host.Main`）= `src/desktop/`（桌面单体，`FormDesktop`）· `src/gui/`（gui 形态客户端，`FormGui`，`-tags split`），各自 `frontend/dist/` 为 embed 落点（构建投放，前端源码工程在 `src/frontend/`）。**flash 薄壳** = `src/flash/`（`gui.RunRemote` 远程总线，**不链 `host`、不 embed 前端**，dist 由构建投放 `<exeDir>/frontend/dist`，以 `os.DirFS` 加载），见下方分体常驻版注。

---

## 1. 职责与边界

- **一句话**：Windows GUI 宿主——WebView2 承载内嵌前端，内嵌 server 生态（persist/gateway/mcp-server/插件/filesys），并以 `bridge` 做前端 ↔ 总线的薄桥。
- **做**：窗口与布局、前端资源服务、`/publish` 上行、总线事件下行、`gui.*` 本地面、本地系统能力（目录选择/资源管理器/控制台/截图/上传）、`--test-port` 测试通道、**多窗口承载**（主窗口 + N 个纯对话窗口，🔵 待实施）。
- **不做**：不做协议翻译（薄桥）；**不持数据库句柄**（数据全经总线 persist）；不做文件变更监听（归 [filesys](23-filesys.md)）。

---

## 2. 形态与部署

| 项 | 值 |
|----|----|
| 编译形态 | windowsgui exe（`-H windowsgui`），`//go:build windows` |
| 前端 | Vue3 + Vite，`//go:embed all:frontend/dist`（**flash 薄壳例外**：dist 由 `<exeDir>/frontend/dist` 以 `os.DirFS` 加载，**不 embed**） |
| 资源服务 | **无 HTTP server**：WebView2 `WebResourceRequested` 拦截虚拟源 `https://app.localhost` |
| 构建 | `build-desktop.ps1`（原名 `build-standalone-gui.ps1`；内含 `npm run build`） |
| 产物 | `dist/desktop/chonkpilot.exe` + 引擎 exe（codegraph/vfts）+ `zvec_c_api.dll` + `capability/` |

**命令行参数**（`main.go`）：`--work-dir`（缺省 cwd）· `--data-dir`（缺省 `<workDir>/.chonkpilot`）· `--test-port`（>0 开测试通道；**仅 `-tags debugport` 构建存在**，release 不含）· `--llm-base` · `--llm-model` · `--no-server`（UI 注入测试）· `--bridge-url`（**分离形态（`-tags split`）下 bridge 对端地址（HTTP）**，跨进程上行 `POST /publish` / 下行 `GET /events`；合并单进程形态忽略；原名 `-nats-url`，**兼容别名保留**（帮助中标注已废弃，仅 `--bridge-url` 未给出时生效））。

> **desktop 分体常驻版（🆕 2026-10-09；🟡 更新 2026-10-10）**：薄壳客户端（`chonkpilot-flash.exe` / `chonkpilot-flash-cli.exe`）**不内嵌 server / persist / filesys**，`gui.*` 本地面（打开目录 / 打开文件 / 截图）本地处理，业务消息（session / llm / task）经 **UDS**（路径 = **约定值** `%LOCALAPPDATA%\chonkpilot\server.sock`，**无 CLI 参数**）转发常驻后端 `chonkpilot-server.exe`（`src/server`，见 [62 §3A](../60-reference/62-命令行与参数.md)）。**免鉴权由前门决定**（UDS 前门 = 本机信任）；flash **不发布 `instance-heartbeat`**（存活由 UDS 连接断开判定），客户端 `form` 注入 **`desktop`**（本地免鉴权 + 不心跳，前端语义与桌面单体一致）。
> **✅ flash 已落地（2026-10-10）**：`src/lib/gui` **远程总线模式**入口 `gui.RunRemote`（桥总线 = `uds.Client`、`srv=nil`/不建 filesys、`SetFacade(nil)` → `data-*` 走总线 persist 经 UDS 事件回投、data-dir 约定 `<work_dir>/.chonkpilot`）+ 新宿主 module `src/flash`（`gui.RunRemote` + **前端 dist 不 embed**：落 `<exeDir>/frontend/dist`，以 `os.DirFS` 加载）+ 纯逻辑包 `src/lib/gui/flash`（打开语义 / 最近项目 / prjusr 文件锁）+ `uds.Client.OnClose`（断线 = 退出）+ `bridge.SetInstanceExitSuppressed`（共享实例多窗口）。见 [19 §8.11](../10-architecture/19-多租户服务端形态.md) / [42 §2.18](../40-roadmap/42-决策记录.md)。**🆕 2026-10-10**：项目单例改 **prjusr 文件锁 `chonkpilot.lock`**（flash 与桌面单体同名同目录；**原「具名窗口类」单例已删除**）；flash-cli / 自动拉起 server 均已落地。见 [42 §2.28](../40-roadmap/42-决策记录.md)。

---

## 3. 对外接口

### 3.1 虚拟源 HTTP 面（`appHandler`）

| 路径 | 用途 |
|------|------|
| `POST /publish` | 前端消息上行（`{type, payload}`）→ 结果信封 `{ok, result, errors}`（恒 200） |
| `GET /show/<path>` | **资源字节流唯一豁免**（`internal/fileserver.FileShowHandler`，iframe 预览） |
| 其余 | embed dist 静态资源（强制 `Cache-Control: no-cache, no-store, must-revalidate`） |

### 3.2 桥的两向

- **上行**：`mq.emit` → `fetch /publish` → `handlePublish` →（`gui.window.status` 特判 / `DispatchLocal` 本地优先 / `br.PublishEvent`）。
- **下行**：桥订阅相对全通配 `>` → `forwardEvent` → `mqTypeMap` 映射为稳定前端 type → `window.mq.emitRemote(envelope)`。

### 3.3 消息分派（`PublishEvent`）

| type | 处理 |
|------|------|
| `llm-start` | 拆两步 `session-start` + `session-send{type:text-user}` |
| `gui.*` | 桥本地 `guiDo`（`guimsg.go`） |
| `data-*` | 经总线 persist（`dataViaPersist`，3s 超时） |
| 含 `.` 的点分主题 | 直通总线（`publishV`） |
| 单字方法 | `frontMethodSubjects` → 域主题 |

`gui.*` 本地动作：`init-data` · `ui.save` · `recent.list` · **`recent.remove`（删除最近项目记录，仅删记录不删资产）** · **`select-dir` / `selected-dir`（目录选择两段式，🆕 2026-10-09 由 `dir.open-dialog` 拆分；`show-dialog` 为 browser 请 UI 弹框）** · `pick-executable` · **`file.save`** · `dir.open` · `console.open` · `vcs.info` · `reveal` · `open-with` · `search` · **`capture` / `captured`（截图两段式：`captured` 只回 `b64`、**不落盘**，🆕 2026-10-09 / 2026-10-10 收敛）** · **`upload`（server 落盘 `<data_dir>/<dir>/<name>`、只回 `{name}`，🆕 2026-10-10）** · `prompt-optimise` · `toolchain.detect`（6 工具链 + Chrome 探测） · `system.builtins`（exe 同目录 `config.json` 的内置 MCP 只读项，不含 `llms` 段）。

> **目录选择两段式（`gui.select-dir` / `gui.selected-dir` / `gui.show-dialog`，🆕 2026-10-09 / 2026-10-10；[61 §1](../60-reference/61-消息一览.md)）**：请求 `gui.select-dir{widget_id}`（结果事件 `gui.selected-dir{widget_id,status,path?}`）由**调用侧适配**包成 Promise（`src/frontend/src/utils/guiAsync.js::selectDir`，按 `widget_id` 关联；`api/config.js::openDirDialog` 委托）。**处理方二分**：① **GUI/WebView2** → 桥 `guimsg.go` 弹**系统原生目录框**后 `EmitFrontend(gui.selected-dir)`；② **browser/服务端** → 无原生框 → 服务端 `publish.go:guiDo` 的 `select-dir` 分支**下发 `gui.show-dialog{widget_id, kind:"select-dir", options}`**（请 UI 自行弹框，**不阻塞**）。**前端消费 `gui.show-dialog`**：`src/frontend/src/composables/useGuiShowDialog.js`（`App.vue` setup 全局订阅一次，`mq.on` 非 `watch`）→ 自研 `DialogShell` 承载 `views/common/DirPickerDialog.vue`（数据源 = 事件 `options`，**options 驱动**，按层浏览/返回上级/确认）→ 用户选定/取消/关闭发 `gui.selected-dir`（`utils/guiShowDialog.js` 纯逻辑构造载荷）。`options` 口径 = **该用户允许根**（`allowedDirs()` 根 = `auth.projectRoots`；缺省用户数据根）**及其子目录**（深≤3 / ≤200 / 跳隐藏与重型目录；原非 MQ 路由 `GET /dirs` 已删除）→ 同一用户下多个项目（如 `proj-a/b/c`）皆可选。「以新进程打开目录」是另一条动作 `gui.dir.open`：**native / GUI** = 起新进程打开；**browser（🆕 2026-10-10，`publish.go:guiDirOpen`）** = 服务端把**本实例** `work_dir` 切换到目标目录（复用既有 `instance-claim` 做归属校验 + `instance-register` 重绑定 → 入口 `applyInstanceBind` 就地更新作用域），前端选后**就地刷新**（`Toolbar.vue` → `openDir(path)` → `reinitProjectState()`：重挂主布局 + 重跑启动序列，**不整页重载**）。见 [19 §8.8 §C](../10-architecture/19-多租户服务端形态.md) · [42 §2.23](../40-roadmap/42-决策记录.md) · [42 §2.25](../40-roadmap/42-决策记录.md)。

> **`gui.file.save`（native 能力；[61 §1](../60-reference/61-消息一览.md)）**：`guimsg.go` 分派 → `bridge/configfile.go`（+ `savefile_windows.go` 的 `GetSaveFileNameW`）。payload `{name, content, mode?}` → `{path?}`（用户取消 = `""`）：`mode` 缺省 / `"dialog"` = 系统「另存为」；`mode:"backup"` = **不弹框**，直接落 `<dataDir>/backup/<name>`（`dataDir` 未配置 → `<workDir>/.chonkpilot/backup/`）。**安全**：`content` **可能含 API Key**（`llms[].apiKey` / `mcps[].env`、`headers`）→ 本能力**绝不记录 `content`**（无日志打印、错误详情不含内容），只回传落盘路径；`name` 经 `filepath.Base` 净化（防路径穿越）、空名拒绝。**browser 形态 = 明确不支持**（`src/lib/llm/httpapi/publish.go` 的 `browserUnsupported`；见 §9）。

> **场景向导启动触发（`agent-wizard`）**：GUI 桥在 `gui.init-data` 分支（`guimsg.go`）stat `<workDir>/.chonkpilot/project_spec.md` —— **缺失** → 应答补**只增布尔字段 `wizard_required: true`**（[61 §1](../60-reference/61-消息一览.md)），并向本窗口下发 **`agent-wizard`** 事件（`EmitFrontend`，payload `{reason:"missing_spec", work_dir, spec_path}`）；**存在** → `wizard_required: false`、不发事件。**检测落点在 GUI 桥本身（非插件）** —— 设计稿设想的 `plugin-wizard` **未落地**（[37 SCEN-012](../30-function-points/37-场景.md)）。前端消费：`MainLayout.vue` 订阅 `EventNames.agentWizard` → `openScenarioWizard`（`src/frontend/src/views/scenario/ScenarioWizardDialog.vue`，复用 `DialogShell`；向导另可经**场景编辑对话框底部【向导】按钮**手动拉起，同入口）。向导 Step1 探测经 `agent-wizard-probe`、提示词合成经 `agent-wizard-compose`（按项目特点把出厂 archetype 合成为定制化提示词，纯字符串组装；合成文本末以 `{{path.*}}` 四根路径占位符给出技能位置，**组装系统提示词时**渲染，指示 LLM 用文件工具自读，不用 `mcp_load`）、Step5 生成经 `agent-wizard-generate`（server 处理：非主 agent 合成提示词经门面 `ProjectAgentWrite` 落项目级 capability agent 文件 + 以**引用**入场景，主 agent 内联；数据访问经 data 门面 inline 直调，**不新增 `data-*` 主题**）、【关闭/稍后】经 `agent-wizard-skip`；消息面明细见 [61 §4.2](../60-reference/61-消息一览.md)。

---

## 4. 内部控制流

**启动（`main`）**：

```text
参数解析 → ResolveDir 规范化 → instanceID(uuid)
  → mq.New(Prefix:"chonk.")
  → filesys.New(bus).Start()
  → （!noServer）server.New(bus, ...)：内嵌 persist+gateway+mcp-server + compress/history/codegraph 插件
  → webview2.NewWithOptions（Frameless、Title=chonkpilot-<工作目录名>、1280x800）
  → bridge.New(...) + Start()：instance-register + 订阅 ">"
  → （--test-port>0）newTestServer + Start
  → 拦截 web 资源、注入 console 捕获
  → 导航完成回调 → SetReady → restoreWindowFromConfig → Show
  → Navigate(appOrigin+"/") + Run()
```

> **启动期 WebView2 profile 孤儿清理**：`Main` 在建窗**之前**调 `pruneWebviewProfiles(~/.chonkpilot/webview2)` —— **尽力而为**清理 `gui-*` 目录中 **LastWriteTime 早于 24h** 者（本进程 profile 目录名 = 实例 id，见 `newUUID` → `gui-<数字>`；活跃 profile 被 WebView2 运行时持续写入 → 24h 未变动即判残留；当前进程自身目录在建窗时才创建、天然晚于 cutoff，**无需显式排除**）；**只认 `gui-` 前缀**（不动根下其它内容）；删除失败（被占用）逐条跳过并忽略，**不阻断启动**；清理数量 + 释放空间记 Info 日志。正常关闭仍由窗口工厂逐窗口 `RemoveAll` 清理（见 [24 §4.1](../10-architecture/24-多窗口模型设计方案.md)）。

**bridge 包文件职责**：

| 文件 | 职责 |
|------|------|
| `bridge.go` | 桥核心：instance 注册、订阅透传、`PublishEvent` 分派、`mqTypeMap`、src 防环（1s 窗口）、`EmitFrontend`、`WaitForEvent`（test-port） |
| `guimsg.go` | `gui.*` 本地面（复用原 `/call` 内部 `callX`，`/call` 已清零）；含 `init-data` **场景向导启动检测**（工程规格 `<workDir>/.chonkpilot/project_spec.md` 缺失 → 应答补 `wizard_required` + 下发 `agent-wizard`） |
| `data.go` | `data-*` 经总线 persist；prj-config 键值面；usr 配置（标量逐 key + 专用表 `llms`） |
| `local.go` | 初始化数据、布局/窗口/UI/文件树/打开态保存（prjusr）、最近目录（list/remove）、打开目录、VCS、检索 |
| `builtins.go` | `gui.system.builtins`：读 exe 同目录 `config.json` 的只读内置 MCP 项（`mcpServers` 段） |
| `toolchain.go` | `gui.toolchain.detect`：探测 6 语言工具链 + Chrome 路径/版本（系统级候选不落库）；探测顺序 = PATH → 注册表 `App Paths`（HKCU→HKLM）→ 环境变量目录（JAVA_HOME/GOROOT 等）→ 常见安装目录；候选环境变量支持 `%VAR%`/`$VAR`；**Chrome 版本来自其目录 `Application\<ver>\` 的最高版本号目录**（不执行 `chrome.exe --version`） |
| `capture.go` | 截图：隐藏本窗 → GDI 全屏 BitBlt → **只回截图字节 base64 `{b64}`**（**不落盘**；落盘由 server 决定，见 [61 §1](../60-reference/61-消息一览.md) gui.capture/gui.captured；纯 syscall，无 CGO）。**R7-D**：收尾**先选回旧位图再 `DeleteObject`**（位图被选入 DC 时 `DeleteObject` 会失败、不回收）→ 修每次截图泄漏 1 个 DIB |
| `upload.go` | 附件上传：base64 → **prjusr 数据根** `<dir>/`（`bridge.uploadRoot()`；`dir` 缺省 `tmp/uploads`、截图 `screenshot`；`--data-dir` 显式时 = 该数据根）→ **只回 `{name}`**（真实落盘名，不暴露服务器路径）。**R7-B-4**：落盘后按 `uploadfs.KeepCap`（2 GiB，**按目录**、mtime **最旧优先**）做**容量兜底** —— 兜底**下沉到 lib core `uploadfs.Save`**（GUI 桥与 browser `guiUpload` **同源**），不再仅 GUI 启动期一次 |
| `optimize.go` | 提示词 AI 优化：SSE 流式，事件 `optimize-token/done/error` |
| `compat.go` | 旧协议事件名兼发（旧回归脚本用） |

> **多窗口（🔵 待实施，[24-多窗口模型设计方案](../10-architecture/24-多窗口模型设计方案.md)）**：`Main` 由"单窗口"改为**窗口工厂**（每窗口独占 OS 线程 + `CoInitializeEx(STA)` + 独立 WebView2 `DataPath`）；`appHandler` 由单例改为**每窗口一个**（`role` = `main`/`chat`），使 `/publish`、`/show/`、`gui.window.status`、截图**绑定到调用来源窗口**；新增进程级**窗口注册表**（`windowID → {hwnd, role, sessionID}` + 反向索引 `sessionID → windowID`）承载"**一个 session 只允许一个窗口**"（已开 → 激活；主窗口当前会话 → 拒绝）。每窗口一个 `bridge`（各自 `instance_id` + 各自 `eval`）→ 事件隔离由既有 `acceptEventInstance` 提供，**不改 `eval` 扇出、不引入 `window_id`**。技术路径已 POC 验证（`TECH-POC/webview2-multiwin/`）；**关窗一律用 `Destroy()`**（`Terminate()` 跨线程无效）。

---

## 5. 预览区（CodeView / renderType 映射）

> 本节按现行实现口径整理（判定顺序与扩展名集合均以 `CodeView.vue` 为准）。

**组件职责**：

| 视图 | 组件 | 职责 |
|------|------|------|
| 主预览 | `CodeView.vue` | 多 tab 预览区：底部 tab 栏，按 `renderType` 分派渲染；功能页 tab（settings / scenario / dbviewer / tools / knowledge） |
| 空态 | `CodeView.vue` | 无 tab 时显示**当前工作目录**路径 + 「打开 / 新建工作目录」按钮；点击复用既有 `workdir-open` / `gui.dir.open` 语义，不再强制启动即选目录 |
| 二进制查看 | `HexView.vue` | `hex` 类型文件的十六进制展示 |

**renderType 判定**（顺序即源码顺序，`CodeView.vue::computeRenderType`）：

| 优先级 | 条件 | renderType | 渲染方式 |
|--------|------|-----------|----------|
| 1 | 路径以 `db://` 开头 | `none` | 不渲染（数据库查看器自行处理） |
| 2 | 备份文件（`~` / `~$` / `.swp` / `.swo`） | `unsupported` | 不支持提示 |
| 3 | 原语文件（`*.tool.md` / `*.skill.md` / `*.prompt.md` / `*.resource.md` / `*.agent.md`） | `primitive` | 原语面板 |
| 4 | 扩展名 = `md` | `markdown` | `@ashlesss/markstream-vue`（可切源码） |
| 5 | 扩展名 = `pdf` | `pdf` | `<iframe>` + `raw=true` 文件 URL |
| 6 | 扩展名 ∈ `codeExtensions` | `code` | `@file-viewer/vue3`（preset-lite code renderer，只读高亮，无工具栏） |
| 7 | 扩展名 = `html` / `htm` | `html` | iframe 预览（可切源码） |
| 8 | Office 扩展名 | `docx` / `xlsx` / `pptx` | `@file-viewer/preset-office`（浏览器本地解析） |
| 9 | 图片扩展名 | `image` | `<img>` + 滚轮缩放 / 拖拽平移 |
| 10 | 音频扩展名 | `audio` | `<audio>` |
| 11 | 视频扩展名 | `video` | `<video>` |
| 12 | hex 扩展名 | `hex` | `HexView.vue` |
| 13 | 压缩包扩展名 | `unsupported` | 不支持提示 |
| 14 | 其余 | `text` | 纯文本 `<pre>` |

扩展名对照：

- **code**：`codeExtensions`（内置扩展名数组，见 `CodeView.vue`；已移除 monaco/monaco.js）
- **docx**：`docx, doc`；**xlsx**：`xlsx, xls`；**pptx**：`pptx, ppt`
- **image**：`png, jpg, jpeg, gif, svg, webp, ico, bmp`
- **audio**：`mp3, wav, wma, ogg`；**video**：`mp4, webm, mkv, avi, mov`
- **hex**：`exe, dll, so, bin, obj, lib, dylib, class, pyc, o, a, out, wasm, dat`
- **unsupported**：`zip, 7z, rar, tar, gz`

> 规则：所有扩展名比较均 `.toLowerCase()`；`noPadTypes` 必须是真实 `renderType`（不能是扩展名），当前含 `code/pdf/docx/xlsx/pptx/image/audio/video/markdown/html/primitive`。

**关键交互**：

| 交互 | 实现 |
|------|------|
| 图片缩放 | 滚轮 ±5% 步进（`imageZoom`） |
| 图片平移 | `mousedown` 记录起点 + `window mousemove` 改 `scrollLeft/scrollTop` |
| markdown / html 源码切换 | 头部「源码 / 预览」toggle（`showSource`） |
| 文件 URL | `getFileUrl(path)`，二进制用 `raw=true` 直出 |
| 预览页签栏溢出（`TabBar.vue`） | 外层 `.tb-bar` = `position:relative; overflow:hidden`（裁剪）+ `ResizeObserver`（**同时观察 `.tb-bar` 与 `.tb-inner`**；另有 window `resize` 兜底）→ 尺寸/内容宽变化即重判；内层 `.tb-inner` = `display:flex; gap:2px; width:max-content; flex:none`（内容自然宽，**不收缩、不裁切、不隐藏**多余页签；不再 `flex:1`/`min-width:0`/`overflow:hidden`）；判定口径 = **按最后一个页签的位置**（`.tb-tab:last-child` 的右边界 > 外层可用宽右边界，即 `bar.right - paddingRight`，容差 0.5px；页签尚未渲染时退化为 `inner.scrollWidth > bar.clientWidth`）→ 决定是否显示 `.tb-more`（"..." 按钮，**绝对定位覆盖**在右侧：`right:0; top:50%` + 自带底色 + `inset` 左边框 + `z-index`，**不占布局宽、不参与判定回路**，故不会"有按钮→更挤→更多按钮"）；**多余页签不再隐藏**（`tb-hidden`/`visibleCount`/`visibleKeys`/`hiddenTabs`/`gotoHidden` 已删除）→ 被遮住的最后一个页签**仍渲染**（可被按钮遮住一半，属预期），**且同时出现在弹框里**。弹框 `.tb-more-pop` 列出**全部**页签（按当前内部顺序）；选中某项 → 该项移到**显示首位**、其余保持相对顺序顺移（**只改组件内部显示顺序**：`orderKeys`，`setFirst` 仅 `emit('update:modelValue')` + 关闭弹框，**不改 `props.tabs`、不 emit 排序事件、不额外通知外部**；`displayTabs` computed 在 `props.tabs` 变化时过滤已删 key、追加新增 key）。旧机制（`.tb-hidden` + 累加 `offsetWidth` 算可见数 + 隐藏多余项）**已删除** |
| 预览页签栏右键菜单 | **关闭本页签 / 关闭右侧 / 关闭其它 / 关闭全部**（`preview-tab-close-this` · `preview-tab-close-right` · `preview-tab-close-others` · `preview-tab-close-all`）。**收敛为单一 MQ 路径**：四项菜单项**只经 `v-mq` 触发**上述四个主题（`TabBar.vue` 触发 → `CodeView.vue` 订阅），**无 emit 本地直连副本**（`TabBar` 已不再 emit 本地动作）；属**前端客户端 mq** 路径，**零新增消息主题、不经后端**。文案：**「关闭全部」（zh）/「Close All Tabs」（en）**（`common.tab_close_all`） |

---

## 6. 数据结构与存储

| 数据 | 载体 | 当前落层 | 目标（见 [02-配置层级](../00-overview/02-配置层级.md)） |
|------|------|---------|------|
| `layout` / `window` / `opened-files` / `filetree-*` | prj config 键（`{"v":"<json>"}`） | **prjusr** | 已落 **prjusr**（个人运行态；读取 prjusr 优先、回落 prj） |
| 用户配置（标量逐 key + 专用表 `llms`） | usr config 表 + `llms` 专用表 | usr | usr（已拆细 key） |
| `recent_dirs` | usr config 自由键（`data-user-config-{load,save}`；值 = JSON 数组字符串，前插 + 去重 + 10 条截断） | usr | 已落 **usr**（跨进程 / 重启持久化；`gui.recent.list` 读、`gui.recent.remove` 删记录，见 [61 §1](../60-reference/61-消息一览.md)） |

**文件树展开态（`filetree-expanded-key` / `filetree-selected-path`）**：只保存展开目录的相对路径数组与选中路径，重启时经 `init-data.expandedKeys`/`selectedKey` 恢复。

**MCP 配置界面**（`views/config/SettingsMCPPage.vue` 列表 + `views/config/EditMCPDialog.vue` 编辑弹窗；落库 = **四级文件化 MCP** `<级别>/capability/mcps/<名>.json`，见 [64 §6](../60-reference/64-配置项一览.md)）：编辑弹窗含「**按实例隔离**」Switch（`config.mcp.isolate`）——**三态表达**：未拨动 = **未设置**（保存时 `delete localData.isolate`，**不写库**）+ 提示「自动（未设置，按传输方式推断）」（`isolateAuto`，另附 `isolateHint`）；一旦拨动即视为**显式设置**并写 `true`/`false`（未显式设置时开关显示随 transport 变化的推断值，computed 推断、无 `watch`）。列表新增「按实例隔离」列（`isolateLabel`）：**显式设置 → 是/否**；**未设置 → 推断值 + 「（自动）」后缀**（`isolateAutoSuffix`；推断口径与 gateway `ServerEntry.IsolateEnabled()` 一致：stdio → 隔离、http/sse → 共享，transport 留空按连接点推断）。

**编辑弹窗页签**：`EditMCPDialog` 内容顶部改用 `Tabs` 拆「**基本信息**」（名称 / 传输方式 / 服务地址 / 命名空间 / 超时 / 描述 / 请求头）·「**运行信息**」（运行时 | 工作目录 / **按实例隔离** | **沙箱** / 启动参数 / 环境变量）·「**工具**」（【加载工具】+ 逐项勾选）共**三页签**（「**启用**」移到底栏与【保存】同行）；页签**仅显示分组**，`handleSave` 仍一次性提交三页全部字段。字段说明改用 label 右侧 `?` 图标 + `Tooltip`（「传输方式」= `config.mcp.transportHint`，新 `help` 图标）；「**分类**」输入项已摘除（`category` 后端字段保留，供 `servers.list` 分组）。「**沙箱**」（`config.mcp.sandbox`）由列表页列迁入「运行信息」页签（三态 Switch，同 `isolate` 口径），**仅 stdio 可开**：http/sse（及 auto 仅填 url）Switch **禁用** + 原因 tooltip（`config.mcp.sandboxStdioOnly`）**〔2026-10-12：`mcps[].sandbox` 已废弃为开关来源（开关来源收敛到 usr `tool_sandbox.mcps`）；本 Switch 的写入口保留、由后端读取时迁移承接，登记 [41 `I-179`](../40-roadmap/41-未决项登记.md)〕**；`SettingsMCPPage` 该列已摘除（编辑入口唯一 = 弹窗）。「**高频工具**」**不再是字段**：改为独立「**工具**」页签 ——【**加载工具**】按钮按别名列出该 server 工具 + 「全部工具」= 全部 hot + 逐项勾选（数据源 = 既有 `tools-list`，**零新增消息面**）；写库仍为**契约原名**列表（`"*"` = 全部），仍随主对话框「保存」落四级文件化 MCP 条目的 `hot_tools`；原独立弹窗 `SetMCPHotToolsDialog.vue` **已删除**（并入页签）。另：「**请求头**」「**环境变量**」改为 **KV 行编辑**（`components/ui/KeyValueEditor.vue`，可增删行），「**启动参数**」为**行列表**编辑（`args` 是 `[]string`，非 KV）。

**MCP 配置四级文件化**：MCP 配置**载体改四级文件** `<级别>/capability/mcps/<名称>.json`（app/user/project/prjusr；一个 server 一个文件）——列表页（`SettingsMCPPage`）经**新数据层 mcp 域**（`data-mcp-{list,save,delete}` + 订阅 `data-mcp-refresh`）读写，**不再整表替换 usr KV `mcpServers`**；列表新增「**级别**」列（文案 = `scenario.level.*`）。编辑弹窗「基本信息」页签新增**级别选择**（`config.mcp.level`，四级，选项文案 = `fileTree.kb_level_*`，缺省 `user`），保存按所选级别落对应级文件（改名/移级先删旧文件、不残留）；i18n = `config.mcp.level` / `config.mcp.levelHint`（zh/en）。**旧 usr KV `mcpServers` 已彻底废弃**（代码零兼容、不再回落）——见 [64 §6](../60-reference/64-配置项一览.md) · [02 §7.3](../00-overview/02-配置层级.md)。

**保存时机**：① 每次展开/折叠后；② 收到变更推送后（防抖约 500ms）；③ 选中节点变化时；④ IDE 关闭时兜底保存。

---

## 7. 依赖

- **上游**：`src/lib/core`(mq/paths) · `src/lib/llm`(server) · `src/lib/filesys` · `src/plugins/plugin*` · `jchv/go-webview2`（本地 `src/lib/go-webview2`）。
- **indirect**：`src/lib/data` · `src/lib/gateway` · `src/lib/mcp-server`。
- **下游**：无（终端应用）。

---

## 8. 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| 无 HTTP server | WebView2 `WebResourceRequested` 虚拟源拦截 | 免端口、免监听、资源不落盘 |
| 前端资源 no-cache | 强制 `no-cache, no-store, must-revalidate` | WebView2 会持久缓存旧页面 |
| Frameless 保留 WS_THICKFRAME | 无原生标题栏；`WS_THICKFRAME` **只保留最大化 / 贴靠等原生行为** | frameless 下 WebView2 子窗铺满客户区 → `WM_NCHITTEST` 恒 `HTCLIENT`，**系统层无边缘 resize 命中区**（WIN-1 / G-59） |
| 窗口 resize（frameless 主窗口） | 前端边缘命中 → `gui.window.resize{hit}` → 宿主 `StartResize(HT*)` 进入系统原生 resize 模态循环 | 系统无命中区，只能由前端补命中：`components/shell/WindowResizeHandles.vue` 渲染 8 条透明命中条（4 边 6px / 4 角 12px，`-webkit-app-region: no-drag`），`mousedown` 发消息；`guiDo` 把方位映射为 `HTLEFT(10)…HTBOTTOMRIGHT(17)` 后调 `webview2.WindowResizer.StartResize`（`src/lib/gui/bridge/guimsg.go` / `window.go`；设计见 [go-webview2/FORK.md](../../../src/lib/go-webview2/FORK.md) P4）。**仅 frameless 装配**；对话窗口（原生标题栏/边框）不装配 → 明确失败。 |
| 窗口移动（frameless 主窗口） | 顶栏非交互区 `mousedown` → 位移 >4px 后发 `gui.window.resize{hit:'caption'}` → 宿主 `WM_NCLBUTTONDOWN(HTCAPTION)` 进入系统移动模态循环 | **WebView2 `app-region` 在本项目自绘 frameless 窗口上实测不触发**（`ICoreWebView2Settings9.IsNonClientRegionSupportEnabled=true` 已生效仍无拖拽区）→ 与 resize 同链路显式进入系统循环（`ChonkTopBar.vue` 阈值判定保留单击/双击；`guimsg.go` 的 `caption → 2(HTCAPTION)`）。CSS 仍保留 `app-region: drag/no-drag` 仅作语义标记。**2026-10-17 修** |
| `--test-port` 仅 127.0.0.1 | 外部脚本驱动端点 | 测试专用，见 [52-test-port驱动](../50-testing/52-test-port驱动.md) |

---

## 9. 场景与边界

- **窗口恢复归一化**：`normalizeWindowRect`（主显示器工作区 clamp、完全屏外居中、保证标题栏 ≥80px 可见）。
- **最大化同步**：`window-maximized-changed` 事件 → 前端切图标。
- **`/show` 路径越界防护**：`fileserver.withinDir`（Windows 大小写不敏感），`DataDirs` 为额外放行根。
- **防环**：本实例刚发布的主题（1s 窗口）不再回发前端。
- **多窗口（🔵 待实施）**：窗口 ↔ `instance` **1:1**（窗 = 视口，instance = 运行态+消息归属+数据根绑定）；**`instance` 不做业务隔离**（同 `work_dir` 多 instance 共用同一 prjusr 库 → 会话列表/历史**共享**）。**关闭语义**：关任一窗口仅销毁该窗口 + 发该实例 `instance-exit`；**主窗口关闭 = 退出本进程**（其余窗口一并关闭）。**几何**：主窗口 `window.*`/`layout.*`（prjusr）；**对话窗口不持久化几何**（每次默认位置）。**不变量**：进程内 instance 数 = 窗口数（非「1 进程 1 instance」），见 [20-实例隔离与后端分离](../10-architecture/20-实例隔离与后端分离.md)。
- **退出**：`instance-exit` → persist 移除绑定 + server 取消 running turn + 释放锁；`beaconSaveLayout`（`sendBeacon`）兜底保存布局。
- **browser 形态的 native 能力边界**：`gui.file.save`（「另存为」对话框，native）**明确不支持** —— 入 `src/lib/llm/httpapi/publish.go` 的 `browserUnsupported`（返回 `{ok:false, error}` + errors，**不静默失败**，前端降级为页面下载 / 剪贴板）；browser 形态点分主题上行另有白名单 `allowedDottedPrefixes`（现含 `filesys.` / `vfts.` / `llm.test-connection` / `config.suggest` / `memory.flush`）。GUI 形态无此限制（桥本地实现 + 「点分直通」分支）。见 [61 §1/§1.1/§8](../60-reference/61-消息一览.md)。

---

## 10. 现状与待办

- ✅ `/call` RPC 已清零（前端无 `window.go.*` 调用点）；`compat.go` 兼容层保留。
- ✅ `layout/window/opened-files/filetree-*` 已落 **prjusr**（个人运行态；读取 prjusr 优先、回落 prj；[02-配置层级](../00-overview/02-配置层级.md) §5）；`ui`/`opened-file` 死键与 `filetree-data` 快照已摘除。
- ✅ `recent_dirs` 已落 **usr** config 自由键（跨进程 / 重启持久化，见 §6）；最近项目下拉支持**按条删除记录**（`gui.recent.remove`；**仅删记录，不删项目资产**）。
- 🗄 `models/config.go` 的 `AgentConfig` 已废弃（project_agents 表 deprecated），**遗留 DB 模型（`ScenarioConfig`/`ScenarioAgent` 等）已随"无历史数据、不写兼容"口径删除**。
- 🔵 工具栏「设置」改下拉菜单 + 配置页在 preview 打开（待改造）。
- 🔵 **预留功能（「敬请期待」、非死代码）**：`fork`（会话导航占位按钮，`src/frontend/src/views/sessions/SessionsPane.vue`，点击仅提示 `chat.fork_coming_soon`／「敬请期待」）· `analyzeDialog`（主布局预留引用，`src/frontend/src/views/layout/MainLayout.vue`）。
- 🔵 **多窗口（主窗口 + 纯对话窗口）设计定稿、待实施**：任务分解见 [24 §7](../10-architecture/24-多窗口模型设计方案.md)（MW-1…MW-13）；含 **Resolve 审计**（多实例下 `View.Resolve` 的「唯一实例回退」失效 → 不带 `instance_id` 的数据入口须逐条排查）；数据层取 **B 方案**（desktop 走 `dataDir == ""`，见 [12 §3/§5.3](../10-architecture/12-数据层.md)）。

---

## 11. 关联测试

- `src/test/chonkpilot-gui/unittest/`（Go：`models/path_test.go` 等）
- `src/test/chonkpilot-gui/systest/`（Python：`run_all.py`、`run_llm.py`，经 `--test-port=2345` + mock LLM `127.0.0.1:8901`）

---

## 12. 实现约定（前端与宿主）

> 本节为**强制约定**。

### 12.1 前端状态管理

- **必须用 `useXxx` composables 封装状态逻辑**（如 `useSession`、`useTaskPanel`）。
- **禁止用 `watch` / `watchEffect` 监听 props 或 store 的变化**；组件间通信用事件 `emit` 或 `bridge.on`。
- 通用模式见 [17-前端状态与MQ](../10-architecture/17-前端状态与MQ.md)。

### 12.2 `:style` 绑定单位

`:style` 中数字值**必须显式加 `'px'`**：`:style="{ width: sidebarWidth + 'px' }"`（无单位会被浏览器忽略）。

### 12.3 预览组件库（`@file-viewer/vue3`）

- **必须 named import**：`import { FileViewer } from '@file-viewer/vue3'`。
- Office 预览经 `@file-viewer/preset-office` 浏览器本地解析。
- 图片缩放用 `overflow: auto` + 原生滚动条；拖拽平移用 `scrollLeft` / `scrollTop`。
- PDF 预览用 `<iframe>` + `raw=true` URL。

### 12.4 Resizer 设计规范

> **方案 A（视觉 / 命中解耦，`components/split/SplitPanel.vue`）**：本体恒 **1px 发丝线**、命中区为 **5px 透明伪元素**，hover/拖拽只染伪元素 → **拖动瞬间不重排、不抖动**。旧口径（handle 4px + 常态 `transparent` + hover 直接改本体）**作废**。

1. **本体 = 真实 `<div>`**（`.split-resizer`），占布局空间 = **`var(--split-hairline)`（1px）**：水平 `width: var(--split-hairline)` + `cursor: col-resize`；垂直 `height: var(--split-hairline)` + `cursor: row-resize`。
2. **本体常态 `background: var(--border)`**（发丝线取**边框色**，不再「露容器底色」，亦非 `transparent`）。
3. **命中区 = `::after` 绝对定位伪元素**：`content: ''` + `position: absolute` + 常态 `background: transparent`（**不占布局、不改 1px 视觉**）。水平 `top/bottom: 0` + `left/right: calc((var(--split-hairline) - var(--split-hit)) / 2)`；垂直 `left/right: 0` + `top/bottom: calc((var(--split-hairline) - var(--split-hit)) / 2)` —— 即向两侧各外溢 `(hit − hairline)/2 ≈ 2px`，命中跨度 = `--split-hit`（5px）。
4. **hover / 拖拽激活（`.is-active`）只给 `::after` 填 `var(--accent)`** → 5px 蓝块；**本体恒 1px、绝不加宽**（原 `.is-dragging` 本体 `gap+2px` 加宽已移除）。
5. `@mousedown` 用 `.stop.prevent`（阻冒泡 + 默认行为）+ 闭包捕获起点（`clientX/Y` 与目标 pane 的 `getBoundingClientRect()`），不用全局 ctx。
6. `window mousemove` 设 `{ capture: true }`，按 `paneMin/paneMax` **clamp** 后**直改 DOM**（`targetEl.style[width|height]`）；`window mouseup` 移除 listener，恢复 cursor / userSelect。
7. 拖拽数学走 **pane 的 `getBoundingClientRect()`**（与 resizer 宽度无关）；被 resize 的 pane 加 `flex-shrink: 0`。

- **token（`variables.css` :root）**：`--split-hairline: 1px` · `--split-hit: 5px`；**`--split-gap` 已移除**。
- **`gap` prop**：`SplitPanel` **保留声明但不再参与视觉 / 命中宽度**（兼容既有调用方）；`MainLayout.vue` 外层 `:gap="0"`（不渲染 resizer）+ 4 处内层 `:gap="4"`。
- **`LAYOUT_GAPS`**：`MainLayout.vue` 由 **8 → 2**（= content\|chat + filetree\|preview 两条 1px 发丝线，与真实分隔条宽度一致；用于 filetree 宽度的 clamp 余量）。
- **已知取舍**：命中区对称外溢 2px → 会**压住相邻 pane 边缘 2px**（点该 2px 带优先命中 resizer）。
- **关联测试**：前端守卫 `src/frontend/test/uxLayoutSplit.test.js`（本体 1px / `--border` / 命中伪元素 5px / 激活 `--accent` / `--split-gap` 移除）；L4 `test_layout.py` **L1c**（真机分隔条几何与颜色）+ **L8b**（拖拽中本体恒 1px）。

### 12.5 DialogShell 对话框高度

**禁止**只在内部 div 设固定高度（表头 + padding 会叠加溢出）。正确做法：用 `max-height` 约束弹窗根节点，内部内容区 `.dialog-body` 用 `flex: 1` 撑满（`overflow: auto` 内滚动、表头固定）：

```css
/* 自研 DialogShell：根节点 .dialog-shell 限高，内容区 .dialog-body 撑满并内滚动 */
.dialog-shell:not(.dialog-state-maximized) { max-height: calc(100vh - 40px); display: flex; flex-direction: column; }
.dialog-body { flex: 1; overflow: auto; min-height: 0; }
```

**弹窗载体 = 自研 `components/dialog/DialogShell.vue`，其等价约束已内建**：JS 侧 `base.maxHeight = min(options.maxHeight, calc(100vh - 40px))`（`DialogShell.vue` `:228-233`），CSS 侧 `.dialog-shell:not(.dialog-state-maximized){ max-height: calc(100vh - 40px) }`（`:462-464`）与 `.dialog-body{ flex:1; overflow:auto; min-height:0 }`（`:518-523`）。**实质要求**：必须用 `max-height` 约束弹窗 + 内部 `flex:1` 撑满内容区（本项目前端无 element-plus 依赖、全仓 0 处 `el-dialog`）。

### 12.6 静态文件服务

嵌入式文件服务**必须加** `Cache-Control: no-cache, no-store, must-revalidate`，否则 WebView2 会持久缓存旧页面。

### 12.7 日志规范（宿主进程）

- 宿主用标准库 **`log/slog`**（键值对形式，如 `slog.Error("msg", "err", err)`），**禁止** `log.Printf` / `fmt.Print`。
- 级别：启动/关闭/关键流程 → `Info`；调试 → `Debug`；异常 → `Warn` / `Error`。
- 落盘：宿主与后端日志统一落 `<prjusr 数据根>/logs/gui.log`（详见下）。
- 若需排查运行时问题，优先用结构化字段而非拼串。

> **日志落盘：统一到 `<prjusr 数据根>/logs/gui.log`**
> ① **宿主（gui）**：`src/lib/gui/logfile.go` 的 `dualWriter`（stderr 并存）+ `rotateWriter`（2MiB 滚动、保留 5 份）把 **slog 默认 logger** 同时写 stderr 与 `<prjusr 数据根>/logs/gui.log`；日志目录经 `gui.init-data` 只增字段 `logDir` 下发（[61 §1](../60-reference/61-消息一览.md)）。日志目录 = **`data.PrjUsrDir`（prjusr 数据根）** + `/logs`（`src/lib/gui/main.go` 传入 `attachFileLog`）：desktop 缺省（`data_dir == ""`）→ **`~/.chonkpilot/data/<prj-id>/logs/gui.log`**（跟随数据根）；**显式 `--data-dir`** → `<该数据根>/logs/gui.log`；`gui.init-data.logDir` 指向该处。
> ② **后端（`src/lib/llm/server` + 内嵌插件）**：本包输出收敛到单一出口 **`logf`**（`src/lib/llm/server/log.go`）—— **`logf` 始终写 stdout（行为不变，`--test-port` 捕获口径不变）**；装配方经 **`server.Options.LogWriter`（`io.Writer`，nil = 仅 stdout）** 注入额外 sink 时**同写 sink**，GUI 注入的即宿主那一个 `<prjusr 数据根>/logs/gui.log` 文件 sink（`src/gui/main.go` 传 `logWriter.fileSink()`）。插件仍走 `plugin.Deps.Logf`（= `Server.pluginLogf`）→ 经**同一出口**落文件。库侧只收 `io.Writer`，**不依赖 gui 包**。
> ③ **未覆盖（如实标注）**：`src/lib/llm/httpapi` 与 `src/lib/llm/main.go` 的 `log.Printf`（stderr；属独立入口 / browser 形态）未接入该 sink。

> **启动分段计时日志（OP-15）：仅插桩量测，不改启动行为/顺序**。为定位「启动慢」真实原因，在启动路径按段打点。格式统一：**`[startup] <阶段> 耗时 <段>ms（累计 <总>ms）`**（段 = 距上次打点，总 = 距起点 `markStartupBegin`）。
> - **宿主（gui，`slog`）**（`src/lib/gui/main.go`）：参数与目录解析 → prjusr 数据根解析（含 prj 库打开）→ 文件日志挂载 → MQ 总线初始化 → filesys 文件服务启动 → server 装配（capability 契约扫描 + gateway 构建）→ server 启动（数据服务/gateway/能力注册/工具缓存预热/插件）→ 主窗口创建（bridge/WebView2 环境就绪）→ 启动流程就绪；**主窗口首屏渲染完成**在 `window.go` NavigationCompleted（roleMain）经 `logStartupStageOnce` 只记一次。
> - **内嵌 server（`src/lib/llm/server/server.go`，统一出口 `logf`；同一 `Start` 亦为分离形态 `chonkpilot-server.exe` 入口 → 一处覆盖两形态）**：数据服务(persist)启动 → 任务层启动 → gateway 启动/下游连接 → capability fswatcher 监听 → 实例视图/心跳扫描启动 → 方法面/事件订阅 → 域工具注册 → 域 agent 注册 → 工具定义缓存预热(`gc.ListTools`) → 内嵌插件启动 → server 启动完成。
> - **装配期（`src/lib/assembly/assemble.go`，经 `logf`）**：capability 契约扫描/注册（含 `AppToolNames` 全树 `WalkDir`）、gateway 构建。
>
> **采数**：桌面单体见 `<prjusr 数据根>/logs/gui.log`；分离形态 `chonkpilot-server.exe` 见 stdout。**候选耗时段**（供定位）：capability 契约扫描、gateway Start/下游连接、`gc.ListTools` 预热（15s 上限）、插件 Start、DB 迁移/seed；**优化项待采数后再定**。


### 12.8 主题与浮层样式（token 约定）

> 暗色浮层配色修复的防回归约定。**主题载体 = `documentElement` 的 `data-theme` 属性，非 class** —— 页面不存在 `.dark` 之类的 class，故主题相关选择器**必须**写属性选择器（`[data-theme="dark"] …` / `[data-theme="nord"] …`）；写成 `.dark` / `:root.dark` / `xxx.dark` 属**失效选择器**（编译通过但永不命中）。

- **主题集**：`light`（默认，`:root`）/ `dark` / `nord`（`src/frontend/src/assets/styles/variables.css`）。**无 `system`**（跟随系统未实现）；切换见 `Toolbar.vue setTheme()`。
- **浮层（dialog / tooltip / popup / dropdown / 右键菜单）必须用主题 token，禁止硬编码浅色**：不得写死 `#fff` / `#f5f7fa` 之类浅色底色，或只在 `:root` 定义、暗色主题不覆写的自造 token。浮层底色一律引 `var(--panel-bg)` / `var(--bg-secondary)` / `var(--bg-primary)`，文字引 `var(--text-primary)` / `var(--text-secondary)`，描边引 `var(--border)`，hover 引 `var(--bg-hover)`。
- **浮层/强调/页签 token（`variables.css`，dark/nord 覆写）**：
  - `--tooltip-bg` / `--tooltip-color`：tooltip 底色/文字（light = 半透明黑 + 白字；dark/nord = `var(--bg-surface)` + `var(--text-primary)`），`Tooltip.vue` 的箭头用 `border-*-color: var(--tooltip-bg)` 同步。
  - `--accent-bg`：强调背景（light = `#ecf5ff`；dark/nord = `color-mix(in srgb, var(--accent) 18%, transparent)`），用于 `Button.vue` 的 `.b-btn.is-text:hover`、`AskUserContent.vue`、`AgentEditor.vue` 等 —— 避免暗色下刺眼浅蓝。
  - `--tab-inactive-fg`：页签**非激活**文字色，与激活态（`--text-primary` / `--accent`）保持可见差别。取值为语义次要色 `--fg-secondary`（三主题各自取值），不用 `color-mix` 调和。消费方 = `TabBar.vue`（`.tb-tab` / `.tb-more-item`）与 `Tabs.vue`（`.b-tabs-item`）。
  - **语义文字色 `--fg-*`**：统一文字角色，**新增代码优先用这组名**，不再直接写 `--text-primary` / `--danger` / `--accent`。
    - `--fg-primary`（主要，黑）＝ `var(--text-primary)`；`--fg-important`（重要，红）＝ `var(--danger)`；`--fg-link`（可点击，蓝）＝ `var(--accent)` —— 三者是**别名**，只在 `:root` 声明一次，自动随主题。
    - `--fg-secondary`（次要，暖灰偏黄）：**逐主题字面值** —— 消费场景 = **除 title/input 以外的说明文字**（字段 hint、页面描述、空态副文案）与**页签非激活文字**。light `#6f6a5a`（`--bg-primary #f8f9fa` 上 5.12:1；用户原拟 `#7a7566` 仅 4.04 → 压深）· dark `#b8b2a0`（`#181825` 上 7.9:1）· nord `#c9c3b2`（`#3b4252` 上 5.8:1）。
    - `--fg-disabled`（无效，灰）：light `#adb5bd` · dark `#6c7086` · nord `#7b8699`（禁用态文字，豁免 4.5 门槛）。
- **禁止引用未定义 token `--bg-elevated`**：全仓无定义 → 声明无效、回落透明/浅色，应为 `--bg-secondary`（`FileTree.vue` / `MessageList.vue` 等已归一）。新增代码不得再引该名。
- **色值混用 `color-mix`**：半透明 hover/淡色底统一用 `color-mix(in srgb, var(--token) N%, transparent)`（如 `TabBar.vue .tb-more-close:hover` 用 `var(--text-primary) 8%`），以便随主题 token 自适应。
- **DialogShell 覆写范式**（`components/dialog/DialogShell.vue`）：`.dialog-overlay` 与 `.dialog-shell` 是**兄弟节点**，需各自命中；暗色覆写以 `[data-theme="dark"] .dialog-shell, [data-theme="dark"] .dialog-overlay, [data-theme="nord"] …` 成组声明，内部只重绑 `--dialog-*` 变量到主题 token。
- **token 定义口径（唯一权威）**：只有 `src/frontend/src/assets/styles/variables.css` 的 `:root`（= light 默认，兼公共 token）/ `[data-theme="dark"]` / `[data-theme="nord"]` 三块中**声明过**的 `--x` 才算"已定义 token"；其他位置自造名一律不算（组件级可选覆盖钩子见下方专条）。
- **禁止引用未定义 token、禁止 `var()` 兜底硬编码**：`var(--x, <字面值>)` 若 `--x` 全仓无定义 → 恒落字面值、主题失效，属误用（同 `--bg-elevated`）。**token 归一对照**（94 个前端文件做"使用 − 定义"差集，复核 0 残留）：

  | 原误用 token | 原兜底值 | 归一为 |
  |---|---|---|
  | `--accent-color` | `#409eff` | `--accent` |
  | `--danger-color` | `#f56c6c` | `--danger` |
  | `--accent-light` | `rgba(64,158,255,.14)` | `--accent-bg` |
  | `--border-color` | `#d9d9d9` / `#e0e0e0` | `--border` |
  | `--bg-active` | `rgba(0,0,0,.08)` | `--bg-hover` |
  | `--warning-color` / `--warning-bg` / `--warning-border` | `#e6a23c` / `#fdf6ec` / `#e6a23c` | `--warning` / 新增同名 token |
  | `--bg-warning-soft` / `--border-warning` | `#fff8e6` / `#f0d98c` | 同上 `--warning-bg` / `--warning-border` |
  | `--font-size-sm` | `13px`（`SecurityConfig` 处 `12px`） | **新增** `:root --font-size-sm: 13px` |
  | `--border-radius` | `4px` | **新增** `:root --border-radius: 4px` |
  | `--split-gap` | `4px` | **新增** `:root --split-gap: 4px`（**已移除**，改由 `--split-hairline`(1px) / `--split-hit`(5px) 承载，见 §12.4） |
  | `--dialog-bg/-border/-header-bg/-overlay-bg/-btn-hover-bg/-radius/-shadow/-header-padding/-body-padding/-text-color/-font-size/-font-family` | 亮色缺省字面值 | `DialogShell.vue` 在 `.dialog-shell` / `.dialog-overlay` 基规则内**声明 light 默认值**（dark/nord 覆写，范式同上条） |

  新增 token 一律落在 `variables.css`：语义随主题变的（`--warning-bg` / `--warning-border`）在 dark/nord 同段覆写，不随主题变的（尺寸/字号/圆角）只在 `:root` 声明一次。
- **组件级可选覆盖钩子**（如 `--split-hairline` / `--split-hit`、`--dialog-*`）：允许组件暴露为覆盖点，但**默认值必须写在其声明处**（`:root` 或组件基规则），`var()` 内不得再写兜底 —— 即"未定义 token + 兜底"零容忍。
- **实心填充的文字色**：`--danger` / `--accent` 作**背景**时，文字统一用主题最底层色 `--bg-secondary`（light 恰为纯白 = 历史 `#fff`，light 零变化；dark/nord = 主题最深底色）。深色主题下 `--danger` 本身很亮，白字对比度仅约 2.3:1。既有 `TabBar.vue .tb-close:hover`、`CodeView.vue .preview-selection-bar` 用等价的 `--bg-primary`。
- **nord `--danger` 调亮**：`#bf616a` → `#f0959e`（同色相 354°，仍属 nord 红）。实测（test-port 实跑 computed style + WCAG 计算）：面板 `--panel-bg` #2e3440 **3.05 → 5.65**；工具面 `--toolbar-bg` #3b4252 **2.46 → 4.55**；实心填充（`--danger` 底 + `--bg-secondary` 字）**3.05 → 4.55**；菜单面 `--bg-surface` #4c566a **1.80 → 3.34**（**客观达不到 4.5**：该面亮度 L=0.094，需 L≥0.60 的近白粉才达标，会丢失"危险"语义；最优解是菜单/右键浮层底改用 `--bg-secondary`（→4.55），但属另一处变更）。`dark #f38ba8`（面板 7.17 / 工具面 5.43）与 `light #dc3545`（白底 4.53）不改。

> **对比度实测表 —— 表格为当前实测值**（tokens 取 `src/frontend/src/assets/styles/variables.css` 三主题块，对比度按 WCAG 2.1 相对亮度公式计算；阈值：正文字/小字（11–13px）**≥4.5:1**，非文本图形/图标/状态圆点（WCAG 1.4.11）**≥3:1**）。数值可由 `src/frontend` 的 `npm test`（`test/uxBatch1.test.js` 打印 `[contrast] …` 行）复现。

**① token / 规则**

| 元素 | 规则 / 取值 | light | dark | nord | 阈值 | 改前 |
|---|---|---|---|---|---|---|
| 弱化文字 `--text-muted` | 对 `--bg-primary`（token 值：light `#6b7280` / dark `#a6adc8` / nord `#93a8c8`） | **4.59** | **7.37** | **5.16** | ≥4.5 | light `#6c757d` 4.45 · dark `#6c7086` 3.36 · nord `#81a1c1` 4.64（dark 不可读、nord 余量过小） |
| inline code | `--text-primary` on `--bg-tertiary` | **13.01** | **12.97** | **7.49** | ≥4.5 | 原 `color: black` + 浅色底（dark/nord 不可读） |
| 工具卡头 `.section-header` | `--text-secondary` on `--bg-tertiary` | **6.90** | **8.42** | **6.39** | ≥4.5 | 原硬编码浅色字（`#b8860b`/`#1565c0`/`#2e7d32`/`#7b1fa2`）+ 浅色底 |
| 状态徽标 `.status-badge` | `--text-primary` on `color-mix(--语义 22%, --bg-tertiary)`（5 类徽标 × 3 主题，取最低） | **9.63** | **7.18** | **4.83** | ≥4.5 | 原硬编码彩色字（`#e65100`/`#2e7d32`/`#c62828`…） |
| 任务详情错误条 `.td-error` | `color-mix(--danger 70%, --text-primary)` on `color-mix(--danger 12%, 面板底)` | **5.77** | **6.55** | **4.57** | ≥4.5 | `#f5222d` 3.60（全主题不随主题） |

**② 残留硬编码浅色**

| 元素 | 规则 / 取值 | light | dark | nord | 阈值 | 改前 |
|---|---|---|---|---|---|---|
| 超时裁决提示 `.arbitration-hint`（11px） | light = `color-mix(--warning 55%, --text-primary)`；dark/nord = `var(--warning)` 原色（`[data-theme=…]` 覆写） | **4.71** | **12.91** | **8.00** | ≥4.5 | `#e65100` 3.60 / 4.33 / 3.30（三主题**均不达标**） |
| 停止按钮 hover `.stop-btn:hover`（图标） | `var(--danger)`，底 = `color-mix(--danger 12%, transparent)` 叠 `--bg-tertiary` | **3.24** | **6.72** | **3.27** | ≥3（图形） | `#ff4d4f` 2.42 / 5.07 / 2.45 |
| 任务区裁决条取消键 `.await-btn-danger`（11px 文字） | `color-mix(--danger 70%, --text-primary)`，底 = 按钮底 `--bg-primary` | **6.49** | **8.08** | **6.88** | ≥4.5 | `#ff4d4f` 3.10 / 5.02 / 3.82 |
| 任务节点停止图标 `.node-stop` | `var(--danger)`，底 = `--bg-primary` | **4.30** | **7.08** | **5.65** | ≥3（图形） | `#ff4d4f` 3.10 / 5.02 / 3.82 |
| 任务详情状态圆点 `.td-status`（7px） | `is-done` → `var(--success)` / `is-error` → `var(--danger)` / `is-stopped` → `var(--text-muted)`（底取 `--bg-primary` / `--bg-secondary` 最低者） | **3.22 / 4.30 / 4.59** | **11.03 / 7.08 / 7.37** | **4.94 / 4.55 / 4.16** | ≥3（图形） | `#52c41a` 2.15 · `#f5222d` 3.87 · `#bfbfbf` 1.74（light；dark 下 success `#52c41a`、nord 下 error `#f5222d` 亦不随主题） |

**③ 扫描口径（残留归零）**：`src/frontend/src/**/*.{vue,js,ts,css}`（**112 个文件**，去 HTML/CSS/JS 注释后）中，**硬编码浅色字面量 `#e65100` / `#ff4d4f` / `#f5222d` / `#52c41a` 零命中**（`npm test` 的「④ 全仓扫描」用例守卫）。**明确保留项（附理由，不视为漏网）**：

- `color: #fff`（工具条窗口关闭按钮、popover active、设置页徽标/按钮等）：**实心强调色填充上的文字**，属上文「实心填充的文字色」专条口径（light 恰为纯白 → 零变化）；dark/nord 下若要更稳，后续按该专条改用 `--bg-secondary`。
- `#67c23a` / `#bfbfbf`（`src/frontend/src/composables/useTaskStatus.js` 的状态→图标配色映射）：既有单测 `test/useTaskStatus.test.js` **锁定「既有 4 态逐值不变」**，改动会破坏该契约 → 保留。
- `rgba(0, 0, 0, ·)` 一类中性叠底（弹层遮罩/阴影/hover）与 `rgba(255, 193, 7, ·)` + `#f0ad4e`（`MessageItem.vue .notify-row` 的 🔔 完成通知浅琥珀底/色条）：**装饰性叠底与色条，不承载文字对比度**（`.notify-row` 文字走 `--text-primary`）→ 保留。
- `--tooltip-bg: rgba(0, 0, 0, 0.8)` / `--tooltip-color: #ffffff`：**token 定义本体**（dark/nord 已覆写为主题面色），非误用 → 保留。

### 12.9 UI 一致性与交互口径

> 本节固化「界面一致性」口径。新增/修改 UI 一律遵循；与此前分散决定冲突时以本节为准。

**① 对话框（`DialogShell`）**
- **默认高度**：未显式传 `height` 时取 `min(80vh, 640px)`（`types/dialog.js` 的 `DefaultDialogOptions`），调用方 `options.height` 可覆盖 → 消除「打开时高度跳动」。
  - **例外**：`MessageBox`（确认/输入）与 `AskUserContent`（问题数不定）显式 `height:'auto'`，保持内容自适应。
- **三段式结构**：头部固定 + `.dialog-body` 内滚动 + **footer 固定在 `.dialog-body` 之外**（不随 body 滚动）；`DialogShell` 提供**可选 `footer` 具名插槽**（缺省不渲染任何多余 DOM）。
- **定向关闭通道**：订阅桥事件 `dialog-close`，payload `{dialogId?}` —— 有 `dialogId` 则**仅关该弹窗**（按 `dialogId` 过滤），无则关**当前最上层（唯一）弹窗**（外部 `mq.emit` 可据此关掉编辑弹窗）；挂载期以模块级单例 `openDialogs` 登记 `dialogId → zIndex` 以定位最上层，卸载时退订并注销。**其余折叠 / 最小化 / 最大化 / 还原均为组件内部方法（`collapse`/`minimize`/`maximize`/`restore`），不经 MQ。**
- **有表格的弹窗** → 表格**自身内滚**，减少整体滚动。
- **页签内容与页签分割线留 0.5em 间距**（`Tabs` 的 body `padding-top: 0.5em`；如 MCP 编辑弹窗三页签）。

**② 按钮**
- **保存 / 取消 / 确定等操作按钮统一在弹窗底部按钮区，固定、不随内容滚动**（三段式：顶栏固定 / 中部滚动 / 底栏固定；底栏须为滚动容器的**兄弟节点**且 `flex-shrink: 0`）。
  - 覆盖编辑类（`EditLLMDialog` / `EditMCPDialog` / `TextEditDialog` / `ScenarioEditDialog`）与 **confirm / 选择类**（`MessageBox` / `DirPickerDialog`）；无滚动、内容自适应类（`MessageBox`、`AskUserContent`）底栏同样固定在内容之外。
- **主操作文案统一「确定」**（`common.ok`）；不再引用 `common.confirm` / `dialog.ok` 等重复语义键。
- **恢复类按钮统一「重置」**（配置/设置页与弹窗）；**PrimitivePanel 保留「恢复默认」**（资产语义）。
- 编辑类弹窗底栏的**次要项固定最左**（如 MCP 的「启用」：`margin-right: auto`），主操作（保存）保持**最右**。

**③ 帮助信息载体**
- `label` 旁的**术语/取值解释** → `?` 图标 + `Tooltip` 组件（`Icon name="help"`）。
- **非 `?` 元素**（chip / Switch / 按钮 / 状态徽标）的补充信息 → 原生 `title`。
- `?` 图标**必须**用 `Tooltip` 组件，不得用原生 `title` 替代。
- **Tooltip 长文案必须折行**：popper 用 `white-space: normal` + `width: max-content` + `max-width: 360px` + `overflow-wrap: anywhere` —— 短文案仍单行，长文案折行，超长无空白串（路径 / URL）强制断行（**不得**用 `white-space: nowrap`）。

**④ 字号与字色**
- **区块标题统一 14px / 600**；正文按内容（13/14px）；**说明文字统一 12px + `--fg-secondary`**。
- 文字按语义取色：`--fg-primary`（主要）/ `--fg-important`（重要）/ `--fg-secondary`（次要 = 说明文字与页签非激活）/ `--fg-disabled`（无效）/ `--fg-link`（可点击）——定义与实测对比度见 §12.8。

**⑤ 表格与滚动**
- **统一用自研 `Table` 组件**（`components/ui/Table.vue`）；**不再新增原生 `<table>`**（`SecurityConfig` / `ContextConfig` 已迁移）。
- 页面骨架 = `height:100%; display:flex; flex-direction:column` → 头部固定 + 正文 `flex:1; overflow:auto; min-height:0`。
- **长表单页**（如上下文管理）允许正文整体滚一次；其中**列表/记录区自身内滚**，避免"滚到底才看到下一区块"。
- **有表格的页**：横向滚动由**页面正文区**承担 —— `Table` 的 wrapper 置 `overflow-x: visible`、正文区 `overflow: auto`、卡片 `min-width: max-content` 随表格变宽；**滚动条始终贴 preview 区边缘**（纵向右侧、横向底部），不会出现"要滚到表格底边才见到横向滚动条"。**该范式应用于工具配置 / 工具沙箱 / 上下文管理 / 安全 / LLM 五页**（`ContextConfig` 压缩内容记录列表仍自身内滚、`SecurityConfig` 表头仍 sticky）。
- 表格内**长文本单元格**按**像素宽度**截断（如工具配置页「工具内容介绍」`max-width: 200px` + 省略号），**不按字符数**截断。

**⑥ 布局与交互**
- **任务区（下区）常显**（**2026-10-16 用户裁决**：三区显隐能力与 UI 入口整体移除；原「`taskOpen` 默认 `false`、首屏减负」口径已废止）→ 启动即渲染，与 chat / filetree 同口径。**已知可见变化 = 首屏多出任务区高度**。「查看任务详情」等既有发送方仍发 `tasks-toggle`，收到即 no-op。
- ~~顶部「任务 / 文件树 / 聊天」三开关~~ **已移除（2026-10-16 用户裁决）**：三区常显、无显隐入口（三主题保留定义，不驱动显隐）。左侧工具栏右区 `tb-btn` 顺序随之变为 **0=theme 1=lang 2=sessions**（`test_toolbar` 依赖该顺序）。

**⑦ 反馈**
- **保存类操作必须有可见提示**（成功与失败都要）。
- **失败必须有提示**（如截图失败，原为 `console.warn` 静默 → 已改 `message.error`）。
- 纯成功且非"保存"语义的操作（切场景 / 切模型 / 新建会话等）**不加**提示。
- **不加**首次使用引导：本工具面向已有 IDE（IDEA / Eclipse 等）使用经验的开发者。

### 12.10 状态栏（`views/statusbar/StatusBar.vue` / 默认 `layout/bar-views/*`）

> 用户口径：「statusbar 可否显示 当前索引和 codebase 的状态？未启用，完成，N/total」。
> 状态栏自左向右 = 记忆总 token 数（仅记忆库启用时显示）· **索引状态区**（恒显示）· DevTools 图标 · 撑开 · 语言切换（`LangSwitcher`）。
> **STB-1（2026-10-17）**：记忆提取 / 上下文压缩 / 代码索引（含 AST）**各一个长任务状态 view**，点击 → preview 详情页；长任务**不再用 toast**。

**长任务状态条目（STB-1，默认 free 形态 = `layout/bar-views/`）**

| 条目（viewKey） | 数据源（零新增主题 / 键） | 空闲 | 排队 / 进行中 | 完成 / 失败 | 点击 |
|---|---|---|---|---|---|
| `memory-status`（记忆提取） | 既有通知面 `tool-notify`：`memory-{queued,start,done}` + `plugin-failure{plugin:memory}`；`memory.enabled` 仅用于「未启用」文案 | `记忆 · 未启用`（记忆库关闭）/ `记忆 · 空闲` | `记忆沉淀排队中` / `记忆沉淀中…`（进行中色） | `记忆沉淀完成` / `记忆 · 失败`（错误色，title = 错误原文） | `open-view{status-memory}` → preview「记忆提取详情」 |
| `compress-status`（上下文压缩） | 同上：`compress-{queued,start,done}` + `plugin-failure{plugin:compress}` | `压缩 · 空闲` | `压缩排队中` / `压缩中…`（进行中色） | `压缩完成` / `压缩 · 失败`（错误色） | `open-view{status-compress}` → preview「上下文压缩详情」 |
| `index-badge`（代码索引；codegraph + vfts 两枚徽标同一详情页） | prj 既有键 `enable-codegraph` / `codegraph.status` / `enable-vfts` / `vfts.status` | `未启用` / `未初始化` | `索引中 N/total`（`progressTotal>0`） | `完成` / `失败`（错误色） | `open-view{status-index}` → preview「代码索引详情」 |

- **条目恒可见**（「每种任务都有一个状态 view」）：空闲 / 未启用也渲染（中性色 `--fg-disabled`），不因空闲而消失。
- **失败不静默**：`plugin-failure`（既有 notice）落到条目错误色 + 详情页错误文本；**worker 失败后仍会补发 `*-done` → 前端「失败优先」**（`reduceTask`：error 态忽略后续 done），避免失败被「完成」洗掉。
- **AST 口径**：引擎 `codegraph.status.phase` 仅 `configure|index`（插件回写），**未单独暴露 AST 阶段** → AST 并入代码索引条目 / 详情页「索引阶段」说明，**不编造独立进度**。
- **去 toast（STB-1）**：记忆 / 压缩的进度 / 完成 / 失败不再走 `ChatPanel.sendNotify`（`message.info`）；`tool-notify` 长任务取值在 `sessionGuard` **之前**归并进 `useQueueStatus`（状态栏是项目级，跨会话可见），命中即 `return`（仅压缩进度继续驱动会话内联指示条）。其它插件的 `plugin-failure` 提示链路保持不变。
- **零新增消息面**：仅消费既有 `tool-notify` 取值（`memory-*` / `compress-*` / `plugin-failure`）与既有 prj 键；命令复用 `open-view{viewKey,target}` → `preview-tab-open{kind}`。

**长任务详情页（preview 功能页，`views/status/*`）**

- `status-memory` / `status-compress`：当前状态（含自动隐藏后的「空闲/未启用」）、最近一次任务（状态 / 开始 / 最近更新 / 会话 / 轮次 / 错误）、**真实 notice 状态时间线**（有界 20 条；数据只有 notice → **不伪造队列条目**）。「去设置」→ `project-config-open{tab:'context'}`。
- `status-index`：codegraph / vfts 两区块 —— 状态、`phase`（配置 / 索引阶段）、进度 `done/total` + 进度条、`indexedFiles` / `indexedSymbols` / `chunkCount`、`lastIndexedAt`（UnixNano → 本地时间）、`loaded`、`err` / `message`。「去设置」→ `project-config-open{tab:'codegraph'}`（Vfts 区块另有 `tab:'vfts'`）。
- **三页均注册进** `layout/view-definitions.js`（`status-*`，`designable:false`）+ `layout/views.registry.js`（组件映射）+ `CodeView.specialComponents`（功能页页签 / 标题 i18n `statusBar.detail.*`）。

**索引状态区（`.sb-section.sb-index`）**：内含 **两枚徽标**（`.sb-idx[data-engine="codegraph"|"vfts"]`，**图标 + 状态文本**），紧凑单行，**恒显示**。图标取自既有图标集（codegraph = `collection`，vfts = `search`）。
- **数据源**（全部**既有** prj-config 键，**零新增消息面**）：`enable-codegraph` + `codegraph.status`；`enable-vfts` + `vfts.status`。取数 = `getAllConfig()`（`data-prj-config-list`）；刷新 = 既有广播 `onDataRefresh('prj-config', reload)`；显示值用 `computed` 派生（**禁 watch/watchEffect**）。
- **状态映射**（纯函数 `src/frontend/src/utils/indexStatus.js`）：

  | 条件 | 文案 | 字色 token |
  |---|---|---|
  | 开关非 `"true"` 或无键 | 未启用 | `--fg-disabled` |
  | 开关为 `"true"` 且 `state ∈ {"" , not_initialized}` 且无进度/阶段 | 未初始化 | `--fg-disabled` |
  | `state == "indexing"`（或状态未落定但已见 `progressTotal > 0` / `phase`） | 索引中；`progressTotal > 0` 时附 `N/total`（如 `索引中 320/1024`） | `--fg-secondary` |
  | `state == "ready"` | 完成 | `--text-primary` |
  | `state == "error"` | 失败 | `--fg-important` |

  > 「索引中」取 `--fg-secondary`（进行中的中性次要色）：状态栏的 `--accent` 留给可交互/选中语义（如记忆总量 hover），避免「进行中」与「可点击」混淆。
- **悬停**：徽标**原生 `title`**（**不再用 `Tooltip` 组件**）——codegraph = 「点击查看索引详情：codegraph」、vfts = 「点击查看索引详情：Vfts 全文索引」（i18n 键 `statusBar.idx_open_codegraph` / `idx_open_vfts`，zh/en 齐备；STB-1 改文案指向详情页）。原 Tooltip 多行明细**已整体移除**。
- **点击**（STB-1 口径变更）：每枚徽标 → 统一命令 `open-view{viewKey:'status-index', target:'auto'}`（`layout/statusDetail.js`）→ 既有 `preview-tab-open{kind:'status-index'}` → preview 打开「代码索引详情」；**不再直接跳项目配置**。配置仍可从详情页「去设置」到达：`projectConfigOpen{tab:'codegraph'}` / `{tab:'vfts'}` → `MainLayout.handleOpenProjectConfig` 透传 → `ProjectConfig` 激活对应页签（缺省/非法 tab → 默认页签 `security`）。**零新增 MQ 主题**。
- **异常兜底**：状态 JSON 解析失败/缺键 → 视为「未启用 / 未初始化」，静默（不抛错、不 `console.error`）。
- **关联测试**：前端守卫 `test/statusbarIndexStatus.test.js`（映射全状态 + 4 键 + 既有广播 + 徽标 = 图标+文本/无 Tooltip + 原生 title + **点击走 open-view 详情** + 零新增主题 + i18n 齐备）· `test/statusTasks.test.js`（长任务条目状态映射 / 点击 open-view / 详情页注册与「去设置」/ 去 toast / 详情字段）· `test/layoutAdapter.test.js`（注册表新增 `memory-status` / `compress-status` / `status-*`）；L4 `test_statusbar.py::SB6/SB7/SB8`（真机：索引中 N-total / 完成 / 失败 → 点击开**索引详情**（错误 + 进度 + 去设置 → CodeGraph/Vfts 页签）；记忆/压缩条目（未启用/空闲基线 → 进行中 → 点击开详情 → 失败错误文本 → 失败优先于 done → 完成）；截图存 `E:\TEMP\ck-l4-visual\stb\`）。

### 12.11 文件树：被索引排除条目灰显

> 用户口径：「如果设置了 gitignored，目录树显示时，判断是否是排除项目。如果是，则文本颜色变成灰色。」方案 = **后端判定**（只读面 `data-index-ignored`）、位置 = 文件树（目录树）。功能点见 [32 §FT-016](../30-function-points/32-文件树与文件操作.md)。

- **判据来源** = 只读数据面 `data-index-ignored`（[61 §3.6](../60-reference/61-消息一览.md)）：入参 = 当前**已加载节点**的 **workdir 相对路径**（`/` 分隔，一次批量、去重）；应答 = `{codegraph:{enabled,ignored}, vfts:{enabled,ignored}, truncated?}`。判定口径与引擎实际索引范围一致（祖先目录剪枝 / `!` 同层反选 / 内置强制排除）。前端封装 = `src/frontend/src/utils/indexIgnored.js`（纯逻辑 `toWorkdirRelPath` / `ignoredSetFromResult` + 批量请求 `fetchIndexIgnored`；`emit` 可注入便于单测）。
- **灰显规则**：仅统计 `enabled=true` 的引擎，两引擎 `ignored` 取**并集**（被任一已启用引擎排除即灰显）；引擎未启用 → 其 `ignored` 恒空 → **不灰**。
- **样式 token**：命中节点在 `TreeNode.vue` 的 `.tree-row` 上加语义类 **`is-ignored`**，**仅文本颜色** = `var(--fg-disabled)`（`--fg-disabled` 定义见 §12.8）。**只改颜色**——点击 / 双击 / 右键 / 拖拽等交互与布局均不变；另加原生 `title`（i18n `fileTree.index_ignored`：zh「已排除：不会参与索引」/ en「Excluded: not indexed」）。
- **判定时机**：树数据到手 / 每层目录加载（`loadDirChildren`）/ `filesys.changed` 合并后（新增节点补判）。**刷新** = 既有广播 `onDataRefresh('prj-config', …)`（排除规则 / 引擎开关变化）→ 清缓存**重判已加载节点**（已灰的可恢复不灰）。**禁 `watch` / `watchEffect`**。
- **请求克制与降级**：同一批路径只请求一次（单飞 + 已判定缓存）；`truncated=true` 时未判定节点不灰显、不缓存、不报错；请求失败 / 主题不可用 / 应答异常 → **静默降级为不灰显**。**零新增 MQ 主题**（只新增 `data-index-ignored` 一个只读面，见 [61 §3.6](../60-reference/61-消息一览.md)）。
- **关联测试**：前端守卫 `test/fileTreeIndexIgnored.test.js`（纯逻辑 + 请求/降级/topic/去重 + FileTree 判定时机/刷新/禁 watch + TreeNode `is-ignored`/`--fg-disabled`/title/交互保留 + 零新增主题 + i18n）；L4 `run_filetree_ignored.py`（真机：`.gitignore` 命中条目灰显（含子项祖先剪枝）+ 反选/无命中不灰 + 引擎未启用全不灰对照组）。

### 12.12 项目配置「文件历史」页签（`views/settings/HistoryConfig.vue`）

> 页签 = 项目配置页签 **`history`**（[61 §6.1 前端本地事件](../60-reference/61-消息一览.md) `project-config-open{tab?}` 取值之一）。**页签名由「自动提交」改名「文件历史」**（i18n `projectConfig.history`：zh「文件历史」/ en「File History」；旧「自动提交」语义已废弃）。后端语义见 [28-plugins §3.2](28-plugins.md)，配置键见 [64 §4](../60-reference/64-配置项一览.md)。

分三区块（自上而下）：

- **① 启用开关**（`history.enabled`）：单 `Switch`；**缺省关闭**（仅显式 `"true"` 视为开）；**开启前 `confirm` 二次确认**（说明每次工具调用前会打 checkpoint、大工程可能影响效率）；无 git 时 **disable + 原因提示**（`historyConfig.no_git_installed` / `no_git_repo`，判据 = `gui.vcs.info` 的 `gitInstalled` / `git`）。保存即生效（订阅既有 `data-prj-config-refresh`）。
- **② 保留策略**：两个数字输入 —— **保留个数**（`history.checkpoint_keep`，默认 500）/ **保留天数**（`history.checkpoint_ttl_days`，默认 7，锚点 = 链上最新检查点时间）；共用一个【保存】按钮（**正整数校验**，非法不写库并显式报错）；任一超限即修剪（后端口径）。
- **③ 检查点时间轴（只读，**按当前会话**）**：
  - **当前会话** = 活动会话（既有 `data-session-active-get`）→ `chainSlug`（与后端同口径）= 链 slug；只读**该会话**的键。
  - **状态条**（`data-history-status`，读 `history.status.<slug>`）：**模式**（`active` 正常 / `fused` 已熔断放行 / `off` 未启用）· 检查点数 · 占用体积（人类可读）· 最近打点时间 · 最近耗时 · 失败次数 · 有未打点变更时附「有未打点变更」标记 · 最近错误非空时红字一行。
  - **列表**（`data-history-timeline`，读 `history.timeline.<slug>`）：列 = **序号**（相对编号，最新 `-1`，由数组索引派生、顺序不反转）/ 时间 / 工具 / 来源会话 / 文件数 / 增删（`+n`/`−m`）；**最新在前、≤200 条**；空态可区分「未启用」（`empty_disabled`）与「暂无检查点」（`empty_none`）。
  - **【清空本会话历史】**按钮（`data-history-clear`，列表非空才可用）：**二次确认**后写 `history.clear` = **JSON `{"ts","session":"<当前会话 slug>"}`** → 后端**只清该会话的链**（`refs/chonkpilot/<slug>`；其它会话链保留）；成功给可见反馈。
- **只读边界（有意为之）**：检查点时间轴**不提供手动恢复、不做 diff 展开**（恢复/查看差异属 LLM 侧 4 个 `history_*` 工具的职责）。
- **数据来源与刷新**：`history.enabled`/`keep`/`ttl`/`clear` 为 **prj 键**、`history.status.<slug>` / `history.timeline.<slug>` 为**会话级内部键（落 prjusr）**，统一经既有 `data-prj-config-list`（合并 prjusr）读取、`data-prj-config-save` 写入；刷新走统一机制 `usePrjConfigRefresh`（4 精确键 + **2 会话级前缀** `history.status.` / `history.timeline.`；按键过滤 + 突发合并 + 保存期跳过）；派生用 `computed`（**禁 `watch`/`watchEffect`**）；解析失败静默降级（不刷 `console.error`）。**零新增 MQ 主题**（`event-names.js` 无 history 通道）。
- **关联测试**：前端守卫 `test/historyCheckpoint.test.js`（纯逻辑 `utils/historyTimeline.js`：JSON 解析兜底 / 相对编号 `-1` / `active|fused|off` 归一 / 体积·时间格式化 / 默认 500·7 / **`chainSlug` 与会话级键名** · 源码守卫：两设置项键名与写库、**正整数校验先于写库**、**按当前会话读 status/timeline（活动会话 + 前缀订阅）**、禁 watch、`history.clear` 写 **JSON `{ts,session}`** + 二次确认、零新增主题、i18n zh/en 键集一致且「保留口径 / 不向分支提交 / 效率 / 工具调用前打点 / **本会话清空**」文案齐备、页签名「文件历史」）；L4 `run_hist_git.py`（真机产物断言见 [51 §2](../50-testing/51-FP与测试映射.md)：H1/H4/H6）。

### 12.14 左侧资源面板「扩展」页与四级（`views/filetree/ExplorerPane.vue` + `views/extensions/ExtensionsPane.vue`）

> 用户口径：分段改 **`项目 / 会话 / 记忆 / 扩展`**（删「知识库」「工具」两个一级分段；「项目记忆」文案改「记忆」）；新增「扩展」页 = 顶部 **5 子 tab**（知识/技能/工具/命令/智能体）+ 右侧**【级别】**popup；**四级都可写**（撤系统级只读）。

- **分段栏（4 段）**：`项目 | 会话 | 记忆 | 扩展`（`mode` = `project|sessions|memory|extensions`）；`v-show` 同显 4 体；「刷新」图标在 `memory`/`extensions` 模式显示（扩展页 → `ExtensionsPane.reload()`）。`filetree-mode-toggle` 休眠态改 `project ↔ extensions`。
- **扩展页（`ExtensionsPane.vue`）**：`SUBTABS` = 知识(`resource`) / 技能(`skill`) / 工具(`tool`) / 命令(`prompt`) / 智能体(`agent`)（顺序固定）；内部 `v-show`/`key` 渲染 **复用** `views/filetree/KnowledgeTree.vue`（`scope=子 tab key`、`kinds=[单类型]`）。
- **级别（四级）**：右侧【级别】按钮 → `Popover` 下拉（系统/用户/项目/项目私有，`fileTree.kb_level_*`）；**级别状态由扩展页统一持有并经 `props.level` 下发**（`KnowledgeTree` 只读 `level`，内联级别按钮组与自持 `kbLevel` **已移除**）。切换级别/子 tab 经 `:key="subTab + '-' + level"` 重挂载树（**不用 watch**）。
- **四级均可写**：撤 `isKbReadonly`/`kb_readonly`（原「系统级只读」作废）；拖拽移动仍限当前级别根内同级。
- **智能体子 tab**：`*.agent.md` 编辑走 `PrimitivePanel`（token=agent → **复用** `views/scenario/AgentEditor.vue`，`showCopy=false`；保存 `data-knowledge-save`）。**「启用工具过滤」开关**：开关态为**前端本地、不持久化**（后端判据 = `tools` 非空即白名单）—— 载入按 `tools` 反推回填（单源 `utils/agentToolFilter.filterToolsLoadPatch`，与场景编辑同口径）、`update:agent` 回写；**无 tools 的智能体勾选后须保持开并展开候选**（`PrimitivePanel` 不再由 `tools.length>0` 派生）。
- **场景编辑页的子 agent = 可编辑引用**：`views/scenario/ScenarioEditDialog.vue` 对子 agent（唯一形态 = ref）**复用** `AgentEditor` 并可编辑；选中时经 `data-knowledge-read` 读被引 `agents/*.agent.md` 正文，点【保存】经 `data-knowledge-save` 写回该文件（v2 契约：`# 基本信息`+`# 摘要`+`# 委托`+`# 正文`），**`scenario.json.agents` 里的 ref 不变**；ref 前缀 → 绝对路径解析单源 `utils/agentRef.js`。四级 capability 根均可写 → 不复制到项目级（见 [37 SCEN-003](../30-function-points/37-场景.md)）。
- **消息面**：**主题零新增** —— 子 tab 复用 `filetree-mode-select`（payload 增**可选** `ext`）；级别复用 `kb-level-select{kind}`（语义改「扩展页统一持有」）。二者均为**前端内部**事件（未登记 [61 §6.1](../60-reference/61-消息一览.md)）。
- **树形维护能力（🆕 2026-10-15）**：**单类型**子 tab 树根 = **该类型目录本身**（如 `<capRoot>/resources`，**不再多显示一层**类型目录；根节点 `title` = **真实绝对路径**）。**目录/文件右键**增【在资源管理器显示】（`gui.reveal`）；**文件**增【打开】（系统默认程序）/【打开方式】（对话框）。**知识子 tab 目录右键**增【导入】（多选文件，`data-knowledge-import`）；**知识子 tab 新建**先**选类型**（「知识（md 契约）」/「其他（任意类型文件）」）。「其他类型文件」（后端 `type='file'`，如 `config.json`）**单击仅选中、双击用系统默认程序打开**（应用内不预览——见 [41 D-60](file:///e:/BizWorks/chonkpilot/docs/spec/40-roadmap/41-未决项登记.md)）。**删除文件/目录后**经 `filesys.changed` **本地广播**（`mq.emitRemote`，**仅本地分发、不发后端**）通知预览区关闭对应页签（文件自身 / 目录下全部）；各类型新建入口加「名称即文件名、创建后不可改」提示。**原语编辑面板**（`PrimitivePanel`）顶部对**当前编辑的契约 md** 同样提供 **在资源管理器中显示 / 打开 / 打开方式** 三入口（复用 `gui.reveal` / `gui.open-with`）。见 [42 §2.30](file:///e:/BizWorks/chonkpilot/docs/spec/40-roadmap/42-决策记录.md)。
- **原语「基本信息」按类型表单（🆕 2026-10-15）**：`PrimitivePanel` 的「基本信息」页签由**通用 KV 网格**改为**按类型字段表单**（用户口径：meta「有些值不能乱填」）——字段 = 该类型契约 meta 键，控件按值类型映射：枚举 → `Select`（候选受契约约束 + 「（未设置）」空项）、布尔 → `Switch`、数字 → `Input type=number`、文本/路径 → `Input`；字段定义与值转换 = 纯模块 `utils/metaForm.js`（`META_FIELDS` / `metaFieldsOf` / `isSchemaKey` / `fieldValueOf` / `fieldToMetaOf`）。**`name` 只读**（= 文件名，首行固定）；**skill / prompt 无专属键**（提示文案）；**agent 不渲染本表单**（整体走 `AgentEditor`）。表单未覆盖的键（用户自建）保留在下方**「自定义键」KV 区**（可增删改、原样回写）→ load → form → save **往返保真**；`form.metaRows` 仍是**唯一真源**（字段读 `metaMap()`、写 `setMeta()`）。**锁定键语义订正**：锁定键（`name` + 类型默认键）**值可清空、键不可删**（值空 → 非锁定键删行、锁定键留行清值）。**契约/消息面零变更**（仍走 `data-knowledge-save`，落盘由服务端 `contractmd.Build` 序列化）。见 [39 KB-003](../30-function-points/39-知识库与原语.md) · [42 §2.30](file:///e:/BizWorks/chonkpilot/docs/spec/40-roadmap/42-决策记录.md)。
- **智能体编辑页签（🆕 2026-10-15）**：`AgentEditor` 的「参考」拆 **技能 / 资源** 两个页签（**表格式**：新增 + 弹窗**多选**，按 **MCP 分类分组**可勾选）；提示词页签加 **「AI 编写」**（输入简单角色 → 走 `gui.prompt-optimise` 流式撰写）；**skill 关联工具默认勾选且不可删**（`filterTools` 未启用时 = 全部工具可用）。场景编辑页的**主 agent** 同样提供 skills/resources 选择（`agents[]` 扩字段，见 [25 §3.1](25-mcp-server.md)）。
- **关联测试**：前端守卫 `src/frontend/test/extensionsPane.test.js`（4 段位置/5 子 tab 顺序与 kinds/级别 popup/四级可写/复用/零裸主题/agent token/i18n）+ `test/scenarioAgentsRef.test.js`（智能体编辑器复用）+ `test/sessionNavCopy.test.js`（导航顺序）+ `test/primitiveMetaForm.test.js`（基本信息按类型表单：字段清单对齐契约 / 控件类型 / 值转换往返 / 自定义键与 name 只读落点守卫 / zh-en 文案全量对齐）。L4 `run_explore_kb.py` / `run_config_ui.py` / `run_ui_regressions.py` 按新 UI（「扩展」页 + 基本信息表单）同步（**需新构建后实跑**）。

### 12.15 提示词「变量插入」统一组件（OP-12）

> 目标：把散落的提示词编辑处（场景 agent / 记忆沉淀提示词 / 总结提示词 / 技能原语描述与正文 / 场景向导）统一为「**变量插入 + 优化**」组件；变量清单**由后端提供**（前端不硬编码）。

- **后端只读面 `gui.prompt-vars`**（[61 §1](../60-reference/61-消息一览.md)；`src/lib/gui/bridge/promptvars.go`）：返回分组目录 `{groups:[{id,label,items:[{key,desc,dslOnly}]}]}`；**单一数据源 = 后端常量** —— `toolchain`（java/python/node/go/rust/c/chrome，逐项取自工具链探测候选 `toolchainCandidates`）· `path`（`exeDir`/`userDir`/`dataDir`/`workDir`）· `env`（`CHONKPILOT_*`：WORKDIR/DATADIR/TEMPDIR/EXEDIR/PROJECT，**`dslOnly=true`**）。`key` = 占位符整串（含 `{{ }}`，直接插入文本）；无副作用、不落库、与实例无关。
- **前端**：composable `composables/usePromptVariables.js`（`usePromptVariables` 拉取并**缓存**清单 + `visibleGroups(dsl)` 过滤 `dslOnly`；`useVariableInsert` 在目标 textarea 光标处插入并在下一帧恢复光标，插入算法单源 `utils/promptInsert.js`）+ 统一组件 `components/common/PromptVariablesButton.vue`（按钮 → `Popover` 分组列表 → 每项 `Tooltip` 显示 `desc` → 点击 `emit('insert', key)`）。**复用既有【优化】**（`gui.prompt-optimise`，`api/config.optimizeAgentPrompt`）——本组件只负责「变量插入」，不新增优化通道。
- **挂载点（全清单）**：`components/common/TextEditDialog.vue`（`variables` 开关；上下文管理页「总结提示词」「记忆沉淀提示词」两处开启）· `views/scenario/AgentEditor.vue`（提示词页签工具条）· `views/knowledge/PrimitivePanel.vue`（`description` + `content` 两页签）· `views/scenario/ScenarioWizardDialog.vue`（agent 提示词 + 记忆沉淀提示词）。
- **`{{env.*}}` 仅 DSL/脚本类编辑器列出**（`dslOnly`）：本仓前端暂无此类编辑器 → 按 `dslOnly` **预留**，不在上述编辑器显示（`PromptVariablesButton` 的 `dsl` 开关默认 `false`）。
- **i18n**：`scenario.insert_variable`（按钮文案）/ `scenario.available_vars`（popup 标题）——原为**无实现残留**（仅 i18n 键、无引用），现由本组件使用。
- **关联测试**：`src/lib/gui/bridge/promptvars_test.go`（返回形状 + 分派 `guiDo`）+ 前端 `src/frontend/test/promptVariablesCompress.test.js`（插入算法 / 挂载点 / i18n / 无 watch）。

### 12.16 设置页保存交互口径

> 用户口径（原话）：「**统一加保存**。但是**一栏页面不需要**，**对话框添保存后即为增加或者修改**。」本节固化「哪些设置页有【保存】、哪些没有」的判定（承接 §12.9 ⑦ 反馈口径；覆盖 CFG-015 名单）。

- **列表型（「一栏」）页面 → 不加【保存】**：页面的增 / 删 / 改走**对话框**（或即改即存的开关），对话框底栏的【保存】= **新增 / 修改的确认**（点击即落库，见 §12.9 ②）。
  - `views/config/SettingsMCPPage.vue`（MCP 配置，server 清单）+ `EditMCPDialog.vue`（底栏【保存】）。
  - `views/config/SettingsLLMPage.vue` 的**「一览」页签**（provider 清单）+ `EditLLMDialog.vue`（底栏【保存】）。
- **表单型页面 → 页签右上角加【保存】**：编辑只改本地**待保存态**（显示「未保存」，`useUnsavedMark`），点【保存】才落库；**无改动时保存按钮禁用**；成功 / 失败反馈统一走 `settingsFeedback`（`savedText(t, APPLY_INSTANT|APPLY_RESTART)` / `saveFailedText`）；**禁 `watch` / `watchEffect`**。
  - `views/config/SettingsParamsPage.vue`（参数设置）：用户 / 项目两页签各一【保存】（`data-params-save-user` / `-project`），均**即时生效**（`APPLY_INSTANT`）；数值项前置校验非法**不写库** + 内联报错。
  - `views/config/SettingsPathsPage.vue`（路径设置）：用户页签【保存】**需重启**（`APPLY_RESTART`）；项目页签【保存】**即时生效**（`APPLY_INSTANT`）；「选择可执行文件」只回填待保存态。
  - `views/config/SettingsLLMPage.vue` 的**「默认模型」页签**（主对话 + 各子系统共 6 个下拉）：页签右上角一【保存】（`data-llm-save-defaults`），**即时生效**（`APPLY_INSTANT`）；编辑只改本地待保存态，空串 = 跟随默认（回落 `defaultLLM`）。
  - 三页「重置」仍为**显式即时动作**（删本层键回落默认），完成后重算 dirty（不影响其它未保存编辑）。
- **判定口诀**：页面主体是「一张清单 + 对话框增改」→ 列表型（无【保存】）；主体是「若干表单项」→ 表单型（有【保存】）。
- **关联测试**：`src/frontend/test/uxBatch2Settings.test.js` · `uxBatch2Closure.test.js`（保存按钮存在 / 按 dirty 禁用 / 不再失焦即存 / 反馈标注）· `subsystemLLMDefaults.test.js`（默认模型页签显式保存 / dirty 禁用 / loading 防重 / 无 watch）。
- `SettingsLLMPage.vue` 为**列表 + 表单混合**：**「一览」页签 = 一栏**（provider 清单，增改走 `EditLLMDialog`）→ **不加【保存】**；**「默认模型」页签 = 表单**（主对话 + 各子系统下拉）→ **加【保存】**（页签右上角，同参数 / 路径页）。

### 12.17 Vfts 索引页「系统级词典」+【重新索引】按钮（`views/settings/VftsConfig.vue` + `composables/useVftsDict.js`）

> 分词器固定 `jieba`（[2B-vfts §4.1](2B-vfts.md)）；词典**只有系统级一份**（全机共用、无项目/用户级覆盖）。本节固化该页两个新增入口的交互口径。

- **工具栏【重新索引】**（`projectConfig.index_reindex`）：`mq.emit('vfts.reindex')` → 插件后台全量重建，**立即回执** `{ok, started}` 后给一次 `savedText` 反馈；重建期间按钮 `:disabled="reindexing"`。**明确提示「分词器 / 词典变更必须重新索引」**（词典区块 hint，`projectConfig.dict_hint`）；重建时全工作区重扫，进度经既有 `vfts.status`（`indexing` + `progressDone/progressTotal`）回显（本页状态行，**零新增消息面**）。
- **「系统级词典」区块**（`projectConfig.dict_section`）：进页 `loadDict()` → `vfts.dict.get` 回填 `dict_dir` / `base_dicts`（内嵌基础词典，只读）/ `user_dict` / `word_count`；`Textarea` 编辑自定义词（每行一词）→【保存自定义词】`vfts.dict.set`（**后端同时自动调度强制重建**），成功后一次 `savedText` 反馈。编辑 / 读取失败走 `saveFailedText`。
- **实现约束**：状态逻辑封装在 `useVftsDict` composable；**禁 `watch`/`watchEffect`**；`:style` 数字带 `'px'`（本页无数字样式）；i18n（zh/en）齐备。
- **消息面**：`vfts.dict.get` / `vfts.dict.set` / `vfts.reindex`（§1.1 点分相对主题；[61 §1.1](../60-reference/61-消息一览.md)）。
- **关联测试**：前端守卫 `src/frontend/test/vftsDictReindex.test.js`（composable 用三主题常量 / 页面区块与按钮 / msgkeys 生成物 / i18n 齐备 / 无 watch）。

### 12.18 子聊天（单聊）前端：状态条 · 会话列表子节点 · 启动提醒（SUBCHAT-2 阶段 C）

> 设计依据 = 单聊会话设计方案 §3.4 / §3.8（F1–F10 逐条）；消息面 = [61 §4.2/§4.3](../60-reference/61-消息一览.md)（`subchat-opened` / `subchat-list` / `subchat-finish` / `subchat-state`，**载荷零改动**）。功能点见 [33-对话 CHAT-024](../30-function-points/33-对话.md) · [31-窗口与布局 WIN-023](../30-function-points/31-窗口与布局.md)。

**组件 / 模块落点**

| 落点 | 职责 |
|---|---|
| `composables/useSubChats.js` | 接线层：`subchat-list` 取数（`parent_id` 有值 = 该父会话全部含 `finished`；缺省 = 全局未结束）· `subchat-finish` 收口 · `openSubChatWindow`（`gui.window.open-chat` 幂等）· F10 启动提醒对话框（`dialog.show`） |
| `utils/subchat.js` | **叶子模块**（无 import / 无副作用）：`kind` 判定（`sub_chat`/`group_chat`）· 三态常量 · 可见性判据（`lifecycle ∈ {chatting, ready}`）· 文案（`purpose` 优先）与状态标签 i18n 键 |
| `views/chat/SubChatBar.vue` | 输入框**上方常驻**状态条（默认折叠一条「N 个子聊天进行中」+ 展开 ≤15 条可滚动；每条「正在 `<purpose>`」+「继续」+「强制结束」）；子聊天窗口内另承载常驻「结束」（`ready` 点亮） |
| `views/chat/SubChatStartupDialog.vue` | F10 启动提醒列表内容（每条 = `purpose` + 父会话标题 + 状态；点一条 → 开窗并关框） |

**约定**

- **订阅（零 `watch`）**：`SubChatBar` 订 `subchat-opened` / `subchat-state` / `gui.window.closed` / `data-session-title-changed` 触发刷新；`ChatPanel` 订 `subchat-opened`（F2 自动开窗）与 `subchat-state`（F7 点亮 / F9 只读）。会话切换用 `:key="currentSessionId"` 重挂替代 `watch`。
- **状态条淡色**：组件级声明 `--subchat-bg: var(--accent-bg)` + `--subchat-border: color-mix(in srgb, var(--accent) 30%, transparent)`（§12.8 的组件级覆盖钩子口径；`var()` 内**不写兜底**，三主题自动跟随）。
- **下游事件归属过滤**：桥只按 `instance_id` 精确过滤（无 session 级路由）→ `subchat-opened` / `subchat-state` 的归属判定一律由前端比较 `parent_session_id`（[61 §4.3](../60-reference/61-消息一览.md)）。
- **固定参数锁入口（F1）**：`kind ∈ {sub_chat, group_chat}` → `ChatPanel` 隐藏场景切换 Tag（chatOnly 精简标题条整条隐藏）；只读（F9）→ 不渲染输入框。
- **i18n**：新增键一律 `chat.subchat_*`（`locales/{zh-CN,en-US}/chat.json`）。
- **展示层命名（RENAME-1，2026-10-17）**：面板标题「子会话 / 子会话详细」→ **「任务树 / 任务详情」**（键名不变：`common.sub_sessions` 读作「任务树」、`common.session_detail` 读作「任务详情」）；`chat.select_subsession` 空态与 `chat.ask_user_subsession_title` 同步改为「任务」。**契约与代码标识仍用 `subchat_*` / `subsession-changed`（不改）**——概念层仍是子会话/子聊天，仅展示层改称任务。

### 12.19 群聊窗口（只读展示板）（SUBCHAT-3 阶段 G，🆕 2026-10-16）

> 设计依据 = 群聊会话设计方案 §4（窗口 UI）· §9 D14/D28/D29/D30/D37/D41/D47/D48 · §15.4（关窗暂停/终止）；功能点见 [33-对话 CHAT-025](../30-function-points/33-对话.md) · [31-窗口与布局 WIN-024](../30-function-points/31-窗口与布局.md)。
> **消息面零改动**（[61](../60-reference/61-消息一览.md)）：`subchat-opened` / `subchat-list` / `subchat-finish` / `subchat-continue` / `subchat-state` / `subchat-participant-state`（§4.2/§4.3）+ `data-session-history`（回带 `sender_agent`/`mentions`）· `data-session-group-participants-load`（§3.1/§3.2）+ 复用 `gui.window.open-chat` / `gui.window.closed` / `gui.window.status` / `data-session-get`。

**组件 / 模块落点**

| 落点 | 职责 |
|---|---|
| `views/chat/ChatOnlyView.vue` | **视图分派**：`kind=group_chat` → `GroupChatBoard`；其余 → `ChatPanel`（**原有行为不变**）。`kind` 取自标题读取的**同一次** `data-session-get`（零额外往返）；`kind` 就绪前渲染空载（避免双挂载闪烁） |
| `views/chat/GroupChatBoard.vue` | 群聊窗口主体：**左**（参与者头像区（`thinking` 转 / `idle` 停 / `spoken` 打勾）+ 结论流（主 agent 靠左 / 其余靠右 / 系统提示居中，D47））｜**右**（选中 agent 的 `work_session` 只读分栏 = `MessageList(viewer=true)`，D29/D48）。**无输入框**（人只读旁观，D14） |
| `views/chat/GroupChatCloseDialog.vue` | 关窗二选一（**暂停 / 终止**，D37/N22）——`DialogShell` 内容（`dialog.show` 承载）；暂停 = 不发消息关窗（`instance-exit` → 调度即停）/ 终止 = `subchat-finish` → `finished` |
| `composables/useGroupChat.js` | 接线层：`data-session-group-participants-load`（名册，进程内缓存）· `data-session-history`（展示板结论流，分页取全）· `subchat-continue`（「继续」断点补跑） |
| `composables/useSessionKinds.js` | 会话 `kind` 只读批量取数 + 进程内缓存（`data-session-get`）——供**图标区分**与**窗口额度分类**共用（`subchat-list` 结果项**不含** `kind`，不得为此改载荷） |
| `utils/groupchat.js` | **叶子模块**（无 import / 无副作用）：群聊 kind 判定 · `work_session` id 确定性公式（与后端 `groupchatSessionID` 同源，含 FNV-1a/净化复刻）· 主 agent 靠左判据 · 参与者状态常量 |
| `utils/chatWindows.js` + `composables/useChatWindows.js` | **额度分类**：`MAX_GROUP_CHAT_WINDOWS = 5` 与 `MAX_CHAT_WINDOWS = 5` 互不联动；默认（不传 `count`）时既有普通窗口判定**逐字不变** |

**约定**

- **只读硬约束**：展示板**不渲染输入框 / 不提供发言入口**；群聊本体不跑 LLM、不挂工具（D11）——判定与执行都在各参与者的 `work_session` 里。
- **头像实时状态**：订 `subchat-participant-state`（server→client，**广播**投递 → 前端按 `session_id` 过滤；桥按原名直通，不在 `mqTypeMap`）。下同 `subchat-state`（`lifecycle` 变更）。
- **结论流刷新触发（无新主题）**：调度器在**追加展示板消息之前**发 `spoken` 状态 → 前端收到即（延时兜底 500ms / 1500ms）重读 `data-session-history`；无专用下行事件。
- **右侧分栏不占窗口数**（D29）：`work_session` **不另开窗口**，只在右侧只读渲染（运行中实时 / 已结束只读，D48）。
- **收敛点亮与续跑（G5）**：收 `subchat-state{lifecycle=ready}`（主 agent 判「已收敛」，D10/D52）→ 头部状态位**点亮**并显示「可以结束了」（`chat.subchat_ready_hint` + `.gb-state.lit`）；用户拍板结束 = 复用既有 `subchat-finish`（走「关闭窗口」→「终止」）。到顶硬停后头部常驻**「继续」**（`subchat-continue`，断点补跑；有参与者 `thinking` 时禁用防误触）。
- **关窗二选一（G4）**：窗口内「关闭窗口」→ `GroupChatCloseDialog`（`DialogShell`，`dialog.show` 承载）选**暂停**（不发消息关窗 → `instance-exit` → 后端调度即停、`lifecycle` 不变）/ **终止**（`subchat-finish` → `finished`）；失败显式提示（`chat.subchat_finish_failed`）。原生标题栏 ✕ 不可拦截 → 等价「暂停」。
- **图标区分（D41）**：会话列表下挂 / 状态条 / 启动提醒三处入口与单聊**完全统一**，唯一区别 = 图标（`group` vs `chat-dot-round`）；图标常量在 `components/icon/icons.js`。
- **i18n**：新增键一律 `chat.group_chat_*`（`locales/{zh-CN,en-US}/chat.json`）。
- ⚠️ **原生关窗按钮无法拦截**：群聊窗口沿用宿主原生标题栏（`Frameless:false`）→ 系统「✕」直接投 `WM_CLOSE`（宿主无关窗前钩子，前端收不到"即将关闭"事件；不新增消息面则无下游可用主题）→ **原生 ✕ 等价「暂停」**（关窗即停机、`lifecycle` 不变，不会静默长跑）。「终止」入口 = 窗口内「关闭窗口」按钮的二选一。若要原生 ✕ 弹选择 → 需宿主关窗钩子 + 新下行事件（消息面变更，须先经确认）。

### 12.20 布局容器（自由布局容器子系统 · UILAYOUT-1）

> 设计依据 = [01-自由布局容器改造设计方案](../../ui-config/01-自由布局容器改造设计方案.md)（🔵 设计稿，P0–P4 已实施）。功能点见 [31-窗口与布局 WIN-008/009/010](../30-function-points/31-窗口与布局.md)。

**分层（机制层 / 适配层 / 状态层）**

| 层 | 落点 | 职责 |
|----|------|------|
| 机制层（子系统） | `src/frontend/src/layout-free/` | 复制并入的**纯前端子系统**（组件 `Lf*` 命名域 + 80 个 `lf-*` 主题）；**不认识宿主** —— mq / dialog / icon / split / 视图组件全部经 `deps.js` **注入**（去重，§3.10）。门面 = `engine.js#createLayoutEngine`（`install(app)` / `state` / `actions` / `renderLayout` / `renderBar` / `editing` / `selectedNodeId` / `validate`） |
| 适配层 | `src/frontend/src/layout/` | 宿主唯一装配点：`views.registry.js`（区块注册表）/ `commands.js`（命令落点，含第 5 类 **`publish-mq`**）/ `persist.js`（`layout.free` ↔ `gui.ui.save`·`gui.init-data`）/ `theme.css` / `i18n.js` / `mode.js` / `bar-views/`（10 个栏视图 + STB-1 长任务条目；长任务详情页组件在 `src/frontend/src/views/status/`）/ `ChonkLayout.vue`·`ChonkTopBar.vue`·`ChonkStatusBar.vue`·`barActions.js`；`layout/index.js#setupLayoutEngine(app)` 在 `main.js` 的 pinia / mqPlugin **之后**、`mount` 之前调用 |
| 状态层 | `src/frontend/src/stores/selection.js` | **跨 view 共享状态唯一真相源**（Pinia）：`workDir` / `activeFile` / `activeSession` / `activeTask` / `explorerMode` / `activeSelection`；由 `selectionSubscribers.js` **订阅既有主题**写入，各 view 改读 store（见 [17-前端状态与MQ §9](../10-architecture/17-前端状态与MQ.md)） |

**运行形态（默认 free）**

- `views/layout/MainLayout.vue` **双分支**：`freeMode`（**默认 `true`**；`?layout=legacy` 回退）→ `<ChonkLayout>` 渲染 `layout.free` 文档树（顶栏 / 状态栏为条目化 `ChonkTopBar` / `ChonkStatusBar`）；**旧硬编码分支保留、不删除**。
- **18 类条目 + 默认文档**（`persist.js#defaultLayoutDoc`）：`topToolbar` / `statusbar` / `toolbars` / `drawers`；栏条目 `kind` = `button` / `menu` / `view`；**窗口控件（最小化/最大化/关闭）** 与品牌 / 拖拽区 = 容器外壳**固定件，不进配置**（§4.2）。
- **命令模型**：`open-view{target}`（原 4 类作语法糖）+ **`publish-mq`**（第 5 类，ADR 裁决 1；配置形状 `{type:'publish-mq', topic, payload}` — `commands.js#commandToMq` + `COMMAND_TYPES` + `config.js#validateCommand` 校验「只要求既有主题」+ `LfCommandEditor` 增「MQ 主题 + 载荷(JSON)」）。机制层不认识宿主主题，故**不校验主题存在性**（由适配层保证「只用既有主题」）。
- **栏视图（裁决 2）**：状态栏 / 顶栏的实时 / 条件 / 动态项做成 `kind:'view'` 的 **栏视图条目**（`layout/bar-views/`：`SearchBoxView`·`RecentDirsView`·`ChromeWarnView`·`LogoutView`·`MemoryTokenView`·`QueueStatusView`（legacy 兼容）·`IndexBadgeView`·`MemoryStatusView`·`CompressStatusView`·`LangSwitcherView` + `WindowControls` + **`LayoutProfilesView`（布局档案下拉）**；新建对话框内容 = `LayoutNewDialog`）；**不新增 `valueExpr`/`badgeKey`/`when` 协议**（条件显示由组件自持 `v-if`）。**长任务详情页**（`status-memory` / `status-compress` / `status-index`，STB-1）以 `host:'layout'` 注册（可在布局区放置），默认通过状态栏条目的 `open-view` 在 preview 功能页打开。
- **区块保活**：机制层「**全量渲染 + `v-show`**」——显隐 / 切页签**不卸载**组件（会话 / 页签 / 流式状态留在组件内）；仅配置**删除节点**才卸载。
- **区域显隐（WIN-025，2026-10-21 / UILAYOUT-2）**：节点 `visible`（缺省可见）+ `hideLocked`（不允许直接隐藏，祖先连带不受限）；叶子与容器同权，容器隐藏时子节点 `visible` 原样保留；隐藏用子区 `v-show` 保活，**不参与**尺寸分配与分割条渲染；`LfLayoutSplit` 的 `measure/commit` 只对可见子区计算，并在**隐藏瞬间**把当前占比写回 `sizes[i]`（显示时按快照先占份额）；`validate()` 要求至少一个「有效可见」叶子（自身与所有祖先可见），单例占用继续计入隐藏区域。编辑器入口 = 区域 / 容器设置开关 + 树眼睛 + 右键；运行态入口 = **用户可配置命令**（`publish-mq → lf-area-toggle-visible`，默认不预置按钮）。
- **视图池（WIN-027，2026-10-21）**：运行时单例 view 由 `LfViewPool` **各挂载一次**；`LfViewHost` 运行态只渲染槽位并注册 DOM 目标，池用 `<Teleport :to>` 搬 DOM；切换布局 = 槽位变化（实例不销毁），新布局中不存在的 view 落池内「停车场」隐藏保活。切换时做 FLIP 位移 + 淡入（≈200ms，尊重 `prefers-reduced-motion`）。**设计态**：`LfViewHost` **一律渲染骨架**（图标 + 名称），不挂真实组件 —— 消除单例双开。
- **布局档案（WIN-026，2026-10-21 / UILAYOUT-2）**：`layout.profiles`（JSON 字符串数组 `{id,name,doc,createdAt,updatedAt}`，上限 10 条、名称唯一）+ `layout.activeProfile`（当前档案 id），`layout.free` 仍为当前生效文档；均落 prjusr、经既有 `gui.ui.save` / `gui.init-data`。**种子两套** = `seed:dev`（开发）/ `seed:simple`（简洁，只有一个聊天框），初始化时写入 DB，之后与用户档案**同权**（可改名 / 编辑 / 删除）；「恢复初始布局」按保留 ID 覆盖名称与内容。顶栏 `layout-profiles` 下拉负责列出 / 切换 / 新建（复制自或空白）/ 编辑当前 / 重命名 / 删除 / 恢复初始布局。**保存 ≠ 应用**：编辑非当前档案只写档案，点列表项才切换；编辑当前档案保存即应用 + 更新当前档案。
- **持久化（§3.9）**：只写 `prjusr` 的 **`layout.*`** 子键（`gui.ui.save`；退出兜底 `sendBeacon('/publish')`）；读取经 `gui.init-data` 的 `layout` 子对象。子键 = **`layout.free`**（当前生效文档）· **`layout.profiles`**（档案数组 JSON 字符串）· **`layout.activeProfile`**（当前档案 id）。**旧 7 字段迁移已随 UILAYOUT-2-A 删除**（`?layout=legacy` / 旧硬编码分支 / 旧尺寸键迁移一并移除，存量旧键不再读取）。消息面**零增删**（[61](../60-reference/61-消息一览.md)）。

**尺寸口径（G-57）**：配置占比 `sizes`（持久化/权威）+ 运行态像素定尺（渲染）；拖拽只改像素、松手 `commit()` 写回占比；**配置 `sizes` 变化时**（重启回灌 / 编辑器保存）以配置为权威**幂等重量测**，用户拖拽产生的运行态**不被覆盖** —— 判据 = `LfLayoutSplit.vue#knownRatios`（[31 WIN-009/010 明细](../30-function-points/31-窗口与布局.md)）。禁止自触发渲染（[71 §7.3](../70-conventions/71-编程规范.md)）。

**实现约定**：禁 `watch`/`watchEffect`/`$subscribe`（§4.2 / [17 §3](../10-architecture/17-前端状态与MQ.md)）；布局全域交互**只走 mq**（`v-mq`，与宿主指令**同协议**，见 [71 §7.4](../70-conventions/71-编程规范.md)）；弹窗走自研 `DialogShell`；`:style` 数字加 `'px'`。

> ⚠️ **消息面（P6⑤）**：80 个 `lf-*` 主题登记在**机制层唯一事实源** `layout-free/events/event-names.js`（键名零前缀、值 `lf-*`）；宿主 `src/frontend/src/events/event-names.js` **不重复登记**（两文件为**不同命名域**，机制层经 `deps.js` 注入 mq，不走宿主主题表）。**是否把 `lf-*` 主题在宿主文件里做字面登记 = 待用户确认清单**（[41 G-58](../40-roadmap/41-未决项登记.md)），登记前**不改契约**。

### 12.21 错误呈现与可见重试（ERRUX-1 / RETRY-1，🆕 2026-10-17）

> 用户口径（2026-10-17 实测反馈）：同一错误曾在**聊天流错误气泡**与**下方提示条**重复出现；
> 自动重试**不可见**（i18n 文案存在但无数据源）。契约见 [61 §4.3](../60-reference/61-消息一览.md)。

| 面 | 组件/选择器 | 职责 |
|----|------------|------|
| **唯一错误面** | `ChatPanel.vue` → `.llm-fail-hint.llm-error-hint` | 终态错误（`llm-complete{status:error}`，空回复除外）：人话摘要（`utils/errorMessage.js#classifyError`）+ **「已重试 n/N 次」**（`chat.retry_count_label`，取真实 `retry_attempt`/`retry_count`）+ **「查看错误信息」**（`.llm-fail-detail-toggle` 展开 `.llm-fail-raw` 原始错误全文）+（需要时）「去配置 LLM」CTA |
| **重试可见** | `ChatPanel.vue` → `.llm-fail-hint.llm-retry-hint` | 重试中间态（`session-retry` → 前端 `llm-retry`）：`chat.llm_retry`「正在重试 {attempt}/{max}（等待 {wait} 秒）」，等待秒数 = `retry_wait_ms` 四舍五入；**不落错误消息、不进聊天流**；成功/终态即消失 |
| **聊天流** | `MessageItem.vue` | **不再渲染轮次错误气泡**（`type:'error'` 分支为空模板；头像/时间戳同 suppressed）。错误消息**仍保留在数据流**（复制/诊断/回放可用），仅不渲染。工具级 `.pair-error-hint`（工具结果失败）**保留**，不属本次目标 |

**判据（与 61 §4.3 一致）**：`retryable=true && retry_wait_ms>0` = 中间态；终态 = `retryable=false` 或次数用尽（`retry_wait_ms=0`），仅发一次。
**前端行为变更**：`MessageList.onLlmComplete` 的自动续写**仅**用于"回复未写完"（length/断链/中断）；`wasError`（含 `retryable=true`）**不再**静默续写 —— 否则重试仍不可见；终态错误给手动「继续」。
**约束**：提示条按会话隔离（`session-changed` / 新发送即清空）；禁 `watch`，重试/终态均经既有 `mq.on` + 本地 `ref` 驱动。

### 12.22 输入约束（INPUTVAL-1，🆕 2026-10-21）

> 口径：**前端约束 = 体验，后端写侧校验 = 唯一可信边界**（[12 §4.4](../10-architecture/12-数据层.md) · [CFG-019](../30-function-points/36-配置.md)）。前端规则**同源**集中在 `src/frontend/src/utils/fieldRules.js`（常量 + 纯函数），i18n 文案 `locales/{zh-CN,en-US}/fieldRules.json`，接入点单测 `test/fieldRules.test.js`。

| 面 | 约束 |
|---|---|
| 名称类（LLM / MCP / 场景 / 目录 / Agent / 原语标题） | `maxlength=64`（原语标题 128）+ 保存前必填 / 长度校验（`fieldRules.tooLong` 提示，不静默通过） |
| 标题 / 正文类 | 会话标题 ≤128；`Textarea` 正文 / 提示词 ≤262144（256 KB 字符） |
| URL / 路径 | baseUrl / MCP url ≤2048 且 `http(s)`；各类路径 ≤512 且要求绝对路径 |
| 数值 | LLM `temperature` 0–2、token / 迭代次数按 min/max；会话参数非法值 **blur 时提示 `fieldRules.invalidNumber`**（不再静默回落） |
| 聊天输入 | ≤16384 字符：超限显示 `.input-limit` 提示并在 `onEdit` 截断（粘贴同样经该路径）；附件**单文件 ≤20MB、单条 <10 个**，超限拦截并提示 |
| 通用输入组件 | `components/ui/{Input,Textarea}.vue` 透传 `maxlength/minlength/pattern/required/min/max/step`（**无全局默认**，由各接入点显式声明） |

