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
		bus:                  cfg.Bus,
		distFS:               cfg.DistFS,
		workDir:              cfg.WorkDir,
		dataDir:              cfg.DataDir,
		projectDataDir:       cfg.ProjectDataDir,
		prjUsrRoot:           cfg.PrjUsrRoot,
		logDir:               cfg.LogDir,
		form:                 cfg.Form,
		srv:                  cfg.Server,
		dataOffline:          cfg.DataOffline,
		windows:              newWindowRegistry(),
	}
}

// Window 是一个窗口宿主句柄（host 只读使用）。
type Window = windowHost

// Done 返回本窗口关闭（Run 返回且收尾完成）的信号。
func (h *windowHost) Done() <-chan struct{} { return h.done }

// Bridge 返回本窗口的桥（host 主窗口接线读 prj 配置用）。
func (h *windowHost) Bridge() *bridge.Bridge { return h.br }

// MainWindowSpec 描述主窗口（标题 + 建窗后、导航前的接线回调）。
type MainWindowSpec struct {
	Title string
	// OnWired 在**窗口线程**上、webview 建好（句柄/桥/handler 就绪）后、导航前调用；
	// 返回错误 → 中止建窗（宿主据此弹框退出）。
	OnWired func(*Window) error
}

// StartMainWindow 创建主窗口（与改前 runMain 逐条一致：几何恢复 / 拦截器 / lifecycle /
// 显示时机由窗口工厂承担；URL / frameless 为固定主窗口取值）。
func (e *Env) StartMainWindow(spec MainWindowSpec) (*Window, error) {
	var onWired func(*windowHost) error
	if spec.OnWired != nil {
		onWired = func(h *windowHost) error { return spec.OnWired(h) }
	}
	return startWindow(e, windowSpec{
		role:      roleMain,
		title:     spec.Title,
		url:       appOrigin + "/",
		frameless: true,
		onWired:   onWired,
	})
}

// SetMain 登记主窗口（StartMainWindow 成功后调用一次；广播/收尾用）。
func (e *Env) SetMain(h *Window) { e.windows.setMain(h) }

// Shutdown 关闭全部对话窗口（主窗口关闭后调用；主窗口关闭 = 退出本进程）。
func (e *Env) Shutdown() { e.windows.shutdown() }

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
