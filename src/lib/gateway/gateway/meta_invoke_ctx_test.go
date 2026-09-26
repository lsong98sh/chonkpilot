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
