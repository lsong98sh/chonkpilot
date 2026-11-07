//go:build windows

// projectclosed.go —— 项目集合变更的**轻量进程内广播**（异步改造 §10.1，2026-11-06）。
//
// 用途：无托盘退回路径 `waitAllProjectsClosed` 原来每 500ms 轮询 `len(env.Projects())`；
// 改为「窗口关闭事件唤醒 + 每次唤醒重查项目数」。窗口关闭信号来自 `gui.Window.Done()`
// （`windowHost.finish` 收尾后 close），由本包 `watch(done)` 转成一次广播——**不新增 MQ 主题**，
// 也不依赖 framework/host 内部事件。
package host

import "sync"

// projectCloseTracker 是项目集合可能变化的 close-and-replace 广播。
//
// 等待方必须**先取通道、再查项目数**：关闭若发生在取通道前 → 查询看到 0；发生在取通道后 →
// 通道被 close。两者都不会丢唤醒（与 workdirensure 的实例视图广播同款模式）。
type projectCloseTracker struct {
	mu sync.Mutex
	ch chan struct{}
}

// newProjectCloseTracker 构造广播（初值 = 永不关闭的通道）。
func newProjectCloseTracker() *projectCloseTracker {
	return &projectCloseTracker{ch: make(chan struct{})}
}

// notify 广播一次「项目集合可能已变」。
func (t *projectCloseTracker) notify() {
	if t == nil {
		return
	}
	t.mu.Lock()
	close(t.ch)
	t.ch = make(chan struct{})
	t.mu.Unlock()
}

// changed 返回当前这一代变更通道。
func (t *projectCloseTracker) changed() <-chan struct{} {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ch
}

// watch 监听一个窗口的关闭信号并广播；done 关闭即收尾（goroutine 退出）。
// 多个窗口各一条 watcher，开销 = 每条一个阻塞在 Done 上的 goroutine（窗口关闭即结束）。
func (t *projectCloseTracker) watch(done <-chan struct{}) {
	if t == nil || done == nil {
		return
	}
	go func() {
		<-done
		t.notify()
	}()
}

// waitAllProjectsClosed 是无托盘时的退回路径：等到**所有**项目主窗口关闭（或退出信号）后返回。
//
// 事件驱动（不再 500ms ticker）：每一轮先取变更通道快照，再查项目数；项目数为 0 → 返回，
// 否则阻塞在 `select(exitCh / changed)`。任意窗口关闭都会唤醒并重查；退出信号优先结束。
func waitAllProjectsClosed(projects func() int, changed func() <-chan struct{}, exitCh <-chan struct{}) {
	for {
		// 顺序关键：先取通道、再查状态（防丢唤醒；见 projectCloseTracker 注释）。
		var ch <-chan struct{}
		if changed != nil {
			ch = changed()
		}
		if projects == nil || projects() == 0 {
			return
		}
		select {
		case <-exitCh:
			return
		case <-ch:
			// 项目集合变更（某个主窗口关闭）→ 重查。
		}
	}
}