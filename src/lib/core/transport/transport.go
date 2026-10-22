// Package transport 是与业务无关的进程间传输层：只搬帧，不管帧里装什么。
//
// 设计见 docs/bridge-plugin/插件桥接端口设计方案.md §3：
//   - 帧 = 行分隔 JSON（与既有 uds 同一套形态）；
//   - 四种帧：req / res / evt / ctl；
//   - 后端 uds（默认）| tcp；
//   - 本包不出现主题名 / 实例 / 方法名 / 能力名 / 61 概念。
package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Backend 是传输后端。
type Backend string

const (
	BackendUDS Backend = "uds"
	BackendTCP Backend = "tcp"
)

// Kind 是帧类型。
type Kind string

const (
	KindReq Kind = "req" // 请求：{name,payload} → 期待同 id 的 res
	KindRes Kind = "res" // 应答：{id,ok,result,errors}
	KindEvt Kind = "evt" // 事件：{name,payload}（服务端 → 对端，单向）
	KindCtl Kind = "ctl" // 控制：{name,payload}（由宿主处理）
)

// Frame 是通用帧；transport 不解释 Name / Payload 的业务含义。
type Frame struct {
	Kind    Kind            `json:"k"`
	ID      int64           `json:"id,omitempty"`
	Name    string          `json:"name,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	OK      bool            `json:"ok,omitempty"`
	Result  any             `json:"result,omitempty"`
	Errors  []string        `json:"errors,omitempty"`
}

// Result 是一次调用结果。
type Result struct {
	OK     bool
	Value  any
	Errors []string
}

// Principal 是认证后的不透明身份；nil 语义 = 未认证。
type Principal struct {
	ID   string
	Data map[string]any
}

// NilPrincipal 是未认证身份。
var NilPrincipal = Principal{}

// Authenticator 把底层连接换成不透明身份；nil = 不认证。
type Authenticator func(nc net.Conn) (Principal, error)

// Options 是传输层构造参数。
type Options struct {
	Backend       Backend
	Addr          string // uds: socket 路径；tcp: host:port
	Authenticator Authenticator
	MaxFrameBytes int // 缺省 64 MiB
	MaxConns      int // 服务端缺省 64
	// InboundQueue 是单连接入站队列长度（慢消费者丢帧）；缺省 256。
	InboundQueue int
	Logf         func(format string, args ...any)
}

// DefaultMaxFrameBytes / DefaultMaxConns 对齐既有 uds。
const (
	DefaultMaxFrameBytes = 64 << 20
	DefaultMaxConns      = 64
	DefaultInboundQueue  = 256
)

// ErrClosed 是连接已关闭。
var ErrClosed = errors.New("transport: closed")

// ErrFrameTooLarge 是单帧超过上限。
var ErrFrameTooLarge = errors.New("transport: frame too large")

func (o *Options) normalize() {
	if o.Backend == "" {
		o.Backend = BackendUDS
	}
	if o.MaxFrameBytes <= 0 {
		o.MaxFrameBytes = DefaultMaxFrameBytes
	}
	if o.MaxConns <= 0 {
		o.MaxConns = DefaultMaxConns
	}
	if o.InboundQueue <= 0 {
		o.InboundQueue = DefaultInboundQueue
	}
}

func (o *Options) logf(format string, args ...any) {
	if o.Logf != nil {
		o.Logf(format, args...)
	}
}

// Conn 是一条双向连接。
type Conn interface {
	Send(f Frame) error
	Recv() (Frame, error)
	Call(ctx context.Context, name string, payload any) (Result, error)
	Principal() Principal
	Remote() string
	Closed() <-chan struct{}
	Close() error
}

// Endpoint 是监听点。
type Endpoint interface {
	Addr() string
	Accept() (Conn, error)
	Close() error
}

// Dialer 是连接方。
type Dialer interface {
	Dial(ctx context.Context) (Conn, error)
}

// Listen 建立监听点。
func Listen(o Options) (Endpoint, error) {
	o.normalize()
	switch o.Backend {
	case BackendUDS, BackendTCP:
	default:
		return nil, fmt.Errorf("transport: unknown backend %q", o.Backend)
	}
	network := "unix"
	addr := o.Addr
	if o.Backend == BackendTCP {
		network = "tcp"
	}
	if network == "unix" {
		// 单例保护：已有活实例监听 → 拒绝启动；陈旧 socket 才清理重绑。
		if c, derr := net.Dial("unix", addr); derr == nil {
			_ = c.Close()
			return nil, fmt.Errorf("transport: %s 已有实例在监听", addr)
		}
		_ = os.Remove(addr)
		if dir := filepath.Dir(addr); dir != "" && dir != "." {
			_ = os.MkdirAll(dir, 0o700)
		}
	}
	ln, err := net.Listen(network, addr)
	if err != nil {
		return nil, fmt.Errorf("transport: listen %s %s: %w", network, addr, err)
	}
	ep := &endpoint{opts: o, ln: ln, acceptQ: make(chan Conn, o.MaxConns), closed: make(chan struct{})}
	go ep.acceptLoop()
	return ep, nil
}

// Dial 连接一个监听点。
func Dial(ctx context.Context, o Options) (Conn, error) {
	o.normalize()
	network := "unix"
	addr := o.Addr
	if o.Backend == BackendTCP {
		network = "tcp"
	}
	d := net.Dialer{}
	nc, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	c := newConn(o, nc, NilPrincipal)
	c.start()
	return c, nil
}

// ── Endpoint ──

type endpoint struct {
	opts    Options
	ln      net.Listener
	acceptQ chan Conn
	closed  chan struct{}
	once    sync.Once

	mu    sync.Mutex
	conns map[*conn]struct{}
}

func (e *endpoint) Addr() string { return e.ln.Addr().String() }

func (e *endpoint) Accept() (Conn, error) {
	select {
	case c := <-e.acceptQ:
		return c, nil
	case <-e.closed:
		return nil, ErrClosed
	}
}

func (e *endpoint) Close() error {
	e.once.Do(func() {
		close(e.closed)
		_ = e.ln.Close()
		e.mu.Lock()
		cs := make([]*conn, 0, len(e.conns))
		for c := range e.conns {
			cs = append(cs, c)
		}
		e.mu.Unlock()
		for _, c := range cs {
			_ = c.Close()
		}
	})
	return nil
}

func (e *endpoint) acceptLoop() {
	for {
		nc, err := e.ln.Accept()
		if err != nil {
			select {
			case <-e.closed:
				return
			default:
				e.opts.logf("transport: accept: %v", err)
				return
			}
		}
		if e.count() >= e.opts.MaxConns {
			e.opts.logf("transport: 连接数达上限 %d，拒绝新连接", e.opts.MaxConns)
			_ = nc.Close()
			continue
		}
		p := NilPrincipal
		if e.opts.Authenticator != nil {
			pp, aerr := e.opts.Authenticator(nc)
			if aerr != nil {
				e.opts.logf("transport: auth: %v", aerr)
				_ = nc.Close()
				continue
			}
			p = pp
		}
		c := newConn(e.opts, nc, p)
		e.mu.Lock()
		if e.conns == nil {
			e.conns = make(map[*conn]struct{})
		}
		e.conns[c] = struct{}{}
		e.mu.Unlock()
		c.onClose = func() {
			e.mu.Lock()
			delete(e.conns, c)
			e.mu.Unlock()
		}
		c.start()
		select {
		case e.acceptQ <- c:
		case <-e.closed:
			_ = c.Close()
			return
		}
	}
}

func (e *endpoint) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.conns)
}

// ── Conn ──

type conn struct {
	opts Options
	nc   net.Conn
	prin Principal
	rem  string

	writeMu sync.Mutex
	pendMu  sync.Mutex
	pend    map[int64]chan Frame
	nextID  int64

	inQ       chan Frame
	closed    chan struct{}
	closeOnce sync.Once
	onClose   func()
}

func newConn(o Options, nc net.Conn, p Principal) *conn {
	c := &conn{
		opts:   o,
		nc:     nc,
		prin:   p,
		rem:    nc.RemoteAddr().String(),
		pend:   make(map[int64]chan Frame),
		inQ:    make(chan Frame, o.InboundQueue),
		closed: make(chan struct{}),
	}
	return c
}

func (c *conn) start() { go c.readLoop() }

func (c *conn) Principal() Principal { return c.prin }
func (c *conn) Remote() string       { return c.rem }
func (c *conn) Closed() <-chan struct{} { return c.closed }

func (c *conn) Send(f Frame) error {
	return c.write(f)
}

func (c *conn) Recv() (Frame, error) {
	select {
	case f := <-c.inQ:
		return f, nil
	case <-c.closed:
		return Frame{}, ErrClosed
	}
}

func (c *conn) Call(ctx context.Context, name string, payload any) (Result, error) {
	raw, err := rawPayload(payload)
	if err != nil {
		return Result{}, err
	}
	c.pendMu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan Frame, 1)
	c.pend[id] = ch
	c.pendMu.Unlock()
	if err := c.write(Frame{Kind: KindReq, ID: id, Name: name, Payload: raw}); err != nil {
		c.dropPending(id)
		return Result{}, err
	}
	select {
	case f := <-ch:
		return Result{OK: f.OK, Value: f.Result, Errors: f.Errors}, nil
	case <-ctx.Done():
		c.dropPending(id)
		return Result{}, ctx.Err()
	case <-c.closed:
		c.dropPending(id)
		return Result{}, ErrClosed
	}
}

func (c *conn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		_ = c.nc.Close()
		c.pendMu.Lock()
		c.pend = make(map[int64]chan Frame)
		c.pendMu.Unlock()
		if c.onClose != nil {
			c.onClose()
		}
	})
	return nil
}

func (c *conn) write(f Frame) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.closed:
		return ErrClosed
	default:
	}
	_, err = c.nc.Write(b)
	return err
}

func (c *conn) readLoop() {
	r := bufio.NewReaderSize(c.nc, 64<<10)
	for {
		line, err := readFrame(r, c.opts.MaxFrameBytes)
		if len(line) > 0 {
			c.handleLine(line)
		}
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				c.opts.logf("transport: read: %v", err)
			}
			break
		}
	}
	_ = c.Close()
}

func (c *conn) handleLine(line []byte) {
	var f Frame
	if json.Unmarshal(line, &f) != nil {
		return
	}
	if f.Kind == KindRes {
		c.pendMu.Lock()
		ch := c.pend[f.ID]
		delete(c.pend, f.ID)
		c.pendMu.Unlock()
		if ch != nil {
			ch <- f
		}
		return
	}
	select {
	case c.inQ <- f:
	default:
		// 慢消费者：丢帧而不是阻塞读循环（对齐 uds / httpapi 口径）。
		c.opts.logf("transport: inbound queue full，丢弃帧 %s/%s", f.Kind, f.Name)
	}
}

func (c *conn) dropPending(id int64) {
	c.pendMu.Lock()
	delete(c.pend, id)
	c.pendMu.Unlock()
}

func rawPayload(v any) (json.RawMessage, error) {
	switch p := v.(type) {
	case nil:
		return nil, nil
	case json.RawMessage:
		return p, nil
	case []byte:
		return json.RawMessage(p), nil
	case string:
		return json.RawMessage(p), nil
	default:
		b, err := json.Marshal(p)
		if err != nil {
			return nil, err
		}
		return json.RawMessage(b), nil
	}
}

func readFrame(r *bufio.Reader, max int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if err == bufio.ErrBufferFull {
			if len(buf) > max {
				return nil, ErrFrameTooLarge
			}
			continue
		}
		if len(buf) > max {
			return nil, ErrFrameTooLarge
		}
		return buf, err
	}
}

// CallTimeout 是 Call 的默认超时（与 uds 60s 对齐）。
const CallTimeout = 60 * time.Second
