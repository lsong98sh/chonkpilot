// L2 崩溃恢复（MOD-1 M8-C3；mq 驱动 + data 面断言）。
//
// 覆盖两条恢复事实（21-llm-server §12）：
//   - ask：重启扫描口径 = `data-ask-pending{pending_only}` 只命中带 ask_id 的非终态行；
//     启动收敛（`CleanupStaleTurns`）把非终态 tool_pair 置 interrupted；失效应答转新 turn；
//   - inbox：`data-inbox-commit` 同事务消费，重试幂等（消息只落一条 + inbox consumed）。
//
// 生产逻辑零改动（本文件只新增断言）。
package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lsong98sh/chonkpilot-chat"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// TestAskRecoveryAfterRestart：模拟进程重启后的 ask 收敛与失效应答。
func TestAskRecoveryAfterRestart(t *testing.T) {
	s := newTestServer(t, httptest.NewServer(http.NotFoundHandler()))
	registerTestInstance(t, s)

	st := newSessionStore(s.bus, "ins-test")
	if err := st.EnsureSession("s-recover"); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureTurn("t-recover", "s-recover"); err != nil {
		t.Fatal(err)
	}
	// 崩溃前已落库的输入（受理即落库，A2）。
	if _, err := st.AppendMsgMap("t-recover", map[string]any{
		"role": "user", "content": "开始吧",
	}, ""); err != nil {
		t.Fatal(err)
	}
	// 未答 ask：发起时的 role=tool running 行，ask_id 随行落库（不建 ask 表）。
	content := `{"call":{"tool_call_id":"c-recover","name":"ask_user","arguments":{"question":"继续吗？"}}}`
	if _, err := st.AppendMsgMap("t-recover", map[string]any{
		"role": "tool", "content": content, "tool_call_id": "c-recover",
		"tool_call_status": "running", "ask_id": "ask-recover",
	}, ""); err != nil {
		t.Fatal(err)
	}

	// 重启扫描口径：只留痕、不改状态；`pending_only` 命中 1 条未答 ask。
	s.scanPendingAsks("ins-test")
	items, err := s.dataAskPending("ins-test", "", true)
	if err != nil {
		t.Fatalf("data-ask-pending: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("重启扫描应命中 1 条未答 ask：%+v", items)
	}
	it := items[0]
	if it.AskID != "ask-recover" || it.SessionID != "s-recover" || it.TurnID != "t-recover" ||
		it.Status != "running" || it.Question != "继续吗？" {
		t.Fatalf("未答 ask 归属 / 状态 / 提问文本错：%+v", it)
	}

	// 启动收敛：遗留 running turn → interrupted；非终态 tool_pair → interrupted。
	if err := st.CleanupStaleTurns(); err != nil {
		t.Fatalf("CleanupStaleTurns: %v", err)
	}
	if items, err = s.dataAskPending("ins-test", "", true); err != nil || len(items) != 0 {
		t.Fatalf("收敛后不应再命中未答 ask：%+v %v", items, err)
	}
	// 精确回查（含终态）证明该 tool 行已置 interrupted（data 面可观察口径）。
	items, err = s.dataAskPending("ins-test", "ask-recover", false)
	if err != nil || len(items) != 1 || items[0].Status != "interrupted" {
		t.Fatalf("收敛后 ask 行应 interrupted：%+v %v", items, err)
	}

	// 失效应答：非终态（interrupted）→ 转普通 user 消息开新 turn，不报错。
	if err := s.replyExpiredAsk("ins-test", "ask-recover", "答案"); err != nil {
		t.Fatalf("失效 ask 应答应转新轮：%v", err)
	}
	if latest := st.LatestTurnID("s-recover"); latest == "" || latest == "t-recover" {
		t.Fatalf("应已为该会话开新轮：latest=%q", latest)
	}

	// 收尾：等新轮退出（否则 TempDir 清理会撞上仍在用的 DB 句柄）。
	for i := 0; i < 150; i++ {
		s.mu.Lock()
		tc := s.turnBySessionLocked("ins-test", "s-recover")
		if tc != nil {
			tc.Close()
		}
		s.mu.Unlock()
		if tc == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestInboxCommitCrashRetryOnce：`data-inbox-commit` 重试幂等（宿主适配器口径）。
//
// 说明：「消息已落、inbox 未置 consumed」的半完成态在 data 层已收进同一 bbolt 写事务
// （`InboxCommit` 内 `prj.Update`），测试侧无法构造；故以原子实现为准，直接验证
// 「崩溃后重放同一次提交」不重复落库、inbox 为 consumed。
func TestInboxCommitCrashRetryOnce(t *testing.T) {
	s := newTestServer(t, httptest.NewServer(http.NotFoundHandler()))
	registerTestInstance(t, s)

	res, err := dataRequest(s.bus, msgkeys.TopicDataInboxAppend, map[string]any{
		"instance_id": "ins-test",
		"data":        map[string]any{"session": "s-inbox-crash", "type": "insert", "content": "hi"},
	})
	if err != nil {
		t.Fatalf("inbox append: %v", err)
	}
	inboxID := str(res["id"])
	if inboxID == "" {
		t.Fatalf("inbox append 未返回 id：%+v", res)
	}

	st := newSessionStore(s.bus, "ins-test")
	if err := st.EnsureSession("s-inbox-crash"); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureTurn("t-inbox-crash", "s-inbox-crash"); err != nil {
		t.Fatal(err)
	}
	store := newChatTurnStore(s, "ins-test", "s-inbox-crash", "t-inbox-crash")

	msg := chat.Message{Role: "user", Content: "hi"}
	if err := store.CommitInbox(context.Background(), inboxID, msg); err != nil {
		t.Fatalf("CommitInbox: %v", err)
	}
	if msgs := st.LoadMessages("t-inbox-crash"); len(msgs) != 1 || msgs[0].Content != "hi" {
		t.Fatalf("首次提交落库错：%+v", msgs)
	}

	// 重试（模拟重启后重放同一次提交）：不报错、不重复落库。
	if err := store.CommitInbox(context.Background(), inboxID, msg); err != nil {
		t.Fatalf("CommitInbox 重试应幂等：%v", err)
	}
	if msgs := st.LoadMessages("t-inbox-crash"); len(msgs) != 1 {
		t.Fatalf("重试后消息数应仍为 1：%d", len(msgs))
	}

	// inbox 已 consumed（唯一一条，且就是本次提交的 inbox 项）。
	res, err = dataRequest(s.bus, msgkeys.TopicDataInboxList, map[string]any{
		"instance_id": "ins-test",
		"data":        map[string]any{"session": "s-inbox-crash", "status": "consumed"},
	})
	if err != nil {
		t.Fatalf("inbox list: %v", err)
	}
	items, _ := res["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("consumed inbox 应唯一：%+v", items)
	}
	it, _ := items[0].(map[string]any)
	if str(it["id"]) != inboxID || str(it["status"]) != "consumed" {
		t.Fatalf("inbox 状态错：%+v", it)
	}
}
