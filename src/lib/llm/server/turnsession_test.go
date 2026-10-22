// A4-P3a-0：turn 生命周期接口抽象（行为零变更）的最小验证。
package server

import (
	"context"
	"testing"
	"time"
)

// fakeTurnSession 是最小 turnSession 实现（非 *turnCtx）：用于验证旧 *turnCtx 类型化助手的
// 防御分支（断言失败 → 返回 nil，不 panic）。
type fakeTurnSession struct {
	instanceID string
	sessionID  string
	turnID     string
	closed     bool
}

func (f *fakeTurnSession) InstanceID() string { return f.instanceID }
func (f *fakeTurnSession) SessionID() string  { return f.sessionID }
func (f *fakeTurnSession) TurnID() string     { return f.turnID }
func (f *fakeTurnSession) Close()             { f.closed = true }

// TestTurnSessionInterface：*turnCtx 满足 turnSession / turnInput；接口容器（s.turns）定位
// 与身份访问器正确；非 *turnCtx 句柄走旧的类型化助手时返回 nil（防御分支）。
func TestTurnSessionInterface(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	tc := newTurnCtx(context.Background(), s, StartReq{
		InstanceID: "ins-intf", Session: "s-intf", Turn: "t-intf",
	})
	defer func() {
		tc.Close()
		time.Sleep(150 * time.Millisecond) // 等 loop 退出（避免 TempDir 清理撞 DB）
	}()

	s.mu.Lock()
	s.registerTurnLocked(instKey("ins-intf", "t-intf"), tc)
	s.mu.Unlock()

	// 接口定位：同一实例
	if got := s.lookupTurnSession("ins-intf", "t-intf"); got != turnSession(tc) {
		t.Fatalf("lookupTurnSession = %v, want turnCtx 本体", got)
	}
	// 旧类型化助手：语义不变（内部断言）
	if got := s.lookupTurnIn("ins-intf", "t-intf"); got != tc {
		t.Fatalf("lookupTurnIn = %v, want turnCtx 本体", got)
	}
	if got := s.turnBySessionForAdd("ins-intf", "s-intf"); got != tc {
		t.Fatalf("turnBySessionForAdd = %v, want turnCtx 本体", got)
	}
	// 身份访问器与 req 同源
	if tc.InstanceID() != "ins-intf" || tc.SessionID() != "s-intf" || tc.TurnID() != "t-intf" {
		t.Fatalf("身份访问器不符: instance=%s session=%s turn=%s",
			tc.InstanceID(), tc.SessionID(), tc.TurnID())
	}
	// *turnCtx 亦满足输入投递接口
	if _, ok := turnSession(tc).(turnInput); !ok {
		t.Fatal("turnCtx 未实现 turnInput")
	}

	// 非 *turnCtx 句柄：旧助手返回 nil（防御分支），接口助手可正常取到。
	fake := &fakeTurnSession{instanceID: "ins-intf", sessionID: "s-fake", turnID: "t-fake"}
	s.mu.Lock()
	s.registerTurnLocked(instKey("ins-intf", "t-fake"), fake)
	s.mu.Unlock()
	if got := s.lookupTurnSession("ins-intf", "t-fake"); got != turnSession(fake) {
		t.Fatalf("lookupTurnSession(fake) = %v, want fake 本体", got)
	}
	if got := s.lookupTurnIn("ins-intf", "t-fake"); got != nil {
		t.Fatalf("lookupTurnIn(fake) = %v, want nil（防御分支）", got)
	}
	s.mu.Lock()
	delete(s.turns, instKey("ins-intf", "t-fake"))
	s.mu.Unlock()
	fake.Close()
}
