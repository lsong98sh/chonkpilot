package pluginmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/winproc"
	"github.com/chonkpilot/chonkpilot-llm/pluginbridge"
)

// 前端 ↔ server 消息（相对主题；新增见设计稿 §10.2）。
const (
	TopicList           = "plugin-list"
	TopicRegistered     = "plugin-registered"
	TopicUnregistered   = "plugin-unregistered"
	TopicStatus         = "plugin-status"
	TopicSetEnabled     = "plugin-set-enabled"
	TopicCommand        = "plugin-command"
	TopicConfigGet      = "plugin-config-get"
	TopicConfigSet      = "plugin-config-set"
	TopicConfigChanged  = "plugin-config-changed"
	TopicCall           = "plugin-call"
	TopicUIEvent        = "plugin-ui-event"
)

// 插件状态。
const (
	StatusDiscovered  = "discovered"
	StatusStarting    = "starting"
	StatusRegistering = "registering"
	StatusRunning     = "running"
	StatusDegraded    = "degraded"
	StatusError       = "error"
	StatusStopping    = "stopping"
	StatusStopped     = "stopped"
	StatusDisabled    = "disabled"
)

// Options 是 Manager 的装配参数。
type Options struct {
	Bus     mq.Bus
	DataDir string
	// Roots 是 capability 根（**优先级从高到低**），如 prjusr / project / user / app。
	Roots []string
	Logf  func(format string, args ...any)

	RegisterMCP    func(ctx context.Context, name, url, token, namespace string, hot []string, category, origin string, timeout int) error
	UnregisterMCP  func(name string)
	ShutdownPlugin func(pluginID string) bool
	RequestPlugin  func(ctx context.Context, pluginID, method string, payload any) (any, error)
}

// Plugin 是一个 manifest 插件的运行态。
type Plugin struct {
	ID       string
	Root     string
	Manifest *Manifest

	mu          sync.Mutex
	status      string
	detail      string
	enabled     bool
	stopping    bool
	stopReq     bool
	supervising bool
	restarts    int
	pid         int
	startedAt time.Time
	ui        json.RawMessage
	tools     []string
	cmd       *exec.Cmd
	// gen 是运行代次（每次成功拉起一次进程 +1）：插件自持 webui 资源的宿主短缓存
	// 版本分量之一 —— 插件重启（同版本新进程）也据此立即失效（PLUGIN-UI-1）。
	gen uint64
}

// Manager 是插件平台宿主。
type Manager struct {
	opts Options

	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	plugins map[string]*Plugin
	order   []string
	enabled map[string]bool
	subs    []mq.Sub
	started bool

	// cfgDB 是插件配置的权威存储（usr 层 chonkpilot.db）；打不开则降级为文件态。
	cfgDB      *data.DB
	cfgRelease func()
}

// New 构造 Manager（未启动）。
func New(o Options) *Manager {
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	return &Manager{opts: o, plugins: map[string]*Plugin{}, enabled: map[string]bool{}}
}

func (m *Manager) logf(format string, args ...any) { m.opts.Logf(format, args...) }

// Start 发现并拉起启用插件，订阅管理消息。
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return nil
	}
	m.started = true
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.mu.Unlock()

	if err := os.MkdirAll(filepath.Join(m.opts.DataDir, "plugins"), 0o755); err != nil {
		return fmt.Errorf("pluginmgr: 创建数据目录: %w", err)
	}
	// 配置落库：权威 = usr 层 chonkpilot.db；打不开则降级为文件态（仍可运行）。
	if db, release, err := openConfigDB(); err != nil {
		m.logf("plugin-config: 打开配置库失败（降级文件态）: %v", err)
	} else {
		m.cfgDB, m.cfgRelease = db, release
	}
	m.loadEnabled()
	m.discover()
	m.installMessages()

	m.mu.Lock()
	list := make([]*Plugin, 0, len(m.order))
	for _, id := range m.order {
		list = append(list, m.plugins[id])
	}
	m.mu.Unlock()
	for _, p := range list {
		m.publishRegistered(p)
		if !p.enabled {
			m.setStatus(p, StatusDisabled, "")
			continue
		}
		if p.Manifest.Runtime.Mode == "managed" {
			m.startSupervise(p)
		} else {
			m.setStatus(p, StatusStarting, "等待外部进程连接")
		}
	}
	return nil
}

// Stop 停掉全部插件（逆序）并退订。
func (m *Manager) Stop() {
	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		return
	}
	m.started = false
	cancel := m.cancel
	subs := m.subs
	m.subs = nil
	list := make([]*Plugin, 0, len(m.order))
	for i := len(m.order) - 1; i >= 0; i-- {
		list = append(list, m.plugins[m.order[i]])
	}
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	for _, sub := range subs {
		_ = sub.Unsubscribe()
	}
	for _, p := range list {
		m.stopPlugin(p, false)
	}
	if m.cfgRelease != nil {
		m.cfgRelease()
		m.cfgRelease = nil
		m.cfgDB = nil
	}
}

// Hooks 返回插件桥回调（宿主在 pb.Start 前注入）。
func (m *Manager) Hooks() pluginbridge.PluginHooks {
	return pluginbridge.PluginHooks{
		RegisterMCP:   m.registerMCP,
		UnregisterMCP: m.unregisterMCP,
		RegisterUI:    m.registerUI,
		Status:        m.pluginStatus,
		Publish:       m.publishUIEvent,
		DataGuard:     m.dataGuard,
		Storage:       m.storage,
	}
}

func (m *Manager) discover() {
	found := map[string]*Plugin{}
	for _, base := range m.opts.Roots {
		base = strings.TrimSpace(base)
		if base == "" || !filepath.IsAbs(base) {
			continue
		}
		dir := filepath.Join(base, "plugins")
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			id := e.Name()
			if _, ok := found[id]; ok {
				continue // 更具体的级别已覆盖
			}
			root := filepath.Join(dir, id)
			mf, err := loadManifest(root, id)
			if err != nil {
				m.logf("plugin: 跳过 %s: %v", root, err)
				continue
			}
			found[id] = &Plugin{ID: id, Root: root, Manifest: mf, status: StatusDiscovered, ui: mf.uiPayload()}
		}
	}
	ids := make([]string, 0, len(found))
	for id := range found {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	m.mu.Lock()
	m.plugins = found
	m.order = ids
	for _, id := range ids {
		p := found[id]
		v, ok := m.enabled[id]
		if !ok {
			v = p.Manifest.Runtime.Autostart == nil || *p.Manifest.Runtime.Autostart
		}
		p.enabled = v
	}
	m.mu.Unlock()
}

// ── 进程守护 ─────────────────────────

// startSupervise 保证同一插件同时只有一个守护 goroutine（避免重复「禁用→启用」叠加）。
func (m *Manager) startSupervise(p *Plugin) {
	p.mu.Lock()
	if p.supervising {
		p.mu.Unlock()
		return
	}
	p.supervising = true
	p.mu.Unlock()
	go m.supervise(p)
}

func (m *Manager) supervise(p *Plugin) {
	defer func() {
		p.mu.Lock()
		p.supervising = false
		p.mu.Unlock()
	}()
	mf := p.Manifest
	backoff := time.Duration(mf.Runtime.Restart.BackoffMS) * time.Millisecond
	if backoff <= 0 {
		backoff = time.Second
	}
	maxBackoff := time.Duration(mf.Runtime.Restart.MaxBackoffMS) * time.Millisecond
	if maxBackoff <= 0 {
		maxBackoff = 30 * time.Second
	}
	maxFails := mf.Runtime.Restart.MaxFails
	if maxFails <= 0 {
		maxFails = 3
	}
	for {
		p.mu.Lock()
		if p.stopReq {
			p.mu.Unlock()
			return
		}
		p.mu.Unlock()
		m.setStatus(p, StatusStarting, "")
		err := m.runOnce(p)
		if err == nil {
			m.setStatus(p, StatusStopped, "")
			return
		}
		p.mu.Lock()
		stopping := p.stopReq
		p.restarts++
		n := p.restarts
		p.mu.Unlock()
		if stopping {
			m.setStatus(p, StatusStopped, "")
			return
		}
		if n > maxFails {
			m.setStatus(p, StatusError, err.Error())
			m.logf("plugin-failed id=%s restarts=%d err=%v", p.ID, n-1, err)
			return
		}
		m.setStatus(p, StatusStarting, fmt.Sprintf("异常退出，%s 后重启（%d/%d）", backoff, n, maxFails))
		select {
		case <-time.After(backoff):
		case <-m.ctx.Done():
			return
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (m *Manager) runOnce(p *Plugin) error {
	// runtime.exe 可以是插件目录内的 exe，也可以是 PATH 上的解释器（如 python / node）。
	exe := p.Manifest.Runtime.Exe
	if filepath.IsAbs(exe) || strings.ContainsAny(exe, `/\`) || strings.HasSuffix(strings.ToLower(exe), ".exe") {
		if !filepath.IsAbs(exe) {
			exe = filepath.Join(p.Root, exe)
		}
	}
	dataDir := filepath.Join(m.opts.DataDir, "plugins", p.ID)
	if err := os.MkdirAll(filepath.Join(dataDir, "logs"), 0o755); err != nil {
		return err
	}
	// 配置落库 → 每次 spawn 前物化运行时快照（插件侧仍按文件读取，行为不变）。
	if err := m.materializeConfig(p.ID); err != nil {
		m.logf("plugin-config: 物化失败 id=%s err=%v（继续启动，插件读到旧快照）", p.ID, err)
	}
	logPath := filepath.Join(dataDir, "logs", "plugin.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, p.Manifest.Runtime.Args...)
	cmd.Dir = p.Root
	// 插件 exe 为 console 子系统：宿主是 windowsgui 子系统，不隐藏会闪控制台黑框
	// （stdout/stderr 已重定向到插件日志文件，隐藏控制台不影响通信）。
	cmd.SysProcAttr = winproc.SysProcAttr()
	env := append(os.Environ(),
		"CHONKPILOT_PLUGIN_ADDR="+m.pluginAddr(),
		"CHONKPILOT_PLUGIN_ID="+p.ID,
		"CHONKPILOT_PLUGIN_ROOT="+p.Root,
		"CHONKPILOT_PLUGIN_DATA_DIR="+dataDir,
		"CHONKPILOT_PLUGIN_CONFIG="+filepath.Join(dataDir, "config.json"),
		"CHONKPILOT_PLUGIN_SECRETS="+filepath.Join(dataDir, "secrets.json"),
	)
	for k, v := range p.Manifest.Runtime.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("start: %w", err)
	}
	p.mu.Lock()
	p.cmd = cmd
	p.pid = cmd.Process.Pid
	p.startedAt = time.Now()
	p.gen++
	p.mu.Unlock()

	// 健康检查（manifest runtime.health）：连续 fail_threshold 次 ping 失败 → kill，
	// 由 supervise 走退避重启（与崩溃同一收口路径）。
	stopHealth := make(chan struct{})
	if m.opts.RequestPlugin != nil {
		go m.healthLoop(p, cmd, stopHealth)
	}
	err = cmd.Wait()
	close(stopHealth)
	_ = logFile.Close()
	p.mu.Lock()
	p.cmd = nil
	p.pid = 0
	p.mu.Unlock()
	return err
}

// healthLoop 周期性 ping 插件；连续失败达阈值后 kill 本进程（触发 supervise 重启）。
func (m *Manager) healthLoop(p *Plugin, cmd *exec.Cmd, stop <-chan struct{}) {
	h := p.Manifest.Runtime.Health
	interval := time.Duration(h.IntervalMS) * time.Millisecond
	if interval <= 0 {
		interval = 15 * time.Second
	}
	timeout := time.Duration(h.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	threshold := h.FailThreshold
	if threshold <= 0 {
		threshold = 3
	}
	tk := time.NewTicker(interval)
	defer tk.Stop()
	fails := 0
	for {
		select {
		case <-stop:
			return
		case <-m.ctx.Done():
			return
		case <-tk.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		_, err := m.opts.RequestPlugin(ctx, p.ID, "plugin.sys.ping", nil)
		cancel()
		if err == nil {
			fails = 0
			continue
		}
		fails++
		if fails < threshold {
			continue
		}
		m.logf("plugin-unhealthy id=%s fails=%d err=%v（kill 并重启）", p.ID, fails, err)
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return
	}
}

func (m *Manager) stopPlugin(p *Plugin, disabled bool) {
	p.mu.Lock()
	if p.stopping {
		p.mu.Unlock()
		return
	}
	p.stopping = true
	p.stopReq = true
	p.mu.Unlock()
	m.setStatus(p, StatusStopping, "")
	if m.opts.ShutdownPlugin != nil {
		m.opts.ShutdownPlugin(p.ID)
	}
	grace := time.Duration(p.Manifest.Runtime.Stop.GraceMS) * time.Millisecond
	if grace <= 0 {
		grace = 5 * time.Second
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		cmd := p.cmd
		p.mu.Unlock()
		if cmd == nil || cmd.Process == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	p.mu.Lock()
	cmd := p.cmd
	p.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	m.unregisterMCP(context.Background(), p.ID)
	p.mu.Lock()
	p.stopping = false
	if disabled {
		p.enabled = false
	}
	p.restarts = 0
	p.mu.Unlock()
	if disabled {
		m.setStatus(p, StatusDisabled, "")
	} else {
		m.setStatus(p, StatusStopped, "")
	}
}

func (m *Manager) pluginAddr() string {
	if v := strings.TrimSpace(os.Getenv("CHONK_PLUGIN_LISTEN")); v != "" {
		return v
	}
	return pluginbridge.DefaultAddr()
}

// ── 状态与广播 ─────────────────────────

func (m *Manager) setStatus(p *Plugin, status, detail string) {
	p.mu.Lock()
	p.status = status
	p.detail = detail
	p.mu.Unlock()
	m.broadcast(TopicStatus, map[string]any{"id": p.ID, "status": status, "detail": detail})
}

func (m *Manager) broadcast(topic string, payload any) {
	if m.opts.Bus == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	m.opts.Bus.Emit(ctx, topic, payload)
}

func (m *Manager) publishRegistered(p *Plugin) {
	m.broadcast(TopicRegistered, map[string]any{"plugin": m.view(p)})
}

func (m *Manager) publishUIEvent(pluginID, topic string, payload json.RawMessage) {
	m.broadcast(TopicUIEvent, map[string]any{"id": pluginID, "topic": topic, "payload": payload})
}

type pluginView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Icon        string `json:"icon"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Detail      string `json:"detail"`
	Enabled     bool   `json:"enabled"`
	Product     bool   `json:"product"`
	PID         int    `json:"pid"`
	StartedAt   string `json:"started_at"`
	// Command 是启动命令（managed = exe + args；external = 连接地址），插件维护卡片展示用。
	Command string `json:"command"`
	// ConfigView 是配置视图 id（约定 ui.views 中 id=config；空 = 未注册配置页 → 不显示配置按钮）。
	ConfigView string `json:"config_view"`
	// SelfServe 是「插件自持 webui」（`ui.selfServe`）：资源由插件经 UDS 提供，
	// 前端路由已统一 `/plugins/<id>/…`；本字段保留用于遥测 / 兼容（前端不再据此分流）。
	SelfServe bool      `json:"self_serve"`
	Views     []View    `json:"views"`
	Menus     []Menu    `json:"menus"`
	Statusbar []BarItem `json:"statusbar"`
	// Actions 是插件注册的动作按钮（名称 / 图标 / 命令 / 打开容器），与布局栏按钮同形。
	Actions      []ButtonAction `json:"actions"`
	ConfigFields []ConfigField  `json:"config_fields"`
	Tools        []string       `json:"tools"`
	Permissions  map[string]any `json:"permissions"`
}

func (m *Manager) view(p *Plugin) pluginView {
	p.mu.Lock()
	status, detail, pid, started, enabled := p.status, p.detail, p.pid, p.startedAt, p.enabled
	ui := p.ui
	tools := append([]string(nil), p.tools...)
	p.mu.Unlock()
	v := pluginView{
		ID: p.ID, Name: p.Manifest.Name, Version: p.Manifest.Version, Icon: p.Manifest.Icon,
		Description: p.Manifest.Description, Status: status, Detail: detail, Enabled: enabled,
		Product: p.Manifest.Product, PID: pid, Tools: tools,
		Command:      p.Manifest.launchCommand(),
		ConfigView:   p.Manifest.configViewID(),
		SelfServe:    p.Manifest.UI.SelfServe,
		Views:        p.Manifest.UI.Views,
		Menus:        p.Manifest.UI.Menus,
		Statusbar:    p.Manifest.UI.Statusbar,
		Actions:      p.Manifest.UI.Actions,
		ConfigFields: p.Manifest.Config.Fields,
	}
	if !started.IsZero() {
		v.StartedAt = started.Format(time.RFC3339)
	}
	if len(ui) > 0 {
		var dyn struct {
			Views     []View         `json:"views"`
			Menus     []Menu         `json:"menus"`
			Statusbar []BarItem      `json:"statusbar"`
			Actions   []ButtonAction `json:"actions"`
			SelfServe *bool          `json:"self_serve"`
		}
		if json.Unmarshal(ui, &dyn) == nil {
			if dyn.SelfServe != nil {
				v.SelfServe = *dyn.SelfServe
			}
			if len(dyn.Views) > 0 {
				v.Views = dyn.Views
			}
			if len(dyn.Menus) > 0 {
				v.Menus = dyn.Menus
			}
			if len(dyn.Statusbar) > 0 {
				v.Statusbar = dyn.Statusbar
			}
			if len(dyn.Actions) > 0 {
				v.Actions = dyn.Actions
			}
		}
	}
	return v
}

func (m *Manager) viewByID(id string) (pluginView, bool) {
	m.mu.Lock()
	p := m.plugins[id]
	m.mu.Unlock()
	if p == nil {
		return pluginView{}, false
	}
	return m.view(p), true
}

func (m *Manager) plugin(id string) *Plugin {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.plugins[id]
}
