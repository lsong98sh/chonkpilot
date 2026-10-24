package session

import (
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// TestAskPending：按 messages.ask_id 回查（不建 ask 表）——未答过滤 / 精确回查（含终态）/
// 无 ask_id 行不命中 / 未命中返回空。
func TestAskPending(t *testing.T) {
	s, scope := newSessionEnv(t)
	disableShard(t, s, "ins-ask", scope) // 旧布局断言（P3-4d 起 shard 生效 → 跨对话库扫描）
	prj, err := s.PrjUsrFor("ins-ask", scope)
	if err != nil {
		t.Fatalf("prjusr: %v", err)
	}
	now := time.Now().UTC().Format(kernel.RFC3339FixedNano)
	rows := []struct {
		key string
		rec data.Record
	}{
		{"M-ask-1", data.Record{
			"session_id": "s-ask", "turn_id": "t-ask", "role": "tool", "tool_call_id": "c1",
			"tool_call_status": "running", "ask_id": "ask-1",
			"content": `{"call":{"tool_call_id":"c1","name":"ask_user","arguments":{"question":"继续吗？"}}}`, "created_at": now,
		}},
		{"M-ask-2", data.Record{
			"session_id": "s-ask", "turn_id": "t-ask", "role": "tool", "tool_call_id": "c2",
			"tool_call_status": "completed", "ask_id": "ask-2",
			"content": `{"call":{"tool_call_id":"c2","name":"ask_user","arguments":{"question":"选哪个？"}}}`, "created_at": now,
		}},
		{"M-msg", data.Record{"session_id": "s-ask", "turn_id": "t-ask", "role": "user", "content": "hi", "created_at": now}},
	}
	for _, r := range rows {
		if err := prj.Table("messages").Insert(r.key, r.rec); err != nil {
			t.Fatalf("insert %s: %v", r.key, err)
		}
	}

	// 启动扫描口径：只回非终态。
	resp, err := s.AskPending(facade.AskPendingRequest{InstanceID: "ins-ask", PendingOnly: true, Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 1 || resp.Items[0].AskID != "ask-1" || resp.Items[0].Status != "running" {
		t.Fatalf("未答 ask 回查错：%+v", resp.Items)
	}
	if q := resp.Items[0].Question; q != "继续吗？" {
		t.Fatalf("提问文本未解析：%q", q)
	}
	if resp.Items[0].SessionID != "s-ask" || resp.Items[0].TurnID != "t-ask" || resp.Items[0].ToolCallID != "c1" {
		t.Fatalf("归属字段错：%+v", resp.Items[0])
	}

	// 精确回查：含已终态（失效回执据此区分「已回答」与「可转新轮」）。
	resp, err = s.AskPending(facade.AskPendingRequest{InstanceID: "ins-ask", AskID: "ask-2", Scope: scope})
	if err != nil || len(resp.Items) != 1 || resp.Items[0].Status != "completed" {
		t.Fatalf("精确回查错：%+v %v", resp.Items, err)
	}

	// 未命中：空。
	resp, err = s.AskPending(facade.AskPendingRequest{InstanceID: "ins-ask", AskID: "ask-missing", Scope: scope})
	if err != nil || len(resp.Items) != 0 {
		t.Fatalf("未命中应空：%+v %v", resp.Items, err)
	}
}

// TestAskPendingAcrossDialogs（P3-4 残留回填）：一树一库生效、调用方无 dialog_id →
// 跨对话库扫描，并在结果项回带该 ask 所属 dialog_id。
func TestAskPendingAcrossDialogs(t *testing.T) {
	svc, instID, prjUsr, dlgID := setup(t) // shard 缺省 on；setup 已建主会话 m1 + 子会话 c1
	db, release, err := data.OpenExistingDialog(prjUsr, dlgID)
	if err != nil {
		t.Fatalf("open dialog: %v", err)
	}
	if err := db.Table("messages").Insert("M-ask-d", data.Record{
		"session_id": "c1", "turn_id": "t1", "role": "tool", "tool_call_id": "cd1",
		"tool_call_status": "running", "ask_id": "ask-d",
		"content":    `{"call":{"tool_call_id":"cd1","name":"ask_user","arguments":{"question":"继续？"}}}`,
		"created_at": ts0,
	}); err != nil {
		release()
		t.Fatalf("seed ask: %v", err)
	}
	release()

	resp, err := svc.AskPending(facade.AskPendingRequest{InstanceID: instID, PendingOnly: true})
	if err != nil {
		t.Fatalf("AskPending: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("应命中 1 条跨对话 ask，实际 %d", len(resp.Items))
	}
	if resp.Items[0].AskID != "ask-d" || resp.Items[0].DialogID != dlgID || resp.Items[0].SessionID != "c1" {
		t.Fatalf("结果项应回带 dialog_id/session_id: %+v", resp.Items[0])
	}
}
