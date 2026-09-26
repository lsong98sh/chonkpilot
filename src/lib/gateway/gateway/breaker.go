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

// allow 判断是否放行调用（false = 熔断快速失败）。
func (b *breaker) allow() bool {
	if b.disabled {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == "open" {
		// 冷却期后放 1 个试探
		if time.Since(b.openedAt) >= b.cooldown {
			b.state = "half-open"
			return true
		}
		return false
	}
	return true
}

func (b *breaker) success() {
	if b.disabled {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.state = "closed"
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
	}
}

func (b *breaker) stateName() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}
