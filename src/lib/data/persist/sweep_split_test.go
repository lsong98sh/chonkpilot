//go:build split

// 分离形态（`-tags split`）实例心跳超时清理测试：陈旧实例 → 释放数据根绑定 + data.Unregister
// （与既有 instance-exit 同一实现 instanceGone）；新鲜心跳不被清理；Start 起的 ticker 真实生效
// （默认构建的"零 ticker"用例见 sweep_inprocess_test.go）。
package persist

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// TestStaleSweepReleasesBinding（分离形态）：实例心跳超 staleTimeout（且无 instance-exit）→
// 释放数据根绑定（实例视图移除 + data.Unregister，使 data.PrjUsr 报未登记）；重复扫描幂等。
func TestStaleSweepReleasesBinding(t *testing.T) {
	// 数据目录先于服务创建：清理顺序 LIFO → data.Reset（关连接）先于 TempDir RemoveAll 执行。
	wd, dd := t.TempDir(), t.TempDir()
	s := newSweepTestService(t)
	s.onInstanceRegister(instanceRegister, instancePayload("i-stale", wd, dd))

	// 生产路径：一次数据请求触发 data.Register（登记绑定到数据层）。
	if _, err := s.prjUsrDB("i-stale"); err != nil {
		t.Fatalf("prjUsrDB: %v", err)
	}
	if _, err := data.PrjUsr("i-stale"); err != nil {
		t.Fatalf("前置：绑定应已登记（data 层可解析）: %v", err)
	}

	backdate(t, s, "i-stale", 2*staleTimeout) // 心跳停摆 → 超时
	if n := s.sweepStale(); n != 1 {
		t.Fatalf("扫描应清理 1 个陈旧实例，got %d", n)
	}

	if _, ok := s.lookupInstance("i-stale"); ok {
		t.Fatal("陈旧实例应从实例视图移除")
	}
	if _, err := data.PrjUsr("i-stale"); err == nil {
		t.Fatal("data.Unregister 未生效（绑定仍可解析）")
	}

	// 幂等：重复扫描不重复清理、不报错。
	if n := s.sweepStale(); n != 0 {
		t.Fatalf("重复扫描不应再清理，got %d", n)
	}
	if n := s.sweepStale(); n != 0 {
		t.Fatalf("重复扫描不应再清理，got %d", n)
	}
}

// TestSweepPublishesInstanceExit（分离形态，G-29）：超时回收**必须广播 `instance-exit`**
// —— 主题与 payload 与显式退出一致（`{instance_id}`，61-消息一览 §4.1 ③）→ 订阅方（如 filesys
// 的 instance → work_dir 绑定）据此解绑；重复扫描不重复广播；同一 id 重复到达不报错（幂等）。
func TestSweepPublishesInstanceExit(t *testing.T) {
	s := newSweepTestService(t)

	exits := make(chan []byte, 8)
	if _, err := s.Bus.On(instanceExit, 0, func(_ context.Context, _ string, v *mq.Value) error {
		exits <- append([]byte(nil), v.Payload...)
		return nil
	}); err != nil {
		t.Fatalf("订阅 %s: %v", instanceExit, err)
	}

	wd, dd := t.TempDir(), t.TempDir()
	s.onInstanceRegister(instanceRegister, instancePayload("i-exit-pub", wd, dd))
	backdate(t, s, "i-exit-pub", 2*staleTimeout) // 心跳停摆 → 超时
	if n := s.sweepStale(); n != 1 {
		t.Fatalf("扫描应清理 1 个陈旧实例，got %d", n)
	}

	select {
	case raw := <-exits:
		if string(raw) != `{"instance_id":"i-exit-pub"}` {
			t.Fatalf("超时回收应广播 instance-exit{instance_id}（payload 契约不变），got %s", raw)
		}
	case <-time.After(time.Second):
		t.Fatal("超时回收未广播 instance-exit（filesys 绑定不会释放，G-29）")
	}

	// 幂等①：重复扫描（陈旧实例已不在视图）不再重复清理、不再重复广播。
	if n := s.sweepStale(); n != 0 {
		t.Fatalf("重复扫描不应再清理，got %d", n)
	}
	select {
	case raw := <-exits:
		t.Fatalf("重复扫描不应重复广播 instance-exit，got %s", raw)
	case <-time.After(100 * time.Millisecond):
	}

	// 幂等②：同一 id 的 instance-exit 重复到达（本层 onInstanceExit → instanceGone）不报错、
	// 绑定不复活（模拟 llm 与 persist 两处扫描同时判定 / 显式 exit 与超时回收叠加）。
	for i := 0; i < 2; i++ {
		s.onInstanceExit(instanceExit, instancePayload("i-exit-pub", wd, dd))
	}
	if _, ok := s.lookupInstance("i-exit-pub"); ok {
		t.Fatal("重复 instance-exit 后绑定不应复活")
	}
}

// TestHeartbeatFreshNotSwept（分离形态）：心跳刷新 LastBeat 的实例不判超时——同一轮扫描中
// 陈旧实例被清理、新鲜实例保留。
func TestHeartbeatFreshNotSwept(t *testing.T) {
	s := newSweepTestService(t)
	wd, dd := t.TempDir(), t.TempDir()
	s.onInstanceRegister(instanceRegister, instancePayload("i-stale", wd, dd))
	s.onInstanceRegister(instanceRegister, instancePayload("i-fresh", wd, dd))

	backdate(t, s, "i-stale", 2*staleTimeout)
	backdate(t, s, "i-fresh", 2*staleTimeout)
	s.onInstanceHeartbeat(instanceHeartbeat, instancePayload("i-fresh", "", "")) // 心跳刷新

	if n := s.sweepStale(); n != 1 {
		t.Fatalf("应仅清理陈旧实例，got %d", n)
	}
	if _, ok := s.lookupInstance("i-fresh"); !ok {
		t.Fatal("心跳新鲜不应判退出")
	}
	if info, _ := s.lookupInstance("i-fresh"); time.Since(info.LastBeat) > staleTimeout {
		t.Fatalf("心跳刷新未生效：LastBeat=%v", info.LastBeat)
	}
}

// TestStaleSweepTickerWired（分离形态）：Start 起的周期 ticker 真实生效——陈旧实例在
// 数个扫描周期内被清理（停止后无扫描在跑）。
func TestStaleSweepTickerWired(t *testing.T) {
	old := sweepInterval
	sweepInterval = 20 * time.Millisecond
	// 恢复注册在 Stop 之前（t.Cleanup LIFO）：Stop 已 join 扫描 goroutine，
	// 恢复写与其读之间有序，避免 -race 误报。
	t.Cleanup(func() { sweepInterval = old })

	s := newSweepTestService(t)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(s.Stop)
	if s.sweepStop == nil {
		t.Fatal("分离形态 Start 应起心跳超时扫描 goroutine")
	}

	s.onInstanceRegister(instanceRegister, instancePayload("i-tick", t.TempDir(), t.TempDir()))
	backdate(t, s, "i-tick", 2*staleTimeout)

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := s.lookupInstance("i-tick"); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("2s 内 ticker 未清理陈旧实例")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestStaleSweepIdempotentConcurrent（分离形态）：并发扫描 + 并发心跳/注册不重复清理、不 panic
// （`-race` 下验证无数据竞争）；新鲜实例在并发交错中始终保留。
func TestStaleSweepIdempotentConcurrent(t *testing.T) {
	s := newSweepTestService(t)
	wd, dd := t.TempDir(), t.TempDir()
	staleIDs := []string{"i-s0", "i-s1", "i-s2", "i-s3"}
	for _, id := range staleIDs {
		s.onInstanceRegister(instanceRegister, instancePayload(id, wd, dd))
		backdate(t, s, id, 2*staleTimeout)
	}
	s.onInstanceRegister(instanceRegister, instancePayload("i-fresh", wd, dd))
	s.onInstanceHeartbeat(instanceHeartbeat, instancePayload("i-fresh", "", ""))

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				s.sweepStale()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 50; j++ {
			s.onInstanceHeartbeat(instanceHeartbeat, instancePayload("i-fresh", "", ""))
			s.onInstanceRegister(instanceRegister, instancePayload("i-fresh", wd, dd))
		}
	}()
	wg.Wait()

	for _, id := range staleIDs {
		if _, ok := s.lookupInstance(id); ok {
			t.Fatalf("陈旧实例 %s 应被清理", id)
		}
	}
	if _, ok := s.lookupInstance("i-fresh"); !ok {
		t.Fatal("并发心跳/注册的新鲜实例不应被清理")
	}
}
