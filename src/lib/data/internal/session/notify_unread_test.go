// 任务完成通知未读水位：append-notice 置未读 → 列表 notify_unread=true → 激活会话已读。
package session

import (
	"testing"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

func TestSessionNotifyUnreadSetAndActiveRead(t *testing.T) {
	s, scope := newSessionEnv(t)
	const inst = "ins-notify"
	res, err := s.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: inst, Scope: scope, SessionID: "s-main", Kind: "crontab_own", Title: "定时代码审查",
	})
	if err != nil {
		t.Fatalf("SessionEnsure: %v", err)
	}
	if res.DialogID == "" {
		t.Fatal("根会话应自举 dialog_id")
	}

	list := func() facade.Session {
		t.Helper()
		resp, err := s.SessionList(facade.SessionListRequest{InstanceID: inst, Scope: scope})
		if err != nil {
			t.Fatalf("SessionList: %v", err)
		}
		for _, it := range resp.List {
			if it.ID == "s-main" {
				return it
			}
		}
		t.Fatalf("列表缺 s-main: %+v", resp.List)
		return facade.Session{}
	}

	if list().NotifyUnread {
		t.Fatal("初始不应有未读")
	}
	if _, err := s.SessionNotifyUnreadSet(facade.SessionNotifyUnreadSetRequest{
		InstanceID: inst, Scope: scope, SessionID: "s-main", DialogID: res.DialogID, Unread: true,
	}); err != nil {
		t.Fatalf("置未读: %v", err)
	}
	if !list().NotifyUnread {
		t.Fatal("置未读后列表应 notify_unread=true")
	}

	if _, err := s.SessionActiveSet(facade.SessionActiveSetRequest{
		InstanceID: inst, Scope: scope, SessionID: "s-main", DialogID: res.DialogID,
	}); err != nil {
		t.Fatalf("SessionActiveSet: %v", err)
	}
	if list().NotifyUnread {
		t.Fatal("激活会话后应已读（持久化清除）")
	}

	// 再次置未读 → 另一个实例读同一 prjusr 库仍能看到（水位落 dialogs 目录行，非内存态）。
	if _, err := s.SessionNotifyUnreadSet(facade.SessionNotifyUnreadSetRequest{
		InstanceID: inst, Scope: scope, SessionID: "s-main", Unread: true,
	}); err != nil {
		t.Fatalf("二次置未读: %v", err)
	}
	if !list().NotifyUnread {
		t.Fatal("按 session_id 反查目录行失败")
	}
}
