//go:build windows

package host

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// SANDBOX-WRITE-1 P3：`workdirEnsurer` = `plugin/v1/workdir.ensure` 的可测核心
// （fake 窗口工厂 = `open`，fake 实例视图 = `registered`，fake 事件源 = 广播通道）。
//
// 异步改造 §10.1（2026-11-06）：等待改为「实例视图变更事件 + 单个超时 timer」，测试不再注入
// sleep/poll；事件唤醒路径由测试自行 `notify()` 驱动。
func newTestEnsurer(open func(string, bool) error, registered func(string) (string, bool), changes *instanceChangeBroadcaster) *workdirEnsurer {
	e := &workdirEnsurer{
		open:       open,
		registered: registered,
		timeout:    200 * time.Millisecond,
	}
	if changes != nil {
		e.changed = changes.changed
	}
	return e
}

// 已在册 → 直接返回主实例 id，不建窗（opened=false）。
func TestWorkdirEnsureAlreadyOpen(t *testing.T) {
	opened := 0
	e := newTestEnsurer(func(string, bool) error { opened++; return nil },
		func(string) (string, bool) { return "ins-main", true }, nil)

	id, isOpen, err := e.ensure(context.Background(), `E:\proj\a`, true)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if id != "ins-main" || isOpen {
		t.Fatalf("id=%q opened=%v", id, isOpen)
	}
	if opened != 0 {
		t.Fatalf("已在册不得建窗，open 调用 %d 次", opened)
	}
}

// 未在册 → 隐藏打开（hidden 原样透传）→ 等待 instance-register 事件 → opened=true。
// 断言「事件唤醒」而非超时命中：timeout 取 2s，事件在 20ms 内到达，总耗时须明显小于超时。
func TestWorkdirEnsureHiddenOpen(t *testing.T) {
	changes := newInstanceChangeBroadcaster()
	var mu sync.Mutex
	registered := false
	var gotDir string
	var gotHidden bool
	e := newTestEnsurer(
		func(dir string, hidden bool) error {
			mu.Lock()
			gotDir, gotHidden = dir, hidden
			mu.Unlock()
			// 模拟「建窗后经桥异步发 instance-register」：登记与广播稍后到达。
			go func() {
				time.Sleep(20 * time.Millisecond)
				mu.Lock()
				registered = true
				mu.Unlock()
				changes.notify()
			}()
			return nil
		},
		func(string) (string, bool) {
			mu.Lock()
			defer mu.Unlock()
			if registered {
				return "ins-hidden", true
			}
			return "", false
		}, changes)
	e.timeout = 2 * time.Second

	start := time.Now()
	id, isOpen, err := e.ensure(context.Background(), `E:\proj\a`, true)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if id != "ins-hidden" || !isOpen {
		t.Fatalf("id=%q opened=%v", id, isOpen)
	}
	if gotDir != `E:\proj\a` || !gotHidden {
		t.Fatalf("open 入参不符: dir=%q hidden=%v", gotDir, gotHidden)
	}
	if elapsed >= time.Second {
		t.Fatalf("应由事件唤醒而非等到超时，耗时 %v", elapsed)
	}

	// hidden=false 原样透传（可见打开；登记同步命中，不进入等待）。
	pre := true
	e2 := newTestEnsurer(func(_ string, hidden bool) error { gotHidden = hidden; return nil },
		func(string) (string, bool) {
			if pre {
				pre = false
				return "", false
			}
			return "ins-visible", true
		}, changes)
	if _, _, err := e2.ensure(context.Background(), `E:\proj\b`, false); err != nil {
		t.Fatalf("ensure visible: %v", err)
	}
	if gotHidden {
		t.Fatal("hidden=false 应透传")
	}
}

// 等不到 instance-register → **明确错误**（不静默成功）。
func TestWorkdirEnsureTimeout(t *testing.T) {
	changes := newInstanceChangeBroadcaster()
	opened := 0
	e := newTestEnsurer(func(string, bool) error { opened++; return nil },
		func(string) (string, bool) { return "", false }, changes)
	e.timeout = 80 * time.Millisecond

	id, isOpen, err := e.ensure(context.Background(), `E:\proj\a`, true)
	if err == nil {
		t.Fatal("超时应回错误")
	}
	if !strings.Contains(err.Error(), "超时") {
		t.Fatalf("错误文案应含「超时」: %v", err)
	}
	if id != "" {
		t.Fatalf("超时不得返回实例 id: %q", id)
	}
	if !isOpen || opened != 1 {
		t.Fatalf("应尝试建窗 1 次: opened=%v calls=%d", isOpen, opened)
	}
}

// ctx 取消 → 及时返回明确取消错误（不等到 timeout）。
func TestWorkdirEnsureCanceled(t *testing.T) {
	changes := newInstanceChangeBroadcaster()
	e := newTestEnsurer(func(string, bool) error { return nil },
		func(string) (string, bool) { return "", false }, changes)
	e.timeout = 2 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, _, err := e.ensure(ctx, `E:\proj\a`, true)
	if err == nil || !strings.Contains(err.Error(), "已取消") {
		t.Fatalf("ctx 取消应回明确错误: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("ctx 取消应及时返回，耗时 %v", elapsed)
	}
}

// 建窗失败 → 明确错误，且不再等待。
func TestWorkdirEnsureOpenFails(t *testing.T) {
	changes := newInstanceChangeBroadcaster()
	e := newTestEnsurer(func(string, bool) error { return errors.New("无效的项目目录") },
		func(string) (string, bool) { return "", false }, changes)
	e.timeout = 2 * time.Second

	start := time.Now()
	if _, _, err := e.ensure(context.Background(), `E:\proj\a`, true); err == nil {
		t.Fatal("建窗失败应回错误")
	}
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("建窗失败不应进入等待，耗时 %v", elapsed)
	}
}

// 空 work_dir → 参数错误（不建窗）。
func TestWorkdirEnsureEmptyDir(t *testing.T) {
	opened := 0
	e := newTestEnsurer(func(string, bool) error { opened++; return nil },
		func(string) (string, bool) { return "", false }, nil)
	if _, _, err := e.ensure(context.Background(), "  ", true); err == nil {
		t.Fatal("空 work_dir 须报错")
	}
	if opened != 0 {
		t.Fatal("空 work_dir 不得建窗")
	}
}

// 并发同 work_dir → 只建一次窗（门面内串行化「查 → 开 → 等」）。
func TestWorkdirEnsureConcurrentSingleOpen(t *testing.T) {
	changes := newInstanceChangeBroadcaster()
	var mu sync.Mutex
	openCalls := 0
	registered := false
	e := newTestEnsurer(
		func(string, bool) error {
			mu.Lock()
			openCalls++
			registered = true
			mu.Unlock()
			return nil
		},
		func(string) (string, bool) {
			mu.Lock()
			defer mu.Unlock()
			if registered {
				return "ins-main", true
			}
			return "", false
		}, changes)

	var wg sync.WaitGroup
	results := make([]string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, _, err := e.ensure(context.Background(), `E:\proj\a`, true)
			if err != nil {
				t.Errorf("ensure[%d]: %v", i, err)
			}
			results[i] = id
		}(i)
	}
	wg.Wait()
	if openCalls != 1 {
		t.Fatalf("并发 ensure 应只建一次窗，实际 %d", openCalls)
	}
	for i, id := range results {
		if id != "ins-main" {
			t.Fatalf("results[%d] = %q", i, id)
		}
	}
}

// 广播语义：notify 后旧通道关闭、新通道未关闭；多次 notify 不 panic。
func TestInstanceChangeBroadcasterCloseAndReplace(t *testing.T) {
	b := newInstanceChangeBroadcaster()
	first := b.changed()
	b.notify()
	select {
	case <-first:
	default:
		t.Fatal("notify 应关闭上一代通道")
	}
	second := b.changed()
	if second == first {
		t.Fatal("notify 应换新通道")
	}
	select {
	case <-second:
		t.Fatal("新一代通道不应已关闭")
	default:
	}
	b.notify()
	select {
	case <-second:
	default:
		t.Fatal("第二次 notify 应关闭新一代通道")
	}
}