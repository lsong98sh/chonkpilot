// L6 池化 / L7 取消阶梯 / L8 通知路由 / L4 协议 v1 的单元测试（2026-10-21）：
//   - 池配置解析与缺省值（纯函数）；
//   - pool_key=call：每次调用独占槽、结束即销毁（真实 stdio helper 子进程）；
//   - max_concurrent 排队超时 → error.kind=pool_timeout；
//   - CancelCall 阶梯：L0/L1 优雅结束、L2 关连接、L3 后 cascade-report（slot_killed）；
//   - progressToken=call_id 的进度路由（命中投递 / 未命中丢弃 + warn）与 elicitation 路由；
//   - structuredContent 透传（L4）。
package mcpgateway

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ─── 池配置解析（L6.1，纯函数）─────────────────────────

func TestServerEntryPoolConfigParseAndDefaults(t *testing.T) {
	data := []byte(strings.Join([]string{
		"mcp.alias=pooled",
		"pooled.runtime=node.exe",
		"pooled.pool_key=call",
		"pooled.pool_size=3",
		"pooled.max_concurrent=9",
		"pooled.grace_ms=500",
		"pooled.kill_policy=cascade-report",
		"",
		"mcp.alias=plain",
		"plain.url=http://127.0.0.1:1/mcp",
	}, "\n"))
	entries, err := ParseServersList(data)
	if err != nil {
		t.Fatalf("ParseServersList: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	e := entries[0]
	if got := e.PoolKeyMode(); got != PoolKeyCall {
		t.Fatalf("PoolKeyMode = %q, want %q", got, PoolKeyCall)
	}
	if got := e.PoolSizeOrDefault(); got != 3 {
		t.Fatalf("PoolSizeOrDefault = %d, want 3", got)
	}
	if got := e.MaxConcurrentOrDefault(); got != 9 {
		t.Fatalf("MaxConcurrentOrDefault = %d, want 9", got)
	}
	if got := e.GraceOrDefault(); got != 500*time.Millisecond {
		t.Fatalf("GraceOrDefault = %v, want 500ms", got)
	}
	if got := e.KillPolicyName(); got != KillPolicyCascadeReport {
		t.Fatalf("KillPolicyName = %q, want %q", got, KillPolicyCascadeReport)
	}

	// 缺省：shared / 1 / 5 / 2000ms / cascade-report
	d := entries[1]
	if got := d.PoolKeyMode(); got != PoolKeyShared {
		t.Fatalf("缺省 PoolKeyMode = %q, want %q", got, PoolKeyShared)
	}
	if d.PoolSizeOrDefault() != defaultPoolSize || d.MaxConcurrentOrDefault() != defaultMaxConcurrent {
		t.Fatalf("缺省 pool_size/max_concurrent = %d/%d, want %d/%d",
			d.PoolSizeOrDefault(), d.MaxConcurrentOrDefault(), defaultPoolSize, defaultMaxConcurrent)
	}
	if got := d.GraceOrDefault(); got != defaultGraceMs*time.Millisecond {
		t.Fatalf("缺省 GraceOrDefault = %v, want %dms", got, defaultGraceMs)
	}
	// 未知 pool_key → shared（安全默认）
	if got := (ServerEntry{PoolKey: "weird"}).PoolKeyMode(); got != PoolKeyShared {
		t.Fatalf("未知 pool_key 应归一 shared，got %q", got)
	}
}

// ─── pool_key=call：独占槽 + 结束即销毁（L6.3）──────────

func TestPoolKeyCallPerCallSlotDestroyedAfterCall(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("skip：测试二进制路径不可用（%v）", err)
	}
	p, err := newProxyProvider(&ServerEntry{
		ID: "callpool", Runtime: exe, Transport: "stdio", PoolKey: PoolKeyCall,
		Env: []string{"GATEWAY_STDIO_HELPER=1"},
	}, 30*time.Second, t.Logf, nil)
	if err != nil {
		t.Fatalf("newProxyProvider: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := p.connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	call := func(callID string) string {
		cctx, ccancel := context.WithTimeout(withCallID(context.Background(), callID), 60*time.Second)
		defer ccancel()
		res, err := p.Call(cctx, "pid", map[string]any{})
		if err != nil {
			t.Fatalf("call %s: %v", callID, err)
		}
		if len(res.Content) == 0 {
			t.Fatalf("call %s 无内容: %v", callID, res)
		}
		return strings.TrimSpace(res.Content[0].(*mcp.TextContent).Text)
	}
	pid1 := call("c-1")
	if n := poolSize(p); n != 0 {
		t.Fatalf("per-call 槽应在调用结束后销毁，pool=%d", n)
	}
	pid2 := call("c-1")
	if pid1 == pid2 {
		t.Fatalf("pool_key=call 应每次调用新建进程：%s == %s", pid1, pid2)
	}
	if n := poolSize(p); n != 0 {
		t.Fatalf("第二次调用后池应仍为空，pool=%d", n)
	}
}

// ─── max_concurrent 排队超时（L6.4）──────────────────────

func TestAcquireConcurrencyPoolTimeout(t *testing.T) {
	p := &proxyProvider{name: "conc", maxConcurrent: 1, queueTimeout: 30 * time.Millisecond}
	p.sem = make(chan struct{}, 1)
	p.sem <- struct{}{} // 占满配额

	_, err := p.Call(context.Background(), "t", nil)
	if got := ErrorKind(err); got != ErrKindPoolTimeout {
		t.Fatalf("排队超时 kind = %q, want %q（err=%v）", got, ErrKindPoolTimeout, err)
	}
	var ce *CallError
	if !errors.As(err, &ce) || !strings.Contains(ce.Error(), "1 concurrent") {
		t.Fatalf("pool_timeout 应带并发摘要：%v", err)
	}

	// 调用方 ctx 取消优先于排队超时
	cc, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Call(cc, "t", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("ctx 取消应直接返回 context.Canceled，got %v", err)
	}

	// 释放配额后可正常获取
	<-p.sem
	release, err := p.acquireConcurrency(context.Background())
	if err != nil {
		t.Fatalf("释放配额后 acquire: %v", err)
	}
	release()
}

// ─── 假连接（通知/取消阶梯用例）────────────────────────

// fakeNotifyConn 是可控的下游会话：记录收到的 progressToken；tool=block 时**不可中断**
// （忽略 ctx 取消，模拟强杀前仍在跑的下游调用）；其余工具随 ctx 取消返回。
type fakeNotifyConn struct {
	mu       sync.Mutex
	started  chan string
	block    chan struct{}
	tokens   []string
	closes   int
	toolSeen []string
}

func newFakeNotifyConn() *fakeNotifyConn {
	return &fakeNotifyConn{started: make(chan string, 8), block: make(chan struct{})}
}

func (c *fakeNotifyConn) connect(context.Context) error { return nil }

func (c *fakeNotifyConn) ListTools(context.Context) ([]*mcp.Tool, error) { return nil, nil }

func (c *fakeNotifyConn) Call(ctx context.Context, tool string, _ map[string]any, _ mcp.Meta, token string) (*mcp.CallToolResult, error) {
	c.mu.Lock()
	c.tokens = append(c.tokens, token)
	c.toolSeen = append(c.toolSeen, tool)
	c.mu.Unlock()
	select {
	case c.started <- tool:
	default:
	}
	ok := func() (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	}
	if tool == "block" {
		<-c.block // 不可中断：只能靠进程树终止
		return ok()
	}
	select {
	case <-c.block:
		return ok()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *fakeNotifyConn) Close() error {
	c.mu.Lock()
	c.closes++
	c.mu.Unlock()
	return nil
}

func (c *fakeNotifyConn) lastToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.tokens) == 0 {
		return ""
	}
	return c.tokens[len(c.tokens)-1]
}

// closesCount 返回 Close 调用次数（L2 断言用；加锁避免 -race）。
func (c *fakeNotifyConn) closesCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closes
}

// newFakeConnProvider 组装一个只连假连接的单 server provider（isolate=false → 全调用走共享槽）。
func newFakeConnProvider(conn providerConn, handlers NotifyHandlers, logf func(string, ...any)) *proxyProvider {
	p := &proxyProvider{
		name: "fake", kind: "proxied", entry: &ServerEntry{ID: "fake"},
		logf: logf, handlers: handlers, grace: 100 * time.Millisecond,
		calls: map[string]*inflightCall{}, pool: map[string]*connSlot{},
		building: map[string]*buildFlight{}, stopCh: make(chan struct{}),
	}
	p.sem = make(chan struct{}, defaultMaxConcurrent)
	s := &connSlot{conn: conn}
	s.touch()
	s.gen = p.genSeq.Add(1)
	p.shared.Store(s)
	return p
}

func waitStarted(t *testing.T, c *fakeNotifyConn, tool string) {
	t.Helper()
	select {
	case got := <-c.started:
		if got != tool {
			t.Fatalf("started tool = %q, want %q", got, tool)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("等待 %q 调用启动超时", tool)
	}
}

// ─── L8：progressToken = call_id 路由 ───────────────────

func TestProgressRoutesByCallID(t *testing.T) {
	conn := newFakeNotifyConn()
	var mu sync.Mutex
	var events []ProgressEvent
	var logs []string
	p := newFakeConnProvider(conn,
		NotifyHandlers{Progress: func(ev ProgressEvent) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		}},
		func(format string, args ...any) {
			mu.Lock()
			logs = append(logs, fmt.Sprintf(format, args...))
			mu.Unlock()
		})

	done := make(chan error, 1)
	go func() {
		_, err := p.Call(withCallID(context.Background(), "call-abc"), "pid", map[string]any{})
		done <- err
	}()
	waitStarted(t, conn, "pid")
	if got := conn.lastToken(); got != "call-abc" {
		t.Fatalf("下游收到的 progressToken = %q, want call_id=call-abc", got)
	}

	// 命中 token → 投递给该调用
	p.routeProgress("call-abc", &mcp.ProgressNotificationParams{Progress: 3, Total: 10, Message: "halfway"})
	mu.Lock()
	if len(events) != 1 || events[0].CallID != "call-abc" || events[0].Tool != "pid" || events[0].Progress != 3 {
		mu.Unlock()
		t.Fatalf("progress 未按 call_id 归位：%v", events)
	}
	mu.Unlock()

	// 未命中 token → 丢弃 + warn（不猜、不广播）
	p.routeProgress("call-unknown", &mcp.ProgressNotificationParams{Progress: 1})
	mu.Lock()
	if len(events) != 1 {
		mu.Unlock()
		t.Fatalf("未命中 token 不应投递，events=%d", len(events))
	}
	var warned bool
	for _, l := range logs {
		if strings.Contains(l, "call-unknown") && strings.Contains(l, "dropped") {
			warned = true
		}
	}
	mu.Unlock()
	if !warned {
		t.Fatalf("未命中 token 应 warn 丢弃：%v", logs)
	}

	close(conn.block)
	if err := <-done; err != nil {
		t.Fatalf("调用应成功返回：%v", err)
	}
	// 调用结束后再投递 → 未登记 → 丢弃
	p.routeProgress("call-abc", &mcp.ProgressNotificationParams{Progress: 2})
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 {
		t.Fatalf("调用结束后 progress 不应投递，events=%d", len(events))
	}
}

// ─── L8：elicitation 路由 ───────────────────────────────

func TestElicitRoutesByCallID(t *testing.T) {
	conn := newFakeNotifyConn()
	var got string
	p := newFakeConnProvider(conn, NotifyHandlers{Elicit: func(callID string, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		got = callID
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"answer": req.Params.Message}}, nil
	}}, nil)

	// 已登记 call_id → 归位
	infl := &inflightCall{callID: "call-e", done: make(chan struct{})}
	p.calls["call-e"] = infl
	res, err := p.routeElicit("call-e", &mcp.ElicitRequest{Params: &mcp.ElicitParams{Message: "q"}})
	if err != nil || got != "call-e" || res.Action != "accept" || res.Content["answer"] != "q" {
		t.Fatalf("elicitation 未按 call_id 归位：got=%q res=%+v err=%v", got, res, err)
	}

	// 未登记 token → 仍投递（不伪造）但不绑定
	got = ""
	if _, err := p.routeElicit("call-x", &mcp.ElicitRequest{Params: &mcp.ElicitParams{Message: "q2"}}); err != nil {
		t.Fatalf("未登记 token 应仍投递：%v", err)
	}
	if got != "call-x" {
		t.Fatalf("未登记 token 应以原 token 投递（不猜），got %q", got)
	}

	// 未注入回调 → 明确不支持（且 clientOptions 不注册 handler、不声明能力）
	noHooks := newFakeConnProvider(conn, NotifyHandlers{}, nil)
	if _, err := noHooks.routeElicit("", &mcp.ElicitRequest{Params: &mcp.ElicitParams{}}); err == nil {
		t.Fatalf("未注入 Elicit 回调时应返回不支持错误")
	}
	if opts := clientOptions(nil); opts != nil {
		t.Fatalf("clientHooks=nil 时不应注册任何通知 handler，got %+v", opts)
	}
}

// ─── L7：取消阶梯 ───────────────────────────────────────

func TestCancelCallGracefulStopsAtL1(t *testing.T) {
	conn := newFakeNotifyConn()
	p := newFakeConnProvider(conn, NotifyHandlers{}, nil)

	done := make(chan error, 1)
	go func() {
		_, err := p.Call(withCallID(context.Background(), "call-g"), "pid", map[string]any{})
		done <- err
	}()
	waitStarted(t, conn, "pid")

	rep, err := p.CancelCall(context.Background(), "call-g", 3*time.Second)
	if err != nil {
		t.Fatalf("CancelCall: %v", err)
	}
	if !rep.Graceful || rep.ConnClosed || rep.Killed || len(rep.Cascaded) != 0 {
		t.Fatalf("宽限期内自行结束应止步 L1：%+v", rep)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("被取消调用应返回 context.Canceled，got %v", err)
	}
	if n := conn.closesCount(); n != 0 {
		t.Fatalf("L1 成功不应关连接，closes=%d", n)
	}
	// 幂等：未登记 call_id 无动作
	if rep2, err := p.CancelCall(context.Background(), "call-g", time.Second); err != nil || rep2.Graceful {
		t.Fatalf("已结束调用的取消应幂等无动作：%+v err=%v", rep2, err)
	}
}

func TestCancelCallCascadeReportsSlotKilled(t *testing.T) {
	conn := newFakeNotifyConn()
	var mu sync.Mutex
	var logs []string
	p := newFakeConnProvider(conn, NotifyHandlers{}, func(format string, args ...any) {
		mu.Lock()
		logs = append(logs, fmt.Sprintf(format, args...))
		mu.Unlock()
	})

	mainDone := make(chan error, 1)
	victimDone := make(chan error, 1)
	go func() {
		_, err := p.Call(withCallID(context.Background(), "call-main"), "block", map[string]any{})
		mainDone <- err
	}()
	waitStarted(t, conn, "block")
	go func() {
		_, err := p.Call(withCallID(context.Background(), "call-victim"), "pid", map[string]any{})
		victimDone <- err
	}()
	waitStarted(t, conn, "pid")

	// L1 宽限 50ms（主调用不可中断）→ L2 关连接 → L3（无进程可杀）→ cascade-report
	rep, err := p.CancelCall(context.Background(), "call-main", 50*time.Millisecond)
	if err != nil {
		t.Fatalf("CancelCall: %v", err)
	}
	if rep.Graceful {
		t.Fatalf("不可中断调用不应记 graceful：%+v", rep)
	}
	if !rep.ConnClosed {
		t.Fatalf("应执行 L2 关连接：%+v", rep)
	}
	if rep.Killed {
		t.Fatalf("无网关侧进程（假连接）不应伪造 L3 kill：%+v", rep)
	}
	if len(rep.Cascaded) != 1 || rep.Cascaded[0] != "call-victim" {
		t.Fatalf("cascade-report 应列出同槽受影响 call_id：%+v", rep.Cascaded)
	}
	if conn.closesCount() == 0 {
		t.Fatalf("L2 应关闭该槽连接")
	}

	// 连带被打断的调用以 slot_killed 结束并附受影响 call_id 列表（不假装干净）
	err = <-victimDone
	if got := ErrorKind(err); got != ErrKindSlotKilled {
		t.Fatalf("连带调用 kind = %q, want %q（err=%v）", got, ErrKindSlotKilled, err)
	}
	var ce *CallError
	if !errors.As(err, &ce) || len(ce.CallIDs) != 1 || ce.CallIDs[0] != "call-victim" {
		t.Fatalf("slot_killed 应附受影响 call_id：%+v", ce)
	}
	mu.Lock()
	var warned bool
	for _, l := range logs {
		if strings.Contains(l, "cascade") {
			warned = true
		}
	}
	mu.Unlock()
	if !warned {
		t.Fatalf("连带取消应 warn 报告：%v", logs)
	}

	close(conn.block) // 释放不可中断的主调用
	if err := <-mainDone; err != nil {
		t.Fatalf("主调用最终应正常返回：%v", err)
	}
}

// ─── L4：structuredContent 透传 ─────────────────────────

func TestCallResultMapStructuredContentPassthrough(t *testing.T) {
	res := &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: "x"}},
		StructuredContent: map[string]any{"k": "v", "n": float64(1)},
	}
	m := callResultMap(res)
	sc, ok := m["structuredContent"].(map[string]any)
	if !ok || sc["k"] != "v" || sc["n"] != float64(1) {
		t.Fatalf("structuredContent 未透传：%v", m)
	}
	// 下游未提供时不伪造
	if _, exists := callResultMap(&mcp.CallToolResult{})["structuredContent"]; exists {
		t.Fatalf("无 structuredContent 时不应凭空生成")
	}
}
