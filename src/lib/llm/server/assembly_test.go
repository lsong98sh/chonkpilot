// SESSION-1 阶段一：组装结果 `Assembly` 的单测（无 MQ 驱动 —— 纯内存组装 + 真实 data 门面）。
package server

import (
	"reflect"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/lsong98sh/chonkpilot-agent/session"
)

// histThree 三轮历史（每轮 = user + assistant；用于三段定位）。
func histThree() []ChatMsg {
	return []ChatMsg{
		{Role: "user", Kind: "text", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Kind: "text", Content: "q2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Kind: "text", Content: "q3"},
		{Role: "assistant", Content: "a3"},
	}
}

// TestAssemblyFullUnderTrigger：未达触发阈值 → 2.1 全量（历史全部原文，不简化）；`LastTurnID`
// = 最后一个已完成轮；Zones 全量（FullFrom = 首个历史轮，无简化区）。
func TestAssemblyFullUnderTrigger(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	hist := histThree()
	tc := &turnCtx{
		server:     s,
		req:        StartReq{InstanceID: "ins-test", Session: "s-asm", Turn: "t-cur"},
		hist:       hist,
		turnTokens: []facade.TurnToken{{TurnID: "t1"}, {TurnID: "t2"}, {TurnID: "t3"}},
	}
	a := tc.assembly()

	if a.LastTurnID != "t3" {
		t.Fatalf("LastTurnID = %q, want t3（最后一个被纳入的已完成轮，不含当前轮）", a.LastTurnID)
	}
	if a.Zones.FullFrom != "t1" {
		t.Fatalf("Zones.FullFrom = %q, want t1（全量）", a.Zones.FullFrom)
	}
	if a.Zones.BriefFrom != "" {
		t.Fatalf("未达阈值 → 无简化区，got BriefFrom=%q", a.Zones.BriefFrom)
	}
	if a.Zones.LastTurnID != a.LastTurnID {
		t.Fatalf("Zones.LastTurnID = %q, want %q", a.Zones.LastTurnID, a.LastTurnID)
	}
	// 全量 = 原文（仅 reasoning 规则：无 tool_calls 的 assistant 清 reasoning）。
	want := session.ApplyReasoningRule(hist)
	if !reflect.DeepEqual(a.Context.Messages, want) {
		t.Fatalf("全量投影应逐条保留历史原文:\n got=%+v\nwant=%+v",
			a.Context.Messages, want)
	}
	if a.Usage.History <= 0 {
		t.Fatalf("Usage.History 应 > 0: %+v", a.Usage)
	}
	if a.Usage.Total != a.Usage.Fixed+a.Usage.History+a.Usage.Current {
		t.Fatalf("Usage.Total 口径不符（应为 Fixed+History+Current）: %+v", a.Usage)
	}
}

// TestAssemblyCurrentTurnCounted：`Usage.Current` 计入本轮已落库消息，`Total` 为本次请求总输入。
func TestAssemblyCurrentTurnCounted(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	tc := &turnCtx{
		server:     s,
		req:        StartReq{InstanceID: "ins-test", Session: "s-asm2", Turn: "t-cur"},
		hist:       histThree(),
		turnTokens: []facade.TurnToken{{TurnID: "t1"}, {TurnID: "t2"}, {TurnID: "t3"}},
	}
	a := tc.assembly()
	if a.Usage.Current != 0 {
		t.Fatalf("无当前轮消息 → Current 应为 0: %+v", a.Usage)
	}
	if a.LastTurnID != "t3" {
		t.Fatalf("LastTurnID = %q, want t3", a.LastTurnID)
	}
}

// TestAssemblyUsageFixed：Fixed = 系统提示词 + 两条指引 + 工具 schema（口径 = data.EstimateTokens*）；
// 指引顺序仍为 [资产, 记忆, ...]（先 prepend 记忆、再 prepend 资产）。
func TestAssemblyUsageFixed(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	tc := &turnCtx{
		server:         s,
		req:            StartReq{InstanceID: "ins-test", Session: "s-asm3", Turn: "t-cur"},
		hist:           histThree(),
		turnTokens:     []facade.TurnToken{{TurnID: "t1"}, {TurnID: "t2"}, {TurnID: "t3"}},
		sysPromptText:  "你是测试智能体，一个全能智能体。你运行在测试环境中。",
		memGuideText:   "【记忆】按需读取记忆目录下的相关记忆。",
		assetGuideText: "【知识库资产】用 mcp_find 检索、mcp_load 加载。",
	}
	tc.guideOnce.Do(func() {}) // 固定已注入的两条指引（不再真实读取配置）

	a := tc.assembly()
	wantFixed := data.EstimateTokens(tc.sysPromptText) +
		data.EstimateTokens(tc.memGuideText) + data.EstimateTokens(tc.assetGuideText) +
		session.ToolsTokenEstimate(toolSchemas(tc.llmTools()))
	if a.Usage.Fixed != wantFixed {
		t.Fatalf("Usage.Fixed = %d, want %d（system + 两条指引 + 工具 schema）", a.Usage.Fixed, wantFixed)
	}
	// 前导 system 与 system prompt 逐字相同 → 不重复计入 History（见 compose.Build）。
	wantHist := session.MessageTokens(histThree())
	if a.Usage.History != wantHist {
		t.Fatalf("Usage.History = %d, want %d", a.Usage.History, wantHist)
	}
	if a.Usage.Total != wantFixed+a.Usage.History+a.Usage.Current {
		t.Fatalf("Usage.Total 口径不符: %+v", a.Usage)
	}
	if a.Usage.Fixed <= 0 {
		t.Fatalf("Usage.Fixed 应 > 0: %+v", a.Usage)
	}
	msgs := a.Context.Messages
	if len(msgs) < 2 || msgs[0].Role != "system" || msgs[0].Content != tc.assetGuideText {
		t.Fatalf("Messages 首位应为资产指引: %+v", msgs[:min(2, len(msgs))])
	}
	if msgs[1].Role != "system" || msgs[1].Content != tc.memGuideText {
		t.Fatalf("Messages 次位应为记忆指引: %+v", msgs[:min(2, len(msgs))])
	}
}
