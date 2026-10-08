// breaker_test.go — 熔断器 allow/cancel 探测标记语义（C-32）。
package mcpgateway

import (
	"testing"
	"time"
)

// TestBreakerAllowProbeFlag：allow 返回 (allowed, isProbe)——仅 half-open 单飞试探 isProbe=true。
func TestBreakerAllowProbeFlag(t *testing.T) {
	b := newBreaker(1, time.Hour, false)

	if allowed, isProbe := b.allow(); !allowed || isProbe {
		t.Fatalf("closed 期放行应为 (true,false)，got (%v,%v)", allowed, isProbe)
	}
	b.success()

	// 打开熔断并伪造冷却期已过 → half-open 首个放行是试探
	b.failure()
	b.mu.Lock()
	b.openedAt = time.Now().Add(-b.cooldown)
	b.mu.Unlock()
	allowed, isProbe := b.allow()
	if !allowed || !isProbe {
		t.Fatalf("half-open 首个放行应为试探 (true,true)，got (%v,%v)", allowed, isProbe)
	}
	// 单飞：试探在飞期间其余调用快速失败且非试探
	if allowed, isProbe := b.allow(); allowed || isProbe {
		t.Fatalf("试探在飞期间应拒绝 (false,false)，got (%v,%v)", allowed, isProbe)
	}

	// 试探成功 → 回 closed
	b.success()
	if b.stateName() != "closed" {
		t.Fatalf("试探成功后应回 closed，got %s", b.stateName())
	}
}

// TestBreakerCancelProbeOnly：cancel 仅在试探调用（probe=true）时复位单飞标志；
// 非试探调用被取消不得复位（否则打破 half-open 单飞，C-32）。
func TestBreakerCancelProbeOnly(t *testing.T) {
	b := newBreaker(1, time.Hour, false)
	b.failure() // → open
	b.mu.Lock()
	b.openedAt = time.Now().Add(-b.cooldown) // 冷却期已过
	b.mu.Unlock()

	if allowed, isProbe := b.allow(); !allowed || !isProbe {
		t.Fatalf("应放行试探 (true,true)，got (%v,%v)", allowed, isProbe)
	}

	// 非试探调用被取消：probing 必须保持 → 后续调用仍被拒（单飞未被打破）
	b.cancel(false)
	if allowed, _ := b.allow(); allowed {
		t.Fatal("cancel(false) 不得复位 probing：试探仍在飞，后续调用应被拒")
	}

	// 试探调用被取消：复位 probing → 后续可再次放行试探（C-18：避免永久 fail-closed）
	b.cancel(true)
	if allowed, isProbe := b.allow(); !allowed || !isProbe {
		t.Fatalf("cancel(true) 后应可再次放行试探 (true,true)，got (%v,%v)", allowed, isProbe)
	}
}
