//go:build windows

package host

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// SANDBOX-WRITE-1 P3：`workdirEnsurer` = `plugin/v1/workdir.ensure` 的可测核心
// （fake 窗口工厂 = `open`，fake 实例视图 = `registered`）。
func newTestEnsurer(open func(string, bool) error, registered func(string) (string, bool)) *workdirEnsurer {
	return &workdirEnsurer{
		open:       open,
		registered: registered,
		timeout:    200 * time.Millisecond,
		poll:       100 * time.Millisecond,
		sleep:      func(time.Duration) {}, // 测试不真等
	}
}

// 已在册 → 直接返回主实例 id，不建窗（opened=false）。
func TestWorkdirEnsureAlreadyOpen(t *testing.T) {
	opened := 0
	e := newTestEnsurer(func(string, bool) error { opened++; return nil },
		func(string) (string, bool) { return "ins-main", true })

	id, isOpen, err := e.ensure(`E:\proj\a`, true)
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

// 未在册 → 隐藏打开（hidden 原样透传）→ 轮询到 instance-register → opened=true。
func TestWorkdirEnsureHiddenOpen(t *testing.T) {
	var mu sync.Mutex
	registered := false
	polls := 0
	var gotDir string
	var gotHidden bool
	e := newTestEnsurer(
		func(dir string, hidden bool) error {
			mu.Lock()
			gotDir, gotHidden = dir, hidden
			registered = true // 模拟「建窗即经桥发 instance-register」
			mu.Unlock()
			return nil
		},
		func(string) (string, bool) {
			mu.Lock()
			defer mu.Unlock()
			polls++
			// 第 1 次轮询模拟「登记尚未到达」，第 2 次命中。
			if registered && polls >= 2 {
				return "ins-hidden", true
			}
			return "", false
		})
	e.timeout, e.poll = 300*time.Millisecond, 100*time.Millisecond // 3 次轮询

	id, isOpen, err := e.ensure(`E:\proj\a`, true)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if id != "ins-hidden" || !isOpen {
		t.Fatalf("id=%q opened=%v", id, isOpen)
	}
	if gotDir != `E:\proj\a` || !gotHidden {
		t.Fatalf("open 入参不符: dir=%q hidden=%v", gotDir, gotHidden)
	}
	if polls < 2 {
		t.Fatalf("应轮询直到命中，polls=%d", polls)
	}

	// hidden=false 原样透传（可见打开）。
	pre := true // 首次查询 = 未在册（触发建窗），其后命中
	e2 := newTestEnsurer(func(_ string, hidden bool) error { gotHidden = hidden; return nil },
		func(string) (string, bool) {
			if pre {
				pre = false
				return "", false
			}
			return "ins-visible", true
		})
	if _, _, err := e2.ensure(`E:\proj\b`, false); err != nil {
		t.Fatalf("ensure visible: %v", err)
	}
	if gotHidden {
		t.Fatal("hidden=false 应透传")
	}
}

// 等不到 instance-register → **明确错误**（不静默成功）。
func TestWorkdirEnsureTimeout(t *testing.T) {
	opened := 0
	e := newTestEnsurer(func(string, bool) error { opened++; return nil },
		func(string) (string, bool) { return "", false })
	e.timeout, e.poll = 200*time.Millisecond, 100*time.Millisecond

	id, isOpen, err := e.ensure(`E:\proj\a`, true)
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

// 建窗失败 → 明确错误，且不再轮询。
func TestWorkdirEnsureOpenFails(t *testing.T) {
	slept := 0
	e := newTestEnsurer(func(string, bool) error { return errors.New("无效的项目目录") },
		func(string) (string, bool) { return "", false })
	e.sleep = func(time.Duration) { slept++ }

	if _, _, err := e.ensure(`E:\proj\a`, true); err == nil {
		t.Fatal("建窗失败应回错误")
	}
	if slept != 0 {
		t.Fatalf("建窗失败不应进入轮询: sleep %d 次", slept)
	}
}

// 空 work_dir → 参数错误（不建窗）。
func TestWorkdirEnsureEmptyDir(t *testing.T) {
	opened := 0
	e := newTestEnsurer(func(string, bool) error { opened++; return nil },
		func(string) (string, bool) { return "", false })
	if _, _, err := e.ensure("  ", true); err == nil {
		t.Fatal("空 work_dir 须报错")
	}
	if opened != 0 {
		t.Fatal("空 work_dir 不得建窗")
	}
}

// 并发同 work_dir → 只建一次窗（门面内串行化「查 → 开 → 等」）。
func TestWorkdirEnsureConcurrentSingleOpen(t *testing.T) {
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
		})

	var wg sync.WaitGroup
	results := make([]string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, _, err := e.ensure(`E:\proj\a`, true)
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
