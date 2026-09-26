//go:build split

package instance

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// newTestBus 建一条进程内总线（前缀与产品初始化一致）。
func newTestBus(t *testing.T) mq.Bus {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	return bus
}

// TestHeartbeatPublishedByInterval：分离形态按周期发布 `instance-heartbeat`，
// payload 仅 {instance_id}（契约不变，61-消息一览 §4.1）；Stop 后停止发布。
func TestHeartbeatPublishedByInterval(t *testing.T) {
	old := heartbeatInterval
	heartbeatInterval = 10 * time.Millisecond // 短周期（可注入）
	defer func() { heartbeatInterval = old }()

	bus := newTestBus(t)
	var mu sync.Mutex
	var got []map[string]string
	if _, err := bus.On(SubjectHeartbeat, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]string
		_ = json.Unmarshal(v.Payload, &m)
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
		return nil
	}); err != nil {
		t.Fatalf("subscribe %s: %v", SubjectHeartbeat, err)
	}

	p := startPublisher(bus, "inst-1", "http://127.0.0.1:18080")
	if p.URL() != "http://127.0.0.1:18080" {
		t.Fatalf("对端地址未登记：%q", p.URL())
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("2s 内未按周期发布心跳（已收 %d 帧）", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	first := got[0]
	mu.Unlock()
	if len(first) != 1 || first["instance_id"] != "inst-1" {
		t.Fatalf("心跳 payload 应为仅 {instance_id}：%v", first)
	}

	p.Stop()
	time.Sleep(20 * time.Millisecond) // 等在飞的一帧落地
	mu.Lock()
	stopped := len(got)
	mu.Unlock()
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	after := len(got)
	mu.Unlock()
	if after != stopped {
		t.Fatalf("Stop 后仍在发布心跳：%d → %d", stopped, after)
	}
}

// TestSweepRemovesOnlyStaleInstances：LastBeat 超 HeartbeatTimeout 的实例被移除；
// 新鲜实例（含心跳刷新）保留——判定基准 = 最近注册/心跳时刻。
func TestSweepRemovesOnlyStaleInstances(t *testing.T) {
	bus := newTestBus(t)
	now := time.Unix(1_700_000_000, 0)
	m := New(bus)
	m.SetClock(func() time.Time { return now })

	m.HandleRegister([]byte(`{"instance_id":"stale","work_dir":"/wd-a"}`))
	now = now.Add(HeartbeatTimeout + 2*time.Second)
	m.HandleRegister([]byte(`{"instance_id":"fresh","work_dir":"/wd-a"}`))

	gone := m.Sweep(0)
	if len(gone) != 1 || gone[0].ID != "stale" {
		t.Fatalf("应仅移除超时实例，实际 %+v", gone)
	}
	if _, ok := m.Lookup("stale"); ok {
		t.Fatal("超时实例应从实例视图移除")
	}
	if _, ok := m.Lookup("fresh"); !ok {
		t.Fatal("新鲜实例不应被移除")
	}

	// 心跳刷新 LastBeat → 未超时不判退出
	now = now.Add(HeartbeatTimeout - time.Second)
	m.HandleHeartbeat([]byte(`{"instance_id":"fresh"}`))
	now = now.Add(HeartbeatTimeout - time.Second)
	if gone := m.Sweep(HeartbeatTimeout); len(gone) != 0 {
		t.Fatalf("心跳新鲜不应判退出：%+v", gone)
	}
}

// TestStartHeartbeatEmptyArgs：bus/instanceID 缺失 → 空实现（不发布、Stop 幂等不 panic）。
func TestStartHeartbeatEmptyArgs(t *testing.T) {
	stop := StartHeartbeat(nil, "", "")
	stop()
	stop()
}
