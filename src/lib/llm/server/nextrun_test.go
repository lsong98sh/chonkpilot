// NEXTRUN-1 `next_run` 自动续跑（L1，mq 驱动 + mq / 数据面断言）。
//
// 覆盖：正常登记 → 本轮结束后自动起新轮（消息 kind=next_run）· 上限文案 · 用户发送重置 ·
// last-wins（同轮多次只留最后一条）· 非主会话拒绝 · 关闭（nextRunMax=0）· 终态丢弃。
package server

import (
	"strconv"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

// nextRunItems 读会话 inbox 项（status="all"）。
func nextRunItems(t *testing.T, s *Server, session string) []map[string]any {
	t.Helper()
	res, err := dataRequest(s.bus, "data-inbox-list", map[string]any{
		"instance_id": "ins-test",
		"data":        map[string]any{"session": session, "status": "all"},
	})
	if err != nil {
		t.Fatalf("data-inbox-list: %v", err)
	}
	raw, _ := res["items"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, v := range raw {
		if m, ok := v.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func nextRunCount(t *testing.T, s *Server, session string) int {
	t.Helper()
	res, err := s.cfg.SessionNextRunGet(facade.SessionNextRunGetRequest{
		InstanceID: "ins-test", SessionID: session,
	})
	if err != nil {
		t.Fatalf("SessionNextRunGet: %v", err)
	}
	return res.Count
}

// TestNextRunRegistersAndStartsNewTurn：登记 → 本轮 complete → 自动新轮（kind=next_run）。
func TestNextRunRegistersAndStartsNewTurn(t *testing.T) {
	llm, _ := newInboxLLM(t)
	s := newTestServer(t, llm)
	startTurn(t, s, "s-nr1", "t-nr1")
	rec := newInboxRec(t, s.bus)
	tc := s.lookupTurnIn("ins-test", "t-nr1")
	if tc == nil {
		t.Fatalf("turn 未登记")
	}
	if tc.nextRunMax != nextRunDefaultMax {
		t.Fatalf("nextRunMax 默认应为 %d：got=%d", nextRunDefaultMax, tc.nextRunMax)
	}
	if text, isErr := s.runNextRun(tc, map[string]any{"prompt": "第二阶段：汇总结果"}); isErr || text != "OK" {
		t.Fatalf("next_run 应受理：text=%q isErr=%v", text, isErr)
	}
	items := nextRunItems(t, s, "s-nr1")
	if len(items) != 1 || str(items[0]["source"]) != "llm" || str(items[0]["turn_id"]) != "t-nr1" {
		t.Fatalf("inbox 应恰有 1 条 source=llm 的本轮项：%+v", items)
	}
	if got := nextRunCount(t, s, "s-nr1"); got != 1 {
		t.Fatalf("计数应为 1：got=%d", got)
	}

	// 本轮正常结束 → 既有 consumeNextTurn 起新 turn。
	s.complete(tc, "complete", "stop", "本轮完成")
	tc.Close()
	rec.wait(t, "session-turn-start", func(m map[string]any) bool {
		return str(m["session"]) == "s-nr1" && str(m["turn"]) != "t-nr1"
	}, 10*time.Second)
	var newTurn string
	for _, m := range rec.matches("session-turn-start", func(m map[string]any) bool {
		return str(m["session"]) == "s-nr1" && str(m["turn"]) != "t-nr1"
	}) {
		newTurn = str(m["turn"])
	}
	if newTurn == "" {
		t.Fatalf("未捕获新轮 turn id")
	}
	rec.wait(t, "session-complete", byTurn(newTurn), 10*time.Second)

	msgs := newSessionStore(s.bus, "ins-test").LoadMessages(newTurn)
	found := false
	for _, m := range msgs {
		if m.Role == "user" && m.Kind == nextRunKind && m.Content == "第二阶段：汇总结果" {
			found = true
		}
	}
	if !found {
		t.Fatalf("新轮应含 kind=next_run 的 user 消息：%+v", msgs)
	}
}

// TestNextRunLimitAndReset：上限文案 + 用户发送重置 → 额度恢复。
func TestNextRunLimitAndReset(t *testing.T) {
	llm, _ := newInboxLLM(t)
	s := newTestServer(t, llm)
	startTurn(t, s, "s-nr2", "t-nr2")
	tc := s.lookupTurnIn("ins-test", "t-nr2")
	tc.nextRunMax = 2

	for i, want := range []string{"OK", "OK"} {
		if text, isErr := s.runNextRun(tc, map[string]any{"prompt": "第" + strconv.Itoa(i+1) + "段"}); isErr || text != want {
			t.Fatalf("第 %d 次应受理：text=%q", i+1, text)
		}
	}
	// last-wins：仍只有 1 条 inbox 项，内容 = 最后一次。
	items := nextRunItems(t, s, "s-nr2")
	if len(items) != 1 || str(items[0]["content"]) != "第2段" {
		t.Fatalf("同轮多次应只保留最后一条：%+v", items)
	}
	if text, isErr := s.runNextRun(tc, map[string]any{"prompt": "第3段"}); isErr || text != "已自动接续 2 次，需用户判断是否继续" {
		t.Fatalf("到限文案错：text=%q isErr=%v", text, isErr)
	}
	if got := nextRunCount(t, s, "s-nr2"); got != 2 {
		t.Fatalf("到限不应 +1：got=%d", got)
	}
	// 已登记项不被到限调用破坏。
	if items := nextRunItems(t, s, "s-nr2"); len(items) != 1 || str(items[0]["content"]) != "第2段" {
		t.Fatalf("到限调用不得破坏已登记项：%+v", items)
	}

	// 用户发送 → 重置；之后可再受理。
	s.resetNextRunCount("ins-test", "s-nr2")
	if got := nextRunCount(t, s, "s-nr2"); got != 0 {
		t.Fatalf("用户发送后计数应归零：got=%d", got)
	}
	// 新的一轮（重置后 inbox 已登记项仍是上轮 turn_id → 用新 tc 模拟新一轮的同轮登记）
	tc2 := &turnCtx{server: s, req: StartReq{InstanceID: "ins-test", Session: "s-nr2", Turn: "t-nr2b"}, nextRunMax: 2}
	if text, isErr := s.runNextRun(tc2, map[string]any{"prompt": "重来"}); isErr || text != "OK" {
		t.Fatalf("重置后应可再受理：text=%q isErr=%v", text, isErr)
	}
}

// TestNextRunRejectsNonMainAndDisabled：非主会话 / 关闭 / 空 prompt。
func TestNextRunRejectsNonMainAndDisabled(t *testing.T) {
	llm, _ := newInboxLLM(t)
	s := newTestServer(t, llm)
	registerTestInstance(t, s) // data-inbox-list 需实例已登记
	base := StartReq{InstanceID: "ins-test", Session: "s-nr3", Turn: "t-nr3"}

	nonMain := &turnCtx{server: s, req: base, nextRunMax: 30}
	nonMain.req.Parents = []string{"parent"}
	if text, isErr := s.runNextRun(nonMain, map[string]any{"prompt": "x"}); !isErr ||
		text != "next_run 仅支持主会话（子聊天 / 群聊 / 子会话由各自调度器驱动）" {
		t.Fatalf("非主会话应拒绝：text=%q isErr=%v", text, isErr)
	}

	off := &turnCtx{server: s, req: base, nextRunMax: 0}
	if text, isErr := s.runNextRun(off, map[string]any{"prompt": "x"}); isErr ||
		text != "next_run 未启用（自动接续上限为 0）" {
		t.Fatalf("关闭态文案错：text=%q isErr=%v", text, isErr)
	}

	empty := &turnCtx{server: s, req: base, nextRunMax: 30}
	if text, isErr := s.runNextRun(empty, map[string]any{"prompt": "   "}); !isErr || text != "错误: prompt 必填" {
		t.Fatalf("空 prompt 应拒绝：text=%q isErr=%v", text, isErr)
	}
	if items := nextRunItems(t, s, "s-nr3"); len(items) != 0 {
		t.Fatalf("拒绝路径不得落 inbox：%+v", items)
	}
}

// TestNextRunDiscardedOnError：本轮 error → 本 turn 的 LLM 排队项 discarded。
func TestNextRunDiscardedOnError(t *testing.T) {
	llm, _ := newInboxLLM(t)
	s := newTestServer(t, llm)
	startTurn(t, s, "s-nr4", "t-nr4")
	tc := s.lookupTurnIn("ins-test", "t-nr4")
	if text, isErr := s.runNextRun(tc, map[string]any{"prompt": "下一轮"}); isErr || text != "OK" {
		t.Fatalf("next_run 应受理：text=%q", text)
	}
	s.llmError(tc, "LLM_REQUEST_FAILED", "boom")
	tc.Close()

	deadline := time.Now().Add(5 * time.Second)
	for {
		items := nextRunItems(t, s, "s-nr4")
		if len(items) == 1 && str(items[0]["status"]) == "discarded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("error 终态应丢弃本轮 LLM 排队项：%+v", items)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// 计数保留（等用户下次发送归零）。
	if got := nextRunCount(t, s, "s-nr4"); got != 1 {
		t.Fatalf("终态丢弃不应回滚计数：got=%d", got)
	}
}
