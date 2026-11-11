// askcenter_test.go — Ask 中心三个 server 方法面的**真正驱动**（2026-11-07）。
//
// 覆盖：`ask-center-subscribe` / `ask-center-unsubscribe` / `ask-resolve`（61-messages.schema.json
// face=server 的三个主题）。此前的 TestServerNotDrivenRegistry 把三者列为「未登记也未驱动」（红），
// 但三者均由 llm server 真实实现（handler_ask.go onAskCenterSubscribe/onAskCenterUnsubscribe/
// onAskResolve，subscribe.go 注册），且在「内存总线 + mock LLM」下可完整驱动 → 本文件补驱动，
// 不登记白名单。
//
// 期望值来源（**不从实现反推**）：
//   - docs/spec/60-reference/61-消息一览.md §4.2（第 164/165 行）：subscribe 回
//     `{ok, asks:[…]}`、scope 缺省 user、快照按 `workdir + seq` 升序；unsubscribe `{ok}` 幂等。
//   - 同文档第 163/168 行：`asks` 元素 = `session-ask` 字段 + `work_dir` + `received_at`（+ `seq`）。
//   - docs/spec/20-modules/21-llm-server.md §ask_user（S4/S5）：`ask-resolve` 只更新 tool-result
//   - 任务 done + 摘除待办，**不向 LLM 投递任何消息**。
//   - docs/spec/60-reference/61-messages.schema.json（topic 条目）：result 键集合与 required。
package llmtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
)

// askCenterQuestions 是本用例 mock LLM 写死的两次提问（期望值直接引用，保证可读）。
var askCenterQuestions = []struct {
	question    string
	options     []any
	recommended any
	multi       bool
	custom      bool
	context     string
}{
	{
		question: "第 1 问：部署环境？",
		options: []any{
			map[string]any{"value": "dev", "label": "开发"},
			map[string]any{"value": "prod", "label": "生产"},
		},
		recommended: "dev",
		custom:      true,
		context:     "Ask 中心分发验证",
	},
	{
		question: "第 2 问：是否启用灰度？",
		options: []any{
			map[string]any{"value": "yes", "label": "启用"},
			map[string]any{"value": "no", "label": "不启用"},
		},
		recommended: []any{"no"},
		multi:       true,
	},
}

// askCenterMockLLM 一轮首个请求（末条消息 = user）回**两条** ask_user 工具调用（同一 assistant
// 消息 → 同会话同批次、顺序确定）；收到 tool 结果后（末条 = tool）回文本收尾。
func askCenterMockLLM() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			return
		}
		var req struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		lastRole := ""
		if len(req.Messages) > 0 {
			lastRole = req.Messages[len(req.Messages)-1].Role
		}
		if lastRole == "tool" {
			writeSSE(w, []map[string]any{{"content": "等待用户回复"}}, "stop")
			return
		}
		del := map[string]any{"content": ""}
		calls := make([]any, 0, len(askCenterQuestions))
		for i, q := range askCenterQuestions {
			args := map[string]any{"question": q.question, "options": q.options}
			if q.recommended != nil {
				args["recommended"] = q.recommended
			}
			if q.multi {
				args["multi"] = true
			}
			if q.custom {
				args["custom"] = true
			}
			if q.context != "" {
				args["context"] = q.context
			}
			calls = append(calls, map[string]any{
				"index": i, "id": fmt.Sprintf("tc-ask-%d", i+1), "type": "function",
				"function": map[string]any{"name": "ask_user", "arguments": string(jb(args))},
			})
		}
		del["tool_calls"] = calls
		writeSSE(w, []map[string]any{del}, "tool_calls")
	}))
}

// registerInstanceAt 以**显式 work_dir** 注册实例（快照的 work_dir 断言需要确定性值；
// Emit().Wait 保证 instance-register 的全部订阅者（persist/实例视图）已处理完）。
func registerInstanceAt(t *testing.T, bus mq.Bus, instanceID, workDir string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if v := bus.Emit(ctx, "instance-register", jb(map[string]any{
		"instance_id": instanceID, "client_type": "unittest", "work_dir": workDir,
	})).Wait(); v.Err() != nil {
		t.Fatalf("instance-register: %v", v.Err())
	}
}

// driveAskTurn 起一轮并发送文本，等本轮 session-ask（返回第一条）与 session-complete。
func driveAskTurn(t *testing.T, bus mq.Bus, rec *evtRec, instanceID, session, turn string) []map[string]any {
	t.Helper()
	startTurn(t, bus, session, turn, instanceID)
	w := watch(t, bus, turn)
	bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": instanceID, "session": session, "turn": turn,
		"type": "text-user", "content": "请提问",
	}))
	// ask_user 非阻塞（S4）：提问立即广播，本轮随后以文本收尾。
	// 两条提问都到达后再等收尾（单次 rec.wait 只保证「至少一条」）。
	deadline := time.Now().Add(10 * time.Second)
	for {
		rec.mu.Lock()
		n := 0
		for _, m := range rec.evs["session-ask"] {
			if str(m, "turn") == turn && str(m, "ask_id") != "" {
				n++
			}
		}
		rec.mu.Unlock()
		if n >= len(askCenterQuestions) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("本轮应产生 %d 条 session-ask，实际 %d", len(askCenterQuestions), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, status := waitComplete(t, w, 10*time.Second); status != "complete" {
		t.Fatalf("turn %s status = %q, want complete（ask_user 非阻塞，不应挂起本轮）", turn, status)
	}
	// 收集本轮全部 session-ask（按到达顺序）。
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var out []map[string]any
	for _, m := range rec.evs["session-ask"] {
		if str(m, "turn") == turn {
			out = append(out, m)
		}
	}
	return out
}

// callTopic 经内存总线驱动一个 server 方法面，返回 result（JSON 归一，避免依赖总线内部对象形态）。
func callTopic(t *testing.T, bus mq.Bus, topic string, payload map[string]any) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v := bus.Emit(ctx, topic, payload).Wait()
	if err := v.Err(); err != nil {
		t.Fatalf("%s: %v", topic, err)
	}
	raw, err := json.Marshal(v.Result)
	if err != nil {
		t.Fatalf("%s result marshal: %v", topic, err)
	}
	var res map[string]any
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("%s result unmarshal: %v（raw=%s）", topic, err, raw)
	}
	return res
}

// asksOf 取 subscribe 结果的 asks（JSON 归一为 []map[string]any）。
func asksOf(t *testing.T, res map[string]any) []map[string]any {
	t.Helper()
	raw, err := json.Marshal(res["asks"])
	if err != nil {
		t.Fatalf("asks marshal: %v", err)
	}
	var asks []map[string]any
	if err := json.Unmarshal(raw, &asks); err != nil {
		t.Fatalf("asks unmarshal: %v（raw=%s）", err, raw)
	}
	return asks
}

// TestAskCenterSubscribeResolveUnsubscribe（61 §4.2 / 21 §ask_user S4-S5）：
//  1. subscribe → {ok, asks} 快照：两条待办、按 workdir+seq 升序、携带 ask 载荷 + work_dir/received_at/seq；
//  2. resolve → {ok} + ask-center-remove{reason:"resolved"} + 快照摘除 + **不向 LLM 投递**；幂等；
//  3. unsubscribe → {ok} 幂等；退订后新提问**不再分发**（ask-center-add 不再到达）。
func TestAskCenterSubscribeResolveUnsubscribe(t *testing.T) {
	c := srvLoad(t)
	byTopic := c.byTopic()

	llm := askCenterMockLLM()
	defer llm.Close()
	bus := newBus(t)
	srv := startLLM(t, bus, llm.URL, false) // 真实内嵌 gateway
	startMovedDomainPlugin(t, bus, srv)     // ask_user 已迁 task 插件：测试适配
	t.Cleanup(data.Reset)            // 晚于 startLLM 的 TempDir 注册 → LIFO 先关库连接再删目录（Windows 句柄）
	const inst = "ins-askc"
	workDir := t.TempDir()
	registerInstanceAt(t, bus, inst, workDir)
	time.Sleep(300 * time.Millisecond) // 域工具/实例视图装配收敛

	rec := newEvtRec(t, bus)
	askEvents := driveAskTurn(t, bus, rec, inst, "s-askc-1", "t-askc-1")
	if len(askEvents) != len(askCenterQuestions) {
		t.Fatalf("本轮应产生 %d 条 session-ask，got %d: %+v", len(askCenterQuestions), len(askEvents), askEvents)
	}
	askIDs := make([]string, 0, len(askEvents))
	for i, ev := range askEvents {
		askIDs = append(askIDs, str(ev, "ask_id"))
		if str(ev, "question") != askCenterQuestions[i].question {
			t.Fatalf("session-ask[%d].question = %q, want %q（工具参数原文）",
				i, str(ev, "question"), askCenterQuestions[i].question)
		}
		if str(ev, "owner_instance") != inst {
			t.Fatalf("session-ask[%d].owner_instance = %q, want %q", i, str(ev, "owner_instance"), inst)
		}
		if str(ev, "session") != "s-askc-1" || str(ev, "turn") != "t-askc-1" {
			t.Fatalf("session-ask[%d] 归属不符: %+v", i, ev)
		}
	}

	// ① 订阅并回快照。契约：result 键 ⊆ 61 schema（ok/asks 均 required）。
	sub := callTopic(t, bus, "ask-center-subscribe", map[string]any{"instance_id": inst})
	ts, ok := byTopic["ask-center-subscribe"]
	if !ok {
		t.Fatal("契约未收录 ask-center-subscribe")
	}
	srvAssertKeys(t, "ask-center-subscribe", "result", ts.Result, sub)
	if sub["ok"] != true {
		t.Fatalf("subscribe ok = %v, want true（61 §4.2：登记订阅并回快照）", sub["ok"])
	}
	asks := asksOf(t, sub)
	if len(asks) != len(askCenterQuestions) {
		t.Fatalf("快照应含全部未办结待办（%d），got %d: %+v", len(askCenterQuestions), len(asks), asks)
	}
	// 快照按 workdir + seq 升序；元素 = session-ask 字段 + work_dir/received_at/seq（61 §4.2/§4.3）。
	for i, a := range asks {
		if a["ask_id"] != askIDs[i] {
			t.Fatalf("asks[%d].ask_id = %v, want %q（按 seq 升序 = 提问先后）", i, a["ask_id"], askIDs[i])
		}
		if a["question"] != askCenterQuestions[i].question {
			t.Fatalf("asks[%d].question = %v, want %q", i, a["question"], askCenterQuestions[i].question)
		}
		if a["work_dir"] != workDir {
			t.Fatalf("asks[%d].work_dir = %v, want %q（实例 work_dir）", i, a["work_dir"], workDir)
		}
		if seq, _ := a["seq"].(float64); seq != float64(i+1) {
			t.Fatalf("asks[%d].seq = %v, want %d（每 workdir 单调序号从 1 起）", i, a["seq"], i+1)
		}
		if received, _ := a["received_at"].(float64); received <= 0 {
			t.Fatalf("asks[%d].received_at = %v, want unix ms > 0", i, a["received_at"])
		}
		if a["owner_instance"] != inst {
			t.Fatalf("asks[%d].owner_instance = %v, want %q（跨窗口作答归属）", i, a["owner_instance"], inst)
		}
	}

	// ② 解析第一条：{ok} + ask-center-remove{ask_id, reason:resolved} + 不向 LLM 投递。
	completesBefore := completesForSession(rec, "s-askc-1")
	resolve := callTopic(t, bus, "ask-resolve", map[string]any{"ask_id": askIDs[0]})
	ts, ok = byTopic["ask-resolve"]
	if !ok {
		t.Fatal("契约未收录 ask-resolve")
	}
	srvAssertKeys(t, "ask-resolve", "result", ts.Result, resolve)
	if resolve["ok"] != true {
		t.Fatalf("ask-resolve ok = %v, want true（幂等且受理）", resolve["ok"])
	}
	remove := rec.wait(t, "ask-center-remove", func(m map[string]any) bool {
		return str(m, "ask_id") == askIDs[0] && str(m, "reason") == "resolved"
	}, 5*time.Second)
	if str(remove, "instance_id") != "" && str(remove, "instance_id") != inst {
		t.Fatalf("ask-center-remove 应定向已订阅实例 %q：%+v", inst, remove)
	}
	// 21 §ask_user S5 / 61 §4.2：「完成」只更新历史，不向 LLM 投递。
	time.Sleep(500 * time.Millisecond)
	if got := completesForSession(rec, "s-askc-1"); got != completesBefore {
		t.Fatalf("ask-resolve 不应新增轮次完成事件（不该向 LLM 投递）：before=%d after=%d", completesBefore, got)
	}
	// 幂等：再次 resolve 同一 ask → ok:true，且不再广播第二条 remove。
	resolve2 := callTopic(t, bus, "ask-resolve", map[string]any{"ask_id": askIDs[0]})
	if resolve2["ok"] != true {
		t.Fatalf("重复 ask-resolve ok = %v, want true（幂等）", resolve2["ok"])
	}
	time.Sleep(300 * time.Millisecond)
	rec.mu.Lock()
	removes := 0
	for _, m := range rec.evs["ask-center-remove"] {
		if str(m, "ask_id") == askIDs[0] {
			removes++
		}
	}
	rec.mu.Unlock()
	if removes != 1 {
		t.Fatalf("ask-center-remove 对同一 ask 应恰 1 条（幂等），got %d", removes)
	}
	// 快照摘除：重新订阅后只剩第二条。
	asks = asksOf(t, callTopic(t, bus, "ask-center-subscribe", map[string]any{"instance_id": inst}))
	if len(asks) != 1 || asks[0]["ask_id"] != askIDs[1] {
		t.Fatalf("resolved 后快照应只剩未办结的第 2 条（%q），got %+v", askIDs[1], asks)
	}

	// ③ 订阅在册时的新提问 → **真实分发**：ask-center-add 逐条推送（61 §168：{ask, work_dir,
	// received_at, seq?}；后端唯一分发 → 订阅在册才推）。快照同时按 seq 续排（1..4）。
	rec.mu.Lock()
	addsBefore := len(rec.evs["ask-center-add"])
	rec.mu.Unlock()
	askEvents2 := driveAskTurn(t, bus, rec, inst, "s-askc-2", "t-askc-2")
	ids2 := make([]string, 0, len(askEvents2))
	for _, ev := range askEvents2 {
		ids2 = append(ids2, str(ev, "ask_id"))
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		rec.mu.Lock()
		n := len(rec.evs["ask-center-add"])
		rec.mu.Unlock()
		if n >= addsBefore+len(ids2) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("订阅在册时新提问应推送 ask-center-add（%d 条），got %d", len(ids2), n-addsBefore)
		}
		time.Sleep(10 * time.Millisecond)
	}
	rec.mu.Lock()
	adds := append([]map[string]any(nil), rec.evs["ask-center-add"][addsBefore:]...)
	rec.mu.Unlock()
	for i, m := range adds {
		askObj, _ := m["ask"].(map[string]any)
		if str(askObj, "ask_id") != ids2[i] {
			t.Fatalf("ask-center-add[%d].ask.ask_id = %v, want %q", i, askObj["ask_id"], ids2[i])
		}
		if str(askObj, "question") != askCenterQuestions[i].question {
			t.Fatalf("ask-center-add[%d].ask.question = %v, want %q", i, askObj["question"], askCenterQuestions[i].question)
		}
		if m["work_dir"] != workDir {
			t.Fatalf("ask-center-add[%d].work_dir = %v, want %q", i, m["work_dir"], workDir)
		}
		if rcv, _ := m["received_at"].(float64); rcv <= 0 {
			t.Fatalf("ask-center-add[%d].received_at = %v, want unix ms > 0", i, m["received_at"])
		}
		if seq, _ := m["seq"].(float64); seq != float64(3+i) {
			t.Fatalf("ask-center-add[%d].seq = %v, want %d（每 workdir 单调续排）", i, m["seq"], 3+i)
		}
	}
	// 快照 = 第 1 轮未办结的第 2 条 + 第 2 轮两条，按 seq 升序（61 §4.2「workdir + seq 升序」）。
	asks = asksOf(t, callTopic(t, bus, "ask-center-subscribe", map[string]any{"instance_id": inst}))
	wantOrder := []string{askIDs[1], ids2[0], ids2[1]}
	if len(asks) != len(wantOrder) {
		t.Fatalf("快照应含 %d 条（第 1 条已 resolved），got %d: %+v", len(wantOrder), len(asks), asks)
	}
	for i, want := range wantOrder {
		if asks[i]["ask_id"] != want {
			t.Fatalf("asks[%d].ask_id = %v, want %q（seq 升序）", i, asks[i]["ask_id"], want)
		}
		if seq, _ := asks[i]["seq"].(float64); seq != float64(i+2) {
			t.Fatalf("asks[%d].seq = %v, want %d", i, asks[i]["seq"], i+2)
		}
	}

	// ④ 退订（幂等）→ 之后新提问不再分发到 Ask 中心。
	unsub := callTopic(t, bus, "ask-center-unsubscribe", map[string]any{"instance_id": inst})
	ts, ok = byTopic["ask-center-unsubscribe"]
	if !ok {
		t.Fatal("契约未收录 ask-center-unsubscribe")
	}
	srvAssertKeys(t, "ask-center-unsubscribe", "result", ts.Result, unsub)
	if unsub["ok"] != true {
		t.Fatalf("unsubscribe ok = %v, want true", unsub["ok"])
	}
	if again := callTopic(t, bus, "ask-center-unsubscribe", map[string]any{"instance_id": inst}); again["ok"] != true {
		t.Fatalf("重复 unsubscribe ok = %v, want true（61 §4.2：幂等）", again["ok"])
	}

	// 退订后新提问（第 3 轮）：ask-center-add 不得到达（后端唯一分发 → 无订阅即不推）。
	rec.mu.Lock()
	addsBefore = len(rec.evs["ask-center-add"])
	rec.mu.Unlock()
	askEvents = driveAskTurn(t, bus, rec, inst, "s-askc-3", "t-askc-3")
	if len(askEvents) != len(askCenterQuestions) {
		t.Fatalf("第 3 轮应产生 %d 条 session-ask，got %d", len(askCenterQuestions), len(askEvents))
	}
	time.Sleep(700 * time.Millisecond)
	rec.mu.Lock()
	addsAfter := len(rec.evs["ask-center-add"])
	rec.mu.Unlock()
	if addsAfter != addsBefore {
		t.Fatalf("退订后不应再推送 ask-center-add：before=%d after=%d", addsBefore, addsAfter)
	}

	// ⑤ 作用域边界（61 §4.2「work_dir 非空 → 只订该目录」）：另一项目实例按**自己的** work_dir
	// 订阅 → 本项目的待办（5 条）不进入其快照（Ask 中心跨项目单例，但作用域按 work_dir 收窄）。
	otherDir := t.TempDir()
	registerInstanceAt(t, bus, "ins-askc-other", otherDir)
	scoped := callTopic(t, bus, "ask-center-subscribe", map[string]any{
		"instance_id": "ins-askc-other", "work_dir": otherDir,
	})
	if scoped["ok"] != true {
		t.Fatalf("按 work_dir 订阅 ok = %v, want true", scoped["ok"])
	}
	if scopedAsks := asksOf(t, scoped); len(scopedAsks) != 0 {
		t.Fatalf("work_dir 收窄后不应看到其他项目的待办，got %+v", scopedAsks)
	}
}

// completesForSession 统计某会话的 session-complete 条数（负例：resolve 不投递 → 不新增轮次）。
func completesForSession(rec *evtRec, session string) int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	n := 0
	for _, m := range rec.evs["session-complete"] {
		if str(m, "session") == session {
			n++
		}
	}
	return n
}
