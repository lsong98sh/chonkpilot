// G-79（2026-10-31）：快照分支下「消息侧跳过的 interrupted 轮」必须同步从 `turn_tokens` 移除，
// 否则尾部对齐会整体偏移一格 → 压缩锚点 LastTurnID / 三段边界偏移。
package session

import (
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
)

func TestMessageContextTurnTokensSkipInterrupted(t *testing.T) {
	svc, instID, prjUsr, dlgID := setup(t)
	sdb, release, err := data.OpenExistingDialog(prjUsr, dlgID)
	if err != nil {
		t.Fatalf("open dialog: %v", err)
	}
	// 两个历史轮：t1 done（快照覆盖到它）· t2 interrupted（不应进上下文）。
	const ts1 = "2026-10-24T10:00:00.000000002Z"
	for _, r := range []data.Record{
		{"turn_id": "t1", "session_id": "m1", "status": "done", "created_at": ts0, "updated_at": ts0},
		{"turn_id": "t2", "session_id": "m1", "status": "interrupted", "created_at": ts1, "updated_at": ts1},
	} {
		if err := sdb.Table("turns").Upsert(r["turn_id"].(string), r); err != nil {
			t.Fatalf("seed turn: %v", err)
		}
	}
	for _, m := range []struct{ key, turn, content string }{
		{"m-t1", "t1", "q1"},
		{"m-t2", "t2", "q2"},
	} {
		if err := sdb.Table("messages").Insert(m.key, data.Record{
			"session_id": "m1", "turn_id": m.turn, "role": "user", "content": m.content, "created_at": ts0,
		}); err != nil {
			t.Fatalf("seed message %s: %v", m.key, err)
		}
	}
	if err := sdb.Table("snapshot").Insert("m1", data.Record{
		"session_id": "m1", "snapshot_turn": "t1",
		"history":    `[{"role":"user","content":"q1"}]`,
		"updated_at": ts0,
	}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	release()

	got, err := svc.MessageContext(facade.MessageContextRequest{
		InstanceID: instID, DialogID: dlgID, SessionID: "m1", IncludeSnapshot: true,
	})
	if err != nil {
		t.Fatalf("MessageContext: %v", err)
	}
	if len(got.Messages) != 1 || got.Messages[0].Content != "q1" {
		t.Fatalf("interrupted 轮不应进上下文：%+v", got.Messages)
	}
	if len(got.TurnTokens) != 1 || got.TurnTokens[0].TurnID != "t1" {
		t.Fatalf("turn_tokens 应与消息侧对齐（只含 t1）：%+v", got.TurnTokens)
	}
}
