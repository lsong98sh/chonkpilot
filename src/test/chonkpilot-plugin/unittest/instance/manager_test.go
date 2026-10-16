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

// ── 跨窗口定向投递（单聊 §12.4 B4 / 群聊 §14.2 · §14.4 N18–N21；2026-10-16） ──────────
//
// 写侧 = `instance-register.parent_instance`（谁开的窗口指谁，**逐层链**）；投递侧 = 查表取
// **直接父**实例 id 填进消息 `instance_id`（**不改桥** —— 桥按精确相等过滤天然投给它）；
// 「父 + 子都要」= 不带 `instance_id`（广播，走桥的「唯一例外」）。

// regPayloadParent 发布一条带 `parent_instance` 的 instance-register（子窗口实例）。
func regPayloadParent(id, parent string) []byte {
	b, _ := json.Marshal(map[string]any{
		"instance_id": id, "work_dir": "w", "parent_instance": parent,
	})
	return b
}

// TestParentOfInstanceChain 逐层链：只取**直接父**（不通通指主窗口，群聊 §14.4 N20）。
func TestParentOfInstanceChain(t *testing.T) {
	m, _ := newTestManager(t)
	m.HandleRegister(regPayload("main", "w", "d"))       // 顶层（不带 parent_instance）
	m.HandleRegister(regPayloadParent("child", "main"))  // 子
	m.HandleRegister(regPayloadParent("grand", "child")) // 孙

	if info, ok := m.Lookup("child"); !ok || info.ParentInstance != "main" {
		t.Fatalf("注册视图未记录 parent_instance: %+v ok=%v", info, ok)
	}
	if p, ok := m.ParentOfInstance("grand"); !ok || p != "child" {
		t.Fatalf("ParentOfInstance(grand)=(%q,%v)，want (child,true)", p, ok)
	}
	if p, ok := m.ParentOfInstance("child"); !ok || p != "main" {
		t.Fatalf("ParentOfInstance(child)=(%q,%v)，want (main,true)", p, ok)
	}
	if p, ok := m.ParentOfInstance("main"); ok || p != "" {
		t.Fatalf("顶层实例应无父，got (%q,%v)", p, ok)
	}
	if p, ok := m.ParentOfInstance("ghost"); ok || p != "" {
		t.Fatalf("未登记实例应无父，got (%q,%v)", p, ok)
	}
	if p, ok := m.ParentOfInstance(""); ok || p != "" {
		t.Fatalf("空 id 应无父，got (%q,%v)", p, ok)
	}
}

// TestDeliveryTarget 定向投递解析：查表 → 目标 instance_id；广播分支（父 + 子都要）。
func TestDeliveryTarget(t *testing.T) {
	m, _ := newTestManager(t)
	m.HandleRegister(regPayload("main", "w", "d"))
	m.HandleRegister(regPayloadParent("child", "main"))

	cases := []struct {
		name     string
		origin   string
		toParent bool
		wantID   string
		wantHit  bool
	}{
		{"子 → 父（定向投递，填父实例 id）", "child", true, "main", true},
		{"顶层 → 父（无目标，回落广播）", "main", true, "", false},
		{"未登记 → 父（无目标，回落广播）", "ghost", true, "", false},
		{"父 + 子都要（广播，不带 instance_id）", "child", false, "", false},
		{"空 origin + 要父（无目标，回落广播）", "", true, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, hit := m.DeliveryTarget(tc.origin, tc.toParent)
			if id != tc.wantID || hit != tc.wantHit {
				t.Fatalf("DeliveryTarget(%q,%v)=(%q,%v)，want (%q,%v)",
					tc.origin, tc.toParent, id, hit, tc.wantID, tc.wantHit)
			}
		})
	}
}

// regPayloadSession 发布一条带 `session_id` 的 instance-register（会话承载窗口实例）。
func regPayloadSession(id, sessionID string) []byte {
	b, _ := json.Marshal(map[string]any{
		"instance_id": id, "work_dir": "w", "session_id": sessionID,
	})
	return b
}

// TestInstanceForSession 会话 → 实例反查（session→instance；SUBCHAT-3 阶段 F-2 · 群聊 §13 D42，
// 闭环 41 D-62）：窗口注册时自报承载会话 → 按会话反查窗口实例；主窗口（无 session_id）/ 未登记
// 会话 → 无命中；会话字段可随注册刷新。
func TestInstanceForSession(t *testing.T) {
	m, _ := newTestManager(t)
	m.HandleRegister(regPayload("main", "w", "d"))               // 主窗口（不带 session_id）
	m.HandleRegister(regPayloadSession("ins-sub", "subchat-1"))  // 单聊窗口
	m.HandleRegister(regPayloadSession("ins-group", "group-1"))  // 群聊窗口
	m.HandleRegister(regPayloadSession("ghost-holder", ""))      // session_id 空 → 不构成承载

	if ins, ok := m.InstanceForSession("subchat-1"); !ok || ins != "ins-sub" {
		t.Fatalf("InstanceForSession(subchat-1)=(%q,%v)，want (ins-sub,true)", ins, ok)
	}
	if ins, ok := m.InstanceForSession("group-1"); !ok || ins != "ins-group" {
		t.Fatalf("InstanceForSession(group-1)=(%q,%v)，want (ins-group,true)", ins, ok)
	}
	if ins, ok := m.InstanceForSession("nope"); ok || ins != "" {
		t.Fatalf("未承载的会话应无命中，got (%q,%v)", ins, ok)
	}
	if ins, ok := m.InstanceForSession(""); ok || ins != "" {
		t.Fatalf("空 session_id 应无命中，got (%q,%v)", ins, ok)
	}
	// 注册刷新可更新承载会话（窗口复用 / 换绑；不改变已登记其余字段语义）。
	m.HandleRegister(regPayloadSession("ins-sub", "subchat-2"))
	if ins, ok := m.InstanceForSession("subchat-2"); !ok || ins != "ins-sub" {
		t.Fatalf("刷新后 InstanceForSession(subchat-2)=(%q,%v)，want (ins-sub,true)", ins, ok)
	}
}
