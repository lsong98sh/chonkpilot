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
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-gui/bridge"
	"github.com/chonkpilot/chonkpilot-gui/internal/fileserver"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	webview2 "github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

// 窗口角色（MW-2：appHandler.role 取值）。
const (
	roleMain = "main"
	roleChat = "chat"
	// roleLog 是**日志窗口**（PRODSIM-1）：进程级唯一、创建后默认隐藏，仅作为 server 进程的
	// 窗口载体（单例判断 / 托盘唤出 / 关闭 = hide）。不绑定会话、不参与窗口额度。
	roleLog = "log"
)

// maxChatWindows 是独立对话窗口数量上限（24 §8 U-5 已决：≤5，不含主窗口）。
const maxChatWindows = 5

// maxGroupChatWindows 是**群聊窗口单独**数量上限（群聊方案 §9 D28/D30 · N13/N17 已决：5；
// 与普通对话窗口**互不联动** —— 见 registry.go 的额度分类判定）。
const maxGroupChatWindows = 5

// coInitApartmentThreaded = COINIT_APARTMENTTHREADED（0x2；WebView2 要求 STA，24 §4.1）。
const coInitApartmentThreaded = 2

// swpNoSizeNoActivate = SWP_NOSIZE(0x1) | SWP_NOACTIVATE(0x10)：只挪位置、不改尺寸、不激活。
const swpNoSizeNoActivate = 0x0011

// isAppOrigin 判定请求 URI 是否属于虚拟宿主源（**精确** scheme + host 相等，非前缀匹配）。
// 前缀匹配下 `https://app.localhost.evil.com/...` 亦以 appOrigin 开头 → 会误走本地 handler
// 应答（E5-②）；此处解析 URI 后精确比较：host 后缀、端口均不匹配。
func isAppOrigin(rawURI string) bool {
	u, err := url.Parse(rawURI)
	if err != nil {
		return false
	}
	return u.Scheme == "https" && u.Host == appOriginHost
}

var (
	ole32                   = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx      = ole32.NewProc("CoInitializeEx")
	procCoUninitialize      = ole32.NewProc("CoUninitialize")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
)

// hostEnv 是窗口工厂的共享装配上下文（进程级，只读）：总线 / 内嵌 dist / 形态 / 内嵌 server
// 由 host 一次装配（host.NewEnv），各窗口按需取用（桥与 appHandler 是**每窗口一份**，
// 见 createWindow）。
type hostEnv struct {
	bus            mq.Bus
	distFS         fs.FS // 已 fs.Sub 到 dist 根（embed 前缀 frontend/dist 已剥离）
	workDir        string
	dataDir        string // 数据层分支判据（desktop 缺省为空，12-数据层 §3）
	projectDataDir string // 项目数据根（prj 层；fileserver 白名单用）
	prjUsrRoot     string // prjusr 数据根（附件/截图落盘根；[24 §3.2] MW-8）
	logDir         string
	form           string
	srv            Server // nil = --no-server（UI 注入测试模式）/ 远程薄壳模式
	windows        *windowRegistry
	// dataOffline = 「本地无数据服务」（--no-server UI 注入测试模式）；远程薄壳（flash）**非**此处
	// （其 data-* 经 UDS 转发到常驻 server）→ 建窗时据此置桥 SetDataOffline。
	dataOffline bool
	// ── flash 远程薄壳模式（🆕 2026-10-10；见 remote.go）─────────────────
	// instanceID 非空 → **所有窗口共享同一实例 id**（flash：一条 UDS 连接 = 一个实例，
	// 见 19 §8.11）；空 → 每窗口自生成（desktop/gui 既有语义）。
	instanceID string
	// suppressInstanceExit = 远程薄壳下**非主窗口**关闭不发 `instance-exit`：
	// 全部窗口共享同一实例/同一连接，实例注销只应由主窗口关闭（→ 进程退出）或连接断开触发。
	suppressInstanceExit bool
	// testSrv 是 --test-port 的测试通道（未传 --test-port 时为 nil）：对话窗口建窗时经
	// RegisterChat 登记为可选目标（window_id 路由，24 §4.5）。由主窗口 onWired 接线后赋值。
	// 读写跨窗口线程（主窗口 onWired 写 / 对话窗口 onWired 读 / finish 读）→ 以 mu 守护
	// （D-10），一律经 testServer()/setTestServer() 访问，避免数据竞争。
	testSrvMu sync.RWMutex
	testSrv   *testServer
	// ── PRODSIM-1 日志窗口 ─────────────────────────────────────────────
	// logHost 是进程级唯一的日志窗口（创建后默认隐藏，关闭=收起，托盘可唤出）。
	// 它是 server 进程的窗口载体：即使所有项目主窗口都关闭，进程仍靠它 + 托盘常驻。
	logMu   sync.Mutex
	logHost *windowHost
	// ── PRODSIM-1 多项目 ────────────────────────────────────────────────
	// projects = 规范化 work_dir → 项目上下文（**同 work_dir 唯一**，唯一事实来源）。
	// 主窗口关闭 → 该项目从这里移除（进程继续常驻）。
	projMu   sync.Mutex
	projects map[string]*projectCtx
	// projectOpener 是宿主注入的「同进程打开项目」回调（gui.dir.open 用）；
	// nil = 未接线（flash 等形态）→ 桥回落 spawn 新进程。
	projectOpener func(dir string) error
}

// projectKey 归一 work_dir 作为映射键（Windows 大小写不敏感）。
func projectKey(workDir string) string {
	p, err := filepath.Abs(workDir)
	if err != nil {
		p = workDir
	}
	return strings.ToLower(filepath.Clean(p))
}

// lookupProject 返回已打开的项目上下文（nil = 未打开）。
func (e *hostEnv) lookupProject(workDir string) *projectCtx {
	e.projMu.Lock()
	defer e.projMu.Unlock()
	return e.projects[projectKey(workDir)]
}

// addProject 登记项目上下文；已存在 → 返回既有 + false（调用方应激活其主窗口）。
func (e *hostEnv) addProject(p *projectCtx) (*projectCtx, bool) {
	e.projMu.Lock()
	defer e.projMu.Unlock()
	k := projectKey(p.workDir)
	if old, ok := e.projects[k]; ok {
		return old, false
	}
	e.projects[k] = p
	return p, true
}

// removeProject 移除项目上下文（主窗口关闭时）。
func (e *hostEnv) removeProject(p *projectCtx) {
	e.projMu.Lock()
	defer e.projMu.Unlock()
	k := projectKey(p.workDir)
	if e.projects[k] == p {
		delete(e.projects, k)
	}
}

// projectsSnapshot 返回全部已打开项目（托盘「项目列表」）。
func (e *hostEnv) projectsSnapshot() []*projectCtx {
	e.projMu.Lock()
	defer e.projMu.Unlock()
	out := make([]*projectCtx, 0, len(e.projects))
	for _, p := range e.projects {
		out = append(out, p)
	}
	return out
}

// logWindow 返回日志窗口（未创建 = nil）。
func (e *hostEnv) logWindow() *windowHost {
	e.logMu.Lock()
	defer e.logMu.Unlock()
	return e.logHost
}

// setLogWindow 登记日志窗口。
func (e *hostEnv) setLogWindow(h *windowHost) {
	e.logMu.Lock()
	e.logHost = h
	e.logMu.Unlock()
}

// testServer 返回 --test-port 测试通道（未传 --test-port / 尚未接线时为 nil）。
func (e *hostEnv) testServer() *testServer {
	e.testSrvMu.RLock()
	defer e.testSrvMu.RUnlock()
	return e.testSrv
}

// setTestServer 接线测试通道（主窗口 onWired，主窗口线程；先于任何对话窗口建窗）。
func (e *hostEnv) setTestServer(s *testServer) {
	e.testSrvMu.Lock()
	e.testSrv = s
	e.testSrvMu.Unlock()
}

// windowHost 是一个窗口的宿主句柄（每窗口一份：桥 / appHandler / 句柄 / 生命周期）。
type windowHost struct {
	role      string // roleMain / roleChat
	instID    string // 本窗口实例 id（GUI 反向通道 / 消息定向用）
	windowID  string // 对话窗口的注册 id（主窗口为空）
	sessionID string // 对话窗口绑定的会话（主窗口为空）
	// proj 是本窗口所属**项目上下文**（PRODSIM-1 多项目）；nil = flash 远程薄壳 /
	// --no-server（沿用 hostEnv 的进程级单项目字段）。
	proj *projectCtx

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

// destroyForced 强制销毁本窗口（无视 HideOnClose）—— 程序收尾关日志窗口用。
func (h *windowHost) destroyForced() {
	if f, ok := h.w.(interface{ DestroyForced() }); ok {
		f.DestroyForced()
		return
	}
	h.destroy()
}

// show 显示并前置本窗口（跨线程安全；日志窗口由托盘「显示日志」唤出）。
func (h *windowHost) show() {
	if lc, ok := h.w.(webview2.WebViewLifecycle); ok {
		lc.Show()
	}
	activateWindow(h.hwnd)
}

// hide 隐藏本窗口（不销毁；日志窗口关闭 = 收起回托盘）。
func (h *windowHost) hide() {
	if lc, ok := h.w.(webview2.WebViewLifecycle); ok {
		lc.Hide()
	}
}

// projectCtx 是一个**项目**的宿主上下文（PRODSIM-1：单进程可开多个项目主窗口）。
// 进程级资源（bus / srv / distFS / 日志）在 hostEnv；项目级资源（数据根 / 小窗口表 / 主窗口）
// 在此。同一 work_dir 只允许一个 projectCtx（唯一事实来源 = hostEnv.projects 映射表）。
type projectCtx struct {
	workDir        string
	dataDir        string
	projectDataDir string
	prjUsrRoot     string
	// windows 是本项目的小窗口注册表（主窗口 + 单聊/群聊/子聊窗口）。
	windows *windowRegistry
	// mainHost 是本项目的主窗口（同 work_dir 唯一；关闭 = 移除本项目，进程继续常驻）。
	mainHost *windowHost
}

// windowSpec 描述一个待建窗口。
type windowSpec struct {
	role      string
	windowID  string
	sessionID string
	title     string
	url       string
	frameless bool
	// proj 是本窗口所属项目上下文（nil = 沿用 hostEnv 的项目级字段，flash / --no-server）。
	proj *projectCtx
	// parentInstance 是**开本窗口的那个窗口的实例 id**（"谁开的指谁"，逐层链；空 = 顶层）。
	// 用于跨窗口定向投递（61 §4.1 ④ · 单聊 §12.4 B4 / 群聊 §14.4 N18–N20）：随
	// `instance-register.parent_instance` 上报，server 实例视图据此把子实例的事件投回父窗口。
	parentInstance string
	// onWired 在**窗口线程**上、webview 建好（句柄已解析、桥与 handler 已就绪）后、
	// 导航前调用（主窗口用于 --test-port 接线）；返回错误 → 中止建窗。
	onWired func(h *windowHost) error
	// ── PRODSIM-1 窗口层级 ─────────────────────────────────────────────
	// hidden = 建窗后保持隐藏，且首次导航完成也**不自动 Show**（日志窗口）。
	hidden bool
	// hideOnClose = 用户点关闭只隐藏、不销毁（日志窗口：关闭 = 收回托盘）。
	hideOnClose bool
	// owner = 本窗口的 owner（hwndParent）；非 0 时随 owner 最小化/销毁（小窗口指主窗口，
	// 主窗口指日志窗口——保证所有窗口都是同一 owner 链，进程退出行为一致）。
	owner uintptr
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
	// 项目级字段（PRODSIM-1）：spec.proj 优先（单进程多项目）；nil → 沿用 hostEnv 的
	// 进程级单项目字段（flash 远程薄壳 / --no-server 注入测试）。
	wd, dd, pdd, pur := env.workDir, env.dataDir, env.projectDataDir, env.prjUsrRoot
	if spec.proj != nil {
		wd, dd, pdd, pur = spec.proj.workDir, spec.proj.dataDir, spec.proj.projectDataDir, spec.proj.prjUsrRoot
	}
	// 每窗口一份 WebView2 用户数据目录：fork 默认 DataPath = %AppData%\<exe名>，多窗口共享
	// 同一 profile 会共享浏览器进程，任一方销毁/崩溃波及另一方（历史实测：销毁一个实例会
	// 冻结其他窗口）。前端 ui.locale 有 DB 兜底（MainLayout），不依赖 WebView2 缓存留存。
	// 〔2026-10-07 实测〕固定 profile（复用）与每次新建 profile 的建窗耗时无显著差异（稳态 0.7~1.4s）
	// → 保留"每窗口一份"的隔离设计；建窗耗时的分段观测见 main.go 的启动打点。
	wvData := webviewDataDir(newUUID())
	if err := os.MkdirAll(wvData, 0o755); err != nil {
		slog.Warn("webview2 data dir create failed", "dir", wvData, "err", err)
	}
	// 活跃标记（含本进程 pid，D-34）：prune 据此跳过其它实例正在使用的 profile，
	// 不因「profile 根目录 mtime 超期」而误删活跃目录；目录收尾（finish/cleanup）随 RemoveAll 一并清除。
	markProfileActive(wvData)
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug: true,
		// 隐藏窗口（日志窗口）不得抢焦点。
		AutoFocus: !spec.hidden,
		DataPath:  wvData,
		// DevTools 用户入口屏蔽（B3）：F12 / Ctrl+Shift+I 快捷键与右键菜单 Inspect 不再打开
		// DevTools（宿主仍可程序化打开 → 状态栏调试图标 gui.devtools.open）。Debug 保留 true
		// 使默认右键菜单（复制/粘贴等）仍可用。
		DevToolsDisabled: true,
		// 主窗口 Frameless：无原生标题栏/边框（WM_NCCALCSIZE=0），HTML 自绘标题栏。
		// **注意**：frameless 下 WebView2 子窗（Chrome_RenderWidgetHostHWND）铺满整个客户区 →
		// WM_NCHITTEST 恒 HTCLIENT，**系统层不存在边缘 resize 命中区**；WS_THICKFRAME 只保留
		// 最大化 / 贴靠等原生行为。resize 由**前端边缘命中 + gui.window.resize → StartResize(HT*)**
		// 提供（见下方面向 WindowResizer 的装配与 go-webview2/FORK.md P4）。
		// 对话窗口用**原生标题栏**（最大化/最小化/关闭/拖动/八方向 resize 全由系统提供，
		// 零代码，24 §1.1）。
		Frameless: spec.frameless,
		// PRODSIM-1：日志窗口隐藏 + 关闭=收起；owner 层级（小窗口随主窗口）。
		Hidden:      spec.hidden,
		HideOnClose: spec.hideOnClose,
		Owner:       spec.owner,
		WindowOptions: webview2.WindowOptions{
			// 主窗口 Taskbar 标题 = 固定 `chonkpilot-<工程目录名>`（不随会话变化，见 MainWindowTitle）；
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
		role: spec.role, windowID: spec.windowID, sessionID: spec.sessionID, proj: spec.proj,
		w: w, wvData: wvData, done: make(chan struct{}),
	}
	h.hwnd = hwndOf(w)

	// ── 每窗口一个桥（MW-3）──────────────────────────────────────────────
	// 各自 instance_id（运行态分区 + 消息归属过滤）+ 各自 eval（→ 各自窗口）：后端 → 各前端
	// 由既有 acceptEventInstance 过滤免费提供（无需改 eval 扇出），窗口间前端事件不互通。
	id := env.instanceID
	if id == "" {
		id = newUUID() // desktop/gui：一窗口一实例（24 §2.2 C1）
	}
	h.instID = id
	h.br = bridge.New(id, wd, dd, func(script string) {
		// 窗口已销毁（done 已 close）→ 跳过 Eval（D-14：原 fire-and-forget 失败静默）。
		// 二次 Eval 到已 Destroy 的 webview 无意义，且宿主 Eval 失败无返回通道可观测。
		select {
		case <-h.done:
			slog.Debug("skip eval: window closed", "role", spec.role, "window_id", spec.windowID)
			return
		default:
		}
		w.Dispatch(func() { w.Eval(script) })
	}, env.bus)
	// 窗口父子链（61 §4.1 ④ · 单聊 §12.4 B4）：desktop/gui 每窗口一个实例 → 新窗口登记
	// 「谁开的指谁」（= 调用来源窗口实例，逐层链）。flash 形态全窗口**共享同一实例**
	// （env.instanceID 非空）→ 无子实例，不写（写了会自指）。
	if env.instanceID == "" && spec.parentInstance != "" {
		h.br.SetParentInstance(spec.parentInstance)
	}
	// session 绑定上报（61 §4.1 ④ · 群聊 §13 D42 / §15.4 N23；🆕 2026-10-16 SUBCHAT-3 阶段 F-2）：
	// desktop/gui 对话/群聊窗口每窗一个实例，登记其**绑定的会话** → server 端据此 **session → instance
	// 反查**，把定向消息（`session-ask`）投到该会话所在窗口。flash 形态全窗口共享同一实例 → 不写。
	if env.instanceID == "" && spec.sessionID != "" {
		h.br.SetSessionID(spec.sessionID)
	}
	// 文件日志目录下发前端（init-data 只增字段 logDir；未挂 sink 时为空 → 字段缺省）。
	h.br.SetLogDir(env.logDir)
	// prjusr 数据根注入桥（附件/截图落盘根；[24 §3.2] MW-8）。
	h.br.SetPrjUsrRoot(pur)
	// PRODSIM-1：gui.dir.open 走同进程打开项目（宿主注入；nil → 桥回落 spawn 新进程）。
	h.br.SetProjectOpener(env.projectOpener)
	if env.srv != nil {
		// 认证域（61 §4.6；阶段 2b-2）：首屏 authed 由服务端读桥持有的令牌判定。
		h.br.SetAuthCheck(env.srv.Authenticated)
		// config 类 data-* 走数据门面（同进程直调）；未接线（-no-server）时回落总线转发。
		h.br.SetFacade(inline.New(env.bus))
	}
	// --no-server（本地无数据服务）：data-* 快速失败，不经总线等 3s 超时（首屏提速）。
	// flash 远程薄壳（srv=nil 但有 UDS server）**不**置位。
	if env.dataOffline {
		h.br.SetDataOffline(true)
	}
	// B3：DevTools 用户入口已在 WebViewOptions 屏蔽；把「程序化打开」能力注入桥
	// （gui.devtools.open）。OpenDevToolsWindow 走 Dispatch 派发到 WebView2 UI 线程。
	if dt, ok := w.(webview2.DevTools); ok {
		h.br.SetDevToolsOpener(func() { w.Dispatch(func() { dt.OpenDevToolsWindow() }) })
	}
	// WIN-1 / G-59（2026-10-17）：frameless 主窗口无系统边缘命中区（见上方 Frameless 注释）→
	// 注入「进入系统原生 resize 模态循环」能力，由前端边缘命中后经 gui.window.resize 调起。
	// **仅 frameless 装配**；非 frameless（对话窗口走原生标题栏/边框）保持 nil → 明确失败。
	if spec.frameless {
		if rs, ok := w.(webview2.WindowResizer); ok {
			h.br.SetWindowResizer(rs.StartResize)
		}
	}
	// flash 远程薄壳：非主窗口关闭不发 instance-exit（共享实例由主窗口/连接断开收尾）。
	if env.suppressInstanceExit && spec.role != roleMain {
		h.br.SetInstanceExitSuppressed(true)
	}
	// 日志窗口（PRODSIM-1）：不注册 instance（它不参与 claim / 业务消息，只读 /api/log/tail）——
	// 否则会白占一个实例额度、且直到进程退出才注销。
	if spec.role == roleLog {
		h.br.SetNoInstance()
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
			WorkDir: wd,
			// 放行根（[24 §3.2] MW-8）：① 项目数据根（prj 库/旧附件等，`<workDir>/.chonkpilot`
			// 或显式 --data-dir）② prjusr 数据根（迁走后的附件/截图 `~/.chonkpilot/data/<prj-id>`）。
			// 两者同列，保证迁移前后资源均可经 `/show/` 取回（WorkDir 自身另由 WorkDir 字段放行）。
			DataDirs: []string{pdd, pur},
			// 相对引用解析根（🆕 2026-10-10）：消息内附件/截图引用为相对 prjusr 数据根的
			// `dir/name`（gui.upload 落 <prjUsrRoot>/<dir>/<name>，见 61 §1）。
			UploadRoot: pur,
		},
		br:          h.br,
		hwnd:        h.hwnd,
		destroy:     h.destroy,
		form:        env.form,
		requireAuth: env.srv != nil && env.srv.RequireAuth(),
		authed:      h.br.Authed,
		env:         env,
		proj:        spec.proj,
		role:        spec.role,
		windowID:    spec.windowID,
		sessionID:   spec.sessionID,
		logDir:      env.logDir, // 日志窗口 /api/log/tail（PRODSIM-1）
	}
	if spec.role == roleChat {
		// gui.window.set-title：**仅独立对话窗口**（主窗口标题不变，61 §1）。
		h.handler.setTitle = func(title string) { w.Dispatch(func() { w.SetTitle(title) }) }
	}

	interceptor.AddWebResourceRequestedFilter("*", edge.COREWEBVIEW2_WEB_RESOURCE_CONTEXT_ALL)
	interceptor.SetWebResourceRequestedCallback(func(request *edge.ICoreWebView2WebResourceRequest, args *edge.ICoreWebView2WebResourceRequestedEventArgs) {
		uri, err := request.GetUri()
		if err != nil || !isAppOrigin(uri) {
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
				logStartupStageOnce("main-first-paint", "主窗口首屏渲染完成") // OP-15（一次）
				restoreWindowFromConfig(h.br, h.hwnd)
			}
			// 隐藏窗口（日志窗口）保持隐藏，仅由托盘「显示日志」显式 Show。
			if !spec.hidden {
				lifecycle.Show()
			}
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
	// 本窗口所属项目的小窗口表（多项目 PRODSIM-1）；nil → flash / --no-server 用进程级表。
	wins := env.windows
	if h.proj != nil {
		wins = h.proj.windows
	}
	if h.role == roleChat {
		wins.remove(h)
		// 关闭的窗口不再作为 --test-port 目标（多窗口路由表同步出表）。
		if s := env.testServer(); s != nil {
			s.UnregisterChat(h.windowID)
		}
	}
	// PRODSIM-1：主窗口关闭 = 关掉本项目所有小窗口（cancel 会话）+ 从项目表移除，
	// **进程继续常驻**（托盘「退出」才结束）。
	if h.role == roleMain && h.proj != nil {
		h.proj.windows.shutdown()
		h.proj.windows.setMain(nil)
		env.removeProject(h.proj)
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
