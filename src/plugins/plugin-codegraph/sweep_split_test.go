//go:build split

package codegraph

import (
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-plugin/instance"
)

// TestHeartbeatTimeoutUnregisters（分离形态）：实例心跳超 instance.HeartbeatTimeout →
// tick 走**既有** instanceGone 清理路径（解绑 + refs 递减归零 → 注销 gateway 查询工具面）。
func TestHeartbeatTimeoutUnregisters(t *testing.T) {
	var mu sync.Mutex
	var unregistered []string
	bus := newStubGatewayBus(t, &mu, &unregistered)

	p := New(Options{Exe: "dummy-not-spawned.exe"})
	p.logf = func(string, ...any) {}
	p.deps.Bus = bus
	wd := "/wd-stale"
	p.insts["i1"] = &instRec{workdir: wd, last: time.Now().Add(-instance.HeartbeatTimeout - time.Minute)}
	p.works[wd] = &workRec{workDir: wd, refs: 1, enabled: true}
	p.syncTools()

	p.tick() // 分离形态：超时 → instanceGone

	p.mu.Lock()
	_, alive := p.insts["i1"]
	registered := p.registered
	refs := p.works[wd].refs
	p.mu.Unlock()
	if alive || registered || refs != 0 {
		t.Fatalf("心跳超时应清理实例与工具面：alive=%v registered=%v refs=%d", alive, registered, refs)
	}
	mu.Lock()
	got := append([]string(nil), unregistered...)
	mu.Unlock()
	if len(got) != len(queryTools) {
		t.Fatalf("心跳超时应注销 %d 个查询工具，实际 %d：%v", len(queryTools), len(got), got)
	}
}

// TestHeartbeatFreshNotSwept（分离形态）：心跳新鲜（含心跳刷新 LastBeat）的实例不判超时。
func TestHeartbeatFreshNotSwept(t *testing.T) {
	var mu sync.Mutex
	var unregistered []string
	bus := newStubGatewayBus(t, &mu, &unregistered)

	p := New(Options{Exe: "dummy-not-spawned.exe"})
	p.logf = func(string, ...any) {}
	p.deps.Bus = bus
	wd := "/wd-live"
	p.insts["i2"] = &instRec{workdir: wd, last: time.Now().Add(-instance.HeartbeatTimeout + time.Minute)}
	p.works[wd] = &workRec{workDir: wd, refs: 1, enabled: true}
	p.syncTools()

	p.onInstanceHeartbeat("", []byte(`{"instance_id":"i2"}`)) // 心跳刷新 LastBeat
	p.tick()

	p.mu.Lock()
	_, alive := p.insts["i2"]
	refs := p.works[wd].refs
	p.mu.Unlock()
	if !alive || refs != 1 {
		t.Fatalf("心跳新鲜不应判退出：alive=%v refs=%d", alive, refs)
	}
	mu.Lock()
	got := append([]string(nil), unregistered...)
	mu.Unlock()
	if len(got) != 0 {
		t.Fatalf("心跳新鲜不应产生注销调用：%v", got)
	}
}
