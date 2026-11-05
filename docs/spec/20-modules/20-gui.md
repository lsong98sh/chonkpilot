# 20 · src/lib/gui（宿主实现）+ src/desktop（唯一宿主壳）

> **唯一宿主 = `src/desktop`（`FormDesktop`）**，embed 落点 = `src/desktop/frontend/dist`。宿主含：**日志窗口**（隐藏、`HideOnClose`、不注册 instance）+ **项目主窗口映射表**（`OpenProject` / `Projects` / `ActivateProject`）+ 托盘（`src/lib/core/tray`）+ `/api/log/tail`（`source` = `host` | `plugin:<id>`）+ `/api/log/sources`（日志源列表：宿主 + 已有 `plugin.log` 的插件）；日志窗口支持**日志源切换 + 行级关键字过滤 + 按项目过滤**（PLUGIN-LOG-1：行内 `wd=<workdir>` 标签，下拉从当前日志文本收集，纯前端过滤；宿主按该标签把插件日志镜像落盘到 `<workDir>/.chonkpilot/users/<OS 用户名>/logs/plugins/<id>.log`，见 [28 §5.1](28-plugins.md)）。
> 状态：⚠️ 部分与代码一致（2026-11-04 核对：framework 迁移 / 启动流程 / 已下线功能已订正）
> 关联：[10-分层与依赖](../10-architecture/10-分层与依赖.md) · [11-MQ与消息](../10-architecture/11-MQ与消息.md) · [23-filesys](23-filesys.md)
> 代码目录（2026-11-04 核对，TOPO-1 / 提交 `43bfacc`）：**客户端能力**已迁同级仓 `chonkpilot-framework/host/src/` —— `gui/`（窗口工厂 `window.go` / `registry.go` / `testserver.go` / 日志 `logfile.go`）、`models/`、`themefs/`、`fileserver/`、`assetserver/`；主仓 `src/lib/gui/` 现只剩 `bridge/` · `host/` · `mqpolicy/`（根目录无 `.go` 文件）。**单体装配宿主** = `src/lib/gui/host/`（内嵌 server→persist→filesys / 桥接线；入口 `host.Main(distFS, Options{Form}, activations)`，依赖方向 `host → framework gui` **单向**；`activations` = 第二实例激活命令通道，见 [49 §2.22.6](../40-roadmap/49-待实现项.md#1-引用迁移与历史索引2026-11-04)）。**壳** = `src/desktop/`（只嵌前端 dist + 传形态 + 单例判定与激活命令收发 + 调 `host.Main`），`frontend/dist/` 为 embed 落点（构建投放，前端源码工程在 `src/frontend/`）。

---

## 1. 职责与边界

- **一句话**：Windows GUI 宿主——WebView2 承载内嵌前端，内嵌 server 生态（persist/gateway/mcp-server/filesys；插件为外部进程，2026-10-27 起主仓无内嵌插件），并以 `bridge` 做前端 ↔ 总线的薄桥。
- **做**：窗口与布局、前端资源服务、`/publish` 上行、总线事件下行、`gui.*` 本地面、本地系统能力（目录选择/资源管理器/控制台/截图/上传）、`--test-port` 测试通道、**多窗口承载**（主窗口 + N 个纯对话窗口 + **Ask 中心窗口**）。
- **不做**：不做协议翻译（薄桥）；**不持数据库句柄**（数据全经总线 persist）；不做文件变更监听（归 [filesys](23-filesys.md)）。

---

## 2. 形态与部署

| 项 | 值 |
|----|----|
| 编译形态 | windowsgui exe（`-H windowsgui`），`//go:build windows` |
| 前端 | Vue3 + Vite，`//go:embed all:frontend/dist` |
| 资源服务 | **应用资源无 HTTP server**：WebView2 `WebResourceRequested` 拦截虚拟源 `https://app.localhost`；另有**可选 loopback 只读资产端口** `assetserver`（`--ui-assets-addr`，仅 `/themes.css`、`/shared/*`，供插件 webui / 第三方页面 include，见 §4） |
| 构建 | `build-desktop.ps1`（内含 `npm run build`） |
| 产物 | `dist/desktop/chonkpilot.exe` + 引擎 exe（codegraph/vfts）+ `zvec_c_api.dll` + `capability/` |

**命令行参数**（`src/lib/gui/host/main.go`，2026-11-04 核对）：`--work-dir` · `--bridge-url`（分离形态 bridge 对端；合并单进程形态忽略）· `--nats-url`（**已废弃别名**，等价 `--bridge-url`，仅在未显式给 `--bridge-url` 时生效）· `--llm-base` · `--llm-model` · `--no-server`（UI 注入测试）· `--ui-assets-addr` · `--test-port`（**仅 `-tags debugport` 构建存在**）。

> **单进程单产物**：唯一宿主 = `src/desktop` → `host.Main`（日志窗口隐藏 + 托盘 + 每 `work_dir` 一个项目主窗口）；同项目唯一性由 `work_dir → 主窗口` 映射表保证。

---

## 3. 对外接口

### 3.1 虚拟源 HTTP 面（`appHandler`）

| 路径 | 用途 |
|------|------|
| `POST /publish` | 前端消息上行（`{type, payload}`）→ 结果信封 `{ok, result, errors}`（恒 200） |
| `GET /show/<path>` | **资源字节流唯一豁免**（framework `host/src/fileserver.FileShowHandler`，iframe 预览；2026-11-04 核对路径） |
| 其余 | embed dist 静态资源（强制 `Cache-Control: no-cache, no-store, must-revalidate`） |

### 3.2 桥的两向

- **上行**：`mq.emit` → `fetch /publish` → `handlePublish` →（`gui.window.status` 特判 / `DispatchLocal` 本地优先 / `br.PublishEvent`）。
- **下行**（2026-11-04 核对）：订阅已**不是每桥一份**——framework `runtime/dispatch` 对总线 `>` **单订阅**，经共享 `mqpolicy.Hub`/dispatcher 组装 Event、问 Policy（`workspace.Router`）后逐目标投递；每窗口只以 `dispatch.SinkWindow` 注册 sink（`src/lib/gui/bridge/bridge.go:262-286`、`src/lib/gui/mqpolicy/policy.go`）→ `forwardEvent` → `mqTypeMap` 映射为稳定前端 type → `window.mq.emitRemote(envelope)`。

### 3.3 消息分派（`PublishEvent`）

| type | 处理 |
|------|------|
| `llm-start` | 拆两步 `session-start` + `session-send{type:text-user}` |
| `gui.*` | 桥本地 `guiDo`（`guimsg.go`） |
| `data-*` | 经总线 persist（`dataViaPersist`，3s 超时） |
| 含 `.` 的点分主题 | 直通总线（`publishV`） |
| 单字方法 | `frontMethodSubjects` → 域主题 |

`gui.*` 本地动作：`init-data` · `ui.save` · `recent.list` · **`recent.remove`（删除最近项目记录，仅删记录不删资产）** · **`select-dir` / `selected-dir`（目录选择两段式，🆕 2026-10-09 由 `dir.open-dialog` 拆分）** · `pick-executable` · **`file.save`** · `dir.open` · `console.open` · `vcs.info` · `reveal` · `open-with` · `search` · **`capture` / `captured`（截图两段式：`captured` 只回 `b64`、**不落盘**，🆕 2026-10-09 / 2026-10-10 收敛）** · **`upload`（server 落盘 `<data_dir>/<dir>/<name>`、只回 `{name}`，🆕 2026-10-10）** · `prompt-optimise` · `toolchain.detect`（6 工具链 + Chrome 探测） · `system.builtins`（exe 同目录 `config.json` 的内置 MCP 只读项，不含 `llms` 段） · **`zprompt-summarize`**（正文摘要，结果事件 `zprompt-summarized`；`bridge/summarize.go`） · **`devtools.open`**（宿主注入 `SetDevToolsOpener`；未接线则明确失败）。（2026-11-04 核对补全后两项，见 [61 §1](../60-reference/61-消息一览.md)）

> **目录选择两段式（`gui.select-dir` / `gui.selected-dir` / `gui.show-dialog`，🆕 2026-10-09 / 2026-10-10；[61 §1](../60-reference/61-消息一览.md)）**：请求 `gui.select-dir{widget_id}`（结果事件 `gui.selected-dir{widget_id,status,path?}`）由**调用侧适配**包成 Promise（`src/frontend/src/utils/guiAsync.js::selectDir`，按 `widget_id` 关联；`api/config.js::openDirDialog` 委托）。**处理方**：桥 `guimsg.go` 弹**系统原生目录框**后 `EmitFrontend(gui.selected-dir)`。**前端消费 `gui.show-dialog`**：`src/frontend/src/composables/useGuiShowDialog.js`（`App.vue` setup 全局订阅一次，`mq.on` 非 `watch`）→ 自研 `DialogShell` 承载 `views/common/DirPickerDialog.vue`（数据源 = 事件 `options`，**options 驱动**，按层浏览/返回上级/确认）→ 用户选定/取消/关闭发 `gui.selected-dir`（`utils/guiShowDialog.js` 纯逻辑构造载荷）。`options` 口径 = **该用户允许根**（`allowedDirs()` 根 = `auth.projectRoots`；缺省用户数据根）**及其子目录**（深≤3 / ≤200 / 跳隐藏与重型目录；原非 MQ 路由 `GET /dirs` 已删除）→ 同一用户下多个项目（如 `proj-a/b/c`）皆可选。「以新进程打开目录」= `gui.dir.open`：起新进程打开目标目录。

> **`gui.file.save`（native 能力；[61 §1](../60-reference/61-消息一览.md)）**：`guimsg.go` 分派 → `bridge/configfile.go`（+ `savefile_windows.go` 的 `GetSaveFileNameW`）。payload `{name, content, mode?}` → `{path?}`（用户取消 = `""`）：`mode` 缺省 / `"dialog"` = 系统「另存为」；`mode:"backup"` = **不弹框**，直接落 `<dataDir>/backup/<name>`（`dataDir` 未配置 → `<workDir>/.chonkpilot/backup/`）。**安全**：`content` **可能含 API Key**（`llms[].apiKey` / `mcps[].env`、`headers`）→ 本能力**绝不记录 `content`**（无日志打印、错误详情不含内容），只回传落盘路径；`name` 经 `filepath.Base` 净化（防路径穿越）、空名拒绝。

> **场景向导启动触发（`agent-wizard`）**：GUI 桥在 `gui.init-data` 分支（`guimsg.go`）stat `<workDir>/.chonkpilot/project_spec.md` —— **缺失** → 应答补**只增布尔字段 `wizard_required: true`**（[61 §1](../60-reference/61-消息一览.md)），并向本窗口下发 **`agent-wizard`** 事件（`EmitFrontend`，payload `{reason:"missing_spec", work_dir, spec_path}`）；**存在** → `wizard_required: false`、不发事件。**检测落点在 GUI 桥本身（非插件）** —— 设计稿设想的 `plugin-wizard` **未落地**（[37 SCEN-012](../30-function-points/37-场景.md)）。前端消费：`MainLayout.vue` 订阅 `EventNames.agentWizard` → `openScenarioWizard`（`src/frontend/src/views/scenario/ScenarioWizardDialog.vue`，复用 `DialogShell`；向导另可经**场景编辑对话框底部【向导】按钮**手动拉起，同入口）。向导 Step1 探测经 `agent-wizard-probe`、提示词合成经 `agent-wizard-compose`（按项目特点把出厂 archetype 合成为定制化提示词，纯字符串组装；合成文本末以 `{{path.*}}` 四根路径占位符给出技能位置，**组装系统提示词时**渲染，指示 LLM 用文件工具自读，不用 `mcp_load`）、Step5 生成经 `agent-wizard-generate`（server 处理：非主 agent 合成提示词经门面 `ProjectAgentWrite` 落项目级 capability agent 文件 + 以**引用**入场景，主 agent 内联；数据访问经 data 门面 inline 直调，**不新增 `data-*` 主题**）、【关闭/稍后】经 `agent-wizard-skip`；消息面明细见 [61 §4.2](../60-reference/61-消息一览.md)。

---

## 4. 内部控制流

**启动（2026-11-04 核对：`src/desktop/main.go` → `host.Main`）**：

```text
src/desktop/main.go：
参数解析 → 数据根解析 → singleinstance.Acquire(UDS)（同数据根单例；已运行 → Send(CmdActivate) 后退出）
  → activations 通道 → host.Main(distFS, gui.Options{Form:FormDesktop}, activations)
src/lib/gui/host/main.go（runMain）：
  gui.SetBridgeFactory → gui.InitLogging → appwire.Build/Start（persist + gateway + mcp-server + filesys 装配；
  2026-10-27 起主仓无内嵌插件，DefaultPlugins=nil）
  → 建 WebView2 环境 / 窗口工厂（主窗口 Frameless、Title=chonkpilot-<工作目录名>、1280x800）
  → bridge.New(...) + Start()：instance-register + 以 window sink 接入共享 dispatcher
  → （--ui-assets-addr）assetserver 可选启动（loopback 只读静态资产）
  → （--test-port>0）newTestServer + Start
  → 拦截 web 资源、注入 console 捕获
  → 导航完成回调 → SetReady → restoreWindowFromConfig/applyWindowPlacement → Show
  → Navigate(appOrigin+"/") + Run()
```

> **启动期 WebView2 profile 孤儿清理**：`Main` 在建窗**之前**调 `pruneWebviewProfiles(~/.chonkpilot/webview2)` —— **尽力而为**清理 `gui-*` 目录中 **LastWriteTime 早于 24h** 者（本进程 profile 目录名 = 实例 id，见 `newUUID` → `gui-<数字>`；活跃 profile 被 WebView2 运行时持续写入 → 24h 未变动即判残留；当前进程自身目录在建窗时才创建、天然晚于 cutoff，**无需显式排除**）；**只认 `gui-` 前缀**（不动根下其它内容）；删除失败（被占用）逐条跳过并忽略，**不阻断启动**；清理数量 + 释放空间记 Info 日志。正常关闭仍由窗口工厂逐窗口 `RemoveAll` 清理（见 [24 §4.1](../10-architecture/24-多窗口模型设计方案.md)）。

**bridge 包文件职责**：

| 文件 | 职责 |
|------|------|
| `bridge.go` | 桥核心：instance 注册、window sink 接入共享 dispatcher（下行）、`PublishEvent` 分派、`mqTypeMap`、`EmitFrontend`、`WaitForEvent`（test-port）；**防环已移出本文件**（见 `src/lib/workspace/router.go` 的 1s 本实例窗口） |
| `guimsg.go` | `gui.*` 本地面（复用原 `/call` 内部 `callX`，`/call` 已清零）；含 `init-data` **场景向导启动检测**（工程规格 `<workDir>/.chonkpilot/project_spec.md` 缺失 → 应答补 `wizard_required` + 下发 `agent-wizard`）、`zprompt-summarize`、`devtools.open` |
| `summarize.go` | `gui.zprompt-summarize`：后台生成正文摘要，立即回 `{ok,started}`，结果经 `zprompt-summarized{content,error?}` 推送（2026-11-04 核对补入） |
| `data.go` | `data-*` 经总线 persist；prj-config 键值面；usr 配置（标量逐 key + 专用表 `llms`） |
| `local.go` | 初始化数据、布局/窗口/UI/文件树/打开态保存（prjusr）、最近目录（list/remove）、打开目录、VCS、检索 |
| `builtins.go` | `gui.system.builtins`：读 exe 同目录 `config.json` 的只读内置 MCP 项（`mcpServers` 段） |
| `toolchain.go` | `gui.toolchain.detect`：探测 6 语言工具链 + Chrome 路径/版本（系统级候选不落库）；探测顺序 = PATH → 注册表 `App Paths`（HKCU→HKLM）→ 环境变量目录（JAVA_HOME/GOROOT 等）→ 常见安装目录；候选环境变量支持 `%VAR%`/`$VAR`；**Chrome 版本来自其目录 `Application\<ver>\` 的最高版本号目录**（不执行 `chrome.exe --version`）。**result 载荷 = `{tools:[{id, name, path, version}]}`（61 §1 逐字一致，2026-10-28 回退：不再带任何附加字段）**。版本展示（P2-3 ②③④）**全在前端**：规范化（`normalizeVersion`）+ 最小版本判定（`belowMinVersion`）在 `src/frontend/src/utils/toolchainGuide.js`（`minVersion` 取 usr 自由键 `toolchain.<id>.minVersion`，经既有 `data-user-config-load` 读，**零新增消息**）；下载页/winget 静态引导表同文件，宿主不下载、不安装。**只读展示 + UI 提示，不阻断执行** |
| `capture.go` | 截图：隐藏本窗 → GDI 全屏 BitBlt → **只回截图字节 base64 `{b64}`**（**不落盘**；落盘由 server 决定，见 [61 §1](../60-reference/61-消息一览.md) gui.capture/gui.captured；纯 syscall，无 CGO）。**R7-D**：收尾**先选回旧位图再 `DeleteObject`**（位图被选入 DC 时 `DeleteObject` 会失败、不回收）→ 修每次截图泄漏 1 个 DIB |
| `upload.go` | 附件上传：base64 → **prjusr 数据根** `<dir>/`（`bridge.uploadRoot()`；`dir` 缺省 `tmp/uploads`、截图 `screenshot`）→ **只回 `{name}`**（真实落盘名，不暴露服务器路径）。**R7-B-4**：落盘后按 `uploadfs.KeepCap`（2 GiB，**按目录**、mtime **最旧优先**）做**容量兜底** —— 兜底**下沉到 lib core `uploadfs.Save`**（GUI 桥与 browser `guiUpload` **同源**），不再仅 GUI 启动期一次 |
| `optimize.go` | 提示词 AI 优化：SSE 流式，事件 `optimize-token/done/error` |
| `compat.go` | 旧协议事件名兼发（旧回归脚本用） |

> **多窗口（✅ 已实现，2026-11-04 核对；[24-多窗口模型设计方案](../10-architecture/24-多窗口模型设计方案.md)）**：`Main` 为**窗口工厂**（每窗口独占 OS 线程 + `CoInitializeEx(STA)` + 独立 WebView2 `DataPath`）；`appHandler` **每窗口一个**（`role` = `main`/`chat`/`ask`），使 `/publish`、`/show/`、`gui.window.status`、截图**绑定到调用来源窗口**；进程级**窗口注册表**（`windowID → {hwnd, role, sessionID}` + 反向索引 `sessionID → windowID`）承载"**一个 session 只允许一个窗口**"（已开 → 激活；主窗口当前会话 → 拒绝）。每窗口一个 `bridge`（各自 `instance_id` + 各自 `eval`）→ 事件隔离由既有 `acceptEventInstance` 提供，**不改 `eval` 扇出、不引入 `window_id`**。对话窗口打开入口 = framework `gui.Env.OpenChatWindow`（`gui/projectops.go`），与 `gui.window.open-chat` 同一实现；**关窗一律用 `Destroy()`**（`Terminate()` 跨线程无效）。

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

**编辑弹窗页签**：`EditMCPDialog` 内容顶部改用 `Tabs` 拆「**基本信息**」（名称 / 传输方式 / 服务地址 / 命名空间 / 超时 / 描述 / 请求头）·「**运行信息**」（运行时 | 工作目录 / **按实例隔离** | **沙箱** / 启动参数 / 环境变量）·「**工具**」（【加载工具】+ 逐项勾选）共**三页签**（「**启用**」移到底栏与【保存】同行）；页签**仅显示分组**，`handleSave` 仍一次性提交三页全部字段。字段说明改用 label 右侧 `?` 图标 + `Tooltip`（「传输方式」= `config.mcp.transportHint`，新 `help` 图标）；「**分类**」输入项已摘除（`category` 后端字段保留，供 `servers.list` 分组）。「**沙箱**」（`config.mcp.sandbox`）由列表页列迁入「运行信息」页签（三态 Switch，同 `isolate` 口径），**仅 stdio 可开**：http/sse（及 auto 仅填 url）Switch **禁用** + 原因 tooltip（`config.mcp.sandboxStdioOnly`）**〔2026-10-12：`mcps[].sandbox` 已废弃为开关来源（开关来源收敛到 usr `tool_sandbox.mcps`）；本 Switch 的写入口保留、由后端读取时迁移承接，登记 [41 `I-179`](../40-roadmap/41-未决项登记.md#2-已收口--历史只读不再待决)〕**；`SettingsMCPPage` 该列已摘除（编辑入口唯一 = 弹窗）。「**高频工具**」**不再是字段**：改为独立「**工具**」页签 ——【**加载工具**】按钮按别名列出该 server 工具 + 「全部工具」= 全部 hot + 逐项勾选（数据源 = 既有 `tools-list`，**零新增消息面**）；写库仍为**契约原名**列表（`"*"` = 全部），仍随主对话框「保存」落四级文件化 MCP 条目的 `hot_tools`；原独立弹窗 `SetMCPHotToolsDialog.vue` **已删除**（并入页签）。另：「**请求头**」「**环境变量**」改为 **KV 行编辑**（`components/ui/KeyValueEditor.vue`，可增删行），「**启动参数**」为**行列表**编辑（`args` 是 `[]string`，非 KV）。

**MCP 配置四级文件化**：MCP 配置**载体改四级文件** `<级别>/capability/mcps/<名称>.json`（app/user/project/prjusr；一个 server 一个文件）——列表页（`SettingsMCPPage`）经**新数据层 mcp 域**（`data-mcp-{list,save,delete}` + 订阅 `data-mcp-refresh`）读写，**不再整表替换 usr KV `mcpServers`**；列表新增「**级别**」列（文案 = `scenario.level.*`）。编辑弹窗「基本信息」页签新增**级别选择**（`config.mcp.level`，四级，选项文案 = `fileTree.kb_level_*`，缺省 `user`），保存按所选级别落对应级文件（改名/移级先删旧文件、不残留）；i18n = `config.mcp.level` / `config.mcp.levelHint`（zh/en）。**旧 usr KV `mcpServers` 已彻底废弃**（代码零兼容、不再回落）——见 [64 §6](../60-reference/64-配置项一览.md) · [02 §7.3](../00-overview/02-配置层级.md)。

**保存时机**：① 每次展开/折叠后；② 收到变更推送后（防抖约 500ms）；③ 选中节点变化时；④ IDE 关闭时兜底保存。

---

## 7. 依赖

- **上游**：`src/lib/core`(mq/paths) · `src/lib/llm`(server) · `src/lib/filesys` · `chonkpilot-framework/plugin`（宿主运行时 `host/*`）· `jchv/go-webview2`（本地 `src/lib/go-webview2`）。
- **indirect**：`src/lib/data` · `chonkpilot-agent/gateway`（独立仓） · `src/lib/mcp-server`。
- **下游**：无（终端应用）。

---

## 8. 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| 无 HTTP server | WebView2 `WebResourceRequested` 虚拟源拦截 | 免端口、免监听、资源不落盘 |
| 前端资源 no-cache | 强制 `no-cache, no-store, must-revalidate` | WebView2 会持久缓存旧页面 |
| Frameless 保留 WS_THICKFRAME | 无原生标题栏；`WS_THICKFRAME` **只保留最大化 / 贴靠等原生行为** | frameless 下 WebView2 子窗铺满客户区 → `WM_NCHITTEST` 恒 `HTCLIENT`，**系统层无边缘 resize 命中区**（WIN-1 / G-59） |
| 窗口 resize（frameless 主窗口） | 前端边缘命中 → `gui.window.resize{hit}` → 宿主 `StartResize(HT*)` 进入系统原生 resize 模态循环 | 系统无命中区，只能由前端补命中：`components/shell/WindowResizeHandles.vue` 渲染 8 条透明命中条（4 边 6px / 4 角 12px，`-webkit-app-region: no-drag`），`mousedown` 发消息；`guiDo` 把方位映射为 `HTLEFT(10)…HTBOTTOMRIGHT(17)` 后调 `webview2.WindowResizer.StartResize`（`src/lib/gui/bridge/guimsg.go` / framework `host/src/gui/window.go`；设计见同级仓 `chonkpilot-webview2` 的 `FORK.md` P4）。**仅 frameless 装配**；对话窗口（原生标题栏/边框）不装配 → 明确失败。**🆕 最大化时禁用（2026-11-04）**：宿主 `StartResize` 包装层对最大化窗口直接忽略（权威守卫）；前端命中发送与 resize 光标同步关闭（`useWindowMaximized` composable，WindowResizeHandles / ChonkTopBar / WindowControls 共用）。 |
| 窗口移动（frameless 主窗口） | 顶栏非交互区 `mousedown` → 位移 >4px 后发 `gui.window.resize{hit:'caption'}` → 宿主 `WM_NCLBUTTONDOWN(HTCAPTION)` 进入系统移动模态循环 | **WebView2 `app-region` 在本项目自绘 frameless 窗口上实测不触发**（`ICoreWebView2Settings9.IsNonClientRegionSupportEnabled=true` 已生效仍无拖拽区）→ 与 resize 同链路显式进入系统循环（`ChonkTopBar.vue` 阈值判定保留单击/双击；`guimsg.go` 的 `caption → 2(HTCAPTION)`）。CSS 仍保留 `app-region: drag/no-drag` 仅作语义标记。**2026-10-17 修**；**🆕 最大化时禁用（2026-11-04，同上宿主权威守卫 + 前端 `onDragMove` 拦截）** |
| `--test-port` 仅 127.0.0.1 | 外部脚本驱动端点 | 测试专用，见 [52-test-port驱动](../50-testing/52-test-port驱动.md) |

---

## 9. 场景与边界

- **窗口恢复归一化**：`normalizeWindowRect`（主显示器工作区 clamp、完全屏外居中、保证标题栏 ≥80px 可见）。
- **窗口几何落盘权威校正（2026-11-04）**：`gui.ui.save{window}` 落盘几何/最大化标记一律以宿主 `GetWindowPlacement` 为准（`windowNormalPlacement` → `windowSaveEntries`）——最大化时 `NormalPosition` = 最大化前**还原边界**（禁把最大化尺寸当默认启动尺寸）；`maximized` 精确（`ShowCmd==SW_SHOWMAXIMIZED`）；最小化按 `maximized=false` 记录（退出改正常）。启动恢复（`applyWindowPlacement`）：`maximized=true → SW_SHOWMAXIMIZED`（重启恢复最大化）、否则恒 `SW_RESTORE`（启动不最小化）。
- **最大化同步**：`window-maximized-changed` 事件 → 前端切图标；最大化状态由 `useWindowMaximized` composable 统一跟踪（初始查询 + resize 兜底 + 事件同步），消费方 = WindowControls / WindowResizeHandles / ChonkTopBar。
- **`/show` 路径越界防护**：framework `host/src/fileserver.withinDir`（Windows 大小写不敏感），`DataDirs` 为额外放行根。
- **防环（2026-11-04 核对）**：本实例刚发布的主题（1s 窗口）不再回发前端；实现位置已从 `bridge.go` 移到 `src/lib/workspace/router.go`（`mqpolicy` 只做转发）。
- **多窗口（✅ 已实现）**：窗口 ↔ `instance` **1:1**（窗 = 视口，instance = 运行态+消息归属+数据根绑定）；**`instance` 不做业务隔离**（同 `work_dir` 多 instance 共用同一 prjusr 库 → 会话列表/历史**共享**）。**关闭语义**：关任一窗口仅销毁该窗口 + 发该实例 `instance-exit`；**主窗口关闭 = 退出本进程**（其余窗口一并关闭）。**几何**：主窗口 `window.*`/`layout.*`（prjusr）；**对话窗口不持久化几何**（每次默认位置）。**不变量**：进程内 instance 数 = 窗口数（非「1 进程 1 instance」），见 [20-实例隔离与后端分离](../10-architecture/20-实例隔离与后端分离.md)。
- **Ask 中心窗口（🆕 2026-11-04；2026-11-05 全局）**：**每进程单例** `#ask`（跨项目；原生标题栏、不绑定会话、**不占对话窗口额度**；窗口 id 用进程级序号）；归属窗口收到 `ask-user` → `gui.window.open-ask{ask}` → 宿主打开/激活（**已是前景窗口则不抢焦点**）并把 ask 并入**进程级待办缓存**（`ask_id` 去重）→ 左列待办 + 右侧作答（`gui.ask.list` / `gui.ask.remove` / `ask-center-add` / `ask-center-remove`）。**后端唯一分发**：Ask 中心经 `ask-center-subscribe`（user 作用域，可选 work_dir）登记订阅；server 按 workdir+seq 有序快照/推送，实例退出自动退订。询问超时由 `ask_user.timeout` / 项目 `ask_timeout_sec` 决定，到点自动回复（推荐值优先）并继续本轮。宿主不支持时归属窗口回落既有 dialog（`AskUserManager` 队列）。
- **退出**：`instance-exit` → persist 移除绑定 + server 取消 running turn + 回收实例运行态（`busy` / `turns`；**无文件锁**，2026-11-04 核对）；`beaconSaveLayout`（`sendBeacon`）兜底保存布局。

---

## 10. 现状与待办

- ✅ `/call` RPC 已清零（前端无 `window.go.*` 调用点）；`compat.go` 兼容层保留。
- ✅ `layout/window/opened-files/filetree-*` 已落 **prjusr**（个人运行态；读取 prjusr 优先、回落 prj；[02-配置层级](../00-overview/02-配置层级.md) §5）；`ui`/`opened-file` 死键与 `filetree-data` 快照已摘除。
- ✅ `recent_dirs` 已落 **usr** config 自由键（跨进程 / 重启持久化，见 §6）；最近项目下拉支持**按条删除记录**（`gui.recent.remove`；**仅删记录，不删项目资产**）。
- 🗄 `models/config.go` 的 `AgentConfig` 已废弃（project_agents 表 deprecated），**遗留 DB 模型（`ScenarioConfig`/`ScenarioAgent` 等）已随"无历史数据、不写兼容"口径删除**（2026-11-04 核对：`models/` 已迁 `chonkpilot-framework/host/src/models/`，主仓 `src/lib/gui/` 无该目录）。
- 🔵 工具栏「设置」改下拉菜单 + 配置页在 preview 打开（待改造）。
- 🔵 **预留功能（「敬请期待」、非死代码）**：`fork`（会话导航占位按钮，`src/frontend/src/views/sessions/SessionsPane.vue`，点击仅提示 `chat.fork_coming_soon`／「敬请期待」）· `analyzeDialog`（主布局预留引用，`src/frontend/src/views/layout/MainLayout.vue`）。
- ✅ **多窗口（主窗口 + 纯对话窗口 + Ask 中心）已实现（2026-11-04 核对）**：任务分解见 [24 §7](../10-architecture/24-多窗口模型设计方案.md)（MW-1…MW-13）；含 **Resolve 审计**（多实例下 `View.Resolve` 的「唯一实例回退」失效 → 不带 `instance_id` 的数据入口须逐条排查）；数据层取 **B 方案**（desktop 走 `dataDir == ""`，见 [12 §3/§5.3](../10-architecture/12-数据层.md)）。

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
> ① **宿主（gui）**（2026-11-04 核对：实现已迁 framework）：`chonkpilot-framework/host/src/gui/logfile.go` 的 `dualWriter`（stderr 并存）+ `rotateWriter`（2MiB 滚动、保留 5 份）把 **slog 默认 logger** 同时写 stderr 与 `<prjusr 数据根>/logs/gui.log`（`AttachFileLog` / `LogFileSink`）；日志目录经 `gui.init-data` 只增字段 `logDir` 下发（[61 §1](../60-reference/61-消息一览.md)）。日志目录 = **`data.PrjUsrDir`（prjusr 数据根）** + `/logs`（由 `src/lib/gui/host/main.go` 装配，`LogFileSink` 注入 `server.Options.LogWriter`）：固定 **`<workDir>/.chonkpilot/users/<OS 用户名>/logs/gui.log`**（数据根为固定规则，`--data-dir` 已删除）；`gui.init-data.logDir` 指向该处。
> ② **后端（`src/lib/llm/server` + 内嵌插件）**：本包输出收敛到单一出口 **`logf`**（`src/lib/llm/server/log.go`）—— **`logf` 始终写 stdout（行为不变，`--test-port` 捕获口径不变）**；装配方经 **`server.Options.LogWriter`（`io.Writer`，nil = 仅 stdout）** 注入额外 sink 时**同写 sink**，GUI 注入的即宿主那一个 `<prjusr 数据根>/logs/gui.log` 文件 sink。

> **启动分段计时日志（OP-15）：仅插桩量测，不改启动行为/顺序**。为定位「启动慢」真实原因，在启动路径按段打点。格式统一：**`[startup] <阶段> 耗时 <段>ms（累计 <总>ms）`**（段 = 距上次打点，总 = 距起点 `markStartupBegin`）。
> - **宿主（gui，`slog`）**（framework `host/src/gui/main.go` + 主仓 `src/lib/gui/host/main.go`）：参数与目录解析 → prjusr 数据根解析（含 prj 库打开）→ 文件日志挂载 → MQ 总线初始化 → filesys 文件服务启动 → server 装配（capability 契约扫描 + gateway 构建）→ server 启动（数据服务/gateway/能力注册/工具缓存预热/插件）→ 主窗口创建（bridge/WebView2 环境就绪）→ 启动流程就绪；**主窗口首屏渲染完成**在 framework `gui/window.go` NavigationCompleted（roleMain）经 `logStartupStageOnce` 只记一次。
> - **内嵌 server（`src/lib/llm/server/server.go`，统一出口 `logf`）**：数据服务(persist)启动 → 任务层启动 → gateway 启动/下游连接 → capability fswatcher 监听 → 实例视图/心跳扫描启动 → 方法面/事件订阅 → 域工具注册 → 域 agent 注册 → 工具定义缓存预热(`gc.ListTools`) → 内嵌插件启动 → server 启动完成。
> - **装配期（`src/lib/assembly/assemble.go`，经 `logf`）**：capability 契约扫描/注册（含 `AppToolNames` 全树 `WalkDir`）、gateway 构建。
>
> **采数**：桌面单体见 `<prjusr 数据根>/logs/gui.log`。**候选耗时段**（供定位）：capability 契约扫描、gateway Start/下游连接、`gc.ListTools` 预热（15s 上限）、插件 Start、DB 迁移/seed；**优化项待采数后再定**。


### 12.8 主题与浮层样式（token 约定）

> 暗色浮层配色修复的防回归约定。**主题载体 = `documentElement` 的 `data-theme` 属性，非 class** —— 页面不存在 `.dark` 之类的 class，故主题相关选择器**必须**写属性选择器（`[data-theme="dark"] …` / `[data-theme="nord"] …`）；写成 `.dark` / `:root.dark` / `xxx.dark` 属**失效选择器**（编译通过但永不命中）。

- **主题集**：`light`（默认，`:root`）/ `dark` / `nord`。**〔订正（2026-10-23；THEME-1；2026-11-04 核对路径）〕主题值已迁出编译资产** —— 唯一来源 = `src/initdata/themes/themes.css`（运行时优先读 `<exeDir>/themes/themes.css`、缺失回落 `go:embed` 副本，见 [themefs](../../../../chonkpilot-framework/host/src/themefs/themefs.go)）；`variables.css` **只保留几何 / 字体 / 尺寸 token**，不再有颜色声明。宿主导入一行 `<link rel="stylesheet" href="/themes.css">`（**零 `<style>` 注入**）。**无 `system`**（跟随系统未实现）；**切换唯一写点 = `src/frontend/src/composables/useTheme.js` 的 `applyTheme(id, {persist})`**（用户点选 `persist:true`；收广播 / 启动加载 `persist:false`，不写库以防自激）。首帧由 `index.html` head 的经典脚本 `public/shared/ck-theme.js` 读 `localStorage['chonkpilot-theme']` 立即落地（**禁止** `type=module` / `defer` / `async`）。
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
- **token 定义口径（唯一权威）**：**颜色 token** 只有 `src/initdata/themes/themes.css` 的 `:root`（= light 默认）/ `[data-theme="dark"]` / `[data-theme="nord"]` 三块中**声明过**的 `--x` 才算"已定义"（运行时 = `<exeDir>/themes/themes.css`，embed 兜底）；**几何 / 字体 token** 仍在 `src/frontend/src/assets/styles/variables.css`。`public/shared/ck-theme.css` 只做 **`--ck-*` 语义映射**（`--ck-bg: var(--bg-primary, 兜底)`），不写调色板——**其 `var()` 兜底是刻意保留的例外**（`/themes.css` 失效时不白屏，见下条）。其他位置自造名一律不算（组件级可选覆盖钩子见下方专条）。
- **插件 webui 强制口径（2026-10-23；THEME-1）**：插件页面（`capability/plugins/*/webui/*.html`）必须 `<link href="/themes.css">` + `<link href="/shared/ck-theme.css">` + `<script src="/shared/ck-theme.js">`，**只用 `var(--ck-*)`**，禁止硬编码十六进制色、禁止自带 `prefers-color-scheme` 分支（仅 `ck-theme.js` 在宿主未连上时做首次兜底）。宿主经 `PluginPageHost` 在 iframe `load` 后推 `{ckPlugin:1,type:'theme',theme:{id,scheme,tokens}}`，插件不持有调色板、不做主题选择。
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
| 弱化文字 `--text-muted` | 对 `--bg-primary`（token 值：light `#6b7280` / dark `#a6adc8` / nord `#a0b5d4`） | **4.59** | **7.37** | **5.98** | ≥4.5 | light `#6c757d` 4.45 · dark `#6c7086` 3.36 · nord `#81a1c1` 4.64（dark 不可读、nord 余量过小）；**2026-11-01**：nord 状态栏面 `--bg-secondary #3b4252` 上原 `#93a8c8` 仅 4.16 → 提到 `#a0b5d4`（该面 4.82） |
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

### 12.10 状态栏（`layout/ChonkStatusBar.vue` / 默认 `layout/bar-views/*`）

> 用户口径：「statusbar 可否显示 当前索引和 codegraph 的状态？未启用，完成，N/total」。
> **2026-11-04 核对**：状态栏载体 = `src/frontend/src/layout/ChonkStatusBar.vue`（`views/statusbar/StatusBar.vue` 不存在，该目录已空）；自左向右 = 记忆总 token 数（仅记忆库启用时显示）· DevTools 图标 · 撑开 · 语言切换（`LangSwitcher`）。**索引状态区 `index-badge` / 详情页 `status-index` 已下线**（PLUGIN-UI-2 归 codegraph 插件；主仓源码零引用，`test/statusTasks.test.js` 有禁止回归断言）。**STB-1（2026-10-17）**：记忆提取 / 上下文压缩**各一个长任务状态 view**，点击 → preview 详情页；长任务**不再用 toast**。

**长任务状态条目（STB-1，默认 free 形态 = `layout/bar-views/`）**

| 条目（viewKey） | 数据源（零新增主题 / 键） | 空闲 | 排队 / 进行中 | 完成 / 失败 | 点击 |
|---|---|---|---|---|---|
| `memory-status`（记忆提取） | 既有通知面 `tool-notify`：`memory-{queued,start,done}` + `plugin-failure{plugin:memory}`；`memory.enabled` 仅用于「未启用」文案 | `记忆 · 未启用`（记忆库关闭）/ `记忆 · 空闲` | `记忆沉淀排队中` / `记忆沉淀中…`（进行中色） | `记忆沉淀完成` / `记忆 · 失败`（错误色，title = 错误原文） | `open-view{status-memory}` → preview「记忆提取详情」 |
| `compress-status`（上下文压缩） | 同上：`compress-{queued,start,done}` + `plugin-failure{plugin:compress}` | `压缩 · 空闲` | `压缩排队中` / `压缩中…`（进行中色） | `压缩完成` / `压缩 · 失败`（错误色） | `open-view{status-compress}` → preview「上下文压缩详情」 |

- **条目恒可见**（「每种任务都有一个状态 view」）：空闲 / 未启用也渲染（中性色 `--fg-disabled`），不因空闲而消失。
- **失败不静默**：`plugin-failure`（既有 notice）落到条目错误色 + 详情页错误文本；**worker 失败后仍会补发 `*-done` → 前端「失败优先」**（`reduceTask`：error 态忽略后续 done），避免失败被「完成」洗掉。
- **AST 口径**：引擎 `codegraph.status.phase` 仅 `configure|index`（插件回写），**未单独暴露 AST 阶段** → 不编造独立进度；展示归 codegraph 插件状态页（主仓已无索引条目 / 详情页）。
- **去 toast（STB-1）**：记忆 / 压缩的进度 / 完成 / 失败不再走 `ChatPanel.sendNotify`（`message.info`）；`tool-notify` 长任务取值在 `sessionGuard` **之前**归并进 `useQueueStatus`（状态栏是项目级，跨会话可见），命中即 `return`（仅压缩进度继续驱动会话内联指示条）。其它插件的 `plugin-failure` 提示链路保持不变。
- **零新增消息面**：仅消费既有 `tool-notify` 取值（`memory-*` / `compress-*` / `plugin-failure`）与既有 prj 键；命令复用 `open-view{viewKey,target}` → `preview-tab-open{kind}`。

**长任务详情页（preview 功能页，`views/status/*`）**

- `status-memory` / `status-compress`：当前状态（含自动隐藏后的「空闲/未启用」）、最近一次任务（状态 / 开始 / 最近更新 / 会话 / 轮次 / 错误）、**真实 notice 状态时间线**（有界 20 条；数据只有 notice → **不伪造队列条目**）。「去设置」→ `project-config-open{tab:'context'}`。
- **两页均注册进** `layout/view-definitions.js`（`status-*`，`designable:false`）+ `layout/views.registry.js`（组件映射）+ `CodeView.specialComponents`（功能页页签 / 标题 i18n `statusBar.detail.*`）。2026-11-04 核对：`views.registry.js` 仅映射 `status-memory` / `status-compress`；`views/status/` 另有 `TaskDetailLayout.vue`（任务详情，不属 STB-1 的两页）。

**已下线：索引状态区 / `status-index` 详情页（2026-11-04 核对）**

- 代码与主仓测试明确该功能已归 codegraph 插件（PLUGIN-UI-2）：`CodeView.vue:291` 注释「`status-index` 不再存在」；`test/statusTasks.test.js` 断言主仓源码零引用 `status-index` / `index-badge` / `codegraph.status` / `vfts.status`；无 `IndexBadgeView.vue`、无 `test/statusbarIndexStatus.test.js`。
- 原 `.sb-section.sb-index` 两枚徽标、映射函数 `utils/indexStatus.js`、点击 `open-view{status-index}` 与 L4 `test_statusbar.py::SB6-SB8` **均随功能移除**；索引进度改由插件自带 status 页/徽标承载（见 [61 §1.1](../60-reference/61-消息一览.md)）。

**保留的关联测试**：`test/statusTasks.test.js`（长任务条目状态映射 / 点击 open-view / 详情页注册与「去设置」/ 去 toast / 详情字段）· `test/statusbarMemoryEntry.test.js`（记忆总量入口）· `test/layoutAdapter.test.js`（注册表 `memory-status` / `compress-status` / `status-*`，并注明 `index-badge` / `status-index` 已归插件）；L4 `test_statusbar.py` 覆盖记忆/压缩条目（未启用/空闲基线 → 进行中 → 点击开详情 → 失败错误文本 → 失败优先于 done → 完成；截图存 `E:\TEMP\ck-l4-visual\stb\`）。

### 12.11 文件树：被索引排除条目灰显（**2026-10-25 功能下线**）

> ~~用户口径：「如果设置了 gitignored，目录树显示时，判断是否是排除项目。如果是，则文本颜色变成灰色。」方案 = 后端判定（只读面 `data-index-ignored`）~~ —— **该功能已移除**（用户 2026-10-25 决定）：`data-index-ignored` 主题、`internal/indexignored`、`utils/indexIgnored.js`、`TreeNode.is-ignored` 样式与 i18n 键、前端守卫测试与 L4 `run_filetree_ignored.py` 一并删除。**文件树不再灰显被排除条目**；排除口径仍由 `src/lib/ignore` 单一实现供 codegraph / vfts 引擎与插件清单扫描使用（见 [2F-ignore](2F-ignore.md)）。功能点 FT-016 同步标记下线（[32 §FT-016](../30-function-points/32-文件树与文件操作.md)）。

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
- **树形维护能力（🆕 2026-10-15）**：**单类型**子 tab 树根 = **该类型目录本身**（如 `<capRoot>/resources`，**不再多显示一层**类型目录；根节点 `title` = **真实绝对路径**）。**目录/文件右键**增【在资源管理器显示】（`gui.reveal`）；**文件**增【打开】（系统默认程序）/【打开方式】（对话框）。**知识子 tab 目录右键**增【导入】（多选文件，`data-knowledge-import`）；**知识子 tab 新建**先**选类型**（「知识（md 契约）」/「其他（任意类型文件）」）。「其他类型文件」（后端 `type='file'`，如 `config.json`）**单击仅选中、双击用系统默认程序打开**（应用内不预览——见 [41 D-60](file:///e:/BizWorks/chonkpilot/docs/spec/40-roadmap/41-未决项登记.md#2-已收口--历史只读不再待决)）。**删除文件/目录后**经 `filesys.changed` **本地广播**（`mq.emitRemote`，**仅本地分发、不发后端**）通知预览区关闭对应页签（文件自身 / 目录下全部）；各类型新建入口加「名称即文件名、创建后不可改」提示。**原语编辑面板**（`PrimitivePanel`）顶部对**当前编辑的契约 md** 同样提供 **在资源管理器中显示 / 打开 / 打开方式** 三入口（复用 `gui.reveal` / `gui.open-with`）。见 [42 §2.30](file:///e:/BizWorks/chonkpilot/docs/spec/40-roadmap/42-决策记录.md)。
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

### 12.17 Vfts 索引页 / 系统级词典（🗄 页面已删除，2026-11-04 核对）

> 分词器仍固定 `jieba`（[2B-vfts §4.1](2B-vfts.md)）；消息面 `vfts.dict.get` / `vfts.dict.set` / `vfts.reindex` 仍存在。但**前端配置页 `views/settings/VftsConfig.vue` 已删除**（UIREG-1 并入 codegraph 插件；`src/frontend/test/vftsDictReindex.test.js` 文件头明示「配置页已并入 codegraph 插件（前端页面删除）」）。`composables/useVftsDict.js` 仍在但**无任何 `.vue` 消费方**；`views/settings/ProjectConfig.vue` 的旧 `vfts` 页签映射到 `codegraph`。

- **现行入口**：配置 → 插件维护 → codegraph 插件页（词典 / 重新索引），非主仓内置页。
- **保留契约（供插件页使用）**：`vfts.dict.get` 回填 `dict_dir` / `base_dicts`（内嵌基础词典，只读）/ `user_dict` / `word_count`；`vfts.dict.set` 保存自定义词并自动调度强制重建；`vfts.reindex` 立即回执 `{ok,started}`，进度经既有 `vfts.status`（`indexing` + `progressDone/progressTotal`）回显。均**零新增消息面**。
- **关联测试**：`src/frontend/test/vftsDictReindex.test.js` 现仅守卫 composable 三主题、msgkeys 生成物与 i18n，**不再有页面级断言**。

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
| 机制层（子系统） | 平台基座 `chonkpilot-framework/host/web/src/layout-free/`（TOPO-1；主仓经 `@ckfw/layout-free/**` 引用） | 复制并入的**纯前端子系统**（组件 `Lf*` 命名域；内部交互一律直调 `stores/actions.js`，**无 `lf-*` 主题**）；**不认识宿主** —— mq / dialog / icon / split / 视图组件全部经 `deps.js` **注入**（去重，§3.10）。门面 = `engine.js#createLayoutEngine`（`install(app)` / `state` / `actions` / `renderLayout` / `renderBar` / `editing` / `selectedNodeId` / `validate`） |
| 适配层 | 主仓 `src/frontend/src/layout/` + 平台基座 `chonkpilot-framework/host/web/src/layout/`（TOPO-1）：`views.registry.js`（区块注册表；**唯一 view 注册 API** `registerViews(entries, source)`，viewKey ↔ 组件实例/module 由此关联）/ `openView.js`（按注册表 `host` 声明分发打开，**无独立翻译表**）/ `persist.js`（`layout.free` ↔ `gui.ui.save`·`gui.init-data`）/ `theme.css` / `i18n.js` / `bar-views/`（栏视图 + STB-1 长任务条目；长任务详情页组件在 `src/frontend/src/views/status/`）/ `ChonkLayout.vue`·`ChonkTopBar.vue`·`ChonkStatusBar.vue`·`barActions.js`；`layout/index.js#setupLayoutEngine(app)` 在 `main.js` 的 pinia / mqPlugin **之后**、`mount` 之前调用 |
| 状态层 | `src/frontend/src/stores/selection.js` | **跨 view 共享状态唯一真相源**（Pinia）：`workDir` / `activeFile` / `activeSession` / `activeTask` / `explorerMode` / `activeSelection`；由 `selectionSubscribers.js` **订阅既有主题**写入，各 view 改读 store（见 [17-前端状态与MQ §9](../10-architecture/17-前端状态与MQ.md)） |

**运行形态（默认 free）**

- `views/layout/MainLayout.vue` **单分支 free（2026-11-04 核对）**：`freeMode`（**默认 `true`**）→ `<ChonkLayout>` 渲染 `layout.free` 文档树（顶栏 / 状态栏为条目化 `ChonkTopBar` / `ChonkStatusBar`）；**旧硬编码分支与 `?layout=legacy` 回退已于 UILAYOUT-2-A 删除**（`MainLayout.vue:7` 注释；同节下方「旧 7 字段迁移已删除」一致）。
- **18 类条目 + 默认文档**（`persist.js#defaultLayoutDoc`）：`topToolbar` / `statusbar` / `toolbars` / `drawers`；栏条目 `kind` = `button` / `menu` / `view`；**窗口控件（最小化/最大化/关闭）** 与品牌 / 拖拽区 = 容器外壳**固定件，不进配置**（§4.2）。
- **view 承载 = 注册表声明（2026-10-30 定稿）**：一个 view 的承载方式由它自己的定义声明（`host: 'layout' | 'bar' | 'dialog' | 'preview'`，插件 view 另有 `containers`），打开逻辑 `openView(viewKey)` **按声明分发**。**禁止**再维护「viewKey → 目标页 / 具体 Vue」的白名单翻译层（原 `layout/commands.js` 的 `PREVIEW_TAB_TARGETS` 及 4 类语法糖归一**已删除**）。长任务详情（`status-memory` / `status-compress`）声明 `host:'preview'`，点击状态条目 → `preview-tab-open{kind}`（跨模块，走 MQ 合法）。
- **MQ 边界（2026-10-30 定稿；2026-10-31 G-89 全量落地）**：**MQ 只用于跨模块 / 跨进程边界**（如状态栏条目 → 预览区页签、前端 → 后端、插件 ↔ 宿主）。**同一前端进程内的组件协作不走 MQ**——用 props / 回调 / store 动作（容器内部页签切换、编辑器草稿、弹层开关等）。**G-89 完成态（2026-10-31）**：机制层 91 个 `lf-*` 主题**全部清退**（批次 1 = 运行期命令 / 页签操作 20 个；批次 2 = 设计态编辑 / 纯 UI 行为 71 个）→ 统一由 `layout-free/stores/actions.js` 直调 config / edit / runtime store；`layout-free/events/event-names.js` 与 `stores/mqCommands.js` **已删除**（[41 G-89](../40-roadmap/41-未决项登记.md#2-已收口--历史只读不再待决)）。
- **布局文档内的命令模型（机制层）**：栏 / 菜单条目可声明 `command`（`open-tabs` / `open-drawer` / `toggle-drawer` / `close-tabs-active` / `toggle-area-visible` / `publish-mq`），由 `layout-free/utils/commands.js#runCommand` 执行——**内部命令直调域动作**（G-89），仅 `publish-mq` 仍发布**既有宿主主题**；机制层不认识宿主主题（**不校验主题存在性**，适配层保证「只用既有主题」）。这是**写在布局文档里的声明式数据**，不属于「viewKey → 具体 Vue」翻译表。
- **栏视图（裁决 2）**：状态栏 / 顶栏的实时 / 条件 / 动态项做成 `kind:'view'` 的 **栏视图条目**（`layout/bar-views/` 实测 11 个组件：`SearchBoxView`·`RecentDirsView`·`ChromeWarnView`·`CkStoreView`·`MemoryTokenView`·`QueueStatusView`（legacy 兼容）·`MemoryStatusView`·`CompressStatusView`·`LangSwitcherView`·`LayoutProfilesView`（布局档案下拉）·`LayoutNewDialog`，另有容器固定件 `WindowControls`；**`LogoutView` / `IndexBadgeView` 不存在，已删除**，2026-11-04 核对）；**不新增 `valueExpr`/`badgeKey`/`when` 协议**（条件显示由组件自持 `v-if`）。**长任务详情页**（`status-memory` / `status-compress`，STB-1）声明 `host:'preview'`，由状态栏条目的 `openView` 在 preview 功能页打开；索引进度已归插件（PLUGIN-UI-2，`status-index` / `index-badge` 不存在）。
- **区块保活**：机制层「**全量渲染 + `v-show`**」——显隐 / 切页签**不卸载**组件（会话 / 页签 / 流式状态留在组件内）；仅配置**删除节点**才卸载。
- **区域显隐（WIN-025，2026-10-21 / UILAYOUT-2）**：节点 `visible`（缺省可见）+ `hideLocked`（不允许直接隐藏，祖先连带不受限）；叶子与容器同权，容器隐藏时子节点 `visible` 原样保留；隐藏用子区 `v-show` 保活，**不参与**尺寸分配与分割条渲染；`LfLayoutSplit` 的 `measure/commit` 只对可见子区计算，并在**隐藏瞬间**把当前占比写回 `sizes[i]`（显示时按快照先占份额）；`validate()` 要求至少一个「有效可见」叶子（自身与所有祖先可见），单例占用继续计入隐藏区域。编辑器入口 = 区域 / 容器设置开关 + 树眼睛 + 右键；运行态入口 = **内部命令 `toggle-area-visible`**（`{ nodeId, viewKey? }`，直调 `stores/actions.js#toggleAreaVisible` 写 live 并落盘；默认不预置按钮，G-89 起不再借 `publish-mq` 发内部主题）。
- **视图池（WIN-027，2026-10-21）**：运行时单例 view 由 `LfViewPool` **各挂载一次**；`LfViewHost` 运行态只渲染槽位并注册 DOM 目标，池用 `<Teleport :to>` 搬 DOM；切换布局 = 槽位变化（实例不销毁），新布局中不存在的 view 落池内「停车场」隐藏保活。切换时做 FLIP 位移 + 淡入（≈200ms，尊重 `prefers-reduced-motion`）。**设计态**：`LfViewHost` **一律渲染骨架**（图标 + 名称），不挂真实组件 —— 消除单例双开。
- **布局档案（WIN-026，2026-10-21 / UILAYOUT-2）**：`layout.profiles`（JSON 字符串数组 `{id,name,doc,createdAt,updatedAt}`，上限 10 条、名称唯一）+ `layout.activeProfile`（当前档案 id），`layout.free` 仍为当前生效文档；均落 prjusr、经既有 `gui.ui.save` / `gui.init-data`。**种子两套** = `seed:dev`（开发）/ `seed:simple`（简洁，只有一个聊天框），初始化时写入 DB，之后与用户档案**同权**（可改名 / 编辑 / 删除）；「恢复初始布局」按保留 ID 覆盖名称与内容。顶栏 `layout-profiles` 下拉负责列出 / 切换 / 新建（复制自或空白）/ 编辑当前 / 重命名 / 删除 / 恢复初始布局。**保存 ≠ 应用**：编辑非当前档案只写档案，点列表项才切换；编辑当前档案保存即应用 + 更新当前档案。
- **持久化（§3.9）**：只写 `prjusr` 的 **`layout.*`** 子键（`gui.ui.save`；退出兜底 `sendBeacon('/publish')`）；读取经 `gui.init-data` 的 `layout` 子对象。子键 = **`layout.free`**（当前生效文档）· **`layout.profiles`**（档案数组 JSON 字符串）· **`layout.activeProfile`**（当前档案 id）。**旧 7 字段迁移已随 UILAYOUT-2-A 删除**（`?layout=legacy` / 旧硬编码分支 / 旧尺寸键迁移一并移除，存量旧键不再读取）。消息面**零增删**（[61](../60-reference/61-消息一览.md)）。
- **顶栏分区与条目（UI-FIX-1，2026-11-02 用户定稿；文档 `DOC_VERSION=7`）**：左区 = 打开 / 最近（箭头）/ 配置（设置菜单，含插件维护）；中区 = 全局搜索；右区 = 场景 / 主题 / 语言 / 会话 / **任务详情** / 布局（**一律图标**，hover 给 tooltip；菜单条目在 `style='icon'` 时不渲染文字与 caret）。窗口控件仍为外壳固定件。`parseFreeDoc` 的 **v7 迁移**把旧文档 `topToolbar.items` 整体替换为新默认条目（保留 `background`/`zoneBackground`）。
- **「任务详情」= 任务区整块显隐**：默认栏条目命令改为内部命令 `toggle-area-visible{nodeId:'split_left_bottom'}`（容器级显隐，任务树 + 任务详情一起显示/隐藏），不再开右侧抽屉；`toggle-area-visible` 与配置校验均支持**容器节点**。
- **顶栏三区宽度收敛（UI-FIX-1）**：`LfBarZones` 按容器实测宽等比压缩两侧定宽区（`ResizeObserver`），保证 `#right-extra`（最小化 / 最大化 / 关闭）永不被挤出窗口；持久化键升级为 `layout-free:bar-zones:v2:*`。
- **输入框最小高度**：会话输入框（`InputBox.vue` 自研 contenteditable）最小高度 = **3 行正文**（`min-height: 70px` = 20px × 3 + 上下内边距 10px），`autoResize()` 同步。
- **页签 label**：`TabBar` / `LfTabbedContainer` / 通用 `Tabs` 一律 `white-space: nowrap` + `min-width:0` + `text-overflow: ellipsis`（长 label 不换行，超出显示 `…`）。
- **布局档案下拉**：`LayoutProfilesView` 弹层 **Teleport 到 body**（栏内 view / zone 都是 `overflow:hidden`，原地绝对定位会被裁剪 → 表现为「点击无效」）。
- **插件状态栏徽标占位**：`PluginPageHost` 以容器查询（`@container (max-height:48px)`）走紧凑态 —— 徽标位（96×20）只显示 10px spinner，不渲染「加载中」文字/占位说明。
- **记忆沉淀配置迁移（MEMORY-UI-1，2026-11-02）**：`项目配置 → 上下文` 不再承载记忆库 / 类别 / 提示词 / 立即沉淀；这些配置改由 **memory 插件配置页**（配置 → 插件维护 → 记忆沉淀 → 配置，`webui/config.html`）承载，读写仍是既有 `memory.*` prj 键 + `data-memory-*` / prompt 域，**零新增配置键 / 零改形**。上下文页只保留压缩配置与一行迁移提示。

**尺寸口径（G-57）**：配置占比 `sizes`（持久化/权威）+ 运行态像素定尺（渲染）；拖拽只改像素、松手 `commit()` 写回占比；**配置 `sizes` 变化时**（重启回灌 / 编辑器保存）以配置为权威**幂等重量测**，用户拖拽产生的运行态**不被覆盖** —— 判据 = `LfLayoutSplit.vue#knownRatios`（[31 WIN-009/010 明细](../30-function-points/31-窗口与布局.md)）。禁止自触发渲染（[71 §7.3](../70-conventions/71-编程规范.md)）。

**实现约定**：禁 `watch`/`watchEffect`/`$subscribe`（§4.2 / [17 §3](../10-architecture/17-前端状态与MQ.md)）；**同进程组件协作走 props / 回调 / store 动作**，跨模块 / 跨进程边界才走 MQ（`v-mq`，与宿主指令**同协议**，见 [71 §7.4](../70-conventions/71-编程规范.md)）；弹窗走自研 `DialogShell`；`:style` 数字加 `'px'`。

> ⚠️ **消息面（P6⑤ / G-89 完成态）**：机制层**不再有自己的 MQ 主题表** —— `layout-free/events/event-names.js` 已删除，内部交互直调 `stores/actions.js`。跨边界仍走宿主主题（`src/frontend/src/events/event-names.js` + framework 分片）。[61](../60-reference/61-消息一览.md) **零变更**。

### 12.21 错误呈现与可见重试（ERRUX-1 / RETRY-1，🆕 2026-10-17）

> 用户口径（2026-10-17 实测反馈）：同一错误曾在**聊天流错误气泡**与**下方提示条**重复出现；
> 自动重试**不可见**（i18n 文案存在但无数据源）。契约见 [61 §4.3](../60-reference/61-消息一览.md)。

| 面 | 组件/选择器 | 职责 |
|----|------------|------|
| **唯一错误面** | `ChatPanel.vue` → `.llm-fail-hint.llm-error-hint` | 终态错误（`llm-complete{status:error}`，空回复除外）：人话摘要（`utils/errorMessage.js#classifyError`）+ **「已重试 n/N 次」**（`chat.retry_count_label`，取真实 `retry_attempt`/`retry_count`）+ **「查看错误信息」**（`.llm-fail-detail-toggle` 展开 `.llm-fail-raw` 原始错误全文）+（需要时）「去配置 LLM」CTA |
| **重试可见** | `ChatPanel.vue` → `.llm-fail-hint.llm-retry-hint` | 重试中间态（`session-retry` → 前端 `llm-retry`）：`chat.llm_retry`「正在重试 {attempt}/{max}（等待 {wait} 秒）」，等待秒数 = `retry_wait_ms` 四舍五入；**2026-10-23 起前端本地逐秒倒计时**（1s 定时器从该值递减到 0；下一次 `session-retry` 重置、`llm-complete`/`session-interrupted`/新发送即停表收起），契约不变（不新增消息/payload）；**不落错误消息、不进聊天流**；成功/终态即消失 |
| **聊天流** | `MessageItem.vue` | **不再渲染轮次错误气泡**（`type:'error'` 分支为空模板；头像/时间戳同 suppressed）。错误消息**仍保留在数据流**（复制/诊断/回放可用），仅不渲染。工具级 `.pair-error-hint`（工具结果失败）**保留**，不属本次目标 |

**判据（与 61 §4.3 一致）**：`retryable=true && retry_wait_ms>0` = 中间态；终态 = `retryable=false` 或次数用尽（`retry_wait_ms=0`），仅发一次。
**前端行为变更（RETRY-2，2026-10-20 再收口）**：`MessageList.onLlmComplete` **不再有任何静默自动续写** —— 续写统一由后端按宽限期承担（`autoContinueDelaySec` / `continueText`，见 [64 §8](../60-reference/64-配置项一览.md) · [21 §4](../20-modules/21-llm-server.md)）；前端仅在错误/截断终态评估并展示手动「继续」入口（`.continue-btn`），`wasError`（含 `retryable=true`）同样不续写 —— 否则重试仍不可见。`incomplete`（length 截断）终态下前端**不**显示「继续」，等后端宽限期续写。
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

### 12.23 图标体系（ICON-1，🆕 2026-10-28）

> 口径：**唯一图标源 = `src/frontend/src/components/icon/icons.js`（name → 内联 SVG 字符串）**；渲染器 = `components/icon/Icon.vue`。`layout-free` 的 `LfIcon` 是同一个组件（`layout/index.js` 注入 `components: { Icon }`，`layout-free/engine.js` 注册为 `LfIcon`），**不存在第二套通用图标实现**。

| 面 | 约定 |
|---|---|
| 文件类型图标 | 独立第二套：`assets/file-icons/vivid/*.svg`（file-icon-vectors **Vivid**，MIT，99 个），经 `FileIcon.vue` 用 CSS `background-image` 渲染，**不并入** `icons.js` |
| 通用 UI 命名 | 统一 **Lucide 命名**（`settings` / `eye` / `eye-closed` / `trash-2` / `x` / `chevron-down` / `panel-right` / `grid` …）；`layout` / `layout-free` / 默认布局文档（`layout/persist.js`、`layout/view-definitions.js`、`layout-free/config/defaultConfig.js`、`layout-free/utils/regions.js`）里的 `icon` 值**直接就是 Lucide 名** |
| 图形来源 | Lucide **v0.544.0**（ISC）；**只提取 `<svg>` 内部图形**，套统一外壳 `viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap/linejoin="round"`；**不引入任何运行时依赖**（禁 `lucide-vue-next` / `lucide-static` 包） |
| 历史别名 | `sunny→sun`、`moon-night→moon-star`、`split-square-*→square-split-*`、`unlock→lock-open`、`coin→coins`、`paste→clipboard-paste`、`grid→grid-3x3`、`filter→funnel`；**键名保留调用点既有写法**，图形一一对应 |
| 业务专有图标 | 保留 1024×1024 `fill=currentColor`：`folder` / `folder-opened` / `document` / `think` / `effort` / `icon-llm` / `icon-other-tool` / `recent-dir` / `folder-tree` |
| 未定义兜底 | `Icon.vue` 对未注册 name 渲染 `fallback`（默认 `·`，`opacity .45`，`title="未定义图标：<name>"`）并在 dev 下 `console.warn` 一次 —— **禁止**静默渲染为空白 |
| 回归防线 | `src/frontend/test/iconRegistry.test.js`：静态扫描全仓 `<Icon\|LfIcon name="…">`、`icon: '…'`、`:name="'…'"`、`LfBarEditor.iconPresets`，任一名字不在 `icons.js` 即失败；并校验无重复键 / `viewBox` 合法 / 继承 `currentColor` |
| 图标选择器 | `layout-free/components/icon/LfIconPicker.vue` = popup 面板 + 搜索，**每行固定 8 个**（单元格 30px），点击即选并收起；候选清单 = `Object.keys(icons)`（宿主经 `deps.iconNames` 注入），**不得**再内联 `icon-set.js` |

### 12.24 会话列表「运行中」标识（🆕 2026-11-03）

- **展示**：`views/sessions/SessionsPane.vue` 会话行 meta 行（`#id` 左侧）显示「运行中」徽标（小圆点 + `chat.session_running`），仅对有在跑轮次的会话显示。
- **数据**：`session-running-list`（[61 §4.2](../60-reference/61-消息一览.md)）—— server **只读内存快照**（`busy` 注册表，键 `instKey(instance, session)`），**不落库**；返回 `{list:[{session_id,turn,instance_id}]}`（本进程全部窗口，前端按自己的会话列表过滤）。
- **刷新**：窗格打开 / `load()` 时拉一次 + 4s 轮询（`RUNNING_POLL_MS`）。**不以** `session-turn-start` / `session-complete` 作唯一来源：二者带 `instance_id`，只投发起窗口 → 主窗口看不到其它对话窗口在跑的会话；轮询兜跨窗口一致性，窗格卸载即停。
- **口径**：进程重启后运行态本就不保留（启动 `data-session-cleanup-stale` 把遗留 `running` 置 `interrupted`）→ 内存快照即权威。

### 12.25 项目树（PM）门面与边界（SANDBOX-WRITE-1 P5 / C15，🆕 2026-11-03）

> 权威模块文档：[2I-工作区管理](2I-工作区管理.md)（PM 的职责 / 接口 / 生命周期）· [23 §2](../10-architecture/23-工程与部署拓扑.md)（依赖方向）

**四层模型**（PM 只存 ID + 关系 + 状态，不存消息 / 文件内容）：

| 层 | 内容 | 归属 |
|----|------|------|
| L1 | `work_dir` + `user` → 数据访问面 | `workspace/layout`（prj 共享 / prjusr 按用户） |
| L2 | instance(window) → 可见性 / 承载关系 | framework `runtime/instance.Manager`（**单源**，PM 委托） |
| L3 | dialog → 该实例 / 项目下的对话 | 宿主窗口层 + 数据面 |
| L4 | session → 对话内的会话（轮次挂此） | llm-server 运行态 + 数据面 |

**门面（`workspace.Manager`，薄）**：`OpenProject / CreateWindow / Hide / Close / CreateDialog /
CreateSession` → 委托 `ProjectOps` 端口；`Route / Mark` → MQ 分发策略唯一实现。

**边界（硬约束）**：
1. **PM 不持有窗口句柄**（否则无 GUI 形态跑不了）：窗口动作一律经 `ProjectOps`，GUI 宿主实现 =
   `src/lib/gui/host/projectops.go`（`OpenProject` 直接复用唯一开窗实现 `openProjectAs`，不写第二套）。
2. `create*` 是**薄门面**，业务动作仍走窗口工厂 / 既有 MQ。
3. **实例视图单源**：入口装配器先建 `instance.Manager`，同时注入 `workspace` 与 `llm-server`；
   PM 的 `Resolve` / `InstanceIDs` / 开闭判定全部读它。
4. **路由单源**：`mqpolicy` 已收归 `workspace.Router` —— `gui/mqpolicy.WorkDirPolicy` 只做
   `dispatch.Sink ↔ workspace.Sink` 转换（`SetRouter(app.Workspace.Router())`），规则不再有两份。
5. **窗口 / 会话动作已全部接线（2026-11-03，[41 G-100](../40-roadmap/41-未决项登记.md#2-已收口--历史只读不再待决) ✅）**：
   - 框架导出薄动作 `gui.Env.{HideProject,CloseProject,OpenChatWindow,ProjectBridge}`
     （与托盘 / `gui.window.open-chat` **同一实现**，零行为变更）；
   - `CreateWindow`：`main` → `OpenProject(hidden=!visible)`；`chat` → `OpenChatWindow`（需 `params[session_id]`）；
   - `CreateDialog / CreateSession` → 项目桥的**既有** `data-session-ensure-session`（根会话自举回带 `dialog_id`），零新增消息面；
   - `gui/host/projectops.go` 依赖抽为 `projectWindowOps` 接口（仅为可测，生产仍传 `*gui.Env`）；
     全部未接线分支已消失，`workspace.ErrOpsNotWired` 只在「装配未注入端口」时出现。
6. **归属存储单源（[41 G-101](../40-roadmap/41-未决项登记.md#2-已收口--历史只读不再待决) ✅）**：框架定义 `gui.OwnerStore`
   （`OwnerOf/SetOwner/ClearOwnerIf/ForgetInstance`），主仓 `workspace.Manager` 实现并经
   `Env.SetOwnerStore(app.Workspace)` 注入；注入后框架**不再自持副本**（`gui/owner.go` 的 map 仅作
   测试 / 无 PM 形态的回落）。依赖方向仍为「框架不 import 主仓」，只是把存储反转给 PM。


