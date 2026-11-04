//go:build windows

package host

import (
	"encoding/json"
	"testing"

	gui "github.com/lsong98sh/chonkpilot-framework/host/gui"
	"github.com/chonkpilot/chonkpilot-workspace"
)

// fakeWinOps 是 projectWindowOps 的假实现（只记录调用）。
type fakeWinOps struct {
	hidden   []string
	closed   []string
	chatID   string
	chatSess string // 最近一次 OpenChatWindow 的 session_id
	chatDlg  string // 最近一次 OpenChatWindow 的 dialog_id（G-105）
	ok       bool
}

func (f *fakeWinOps) HideProject(workDir string) bool {
	f.hidden = append(f.hidden, workDir)
	return f.ok
}

func (f *fakeWinOps) CloseProject(workDir string) bool {
	f.closed = append(f.closed, workDir)
	return f.ok
}

func (f *fakeWinOps) OpenChatWindow(_, sessionID, dialogID, _ string) (string, bool, bool) {
	f.chatSess, f.chatDlg = sessionID, dialogID
	return f.chatID, false, f.ok
}

func (f *fakeWinOps) ProjectBridge(string) gui.Bridge { return nil }

// TestProjectOpsWindowActions（41 G-100）：窗口动作经框架薄动作落地（main 复用隐藏打开、
// chat 需 session_id、Hide/Close 照传）。
func TestProjectOpsWindowActions(t *testing.T) {
	var opened []struct {
		dir    string
		hidden bool
	}
	ops := hostProjectOps{
		env: &fakeWinOps{chatID: "w7", ok: true},
		open: func(dir string, hidden bool) error {
			opened = append(opened, struct {
				dir    string
				hidden bool
			}{dir, hidden})
			return nil
		},
		instanceID: func(string) (string, bool) { return "inst-1", true },
	}

	// main + visible=false → 隐藏打开。
	id, err := ops.CreateWindow(`C:\ws`, workspace.WindowMain, false, nil)
	if err != nil || id != "inst-1" {
		t.Fatalf("main CreateWindow = (%q,%v)", id, err)
	}
	if len(opened) != 1 || !opened[0].hidden || opened[0].dir != `C:\ws` {
		t.Fatalf("隐藏打开参数不符: %+v", opened)
	}

	// chat 缺 session_id → 明确失败。
	if _, err := ops.CreateWindow(`C:\ws`, workspace.WindowChat, true, nil); err == nil {
		t.Fatal("chat 缺 session_id 应失败")
	}
	wid, err := ops.CreateWindow(`C:\ws`, workspace.WindowChat, true, map[string]any{"session_id": "s-1"})
	if err != nil || wid != "w7" {
		t.Fatalf("chat CreateWindow = (%q,%v)", wid, err)
	}
	// G-105：可选 params["dialog_id"] 透传给窗口动作（不带时不落键 → 空串 = 行为不变）。
	fw := ops.env.(*fakeWinOps)
	if fw.chatSess != "s-1" || fw.chatDlg != "" {
		t.Fatalf("缺省 dialog_id 不应透传非空值: sess=%q dlg=%q", fw.chatSess, fw.chatDlg)
	}
	if _, err := ops.CreateWindow(`C:\ws`, workspace.WindowChat, true, map[string]any{
		"session_id": "s-2", "dialog_id": "dg-9",
	}); err != nil {
		t.Fatalf("chat CreateWindow(dialog_id): %v", err)
	}
	if fw.chatSess != "s-2" || fw.chatDlg != "dg-9" {
		t.Fatalf("dialog_id 未透传: sess=%q dlg=%q", fw.chatSess, fw.chatDlg)
	}

	if err := ops.Hide(`C:\ws`); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	if err := ops.Close(`C:\ws`); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(fw.hidden) != 1 || len(fw.closed) != 1 {
		t.Fatalf("Hide/Close 未落到窗口动作: hidden=%v closed=%v", fw.hidden, fw.closed)
	}
}

// TestProjectOpsSessionPayload（41 G-100）：会话类走既有 `data-session-ensure-session` 载荷，
// 根会话不带 dialog_id（自举），子会话带；空值不落键。
func TestProjectOpsSessionPayload(t *testing.T) {
	root, err := ensureSessionPayload("s-1", "", map[string]any{
		"kind": "crontab", "title": "定时", "origin": "scheduler", "parent_session_id": "s-root",
	})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(root, &m); err != nil {
		t.Fatal(err)
	}
	if _, has := m["dialog_id"]; has {
		t.Fatalf("根会话不应带 dialog_id: %s", root)
	}
	if m["kind"] != "crontab" || m["origin"] != "scheduler" || m["parent_session_id"] != "s-root" {
		t.Fatalf("可选参数丢失: %s", root)
	}

	child, _ := ensureSessionPayload("s-2", "dg-1", map[string]any{"title": ""})
	var cm map[string]any
	_ = json.Unmarshal(child, &cm)
	if cm["dialog_id"] != "dg-1" {
		t.Fatalf("子会话应带 dialog_id: %s", child)
	}
	if _, has := cm["title"]; has {
		t.Fatalf("空值不应落键: %s", child)
	}
}

// TestProjectOpsSessionID：调用方指定优先；否则用注入的生成器。
func TestProjectOpsSessionID(t *testing.T) {
	ops := hostProjectOps{newID: func() string { return "s-fixed" }}
	if got := ops.sessionID(map[string]any{"session_id": "s-given"}); got != "s-given" {
		t.Fatalf("指定 session_id 未沿用: %q", got)
	}
	if got := ops.sessionID(nil); got != "s-fixed" {
		t.Fatalf("未用注入生成器: %q", got)
	}
	if got := (hostProjectOps{}).sessionID(nil); len(got) < 3 || got[:2] != "s-" {
		t.Fatalf("缺省生成 id 形态不符: %q", got)
	}
}
