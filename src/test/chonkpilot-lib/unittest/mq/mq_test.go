// 内部内存 MQ 单测（对齐 11-MQ与消息：Emit/On 收发 / 通配 / 并发 / panic 恢复；
// 2026-09-03 去 NATS：mq 仅进程内内存实现，无构建标签分支）。
package mq_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// recv 收集消息（同步派发 → 轮询等待）。
type recv struct {
	mu   sync.Mutex
	subs map[string][]string // subject → payloads
}

func (r *recv) add(subject string, payload []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subs[subject] = append(r.subs[subject], string(payload))
}

func (r *recv) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, ps := range r.subs {
		n += len(ps)
	}
	return n
}

func (r *recv) subjectCount(subject string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.subs[subject])
}

func waitCount(t *testing.T, r *recv, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r.count() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("want %d messages, got %d (subs=%v)", want, r.count(), r.subs)
}

// onCollect 以 v2 On 订阅并把载荷交给收集器（仅测试用）。
func onCollect(bus mq.Bus, subject string, add func(subject string, payload []byte)) (mq.Sub, error) {
	return bus.On(subject, 0, func(_ context.Context, subject string, v *mq.Value) error {
		add(subject, v.Payload)
		return nil
	})
}

// emitFire 以 v2 Emit fire-and-forget 发布（仅测试用；忽略派发结果）。
func emitFire(bus mq.Bus, subject string, payload any) {
	_ = bus.Emit(context.Background(), subject, payload)
}

func TestPublishSubscribeExact(t *testing.T) {
	bus, err := mq.New(mq.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()

	r := &recv{subs: map[string][]string{}}
	if _, err := onCollect(bus, "a.b.c", r.add); err != nil {
		t.Fatal(err)
	}
	emitFire(bus, "a.b.c", []byte("hello"))
	waitCount(t, r, 1)
	if got := r.subs["a.b.c"]; len(got) != 1 || got[0] != "hello" {
		t.Fatalf("got %v", got)
	}
}

func TestWildcardGt(t *testing.T) {
	bus, _ := mq.New(mq.Options{})
	defer bus.Close()
	r := &recv{subs: map[string][]string{}}
	_, _ = onCollect(bus, "chonk.mcp.notify.>", r.add)

	emitFire(bus, "chonk.mcp.notify.tools.call", []byte("1"))
	emitFire(bus, "chonk.mcp.notify.tools.call.reply", []byte("2"))
	emitFire(bus, "chonk.mcp.notify.tasks.done", []byte("3"))
	waitCount(t, r, 3)
	if r.subjectCount("chonk.mcp.notify.tools.call") != 1 ||
		r.subjectCount("chonk.mcp.notify.tools.call.reply") != 1 ||
		r.subjectCount("chonk.mcp.notify.tasks.done") != 1 {
		t.Fatalf("gt wildcard mismatch: %v", r.subs)
	}
}

func TestWildcardStar(t *testing.T) {
	bus, _ := mq.New(mq.Options{})
	defer bus.Close()
	r := &recv{subs: map[string][]string{}}
	_, _ = onCollect(bus, "chonk.mcp.notify.tools.*", r.add)

	emitFire(bus, "chonk.mcp.notify.tools.call", []byte("1")) // 命中（单段）
	emitFire(bus, "chonk.mcp.notify.tools.call.reply", []byte("2"))
	waitCount(t, r, 1)
	if r.subjectCount("chonk.mcp.notify.tools.call") != 1 {
		t.Fatalf("star wildcard mismatch: %v", r.subs)
	}
}

func TestSubscribeThenUnsubscribe(t *testing.T) {
	bus, _ := mq.New(mq.Options{})
	defer bus.Close()
	r := &recv{subs: map[string][]string{}}
	sub, _ := onCollect(bus, "t.1", r.add)

	emitFire(bus, "t.1", []byte("1"))
	waitCount(t, r, 1)
	if err := sub.Unsubscribe(); err != nil {
		t.Fatal(err)
	}
	emitFire(bus, "t.1", []byte("2"))
	time.Sleep(100 * time.Millisecond)
	if got := r.subjectCount("t.1"); got != 1 {
		t.Fatalf("after unsubscribe got %d (want 1)", got)
	}
}

func TestConcurrentPublish(t *testing.T) {
	bus, _ := mq.New(mq.Options{})
	defer bus.Close()
	var n int32
	_, _ = onCollect(bus, "conc.>", func(string, []byte) { atomic.AddInt32(&n, 1) })

	const workers, each = 8, 100
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				emitFire(bus, "conc.t", []byte(strings.Repeat("x", 100)))
			}
		}(w)
	}
	wg.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&n) < workers*each {
		time.Sleep(10 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&n); got != workers*each {
		t.Fatalf("want %d got %d", workers*each, got)
	}
}

func TestHandlerPanicRecovered(t *testing.T) {
	bus, _ := mq.New(mq.Options{})
	defer bus.Close()
	ok := make(chan struct{}, 1)
	_, _ = onCollect(bus, "p.1", func(string, []byte) { panic("boom") })
	_, _ = onCollect(bus, "p.1", func(string, []byte) { ok <- struct{}{} })

	// fire-and-forget：panic 收集进 Future.Errors 但不中断后续 handler（不检查 Err，对齐事件语义）
	_ = bus.Emit(context.Background(), "p.1", nil)
	select {
	case <-ok:
	case <-time.After(2 * time.Second):
		t.Fatal("second handler not called after first panicked")
	}
}

func TestSubscribeAfterClose(t *testing.T) {
	bus, _ := mq.New(mq.Options{})
	_ = bus.Close()
	if _, err := bus.On("x", 0, func(context.Context, string, *mq.Value) error { return nil }); err == nil {
		t.Fatal("want error subscribing to closed bus")
	}
}
