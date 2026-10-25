package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	transport "github.com/lsong98sh/chonkpilot-plugin-framework/protocol"
	mq "github.com/lsong98sh/chonkpilot-plugin-framework/runtime/bus"
)

// EnvUDS 是插件前门 UDS 路径的环境变量覆盖（测试 / 多实例隔离）。
const EnvUDS = "CHONK_PLUGIN_UDS"

// DefaultAddr 返回插件前门约定路径：`%LOCALAPPDATA%\chonkpilot\plugin.sock`。
func DefaultAddr() string {
	if p := strings.TrimSpace(os.Getenv(EnvUDS)); p != "" {
		return p
	}
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "chonkpilot", "plugin.sock")
}

// Options 是插件桥服务端构造参数。
type Options struct {
	// Bus 是进程内消息总线（必填；用于事件下行）。
	Bus mq.Bus
	// Transport 是传输层参数；Addr 为空时用 Addr / DefaultAddr()。
	Transport transport.Options
	// Addr 是插件前门地址（uds 路径 / tcp host:port）；空 = DefaultAddr()。
	Addr string
	// Version 是平台版本（sys.info 用）。
	Version string
	// Form 是形态标识（desktop / gui / server）。
	Form string
	// ExistingInstanceIDs 返回当前 GUI / 业务实例 id（用于 instance.list 与保护）。
	ExistingInstanceIDs func() []string
	// GUI 是宿主注入的 GUI 反向通道（desktop/gui 有；纯 server 为 nil）。
	GUI GUIController
	// IndexQueue 是待索引队列宿主桥接（nil = 未装配，控制帧返回明确错误）。
	IndexQueue IndexQueueHooks
	// WorkDirs 返回宿主当前**活实例**的去重 work_dir（plugin.index.workdirs）。
	// 单实例多 workdir 的 resident indexer 据此发现待消费项目；nil = 返回空列表（插件空转等待）。
	WorkDirs func() []string
	// Plugins 是插件平台回调（MCP/UI/状态/数据权限）；nil = 对应能力不可用。
	Plugins PluginHooks
	// Logf 诊断输出。
	Logf func(format string, args ...any)
}

// Server 是插件前门（accept 侧）：连接 = 一个插件身份。
type Server struct {
	opts Options
	reg  *Registry

	ctx    context.Context
	cancel context.CancelFunc

	ep      transport.Endpoint
	backend transport.Backend
	sub     mq.Sub

	mu       sync.Mutex
	sessions map[*Session]struct{}
	closed   bool
	// ready 表示宿主「全部模块就绪」（server-ready 事件）；readyPayload 供晚连接插件补发。
	ready        bool
	readyPayload any

	toolMu sync.Mutex
	tools  map[string]*toolOwner

	existingMu sync.RWMutex
	existing   func() []string

	pluginsMu sync.Mutex
	plugins   PluginHooks
}

// New 构造插件桥服务端（未监听）。
func New(o Options) *Server {
	if o.Version == "" {
		o.Version = "dev"
	}
	if o.Form == "" {
		o.Form = "server"
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		opts:     o,
		reg:      NewRegistry(),
		ctx:      ctx,
		cancel:   cancel,
		sessions: make(map[*Session]struct{}),
		plugins:  o.Plugins,
	}
	registerMQCapabilities(s.reg, o.Bus)
	registerGUICapabilities(s.reg, o.GUI)
	s.existing = o.ExistingInstanceIDs
	return s
}

// SetGUI 在宿主窗口就绪后注入 GUI 反向通道（可重复调用，后者覆盖）。
func (s *Server) SetGUI(g GUIController) {
	registerGUICapabilities(s.reg, g)
}

// SetExistingInstanceIDs 在宿主窗口就绪后注入实例列表来源。
func (s *Server) SetExistingInstanceIDs(fn func() []string) {
	s.existingMu.Lock()
	s.existing = fn
	s.existingMu.Unlock()
}

// Registry 返回能力注册表（叠加能力的唯一入口）。
func (s *Server) Registry() *Registry { return s.reg }

// Register 便捷方法：注册一个能力 handler。
func (s *Server) Register(name string, h Handler) { s.reg.Register(name, h) }

// Start 建立监听并接收插件连接（非阻塞）。
func (s *Server) Start() error {
	to := s.opts.Transport
	if to.Addr == "" {
		to.Addr = s.opts.Addr
	}
	if to.Addr == "" {
		to.Addr = DefaultAddr()
	}
	if strings.HasPrefix(to.Addr, "tcp://") {
		to.Backend = transport.BackendTCP
		to.Addr = strings.TrimPrefix(to.Addr, "tcp://")
	}
	if to.Backend == "" {
		to.Backend = transport.BackendUDS
	}
	if to.Logf == nil {
		to.Logf = s.logf
	}
	ep, err := transport.Listen(to)
	if err != nil {
		return err
	}
	s.ep = ep
	s.backend = to.Backend
	if s.opts.Bus != nil {
		sub, err := s.opts.Bus.On(">", 0, func(_ context.Context, subject string, v *mq.Value) error {
			s.fanout(subject, v.Payload)
			return nil
		})
		if err != nil {
			_ = ep.Close()
			return err
		}
		s.sub = sub
	}
	go s.acceptLoop()
	return nil
}

// Addr 返回实际监听地址（tcp 后端带 `tcp://` 前缀，便于下游按同一规则 dial）。
func (s *Server) Addr() string {
	if s.ep == nil {
		return ""
	}
	addr := s.ep.Addr()
	if s.backend == transport.BackendTCP {
		return "tcp://" + addr
	}
	return addr
}

// Close 关闭前门与全部插件连接（幂等）。
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ss := make([]*Session, 0, len(s.sessions))
	for x := range s.sessions {
		ss = append(ss, x)
	}
	s.mu.Unlock()
	s.cancel()
	if s.sub != nil {
		_ = s.sub.Unsubscribe()
	}
	if s.ep != nil {
		_ = s.ep.Close()
	}
	for _, x := range ss {
		x.Close()
	}
	return nil
}

func (s *Server) acceptLoop() {
	for {
		c, err := s.ep.Accept()
		if err != nil {
			return
		}
		sess := newSession(s, c)
		s.mu.Lock()
		s.sessions[sess] = struct{}{}
		s.mu.Unlock()
		go sess.serve()
	}
}

func (s *Server) removeSession(sess *Session) {
	s.mu.Lock()
	delete(s.sessions, sess)
	s.mu.Unlock()
}

// MarkReady 标记宿主全部模块就绪，并向当前全部会话广播 `server-ready` 事件。
// 幂等：重复调用只更新载荷并再广播一次。payload 为 nil → 载荷 `{}`。
func (s *Server) MarkReady(payload any) {
	if payload == nil {
		payload = map[string]any{}
	}
	s.mu.Lock()
	s.ready = true
	s.readyPayload = payload
	list := s.sessionListLocked()
	s.mu.Unlock()
	for _, x := range list {
		x.Emit("server-ready", payload)
	}
}

// Broadcast 向当前全部会话推送一条事件（复用 Session.Emit 的有界队列语义）。
func (s *Server) Broadcast(name string, payload any) {
	s.mu.Lock()
	list := s.sessionListLocked()
	s.mu.Unlock()
	for _, x := range list {
		x.Emit(name, payload)
	}
}

// ShutdownPlugin 向指定 plugin_id 的会话发送 ctl 帧 plugin.shutdown，返回是否找到会话。
//
// 载荷 = `{grace_ms: 5000}`（插件收到后应在宽限期内自行退出）；同一 plugin_id 的多个会话
// （重连残留）都会被通知。帧走 Session.Ctl 的有界队列（满则丢弃，不阻塞宿主）。
func (s *Server) ShutdownPlugin(pluginID string) bool {
	if pluginID == "" {
		return false
	}
	s.mu.Lock()
	list := s.sessionListLocked()
	s.mu.Unlock()
	found := false
	for _, x := range list {
		if x.PluginID() != pluginID {
			continue
		}
		x.Ctl("plugin.shutdown", map[string]any{"grace_ms": 5000})
		found = true
	}
	return found
}

// Ready 返回宿主是否已就绪，以及应补发给晚连接插件的载荷。
func (s *Server) Ready() (bool, any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready, s.readyPayload
}

// sessionListLocked 返回当前会话快照（调用方须持有 s.mu）。
func (s *Server) sessionListLocked() []*Session {
	list := make([]*Session, 0, len(s.sessions))
	for x := range s.sessions {
		list = append(list, x)
	}
	return list
}

func (s *Server) fanout(subject string, payload []byte) {
	s.mu.Lock()
	list := make([]*Session, 0, len(s.sessions))
	for x := range s.sessions {
		list = append(list, x)
	}
	s.mu.Unlock()
	for _, x := range list {
		if x.matches(subject, payload) {
			// 以业务主题名作为事件名下发；事件载荷原样。
			x.Emit(subject, json.RawMessage(payload))
		}
	}
}

func (s *Server) dispatch(ctx context.Context, sess *Session, name string, args json.RawMessage) (any, error) {
	if h, ok := controlHandlers[name]; ok {
		return h(ctx, sess, args)
	}
	if sess.PluginID() == "" {
		return nil, errf(CodePluginUnregistered, "请先发送 plugin.hello")
	}
	h, ok := s.reg.Lookup(name)
	if !ok {
		return nil, errf(CodeMethodNotFound, "未知方法 %s", name)
	}
	return h(ctx, sess, args)
}

// controlNames 是控制帧 / 内置能力名单（methods() 的稳定来源，避免与 handler 初始化成环）。
var controlNames = []string{
	"plugin.hello",
	"plugin.register",
	"plugin.ready",
	"plugin.exit",
	"plugin.subscribe",
	"plugin.unsubscribe",
	"plugin.sys.ping",
	"plugin/v1/sys.ping",
	"plugin.sys.info",
	"plugin/v1/sys.info",
	"plugin.filter.register",
	"plugin.tool.bind",
	"plugin.tool.unbind",
	"plugin/v1/instance.claim",
	"plugin/v1/instance.list",
	"plugin.index.take",
	"plugin.index.ack",
	"plugin.index.pending",
	"plugin.index.workdirs",
	"plugin.mcp.register",
	"plugin.mcp.unregister",
	"plugin.ui.register",
	"plugin.status.report",
	"plugin.publish",
	"plugin/v1/data.read",
	"plugin/v1/data.write",
}

// controlHandlers 是控制帧 / 内置能力（不经业务注册表）。
var controlHandlers = map[string]func(ctx context.Context, s *Session, args json.RawMessage) (any, error){
	"plugin.hello":             handleHello,
	"plugin.register":          handleRegister,
	"plugin.ready":             handleReady,
	"plugin.exit":              handleExit,
	"plugin.subscribe":         handleSubscribe,
	"plugin.unsubscribe":       handleUnsubscribe,
	"plugin.sys.ping":          handlePing,
	"plugin/v1/sys.ping":       handlePing,
	"plugin.sys.info":          handleInfo,
	"plugin/v1/sys.info":       handleInfo,
	"plugin.filter.register":   handleFilterRegister,
	"plugin.tool.bind":         handleToolBind,
	"plugin.tool.unbind":       handleToolUnbind,
	"plugin/v1/instance.claim": handleInstanceClaim,
	"plugin/v1/instance.list":  handleInstanceList,
	"plugin.index.take":        handleIndexTake,
	"plugin.index.ack":         handleIndexAck,
	"plugin.index.pending":     handleIndexPending,
	"plugin.index.workdirs":    handleIndexWorkDirs,
	"plugin.mcp.register":      handleMCPRegister,
	"plugin.mcp.unregister":    handleMCPUnegister,
	"plugin.ui.register":       handleUIRegister,
	"plugin.status.report":     handleStatusReport,
	"plugin.publish":           handlePublish,
	"plugin/v1/data.read":      handleDataRead,
	"plugin/v1/data.write":     handleDataWrite,
}

func decode(args json.RawMessage, out any) error {
	if len(args) == 0 {
		return nil
	}
	if err := json.Unmarshal(args, out); err != nil {
		return errf(CodeArgsInvalid, "参数解析失败: %v", err)
	}
	return nil
}

func (s *Server) logf(format string, args ...any) {
	if s.opts.Logf != nil {
		s.opts.Logf(format, args...)
	}
}

func (s *Server) audit(sess *Session, method string, ok bool, code string) {
	if s.opts.Logf == nil {
		return
	}
	status := "ok"
	if !ok {
		status = "err"
	}
	s.opts.Logf("plugin-audit plugin=%s instance=%s method=%s status=%s code=%s ts=%s",
		sess.PluginID(), sess.InstanceID(), method, status, code, time.Now().Format(time.RFC3339))
}
