// 会话数据原语门面直调回归（统一异步 P0）：会话循环内的 persistMessage / LoadMessages /
// BuildContextTokens / SetSnapshot 不得再走 `data-*` 总线同步应答（Emit().Wait）。
//
// 判据：门面非空 → 全部动作直调；此处用**未启动 persist 的总线**构造，若任一路径回退总线，
// 应答超时即失败。
package server

import (
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
)

func TestSessionStoreFacadeDirect(t *testing.T) {
	data.Reset()
	t.Cleanup(data.Reset)
	wd := t.TempDir()
	data.Register("ins-direct", wd, "")

	bus, err := mq.New(mq.Options{Prefix: testBusPrefix})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	// 总线**不启动 persist**：任何回退 dataRequest 的路径都会等 3s 后超时失败。
	api := inline.NewWithOptions(nil, persist.Options{UsrPath: t.TempDir() + "/usr.db"})
	st := newSessionStoreWithFacade(api, bus, "ins-direct")

	if err := st.EnsureSession("s-direct"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if st.idx.dialogOf("s-direct") == "" {
		t.Fatalf("EnsureSession 未回填 dialog_id")
	}
	if err := st.EnsureTurn("t-direct", "s-direct"); err != nil {
		t.Fatalf("EnsureTurn: %v", err)
	}
	if _, err := st.AppendFullKeyed("t-direct", ChatMsg{Role: "user", Kind: "text", Content: "你好"}, ""); err != nil {
		t.Fatalf("AppendFullKeyed: %v", err)
	}
	msgs := st.LoadMessages("t-direct")
	if len(msgs) != 1 || msgs[0].Content != "你好" {
		t.Fatalf("LoadMessages: %+v", msgs)
	}
	if _, err := st.AppendMsgMap("t-direct", map[string]any{
		"role": "tool", "content": `{"call":{"tool_call_id":"tc1"}}`,
		"tool_call_id": "tc1", "tool_call_status": "completed",
	}, ""); err != nil {
		t.Fatalf("AppendMsgMap: %v", err)
	}
	if cur, err := st.req("content", map[string]any{
		"session_id": "s-direct", "keys": []string{"tool_result:tc1"},
	}); err != nil {
		t.Fatalf("content: %v", err)
	} else if _, ok := cur["contents"].(map[string]any); !ok {
		t.Fatalf("content 应答形状不符: %+v", cur)
	}
	if hist, _ := st.BuildContextTokens("s-direct", "", false); len(hist) == 0 {
		t.Fatalf("BuildContextTokens 应含已落消息")
	}
	if err := st.CompleteTurnTokens("t-direct", "done", "stop", nil, nil); err != nil {
		t.Fatalf("CompleteTurnTokens: %v", err)
	}
	if got := st.LatestTurnID("s-direct"); got != "t-direct" {
		t.Fatalf("LatestTurnID: got %q want t-direct", got)
	}
	if err := st.SetSnapshot("s-direct", msgs, "t-direct"); err != nil {
		t.Fatalf("SetSnapshot: %v", err)
	}
	if err := st.CleanupStaleTurns(); err != nil {
		t.Fatalf("CleanupStaleTurns: %v", err)
	}
	if err := st.CleanupTurnEvents(7); err != nil {
		t.Fatalf("CleanupTurnEvents: %v", err)
	}
	// 关库后 TempDir 才能清理（Windows 句柄）。
	t.Cleanup(closeTestDataStore)
}
