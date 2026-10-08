package server

// 上下文拼接内置规则的测试（按 turn 分组：维持轮全量 / 非维持轮仅结论 / 重试轮强制全量；
// 见 assemble.go）。替换原 context.assemble.* 可配置方案的用例。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-router"
)

// mkToolCall 造一条工具调用（参数已序列化）。
func mkToolCall(id, name, args string) ToolCall {
	tc := ToolCall{ID: id, Type: "function"}
	tc.Function.Name = name
	tc.Function.Arguments = args
	return tc
}

// twoTurnHist：system 摘要 + T1（中间文本 + 工具 + 结果 + 结论）+ T2（中间文本 + 工具 + 结果 + 结论）。
func twoTurnHist() []ChatMsg {
	return []ChatMsg{
		{Role: "system", Content: "[已压缩早前对话] 摘要"},
		// T1
		{Role: "user", Kind: "text", Content: "问1"},
		{Role: "assistant", Content: "中间文本1"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{mkToolCall("c1", "read", `{"p":"a"}`)}, Reasoning: "想1"},
		{Role: "tool", ToolCallID: "c1", Content: "结果1"},
		{Role: "assistant", Content: "结论1", Reasoning: "想1b"},
		// T2
		{Role: "user", Kind: "text", Content: "问2"},
		{Role: "assistant", Content: "中间文本2"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{mkToolCall("c2", "read", `{"p":"b"}`)}},
		{Role: "tool", ToolCallID: "c2", Content: "结果2"},
		{Role: "assistant", Content: "结论2"},
	}
}

// TestAssembleTurnsKeepRecentFullRestBrief（口径 X 三段）：完整区（最近 1 轮）全量；
// 简化区（更早轮）→ **仅 text**（去 reasoning / tool_call / tool_result）；前导 system 摘要原样透传；
// 不修改入参。
func TestAssembleTurnsKeepRecentFullRestBrief(t *testing.T) {
	hist := twoTurnHist()
	out := assembleTurns(hist, 1, 0, 0, nil, nil, false)
	// 期望：system + T1 简化态（问1 / 中间文本1 / 结论1 = 3 条） + T2 全量（5 条）= 9
	if len(out) != 1+3+5 {
		t.Fatalf("拼接条数错：got %d %+v", len(out), out)
	}
	if out[0].Role != "system" {
		t.Fatalf("前导 system 摘要应透传：%+v", out[0])
	}
	// 简化态：仅 text（无工具调用 / 工具结果 / 空正文轮）
	if out[1].Role != "user" || out[1].Content != "问1" {
		t.Fatalf("简化区应保留用户 text：%+v", out[1])
	}
	if out[2].Role != "assistant" || out[2].Content != "中间文本1" || len(out[2].ToolCalls) != 0 {
		t.Fatalf("简化区应保留 assistant text 且去 tool_calls：%+v", out[2])
	}
	if out[3].Role != "assistant" || out[3].Content != "结论1" {
		t.Fatalf("简化区应保留收尾 text：%+v", out[3])
	}
	// T2 全量（user/中间文本/工具/tool 结果/结论 顺序一致）
	want := hist[6:]
	for i := range want {
		if out[4+i].Role != want[i].Role || out[4+i].Content != want[i].Content {
			t.Fatalf("完整区第 %d 条不符：got %+v want %+v", i, out[4+i], want[i])
		}
	}
	if len(out[6].ToolCalls) != 1 || out[6].ToolCalls[0].ID != "c2" {
		t.Fatalf("完整区工具调用应原文保留：%+v", out[6])
	}
	if out[7].Role != "tool" || out[7].Content != "结果2" {
		t.Fatalf("完整区工具结果应原文保留：%+v", out[7])
	}
	// 入参不被篡改
	if len(hist) != 11 || hist[2].Content != "中间文本1" || len(hist[3].ToolCalls) != 1 {
		t.Fatalf("入参被篡改：%+v", hist)
	}
}

// TestAssembleTurnsMultipleToolsAndTexts：维持轮内多工具调用 + 多条中间文本全量保留。
func TestAssembleTurnsMultipleToolsAndTexts(t *testing.T) {
	hist := []ChatMsg{
		{Role: "user", Kind: "text", Content: "问1"},
		{Role: "assistant", Content: "文本A"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			mkToolCall("c1", "read", `{"p":"a"}`), mkToolCall("c2", "write", `{"p":"b"}`),
		}},
		{Role: "tool", ToolCallID: "c1", Content: "r1"},
		{Role: "tool", ToolCallID: "c2", Content: "r2"},
		{Role: "assistant", Content: "文本B"},
		{Role: "assistant", Content: "结论"},
	}
	out := assembleTurns(hist, 10, 0, 0, nil, nil, false)
	if len(out) != len(hist) {
		t.Fatalf("完整区应全量：got %d want %d %+v", len(out), len(hist), out)
	}
	if len(out[2].ToolCalls) != 2 || out[2].ToolCalls[1].ID != "c2" {
		t.Fatalf("多工具调用应逐条保留：%+v", out[2].ToolCalls)
	}
	if out[1].Content != "文本A" || out[5].Content != "文本B" {
		t.Fatalf("多条中间文本应保留：%+v", out)
	}
}

// TestAssembleTurnsBriefZoneTextOnly（口径 X）：简化区（更早轮）**仅保留 text**——
// 无收尾结论、以工具调用收尾的轮 → 只留用户提问（工具调用/结果被去掉）。
func TestAssembleTurnsBriefZoneTextOnly(t *testing.T) {
	hist := []ChatMsg{
		// T1：以工具调用收尾、无收尾结论
		{Role: "user", Kind: "text", Content: "问1"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{mkToolCall("c1", "read", `{}`)}},
		{Role: "tool", ToolCallID: "c1", Content: "结果1"},
		// T2：完整区（最近 1 轮）
		{Role: "user", Kind: "text", Content: "问2"},
		{Role: "assistant", Content: "结论2"},
	}
	out := assembleTurns(hist, 1, 0, 0, nil, nil, false)
	// 期望：T1 简化态 = 仅用户 text（1 条）+ T2 全量（2 条）
	if len(out) != 3 {
		t.Fatalf("简化区应仅 text（工具调用/结果被去掉）：got %d %+v", len(out), out)
	}
	if out[0].Role != "user" || out[0].Content != "问1" {
		t.Fatalf("简化区应保留用户 text：%+v", out[:1])
	}
	if out[1].Content != "问2" || out[2].Content != "结论2" {
		t.Fatalf("完整区应全量：%+v", out[1:])
	}
}

// TestAssembleTurnsForceFullLast：**组装侧不变式**——最新轮（当前 turn）恒全量（口径 V，
// 共享函数保留轮数下限 1）；forceFullLast=true（重试/恢复的 turn）给出同一结果，保留断言防回归。
//
// 需求同步（2026-09-25 口径 X/W）：N=1 → 仅当前轮全量、前轮**简化态（仅 text）**；
// **N=0 且 M=0（两条件均不启用）→ 不压缩 → 保留全量**。
func TestAssembleTurnsForceFullLast(t *testing.T) {
	hist := twoTurnHist()[1:] // 去掉 system：T1 + T2
	no := assembleTurns(hist, 1, 0, 0, nil, nil, false)
	// 期望：T1 简化态（问1 / 中间文本1 / 结论1 = 3 条） + T2 全量（5 条）
	if len(no) != 3+5 || no[0].Content != "问1" || no[2].Content != "结论1" {
		t.Fatalf("最新轮应恒全量（T1 简化态 3 条 + T2 全量 5 条）：got %d %+v", len(no), no)
	}
	ff := assembleTurns(hist, 1, 0, 0, nil, nil, true)
	if len(ff) != 3+5 || ff[0].Content != "问1" {
		t.Fatalf("forceFullLast 应保留最后一段全量：got %d %+v", len(ff), ff)
	}
	if ff[3].Role != "user" || ff[3].Content != "问2" {
		t.Fatalf("最后一段应从其用户提问起全量：%+v", ff[3])
	}
	if ff[6].Role != "tool" || len(ff[5].ToolCalls) != 1 {
		t.Fatalf("最后一段工具/结果应全量：%+v", ff[3:])
	}
	// 口径 W：两条件均不启用 → 不压缩 → 全部轮次原样全量。
	if all := assembleTurns(hist, 0, 0, 0, nil, nil, false); len(all) != len(hist) {
		t.Fatalf("N=M=0 不压缩应保留全量：got %d want %d", len(all), len(hist))
	}
}

// TestAssembleTurnsBoundaryRules：系统注入消息（Kind=notify/continue/resume）不计入轮边界；
// 用户真实提问（Kind=text，即便文本为"继续"）仍是新轮边界；无用户提问（纯 system/摘要）原样透传。
func TestAssembleTurnsBoundaryRules(t *testing.T) {
	// 纯 system（无用户提问）→ 原样
	sysOnly := []ChatMsg{{Role: "system", Content: "sys"}, {Role: "assistant", Content: "x"}}
	if out := assembleTurns(sysOnly, 0, 0, 0, nil, nil, false); len(out) != len(sysOnly) {
		t.Fatalf("无用户提问应原样透传：%+v", out)
	}
	// notify / 注入续写 属于当前 turn，不新开轮 → **单一轮**（组装侧下限 → 最新轮全量，整轮原文）
	hist := []ChatMsg{
		{Role: "user", Kind: "text", Content: "问1"},
		{Role: "user", Kind: "notify", Content: "[工具通知] n"},
		{Role: "user", Kind: assembleContinueKind, Content: assembleContinueText},
		{Role: "assistant", Content: "结论1"},
	}
	out := assembleTurns(hist, 0, 0, 0, nil, nil, false)
	if len(out) != len(hist) {
		t.Fatalf("notify/continue 不应作为轮边界（单轮全量）：%+v", out)
	}
	// 用户真实提问（Kind=text）文本恰为"继续" → 是轮边界：新轮全量、前轮简化态（N=1）
	real := []ChatMsg{
		{Role: "user", Kind: "text", Content: "问1"},
		{Role: "assistant", Content: "结论1"},
		{Role: "user", Kind: "text", Content: assembleContinueText},
		{Role: "assistant", Content: "结论2"},
	}
	ro := assembleTurns(real, 1, 0, 0, nil, nil, false)
	if len(ro) != 2+2 || ro[0].Content != "问1" || ro[1].Content != "结论1" ||
		ro[2].Content != assembleContinueText || ro[3].Content != "结论2" {
		t.Fatalf("用户真实'继续'应为新轮边界（前轮简化 text + 新轮全量）：%+v", ro)
	}
}

// TestAssembleTurnsKeepFullIncludesCurrent：keep_full_max_turns=N 表示"**含当前轮**的最近 N 轮全量"。
func TestAssembleTurnsKeepFullIncludesCurrent(t *testing.T) {
	hist := []ChatMsg{
		{Role: "user", Kind: "text", Content: "问1"}, {Role: "assistant", Content: "结论1"},
		{Role: "user", Kind: "text", Content: "问2"}, {Role: "assistant", Content: "结论2"},
		{Role: "user", Kind: "text", Content: "问3"}, {Role: "assistant", Content: "结论3"},
	}
	// N=2 → T2+T3 全量（4 条）+ T1 简化态（问1 / 结论1 = 2 条）
	out := assembleTurns(hist, 2, 0, 0, nil, nil, false)
	if len(out) != 2+4 || out[0].Content != "问1" ||
		out[2].Content != "问2" || out[5].Content != "结论3" {
		t.Fatalf("keep=2 应含当前轮共 2 轮全量：got %d %+v", len(out), out)
	}
	// N=1 → 仅当前轮 T3 全量（2 条）+ T1/T2 简化态（各 2 条）
	out1 := assembleTurns(hist, 1, 0, 0, nil, nil, false)
	if len(out1) != 4+2 || out1[0].Content != "问1" || out1[2].Content != "问2" || out1[4].Content != "问3" {
		t.Fatalf("keep=1 应仅当前轮全量：got %d %+v", len(out1), out1)
	}
}

// TestAssembleTurnsTokenBound（口径 X）：组装侧与压缩侧**同一边界**——完整区受 `keep_full_max_tokens`(M)
// 约束（自最新轮向前累计完整态 token）；更早轮 → **简化态（仅 text）**（去工具调用/结果）；
// N 足够大时以 M 为准；x=0（M 连最新轮都放不下）→ 下限仍保最新轮全量。
func TestAssembleTurnsTokenBound(t *testing.T) {
	big := strings.Repeat("x", 200) // 200 字符正文
	// 每轮 = 用户提问 + 带工具调用的中间文本 + 工具结果（完整态 > 简化态：简化态去掉工具调用与结果）。
	turn := func(q string) []ChatMsg {
		return []ChatMsg{
			{Role: "user", Kind: "text", Content: q},
			{Role: "assistant", Content: big, ToolCalls: []ToolCall{mkToolCall("c-"+q, "read", `{}`)}},
			{Role: "tool", ToolCallID: "c-" + q, Content: big},
		}
	}
	var hist []ChatMsg
	for _, q := range []string{"q1", "q2", "q3"} {
		hist = append(hist, turn(q)...)
	}
	// N=10（不构成约束）+ M=400（容不下 2 轮：单轮完整态 ≈ (2+200+6+200)/2 = 204）→ 末轮全量、
	// 前 2 轮简化态（各 2 条：用户提问 + 中间文本；工具调用/结果被去掉）。
	out := assembleTurns(hist, 10, 400, 0, nil, nil, false)
	if len(out) != 2+2+3 {
		t.Fatalf("超 M → 前 2 轮简化态、末轮全量：got %d %+v", len(out), out)
	}
	if out[0].Content != "q1" || out[1].Content != big || len(out[1].ToolCalls) != 0 {
		t.Fatalf("简化态 = 仅 text（去工具调用）：%+v", out[:2])
	}
	if out[4].Content != "q3" || len(out[5].ToolCalls) != 1 || out[6].Role != "tool" {
		t.Fatalf("完整区末轮应含工具调用与结果原文：%+v", out[4:])
	}
	// M 容得下全部 → 全量。
	if all := assembleTurns(hist, 10, 100000, 0, nil, nil, false); len(all) != len(hist) {
		t.Fatalf("M 未超 → 全量：got %d %+v", len(all), all)
	}
	// x=0：M 连最新轮都放不下 → 组装侧**下限**仍保最新轮全量（仅更早轮简化）。
	z := assembleTurns(hist, 10, 1, 0, nil, nil, false)
	if len(z) != 2+2+3 || z[4].Content != "q3" || len(z[5].ToolCalls) != 1 {
		t.Fatalf("x=0 → 组装侧仍保最新轮全量：got %d %+v", len(z), z)
	}
}

// TestAssembleTurnsThreeZones：三段齐备——完整区原文 / 简化区仅 text / 摘要区残留同按简化态投影；
// **预存 token 优先**（storedFull/storedBrief 尾部对齐覆盖实时估算）。
func TestAssembleTurnsThreeZones(t *testing.T) {
	hist := []ChatMsg{
		// T1：工具轮（简化态仅 q1 + 中间文本1）
		{Role: "user", Kind: "text", Content: "q1"},
		{Role: "assistant", Content: "中间文本1", ToolCalls: []ToolCall{mkToolCall("c1", "read", `{}`)}},
		{Role: "tool", ToolCallID: "c1", Content: "结果1"},
		// T2：普通轮（简化态 = 原文）
		{Role: "user", Kind: "text", Content: "q2"},
		{Role: "assistant", Content: "结论2"},
		// T3：完整区（最近 1 轮）
		{Role: "user", Kind: "text", Content: "q3"},
		{Role: "assistant", Content: "结论3"},
	}
	// 实时估算值过小，用**预存值**精确驱动三段：
	// fullTokens=[10,10,10]（M=15 → 完整区 = 末 1 轮）；briefTokens=[5,5,5]（T=5 → 简化区 = 1 轮）。
	storedFull := []int{10, 10, 10}
	storedBrief := []int{5, 5, 5}
	out := assembleTurns(hist, 10, 15, 5, storedFull, storedBrief, false)
	// 摘要区残留 = T1（简化态 2 条）；简化区 = T2（2 条）；完整区 = T3（2 条）。
	if len(out) != 2+2+2 {
		t.Fatalf("三段拼接条数：got %d %+v", len(out), out)
	}
	if len(out[1].ToolCalls) != 0 || out[2].Content != "q2" || out[5].Content != "结论3" {
		t.Fatalf("三段内容不符：%+v", out)
	}
}

// TestAssembleTurnsInjectedContinueSameTurn：恢复/续写注入的 user 消息（Kind=continue）
// 不计入新轮边界 → 仍属同一 turn；配合 forceFullLast 整轮全量（不因注入消息而误判为非完整区）。
func TestAssembleTurnsInjectedContinueSameTurn(t *testing.T) {
	// 一个被恢复/续写的 turn：原提问 + 原部分输出 + 注入续写 + 续写结论
	hist := []ChatMsg{
		{Role: "user", Kind: "text", Content: "问1"},
		{Role: "assistant", Content: "部分1"},
		{Role: "user", Kind: assembleContinueKind, Content: assembleContinueText},
		{Role: "assistant", Content: "结论1"},
	}
	// 注入消息带标记 → 仍是同一个 turn（最后一段）→ 最新轮全量（4 条）
	out := assembleTurns(hist, 0, 0, 0, nil, nil, true)
	if len(out) != len(hist) {
		t.Fatalf("注入续写消息不应新开轮（应整轮全量）：got %d %+v", len(out), out)
	}
	if out[1].Content != "部分1" || out[2].Content != assembleContinueText {
		t.Fatalf("整轮全量应保留中间输出与注入消息：%+v", out)
	}
	// 反向验证标记必要性：注入消息改为真实提问（Kind=text）→ 拆成 2 轮，forceFull 只作用于新轮，
	// 原轮退化为简化态（N=1）→ 成为 2 条 text（违反"保持该 turn 全量"）。
	bad := append([]ChatMsg{}, hist...)
	bad[2].Kind = "text"
	bo := assembleTurns(bad, 1, 0, 0, nil, nil, true)
	if len(bo) != 2+2 || bo[0].Content != "问1" || bo[1].Content != "部分1" {
		t.Fatalf("无标记时应拆轮（原轮简化态）：got %d %+v", len(bo), bo)
	}
}

// TestApplyReasoningRule：DeepSeek 协议回传规则——带 tool_calls 的 assistant 保留 reasoning，
// 不带 tool_calls 的 assistant（纯文本轮）与 tool/user/system 一律清空；且不改动入参。
func TestApplyReasoningRule(t *testing.T) {
	msgs := []ChatMsg{
		{Role: "system", Content: "sys"},
		{Role: "user", Kind: "text", Content: "q", Reasoning: "不应存在"},
		{Role: "assistant", Content: "中间", ToolCalls: []ToolCall{mkToolCall("c1", "read", "{}")}, Reasoning: "思考A"},
		{Role: "tool", ToolCallID: "c1", Content: "r", Reasoning: "不应存在"},
		{Role: "assistant", Content: "结论", Reasoning: "思考B"},
	}
	out := applyReasoningRule(msgs)
	if out[2].Reasoning != "思考A" {
		t.Fatalf("带 tool_calls 的 assistant 应回传 reasoning: %+v", out[2])
	}
	for i := range out {
		if i != 2 && out[i].Reasoning != "" {
			t.Fatalf("非工具调用消息不应回传 reasoning: out[%d]=%+v", i, out[i])
		}
	}
	// 入参不被修改
	if msgs[2].Reasoning != "思考A" || msgs[4].Reasoning != "思考B" {
		t.Fatalf("入参被篡改: %+v", msgs)
	}
}

// TestRouterMessagesReasoning：canonical 折算保留 reasoning（→ router 适配器再映射为
// 协议字段 `reasoning_content`；线格式断言归 router 白盒 `internal/adaptor/openai`）。
func TestRouterMessagesReasoning(t *testing.T) {
	msgs := []ChatMsg{
		{Role: "assistant", Content: "c", Reasoning: "R"},
		{Role: "user", Content: "u"},
	}
	w := routerMessages(msgs, nil)
	if w[0].Reasoning != "R" || w[1].Reasoning != "" {
		t.Fatalf("canonical reasoning 映射错: %+v", w)
	}
	if len(w[0].Content) != 1 || w[0].Content[0].Type != router.PartText || w[0].Content[0].Text != "c" {
		t.Fatalf("canonical 内容块错: %+v", w[0].Content)
	}
}

// TestUnpersistedInputsUserDedup：本次进入会话的 user 消息已前置落库（由 msgs() 从库带回），
// 不再重复叠加；history 中同内容消息不受影响（不按内容匹配）。
func TestUnpersistedInputsUserDedup(t *testing.T) {
	base := []ChatMsg{
		{Role: "user", Kind: "text", Content: "hi"}, // 本轮刚落库的 user（历史中亦有同内容）
		{Role: "user", Kind: "text", Content: "hi"}, // 更早历史里的同内容问题
		{Role: "tool", ToolCallID: "c1", Content: "r1"},
	}
	out := unpersistedInputs([]ChatMsg{
		{Role: "user", Kind: "text", Content: "hi"},
		{Role: "tool", ToolCallID: "c1", Content: "r1"}, // 已落库 → 剔除
		{Role: "tool", ToolCallID: "c2", Content: "r2"}, // 未落库 → 保留
	}, base)
	if len(out) != 1 || out[0].Role != "tool" || out[0].ToolCallID != "c2" {
		t.Fatalf("应只保留未落库的 tool 输入：%+v", out)
	}
}

// TestTurnUserMessageNotDuplicated（I-25 回归）：一次用户提问在 LLM 请求里只出现一次。
func TestTurnUserMessageNotDuplicated(t *testing.T) {
	rec := &llmRecorder{}
	llm := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-dedup", "t-dedup")

	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-dedup", "turn": "t-dedup",
		"type": "text-user", "content": "唯一提问",
	}))
	evs := collectTurn(t, s.bus, "t-dedup", 10*time.Second)
	if c := lastComplete(evs); c == nil || c["status"] != "complete" {
		t.Fatalf("no complete: %+v", evs)
	}
	reqs := rec.requests()
	if len(reqs) == 0 {
		t.Fatal("no llm request recorded")
	}
	last := reqs[len(reqs)-1]
	n := 0
	for _, m := range last {
		if m.Role == "user" && m.Content == "唯一提问" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("user 消息出现 %d 次，应为 1：%+v", n, last)
	}
}

// TestRecoverTurnCtxMarksForceFull：重启/死 turn 恢复重建的轮次标记 forceFull（该 turn 必须全量）。
func TestRecoverTurnCtxMarksForceFull(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc((&llmRecorder{}).handler))
	defer llm.Close()
	s := newTestServer(t, llm)
	tc := s.recoverTurnCtx("ins-test", "s-force", "t-force")
	if !tc.forceFull {
		t.Fatal("恢复重建的轮次应标记 forceFull")
	}
	if tc.keepFullTurns != defaultKeepFullTurns {
		t.Fatalf("维持轮数应回落默认 %d：got %d", defaultKeepFullTurns, tc.keepFullTurns)
	}
	tc.Close()
}

// TestRecoverTurnCtxCarriesSystemPromptAndDirs（WP2-4）：恢复轮与新建轮**同口径** —— 系统提示词层
// 注入 hist、WorkDir/DataDir 随 instance 绑定回填（gateway 上下文与记忆/资产指引据此生效）。
func TestRecoverTurnCtxCarriesSystemPromptAndDirs(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc((&llmRecorder{}).handler))
	defer llm.Close()
	s := newTestServer(t, llm)
	t.Cleanup(data.Reset)
	dataDir := t.TempDir()
	// 注册 instance（绑定 work_dir / data_dir，与 onLLMStart 同一事实源）
	s.bus.Emit(context.Background(), "instance-register", jb(map[string]any{
		"instance_id": "ins-test", "client_type": "unittest",
		"work_dir": testWorkDir, "data_dir": dataDir,
	})).Wait()
	time.Sleep(50 * time.Millisecond)

	tc := s.recoverTurnCtx("ins-test", "s-rec", "t-rec")
	defer tc.Close()
	if tc.req.WorkDir != testWorkDir {
		t.Fatalf("恢复轮 WorkDir=%q want %q（随 instance 绑定）", tc.req.WorkDir, testWorkDir)
	}
	if tc.req.DataDir != dataDir {
		t.Fatalf("恢复轮 DataDir=%q want %q（随 instance 绑定）", tc.req.DataDir, dataDir)
	}
	if len(tc.hist) == 0 || tc.hist[0].Role != "system" || strings.TrimSpace(tc.hist[0].Content) == "" {
		t.Fatalf("恢复轮 hist 应含非空系统提示词层，got %+v", tc.hist)
	}
}

// TestLoadKeepFullBoundsValueSemantics（P1 口径 W，组装侧读点）：键**存在且为数字** → 原样采用
// （`0` = 该条件不启用、负数 = 非法按不启用）；**缺失/非数字** → 回落默认（10 / 24000）。
// **与压缩侧 `resolveOpts` 同口径**（差异即"两侧判定不一致"，故单列断言）。
func TestLoadKeepFullBoundsValueSemantics(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc((&llmRecorder{}).handler))
	defer llm.Close()
	s := newTestServer(t, llm)
	// 登记实例（data.Prj / 门面配置读同一份事实）→ 之后才能写 prj 配置。
	data.Register("ins-test", testWorkDir, "")
	t.Cleanup(data.Reset)
	prj, releasePrj, err := data.Prj("ins-test")
	if err != nil {
		t.Fatalf("data.Prj: %v", err)
	}
	defer releasePrj() // 短开（D-45）：用完即释
	// 缺失 → 默认。
	if got := s.loadKeepFullTurns("ins-test"); got != defaultKeepFullTurns {
		t.Fatalf("缺失 keep_full_max_turns → 默认 %d：got %d", defaultKeepFullTurns, got)
	}
	if got := s.loadKeepFullTokens("ins-test"); got != defaultKeepFullTokens {
		t.Fatalf("缺失 keep_full_max_tokens → 默认 %d：got %d", defaultKeepFullTokens, got)
	}
	// 显式 0 → 原样（该条件不启用，不再回落默认）。
	if err := data.SetConfig(prj, "keep_full_max_turns", "0"); err != nil {
		t.Fatal(err)
	}
	if err := data.SetConfig(prj, "keep_full_max_tokens", "0"); err != nil {
		t.Fatal(err)
	}
	if got := s.loadKeepFullTurns("ins-test"); got != 0 {
		t.Fatalf("显式 0 应原样采用：got %d", got)
	}
	if got := s.loadKeepFullTokens("ins-test"); got != 0 {
		t.Fatalf("显式 0 应原样采用：got %d", got)
	}
	// 负数（非法）→ 原样采用（按不启用处理）。
	if err := data.SetConfig(prj, "keep_full_max_turns", "-3"); err != nil {
		t.Fatal(err)
	}
	if err := data.SetConfig(prj, "keep_full_max_tokens", "-9"); err != nil {
		t.Fatal(err)
	}
	if got := s.loadKeepFullTurns("ins-test"); got != -3 {
		t.Fatalf("负数应原样采用（按不启用）：got %d", got)
	}
	if got := s.loadKeepFullTokens("ins-test"); got != -9 {
		t.Fatalf("负数应原样采用（按不启用）：got %d", got)
	}
	// 非数字 → 回落默认。
	if err := data.SetConfig(prj, "keep_full_max_turns", "abc"); err != nil {
		t.Fatal(err)
	}
	if err := data.SetConfig(prj, "keep_full_max_tokens", "x1"); err != nil {
		t.Fatal(err)
	}
	if got := s.loadKeepFullTurns("ins-test"); got != defaultKeepFullTurns {
		t.Fatalf("非数字 → 默认 %d：got %d", defaultKeepFullTurns, got)
	}
	if got := s.loadKeepFullTokens("ins-test"); got != defaultKeepFullTokens {
		t.Fatalf("非数字 → 默认 %d：got %d", defaultKeepFullTokens, got)
	}
}
