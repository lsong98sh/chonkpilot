// 统一异步模型（超时后交用户裁决）白盒测试（package mcpgateway 内部）：
// 覆盖 never/manual/auto 三条任务化路径 + 裁决（wait/detach/cancel）+ detached 可取消
// + auto 不重跑（上游调用次数=1）+ 超时不计熔断失败 + 第三方缺省 never + kill/respawn。
//
// 通过 fake Provider 直接注册进 registry（不走真实下游进程），可精确断言调用次数与
// Invalidate（作废/重建）次数；另附 proxyProvider.Invalidate 真实 stdio 子进程重建用例。
package mcpgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ─── stdio helper（真实子进程重建用例用；go test 二进制自 re-exec）───

// TestMain 在 GATEWAY_STDIO_HELPER=1 时把本进程作为 stdio MCP server 运行（pid 工具）。
func TestMain(m *testing.M) {
	if os.Getenv("GATEWAY_STDIO_HELPER") == "1" {
		runStdioHelper()
		return
	}
	os.Exit(m.Run())
}

// runStdioHelper 以 stdio 暴露 pid 工具（回显本进程 pid，用于断言重建换了进程）；
// slow 工具（回显 "<pid>:<ms>"）用于按 workdir 隔离的并发用例（GATEWAY_STDIO_SLOW_MS 定延时，
// 缺省 500ms）。
// GATEWAY_STDIO_PROBE（目录）非空时先落盘探针（cwd + 展开后的 env 值），
// 供 TestSpawnedStdioCwdAndEnvExpansion 断言 stdio 子进程 cwd/env 生效（见 provider_test.go）。
func runStdioHelper() {
	if dir := os.Getenv("GATEWAY_STDIO_PROBE"); dir != "" {
		writeSpawnProbe(dir)
	}
	ms := mcp.NewServer(&mcp.Implementation{Name: "stdio-helper", Version: "1.0.0"}, nil)
	ms.AddTool(
		&mcp.Tool{Name: "pid", Description: "pid", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strconv.Itoa(os.Getpid())}}}, nil
		})
	ms.AddTool(
		&mcp.Tool{Name: "slow", Description: "slow", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			d := 500 * time.Millisecond
			if v, err := strconv.Atoi(os.Getenv("GATEWAY_STDIO_SLOW_MS")); err == nil && v > 0 {
				d = time.Duration(v) * time.Millisecond
			}
			time.Sleep(d)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
				Text: strconv.Itoa(os.Getpid()) + ":" + strconv.Itoa(int(d.Milliseconds()))}}}, nil
		})
	if err := ms.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(1)
	}
}

// ─── fake Provider ─────────────────────────────────────

// fakeProvider 是可控的下游 provider：Call 计数 + 阻塞在 release 上（或随 ctx 取消返回）。
type fakeProvider struct {
	id     string
	origin string // builtin / user
	tools  []*mcp.Tool

	mu          sync.Mutex
	calls       map[string]int
	invalidates int
	rebuilt     bool  // Invalidate 返回值（默认 true）
	invErr      error // Invalidate 错误（默认 nil；模拟 respawn 失败）
	callErr     error // Call 错误（默认 nil；模拟上游调用失败）
	started     chan string
	release     chan struct{}
}

func newFakeProvider(id, origin string) *fakeProvider {
	return &fakeProvider{
		id: id, origin: origin, calls: map[string]int{}, rebuilt: true,
		started: make(chan string, 16), release: make(chan struct{}),
	}
}

func (p *fakeProvider) Name() string { return "fake:" + p.id }

func (p *fakeProvider) ListTools(context.Context) ([]*mcp.Tool, error) { return p.tools, nil }

func (p *fakeProvider) Call(ctx context.Context, tool string, _ map[string]any) (*mcp.CallToolResult, error) {
	p.mu.Lock()
	p.calls[tool]++
	err := p.callErr
	p.mu.Unlock()
	select {
	case p.started <- tool:
	default:
	}
	if err != nil {
		return nil, err
	}
	select {
	case <-p.release:
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done:" + tool}}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// setCallErr 注入 Call 错误（模拟上游调用失败）。
func (p *fakeProvider) setCallErr(err error) {
	p.mu.Lock()
	p.callErr = err
	p.mu.Unlock()
}

func (p *fakeProvider) Invalidate(context.Context) (bool, error) {
	p.mu.Lock()
	p.invalidates++
	err := p.invErr
	rebuilt := p.rebuilt
	p.mu.Unlock()
	if err != nil {
		return false, err
	}
	return rebuilt, nil
}

func (p *fakeProvider) Close() error { return nil }

func (p *fakeProvider) callCount(tool string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[tool]
}

func (p *fakeProvider) invalidateCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.invalidates
}

// ─── 装配 ─────────────────────────────────────────────

func newTestGW(t *testing.T, callTimeout time.Duration) (mq.Bus, *Gateway) {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	g, err := New(Params{Bus: bus, CallTimeout: callTimeout, MaxTasks: 8, Logf: t.Logf})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := g.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		_ = g.Stop(context.Background())
		bus.Close()
	})
	return bus, g
}

// addFakeProvider 把 fake provider 注册进 registry（工具暴露名 = <id>_<name>）。
func addFakeProvider(t *testing.T, g *Gateway, p *fakeProvider, toolNames ...string) {
	t.Helper()
	p.tools = nil
	for _, n := range toolNames {
		p.tools = append(p.tools, &mcp.Tool{Name: n, Description: n, InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}})
	}
	ps := &providerState{
		key:    provKey("proxied", scopeGlobal, p.id),
		entry:  &ServerEntry{ID: p.id, Name: p.id, Enabled: true, Origin: p.origin},
		prov:   p,
		status: "connected",
		cb:     newBreaker(1, 30*time.Second, false), // 阈值=1，便于断言“超时不计失败”
		scope:  scopeGlobal,
	}
	if err := g.reg.registerProvider(ps, p.tools, ""); err != nil {
		t.Fatalf("register fake provider: %v", err)
	}
}

// setToolMeta 覆盖已注册工具的 _meta（测试需按用例设 async/timeout）。
func setToolMeta(t *testing.T, g *Gateway, exposedName string, meta map[string]any) {
	t.Helper()
	rt, ok := g.reg.find(exposedName)
	if !ok {
		t.Fatalf("tool %s not found", exposedName)
	}
	rt.Tool.Meta = meta
}

// ─── 总线辅助 ──────────────────────────────────────────

func gwSubj(method string) string { return "mcp-" + strings.ReplaceAll(method, "/", "-") }

func gwCall(t *testing.T, bus mq.Bus, method string, payload map[string]any) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v := bus.Emit(ctx, gwSubj(method), payload).Wait()
	if err := v.Err(); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	m, _ := v.Result.(map[string]any)
	return m
}

// gwCallErr 返回方法错误（不 Fatal）。
func gwCallErr(bus mq.Bus, method string, payload map[string]any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return bus.Emit(ctx, gwSubj(method), payload).Wait().Err()
}

// gwCallAsync 在独立 goroutine 发射调用（用于会阻塞的 tools/call），返回结果通道。
type gwOutcome struct {
	res map[string]any
	err error
}

func gwCallAsync(bus mq.Bus, method string, payload map[string]any) <-chan gwOutcome {
	ch := make(chan gwOutcome, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		v := bus.Emit(ctx, gwSubj(method), payload).Wait()
		res, _ := v.Result.(map[string]any)
		ch <- gwOutcome{res: res, err: v.Err()}
	}()
	return ch
}

func gwText(res map[string]any) string {
	if content, ok := res["content"].([]any); ok && len(content) > 0 {
		if c, ok := content[0].(map[string]any); ok {
			if text, ok := c["text"].(string); ok {
				return text
			}
		}
	}
	return ""
}

func gwPendingTaskID(t *testing.T, res map[string]any) string {
	t.Helper()
	if sc, ok := res["structuredContent"].(map[string]any); ok {
		if id, ok := sc["task_id"].(string); ok && id != "" {
			return id
		}
	}
	t.Fatalf("result not pending: %v", res)
	return ""
}

// subTimeout 订阅 mcp-tools-timeout 事件。
func subTimeout(t *testing.T, bus mq.Bus) chan map[string]any {
	t.Helper()
	ch := make(chan map[string]any, 8)
	sub, err := bus.On(SubjectToolsTimeout, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if err := json.Unmarshal(v.Payload, &m); err == nil {
			ch <- m
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe timeout: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	return ch
}

// waitTimeoutEvent 等待一条 mcp-tools-timeout。
func waitTimeoutEvent(t *testing.T, ch chan map[string]any) map[string]any {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(5 * time.Second):
		t.Fatalf("no mcp-tools-timeout within 5s")
	}
	return nil
}

// strList 归一 options 取值：执行池快照内为 []string（经 MQ 序列化后为 []any）——测试两种形态通用。
func strList(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, it := range x {
			if s, ok := it.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// poolStatus 读执行池任务状态快照（取代已移除的 `tasks/status` 方法面；白盒测试专用）。
// 2026-09-19（缺口 2）：执行池按 instance 分桶 → 测试辅助跨桶直取（gw task id 全局唯一）。
func poolStatus(t *testing.T, g *Gateway, taskID string) map[string]any {
	t.Helper()
	g.tm.mu.Lock()
	defer g.tm.mu.Unlock()
	for _, b := range g.tm.execs {
		if tk, ok := b[taskID]; ok {
			return taskStatusMap(tk)
		}
	}
	t.Fatalf("task %s not found in exec pool", taskID)
	return nil
}

// poolResult 读执行池任务结果文本（取代已移除的 `tasks/result` 方法面；白盒测试专用）。
func poolResult(t *testing.T, g *Gateway, taskID string) string {
	t.Helper()
	g.tm.mu.Lock()
	defer g.tm.mu.Unlock()
	var tk *ExecTask
	for _, b := range g.tm.execs {
		if cand, ok := b[taskID]; ok {
			tk = cand
			break
		}
	}
	if tk == nil || tk.Result == nil {
		t.Fatalf("task %s 无结果（found=%v）", taskID, tk != nil)
	}
	for _, c := range tk.Result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// awaitTerminal 轮询**执行池**直到终态（取代已移除的 `tasks/status` 轮询）。
func awaitTerminal(t *testing.T, g *Gateway, taskID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if res := tryPoolStatus(g, taskID); res != nil {
			if st, _ := res["state"].(string); st == "done" || st == "error" || st == "cancelled" {
				return res
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %s not terminal within 5s", taskID)
	return nil
}

// tryPoolStatus 跨桶取任务状态快照（未命中 → nil；测试辅助，不 Fatal）。
func tryPoolStatus(g *Gateway, taskID string) map[string]any {
	g.tm.mu.Lock()
	defer g.tm.mu.Unlock()
	for _, b := range g.tm.execs {
		if tk, ok := b[taskID]; ok {
			return taskStatusMap(tk)
		}
	}
	return nil
}

// ─── 用例 ─────────────────────────────────────────────

// TestNeverTimeoutAwaitsDecision：never 到超时点 → 发 mcp-tools-timeout（options=[wait,cancel]）；
// wait → 撤销超时继续等并交付结果；上游仅调用一次。
func TestNeverTimeoutAwaitsDecision(t *testing.T) {
	bus, g := newTestGW(t, 5*time.Second)
	fk := newFakeProvider("fk", OriginBuiltin)
	addFakeProvider(t, g, fk, "slow")
	setToolMeta(t, g, "fk_slow", map[string]any{"async": "never", "timeout": 0.1})
	evCh := subTimeout(t, bus)

	out := gwCallAsync(bus, "tools/call", map[string]any{
		"name": "fk_slow", "instance_id": "ins-1", "tool_call_id": "call-1",
	})
	ev := waitTimeoutEvent(t, evCh)
	if ev["reason"] != "timeout" {
		t.Fatalf("reason = %v, want timeout", ev["reason"])
	}
	if ev["tool"] != "fk_slow" || ev["instance_id"] != "ins-1" || ev["tool_call_id"] != "call-1" {
		t.Fatalf("event 标识不符: %v", ev)
	}
	taskID, _ := ev["task_id"].(string)
	if taskID == "" {
		t.Fatalf("event 缺 task_id: %v", ev)
	}
	if fs, _ := ev["timeout_s"].(float64); fs != 0.1 {
		t.Fatalf("timeout_s = %v, want 0.1", ev["timeout_s"])
	}
	opts, _ := ev["options"].([]any)
	if len(opts) != 2 || opts[0] != "wait" || opts[1] != "cancel" {
		t.Fatalf("never options = %v, want [wait,cancel]", ev["options"])
	}

	// wait：撤销超时继续等
	w := gwCall(t, bus, "tools/wait", map[string]any{"task_id": taskID, "instance_id": "ins-1"})
	if w["waiting"] != true {
		t.Fatalf("tools/wait 回执 = %v", w)
	}
	close(fk.release) // 放行上游调用
	select {
	case out := <-out:
		if out.err != nil {
			t.Fatalf("call err: %v", out.err)
		}
		if got := gwText(out.res); got != "done:slow" {
			t.Fatalf("wait 后应交付结果, got %q (res=%v)", got, out.res)
		}
		if _, pending := out.res["structuredContent"]; pending {
			t.Fatalf("wait 后不应 pending: %v", out.res)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("wait 后调用未返回")
	}
	if n := fk.callCount("slow"); n != 1 {
		t.Fatalf("上游调用次数 = %d, want 1", n)
	}
}

// TestAwaitingVisibleInPool：待裁决态（never 到超时点）→ 执行池快照含
// awaiting{reason:timeout, timeout_s, options:[wait,cancel]}（前端裁决条恢复用）；
// 裁决（wait）后消失（I-57）。
// 2026-09-18：`tasks/status|list` 方法面已移除 → 断言改读执行池快照（taskStatusMap 同源）。
func TestAwaitingVisibleInPool(t *testing.T) {
	bus, g := newTestGW(t, 5*time.Second)
	fk := newFakeProvider("fk", OriginBuiltin)
	addFakeProvider(t, g, fk, "slow")
	setToolMeta(t, g, "fk_slow", map[string]any{"async": "never", "timeout": 0.1})
	evCh := subTimeout(t, bus)

	out := gwCallAsync(bus, "tools/call", map[string]any{"name": "fk_slow", "tool_call_id": "call-aw"})
	ev := waitTimeoutEvent(t, evCh)
	taskID, _ := ev["task_id"].(string)

	// 执行池快照：待裁决态含 awaiting（options = never 的 [wait,cancel]）
	st := poolStatus(t, g, taskID)
	aw, ok := st["awaiting"].(map[string]any)
	if !ok {
		t.Fatalf("池快照缺 awaiting: %v", st)
	}
	if aw["reason"] != "timeout" {
		t.Fatalf("awaiting.reason = %v, want timeout", aw["reason"])
	}
	if fs, _ := aw["timeout_s"].(float64); fs != 0.1 {
		t.Fatalf("awaiting.timeout_s = %v, want 0.1", aw["timeout_s"])
	}
	if opts := strList(aw["options"]); len(opts) != 2 || opts[0] != "wait" || opts[1] != "cancel" {
		t.Fatalf("awaiting.options = %v, want [wait,cancel]", aw["options"])
	}

	// 裁决（wait）→ awaiting 消失
	if w := gwCall(t, bus, "tools/wait", map[string]any{"task_id": taskID}); w["waiting"] != true {
		t.Fatalf("tools/wait 回执 = %v", w)
	}
	st2 := poolStatus(t, g, taskID)
	if _, ok := st2["awaiting"]; ok {
		t.Fatalf("裁决后 awaiting 未清除: %v", st2)
	}
	close(fk.release)
	<-out
}

// TestManualTimeoutDetach：manual 到超时点 → 发 mcp-tools-timeout（options=[detach,cancel]）；
// detach → 原调用返回 pending，任务后台完成并经 mcp-tasks-report 回报。
func TestManualTimeoutDetach(t *testing.T) {
	bus, g := newTestGW(t, 5*time.Second)
	fk := newFakeProvider("fk", OriginBuiltin)
	addFakeProvider(t, g, fk, "srv")
	setToolMeta(t, g, "fk_srv", map[string]any{"async": "manual", "timeout": 0.1})
	evCh := subTimeout(t, bus)

	reportCh := make(chan map[string]any, 4)
	sub, err := bus.On(SubjectTaskReport, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if err := json.Unmarshal(v.Payload, &m); err == nil {
			reportCh <- m
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe report: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	out := gwCallAsync(bus, "tools/call", map[string]any{
		"name": "fk_srv", "tool_call_id": "call-manual",
	})
	ev := waitTimeoutEvent(t, evCh)
	opts, _ := ev["options"].([]any)
	if len(opts) != 2 || opts[0] != "detach" || opts[1] != "cancel" {
		t.Fatalf("manual options = %v, want [detach,cancel]", ev["options"])
	}
	taskID, _ := ev["task_id"].(string)

	// 待裁决态：执行池快照含 awaiting（manual options = [detach,cancel]）
	st0 := poolStatus(t, g, taskID)
	aw, ok := st0["awaiting"].(map[string]any)
	if !ok {
		t.Fatalf("manual 待裁决态池快照缺 awaiting: %v", st0)
	}
	if o := strList(aw["options"]); len(o) != 2 || o[0] != "detach" || o[1] != "cancel" {
		t.Fatalf("manual awaiting.options = %v, want [detach,cancel]", aw["options"])
	}

	// 转后台（2026-09-18：`tools/background` 方法面移除 → 进程内 DetachExec）
	bgTaskID, err := g.DetachExec("", "call-manual")
	if err != nil {
		t.Fatalf("DetachExec: %v", err)
	}
	if bgTaskID != taskID {
		t.Fatalf("DetachExec task_id = %q, want %q", bgTaskID, taskID)
	}
	// detach 裁决后 awaiting 消失
	if st := poolStatus(t, g, taskID); st["awaiting"] != nil {
		t.Fatalf("detach 裁决后 awaiting 未清除: %v", st)
	}
	select {
	case out := <-out:
		if out.err != nil {
			t.Fatalf("call err: %v", out.err)
		}
		if got := gwPendingTaskID(t, out.res); got != taskID {
			t.Fatalf("detach 后应返回 pending %q (res=%v)", taskID, out.res)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("detach 后同步调用未返回 pending")
	}
	close(fk.release)
	st := awaitTerminal(t, g, taskID)
	if st["state"] != "done" {
		t.Fatalf("后台任务 state = %v, want done", st["state"])
	}
	select {
	case r := <-reportCh:
		if r["task_id"] != taskID || r["state"] != "done" {
			t.Fatalf("report = %v, want task_id=%s state=done", r, taskID)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("no mcp-tasks-report within 3s")
	}
	if n := fk.callCount("srv"); n != 1 {
		t.Fatalf("上游调用次数 = %d, want 1", n)
	}
}

// TestNeverTimeoutCancel：never 到超时点 → cancel（进程内 CancelExec）→ 任务 cancelled 终态 +
// 原调用返回 isError + provider 被作废/重建（Invalidate 调用一次）。
func TestNeverTimeoutCancel(t *testing.T) {
	bus, g := newTestGW(t, 5*time.Second)
	fk := newFakeProvider("fk", OriginBuiltin)
	addFakeProvider(t, g, fk, "slow")
	setToolMeta(t, g, "fk_slow", map[string]any{"async": "never", "timeout": 0.1})
	evCh := subTimeout(t, bus)

	out := gwCallAsync(bus, "tools/call", map[string]any{"name": "fk_slow", "tool_call_id": "call-cancel"})
	ev := waitTimeoutEvent(t, evCh)
	taskID, _ := ev["task_id"].(string)

	// 取消（2026-09-18：`tasks/cancel` 方法面移除 → 进程内 CancelExec）
	ok, err := g.CancelExec("", taskID)
	if err != nil || !ok {
		t.Fatalf("CancelExec: ok=%v err=%v", ok, err)
	}
	select {
	case out := <-out:
		if out.err != nil {
			t.Fatalf("call err: %v", out.err)
		}
		if out.res["isError"] != true {
			t.Fatalf("cancel 后调用应 isError: %v", out.res)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("cancel 后调用未返回")
	}
	if st := awaitTerminal(t, g, taskID); st["state"] != "cancelled" {
		t.Fatalf("task state = %v, want cancelled", st["state"])
	}
	if n := fk.invalidateCount(); n != 1 {
		t.Fatalf("provider Invalidate 次数 = %d, want 1", n)
	}
}

// TestNeverTimeoutCancelReports：never 到超时点 → cancel（**非 detached** 在飞任务）→
// ① 同步返回附结构化取消标记 structuredContent.status=cancelled（I-62：供 server 落 cancelled
// 而非 done）；② 补发 mcp-tasks-report{state:cancelled, tool_call_id}（非 detached 未登记
// gwTaskID，server 按 tool_call_id 兜底定位）；③ 仅回报一次（执行 goroutine 不重复回报）。
func TestNeverTimeoutCancelReports(t *testing.T) {
	bus, g := newTestGW(t, 5*time.Second)
	fk := newFakeProvider("fk", OriginBuiltin)
	addFakeProvider(t, g, fk, "slow")
	setToolMeta(t, g, "fk_slow", map[string]any{"async": "never", "timeout": 0.1})
	evCh := subTimeout(t, bus)

	reportCh := make(chan map[string]any, 4)
	sub, err := bus.On(SubjectTaskReport, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if err := json.Unmarshal(v.Payload, &m); err == nil {
			reportCh <- m
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe report: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	out := gwCallAsync(bus, "tools/call", map[string]any{
		"name": "fk_slow", "instance_id": "ins-rc", "session": "s-rc", "turn": "t-rc",
		"tool_call_id": "call-rc",
	})
	ev := waitTimeoutEvent(t, evCh)
	taskID, _ := ev["task_id"].(string)
	if ok, err := g.CancelExec("ins-rc", taskID); err != nil || !ok {
		t.Fatalf("CancelExec: ok=%v err=%v", ok, err)
	}
	// ① 同步返回：isError + structuredContent.status=cancelled
	select {
	case out := <-out:
		if out.err != nil {
			t.Fatalf("call err: %v", out.err)
		}
		if out.res["isError"] != true {
			t.Fatalf("取消同步返回应 isError: %v", out.res)
		}
		sc, _ := out.res["structuredContent"].(map[string]any)
		if sc == nil || sc["status"] != "cancelled" {
			t.Fatalf("同步返回缺 structuredContent.status=cancelled: %v", out.res)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("cancel 后调用未返回")
	}
	// ② 补发回报：state=cancelled + tool_call_id
	select {
	case r := <-reportCh:
		if r["state"] != "cancelled" {
			t.Fatalf("report.state = %v, want cancelled (%v)", r["state"], r)
		}
		if r["tool_call_id"] != "call-rc" {
			t.Fatalf("report.tool_call_id = %v, want call-rc (%v)", r["tool_call_id"], r)
		}
		if r["task_id"] != taskID || r["instance_id"] != "ins-rc" {
			t.Fatalf("report 标识不符: %v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("no mcp-tasks-report within 3s")
	}
	// ③ 非 detached 仅回报一次
	select {
	case r := <-reportCh:
		t.Fatalf("非 detached 取消不应重复回报: %v", r)
	case <-time.After(500 * time.Millisecond):
	}
}

// TestDetachedTaskCancelable：detached（后台）任务仍可被 CancelExec 取消（用户硬约束）。
func TestDetachedTaskCancelable(t *testing.T) {
	bus, g := newTestGW(t, 5*time.Second)
	fk := newFakeProvider("fk", OriginBuiltin)
	addFakeProvider(t, g, fk, "bg")
	setToolMeta(t, g, "fk_bg", map[string]any{"async": "manual", "timeout": 0.1})
	evCh := subTimeout(t, bus)

	out := gwCallAsync(bus, "tools/call", map[string]any{"name": "fk_bg", "tool_call_id": "call-bg"})
	ev := waitTimeoutEvent(t, evCh)
	taskID, _ := ev["task_id"].(string)
	// detach → 转后台
	if bgTaskID, err := g.DetachExec("", "call-bg"); err != nil || bgTaskID != taskID {
		t.Fatalf("DetachExec: task=%q err=%v（want %q）", bgTaskID, err, taskID)
	}
	select {
	case <-out:
	case <-time.After(5 * time.Second):
		t.Fatalf("detach 后同步调用未返回")
	}
	// 后台运行中取消
	if ok, err := g.CancelExec("", taskID); err != nil || !ok {
		t.Fatalf("后台任务 CancelExec: ok=%v err=%v", ok, err)
	}
	if st := awaitTerminal(t, g, taskID); st["state"] != "cancelled" {
		t.Fatalf("后台任务 state = %v, want cancelled", st["state"])
	}
	if n := fk.invalidateCount(); n != 1 {
		t.Fatalf("后台取消后 provider Invalidate 次数 = %d, want 1", n)
	}
}

// TestAutoThresholdNoRerun：auto 到 async-threshold → 只调用上游一次（任务转移，不重跑）；
// 结果经后台任务交付。
func TestAutoThresholdNoRerun(t *testing.T) {
	bus, g := newTestGW(t, 5*time.Second)
	fk := newFakeProvider("fk", OriginBuiltin)
	addFakeProvider(t, g, fk, "auto")
	setToolMeta(t, g, "fk_auto", map[string]any{"async": "auto", "async-threshold": 0.1})

	res := gwCall(t, bus, "tools/call", map[string]any{"name": "fk_auto", "tool_call_id": "call-auto"})
	taskID := gwPendingTaskID(t, res)
	if n := fk.callCount("auto"); n != 1 {
		t.Fatalf("auto 转后台时上游调用次数 = %d, want 1（不得重跑）", n)
	}
	close(fk.release)
	st := awaitTerminal(t, g, taskID)
	if st["state"] != "done" {
		t.Fatalf("auto 后台任务 state = %v, want done", st["state"])
	}
	if n := fk.callCount("auto"); n != 1 {
		t.Fatalf("auto 完成后上游调用次数 = %d, want 1（不重跑）", n)
	}
	if got := poolResult(t, g, taskID); got != "done:auto" {
		t.Fatalf("auto 后台结果 = %q, want done:auto", got)
	}
}

// TestTimeoutNotCountedAsBreakerFailure：到超时点（未失败）不计熔断失败（慢 ≠ 坏）。
func TestTimeoutNotCountedAsBreakerFailure(t *testing.T) {
	bus, g := newTestGW(t, 5*time.Second)
	fk := newFakeProvider("fk", OriginBuiltin)
	addFakeProvider(t, g, fk, "slow")
	setToolMeta(t, g, "fk_slow", map[string]any{"async": "never", "timeout": 0.1})
	ps, _ := g.reg.provider(provKey("proxied", scopeGlobal, "fk"))
	if ps.cb.threshold != 1 {
		t.Fatalf("测试前提：熔断阈值应为 1，got %d", ps.cb.threshold)
	}
	evCh := subTimeout(t, bus)
	out := gwCallAsync(bus, "tools/call", map[string]any{"name": "fk_slow", "tool_call_id": "call-to"})
	ev := waitTimeoutEvent(t, evCh)
	taskID, _ := ev["task_id"].(string)

	if st := ps.cb.stateName(); st != "closed" {
		t.Fatalf("超时后熔断器应为 closed（超时不计失败），got %s", st)
	}
	// 收尾：wait 交付，验证正常完成仍 closed
	_ = gwCall(t, bus, "tools/wait", map[string]any{"task_id": taskID})
	close(fk.release)
	<-out
	if st := ps.cb.stateName(); st != "closed" {
		t.Fatalf("完成后熔断器应为 closed，got %s", st)
	}
}

// TestThirdPartySoftNever：第三方（Origin=user）工具契约未声明 async → 软缺省 never
// （超时发 [wait,cancel]）；调用级 async 可覆盖（软缺省非锁死）。
func TestThirdPartySoftNever(t *testing.T) {
	bus, g := newTestGW(t, 300*time.Millisecond)
	fk := newFakeProvider("ext", OriginUser)
	addFakeProvider(t, g, fk, "slow")
	evCh := subTimeout(t, bus)

	// 缺省（无 async）→ 软缺省 never：到超时点发事件
	out := gwCallAsync(bus, "tools/call", map[string]any{"name": "ext_slow", "tool_call_id": "call-ext"})
	ev := waitTimeoutEvent(t, evCh)
	opts, _ := ev["options"].([]any)
	if len(opts) != 2 || opts[0] != "wait" || opts[1] != "cancel" {
		t.Fatalf("第三方软缺省 never options = %v, want [wait,cancel]", ev["options"])
	}
	taskID, _ := ev["task_id"].(string)
	// 收尾：取消第一次调用（进程内 CancelExec）
	if ok, err := g.CancelExec("", taskID); err != nil || !ok {
		t.Fatalf("CancelExec: ok=%v err=%v", ok, err)
	}
	<-out

	// 调用级 async=always 覆盖软缺省 → 立即 pending
	res := gwCall(t, bus, "tools/call", map[string]any{
		"name": "ext_slow", "tool_call_id": "call-ext-2", "arguments": map[string]any{"async": "always"},
	})
	taskID2 := gwPendingTaskID(t, res)
	close(fk.release)
	if st := awaitTerminal(t, g, taskID2); st["state"] != "done" {
		t.Fatalf("调用级覆盖后任务 state = %v, want done", st["state"])
	}
}

// TestContractNeverNotOverridable：契约显式 async=never → 调用级不可覆盖（仍 never；
// options 无 detach，detach 请求被拒）。
func TestContractNeverNotOverridable(t *testing.T) {
	bus, g := newTestGW(t, 5*time.Second)
	fk := newFakeProvider("fk", OriginBuiltin)
	addFakeProvider(t, g, fk, "slow")
	// 契约显式 never（builtin，非第三方缺省）
	setToolMeta(t, g, "fk_slow", map[string]any{"async": "never", "timeout": 0.1})
	evCh := subTimeout(t, bus)
	out := gwCallAsync(bus, "tools/call", map[string]any{
		"name": "fk_slow", "tool_call_id": "call-lock", "arguments": map[string]any{"async": "always"},
	})
	ev := waitTimeoutEvent(t, evCh)
	taskID, _ := ev["task_id"].(string)
	// 无 detach：DetachExec 应被拒（never 的 options 不含 detach）
	if _, err := g.DetachExec("", "call-lock"); err == nil {
		t.Fatalf("契约 never 不应允许 detach")
	}
	_ = gwCall(t, bus, "tools/wait", map[string]any{"task_id": taskID})
	close(fk.release)
	select {
	case out := <-out:
		if got := gwText(out.res); got != "done:slow" {
			t.Fatalf("契约 never 覆盖无效（应仍为 never 并交付结果），got %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("call 未返回")
	}
}

// ─── proxyProvider.Invalidate：真实 stdio 子进程 kill + respawn ───

// TestProxyProviderInvalidateRespawns：Invalidate 恒 kill + respawn → 换新子进程（pid 变化）。
// 注：runtime 为单个 token（不再 strings.Fields 切分），测试二进制路径即使含空格也能拉起。
func TestProxyProviderInvalidateRespawns(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("skip：测试二进制路径不可用（%v）", err)
	}
	p, err := newProxyProvider(&ServerEntry{
		ID: "h", Runtime: exe, Transport: "stdio", Origin: OriginBuiltin,
		Env: []string{"GATEWAY_STDIO_HELPER=1"},
	}, 5*time.Second, t.Logf)
	if err != nil {
		t.Fatalf("newProxyProvider: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := p.connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	pid1 := callPID(t, p)
	rebuilt, err := p.Invalidate(ctx)
	if err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if !rebuilt {
		t.Fatalf("Invalidate 应重建（rebuilt=true）")
	}
	pid2 := callPID(t, p)
	if pid1 == "" || pid2 == "" || pid1 == pid2 {
		t.Fatalf("子进程未重建：pid1=%q pid2=%q", pid1, pid2)
	}
	t.Logf("stdio 子进程重建: pid %s → %s", pid1, pid2)
}

// TestSpawnArgvPreservesSpaces：runtime+args 逐个作为 exec 参数透传，**不做任何 shell/引号解析**
// —— 含空格的可执行路径 / 参数各自保持为单个完整 argv 元素（旧 strings.Fields 会切碎）。
func TestSpawnArgvPreservesSpaces(t *testing.T) {
	e := &ServerEntry{
		ID:      "sp",
		Runtime: `C:\Program Files\X\y.exe`,
		Args:    []string{"--config", `C:\Program Files\conf\a b.json`, "-n", "hello world"},
	}
	argv, err := spawnArgv(e)
	if err != nil {
		t.Fatalf("spawnArgv: %v", err)
	}
	want := []string{`C:\Program Files\X\y.exe`, "--config", `C:\Program Files\conf\a b.json`, "-n", "hello world"}
	if len(argv) != len(want) {
		t.Fatalf("argv 长度 = %d, want %d: %#v", len(argv), len(want), argv)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q（不得切分/去引号）", i, argv[i], want[i])
		}
	}
	// 实际构造 exec.Cmd：Args 原样保留（含空格路径不被切碎）
	cmd := exec.Command(argv[0], argv[1:]...)
	if len(cmd.Args) != len(want) {
		t.Fatalf("exec.Cmd.Args 长度 = %d, want %d: %#v", len(cmd.Args), len(want), cmd.Args)
	}
	for i := range want {
		if cmd.Args[i] != want[i] {
			t.Fatalf("exec.Cmd.Args[%d] = %q, want %q", i, cmd.Args[i], want[i])
		}
	}
}

// TestCancelRespawnFailureBroadcasts：取消引发的 respawn 失败 → 发 mcp-gateway-changed
// {kind:server, name:<server>, status:"failed", reason}（I-58；instance_id = 触发实例）。
func TestCancelRespawnFailureBroadcasts(t *testing.T) {
	bus, g := newTestGW(t, 5*time.Second)
	fk := newFakeProvider("fk", OriginBuiltin)
	fk.invErr = fmt.Errorf("respawn boom")
	addFakeProvider(t, g, fk, "slow")
	setToolMeta(t, g, "fk_slow", map[string]any{"async": "never", "timeout": 0.1})
	evCh := subTimeout(t, bus)
	chCh := make(chan map[string]any, 4)
	sub, err := bus.On(SubjectMCPChanged, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if err := json.Unmarshal(v.Payload, &m); err == nil {
			chCh <- m
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe changed: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	out := gwCallAsync(bus, "tools/call", map[string]any{
		"name": "fk_slow", "instance_id": "ins-x", "tool_call_id": "call-rf",
	})
	ev := waitTimeoutEvent(t, evCh)
	taskID, _ := ev["task_id"].(string)
	if ok, err := g.CancelExec("ins-x", taskID); err != nil || !ok {
		t.Fatalf("CancelExec: ok=%v err=%v", ok, err)
	}
	<-out
	if n := fk.invalidateCount(); n != 1 {
		t.Fatalf("取消后 Invalidate 次数 = %d, want 1", n)
	}
	select {
	case m := <-chCh:
		if m["kind"] != "server" || m["name"] != "fk" || m["status"] != "failed" {
			t.Fatalf("mcp-gateway-changed = %v, want kind=server name=fk status=failed", m)
		}
		if m["instance_id"] != "ins-x" {
			t.Fatalf("instance_id = %v, want ins-x", m["instance_id"])
		}
		if r, _ := m["reason"].(string); r == "" {
			t.Fatalf("failed 应带 reason: %v", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("no mcp-gateway-changed within 3s")
	}
}

// callPID 调用 stdio helper 的 pid 工具并返回 pid 文本。
func callPID(t *testing.T, p *proxyProvider) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := p.Call(ctx, "pid", map[string]any{})
	if err != nil {
		t.Fatalf("call pid: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatalf("pid 无内容: %v", res)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("pid 内容类型 = %T", res.Content[0])
	}
	return strings.TrimSpace(tc.Text)
}
