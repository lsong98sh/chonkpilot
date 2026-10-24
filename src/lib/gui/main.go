//go:build windows

package gui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/chonkpilot/chonkpilot-gui/bridge"
	"github.com/chonkpilot/chonkpilot-gui/internal/fileserver"
	"github.com/chonkpilot/chonkpilot-gui/internal/themefs"
	"github.com/chonkpilot/chonkpilot-lib/exedir"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	webview2 "github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

// 宿主形态（61 §4.6：desktop / gui / browser）—— **由壳静态给定**
// （见 host.Main 的 `opts.Form`），不再由本包的编译开关（原 form_inprocess.go / form_split.go 的
// `runtimeForm()`）决定宿主分支。
//
// 🆕 2026-10-09：**flash 薄壳客户端注入 `desktop` 语义**（本地免鉴权 + 前端不心跳）——
// 免鉴权由**前门**决定（UDS = 本机信任），形态取值保持三态、不增 `flash`。
const (
	// FormDesktop = 桌面单体（GUI 内嵌 server，本机单用户、免鉴权）—— flash 薄壳同此语义。
	FormDesktop = "desktop"
	// FormGui = GUI 客户端 + 独立 server（可异地 → 要求认证）。
	FormGui = "gui"
)

// Options 是壳传给宿主的静态启动参数。
//
// 注：其余启动参数（`--work-dir` / `--data-dir` / `--bridge-url` / `--test-port` /
// `--llm-base` / `--llm-model` / `--no-server`）仍由 host 包在 Main 内解析命令行 —— 两个壳
// （`src/desktop` / `src/gui`）只差**形态**一项，故只有 Form 经 Options 传入。
type Options struct {
	// Form 是本宿主运行形态（61 §4.6）：FormDesktop（桌面单体）| FormGui（GUI 客户端）。
	// 随首屏注入给前端（`window.__ck.form`）并下发内嵌 server（Options.Form），
	// 决定 claim 鉴权分支与首屏 requireAuth。
	Form string
}

// appOrigin / appOriginHost 是虚拟宿主源（精确 scheme+host；请求经 WebResourceRequested
// 拦截，无 HTTP server）。判定一律用**精确 host 相等**（见 window.go isAppOrigin），
// 不用字符串前缀匹配——否则 `https://app.localhost.evil.com/...` 会误命中。
const (
	appOrigin     = "https://app.localhost"
	appOriginHost = "app.localhost"
)

// syscalls
var (
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	user32                    = windows.NewLazySystemDLL("user32.dll")
	procShowWindow            = user32.NewProc("ShowWindow")
	procGetWindowRect         = user32.NewProc("GetWindowRect")
	procSetWindowPos          = user32.NewProc("SetWindowPos")
	procIsWindowVisible       = user32.NewProc("IsWindowVisible")
	procAttachConsole         = kernel32.NewProc("AttachConsole")
	procFreeConsole           = kernel32.NewProc("FreeConsole")
	procGetStdHandle          = kernel32.NewProc("GetStdHandle")
	procSetConsoleOutputMode  = kernel32.NewProc("SetConsoleOutputMode")
	_                         = procAttachConsole
	_                         = procFreeConsole
	_                         = procGetStdHandle
	_                         = procSetConsoleOutputMode
	_                         = procIsWindowVisible
	SW_MINIMIZE               = uintptr(6)
	SW_MAXIMIZE               = uintptr(3)
	SW_RESTORE                = uintptr(9)
	procGetWindowPlacement    = user32.NewProc("GetWindowPlacement")
	procSetWindowPlacement    = user32.NewProc("SetWindowPlacement")
	procSystemParametersInfoW = user32.NewProc("SystemParametersInfoW")
	procMonitorFromRect       = user32.NewProc("MonitorFromRect")
	procMonitorFromPoint      = user32.NewProc("MonitorFromPoint")
	procGetMonitorInfoW       = user32.NewProc("GetMonitorInfoW")
	_                         = procGetWindowPlacement
	_                         = procSetWindowPlacement
	_                         = procSystemParametersInfoW
	_                         = procMonitorFromRect
	_                         = procMonitorFromPoint
	_                         = procGetMonitorInfoW
)

// RECT / MONITORINFO 是 Win32 显示器几何结构（窗口正常化用）。
type RECT struct{ Left, Top, Right, Bottom int32 }

type MONITORINFO struct {
	CbSize    uint32
	RcMonitor RECT
	RcWork    RECT
	DwFlags   uint32
}

// monitorWorkArea 取指定显示器的工作区（hmon=0 → false）。
func monitorWorkArea(hmon uintptr) (RECT, bool) {
	if hmon == 0 {
		return RECT{}, false
	}
	mi := MONITORINFO{CbSize: uint32(unsafe.Sizeof(MONITORINFO{}))}
	r, _, _ := procGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi)))
	if r == 0 {
		return RECT{}, false
	}
	return mi.RcWork, true
}

func hwndOf(w webview2.WebView) uintptr { return uintptr(w.Window()) }

func showWindow(hwnd uintptr, cmd uintptr) {
	procShowWindow.Call(hwnd, cmd)
}

func isMaximisedWindow(hwnd uintptr) bool {
	type WINDOWPLACEMENT struct {
		Length         uint32
		Flags          uint32
		ShowCmd        uint32
		MinPosition    [2]int32
		MaxPosition    [2]int32
		NormalPosition [4]int32
	}
	var wp WINDOWPLACEMENT
	wp.Length = uint32(unsafe.Sizeof(wp))
	r, _, _ := procGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp)))
	// 仅 SW_SHOWMAXIMIZED(3) 视为最大化；SW_SHOWMINIMIZED(2) 是最小化（见 isMinimizedWindow），
	// 原实现把 2 并入最大化属误判（最小化窗口会被误报为最大化）。
	return r != 0 && wp.ShowCmd == 3
}

// windowState 是前端 SaveWindowState 落盘的窗口几何（prj config 表 window，{"v":"<json>"}）。
type windowState struct {
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Maximized bool `json:"maximized"`
}

// restoreWindowFromConfig 启动时从配置恢复窗口位置/尺寸/最大化，并做超屏正常化
// （FP：启动恢复关闭前布局尺寸位置状态；最小化改正常；超出当前主显示器收进可见区域）。
// v6（12-数据层）：窗口几何属"项目用户级"——落 prjusr 的 window.<字段> 细 key；
// 一次 PrjConfigList 取回（含 prj 层回落值）。
func restoreWindowFromConfig(br *bridge.Bridge, hwnd uintptr) {
	st, ok := windowStateFromConfig(br.PrjConfigList())
	if !ok {
		return
	}
	if st.Width <= 0 {
		st.Width = 1280
	}
	if st.Height <= 0 {
		st.Height = 800
	}
	normalizeWindowRect(&st)
	applyWindowPlacement(hwnd, st)
}

// windowStateFromConfig 从扁平配置 map 还原窗口状态（读 window.<字段> 细 key）。
// 无任何窗口记录 → ok=false。
func windowStateFromConfig(cfg map[string]any) (windowState, bool) {
	if len(cfg) == 0 {
		return windowState{}, false
	}
	str := func(v any) string {
		if v == nil {
			return ""
		}
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprint(v)
	}
	num := func(key string) (float64, bool) {
		s := str(cfg["window."+key])
		if s == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	// boolean：布尔字面量（前端 cfgScalar 存 "true"/"false"）与数值字面量（1/0）都认。
	boolean := func(key string) (bool, bool) {
		s := str(cfg["window."+key])
		if s == "" {
			return false, false
		}
		if b, err := strconv.ParseBool(s); err == nil {
			return b, true
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f != 0, true
		}
		return false, false
	}
	var st windowState
	if f, ok := num("x"); ok {
		st.X = int(f)
	}
	if f, ok := num("y"); ok {
		st.Y = int(f)
	}
	if f, ok := num("width"); ok {
		st.Width = int(f)
	}
	if f, ok := num("height"); ok {
		st.Height = int(f)
	}
	if b, ok := boolean("maximized"); ok {
		st.Maximized = b
	}
	if _, hasW := num("width"); hasW {
		return st, true
	}
	return windowState{}, false
}

// normalizeWindowRect 按**当前显示器布局**正常化窗口矩形（显示器变化自修复，12-数据层）：
//   - 窗口与任一显示器有交集 → 保留其位置，尺寸钳制到该显示器工作区；
//   - 完全无交集（显示器拔出/分辨率变小/换机器）→ 收进主显示器工作区并居中；
//   - 最后保证标题栏至少 minVisible 像素可见。
func normalizeWindowRect(st *windowState) {
	const minW, minH, minVisible = 400, 300, 80
	if st.Width < minW {
		st.Width = minW
	}
	if st.Height < minH {
		st.Height = minH
	}
	rect := RECT{int32(st.X), int32(st.Y), int32(st.X + st.Width), int32(st.Y + st.Height)}
	// MONITOR_DEFAULTTONULL(0)：无交集返回 0
	hmon, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&rect)), 0)
	wa, ok := monitorWorkArea(hmon)
	if !ok {
		// 无任何显示器包含该窗口 → 主显示器（MONITOR_DEFAULTTOPRIMARY=1）工作区。
		// 用 MonitorFromPoint（POINT{0,0} 按 Win64 ABI 以单个 8 字节整数传值 → uintptr(0)）：
		// **不可**对 MonitorFromRect 传 NULL rect（user32 解引用空指针 → 0xc0000005 进程崩溃）。
		primary, _, _ := procMonitorFromPoint.Call(0, 1)
		wa, ok = monitorWorkArea(primary)
		if !ok {
			// 兜底：沿用系统主工作区（SPI_GETWORKAREA）
			r, _, _ := procSystemParametersInfoW.Call(0x0030, 0, uintptr(unsafe.Pointer(&wa)), 0) // SPI_GETWORKAREA
			if r == 0 || wa.Right <= wa.Left || wa.Bottom <= wa.Top {
				wa = RECT{0, 0, 1920, 1040}
			}
		}
		waW, waH := int(wa.Right-wa.Left), int(wa.Bottom-wa.Top)
		st.X = int(wa.Left) + (waW-st.Width)/2
		st.Y = int(wa.Top) + (waH-st.Height)/2
	}
	waW, waH := int(wa.Right-wa.Left), int(wa.Bottom-wa.Top)
	if waW > 0 && st.Width > waW {
		st.Width = waW
	}
	if waH > 0 && st.Height > waH {
		st.Height = waH
	}
	// 保证至少 minVisible 像素可见（标题栏）
	if st.X < int(wa.Left) {
		st.X = int(wa.Left)
	}
	if st.Y < int(wa.Top) {
		st.Y = int(wa.Top)
	}
	if st.X > int(wa.Right)-minVisible {
		st.X = int(wa.Right) - minVisible
	}
	if st.Y > int(wa.Bottom)-minVisible {
		st.Y = int(wa.Bottom) - minVisible
	}
}

// applyWindowPlacement 设置窗口位置/尺寸/显示命令（最大化按 SW_SHOWMAXIMIZED，其余正常）。
// **Length 必须是 sizeof(WINDOWPLACEMENT)**：写死 64（> 结构体实际大小）会被
// SetWindowPlacement 拒绝（ERROR_INVALID_PARAMETER）→ 几何恢复静默失效。
func applyWindowPlacement(hwnd uintptr, st windowState) {
	type WINDOWPLACEMENT struct {
		Length         uint32
		Flags          uint32
		ShowCmd        uint32
		MinPosition    [2]int32
		MaxPosition    [2]int32
		NormalPosition [4]int32
	}
	var wp WINDOWPLACEMENT
	wp.Length = uint32(unsafe.Sizeof(wp))
	wp.NormalPosition = [4]int32{int32(st.X), int32(st.Y), int32(st.X + st.Width), int32(st.Y + st.Height)}
	if st.Maximized {
		wp.ShowCmd = 3 // SW_SHOWMAXIMIZED
	} else {
		wp.ShowCmd = 9 // SW_RESTORE（最小化改正常）
	}
	procSetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp)))
}

// appHandler 是虚拟源 HTTP 处理器：静态资源 / /publish / /show/。
// 2026-09-04 /call 清零（61-消息一览 §8：历史 dataCalls 归位 gui.*/data-*/filesys.*
// 消息面，前端统一 publish），/call/ 端点与后端 bridge.Call 已随清理批次删除。
//
// **每窗口一个**（MW-2）：拦截器天然 per-webview → /publish、/show/、静态资源都绑定到本窗口，
// 故窗口定向命令（gui.window.status / 截图 / DevTools / set-title）天然只作用于来源窗口。
type appHandler struct {
	distFS      fs.FS
	fileHandler *fileserver.FileShowHandler
	br          *bridge.Bridge
	hwnd        uintptr
	destroy     func()
	// ── 多窗口（MW-2 / MW-6）──
	// env 是进程级装配上下文（对话窗口注册表等）；role/windowID/sessionID 标识本窗口。
	env       *hostEnv
	role      string // roleMain / roleChat（24 §4.2）
	windowID  string // 对话窗口注册 id（主窗口为空）
	sessionID string // 对话窗口绑定的会话（主窗口为空）
	// proj 是本窗口所属项目（PRODSIM-1 多项目）；nil = flash / --no-server（用 env.windows）。
	proj *projectCtx
	// setTitle 更新本窗口标题（gui.window.set-title）：**仅对话窗口**接线；主窗口 nil → ok=false。
	setTitle func(title string)
	// form 是本宿主运行形态（61 §4.6：desktop / gui）——**由壳传入**（host.Main 的 opts.Form），
	// 首屏注入 `window.__ck.form` 用（见 serveStatic）。
	form string
	// ── 认证域首屏注入（61 §4.6；阶段 2b-2）──
	// requireAuth = 形态决定（desktop=false；gui=true）；authed = 读凭证判定
	// （桥持有的令牌有效 = true）。两者**均由服务端（宿主）判定**，前端零额外往返。
	requireAuth bool
	authed      func() bool
	// logDir 是 GUI 文件日志目录（PRODSIM-1 日志窗口经 `/api/log/tail` 读取；空 = 未挂 sink）。
	logDir string
	// pluginAssets 提供 `/plugins/<id>/<path>`（插件自持 webui，PLUGIN-UI-1）；nil = 404。
	pluginAssets *pluginAssetHandler
	// pluginActions 提供 `/plugins/<id>/api/<action>`（插件 UI 动作面，PLUGIN-UI-1）；nil = 404。
	pluginActions *pluginActionHandler
}

// windows 返回本窗口所属项目的小窗口表（nil 项目 → 进程级表，flash / --no-server）。
func (h *appHandler) windows() *windowRegistry {
	if h.proj != nil {
		return h.proj.windows
	}
	return h.env.windows
}

func (h *appHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case path == "/publish":
		h.handlePublish(w, r)
	case path == "/api/log/tail":
		h.serveLogTail(w, r)
	case path == "/themes.css":
		h.serveThemes(w)
	case strings.HasPrefix(path, "/plugin-ui/"):
		h.servePluginUISDK(w, r)
	case strings.HasPrefix(path, "/plugins/"):
		// 插件 UI 动作面（PLUGIN-UI-1 第二段）：`POST /plugins/<id>/api/<action>` → 插件 UI RPC。
		if matchesPluginAPIPath(path) {
			if h.pluginActions == nil {
				http.NotFound(w, r)
				return
			}
			h.pluginActions.ServeHTTP(w, r)
			return
		}
		// 插件自持 webui 资源（PLUGIN-UI-1）：宿主经 UDS 向插件拉取，不再读磁盘。
		if h.pluginAssets == nil {
			http.NotFound(w, r)
			return
		}
		h.pluginAssets.ServeHTTP(w, r)
	case strings.HasPrefix(path, "/show/"):
		h.fileHandler.ServeHTTP(w, r)
	default:
		h.serveStatic(w, r, path)
	}
}

// serveThemes 提供主题资产 `GET /themes.css`（THEME-1，2026-10-23）。
//
// 来源：`<exeDir>/themes/themes.css`（静态可编辑，重启生效）；缺失 / 不可读 → 内置 embed 副本
// （`themefs.Load` **永不返回空**，故本端点不会 404）。响应头与 serveStatic 完全同口径
// （no-store + nosniff），否则 WebView2 会持久缓存旧主题。
func (h *appHandler) serveThemes(w http.ResponseWriter) {
	data, err := themefs.Load(exeDir())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

// exeDirCached 是进程级缓存的 exe 所在目录（主题资产等「随 exe 目录」资产按它定位）。
var (
	exeDirOnce   sync.Once
	exeDirCached string
)

// exeDir 返回当前可执行文件所在目录（失败 → 空串，调用方按「无磁盘资产」处理）。
func exeDir() string {
	exeDirOnce.Do(func() {
		if d, err := exedir.Dir(); err == nil {
			exeDirCached = d
		}
	})
	return exeDirCached
}

// servePluginUISDK 提供插件 iframe 的共享桥 SDK（`GET /plugin-ui/_sdk/plugin.js`）。
//
// 旧路由 `/plugin-ui/<id>/*`（宿主直读磁盘 `<插件目录>/webui`）**已下线** → 一律 404：
// 内置插件全部 `ui.selfServe: true`，资源走 `/plugins/<id>/<path>`（`plugin/v1/ui.asset.get`）。
// `_sdk/plugin.js` 仍被所有插件页面直接引用，必须继续提供，内容逐字节不变。
func (h *appHandler) servePluginUISDK(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/plugin-ui/_sdk/plugin.js" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_, _ = w.Write([]byte(pluginUIBridgeJS))
}

// pluginUIBridgeJS 是插件 iframe 的共享桥（宿主提供，插件只需 <script src="/plugin-ui/_sdk/plugin.js">）。
const pluginUIBridgeJS = `(function(){
  var pending = new Map(), seq = 0;
  window.ckPlugin = {
    call: function(method, params){
      return new Promise(function(resolve, reject){
        var id = 'r' + (++seq);
        pending.set(id, {resolve: resolve, reject: reject});
        parent.postMessage({ckPlugin:1, id:id, type:'call', method:method, params:params||{}}, '*');
      });
    },
    onEvent: function(fn){
      window.addEventListener('message', function(e){
        var d = e.data;
        if (d && d.ckPlugin === 1 && d.type === 'event') fn(d.topic, d.payload);
      });
    }
  };
  window.addEventListener('message', function(e){
    var d = e.data;
    if (!d || d.ckPlugin !== 1 || d.type !== 'result') return;
    var p = pending.get(d.id);
    if (!p) return;
    pending.delete(d.id);
    if (d.ok) p.resolve(d.result);
    else p.reject(new Error((d.errors || []).join('; ') || ('plugin call failed: ' + d.id)));
  });
})();`

// serveLogTail 返回 GUI 日志文件尾部（日志窗口只读通道，PRODSIM-1）。
// 只读本进程日志目录下的 gui.log，不接受任意路径（避免变成越权文件读取面）。
// 查询参数 lines（1..5000，缺省 500）控制返回行数。
func (h *appHandler) serveLogTail(w http.ResponseWriter, r *http.Request) {
	if h.logDir == "" {
		http.Error(w, "log dir unavailable", http.StatusNotFound)
		return
	}
	lines := 500
	if v := r.URL.Query().Get("lines"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 5000 {
			lines = n
		}
	}
	data, err := os.ReadFile(filepath.Join(h.logDir, guiLogFileName))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	// 取尾部 lines 行（日志按 2MB 滚动，整读可接受）。
	parts := bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n"))
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	out := bytes.Join(parts, []byte("\n"))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// handlePublish 前端事件 → 本地事件优先，否则桥发布到 mq；
// 响应携带处理结果信封 {ok, result, errors}（61-消息一览 §9.1 上行 bridge promise）。
func (h *appHandler) handlePublish(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type    string `json:"type"`
		Payload string `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// gui.window.status：统一窗口控制/查询（command 语义见 handleWindowStatus）。
	if body.Type == msgkeys.TopicGuiWindowStatus {
		writePublishResult(w, func() (any, []error) { return h.handleWindowStatus(body.Payload) })
		return
	}
	// 窗口面消息（MW-6：open-chat / list / set-title）由**宿主侧**处理（同 gui.window.status
	// 口径：需要窗口能力，不进入桥）。
	if isWindowMessage(body.Type) {
		writePublishResult(w, func() (any, []error) { return h.handleWindowMessage(body.Type, body.Payload) })
		return
	}
	writePublishResult(w, func() (any, []error) {
		return h.br.PublishEvent(body.Type, body.Payload)
	})
}

// handleWindowStatus 统一窗口控制/查询（gui.window.status，61-消息一览 §1）：
// payload {command}：null/query = 仅查询状态；minimize / maximize / close = 执行后返回新状态。
func (h *appHandler) handleWindowStatus(payloadJSON string) (any, []error) {
	return applyWindowCommand(h.hwnd, h.destroy, h.br, payloadJSON)
}

// applyWindowCommand 执行窗口命令并返回状态（appHandler 与 testServer 共用同一语义）。
// 最大化切换后广播 window-maximized-changed（同步按钮图标，沿用）。
func applyWindowCommand(hwnd uintptr, destroy func(), br *bridge.Bridge, payloadJSON string) (any, []error) {
	var p struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal([]byte(payloadJSON), &p)
	switch p.Command {
	case "minimize":
		showWindow(hwnd, SW_MINIMIZE)
		return winState(hwnd), nil
	case "maximize":
		if isMaximisedWindow(hwnd) {
			showWindow(hwnd, SW_RESTORE)
		} else {
			showWindow(hwnd, SW_MAXIMIZE)
		}
		env, _ := json.Marshal(map[string]interface{}{"maximized": isMaximisedWindow(hwnd), "instance_id": br.InstanceID()})
		br.EmitFrontend(msgkeys.TopicWindowMaximizedChanged, string(env))
		return winState(hwnd), nil
	case "close":
		if destroy != nil {
			destroy()
		}
		return nil, nil
	default: // command 为空/null → 仅查询
		return winState(hwnd), nil
	}
}

// windowState 返回窗口状态（查询与命令执行的统一 result 载荷）。
func winState(hwnd uintptr) map[string]bool {
	return map[string]bool{
		"maximized": isMaximisedWindow(hwnd),
		"minimized": isMinimizedWindow(hwnd),
	}
}

// isMinimizedWindow 判断当前是否最小化（GetWindowPlacement ShowCmd == SW_SHOWMINIMIZED）。
func isMinimizedWindow(hwnd uintptr) bool {
	type WINDOWPLACEMENT struct {
		Length         uint32
		Flags          uint32
		ShowCmd        uint32
		MinPosition    [2]int32
		MaxPosition    [2]int32
		NormalPosition [4]int32
	}
	var wp WINDOWPLACEMENT
	wp.Length = uint32(unsafe.Sizeof(wp))
	r, _, _ := procGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp)))
	return r != 0 && wp.ShowCmd == 2 // SW_SHOWMINIMIZED
}

// writePublishResult 统一输出结果信封（HTTP 恒 200；errors 收进 payload 由发送端自查）。
func writePublishResult(w http.ResponseWriter, run func() (any, []error)) {
	result, errs := run()
	var emsg []string
	for _, e := range errs {
		emsg = append(emsg, e.Error())
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":     len(emsg) == 0,
		"result": result,
		"errors": emsg,
	})
}

// serveStatic 从 embedded dist 提供静态资源。
func (h *appHandler) serveStatic(w http.ResponseWriter, r *http.Request, path string) {
	if path == "/" {
		path = "/index.html"
	}
	path = strings.TrimPrefix(path, "/")
	b, err := fs.ReadFile(h.distFS, path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	// 与 fileserver 同口径：阻止 MIME 嗅探（经 serveWebResource 一并转发给 WebView2）。
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// 首屏注入（61 §4.6，阶段 2a）：
	//   - `window.__chonkpilotInstanceId`（既有）：前端 mq.emit 据此给业务 payload 统一补
	//     instance_id（61-消息一览 §0 第 21/45 行）；
	//   - `window.__ck`（新增）：`{form, requireAuth, authed, instanceId}` —— 形态/认证标记由
	//     **宿主（服务端侧）判定**，前端据此分派视图与「是否发布 instance-heartbeat」，
	//     **不使用 UA 判形态**。
	// index.html 在 <head> 后插入内联脚本——内联经典脚本先于模块脚本执行，保证早于任何前端
	// emit（不用 NavigationCompleted 后的 Eval 注入：App.vue onMounted 等早期 emit 会先于该
	// 时机发生而拿不到标记）。
	if path == "index.html" {
		// authed 缺省 true（未接线，如 `--no-server` UI 注入模式）——该模式下 requireAuth=false
		// （desktop 编译形态），前端不据 authed 分派登录视图 → 行为与 2a 等价。
		authed := true
		if h.authed != nil {
			authed = h.authed()
		}
		script := "<script>window.__chonkpilotInstanceId=" + strconv.Quote(h.br.InstanceID()) + ";" +
			"window.__ck=" + bootstrapJSON(h.form, h.br.InstanceID(), h.requireAuth, authed) + ";</script>"
		s := string(b)
		if i := strings.Index(s, "<head>"); i >= 0 {
			j := i + len("<head>")
			s = s[:j] + script + s[j:]
		} else {
			s = script + s
		}
		b = []byte(s)
	}
	ct := "text/plain"
	switch {
	case strings.HasSuffix(path, ".html"):
		ct = "text/html"
	case strings.HasSuffix(path, ".js"):
		ct = "application/javascript"
	case strings.HasSuffix(path, ".css"):
		ct = "text/css"
	case strings.HasSuffix(path, ".json"):
		ct = "application/json"
	case strings.HasSuffix(path, ".ico"):
		ct = "image/x-icon"
	}
	w.Header().Set("Content-Type", ct)
	_, _ = w.Write(b)
}

// bootstrapJSON 生成首屏注入 `window.__ck` 字面量（61 §4.6：{form, requireAuth, authed, instanceId}）。
//
// 口径（**服务端（宿主）判定，绝不读 UA** —— UA 可伪造，仅作日志诊断）：
//   - form = 宿主运行形态（**由壳传入**：`src/desktop` → FormDesktop / `src/gui` → FormGui；
//     三形态 = desktop / gui / browser）；
//   - requireAuth = **形态决定**（阶段 2b-2）：desktop=false（本机单用户免鉴权）、
//     gui=true；由 llm server 的 `RequireAuth()` 判定（未启动 server 的 `--no-server`
//     测试模式回落到传入形态）；
//   - authed = **服务端读凭证判定**（阶段 2b-2）：读桥**持有**的令牌 → 校验有效性
//     （`bridge.Authed()` → `server.Authenticated`）；不读 UA、不接受前端自报。
//
// 注：**未认证时 instanceId 仍注入入口绑定值**（既有兼容值）—— 它是事件过滤 / `/show` URL /
// 前端 payload 注入的既有依赖，且**不构成凭据**（22 §2）；前端据 `requireAuth && !authed`
// 分派登录视图，**不据 instanceId 判身份**。
func bootstrapJSON(form, instanceID string, requireAuth, authed bool) string {
	b, err := json.Marshal(map[string]any{
		"form":        form,
		"requireAuth": requireAuth,
		"authed":      authed,
		"instanceId":  instanceID,
	})
	if err != nil {
		return "{}"
	}
	return string(b)
}

// readRequestBody 读取请求体并施加 limit 上限（D-26）：用 io.LimitReader(limit+1) 读取，
// 超出 limit 返回 tooLarge=true（调用方回 413）；limit 复用 maxRecordedBody 常量，不复制魔数。
func readRequestBody(stream io.Reader, limit int64) (body []byte, tooLarge bool) {
	body, _ = io.ReadAll(io.LimitReader(stream, limit+1))
	return body, int64(len(body)) > limit
}

// serveWebResource 适配 WebView2 请求到 http.Handler。
func serveWebResource(h http.Handler, c *edge.Chromium, request *edge.ICoreWebView2WebResourceRequest, rawURI string) (*edge.ICoreWebView2WebResourceResponse, error) {
	method, err := request.GetMethod()
	if err != nil {
		return nil, err
	}
	var body []byte
	if method == "POST" || method == "PUT" {
		if stream, err := request.GetContent(); err == nil && stream != nil {
			// 请求体上限（D-26）：无界 ReadAll 会把超大 POST/PUT 体整块缓冲进宿主内存；
			// 读取上限复用响应方向的 maxRecordedBody（D-07），超出即回 413（响应体超限同款式）。
			tooLarge := false
			body, tooLarge = readRequestBody(stream, maxRecordedBody)
			if tooLarge {
				return c.CreateWebResourceResponse(
					[]byte("request body too large"),
					http.StatusRequestEntityTooLarge,
					"Payload Too Large",
					"Content-Type: text/plain; charset=utf-8\r\n",
				)
			}
		}
	}
	url, err := request.GetUri()
	if err != nil {
		return nil, err
	}
	// D-31：直接以 *bytes.Reader 包装 body，省去 `string(body)` 对最大 128MB 请求体的整块
	// 复制（http.NewRequest 对 *bytes.Reader 同样设置 GetBody/ContentLength，行为一致）。
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	rr := &recorder{header: make(http.Header), status: 200}
	h.ServeHTTP(rr, req)
	// 响应体超上限（D-07）：原 /publish、/show/ 的响应体无界缓冲内存 → 超限即返回 413，
	// 防止异常/超大响应体把宿主内存吃满（正常静态资源/发布应答远低于上限）。
	if rr.truncated {
		resp, err := c.CreateWebResourceResponse(
			[]byte("response body too large"),
			http.StatusRequestEntityTooLarge,
			"Payload Too Large",
			"Content-Type: text/plain; charset=utf-8\r\n",
		)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
	if rr.header.Get("Content-Type") == "" {
		rr.header.Set("Content-Type", "text/plain; charset=utf-8")
	}
	resp, err := c.CreateWebResourceResponse(rr.body, rr.status, "OK", forwardedHeaders(rr.header))
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// forwardedHeaders 把 recorder 收集到的响应头拼成 WebView2 需要的多行格式（`K: V\r\n`）。
// 必须转发 Cache-Control（serveStatic/fileserver 均设 `no-cache, no-store, must-revalidate`），
// 否则 WebView2 会持久缓存旧页面（项目规则明令禁止）；X-Content-Type-Options 阻止 MIME 嗅探
// （/show/ 下的用户文件）。Content-Length 由 WebView2 依 body 自算，不转发以免冲突。
func forwardedHeaders(h http.Header) string {
	var b strings.Builder
	for _, name := range []string{"Content-Type", "Cache-Control", "X-Content-Type-Options"} {
		if v := h.Get(name); v != "" {
			b.WriteString(name)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteString("\r\n")
		}
	}
	return b.String()
}

// maxRecordedBody 是 recorder 缓冲的响应体上限（D-07：防 /publish、/show/ 大响应无界占内存）。
// 128MiB 为安全网——正常静态资源/发布应答远小于此；/show/ 下超大文件预览触及上限 → 返回 413。
const maxRecordedBody = 128 << 20

type recorder struct {
	header    http.Header
	body      []byte
	status    int
	truncated bool // 写入累计超 maxRecordedBody（body 已按上限截断）
}

func (r *recorder) Header() http.Header { return r.header }

// Write 追加响应体；累计超 maxRecordedBody 时截断并置 truncated。**不返回 error**（否则上游
// io.Copy 会因短写提前中断，反而拿不到完整状态）；由 serveWebResource 据 truncated 改返回 413。
func (r *recorder) Write(b []byte) (int, error) {
	if len(r.body)+len(b) > maxRecordedBody {
		r.truncated = true
		if remaining := maxRecordedBody - len(r.body); remaining > 0 {
			r.body = append(r.body, b[:remaining]...)
		}
		return len(b), nil
	}
	r.body = append(r.body, b...)
	return len(b), nil
}

func (r *recorder) WriteHeader(s int) { r.status = s }

// ── 启动分段计时（OP-15：定位启动慢，仅插桩，不改启动行为/顺序）──────────────
//
// 统一前缀 `[startup] <阶段> 耗时 <段>ms（累计 <总>ms）`：段 = 距上次打点，
// 总 = 距 MarkStartupBegin（host.Main 最早期）。据此逐段定位耗时段（capability 扫描 /
// gateway 启动 / 工具缓存预热 / 插件 Start / WebView2 建窗 / 首屏渲染）。
// 日志走本包统一出口 slog（见 loglevel.go；桌面主力形态非旧 wails 的 zap）。
var (
	startupMu   sync.Mutex
	startupT0   time.Time
	startupLast time.Time
)

// MarkStartupBegin 重置启动计时基准（host.Main 最早期调用一次）。
func MarkStartupBegin() {
	startupMu.Lock()
	startupT0 = time.Now()
	startupLast = startupT0
	startupMu.Unlock()
}

// LogStartupStage 输出一段启动耗时（OP-15）：段 = 距上次打点，总 = 距 MarkStartupBegin。
func LogStartupStage(stage string) {
	startupMu.Lock()
	now := time.Now()
	if startupT0.IsZero() {
		startupT0, startupLast = now, now
	}
	seg, total := now.Sub(startupLast), now.Sub(startupT0)
	startupLast = now
	startupMu.Unlock()
	slog.Info(fmt.Sprintf("[startup] %s 耗时 %dms（累计 %dms）", stage, seg.Milliseconds(), total.Milliseconds()))
}

// startupOnce 保证「首屏」类阶段只记一次（导航完成回调可能因刷新多次触发）。
var startupOnce sync.Map

// logStartupStageOnce 同 LogStartupStage，但同一 key 只记一次。
func logStartupStageOnce(key, stage string) {
	if _, loaded := startupOnce.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	LogStartupStage(stage)
}

// 工具函数（避免导入额外包）。
//
// newUUID 生成**实例/profile 短标识**（形态 `gui-<数字>`，源自 CreateTemp 唯一后缀），
// **刻意非 RFC-4122 UUID**：它同时用作 WebView2 用户数据目录名（见 webviewDataDir），
// `webviewProfilePrefix = "gui-"` 的孤儿清理（PruneWebviewProfiles）依赖该前缀——改前缀会
// 破坏 profile 清理，故**不得**替换为 idgen.NewUUID()。真实 UUID（session/turn/req_id 等）
// 一律走 idgen.NewUUID()（见 flash/session.go、bridge/bridge.go）。
func newUUID() string {
	f, err := os.CreateTemp("", "gui-*.uuid")
	if err != nil {
		return "gui-" + fmt.Sprint(os.Getpid())
	}
	name := filepath.Base(f.Name())
	_ = f.Close()
	_ = os.Remove(f.Name())
	return strings.TrimSuffix(name, ".uuid")
}

// WebviewProfilesRoot 返回 WebView2 用户数据目录根（~/.chonkpilot/webview2）。
func WebviewProfilesRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, ".chonkpilot", "webview2")
}

// webviewDataDir 返回本实例专属的 WebView2 用户数据目录（根/<instance_id>）。
// 多实例共享同一 profile 会共享浏览器进程、互相波及（一方销毁导致另一方窗口冻结），
// 故按 instance_id 一实例一份。
func webviewDataDir(instanceID string) string {
	return filepath.Join(WebviewProfilesRoot(), instanceID)
}

// webviewProfilePrefix 是本进程创建的 WebView2 profile 目录名前缀：实例 id 由 newUUID 生成
// （`CreateTemp("", "gui-*.uuid")` → `gui-<数字>`），profile 目录名 = 实例 id（见 webviewDataDir）。
// 清理只认该前缀，避免误删根下其它内容。
const webviewProfilePrefix = "gui-"

// webviewProfileMarker 是每个 profile 根目录下的活跃标记文件名：窗口创建时写入本进程 pid，
// 窗口收尾时随目录一并 RemoveAll。prune 借此区分「正在被其它实例使用」的活跃 profile（D-34）。
const webviewProfileMarker = ".active"

// stillActiveProcess = GetExitCodeProcess 的 STILL_ACTIVE 值（Windows 常量 259）。
const stillActiveProcess = 259

// markProfileActive 写入活跃标记（内容 = 本进程 pid）；尽力而为，失败仅告警不阻断建窗。
func markProfileActive(dir string) {
	if err := os.WriteFile(filepath.Join(dir, webviewProfileMarker),
		[]byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		slog.Warn("webview profile active marker write failed", "dir", dir, "err", err)
	}
}

// profileInUse 判定 profile 目录是否「正在被使用」：读 `.active` 标记内的 pid，进程仍存活 → true。
// 无标记 / pid 非法 / 进程已退出 → false（视为可清理的残留）。仅用于 prune 的占用防护（D-34）。
func profileInUse(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, webviewProfileMarker))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false // 打不开句柄（进程已退出 / 无权限）→ 非占用
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActiveProcess
}

// PruneWebviewProfiles 尽力而为清理孤儿 profile（被强杀/崩溃、未走退出清理的目录）：
//   - 只清 `<root>/gui-*` 目录（本进程命名，见 webviewProfilePrefix）——不动根下其它内容；
//   - **占用即跳过（D-34）**：目录内有 `.active` 标记且其 pid 进程存活 → 正在被其它实例使用，
//     绝不删除（多项目并行时保护活跃 profile；NTFS 下仅建/删/改名目录项才更新父目录 mtime，
//     持续写子目录文件不保证更新 profile 根 mtime，单靠 mtime 会误删活跃目录）；
//   - 无标记 / 标记 pid 已退出 → 判为残留：再要求 LastWriteTime 早于 24h 才清；
//   - 删除失败（被占用等）逐条跳过并忽略，**不阻断启动**。
//
// 返回（清理数量, 释放字节数）供日志与测试断言。由 host 启动期调用一次。
func PruneWebviewProfiles(root string) (removed int, freed int64) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, 0
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), webviewProfilePrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if profileInUse(dir) {
			continue // 占用（活跃标记的 pid 存活）→ 跳过，绝不删活跃 profile
		}
		size := dirSize(dir)
		if err := os.RemoveAll(dir); err != nil {
			continue // 被占用/失败：跳过并忽略（不阻断启动）
		}
		removed++
		freed += size
	}
	if removed > 0 {
		slog.Info("webview 残留 profile 已清理", "count", removed, "freed_mb", freed/(1024*1024))
	}
	return removed, freed
}

// dirSize 递归累加目录内文件字节数（尽力而为；读取失败忽略 → 仅影响日志/返回值估算）。
func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, e := d.Info(); e == nil {
			total += fi.Size()
		}
		return nil
	})
	return total
}
