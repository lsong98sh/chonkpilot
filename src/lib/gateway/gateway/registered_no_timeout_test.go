package mcpgateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestRegisteredProviderNoTimeoutHint：契约显式 timeout=0/-1（无上限）的外部工具，
// 跨进程回调载荷必须带 `no_timeout:true`（pluginbridge 据此去掉 11 分钟兜底，BRIDGE-2 ⑥）。
func TestRegisteredProviderNoTimeoutHint(t *testing.T) {
	bus, err := mq.New(mq.Options{})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	defer bus.Close()

	cases := []struct {
		name string
		meta mcp.Meta
		want bool
	}{
		{"unlimited_zero", mcp.Meta{"timeout": float64(0)}, true},
		{"unlimited_neg", mcp.Meta{"timeout": float64(-1)}, true},
		{"limited", mcp.Meta{"timeout": float64(300)}, false},
		{"unset", mcp.Meta{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subject := "test-ext-" + tc.name
			got := make(chan map[string]any, 1)
			if _, err := bus.On(subject, 0, func(_ context.Context, _ string, v *mq.Value) error {
				var m map[string]any
				_ = json.Unmarshal(v.Payload, &m)
				got <- m
				v.Result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}}
				return nil
			}); err != nil {
				t.Fatalf("subscribe: %v", err)
			}
			p := newRegisteredProvider(newTestTransport(bus), 5*time.Second)
			tool := &mcp.Tool{Name: tc.name, Description: "d", Meta: tc.meta}
			if err := p.RegisterTool(&registeredTool{
				Tool: tool, HandlerSubject: subject, Owner: "test",
			}); err != nil {
				t.Fatalf("register: %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if _, err := p.Call(ctx, tc.name, map[string]any{}); err != nil {
				t.Fatalf("call: %v", err)
			}
			m := <-got
			_, has := m["no_timeout"]
			if has != tc.want {
				t.Fatalf("no_timeout=%v，期望 %v（payload=%v）", has, tc.want, m)
			}
		})
	}
}
