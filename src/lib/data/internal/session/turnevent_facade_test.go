// TURN EVENT（MOD-1 M3）：事件日志写入幂等 + 终态保留期清理。
package session

import (
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// TestTurnEventAppendIdempotent：同 turn+seq 重复写入幂等；type 冲突 / 非法入参 → 明确失败。
func TestTurnEventAppendIdempotent(t *testing.T) {
	s, scope := newSessionEnv(t)
	req := facade.TurnEventAppendRequest{
		InstanceID: "ins-te", Scope: scope, SessionID: "s1", TurnID: "t1",
		Seq: 1, Type: "turn_start", Payload: []byte(`{"session_id":"s1"}`),
	}
	if _, err := s.TurnEventAppend(req); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := s.TurnEventAppend(req); err != nil {
		t.Fatalf("幂等重复写入应成功：%v", err)
	}

	conflict := req
	conflict.Type = "step_start"
	if _, err := s.TurnEventAppend(conflict); err == nil {
		t.Fatal("同 seq 不同 type 应失败")
	}

	bad := req
	bad.Seq = 0
	if _, err := s.TurnEventAppend(bad); err == nil {
		t.Fatal("seq=0 应失败")
	}
	bad = req
	bad.TurnID = "a/b"
	if _, err := s.TurnEventAppend(bad); err == nil {
		t.Fatal("turn_id 含 '/' 应失败")
	}
	bad = req
	bad.Type = ""
	if _, err := s.TurnEventAppend(bad); err == nil {
		t.Fatal("type 为空应失败")
	}
}

// TestTurnEventCleanupRetention：只删「终态 + 超保留期」turn 的事件；running / 未超期 / 时间不可解析保留；
// retention=0 永久（不删）。
func TestTurnEventCleanupRetention(t *testing.T) {
	s, scope := newSessionEnv(t)
	prj, err := s.PrjUsrFor("ins-te-clean", scope)
	if err != nil {
		t.Fatalf("prjusr: %v", err)
	}
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	ts := func(d time.Duration) string { return now.Add(d).Format(kernel.RFC3339FixedNano) }

	turns := map[string]data.Record{
		"t-old-done":      {"session_id": "s", "status": "done", "created_at": ts(-20 * 24 * time.Hour), "updated_at": ts(-10 * 24 * time.Hour)},
		"t-old-interrupt": {"session_id": "s", "status": "interrupted", "created_at": ts(-20 * 24 * time.Hour), "updated_at": ts(-9 * 24 * time.Hour)},
		"t-old-running":   {"session_id": "s", "status": "running", "created_at": ts(-30 * 24 * time.Hour), "updated_at": ts(-30 * 24 * time.Hour)},
		"t-new-done":      {"session_id": "s", "status": "done", "created_at": ts(-3 * 24 * time.Hour), "updated_at": ts(-1 * 24 * time.Hour)},
		"t-bad-ts":        {"session_id": "s", "status": "done", "created_at": "bad", "updated_at": "bad"},
	}
	for k, r := range turns {
		if err := prj.Table("turns").Insert(k, r); err != nil {
			t.Fatalf("insert turn %s: %v", k, err)
		}
		// 每个 turn 落 2 条事件。
		for seq := uint64(1); seq <= 2; seq++ {
			if _, err := s.TurnEventAppend(facade.TurnEventAppendRequest{
				InstanceID: "ins-te-clean", Scope: scope, SessionID: "s", TurnID: k,
				Seq: seq, Type: "step_start",
			}); err != nil {
				t.Fatalf("append %s/%d: %v", k, seq, err)
			}
		}
	}

	// retention=0 = 永久 → 不删。
	resp, err := s.TurnEventCleanup(facade.TurnEventCleanupRequest{
		InstanceID: "ins-te-clean", Scope: scope, RetentionDays: 0,
	})
	if err != nil || resp.Deleted != 0 {
		t.Fatalf("永久保留：resp=%+v err=%v", resp, err)
	}

	resp, err = s.TurnEventCleanup(facade.TurnEventCleanupRequest{
		InstanceID: "ins-te-clean", Scope: scope, RetentionDays: 7,
	})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if resp.Deleted != 4 { // t-old-done + t-old-interrupt 各 2 条
		t.Fatalf("deleted=%d want 4", resp.Deleted)
	}

	count := func(turnID string) int {
		n := 0
		_ = prj.Table(turnEventTable).ForEachPrefix(turnID+"/", func(string, data.Record) error {
			n++
			return nil
		})
		return n
	}
	if count("t-old-done") != 0 || count("t-old-interrupt") != 0 {
		t.Fatal("超期终态 turn 的事件应已删除")
	}
	for _, k := range []string{"t-old-running", "t-new-done", "t-bad-ts"} {
		if count(k) != 2 {
			t.Fatalf("%s 事件应保留（running / 未超期 / 时间不可解析）：got %d", k, count(k))
		}
	}
}
