package uds

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// ErrClosed 是客户端已关闭（或对端断开）后操作的返回错误。
var ErrClosed = errors.New("uds: client closed")

// DefaultPrefix 是客户端总线命名空间前缀（与服务端总线一致；业务侧一律写相对主题）。
const DefaultPrefix = "chonk."

// ClientOptions 是客户端构造参数。
type ClientOptions struct {
	// Path 是服务端 UDS 路径；空 = DefaultPath()。
	Path string
	// Prefix 是总线前缀；空 = DefaultPrefix（"chonk."）。
	Prefix string
	// OnControl 接收**定向控制帧**（如 `instance-goto-top`；消费方 = flash 宿主，不下发前端）。
	OnControl func(typ string, data []byte)
	// OnClose 在连接关闭时回调**恰好一次**（对端断开 / 本端 Close）——承载 flash
	// 「连接断开 = 实例退出」语义（see 19 §8.11）。
	OnClose func()
	// CallTimeout 是单次上行等待应答的上限；0 = 60s。
	CallTimeout time.Duration
	// Logf 诊断输出；nil = 不输出。
	Logf func(format string, args ...any)
}

// Client 是 flash 侧的 UDS 客户端，实现 `mq.Bus`：
//   - Emit / Publish：上行请求-响应（Emit 阻塞至应答，填充 Value.Result/Errors）；
//   - On / Subscribe：本地订阅表（服务端下行事件按 subject 本地派发）；
//   - 定向控制帧（ctl）经 OnControl 回调，不进本地订阅表（不下发前端）。
//
// 断线即 Close（承载 flash「连接断开 = 实例退出」语义）。
type Client struct {
	opts   ClientOptions
	prefix string

	nc     net.Conn
	mu     sync.Mutex
	nextID int64
	pend   map[int64]chan responseFrame

	subMu     sync.RWMutex
	subs      []*clientSub
	nextSubID uint64

	// 事件串行消费队列：readLoop 只入队（不执行 handler），单个 worker goroutine 顺序
	// 执行订阅回调。避免「在唯一 readLoop goroutine 上同步执行 handler」时，handler 内
	// Emit/Publish（call 阻塞等待应答）与 readLoop 读取应答互等而自死锁。
	// 队列无界（slice + Cond）→ readLoop 入队永不阻塞，应答始终可被及时读取（背压不作用于
	// 读路径）；代价是极端积压时内存增长。
	evtMu     sync.Mutex
	evtQ      []eventFrame
	evtCond   *sync.Cond
	evtClosed bool

	closed    chan struct{}
	closeOnce sync.Once
}

// Dial 连接服务端 UDS 前门。
func Dial(opts ClientOptions) (*Client, error) {
	p := opts.Path
	if p == "" {
		p = DefaultPath()
	}
	nc, err := net.Dial("unix", p)
	if err != nil {
		return nil, err
	}
	prefix := opts.Prefix
	if prefix == "" {
		prefix = DefaultPrefix
	}
	c := &Client{
		opts:   opts,
		prefix: prefix,
		nc:     nc,
		pend:   make(map[int64]chan responseFrame),
		closed: make(chan struct{}),
	}
	c.evtCond = sync.NewCond(&c.evtMu)
	go c.dispatchLoop()
	go c.readLoop()
	return c, nil
}

// Prefix 返回总线前缀（mq.Bus 接口）。
func (c *Client) Prefix() string { return c.prefix }

// Emit 上行一条消息并**等待应答**（阻塞），填充 Value.Result/Errors 后返回已完成的 Future。
func (c *Client) Emit(ctx context.Context, subject string, payload any) *mq.Future {
	raw := payloadBytes(payload)
	v := &mq.Value{Subject: subject, Payload: raw}
	resp, err := c.call(ctx, subject, raw)
	if err != nil {
		v.Errors = append(v.Errors, err)
	} else {
		v.Result = resp.Result
		for _, e := range resp.Errors {
			v.Errors = append(v.Errors, errors.New(e))
		}
	}
	f := mq.NewFuture(v)
	f.Resolve()
	return f
}

// Publish 是上行发布（**阻塞至应答或 CallTimeout**，与 Emit 同等待遇）——返回应答/超时错误。
// 注：与 `mq.Bus.Publish` 的「fire-and-forget」文档语义不同，本客户端无本地派发、必须等对端
// 应答方可判定成败；调用方按错误返回处理即可（R7-B-7：注释与实现一致）。
func (c *Client) Publish(subject string, payload []byte) error {
	_, err := c.call(context.Background(), subject, json.RawMessage(payload))
	return err
}

// Subscribe 是 v1 兼容订阅（不参与结果写回）。
func (c *Client) Subscribe(subject string, h mq.Handler) (mq.Sub, error) {
	return c.On(subject, 0, func(_ context.Context, s string, v *mq.Value) error {
		h(s, v.Payload)
		return nil
	})
}

// On 注册本地订阅（服务端下行事件按 subject 本地派发；order 升序 + 注册序稳定）。
func (c *Client) On(subject string, order int, h mq.VHandler) (mq.Sub, error) {
	if h == nil {
		return nil, errors.New("uds: nil handler")
	}
	c.subMu.Lock()
	c.nextSubID++
	sub := &clientSub{cli: c, id: c.nextSubID, pattern: subject, order: order, h: h}
	c.subs = append(c.subs, sub)
	c.subMu.Unlock()
	return sub, nil
}

// Close 关闭连接（幂等；触发 readLoop 退出与所有等待中的上行失败）。
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		_ = c.nc.Close()
		c.clearPending()
		// 唤醒并按「不再投递」收尾事件 worker：置 evtClosed + 清空待派队列，再 Broadcast
		//（在锁内置位，保证与 worker 的 Cond.Wait 不丢唤醒、不重复投递）。
		c.evtMu.Lock()
		c.evtClosed = true
		c.evtQ = nil
		c.evtMu.Unlock()
		c.evtCond.Broadcast()
		if c.opts.OnClose != nil {
			c.opts.OnClose()
		}
	})
	return nil
}

// call 发送一次请求并等待应答（含 CallTimeout 兜底；断线 / 关闭 → 立即失败）。
func (c *Client) call(ctx context.Context, typ string, payload json.RawMessage) (responseFrame, error) {
	timeout := c.opts.CallTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	c.mu.Lock()
	select {
	case <-c.closed:
		c.mu.Unlock()
		return responseFrame{}, ErrClosed
	default:
	}
	c.nextID++
	id := c.nextID
	ch := make(chan responseFrame, 1)
	c.pend[id] = ch
	c.mu.Unlock()

	if err := c.writeFrame(requestFrame{K: frameRequest, ID: id, Type: typ, Payload: payload}); err != nil {
		c.dropPending(id)
		return responseFrame{}, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		c.dropPending(id)
		return responseFrame{}, ctx.Err()
	case <-c.closed:
		c.dropPending(id)
		return responseFrame{}, ErrClosed
	case <-timer.C:
		c.dropPending(id)
		return responseFrame{}, errors.New("uds: call timeout: " + typ)
	}
}

// writeFrame 写一帧（单写者：publish 与 call 均经此，网络写由 net.Conn 保证并发安全）。
func (c *Client) writeFrame(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	select {
	case <-c.closed:
		return ErrClosed
	default:
	}
	_, err = c.nc.Write(b)
	return err
}

// dropPending 移除等待表项（已投递的应答由 GC 回收）。
func (c *Client) dropPending(id int64) {
	c.mu.Lock()
	delete(c.pend, id)
	c.mu.Unlock()
}

// clearPending 清空等待表。断开 / 关闭时调用 —— 等待中的 call 由 `<-c.closed` 分支解除阻塞
// （**不向 ch 投递**，避免与 closed 分支竞态而误判为"应答成功"）。
func (c *Client) clearPending() {
	c.mu.Lock()
	c.pend = make(map[int64]chan responseFrame)
	c.mu.Unlock()
}

// readLoop 读下行帧：应答（res）→ 等待表；事件（evt）→ 本地订阅；控制（ctl）→ OnControl。
func (c *Client) readLoop() {
	r := bufio.NewReaderSize(c.nc, 64<<10)
	for {
		line, err := readFrame(r)
		if len(line) > 0 {
			c.handleLine(line)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && c.opts.Logf != nil {
				c.opts.Logf("uds: client read: %v", err)
			}
			break
		}
	}
	c.Close()
}

// handleLine 解析一行帧并分派。
func (c *Client) handleLine(line []byte) {
	var probe struct {
		K string `json:"k"`
	}
	if json.Unmarshal(line, &probe) != nil {
		return
	}
	switch probe.K {
	case frameResponse:
		var f responseFrame
		if json.Unmarshal(line, &f) != nil {
			return
		}
		c.mu.Lock()
		ch := c.pend[f.ID]
		delete(c.pend, f.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- f
		}
	case frameEvent:
		var f eventFrame
		if json.Unmarshal(line, &f) != nil {
			return
		}
		// 只入队，不在 readLoop goroutine 上执行 handler（见 evtQ 字段注释）。
		c.enqueueEvent(f)
	case frameControl:
		var f controlFrame
		if json.Unmarshal(line, &f) != nil {
			return
		}
		if c.opts.OnControl != nil {
			c.opts.OnControl(f.Type, f.Data)
		}
	}
}

// enqueueEvent 把一条下行事件追加到串行队列（无界，不阻塞 readLoop）。已关闭 → 丢弃。
func (c *Client) enqueueEvent(f eventFrame) {
	c.evtMu.Lock()
	if c.evtClosed {
		c.evtMu.Unlock()
		return
	}
	c.evtQ = append(c.evtQ, f)
	c.evtMu.Unlock()
	c.evtCond.Signal()
}

// dispatchLoop 是**唯一**的事件消费 goroutine：顺序取出队列中的事件并派发（保序，与旧
// 「在 readLoop 上顺序派发」一致），使 readLoop 不再被 handler 阻塞（handler 内的上行应答
// 可被及时读取）。Close 后退出、不再投递。handler 内 panic 在 invokeHandler 内 recover。
func (c *Client) dispatchLoop() {
	for {
		c.evtMu.Lock()
		for !c.evtClosed && len(c.evtQ) == 0 {
			c.evtCond.Wait()
		}
		if c.evtClosed {
			c.evtMu.Unlock()
			return
		}
		f := c.evtQ[0]
		c.evtQ = c.evtQ[1:]
		c.evtMu.Unlock()
		c.dispatch(f.Type, f.Payload)
	}
}

// dispatch 把一条事件按 subject 派发到本地订阅者（order 升序 + 注册序稳定）。
func (c *Client) dispatch(subject string, payload json.RawMessage) {
	c.subMu.RLock()
	hit := make([]*clientSub, 0, 4)
	for _, s := range c.subs {
		if matchSubject(s.pattern, subject) {
			hit = append(hit, s)
		}
	}
	c.subMu.RUnlock()
	sort.SliceStable(hit, func(i, j int) bool {
		if hit[i].order != hit[j].order {
			return hit[i].order < hit[j].order
		}
		return hit[i].id < hit[j].id
	})
	for _, s := range hit {
		v := &mq.Value{Subject: subject, Payload: payload}
		c.invokeHandler(s, subject, v)
	}
}

// invokeHandler 执行单个订阅回调并 recover（单个 handler panic 不得终止消费 worker / 跳过
// 其余订阅者）。
func (c *Client) invokeHandler(s *clientSub, subject string, v *mq.Value) {
	defer func() {
		if r := recover(); r != nil && c.opts.Logf != nil {
			c.opts.Logf("uds: client handler panic on %q: %v", subject, r)
		}
	}()
	_ = s.h(context.Background(), subject, v)
}

// clientSub 是本地订阅句柄。
type clientSub struct {
	cli     *Client
	id      uint64
	pattern string
	order   int
	h       mq.VHandler
}

// Unsubscribe 注销本地订阅（幂等）。
func (s *clientSub) Unsubscribe() error {
	s.cli.subMu.Lock()
	defer s.cli.subMu.Unlock()
	kept := s.cli.subs[:0]
	for _, x := range s.cli.subs {
		if x != s {
			kept = append(kept, x)
		}
	}
	s.cli.subs = kept
	return nil
}
