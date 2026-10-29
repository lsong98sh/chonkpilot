// INPUTVAL-1 会话域写侧校验单测（L1）。
//
// 一树一库：会话类入口一律带 `dialog_id`，故先经根会话自举拿 id。
package session

import (
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

func TestSessionTitleValidation(t *testing.T) {
	s, scope := newSessionEnvBus(t)
	const inst, sid = "ins-val", "s-val"
	root, err := s.SessionEnsure(facade.SessionEnsureRequest{InstanceID: inst, SessionID: sid, Scope: scope})
	if err != nil {
		t.Fatalf("SessionEnsure: %v", err)
	}
	dialogID := root.DialogID
	if _, err := s.SessionTitle(facade.SessionTitleRequest{
		InstanceID: inst, DialogID: dialogID, SessionID: sid, Title: strings.Repeat("题", maxSessionTitleChars+1), Scope: scope,
	}); err == nil {
		t.Fatalf("超长标题应拒绝")
	}
	if _, err := s.SessionTitle(facade.SessionTitleRequest{
		InstanceID: inst, DialogID: dialogID, SessionID: sid, Title: "a\x00b", Scope: scope,
	}); err == nil {
		t.Fatalf("控制字符应拒绝")
	}
	if _, err := s.SessionTitle(facade.SessionTitleRequest{
		InstanceID: inst, DialogID: dialogID, SessionID: sid, Title: strings.Repeat("题", maxSessionTitleChars), Scope: scope,
	}); err != nil {
		t.Fatalf("上限内标题应通过：%v", err)
	}
}

func TestTurnAndMessageValidation(t *testing.T) {
	s, scope := newSessionEnvBus(t)
	const inst = "ins-val2"
	root, err := s.SessionEnsure(facade.SessionEnsureRequest{InstanceID: inst, SessionID: "s-val2", Scope: scope})
	if err != nil {
		t.Fatalf("SessionEnsure: %v", err)
	}
	dialogID := root.DialogID
	if _, err := s.TurnEnsure(facade.TurnEnsureRequest{
		InstanceID: inst, DialogID: dialogID, TurnID: "t-val", SessionID: "s-val2", Scope: scope,
	}); err != nil {
		t.Fatalf("TurnEnsure: %v", err)
	}
	if _, err := s.TurnComplete(facade.TurnCompleteRequest{
		InstanceID: inst, DialogID: dialogID, TurnID: "t-val", Status: "bogus", Scope: scope,
	}); err == nil {
		t.Fatalf("非法轮次状态应拒绝")
	}
	if _, err := s.TurnComplete(facade.TurnCompleteRequest{
		InstanceID: inst, DialogID: dialogID, TurnID: "t-val", Status: "done", Scope: scope,
	}); err != nil {
		t.Fatalf("合法轮次状态应通过：%v", err)
	}

	if _, err := s.MessageAppend(facade.MessageAppendRequest{
		InstanceID: inst, DialogID: dialogID, TurnID: "t-val", Scope: scope,
		Message: facade.Message{Role: "robot", Content: "x"},
	}); err == nil {
		t.Fatalf("非法角色应拒绝")
	}
	if _, err := s.MessageAppend(facade.MessageAppendRequest{
		InstanceID: inst, DialogID: dialogID, TurnID: "t-val", Scope: scope,
		Message: facade.Message{Role: "user", Content: strings.Repeat("x", maxMessageChars+1)},
	}); err == nil {
		t.Fatalf("超长正文应拒绝")
	}
	if _, err := s.MessageAppend(facade.MessageAppendRequest{
		InstanceID: inst, DialogID: dialogID, TurnID: "t-val", Scope: scope,
		Message: facade.Message{Role: "user", Content: "hello"},
	}); err != nil {
		t.Fatalf("合法消息应通过：%v", err)
	}
}
