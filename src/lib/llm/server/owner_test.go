// 运行态归属（SCOPE-1 强制不变式「同一 dialog 不得在两个窗口打开」）的 L1 用例：
// 归属产生（受理 llm-start / llm-send）、跨实例拒绝（既有 accepted:false）、同实例重入幂等、
// 空闲自动结束释放、instance-exit 整窗释放。全部 mq 驱动（session-start / session-send / session-cancel）。
package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

// registerInstanceAs 注册一个指定 instance_id 的实例（绑定共享 workdir，与 startTurn 同口径）。
func registerInstanceAs(t *testing.T, s *Server, inst string) {
	t.Helper()
	_ = s.bus.Emit(context.Background(), "instance-register", jb(map[string]any{
		"instance_id": inst, "client_type": "unittest", "work_dir": testWorkDir,
	}))
	time.Sleep(50 * time.Millisecond)
}

// emitStartAs 以指定 instance 发 llm-start（session-start），返回应答载荷。
func emitStartAs(t *testing.T, s *Server, inst, session, turn string) map[string]any {
	t.Helper()
	v := s.bus.Emit(context.Background(), "session-start", jb(map[string]any{
		"req_id": "r-" + inst + "-" + turn, "instance_id": inst, "session": session, "turn": turn,
	})).Wait()
	if v.Err() != nil {
		t.Fatalf("llm-start(%s/%s) error: %v", inst, turn, v.Err())
	}
	res, _ := v.Result.(map[string]any)
	return res
}

// emitSendAs 以指定 instance 发 llm-send（text-user）：成功路径不写 Result（= nil）。
func emitSendAs(t *testing.T, s *Server, inst, session, turn, content string) map[string]any {
	t.Helper()
	v := s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": inst, "session": session, "turn": turn,
		"type": "text-user", "content": content,
	})).Wait()
	if v.Err() != nil {
		t.Fatalf("llm-send(%s/%s) error: %v", inst, turn, v.Err())
	}
	res, _ := v.Result.(map[string]any)
	return res
}

// waitOwnerReleased 有界等待该会话的归属被释放（空串）。
func waitOwnerReleased(t *testing.T, s *Server, session string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.sessionOwner(session) == "" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("会话 %s 的归属未在超时内释放（owner=%q）", session, s.sessionOwner(session))
}

// TestSessionOwnerRejectAndTransfer：A 受理后 S 归 A → B 的 llm-start / llm-send 均被拒
// （走既有 accepted:false）；同实例重入不被归属拦（落到既有 busy 拒绝）；S 空闲后 B 受理成功并成为新 owner。
func TestSessionOwnerRejectAndTransfer(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerInstanceAs(t, s, "ins-a")
	registerInstanceAs(t, s, "ins-b")

	// A 起一轮（不喂输入 → 轮次保持运行中 = 活动运行态）
	if res := emitStartAs(t, s, "ins-a", "s-own", "t-own-a"); res["accepted"] != true {
		t.Fatalf("ins-a 的 llm-start 应受理：%+v", res)
	}
	if got := s.sessionOwner("s-own"); got != "ins-a" {
		t.Fatalf("owner = %q, want ins-a", got)
	}

	// B 对同一 session 再 llm-start → 拒绝（新文案 + 既有 accepted:false，无新字段）
	res := emitStartAs(t, s, "ins-b", "s-own", "t-own-b")
	if ok, _ := res["accepted"].(bool); ok {
		t.Fatalf("ins-b 的 llm-start 应被归属拒绝：%+v", res)
	}
	if msg := str(res["error"]); !strings.Contains(msg, ownerConflictText) {
		t.Fatalf("拒绝文案应包含 %q，got=%q", ownerConflictText, msg)
	}
	// B 对 A 正在跑的 session 发 llm-send → 拒绝（同一文案）
	res = emitSendAs(t, s, "ins-b", "s-own", "t-own-a", "抢发")
	if ok, _ := res["accepted"].(bool); ok {
		t.Fatalf("ins-b 的 llm-send 应被归属拒绝：%+v", res)
	}
	if msg := str(res["error"]); !strings.Contains(msg, ownerConflictText) {
		t.Fatalf("llm-send 拒绝文案应包含 %q，got=%q", ownerConflictText, msg)
	}

	// 同实例重入不受归属影响：A 再起一轮 → 被**既有** busy 拒绝（不是归属拒绝）
	res = emitStartAs(t, s, "ins-a", "s-own", "t-own-a2")
	if ok, _ := res["accepted"].(bool); ok {
		t.Fatalf("同实例重入应被 busy 拒绝（会话已有运行轮次）：%+v", res)
	}
	if msg := str(res["error"]); strings.Contains(msg, ownerConflictText) || !strings.Contains(msg, "session busy") {
		t.Fatalf("同实例重入应命中 busy 而非归属：%q", msg)
	}
	if got := s.sessionOwner("s-own"); got != "ins-a" {
		t.Fatalf("同实例重入不得改变 owner：%q", got)
	}

	// A 的轮次结束（取消 → 空闲：inbox 空 + 无 async task）→ 归属释放
	s.bus.Emit(context.Background(), "session-cancel", jb(map[string]any{
		"instance_id": "ins-a", "session": "s-own", "turn": "t-own-a",
	}))
	waitOwnerReleased(t, s, "s-own")

	// 空闲后 B 受理成功 → owner 变 B（「用户选择即激活」）
	if res := emitStartAs(t, s, "ins-b", "s-own", "t-own-b2"); res["accepted"] != true {
		t.Fatalf("空闲后 ins-b 的 llm-start 应受理：%+v", res)
	}
	if got := s.sessionOwner("s-own"); got != "ins-b" {
		t.Fatalf("owner 应转移给 ins-b，got %q", got)
	}
}

// TestSessionOwnerReleasedOnInstanceExit：`instance-exit` 整窗释放（防退出后永久占位）。
func TestSessionOwnerReleasedOnInstanceExit(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerInstanceAs(t, s, "ins-x")
	registerInstanceAs(t, s, "ins-y")

	if res := emitStartAs(t, s, "ins-x", "s-exit", "t-exit"); res["accepted"] != true {
		t.Fatalf("ins-x 的 llm-start 应受理：%+v", res)
	}
	if got := s.sessionOwner("s-exit"); got != "ins-x" {
		t.Fatalf("owner = %q, want ins-x", got)
	}

	s.exitInstance("ins-x")
	if got := s.sessionOwner("s-exit"); got != "" {
		t.Fatalf("instance-exit 应整窗释放归属，got %q", got)
	}
	if res := emitStartAs(t, s, "ins-y", "s-exit", "t-exit2"); res["accepted"] != true {
		t.Fatalf("整窗释放后 ins-y 的 llm-start 应受理：%+v", res)
	}
}

// TestReleaseOwnerIfIdleConditions：释放判据三条（turn 结束 ∧ inbox 无 pending ∧ 无 async task）——
// 任一不满足即保留归属；三者皆无才释放。
func TestReleaseOwnerIfIdleConditions(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerInstanceAs(t, s, "ins-a")

	// ① 无任何运行态 → 释放
	s.claimSessionOwner("ins-a", "s-idle")
	s.releaseOwnerIfIdle("ins-a", "s-idle")
	if got := s.sessionOwner("s-idle"); got != "" {
		t.Fatalf("无活动运行态应释放归属，got %q", got)
	}

	// ② inbox 有 pending → 保留；转终态后 → 释放
	s.claimSessionOwner("ins-a", "s-inbox")
	s.inboxRemember(facade.InboxItem{ID: "in-1", SessionID: "s-inbox", Status: "pending", Type: "next_turn"})
	s.releaseOwnerIfIdle("ins-a", "s-inbox")
	if got := s.sessionOwner("s-inbox"); got != "ins-a" {
		t.Fatalf("inbox 有 pending 时不应释放归属，got %q", got)
	}
	s.inboxSetStatus("in-1", "consumed")
	s.releaseOwnerIfIdle("ins-a", "s-inbox")
	if got := s.sessionOwner("s-inbox"); got != "" {
		t.Fatalf("inbox 无 pending 后应释放归属，got %q", got)
	}

	// ③ 有 async task（任务树非终态节点）→ 保留；任务终态后 → 释放
	s.claimSessionOwner("ins-a", "s-async")
	node, err := s.tasks.start(&TaskNode{
		InstanceID: "ins-a", TopSession: "s-async", SessionID: "s-async", TurnID: "t-async",
		Tool: "core_file_read", Name: "async 任务",
	})
	if err != nil {
		t.Fatalf("start 任务节点：%v", err)
	}
	s.tasks.setState(node.TaskID, TaskStatePending)
	s.releaseOwnerIfIdle("ins-a", "s-async")
	if got := s.sessionOwner("s-async"); got != "ins-a" {
		t.Fatalf("有 async task 时不应释放归属，got %q", got)
	}
	s.tasks.done(node.TaskID, TaskStateDone, "", "")
	s.releaseOwnerIfIdle("ins-a", "s-async")
	if got := s.sessionOwner("s-async"); got != "" {
		t.Fatalf("async task 终态后应释放归属，got %q", got)
	}
}
