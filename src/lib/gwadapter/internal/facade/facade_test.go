package facade

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
)

// mockMethodFace 在 mcp-* 方法面主题上按 req_id 回执空结果（tools/prompts/resources list
// → 空集合）：buscall 的「无应答方 = 超时」语义（2026-11-19 总线 promise 退场）要求测试
// 装一个应答方；空集合即可让 reconcile 走通零工具路径。
func mockMethodFace(t *testing.T, bus mq.Bus) {
	t.Helper()
	for _, topic := range []string{"mcp-tools-list", "mcp-prompts-list", "mcp-resources-list"} {
		_, err := bus.On(topic, 0, func(_ context.Context, subject string, v *mq.Value) error {
			var hdr struct {
				ReqID string `json:"req_id"`
			}
			if json.Unmarshal(v.Payload, &hdr) != nil || hdr.ReqID == "" {
				return nil
			}
			// 应答信封（顶层 ok + result）：回环不再应答。
			var probe struct {
				OK     *bool `json:"ok"`
				Result *any  `json:"result"`
			}
			_ = json.Unmarshal(v.Payload, &probe)
			if probe.OK != nil && probe.Result != nil {
				return nil
			}
			resp := map[string]any{"req_id": hdr.ReqID, "ok": true, "result": map[string]any{
				"resultType": "complete",
				"tools":      []any{},
				"prompts":    []any{},
				"resources":  []any{},
			}}
			b, _ := json.Marshal(resp)
			_ = bus.Publish(subject, b)
			return nil
		})
		if err != nil {
			t.Fatalf("mock %s: %v", topic, err)
		}
	}
}

// spyBus 包装真实 Bus，记录 mcp-gateway-changed 订阅的退订次数（验证 Stop 退订）。
type spyBus struct {
	mq.Bus
	unsub *atomic.Int32
}

func (s *spyBus) On(subject string, order int, h mq.VHandler) (mq.Sub, error) {
	sub, err := s.Bus.On(subject, order, h)
	if err != nil {
		return nil, err
	}
	return &spySub{Sub: sub, unsub: s.unsub}, nil
}

type spySub struct {
	mq.Sub
	unsub *atomic.Int32
}

func (s *spySub) Unsubscribe() error {
	s.unsub.Add(1)
	return s.Sub.Unsubscribe()
}

// TestStopIdempotentAndUnsubscribes：Start 建立订阅；Stop 幂等（重复调用不 panic），且退订恰一次。
func TestStopIdempotentAndUnsubscribes(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	defer bus.Close()
	mockMethodFace(t, bus) // buscall（req_id 回执）需要应答方；经 spyBus 计入退订数（buscall 客户端调用即退）
	var unsub atomic.Int32
	ad := New(&spyBus{Bus: bus, unsub: &unsub})
	if err := ad.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	ad.Stop()
	ad.Stop() // 重复调用：不应 panic
	// Start 内三次 reconcile 各建一个 buscall 客户端（tools/prompts/resources）→ 各退订一次
	// （3 次）+ mcp-gateway-changed 长驻订阅退订一次 = 4。
	if n := unsub.Load(); n != 4 {
		t.Fatalf("Stop 应退订恰一次（含 buscall 临时订阅 3 次），实得 %d", n)
	}
}

// TestStopBeforeStartSafe：未 Start 亦可 Stop（幂等保护），重复调用不 panic。
func TestStopBeforeStartSafe(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	defer bus.Close()
	ad := New(bus)
	ad.Stop()
	ad.Stop()
}
