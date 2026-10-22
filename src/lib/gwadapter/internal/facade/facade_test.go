package facade

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

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
	var unsub atomic.Int32
	ad := New(&spyBus{Bus: bus, unsub: &unsub})
	if err := ad.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	ad.Stop()
	ad.Stop() // 重复调用：不应 panic
	if n := unsub.Load(); n != 1 {
		t.Fatalf("Stop 应退订恰一次，实得 %d", n)
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
