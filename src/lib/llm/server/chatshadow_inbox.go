// chat Inbox 宿主适配器（MOD-1 A4-P1）：per-turn 内存 FIFO。
//
// **本文件不改生产路径**：生产 `turnCtx` 的中途输入仍走 `inboxCh` / `facade.InboxItem`；
// `chatInbox` 供引擎驱动时使用（生产投递点 = `engineRouteInput` / `engineRouteInsert`）。
package server

import (
	"context"
	"sync"

	"github.com/lsong98sh/chonkpilot-agent/chat"
)

// chatInbox 是 chat.Inbox 的宿主实现：per-turn 内存 FIFO（生产投递点在 P3 切换时接入）。
//
// 并发模型：Push 入队并**广播**唤醒（关闭当前通知 channel）；Wait 无输入时阻塞在通知
// channel 或 ctx；Poll 保持非阻塞（step 边界取件 / 兼容旧调用点）。两者共用同一队列，
// 因此不会因取件方式不同而丢项 / 乱序。
type chatInbox struct {
	mu     sync.Mutex
	q      []chat.InboxItem
	notify chan struct{} // 关闭即广播（下次 Wait 前重建）
}

// newChatInbox 构造空队列（per-turn 一个实例）。
func newChatInbox() *chatInbox { return &chatInbox{} }

// wakeLocked 广播「有新输入」：关闭当前通知 channel，等待者被唤醒后自行重建。
// 调用方须持 i.mu。
func (i *chatInbox) wakeLocked() {
	if i.notify != nil {
		close(i.notify)
		i.notify = nil
	}
}

// Push 入队一条待处理发送（生产 FeedInsert / FeedToolResult 调用）。
func (i *chatInbox) Push(item chat.InboxItem) {
	i.mu.Lock()
	i.q = append(i.q, item)
	i.wakeLocked()
	i.mu.Unlock()
}

// Poll 非阻塞 FIFO 出队：ok=false = 当前无输入。
func (i *chatInbox) Poll(context.Context) (chat.InboxItem, bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if len(i.q) == 0 {
		return chat.InboxItem{}, false, nil
	}
	item := i.q[0]
	i.q = i.q[1:]
	return item, true, nil
}

// Wait 阻塞 FIFO 出队：有输入立即返回；无输入阻塞到 Push 唤醒或 ctx 结束。
func (i *chatInbox) Wait(ctx context.Context) (chat.InboxItem, bool, error) {
	for {
		i.mu.Lock()
		if len(i.q) > 0 {
			item := i.q[0]
			i.q = i.q[1:]
			i.mu.Unlock()
			return item, true, nil
		}
		if i.notify == nil {
			i.notify = make(chan struct{})
		}
		ch := i.notify
		i.mu.Unlock()

		select {
		case <-ch:
			// 有新输入（或一次广播）→ 回到队首复查。
		case <-ctx.Done():
			return chat.InboxItem{}, false, ctx.Err()
		}
	}
}

// Len 返回当前积压条数（诊断 / 测试用）。
func (i *chatInbox) Len() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.q)
}

// 编译期断言：内存 FIFO 满足 chat 的输入端口与阻塞取件扩展。
var (
	_ chat.Inbox       = (*chatInbox)(nil)
	_ chat.InboxWaiter = (*chatInbox)(nil)
)
