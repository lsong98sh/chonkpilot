//go:build windows

package host

import (
	"sync/atomic"
	"testing"
	"time"
)

// 项目数已为 0 → 立即返回（不等待事件）。
func TestWaitAllProjectsClosedImmediate(t *testing.T) {
	tracker := newProjectCloseTracker()
	done := make(chan struct{})
	go func() {
		waitAllProjectsClosed(func() int { return 0 }, tracker.changed, done)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("项目数为 0 时应立即返回")
	}
}

// 项目关闭事件唤醒：关闭最后一个项目后立即返回（不等 500ms 轮询）。
func TestWaitAllProjectsClosedEventWakes(t *testing.T) {
	tracker := newProjectCloseTracker()
	var count int32 = 1
	exit := make(chan struct{})
	done := make(chan struct{})
	go func() {
		waitAllProjectsClosed(func() int { return int(atomic.LoadInt32(&count)) }, tracker.changed, exit)
		close(done)
	}()

	// 未关闭 → 不返回（事件驱动，无轮询）。
	select {
	case <-done:
		t.Fatal("仍有项目时不得返回")
	case <-time.After(30 * time.Millisecond):
	}
	atomic.StoreInt32(&count, 0)
	tracker.notify()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("项目关闭事件应唤醒返回")
	}
}

// 退出信号优先：项目仍在也返回。
func TestWaitAllProjectsClosedExitSignal(t *testing.T) {
	tracker := newProjectCloseTracker()
	exit := make(chan struct{})
	done := make(chan struct{})
	go func() {
		waitAllProjectsClosed(func() int { return 3 }, tracker.changed, exit)
		close(done)
	}()
	close(exit)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("exitCh 关闭应结束等待")
	}
}

// 广播语义：close-and-replace，多次 notify 不 panic。
func TestProjectCloseTrackerCloseAndReplace(t *testing.T) {
	tracker := newProjectCloseTracker()
	first := tracker.changed()
	tracker.notify()
	select {
	case <-first:
	default:
		t.Fatal("notify 应关闭上一代通道")
	}
	second := tracker.changed()
	if second == first {
		t.Fatal("notify 应换新通道")
	}
	tracker.notify()
	select {
	case <-second:
	default:
		t.Fatal("第二次 notify 应关闭新一代通道")
	}
}

// watch：窗口 Done 关闭 → 广播。
func TestProjectCloseTrackerWatch(t *testing.T) {
	tracker := newProjectCloseTracker()
	ch := tracker.changed()
	done := make(chan struct{})
	tracker.watch(done)
	close(done)
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("窗口关闭应触发广播")
	}
}