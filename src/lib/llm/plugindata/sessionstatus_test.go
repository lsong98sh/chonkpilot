// sessionstatus_test.go —— SESSION-STATUS-1：持久化状态 / 最终文本 / 指定轮次 / 等待 / 跨用户拒绝。
package plugindata

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionStatusDoneReturnsFinalText(t *testing.T) {
	s, usersRoot := newExportStore(t)
	seedDialogDB(t, filepath.Join(usersRoot, "alice"), "d-status",
		[]seedSession{{id: "s-status", title: "交付讨论", turns: []seedTurn{
			{id: "t1", msgs: []seedMsg{{"user", "第一问"}, {"assistant", "第一答"}}},
			{id: "t2", msgs: []seedMsg{{"user", "第二问"}, {"assistant", "最终答复"}}},
		}}})

	res, err := s.SessionStatus(context.Background(), SessionStatusRequest{
		WorkDir: testWorkDir, Mode: "private", SessionID: "s-status",
	})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if res.State != "done" || res.TurnID != "t2" || res.Text != "最终答复" {
		t.Fatalf("状态/文本不符：%+v", res)
	}
	if res.Session.DialogID != "d-status" || res.Session.TurnCount != 2 {
		t.Fatalf("会话元数据不符：%+v", res.Session)
	}
	if res.WaitedMs < 0 {
		t.Fatalf("waited_ms 异常：%d", res.WaitedMs)
	}

	// 指定旧轮次 → 读该轮最终文本。
	res, err = s.SessionStatus(context.Background(), SessionStatusRequest{
		WorkDir: testWorkDir, Mode: "private", SessionID: "s-status", TurnID: "t1",
	})
	if err != nil {
		t.Fatalf("status(t1): %v", err)
	}
	if res.TurnID != "t1" || res.Text != "第一答" {
		t.Fatalf("指定轮次不符：%+v", res)
	}

	// 指定不存在轮次 → not_found（不静默回落最新轮）。
	_, err = s.SessionStatus(context.Background(), SessionStatusRequest{
		WorkDir: testWorkDir, Mode: "private", SessionID: "s-status", TurnID: "nope",
	})
	if got := codeOf(t, err); got != "not_found" {
		t.Fatalf("未知轮次应 not_found：%q %v", got, err)
	}
}

func TestSessionStatusRunningAndWaitPrompt(t *testing.T) {
	s, usersRoot := newExportStore(t)
	seedDialogDB(t, filepath.Join(usersRoot, "alice"), "d-run",
		[]seedSession{{id: "s-run", title: "运行中", turns: []seedTurn{
			{id: "t-run", status: "running", msgs: []seedMsg{
				{"user", "长任务"}, {"assistant", "已产出片段"},
			}},
		}}})

	res, err := s.SessionStatus(context.Background(), SessionStatusRequest{
		WorkDir: testWorkDir, Mode: "private", SessionID: "s-run",
	})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if res.State != "running" || res.TurnStatus != "running" {
		t.Fatalf("应为 running：%+v", res)
	}
	if res.Output != "已产出片段" || res.Text != "" {
		t.Fatalf("running 应只回 output：%+v", res)
	}

	// 已完成 + wait_sec > 0：立即返回（不空等）。
	seedDialogDB(t, filepath.Join(usersRoot, "alice"), "d-done",
		[]seedSession{{id: "s-done", title: "已完成", turns: []seedTurn{
			{id: "t-done", msgs: []seedMsg{{"assistant", "好了"}}},
		}}})
	start := time.Now()
	res, err = s.SessionStatus(context.Background(), SessionStatusRequest{
		WorkDir: testWorkDir, Mode: "private", SessionID: "s-done", WaitSec: 10,
	})
	if err != nil {
		t.Fatalf("status(wait): %v", err)
	}
	if res.State != "done" || res.Text != "好了" {
		t.Fatalf("等待结果不符：%+v", res)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("终态不应空等：%v", elapsed)
	}
}

func TestSessionStatusPrivateCrossUserDenied(t *testing.T) {
	s, usersRoot := newExportStore(t)
	seedDialogDB(t, filepath.Join(usersRoot, "alice"), "d-a",
		[]seedSession{{id: "s-a", turns: []seedTurn{{id: "t-a", msgs: []seedMsg{{"assistant", "a"}}}}}})
	seedDialogDB(t, filepath.Join(usersRoot, "bob"), "d-b",
		[]seedSession{{id: "s-b", turns: []seedTurn{{id: "t-b", msgs: []seedMsg{{"assistant", "b"}}}}}})

	_, err := s.SessionStatus(context.Background(), SessionStatusRequest{
		WorkDir: testWorkDir, Mode: "private", SessionID: "s-b", Targets: []string{"bob"},
	})
	if got := codeOf(t, err); got != "permission_denied" {
		t.Fatalf("private 点名他人应 permission_denied：%q %v", got, err)
	}

	// public 可跨用户查看（显式 targets）。
	res, err := s.SessionStatus(context.Background(), SessionStatusRequest{
		WorkDir: testWorkDir, Mode: "public", SessionID: "s-b", Targets: []string{"bob"},
	})
	if err != nil {
		t.Fatalf("public status: %v", err)
	}
	if res.User != "bob" || res.State != "done" || res.Text != "b" {
		t.Fatalf("跨用户状态不符：%+v", res)
	}
}
