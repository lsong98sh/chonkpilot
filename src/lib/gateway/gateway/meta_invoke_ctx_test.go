// G-19 验收：mcp_invoke 必须把**调用方 ctx** 转发给下游 provider。
//
// 修前：`ps.prov.Call(context.Background(), ...)` → 调用链 ctx 被丢 → 取消调用方后
// 下游调用仍在跑（本用例等待返回会超时失败，「修前」表现 = 卡到 2s 判定失败）。
// 修后：ctx 直传 + 用户取消不计熔断失败（与主调用链 doCall 同口径）。
package mcpgateway

import (
	"context"
	"testing"
	"time"
)

// TestMCPInvokeForwardsCallerCtx：取消调用方 ctx → 下游 provider 立即观察到取消且不触发熔断。
func TestMCPInvokeForwardsCallerCtx(t *testing.T) {
	_, g := newTestGW(t, 30*time.Second)
	p := newFakeProvider("fk-inv", "user")
	addFakeProvider(t, g, p, "slowtool") // 暴露名 = fk-inv_slowtool；熔断阈值=1（一次失败即 open）

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := g.handleMCPInvoke(ctx, map[string]any{
			"name": "fk-inv_slowtool", "arguments": map[string]any{},
		})
		done <- err
	}()

	// 等下游 provider 收到调用（在飞；fakeProvider.Call 阻塞在 release/ctx.Done）
	select {
	case tool := <-p.started:
		if tool != "slowtool" {
			t.Fatalf("下游收到的工具名=%q want slowtool（原始名）", tool)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("下游 provider 未被调用")
	}

	// 取消调用方 ctx → 下游应立即观察到取消、mcp_invoke 立即返回
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("handleMCPInvoke 返回 error（期望工具级错误文本）: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("调用方取消后下游仍在跑：mcp_invoke 丢弃了调用方 ctx（G-19 未修）")
	}
	if got := p.callCount("slowtool"); got != 1 {
		t.Fatalf("下游调用次数=%d want 1", got)
	}
	// 用户取消不计熔断失败（阈值=1：若计入，状态会变 open）
	if ps, ok := g.reg.provider(provKey("proxied", scopeGlobal, "fk-inv")); ok {
		if st := ps.cb.stateName(); st != "closed" {
			t.Fatalf("熔断器状态=%s want closed（用户取消不应计为 provider 失败）", st)
		}
	} else {
		t.Fatal("provider 未登记")
	}
}

// TestMCPInvokeProbeCancelResetsProbing（C-47）：half-open 试探调用经 mcp_invoke 被用户取消后，
// 必须归还 probing 单飞标志（cancel(isProbe)）；否则 probing 恒真 → 该 provider 永久 fail-closed。
func TestMCPInvokeProbeCancelResetsProbing(t *testing.T) {
	_, g := newTestGW(t, 30*time.Second)
	p := newFakeProvider("fk-c47", "user")
	addFakeProvider(t, g, p, "slowtool") // 熔断阈值=1（一次失败即 open）
	ps, ok := g.reg.provider(provKey("proxied", scopeGlobal, "fk-c47"))
	if !ok {
		t.Fatal("provider 未登记")
	}
	// 令熔断器进入 half-open：先 open，再把冷却起点前移使下次 allow 放行试探。
	ps.cb.failure()
	ps.cb.mu.Lock()
	ps.cb.openedAt = time.Now().Add(-ps.cb.cooldown)
	ps.cb.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := g.handleMCPInvoke(ctx, map[string]any{
			"name": "fk-c47_slowtool", "arguments": map[string]any{},
		})
		done <- err
	}()

	select {
	case <-p.started: // 试探已放行并在飞
	case <-time.After(3 * time.Second):
		t.Fatal("下游 provider 未被调用（half-open 试探未放行？）")
	}
	cancel() // 用户取消在飞试探调用
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("handleMCPInvoke 返回 error（期望工具级错误文本）: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("取消后 mcp_invoke 未返回")
	}
	// 取消试探须复位 probing → 后续调用可再次放行试探（未修时 probing 恒真 → allowed=false）。
	if allowed, isProbe := ps.cb.allow(); !allowed || !isProbe {
		t.Fatalf("取消试探后应复位 probing 并可再次放行试探，got (allowed=%v,isProbe=%v)", allowed, isProbe)
	}
}
