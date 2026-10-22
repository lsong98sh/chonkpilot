// 群聊「依次认领」调度器 / work_session / 注入 / @ 过滤 / 无关 / 屏障 / 到顶续跑 的
// 驱动与断言（SUBCHAT-3 阶段 F）。
//
// 测试准则（项目规则 / 50-测试体系 §1）：**发送 mq 消息驱动、监听 mq 消息断言** ——
// 驱动面 = `domain-tool-call`（`subchat_open`）/ `subchat-continue`；断言面 = 监听
// `subchat-opened` / `subchat-state` / `subchat-participant-state` + `data-session-*`（会话行 /
// 名册 / 大轮次投递对象 / 展示板消息）+ work_session 轮次。消息面形状只依据
// `docs/spec/60-reference/61-消息一览.md`（唯一准则）。**用 mock LLM，不触发真实模型**。
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

// ── 可控 LLM 夹具 ────────────────────────────────────────────────────

// groupLLMCall 是一次 LLM 请求的观测（agent = 从 system 里解析出的子会话 agent 名）。
type groupLLMCall struct {
	agent string
	last  string
	msgs  []ChatMsg
}

// groupLLMLog 归集 LLM 请求（按 agent 分组）。
type groupLLMLog struct {
	mu    sync.Mutex
	all   []groupLLMCall
	byAgt map[string][]groupLLMCall
}

func newGroupLLMLog() *groupLLMLog { return &groupLLMLog{byAgt: map[string][]groupLLMCall{}} }

func (l *groupLLMLog) add(c groupLLMCall) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.all = append(l.all, c)
	l.byAgt[c.agent] = append(l.byAgt[c.agent], c)
}

func (l *groupLLMLog) calls(agent string) []groupLLMCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]groupLLMCall{}, l.byAgt[agent]...)
}

// groupTestLLM 起一个按 `script(agent, 最后一条 user 文本)` 逐次回文本的 mock LLM，并记录全部请求。
func groupTestLLM(script func(agent, last string) string) (*httptest.Server, *groupLLMLog) {
	log := newGroupLLMLog()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ChatMsg `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		agent := sysAgentRef(body.Messages)
		last := lastUserText(body.Messages)
		log.add(groupLLMCall{agent: agent, last: last, msgs: body.Messages})
		text := script(agent, last)
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": text}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	}))
	return srv, log
}

// sysAgentRef 从 system 消息里解析「子会话 agent」名（`childAgentSystem` 的第 0 行）。
func sysAgentRef(msgs []ChatMsg) string {
	for _, m := range msgs {
		if m.Role != "system" {
			continue
		}
		const p = "[子会话 agent: "
		if i := strings.Index(m.Content, p); i >= 0 {
			rest := m.Content[i+len(p):]
			if j := strings.IndexByte(rest, ']'); j >= 0 {
				return strings.TrimSpace(rest[:j])
			}
		}
	}
	return ""
}

// lastUserText 取最后一条 user 消息正文。
func lastUserText(msgs []ChatMsg) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	return ""
}

// scripted 构造按调用次序回文本的脚本（超出次数 → 重复最后一条；无脚本 → 空）。
func scripted(m map[string][]string) func(string, string) string {
	var mu sync.Mutex
	idx := map[string]int{}
	return func(agent, _ string) string {
		mu.Lock()
		defer mu.Unlock()
		reps := m[agent]
		if len(reps) == 0 {
			return ""
		}
		i := idx[agent]
		idx[agent]++
		if i >= len(reps) {
			i = len(reps) - 1
		}
		return reps[i]
	}
}

// ── 群聊夹具与断言工具 ───────────────────────────────────────────────

// openGroup 经 `domain-tool-call` 驱动 `subchat_open`（带 participants）并返回新建群聊会话 id。
// mainAgent = 主 agent 名（= `agent` 入参；须在 participants 内，否则被自动补入）。
func openGroup(t *testing.T, s *Server, mainAgent string, participants []any, goal, playbook, convergence string) string {
	t.Helper()
	res := emitDomainTool(t, s, "subchat_open", map[string]any{
		"agent": mainAgent, "scenario_id": "scn-1", "llm": "llm-1", "prompt": "审查这次改动",
		"participants": participants, "goal": goal, "playbook": playbook, "convergence": convergence,
	}, "t-g", "s-main-g", "tc-open", "代码审查")
	text := domainResultText(t, res)
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil || out["ok"] != true {
		t.Fatalf("subchat_open 群聊分支应 ok=true：%q err=%v", text, err)
	}
	gid, _ := out["session_id"].(string)
	if gid == "" {
		t.Fatalf("应返回 session_id：%q", text)
	}
	return gid
}

// groupRoster 读群聊名册（按 seq 升序）。
func groupRoster(t *testing.T, s *Server, gid string) []facade.GroupParticipant {
	t.Helper()
	res, err := s.cfg.GroupParticipantsLoad(facade.GroupParticipantsLoadRequest{
		InstanceID: "ins-test", SessionID: gid, Scope: s.cfgScope("ins-test"),
	})
	if err != nil {
		t.Fatalf("GroupParticipantsLoad(%s): %v", gid, err)
	}
	return res.List
}

// groupRoundTargets 读群聊各大轮次投递对象（升序）。
func groupRoundTargets(t *testing.T, s *Server, gid string) []facade.GroupRoundTarget {
	t.Helper()
	res, err := s.cfg.GroupRoundTargetsLoad(facade.GroupRoundTargetsLoadRequest{
		InstanceID: "ins-test", SessionID: gid, Scope: s.cfgScope("ins-test"),
	})
	if err != nil {
		t.Fatalf("GroupRoundTargetsLoad(%s): %v", gid, err)
	}
	return res.List
}

// boardMessages 读群聊展示板消息视图（含 sender_agent / mentions）。
func boardMessages(t *testing.T, s *Server, gid string) []map[string]any {
	t.Helper()
	res := dataResult(t, dataCall(t, s, "data-session-history", map[string]any{
		"instance_id": "ins-test", "session_id": gid,
	}))
	msgs, _ := res["messages"].(map[string]any)
	if msgs == nil {
		return nil
	}
	raw, _ := msgs["messages"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, e := range raw {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// boardTexts 取展示板上所有**非 user 布置**消息的正文（用于「不上屏」断言）。
func boardAgentTexts(msgs []map[string]any) []string {
	out := []string{}
	for _, m := range msgs {
		if a := str(m["sender_agent"]); a != "" {
			out = append(out, str(m["content"]))
		}
	}
	return out
}

// waitLifecycle 等群聊 lifecycle 事件（subchat-state）。
func waitLifecycle(t *testing.T, rec *busRecorder, gid, lifecycle string, timeout time.Duration) map[string]any {
	t.Helper()
	return rec.wait(t, "subchat-state", func(m map[string]any) bool {
		return str(m["session_id"]) == gid && str(m["lifecycle"]) == lifecycle
	}, timeout)
}

// ── 用例 ─────────────────────────────────────────────────────────────

// TestGroupChatOpenSessionAndRoster（F1-1 · 群聊 §5/D44）：N≥2 → 建 kind=group_chat 会话
// （lifecycle=chatting + parent + agent=主 agent + goal/playbook/convergence）+ 写名册（含 seq 顺序）
// + 发 subchat-opened + 展示板首条 = 主 agent 布置任务（role=user，D18/D47）。
func TestGroupChatOpenSessionAndRoster(t *testing.T) {
	llm, _ := groupTestLLM(scripted(map[string][]string{
		"main": {"【已收敛】\n完成"},
		"b":    {"无关"},
	}))
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-main-g", "t-g")
	rec := newBusRecorder(t, s.bus)

	gid := openGroup(t, s, "main", []any{
		map[string]any{"agent": "b", "role": "程序员"},
		map[string]any{"agent": "main", "role": "审查员"},
	}, "审查 src/foo/", "发现问题→@程序员", "连续2轮无新问题即收敛")

	// ① 会话行
	sess := sessionOf(t, s, gid)
	if sess.Kind != facade.SessionKindGroupChat || sess.ParentID != "s-main-g" ||
		sess.Lifecycle != facade.SessionLifecycleChatting || sess.Agent != "main" {
		t.Fatalf("群聊会话行不符：%+v", sess)
	}
	if sess.Goal != "审查 src/foo/" || sess.Playbook != "发现问题→@程序员" ||
		sess.Convergence != "连续2轮无新问题即收敛" {
		t.Fatalf("群聊 目标/剧本/收敛 未落库：%+v", sess)
	}

	// ② 名册（seq 固定顺序 = 入参顺序）
	roster := groupRoster(t, s, gid)
	if len(roster) != 2 || roster[0].Agent != "b" || roster[0].Seq != 1 ||
		roster[1].Agent != "main" || roster[1].Seq != 2 {
		t.Fatalf("名册顺序/seq 不符：%+v", roster)
	}
	if roster[1].Role != "审查员" {
		t.Fatalf("名册角色不符：%+v", roster[1])
	}

	// ③ subchat-opened（父窗口 + title/purpose）
	opened := rec.wait(t, "subchat-opened", func(m map[string]any) bool { return str(m["session_id"]) == gid }, 3*time.Second)
	if str(opened["instance_id"]) != "ins-test" || str(opened["parent_session_id"]) != "s-main-g" {
		t.Fatalf("subchat-opened 归属不符：%+v", opened)
	}

	// ④ 展示板首条 = 布置任务（role=user + sender_agent=主 agent）
	msgs := boardMessages(t, s, gid)
	if len(msgs) == 0 || str(msgs[0]["role"]) != "user" || str(msgs[0]["content"]) != "审查这次改动" ||
		str(msgs[0]["sender_agent"]) != "main" {
		t.Fatalf("展示板首条应为 布置任务（role=user/sender_agent=main）：%+v", msgs)
	}

	// 收敛（主 agent 判达标）→ ready + subchat-state
	waitLifecycle(t, rec, gid, facade.SessionLifecycleReady, 15*time.Second)
	if got := sessionOf(t, s, gid).Lifecycle; got != facade.SessionLifecycleReady {
		t.Fatalf("收敛后 lifecycle 应为 ready，got %q", got)
	}
}

// TestGroupChatMentionFilterAndIrrelevant（F1-3 · D2/D3/D5/D13/D21/D27/D31/D32/D33）：
// 依次认领顺序 + `@` 定向过滤（定向条只投被 @ 者）+ 「无关」不上屏/不入下一轮 + 每大轮次投递对象落库。
func TestGroupChatMentionFilterAndIrrelevant(t *testing.T) {
	// 脚本按**输入内容**分支（不依赖调用次序 → 稳健）：
	//  r1 各人输入 = 布置任务；r2 各人输入 = 上一轮适用发言（a 只收到 b 的公开条；c 收到 a 的定向条 + b 的公开条）。
	llm, log := groupTestLLM(func(agent, last string) string {
		switch agent {
		case "a":
			if strings.Contains(last, "公开发言") {
				return "【已收敛】\na：完成"
			}
			return "a：给 c 的任务\n@c"
		case "b":
			if strings.Contains(last, "公开发言") {
				return "b：收到"
			}
			return "b：公开发言"
		default: // c
			if strings.Contains(last, "给 c 的任务") {
				return "c：收到 a 的任务"
			}
			return "无关"
		}
	})
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-main-g", "t-g")
	rec := newBusRecorder(t, s.bus)

	gid := openGroup(t, s, "a", []any{
		map[string]any{"agent": "a"}, map[string]any{"agent": "b"}, map[string]any{"agent": "c"},
	}, "目标", "剧本", "收敛")
	if r := groupRoster(t, s, gid); len(r) != 3 || r[0].Agent != "a" || r[2].Agent != "c" {
		t.Fatalf("名册应保持入参顺序 a,b,c：%+v", r)
	}

	// 本轮屏障满足 → 收敛时收口（ready）
	waitLifecycle(t, rec, gid, facade.SessionLifecycleReady, 20*time.Second)

	// ① 每大轮次投递对象：r1 = 全体；@ 定向条只点 c → r2 仍含 c 且含全体（因 b 的公开条）
	targets := groupRoundTargets(t, s, gid)
	if len(targets) < 2 {
		t.Fatalf("应至少落两轮投递对象：%+v", targets)
	}
	if got := targets[0].Agents; len(got) != 3 {
		t.Fatalf("第 1 轮投递对象应为全体：%+v", got)
	}

	// ② 展示板：定向条带 mentions=[c]；公开条无 mentions；「无关」不上屏
	msgs := boardMessages(t, s, gid)
	var aMsg, bMsg map[string]any
	for _, m := range msgs {
		if strings.Contains(str(m["content"]), "给 c 的任务") {
			aMsg = m
		}
		if str(m["sender_agent"]) == "b" && bMsg == nil {
			bMsg = m
		}
	}
	if aMsg == nil || !strings.Contains(str(aMsg["content"]), "给 c 的任务") {
		t.Fatalf("a 的定向发言应上屏：%+v", msgs)
	}
	ment, _ := aMsg["mentions"].([]any)
	if len(ment) != 1 || str(ment[0]) != "c" {
		t.Fatalf("a 的定向发言应带 mentions=[c]：%+v", aMsg)
	}
	if bMsg == nil {
		t.Fatalf("b 的公开发言应上屏：%+v", msgs)
	}
	if bm, _ := bMsg["mentions"].([]any); len(bm) != 0 {
		t.Fatalf("b 的公开发言不应带 mentions：%+v", bMsg)
	}
	for _, txt := range boardAgentTexts(msgs) {
		if strings.Contains(txt, "无关") {
			t.Fatalf("「无关」不得上屏：%q", txt)
		}
	}

	// ③ 参与者状态三态
	st := rec.snapshot("subchat-participant-state")
	hasState := func(agent, state string) bool {
		for _, m := range st {
			if str(m["agent"]) == agent && str(m["state"]) == state {
				return true
			}
		}
		return false
	}
	if !hasState("a", "thinking") || !hasState("a", "spoken") {
		t.Fatalf("a 应有 thinking/spoken：%+v", st)
	}
	if !hasState("c", "idle") {
		t.Fatalf("c（无关）应有 idle：%+v", st)
	}

	// ④ `@` 投递过滤（D33）：c 的第 2 次输入含「给 c 的任务」；a 的第 2 次输入不含
	cCalls, aCalls := log.calls("c"), log.calls("a")
	if len(cCalls) < 2 || len(aCalls) < 2 {
		t.Fatalf("a/c 应各被叫 ≥2 次：a=%d c=%d", len(aCalls), len(cCalls))
	}
	if !strings.Contains(cCalls[1].last, "给 c 的任务") {
		t.Fatalf("c 的第 2 轮输入应含定向条：%q", cCalls[1].last)
	}
	if strings.Contains(aCalls[1].last, "给 c 的任务") {
		t.Fatalf("a 不是被 @ 者，不应收到该定向条：%q", aCalls[1].last)
	}
	if !strings.Contains(aCalls[1].last, "公开发言") {
		t.Fatalf("a 应收到公开条：%q", aCalls[1].last)
	}

	// ⑤ work_session（D12/D23）：每个参与者一个、跨轮复用（第 2 次输入含第 1 次的上下文）
	work := groupWorkSessionID(gid, "c")
	if got := sessionOf(t, s, work).Kind; got != facade.SessionKindWorkSession {
		t.Fatalf("干活子会话 kind 应为 work_session，got %q", got)
	}
	if !strings.Contains(strings.Join(msgContents(cCalls[1].msgs), "\n"), "审查这次改动") {
		t.Fatalf("c 的第 2 轮请求应带第 1 轮（布置任务）历史 → 同一 work_session 复用：%q", cCalls[1].last)
	}
}

// TestGroupChatNoClaimFallbackAndCapContinue（F1-5 · D5/D49/D50）：全员「无关」→ 每轮只有主 agent
// 兜底；到顶强制停 + 主 agent 终审（未达成 → finished + 提示）；`subchat-continue` 续跑。
func TestGroupChatNoClaimFallbackAndCapContinue(t *testing.T) {
	llm, log := groupTestLLM(func(agent, last string) string {
		switch {
		case strings.Contains(last, "已达群聊轮次上限"):
			return "【未达成】\n进度摘要：还有问题未闭环。"
		case strings.Contains(last, "用户要求继续推进本次任务"):
			if agent == "main" {
				return "【已收敛】\n已达成。"
			}
			return "无关"
		default:
			return "无关"
		}
	})
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-main-g", "t-g")
	rec := newBusRecorder(t, s.bus)

	gid := openGroup(t, s, "main", []any{
		map[string]any{"agent": "main"}, map[string]any{"agent": "worker"},
	}, "目标", "剧本", "收敛")

	// 到顶强制停 → finished
	waitLifecycle(t, rec, gid, facade.SessionLifecycleFinished, 40*time.Second)

	targets := groupRoundTargets(t, s, gid)
	if len(targets) < 2 {
		t.Fatalf("应至少 2 轮：%+v", targets)
	}
	if len(targets[0].Agents) != 2 {
		t.Fatalf("第 1 轮应为全体：%+v", targets[0].Agents)
	}
	// 无人认领 → 兜底只叫主 agent（D5）
	for _, tg := range targets[1:] {
		if len(tg.Agents) != 1 || tg.Agents[0] != "main" {
			t.Fatalf("无人认领后每轮应只叫主 agent：round=%d agents=%v", tg.Round, tg.Agents)
		}
	}
	// 「无关」不上屏；但有系统提示（因达上限终止）。
	// 提示在 lifecycle=finished 之后追加（finalReview 先置终态再 appendBoardNotice）→ 观察到
	// finished 时看板可能尚未落到提示，故轮询等待（与既有异步落盘语义一致）。
	var msgs []map[string]any
	deadline := time.Now().Add(3 * time.Second)
	for {
		msgs = boardMessages(t, s, gid)
		foundNotice := false
		for _, m := range msgs {
			if strings.Contains(str(m["content"]), "因达轮次上限终止") {
				foundNotice = true
			}
			if strings.Contains(str(m["content"]), "无关") {
				t.Fatalf("「无关」不得上屏：%+v", m)
			}
		}
		if foundNotice || time.Now().After(deadline) {
			if !foundNotice {
				t.Fatalf("到顶未达成应有提示：%+v", msgs)
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 用户「继续」→ chatting + **断点补跑**（F2-4/D45）：上一大轮次投递对象 = [main]，main 无结论
	// → 只补跑 main（**不另起新大轮次**、不覆盖该轮记录）；补齐后主 agent 收敛 → ready。
	mainBefore := len(log.calls("main"))
	rec2 := newBusRecorder(t, s.bus)
	v := s.bus.Emit(context.Background(), "subchat-continue", jb(map[string]any{
		"instance_id": "ins-test", "session_id": gid,
	})).Wait()
	if v.Err() != nil {
		t.Fatalf("subchat-continue: %v", v.Err())
	}
	if res, _ := v.Result.(map[string]any); res["ok"] != true {
		t.Fatalf("subchat-continue 应返回 {ok:true}：%+v", res)
	}
	waitLifecycle(t, rec2, gid, facade.SessionLifecycleChatting, 5*time.Second)
	waitLifecycle(t, rec2, gid, facade.SessionLifecycleReady, 20*time.Second)

	// 断点补跑：大轮次记录**不新增**（补的是同一轮）且**不覆盖**既有记录（应仍为 [main]）；main 被重叫。
	after := groupRoundTargets(t, s, gid)
	last := after[len(after)-1]
	if last.Round != targets[len(targets)-1].Round {
		t.Fatalf("断点补跑不应另起新大轮次：before=%d after=%d",
			targets[len(targets)-1].Round, last.Round)
	}
	if len(last.Agents) != 1 || last.Agents[0] != "main" {
		t.Fatalf("断点补跑不得覆盖既有轮次记录（应仍为 [main]）：%+v", after)
	}
	if len(log.calls("main")) <= mainBefore {
		t.Fatalf("续跑应重叫主 agent（补跑无结论者）：before=%d after=%d", mainBefore, len(log.calls("main")))
	}
}

// TestSubChatOpenSingleNoRegression（F1-1 · D39）：**省略 `participants` 或只给 1 项 → 单聊**
// （kind=sub_chat），N≥2 才走群聊（kind=group_chat）。
func TestSubChatOpenSingleNoRegression(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-main-g", "t-g")

	// 省略 participants → 单聊
	res := emitDomainTool(t, s, "subchat_open", map[string]any{
		"agent": "ag-1", "scenario_id": "scn-1", "llm": "llm-1", "prompt": "想讨论",
	}, "t-g", "s-main-g", "tc-s1", "单聊")
	var out map[string]any
	_ = json.Unmarshal([]byte(domainResultText(t, res)), &out)
	sid, _ := out["session_id"].(string)
	if sid == "" || sessionOf(t, s, sid).Kind != facade.SessionKindSubChat {
		t.Fatalf("省略 participants 应走单聊（sub_chat）：%+v", out)
	}
	rec := newBusRecorder(t, s.bus)
	rec.wait(t, "session-complete", func(m map[string]any) bool { return str(m["session"]) == sid }, 10*time.Second)

	// 只给 1 项 participants → 仍单聊
	res2 := emitDomainTool(t, s, "subchat_open", map[string]any{
		"agent": "ag-2", "scenario_id": "scn-1", "llm": "llm-1", "prompt": "想讨论2",
		"participants": []any{map[string]any{"agent": "ag-2"}},
	}, "t-g", "s-main-g", "tc-s2", "单聊2")
	_ = json.Unmarshal([]byte(domainResultText(t, res2)), &out)
	sid2, _ := out["session_id"].(string)
	if sid2 == "" || sessionOf(t, s, sid2).Kind != facade.SessionKindSubChat {
		t.Fatalf("1 项 participants 应仍走单聊（sub_chat）：%+v", out)
	}
	rec.wait(t, "session-complete", func(m map[string]any) bool { return str(m["session"]) == sid2 }, 10*time.Second)
}

// TestGroupMentionParseAndMarkers 是解析 / 机读约定的纯函数单测（不驱动链路）。
func TestGroupMentionParseAndMarkers(t *testing.T) {
	sc := &groupSched{byAgent: map[string]groupParticipant{
		"scn/a": {agent: "scn/a"}, "b": {agent: "b"}, "c": {agent: "c"},
	}}
	if got := sc.parseMentions("结论\n@b @c"); len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("多 @ 解析不符：%v", got)
	}
	if got := sc.parseMentions("结论\n@a"); len(got) != 1 || got[0] != "scn/a" {
		t.Fatalf("裸名应归一到名册 agent：%v", got)
	}
	if got := sc.parseMentions("结论\n@全体"); len(got) != 1 || got[0] != groupAllMention {
		t.Fatalf("@全体 应归一：%v", got)
	}
	if got := sc.parseMentions("结论（无 @）"); len(got) != 0 {
		t.Fatalf("无 @ 应返回空：%v", got)
	}
	if !isGroupIrrelevant("无关\n（本轮与我无关）") || isGroupIrrelevant("这条有关") {
		t.Fatal("「无关」判定不符（首个非空行须恰为「无关」）")
	}
	if !isGroupConverged("说明\n【已收敛】\n收尾") || isGroupConverged("这是【已收敛】的内联说法") {
		t.Fatal("「已收敛」判定不符（须单独一行）")
	}
	if groupWorkSessionID("g-1", "scn/a") == groupWorkSessionID("g-1", "scn/b") ||
		groupWorkSessionID("g-1", "scn/a") != groupWorkSessionID("g-1", "scn/a") {
		t.Fatal("work_session id 应确定性且可区分")
	}
}

// msgContents 取消息正文列表（断言历史累积用）。
func msgContents(msgs []ChatMsg) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Content)
	}
	return out
}

// ── F2（SUBCHAT-3 阶段 F-2）：ask 定向 / 驱动实例 / 关窗即停 / 断点补跑 / 工具面收窄 ──────

// regInstance 发布 instance-register（可选 session_id；F2-1/F2-2 用）。
func regInstance(s *Server, id, sessionID string) {
	body := map[string]any{"instance_id": id, "client_type": "unittest", "work_dir": testWorkDir}
	if sessionID != "" {
		body["session_id"] = sessionID
	}
	_ = s.bus.Emit(context.Background(), "instance-register", jb(body))
}

// putRunningTurn 直接放一条运行 turn（仅 `turnSession` 读 req.Session；不启 LLM 循环）。
// 构造须带 ctx/cancel/done —— `s.Stop` 会 Close 全部登记 turn（nil cancel 会 panic）。
func putRunningTurn(s *Server, turnID, session string) {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.turns[instKey("ins-test", turnID)] = &turnCtx{
		req:    StartReq{InstanceID: "ins-test", Session: session, Turn: turnID},
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	s.mu.Unlock()
}

// TestAskTargetInstanceReverseLookup（F2-1 · 群聊 §13 D42）：`ask_user` 定向目标实例按**会话所在
// 窗口**推导 —— work_session → 父 group_chat → 群聊窗口实例；sub_chat / group_chat 本体 → 承载窗口；
// 未命中（普通会话 / 窗口未开）→ 回落 turn 实例。
func TestAskTargetInstanceReverseLookup(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	regInstance(s, "ins-group", "g-1") // 群聊窗口承载 g-1
	regInstance(s, "ins-sub", "sc-1")  // 单聊窗口承载 sc-1
	regInstance(s, "ins-plain", "")    // 主窗口（不带 session_id）
	time.Sleep(40 * time.Millisecond)

	scope := s.cfgScope("ins-test")
	mustEnsure := func(req facade.SessionEnsureRequest) {
		t.Helper()
		if _, err := s.cfg.SessionEnsure(req); err != nil {
			t.Fatalf("SessionEnsure(%s): %v", req.SessionID, err)
		}
	}
	mustEnsure(facade.SessionEnsureRequest{InstanceID: "ins-test", Scope: scope, SessionID: "g-1",
		Kind: facade.SessionKindGroupChat, Lifecycle: facade.SessionLifecycleChatting, Agent: "main"})
	work := groupWorkSessionID("g-1", "a")
	mustEnsure(facade.SessionEnsureRequest{InstanceID: "ins-test", Scope: scope, SessionID: work,
		ParentSessionID: "g-1", Kind: facade.SessionKindWorkSession, Agent: "a"})
	mustEnsure(facade.SessionEnsureRequest{InstanceID: "ins-test", Scope: scope, SessionID: "sc-1",
		Kind: facade.SessionKindSubChat, Lifecycle: facade.SessionLifecycleChatting, Agent: "sub"})

	cases := []struct{ name, turn, sess, want string }{
		{"work_session → 父群聊 → 群聊窗口", "t-w", work, "ins-group"},
		{"group_chat 本体 → 群聊窗口", "t-g", "g-1", "ins-group"},
		{"sub_chat 本体 → 单聊窗口", "t-s", "sc-1", "ins-sub"},
		{"普通会话（无承载窗口）→ 回落 turn 实例", "t-p", "s-plain", "ins-test"},
	}
	for _, tc := range cases {
		putRunningTurn(s, tc.turn, tc.sess)
		if got := s.askTargetInstance("ins-test", tc.turn); got != tc.want {
			t.Errorf("%s: askTargetInstance=%q，want %q", tc.name, got, tc.want)
		}
	}
	// 未知 turn → 回落传入实例（零行为变化）。
	if got := s.askTargetInstance("ins-x", "t-none"); got != "ins-x" {
		t.Errorf("未知 turn 应回落 turn 实例，got %q", got)
	}
}

// TestGroupResumePlanBreakpoint（F2-4 · D45）：续跑计划 = 只补跑**无结论者**；本轮已齐 → 下一轮。
func TestGroupResumePlanBreakpoint(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	gid := "group-plan"
	scope := s.cfgScope("ins-test")
	if _, err := s.cfg.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: "ins-test", Scope: scope, SessionID: gid,
		Kind: facade.SessionKindGroupChat, Lifecycle: facade.SessionLifecycleChatting, Agent: "a",
	}); err != nil {
		t.Fatalf("ensure group: %v", err)
	}
	if _, err := s.cfg.GroupParticipantsSave(facade.GroupParticipantsSaveRequest{
		InstanceID: "ins-test", SessionID: gid, Scope: scope,
		List: []facade.GroupParticipant{{Agent: "a"}, {Agent: "b"}},
	}); err != nil {
		t.Fatalf("roster: %v", err)
	}
	// 展示板：首条 = 主 agent（a）布置任务（role=user）。
	store := newSessionStore(s.bus, "ins-test")
	board := newID()
	if err := store.EnsureTurn(board, gid); err != nil {
		t.Fatalf("board turn: %v", err)
	}
	if _, err := store.AppendMsgMap(board, map[string]any{
		"role": "user", "content": "布置", "sender_agent": "a",
	}, ""); err != nil {
		t.Fatalf("opening: %v", err)
	}
	// 最后一个大轮次（r1）投递对象 = [a, b]。
	if _, err := s.cfg.GroupRoundTargetsSave(facade.GroupRoundTargetsSaveRequest{
		InstanceID: "ins-test", SessionID: gid, Round: 1, Agents: []string{"a", "b"}, Scope: scope,
	}); err != nil {
		t.Fatalf("targets: %v", err)
	}

	sc, err := s.buildGroupSched("ins-test", gid)
	if err != nil {
		t.Fatalf("buildGroupSched: %v", err)
	}
	plan := func() (int, []groupParticipant, bool) {
		start, targets, _, saveFirst := s.groupResumePlan(sc)
		return start, targets, saveFirst
	}

	// ① 双方均无结论 → 补跑 [a, b]（start=1；不覆盖该轮记录）。
	start, targets, saveFirst := plan()
	if start != 1 || saveFirst || len(targets) != 2 || targets[0].agent != "a" || targets[1].agent != "b" {
		t.Fatalf("双方无结论应补跑全体：start=%d targets=%+v saveFirst=%v", start, targets, saveFirst)
	}

	// ② a 已产结论、b 无 → **只补跑 b**（有结论者跳过）。
	if _, err := store.AppendMsgMap(board, map[string]any{
		"role": "user", "content": "a:结论", "sender_agent": "a",
	}, ""); err != nil {
		t.Fatalf("a 结论: %v", err)
	}
	start, targets, saveFirst = plan()
	if start != 1 || saveFirst || len(targets) != 1 || targets[0].agent != "b" {
		t.Fatalf("a 有结论应只补 b：start=%d targets=%+v saveFirst=%v", start, targets, saveFirst)
	}

	// ③ b 也补齐 → 本轮已齐 → 从下一轮起（targets=nil=全体，saveFirst=true）。
	if _, err := store.AppendMsgMap(board, map[string]any{
		"role": "assistant", "content": "b:结论", "sender_agent": "b",
	}, ""); err != nil {
		t.Fatalf("b 结论: %v", err)
	}
	start, targets, saveFirst = plan()
	if start != 2 || !saveFirst || targets != nil {
		t.Fatalf("本轮已齐应从下一轮起：start=%d targets=%+v saveFirst=%v", start, targets, saveFirst)
	}
}

// TestGroupChatDriverInstanceAndWindowStop（F2-2 / F2-3 · §15.4 N23）：群聊驱动实例 = 群聊窗口实例
// （注册 session_id=群聊会话）；关窗（`instance-exit`）→ 调度**停机**（暂停：不落 finished，重开可续）。
func TestGroupChatDriverInstanceAndWindowStop(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		time.Sleep(120 * time.Millisecond) // 拉长轮次 → 观察驱动实例 / 停机
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": "无关"}, ""), // 全员无关 → 主 agent 兜底逐轮（D5）
			sseChunk(map[string]any{}, "stop"),
		})
	}))
	defer srv.Close()
	s := newTestServer(t, srv)
	startTurn(t, s, "s-main-g", "t-g") // openGroup 以 (t-g, s-main-g) 驱动域工具

	gid := openGroup(t, s, "main", []any{
		map[string]any{"agent": "main"}, map[string]any{"agent": "worker"},
	}, "目标", "剧本", "收敛")

	// 注册**群聊窗口实例**（承载该群聊会话）→ 驱动实例应解析为它（F2-2）。
	regInstance(s, "ins-group-win", gid)
	waitCond := func(d time.Duration, cond func() bool) bool {
		deadline := time.Now().Add(d)
		for time.Now().Before(deadline) {
			if cond() {
				return true
			}
			time.Sleep(10 * time.Millisecond)
		}
		return false
	}
	driverOK := waitCond(5*time.Second, func() bool {
		s.groupMu.Lock()
		sc := s.groupRuns[gid]
		s.groupMu.Unlock()
		return sc != nil && sc.currentDriver() == "ins-group-win"
	})
	if !driverOK {
		t.Fatal("群聊驱动实例应解析为群聊窗口实例（F2-2 §15.4 N23）")
	}

	// 关窗（instance-exit）→ 停机（运行注销）。
	_ = s.bus.Emit(context.Background(), "instance-exit", jb(map[string]any{"instance_id": "ins-group-win"}))
	if !waitCond(5*time.Second, func() bool {
		s.groupMu.Lock()
		_, running := s.groupRuns[gid]
		s.groupMu.Unlock()
		return !running
	}) {
		t.Fatal("关窗后群聊调度器应停机（F2-2 关窗即停）")
	}

	// 停机后不再推进（LLM 调用数冻结）且 lifecycle 保持 chatting（**暂停不落库终态**，F2-3/N25）。
	mu.Lock()
	n1 := calls
	mu.Unlock()
	time.Sleep(400 * time.Millisecond)
	mu.Lock()
	n2 := calls
	mu.Unlock()
	if n2 != n1 {
		t.Fatalf("关窗停机后不应继续推进：calls %d → %d", n1, n2)
	}
	if lc := sessionOf(t, s, gid).Lifecycle; lc != facade.SessionLifecycleChatting {
		t.Fatalf("关窗 = 暂停（不落库 paused/finished）：lifecycle=%q", lc)
	}
}

// TestGroupChatTerminateSetsFinished（F2-3 · §15.4 N22）：关窗「终止」= 既有 `subchat-finish`
// → lifecycle=finished（不区分正常/强制）。
func TestGroupChatTerminateSetsFinished(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		time.Sleep(120 * time.Millisecond)
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": "无关"}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	}))
	defer srv.Close()
	s := newTestServer(t, srv)
	startTurn(t, s, "s-main-g", "t-g")
	rec := newBusRecorder(t, s.bus)

	gid := openGroup(t, s, "main", []any{
		map[string]any{"agent": "main"}, map[string]any{"agent": "worker"},
	}, "目标", "剧本", "收敛")

	// 等调度器登记（`runGroupChatOpen` 异步起调度器）→ 终止才能命中在跑运行。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.groupMu.Lock()
		_, running := s.groupRuns[gid]
		s.groupMu.Unlock()
		if running {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	v := s.bus.Emit(context.Background(), "subchat-finish", jb(map[string]any{
		"instance_id": "ins-test", "session_id": gid,
	})).Wait()
	if v.Err() != nil {
		t.Fatalf("subchat-finish: %v", v.Err())
	}
	if res, _ := v.Result.(map[string]any); res["ok"] != true {
		t.Fatalf("subchat-finish 应返回 {ok:true}：%+v", res)
	}
	waitLifecycle(t, rec, gid, facade.SessionLifecycleFinished, 5*time.Second)
	if lc := sessionOf(t, s, gid).Lifecycle; lc != facade.SessionLifecycleFinished {
		t.Fatalf("终止后 lifecycle 应为 finished，got %q", lc)
	}
	// 终止后调度器应停机（不再空转）——等运行注销，避免收尾期与在飞 LLM/写库竞争。
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.groupMu.Lock()
		_, running := s.groupRuns[gid]
		s.groupMu.Unlock()
		if !running {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	n1 := calls
	mu.Unlock()
	time.Sleep(400 * time.Millisecond)
	mu.Lock()
	n2 := calls
	mu.Unlock()
	if n2 != n1 {
		t.Fatalf("终止后群聊不应继续空转：calls %d → %d", n1, n2)
	}
	time.Sleep(150 * time.Millisecond)
}

// TestWorkSessionToolFaceExcludesSubchatFinish（F2-5 · 群聊 §7 / D12）：`work_session` 工具面**不含**
// `subchat_finish`（下发面 + 调用处双拦）；**单聊（sub_chat）不受影响**（仍可调用 subchat_finish）。
func TestWorkSessionToolFaceExcludesSubchatFinish(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	scope := s.cfgScope("ins-test")
	for _, req := range []facade.SessionEnsureRequest{
		{InstanceID: "ins-test", Scope: scope, SessionID: "g-ws", Kind: facade.SessionKindGroupChat,
			Lifecycle: facade.SessionLifecycleChatting, Agent: "main"},
		{InstanceID: "ins-test", Scope: scope, SessionID: "work-ws", ParentSessionID: "g-ws",
			Kind: facade.SessionKindWorkSession, Agent: "a"},
		{InstanceID: "ins-test", Scope: scope, SessionID: "sc-ws", Kind: facade.SessionKindSubChat,
			Lifecycle: facade.SessionLifecycleChatting, Agent: "sub"},
	} {
		if _, err := s.cfg.SessionEnsure(req); err != nil {
			t.Fatalf("ensure %s: %v", req.SessionID, err)
		}
	}

	// work_session：硬拒 subchat_finish（剥 self_ 前缀两种口径均拦），下发面亦不含。
	tcW := newTurnCtx(context.Background(), s, StartReq{InstanceID: "ins-test", Session: "work-ws", Turn: "t-ws", Agent: "a"})
	if !tcW.toolDenied(subchatFinishTool) || !tcW.toolDenied("self_"+subchatFinishTool) {
		t.Fatal("work_session 应硬拒 subchat_finish")
	}
	if tcW.toolAllowed("self_" + subchatFinishTool) {
		t.Fatal("work_session 调用处应拒绝 subchat_finish")
	}
	for _, d := range tcW.llmTools() {
		if strings.TrimPrefix(d.Name, "self_") == subchatFinishTool {
			t.Fatalf("work_session 下发面不得含 subchat_finish：%s", d.Name)
		}
	}
	tcW.Close()

	// sub_chat：不受影响（denyTools 空 → 白名单 nil → 放行）。
	tcS := newTurnCtx(context.Background(), s, StartReq{InstanceID: "ins-test", Session: "sc-ws", Turn: "t-sc", Agent: "sub"})
	if tcS.toolDenied(subchatFinishTool) || !tcS.toolAllowed(subchatFinishTool) {
		t.Fatal("sub_chat 不应被拒 subchat_finish（单聊收口仍靠它）")
	}
	tcS.Close()
	time.Sleep(150 * time.Millisecond) // 等 loop 收尾，避免清理期与在飞写库竞争
}
