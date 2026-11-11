package server

import (
	"context"
	"strings"
	"testing"
)

// TestStartScheduledTurnRejectsBusySession：定时智能体 / agent.run 在既有会话起轮，
// 同一会话已有运行轮次必须拒绝（不覆盖 busy 登记，避免打断用户会话）。
func TestStartScheduledTurnRejectsBusySession(t *testing.T) {
	s, _, _ := newTaskWaitFixture(t, "s-sched", "t-running")
	s.mu.Lock()
	s.busy[instKey("ins-test", "s-sched")] = "t-running"
	s.mu.Unlock()

	_, err := s.startScheduledTurn(context.Background(), StartReq{
		InstanceID: "ins-test", Session: "s-sched", Turn: "t-sched",
	}, "prompt")
	if err == nil || !strings.Contains(err.Error(), "会话正在执行其他轮次") {
		t.Fatalf("busy session 应拒绝，err=%v", err)
	}
}