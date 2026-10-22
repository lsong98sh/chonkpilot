// Provider：spawned / proxied（官方 modelcontextprotocol/go-sdk client 代理转发）。
// L5（2026-10-21）：gateway 不再持有进程内能力源 —— 所有工具/原语均来自本层的下游 client。
// 对齐 21-llm-server：gateway 不扫描目录，工具来源仅注册的下游 server + 自身注册工具表。
//
// spawned http/sse（runtime + url）：网关 spawn 子进程 → 轮询 url 就绪 → 建 client 连接；
// 进程由网关自持（Close kill / 意外退出 → failed 通知）。仅 runtime（无 url）= stdio client
// （子进程由官方 SDK CommandTransport 内部管理，无退出观测）。纯 url = proxied 只连不拉。
package mcpgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/sandbox"
	"github.com/chonkpilot/chonkpilot-lib/winproc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Provider 是工具提供方统一接口（tools-only）。
type Provider interface {
	// Name 返回来源名（dir / spawned:<id> / proxied:<id>）。
	Name() string
	// ListTools 返回全量工具定义（网关启动缓存）。
	ListTools(ctx context.Context) ([]*mcp.Tool, error)
	// Call 执行工具（超时/异步由执行池 execPool 编排）。
	Call(ctx context.Context, tool string, args map[string]any) (*mcp.CallToolResult, error)
	// Invalidate 作废当前连接/子进程（kill stdio 子进程 / 断连），并**恒**重建（respawn + 重连）。
	// 返回 rebuilt = 是否已重建（false = 重建失败，provider 暂不可用，需 unregister/register
	// 或 reload 恢复）。内存 provider 为 no-op（返回 true）。
	Invalidate(ctx context.Context) (rebuilt bool, err error)
	// Terminate 是「取消 → 真停」的**按执行线统一入口**（onTaskCancel 调用；决策 42 §2 (241)）：
	//   - spawned / 纯远程代理（proxyProvider）→ kill + respawn（纯远程无自持子进程 → 仅断请求，
	//     对端进程可能继续运行 = 语义「尽力」）；instanceID/workdir 非空且持隔离槽 → 仅作用该槽；
	//   - in-memory（memNode / registered）→ **协作式**：取消已由执行池 ctx 送达执行体
	//     （gateway 只负责「把取消送到」，真停实现仍在执行体侧，见 18 §3.1/§3.4）→ 此处 no-op。
	Terminate(ctx context.Context, instanceID, workdir string) (rebuilt bool, err error)
	// Close 释放资源（断连 / 杀子进程）。
	Close() error
}

// providerConn 是下游会话最小面：sdkConn（官方 client，connect 懒建会话）。
// meta = 调用上下文（协议 `_meta`）；nil = 不携带（第三方来源）。
type providerConn interface {
	connect(ctx context.Context) error
	ListTools(ctx context.Context) ([]*mcp.Tool, error)
	Call(ctx context.Context, tool string, args map[string]any, meta mcp.Meta, progressToken string) (*mcp.CallToolResult, error)
	Close() error
}

// sandboxOnFunc 报告某外部 MCP（entry）当前是否开启 agentbox 沙箱。由 gateway 注入
// （读实时开关表 usr `tool_sandbox.mcps`，key = entry.SandboxName()）；nil = 一律不注入
// （独立单测 / 未接线）。见 tooloverride.go SetSandboxOverrides。
type sandboxOnFunc func(entry *ServerEntry) bool

// sdkConn 官方 SDK client 会话（stdio CommandTransport / http StreamableClientTransport /
// sse 旧式 SSEClientTransport）。
type sdkConn struct {
	cli *mcp.Client
	tr  mcp.Transport
	ss  *mcp.ClientSession
}

func (c *sdkConn) connect(ctx context.Context) error {
	// 预连接会话（entry.Session，L5 装配方注入）：已握手 → 无需再连。
	if c.ss != nil {
		return nil
	}
	ss, err := c.cli.Connect(ctx, c.tr, nil)
	if err != nil {
		return err
	}
	c.ss = ss
	return nil
}

func (c *sdkConn) ListTools(ctx context.Context) ([]*mcp.Tool, error) {
	if c.ss == nil {
		return nil, fmt.Errorf("not connected")
	}
	// 官方 server 分页（默认 1000/页）：聚合器语义 = 全量，跟随 cursor 收齐
	var out []*mcp.Tool
	cursor := ""
	for {
		res, err := c.ss.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		out = append(out, res.Tools...)
		if res.NextCursor == "" {
			return out, nil
		}
		cursor = res.NextCursor
	}
}

func (c *sdkConn) Call(ctx context.Context, tool string, args map[string]any, meta mcp.Meta, progressToken string) (*mcp.CallToolResult, error) {
	if c.ss == nil {
		return nil, fmt.Errorf("not connected")
	}
	params := &mcp.CallToolParams{Name: tool, Arguments: args, Meta: meta}
	if progressToken != "" {
		// L8：progressToken = call_id → 下游 notifications/progress 按 token 精确路由回该调用。
		params.SetProgressToken(progressToken)
	}
	return c.ss.CallTool(ctx, params)
}

func (c *sdkConn) Close() error {
	if c.ss != nil {
		return c.ss.Close()
	}
	return nil
}

// ── 原语面（prompts / resources；L5：gateway 不再持有能力源，原语经下游标准 client 聚合）──

// ListPrompts 列出下游 prompts（含 skill，_meta.type 区分；跟随 cursor 收齐）。
func (c *sdkConn) ListPrompts(ctx context.Context) ([]*mcp.Prompt, error) {
	if c.ss == nil {
		return nil, fmt.Errorf("not connected")
	}
	var out []*mcp.Prompt
	cursor := ""
	for {
		res, err := c.ss.ListPrompts(ctx, &mcp.ListPromptsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		out = append(out, res.Prompts...)
		if res.NextCursor == "" {
			return out, nil
		}
		cursor = res.NextCursor
	}
}

// GetPrompt 取单个 prompt 渲染结果。
func (c *sdkConn) GetPrompt(ctx context.Context, name string) (*mcp.GetPromptResult, error) {
	if c.ss == nil {
		return nil, fmt.Errorf("not connected")
	}
	return c.ss.GetPrompt(ctx, &mcp.GetPromptParams{Name: name})
}

// ListResources 列出下游 resources（跟随 cursor 收齐）。
func (c *sdkConn) ListResources(ctx context.Context) ([]*mcp.Resource, error) {
	if c.ss == nil {
		return nil, fmt.Errorf("not connected")
	}
	var out []*mcp.Resource
	cursor := ""
	for {
		res, err := c.ss.ListResources(ctx, &mcp.ListResourcesParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		out = append(out, res.Resources...)
		if res.NextCursor == "" {
			return out, nil
		}
		cursor = res.NextCursor
	}
}

// ReadResource 读取单个 resource。
func (c *sdkConn) ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	if c.ss == nil {
		return nil, fmt.Errorf("not connected")
	}
	return c.ss.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
}

// ─── proxyProvider（spawned / proxied：标准 MCP client 转发） ─────────────

// connSlot 是一条下游连接槽：conn = 标准 MCP client 会话；cmd/done 仅 spawned http/sse
// （网关自持子进程）时非 nil；纯 stdio spawned 的子进程由官方 SDK CommandTransport 内部管理。
// 连接池（2026-09-16）：isolate=true → **每个 (instance, workdir) 一槽**（各自管道/子进程，互不阻塞）；
// isolate=false → 全 server 仅共享槽一条（现状）。
// 2026-09-19（实例隔离第一批，缺口 1）：隔离键由 `workdir` 扩为 **`(instance_id, workdir)`** ——
// 同 workdir 的不同 instance 各自建连，取消/打断互不影响（见 poolKeyFor）。
//
// L6.2（2026-10-21）：进程句柄/退出观测拆为 procSlot（cmd/done/exited/selfHeld），
// 连接槽只持会话/管道 → closeConn（L2 关连接）与 killProcess（L3 杀进程树）可分别作用。
type procSlot struct {
	cmd *exec.Cmd
	// done 仅网关自持子进程（spawned http/sse）非 nil：Wait 结果（缓冲通道）。
	done chan error
	// exited 由 Wait goroutine 置位（原子）：替代无同步读 cmd.ProcessState（C-42），
	// 供 Close / waitEndpointReady 无竞争判断进程是否已退出。
	exited *atomic.Bool
	// selfHeld = 网关自持子进程（spawned http/sse：Close/WaitExit 直接 kill/观测）。
	// false = stdio：子进程由官方 SDK CommandTransport 管理（此处只保留句柄供 L3 杀进程树）。
	selfHeld bool
}

type connSlot struct {
	workdir  string // 归属 workdir（"" = 共享槽：isolate=false / workdir 缺失回落 / 控制面 ListTools）
	instance string // 归属 instance（"" = 无 instance 维度；仅隔离槽非空）
	conn     providerConn
	proc     *procSlot
	closing  atomic.Bool  // 主动关闭/重建中 → 退出观测静默
	lastUsed atomic.Int64 // 最近使用时刻（unix nano；空闲回收判据）
	inFlight atomic.Int32 // 在飞调用数（>0 不回收）
	// gen 是本槽的代际号（创建时由 proxyProvider.genSeq 分配；Invalidate 重建递增）。
	// 退出观测（monitorSpawned）用它判定某次退出是否已被重建路径认领：捕获代际 ≠ 当前共享槽
	// 代际 = 该槽已被替换（主动 kill 的结果），而非「意外退出」（修复 closing 复位时序竞态）。
	gen uint64
	// key 是本槽的池键（"" = 共享槽；`call\x00<call_id>` = per-call 槽）；供 in-flight
	// 注册表记录 slot_id（L8）与连带取消定位（L7）。
	key string
}

// slotID 返回 in-flight 注册表使用的槽标识（池键 + 代际）。
func (s *connSlot) slotID() string { return fmt.Sprintf("%s#%d", s.key, s.gen) }

// processExited 报告自持子进程是否已 Wait 完成（原子标志；无进程/无观测时恒 false）。
func (s *connSlot) processExited() bool {
	return s.proc != nil && s.proc.exited != nil && s.proc.exited.Load()
}

// closeConn 只关连接/会话（取消阶梯 L2；不杀进程）。
func (s *connSlot) closeConn() {
	if s.conn != nil {
		_ = s.conn.Close()
	}
}

// killProcess 终止该槽的**进程树**（取消阶梯 L3；stdio 与自持 spawned 均适用）。
// 返回是否实际下达了杀进程动作。
func (s *connSlot) killProcess() bool {
	if s.proc == nil || s.proc.cmd == nil || s.proc.cmd.Process == nil {
		return false
	}
	pid := s.proc.cmd.Process.Pid
	if pid <= 0 {
		return false
	}
	_ = winproc.KillTree(pid)
	return true
}

// poolKeyFor 组合连接池隔离键：`<instance_id>\x00<workdir>`（instance 缺失 → 仅 workdir，
// 与引入 instance 维度之前逐字节等价）。instance_id 与 workdir 均可能含任意字符 →
// 用 NUL 作分隔符（两者都不会含 NUL）。
func poolKeyFor(instanceID, workdir string) string {
	if instanceID == "" {
		return workdir
	}
	return instanceID + "\x00" + workdir
}

// buildFlight 是一次进行中的池键建连（per-key single-flight）：同一池键的并发首调只有持有者执行
// build + connect，其余等待者阻塞在 done 上、完成后从池中复用入池槽。err 在 close(done) **之前**
// 写入，故等待者 `<-done` 后读取安全（channel close 建立 happens-before）。
type buildFlight struct {
	done chan struct{}
	err  error // 建连失败原因（失败时非 nil）；成功为 nil
}

// ─── L8 在飞调用注册表 / 通知路由 ─────────────────────────────────────────

// ProgressEvent 是一次下游进度通知（已按 progressToken 归位到具体调用）。
type ProgressEvent struct {
	CallID     string    `json:"call_id"`
	SlotID     string    `json:"slot_id,omitempty"`
	Server     string    `json:"server,omitempty"`
	Tool       string    `json:"tool,omitempty"`
	InstanceID string    `json:"instance_id,omitempty"`
	Progress   float64   `json:"progress"`
	Total      float64   `json:"total,omitempty"`
	Message    string    `json:"message,omitempty"`
	At         time.Time `json:"at"`
}

// NotifyHandlers 是下游上行通知的装配方回调（L8）：Progress 按 call_id 投递给该调用；
// Elicit 按 progressToken（可能为空）投递。零值 = 未注入（progress 丢弃并 warn，elicitation 不注册 handler）。
type NotifyHandlers struct {
	Progress func(ProgressEvent)
	Elicit   func(callID string, req *mcp.ElicitRequest) (*mcp.ElicitResult, error)
}

// inflightCall 是**调用级**在飞登记（替代纯计数）：供 progressToken 路由（L8）与取消定位（L7）。
type inflightCall struct {
	callID    string
	slot      *connSlot
	slotID    string
	tool      string
	instance  string
	workdir   string
	startedAt time.Time

	mu        sync.Mutex
	phase     string // running / cancelled / killing
	cancelled bool
	err       error    // 终止原因（slot_killed 等）
	cascaded  []string // 被本调用连带的 call_id 列表（cascade-report）
	cancel    context.CancelFunc
	done      chan struct{}
}

func (c *inflightCall) setPhase(phase string) {
	c.mu.Lock()
	c.phase = phase
	c.mu.Unlock()
}

// markKilled 记录本例被同槽其他调用的取消连带打断（L7 cascade-report）。
func (c *inflightCall) markKilled(cause string, cascaded []string) {
	c.mu.Lock()
	c.cancelled = true
	c.phase = "killed"
	c.err = &CallError{Kind: ErrKindSlotKilled, CallIDs: cascaded, Cause: cause}
	c.mu.Unlock()
}

// CancelReport 是取消阶梯的执行结果（L7）：各层是否生效 + 连带受影响的 call_id。
type CancelReport struct {
	CallID     string   `json:"call_id"`
	Graceful   bool     `json:"graceful"`           // L0/L1：协议取消送达且宽限期内调用自行结束
	ConnClosed bool     `json:"conn_closed"`        // L2：已关闭连接/会话
	Killed     bool     `json:"killed"`             // L3：已终止 stdio 进程树
	Cascaded   []string `json:"cascaded,omitempty"` // 同槽被连带打断的在飞 call_id（cascade-report）
}

// CallCanceller 是「按调用取消」的可选能力（L7）：proxyProvider 实现；未实现者（in-memory /
// 注册型）由调用方回落既有 Terminate（协作式）。**不加入 Provider 接口**，避免破坏既有实现。
type CallCanceller interface {
	CancelCall(ctx context.Context, callID string, grace time.Duration) (CancelReport, error)
}

// 错误 kind（L6/L7/L8；设计稿 §6.1）。
const (
	ErrKindPoolTimeout  = "pool_timeout"  // 并发已满且排队超时
	ErrKindSlotKilled   = "slot_killed"   // 同槽进程被取消连带终止
	ErrKindServerExited = "server_exited" // 下游进程已退出（保留口径）
)

// CallError 是带 kind 的调用错误（供 ExecSink/任务层按 kind 判定，不止看文本）。
type CallError struct {
	Kind    string   `json:"kind"`
	CallIDs []string `json:"call_ids,omitempty"`
	Cause   string   `json:"cause,omitempty"`
	Err     error    `json:"-"`
}

func (e *CallError) Error() string {
	msg := e.Kind
	if e.Cause != "" {
		msg += ": " + e.Cause
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *CallError) Unwrap() error { return e.Err }

// ErrorKind 返回错误的 kind（非 CallError → ""）。
func ErrorKind(err error) string {
	var ce *CallError
	if errors.As(err, &ce) {
		return ce.Kind
	}
	return ""
}

func (s *connSlot) touch() { s.lastUsed.Store(time.Now().UnixNano()) }

// idleFor 返回该槽距最近一次调用的空闲时长。
func (s *connSlot) idleFor(now time.Time) time.Duration {
	return now.Sub(time.Unix(0, s.lastUsed.Load()))
}

// connect 建立该槽连接：spawned http/sse 先等 url 就绪，再 Connect（含 initialize 握手）。
func (s *connSlot) connect(ctx context.Context, endpoint string) error {
	if s.proc != nil && s.proc.selfHeld {
		if err := waitEndpointReady(ctx, endpoint, s.proc.exited); err != nil {
			return fmt.Errorf("wait spawned endpoint: %w", err)
		}
	}
	if s.conn == nil {
		return fmt.Errorf("no transport connection")
	}
	if err := s.conn.connect(ctx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	return nil
}

// Close 释放该槽：下游 client 净断（先于 kill，避免断开噪音）→ 自持子进程 kill。
// 子进程已自行退出（崩溃监测路径）时跳过 client.Close（通道已死）。
func (s *connSlot) Close() error {
	s.closing.Store(true)
	if s.conn != nil && (s.proc == nil || !s.processExited()) {
		_ = s.conn.Close()
	}
	// 仅网关自持子进程（spawned http/sse）在此 kill；stdio 子进程归 SDK 管理（其 Close 已
	// 完成关 stdin → 等待 → 终止），L3 的进程树强杀走 killProcess（CancelCall）。
	if s.proc != nil && s.proc.selfHeld && s.proc.cmd != nil && s.proc.cmd.Process != nil {
		_ = s.proc.cmd.Process.Kill()
	}
	return nil
}

// proxyProvider 是 spawned（runtime 拉起）或 proxied（url 连接）下游的代理。
// 共享槽（p.shared）承担控制面（首连/ListTools/无 workdir 回落）；isolate=true 时调用面
// 按调用上下文的 (instance_id, work_dir)（withTurnContext → ctxKeyInstance/ctxKeyWorkDir）
// 走 p.pool 的专属槽。
// mu 保护池的增删与 building（single-flight）表；建连在 p.mu 外进行（不同池键互不阻塞，
// 同池键由 building 单飞，仅一次建连）。
type proxyProvider struct {
	name     string
	entry    *ServerEntry // 进程规格（Invalidate respawn / 池内新槽建连复用）
	kind     string       // spawned / proxied
	endpoint string       // http/sse 连接点（url，就绪轮询用）
	inject   bool         // 来源=builtin → 调用携带 _meta 内部上下文；user（第三方）→ 不携带
	isolate  bool         // 按 (instance, workdir) 隔离（ServerEntry.IsolateEnabled()）
	// sandboxOn 报告该 MCP 当前是否开启 agentbox 沙箱（stdio spawn 时按 entry 名查
	// usr `tool_sandbox.mcps`）；nil = 不注入。见 buildConn。
	sandboxOn sandboxOnFunc
	idleTTL   time.Duration
	logf      func(string, ...any)

	// ── L6 池化配置（ServerEntry 透传；缺省值见 PoolKeyMode 等方法）──
	poolMode      string        // shared（缺省）| call（每调用独占槽）
	poolSize      int           // 每池键连接槽数（当前实现按 1 消费，字段预留给多连接池）
	maxConcurrent int           // 每 server 在途调用上限（超出排队）
	grace         time.Duration // 取消阶梯 L1 宽限期
	queueTimeout  time.Duration // 排队超时（pool_timeout）
	sem           chan struct{} // 并发信号量（缓冲 = maxConcurrent）

	// ── L8 通知路由 ──
	callMu   sync.Mutex
	calls    map[string]*inflightCall // call_id → 在飞调用（progress/取消共用）
	callSeq  atomic.Uint64            // 无 tool_call_id 时的 call_id 序号
	handlers NotifyHandlers           // 进度/elicitation 上行回调（装配方注入；零值 = 丢弃）
	// clientHooks 注入 sdkConn（建连时）；nil = 不注册通知 handler（elicitation 不声明能力）。
	clientHooks *clientHooks

	mu sync.Mutex
	// rebuildMu 串行化 Invalidate 的「作废 + 重建」（C-52）：monitorSpawned 与取消路径
	// （Terminate→InvalidateScoped→Invalidate）并发进入会各自 spawn，后写 shared 覆盖前者 → 孤儿进程。
	// 锁序：rebuildMu → mu（Invalidate 内 closePooled 取 mu）；无反向路径（持 mu 者不取 rebuildMu）。
	rebuildMu sync.Mutex
	shared    atomic.Pointer[connSlot] // 共享槽（isolate=false 的唯一连接；always 建；Invalidate 原地换新）
	pool      map[string]*connSlot     // poolKeyFor(instance, workdir) → 专属槽（isolate=true）
	// building 是 per-key single-flight 表：poolKey → 进行中的建连（首调者建连、其余等待其完成并
	// 复用入池槽），保证同池键并发首调**只建一次连**（否则重复 spawn → isolate 固定端口可能端口冲突）。
	// 由 p.mu 保护；建连结束后（成功/失败）即移出。
	building map[string]*buildFlight
	genSeq   atomic.Uint64 // 代际序号（每次重建递增；见 connSlot.gen / WaitExit）
	noWd     atomic.Bool   // 「缺 workdir 回落共享」告警只记一次

	closing  atomic.Bool // 网关主动关闭/重建中（Close/Invalidate）→ 退出观测静默
	stopCh   chan struct{}
	stopOnce sync.Once
}

// newProxyProvider 建立下游 MCP client（stdio / http / sse）。
// transport 缺省推断（对齐 26-mcp-gateway）：url 存在 → http（runtime+url = spawn 后连 url）；
// 仅 runtime → stdio。
// callTimeout 复用为**池空闲回收 TTL**（既有项，不新增配置键）；<=0 时按全局默认 60s。
func newProxyProvider(entry *ServerEntry, callTimeout time.Duration, logf func(string, ...any), sandboxOn sandboxOnFunc, handlers ...NotifyHandlers) (*proxyProvider, error) {
	kind := "proxied"
	if entry.Runtime != "" {
		kind = "spawned"
	}
	ttl := callTimeout
	if ttl <= 0 {
		ttl = 60 * time.Second // 与 Params.CallTimeout 默认一致
	}
	p := &proxyProvider{name: entry.ID, entry: entry, kind: kind, endpoint: entry.URL,
		inject: entry.IsBuiltin(), isolate: entry.IsolateEnabled(), sandboxOn: sandboxOn, idleTTL: ttl, logf: logf,
		poolMode: entry.PoolKeyMode(), poolSize: entry.PoolSizeOrDefault(),
		maxConcurrent: entry.MaxConcurrentOrDefault(), grace: entry.GraceOrDefault(),
		queueTimeout: defaultPoolQueueTimeout, calls: map[string]*inflightCall{},
		pool: map[string]*connSlot{}, building: map[string]*buildFlight{}, stopCh: make(chan struct{})}
	if len(handlers) > 0 {
		p.handlers = handlers[0]
	}
	p.sem = make(chan struct{}, p.maxConcurrent)
	if p.handlers.Progress != nil || p.handlers.Elicit != nil {
		p.clientHooks = &clientHooks{progress: p.routeProgress, elicit: p.routeElicit}
	}
	shared, err := buildSlot(entry, sandboxOn, "", "", logf, p.clientHooks)
	if err != nil {
		return nil, err
	}
	shared.gen = p.genSeq.Add(1) // 代际 1（genSeq 同步推进；后续 Invalidate 自 2 起递增）
	p.shared.Store(shared)
	// 空闲回收：共享槽不存在回收需求；per-call 槽随调用结束销毁（无需 TTL）；仅隔离槽需要。
	if p.isolate || p.poolMode == PoolKeyCall {
		go p.sweepIdle() // 空闲池键连接回收（无活跃调用超时 → 关闭并移出池）
	}
	return p, nil
}

// defaultPoolQueueTimeout 是并发排队上限（L6.4）：超过即返回 pool_timeout，避免无限占用调用方。
// 非配置键（设计稿未列），单测可直改 provider.queueTimeout。
const defaultPoolQueueTimeout = 30 * time.Second

// clientHooks 把下游 client 的上行通知回调注入 sdkConn（L8）：progress 按 token 路由、
// elicitation 按 token 路由。nil = 不注册（progress 丢弃、elicitation 不声明能力）。
type clientHooks struct {
	progress func(token string, params *mcp.ProgressNotificationParams)
	elicit   func(token string, req *mcp.ElicitRequest) (*mcp.ElicitResult, error)
}

// buildSlot 按 entry 规格构建一个连接槽（含 spawned http/sse 的自持子进程）。
// 供首建共享槽、池内按 (instance, workdir) 懒建复用。logf 用于子进程 stderr 接入（C-58）。
func buildSlot(entry *ServerEntry, sandboxOn sandboxOnFunc, workdir, instance string, logf func(string, ...any), hooks ...*clientHooks) (*connSlot, error) {
	// 进程级写隔离的「允许写目录」须先打 Low IL 标签，否则 Low IL 子进程对其只能读不能写。
	// stdio 才做（http/sse 无网关侧 stdio 子进程）；每槽一次（不随每次工具调用重复 icacls）。
	if workdir != "" && entry.TransportName() == "stdio" {
		ensureLowILWritableRoots(entry.ID, workdir, entry.SandboxDirs, logf)
	}
	conn, cmd, done, exited, err := buildConn(entry, sandboxOn, logf, hooks...)
	if err != nil {
		return nil, err
	}
	s := &connSlot{workdir: workdir, instance: instance, conn: conn, proc: newProcSlot(entry, cmd, done, exited)}
	s.touch()
	return s, nil
}

// newProcSlot 组装进程槽：selfHeld = 网关自持（非 stdio：spawned http/sse 由本层 Wait/kill）；
// stdio 的 cmd 由 SDK CommandTransport Start/Wait，此处只保留句柄供 L3 杀进程树。
func newProcSlot(entry *ServerEntry, cmd *exec.Cmd, done chan error, exited *atomic.Bool) *procSlot {
	if cmd == nil && done == nil && exited == nil {
		return nil
	}
	return &procSlot{cmd: cmd, done: done, exited: exited, selfHeld: entry.TransportName() != "stdio"}
}

// ensureLowILWritableRoots 给 stdio 子进程的允许写目录打 Low IL 标签：
// 槽位 workdir + .chonkpilot/tmp（内置默认）+ SandboxDirs 中 writable=true 的目录
// （prj security-* 用户配置 + server 侧合并的默认项，G-75）。
// 打标失败只记 warn 并继续：失败时该目录写会被 OS 拒绝（安全方向 fail-closed），服务仍可读可用。
func ensureLowILWritableRoots(serverID, workdir string, sandboxDirs []sandbox.Rule, logf func(string, ...any)) {
	warnf := func(format string, args ...any) {
		if logf != nil {
			logf(format, args...)
		}
	}
	seen := map[string]bool{}
	var dirs []string
	add := func(dir string) {
		d := strings.TrimSpace(dir)
		if d == "" || seen[strings.ToLower(d)] {
			return
		}
		seen[strings.ToLower(d)] = true
		dirs = append(dirs, d)
	}
	add(workdir)
	tmpDir := filepath.Join(workdir, ".chonkpilot", "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		warnf("[gateway] %s: create sandbox tmp dir %q failed: %v（跳过该目录打标）", serverID, tmpDir, err)
	} else {
		add(tmpDir)
	}
	for _, d := range sandbox.WritableRoots(sandboxDirs) {
		add(d)
	}
	for _, dir := range dirs {
		if err := sandbox.EnsureLowIL(dir); err != nil {
			if errors.Is(err, sandbox.ErrUnsupportedPlatform) {
				return // 平台不支持进程级写隔离：静默（不是失败）
			}
			warnf("[gateway] %s: low-IL tag %q failed: %v（该目录对沙箱子进程不可写）", serverID, dir, err)
		}
	}
}

// buildConn 按 entry 规格构建一个下游连接（及 spawned http/sse 的自持子进程）。
func buildConn(entry *ServerEntry, sandboxOn sandboxOnFunc, logf func(string, ...any), hooks ...*clientHooks) (providerConn, *exec.Cmd, chan error, *atomic.Bool, error) {
	// 预连接会话（L5）：装配方已把进程内能力源连成 ClientSession → 直接复用（无子进程）。
	if entry.Session != nil {
		return &sdkConn{ss: entry.Session}, nil, nil, nil, nil
	}
	var hk *clientHooks
	if len(hooks) > 0 {
		hk = hooks[0]
	}
	transportName := entry.TransportName()
	switch transportName {
	case "stdio", "http", "sse":
	default:
		return nil, nil, nil, nil, fmt.Errorf("unknown transport %q", transportName)
	}
	// 显式 http/sse 必须有 url（runtime+url = spawn 后就绪再连；纯 url = proxied）。
	// 缺 url 时旧实现会先 spawn 再连空端点（必然失败）→ 此处前置明确报错，不 spawn。
	if transportName != "stdio" && strings.TrimSpace(entry.URL) == "" {
		return nil, nil, nil, nil, fmt.Errorf("transport %q needs url（server %q）", transportName, entry.ID)
	}

	// spawned http/sse：先拉起子进程（connect 时再轮询 url 就绪）
	var cmd *exec.Cmd
	var done chan error
	var exited *atomic.Bool
	needsSpawn := entry.Runtime != "" && (transportName == "http" || transportName == "sse")
	if needsSpawn {
		c, d, ex, err := startProcess(entry, logf)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("spawn %q: %w", entry.ID, err)
		}
		cmd, done, exited = c, d, ex
	}

	var conn providerConn
	switch transportName {
	case "stdio":
		argv, err := spawnArgv(entry)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		proc := exec.Command(argv[0], argv[1:]...)
		proc.SysProcAttr = winproc.SysProcAttr()
		// 进程级写隔离（stdio 才做；http/sse 无网关侧进程可管）：Low IL 令牌挂到 cmd 上，
		// 由官方 SDK CommandTransport 负责 Start。非 Windows 平台跳过（无该能力）。
		if err := sandbox.Prepare(proc, sandbox.Spec{}); err != nil && !errors.Is(err, sandbox.ErrUnsupportedPlatform) {
			return nil, nil, nil, nil, fmt.Errorf("gateway: 施加 Low IL 沙箱失败: %w", err)
		}
		if entry.Cwd != "" {
			proc.Dir = entry.Cwd // stdio 子进程工作目录（与 startProcess 同语义）
		}
		proc.Env = processEnv(entry)
		cli := mcp.NewClient(&mcp.Implementation{Name: "chonkpilot-mcp-gateway", Version: "0.1.0"}, clientOptions(hk))
		conn = &sdkConn{cli: cli, tr: &mcp.CommandTransport{Command: proc}}
		// 保留句柄（Process 由 SDK Start 后填充）：L7 L3 需按 pid 终止进程树。
		cmd = proc
	case "http", "sse":
		// http = streamable HTTP（2025-03-26 规范，自定义 SSE 常连可选）；
		// sse = 旧式 SSE（2024-11-05 规范：GET 常连 + endpoint 事件 + POST 上行）。
		// 两者协议不同，必须用各自 transport（旧实现统一走 StreamableClientTransport →
		// 旧式 SSE 端点连不上）。
		httpClient := http.DefaultClient
		if len(entry.Headers) > 0 {
			// SDK 未提供附加请求头字段 → 经自定义 http.Client RoundTripper 注入
			httpClient = &http.Client{Transport: &headerRoundTripper{base: http.DefaultTransport, headers: entry.Headers}}
		}
		cli := mcp.NewClient(&mcp.Implementation{Name: "chonkpilot-mcp-gateway", Version: "0.1.0"}, clientOptions(hk))
		if transportName == "sse" {
			conn = &sdkConn{cli: cli, tr: &mcp.SSEClientTransport{Endpoint: entry.URL, HTTPClient: httpClient}}
		} else {
			conn = &sdkConn{cli: cli, tr: &mcp.StreamableClientTransport{Endpoint: entry.URL, HTTPClient: httpClient}}
		}
	}
	return conn, cmd, done, exited, nil
}

// clientOptions 组装下游 client 的通知回调（L8）：
//   - ProgressNotificationHandler：收到 notifications/progress → 按 token 路由（未命中丢弃 + warn）；
//   - ElicitationHandler：仅在装配方注入 Elicit 回调时注册（否则**不声明** elicitation 能力，
//     避免第三方 server 发起无人处理的请求）。
func clientOptions(hk *clientHooks) *mcp.ClientOptions {
	if hk == nil || (hk.progress == nil && hk.elicit == nil) {
		return nil
	}
	opts := &mcp.ClientOptions{}
	if hk.progress != nil {
		opts.ProgressNotificationHandler = func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			if req == nil || req.Params == nil {
				return
			}
			hk.progress(tokenString(req.Params.ProgressToken), req.Params)
		}
	}
	if hk.elicit != nil {
		opts.ElicitationHandler = func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			if req == nil || req.Params == nil {
				return nil, fmt.Errorf("elicitation: empty request")
			}
			return hk.elicit(tokenString(req.Params.GetProgressToken()), req)
		}
	}
	return opts
}

// tokenString 归一 progressToken（协议允许 string / 数字）为字符串（非 string 数字用十进制文本；
// 其他类型返回空串 = 无法路由 → 丢弃）。
func tokenString(tok any) string {
	switch v := tok.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	default:
		return ""
	}
}

// headerRoundTripper 给下行请求注入附加头（StreamableClientTransport 未提供 Headers 字段，
// 以自定义 Transport 等效 mark3labs transport.WithHTTPHeaders/WithHeaders 语义）。
type headerRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	return t.base.RoundTrip(req)
}

// spawnArgv 组装 spawn 的 exec 参数数组：runtime 为**单个 token**（可执行/解释器，
// 可为含空格的完整路径），args 逐个原样透传——**不做任何 shell/引号解析**
// （对齐 mcp-server 工具契约的 runtime/args 语义：args = 传给子进程的 argv）。
func spawnArgv(e *ServerEntry) ([]string, error) {
	rt := strings.TrimSpace(e.Runtime)
	if rt == "" {
		return nil, fmt.Errorf("spawned transport needs runtime")
	}
	return append([]string{rt}, e.Args...), nil
}

// startProcess 按 entry.runtime/args 拉起子进程（env/cwd 生效），返回进程、Wait 结果通道与
// 退出标志（exited 由 Wait goroutine 原子置位，供无竞争读取，C-42）。logf 用于把子进程 stderr
// 逐行接入日志（-H windowsgui 下 stderr 不可见，启动/崩溃留痕，C-58）。
func startProcess(e *ServerEntry, logf func(string, ...any)) (*exec.Cmd, chan error, *atomic.Bool, error) {
	argv, err := spawnArgv(e)
	if err != nil {
		return nil, nil, nil, err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = winproc.SysProcAttr()
	if e.Cwd != "" {
		cmd.Dir = e.Cwd
	}
	cmd.Env = processEnv(e)
	// C-58：子进程 stderr 逐行转日志（nil logf → 仅消费管道不输出，避免子进程写满管道阻塞）。
	cmd.Stderr = &lineLogWriter{logf: logf, prefix: fmt.Sprintf("[gateway] spawned %s stderr:", e.ID)}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
	}
	done := make(chan error, 1)
	exited := &atomic.Bool{}
	go func() {
		err := cmd.Wait()
		exited.Store(true) // 先置退出标志（原子），再投递结果通道
		done <- err
	}()
	return cmd, done, exited, nil
}

// processEnv 组装 spawned 子进程环境变量：os.Environ() 基线 + entry.Env（K=V），
// 值经 expandVars 展开 %VAR% 与 ${VAR}（沿用 servers.list 字段同一展开函数；未定义变量
// 展开为空串，与 os.Expand/os.Getenv 既有语义一致）。无 env → nil（继承父进程环境）。
func processEnv(e *ServerEntry) []string {
	if len(e.Env) == 0 {
		return nil
	}
	env := make([]string, 0, len(e.Env))
	for _, kv := range e.Env {
		env = append(env, expandVars(kv))
	}
	return append(os.Environ(), env...)
}

// waitEndpointReady 轮询 url 的 TCP 端点直到可连（transport-safe：http/sse 均可），
// 或 ctx 超时 / 子进程提前退出（exited 原子标志，替代无同步读 cmd.ProcessState，C-42）。
func waitEndpointReady(ctx context.Context, rawURL string, exited *atomic.Bool) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	addr := net.JoinHostPort(u.Hostname(), port)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if exited != nil && exited.Load() {
			return fmt.Errorf("spawned process exited before ready")
		}
		conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// connect 建立**共享槽**连接：spawned http/sse 先等 url 就绪，再 Connect（含 initialize 握手）。
// 控制面（首连校验 / ListTools / reload）恒走共享槽；隔离槽在首次调用时懒建。
func (p *proxyProvider) connect(ctx context.Context) error {
	return p.shared.Load().connect(ctx, p.endpoint)
}

// warn 记一条告警（logf 缺省 nil → 静默）。
func (p *proxyProvider) warn(format string, args ...any) {
	if p.logf != nil {
		p.logf(format, args...)
	}
}

// slotFor 取本次调用应使用的连接槽并**占用**（inFlight+1；返回的 release 释放占用）：
//   - isolate=false（http/sse 缺省）→ 共享槽（现状：全 server 单连接共享）；
//   - isolate=true + workdir 非空 → 该 **(instance, workdir)** 专属槽（懒建；
//     **不同 instance / 不同 workdir 各自建连、互不阻塞**）；
//   - isolate=true 但 workdir 缺失 → 回落共享槽 + warn（不报错，保持可用）。
//
// 占用在 p.mu 内与「查池」一并完成，与 reclaimIdle 的「删除 + 关闭」互斥 → 空闲回收绝不关闭在飞槽。
// 建连（含 spawn + 握手，可数秒）在 p.mu **之外**进行——不同池键并发建连互不阻塞；同池键并发首调由
// **per-key single-flight**（p.building）保证**仅一个**建连，其余等待其结果（避免各自 build 造成重复
// spawn——isolate 固定端口可能端口冲突）。专属槽建连/握手失败 → warn 并回落共享槽（保证调用可用性，
// 如 spawned http/sse 因固定端口无法按 instance/workdir 重复拉起时退化为共享）。
func (p *proxyProvider) slotFor(ctx context.Context) (*connSlot, func()) {
	never := func() {}
	// L6.3：poolKey=call → 每次调用独占一条槽（池键含 call_id），调用结束即销毁。
	if p.poolMode == PoolKeyCall {
		if callID := strings.TrimSpace(CallIDFromContext(ctx)); callID != "" {
			return p.callSlotFor(ctx, callID)
		}
		if p.noWd.CompareAndSwap(false, true) {
			p.warn("[gateway] %s: pool_key=call but call context has no call_id → fallback to shared conn", p.name)
		}
		return p.shared.Load(), never
	}
	if !p.isolate {
		return p.shared.Load(), never
	}
	wd := strings.TrimSpace(WorkDirFromContext(ctx))
	if wd == "" {
		if p.noWd.CompareAndSwap(false, true) {
			p.warn("[gateway] %s: isolate=true but call context has no work_dir → fallback to shared conn", p.name)
		}
		return p.shared.Load(), never
	}
	inst := strings.TrimSpace(InstanceFromContext(ctx))
	key := poolKeyFor(inst, wd)

	// 查池 / 加入进行中的建连（single-flight）——同在 p.mu 内判定，避免「查池→建连」之间的窗口。
	p.mu.Lock()
	if s := p.pool[key]; s != nil {
		s.inFlight.Add(1)
		p.mu.Unlock()
		return s, s.releaseFn()
	}
	if f := p.building[key]; f != nil {
		// 同池键已有他者在建连：等待其结果，复用入池槽（**不**各自 build）。等待**可被本调用自身
		// 的 ctx（取消 / 超时）打断**——否则 owner 建连卡死（spawn 后既不监听也不退出、且 ctx 无
		// deadline）时 waiter 永久阻塞，且自身任务取消（onTaskCancel→Invalidate）也无法解除等待，
		// 执行池 goroutine / active 计数滞留。
		p.mu.Unlock()
		select {
		case <-f.done:
		case <-ctx.Done():
			return p.shared.Load(), never
		}
		if f.err != nil {
			return p.shared.Load(), never
		}
		if s := p.acquirePooled(key); s != nil {
			return s, s.releaseFn()
		}
		return p.shared.Load(), never
	}
	// provider 已停（Close/Stop 关闭 stopCh）/ 正在作废重建（closing）→ **不再 spawn 新子进程**：
	// 否则 Stop 之后到达的调用会先拉起子进程（spawned http/sse 还会 waitEndpointReady 阻塞到就绪）
	// 再被 kill，形成「停机后 spawn」窗口与端口占用风险（对齐 Invalidate 的前置 closed 守卫）。
	if p.closed() || p.closing.Load() {
		p.mu.Unlock()
		return p.shared.Load(), never
	}
	flight := &buildFlight{done: make(chan struct{})}
	p.building[key] = flight
	p.mu.Unlock()

	// 建连（含 spawn + 握手，可达数秒）在 p.mu 外进行：不同池键并发建连互不阻塞。
	slot, err := buildSlot(p.entry, p.sandboxOn, wd, inst, p.logf)
	if err == nil {
		err = slot.connect(ctx, p.endpoint)
	}
	if err != nil {
		if slot != nil {
			_ = slot.Close()
		}
		p.warn("[gateway] %s: build isolated conn for instance %s work_dir %s failed (%v) → fallback to shared conn", p.name, inst, wd, err)
		p.mu.Lock()
		flight.err = err
		delete(p.building, key)
		close(flight.done) // 在 p.mu 内关闭：与「移出 building」原子，避免新来者看到空档重复建连
		p.mu.Unlock()
		return p.shared.Load(), never
	}
	// 入池（短临界区）：作废/重建已发起（closing）→ 关闭新槽、不入池，回落共享槽。
	p.mu.Lock()
	if p.closing.Load() {
		flight.err = fmt.Errorf("%s: provider closing during isolated build", p.name)
		delete(p.building, key)
		close(flight.done)
		p.mu.Unlock()
		_ = slot.Close()
		return p.shared.Load(), never
	}
	slot.inFlight.Add(1) // 先占位（在飞），再入池
	p.pool[key] = slot
	delete(p.building, key)
	close(flight.done) // 与「入池」原子：等待者随后从池中取到同一槽
	p.mu.Unlock()
	if slot.proc != nil && slot.proc.selfHeld { // spawned http/sse：池化自持子进程同样挂退出观测
		go p.watchPooled(key, slot)
	}
	p.warn("[gateway] %s: isolated conn for instance %s work_dir %s ready (isolate=true)", p.name, inst, wd)
	return slot, slot.releaseFn()
}

// acquirePooled 在锁内查池并占用（inFlight+1）；未命中返回 nil。
func (p *proxyProvider) acquirePooled(key string) *connSlot {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.pool[key]
	if !ok {
		return nil
	}
	s.inFlight.Add(1)
	return s
}

// releaseFn 返回释放占用的闭包（调用方 defer）。
func (s *connSlot) releaseFn() func() { return func() { s.inFlight.Add(-1) } }

// never 是不占用连接的 no-op 释放（共享槽 / 回落路径）。
var never = func() {}

// callSlotKey 组合 per-call 独占槽的池键（`call\x00<call_id>`，与 (instance, workdir) 键不相交）。
func callSlotKey(callID string) string { return "call\x00" + callID }

// callSlotFor 为一次调用构建**独占**连接槽（L6.3 poolKey=call）：建连（spawn + 握手）后入池，
// 返回的 release 在调用结束时关闭并移出（每调用一条，结束后不残留）。
func (p *proxyProvider) callSlotFor(ctx context.Context, callID string) (*connSlot, func()) {
	key := callSlotKey(callID)
	never := func() {}
	p.mu.Lock()
	if s := p.pool[key]; s != nil { // 同 call_id 重复进入（理论不该发生）→ 复用并按占用计数
		s.inFlight.Add(1)
		p.mu.Unlock()
		return s, s.releaseFn()
	}
	if p.closed() || p.closing.Load() {
		p.mu.Unlock()
		return p.shared.Load(), never
	}
	p.mu.Unlock()

	wd := strings.TrimSpace(WorkDirFromContext(ctx))
	inst := strings.TrimSpace(InstanceFromContext(ctx))
	slot, err := buildSlot(p.entry, p.sandboxOn, wd, inst, p.logf, p.clientHooks)
	if err == nil {
		err = slot.connect(ctx, p.endpoint)
	}
	if err != nil {
		if slot != nil {
			_ = slot.Close()
		}
		p.warn("[gateway] %s: build per-call conn for %s failed (%v) → fallback to shared conn", p.name, callID, err)
		return p.shared.Load(), never
	}
	slot.key = key
	slot.gen = p.genSeq.Add(1)
	p.mu.Lock()
	if p.closing.Load() {
		p.mu.Unlock()
		_ = slot.Close()
		return p.shared.Load(), never
	}
	slot.inFlight.Add(1)
	p.pool[key] = slot
	p.mu.Unlock()
	release := func() {
		slot.inFlight.Add(-1)
		p.destroyCallSlot(key, slot)
	}
	return slot, release
}

// destroyCallSlot 移除并关闭 per-call 独占槽（调用结束即销毁）。
func (p *proxyProvider) destroyCallSlot(key string, s *connSlot) {
	p.mu.Lock()
	if p.pool[key] == s {
		delete(p.pool, key)
		p.mu.Unlock()
		_ = s.Close()
		return
	}
	p.mu.Unlock()
}

// watchPooled 观测池化自持子进程（spawned http/sse）：意外退出 → 从池中移除（下次调用懒建）；
// provider/槽主动关闭（closing）→ 静默返回。
func (p *proxyProvider) watchPooled(key string, s *connSlot) {
	err := <-s.proc.done
	if p.closing.Load() || s.closing.Load() {
		return
	}
	p.mu.Lock()
	if p.pool[key] == s {
		delete(p.pool, key)
	}
	p.mu.Unlock()
	p.warn("[gateway] %s: isolated process for instance %s work_dir %s exited (%v), removed from pool", p.name, s.instance, s.workdir, err)
}

// sweepIdle 周期性回收空闲连接槽（isolate=true）：空闲超过 idleTTL（复用 Params.CallTimeout，
// **不新增配置键**）且无在飞调用 → 关闭该槽的 conn/子进程并移出池（下次调用懒建）。
// instance 退出后其不再有调用 → 随之空闲被回收。扫描间隔 = TTL/2（clamp 到 [100ms, 30s]）。
func (p *proxyProvider) sweepIdle() {
	interval := p.idleTTL / 2
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	if interval > 30*time.Second {
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-t.C:
			p.reclaimIdle()
		}
	}
}

// reclaimIdle 回收空闲超时的连接槽（不回收在飞调用的槽：占用与删除同在 p.mu 内判定）。
func (p *proxyProvider) reclaimIdle() {
	now := time.Now()
	var dead []*connSlot
	p.mu.Lock()
	for key, s := range p.pool {
		if strings.HasPrefix(key, "call\x00") {
			continue // per-call 独占槽随调用结束销毁（不参与空闲回收）
		}
		if s.inFlight.Load() > 0 || s.idleFor(now) < p.idleTTL {
			continue
		}
		delete(p.pool, key)
		dead = append(dead, s)
	}
	p.mu.Unlock()
	for _, s := range dead {
		_ = s.Close()
		p.warn("[gateway] %s: isolated conn for instance %s work_dir %s reclaimed (idle > %s)", p.name, s.instance, s.workdir, p.idleTTL)
	}
}

// closePooled 关闭并移出池中**全部**连接槽（Invalidate/Close：作废路径须覆盖全池；
// 下次调用按 (instance, workdir) 懒建）。
func (p *proxyProvider) closePooled() {
	p.mu.Lock()
	slots := make([]*connSlot, 0, len(p.pool))
	for key, s := range p.pool {
		slots = append(slots, s)
		delete(p.pool, key)
	}
	p.mu.Unlock()
	for _, s := range slots {
		_ = s.Close()
	}
}

// closePooledSlot 关闭并移出**指定池键**的连接槽（返回被关闭的槽；未命中 → nil）。
// 供 InvalidateScoped 使用：只打断该 (instance, workdir) 的调用，其他 instance/workdir 不受影响。
func (p *proxyProvider) closePooledSlot(instanceID, workdir string) *connSlot {
	key := poolKeyFor(instanceID, workdir)
	p.mu.Lock()
	s, ok := p.pool[key]
	if ok {
		delete(p.pool, key)
	}
	p.mu.Unlock()
	if !ok {
		return nil
	}
	_ = s.Close()
	return s
}

func (p *proxyProvider) Name() string { return p.kind + ":" + p.name }

// SpawnedSelf 是否网关自持子进程（spawned http/sse；退出观测/Close kill 的对象）。
// 以共享槽为准（网关 monitorSpawned 的观测对象；池化槽由 watchPooled 各自观测）。
func (p *proxyProvider) SpawnedSelf() bool {
	s := p.shared.Load()
	return s != nil && s.proc != nil && s.proc.selfHeld
}

// WaitExit 返回共享槽子进程退出结果、**该槽的代际号**与**是否自持子进程**（单次 shared.Load()，
// 避免与 SpawnedSelf 两次独立 Load 之间的窄竞态——期间重建换槽会多一次假重建）。仅自持子进程
// 携带退出通道（缓冲通道，可多次/提前接收）；spawned=false 时返回的空通道与 nil 不参与观测。
// 重建（Invalidate）后返回新进程的退出通道与新代际；调用方（monitorSpawned）据此识别本次退出是否
// 已被重建路径认领（捕获代际 ≠ 当前共享槽代际 = 该槽已被主动替换，而非意外退出）。
func (p *proxyProvider) WaitExit() (<-chan error, uint64, bool) {
	s := p.shared.Load()
	if s.proc == nil || !s.proc.selfHeld || s.proc.done == nil {
		ch := make(chan error, 1)
		ch <- nil
		return ch, s.gen, false
	}
	return s.proc.done, s.gen, true
}

// closed 报告 provider 是否已进入 Close 流程（Stop/unregister）。Close 关闭 stopCh，
// 而 Invalidate（重建）不关闭 → 供退出观测区分「主动关停」与「临时重建窗口」（C-37）。
func (p *proxyProvider) closed() bool {
	select {
	case <-p.stopCh:
		return true
	default:
		return false
	}
}

// ListTools 走共享槽（工具面为 server 级、与 workdir 无关）。
func (p *proxyProvider) ListTools(ctx context.Context) ([]*mcp.Tool, error) {
	conn := p.shared.Load().conn
	if conn == nil {
		return nil, fmt.Errorf("no transport connection")
	}
	return conn.ListTools(ctx)
}

// ── 原语面（primitiveSource）：经共享槽连接透传下游 prompts/resources ──

func (p *proxyProvider) primitiveConn() (primitiveSource, error) {
	conn := p.shared.Load().conn
	if conn == nil {
		return nil, fmt.Errorf("no transport connection")
	}
	src, ok := conn.(primitiveSource)
	if !ok {
		return nil, fmt.Errorf("provider %s has no primitive surface", p.entryID())
	}
	return src, nil
}

func (p *proxyProvider) ListPrompts(ctx context.Context) ([]*mcp.Prompt, error) {
	src, err := p.primitiveConn()
	if err != nil {
		return nil, err
	}
	return src.ListPrompts(ctx)
}

func (p *proxyProvider) GetPrompt(ctx context.Context, name string) (*mcp.GetPromptResult, error) {
	src, err := p.primitiveConn()
	if err != nil {
		return nil, err
	}
	return src.GetPrompt(ctx, name)
}

func (p *proxyProvider) ListResources(ctx context.Context) ([]*mcp.Resource, error) {
	src, err := p.primitiveConn()
	if err != nil {
		return nil, err
	}
	return src.ListResources(ctx)
}

func (p *proxyProvider) ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	src, err := p.primitiveConn()
	if err != nil {
		return nil, err
	}
	return src.ReadResource(ctx, uri)
}

// entryID 返回下游条目名（日志/错误用）。
func (p *proxyProvider) entryID() string {
	if p.entry == nil {
		return p.Name()
	}
	return p.entry.ID
}

func (p *proxyProvider) Call(ctx context.Context, tool string, args map[string]any) (*mcp.CallToolResult, error) {
	// L6.4：每 server 在途调用上限 → 超出有界排队；排队超时返回 pool_timeout。
	releaseSem, err := p.acquireConcurrency(ctx)
	if err != nil {
		return nil, err
	}
	defer releaseSem()

	// 调用上下文 `_meta` 只注入来源=builtin（本仓自有/内置）的下游；user（第三方）不携带。
	// 判定按来源（ServerEntry.Origin），**与是否 in-process 无关**——desktop（单体）部署自有
	// mcp-server（spawned/proxied）同为 builtin，同样注入。
	slot, release := p.slotFor(ctx)
	defer release()
	if slot.conn == nil {
		return nil, fmt.Errorf("no transport connection")
	}
	slot.touch()
	var meta mcp.Meta
	if p.inject {
		meta = callContextMeta(ctx)
	}
	// L8：登记调用级 in-flight（progressToken = call_id）→ 进度通知可精确路由、取消可精确定位。
	infl, callCtx := p.registerCall(ctx, tool, slot)
	defer p.unregisterCall(infl)

	res, err := slot.conn.Call(callCtx, tool, args, meta, infl.callID)
	// L7 连带：同槽被其他调用取消 → 本调用以 slot_killed 结束并附受影响 call_id 列表。
	infl.mu.Lock()
	killed := infl.err
	infl.mu.Unlock()
	if killed != nil {
		return nil, killed
	}
	return res, err
}

// acquireConcurrency 获取本 server 的并发配额（L6.4）：满则排队，排队超过 queueTimeout
// 返回 kind=pool_timeout 的 CallError；调用 ctx 取消则立即返回。
func (p *proxyProvider) acquireConcurrency(ctx context.Context) (func(), error) {
	if p.sem == nil {
		return func() {}, nil
	}
	select {
	case p.sem <- struct{}{}:
		return func() { <-p.sem }, nil
	default:
	}
	timer := time.NewTimer(p.queueTimeout)
	defer timer.Stop()
	select {
	case p.sem <- struct{}{}:
		return func() { <-p.sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, &CallError{
			Kind:  ErrKindPoolTimeout,
			Cause: fmt.Sprintf("server %s: %d concurrent calls in flight, queued > %s", p.name, p.maxConcurrent, p.queueTimeout),
		}
	}
}

// registerCall 生成/占用 call_id 并登记在飞调用（L8）。call_id 来源：调用上下文
// （tool_call_id）优先，否则 provider 自增；冲突时加 `#n` 后缀保证注册表唯一。
func (p *proxyProvider) registerCall(parent context.Context, tool string, slot *connSlot) (*inflightCall, context.Context) {
	base := strings.TrimSpace(CallIDFromContext(parent))
	if base == "" {
		base = fmt.Sprintf("call-%06d", p.callSeq.Add(1))
	}
	callCtx, cancel := context.WithCancel(parent)
	infl := &inflightCall{
		slot: slot, slotID: slot.slotID(), tool: tool,
		instance:  strings.TrimSpace(InstanceFromContext(parent)),
		workdir:   strings.TrimSpace(WorkDirFromContext(parent)),
		startedAt: time.Now(), phase: "running", cancel: cancel, done: make(chan struct{}),
	}
	p.callMu.Lock()
	id := base
	for n := 2; ; n++ {
		if _, exists := p.calls[id]; !exists {
			break
		}
		id = fmt.Sprintf("%s#%d", base, n)
	}
	infl.callID = id
	p.calls[id] = infl
	p.callMu.Unlock()
	return infl, callCtx
}

// unregisterCall 移出注册表并关闭完成信号（幂等）。
func (p *proxyProvider) unregisterCall(c *inflightCall) {
	p.callMu.Lock()
	if p.calls[c.callID] == c {
		delete(p.calls, c.callID)
	}
	p.callMu.Unlock()
	c.mu.Lock()
	if c.phase == "running" {
		c.phase = "done"
	}
	c.mu.Unlock()
	select {
	case <-c.done:
	default:
		close(c.done)
	}
}

// CancelCall 按**调用**执行取消阶梯（L7；`onTaskCancel` 在调用方实现 CallCanceller 时走此路径）：
//
//	L0 取消该调用的 ctx → 官方 SDK 的 call() 在 ctx 取消时发 `notifications/cancelled`
//	   （requestId = 该调用 id；go-sdk 无直发 API，ctx 取消即协议取消的等价实现，见
//	   memnode_cancel_probe_test.go 实证）；
//	L1 等 grace（缺省 grace_ms=2000）：调用自行结束 → Graceful；
//	L2 关该槽连接/会话（stdio 关 stdin/session；http streamable 断会话；sse 断常连）；
//	L3 槽仍有进程（stdio 或 spawned http/sse）→ kill 进程树（taskkill /T /F）；纯远程 http/sse
//	   无网关侧进程 → 回退 L2（不伪造 Killed）；
//	cascade-report：同槽其他在飞调用一并标记 slot_killed（附受影响 call_id 列表），不假装干净。
func (p *proxyProvider) CancelCall(ctx context.Context, callID string, grace time.Duration) (CancelReport, error) {
	if grace <= 0 {
		grace = p.grace
	}
	rep := CancelReport{CallID: callID}
	p.callMu.Lock()
	c := p.calls[callID]
	p.callMu.Unlock()
	if c == nil {
		return rep, nil // 已结束 / 未登记：无动作（幂等）
	}
	// L0：协议取消（ctx 取消 → SDK 发 notifications/cancelled，requestId = 该调用 id）
	c.mu.Lock()
	c.phase = "cancelled"
	c.cancelled = true
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	// L1：宽限期等调用自行返回
	if waitCallDone(ctx, c.done, grace) {
		rep.Graceful = true
		return rep, nil
	}
	// L2：关连接（该槽）
	if c.slot != nil {
		c.slot.closeConn()
		rep.ConnClosed = true
	}
	const connCloseWait = 500 * time.Millisecond
	if waitCallDone(ctx, c.done, connCloseWait) {
		return rep, nil
	}
	// L3：进程树终止（仅存在网关可见进程时；纯远程回退 L2）
	if c.slot != nil {
		rep.Killed = c.slot.killProcess()
	}
	// cascade-report：同槽其他在飞调用连带终止
	rep.Cascaded = p.cascadeSlot(c.slot, callID)
	// 收口：L2/L3 已关连接/杀进程 → 该槽不可再用，池槽移出（下次懒建）、共享槽异步重建
	// （否则后续调用会复用已关闭的会话，且不能像旧 Terminate 那样同步重建阻塞取消路径）。
	p.retireCancelledSlot(c.slot)
	return rep, nil
}

// retireCancelledSlot 在取消阶梯非优雅收口后使该槽失效（不阻塞取消路径）：
//   - 池槽（isolated / per-call）→ 移出池（per-call 由 release 销毁；下次调用按池键懒建）；
//   - 共享槽（isolate=false 的唯一连接 / workdir 缺失回落）→ 异步**只**重建共享槽
//     （不影响池中其他 (instance, workdir) 槽）。
func (p *proxyProvider) retireCancelledSlot(slot *connSlot) {
	if slot == nil {
		return
	}
	if slot.key == "" && p.shared.Load() == slot {
		go p.rebuildShared()
		return
	}
	p.mu.Lock()
	if slot.key != "" && p.pool[slot.key] == slot {
		delete(p.pool, slot.key)
	}
	p.mu.Unlock()
	_ = slot.Close()
}

// rebuildShared 只重建**共享槽**（取消阶梯关连接/杀进程后的收口；异步、不阻塞取消路径）：
// 与 Invalidate 的全量语义不同——**不**触碰池中 (instance, workdir) 槽。重建失败 → 留空槽
// （后续调用明确报 no transport connection），并 warn。
func (p *proxyProvider) rebuildShared() {
	p.rebuildMu.Lock()
	defer p.rebuildMu.Unlock()
	if p.closed() {
		return
	}
	p.closing.Store(true)
	defer p.closing.Store(false)
	_ = p.shared.Load().Close()
	conn, cmd, done, exited, err := buildConn(p.entry, p.sandboxOn, p.logf, p.clientHooks)
	if err != nil {
		p.shared.Store(&connSlot{gen: p.genSeq.Add(1)})
		p.warn("[gateway] %s: rebuild shared conn after cancel failed: %v", p.name, err)
		return
	}
	slot := &connSlot{conn: conn, proc: newProcSlot(p.entry, cmd, done, exited), gen: p.genSeq.Add(1)}
	slot.touch()
	p.shared.Store(slot)
	ctx, cancel := context.WithTimeout(context.Background(), p.idleTTL)
	defer cancel()
	if err := slot.connect(ctx, p.endpoint); err != nil {
		_ = slot.Close()
		p.shared.Store(&connSlot{gen: p.genSeq.Add(1)})
		p.warn("[gateway] %s: reconnect shared conn after cancel failed: %v", p.name, err)
	}
}

// waitCallDone 等待调用完成信号（最多 d；ctx 取消也立即返回）。
func waitCallDone(ctx context.Context, done <-chan struct{}, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}

// cascadeSlot 把同槽的其他在飞调用标记为 slot_killed 并取消（L7 连带语义），
// 返回受影响的 call_id 列表（不含发起取消的调用）。
func (p *proxyProvider) cascadeSlot(slot *connSlot, exclude string) []string {
	if slot == nil {
		return nil
	}
	p.callMu.Lock()
	var victims []*inflightCall
	for _, c := range p.calls {
		if c.slot == slot && c.callID != exclude {
			victims = append(victims, c)
		}
	}
	ids := make([]string, 0, len(victims))
	for _, v := range victims {
		ids = append(ids, v.callID)
	}
	p.callMu.Unlock()
	for _, v := range victims {
		v.markKilled("same slot as cancelled call "+exclude, ids)
		if v.cancel != nil {
			v.cancel()
		}
	}
	if len(ids) > 0 {
		p.warn("[gateway] %s: cascade cancel of call %s also aborted %v (slot %s)", p.name, exclude, ids, slot.slotID())
	}
	return ids
}

// routeProgress 把下游 notifications/progress 按其 progressToken 路由到对应调用（L8）：
// 命中 → 投递给装配方回调；未命中/不可路由 → **丢弃并 warn**（不猜、不广播）。
func (p *proxyProvider) routeProgress(token string, params *mcp.ProgressNotificationParams) {
	if token == "" {
		p.warn("[gateway] %s: progress notification without usable progressToken → dropped", p.name)
		return
	}
	p.callMu.Lock()
	c := p.calls[token]
	p.callMu.Unlock()
	if c == nil {
		p.warn("[gateway] %s: progress token %q does not match any in-flight call → dropped", p.name, token)
		return
	}
	if p.handlers.Progress == nil {
		return
	}
	p.handlers.Progress(ProgressEvent{
		CallID: c.callID, SlotID: c.slotID, Server: p.name, Tool: c.tool,
		InstanceID: c.instance, Progress: params.Progress, Total: params.Total,
		Message: params.Message, At: time.Now(),
	})
}

// routeElicit 把下游 elicitation 请求按其 progressToken 路由（L8）：token 即 call_id 时可归位；
// 未携带 token 时以空 callID 上报（装配方自行决定接受/拒绝），不伪造来源。
func (p *proxyProvider) routeElicit(token string, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	if p.handlers.Elicit == nil {
		return nil, fmt.Errorf("elicitation not supported")
	}
	if token != "" {
		p.callMu.Lock()
		_, ok := p.calls[token]
		p.callMu.Unlock()
		if !ok {
			p.warn("[gateway] %s: elicitation token %q does not match any in-flight call → delivered without call binding", p.name, token)
		}
	}
	return p.handlers.Elicit(token, req)
}

// Invalidate 作废当前连接/子进程并**恒**重建（统一异步模型：取消/重建恒 = kill + respawn，
// 与用户口径「取消直接 kill process 即可，重新 spawn 新的」一致；不再按 restart 策略分支）。
// 覆盖范围 = 共享槽 + 池中**全部** workdir 槽（隔离槽一律关闭移出，下次调用按 workdir 懒建）。
//
// stdio 路径：sdkConn.Close() → 官方 CommandTransport 的 pipeRWC.Close()（关 stdin → 等退出 →
// SIGTERM → SIGKILL）；随后 buildConn 新起 exec.Cmd + 新 client 会话并 connect（initialize 握手）。
// spawned http/sse：kill 自持子进程 + 断连，再按规格重新 spawn + 轮询就绪 + 连接。
// 返回 rebuilt=false 仅在重建/重连失败时（provider 暂不可用，需重新注册/reload）。
func (p *proxyProvider) Invalidate(ctx context.Context) (bool, error) {
	// 已停（Close / Stop 已关闭 stopCh）：不再 spawn/重连——否则并发的取消路径
	//（onTaskCancel / CancelExec → Terminate → Invalidate）会在 Stop 之后 respawn 出无人观测的
	// 孤儿子进程（观测 goroutine 已随 Stop 退出）。
	if p.closed() {
		return false, fmt.Errorf("%s: provider closed", p.name)
	}
	// 串行化重建（C-52）：并发进入（如 monitorSpawned 重建与取消路径重建）会各自 spawn，
	// 后写 shared 覆盖前者 → 前一子进程失去句柄成孤儿。持锁期间仍在 p.mu 内完成作废（closePooled）。
	p.rebuildMu.Lock()
	defer p.rebuildMu.Unlock()
	p.closing.Store(true) // 作废/重建期间退出观测静默
	p.closePooled()
	_ = p.shared.Load().Close()
	// 失败路径统一复位：关掉可能半初始化的新槽 → shared 清为空槽（conn=nil，后续调用明确
	// 报 no transport connection，而非复用已关闭/半初始化的连接）→ closing 归位（避免长期静默）。
	fail := func(err error) (bool, error) {
		_ = p.shared.Load().Close()
		// 失败回落：保留**单调代际**（不复用零值 0）——monitorSpawned 以「捕获代际 ≠ 当前共享槽
		// 代际」判定某次退出是否已被重建路径认领；置 0 会让空槽与任意代际都无法区分 → 代际判定
		// 误判、多一次假重建。
		p.shared.Store(&connSlot{gen: p.genSeq.Add(1)})
		p.closing.Store(false)
		return false, err
	}
	conn, cmd, done, exited, err := buildConn(p.entry, p.sandboxOn, p.logf, p.clientHooks)
	if err != nil {
		return fail(fmt.Errorf("respawn %q: %w", p.name, err))
	}
	slot := &connSlot{conn: conn, proc: newProcSlot(p.entry, cmd, done, exited), gen: p.genSeq.Add(1)}
	slot.touch()
	p.shared.Store(slot)
	if err := p.connect(ctx); err != nil {
		return fail(fmt.Errorf("reconnect %q: %w", p.name, err))
	}
	p.closing.Store(false)
	return true, nil
}

// InvalidateScoped 是 Invalidate 的**作用域版**（2026-09-19，缺口 1）：只作废/重建
// **指定 (instance_id, work_dir)** 归属的池化连接槽（kill 该槽的下游子进程/断连），
// 其他 instance / workdir 的连接与在飞调用**不受影响** —— 取消一方不再打断另一方。
//
// 语义与回落：
//   - instanceID 与 workdir 均空 → 退化为全量 Invalidate（旧语义：无隔离维度的调用方兜底）；
//   - 命中该池键 → 关闭并移出池（下次调用按该键懒建；**不**原地重建，避免为已结束的调用
//     白起进程），返回 (true, nil)；
//   - 未命中（isolate=false / workdir 缺失回落共享槽 / 已被回收）→ 回落全量 Invalidate
//     （保证取消仍真实打断在飞调用，与引入本方法前行为等价）。
//
// Terminate 即调用本方法（取消的执行线入口）；内存/注册型 provider 的 Terminate 为协作式 no-op。
func (p *proxyProvider) InvalidateScoped(ctx context.Context, instanceID, workdir string) (bool, error) {
	if instanceID == "" && workdir == "" {
		return p.Invalidate(ctx)
	}
	if slot := p.closePooledSlot(instanceID, workdir); slot != nil {
		p.warn("[gateway] %s: invalidated isolated conn for instance %s work_dir %s (scoped cancel)", p.name, instanceID, workdir)
		return true, nil
	}
	return p.Invalidate(ctx)
}

// Terminate 是取消的**执行线入口**（`onTaskCancel` 调用）：
//   - 纯远程（proxied，无自持 cmd）→ **协作式 no-op**：取消已由执行池 ctx 送达（MCP client
//     随 ctx 中止 HTTP 请求并发出 notifications/cancelled）；对端进程可能继续运行 = 语义
//     「尽力」（见 18 §3.4）。**不重建共享连接**：重建（断连 + initialize 握手）在取消路径上
//     同步进行，会阻塞 mq 同步派发线程（实测 30s ctx 打满 → task-stop / `/eval` 超时）；并发
//     取消还串行化在 rebuildMu 上、各自 ctx 在等锁期间过期，最终把共享槽置为 **空槽**（conn=nil）
//     → 之后所有调用报 "no transport connection"（2026-10-19 G-68 第二步回归实测）。
//   - spawned（自持子进程）→ 按 (instance, workdir) 作用域 kill + respawn：命中隔离槽仅 kill
//     该槽（下次调用懒建）；否则全量 kill + respawn。
func (p *proxyProvider) Terminate(ctx context.Context, instanceID, workdir string) (bool, error) {
	if p.kind == "proxied" {
		return true, nil
	}
	return p.InvalidateScoped(ctx, instanceID, workdir)
}

// Close 释放资源：停空闲回收 → 关闭池中全部连接槽 → 共享槽净断（先于 kill，避免断开噪音）。
func (p *proxyProvider) Close() error {
	p.closing.Store(true)
	p.stopOnce.Do(func() { close(p.stopCh) })
	p.closePooled()
	return p.shared.Load().Close()
}

// lineLogMaxBuf 是单条未换行缓冲的上限：长时间无换行时强制截断输出（C-59）。
const lineLogMaxBuf = 64 << 10

// lineLogWriter 把子进程 stderr 逐行转日志（避免 -H windowsgui 下 stderr 不可见，C-58）。
// prefix 为日志行前缀；未出现换行的超长缓冲按 maxBuf（缺省 lineLogMaxBuf）强制截断输出（C-59）。
type lineLogWriter struct {
	logf   func(string, ...any)
	prefix string
	maxBuf int
	mu     sync.Mutex
	buf    []byte
}

func (w *lineLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(w.buf[:i]), "\r")
		w.buf = w.buf[i+1:]
		if line != "" && w.logf != nil {
			w.logf("%s %s", w.prefix, line)
		}
	}
	max := w.maxBuf
	if max <= 0 {
		max = lineLogMaxBuf
	}
	if len(w.buf) > max {
		if w.logf != nil {
			w.logf("%s %s…（超长行截断）", w.prefix, string(w.buf[:max]))
		}
		w.buf = w.buf[:0]
	}
	return len(p), nil
}

// ctxKey 是 turn 上下文键（doCall 经 withTurnContext 注入，供 inmemory 节点 handler 恢复 server turn
// 与调用上下文——Context 经协议 _meta 透传给内嵌 mcp-server）。
type ctxKey int

const (
	ctxKeyTurn ctxKey = iota
	ctxKeySession
	ctxKeyInstance
	ctxKeyToolCallID
	ctxKeyWorkDir
	ctxKeyDataDir
	ctxKeyTopSession
	ctxKeyParent
	ctxKeyPurpose
	ctxKeyCallID
)

// withCallID 把本次调用的 call_id 注入执行 context（L6.3 pool_key=call 的池键 / L7 取消定位 /
// L8 progressToken 路由三者共用同一标识）。
func withCallID(ctx context.Context, callID string) context.Context {
	if callID == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKeyCallID, callID)
}

// CallIDFromContext 返回 context 中的调用 id（无则空）。
func CallIDFromContext(ctx context.Context) string { return strFromCtx(ctx, ctxKeyCallID) }

// withTurnContext 把请求 turn 上下文（session/turn/instance/tool_call_id + work_dir/data_dir +
// top_session/parent）注入执行 context——inmemory 节点 handler 据此恢复对应 server turn，并把调用
// 上下文以 _meta 透传给内嵌 mcp-server（spawn executor 时注入 CHONKPILOT_*）。
func withTurnContext(ctx context.Context, c Context) context.Context {
	if c.Session != "" {
		ctx = context.WithValue(ctx, ctxKeySession, c.Session)
	}
	if c.Turn != "" {
		ctx = context.WithValue(ctx, ctxKeyTurn, c.Turn)
	}
	if c.InstanceID != "" {
		ctx = context.WithValue(ctx, ctxKeyInstance, c.InstanceID)
	}
	if c.ToolCallID != "" {
		ctx = context.WithValue(ctx, ctxKeyToolCallID, c.ToolCallID)
	}
	if c.WorkDir != "" {
		ctx = context.WithValue(ctx, ctxKeyWorkDir, c.WorkDir)
	}
	if c.DataDir != "" {
		ctx = context.WithValue(ctx, ctxKeyDataDir, c.DataDir)
	}
	if c.TopSession != "" {
		ctx = context.WithValue(ctx, ctxKeyTopSession, c.TopSession)
	}
	if c.Parent != "" {
		ctx = context.WithValue(ctx, ctxKeyParent, c.Parent)
	}
	if c.Purpose != "" {
		ctx = context.WithValue(ctx, ctxKeyPurpose, c.Purpose)
	}
	return ctx
}

// ─── turn 上下文导出访问器（内嵌 mcp-server 域工具 handler 使用）───

// TurnFromContext 返回 context 中的 turn id（无则空）。
func TurnFromContext(ctx context.Context) string { return strFromCtx(ctx, ctxKeyTurn) }

// SessionFromContext 返回 context 中的 session id（无则空）。
func SessionFromContext(ctx context.Context) string { return strFromCtx(ctx, ctxKeySession) }

// InstanceFromContext 返回 context 中的 instance id（无则空）。
func InstanceFromContext(ctx context.Context) string { return strFromCtx(ctx, ctxKeyInstance) }

// ToolCallIDFromContext 返回 context 中的 LLM tool-call id（无则空）。
func ToolCallIDFromContext(ctx context.Context) string { return strFromCtx(ctx, ctxKeyToolCallID) }

// WorkDirFromContext 返回 context 中的项目工作目录（无则空）。
func WorkDirFromContext(ctx context.Context) string { return strFromCtx(ctx, ctxKeyWorkDir) }

// DataDirFromContext 返回 context 中的数据目录（无则空）。
func DataDirFromContext(ctx context.Context) string { return strFromCtx(ctx, ctxKeyDataDir) }

// TopSessionFromContext 返回 context 中的主会话 id（任务树归属；无则空）。
func TopSessionFromContext(ctx context.Context) string { return strFromCtx(ctx, ctxKeyTopSession) }

// ParentFromContext 返回 context 中的父任务节点 id（调用层已登记的 llm 侧节点；无则空）。
func ParentFromContext(ctx context.Context) string { return strFromCtx(ctx, ctxKeyParent) }

// PurposeFromContext 返回 context 中本次调用的**运行目的**（= 注入的展示名参数
// tool_call_display_name，C1 2026-10-15 补入上下文；无则空）。
func PurposeFromContext(ctx context.Context) string { return strFromCtx(ctx, ctxKeyPurpose) }

func strFromCtx(ctx context.Context, k ctxKey) string {
	if v := ctx.Value(k); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
