// sessionquery_test.go —— SESSION-QUERY-1（2026-11-15）的 L1 用例：
// 跨用户模式（private / llm / public）、self/top 锚点、分页历史、路径穿越拒绝、共享数据根语义。
package plugindata

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
)

const testWorkDir = `E:\proj\a`

type seedMsg struct {
	role    string
	content string
}

type seedTurn struct {
	id     string
	status string // 空 = done
	msgs   []seedMsg
}

type seedSession struct {
	id       string
	title    string
	kind     string
	parentID string
	at       string
	turns    []seedTurn
}

// seedDialogDB 在一个真实对话库里写入多条会话 / 轮次 / 消息（时间戳递增，保证序稳定）。
func seedDialogDB(t *testing.T, prjUsrDir, dialogID string, sessions []seedSession) {
	t.Helper()
	db, err := data.Open(filepath.Join(prjUsrDir, "dialogs", data.DialogFileName(dialogID)))
	if err != nil {
		t.Fatalf("造对话库: %v", err)
	}
	defer db.Close()
	for _, ss := range sessions {
		at := ss.at
		if at == "" {
			at = time.Now().UTC().Format(data.RFC3339FixedNano)
		}
		rec := data.Record{
			"session_id": ss.id, "dialog_id": dialogID, "title": ss.title,
			"created_at": at, "updated_at": at, "parent_id": ss.parentID,
		}
		if ss.kind != "" {
			rec["kind"] = ss.kind
		}
		if err := db.Table(tableSessions).Insert(ss.id, rec); err != nil {
			t.Fatalf("造会话 %s: %v", ss.id, err)
		}
		for ti, turn := range ss.turns {
			tid := turn.id
			if tid == "" {
				tid = ss.id + "-t" + string(rune('1'+ti))
			}
			ts := time.Date(2026, 11, 1, 10, ti, 0, 0, time.UTC).Format(data.RFC3339FixedNano)
			status := turn.status
			if status == "" {
				status = "done"
			}
			if err := db.Table(tableTurns).Insert(tid, data.Record{
				"session_id": ss.id, "turn_id": tid, "created_at": ts, "status": status,
			}); err != nil {
				t.Fatalf("造轮次 %s: %v", tid, err)
			}
			for mi, msg := range turn.msgs {
				mid := tid + "-m" + string(rune('1'+mi))
				if err := db.Table(tableMessages).Insert(mid, data.Record{
					"session_id": ss.id, "turn_id": tid, "role": msg.role,
					"content": msg.content, "created_at": ts,
				}); err != nil {
					t.Fatalf("造消息 %s: %v", mid, err)
				}
			}
		}
	}
}

// seedTwoUsers 造 alice（主会话 + 子会话）与 bob（单会话）两个用户根。
func seedTwoUsers(t *testing.T) (*Store, string) {
	t.Helper()
	s, usersRoot := newExportStore(t)
	t0 := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC).Format(data.RFC3339FixedNano)
	seedDialogDB(t, filepath.Join(usersRoot, "alice"), "d-alice",
		[]seedSession{
			{id: "s-main", title: "主会话", parentID: "", at: t0, turns: []seedTurn{
				{id: "t-main-1", msgs: []seedMsg{{"user", "帮我安排登录重构"}, {"assistant", "好的，先列清单"}}},
				{id: "t-main-2", msgs: []seedMsg{{"user", "交付时间定在周五"}, {"assistant", "已记录：周五交付"}}},
				{id: "t-main-3", msgs: []seedMsg{{"user", "继续"}}},
			}},
			{id: "s-sub", title: "子任务", kind: "sub_chat", parentID: "s-main", at: t0},
		})
	seedDialogDB(t, filepath.Join(usersRoot, "bob"), "d-bob",
		[]seedSession{
			{id: "b-main", title: "Bob 的主会话", parentID: "", at: t0, turns: []seedTurn{
				{id: "t-bob-1", msgs: []seedMsg{{"user", "交付时间改到下周一"}, {"assistant", "收到"}}},
			}},
		})
	return s, usersRoot
}

// TestBrowseSessionsVisibilityAndAnchors：默认仅顶层；IncludeSub 含子会话；self/top 锚点生效。
func TestBrowseSessionsVisibilityAndAnchors(t *testing.T) {
	s, _ := seedTwoUsers(t)
	ctx := context.Background()

	res, err := s.BrowseSessions(ctx, SessionBrowseRequest{
		WorkDir: testWorkDir, Mode: "private", SelfSession: "s-main", TopSession: "s-main",
	})
	if err != nil {
		t.Fatalf("browse: %v", err)
	}
	if len(res.Sessions) != 1 || res.Sessions[0].SessionID != "s-main" {
		t.Fatalf("默认应只回顶层会话，got %+v", res.Sessions)
	}
	if !res.Sessions[0].Self || !res.Sessions[0].Top {
		t.Fatalf("self/top 锚点未标记: %+v", res.Sessions[0])
	}
	if res.Sessions[0].TurnCount != 3 {
		t.Fatalf("turn_count=%d, want 3", res.Sessions[0].TurnCount)
	}

	res, err = s.BrowseSessions(ctx, SessionBrowseRequest{
		WorkDir: testWorkDir, Mode: "private", IncludeSub: true,
	})
	if err != nil {
		t.Fatalf("browse include_sub: %v", err)
	}
	if len(res.Sessions) != 2 {
		t.Fatalf("IncludeSub 应回 2 条, got %d", len(res.Sessions))
	}
}

// TestBrowsePrivateDenied：private 下点名他人 → permission_denied（不是空结果），且落 denied 审计。
func TestBrowsePrivateDenied(t *testing.T) {
	s, _ := seedTwoUsers(t)
	_, err := s.BrowseSessions(context.Background(), SessionBrowseRequest{
		WorkDir: testWorkDir, Mode: "private", Targets: []string{"bob"},
	})
	if got := codeOf(t, err); got != pluginbridge.CodePermissionDenied {
		t.Fatalf("code=%q, want %q (%v)", got, pluginbridge.CodePermissionDenied, err)
	}
	recs := readAudit(t, s)
	if len(recs) == 0 || recs[len(recs)-1].Result != "denied" || recs[len(recs)-1].TargetUser != "bob" {
		t.Fatalf("缺少 denied 审计: %+v", recs)
	}
}

// TestBrowsePublicAllUsers：public + ["*"] → 跨用户结果带 user/cross_user；一次汇总审计。
func TestBrowsePublicAllUsers(t *testing.T) {
	s, _ := seedTwoUsers(t)
	res, err := s.BrowseSessions(context.Background(), SessionBrowseRequest{
		WorkDir: testWorkDir, Mode: "public", Targets: []string{"*"}, SelfSession: "s-main",
	})
	if err != nil {
		t.Fatalf("browse public: %v", err)
	}
	if len(res.Sessions) != 2 {
		t.Fatalf("应命中 alice+bob 各 1 条顶层会话, got %+v", res.Sessions)
	}
	var bob *SessionMeta
	for i := range res.Sessions {
		if res.Sessions[i].User == "bob" {
			bob = &res.Sessions[i]
		}
	}
	if bob == nil || !bob.CrossUser {
		t.Fatalf("bob 结果缺失或未标记 cross_user: %+v", res.Sessions)
	}
	recs := readAudit(t, s)
	if len(recs) != 1 || recs[0].Result != "ok" || len(recs[0].Targets) != 2 {
		t.Fatalf("应落一条汇总审计: %+v", recs)
	}
}

// TestSearchPrivateSelfOnly：private 缺省只检索自己；命中带 message_id/turn_id/snippet。
func TestSearchPrivateSelfOnly(t *testing.T) {
	s, _ := seedTwoUsers(t)
	res, err := s.SearchSessions(context.Background(), SessionSearchRequest{
		WorkDir: testWorkDir, Mode: "private", Query: "交付时间", SelfSession: "s-main",
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Hits) == 0 {
		t.Fatalf("未命中 alice 的消息")
	}
	for _, h := range res.Hits {
		if h.User != "alice" || h.CrossUser {
			t.Fatalf("private 下不应命中他人: %+v", h)
		}
		if !h.Self || h.MessageID == "" || h.TurnID == "" || !strings.Contains(h.Snippet, "交付时间") && !strings.Contains(h.Snippet, "周五") {
			t.Fatalf("命中缺少锚点/片段: %+v", h)
		}
	}
}

// TestSearchPublicCrossUser：public + ["*"] → 命中 bob，且 cross_user=true。
func TestSearchPublicCrossUser(t *testing.T) {
	s, _ := seedTwoUsers(t)
	res, err := s.SearchSessions(context.Background(), SessionSearchRequest{
		WorkDir: testWorkDir, Mode: "public", Targets: []string{"*"}, Query: "交付时间",
	})
	if err != nil {
		t.Fatalf("search public: %v", err)
	}
	var bobHit bool
	for _, h := range res.Hits {
		if h.User == "bob" {
			bobHit = true
			if !h.CrossUser {
				t.Fatalf("bob 命中未标记 cross_user: %+v", h)
			}
			if h.Title != "Bob 的主会话" {
				t.Fatalf("命中缺 title: %+v", h)
			}
		}
	}
	if !bobHit {
		t.Fatalf("public 未命中 bob: %+v", res.Hits)
	}
}

// TestTargetUserPathTraversalRejected：显式 user 必须是单段目录名。
func TestTargetUserPathTraversalRejected(t *testing.T) {
	s, _ := seedTwoUsers(t)
	_, err := s.SearchSessions(context.Background(), SessionSearchRequest{
		WorkDir: testWorkDir, Mode: "llm", Targets: []string{"../bob"}, Query: "交付",
	})
	if got := codeOf(t, err); got != pluginbridge.CodeArgsInvalid {
		t.Fatalf("code=%q, want %s (%v)", got, pluginbridge.CodeArgsInvalid, err)
	}
}

// TestReadHistoryPaging：before_turn_id + target_messages 分页；跨用户 meta 读取。
func TestReadHistoryPaging(t *testing.T) {
	s, _ := seedTwoUsers(t)
	ctx := context.Background()

	// 跨用户 meta（llm 模式）。
	meta, err := s.ReadSession(ctx, SessionReadRequest{
		WorkDir: testWorkDir, Mode: "llm", Targets: []string{"bob"}, SessionID: "b-main", View: "meta",
	})
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	if meta.User != "bob" || meta.Session.Title != "Bob 的主会话" || !meta.Session.CrossUser {
		t.Fatalf("跨用户 meta 异常: %+v", meta)
	}

	// 分页：target_messages=2 → 最新一轮（2 条消息）刚好，has_more=true。
	page, err := s.ReadSession(ctx, SessionReadRequest{
		WorkDir: testWorkDir, Mode: "private", SessionID: "s-main", View: "history", TargetMessages: 2,
	})
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	if len(page.Turns) != 1 || page.Turns[0].TurnID != "t-main-3" {
		t.Fatalf("分页应只回最新一轮: %+v", page.Turns)
	}
	if !page.HasMore {
		t.Fatalf("应 has_more=true")
	}

	// before_turn_id 跳过该轮及之后 → 回更早的轮。
	page, err = s.ReadSession(ctx, SessionReadRequest{
		WorkDir: testWorkDir, Mode: "private", SessionID: "s-main", View: "history", BeforeTurnID: "t-main-3",
	})
	if err != nil {
		t.Fatalf("read before_turn: %v", err)
	}
	if len(page.Turns) != 2 || page.Turns[len(page.Turns)-1].TurnID != "t-main-2" {
		t.Fatalf("before_turn_id 分页异常: %+v", page.Turns)
	}
}

// TestSharedDataDirCrossUserUnsupported：共享数据根形态无 users/ 分支 → 显式跨用户 unsupported。
func TestSharedDataDirCrossUserUnsupported(t *testing.T) {
	s := newStore(t, Options{DataDir: t.TempDir()})
	_, err := s.SearchSessions(context.Background(), SessionSearchRequest{
		WorkDir: testWorkDir, Mode: "llm", Targets: []string{"bob"}, Query: "x",
	})
	if got := codeOf(t, err); got != pluginbridge.CodeUnsupported {
		t.Fatalf("code=%q, want %s (%v)", got, pluginbridge.CodeUnsupported, err)
	}
}

// TestSessionExportRejectsTraversal 覆盖 export 经 resolveUser 的同一道校验。
func TestSessionExportRejectsTraversal(t *testing.T) {
	s, _ := seedTwoUsers(t)
	_, err := s.sessionExport(pluginbridge.SessionExportRequest{
		WorkDir: testWorkDir, User: "../bob", SessionID: "b-main",
	})
	if got := codeOf(t, err); got != pluginbridge.CodeArgsInvalid {
		t.Fatalf("code=%q, want %s (%v)", got, pluginbridge.CodeArgsInvalid, err)
	}
}
