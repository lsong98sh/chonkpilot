// data-session-title-changed 广播白盒（61 §3.2 · G-48 ⑦ / #12，2026-09-26 新增）：
// `data-session-title` **写库成功后**服务方广播 `{session_id, title}`（**不带 `instance_id`**
// = 全局投递 → 另一 instance 的对话窗口也能收到，即本主题用途）；失败路径**不广播**。
//
// 口径（判定是否带 instance_id）以「对话窗口 = 另一 instance 也能收到」为准 → 见 61 §3.2：
// 每个窗口的桥各自转发 → 所有窗口都收到（与 `data-<domain>-refresh` 家族按 `instance_id`
// 过滤、「只到来源窗口」正好相反）。
package persist

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// TestSessionTitleBroadcastMQ：改名成功 → 总线收到 data-session-title-changed
// `{session_id, title}` 且**不含 instance_id**；改名失败（会话不存在）→ 无事件。
func TestSessionTitleBroadcastMQ(t *testing.T) {
	// 工作目录先于服务创建：清理顺序 LIFO → data.Reset（关连接）先于 TempDir RemoveAll 执行。
	wd := t.TempDir()
	s := newSweepTestService(t)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(s.Stop)

	const (
		inst    = "ins-title"
		sid     = "mw-title-broadcast"
		newTi   = "广播后的标题"
		missing = "no-such-session"
	)
	s.onInstanceRegister(instanceRegister, instancePayload(inst, wd, ""))

	got := make(chan map[string]any, 4)
	sub, err := s.Bus.On("data-session-title-changed", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var ev map[string]any
		if json.Unmarshal(v.Payload, &ev) == nil {
			select {
			case got <- ev:
			default:
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("订阅 data-session-title-changed: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	// 进程内总线 Emit 同步派发：Wait() 返回即订阅者（含 persist 路由 handler）已执行完。
	emit := func(subject string, payload map[string]any) {
		t.Helper()
		b, _ := json.Marshal(payload)
		_ = s.Bus.Emit(context.Background(), subject, b).Wait()
	}

	// 前置：幂等建会话（标题初值 = session_id，见 SessionEnsure）
	emit("data-session-ensure-session", map[string]any{"instance_id": inst, "session_id": sid})

	// ① 改名成功 → 广播 {session_id, title}，**不带 instance_id**（全局投递）
	emit("data-session-title", map[string]any{"instance_id": inst, "id": sid, "title": newTi})
	select {
	case ev := <-got:
		if ev["session_id"] != sid || ev["title"] != newTi {
			t.Fatalf("广播载荷应为 {session_id:%q, title:%q}（61 §3.2）: %v", sid, newTi, ev)
		}
		if _, has := ev["instance_id"]; has {
			t.Fatalf("data-session-title-changed 不应带 instance_id（61 §3.2：全局投递）: %v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("data-session-title 成功后未广播 data-session-title-changed")
	}

	// ② 失败路径（会话不存在）→ 不广播
	emit("data-session-title", map[string]any{"instance_id": inst, "id": missing, "title": "x"})
	select {
	case ev := <-got:
		t.Fatalf("改名失败路径不应广播: %v", ev)
	case <-time.After(200 * time.Millisecond):
	}
}
