// chat Inbox 宿主适配器（MOD-1 A4-P1）：per-turn 内存 FIFO。
//
// **本文件不改生产路径**：生产 `turnCtx` 的中途输入仍走 `inboxCh` / `facade.InboxItem`；
// `chatInbox` 供引擎驱动时使用，生产投递点（`FeedInsert` / `FeedToolResult`）在 P3 切换时接入。
// 数据面行的转换复用 `chatadapter.go` 的 `ChatInboxFromFacade`（字段对齐，`Think`/`Effort` 原样 JSON）。
package server

import (
	"context"
	"sync"

	"github.com/chonkpilot/chonkpilot-chat"
)

// chatInbox 是 chat.Inbox 的宿主实现：per-turn 内存 FIFO（生产投递点在 P3 切换时接入）。
type chatInbox struct {
	mu sync.Mutex
	q  []chat.InboxItem
}

// newChatInbox 构造空队列（per-turn 一个实例）。
func newChatInbox() *chatInbox { return &chatInbox{} }

// Push 入队一条待处理发送（生产 FeedInsert / FeedToolResult 调用；P1 仅测试用）。
func (i *chatInbox) Push(item chat.InboxItem) {
	i.mu.Lock()
	i.q = append(i.q, item)
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

// Len 返回当前积压条数（诊断 / 测试用）。
func (i *chatInbox) Len() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.q)
}

// 编译期断言：内存 FIFO 满足 chat 的输入端口。
var _ chat.Inbox = (*chatInbox)(nil)
