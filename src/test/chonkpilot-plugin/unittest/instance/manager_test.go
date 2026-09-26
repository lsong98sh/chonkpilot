// instance.Manager 黑盒外部测试（独立测试模块 chonkpilot-test/chonkpilot-plugin/unittest，
// 只经导出 API + SetClock 测试口驱动；mq 为进程内同步派发，Emit 派发即已受理）。
package instance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-plugin/instance"
)

// newTestManager 建内存 MQ（带命名空间前缀，与生产一致：业务只写相对主题）+ Manager（已 Start）。
func newTestManager(t *testing.T) (*instance.Manager, mq.Bus) {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	m := instance.New(bus)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { m.Stop(); _ = bus.Close() })
	return m, bus
}

// waitFor 轮询条件（保持与迁移前一致的等待语义）。
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func regPayload(id, workDir, dataDir string) []byte {
	b, _ := json.Marshal(map[string]any{
		"instance_id": id, "work_dir": workDir, "data_dir": dataDir,
	})
	return b
}

// TestRegister：register → Lookup 命中（work_dir/data_dir 绑定）。
func TestRegister(t *testing.T) {
	m, bus := newTestManager(t)
	_ = bus.Emit(context.Background(), instance.SubjectRegister, regPayload("i1", `C:\proj`, `D:\data`))
	waitFor(t, time.Second, func() bool {
		_, ok := m.Lookup("i1")
		return ok
	})
	info, _ := m.Lookup("i1")
	if info.WorkDir != `C:\proj` || info.DataDir != `D:\data` {
		t.Fatalf("binding wrong: %+v", info)
	}
	if m.Count() != 1 {
		t.Fatalf("Count=%d, want 1", m.Count())
	}
}

// TestRegisterEmptyIgnored：缺 instance_id 的注册被忽略。
func TestRegisterEmptyIgnored(t *testing.T) {
	m, bus := newTestManager(t)
	_ = bus.Emit(context.Background(), instance.SubjectRegister, regPayload("", "", ""))
	time.Sleep(50 * time.Millisecond)
	if m.Count() != 0 {
		t.Fatalf("Count=%d, want 0", m.Count())
	}
}

// TestHeartbeatRefresh：心跳刷新 LastBeat（SetClock 注入时钟验证）。
func TestHeartbeatRefresh(t *testing.T) {
	m, bus := newTestManager(t)
	m.SetClock(func() time.Time { return time.Unix(1000, 0) })
	m.HandleRegister(regPayload("i1", "w", "d"))
	// 快进时钟后心跳 → LastBeat 跟随
	m.SetClock(func() time.Time { return time.Unix(1010, 0) })
	hb, _ := json.Marshal(map[string]any{"instance_id": "i1"})
	_ = bus.Emit(context.Background(), instance.SubjectHeartbeat, hb)
	waitFor(t, time.Second, func() bool {
		info, ok := m.Lookup("i1")
		return ok && info.LastBeat.Equal(time.Unix(1010, 0))
	})
}

// TestExitRemoval：exit → 实例移除。
func TestExitRemoval(t *testing.T) {
	m, bus := newTestManager(t)
	m.HandleRegister(regPayload("i1", "w", "d"))
	if m.Count() != 1 {
		t.Fatalf("setup Count=%d", m.Count())
	}
	ex, _ := json.Marshal(map[string]any{"instance_id": "i1"})
	_ = bus.Emit(context.Background(), instance.SubjectExit, ex)
	waitFor(t, time.Second, func() bool { return m.Count() == 0 })
}

// TestStopUnsubscribes：Stop 后不再响应消息。
func TestStopUnsubscribes(t *testing.T) {
	bus, _ := mq.New(mq.Options{Prefix: "chonk."})
	m := instance.New(bus)
	_ = m.Start()
	m.HandleRegister(regPayload("i1", "w", "d"))
	m.Stop()
	_ = bus.Emit(context.Background(), instance.SubjectExit, []byte(`{"instance_id":"i1"}`))
	time.Sleep(50 * time.Millisecond)
	if m.Count() != 1 {
		t.Fatalf("after Stop, Count=%d, want 1 (should ignore exit)", m.Count())
	}
	_ = bus.Close()
}

// TestList：List 返回全部实例。
func TestList(t *testing.T) {
	m, _ := newTestManager(t)
	m.HandleRegister(regPayload("i1", "w1", "d1"))
	m.HandleRegister(regPayload("i2", "w2", "d2"))
	if got := m.List(); len(got) != 2 {
		t.Fatalf("List len=%d, want 2", len(got))
	}
}

// TestInstanceLimit 缺口 7（2026-09-19）：实例数上限可配；超限注册返回**明确错误**（不 panic、
// 不静默丢）；已登记实例的刷新不受限。
func TestInstanceLimit(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	defer func() { _ = bus.Close() }()
	m := instance.NewWithLimit(bus, 2)
	if m.MaxInstances() != 2 {
		t.Fatalf("MaxInstances=%d, want 2", m.MaxInstances())
	}

	if err := m.HandleRegister(regPayload("i1", "w1", "d1")); err != nil {
		t.Fatalf("i1 注册不应报错: %v", err)
	}
	if err := m.HandleRegister(regPayload("i2", "w2", "d2")); err != nil {
		t.Fatalf("i2 注册不应报错: %v", err)
	}
	err = m.HandleRegister(regPayload("i3", "w3", "d3"))
	if err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("超限注册应返回明确错误，got %v", err)
	}
	if m.Count() != 2 {
		t.Fatalf("超限后 Count=%d, want 2", m.Count())
	}
	if _, ok := m.Lookup("i3"); ok {
		t.Fatal("被拒实例不应进入视图")
	}
	// 已登记实例的注册刷新（work_dir 变更）不受上限影响
	if err := m.HandleRegister(regPayload("i1", "w1-new", "d1")); err != nil {
		t.Fatalf("既有实例刷新不应报错: %v", err)
	}
	if info, _ := m.Lookup("i1"); info.WorkDir != "w1-new" {
		t.Fatalf("既有实例刷新未生效: %+v", info)
	}
	// 退出后可再注册（计数释放）
	m.HandleExit([]byte(`{"instance_id":"i2"}`))
	if err := m.HandleRegister(regPayload("i4", "w4", "d4")); err != nil {
		t.Fatalf("退出后应可再注册: %v", err)
	}
}

// TestInstanceLimitConcurrentRegistration 缺口 7：并发注册不得绕过上限（检查+插入同一把写锁内）。
func TestInstanceLimitConcurrentRegistration(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	defer func() { _ = bus.Close() }()
	const limit = 3
	m := instance.NewWithLimit(bus, limit)

	const n = 40
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted, rejected := 0, 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := m.HandleRegister(regPayload(fmt.Sprintf("i%02d", i), "w", "d"))
			mu.Lock()
			if err == nil {
				accepted++
			} else {
				rejected++
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if m.Count() != limit {
		t.Fatalf("并发注册后 Count=%d, want %d（上限必须严格）", m.Count(), limit)
	}
	if accepted != limit || rejected != n-limit {
		t.Fatalf("并发注册计数异常：accepted=%d rejected=%d（want %d / %d）", accepted, rejected, limit, n-limit)
	}
}

// TestInstanceLimitUnlimited：上限 <=0 = 不限（显式关闭，保持旧行为）。
func TestInstanceLimitUnlimited(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	defer func() { _ = bus.Close() }()
	m := instance.New(bus)
	m.SetMaxInstances(0)
	for i := 0; i < 100; i++ {
		if err := m.HandleRegister(regPayload(fmt.Sprintf("i%03d", i), "w", "d")); err != nil {
			t.Fatalf("不限上限时注册 i%03d 不应报错: %v", i, err)
		}
	}
	if m.Count() != 100 {
		t.Fatalf("Count=%d, want 100", m.Count())
	}
}
