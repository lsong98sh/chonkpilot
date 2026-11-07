// chat 宿主接线（MOD-1 A4-P1）：Inbox FIFO。
package server

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/lsong98sh/chonkpilot-agent/chat"
)

// TestChatInboxAdapter：FIFO / 空队列非阻塞 / Len / 并发 Push。
func TestChatInboxAdapter(t *testing.T) {
	q := newChatInbox()
	if q.Len() != 0 {
		t.Fatalf("初始 Len=%d want 0", q.Len())
	}
	if _, ok, err := q.Poll(context.Background()); ok || err != nil {
		t.Fatalf("空队列 Poll 应非阻塞返回 false/nil：%v %v", ok, err)
	}

	q.Push(chat.InboxItem{ID: "a"})
	q.Push(chat.InboxItem{ID: "b"})
	if q.Len() != 2 {
		t.Fatalf("Len=%d want 2", q.Len())
	}
	for _, want := range []string{"a", "b"} {
		item, ok, err := q.Poll(context.Background())
		if err != nil || !ok || item.ID != want {
			t.Fatalf("FIFO 出队错：item=%+v ok=%v err=%v want=%s", item, ok, err, want)
		}
	}
	if _, ok, _ := q.Poll(context.Background()); ok {
		t.Fatal("耗尽后 Poll 应返回 false")
	}

	const n = 32
	var wg sync.WaitGroup
	for k := 0; k < n; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			q.Push(chat.InboxItem{ID: fmt.Sprintf("i-%d", k)})
		}(k)
	}
	wg.Wait()
	if q.Len() != n {
		t.Fatalf("并发 Push 后 Len=%d want %d", q.Len(), n)
	}
	seen := make(map[string]bool, n)
	for k := 0; k < n; k++ {
		item, ok, err := q.Poll(context.Background())
		if err != nil || !ok {
			t.Fatalf("第 %d 次 Poll：ok=%v err=%v", k, ok, err)
		}
		seen[item.ID] = true
	}
	if len(seen) != n {
		t.Fatalf("并发 Push 丢项：len=%d want %d", len(seen), n)
	}
}

// TestChatInboxWait：Wait 空队列时阻塞（不推进时间），Push 立即唤醒；ctx 取消可退出。
func TestChatInboxWait(t *testing.T) {
	q := newChatInbox()
	type got struct {
		item chat.InboxItem
		ok   bool
		err  error
	}
	ch := make(chan got, 1)
	go func() {
		item, ok, err := q.Wait(context.Background())
		ch <- got{item: item, ok: ok, err: err}
	}()

	select {
	case <-ch:
		t.Fatal("空队列 Wait 应立即阻塞（不应返回）")
	case <-time.After(50 * time.Millisecond):
	}

	q.Push(chat.InboxItem{ID: "w1"})
	select {
	case g := <-ch:
		if g.err != nil || !g.ok || g.item.ID != "w1" {
			t.Fatalf("Push 应唤醒 Wait 并返回该条：%+v", g)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Push 未唤醒 Wait")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok, err := q.Wait(ctx); ok || err == nil {
		t.Fatalf("ctx 取消应返回 ok=false + err：ok=%v err=%v", ok, err)
	}
}
