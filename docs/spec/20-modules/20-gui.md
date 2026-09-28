# 20 · src/lib/gui（宿主实现）+ src/desktop / src/gui（两个宿主壳）

> 日期：2026-09-10 ｜ 状态：✅ 与代码一致
> 关联：[10-分层与依赖](../10-architecture/10-分层与依赖.md) · [11-MQ与消息](../10-architecture/11-MQ与消息.md) · [23-filesys](23-filesys.md)
> 代码目录（D-28 结构重整）：宿主实现（唯一一份）= `src/lib/gui/`（`main.go` 的 `Main(distFS, Options{Form})` + `bridge/` + `internal/` + `models/`）；**两个壳**（只嵌前端 dist + 传形态）= `src/desktop/`（桌面单体，`FormDesktop`）· `src/gui/`（GUI 客户端，`FormGui`，`-tags split`），各自 `frontend/dist/` 为 embed 落点（构建投放，前端源码工程在 `src/frontend/`）。〔2026-09-19 订正：原「`frontend/` 只保留 embed 产物」；现为两壳各自的 `frontend/dist/`，原文保留〕

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
| 前端 | Vue3 + Vite，`//go:embed all:frontend/dist` |
| 资源服务 | **无 HTTP server**：WebView2 `WebResourceRequested` 拦截虚拟源 `https://app.localhost` |
| 构建 | `build-desktop.ps1`（原名 `build-standalone-gui.ps1`；内含 `npm run build`） |
| 产物 | `dist/desktop/chonkpilot.exe` + 引擎 exe（codegraph/vfts）+ `zvec_c_api.dll` + `capability/` |

**命令行参数**（`main.go`）：`--work-dir`（缺省 cwd）· `--data-dir`（缺省 `<workDir>/.chonkpilot`）· `--test-port`（>0 开测试通道）· `--llm-base` · `--llm-model` · `--no-server`（UI 注入测试）· `--bridge-url`（**分离形态（`-tags split`）下 bridge 对端地址（HTTP）**；inprocess 合并形态忽略；原名 `-nats-url`，**兼容别名保留**（帮助中标注已废弃，仅 `--bridge-url` 未给出时生效））。

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

`gui.*` 本地动作：`init-data` · `ui.save` · `recent.list` · `dir.open-dialog` · `pick-executable` · **`file.save`（2026-09-20 新增，批 3 · ⑯）** · `dir.open` · `console.open` · `vcs.info` · `reveal` · `open-with` · `search` · `capture` · `upload` · `prompt-optimise` · `toolchain.detect`（6 工具链 + Chrome 探测） · `system.builtins`（exe 同目录 `config.json` 的内置 MCP 只读项，2026-09-14 移除 `llms` 段）。

> **`gui.file.save`（2026-09-20 新增 native 能力；[61 §1](../60-reference/61-消息一览.md) · [42 §2 (133)](../40-roadmap/42-决策记录.md)）**：`guimsg.go` 分派 → `bridge/configfile.go`（+ `savefile_windows.go` 的 `GetSaveFileNameW`）。payload `{name, content, mode?}` → `{path?}`（用户取消 = `""`）：`mode` 缺省 / `"dialog"` = 系统「另存为」；`mode:"backup"` = **不弹框**，直接落 `<dataDir>/backup/<name>`（`dataDir` 未配置 → `<workDir>/.chonkpilot/backup/`）。**安全**：`content` **可能含 API Key**（`llms[].apiKey` / `mcps[].env`、`headers`）→ 本能力**绝不记录 `content`**（无日志打印、错误详情不含内容），只回传落盘路径；`name` 经 `filepath.Base` 净化（防路径穿越）、空名拒绝。**browser 形态 = 明确不支持**（`src/llm/httpapi/publish.go` 的 `browserUnsupported`；见 §9）。

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

**bridge 包文件职责**：

| 文件 | 职责 |
|------|------|
| `bridge.go` | 桥核心：instance 注册、订阅透传、`PublishEvent` 分派、`mqTypeMap`、src 防环（1s 窗口）、`EmitFrontend`、`WaitForEvent`（test-port） |
| `guimsg.go` | `gui.*` 本地面（复用原 `/call` 内部 `callX`，`/call` 已清零） |
| `data.go` | `data-*` 经总线 persist；prj-config 键值面；usr 配置（标量逐 key + 专用表 `llms`/`mcps`） |
| `local.go` | 初始化数据、布局/窗口/UI/文件树/打开态保存（prjusr）、最近目录、打开目录、VCS、检索 |
| `builtins.go` | `gui.system.builtins`：读 exe 同目录 `config.json` 的只读内置 MCP 项（`mcpServers` 段） |
| `toolchain.go` | `gui.toolchain.detect`：探测 6 语言工具链 + Chrome 路径/版本（系统级候选不落库）；探测顺序 = PATH → 注册表 `App Paths`（HKCU→HKLM）→ 环境变量目录（JAVA_HOME/GOROOT 等）→ 常见安装目录；候选环境变量支持 `%VAR%`/`$VAR`；**Chrome 版本来自其目录 `Application\<ver>\` 的最高版本号目录**（不执行 `chrome.exe --version`） |
| `capture.go` | 截图：隐藏本窗 → GDI 全屏 BitBlt → PNG 落盘 → `{url,b64}`（纯 syscall，无 CGO） |
| `upload.go` | 附件上传：base64 → **prjusr 数据根** `tmp/uploads/`（`bridge.uploadRoot()`；`--data-dir` 显式时 = 该数据根）→ `{file_id,name,path,url}` |
| `optimize.go` | 提示词 AI 优化：SSE 流式，事件 `optimize-token/done/error` |
| `compat.go` | 旧协议事件名兼发（旧回归脚本用） |

> **多窗口（🔵 待实施，[24-多窗口模型设计方案](../10-architecture/24-多窗口模型设计方案.md)）**：`Main` 由"单窗口"改为**窗口工厂**（每窗口独占 OS 线程 + `CoInitializeEx(STA)` + 独立 WebView2 `DataPath`）；`appHandler` 由单例改为**每窗口一个**（`role` = `main`/`chat`），使 `/publish`、`/show/`、`gui.window.status`、截图**绑定到调用来源窗口**；新增进程级**窗口注册表**（`windowID → {hwnd, role, sessionID}` + 反向索引 `sessionID → windowID`）承载"**一个 session 只允许一个窗口**"（已开 → 激活；主窗口当前会话 → 拒绝）。每窗口一个 `bridge`（各自 `instance_id` + 各自 `eval`）→ 事件隔离由既有 `acceptEventInstance` 提供，**不改 `eval` 扇出、不引入 `window_id`**。技术路径已 POC 验证（`TECH-POC/webview2-multiwin/`）；**关窗一律用 `Destroy()`**（`Terminate()` 跨线程无效）。

---

## 5. 预览区（CodeView / renderType 映射）

> 本节为 2026-08/09 原设计稿整体迁入，按现行实现口径整理（判定顺序与扩展名集合均以 `CodeView.vue` 为准）。

**组件职责**：

| 视图 | 组件 | 职责 |
|------|------|------|
| 主预览 | `CodeView.vue` | 多 tab 预览区：底部 tab 栏，按 `renderType` 分派渲染；功能页 tab（settings / scenario / dbviewer / tools / knowledge） |
| 二进制查看 | `HexView.vue` | `hex` 类型文件的十六进制展示 |

**renderType 判定**（顺序即源码顺序，`CodeView.vue::computeRenderType`）：

| 优先级 | 条件 | renderType | 渲染方式 |
|--------|------|-----------|----------|
| 1 | 路径以 `db://` 开头 | `none` | 不渲染（数据库查看器自行处理） |
| 2 | 备份文件（`~` / `~$` / `.swp` / `.swo`） | `unsupported` | 不支持提示 |
| 3 | 原语文件（`*.tool.md` / `*.skill.md` / `*.prompt.md` / `*.resource.md`） | `primitive` | 原语面板 |
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
| 预览页签栏溢出（`TabBar.vue`） | **2026-09-16 用户口径**：外层 `.tb-bar` = `position:relative; overflow:hidden`（裁剪）+ `ResizeObserver`（**同时观察 `.tb-bar` 与 `.tb-inner`**；另有 window `resize` 兜底）→ 尺寸/内容宽变化即重判；内层 `.tb-inner` = `display:flex; gap:2px; width:max-content; flex:none`（内容自然宽，**不收缩、不裁切、不隐藏**多余页签；不再 `flex:1`/`min-width:0`/`overflow:hidden`）；判定口径 = **按最后一个页签的位置**（`.tb-tab:last-child` 的右边界 > 外层可用宽右边界，即 `bar.right - paddingRight`，容差 0.5px；页签尚未渲染时退化为 `inner.scrollWidth > bar.clientWidth`）→ 决定是否显示 `.tb-more`（"..." 按钮，**绝对定位覆盖**在右侧：`right:0; top:50%` + 自带底色 + `inset` 左边框 + `z-index`，**不占布局宽、不参与判定回路**，故不会"有按钮→更挤→更多按钮"）；**多余页签不再隐藏**（`tb-hidden`/`visibleCount`/`visibleKeys`/`hiddenTabs`/`gotoHidden` 已删除）→ 被遮住的最后一个页签**仍渲染**（可被按钮遮住一半，属预期），**且同时出现在弹框里**。弹框 `.tb-more-pop` 列出**全部**页签（按当前内部顺序）；选中某项 → 该项移到**显示首位**、其余保持相对顺序顺移（**只改组件内部显示顺序**：`orderKeys`，`setFirst` 仅 `emit('update:modelValue')` + 关闭弹框，**不改 `props.tabs`、不 emit 排序事件、不额外通知外部**；`displayTabs` computed 在 `props.tabs` 变化时过滤已删 key、追加新增 key）。旧机制（`.tb-hidden` + 累加 `offsetWidth` 算可见数 + 隐藏多余项）**已删除** |
| 预览页签栏右键菜单 | **关闭本页签 / 关闭右侧 / 关闭其它 / 关闭全部**（`preview-tab-close-this` · `preview-tab-close-right` · `preview-tab-close-others` · `preview-tab-close-all`）。**收敛为单一 MQ 路径（2026-09-15 后订正）**：四项菜单项**只经 `v-mq` 触发**上述四个主题（`TabBar.vue` 触发 → `CodeView.vue` 订阅），**无 emit 本地直连副本**（`TabBar` 已不再 emit 本地动作）；属**前端客户端 mq** 路径，**零新增消息主题、不经后端**。文案：**「关闭全部」（zh）/「Close All Tabs」（en）**（`common.tab_close_all`） |

---

## 6. 数据结构与存储

| 数据 | 载体 | 当前落层 | 目标（见 [02-配置层级](../00-overview/02-配置层级.md)） |
|------|------|---------|------|
| `layout` / `window` / `opened-files` / `filetree-*` | prj config 键（`{"v":"<json>"}`） | **prjusr** | 已落 **prjusr**（个人运行态；读取 prjusr 优先、回落 prj） |
| 用户配置 `user_config` | usr config 表 | usr | usr（拆细 key） |
| `recent_dirs` | **桥内进程内存**（无 usr 自由键通道） | 内存 | 待定（跨进程持久化未实现） |

**文件树展开态（`filetree-expanded-key` / `filetree-selected-path`）**：只保存展开目录的相对路径数组与选中路径，重启时经 `init-data.expandedKeys`/`selectedKey` 恢复。原 `filetree-data` 整树快照（2026-08/09 设计稿）为**只写不读**，已于 2026-09-11 摘除（见 [42-决策记录](../40-roadmap/42-决策记录.md) F）。

**MCP 配置界面**（`views/config/SettingsMCPPage.vue` 列表 + `views/config/EditMCPDialog.vue` 编辑弹窗；落库 = usr `mcps` 表，见 [64 §6](../60-reference/64-配置项一览.md)）：编辑弹窗含「**按实例隔离**」Switch（`config.mcp.isolate`）——**三态表达**：未拨动 = **未设置**（保存时 `delete localData.isolate`，**不写库**）+ 提示「自动（未设置，按传输方式推断）」（`isolateAuto`，另附 `isolateHint`）；一旦拨动即视为**显式设置**并写 `true`/`false`（未显式设置时开关显示随 transport 变化的推断值，computed 推断、无 `watch`）。列表新增「按实例隔离」列（`isolateLabel`）：**显式设置 → 是/否**；**未设置 → 推断值 + 「（自动）」后缀**（`isolateAutoSuffix`；推断口径与 gateway `ServerEntry.IsolateEnabled()` 一致：stdio → 隔离、http/sse → 共享，transport 留空按连接点推断）。

**编辑弹窗页签（2026-09-27）**：`EditMCPDialog` 内容顶部改用 `Tabs` 拆「**基本信息**」（名称 / 传输方式 / 服务地址 / 命名空间 / 超时 / 描述 / 请求头）·「**运行信息**」（运行时 | 工作目录 / **按实例隔离** | **沙箱** / 启动参数 / 环境变量）·「**工具**」（【加载工具】+ 逐项勾选）共**三页签**（2026-09-27 改造；「**启用**」移到底栏与【保存】同行）；页签**仅显示分组**，`handleSave` 仍一次性提交三页全部字段。字段说明改用 label 右侧 `?` 图标 + `Tooltip`（「传输方式」= `config.mcp.transportHint`，新 `help` 图标）；「**分类**」输入项已摘除（`category` 后端字段保留，供 `servers.list` 分组）。「**沙箱**」（`config.mcp.sandbox`）由列表页列迁入「运行信息」页签（三态 Switch，同 `isolate` 口径），**仅 stdio 可开**：http/sse（及 auto 仅填 url）Switch **禁用** + 原因 tooltip（`config.mcp.sandboxStdioOnly`）；`SettingsMCPPage` 该列已摘除（编辑入口唯一 = 弹窗）。「**高频工具**」（2026-09-27 改造）**不再是字段**：改为独立「**工具**」页签 ——【**加载工具**】按钮按别名列出该 server 工具 + 「全部工具」= 全部 hot + 逐项勾选（数据源 = 既有 `tools-list`，**零新增消息面**）；写库仍为**契约原名**列表（`"*"` = 全部），仍随主对话框「保存」落 usr `mcps[].hot_tools`；原独立弹窗 `SetMCPHotToolsDialog.vue` **已删除**（并入页签）。另：「**请求头**」「**环境变量**」改为 **KV 行编辑**（`components/ui/KeyValueEditor.vue`，可增删行），「**启动参数**」为**行列表**编辑（`args` 是 `[]string`，非 KV）。

**保存时机**：① 每次展开/折叠后；② 收到变更推送后（防抖约 500ms）；③ 选中节点变化时；④ IDE 关闭时兜底保存。

---

## 7. 依赖

- **上游**：`src/lib`(mq/paths) · `src/llm`(server) · `src/filesys` · `src/plugin*` · `jchv/go-webview2`（本地 `lib/go-webview2`）。
- **indirect**：`src/data` · `src/gateway` · `src/mcp-server`。
- **下游**：无（终端应用）。

---

## 8. 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| 无 HTTP server | WebView2 `WebResourceRequested` 虚拟源拦截 | 免端口、免监听、资源不落盘 |
| 前端资源 no-cache | 强制 `no-cache, no-store, must-revalidate` | WebView2 会持久缓存旧页面 |
| Frameless 保留 WS_THICKFRAME | 无原生标题栏 + 系统八方向 resize | 自绘 toolbar + 原生 resize 体验 |
| 拖动用 `-webkit-app-region: drag` | CSS 区域拖拽，控件 `no-drag` | 免手写拖动逻辑 |
| `--test-port` 仅 127.0.0.1 | 外部脚本驱动端点 | 测试专用，见 [52-test-port驱动](../50-testing/52-test-port驱动.md) |

---

## 9. 场景与边界

- **窗口恢复归一化**：`normalizeWindowRect`（主显示器工作区 clamp、完全屏外居中、保证标题栏 ≥80px 可见）。
- **最大化同步**：`window-maximized-changed` 事件 → 前端切图标。
- **`/show` 路径越界防护**：`fileserver.withinDir`（Windows 大小写不敏感），`DataDirs` 为额外放行根。
- **防环**：本实例刚发布的主题（1s 窗口）不再回发前端。
- **多窗口（🔵 待实施）**：窗口 ↔ `instance` **1:1**（窗 = 视口，instance = 运行态+消息归属+数据根绑定）；**`instance` 不做业务隔离**（同 `work_dir` 多 instance 共用同一 prjusr 库 → 会话列表/历史**共享**）。**关闭语义**：关任一窗口仅销毁该窗口 + 发该实例 `instance-exit`；**主窗口关闭 = 退出本进程**（其余窗口一并关闭）。**几何**：主窗口 `window.*`/`layout.*`（prjusr）；**对话窗口不持久化几何**（每次默认位置）。**不变量订正**：原「desktop = 1 进程 1 instance」不再成立（进程内 instance 数 = 窗口数），见 [20-实例隔离与后端分离](../10-architecture/20-实例隔离与后端分离.md) 订正注。
- **退出**：`instance-exit` → persist 移除绑定 + server 取消 running turn + 释放锁；`beaconSaveLayout`（`sendBeacon`）兜底保存布局。
- **browser 形态的 native 能力边界（2026-09-20 补注）**：`gui.file.save`（「另存为」对话框，native）**明确不支持** —— 入 `src/llm/httpapi/publish.go` 的 `browserUnsupported`（返回 `{ok:false, error}` + errors，**不静默失败**，前端降级为页面下载 / 剪贴板）；browser 形态点分主题上行另有白名单 `allowedDottedPrefixes`（现含 `filesys.` / `llm.test-connection` / `memory.flush`）。GUI 形态无此限制（桥本地实现 + 「点分直通」分支）。见 [61 §1/§1.1/§8](../60-reference/61-消息一览.md) · [42 §2 (133)](../40-roadmap/42-决策记录.md)。

---

## 10. 现状与待办

- ✅ `/call` RPC 已清零（前端无 `window.go.*` 调用点）；`compat.go` 兼容层保留。
- ✅ `layout/window/opened-files/filetree-*` 已落 **prjusr**（个人运行态；读取 prjusr 优先、回落 prj；[02-配置层级](../00-overview/02-配置层级.md) §5）；`ui`/`opened-file` 死键与 `filetree-data` 快照已于 2026-09-11 摘除（G1/F）。
- ⚠️ `recent_dirs` 降级为进程内存，跨进程/重启持久化未实现。
- 🗄 `models/config.go` 的 `AgentConfig` 已废弃（project_agents 表 deprecated）。
- 🔵 工具栏「设置」改下拉菜单 + 配置页在 preview 打开（待改造）。
- 🔵 **预留功能（「敬请期待」、非死代码，2026-09-26 用户裁决，见 [42 §2 (175)](../40-roadmap/42-决策记录.md)）**：`fork`（会话导航占位按钮，`src/frontend/src/views/sessions/SessionsPane.vue`，点击仅提示 `chat.fork_coming_soon`／「敬请期待」）· `analyzeDialog`（主布局预留引用，`src/frontend/src/views/layout/MainLayout.vue`）。
- 🔵 **多窗口（主窗口 + 纯对话窗口）设计定稿、待实施**：任务分解见 [24 §7](../10-architecture/24-多窗口模型设计方案.md)（MW-1…MW-13）；含 **Resolve 审计**（多实例下 `View.Resolve` 的「唯一实例回退」失效 → 不带 `instance_id` 的数据入口须逐条排查）；数据层取 **B 方案**（desktop 走 `dataDir == ""`，见 [12 §3/§5.3](../10-architecture/12-数据层.md)）。未决 4 项见 [41 D-33~D-36](../40-roadmap/41-未决项登记.md)。

---

## 11. 关联测试

- `src/test/chonkpilot-gui/unittest/`（Go：`models/path_test.go` 等）
- `src/test/chonkpilot-gui/systest/`（Python：`run_all.py`、`run_llm.py`，经 `--test-port=2345` + mock LLM `127.0.0.1:8901`）

---

## 12. 实现约定（前端与宿主）

> 本节由原工程规约整体迁入（2026-09-11），为**强制约定**。

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

> **2026-09-28 方案 A（视觉 / 命中解耦，`components/split/SplitPanel.vue`）**：本体恒 **1px 发丝线**、命中区为 **5px 透明伪元素**，hover/拖拽只染伪元素 → **拖动瞬间不重排、不抖动**。旧口径（handle 4px + 常态 `transparent` + hover 直接改本体）**作废**，原文保留于文末。

1. **本体 = 真实 `<div>`**（`.split-resizer`），占布局空间 = **`var(--split-hairline)`（1px）**：水平 `width: var(--split-hairline)` + `cursor: col-resize`；垂直 `height: var(--split-hairline)` + `cursor: row-resize`。
2. **本体常态 `background: var(--border)`**（发丝线取**边框色**，不再「露容器底色」，亦非 `transparent`）。
3. **命中区 = `::after` 绝对定位伪元素**：`content: ''` + `position: absolute` + 常态 `background: transparent`（**不占布局、不改 1px 视觉**）。水平 `top/bottom: 0` + `left/right: calc((var(--split-hairline) - var(--split-hit)) / 2)`；垂直 `left/right: 0` + `top/bottom: calc((var(--split-hairline) - var(--split-hit)) / 2)` —— 即向两侧各外溢 `(hit − hairline)/2 ≈ 2px`，命中跨度 = `--split-hit`（5px）。
4. **hover / 拖拽激活（`.is-active`）只给 `::after` 填 `var(--accent)`** → 5px 蓝块；**本体恒 1px、绝不加宽**（原 `.is-dragging` 本体 `gap+2px` 加宽已移除）。
5. `@mousedown` 用 `.stop.prevent`（阻冒泡 + 默认行为）+ 闭包捕获起点（`clientX/Y` 与目标 pane 的 `getBoundingClientRect()`），不用全局 ctx。
6. `window mousemove` 设 `{ capture: true }`，按 `paneMin/paneMax` **clamp** 后**直改 DOM**（`targetEl.style[width|height]`）；`window mouseup` 移除 listener，恢复 cursor / userSelect。
7. 拖拽数学走 **pane 的 `getBoundingClientRect()`**（与 resizer 宽度无关）；被 resize 的 pane 加 `flex-shrink: 0`。

- **token（`variables.css` :root）**：`--split-hairline: 1px` · `--split-hit: 5px`；**`--split-gap` 已移除**（原 4px「视觉=命中」口径作废）。
- **`gap` prop**：`SplitPanel` **保留声明但不再参与视觉 / 命中宽度**（兼容既有调用方）；`MainLayout.vue` 外层 `:gap="0"`（不渲染 resizer）+ 4 处内层 `:gap="4"`。
- **`LAYOUT_GAPS`**：`MainLayout.vue` 由 **8 → 2**（= content\|chat + filetree\|preview 两条 1px 发丝线，与真实分隔条宽度一致；用于 filetree 宽度的 clamp 余量）。
- **已知取舍**：命中区对称外溢 2px → 会**压住相邻 pane 边缘 2px**（点该 2px 带优先命中 resizer）。
- **关联测试**：前端守卫 `src/frontend/test/uxLayoutSplit.test.js`（P1-6b：本体 1px / `--border` / 命中伪元素 5px / 激活 `--accent` / `--split-gap` 移除）；L4 `test_layout.py` **L1c**（真机分隔条几何与颜色）+ **L8b**（拖拽中本体恒 1px）。

> **原文保留（旧口径，2026-09-26 及更早）**：
> 1. handle 用真实 `<div>`（非 `::after`），占布局空间（`width/height: 4px`）。
> 2. `background: transparent`，hover 变 `var(--accent)`。
> 3. `@mousedown` 用闭包捕获 `startX/startW`，不用全局 ctx。
> 4. `window mousemove` 设 `{ capture: true }`，clamp 到阈值。
> 5. `window mouseup` 移除 listener，恢复 cursor / userSelect。
> 6. 每次 `e.preventDefault() + e.stopPropagation()`。
> 7. 被 resize 的容器加 `flex-shrink: 0`。

### 12.5 DialogShell 对话框高度

**禁止**只在内部 div 设固定高度（表头 + padding 会叠加溢出）。正确做法：用 `max-height` 约束弹窗根节点，内部内容区 `.dialog-body` 用 `flex: 1` 撑满（`overflow: auto` 内滚动、表头固定）：

```css
/* 自研 DialogShell：根节点 .dialog-shell 限高，内容区 .dialog-body 撑满并内滚动 */
.dialog-shell:not(.dialog-state-maximized) { max-height: calc(100vh - 40px); display: flex; flex-direction: column; }
.dialog-body { flex: 1; overflow: auto; min-height: 0; }
```

**〔订正（2026-09-26）：载体口径由 `el-dialog` 改为自研 `DialogShell`〕** 原文写 `el-dialog` + `dialog-content`，但**本项目前端无 element-plus 依赖、全仓 0 处 `el-dialog`**（已 Grep 复核），实际弹窗载体 = 自研 `components/dialog/DialogShell.vue`，其等价约束已内建：JS 侧 `base.maxHeight = min(options.maxHeight, calc(100vh - 40px))`（`DialogShell.vue` `:228-233`），CSS 侧 `.dialog-shell:not(.dialog-state-maximized){ max-height: calc(100vh - 40px) }`（`:462-464`）与 `.dialog-body{ flex:1; overflow:auto; min-height:0 }`（`:518-523`）。**实质要求不变**（必须用 `max-height` 约束弹窗 + 内部 `flex:1` 撑满内容区），仅换载体。原文保留：

> ~~**禁止**只在内部 div 设固定高度（header + padding 会叠加溢出）。正确做法：用 `max-height` 约束 `el-dialog`，内部 `dialog-content` 用 `flex: 1` 撑满：~~
> ```css
> .scenario-dialog :deep(.el-dialog) { max-height: 980px; display: flex; flex-direction: column; }
> .scenario-dialog :deep(.el-dialog__body) { flex: 1; overflow: hidden; display: flex; flex-direction: column; }
> .dialog-content { flex: 1; display: flex; flex-direction: column; overflow: hidden; min-height: 0; }
> ```

### 12.6 静态文件服务

嵌入式文件服务**必须加** `Cache-Control: no-cache, no-store, must-revalidate`，否则 WebView2 会持久缓存旧页面。

### 12.7 日志规范（宿主进程）

- 宿主用标准库 **`log/slog`**（键值对形式，如 `slog.Error("msg", "err", err)`），**禁止** `log.Printf` / `fmt.Print`。
- 级别：启动/关闭/关键流程 → `Info`；调试 → `Debug`；异常 → `Warn` / `Error`。
- 落盘：主进程当前**未配置文件 handler**（slog 默认输出 stderr）；而 GUI 形态为 `-H windowsgui`，stderr 不可见 → **调试信息必须显式落盘或上报**（如需文件日志，在宿主初始化处配置 handler）。**〔订正（2026-09-20）：该口径已过时 —— 宿主已挂文件 sink**，见下条订正注；原文保留。〕
- 若需排查运行时问题，优先用结构化字段而非拼串。

> **〔订正（2026-09-20）：日志落盘已统一到 `<dataDir>/logs/gui.log`〕**
> ① **宿主（gui）**：`src/gui/logfile.go` 的 `dualWriter`（stderr 并存）+ `rotateWriter`（2MiB 滚动、保留 5 份）把 **slog 默认 logger** 同时写 stderr 与 `<dataDir>/logs/gui.log`（[42 §2 (122)](../40-roadmap/42-决策记录.md)）；日志目录经 `gui.init-data` 只增字段 `logDir` 下发（[61 §1](../60-reference/61-消息一览.md)）。
> ② **后端（`src/llm/server` + 内嵌插件）**：本包输出收敛到单一出口 **`logf`**（`src/llm/server/log.go`）—— **`logf` 始终写 stdout（行为不变，`--test-port` 捕获口径不变）**；装配方经 **`server.Options.LogWriter`（`io.Writer`，nil = 仅 stdout）** 注入额外 sink 时**同写 sink**，GUI 注入的即宿主那一个 `<dataDir>/logs/gui.log` 文件 sink（`src/gui/main.go` 传 `logWriter.fileSink()`）。插件仍走 `plugin.Deps.Logf`（= `Server.pluginLogf`）→ 经**同一出口**落文件。库侧只收 `io.Writer`，**不依赖 gui 包**（[41 I-114](../40-roadmap/41-未决项登记.md)）。
> ③ **未覆盖（如实标注）**：`src/llm/httpapi` 与 `src/llm/main.go` 的 `log.Printf`（stderr；属独立入口 / browser 形态）未接入该 sink。
> **〔订正（2026-09-25，[24 §3.2](../10-architecture/24-多窗口模型设计方案.md) MW-8）：上条 ① 的 `<dataDir>` 已正名为 prjusr 数据根〕** 日志目录 = **`data.PrjUsrDir`（prjusr 数据根）** + `/logs`（`src/lib/gui/main.go` 传入 `attachFileLog`）：desktop 缺省（`data_dir == ""`）→ **`~/.chonkpilot/data/<prj-id>/logs/gui.log`**（跟随数据根，[41 D-33](../40-roadmap/41-未决项登记.md)）；**显式 `--data-dir`** → `<该数据根>/logs/gui.log`（口径不变）。`gui.init-data.logDir` 随之指向该处。~~`<dataDir>/logs/gui.log`~~（原文保留）。

### 12.8 主题与浮层样式（token 约定）

> 2026-09-16 立（暗色浮层配色修复的防回归约定）。**主题载体 = `documentElement` 的 `data-theme` 属性，非 class** —— 页面不存在 `.dark` 之类的 class，故主题相关选择器**必须**写属性选择器（`[data-theme="dark"] …` / `[data-theme="nord"] …`）；写成 `.dark` / `:root.dark` / `xxx.dark` 属**失效选择器**（编译通过但永不命中）。

- **主题集**：`light`（默认，`:root`）/ `dark` / `nord`（`src/frontend/src/assets/styles/variables.css`）。**无 `system`**（跟随系统未实现）；切换见 `Toolbar.vue setTheme()`。〔2026-09-19 订正：原 `frontend/src/assets/styles/variables.css`（含下文 §12.8 token 定义口径处）——前端工程已独立为 `src/frontend`，路径迁移，原文保留。〕
- **浮层（dialog / tooltip / popup / dropdown / 右键菜单）必须用主题 token，禁止硬编码浅色**：不得写死 `#fff` / `#f5f7fa` 之类浅色底色，或只在 `:root` 定义、暗色主题不覆写的自造 token。浮层底色一律引 `var(--panel-bg)` / `var(--bg-secondary)` / `var(--bg-primary)`，文字引 `var(--text-primary)` / `var(--text-secondary)`，描边引 `var(--border)`，hover 引 `var(--bg-hover)`。
- **浮层/强调/页签 token（`variables.css`，dark/nord 覆写）**：
  - `--tooltip-bg` / `--tooltip-color`：tooltip 底色/文字（light = 半透明黑 + 白字；dark/nord = `var(--bg-surface)` + `var(--text-primary)`），`Tooltip.vue` 的箭头用 `border-*-color: var(--tooltip-bg)` 同步。
  - `--accent-bg`：强调背景（light = `#ecf5ff`；dark/nord = `color-mix(in srgb, var(--accent) 18%, transparent)`），用于 `Button.vue` 的 `.b-btn.is-text:hover`、`AskUserContent.vue`、`AgentEditor.vue` 等 —— 避免暗色下刺眼浅蓝。
  - `--tab-inactive-fg`（**2026-09-24**，**2026-09-27 改绑语义色**）：页签**非激活**文字色，与激活态（`--text-primary` / `--accent`）保持可见差别。原为逐 scheme 的 `color-mix(in srgb, var(--text-primary) 78%, …)`；**2026-09-27 用户口径**改为直接绑语义次要色 `--fg-secondary`（三主题各自取值），不再用 `color-mix` 调和。消费方 = `TabBar.vue`（`.tb-tab` / `.tb-more-item`）与 `Tabs.vue`（`.b-tabs-item`）。
  - **语义文字色 `--fg-*`（2026-09-27 立）**：统一文字角色，**新增代码优先用这组名**，不再直接写 `--text-primary` / `--danger` / `--accent`。
    - `--fg-primary`（主要，黑）＝ `var(--text-primary)`；`--fg-important`（重要，红）＝ `var(--danger)`；`--fg-link`（可点击，蓝）＝ `var(--accent)` —— 三者是**别名**，只在 `:root` 声明一次，自动随主题。
    - `--fg-secondary`（次要，暖灰偏黄）：**逐主题字面值** —— 消费场景 = **除 title/input 以外的说明文字**（字段 hint、页面描述、空态副文案）与**页签非激活文字**。light `#6f6a5a`（`--bg-primary #f8f9fa` 上 5.12:1；用户原拟 `#7a7566` 仅 4.04 → 压深）· dark `#b8b2a0`（`#181825` 上 7.9:1）· nord `#c9c3b2`（`#3b4252` 上 5.8:1）。
    - `--fg-disabled`（无效，灰）：light `#adb5bd` · dark `#6c7086` · nord `#7b8699`（禁用态文字，豁免 4.5 门槛）。
- **`--bg-elevated` 为历史误用（未定义 token，已改）**：`FileTree.vue` / `MessageList.vue` 等处曾引 `--bg-elevated`（全仓无定义 → 声明无效、回落透明/浅色）→ 已改 `--bg-secondary`。新增代码不得再引该名。
- **色值混用 `color-mix`**：半透明 hover/淡色底统一用 `color-mix(in srgb, var(--token) N%, transparent)`（如 `TabBar.vue .tb-more-close:hover` 用 `var(--text-primary) 8%`），以便随主题 token 自适应。
- **DialogShell 覆写范式**（`components/dialog/DialogShell.vue`）：`.dialog-overlay` 与 `.dialog-shell` 是**兄弟节点**，需各自命中；暗色覆写以 `[data-theme="dark"] .dialog-shell, [data-theme="dark"] .dialog-overlay, [data-theme="nord"] …` 成组声明，内部只重绑 `--dialog-*` 变量到主题 token。
- **token 定义口径（唯一权威）**：只有 `src/frontend/src/assets/styles/variables.css` 的 `:root`（= light 默认，兼公共 token）/ `[data-theme="dark"]` / `[data-theme="nord"]` 三块中**声明过**的 `--x` 才算"已定义 token"；其他位置自造名一律不算（组件级可选覆盖钩子见下方专条）。
- **禁止引用未定义 token、禁止 `var()` 兜底硬编码**：`var(--x, <字面值>)` 若 `--x` 全仓无定义 → 恒落字面值、主题失效，属误用（同 `--bg-elevated`）。**2026-09-16 全量清理**（94 个前端文件做"使用 − 定义"差集，复核 0 残留）：

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
  | `--split-gap` | `4px` | **新增** `:root --split-gap: 4px`〔**订正（2026-09-28）：`--split-gap` 已移除** —— 改由 `--split-hairline`(1px) / `--split-hit`(5px) 承载（视觉/命中解耦），见 §12.4〕 |
  | `--dialog-bg/-border/-header-bg/-overlay-bg/-btn-hover-bg/-radius/-shadow/-header-padding/-body-padding/-text-color/-font-size/-font-family` | 亮色缺省字面值 | `DialogShell.vue` 在 `.dialog-shell` / `.dialog-overlay` 基规则内**声明 light 默认值**（dark/nord 覆写，范式同上条） |

  新增 token 一律落在 `variables.css`：语义随主题变的（`--warning-bg` / `--warning-border`）在 dark/nord 同段覆写，不随主题变的（尺寸/字号/圆角）只在 `:root` 声明一次。
- **组件级可选覆盖钩子**（如 `--split-hairline` / `--split-hit`、`--dialog-*`）：允许组件暴露为覆盖点，但**默认值必须写在其声明处**（`:root` 或组件基规则），`var()` 内不得再写兜底 —— 即"未定义 token + 兜底"零容忍。
- **实心填充的文字色**：`--danger` / `--accent` 作**背景**时，文字统一用主题最底层色 `--bg-secondary`（light 恰为纯白 = 历史 `#fff`，light 零变化；dark/nord = 主题最深底色）。深色主题下 `--danger` 本身很亮，白字对比度仅约 2.3:1。既有 `TabBar.vue .tb-close:hover`、`CodeView.vue .preview-selection-bar` 用等价的 `--bg-primary`，本轮不动。
- **nord `--danger` 调亮（2026-09-16）**：`#bf616a` → `#f0959e`（同色相 354°，仍属 nord 红）。实测（test-port 实跑 computed style + WCAG 计算）：面板 `--panel-bg` #2e3440 **3.05 → 5.65**；工具面 `--toolbar-bg` #3b4252 **2.46 → 4.55**；实心填充（`--danger` 底 + `--bg-secondary` 字）**3.05 → 4.55**；菜单面 `--bg-surface` #4c566a **1.80 → 3.34**（**客观达不到 4.5**：该面亮度 L=0.094，需 L≥0.60 的近白粉才达标，会丢失"危险"语义；最优解是菜单/右键浮层底改用 `--bg-secondary`（→4.55），但属另一处变更，本轮未动）。`dark #f38ba8`（面板 7.17 / 工具面 5.43）与 `light #dc3545`（白底 4.53）不改。

> **对比度实测表 —— 表格为当前实测值（2026-09-20）**（tokens 取 `src/frontend/src/assets/styles/variables.css` 三主题块，对比度按 WCAG 2.1 相对亮度公式计算；阈值：正文字/小字（11–13px）**≥4.5:1**，非文本图形/图标/状态圆点（WCAG 1.4.11）**≥3:1**）。数值可由 `src/frontend` 的 `npm test`（`test/uxBatch1.test.js` 打印 `[contrast] …` 行）复现。

**① 批 1（2026-09-20）改动的 token / 规则**

| 元素 | 规则 / 取值 | light | dark | nord | 阈值 | 改前 |
|---|---|---|---|---|---|---|
| 弱化文字 `--text-muted` | 对 `--bg-primary`（token 值：light `#6b7280` / dark `#a6adc8` / nord `#93a8c8`） | **4.59** | **7.37** | **5.16** | ≥4.5 | light `#6c757d` 4.45 · dark `#6c7086` 3.36 · nord `#81a1c1` 4.64（dark 不可读、nord 余量过小） |
| inline code | `--text-primary` on `--bg-tertiary` | **13.01** | **12.97** | **7.49** | ≥4.5 | 原 `color: black` + 浅色底（dark/nord 不可读） |
| 工具卡头 `.section-header` | `--text-secondary` on `--bg-tertiary` | **6.90** | **8.42** | **6.39** | ≥4.5 | 原硬编码浅色字（`#b8860b`/`#1565c0`/`#2e7d32`/`#7b1fa2`）+ 浅色底 |
| 状态徽标 `.status-badge` | `--text-primary` on `color-mix(--语义 22%, --bg-tertiary)`（5 类徽标 × 3 主题，取最低） | **9.63** | **7.18** | **4.83** | ≥4.5 | 原硬编码彩色字（`#e65100`/`#2e7d32`/`#c62828`…） |
| 任务详情错误条 `.td-error` | `color-mix(--danger 70%, --text-primary)` on `color-mix(--danger 12%, 面板底)` | **5.77** | **6.55** | **4.57** | ≥4.5 | `#f5222d` 3.60（全主题不随主题） |

**② 本轮（批 1 收口，2026-09-20）清掉的残留硬编码浅色**

| 元素 | 规则 / 取值 | light | dark | nord | 阈值 | 改前 |
|---|---|---|---|---|---|---|
| 超时裁决提示 `.arbitration-hint`（11px） | light = `color-mix(--warning 55%, --text-primary)`；dark/nord = `var(--warning)` 原色（`[data-theme=…]` 覆写） | **4.71** | **12.91** | **8.00** | ≥4.5 | `#e65100` 3.60 / 4.33 / 3.30（三主题**均不达标**） |
| 停止按钮 hover `.stop-btn:hover`（图标） | `var(--danger)`，底 = `color-mix(--danger 12%, transparent)` 叠 `--bg-tertiary` | **3.24** | **6.72** | **3.27** | ≥3（图形） | `#ff4d4f` 2.42 / 5.07 / 2.45 |
| 任务区裁决条取消键 `.await-btn-danger`（11px 文字） | `color-mix(--danger 70%, --text-primary)`，底 = 按钮底 `--bg-primary` | **6.49** | **8.08** | **6.88** | ≥4.5 | `#ff4d4f` 3.10 / 5.02 / 3.82 |
| 任务节点停止图标 `.node-stop` | `var(--danger)`，底 = `--bg-primary` | **4.30** | **7.08** | **5.65** | ≥3（图形） | `#ff4d4f` 3.10 / 5.02 / 3.82 |
| 任务详情状态圆点 `.td-status`（7px） | `is-done` → `var(--success)` / `is-error` → `var(--danger)` / `is-stopped` → `var(--text-muted)`（底取 `--bg-primary` / `--bg-secondary` 最低者） | **3.22 / 4.30 / 4.59** | **11.03 / 7.08 / 7.37** | **4.94 / 4.55 / 4.16** | ≥3（图形） | `#52c41a` 2.15 · `#f5222d` 3.87 · `#bfbfbf` 1.74（light；dark 下 success `#52c41a`、nord 下 error `#f5222d` 亦不随主题） |

**③ 扫描口径（残留归零，2026-09-20）**：`src/frontend/src/**/*.{vue,js,ts,css}`（**112 个文件**，去 HTML/CSS/JS 注释后）中，**本批/批 1 清理过的硬编码浅色字面量 `#e65100` / `#ff4d4f` / `#f5222d` / `#52c41a` 零命中**（`npm test` 的「④ 全仓扫描」用例守卫）。**明确保留项（附理由，不视为漏网）**：

- `color: #fff`（工具条窗口关闭按钮、popover active、设置页徽标/按钮等）：**实心强调色填充上的文字**，属上文「实心填充的文字色」专条口径（light 恰为纯白 → 零变化）；dark/nord 下若要更稳，后续按该专条改用 `--bg-secondary`（本轮不动，牵动多处视觉）。
- `#67c23a` / `#bfbfbf`（`src/frontend/src/composables/useTaskStatus.js` 的状态→图标配色映射）：既有单测 `test/useTaskStatus.test.js` **锁定「既有 4 态逐值不变」**（批 1 之外的显式决策），改动会破坏该契约 → 保留。
- `rgba(0, 0, 0, ·)` 一类中性叠底（弹层遮罩/阴影/hover）与 `rgba(255, 193, 7, ·)` + `#f0ad4e`（`MessageItem.vue .notify-row` 的 🔔 完成通知浅琥珀底/色条）：**装饰性叠底与色条，不承载文字对比度**（`.notify-row` 文字走 `--text-primary`）→ 保留。
- `--tooltip-bg: rgba(0, 0, 0, 0.8)` / `--tooltip-color: #ffffff`：**token 定义本体**（dark/nord 已覆写为主题面色），非误用 → 保留。

### 12.9 UI 一致性与交互口径（2026-09-27 用户口径）

> 立此节以固化一轮「界面一致性」收口（见 [42 §2 (188)/(189)](../40-roadmap/42-决策记录.md)）。新增/修改 UI 一律遵循；与此前分散决定冲突时以本节为准。

**① 对话框（`DialogShell`）**
- **默认高度**：未显式传 `height` 时取 `min(80vh, 640px)`（`types/dialog.js` 的 `DefaultDialogOptions`），调用方 `options.height` 可覆盖 → 消除「打开时高度跳动」。
  - **例外**：`MessageBox`（确认/输入）与 `AskUserContent`（问题数不定）显式 `height:'auto'`，保持内容自适应。
- **三段式结构**：头部固定 + `.dialog-body` 内滚动 + **footer 固定在 `.dialog-body` 之外**（不随 body 滚动）；`DialogShell` 提供**可选 `footer` 具名插槽**（缺省不渲染任何多余 DOM）。
- **有表格的弹窗** → 表格**自身内滚**，减少整体滚动。
- **页签内容与页签分割线留 0.5em 间距**（`Tabs` 的 body `padding-top: 0.5em`；如 MCP 编辑弹窗三页签）。

**② 按钮**
- **保存 / 取消 / 确定等操作按钮统一在弹窗底部按钮区，固定、不随内容滚动**（三段式：顶栏固定 / 中部滚动 / 底栏固定；底栏须为滚动容器的**兄弟节点**且 `flex-shrink: 0`）。
  - 覆盖编辑类（`EditLLMDialog` / `EditMCPDialog` / `TextEditDialog` / `ScenarioEditDialog`）与 **confirm / 选择类**（`MessageBox` / `DirPickerDialog`）；无滚动、内容自适应类（`MessageBox`、`AskUserContent`）底栏同样固定在内容之外。
- **主操作文案统一「确定」**（`common.ok`）；不再引用 `common.confirm` / `dialog.ok` 等重复语义键。
- **恢复类按钮统一「重置」**（配置/设置页与弹窗）；**PrimitivePanel 保留「恢复默认」**（资产语义，见 [42 §2 (183)](../40-roadmap/42-决策记录.md)）。
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
- **统一用自研 `Table` 组件**（`components/ui/Table.vue`）；**不再新增原生 `<table>`**（历史遗留两处 `SecurityConfig` / `ContextConfig` 已于 2026-09-27 迁移）。
- 页面骨架 = `height:100%; display:flex; flex-direction:column` → 头部固定 + 正文 `flex:1; overflow:auto; min-height:0`。
- **长表单页**（如上下文管理）允许正文整体滚一次；其中**列表/记录区自身内滚**，避免"滚到底才看到下一区块"。
- **有表格的页**：横向滚动由**页面正文区**承担 —— `Table` 的 wrapper 置 `overflow-x: visible`、正文区 `overflow: auto`、卡片 `min-width: max-content` 随表格变宽；**滚动条始终贴 preview 区边缘**（纵向右侧、横向底部），不会出现"要滚到表格底边才见到横向滚动条"。**该范式已在工具配置 / 工具沙箱 / 上下文管理 / 安全 / LLM 五页落地**（2026-09-27；`ContextConfig` 压缩内容记录列表仍自身内滚、`SecurityConfig` 表头仍 sticky）。
- 表格内**长文本单元格**按**像素宽度**截断（如工具配置页「工具内容介绍」`max-width: 200px` + 省略号），**不按字符数**截断。

**⑥ 布局与交互**
- **任务区（下区）默认收起**（`taskOpen` 默认 `false`，首屏减负；**布局持久化/恢复照旧**）。展开入口 = 顶部工具栏「任务」开关 + **聊天内工具调用卡片的「查看任务详情」**（打开面板并定位；仅当该调用有关联 `task_id` 时显示，纯同步工具不显示）。
- 顶部「任务 / 文件树 / 聊天」三开关 = **图标 + 文字**（`test_toolbar` 依赖 `.toolbar-right .tb-btn` 顺序，改动须同步用例）。

**⑦ 反馈**
- **保存类操作必须有可见提示**（成功与失败都要）。
- **失败必须有提示**（如截图失败，原为 `console.warn` 静默 → 已改 `message.error`）。
- 纯成功且非"保存"语义的操作（切场景 / 切模型 / 新建会话 / 切换面板显隐等）**不加**提示。
- **不加**首次使用引导：本工具面向已有 IDE（IDEA / Eclipse 等）使用经验的开发者。

### 12.10 状态栏（`views/statusbar/StatusBar.vue`）

> 2026-09-27 立（用户口径：「statusbar 可否显示 当前索引和 codebase 的状态？未启用，完成，N/total」）。
> 状态栏自左向右 = 记忆总 token 数（仅记忆库启用时显示）· **索引状态区**（恒显示）· DevTools 图标 · 撑开 · 语言切换（`LangSwitcher`）。

- **索引状态区**（`.sb-section.sb-index`）：内含 **两枚徽标**（`.sb-idx[data-engine="codegraph"|"vfts"]`，**图标 + 状态文本**：图标在左、状态文本/数字紧贴其右，图标取代原引擎名文本），紧凑单行（`white-space: nowrap` + `flex-shrink: 0`），**不换行、不改变状态栏高度**（`--statusbar-height`）。两枚徽标**恒显示**（「未启用」本身即信息）。图标取自既有图标集（codegraph = `collection`，vfts = `search`）——**不新增图标资源**。
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
- **悬停**：徽标**原生 `title`**（**不再用 `Tooltip` 组件**）——codegraph = 「点击设置：codegraph」、vfts = 「点击设置：Vfts 全文索引」（i18n 键 `statusBar.idx_open_codegraph` / `idx_open_vfts`，zh/en 齐备）。原 Tooltip 多行明细（引擎名/阶段/进度/索引文件数/最后索引/错误…）随本轮改版**整体移除**（`indexStatus.js` 的 `tip` 字段与 `idx_tip_*` i18n 键一并删除）。
- **点击**：每枚徽标经既有前端事件 `EventNames.projectConfigOpen`（`project-config-open`）打开项目配置页，并**带可选 payload `tab`**（codegraph 徽标 → `tab:'codegraph'`，vfts 徽标 → `tab:'vfts'`）→ `MainLayout.handleOpenProjectConfig` 透传 → preview 页签 `kind='settings-project'` → `CodeView` 落到功能页 → `ProjectConfig` 激活对应页签。**缺省/非法 `tab` → 默认页签（`security`），不报错**（向后兼容旧发送方）。**不新增 MQ 主题**（仅给既有 payload 增可选字段，见 [61 §6.1](../60-reference/61-消息一览.md)）。
- **异常兜底**：状态 JSON 解析失败/缺键 → 视为「未启用 / 未初始化」，静默（不抛错、不 `console.error`）。
- **关联测试**：前端守卫 `test/statusbarIndexStatus.test.js`（映射全状态 + 4 键 + 既有广播 + 徽标 = 图标+文本/无 Tooltip + 原生 title + 点击带 tab + 跳页签链 + 禁 watch + 零新增主题 + i18n 齐备）；L4 `test_statusbar.py::SB6`（真机：无键基线 / 索引中 N-total / 完成 / 失败 / 原生 title / 点击开项目配置并定位 CodeGraph、Vfts 页签 / 收尾还原）。

### 12.11 文件树：被索引排除条目灰显（2026-09-27）

> 用户口径：「如果设置了 gitignored，目录树显示时，判断是否是排除项目。如果是，则文本颜色变成灰色。」方案 = **后端判定**（只读面 `data-index-ignored`）、位置 = 文件树（目录树）。功能点见 [32 §FT-016](../30-function-points/32-文件树与文件操作.md)。

- **判据来源** = 只读数据面 `data-index-ignored`（[61 §3.6](../60-reference/61-消息一览.md)）：入参 = 当前**已加载节点**的 **workdir 相对路径**（`/` 分隔，一次批量、去重）；应答 = `{codegraph:{enabled,ignored}, vfts:{enabled,ignored}, truncated?}`。判定口径与引擎实际索引范围一致（祖先目录剪枝 / `!` 同层反选 / 内置强制排除）。前端封装 = `src/frontend/src/utils/indexIgnored.js`（纯逻辑 `toWorkdirRelPath` / `ignoredSetFromResult` + 批量请求 `fetchIndexIgnored`；`emit` 可注入便于单测）。
- **灰显规则**：仅统计 `enabled=true` 的引擎，两引擎 `ignored` 取**并集**（被任一已启用引擎排除即灰显）；引擎未启用 → 其 `ignored` 恒空 → **不灰**。
- **样式 token**：命中节点在 `TreeNode.vue` 的 `.tree-row` 上加语义类 **`is-ignored`**，**仅文本颜色** = `var(--fg-disabled)`（`--fg-disabled` 定义见 §12.8）。**只改颜色**——点击 / 双击 / 右键 / 拖拽等交互与布局均不变；另加原生 `title`（i18n `fileTree.index_ignored`：zh「已排除：不会参与索引」/ en「Excluded: not indexed」）。
- **判定时机**：树数据到手 / 每层目录加载（`loadDirChildren`）/ `filesys.changed` 合并后（新增节点补判）。**刷新** = 既有广播 `onDataRefresh('prj-config', …)`（排除规则 / 引擎开关变化）→ 清缓存**重判已加载节点**（已灰的可恢复不灰）。**禁 `watch` / `watchEffect`**。
- **请求克制与降级**：同一批路径只请求一次（单飞 + 已判定缓存）；`truncated=true` 时未判定节点不灰显、不缓存、不报错；请求失败 / 主题不可用 / 应答异常 → **静默降级为不灰显**。**零新增 MQ 主题**（只新增 `data-index-ignored` 一个只读面，见 [61 §3.6](../60-reference/61-消息一览.md)）。
- **关联测试**：前端守卫 `test/fileTreeIndexIgnored.test.js`（纯逻辑 + 请求/降级/topic/去重 + FileTree 判定时机/刷新/禁 watch + TreeNode `is-ignored`/`--fg-disabled`/title/交互保留 + 零新增主题 + i18n）；L4 `run_filetree_ignored.py`（真机：`.gitignore` 命中条目灰显（含子项祖先剪枝）+ 反选/无命中不灰 + 引擎未启用全不灰对照组）。

### 12.12 项目配置「文件历史」页签（`views/settings/HistoryConfig.vue`；2026-09-27 批次③）

> 页签 = 项目配置页签 **`history`**（[61 §6.1 前端本地事件](../60-reference/61-消息一览.md) `project-config-open{tab?}` 取值之一）。**页签名由「自动提交」改名「文件历史」**（i18n `projectConfig.history`：zh「文件历史」/ en「File History」；旧「自动提交」语义已废弃）。后端语义见 [28-plugins §3.2](28-plugins.md)，配置键见 [64 §4](../60-reference/64-配置项一览.md)。

分三区块（自上而下）：

- **① 启用开关**（`history.enabled`）：单 `Switch`；**缺省关闭**（仅显式 `"true"` 视为开）；**开启前 `confirm` 二次确认**（说明每次工具调用前会打 checkpoint、大工程可能影响效率）；无 git 时 **disable + 原因提示**（`historyConfig.no_git_installed` / `no_git_repo`，判据 = `gui.vcs.info` 的 `gitInstalled` / `git`）。保存即生效（订阅既有 `data-prj-config-refresh`）。
- **② 保留策略**：两个数字输入 —— **保留个数**（`history.checkpoint_keep`，默认 500）/ **保留天数**（`history.checkpoint_ttl_days`，默认 7，锚点 = 链上最新检查点时间）；共用一个【保存】按钮（**正整数校验**，非法不写库并显式报错）；任一超限即修剪（后端口径）。
- **③ 检查点时间轴（只读，**按当前会话**）**：
  - **当前会话** = 活动会话（既有 `data-session-active-get`）→ `chainSlug`（与后端同口径）= 链 slug；只读**该会话**的键（I-135）。
  - **状态条**（`data-history-status`，读 `history.status.<slug>`）：**模式**（`active` 正常 / `fused` 已熔断放行 / `off` 未启用）· 检查点数 · 占用体积（人类可读）· 最近打点时间 · 最近耗时 · 失败次数 · 有未打点变更时附「有未打点变更」标记 · 最近错误非空时红字一行。
  - **列表**（`data-history-timeline`，读 `history.timeline.<slug>`）：列 = **序号**（相对编号，最新 `-1`，由数组索引派生、顺序不反转）/ 时间 / 工具 / 来源会话 / 文件数 / 增删（`+n`/`−m`）；**最新在前、≤200 条**；空态可区分「未启用」（`empty_disabled`）与「暂无检查点」（`empty_none`）。
  - **【清空本会话历史】**按钮（`data-history-clear`，列表非空才可用）：**二次确认**后写 `history.clear` = **JSON `{"ts","session":"<当前会话 slug>"}`** → 后端**只清该会话的链**（`refs/chonkpilot/<slug>`；其它会话链保留，I-136）；成功给可见反馈。
- **只读边界（有意为之）**：检查点时间轴**不提供手动恢复、不做 diff 展开**（恢复/查看差异属 LLM 侧 4 个 `history_*` 工具的职责）。
- **数据来源与刷新**：`history.enabled`/`keep`/`ttl`/`clear` 为 **prj 键**、`history.status.<slug>` / `history.timeline.<slug>` 为**会话级内部键（落 prjusr）**，统一经既有 `data-prj-config-list`（合并 prjusr）读取、`data-prj-config-save` 写入；刷新走统一机制 `usePrjConfigRefresh`（4 精确键 + **2 会话级前缀** `history.status.` / `history.timeline.`；按键过滤 + 突发合并 + 保存期跳过）；派生用 `computed`（**禁 `watch`/`watchEffect`**）；解析失败静默降级（不刷 `console.error`）。**零新增 MQ 主题**（`event-names.js` 无 history 通道）。
- **关联测试**：前端守卫 `test/historyCheckpoint.test.js`（纯逻辑 `utils/historyTimeline.js`：JSON 解析兜底 / 相对编号 `-1` / `active|fused|off` 归一 / 体积·时间格式化 / 默认 500·7 / **`chainSlug` 与会话级键名** · 源码守卫：两设置项键名与写库、**正整数校验先于写库**、**按当前会话读 status/timeline（活动会话 + 前缀订阅）**、禁 watch、`history.clear` 写 **JSON `{ts,session}`** + 二次确认、零新增主题、i18n zh/en 键集一致且「保留口径 / 不向分支提交 / 效率 / 工具调用前打点 / **本会话清空**」文案齐备、页签名「文件历史」）；L4 `run_hist_git.py`（真机产物断言见 [51 §2](../50-testing/51-FP与测试映射.md)：H1/H4/H6）。

