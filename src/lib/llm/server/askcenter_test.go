package server

import (
	"context"
	"testing"

	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
)

// TestAskCenterSnapshotOrder：快照按 workdir + seq 升序（每 wd 有序队列）。
func TestAskCenterSnapshotOrder(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	mk := func(askID, wd string, seq int64) *askWaiter {
		return &askWaiter{
			askID: askID, instanceID: "ins-a", workDir: wd, seq: seq, receivedAt: seq,
			payload: []byte(`{"ask_id":"` + askID + `","question":"q"}`),
		}
	}
	s.mu.Lock()
	s.asks[instKey("ins-a", "a-w1-2")] = mk("a-w1-2", "W1", 2)
	s.asks[instKey("ins-a", "a-w2-1")] = mk("a-w2-1", "W2", 1)
	s.asks[instKey("ins-a", "a-w1-1")] = mk("a-w1-1", "W1", 1)
	s.mu.Unlock()

	got := s.askCenterSnapshot("", "")
	if len(got) != 3 {
		t.Fatalf("snapshot len=%d want 3", len(got))
	}
	want := []string{"a-w1-1", "a-w1-2", "a-w2-1"}
	for i, w := range want {
		if got[i]["ask_id"] != w {
			t.Fatalf("snapshot[%d]=%v want %s", i, got[i]["ask_id"], w)
		}
		if got[i]["work_dir"] == "" || got[i]["seq"] == nil {
			t.Fatalf("snapshot[%d] 缺 work_dir/seq: %v", i, got[i])
		}
	}
}

// TestAskCenterSubscribeSnapshot：订阅登记返回快照；退订后不再保留。
func TestAskCenterSubscribeSnapshot(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	s.mu.Lock()
	s.asks[instKey("ins-a", "a1")] = &askWaiter{
		askID: "a1", instanceID: "ins-a", workDir: "W1", seq: 1, receivedAt: 1,
		payload: []byte(`{"ask_id":"a1","question":"q1"}`),
	}
	s.mu.Unlock()

	v := &mq.Value{Payload: jb(map[string]any{"instance_id": "askc-1"})}
	if err := s.onAskCenterSubscribe(context.Background(), "", v); err != nil {
		t.Fatal(err)
	}
	res, _ := v.Result.(map[string]any)
	if res == nil || res["ok"] != true {
		t.Fatalf("subscribe result=%v", v.Result)
	}
	asks, _ := res["asks"].([]map[string]any)
	if len(asks) != 1 || asks[0]["ask_id"] != "a1" {
		t.Fatalf("subscribe asks=%v", res["asks"])
	}
	s.mu.Lock()
	if _, ok := s.askCenterSubs[""]; !ok {
		t.Fatal("订阅未登记")
	}
	s.mu.Unlock()

	v2 := &mq.Value{Payload: jb(map[string]any{"instance_id": "askc-1"})}
	if err := s.onAskCenterUnsubscribe(context.Background(), "", v2); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	if _, ok := s.askCenterSubs[""]; ok {
		t.Fatal("退订后订阅仍存在")
	}
	s.mu.Unlock()
}