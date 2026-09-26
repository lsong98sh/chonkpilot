package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestSessionLockMutualExclusion：同 session 锁互斥；Release 后释放。
func TestSessionLockMutualExclusion(t *testing.T) {
	s := &Server{locksDir: t.TempDir()}
	l1, err := s.lockSession("sess-1")
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	// 同 session 再锁 → busy
	if _, err := s.lockSession("sess-1"); !errors.Is(err, errSessionBusy) {
		t.Fatalf("second lock should be busy, got %v", err)
	}
	// 不同 session 可锁
	l2, err := s.lockSession("sess-2")
	if err != nil {
		t.Fatalf("different session lock: %v", err)
	}
	l2.Release()
	l1.Release()
	// 释放后可再锁
	l3, err := s.lockSession("sess-1")
	if err != nil {
		t.Fatalf("relock after release: %v", err)
	}
	l3.Release()
}

// TestTurnSnapshotCrossTurn：跨轮次——第一轮结束写回快照，第二轮组装命中快照（增量式）。
func TestTurnSnapshotCrossTurn(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	// 第一轮：完成（回显）
	startTurn(t, s, "s-snap", "t1")
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-snap", "turn": "t1",
		"type": "text-user", "content": "first turn",
	}))
	evs := collectTurn(t, s.bus, "t1", 10*time.Second)
	if lastComplete(evs) == nil {
		t.Fatalf("turn1 no complete: %+v", evs)
	}
	// 等 turn1 写回快照（Close 在 loop defer 中）
	waitTurnGone(t, s, "t1")

	// 第二轮：组装应命中快照（GetSessionHistory 有值 = 增量生效）
	startTurn(t, s, "s-snap", "t2")
	s.mu.Lock()
	tc := s.turns[instKey("ins-test", "t2")]
	s.mu.Unlock()
	snap := snapshotOf(t, s, "s-snap") // 快照经 persist data-snapshot-get 读（同库断言）
	hist, _ := snap["history"].([]any)
	if snap == nil || len(hist) == 0 {
		t.Fatalf("snapshot not written after turn1")
	}
	if tc == nil || len(tc.hist) == 0 {
		t.Fatalf("turn2 hist empty (snapshot not used): tc=%v", tc)
	}
	// 快照应包含 turn1 的消息（user first turn + assistant 回显）
	found := false
	for _, m := range hist {
		mm, _ := m.(map[string]any)
		if mm != nil && str(mm["content"]) == "first turn" {
			found = true
		}
	}
	if !found {
		t.Fatalf("snapshot missing turn1 content: %+v", snap)
	}
}

// waitTurnGone 等待 turn 从 s.turns 移除（Close 完成）。
func waitTurnGone(t *testing.T, s *Server, turn string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		s.mu.Lock()
		_, exists := s.turns[instKey("ins-test", turn)]
		s.mu.Unlock()
		if !exists {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("turn %s not cleaned up", turn)
}

// TestLLMStartBusyRejected：同 session 活动 turn 存在时，再次 llm-start 被拒。
func TestLLMStartBusyRejected(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-busy", "t-busy-1")

	// 同 session 第二个 turn → 拒绝（内存 busy + 文件锁，使用 Emit + Wait 获取结果）
	f := s.bus.Emit(context.Background(), "session-start", jb(map[string]any{
		"req_id": "r-busy-2", "instance_id": "ins-test", "session": "s-busy", "turn": "t-busy-2",
	}))
	v := f.Wait()
	res, _ := v.Result.(map[string]any)
	if ok, _ := res["accepted"].(bool); ok {
		t.Fatalf("second llm-start should be rejected: %+v", res)
	}
}

// TestLLMStartRejectsEmptyInstance：instance 为空 = 异常 → 顶层错误（accepted=false，不进 turn）。
func TestLLMStartRejectsEmptyInstance(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	f := s.bus.Emit(context.Background(), "session-start", jb(map[string]any{
		"req_id": "r-noinst", "instance_id": "", "session": "s-noinst", "turn": "t-noinst",
	}))
	res, _ := f.Wait().Result.(map[string]any)
	if ok, _ := res["accepted"].(bool); ok {
		t.Fatalf("空 instance 应被拒，got %+v", res)
	}
	if msg, _ := res["error"].(string); !strings.Contains(msg, "instance_id is required") {
		t.Fatalf("错误应为 instance_id is required，got %+v", res)
	}
}
