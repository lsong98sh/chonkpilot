// Provider：spawned / proxied（官方 modelcontextprotocol/go-sdk client 代理转发）。
// 同进程能力一律走 memNode（官方 InMemoryTransports），不在此层。
// 对齐 21-llm-server：gateway 不扫描目录，工具来源仅注册的 mcp-server + 自身 hot。
//
// spawned http/sse（runtime + url）：网关 spawn 子进程 → 轮询 url 就绪 → 建 client 连接；
// 进程由网关自持（Close kill / 意外退出 → failed 通知）。仅 runtime（无 url）= stdio client
// （子进程由官方 SDK CommandTransport 内部管理，无退出观测）。纯 url = proxied 只连不拉。
package mcpgateway

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/agentbox"
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
	Call(ctx context.Context, tool string, args map[string]any, meta mcp.Meta) (*mcp.CallToolResult, error)
	Close() error
}

// sdkConn 官方 SDK client 会话（stdio CommandTransport / http StreamableClientTransport /
// sse 旧式 SSEClientTransport）。
type sdkConn struct {
	cli *mcp.Client
	tr  mcp.Transport
	ss  *mcp.ClientSession
}

func (c *sdkConn) connect(ctx context.Context) error {
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

func (c *sdkConn) Call(ctx context.Context, tool string, args map[string]any, meta mcp.Meta) (*mcp.CallToolResult, error) {
	if c.ss == nil {
		return nil, fmt.Errorf("not connected")
	}
	return c.ss.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args, Meta: meta})
}

func (c *sdkConn) Close() error {
	if c.ss != nil {
		return c.ss.Close()
	}
	return nil
}

// ─── proxyProvider（spawned / proxied：标准 MCP client 转发） ─────────────

// connSlot 是一条下游连接槽：conn = 标准 MCP client 会话；cmd/done 仅 spawned http/sse
// （网关自持子进程）时非 nil；纯 stdio spawned 的子进程由官方 SDK CommandTransport 内部管理。
// 连接池（2026-09-16）：isolate=true → **每个 (instance, workdir) 一槽**（各自管道/子进程，互不阻塞）；
// isolate=false → 全 server 仅共享槽一条（现状）。
// 2026-09-19（实例隔离第一批，缺口 1）：隔离键由 `workdir` 扩为 **`(instance_id, workdir)`** ——
// 同 workdir 的不同 instance 各自建连，取消/打断互不影响（见 poolKeyFor）。
type connSlot struct {
	workdir  string // 归属 workdir（"" = 共享槽：isolate=false / workdir 缺失回落 / 控制面 ListTools）
	instance string // 归属 instance（"" = 无 instance 维度；仅隔离槽非空）
	conn     providerConn
	cmd      *exec.Cmd
	done     chan error
	closing  atomic.Bool  // 主动关闭/重建中 → 退出观测静默
	lastUsed atomic.Int64 // 最近使用时刻（unix nano；空闲回收判据）
	inFlight atomic.Int32 // 在飞调用数（>0 不回收）
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

func (s *connSlot) touch() { s.lastUsed.Store(time.Now().UnixNano()) }

// idleFor 返回该槽距最近一次调用的空闲时长。
func (s *connSlot) idleFor(now time.Time) time.Duration {
	return now.Sub(time.Unix(0, s.lastUsed.Load()))
}

// connect 建立该槽连接：spawned http/sse 先等 url 就绪，再 Connect（含 initialize 握手）。
func (s *connSlot) connect(ctx context.Context, endpoint string) error {
	if s.cmd != nil {
		if err := waitEndpointReady(ctx, endpoint, s.cmd); err != nil {
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
	if s.conn != nil && (s.cmd == nil || s.cmd.ProcessState == nil) {
		_ = s.conn.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	return nil
}

// proxyProvider 是 spawned（runtime 拉起）或 proxied（url 连接）下游的代理。
// 共享槽（p.shared）承担控制面（首连/ListTools/无 workdir 回落）；isolate=true 时调用面
// 按调用上下文的 (instance_id, work_dir)（withTurnContext → ctxKeyInstance/ctxKeyWorkDir）
// 走 p.pool 的专属槽。
// mu 保护池的增删（建连在各自 buildMu 锁外进行，不同池键互不阻塞）。
type proxyProvider struct {
	name     string
	entry    *ServerEntry // 进程规格（Invalidate respawn / 池内新槽建连复用）
	kind     string       // spawned / proxied
	endpoint string       // http/sse 连接点（url，就绪轮询用）
	inject   bool         // 来源=builtin → 调用携带 _meta 内部上下文；user（第三方）→ 不携带
	isolate  bool         // 按 (instance, workdir) 隔离（ServerEntry.IsolateEnabled()）
	idleTTL  time.Duration
	logf     func(string, ...any)

	mu      sync.Mutex
	shared  atomic.Pointer[connSlot] // 共享槽（isolate=false 的唯一连接；always 建；Invalidate 原地换新）
	pool    map[string]*connSlot     // poolKeyFor(instance, workdir) → 专属槽（isolate=true）
	buildMu map[string]*sync.Mutex   // 每池键建连锁（同键串行，不同键互不阻塞）
	noWd    atomic.Bool              // 「缺 workdir 回落共享」告警只记一次

	closing  atomic.Bool // 网关主动关闭/重建中（Close/Invalidate）→ 退出观测静默
	stopCh   chan struct{}
	stopOnce sync.Once
}

// newProxyProvider 建立下游 MCP client（stdio / http / sse）。
// transport 缺省推断（对齐 26-mcp-gateway）：url 存在 → http（runtime+url = spawn 后连 url）；
// 仅 runtime → stdio。
// callTimeout 复用为**池空闲回收 TTL**（既有项，不新增配置键）；<=0 时按全局默认 60s。
func newProxyProvider(entry *ServerEntry, callTimeout time.Duration, logf func(string, ...any)) (*proxyProvider, error) {
	shared, err := buildSlot(entry, "", "")
	if err != nil {
		return nil, err
	}
	kind := "proxied"
	if entry.Runtime != "" {
		kind = "spawned"
	}
	ttl := callTimeout
	if ttl <= 0 {
		ttl = 60 * time.Second // 与 Params.CallTimeout 默认一致
	}
	p := &proxyProvider{name: entry.ID, entry: entry, kind: kind, endpoint: entry.URL,
		inject: entry.IsBuiltin(), isolate: entry.IsolateEnabled(), idleTTL: ttl, logf: logf,
		pool: map[string]*connSlot{}, buildMu: map[string]*sync.Mutex{},
		stopCh: make(chan struct{})}
	p.shared.Store(shared)
	if p.isolate {
		go p.sweepIdle() // 空闲池键连接回收（无活跃调用超时 → 关闭并移出池）
	}
	return p, nil
}

// buildSlot 按 entry 规格构建一个连接槽（含 spawned http/sse 的自持子进程）。
// 供首建共享槽、池内按 (instance, workdir) 懒建复用。
func buildSlot(entry *ServerEntry, workdir, instance string) (*connSlot, error) {
	conn, cmd, done, err := buildConn(entry)
	if err != nil {
		return nil, err
	}
	s := &connSlot{workdir: workdir, instance: instance, conn: conn, cmd: cmd, done: done}
	s.touch()
	return s, nil
}

// buildConn 按 entry 规格构建一个下游连接（及 spawned http/sse 的自持子进程）。
func buildConn(entry *ServerEntry) (providerConn, *exec.Cmd, chan error, error) {
	transportName := entry.TransportName()
	switch transportName {
	case "stdio", "http", "sse":
	default:
		return nil, nil, nil, fmt.Errorf("unknown transport %q", transportName)
	}
	// 显式 http/sse 必须有 url（runtime+url = spawn 后就绪再连；纯 url = proxied）。
	// 缺 url 时旧实现会先 spawn 再连空端点（必然失败）→ 此处前置明确报错，不 spawn。
	if transportName != "stdio" && strings.TrimSpace(entry.URL) == "" {
		return nil, nil, nil, fmt.Errorf("transport %q needs url（server %q）", transportName, entry.ID)
	}

	// spawned http/sse：先拉起子进程（connect 时再轮询 url 就绪）
	var cmd *exec.Cmd
	var done chan error
	needsSpawn := entry.Runtime != "" && (transportName == "http" || transportName == "sse")
	if needsSpawn {
		c, d, err := startProcess(entry)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("spawn %q: %w", entry.ID, err)
		}
		cmd, done = c, d
	}

	var conn providerConn
	switch transportName {
	case "stdio":
		argv, err := spawnArgv(entry)
		if err != nil {
			return nil, nil, nil, err
		}
		proc := exec.Command(argv[0], argv[1:]...)
		proc.SysProcAttr = winproc.SysProcAttr()
		if entry.Cwd != "" {
			proc.Dir = entry.Cwd // stdio 子进程工作目录（与 startProcess 同语义）
		}
		proc.Env = processEnv(entry)
		// agentbox 沙箱（决策 42 §2 (104)/(109)）：**仅 stdio** 的 spawn 下发策略
		// （http/sse 不施加，见 14-安全域-agentbox）；未开开关 / 无允许目录 → 不下发（默认兼容）。
		if policy := entry.SandboxPolicyJSON(); policy != "" {
			// processEnv 无 env 时返回 nil（子进程继承父环境）；此时若直接 append，Env 变非 nil
			// 且只剩沙箱一条 → 子进程丢 PATH 等（exec 见非 nil Env 即不再继承）。故先补基线。
			if proc.Env == nil {
				proc.Env = os.Environ()
			}
			proc.Env = append(proc.Env, agentbox.EnvSandbox+"="+policy)
			log.Printf("[gateway] agentbox 隔离下发: server=%s dirs=%s", entry.ID, truncateStr(policy, 500))
		} else if entry.Sandbox != nil && *entry.Sandbox {
			log.Printf("[gateway] agentbox: server=%s 已开启隔离但允许目录为空（未下发策略）", entry.ID)
		}
		cli := mcp.NewClient(&mcp.Implementation{Name: "chonkpilot-mcp-gateway", Version: "0.1.0"}, nil)
		conn = &sdkConn{cli: cli, tr: &mcp.CommandTransport{Command: proc}}
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
		cli := mcp.NewClient(&mcp.Implementation{Name: "chonkpilot-mcp-gateway", Version: "0.1.0"}, nil)
		if transportName == "sse" {
			conn = &sdkConn{cli: cli, tr: &mcp.SSEClientTransport{Endpoint: entry.URL, HTTPClient: httpClient}}
		} else {
			conn = &sdkConn{cli: cli, tr: &mcp.StreamableClientTransport{Endpoint: entry.URL, HTTPClient: httpClient}}
		}
	}
	return conn, cmd, done, nil
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

// startProcess 按 entry.runtime/args 拉起子进程（env/cwd 生效），返回进程与 Wait 结果通道。
func startProcess(e *ServerEntry) (*exec.Cmd, chan error, error) {
	argv, err := spawnArgv(e)
	if err != nil {
		return nil, nil, err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = winproc.SysProcAttr()
	if e.Cwd != "" {
		cmd.Dir = e.Cwd
	}
	cmd.Env = processEnv(e)
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return cmd, done, nil
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
// 或 ctx 超时 / 子进程提前退出。
func waitEndpointReady(ctx context.Context, rawURL string, cmd *exec.Cmd) error {
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
		if cmd != nil && cmd.ProcessState != nil {
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
// 建连（含 spawn + 握手，可数秒）在 per-池键 锁内进行，不进 pool 全局锁——不同键并发调用
// 各自独立建连，不被彼此的建连阻塞。专属槽建连/握手失败 → warn 并回落共享槽（保证调用可用性，
// 如 spawned http/sse 因固定端口无法按 instance/workdir 重复拉起时退化为共享）。
func (p *proxyProvider) slotFor(ctx context.Context) (*connSlot, func()) {
	never := func() {}
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
	if s := p.acquirePooled(key); s != nil {
		return s, s.releaseFn()
	}

	lk := p.buildLock(key)
	lk.Lock() // 同池键建连锁：并发首调只建一条
	defer lk.Unlock()
	if s := p.acquirePooled(key); s != nil { // 双检（等锁期间可能已由他人建好）
		return s, s.releaseFn()
	}

	slot, err := buildSlot(p.entry, wd, inst)
	if err == nil {
		err = slot.connect(ctx, p.endpoint)
	}
	if err != nil {
		if slot != nil {
			_ = slot.Close()
		}
		p.warn("[gateway] %s: build isolated conn for instance %s work_dir %s failed (%v) → fallback to shared conn", p.name, inst, wd, err)
		return p.shared.Load(), never
	}
	slot.inFlight.Add(1) // 先占位（在飞），再入池
	p.mu.Lock()
	p.pool[key] = slot
	p.mu.Unlock()
	if slot.cmd != nil { // spawned http/sse：池化自持子进程同样挂退出观测
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

// buildLock 返回该池键的建连锁（不存在则新建；**不随回收删除**——删除会让同池键
// 出现两把锁并发建连）。
func (p *proxyProvider) buildLock(key string) *sync.Mutex {
	p.mu.Lock()
	defer p.mu.Unlock()
	lk, ok := p.buildMu[key]
	if !ok {
		lk = &sync.Mutex{}
		p.buildMu[key] = lk
	}
	return lk
}

// watchPooled 观测池化自持子进程（spawned http/sse）：意外退出 → 从池中移除（下次调用懒建）；
// provider/槽主动关闭（closing）→ 静默返回。
func (p *proxyProvider) watchPooled(key string, s *connSlot) {
	err := <-s.done
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
func (p *proxyProvider) SpawnedSelf() bool { return p.shared.Load().cmd != nil }

// WaitExit 返回共享槽子进程退出结果（仅自持子进程；缓冲通道，可多次/提前接收）。
// 重建（Invalidate）后返回新进程的退出通道。
func (p *proxyProvider) WaitExit() <-chan error {
	done := p.shared.Load().done
	if done == nil {
		ch := make(chan error, 1)
		ch <- nil
		return ch
	}
	return done
}

// ListTools 走共享槽（工具面为 server 级、与 workdir 无关）。
func (p *proxyProvider) ListTools(ctx context.Context) ([]*mcp.Tool, error) {
	conn := p.shared.Load().conn
	if conn == nil {
		return nil, fmt.Errorf("no transport connection")
	}
	return conn.ListTools(ctx)
}

func (p *proxyProvider) Call(ctx context.Context, tool string, args map[string]any) (*mcp.CallToolResult, error) {
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
	return slot.conn.Call(ctx, tool, args, meta)
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
	p.closing.Store(true) // 作废/重建期间退出观测静默
	p.closePooled()
	_ = p.shared.Load().Close()
	// 失败路径统一复位：关掉可能半初始化的新槽 → shared 清为空槽（conn=nil，后续调用明确
	// 报 no transport connection，而非复用已关闭/半初始化的连接）→ closing 归位（避免长期静默）。
	fail := func(err error) (bool, error) {
		_ = p.shared.Load().Close()
		p.shared.Store(&connSlot{})
		p.closing.Store(false)
		return false, err
	}
	conn, cmd, done, err := buildConn(p.entry)
	if err != nil {
		return fail(fmt.Errorf("respawn %q: %w", p.name, err))
	}
	slot := &connSlot{conn: conn, cmd: cmd, done: done}
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

// Terminate 是取消的**执行线入口**（`onTaskCancel` 调用）：spawned / 纯远程代理统一按
// (instance, workdir) 作用域作废 —— 命中隔离槽 → 仅 kill 该槽子进程/断连（下次调用懒建）；
// 否则（共享槽 / 无隔离维度）→ 全量 kill + respawn。纯远程（proxied，无自持 cmd）退化为
// **仅断请求**（对端进程可能继续运行 = 语义「尽力」，见 18 §3.4）。
func (p *proxyProvider) Terminate(ctx context.Context, instanceID, workdir string) (bool, error) {
	return p.InvalidateScoped(ctx, instanceID, workdir)
}

// Close 释放资源：停空闲回收 → 关闭池中全部连接槽 → 共享槽净断（先于 kill，避免断开噪音）。
func (p *proxyProvider) Close() error {
	p.closing.Store(true)
	p.stopOnce.Do(func() { close(p.stopCh) })
	p.closePooled()
	return p.shared.Load().Close()
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "..."
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
)

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

func strFromCtx(ctx context.Context, k ctxKey) string {
	if v := ctx.Value(k); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
