// 前置打点钩子（2026-09-27）白盒：注册方经 tools/register 的可选字段 pre_hook_subject 声明钩子主题，
// gateway 在执行**任意**工具前向其同步发一次请求；钩子失败 → 拒绝该工具调用（工具不执行）；
// **无钩子声明时零开销**（不发任何消息）。
package mcpgateway

import (
	"context"
	"encoding/json"
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

// TestPreHookPayloadCarriesTouchFiles：前置钩子载荷带 `touch_files`（默认真值 = 保守按涉及），
// 供 `plugin-history` 判定是否打检查点（2026-09-28）。
func TestPreHookPayloadCarriesTouchFiles(t *testing.T) {
	bus, g := newTestGW(t, 2*time.Second)
	p := newFakeProvider("ext", OriginUser)
	addFakeProvider(t, g, p, "slow")
	declarePreHook(t, g, "hook-tool-t", "test-hook-touch")

	var got atomic.Value
	sub, err := bus.On("test-hook-touch", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) == nil {
			got.Store(m)
		}
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
		t.Fatalf("tools/call: %v", o.err)
	}
	m, _ := got.Load().(map[string]any)
	if m == nil {
		t.Fatal("前置钩子未收到载荷")
	}
	if m["tool"] != "ext_slow" {
		t.Fatalf("载荷 tool 不符：%v", m["tool"])
	}
	// 第三方（Origin=user）→ 缺省保守按「涉及」（true）
	if v, ok := m["touch_files"].(bool); !ok || !v {
		t.Fatalf("载荷应带 touch_files=true（第三方保守按涉及），实际=%v", m["touch_files"])
	}
}

// TestResolveTouchFiles：「涉及文件变动」缺省映射 + 显式覆盖优先级（用户口径 2026-09-28）。
func TestResolveTouchFiles(t *testing.T) {
	_, g := newTestGW(t, 2*time.Second)
	self := &ServerEntry{ID: "self", Name: "内置能力源", Category: "core", Origin: OriginBuiltin}
	dirNode := &ServerEntry{ID: "mydir", Category: "dir", Origin: OriginBuiltin}
	third := &ServerEntry{ID: "ext", Origin: OriginUser}

	// self 内置：白名单内（filesys_run / script_run）→ 涉及；其余内置 → 不涉及
	if !g.resolveTouchFiles("self_filesys_run", "filesys_run", self) {
		t.Fatal("self_filesys_run 应涉及文件变动")
	}
	if !g.resolveTouchFiles("self_script_run", "script_run", self) {
		t.Fatal("self_script_run 应涉及文件变动")
	}
	for _, n := range []string{"file_read", "file_find", "file_diff", "web_fetch", "browser_run", "desktop_run"} {
		if g.resolveTouchFiles("self_"+n, n, self) {
			t.Fatalf("self 内置工具 %s 缺省应不涉及文件变动", n)
		}
	}
	// dir 节点 / 第三方 / 无法判定 → 保守按涉及
	if !g.resolveTouchFiles("mydir_x", "x", dirNode) {
		t.Fatal("dir 节点工具缺省应保守按涉及")
	}
	if !g.resolveTouchFiles("ext_y", "y", third) {
		t.Fatal("第三方工具缺省应保守按涉及")
	}
	if !g.resolveTouchFiles("z", "z", nil) {
		t.Fatal("无法判定来源时应保守按涉及")
	}

	// 显式覆盖优先（两侧都能翻转）
	g.SetAsyncOverrides(map[string]ToolAsyncOverride{
		"self_file_read": {TouchFilesSet: true, TouchFiles: true},
		"ext_y":          {TouchFilesSet: true, TouchFiles: false},
	})
	if !g.resolveTouchFiles("self_file_read", "file_read", self) {
		t.Fatal("显式 touch_files=true 应生效（覆盖「不涉及」缺省）")
	}
	if g.resolveTouchFiles("ext_y", "y", third) {
		t.Fatal("显式 touch_files=false 应生效（覆盖「涉及」缺省）")
	}
	// 覆盖表存在但该项未显式设置 touch_files → 仍按缺省
	g.SetAsyncOverrides(map[string]ToolAsyncOverride{"self_filesys_run": {Mode: "never"}})
	if !g.resolveTouchFiles("self_filesys_run", "filesys_run", self) {
		t.Fatal("未显式设置 touch_files 时应回落缺省（self_filesys_run 涉及）")
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
