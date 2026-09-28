// 前置打点钩子（2026-09-27）白盒：注册方经 tools/register 的可选字段 pre_hook_subject 声明钩子主题，
// gateway 在执行**任意**工具前向其同步发一次请求；钩子失败 → 拒绝该工具调用（工具不执行）；
// **无钩子声明时零开销**（不发任何消息）。
package mcpgateway

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// declarePreHook 直接向注册表声明一个前置钩子（toolName 仅作占位，测试路由仍走 fakeProvider）。
func declarePreHook(t *testing.T, g *Gateway, toolName, subject string) {
	t.Helper()
	rt := &registeredTool{
		Tool:           &mcp.Tool{Name: toolName, InputSchema: map[string]any{"type": "object"}},
		PreHookSubject: subject,
		Scope:          scopeGlobal,
		Owner:          "test",
	}
	if err := g.regProv.RegisterTool(rt); err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}
}

// TestPreHookZeroOverheadWhenUnregistered：无任何 pre_hook_subject 声明 → 工具调用**不发**钩子消息。
func TestPreHookZeroOverheadWhenUnregistered(t *testing.T) {
	bus, g := newTestGW(t, 2*time.Second)
	p := newFakeProvider("ext", OriginUser)
	addFakeProvider(t, g, p, "slow")

	var hookCalls atomic.Int64
	sub, err := bus.On("test-hook", 0, func(_ context.Context, _ string, _ *mq.Value) error {
		hookCalls.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	out := gwCallAsync(bus, "tools/call", map[string]any{"name": "ext_slow", "arguments": map[string]any{}})
	select {
	case <-p.started:
	case <-time.After(3 * time.Second):
		t.Fatal("工具未启动")
	}
	close(p.release)
	if o := <-out; o.err != nil {
		t.Fatalf("tools/call: %v", o.err)
	}
	if n := hookCalls.Load(); n != 0 {
		t.Fatalf("无钩子声明时不应发钩子消息，got %d", n)
	}
}

// TestPreHookSuccessAllowsCall：钩子成功（不写回 error）→ 工具正常执行。
func TestPreHookSuccessAllowsCall(t *testing.T) {
	bus, g := newTestGW(t, 2*time.Second)
	p := newFakeProvider("ext", OriginUser)
	addFakeProvider(t, g, p, "slow")
	declarePreHook(t, g, "hook-tool-a", "test-hook-ok")

	var hookCalls atomic.Int64
	sub, err := bus.On("test-hook-ok", 0, func(_ context.Context, _ string, v *mq.Value) error {
		hookCalls.Add(1)
		v.Result = map[string]any{"ok": true}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	out := gwCallAsync(bus, "tools/call", map[string]any{"name": "ext_slow", "arguments": map[string]any{}})
	select {
	case <-p.started:
	case <-time.After(3 * time.Second):
		t.Fatal("工具未启动")
	}
	close(p.release)
	if o := <-out; o.err != nil {
		t.Fatalf("钩子成功后 tools/call 应成功: %v", o.err)
	}
	if n := hookCalls.Load(); n != 1 {
		t.Fatalf("钩子应被调用一次，got %d", n)
	}
}

// TestPreHookFailureRejectsCall：钩子失败（返回 error）→ 工具调用被拒（工具不执行）。
func TestPreHookFailureRejectsCall(t *testing.T) {
	bus, g := newTestGW(t, 2*time.Second)
	p := newFakeProvider("ext", OriginUser)
	addFakeProvider(t, g, p, "slow")
	declarePreHook(t, g, "hook-tool-b", "test-hook-fail")

	sub, err := bus.On("test-hook-fail", 0, func(_ context.Context, _ string, _ *mq.Value) error {
		return errors.New("打点失败")
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	if err := gwCallErr(bus, "tools/call", map[string]any{"name": "ext_slow", "arguments": map[string]any{}}); err == nil {
		t.Fatal("钩子失败时应拒绝工具调用")
	}
	if n := p.callCount("ext_slow"); n != 0 {
		t.Fatalf("被拒的工具不得执行，calls=%d", n)
	}
}

// TestPreHookUnregisterStopsEmission：注销声明钩子的工具 → 钩子引用计数归零、后续调用不再发消息。
func TestPreHookUnregisterStopsEmission(t *testing.T) {
	bus, g := newTestGW(t, 2*time.Second)
	p := newFakeProvider("ext", OriginUser)
	addFakeProvider(t, g, p, "slow")
	declarePreHook(t, g, "hook-tool-c", "test-hook-c")
	if !g.regProv.HasPreHooks() {
		t.Fatal("声明后应存在前置钩子")
	}
	g.regProv.UnregisterTool(scopeGlobal, "hook-tool-c")
	if g.regProv.HasPreHooks() {
		t.Fatal("注销后不应残留前置钩子")
	}

	var hookCalls atomic.Int64
	sub, err := bus.On("test-hook-c", 0, func(_ context.Context, _ string, _ *mq.Value) error {
		hookCalls.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	out := gwCallAsync(bus, "tools/call", map[string]any{"name": "ext_slow", "arguments": map[string]any{}})
	select {
	case <-p.started:
	case <-time.After(3 * time.Second):
		t.Fatal("工具未启动")
	}
	close(p.release)
	if o := <-out; o.err != nil {
		t.Fatalf("tools/call: %v", o.err)
	}
	if n := hookCalls.Load(); n != 0 {
		t.Fatalf("注销后不应再发钩子消息，got %d", n)
	}
}
