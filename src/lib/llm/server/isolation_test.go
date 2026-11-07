// 实例隔离白盒测试（2026-09-19 第一批：缺口 3 / 缺口 5）：
// 同进程多 instance 的任务池与轮次运行态**互不可见、互不取消**；instance 为空时保持旧语义
// （全桶回退）；单 instance 行为与此前逐字节等价（既有测试全绿）。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/persist"
)

// TestTaskPoolIsolationAcrossInstances：两个 instance 使用**同名** turn / tool_call_id / gw_task_id
// → 归属过滤的查询与级联取消均只命中本 instance（缺口 3）。
func TestTaskPoolIsolationAcrossInstances(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	mk := func(inst string) *TaskNode {
		t.Helper()
		n, err := s.tasks.start(&TaskNode{
			InstanceID: inst, TopSession: "s-shared", SessionID: "s-shared", TurnID: "t-shared",
			ToolCallID: "tc-shared", Tool: "core_file_read", Name: "隔离用例",
		})
		if err != nil {
			t.Fatalf("start(%s): %v", inst, err)
		}
		return n
	}
	a, b := mk("ins-a"), mk("ins-b")
	if a.TaskID == b.TaskID {
		t.Fatal("两个 instance 的任务应各自建节点")
	}

	// ① 互不可见：按 tool_call_id / gw_task_id 反查只命中本 instance；instance 空 → 全桶（旧语义）
	if n := s.tasks.findByToolCall("ins-a", "tc-shared"); n == nil || n.TaskID != a.TaskID {
		t.Fatalf("ins-a 反查应命中 A：%+v", n)
	}
	if n := s.tasks.findByToolCall("ins-b", "tc-shared"); n == nil || n.TaskID != b.TaskID {
		t.Fatalf("ins-b 反查应命中 B：%+v", n)
	}
	if n := s.tasks.findByToolCall("", "tc-shared"); n == nil {
		t.Fatal("instance 为空应回退全桶（旧语义）")
	}
	if n := s.tasks.nodeIn("ins-a", b.TaskID); n != nil {
		t.Fatalf("ins-a 不应看到 ins-b 的节点：%+v", n)
	}
	s.tasks.setGwTask(a.TaskID, "gw-shared")
	s.tasks.setGwTask(b.TaskID, "gw-shared")
	if n := s.tasks.findByGwTask("ins-a", "gw-shared"); n == nil || n.TaskID != a.TaskID {
		t.Fatalf("ins-a gw 反查应命中 A：%+v", n)
	}
	if n := s.tasks.findByGwTask("ins-b", "gw-shared"); n == nil || n.TaskID != b.TaskID {
		t.Fatalf("ins-b gw 反查应命中 B：%+v", n)
	}

	// ② 互不取消：取消 ins-a 的 t-shared → 只 A 终态，B 仍在运行
	if got := s.tasks.cancelByTurn("ins-a", "t-shared"); got != 1 {
		t.Fatalf("cancelByTurn(ins-a) = %d, want 1", got)
	}
	if n := s.tasks.get(a.TaskID); n == nil || n.State != TaskStateCancelled {
		t.Fatalf("A 应被取消：%+v", n)
	}
	if n := s.tasks.get(b.TaskID); n == nil || n.State != TaskStateRunning {
		t.Fatalf("B 不应被 A 的取消影响：%+v", n)
	}

	// ③ 同 session 的 llm 子节点按 instance 定位（不跨 instance 挂错父节点）
	llmNode, err := s.tasks.start(&TaskNode{
		Kind: TaskKindLLM, InstanceID: "ins-a", TopSession: "s-shared", SessionID: "sub-1", Name: "llm",
	})
	if err != nil {
		t.Fatalf("start llm 节点: %v", err)
	}
	if n := s.tasks.latestLLMSessionNode("ins-a", "s-shared", "sub-1"); n == nil || n.TaskID != llmNode.TaskID {
		t.Fatalf("ins-a 应命中自己的 llm 节点：%+v", n)
	}
	if n := s.tasks.latestLLMSessionNode("ins-b", "s-shared", "sub-1"); n != nil {
		t.Fatalf("ins-b 不应命中 ins-a 的 llm 节点：%+v", n)
	}

	// ④ instance 空 = 旧语义（全桶）：B 仍非终态 → 仍被取消
	if got := s.tasks.cancelByTurn("", "t-shared"); got != 1 {
		t.Fatalf("cancelByTurn(\"\") 旧语义应命中 B，got %d want 1", got)
	}
	if n := s.tasks.get(b.TaskID); n == nil || n.State != TaskStateCancelled {
		t.Fatalf("B 应被全桶取消取消：%+v", n)
	}
}

// TestTurnRuntimeIsolationAcrossInstances：同 session / turn、不同 instance → 运行态（turns / busy）
// 各一份；取消 A 只停 A，B 不受影响（缺口 5）。
func TestTurnRuntimeIsolationAcrossInstances(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	// 直接建运行态（等价 llm-start 注册后；不喂输入 → 轮次保持运行中，便于断言取消隔离）
	a := s.recoverTurnCtx("ins-a", "s-dup", "t-dup")
	b := s.recoverTurnCtx("ins-b", "s-dup", "t-dup")
	t.Cleanup(func() { a.Close(); b.Close() })

	if s.lookupTurnIn("ins-a", "t-dup") != a || s.lookupTurnIn("ins-b", "t-dup") != b {
		t.Fatal("两 instance 应各自登记运行轮次（互不覆盖）")
	}
	s.mu.Lock()
	_, aBusy := s.busy[instKey("ins-a", "s-dup")]
	_, bBusy := s.busy[instKey("ins-b", "s-dup")]
	s.mu.Unlock()
	if !aBusy || !bBusy {
		t.Fatalf("busy 应按 instance 分桶：a=%v b=%v", aBusy, bBusy)
	}

	// 取消 ins-a（载荷带 instance_id）→ 只 A 终态并移出运行态
	w := s.bus.Emit(context.Background(), "session-cancel", jb(map[string]any{
		"instance_id": "ins-a", "session": "s-dup", "turn": "t-dup",
	}))
	res, _ := w.Wait().Result.(map[string]any)
	if ok, _ := res["cancelled"].(bool); !ok {
		t.Fatalf("ins-a 的轮次应被取消：%+v", res)
	}
	// 运行态由 loop 收尾异步清出 → 轮询等待（只等 A）
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && s.lookupTurnIn("ins-a", "t-dup") != nil {
		time.Sleep(10 * time.Millisecond)
	}
	if s.lookupTurnIn("ins-a", "t-dup") != nil {
		t.Fatal("ins-a 的轮次应已移出运行态")
	}
	if s.lookupTurnIn("ins-b", "t-dup") != b {
		t.Fatal("ins-b 的轮次不应被 ins-a 的取消影响（缺口 5）")
	}

	// 再取消 ins-b → 同样成功（互不干扰）
	w2 := s.bus.Emit(context.Background(), "session-cancel", jb(map[string]any{
		"instance_id": "ins-b", "session": "s-dup", "turn": "t-dup",
	}))
	res2, _ := w2.Wait().Result.(map[string]any)
	if ok, _ := res2["cancelled"].(bool); !ok {
		t.Fatalf("ins-b 的轮次应被取消：%+v", res2)
	}
}

// ── ask 等待登记的清理与惰性过期回收（E2-1）──

// TestAskCleanupHelpers：ask 等待登记的惰性过期回收 + 按 turn / instance 清理（含隔离边界）。
func TestAskCleanupHelpers(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	hour := time.Now().Add(time.Hour)
	s.mu.Lock()
	s.asks[instKey("ins-a", "ask-1")] = &askWaiter{askID: "ask-1", instanceID: "ins-a", turnID: "t-1", expiresAt: hour}
	s.asks[instKey("ins-b", "ask-3")] = &askWaiter{askID: "ask-3", instanceID: "ins-b", turnID: "t-1", expiresAt: hour}
	// 显式超时由 timer/expireAsk 主动结算（无惰性回收）；这里只验 turn/instance 清理边界。
	// 轮次收尾：只清本实例本 turn（不误伤他实例同名 turn）
	s.clearAsksByTurnLocked("ins-a", "t-1")
	if _, ok := s.asks[instKey("ins-a", "ask-1")]; ok {
		t.Fatal("t-1 的 ask 应被清")
	}
	if _, ok := s.asks[instKey("ins-b", "ask-3")]; !ok {
		t.Fatal("他实例同名 turn 的 ask 不应被清")
	}
	// 实例退出：清该实例剩余
	s.clearAsksByInstanceLocked("ins-b")
	s.mu.Unlock()

	if len(s.asks) != 0 {
		t.Fatalf("等待登记应全部清空：%v", s.asks)
	}
}

// stubConfigAPI 只覆写 ConfigKVGet（其余门面调用透传）：用于构造「全局配置不可解析 / 读取失败」
// 的边界——真实 data 层会拒绝非法 ask_timeout_sec 写入（validate：非负整数），无法经 save 路径制造。
type stubConfigAPI struct {
	facade.API
	values map[string]string
	err    error
}

func (c stubConfigAPI) ConfigKVGet(facade.ConfigKVGetRequest) (facade.ConfigKVGetResponse, error) {
	if c.err != nil {
		return facade.ConfigKVGetResponse{}, c.err
	}
	return facade.ConfigKVGetResponse{Values: c.values}, nil
}

// TestAskTimeoutSecondsProduction 直调生产 `s.askTimeoutSeconds`（不再用测试私有复制逻辑），
// 断言规格（AskUser §4.3 / 61 §4.2）的四条折算与全局 `ask_timeout_sec` 的真实配置读取：
//   - 未给 timeout / timeout=0 → 全局；
//   - timeout>0 → 覆盖全局；
//   - timeout<0 → 明确不超时（覆盖全局）。
func TestAskTimeoutSecondsProduction(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	// 全局未配置（缺省 0）：所有「用全局」的入参都折算为 0（一直等）。
	if got := s.askTimeoutSeconds("ins-test", map[string]any{}); got != 0 {
		t.Fatalf("全局未配置 + 未给 timeout → 应 0（一直等），got=%d", got)
	}
	if got := s.askTimeoutSeconds("ins-test", map[string]any{"timeout": 0}); got != 0 {
		t.Fatalf("全局未配置 + timeout=0 → 应取全局 0，got=%d", got)
	}
	if got := s.askTimeoutSeconds("ins-test", map[string]any{"timeout": -1}); got != 0 {
		t.Fatalf("timeout=-1 → 应明确不超时 0，got=%d", got)
	}

	// 项目级全局 = 30（真实 data 配置写路径）。
	saveTestPrjConfig(t, s, askTimeoutKey, "30")
	if got := s.askTimeoutSeconds("ins-test", map[string]any{}); got != 30 {
		t.Fatalf("未给 timeout → 应取全局 30，got=%d", got)
	}
	if got := s.askTimeoutSeconds("ins-test", map[string]any{"timeout": 0}); got != 30 {
		t.Fatalf("timeout=0 → 应取全局 30，got=%d", got)
	}
	if got := s.askTimeoutSeconds("ins-test", map[string]any{"timeout": 5}); got != 5 {
		t.Fatalf("timeout=5 → 应覆盖全局，got=%d", got)
	}
	// 负例：显式 -1 优先于全局（规格：timeout<0 = 明确不超时）。
	if got := s.askTimeoutSeconds("ins-test", map[string]any{"timeout": -1}); got != 0 {
		t.Fatalf("timeout=-1 应覆盖全局 30 → 0，got=%d", got)
	}
}

// TestAskTimeoutSecondsGlobalParseFallback 全局配置不可解析 / 读取失败 → 按「未配置」处 0
// （不注入负值 / 不崩溃）。
func TestAskTimeoutSecondsGlobalParseFallback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values map[string]string
		err    error
	}{
		{"非数字", map[string]string{askTimeoutKey: "soon"}, nil},
		{"负数", map[string]string{askTimeoutKey: "-30"}, nil},
		{"读取失败", nil, errors.New("boom")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{cfg: stubConfigAPI{values: tc.values, err: tc.err}}
			if got := s.askTimeoutSeconds("", map[string]any{}); got != 0 {
				t.Fatalf("全局值不可用时应回落 0（一直等），got=%d", got)
			}
		})
	}
}

// TestAskTimeoutNoExpiresNoTimer 无超时（timeout=-1 / 0，全局 0）时：
// 不下发 `expires_at`、不建 timer（AskUser §4.3 / §6.1「一直等待」）。
func TestAskTimeoutNoExpiresNoTimer(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	tc := s.recoverTurnCtx("ins-test", "s-ask-notimeout", "t-ask-notimeout")
	t.Cleanup(tc.Close)

	for _, tc := range []struct {
		toolID string
		args   map[string]any
	}{
		{"tc-no-a", map[string]any{"question": "Q-未设置"}},
		{"tc-no-b", map[string]any{"question": "Q-zero", "timeout": 0}},
		{"tc-no-c", map[string]any{"question": "Q-minus", "timeout": -1}},
	} {
		askID := s.ask("t-ask-notimeout", tc.toolID, tc.args, "")
		if askID == "" {
			t.Fatalf("ask 应返回 askID: %v", tc.args)
		}
		s.mu.Lock()
		w := s.asks[instKey("ins-test", askID)]
		s.mu.Unlock()
		if w == nil {
			t.Fatalf("ask 等待登记缺失: %s", askID)
		}
		if !w.expiresAt.IsZero() {
			t.Fatalf("%v：无超时不应有 expiresAt，got=%v", tc.args, w.expiresAt)
		}
		if w.timer != nil {
			t.Fatalf("%v：无超时不应建 timer", tc.args)
		}
		var payload map[string]any
		if err := json.Unmarshal(w.payload, &payload); err != nil {
			t.Fatalf("payload 解析失败: %v", err)
		}
		// 规格（61 §4.2）：无超时不下发 expires_at（可选字段不得出现）。
		if _, has := payload["expires_at"]; has {
			t.Fatalf("%v：session-ask 不应下发 expires_at: %v", tc.args, payload)
		}
	}
}

// TestAskSurvivesTurnClose：AskUser S4 —— 轮次**正常结束不摘除** ask 登记
// （非阻塞 ask 允许本轮先收尾；用户稍后作答仍可投递）。取消 / 实例退出才清理。
func TestAskSurvivesTurnClose(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	tc := s.recoverTurnCtx("ins-a", "s-1", "t-1")
	askID := s.ask("t-1", "tc-1", map[string]any{"question": "q"}, "")
	if askID == "" {
		t.Fatal("ask 应返回 askID")
	}
	s.mu.Lock()
	_, ok := s.asks[instKey("ins-a", askID)]
	s.mu.Unlock()
	if !ok {
		t.Fatal("ask 等待登记应已登记")
	}

	tc.Close() // 关闭轮次（正常收尾）→ 提问登记继续存活
	time.Sleep(100 * time.Millisecond)
	s.mu.Lock()
	_, ok = s.asks[instKey("ins-a", askID)]
	batches := len(s.askBatches)
	s.mu.Unlock()
	if !ok {
		t.Fatal("轮次正常结束不应摘除 ask 等待登记")
	}
	if batches == 0 {
		t.Fatal("轮次正常结束不应删除会话批次")
	}

	// 显式清理（取消 / 实例退出路径）→ 登记与批次一并摘除。
	s.mu.Lock()
	removed := s.clearAsksByTurnLocked("ins-a", "t-1")
	nBatches := len(s.askBatches)
	s.mu.Unlock()
	if len(removed) != 1 {
		t.Fatalf("显式清理应摘除 1 条 ask，实际 %d", len(removed))
	}
	if nBatches != 0 {
		t.Fatalf("显式清理后批次应清空：%d", nBatches)
	}
}

// TestAskClearedOnInstanceExit：实例退出摘除该实例的**全部** ask 等待登记。
func TestAskClearedOnInstanceExit(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	tc := s.recoverTurnCtx("ins-x", "s-x", "t-x")
	t.Cleanup(tc.Close)
	if askID := s.ask("t-x", "tc-x", map[string]any{"question": "q"}, ""); askID == "" {
		t.Fatal("ask 应返回 askID")
	}

	s.exitInstance("ins-x")

	s.mu.Lock()
	n := len(s.asks)
	s.mu.Unlock()
	if n != 0 {
		t.Fatalf("实例退出应清空该实例的 ask：%d", n)
	}
}

// TestLLMCancelClearsAsksAndBatches 走**真实方法面** `session-cancel`（onLLMCancel，而非直调
// clearAsksByTurnLocked）：挂起一轮 + 同会话两条未答 ask + 其中一条已答（批次只齐一半）→
//   - asks / askBatches 全部为空（批次内已收答复一并丢弃）；
//   - 不向 LLM 投递后续用户消息（不新起轮）；
//   - ask-center-remove 不重复：已答那条在作答时摘除（answered），另一条在取消时摘除（turn_closed），
//     每个 ask_id 恰好一条（AskUser §6.1「轮次被取消」/ §4.8 S4-3）。
func TestLLMCancelClearsAsksAndBatches(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	const session, turn = "s-cancel-ask", "t-cancel-ask"
	startTurn(t, s, session, turn)

	// Ask 中心订阅（未登记实例 → user 空，与 ins-test 的空 user 同作用域；workdir 通配）。
	rec := watchAskCenter(t, s)
	if res := subscribeAskCenter(t, s, map[string]any{"instance_id": "askc-cancel"}); res["ok"] != true {
		t.Fatalf("ask-center-subscribe 失败: %v", res)
	}

	a1 := s.ask(turn, "tc-cancel-1", map[string]any{"question": "Q1"}, "")
	a2 := s.ask(turn, "tc-cancel-2", map[string]any{"question": "Q2"}, "")
	if a1 == "" || a2 == "" {
		t.Fatal("ask 应返回 askID")
	}

	// 部分批次答复：Q1 答复先暂存（同批仍有未答 Q2 → 不投递）。
	if v := s.bus.Emit(context.Background(), "session-ask-reply", jb(map[string]any{
		"instance_id": "ins-test", "ask_id": a1, "answer": "A1",
	})).Wait(); v.Err() != nil {
		t.Fatalf("ask-user-reply 失败: %v", v.Err())
	}
	s.mu.Lock()
	nAsks, nBatches, open, answered := len(s.asks), len(s.askBatches), 0, 0
	for _, b := range s.askBatches {
		open += len(b.open)
		answered += len(b.answered)
	}
	s.mu.Unlock()
	if nAsks != 1 || nBatches != 1 || open != 1 || answered != 1 {
		t.Fatalf("部分答复后状态不符：asks=%d batches=%d open=%d answered=%d",
			nAsks, nBatches, open, answered)
	}
	if n := rec.countRemovesFor(a1); n != 1 {
		t.Fatalf("Q1 作答应恰好摘除一次（answered），got=%d", n)
	}
	if n := rec.countRemovesFor(a2); n != 0 {
		t.Fatalf("Q2 未作答不应有 remove，got=%d", n)
	}

	// 真实入口：llm-cancel（session-cancel）。
	v := s.bus.Emit(context.Background(), "session-cancel", jb(map[string]any{
		"instance_id": "ins-test", "session": session, "turn": turn,
	})).Wait()
	res, _ := v.Result.(map[string]any)
	if ok, _ := res["cancelled"].(bool); !ok {
		t.Fatalf("llm-cancel 应受理: %v（err=%v）", res, v.Err())
	}

	s.mu.Lock()
	nAsks, nBatches = len(s.asks), len(s.askBatches)
	s.mu.Unlock()
	if nAsks != 0 || nBatches != 0 {
		t.Fatalf("取消后应清空 ask 与批次：asks=%d batches=%d", nAsks, nBatches)
	}

	// remove 不重复：总计 2 条（Q1 answered + Q2 turn_closed），每个 ask_id 各 1 条。
	time.Sleep(200 * time.Millisecond) // 给任何「多余通知」留出可观测窗口
	if got := rec.countRemovesTotal(); got != 2 {
		t.Fatalf("ask-center-remove 应为 2 条（不重复），got=%d: %+v", got, rec.snapshotRemoves())
	}
	if n := rec.countRemovesFor(a1); n != 1 {
		t.Fatalf("Q1 remove 应恰好 1 条，got=%d", n)
	}
	if n := rec.countRemovesFor(a2); n != 1 {
		t.Fatalf("Q2 remove 应恰好 1 条，got=%d", n)
	}
	if reasons := rec.reasonsFor(a2); len(reasons) != 1 || reasons[0] != "turn_closed" {
		t.Fatalf("Q2 remove.reason 应为 turn_closed，got=%v", reasons)
	}

	// 无后续用户消息投递：不新起轮，且本轮没有任何 user 行携带暂存答复 A1。
	if got := newSessionStore(s.bus, "ins-test").LatestTurnID(session); got != turn {
		t.Fatalf("取消后不应起新轮：latest=%s want=%s", got, turn)
	}
	if recs := sessionTurnRecords(t, s, session); len(recs) != 1 {
		t.Fatalf("取消后该会话应仍只有 1 轮：%+v", recs)
	}
	for _, m := range msgsOfTurn(t, turn) {
		if persist.Sval(m["role"]) == "user" && strings.Contains(persist.Sval(m["content"]), "A1") {
			t.Fatalf("批次内已收答复应随取消丢弃（不得作为用户消息投递）：%+v", m)
		}
	}
}
