# go-webview2 内嵌 fork 台账（D-46）

> 本目录是 **jchv/go-webview2 的内嵌 fork**（非 `vendor/`、非 git submodule），随源码入库。
> 本文记录**来源 / 版本 / 本地补丁清单 / 升级路径**，补齐此前无台账的缺口（供应链视角⑯）。

## 1. 来源

| 项 | 值 |
|----|----|
| 上游仓库 | `https://github.com/jchv/go-webview2` |
| module path | `github.com/jchv/go-webview2`（**保持与上游同名**，靠各消费方 `go.mod` 的 `replace` 指向本内嵌目录） |
| 引入方式 | 内嵌快照（文件直接入库），无 submodule / 无 vendor |
| 消费方 `replace` | `src/lib/gui/go.mod` · `src/gui/go.mod` · `src/desktop/go.mod`（`=> ../lib/go-webview2`） · `src/test/chonkpilot-gui/unittest/go.mod`（`=> ../../../lib/go-webview2`） |

## 2. 版本（基线）

- `go.mod` 声明：`go 1.16`；依赖 `golang.org/x/sys v0.0.0-20210218145245-beda7e5e158e`（**2021-02-18 快照**）、
  `github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1`。
- **精确上游 commit / tag 未留档**（内嵌快照引入时未记录）。据 `go 1.16` + `x/sys 2021-02` 可推定基线为
  **2021 年前后的上游版本**；若需精确回溯，建议按下列第 4 节以「diff 上游历史」方式重建对应 commit（待补）。
- 内嵌 `webviewloader/sdk/{x64,x86,arm64}/WebView2Loader.dll` 三份（`//go:embed`，随源码入库，见 `.gitignore` 负模式例外）。

## 3. 本地补丁清单（相对上游的改动）

> 上游基线未精确固定，下列为**本仓已识别**的本地改动；均带中文注释、可据注释与 `D-xx` 标记定位。

| # | 类别 | 位置 | 说明 |
|---|------|------|------|
| P1 | 生命周期 | `webview.go`（`windowContext` / `delWindowContext` / `wndproc` WM_DESTROY） | 窗口销毁（WM_DESTROY）清理 `windowContext` 条目，避免多窗口反复开关时 `*webview` 永久泄漏（**D-28**）；WM_NCCREATE 期间登记 `*webview`（使首个 WM_NCCALCSIZE 即按 frameless 处理）。 |
| P2 | 窗口样式 | `webview.go`（`WebViewOptions.Frameless` / `wndproc` WM_NCCALCSIZE / `CreateWithOptions`） | 无边框窗口支持：WM_NCCALCSIZE 返回 0 隐藏原生标题栏/边框；保留 `WS_OVERLAPPEDWINDOW` 使原生 resize/最大化/贴靠保持；最大化时把客户区钳到监视器工作区。 |
| P3 | 窗口样式 | `webview.go`（`WebViewOptions.Hidden` / `Show` / `WebViewLifecycle`） | 建窗后可先隐藏，导航完成后 `Show()`，避免首屏白闪；`WebViewLifecycle.SetNavigationCompletedCallback` 供宿主挂导航完成回调。 |
| P4 | 窗口样式 | `webview.go`（`WindowResizer` / `StartResize`） | 前端命中边缘后调用 `StartResize(HT*)` 进入系统原生 resize 模态循环。 |
| P5 | DevTools | `webview.go`（`WebViewOptions.DevToolsDisabled` / `DevTools` / `OpenDevToolsWindow`） | 屏蔽用户 F12/右键菜单开 DevTools，同时保留宿主机**程序化**打开能力（`DevTools.OpenDevToolsWindow`）。 |
| P6 | 加速键 | `webview.go`（`Accelerator` / `SetAcceleratorKeyCallback`） | 拦截加速键（如 F12），返回 true 表示吞掉、false 交 WebView2 处理。 |
| P7 | 脚本 | `webview.go`（`ScriptEval` / `EvalWithResult`） | 同步执行 JS 并取回 JSON 结果（阻塞至完成回调或超时）；**不可在 UI 线程调用**（会死锁）。 |
| P8 | 截图 | `webview.go`（`Screenshot`） | 经 `CapturePreview` 取页面 PNG 字节（阻塞至完成或超时）；**不可在 UI 线程调用**。 |
| P9 | 健壮性 | `webview.go`（`NewWithOptions`） | Settings 获取/设置失败由 `log.Fatal`（整进程退出）改为**记日志 + 返回 nil**（走既有单窗口失败收敛路径，**D-36**）。 |
| P10 | 配置 | `webview.go`（`WebViewOptions.DataPath` / `WindowOptions`） | 支持注入 WebView2 用户数据目录（多窗口 profile 隔离）与窗口标题/尺寸/图标/居中。 |
| P11 | 布局 | `webview.go`（`wndproc` WM_MOVE / WM_MOVING） | 窗口移动时调用 `NotifyParentWindowPositionChanged`，使 WebView2 控件跟随。 |
| P12 | 权限 | `webview.go`（`NewWithOptions`） | 授予 `ClipboardRead` 权限（富文本粘贴场景）。 |
| P13 | 供应链 | `webviewloader/` | `//go:embed` 内嵌三架构 `WebView2Loader.dll`，运行期磁盘加载失败时回退内存加载（`module.go`）。 |

> 说明：`pkg/edge/*`、`internal/w32/*` 为上游 WebView2 COM 绑定层；本地改动集中在 `webview.go` 的
> 窗口/生命周期/能力接口，`pkg/edge` 侧仅在需要支撑上述能力处有配套扩展（如 `CapturePreview`、
> `ExecuteScriptWithResult`、`Show`、`AcceleratorKeyCallback`）。

## 4. 升级路径

1. 取上游目标版本：`git -C <upstream-clone> log`/`tag` 定位与 `go.mod` 基线相近的 commit，或直接取最新 release。
2. **diff 归因**：`diff -ru <upstream> src/lib/go-webview2` 得到全部差异 → 对照第 3 节清单，区分
   「上游已修（可丢弃本地补丁）」与「本地仍需（须重新套用）」。
3. 重新套用本地补丁（P1~P13），保留中文注释与 `D-xx` 标记；同步更新 `webviewloader/sdk/*` DLL（如需）。
4. 构建/回归：`go build ./...`（本模块，Windows/CGO）；消费侧（`src/lib/gui`、`src/desktop`、`src/gui`、
   `src/test/chonkpilot-gui/unittest`）跑各自 `go build ./...` 与 GUI 单测；`build-desktop.ps1` 产物验证。
5. 更新本台账：修订第 2 节版本记录（补上**精确 commit/tag**）与第 3 节补丁清单。

## 5. 维护约定

- **不在本目录引入新依赖**（保持与上游一致，降低升级冲突面）。
- 新增本地补丁须**同时**在此台账第 3 节登记（编号 + 位置 + 说明），并在代码处以中文注释 + `D-xx` 标注。
- 命名空间/API 尽量与上游兼容；能力扩展优先以**独立接口**（如 `ScriptEval`/`Screenshot`）叠加，
  而非改动 `WebView` 基础接口，便于升级时保留。
