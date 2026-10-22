// capability 根热重载监听（P6 2026-10-19）：fswatcher 监听 root 及子目录（默认递归 + 内置去抖），
// 去抖后触发重扫。简化自 llm/server capwatch：只关心「app 根内文件变化 → 重新 LoadExecTools →
// diff 注册」，不区分资产/工具语义（工具集 diff 在 registry.sync 完成）。
package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/sgtdi/fswatcher"
)

// watcher 监听单个 capability 根目录树。
type watcher struct {
	root     string
	w        fswatcher.Watcher
	cancel   context.CancelFunc
	onChange func()

	done      chan struct{}
	closeOnce sync.Once
}

// newWatcher 建立监听并启动事件循环；root 不存在/监听失败 → 返回错误（调用方仅记日志）。
func newWatcher(root string, debounce time.Duration, onChange func()) (*watcher, error) {
	ready := make(chan struct{})
	w, err := fswatcher.New(
		fswatcher.WithPath(root, fswatcher.WithDepth(fswatcher.WatchNested)),
		fswatcher.WithCooldown(debounce), // fswatcher 内置按路径去抖合并
		fswatcher.WithSeverity(fswatcher.SeverityNone),
		fswatcher.WithReadyChannel(ready),
	)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	wt := &watcher{
		root:     root,
		w:        w,
		cancel:   cancel,
		onChange: onChange,
		done:     make(chan struct{}),
	}
	go wt.run(ctx)
	go wt.loop()
	return wt, nil
}

// run 驱动 fswatcher 主循环（阻塞至 ctx 取消 / Close）；错误只记日志（下次事件/重启后自愈）。
func (wt *watcher) run(ctx context.Context) {
	if err := wt.w.Watch(ctx); err != nil && ctx.Err() == nil {
		slog.Warn("capability 监听错误", "root", wt.root, "err", err)
	}
}

// loop 消费已去抖合并的事件 → 触发重扫；Events 关闭即退出。
func (wt *watcher) loop() {
	events := wt.w.Events()
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
			wt.fire()
		case <-wt.done:
			return
		}
	}
}

// fire 触发一次重扫（fswatcher 已按 debounce 合并同路径事件，此处不再自管定时器）。
func (wt *watcher) fire() {
	select {
	case <-wt.done:
		return
	default:
	}
	if wt.onChange != nil {
		wt.onChange()
	}
}

// Close 停止监听（幂等）。
func (wt *watcher) Close() {
	if wt == nil {
		return
	}
	wt.closeOnce.Do(func() {
		close(wt.done)
		wt.cancel()
		wt.w.Close()
	})
}
