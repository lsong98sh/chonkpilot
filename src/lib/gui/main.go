//go:build windows

package gui

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-filesys"
	"github.com/chonkpilot/chonkpilot-gui/bridge"
	"github.com/chonkpilot/chonkpilot-gui/internal/fileserver"
	"github.com/chonkpilot/chonkpilot-gui/models"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-llm/server"
	"github.com/chonkpilot/chonkpilot-plugin"
	"github.com/chonkpilot/chonkpilot-plugin-codegraph"
	"github.com/chonkpilot/chonkpilot-plugin-compress"
	"github.com/chonkpilot/chonkpilot-plugin-history"
	"github.com/chonkpilot/chonkpilot-plugin-memory"
	"github.com/chonkpilot/chonkpilot-plugin-vfts"
	webview2 "github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

// 宿主形态（61 §4.6：三形态 = desktop / gui / browser）—— **由壳静态给定**（见 Main 的
// `opts.Form`），不再由本包的编译开关（原 form_inprocess.go / form_split.go 的
// `runtimeForm()`）决定宿主分支。
const (
	// FormDesktop = 桌面单体（GUI 内嵌 server，本机单用户、免鉴权）。
	FormDesktop = "desktop"
	// FormGui = GUI 客户端 + 独立 server（可异地 → 要求认证）。
	FormGui = "gui"
)

// Options 是壳传给宿主的静态启动参数。
//
// 注：其余启动参数（`--work-dir` / `--data-dir` / `--bridge-url` / `--test-port` /
// `--llm-base` / `--llm-model` / `--no-server`）仍由本包在 Main 内解析命令行 —— 两个壳
// （`src/desktop` / `src/gui`）只差**形态**一项，故只有 Form 经 Options 传入。
type Options struct {
	// Form 是本宿主运行形态（61 §4.6）：FormDesktop（桌面单体）| FormGui（GUI 客户端）。
	// 随首屏注入给前端（`window.__ck.form`）并下发内嵌 server（Options.Form），
	// 决定 claim 鉴权分支与首屏 requireAuth。
	Form string
}

// appOrigin 是虚拟宿主源，请求经 WebResourceRequested 拦截（无 HTTP server）。
const appOrigin = "https://app.localhost"

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
	return r != 0 && (wp.ShowCmd == 3 || wp.ShowCmd == 2) // SW_MAXIMIZE / SW_SHOWMAXIMIZED
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
// 一次 PrjConfigList 取回（含 prj 层回落值），细 key 缺失时兼容读取 legacy 整块 "window"。
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

// windowStateFromConfig 从扁平配置 map 还原窗口状态：优先 window.<字段> 细 key，
// 回落 legacy 整块 "window"（v6 迁移期兼容）。无任何窗口记录 → ok=false。
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
	// boolean：布尔字面量（前端 cfgScalar 存 "true"/"false"）与 legacy 数值（1/0）都认。
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
	// legacy 整块回落
	var legacy windowState
	if s := str(cfg["window"]); s != "" {
		if err := json.Unmarshal([]byte(s), &legacy); err == nil {
			return legacy, true
		}
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
	// setTitle 更新本窗口标题（gui.window.set-title）：**仅对话窗口**接线；主窗口 nil → ok=false。
	setTitle func(title string)
	// form 是本宿主运行形态（61 §4.6：desktop / gui）——**由壳传入**（Main 的 opts.Form），
	// 首屏注入 `window.__ck.form` 用（见 serveStatic）。
	form string
	// ── 认证域首屏注入（61 §4.6；阶段 2b-2）──
	// requireAuth = 形态决定（desktop=false；gui=true）；authed = 读凭证判定
	// （桥持有的令牌有效 = true）。两者**均由服务端（宿主）判定**，前端零额外往返。
	requireAuth bool
	authed      func() bool
}

func (h *appHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case path == "/publish":
		h.handlePublish(w, r)
	case strings.HasPrefix(path, "/show/"):
		h.fileHandler.ServeHTTP(w, r)
	default:
		h.serveStatic(w, r, path)
	}
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
	if body.Type == "gui.window.status" {
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
		if h.br.DispatchLocal(body.Type) {
			return nil, nil
		}
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
		br.EmitFrontend("window-maximized-changed", string(env))
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

// serveWebResource 适配 WebView2 请求到 http.Handler。
func serveWebResource(h http.Handler, c *edge.Chromium, request *edge.ICoreWebView2WebResourceRequest, rawURI string) (*edge.ICoreWebView2WebResourceResponse, error) {
	method, err := request.GetMethod()
	if err != nil {
		return nil, err
	}
	var body []byte
	if method == "POST" || method == "PUT" {
		if stream, err := request.GetContent(); err == nil && stream != nil {
			body, _ = io.ReadAll(stream)
		}
	}
	url, err := request.GetUri()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, url, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	rr := &recorder{header: make(http.Header), status: 200}
	h.ServeHTTP(rr, req)
	contentType := rr.header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain; charset=utf-8"
	}
	resp, err := c.CreateWebResourceResponse(rr.body, rr.status, "OK", "Content-Type: "+contentType)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

type recorder struct {
	header http.Header
	body   []byte
	status int
}

func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) Write(b []byte) (int, error) { r.body = append(r.body, b...); return len(b), nil }
func (r *recorder) WriteHeader(s int)           { r.status = s }

// Main 是宿主入口（webview2 宿主 + 内嵌 server/persist/filesys + 插件装配 + 桥接线）。
//
// **调用方 = 壳**（`src/desktop` = 桌面单体 / `src/gui` = GUI 客户端）：壳只做两件事 ——
// `//go:embed all:frontend/dist` 拿到前端 dist，并以 `opts.Form` 声明形态；宿主实现
// **在本包只有一份**（方案 B：零重复）。
//
// distFS 必须是含 `frontend/dist` 前缀的嵌入文件系统（壳的 `//go:embed all:frontend/dist`），
// 本函数内部 `fs.Sub(distFS, "frontend/dist")` 取 dist 根（口径与改前一致）。
// 启动参数（--work-dir/--data-dir/--bridge-url/--test-port/--llm-base/--llm-model/--no-server）
// 仍在本函数内解析（两个壳只有形态不同，不重复解析代码）。
func Main(distFS fs.FS, opts Options) {
	// 日志级别：先以缺省级别装好 logger（早于配置可用无妨），待桥/数据面就绪后经既有配置
	// 通道读 prj logLevel 覆盖并订阅变更即时生效（见 loglevel.go）。
	initLogging()

	var workDir, dataDir, bridgeURL string
	var legacyNatsURL string
	var testPort int
	var llmBase, llmModel string
	var noServer bool
	flag.StringVar(&workDir, "work-dir", "", "工作目录（前端文件树根）")
	flag.StringVar(&dataDir, "data-dir", "", "项目数据根（默认 <workDir>/.chonkpilot；显式给出时 prj/prjusr 同根，缺省时 prjusr 落 ~/.chonkpilot/data/<prj-id>）")
	flag.StringVar(&bridgeURL, "bridge-url", "", "分离形态（-tags split 编译）下 bridge 对端地址（HTTP）；合并单进程形态（默认构建）忽略")
	// 兼容旧名（已废弃，勿在新脚本中使用）：nats-url 语义已演进为 bridge-url，
	// 仅在未显式给出 --bridge-url 时生效，避免既有脚本/参数失效。
	flag.StringVar(&legacyNatsURL, "nats-url", "", "已废弃别名：等价于 --bridge-url（仅 --bridge-url 未给出时生效）")
	flag.IntVar(&testPort, "test-port", 0, "测试通道端口（0=禁用；>0 监听 127.0.0.1 供外部脚本驱动）")
	flag.StringVar(&llmBase, "llm-base", "http://127.0.0.1:8901/v1", "OpenAI 兼容 base URL（inprocess 会话服务）")
	flag.StringVar(&llmModel, "llm-model", "mock", "默认 LLM 模型名（inprocess 会话服务）")
	flag.BoolVar(&noServer, "no-server", false, "不启动 inprocess 会话服务（UI 注入测试模式：注入事件完全控制回合，无真实 LLM）")
	flag.Parse()
	if bridgeURL == "" {
		bridgeURL = legacyNatsURL
	}

	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "cwd:", err)
			os.Exit(1)
		}
	}
	workDir = models.ResolveDir(workDir, "")
	if dataDir != "" {
		models.SetDataDir(models.ResolveDir(dataDir, workDir))
	}
	// 项目数据根（prj 层所在：缺省 <workDir>/.chonkpilot；显式 --data-dir → 该目录）。
	projectDataDir := models.DataDir(workDir)
	// 传给 server / 桥的 data_dir（**数据层分支判据**；12-数据层 §3 · §5.3）：
	//   - 显式 --data-dir 恒生效（prj 与 prjusr 同根，兼容既有脚本/L2 用例）；
	//   - desktop 缺省（未给 --data-dir）**置空** → 数据层走 project-id 分支 → 个人运行态
	//     （会话/任务树/快照）落 `~/.chonkpilot/data/<prj-id>/`（B 方案，[24 §3.2] MW-7）。
	//     ⚠️ 不可再传 `models.DataDir(workDir)`：该访问器**恒非空**（缺省 = 项目数据根），
	//     会让 prjusr 与 prj 同库（个人运行态落进项目目录）。
	//   - gui 形态缺省：保持项目数据根（客户端薄壳；服务端另按用户数据根落库，本轨不改）。
	dataDirArg := projectDataDir
	if dataDir == "" && opts.Form == FormDesktop {
		dataDirArg = ""
	}
	// prjusr 数据根（个人运行态非库文件的落盘根：日志 logs/、附件/截图 tmp/uploads/、
	// fileserver 白名单；[24 §3.2] MW-8）。**必须先读 prj 库拿 project-id 才能算出**
	// （不可拿 models.DataDir(workDir) 当 prjusr 根，见 12-数据层 §3 实施注意）。
	prjUsrRoot, err := data.PrjUsrDir(workDir, dataDirArg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "prjusr data root:", err)
		os.Exit(1)
	}
	// GUI 文件日志（可诊断性，2026-09-19）：windowsgui 下无控制台、stderr 不可见 → 在 prjusr
	// 数据根 `logs/` 挂滚动文件 sink（<prjUsrRoot>/logs/gui.log，按大小滚动，见 logfile.go）。
	// 失败降级为「仅 stderr」，不阻断启动。日志级别仍由 prj `logLevel` 运行时控制（同一 LevelVar）。
	logDir, logErr := attachFileLog(prjUsrRoot)
	if logErr != nil {
		slog.Warn("file log attach failed", "err", logErr)
	} else {
		defer detachFileLog() // 退出时关闭文件 sink（幂等）
		slog.Info("file log enabled", "dir", logDir)
	}

	// 消息总线：命名空间前缀 chonk. 在此注入一次（server/gateway/persist/filesys/bridge
	// 共享同一总线），业务 publish/subscribe 一律写相对主题，见 61-消息一览 §0.1。
	// 注：instance_id 由**窗口工厂每窗口**生成（一窗口一实例，24 §2.2 C1）。
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		slog.Error("mq new failed", "err", err)
		os.Exit(1)
	}

	// chonkpilot-filesys 文件服务：订阅 filesys.* 请求（list/content/create/…/watch/
	// unwatch）→ 写回 Result/Errors；watch 变更经 fsnotify 广播 filesys.changed →
	// 桥 ">" 订阅转发前端（文件域直连，61-消息一览 §2；-no-server 离线 UI 注入
	// 测试仍可用文件树）。
	fsys := filesys.New(bus)
	if err := fsys.Start(); err != nil {
		slog.Error("filesys start failed", "err", err)
		os.Exit(1)
	}
	defer fsys.Stop()

	// ── inprocess 会话服务（GUI 内嵌 chonkpilot-server lib，同进程内存 MQ）──
	// server v2 内嵌 persist（data-* 数据面应答）+ gateway + mcp-server，数据根由
	// bridge.Start 的 instance-register 绑定（persist 自持实例视图，61-消息一览 §4.1），
	// 宿主不再直接打开/持有数据层句柄。
	// -no-server：跳过（UI 注入测试模式，注入事件完全控制回合，无真实 LLM/数据服务）。
	var srv *server.Server
	if !noServer {
		srv = server.New(bus, server.Options{
			LLMBase:  llmBase,
			LLMModel: llmModel,
			// 服务端启动参数下发（唯一消费点 = instance-claim 的 desktop 回落，见 61 §4.1 ①：
			// 本机单用户 + --work-dir/--data-dir）。data_dir = `dataDirArg`（desktop 缺省为空 →
			// persist/instance 绑定 data_dir 空 → prjusr 按 project-id 落 ~/.chonkpilot/data/<id>/，
			// 见 12-数据层 §5.1 / [24 §3.2] MW-7）。
			WorkDir: workDir,
			DataDir: dataDirArg,
			// 运行形态（61 §4.6；阶段 2b-2）：**服务端判定**的形态来源（desktop / gui，
			// **由壳经 Options.Form 传入**）→ claim 鉴权分支与首屏 requireAuth 均据此判定；
			// **非 UA、非客户端自报**（22 §1）。
			Form: opts.Form,
			// 内嵌 server/插件的诊断输出（原先只写 stdout，windowsgui 下不可见）经同一文件 sink
			// 落 <dataDir>/logs/gui.log（2026-09-20；未挂上 sink = nil → 仅 stdout，行为不变）。
			LogWriter: logWriter.fileSink(),
			// 内嵌插件：compress（llm-compress 压缩）+ memory（每轮异步沉淀记忆）+ history（git 快照）+ codegraph/vfts（索引工具面，默认关闭）；就绪后广播 server-starting
			// 插件与入口桥依赖的门面绑定在**装配处**选择（23 §7）：本形态 = inline（同进程直调；
			// bus 传宿主实际总线 → config 域写入的变更广播照旧送达订阅方，见 facade/inline）
			Plugins: []plugin.Hook{
				compress.New(compress.DefaultOptions(), inline.New(bus)),
				memory.New(memory.DefaultOptions()),
				history.New(),
				codegraph.New(codegraph.Options{}),
				vfts.New(vfts.Options{}),
			},
		})
		if err := srv.Start(context.Background()); err != nil {
			slog.Error("server start failed", "err", err)
			os.Exit(1)
		}
		defer srv.Stop()
	}

	// WebView2 用户数据目录必须**一实例一份**：fork 默认 DataPath = %AppData%\<exe名>，
	// 会让同一 exe 的多窗口共享同一 profile → 共享同一个浏览器进程（msedgewebview2.exe），
	// 任一方销毁/崩溃都会波及另一方的窗口（历史实测：销毁一个实例会冻结其他窗口）。
	// 多窗口起（24 §4.1）：**每窗口**一份（~/.chonkpilot/webview2/<instance_id>，由窗口工厂
	// 创建并清理）；前端 ui.locale 有 DB 兜底（MainLayout），不依赖 WebView2 缓存留存。
	wvRoot := webviewProfilesRoot()
	pruneWebviewProfiles(wvRoot) // 清掉超期残留 profile（强杀/崩溃未走退出清理的目录）

	subFS, err := fs.Sub(distFS, "frontend/dist")
	if err != nil {
		slog.Error("fs.Sub dist failed", "err", err)
		os.Exit(1)
	}

	// 进程级装配上下文：窗口工厂据此**每窗口**一份桥 + appHandler（MW-1/MW-2/MW-3）。
	env := &hostEnv{
		bus:            bus,
		distFS:         subFS, // embed 路径带 frontend/dist 前缀，Sub 后根即 dist 内容
		workDir:        workDir,
		dataDir:        dataDirArg, // 数据层分支判据（desktop 缺省为空，见上）
		projectDataDir: projectDataDir,
		prjUsrRoot:     prjUsrRoot,
		logDir:         logDir,
		form:           opts.Form,
		srv:            srv,
		windows:        newWindowRegistry(),
	}

	// 测试通道（--test-port）：仅绑**主窗口**（每窗口一份 appHandler 后，多窗口回归不覆盖，
	// 见 24 §4.5 / U-3）；console 捕获注入须在导航前（见 onWired）。
	var ts *testServer

	// 主窗口（MW-1：主窗口同走窗口工厂；几何恢复 / 拦截器 / lifecycle / Show 逐条与改前一致）。
	mainHost, err := startWindow(env, windowSpec{
		role:      roleMain,
		title:     mainWindowTitle(workDir), // 固定 Taskbar 标题 = chonkpilot-<工程目录名>（不随会话变化）
		url:       appOrigin + "/",
		frameless: true,
		onWired: func(h *windowHost) error {
			// 日志级别接线：数据面就绪后读 prj logLevel 初值（变更订阅见下方 watchLogLevel）。
			applyLogLevelFromConfig(logLevelVar, h.br.PrjConfigList())
			if testPort <= 0 {
				return nil
			}
			ts = newTestServer(h.w, h.br)
			if ts == nil {
				return errors.New("test server: webview lacks ScriptEval")
			}
			if err := ts.Start(testPort); err != nil {
				return fmt.Errorf("test server start failed: %w", err)
			}
			// testServer 的窗口命令（gui.window.status / 窗口面消息）需要句柄/销毁回调/handler。
			ts.hwnd = h.hwnd
			ts.destroy = h.destroy
			ts.handler = h.handler
			// 对话窗口建窗时据此登记为可选测试目标（window_id 路由，24 §4.5）。
			env.testSrv = ts
			// 测试模式注入 console 捕获（导航前）。
			ts.InjectConsoleCapture(h.chromium)
			h.onReady = ts.SetReady
			return nil
		},
	})
	if err != nil {
		slog.Error("main window create failed", "err", err)
		os.Exit(1)
	}
	env.windows.setMain(mainHost)
	// 进程级收尾（defer LIFO = 倒序执行）：总线是**进程唯一一份**，各窗口关闭只注销本实例
	// （Bridge.CloseInstance），总线由宿主在进程收尾时统一关闭。
	defer bus.Close()
	if testPort > 0 {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = ts.Shutdown(ctx)
		}()
	}

	// 桥与 appHandler 已下沉窗口工厂**每窗口一份**（window.go createWindow）：
	// 实例注册 / 认证接线 / 数据门面 / DevTools / 日志目录 / prjusr 根均在那处逐窗口装配。

	// 实例心跳发布侧（2026-09-20，阶段 2a）：**发布方由宿主改为前端 SPA**（61 §4.1 ②订正：
	// 语义 = 客户端续期）。分离形态（`-tags split`）下由前端在 `instance-claim` 成功后起 30s
	// `instance-heartbeat{instance_id}` 定时器（前端据首屏注入的 `__ck.form` 判定：desktop
	// 不发布、gui/browser 发布 —— 与宿主编译开关 `split` 同一口径）；服务端 90s 未收即
	// 回收（sweep_split.go，判定侧不变）。故本宿主**不再发布心跳**（原
	// `instance.StartHeartbeat(bus, instanceID, bridgeURL)` 调用移除）；`--bridge-url`（分离
	// 形态 bridge 对端地址）当前仅登记 + 日志（bridge 路由为分阶段落地项，见 23-工程与部署拓扑）。
	if bridgeURL != "" {
		slog.Info("split bridge endpoint", "bridge_url", bridgeURL)
	}

	// 日志级别：各窗口建窗时读 prj logLevel 初值（主窗口见上方 onWired）；此处订阅既有
	// data-prj-config-refresh（**进程级一次**）→ prj 配置一变更即应用、无需重启。
	watchLogLevel(bus)

	// 主窗口关闭 = 退出本进程（24 §4.4）：其余对话窗口一并关闭（各自发 instance-exit +
	// gui.window.closed），随后返回（总线由 defer 关闭）。
	<-mainHost.done
	env.windows.shutdown()
}

// 工具函数（避免导入额外包）。
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

// webviewProfilesRoot 返回 WebView2 用户数据目录根（~/.chonkpilot/webview2）。
func webviewProfilesRoot() string {
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
	return filepath.Join(webviewProfilesRoot(), instanceID)
}

// pruneWebviewProfiles 清理超期残留 profile（被强杀/崩溃、未走退出清理的目录）：
// 只删 >24h 未变动的目录，不会误伤仍在运行的实例。
func pruneWebviewProfiles(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !e.IsDir() {
			continue
		}
		if time.Since(info.ModTime()) > 24*time.Hour {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}
