// 下游熔断器（closed / open / half-open，对齐 26-mcp-gateway）。
package mcpgateway

import (
	"sync"
	"time"
)

type breaker struct {
	mu        sync.Mutex
	state     string // closed / open / half-open
	failures  int
	openedAt  time.Time
	probing   bool // half-open 期间已有在飞试探（单飞：只放行第一个试探）
	threshold int
	cooldown  time.Duration
	disabled  bool
}

func newBreaker(threshold int, cooldown time.Duration, disabled bool) *breaker {
	if threshold <= 0 {
		threshold = 5
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &breaker{state: "closed", threshold: threshold, cooldown: cooldown, disabled: disabled}
}

// allow 判断是否放行调用（allowed=false = 熔断快速失败）。isProbe=true 表示本次放行是
// half-open 单飞试探：调用结束须以 cancel(true)/success()/failure() 收尾；
// 非试探调用结束只允许 success()/failure()/cancel(false)（普通调用被取消不得复位
// probing，否则会闯入 half-open 单飞窗口，C-32）。
func (b *breaker) allow() (allowed, isProbe bool) {
	if b.disabled {
		return true, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == "open" {
		// 冷却期后进入 half-open 并放 1 个试探（后续在飞试探未回收前不再放行）。
		if time.Since(b.openedAt) >= b.cooldown {
			b.state = "half-open"
			if b.probing {
				return false, false
			}
			b.probing = true
			return true, true
		}
		return false, false
	}
	if b.state == "half-open" {
		// 单飞：已有在飞试探 → 其余快速失败（与注释「放 1 个试探」一致，C-18）。
		if b.probing {
			return false, false
		}
		b.probing = true
		return true, true
	}
	return true, false
}

// cancel 归还探测标志：仅试探调用（probe=true）复位 probing——不清状态、不计失败，
// 使后续调用可再次放行试探（否则 probing 恒真 → 该 provider 永久 fail-closed，C-18）。
// 非试探调用（probe=false）被取消一律不动 probing（closed 期的普通取消与单飞无关，C-32）。
func (b *breaker) cancel(probe bool) {
	if b.disabled || !probe {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probing = false
}

func (b *breaker) success() {
	if b.disabled {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.state = "closed"
	b.probing = false
}

func (b *breaker) failure() {
	if b.disabled {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.state == "half-open" || b.failures >= b.threshold {
		b.state = "open"
		b.openedAt = time.Now()
		b.probing = false
	}
}

func (b *breaker) stateName() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}
