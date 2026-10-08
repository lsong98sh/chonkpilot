// notice_test.go — 压缩进度通知（OP-03，2026-10-06）：worker 处理前后经**既有通知面**
// tool-notify 各发一次 notice=compress-start / compress-done（不新增主题，61-消息一览 §4.3）。
package compress

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-plugin"
)

// TestCompressProgressNotices：一次 session-compress → 收到 compress-start 与 compress-done
// 两条 tool-notify（字段齐备：instance_id/session_id/turn_id/notice/message/message_id/plugin）。
func TestCompressProgressNotices(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	defer bus.Close()

	var mu sync.Mutex
	var seen []map[string]any
	if _, err := bus.On("tool-notify", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil {
			return nil
		}
		if m["plugin"] != "compress" { // 只关心本插件投递
			return nil
		}
		mu.Lock()
		seen = append(seen, m)
		mu.Unlock()
		return nil
	}); err != nil {
		t.Fatalf("subscribe tool-notify: %v", err)
	}

	c := New(DefaultOptions(), inline.New(bus))
	if err := c.Start(plugin.Deps{Bus: bus, Logf: t.Logf}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	body, _ := json.Marshal(map[string]any{
		"instance_id": "ins-n", "work_dir": `C:\ws`, "data_dir": `C:\ws\.chonkpilot`,
		"session": "s-n", "snapshot_turn": "t1",
	})
	bus.Emit(context.Background(), "session-compress", body).Wait()

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return hasNotice(seen, "compress-start") && hasNotice(seen, "compress-done")
	}, 2*time.Second, "未收到 compress-start / compress-done")

	mu.Lock()
	defer mu.Unlock()
	for _, m := range seen {
		for _, k := range []string{"instance_id", "session_id", "turn_id", "notice", "message", "message_id"} {
			if m[k] == nil || m[k] == "" {
				t.Fatalf("tool-notify 缺字段 %s：%+v", k, m)
			}
		}
		if m["instance_id"] != "ins-n" || m["session_id"] != "s-n" {
			t.Fatalf("实例/会话字段不符：%+v", m)
		}
	}
}

func hasNotice(ms []map[string]any, notice string) bool {
	for _, m := range ms {
		if m["notice"] == notice {
			return true
		}
	}
	return false
}
