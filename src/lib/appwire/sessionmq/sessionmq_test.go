// sessionmq_test.go —— 组件事件 → 既有 MQ 主题的映射（61 载荷形状）。
package sessionmq

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/lsong98sh/chonkpilot-session/component"
)

type captured struct {
	subject string
	payload map[string]any
}

func newBus(t *testing.T) mq.Bus {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	return bus
}

func subscribe(t *testing.T, bus mq.Bus, subject string, out chan<- captured) {
	t.Helper()
	sub, err := bus.On(subject, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) == nil {
			select {
			case out <- captured{subject: subject, payload: m}:
			default:
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("订阅 %s: %v", subject, err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
}

func TestEmitMapsExistingTopics(t *testing.T) {
	bus := newBus(t)
	got := make(chan captured, 8)
	subscribe(t, bus, msgkeys.TopicSessionNew, got)
	subscribe(t, bus, msgkeys.TopicDataSessionTitleChanged, got)
	sink := New(bus, "ins-1")

	// ① DialogCreated → session-new（无 parent_session_id）
	sink.Emit(component.Event{Kind: component.EventDialogCreated, DialogID: "d1", SessionID: "s1"})
	// ② SessionCreated → session-new（带 parent_session_id）
	sink.Emit(component.Event{Kind: component.EventSessionCreated, DialogID: "d1", SessionID: "s2", ParentID: "s1"})
	// ③ TitleChanged → data-session-title-changed（不带 instance_id）
	sink.Emit(component.Event{Kind: component.EventTitleChanged, DialogID: "d1", SessionID: "s1", Title: "新标题"})
	// ④ 内容类事件 → 不投递
	sink.Emit(component.Event{Kind: component.EventMessageAppended, DialogID: "d1", SessionID: "s1", TurnID: "t1"})
	sink.Emit(component.Event{Kind: component.EventDialogDeleted, DialogID: "d1"})

	seen := make([]captured, 0, 3)
	deadline := time.After(2 * time.Second)
	for len(seen) < 3 {
		select {
		case c := <-got:
			seen = append(seen, c)
		case <-deadline:
			t.Fatalf("只收到 %d 条事件（期望 3）: %+v", len(seen), seen)
		}
	}

	// ①
	if seen[0].subject != msgkeys.TopicSessionNew {
		t.Fatalf("① 主题应为 session-new: %+v", seen[0])
	}
	if seen[0].payload["instance_id"] != "ins-1" || seen[0].payload["session_id"] != "s1" || seen[0].payload["dialog_id"] != "d1" {
		t.Fatalf("① 载荷不符 61: %+v", seen[0].payload)
	}
	if _, has := seen[0].payload["parent_session_id"]; has {
		t.Fatalf("① DialogCreated 不应带 parent_session_id: %+v", seen[0].payload)
	}
	// ②
	if seen[1].payload["session_id"] != "s2" || seen[1].payload["parent_session_id"] != "s1" {
		t.Fatalf("② 子会话载荷不符 61: %+v", seen[1].payload)
	}
	// ③
	if seen[2].subject != msgkeys.TopicDataSessionTitleChanged {
		t.Fatalf("③ 主题应为 data-session-title-changed: %+v", seen[2])
	}
	if seen[2].payload["session_id"] != "s1" || seen[2].payload["title"] != "新标题" || seen[2].payload["dialog_id"] != "d1" {
		t.Fatalf("③ 载荷不符 61: %+v", seen[2].payload)
	}
	if _, has := seen[2].payload["instance_id"]; has {
		t.Fatalf("③ 标题广播不应带 instance_id（61：全局）: %+v", seen[2].payload)
	}

	// ④ 不投递：再等一小段时间，通道应为空
	select {
	case c := <-got:
		t.Fatalf("内容类事件不应投递 MQ: %+v", c)
	case <-time.After(150 * time.Millisecond):
	}
}
