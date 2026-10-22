// 确定性时钟（MOD-1 M8-C2）：`Now` 固定，`After(d)` 仅对已 `Fire(d)` 的时长立即就绪。
//
// 用法：
//
//	clock.Fire(cfg.AutoContinueDelay) // 宽限期到期
//	clock.Fire(cfg.PendingWait)       // 异步回执超时
//	clock.Advance(5 * time.Minute)    // 仅推进 Now（事件时间）
//
// 未 `Fire` 的时长返回永不就绪的 channel（阻塞点由调用方按需显式打开）；
// `Fire` 必须在 `After` 之前登记（引擎在等待开始时一次性建立 channel）。
package chatest

import (
	"sync"
	"time"
)

// Clock 是测试时钟（并发安全）。
type Clock struct {
	mu   sync.Mutex
	now  time.Time
	fire map[time.Duration]bool
}

// NewClock 构造固定时间（2026-10-20 00:00:00 UTC）的测试时钟。
func NewClock() *Clock {
	return NewClockAt(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
}

// NewClockAt 构造指定起点的测试时钟。
func NewClockAt(now time.Time) *Clock {
	return &Clock{now: now, fire: map[time.Duration]bool{}}
}

// Now 返回固定当前时间。
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// After 返回该时长的 channel：已 Fire → 立即就绪；否则永不就绪。
func (c *Clock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	if c.fire[d] {
		ch <- c.now
	}
	return ch
}

// Fire 登记某时长立即就绪（对所有后续 After 生效）。
func (c *Clock) Fire(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fire == nil {
		c.fire = map[time.Duration]bool{}
	}
	c.fire[d] = true
}

// Advance 推进 Now（不影响已建立的 channel；仅用于事件时间 / 后续 After）。
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
