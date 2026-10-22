// INPUTVAL-1 会话域写侧校验单测（L1）。
package session

import (
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

func TestSessionTitleValidation(t *testing.T) {
	s, scope := newSessionEnvBus(t)
	const inst, sid = "ins-val", "s-val"
	if _, err := s.SessionEnsure(facade.SessionEnsureRequest{InstanceID: inst, SessionID: sid, Scope: scope}); err != nil {
		t.Fatalf("SessionEnsure: %v", err)
	}
	if _, err := s.SessionTitle(facade.SessionTitleRequest{
		InstanceID: inst, SessionID: sid, Title: strings.Repeat("题", maxSessionTitleChars+1), Scope: scope,
	}); err == nil {
		t.Fatalf("超长标题应拒绝")
	}
	if _, err := s.SessionTitle(facade.SessionTitleRequest{
		InstanceID: inst, SessionID: sid, Title: "a\x00b", Scope: scope,
	}); err == nil {
		t.Fatalf("控制字符应拒绝")
	}
	if _, err := s.SessionTitle(facade.SessionTitleRequest{
		InstanceID: inst, SessionID: sid, Title: strings.Repeat("题", maxSessionTitleChars), Scope: scope,
	}); err != nil {
		t.Fatalf("上限内标题应通过：%v", err)
	}
}

func TestTurnAndMessageValidation(t *testing.T) {
	s, scope := newSessionEnvBus(t)
	const inst = "ins-val2"
	if _, err := s.TurnEnsure(facade.TurnEnsureRequest{InstanceID: inst, TurnID: "t-val", SessionID: "s-val2", Scope: scope}); err != nil {
		t.Fatalf("TurnEnsure: %v", err)
	}
	if _, err := s.TurnComplete(facade.TurnCompleteRequest{
		InstanceID: inst, TurnID: "t-val", Status: "bogus", Scope: scope,
	}); err == nil {
		t.Fatalf("非法轮次状态应拒绝")
	}
	if _, err := s.TurnComplete(facade.TurnCompleteRequest{
		InstanceID: inst, TurnID: "t-val", Status: "done", Scope: scope,
	}); err != nil {
		t.Fatalf("合法轮次状态应通过：%v", err)
	}

	if _, err := s.MessageAppend(facade.MessageAppendRequest{
		InstanceID: inst, TurnID: "t-val", Scope: scope,
		Message: facade.Message{Role: "robot", Content: "x"},
	}); err == nil {
		t.Fatalf("非法角色应拒绝")
	}
	if _, err := s.MessageAppend(facade.MessageAppendRequest{
		InstanceID: inst, TurnID: "t-val", Scope: scope,
		Message: facade.Message{Role: "user", Content: strings.Repeat("x", maxMessageChars+1)},
	}); err == nil {
		t.Fatalf("超长正文应拒绝")
	}
	if _, err := s.MessageAppend(facade.MessageAppendRequest{
		InstanceID: inst, TurnID: "t-val", Scope: scope,
		Message: facade.Message{Role: "user", Content: "hello"},
	}); err != nil {
		t.Fatalf("合法消息应通过：%v", err)
	}
}
