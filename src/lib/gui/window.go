//go:build windows

package gui

// 窗口工厂（24 §4.1 / MW-1）：每个窗口**独占一个 OS 线程** + 在该线程上**自建 STA** +
// **独立 WebView2 DataPath**，随后 Run() 阻塞在该线程。主窗口与对话窗口同走本工厂
// （差异由 windowSpec 分派：标题 / Frameless / URL / 几何恢复）。
//
// 技术依据（`TECH-POC/webview2-multiwin`，已验证同进程 n=4 全绿）：
//   - fork 的 pkg/edge 只在 **package init()** 里 CoInitializeEx(STA) → 只钉住主线程；
//     主线程之外新建的窗口线程必须自行 STA，否则 WebView2 环境创建失败；
//   - Run() 是阻塞 GetMessageW 循环，Dispatch 走 PostThreadMessageW 到**创建时线程**；
//   - 关闭窗口一律 Destroy()（投 WM_CLOSE 给窗口自身线程）；**不可跨线程调 Terminate()**
//     （它走 PostQuitMessage，投递到调用线程，跨线程无效）。

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"unsafe"

	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-gui/bridge"
	"github.com/chonkpilot/chonkpilot-gui/internal/fileserver"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-llm/server"
	webview2 "github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

// 窗口角色（MW-2：appHandler.role 取值）。
const (
	roleMain = "main"
	roleChat = "chat"
)

// maxChatWindows 是独立对话窗口数量上限（24 §8 U-5 已决：≤5，不含主窗口）。
const maxChatWindows = 5

// coInitApartmentThreaded = COINIT_APARTMENTTHREADED（0x2；WebView2 要求 STA，24 §4.1）。
const coInitApartmentThreaded = 2

// swpNoSizeNoActivate = SWP_NOSIZE(0x1) | SWP_NOACTIVATE(0x10)：只挪位置、不改尺寸、不激活。
const swpNoSizeNoActivate = 0x0011

var (
	ole32                   = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx      = ole32.NewProc("CoInitializeEx")
	procCoUninitialize      = ole32.NewProc("CoUninitialize")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
)

// hostEnv 是窗口工厂的共享装配上下文（进程级，只读）：总线 / 内嵌 dist / 形态 / 内嵌 server
// 由 Main 一次装配，各窗口按需取用（桥与 appHandler 是**每窗口一份**，见 createWindow）。
type hostEnv struct {
	bus            mq.Bus
	distFS         fs.FS // 已 fs.Sub 到 dist 根（embed 前缀 frontend/dist 已剥离）
	workDir        string
	dataDir        string // 数据层分支判据（desktop 缺省为空，12-数据层 §3）
	projectDataDir string // 项目数据根（prj 层；fileserver 白名单用）
	prjUsrRoot     string // prjusr 数据根（附件/截图落盘根；[24 §3.2] MW-8）
	logDir         string
	form           string
	builtinLLMs    []bridge.BuiltinLLM
	srv            *server.Server // nil = --no-server（UI 注入测试模式）
	windows        *windowRegistry
	// testSrv 是 --test-port 的测试通道（未传 --test-port 时为 nil）：对话窗口建窗时经
	// RegisterChat 登记为可选目标（window_id 路由，24 §4.5）。由主窗口 onWired 接线后赋值。
	testSrv *testServer
}

// windowHost 是一个窗口的宿主句柄（每窗口一份：桥 / appHandler / 句柄 / 生命周期）。
type windowHost struct {
	role      string // roleMain / roleChat
	windowID  string // 对话窗口的注册 id（主窗口为空）
	sessionID string // 对话窗口绑定的会话（主窗口为空）

	w        webview2.WebView
	br       *bridge.Bridge
	handler  *appHandler
	chromium *edge.Chromium
	hwnd     uintptr
	wvData   string // 本窗口 WebView2 用户数据目录（关闭时清理）

	// onReady 由主窗口接线时挂上（--test-port 的就绪门控）；对话窗口为 nil。
	onReady func()

	done chan struct{} // Run() 返回且收尾完成后关闭
}

// destroy 关闭本窗口（投 WM_CLOSE 给本窗口线程；跨线程安全）。
func (h *windowHost) destroy() { h.w.Destroy() }

// windowSpec 描述一个待建窗口。
type windowSpec struct {
	role      string
	windowID  string
	sessionID string
	title     string
	url       string
	frameless bool
	// onWired 在**窗口线程**上、webview 建好（句柄已解析、桥与 handler 已就绪）后、
	// 导航前调用（主窗口用于 --test-port 接线）；返回错误 → 中止建窗。
	onWired func(h *windowHost) error
}

// startWindow 建窗：窗口创建（含全部装配）在**独立 goroutine + 独占 OS 线程**上同步完成；
// 本函数在创建完成后返回（失败返回 error），随后该线程继续 Navigate + Run 阻塞。
func startWindow(env *hostEnv, spec windowSpec) (*windowHost, error) {
	type result struct {
		host *windowHost
		err  error
	}
	ready := make(chan result, 1)
	go func() {
		// 关键①：本窗口独占一个 OS 线程。
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		// 关键②：在该线程上初始化 STA（fork 只在 package init 对主线程做过一次）。
		hr, _, _ := procCoInitializeEx.Call(0, coInitApartmentThreaded)
		defer procCoUninitialize.Call()
		if hr != 0 && hr != 1 { // S_OK=0 / S_FALSE=1（已初始化）为正常
			slog.Warn("CoInitializeEx(STA) failed", "role", spec.role, "hr", fmt.Sprintf("%#x", hr))
		}

		h, err := createWindow(env, spec)
		if err == nil && spec.onWired != nil {
			err = spec.onWired(h)
		}
		if err != nil {
			if h != nil {
				h.cleanup()
			}
			slog.Error("window create failed", "role", spec.role, "err", err)
			ready <- result{err: err}
			return
		}
		slog.Info("window created", "role", spec.role, "window_id", spec.windowID,
			"session_id", spec.sessionID, "instance_id", h.br.InstanceID(),
			"hwnd", fmt.Sprintf("%#x", h.hwnd))
		ready <- result{host: h}

		h.w.Navigate(spec.url)
		h.w.Run() // 阻塞：本窗口自己的消息循环
		slog.Info("window closed", "role", spec.role, "window_id", spec.windowID)
		h.finish(env)
	}()
	r := <-ready
	return r.host, r.err
}

// createWindow 在**调用线程**（= 本窗口的独占线程）上创建 webview 并完成全部装配：
// 独立 DataPath → 独立桥（instance_id + eval）→ 独立 appHandler → 拦截器 / lifecycle 回调。
func createWindow(env *hostEnv, spec windowSpec) (*windowHost, error) {
	// 每窗口一份 WebView2 用户数据目录：fork 默认 DataPath = %AppData%\<exe名>，多窗口共享
	// 同一 profile 会共享浏览器进程，任一方销毁/崩溃波及另一方（历史实测：销毁一个实例会
	// 冻结其他窗口）。前端 ui.locale 有 DB 兜底（MainLayout），不依赖 WebView2 缓存留存。
	wvData := webviewDataDir(newUUID())
	if err := os.MkdirAll(wvData, 0o755); err != nil {
		slog.Warn("webview2 data dir create failed", "dir", wvData, "err", err)
	}
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     true,
		AutoFocus: true,
		DataPath:  wvData,
		// DevTools 用户入口屏蔽（B3）：F12 / Ctrl+Shift+I 快捷键与右键菜单 Inspect 不再打开
		// DevTools（宿主仍可程序化打开 → 状态栏调试图标 gui.devtools.open）。Debug 保留 true
		// 使默认右键菜单（复制/粘贴等）仍可用。
		DevToolsDisabled: true,
		// 主窗口 Frameless：无原生标题栏/边框（WM_NCCALCSIZE=0），HTML 自绘标题栏；保留
		// WS_THICKFRAME → 系统八方向 resize / 最大化 / 贴靠保持原生（与旧 chonkpilot 一致）。
		// 对话窗口用**原生标题栏**（最大化/最小化/关闭/拖动/八方向 resize 全由系统提供，
		// 零代码，24 §1.1）。
		Frameless: spec.frameless,
		WindowOptions: webview2.WindowOptions{
			// 主窗口 Taskbar 标题 = 固定 `chonkpilot-<工程目录名>`（不随会话变化，见 mainWindowTitle）；
			// 对话窗口 = 缺省会话标题（见 chatWindowTitle）。
			Title:  spec.title,
			Width:  1280,
			Height: 800,
			IconId: 2,
		},
	})
	if w == nil {
		_ = os.RemoveAll(wvData)
		return nil, errors.New("webview2.NewWithOptions returned nil")
	}
	h := &windowHost{
		role: spec.role, windowID: spec.windowID, sessionID: spec.sessionID,
		w: w, wvData: wvData, done: make(chan struct{}),
	}
	h.hwnd = hwndOf(w)

	// ── 每窗口一个桥（MW-3）──────────────────────────────────────────────
	// 各自 instance_id（运行态分区 + 消息归属过滤）+ 各自 eval（→ 各自窗口）：后端 → 各前端
	// 由既有 acceptEventInstance 过滤免费提供（无需改 eval 扇出），窗口间前端事件不互通。
	h.br = bridge.New(newUUID(), env.workDir, env.dataDir, func(script string) {
		w.Dispatch(func() { w.Eval(script) })
	}, env.bus)
	// 文件日志目录下发前端（init-data 只增字段 logDir；未挂 sink 时为空 → 字段缺省）。
	h.br.SetLogDir(env.logDir)
	// prjusr 数据根注入桥（附件/截图落盘根；[24 §3.2] MW-8）。
	h.br.SetPrjUsrRoot(env.prjUsrRoot)
	if env.srv != nil {
		// 认证域（61 §4.6；阶段 2b-2）：首屏 authed 由服务端读桥持有的令牌判定。
		h.br.SetAuthCheck(env.srv.Authenticated)
		// config 类 data-* 走数据门面（同进程直调）；未接线（-no-server）时回落总线转发。
		h.br.SetFacade(inline.New(env.bus))
	}
	// 只读 LLM 条目注入桥（LLM 配置页顶部「系统默认（启动参数）」只读行，见 64 §11.1）。
	h.br.SetLLMBuiltins(env.builtinLLMs)
	// B3：DevTools 用户入口已在 WebViewOptions 屏蔽；把「程序化打开」能力注入桥
	// （gui.devtools.open）。OpenDevToolsWindow 走 Dispatch 派发到 WebView2 UI 线程。
	if dt, ok := w.(webview2.DevTools); ok {
		h.br.SetDevToolsOpener(func() { w.Dispatch(func() { dt.OpenDevToolsWindow() }) })
	}
	if err := h.br.Start(); err != nil {
		h.cleanup()
		return nil, fmt.Errorf("bridge start: %w", err)
	}

	interceptor, ok := w.(webview2.WebResourceInterceptor)
	if !ok {
		h.cleanup()
		return nil, errors.New("webview does not support WebResourceRequested")
	}
	h.chromium = interceptor.Chromium()
	h.br.SetHWND(h.hwnd)

	// ── 每窗口一个 appHandler（MW-2）────────────────────────────────────
	// 拦截器天然 per-webview → /publish、/show/、静态资源都绑定到**来源窗口**；故窗口定向
	// 命令（gui.window.status / 截图 / DevTools / set-title）天然只作用于本窗口，无需 window_id。
	h.handler = &appHandler{
		distFS: env.distFS,
		fileHandler: &fileserver.FileShowHandler{
			WorkDir: env.workDir,
			// 放行根（[24 §3.2] MW-8）：① 项目数据根（prj 库/旧附件等，`<workDir>/.chonkpilot`
			// 或显式 --data-dir）② prjusr 数据根（迁走后的附件/截图 `~/.chonkpilot/data/<prj-id>`）。
			// 两者同列，保证迁移前后资源均可经 `/show/` 取回（WorkDir 自身另由 WorkDir 字段放行）。
			DataDirs: []string{env.projectDataDir, env.prjUsrRoot},
		},
		br:          h.br,
		hwnd:        h.hwnd,
		destroy:     h.destroy,
		form:        env.form,
		requireAuth: env.srv != nil && env.srv.RequireAuth(),
		authed:      h.br.Authed,
		env:         env,
		role:        spec.role,
		windowID:    spec.windowID,
		sessionID:   spec.sessionID,
	}
	if spec.role == roleChat {
		// gui.window.set-title：**仅独立对话窗口**（主窗口标题不变，61 §1）。
		h.handler.setTitle = func(title string) { w.Dispatch(func() { w.SetTitle(title) }) }
	}

	interceptor.AddWebResourceRequestedFilter("*", edge.COREWEBVIEW2_WEB_RESOURCE_CONTEXT_ALL)
	interceptor.SetWebResourceRequestedCallback(func(request *edge.ICoreWebView2WebResourceRequest, args *edge.ICoreWebView2WebResourceRequestedEventArgs) {
		uri, err := request.GetUri()
		if err != nil || !strings.HasPrefix(uri, appOrigin) {
			return
		}
		resp, err := serveWebResource(h.handler, h.chromium, request, uri)
		if err != nil || resp == nil {
			return
		}
		_ = args.PutResponse(resp)
	})

	if lifecycle, ok := w.(webview2.WebViewLifecycle); ok {
		lifecycle.SetNavigationCompletedCallback(func(sender *edge.ICoreWebView2, args *edge.ICoreWebView2NavigationCompletedEventArgs) {
			// https 虚拟宿主 + WebResourceRequested：主框架导航会报 success=false
			// （浏览器层证书/DNS 校验失败），但内容已由拦截器返回并渲染 —— 不据 success 判定，
			// 与改前一致：视为内容就绪，置 ready 并显示窗口。
			if h.onReady != nil {
				h.onReady()
			}
			if spec.role == roleMain {
				// 主窗口：恢复上次位置/尺寸/最大化（含超屏正常化）；
				// 对话窗口**不持久化几何**（24 §3.3 C5），按缺省位置打开。
				restoreWindowFromConfig(h.br, h.hwnd)
			}
			lifecycle.Show()
		})
	}
	return h, nil
}

// finish 是窗口关闭后的收尾（在本窗口线程上执行）：本实例注销（instance-exit）→ 清理
// WebView2 profile → 对话窗口出表并广播 gui.window.closed。
//
// 注：**不**关闭总线 —— 多窗口共享同一条进程级总线（24 §4.1），总线由 Main 在进程收尾时
// 统一关闭（Bridge.CloseInstance 只注销本实例）。
func (h *windowHost) finish(env *hostEnv) {
	h.br.CloseInstance()
	_ = os.RemoveAll(h.wvData)
	if h.role == roleChat {
		env.windows.remove(h)
		// 关闭的窗口不再作为 --test-port 目标（多窗口路由表同步出表）。
		env.testSrv.UnregisterChat(h.windowID)
	}
	close(h.done)
}

// cleanup 是**建窗失败**时的回滚（销毁 webview + 清理 profile + 注销可能已建的实例）。
// 注意：调用点在 Run() 之前（建窗期），故 Destroy 之后必须**在本线程把消息泵跑起来**
// （Run 到窗口真正销毁即返回），否则会残留一个「无消息泵的可见窗口」。
func (h *windowHost) cleanup() {
	if h.w != nil {
		h.w.Destroy()
		h.w.Run()
	}
	if h.br != nil {
		h.br.CloseInstance()
	}
	if h.wvData != "" {
		_ = os.RemoveAll(h.wvData)
	}
}

// activateWindow 激活已存在的对话窗口（gui.window.open-chat 幂等分支，24 §4.3）：
// 恢复（去最小化）+ 前置，**不**新建、**不**切主窗口。
func activateWindow(hwnd uintptr) {
	showWindow(hwnd, SW_RESTORE)
	_, _, _ = procSetForegroundWindow.Call(hwnd)
}

// placeChatWindow 把新开的对话窗口错开放到主窗口右下方向（24 §1.1：对话窗口不记录几何，
// 每次按缺省位置打开并**避开主窗口**）；idx = 本窗口在对话窗口中的序号（级联步长）。
func placeChatWindow(main *windowHost, h *windowHost, idx int) {
	if main == nil {
		return
	}
	var rc RECT
	if r, _, _ := procGetWindowRect.Call(main.hwnd, uintptr(unsafe.Pointer(&rc))); r == 0 {
		return
	}
	const step, offsetX, offsetY, minW, minH = 40, 80, 60, 400, 300
	x := int(rc.Left) + offsetX + step*idx
	y := int(rc.Top) + offsetY + step*idx
	hmon, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&rc)), 0)
	if wa, ok := monitorWorkArea(hmon); ok {
		// 级联跑出工作区 → 回到工作区左上角偏移（保证可见）。
		if x+minW > int(wa.Right) || y+minH > int(wa.Bottom) {
			x, y = int(wa.Left)+offsetX, int(wa.Top)+offsetY
		}
	}
	_, _, _ = procSetWindowPos.Call(h.hwnd, 0, uintptr(x), uintptr(y), 0, 0, swpNoSizeNoActivate)
}
