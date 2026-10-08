// 回环应答判定白盒（handle 入口，item：顶层 ok 误判）：
// persist 的 reply/fail 与请求**同主题**发布，需在入口跳过；但**业务载荷恰好含顶层 `ok`
// 的请求**不得被误判为应答丢弃（否则不 reply → 调用方 Promise 永不 resolve）。
package persist

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// TestIsLoopbackReply：仅「顶层 ok + 应答专属信封键 result/error」判为回环；含顶层 ok
// 但无 result/error 的业务请求不判为回环。
func TestIsLoopbackReply(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"成功应答（ok+result）", `{"req_id":"r1","ok":true,"result":{"list":[]}}`, true},
		{"失败应答（ok+error）", `{"req_id":"r1","ok":false,"error":"boom"}`, true},
		{"成功应答 result=null", `{"req_id":"r1","ok":true,"result":null}`, true},
		{"请求：顶层 ok 无应答键", `{"req_id":"r1","instance_id":"i","ok":true}`, false},
		{"请求：顶层 ok=false 无应答键", `{"req_id":"r1","instance_id":"i","ok":false,"data":{}}`, false},
		{"普通请求", `{"req_id":"r1","instance_id":"i","id":"x"}`, false},
		{"空载荷", `{}`, false},
		{"非法 JSON", `not-json`, false},
	}
	for _, c := range cases {
		if got := isLoopbackReply([]byte(c.in)); got != c.want {
			t.Errorf("%s: isLoopbackReply=%v want %v", c.name, got, c.want)
		}
	}
}

// TestHandleProcessesRequestWithTopLevelOK：经 MQ 发一条**顶层带 ok** 的业务请求，
// 应正常处理并应答（修复前会被误判为应答丢弃 → 无回应答 → 调用方挂起）。
func TestHandleProcessesRequestWithTopLevelOK(t *testing.T) {
	s := newSweepTestService(t)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(s.Stop)
	const inst = "ins-okreq"
	s.onInstanceRegister(instanceRegister, instancePayload(inst, t.TempDir(), ""))

	ch := make(chan map[string]any, 4)
	sub, err := s.Bus.On("data-user-config-list", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil {
			return nil
		}
		if _, hasResult := m["result"]; !hasResult { // 只认应答（含 result），忽略请求自身
			return nil
		}
		select {
		case ch <- m:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("sub: %v", err)
	}
	defer sub.Unsubscribe()

	// user-config 属 instanceFreeDomains，无需 instance_id；业务载荷顶层带 ok。
	body, _ := json.Marshal(map[string]any{"req_id": "ok-req", "ok": true})
	_ = s.Bus.Emit(context.Background(), "data-user-config-list", body).Wait()

	select {
	case m := <-ch:
		if ok, _ := m["ok"].(bool); !ok {
			t.Fatalf("应答应 ok=true: %+v", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("顶层带 ok 的请求被误判为应答丢弃：未收到应答")
	}
}
