// Package pluginsdk 是 ChonkPilot 进程外插件的最小开发套件：
// 连接插件桥（UDS / TCP 帧）、握手 / 注册、订阅事件、暴露 MCP 工具（Streamable HTTP）、
// 读取配置、调用宿主能力面。
//
// 设计见 docs/plugin-platform/插件与MCP统一设计方案.md 与
// docs/plugin-platform/插件与MCP-Server开发指南.md。
package pluginsdk

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/transport"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// 宿主注入的环境变量。
const (
	EnvAddr     = "CHONKPILOT_PLUGIN_ADDR"
	EnvPluginID = "CHONKPILOT_PLUGIN_ID"
	EnvRoot     = "CHONKPILOT_PLUGIN_ROOT"
	EnvDataDir  = "CHONKPILOT_PLUGIN_DATA_DIR"
	EnvWorkDir  = "CHONKPILOT_PLUGIN_WORK_DIR"
	EnvConfig   = "CHONKPILOT_PLUGIN_CONFIG"
	EnvSecrets  = "CHONKPILOT_PLUGIN_SECRETS"
	EnvMCPToken = "CHONKPILOT_PLUGIN_TOKEN"
	DefaultAddr = ""
	defaultWait = 15 * time.Second
	// maxFrameSize 与宿主侧 transport 缺省（transport.DefaultMaxFrameBytes）对齐：
	// 宿主 → 插件的大应答（如 data-session-history 取整会话轮次，memory 沉淀用）不受更小上限截断。
	maxFrameSize = transport.DefaultMaxFrameBytes
)

// Options 是 New 的参数。
type Options struct {
	ID      string
	Name    string
	Version string
	// Features 随 plugin.hello 上报（可选）。
	Features map[string]any
	// Addr 覆盖 CHONKPILOT_PLUGIN_ADDR（可选）。
	Addr string
	// Timeout 是单次宿主调用超时（缺省 15s）。
	Timeout time.Duration
	// Logger 缺省 stderr 文本日志。
	Logger *slog.Logger
}

// CallContext 是一次工具调用的上下文（来自 gateway 注入的 _meta.chonkpilot）。
type CallContext struct {
	InstanceID string `json:"instance_id"`
	WorkDir    string `json:"work_dir"`
	DataDir    string `json:"data_dir"`
	Session    string `json:"session"`
	Turn       string `json:"turn"`
	ToolCallID string `json:"tool_call_id"`
	TopSession string `json:"top_session"`
	Parent     string `json:"parent"`
	Purpose    string `json:"purpose"`
}

// ToolFunc 是工具实现。
type ToolFunc func(ctx context.Context, args map[string]any, cx CallContext) (any, error)

// EventFunc 是事件回调。
type EventFunc func(ctx context.Context, topic string, payload []byte)

// RequestFunc 处理宿主反向请求（plugin.ui.rpc / plugin.command）。
type RequestFunc func(ctx context.Context, payload map[string]any) (any, error)

type toolDef struct {
	name   string
	desc   string
	schema map[string]any
	hot    bool
	fn     ToolFunc
}

// Plugin 是一个已连接插件桥的插件实例。
type Plugin struct {
	opts Options
	conn transport.Conn
	log  *slog.Logger

	mu        sync.Mutex
	subs      []map[string]any
	tools     []*toolDef
	events    []eventSub
	requests  map[string]RequestFunc
	subjects  map[string]SubjectHandler // plugin.tool.bind 绑定的「宿主总线主题 → handler」
	ui        map[string]any
	mcpCustom []func(*mcp.Server) error
	mcpHot    []string

	cfgMu sync.RWMutex
	cfg   *Config
	mf    *Manifest

	httpSrv  *http.Server
	mcpURL   string
	mcpToken string
}

type eventSub struct {
	pattern string
	fn      EventFunc
}

// New 连接插件桥并完成 plugin.hello。
func New(opts Options) (*Plugin, error) {
	opts.ID = strings.TrimSpace(opts.ID)
	if opts.ID == "" {
		opts.ID = strings.TrimSpace(os.Getenv(EnvPluginID))
	}
	if opts.ID == "" {
		return nil, errors.New("pluginsdk: Options.ID 不能为空")
	}
	if opts.Version == "" {
		opts.Version = "0.0.0"
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultWait
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	addr := strings.TrimSpace(opts.Addr)
	if addr == "" {
		addr = strings.TrimSpace(os.Getenv(EnvAddr))
	}
	if addr == "" {
		return nil, fmt.Errorf("pluginsdk: %s 为空（应由宿主注入）", EnvAddr)
	}
	to := transport.Options{Backend: transport.BackendUDS, Addr: addr, MaxFrameBytes: maxFrameSize}
	if strings.HasPrefix(addr, "tcp://") {
		to.Backend = transport.BackendTCP
		to.Addr = strings.TrimPrefix(addr, "tcp://")
	}
	dialCtx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()
	conn, err := transport.Dial(dialCtx, to)
	if err != nil {
		return nil, fmt.Errorf("pluginsdk: 连接插件桥 %s: %w", addr, err)
	}
	p := &Plugin{
		opts:     opts,
		conn:     conn,
		log:      opts.Logger,
		requests: map[string]RequestFunc{},
		subjects: map[string]SubjectHandler{},
		ui:       map[string]any{},
	}
	if err := p.hello(dialCtx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	p.mf = loadManifest()
	p.cfg = loadConfig(p.mf)
	// 宿主保存配置后会发 plugin.config.changed（反向请求）→ 重新读取 config/secrets。
	p.requests["plugin.config.changed"] = func(_ context.Context, _ map[string]any) (any, error) {
		p.reloadConfig()
		p.log.Info("config reloaded")
		return map[string]any{"ok": true}, nil
	}
	// 宿主健康检查（manifest runtime.health）会反向 ping 本进程。
	p.requests["plugin.sys.ping"] = func(_ context.Context, _ map[string]any) (any, error) {
		return map[string]any{"pong": true, "ts": time.Now().UnixMilli()}, nil
	}
	p.requests["plugin/v1/sys.ping"] = p.requests["plugin.sys.ping"]
	return p, nil
}

func (p *Plugin) hello(ctx context.Context) error {
	payload := map[string]any{"id": p.opts.ID, "name": p.opts.Name, "version": p.opts.Version}
	if p.opts.Features != nil {
		payload["features"] = p.opts.Features
	}
	res, err := p.conn.Call(ctx, "plugin.hello", payload)
	if err != nil {
		return fmt.Errorf("pluginsdk: plugin.hello: %w", err)
	}
	if !res.OK {
		return fmt.Errorf("pluginsdk: plugin.hello 失败: %s", strings.Join(res.Errors, "; "))
	}
	return nil
}

// Client 返回底层连接（高级用法）。
func (p *Plugin) Client() transport.Conn { return p.conn }

// ID 返回插件 id。
func (p *Plugin) ID() string { return p.opts.ID }

// Logger 返回日志器。
func (p *Plugin) Logger() *slog.Logger { return p.log }

// Manifest 返回加载到的 manifest（可能为 nil）。
func (p *Plugin) Manifest() *Manifest { return p.mf }

// Config 返回只读配置快照（配置变更后指针会整体替换）。
func (p *Plugin) Config() *Config {
	p.cfgMu.RLock()
	defer p.cfgMu.RUnlock()
	return p.cfg
}

// reloadConfig 重新读取 config/secrets 并原子替换快照。
func (p *Plugin) reloadConfig() {
	p.cfgMu.Lock()
	p.cfg = loadConfig(p.mf)
	p.cfgMu.Unlock()
}

// Tool 注册一个 MCP 工具（Run 时统一暴露）。
func (p *Plugin) Tool(name, desc string, schema map[string]any, fn ToolFunc) *Plugin {
	p.mu.Lock()
	p.tools = append(p.tools, &toolDef{name: name, desc: desc, schema: schema, fn: fn})
	p.mu.Unlock()
	return p
}

// HotTool 标记某工具为 hot（默认全部 hot）。
func (p *Plugin) HotTool(name string) *Plugin {
	p.mu.Lock()
	for _, t := range p.tools {
		if t.name == name {
			t.hot = true
		}
	}
	p.mu.Unlock()
	return p
}

// OnMCPServer 允许插件在 SDK 自建的 MCP server 上直接注册工具。
//
// 典型用法：复用既有 `RegisterTools(srv *mcp.Server)` 实现（codebase 等），
// 由插件自行控制工具 schema / hot 标记。SDK 只负责起 HTTP 端点并登记到 gateway。
func (p *Plugin) OnMCPServer(fn func(*mcp.Server) error) *Plugin {
	if fn == nil {
		return p
	}
	p.mu.Lock()
	p.mcpCustom = append(p.mcpCustom, fn)
	p.mu.Unlock()
	return p
}

// MCPHot 声明 hot 工具名（OnMCPServer 注册的工具无法被 SDK 自动识别，需显式声明）。
func (p *Plugin) MCPHot(names ...string) *Plugin {
	p.mu.Lock()
	p.mcpHot = append(p.mcpHot, names...)
	p.mu.Unlock()
	return p
}

// Subscribe 订阅一条总线事件（Run 时统一注册）。
func (p *Plugin) Subscribe(pattern string, fn EventFunc) *Plugin {
	p.mu.Lock()
	p.events = append(p.events, eventSub{pattern: pattern, fn: fn})
	if fn != nil {
		p.subs = append(p.subs, map[string]any{"pattern": pattern, "mode": "async"})
	}
	p.mu.Unlock()
	return p
}

// OnRequest 注册宿主反向请求处理（方法名 = plugin.ui.rpc 的 method / plugin.command 的 name）。
func (p *Plugin) OnRequest(name string, fn RequestFunc) *Plugin {
	p.mu.Lock()
	p.requests[name] = fn
	p.mu.Unlock()
	return p
}

// SetUI 覆盖 manifest 中的 UI 声明（第三方无 manifest 时使用）。
func (p *Plugin) SetUI(ui map[string]any) *Plugin {
	p.mu.Lock()
	p.ui = ui
	p.mu.Unlock()
	return p
}

// Call 调用宿主能力面。
func (p *Plugin) Call(ctx context.Context, method string, args any) (json.RawMessage, error) {
	cctx, cancel := context.WithTimeout(ctx, p.opts.Timeout)
	defer cancel()
	res, err := p.conn.Call(cctx, method, args)
	if err != nil {
		return nil, err
	}
	if !res.OK {
		return nil, fmt.Errorf("%s: %s", method, strings.Join(res.Errors, "; "))
	}
	b, err := json.Marshal(res.Value)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// CallInto 调用并把结果解到 out。
func (p *Plugin) CallInto(ctx context.Context, method string, args, out any) error {
	raw, err := p.Call(ctx, method, args)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// DataRead 调 plugin/v1/data.read。
func (p *Plugin) DataRead(ctx context.Context, resource, op, instanceID string, params map[string]any) (json.RawMessage, error) {
	return p.Call(ctx, "plugin/v1/data.read", map[string]any{
		"resource": resource, "op": op, "instance_id": instanceID, "params": params,
	})
}

// DataWrite 调 plugin/v1/data.write。
func (p *Plugin) DataWrite(ctx context.Context, resource, op, instanceID string, params map[string]any) (json.RawMessage, error) {
	return p.Call(ctx, "plugin/v1/data.write", map[string]any{
		"resource": resource, "op": op, "instance_id": instanceID, "params": params,
	})
}

// ReportStatus 上报状态（running / degraded / error）。
func (p *Plugin) ReportStatus(ctx context.Context, state, detail string) error {
	_, err := p.Call(ctx, "plugin.status.report", map[string]any{"state": state, "detail": detail})
	return err
}

// Run 启动 MCP HTTP（如有工具）、注册订阅/UI、进入事件循环。收到 plugin.shutdown 或 ctx 取消后返回。
func (p *Plugin) Run(ctx context.Context) error {
	p.mu.Lock()
	hasMCP := len(p.tools) > 0 || len(p.mcpCustom) > 0
	p.mu.Unlock()
	if hasMCP {
		if err := p.startMCP(); err != nil {
			return err
		}
	}
	p.mu.Lock()
	subs := append([]map[string]any(nil), p.subs...)
	ui := p.ui
	p.mu.Unlock()
	reg := map[string]any{"features": p.opts.Features, "subscriptions": subs}
	if len(ui) > 0 {
		reg["ui"] = ui
	}
	if _, err := p.Call(ctx, "plugin.register", reg); err != nil {
		return err
	}
	_ = p.ReportStatus(ctx, "running", "")
	p.log.Info("plugin ready", "id", p.opts.ID, "tools", len(p.tools), "subs", len(subs))

	for {
		select {
		case <-ctx.Done():
			p.stopMCP()
			return ctx.Err()
		default:
		}
		f, err := p.conn.Recv()
		if err != nil {
			p.stopMCP()
			return err
		}
		switch f.Kind {
		case transport.KindEvt:
			p.dispatchEvent(f.Name, f.Payload)
		case transport.KindCtl:
			switch f.Name {
			case "plugin.shutdown":
				p.stopMCP()
				return nil
			case "plugin.config.changed":
				p.reloadConfig()
				p.log.Info("config reloaded")
			case "plugin.command", "plugin.ui.rpc":
				go p.handleRequest(f)
			}
		case transport.KindReq:
			go p.handleRequest(f)
		}
	}
}

func (p *Plugin) dispatchEvent(topic string, payload []byte) {
	p.mu.Lock()
	subs := append([]eventSub(nil), p.events...)
	p.mu.Unlock()
	for _, s := range subs {
		if !matchTopic(s.pattern, topic) {
			continue
		}
		if s.fn != nil {
			s.fn(context.Background(), topic, payload)
		}
	}
}

func (p *Plugin) handleRequest(f transport.Frame) {
	var payload map[string]any
	if len(f.Payload) > 0 {
		_ = json.Unmarshal(f.Payload, &payload)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	// plugin.ui.rpc / plugin.command 是"信封"：方法名在外层，业务参数在 params 里。
	name := f.Name
	callPayload := payload
	if f.Name == "plugin.ui.rpc" {
		if m, _ := payload["method"].(string); m != "" {
			name = m
		}
		if inner, ok := payload["params"].(map[string]any); ok {
			callPayload = inner
		}
	}
	if f.Name == "plugin.command" {
		if m, _ := payload["name"].(string); m != "" {
			name = m
		}
		if inner, ok := payload["params"].(map[string]any); ok {
			callPayload = inner
		}
	}
	p.mu.Lock()
	fn := p.requests[name]
	p.mu.Unlock()
	res := transport.Frame{Kind: transport.KindRes, ID: f.ID}
	if fn == nil {
		res.Errors = []string{"method_not_found: " + name}
		_ = p.conn.Send(res)
		return
	}
	v, err := fn(context.Background(), callPayload)
	if err != nil {
		res.Errors = []string{err.Error()}
	} else {
		res.OK = true
		res.Result = v
	}
	_ = p.conn.Send(res)
}

// startMCP 启动 Streamable HTTP MCP server 并登记到 gateway。
func (p *Plugin) startMCP() error {
	ms := mcp.NewServer(&mcp.Implementation{Name: p.opts.ID, Version: p.opts.Version}, nil)
	p.mu.Lock()
	tools := append([]*toolDef(nil), p.tools...)
	custom := append([]func(*mcp.Server) error(nil), p.mcpCustom...)
	hot := append([]string(nil), p.mcpHot...)
	p.mu.Unlock()
	for _, fn := range custom {
		if err := fn(ms); err != nil {
			return err
		}
	}
	for _, t := range tools {
		t := t
		schema := t.schema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		ms.AddTool(&mcp.Tool{Name: t.name, Description: t.desc, InputSchema: schema}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := map[string]any{}
			if len(req.Params.Arguments) > 0 {
				if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
					return mcpError("参数解析失败: " + err.Error()), nil
				}
			}
			cx := callContextFromMeta(req.Params.Meta)
			out, err := t.fn(ctx, args, cx)
			if err != nil {
				return mcpError(err.Error()), nil
			}
			return mcpResult(out), nil
		})
		hot = append(hot, t.name)
	}
	ln, err := listenLoopback()
	if err != nil {
		return err
	}
	token := strings.TrimSpace(os.Getenv(EnvMCPToken))
	if token == "" {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			_ = ln.Close()
			return err
		}
		token = hex.EncodeToString(buf)
	}
	mux := http.NewServeMux()
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, nil)
	mux.Handle("/mcp", bearerAuth(token, handler))
	srv := &http.Server{Handler: mux}
	p.httpSrv = srv
	p.mcpURL = "http://" + ln.Addr().String() + "/mcp"
	p.mcpToken = token
	go func() {
		if serr := srv.Serve(ln); serr != nil && !errors.Is(serr, http.ErrServerClosed) {
			p.log.Warn("mcp serve", "err", serr)
		}
	}()
	// 等 HTTP server 可用（同一进程内 listen 已成功，通常无需等待）。
	if _, err := p.Call(context.Background(), "plugin.mcp.register", map[string]any{
		"url": p.mcpURL, "token": token, "hot_tools": hot,
	}); err != nil {
		p.stopMCP()
		return err
	}
	return nil
}

func (p *Plugin) stopMCP() {
	if p.httpSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = p.httpSrv.Shutdown(ctx)
		cancel()
		p.httpSrv = nil
	}
}

// Close 关闭连接（幂等）。
func (p *Plugin) Close() error {
	p.stopMCP()
	if p.conn != nil {
		return p.conn.Close()
	}
	return nil
}

func mcpResult(v any) *mcp.CallToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcpError("json 编码失败: " + err.Error())
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}
}

func mcpError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: msg}}, IsError: true}
}

func callContextFromMeta(meta mcp.Meta) CallContext {
	var cx CallContext
	if meta == nil {
		return cx
	}
	raw, ok := meta["chonkpilot"]
	if !ok || raw == nil {
		return cx
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return cx
	}
	_ = json.Unmarshal(b, &cx)
	return cx
}

func bearerAuth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if strings.TrimSpace(got) != token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// matchTopic 支持 `>` / `*` / 精确前缀。
func matchTopic(pattern, topic string) bool {
	if pattern == "" || pattern == ">" || pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, ">") {
		return strings.HasPrefix(topic, strings.TrimSuffix(pattern, ">"))
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(topic, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == topic
}

// DefaultDataDir 返回插件数据目录（宿主注入，缺省 = 当前目录下 .data）。
func DefaultDataDir() string {
	if v := strings.TrimSpace(os.Getenv(EnvDataDir)); v != "" {
		return v
	}
	return filepath.Join(".", ".data")
}
