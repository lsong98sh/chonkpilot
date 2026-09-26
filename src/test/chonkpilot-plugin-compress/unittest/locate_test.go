// compress 三段边界定位黑盒外部测试（原模块内 estimateTokens 已导出为 EstimateTokens）。
//
// 2026-09-21（41 G-33「类型双轨收敛」）：纯函数入参 = 门面 DTO（`facade.Message`），
// 用例随同以**领域形状**表达（工具调用 = 平铺 name/arguments，不是协议嵌套 function{}）。
//
// 2026-09-25（口径 X）：两层 → **三段**（完整 / 简化 / 摘要）；T 的判定输入 = **简化区 brief token**
// （推翻上一批「用 full_tokens」）。用例随之重写。
package compress_test

import (
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-plugin-compress"
)

// helper：快速构造消息（命名避开测试参数 t / 包函数冲突）。
func um(content string) facade.Message {
	return facade.Message{Role: "user", Kind: "text", Content: content}
}
func nm(content string) facade.Message {
	return facade.Message{Role: "user", Kind: "notify", Content: content}
}
func am(content string) facade.Message { return facade.Message{Role: "assistant", Content: content} }
func tc(name string) facade.Message {
	return facade.Message{Role: "assistant", ToolCalls: []facade.ToolCall{
		{ID: "c1", Type: "function", Name: name, Arguments: "{}"},
	}}
}

// x100 = 100 字符正文 → 单条 50 token → 单轮（u+a）100 token（无工具/思维 → 简化态 = 完整态）。
func x100() string { return strings.Repeat("x", 100) }

// TestLocateThreeZonesWholeTurnAlign（口径 X / D1 用例 ⑥）：边界落在 turn 起点（工具对不切断）；
// **简化区 / 摘要区在轮维度切分**（prelude 不计入摘要区）。
func TestLocateThreeZonesWholeTurnAlign(t *testing.T) {
	msgs := []facade.Message{um("q1"), am("a1"), um("q2"), am("a2"), tc("file_read"), um("q3"), am("a3")}
	// N=2、M=0（token 条件不启用）、T=0（简化区预算 0）→ 完整区 = 末 2 轮（bounds[1]=2）；
	// 简化区空；摘要区 = turn1（[0,2)）。
	z := compress.LocateThreeZones(msgs, 2, 0, 0, nil, nil)
	if z.FullStart != 2 || z.BriefStart != 2 || z.SummaryStart != 0 {
		t.Fatalf("zones=%+v, want {2 2 0}", z)
	}
	if msgs[z.FullStart].Role != "user" || msgs[z.FullStart].Content != "q2" {
		t.Fatalf("完整区边界须落在 turn 起点（用户发起消息）：%+v", msgs[z.FullStart])
	}
}

// TestLocateThreeZonesExcludeInjected：kind=notify/continue/resume 不计入轮边界
// （口径与组装侧 isTurnBoundary 一致：只认标记、不认内容）。
func TestLocateThreeZonesExcludeInjected(t *testing.T) {
	cm := facade.Message{Role: "user", Kind: "continue", Content: "继续"}
	rm := facade.Message{Role: "user", Kind: "resume", Content: "恢复"}
	msgs := []facade.Message{um("q1"), am("a1"), nm("任务完成"), cm, rm, um("q2"), am("a2")}
	z := compress.LocateThreeZones(msgs, 1, 0, 0, nil, nil)
	// 轮边界 = [0, 5] 两轮；完整区 = 末 1 轮（bounds[1]=5）；摘要区 = turn1（[0,5)）。
	if z.FullStart != 5 || z.SummaryStart != 0 || z.BriefStart != 5 {
		t.Fatalf("zones=%+v, want {5 5 0}", z)
	}
	// "继续"文本的真实提问（Kind=text）仍计入轮边界。
	msgs2 := []facade.Message{um("q1"), am("a1"), um("继续"), am("a2")}
	if z2 := compress.LocateThreeZones(msgs2, 1, 0, 0, nil, nil); z2.FullStart != 2 {
		t.Fatalf("真实提问应计入轮边界：zones=%+v", z2)
	}
}

// TestLocateThreeZonesBriefBudget（口径 X）：**T 是简化区 brief token 预算**——
// 自完整区前一轮向前逐轮累加 brief token，累计 <= T 的轮进简化区，其余更早轮进摘要区。
func TestLocateThreeZonesBriefBudget(t *testing.T) {
	msgs := seed(6, x100()) // 每轮 100 token（完整态 = 简化态）
	// N=2 → 完整区 = 末 2 轮（bounds[4]=8）；T=0 → 简化区空 → 摘要区 = turn1..4。
	if z := compress.LocateThreeZones(msgs, 2, 0, 0, nil, nil); z.FullStart != 8 || z.BriefStart != 8 || z.SummaryStart != 0 {
		t.Fatalf("T=0：zones=%+v, want {8 8 0}", z)
	}
	// T=100 → 简化区 = turn4（bounds[3]=6）。
	if z := compress.LocateThreeZones(msgs, 2, 0, 100, nil, nil); z.FullStart != 8 || z.BriefStart != 6 {
		t.Fatalf("T=100：zones=%+v, want FullStart=8 BriefStart=6", z)
	}
	// T=250 → 简化区 = turn3+turn4（bounds[2]=4）。
	if z := compress.LocateThreeZones(msgs, 2, 0, 250, nil, nil); z.FullStart != 8 || z.BriefStart != 4 {
		t.Fatalf("T=250：zones=%+v, want FullStart=8 BriefStart=4", z)
	}
	// T 足够大 → 简化区吞掉全部更早轮 → 无摘要区（SummaryStart == BriefStart）。
	z := compress.LocateThreeZones(msgs, 2, 0, 100000, nil, nil)
	if z.SummaryStart != z.BriefStart {
		t.Fatalf("预算充足应无摘要区：zones=%+v", z)
	}
}

// TestLocateThreeZonesExampleNumeric（用户口径例子复算）：8 轮（本轮 = turn8），
// 完整区 = 本轮+turn7+turn6 = 3350 · 简化区 = turn5+turn4 = 1600 · 摘要区 = turn3..turn1。
func TestLocateThreeZonesExampleNumeric(t *testing.T) {
	full := []int{500, 500, 500, 800, 800, 800, 800, 1750} // turn1..turn8
	brief := []int{500, 500, 500, 800, 800, 800, 800, 1750}
	z := data.LocateZones(full, brief, 10, 4000, 1600)
	if z.FullTurns != 3 || z.BriefTurns != 2 {
		t.Fatalf("zones=%+v, want {3 2}", z)
	}
	sumFull, sumBrief := 0, 0
	for i := 8 - z.FullTurns; i < 8; i++ {
		sumFull += full[i]
	}
	for i := 8 - z.FullTurns - z.BriefTurns; i < 8-z.FullTurns; i++ {
		sumBrief += brief[i]
	}
	if sumFull != 3350 || sumBrief != 1600 {
		t.Fatalf("完整区=%d 简化区=%d, want 3350 / 1600", sumFull, sumBrief)
	}
	// 同一数值经**快照侧**入口（存量为预存数组）：seed(8) 每轮 2 条消息 → bounds[i]=2i。
	snap := seed(8, x100())
	zz := compress.LocateThreeZones(snap, 10, 4000, 1600, full, brief)
	if zz.FullStart != 10 || zz.BriefStart != 6 || zz.SummaryStart != 0 {
		t.Fatalf("快照侧 zones=%+v, want {10 6 0}", zz)
	}
}

// TestLocateThreeZonesStoredBrief（P3/P4）：简化区预算 T **累加预存 brief_tokens**（不重算）——
// 同一批轮、同一 T，预存 brief 变小 → 简化区纳入更多轮 → 摘要区消失（不压缩）。
func TestLocateThreeZonesStoredBrief(t *testing.T) {
	msgs := seed(6, x100())
	// 基准（无预存）：T=250 → 简化区 = turn3+turn4（BriefStart=4），摘要区 = turn1+turn2（有可压内容）。
	base := compress.LocateThreeZones(msgs, 2, 0, 250, nil, nil)
	if base.BriefStart != 4 || base.SummaryStart >= base.BriefStart {
		t.Fatalf("基准应有摘要区：zones=%+v", base)
	}
	// 预存 brief 全为 10 → 4 轮共 40 <= 250 → 简化区 = 全部更早轮 → **无摘要区**。
	storedBrief := []int{10, 10, 10, 10, 10, 10}
	z := compress.LocateThreeZones(msgs, 2, 0, 250, nil, storedBrief)
	if z.SummaryStart != z.BriefStart {
		t.Fatalf("预存 brief 参与预算：应无摘要区，zones=%+v", z)
	}
}

// TestLocateThreeZonesStoredLongerTailAlign（P3 尾部对齐）：预存数组**比快照轮数长**
// （全会话 turn_tokens 而快照仅留尾部）→ 取尾部对齐；头对齐会得错误边界（本用例可证伪）。
func TestLocateThreeZonesStoredLongerTailAlign(t *testing.T) {
	msgs := seed(4, x100()) // 4 轮
	// 8 项预存（含更早 4 轮的历史值 9）；尾部 4 项 = 本快照的 4 轮。
	storedFull := []int{9, 9, 9, 9, 100, 100, 100, 100}
	storedBrief := []int{9, 9, 9, 9, 10, 10, 10, 10}
	z := compress.LocateThreeZones(msgs, 10, 150, 20, storedFull, storedBrief)
	// 尾部对齐：M=150 只容 1 轮（100）→ 完整区 = 末轮（bounds[3]=6）；T=20 容 2 轮（10+10）
	// → 简化区 = turn2+turn3（bounds[1]=2）；摘要区 = turn1。
	if z.FullStart != 6 || z.BriefStart != 2 || z.SummaryStart != 0 {
		t.Fatalf("尾部对齐 zones=%+v, want {6 2 0}", z)
	}
}

// TestLocateThreeZonesNoCompression（口径 W）：两条件均不启用 / 轮数不足 / 无轮边界 → 无摘要区。
func TestLocateThreeZonesNoCompression(t *testing.T) {
	msgs := seed(4, x100())
	// N=0 且 M=0 → 不压缩（完整区 = 全量）。
	if z := compress.LocateThreeZones(msgs, 0, 0, 100, nil, nil); z.SummaryStart != z.BriefStart {
		t.Fatalf("两条件均不启用 → 不压缩：zones=%+v", z)
	}
	// 轮数 <= N → 完整区 = 全量。
	if z := compress.LocateThreeZones(msgs, 9, 0, 100, nil, nil); z.SummaryStart != z.BriefStart {
		t.Fatalf("轮数不足 → 不压缩：zones=%+v", z)
	}
	// 空输入 / 无轮边界（仅 system）。
	if z := compress.LocateThreeZones(nil, 2, 0, 0, nil, nil); z.FullStart != 0 || z.BriefStart != 0 {
		t.Fatalf("空输入：zones=%+v", z)
	}
	sysOnly := []facade.Message{{Role: "system", Content: "摘要"}, am("x")}
	if z := compress.LocateThreeZones(sysOnly, 2, 0, 0, nil, nil); z.SummaryStart != z.BriefStart {
		t.Fatalf("无轮边界 → 不压缩：zones=%+v", z)
	}
}

// TestLocateThreeZonesIllegalNegative（口径 W）：N<0 / M<0 = 非法 → 后端**不猜**、按「不启用」处理。
func TestLocateThreeZonesIllegalNegative(t *testing.T) {
	msgs := seed(6, x100())
	if z := compress.LocateThreeZones(msgs, -1, 0, 0, nil, nil); z.SummaryStart != z.BriefStart {
		t.Fatalf("N<0（按不启用）且 M==0 → 不压缩：zones=%+v", z)
	}
	// M<0（按不启用）且 N=2 → 只用轮次条件 → 完整区 = 末 2 轮。
	if z := compress.LocateThreeZones(msgs, 2, -5, 0, nil, nil); z.FullStart != 8 {
		t.Fatalf("M<0 按不启用、N=2 → 完整区 = 末 2 轮：zones=%+v", z)
	}
	// N<0（按不启用）且 M=150 → 只用 token 条件 → 完整区 = 末 1 轮。
	if z := compress.LocateThreeZones(msgs, -3, 150, 0, nil, nil); z.FullStart != 10 {
		t.Fatalf("N<0 按不启用、M=150 → 完整区 = 末 1 轮：zones=%+v", z)
	}
}

// TestEstimateTokens：字符数/2 估算。
func TestEstimateTokens(t *testing.T) {
	msgs := []facade.Message{um("你好"), am("世界")} // 4 字符 → 2 token
	if got := compress.EstimateTokens(msgs); got != 2 {
		t.Fatalf("EstimateTokens=%d, want 2", got)
	}
	// 含工具调用：name=file_read(9) + args={}(2) = 11 → +5
	msgs = append(msgs, tc("file_read"))
	if got := compress.EstimateTokens(msgs); got != 2+5 {
		t.Fatalf("EstimateTokens=%d, want %d", got, 2+5)
	}
}

// TestBriefFacadeMessages（口径 X ②）：简化态构造**仅保留 text**——去 reasoning / tool_call /
// tool_result，纯工具调用轮（无正文）整条丢弃；且不修改入参。
func TestBriefFacadeMessages(t *testing.T) {
	in := []facade.Message{
		um("问"),
		{Role: "assistant", Content: "中间文本", Reasoning: "想A"},
		tc("file_read"), // 纯工具调用（无正文）→ 丢
		{Role: "tool", ToolCallID: "c1", Content: "工具结果"}, // → 丢
		{Role: "assistant", Content: "结论", Reasoning: "想B"},
	}
	out := data.BriefFacadeMessages(in)
	if len(out) != 3 {
		t.Fatalf("简化态条数=%d, want 3（问/中间文本/结论）: %+v", len(out), out)
	}
	if out[0].Content != "问" || out[1].Content != "中间文本" || out[2].Content != "结论" {
		t.Fatalf("简化态应仅保留 text：%+v", out)
	}
	for i, m := range out {
		if m.Reasoning != "" || len(m.ToolCalls) != 0 {
			t.Fatalf("简化态应去 reasoning/tool_calls：out[%d]=%+v", i, m)
		}
	}
	// 入参不被篡改（去字段只作用于副本）。
	if in[1].Reasoning != "想A" || in[4].Reasoning != "想B" || len(in[2].ToolCalls) != 1 {
		t.Fatalf("入参被篡改：%+v", in)
	}
}

// eqInts 断言两 int 切片逐元素相等（测试内部 helper）。
func eqInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestResolveStoredTokensFallback（P3 缺值回退 + 尾部对齐双向）：预存值优先、缺值（缺位 / 0）
// 回退实时估算，**绝不把缺值当 0**；stored 短 → 尾部对齐（前若干位无预存）；stored **长** →
// 取尾段对齐（消费方持有全会话轮而估算仅尾部子集）。
func TestResolveStoredTokensFallback(t *testing.T) {
	est := []int{10, 20, 30}
	if got := data.ResolveStoredTokens(nil, est); !eqInts(got, est) {
		t.Fatalf("无预存值 → 原样估算：got %v want %v", got, est)
	}
	// stored 短（只覆盖最近 1 轮）：尾部对齐。
	if got := data.ResolveStoredTokens([]int{77}, est); !eqInts(got, []int{10, 20, 77}) {
		t.Fatalf("stored 短尾部对齐：got %v want [10 20 77]", got)
	}
	// 中间缺值（0 = 缺值）→ 回退估算（不当 0）。
	if got := data.ResolveStoredTokens([]int{11, 0, 33}, est); !eqInts(got, []int{11, 20, 33}) {
		t.Fatalf("缺值回退估算：got %v want [11 20 33]", got)
	}
	// stored **长**（含更早历史）→ 取尾段对齐（跳过头部）。
	if got := data.ResolveStoredTokens([]int{1, 2, 11, 22, 33}, est); !eqInts(got, []int{11, 22, 33}) {
		t.Fatalf("stored 长尾部对齐：got %v want [11 22 33]", got)
	}
	// stored 与实时估算**数值一致** → 结果与纯估算完全相同（口径一致验证）。
	if got := data.ResolveStoredTokens(est, est); !eqInts(got, est) {
		t.Fatalf("预存=估算 → 结果一致：got %v want %v", got, est)
	}
}
