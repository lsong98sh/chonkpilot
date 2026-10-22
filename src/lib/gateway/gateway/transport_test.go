package mcpgateway

import (
	"context"
	"encoding/json"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// testTransport 是 Transport 的 mq 实现（仅测试用）。生产实现见 mqadapter 包——
// 此处重复一份，避免同包测试 import mqadapter 造成 import 循环。
type testTransport struct{ bus mq.Bus }

func newTestTransport(bus mq.Bus) *testTransport { return &testTransport{bus: bus} }

func (t *testTransport) Call(ctx context.Context, op Op, payload []byte) ([]byte, error) {
	v := t.bus.Emit(ctx, string(op), payload).Wait()
	if err := v.Err(); err != nil {
		return nil, err
	}
	if v.Result == nil {
		return nil, nil
	}
	return json.Marshal(v.Result)
}

func (t *testTransport) Publish(ctx context.Context, op Op, payload []byte) error {
	t.bus.Emit(ctx, string(op), payload)
	return nil
}

func (t *testTransport) Subscribe(op Op, h func(ctx context.Context, payload []byte) ([]byte, error)) (Unsubscribe, error) {
	sub, err := t.bus.On(string(op), 0, func(ctx context.Context, _ string, v *mq.Value) error {
		raw, herr := h(ctx, v.Payload)
		if herr != nil {
			return herr
		}
		if len(raw) == 0 {
			return nil
		}
		var res any
		if uerr := json.Unmarshal(raw, &res); uerr != nil {
			res = raw
		}
		v.Result = res
		return nil
	})
	if err != nil {
		return nil, err
	}
	return sub.Unsubscribe, nil
}
