//go:build windows

// 客户端导出面：供 **host 装配包**（`chonkpilot-gui/host`，单体装配宿主）单向调用的最小 API。
//
// 方向约束（拆包目标）：`host` → `gui` 单向依赖；**`gui` 绝不 import `host`**。本文件把窗口
// 工厂/桥/测试通道等**客户端**能力以导出构造器 + 句柄暴露给 host，使 host 只需调导出 API 即可
// 完成单体装配，而 gui 包本身不再链接 llm/server / filesys / plugin* / gateway 等重依赖
// （flash 薄壳 `import chonkpilot-gui` 遂不再把重依赖编进产物）。
package gui

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"

	"github.com/chonkpilot/chonkpilot-gui/bridge"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// Server 是 gui 客户端窗口装配实际依赖的内嵌会话服务方法子集（**解耦 chonkpilot-llm/server**）：
// 仅两个判定方法。host 装配包传入具体 `*server.Server`（满足本接口），gui 侧只按接口调用。
type Server interface {
	// RequireAuth 判定本形态是否要求认证（desktop=false / gui=true）。
	RequireAuth() bool
	// Authenticated 判定令牌是否有效（首屏 `authed` 判定）。
	Authenticated(token string) bool
}

// EnvConfig 是 host 装配包构造 Env 的参数（与 hostEnv 字段一一对应）。
type EnvConfig struct {
	Bus            mq.Bus
	DistFS         fs.FS // 已 fs.Sub 到 dist 根（embed 前缀 frontend/dist 已剥离）
	WorkDir        string
	DataDir        string // 数据层分支判据（desktop 缺省为空，12-数据层 §3）
	ProjectDataDir string // 项目数据根（prj 层；fileserver 白名单用）
	PrjUsrRoot     string // prjusr 数据根（附件/截图落盘根）
	LogDir         string
	Form           string
	// Server = 内嵌会话服务（nil = --no-server UI 注入测试模式 / 远程薄壳模式）。
	// ⚠️ host 传入 **nil 接口值**（非 typed-nil），否则 `env.srv != nil` 判定会失真。
	Server Server
	// DataOffline = 本地无数据服务（--no-server UI 注入测试模式）：置位后桥 data-* 快速失败
	// 返回空信封，不经总线等 3s 超时。远程薄壳（flash）**非**此处（其 data-* 经 UDS 转发）。
	DataOffline bool
}

// Env 是窗口工厂的进程级装配上下文（host 经 NewEnv 构造后单向调用）。
// 导出别名仅为 host 提供句柄；内部字段保持非导出。
type Env = hostEnv

// NewEnv 由 host 装配包构造进程级窗口装配上下文（注册表一并建立）。
func NewEnv(cfg EnvConfig) *Env {
	return &hostEnv{
		bus:            cfg.Bus,
		distFS:         cfg.DistFS,
		workDir:        cfg.WorkDir,
		dataDir:        cfg.DataDir,
		projectDataDir: cfg.ProjectDataDir,
		prjUsrRoot:     cfg.PrjUsrRoot,
		logDir:         cfg.LogDir,
		form:           cfg.Form,
		srv:            cfg.Server,
		dataOffline:    cfg.DataOffline,
		windows:        newWindowRegistry(),
		projects:       map[string]*projectCtx{},
	}
}

// Window 是一个窗口宿主句柄（host 只读使用）。
type Window = windowHost

// Done 返回本窗口关闭（Run 返回且收尾完成）的信号。
func (h *windowHost) Done() <-chan struct{} { return h.done }

// Bridge 返回本窗口的桥（host 主窗口接线读 prj 配置用）。
func (h *windowHost) Bridge() *bridge.Bridge { return h.br }

// ProjectConfig 描述一个项目主窗口（PRODSIM-1：单进程多项目，同 work_dir 唯一）。
type ProjectConfig struct {
	WorkDir        string
	DataDir        string // 数据层分支判据（desktop 缺省空）
	ProjectDataDir string
	PrjUsrRoot     string
	Title          string
	// OnWired 在**窗口线程**上、webview 建好（句柄/桥/handler 就绪）后、导航前调用；
	// 返回错误 → 中止建窗。
	OnWired func(*Window) error
}

// ProjectInfo 是托盘「项目列表」的只读条目。
type ProjectInfo struct {
	WorkDir string
	Title   string
}

// OpenProject 打开或**激活**一个项目的主窗口（同 work_dir 唯一，PRODSIM-1）：
//   - 已打开 → ShowWindow 前置该主窗口，返回 (window, true, nil)；
//   - 未打开 → 新建主窗口并登记 `work_dir → 项目上下文`，返回 (window, false, nil)。
//
// 主窗口 URL 携带 `?work-dir=<path>`（前端 claim 时作为提议上送，服务端不再回落启动参数）。
func (e *Env) OpenProject(cfg ProjectConfig) (*Window, bool, error) {
	if old := e.lookupProject(cfg.WorkDir); old != nil && old.mainHost != nil {
		old.mainHost.show()
		return old.mainHost, true, nil
	}
	p := &projectCtx{
		workDir:        cfg.WorkDir,
		dataDir:        cfg.DataDir,
		projectDataDir: cfg.ProjectDataDir,
		prjUsrRoot:     cfg.PrjUsrRoot,
		windows:        newWindowRegistry(),
	}
	got, created := e.addProject(p)
	if !created && got.mainHost != nil {
		got.mainHost.show()
		return got.mainHost, true, nil
	}
	p = got

	var onWired func(*windowHost) error
	if cfg.OnWired != nil {
		onWired = func(h *windowHost) error { return cfg.OnWired(h) }
	}
	h, err := startWindow(e, windowSpec{
		role:      roleMain,
		proj:      p,
		title:     cfg.Title,
		url:       appOrigin + "/?work-dir=" + url.QueryEscape(cfg.WorkDir),
		frameless: true,
		// owner 暂不启用（PRODSIM-1）：Windows 的 owned window **不出现在任务栏**，
		// 主窗口设 owner=日志窗口 会让用户无法从任务栏切回。窗口从属关系改造留待真机验收。
		onWired: onWired,
	})
	if err != nil {
		e.removeProject(p)
		return nil, false, err
	}
	p.mainHost = h
	p.windows.setMain(h)
	return h, false, nil
}

// SetProjectOpener 注入「同进程打开项目」回调（gui.dir.open 用；PRODSIM-1）。
// nil = 未接线（旧行为：桥 spawn 新进程）。
func (e *Env) SetProjectOpener(fn func(dir string) error) { e.projectOpener = fn }

// Projects 返回当前已打开项目（托盘「项目列表」）。
func (e *Env) Projects() []ProjectInfo {
	ps := e.projectsSnapshot()
	out := make([]ProjectInfo, 0, len(ps))
	for _, p := range ps {
		out = append(out, ProjectInfo{WorkDir: p.workDir, Title: MainWindowTitle(p.workDir)})
	}
	return out
}

// ActivateProject 激活指定 work_dir 的主窗口（托盘点选项目；未打开 → false）。
func (e *Env) ActivateProject(workDir string) bool {
	if p := e.lookupProject(workDir); p != nil && p.mainHost != nil {
		p.mainHost.show()
		return true
	}
	return false
}

// LogWindowSpec 描述日志窗口（PRODSIM-1）：进程级唯一、创建后默认隐藏、关闭 = 收起。
type LogWindowSpec struct {
	Title string
}

// StartLogWindow 创建日志窗口（隐藏 + 关闭=收起）。它是 server 进程的窗口载体：
// 所有项目主窗口关闭后进程仍靠它 + 托盘常驻。
func (e *Env) StartLogWindow(spec LogWindowSpec) (*Window, error) {
	h, err := startWindow(e, windowSpec{
		role:        roleLog,
		title:       spec.Title,
		url:         appOrigin + "/#log",
		frameless:   false, // 原生标题栏（可最小化/拖动；关闭由 HideOnClose 接管）
		hidden:      true,
		hideOnClose: true,
	})
	if err != nil {
		return nil, err
	}
	e.setLogWindow(h)
	return h, nil
}

// ShowLogWindow 显示并前置日志窗口（托盘「显示日志」）。
func (e *Env) ShowLogWindow() {
	if h := e.logWindow(); h != nil {
		h.show()
	}
}

// HideLogWindow 隐藏日志窗口（托盘「隐藏日志」/ 关闭时收起）。
func (e *Env) HideLogWindow() {
	if h := e.logWindow(); h != nil {
		h.hide()
	}
}

// LogWindowHWND 返回日志窗口句柄（0 = 未创建；托盘/owner 接线用）。
func (e *Env) LogWindowHWND() uintptr {
	if h := e.logWindow(); h != nil {
		return h.hwnd
	}
	return 0
}

// CloseLogWindow 强制关闭日志窗口（程序退出收尾；无视 HideOnClose）。
func (e *Env) CloseLogWindow() {
	if h := e.logWindow(); h != nil {
		h.destroyForced()
	}
}

// Shutdown 关闭**所有项目**（主窗口 + 小窗口）与进程级窗口表（flash）。
// 托盘「退出」收尾用；主窗口关闭本身不退出进程（见 finish）。
func (e *Env) Shutdown() {
	for _, p := range e.projectsSnapshot() {
		p.windows.shutdown()
		if p.mainHost != nil {
			p.mainHost.destroy()
		}
	}
	e.windows.shutdown()
}

// HasTestServer 判定 --test-port 通道是否已接线（多项目时只绑进程内**第一个**主窗口）。
func (e *Env) HasTestServer() bool { return e.testServer() != nil }

// TestServer 是 --test-port 测试通道句柄（host 收尾时 Shutdown）。
type TestServer struct{ *testServer }

// AttachTestServer 为主窗口接线 --test-port 测试通道（在 onWired 内、导航前调用）。
// 逐条等价于改前 runMain 的主窗口 onWired：建通道 → 起步监听 → 绑定句柄/销毁回调/handler →
// 登记为进程级测试目标 → 注入 console 捕获 → 置就绪门控。
// 返回 nil + error 表示通道不可用（webview 无 ScriptEval 能力）。
func (e *Env) AttachTestServer(h *Window, port int) (*TestServer, error) {
	ts := newTestServer(h.w, h.br)
	if ts == nil {
		return nil, errors.New("test server: webview lacks ScriptEval")
	}
	if err := ts.Start(port); err != nil {
		return nil, fmt.Errorf("test server start failed: %w", err)
	}
	ts.hwnd = h.hwnd
	ts.destroy = h.destroy
	ts.handler = h.handler
	e.setTestServer(ts)
	ts.InjectConsoleCapture(h.chromium)
	h.onReady = ts.SetReady
	return &TestServer{ts}, nil
}
