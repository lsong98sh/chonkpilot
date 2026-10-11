package server

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestLimiterAcquireReleaseWakesWaiter 达上限时 acquire 阻塞，release 后唤醒。
func TestLimiterAcquireReleaseWakesWaiter(t *testing.T) {
	l := newLimiter(2)
	ctx := context.Background()
	if err := l.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := l.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- l.acquire(ctx) }()
	select {
	case <-done:
		t.Fatal("达上限时 acquire 不应立即返回")
	case <-time.After(50 * time.Millisecond):
	}
	l.release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("release 后 acquire 应成功: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("release 未唤醒等待者")
	}
	l.release()
	l.release() // 多余释放为安全空操作
}

// TestLimiterSetLimitWakesWaiter 提额（setLimit）应广播唤醒等待者。
func TestLimiterSetLimitWakesWaiter(t *testing.T) {
	l := newLimiter(1)
	ctx := context.Background()
	if err := l.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- l.acquire(ctx) }()
	select {
	case <-done:
		t.Fatal("上限=1 时第二个 acquire 不应立即返回")
	case <-time.After(50 * time.Millisecond):
	}
	l.setLimit(2)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("提额后 acquire 应成功: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("setLimit 未唤醒等待者")
	}
}

// TestLimiterAcquireCanceled 排队阶段 ctx 取消 → acquire 立即返回错误（不占名额）。
func TestLimiterAcquireCanceled(t *testing.T) {
	l := newLimiter(1)
	if err := l.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- l.acquire(ctx) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ctx 取消后 acquire 应返回错误")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消未唤醒 acquire")
	}
}

// TestLimiterRespectsCapUnderConcurrency 并发 acquire/release 下在跑数不得突破上限。
func TestLimiterRespectsCapUnderConcurrency(t *testing.T) {
	const limit = 4
	l := newLimiter(limit)
	var mu sync.Mutex
	var cur, max int
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.acquire(context.Background()); err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			cur++
			if cur > max {
				max = cur
			}
			mu.Unlock()
			time.Sleep(200 * time.Microsecond)
			mu.Lock()
			cur--
			mu.Unlock()
			l.release()
		}()
	}
	wg.Wait()
	if max > limit {
		t.Fatalf("并发上限被突破: max=%d limit=%d", max, limit)
	}
}
