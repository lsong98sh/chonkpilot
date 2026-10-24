// SESSION-1 阶段一：组装结果 `Assembly` 的单测（无 MQ 驱动 —— 纯内存组装 + 真实 data 门面）。
package server

import (
	"reflect"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/lsong98sh/chonkpilot-session"
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

// TestAssemblyLastTurnIDAndZones：`LastTurnID` = 最后一个**已完成**轮；`Zones` 用 turn id 表达
// （完整区 = 最近 2 轮、简化区 = t1）；Messages 与投影管线（session.Project + reasoning 规则）逐字节一致。
func TestAssemblyLastTurnIDAndZones(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	hist := histThree()
	tc := &turnCtx{
		server:        s,
		req:           StartReq{InstanceID: "ins-test", Session: "s-asm", Turn: "t-cur"},
		hist:          hist,
		turnTokens:    []facade.TurnToken{{TurnID: "t1"}, {TurnID: "t2"}, {TurnID: "t3"}},
		keepFullTurns: 2,
		briefBudget:   10000,
	}
	a := tc.assembly()

	if a.LastTurnID != "t3" {
		t.Fatalf("LastTurnID = %q, want t3（最后一个被纳入的已完成轮，不含当前轮）", a.LastTurnID)
	}
	if a.Zones.FullFrom != "t2" {
		t.Fatalf("Zones.FullFrom = %q, want t2（keep_full_max_turns=2）", a.Zones.FullFrom)
	}
	if a.Zones.BriefFrom != "t1" {
		t.Fatalf("Zones.BriefFrom = %q, want t1（brief 预算足够 → 简化区含 t1）", a.Zones.BriefFrom)
	}
	if a.Zones.LastTurnID != a.LastTurnID {
		t.Fatalf("Zones.LastTurnID = %q, want %q", a.Zones.LastTurnID, a.LastTurnID)
	}
	// 与改动前管线逐字节一致（system 仍在 Messages 首位、指引顺序 / reasoning 规则不变）。
	projMsgs, _ := session.Project(hist, 2, 0, 10000, nil, nil, false)
	want := session.ApplyReasoningRule(projMsgs)
	if !reflect.DeepEqual(a.Context.Messages, want) {
		t.Fatalf("Assembly.Context.Messages 与改动前管线不一致:\n got=%+v\nwant=%+v",
			a.Context.Messages, want)
	}
	if a.Usage.History <= 0 {
		t.Fatalf("Usage.History 应 > 0: %+v", a.Usage)
	}
	if a.Usage.Total != a.Usage.Fixed+a.Usage.History {
		t.Fatalf("Usage.Total 口径不符: %+v", a.Usage)
	}
}

// TestAssemblyZonesDisabled：N/M 两条件均不启用 → 不压缩（完整区 = 全量、无简化区）。
func TestAssemblyZonesDisabled(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	tc := &turnCtx{
		server:         s,
		req:            StartReq{InstanceID: "ins-test", Session: "s-asm2", Turn: "t-cur"},
		hist:           histThree(),
		turnTokens:     []facade.TurnToken{{TurnID: "t1"}, {TurnID: "t2"}, {TurnID: "t3"}},
		keepFullTurns:  0,
		keepFullTokens: 0,
		briefBudget:    10000,
	}
	a := tc.assembly()
	if a.Zones.FullFrom != "t1" {
		t.Fatalf("两条件均不启用 → 完整区应为全量（FullFrom=t1），got %q", a.Zones.FullFrom)
	}
	if a.Zones.BriefFrom != "" {
		t.Fatalf("两条件均不启用 → 无简化区，got BriefFrom=%q", a.Zones.BriefFrom)
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
		keepFullTurns:  1,
		briefBudget:    10000,
		sysPromptText:  "你是测试智能体，一个全能智能体。你运行在测试环境中。",
		memGuideText:   "【记忆】按需读取记忆目录下的相关记忆。",
		assetGuideText: "【知识库资产】用 mcp_find 检索、mcp_load 加载。",
	}
	tc.guideOnce.Do(func() {}) // 固定已注入的两条指引（不再真实读取配置）

	a := tc.assembly()
	wantFixed := data.EstimateTokens(tc.sysPromptText) +
		data.EstimateTokens(tc.memGuideText) + data.EstimateTokens(tc.assetGuideText) +
		toolsTokenEstimate(tc.llmTools())
	if a.Usage.Fixed != wantFixed {
		t.Fatalf("Usage.Fixed = %d, want %d（system + 两条指引 + 工具 schema）", a.Usage.Fixed, wantFixed)
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

// TestToolsTokenEstimate：工具 schema（名 + 描述 + 参数 JSON）计入固定开销。
func TestToolsTokenEstimate(t *testing.T) {
	if got := toolsTokenEstimate(nil); got != 0 {
		t.Fatalf("无工具 → 0，got %d", got)
	}
	one := toolsTokenEstimate([]ToolDef{{
		Name:        "core_file_read",
		Description: "读取文件内容（按路径）",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
	}})
	if one <= 0 {
		t.Fatalf("单个工具 schema 估算应 > 0，got %d", one)
	}
	two := toolsTokenEstimate([]ToolDef{
		{Name: "a", Description: strings.Repeat("描", 40)},
		{Name: "b", Description: strings.Repeat("述", 40)},
	})
	if two <= one {
		t.Fatalf("两个工具估算应大于一个: one=%d two=%d", one, two)
	}
}
