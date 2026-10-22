// INBOX 收尾（G-65）：终态行 TTL 清理 + 会话删除级联。
//
// 设计：docs/agent-chat/中途插话设计方案.md §5.2（consumed/discarded 保留 7 天；pending 永不自动删；
// 会话删除级联删除 inbox_items，不留孤儿）。
package session

import (
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

func mustInboxRow(t *testing.T, tb *data.Table, key string) (data.Record, bool) {
	t.Helper()
	var rec data.Record
	ok, err := tb.Get(key, &rec)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	return rec, ok
}

// TestInboxCommit：同事务落消息 + 置 consumed；同 inbox_id 幂等（不重复落库）；discarded 拒绝。
func TestInboxCommit(t *testing.T) {
	s, scope := newSessionEnv(t)
	prj, err := s.PrjUsrFor("ins-inbox-commit", scope)
	if err != nil {
		t.Fatalf("prjusr: %v", err)
	}
	inboxKey, err := kernel.NewInboxKey()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(kernel.RFC3339FixedNano)
	if err := prj.Table(inboxTable).Insert(inboxKey, data.Record{
		"session_id": "s-commit", "type": "insert", "content": "hi",
		"status": "pending", "created_at": now,
	}); err != nil {
		t.Fatal(err)
	}

	req := facade.InboxCommitRequest{
		InstanceID: "ins-inbox-commit", InboxID: inboxKey, TurnID: "t-commit",
		Message: facade.Message{Role: "user", Content: "hi", Kind: "insert"},
		Scope:   scope,
	}
	resp, err := s.InboxCommit(req)
	if err != nil {
		t.Fatalf("InboxCommit: %v", err)
	}
	if resp.ID != kernel.InboxMessageKey(inboxKey) {
		t.Fatalf("消息主键应为确定性键：%q", resp.ID)
	}
	var row data.Record
	if ok, err := prj.Table("messages").Get(resp.ID, &row); err != nil || !ok {
		t.Fatalf("消息未落库：ok=%v err=%v", ok, err)
	}
	if row["content"] != "hi" || row["inbox_id"] != inboxKey || row["session_id"] != "s-commit" {
		t.Fatalf("消息行错：%+v", row)
	}
	// 索引同事务维护：按 turn 索引可查到该行。
	recs, _, err := prj.Table("messages").Query(data.Query{
		Where: data.Record{"turn_id": "t-commit"}, OrderBy: "created_at",
	})
	if err != nil || len(recs) != 1 {
		t.Fatalf("turn 索引未命中：%d %v", len(recs), err)
	}
	inbox, ok := mustInboxRow(t, prj.Table(inboxTable), inboxKey)
	if !ok || inbox["status"] != "consumed" {
		t.Fatalf("inbox 未置 consumed：%+v", inbox)
	}

	// 重试幂等：同 inbox_id 再提交 → 同主键，消息数不变。
	resp2, err := s.InboxCommit(req)
	if err != nil || resp2.ID != resp.ID {
		t.Fatalf("重试应幂等：%+v %v", resp2, err)
	}
	recs, _, err = prj.Table("messages").Query(data.Query{
		Where: data.Record{"turn_id": "t-commit"}, OrderBy: "created_at",
	})
	if err != nil || len(recs) != 1 {
		t.Fatalf("重试后消息数应仍为 1：%d %v", len(recs), err)
	}

	// discarded → 拒绝消费。
	discarded, _ := kernel.NewInboxKey()
	if err := prj.Table(inboxTable).Insert(discarded, data.Record{
		"session_id": "s-commit", "type": "insert", "content": "x",
		"status": "discarded", "created_at": now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InboxCommit(facade.InboxCommitRequest{
		InstanceID: "ins-inbox-commit", InboxID: discarded, TurnID: "t-commit",
		Message: facade.Message{Role: "user", Content: "x"}, Scope: scope,
	}); err == nil {
		t.Fatal("discarded 的 inbox 不应被消费")
	}
}

// TestCleanupInboxTTL：仅删除「终态 + 超期」行；pending（无论多旧）与未超期终态行保留；
// 时间不可解析 → 保守保留（不误删）。
func TestCleanupInboxTTL(t *testing.T) {
	s, scope := newSessionEnv(t)
	prj, err := s.PrjUsrFor("ins-inbox-ttl", scope)
	if err != nil {
		t.Fatalf("prjusr: %v", err)
	}
	tb := prj.Table(inboxTable)
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	ts := func(d time.Duration) string { return now.Add(d).Format(kernel.RFC3339FixedNano) }

	rows := map[string]data.Record{
		// 超期终态 → 删除（updated_at 为终态落定时间）
		"old-consumed": {"session_id": "s", "status": "consumed", "created_at": ts(-10 * 24 * time.Hour), "updated_at": ts(-8 * 24 * time.Hour)},
		"old-discarded": {"session_id": "s", "status": "discarded", "created_at": ts(-10 * 24 * time.Hour), "updated_at": ts(-8 * 24 * time.Hour)},
		// 无 updated_at → 回落 created_at（超期 → 删除）
		"old-no-updated": {"session_id": "s", "status": "consumed", "created_at": ts(-9 * 24 * time.Hour)},
		// 超期 pending → 永不自动删
		"old-pending": {"session_id": "s", "status": "pending", "created_at": ts(-30 * 24 * time.Hour)},
		// 终态但未超期 → 保留
		"new-consumed": {"session_id": "s", "status": "consumed", "created_at": ts(-2 * 24 * time.Hour), "updated_at": ts(-1 * 24 * time.Hour)},
		// 时间不可解析 → 保守保留
		"bad-ts": {"session_id": "s", "status": "consumed", "created_at": "not-a-time", "updated_at": "not-a-time"},
	}
	for k, r := range rows {
		if err := tb.Insert(k, r); err != nil {
			t.Fatalf("insert %s: %v", k, err)
		}
	}

	s.cleanupInboxTTL(prj, now)

	for _, k := range []string{"old-consumed", "old-discarded", "old-no-updated"} {
		if _, ok := mustInboxRow(t, tb, k); ok {
			t.Fatalf("%s 超期终态行应被 TTL 清理", k)
		}
	}
	for _, k := range []string{"old-pending", "new-consumed", "bad-ts"} {
		if _, ok := mustInboxRow(t, tb, k); !ok {
			t.Fatalf("%s 不应被 TTL 清理", k)
		}
	}
}

// TestSessionDeleteCascadeInbox：删会话级联删除其全部 inbox 行（含 pending），其他会话不受影响；
// 幂等删除「不存在的会话」也会清理历史孤儿行。
func TestSessionDeleteCascadeInbox(t *testing.T) {
	s, scope := newSessionEnv(t)
	const inst = "ins-inbox-cascade"
	prj, err := s.PrjUsrFor(inst, scope)
	if err != nil {
		t.Fatalf("prjusr: %v", err)
	}
	created := time.Now().UTC().Format(kernel.RFC3339FixedNano)
	if err := prj.Table("sessions").Insert("s1", data.Record{
		"session_id": "s1", "parent_id": "", "title": "t", "created_at": created, "updated_at": created,
	}); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	tb := prj.Table(inboxTable)
	for k, r := range map[string]data.Record{
		"a1": {"session_id": "s1", "status": "pending", "type": "next_turn", "content": "a1", "created_at": created},
		"a2": {"session_id": "s1", "status": "consumed", "type": "insert", "content": "a2", "created_at": created},
		"b1": {"session_id": "s2", "status": "pending", "type": "next_turn", "content": "b1", "created_at": created},
	} {
		if err := tb.Insert(k, r); err != nil {
			t.Fatalf("insert %s: %v", k, err)
		}
	}

	if _, err := s.SessionDelete(facade.SessionDeleteRequest{InstanceID: inst, Scope: scope, SessionID: "s1"}); err != nil {
		t.Fatalf("SessionDelete: %v", err)
	}
	for _, k := range []string{"a1", "a2"} {
		if _, ok := mustInboxRow(t, tb, k); ok {
			t.Fatalf("%s 应随会话级联删除", k)
		}
	}
	if _, ok := mustInboxRow(t, tb, "b1"); !ok {
		t.Fatalf("其他会话的 inbox 行不应被波及")
	}

	// 幂等删除不存在的会话：仍收敛该 session_id 的历史孤儿行。
	if err := tb.Insert("orphan", data.Record{
		"session_id": "s9", "status": "pending", "type": "next_turn", "content": "o", "created_at": created,
	}); err != nil {
		t.Fatalf("insert orphan: %v", err)
	}
	if _, err := s.SessionDelete(facade.SessionDeleteRequest{InstanceID: inst, Scope: scope, SessionID: "s9"}); err != nil {
		t.Fatalf("SessionDelete(not exists): %v", err)
	}
	if _, ok := mustInboxRow(t, tb, "orphan"); ok {
		t.Fatalf("删除不存在的会话也应清理其孤儿 inbox 行")
	}
}
