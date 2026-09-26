//go:build split

// 分离形态（`-tags split`）心跳超时扫描测试：陈旧实例 → 既有退出清理路径（exitInstance）；
// 新鲜心跳不被清理；Start 起的周期 ticker 真实生效（默认构建的"零 ticker"用例见 sweep_inprocess_test.go）。
package server

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-plugin/instance"
)

// newTestServerClock 同 newTestServerMCP，但实例视图时钟在 Start **之前**注入
// （分离形态 Start 已起扫描 goroutine，之后 SetClock 会与其 Sweep 并发读写管理器时钟）。
func newTestServerClock(t *testing.T, llmSrv *httptest.Server, clock func() time.Time) *Server {
	t.Helper()
	data.Reset()
	testWorkDir = t.TempDir()
	t.Cleanup(func() { testWorkDir = "" })
	bus, err := mq.New(mq.Options{Prefix: testBusPrefix})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })

	s := New(bus, Options{LLMBase: llmSrv.URL, LLMModel: "mock",
		MCPServerRoot: t.TempDir(), UsrPath: t.TempDir() + "/usr.db"})
	s.im.SetClock(clock)
	s.locksDir = t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(s.Stop)
	return s
}

// fixedClock 返回可推进的确定性时钟（并发安全；注入 instance.Manager）。
func fixedClock() (func() time.Time, func(time.Duration)) {
	var mu sync.Mutex
	now := time.Now()
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		now = now.Add(d)
		mu.Unlock()
	}
	return clock, advance
}

// dirNodesOf / capWorkDirOf 持锁读 server 内部注册态（扫描 goroutine 会并发写这两张表）。
func dirNodesOf(s *Server, instanceID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.dirNodes[instanceID]...)
}

func capWorkDirOf(s *Server, instanceID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wd, ok := s.capWorkDirs[instanceID]
	return wd, ok
}

// TestStaleInstanceSweptViaExitPath（分离形态）：实例心跳超 instance.HeartbeatTimeout →
// Sweep 返回陈旧实例 → 走**既有**退出清理（exitInstance：注销 capability dir 节点 + 清实例视图）；
// 重复扫描幂等（无副作用、不 panic）。
func TestStaleInstanceSweptViaExitPath(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	clock, advance := fixedClock()
	s := newTestServerClock(t, llm, clock)

	writeHotToolContract(t, persist.CapUserRoot(s.opts.UsrPath), "user_demo")
	writeHotToolContract(t, persist.CapProjectRoot(testWorkDir), "prj_demo")

	payload := jb(map[string]any{
		"instance_id": "ins-stale", "client_type": "unittest", "work_dir": testWorkDir,
	})
	s.im.HandleRegister(payload)                       // 实例视图登记（LastBeat = 时钟当前值）
	s.onInstanceRegister("instance-register", payload) // capability dir 节点接入
	if n := len(dirNodesOf(s, "ins-stale")); n != 2 {
		t.Fatalf("前置：应接入 2 个 dir 节点，got %d", n)
	}

	advance(instance.HeartbeatTimeout + time.Second) // 心跳停摆 → 超时
	s.sweepStaleInstances()

	if _, ok := s.im.Lookup("ins-stale"); ok {
		t.Fatal("超时实例应从实例视图移除（Sweep）")
	}
	if n := len(dirNodesOf(s, "ins-stale")); n != 0 {
		t.Fatalf("超时实例应走既有退出清理（dir 节点未注销，got %d）", n)
	}

	defs, err := s.gc.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if countTool(defs, "ins-stale-user_user_demo") != 0 || countTool(defs, "ins-stale-project_prj_demo") != 0 {
		t.Fatalf("超时实例的工具应已注销: %v", toolNames(defs))
	}

	// 幂等：重复扫描（含已清理实例）无副作用。
	s.sweepStaleInstances()
	s.sweepStaleInstances()
}

// TestSweepPublishesInstanceExit（分离形态，G-29）：超时回收**必须广播 `instance-exit`**
// —— 主题与 payload 与显式退出一致（`{instance_id}`，61-消息一览 §4.1 ③）→ 订阅方（如 filesys
// 的 instance → work_dir 绑定）据此解绑；重复扫描不重复广播；同一 id 重复投递不产生错误（幂等）。
func TestSweepPublishesInstanceExit(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	clock, advance := fixedClock()
	s := newTestServerClock(t, llm, clock)

	exits := make(chan []byte, 8)
	if _, err := s.bus.On(SubjectExit, 0, func(_ context.Context, _ string, v *mq.Value) error {
		exits <- append([]byte(nil), v.Payload...)
		return nil
	}); err != nil {
		t.Fatalf("订阅 %s: %v", SubjectExit, err)
	}

	s.im.HandleRegister(jb(map[string]any{"instance_id": "ins-exit-pub", "work_dir": testWorkDir}))
	advance(instance.HeartbeatTimeout + time.Second) // 心跳停摆 → 超时
	s.sweepStaleInstances()

	select {
	case raw := <-exits:
		if string(raw) != `{"instance_id":"ins-exit-pub"}` {
			t.Fatalf("超时回收应广播 instance-exit{instance_id}（payload 契约不变），got %s", raw)
		}
	case <-time.After(time.Second):
		t.Fatal("超时回收未广播 instance-exit（filesys 绑定不会释放，G-29）")
	}

	// 幂等①：重复扫描（陈旧实例已不在视图）不再重复广播。
	s.sweepStaleInstances()
	select {
	case raw := <-exits:
		t.Fatalf("重复扫描不应重复广播 instance-exit，got %s", raw)
	case <-time.After(100 * time.Millisecond):
	}

	// 幂等②：同一 id 的 instance-exit 再投递两次（模拟本包与 persist 两处扫描同时判定 /
	// 显式 exit 与超时回收叠加）→ 全部订阅方（含本包 onExit → exitInstance）不报错。
	for i := 0; i < 2; i++ {
		v := s.bus.Emit(context.Background(), SubjectExit,
			jb(map[string]any{"instance_id": "ins-exit-pub"})).Wait()
		if len(v.Errors) != 0 {
			t.Fatalf("重复 instance-exit 不应产生错误（第 %d 次）: %v", i+1, v.Errors)
		}
		<-exits // 消费本次投递（订阅观测与产品订阅同一派发）
	}
}

// TestHeartbeatFreshNotSwept（分离形态）：心跳刷新 LastBeat 后未超时 → 不判退出。
func TestHeartbeatFreshNotSwept(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	clock, advance := fixedClock()
	s := newTestServerClock(t, llm, clock)

	payload := jb(map[string]any{"instance_id": "ins-live", "client_type": "unittest", "work_dir": testWorkDir})
	s.im.HandleRegister(payload)
	s.onInstanceRegister("instance-register", payload)

	advance(instance.HeartbeatTimeout - time.Second)
	s.im.HandleHeartbeat(jb(map[string]any{"instance_id": "ins-live"})) // 心跳刷新
	advance(instance.HeartbeatTimeout - time.Second)                    // 距最后心跳 < 超时
	s.sweepStaleInstances()

	if _, ok := s.im.Lookup("ins-live"); !ok {
		t.Fatal("心跳新鲜不应判退出（实例视图被移除）")
	}
	if _, ok := capWorkDirOf(s, "ins-live"); !ok {
		t.Fatal("心跳新鲜不应触发退出清理（注册态被清除）")
	}
}

// TestInstanceSweepTickerWired（分离形态）：Start 起的周期 ticker 真实生效——
// 时钟推进超时后，陈旧实例在数个扫描周期内被清理。
func TestInstanceSweepTickerWired(t *testing.T) {
	oldInterval := sweepInterval
	sweepInterval = 20 * time.Millisecond
	// 恢复注册在 Server Stop 之前（t.Cleanup LIFO）：Stop 已 join 扫描 goroutine，
	// 恢复写与 goroutine 读之间有序，避免 -race 误报。
	t.Cleanup(func() { sweepInterval = oldInterval })

	llm := mockLLMServer()
	defer llm.Close()
	clock, advance := fixedClock()
	s := newTestServerClock(t, llm, clock)
	if s.sweepStop == nil {
		t.Fatal("分离形态 Start 应起心跳超时扫描 goroutine")
	}

	s.im.HandleRegister(jb(map[string]any{"instance_id": "ins-tick", "work_dir": testWorkDir}))
	advance(instance.HeartbeatTimeout + time.Second)

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := s.im.Lookup("ins-tick"); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("2s 内 ticker 未清理陈旧实例")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, ok := capWorkDirOf(s, "ins-tick"); ok {
		t.Fatal("ticker 清理未走既有退出清理（capWorkDirs 未清）")
	}
}

// TestStaleSweepIdempotentConcurrent（分离形态）：并发扫描 + 并发心跳/注册不重复清理、不 panic
// （`-race` 下验证无数据竞争）；新鲜实例在并发交错中始终保留。
func TestStaleSweepIdempotentConcurrent(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	clock, advance := fixedClock()
	s := newTestServerClock(t, llm, clock)

	for _, id := range staleIDs {
		s.im.HandleRegister(jb(map[string]any{"instance_id": id, "work_dir": testWorkDir}))
	}
	s.im.HandleRegister(jb(map[string]any{"instance_id": "ins-fresh", "work_dir": testWorkDir}))

	advance(instance.HeartbeatTimeout + time.Second)
	s.im.HandleHeartbeat(jb(map[string]any{"instance_id": "ins-fresh"})) // 前置刷新：并发期恒新鲜

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				s.sweepStaleInstances() // 幂等：重复扫描陈旧集合无额外副作用
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 50; j++ {
			s.im.HandleHeartbeat(jb(map[string]any{"instance_id": "ins-fresh"}))
		}
	}()
	wg.Wait()

	for _, id := range staleIDs {
		if _, ok := s.im.Lookup(id); ok {
			t.Fatalf("陈旧实例 %s 应被清理", id)
		}
	}
	if _, ok := s.im.Lookup("ins-fresh"); !ok {
		t.Fatal("并发心跳的新鲜实例不应被清理")
	}
}

// staleIDs 是并发用例的陈旧实例名（固定集合，避免下标转字符串）。
var staleIDs = []string{"ins-stale-0", "ins-stale-1", "ins-stale-2", "ins-stale-3"}
