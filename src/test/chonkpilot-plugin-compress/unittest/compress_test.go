// compress 插件黑盒外部测试（独立测试模块 chonkpilot-test/chonkpilot-plugin-compress/unittest；
// 原模块内 doCompress 已导出为 DoCompress 供外部测试）。
//
// 2026-09-21（41 G-33「类型双轨收敛」）：插件纯函数入参/出参 = 门面 DTO
// （`facade.Snapshot` / `facade.Message`）——用例随同以领域形状表达（不再经 `data.ChatMsg`
// 往返）；MQ 面报文键名未变另有断言（`src/test/chonkpilot-data/unittest/persist`）。
//
// 2026-09-25（口径 X/Y）：两层 → **三段**（完整 / 简化 / 摘要）+ **兜底归并**；T = 简化区 brief 预算。
package compress_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-plugin"
	"github.com/chonkpilot/chonkpilot-plugin-compress"
)

// seed 生成 n 轮（每轮 user + assistant 一对）历史；content 长度可调（影响估算 token）。
func seed(n int, content string) []facade.Message {
	var out []facade.Message
	for i := 0; i < n; i++ {
		out = append(out,
			facade.Message{Role: "user", Kind: "text", Content: content},
			facade.Message{Role: "assistant", Content: content},
		)
	}
	return out
}

func bigContent() string { return strings.Repeat("很长的历史内容", 40) }

// TestDoCompressKeepsLastTurns（口径 X）：完整区 = 末 N 轮（原文）+ 摘要（摘要区压缩）；
// 简化区（T=50 容不下单轮 brief=280）为空。
func TestDoCompressKeepsLastTurns(t *testing.T) {
	opts := compress.Options{RetainTurns: 2, TokenMax: 50}
	snap := facade.Snapshot{Messages: seed(6, bigContent()), Turn: "t12"}
	got, changed := compress.DoCompress(snap, opts, func(c string) (string, error) { return "SUMMARY", nil }, nil)
	if !changed {
		t.Fatal("应触发压缩")
	}
	if len(got.Messages) >= len(snap.Messages) {
		t.Fatalf("压缩后应更短: %d -> %d", len(snap.Messages), len(got.Messages))
	}
	if got.Messages[0].Role != "system" || !strings.Contains(got.Messages[0].Content, "SUMMARY") {
		t.Fatalf("首条应为摘要: %+v", got.Messages[0])
	}
	if got.Turn != "t12" {
		t.Fatalf("snapshot_turn 应不变: %s", got.Turn)
	}
	// 完整区 = 末 2 轮 = 4 条消息 + 摘要 = 5
	if len(got.Messages) != 5 {
		t.Fatalf("完整区末 2 轮（4 条）+ 摘要 = 5 条，实际 %d", len(got.Messages))
	}
}

func TestDoCompressNotEnoughTurns(t *testing.T) {
	opts := compress.Options{RetainTurns: 2, TokenMax: 50}
	// 只有 1 轮 → 完整区=全量，无摘要区 → 不可压
	snap := facade.Snapshot{Messages: seed(1, bigContent()), Turn: "t2"}
	if _, changed := compress.DoCompress(snap, opts, func(c string) (string, error) { return "X", nil }, nil); changed {
		t.Fatal("不足 N 轮不应压缩")
	}
}

// TestDoCompressBriefBudgetAbsorbsAll：T 足够大 → 简化区吞掉全部更早轮 → 无摘要区 → 不压缩。
func TestDoCompressBriefBudgetAbsorbsAll(t *testing.T) {
	opts := compress.Options{RetainTurns: 2, TokenMax: 1_000_000}
	snap := facade.Snapshot{Messages: seed(6, bigContent()), Turn: "t12"}
	if _, changed := compress.DoCompress(snap, opts, func(c string) (string, error) { return "X", nil }, nil); changed {
		t.Fatal("简化区预算吞掉全部更早轮 → 无摘要区 → 不应压缩")
	}
}

// TestDoCompressOverMaxTurns（D1 用例 ①）：超 `keep_full_max_turns` 轮 → 完整区 = 最近 N 轮；
// 简化区空（T=1）→ 摘要区 = 更早轮 → 压缩。
func TestDoCompressOverMaxTurns(t *testing.T) {
	opts := compress.Options{RetainTurns: 2, KeepFullTokens: 1_000_000, TokenMax: 1}
	snap := facade.Snapshot{Messages: seed(6, bigContent()), Turn: "t12"}
	got, changed := compress.DoCompress(snap, opts, func(c string) (string, error) { return "SUMMARY", nil }, nil)
	if !changed {
		t.Fatal("超 N 轮应触发压缩")
	}
	if len(got.Messages) != 5 {
		t.Fatalf("完整区末 2 轮（4 条）+ 摘要 = 5 条，实际 %d", len(got.Messages))
	}
	if got.Messages[0].Role != "system" || !strings.Contains(got.Messages[0].Content, "SUMMARY") {
		t.Fatalf("首条应为摘要: %+v", got.Messages[0])
	}
}

// TestDoCompressTokenBoundTriggers：未超 N 轮但完整态 token 超 `keep_full_max_tokens`
// → 完整区收窄到最新 1 轮（T=1 → 简化区空 → 摘要区 = 其余）。
func TestDoCompressTokenBoundTriggers(t *testing.T) {
	opts := compress.Options{RetainTurns: 100, KeepFullTokens: 300, TokenMax: 1}
	snap := facade.Snapshot{Messages: seed(6, bigContent()), Turn: "t12"}
	got, changed := compress.DoCompress(snap, opts, func(c string) (string, error) { return "SUMMARY", nil }, nil)
	if !changed {
		t.Fatal("未超 N 轮但完整态超 M 应触发压缩")
	}
	if len(got.Messages) != 3 {
		t.Fatalf("完整区末 1 轮（2 条）+ 摘要 = 3 条，实际 %d", len(got.Messages))
	}
}

// TestDoCompressCurrentTurnAlwaysKept（口径 V）：最新轮自身完整态 token 已超 M → **本轮仍保留完整**。
func TestDoCompressCurrentTurnAlwaysKept(t *testing.T) {
	opts := compress.Options{RetainTurns: 100, KeepFullTokens: 1, TokenMax: 1}
	snap := facade.Snapshot{Messages: seed(6, bigContent()), Turn: "t12"}
	got, changed := compress.DoCompress(snap, opts, func(c string) (string, error) { return "SUMMARY", nil }, nil)
	if !changed {
		t.Fatal("本轮超 M（更早轮可简化）应触发压缩")
	}
	if len(got.Messages) != 3 || got.Messages[0].Role != "system" ||
		!strings.Contains(got.Messages[0].Content, "SUMMARY") {
		t.Fatalf("本轮恒保留 → 摘要 + 末 1 轮（3 条），实际 %+v", got.Messages)
	}
}

// TestDoCompressBothZeroNoCompression（口径 W）：N==0 且 M==0 → **不压缩**。
func TestDoCompressBothZeroNoCompression(t *testing.T) {
	opts := compress.Options{RetainTurns: 0, KeepFullTokens: 0, TokenMax: 1}
	snap := facade.Snapshot{Messages: seed(6, bigContent()), Turn: "t12"}
	if _, changed := compress.DoCompress(snap, opts, func(c string) (string, error) { return "X", nil }, nil); changed {
		t.Fatal("两条件均不启用 → 不压缩")
	}
}

// TestDoCompressWithinBothBounds（D1 用例 ③）：N 轮与 M token 两约束均未超 → 完整区=全量 → 不压缩。
func TestDoCompressWithinBothBounds(t *testing.T) {
	opts := compress.Options{RetainTurns: 100, KeepFullTokens: 1_000_000, TokenMax: 1}
	snap := facade.Snapshot{Messages: seed(6, bigContent()), Turn: "t12"}
	if _, changed := compress.DoCompress(snap, opts, func(c string) (string, error) { return "X", nil }, nil); changed {
		t.Fatal("两约束均未超不应压缩（完整区=全量）")
	}
}

// TestDoCompressBriefBudgetScope（口径 X 关键变更）：**T 是简化区 brief 预算**，决定**摘要区起点**——
// 6 轮 × 100 token；N=2 → 完整区 = 末 2 轮。
//   - T=100000（预算吞掉全部更早轮）→ 无摘要区 → 不压缩；
//   - T=250（容 2 轮）→ 简化区 = 2 轮、摘要区 = 前 2 轮 → 压缩为「摘要 + 简化区 + 完整区」= 1+8=9 条。
func TestDoCompressBriefBudgetScope(t *testing.T) {
	snap := facade.Snapshot{Messages: seed(6, x100()), Turn: "t12"}
	sum := func(string) (string, error) { return "SUMMARY", nil }
	opts := compress.Options{RetainTurns: 2, KeepFullTokens: 1_000_000, TokenMax: 100000}
	if _, changed := compress.DoCompress(snap, opts, sum, nil); changed {
		t.Fatal("简化区吞掉全部更早轮 → 无摘要区 → 不应压缩")
	}
	opts.TokenMax = 250
	got, changed := compress.DoCompress(snap, opts, sum, nil)
	if !changed || len(got.Messages) != 9 {
		t.Fatalf("T=250 → 摘要 + 简化区(4 条) + 完整区(4 条) = 9 条：changed=%v n=%d", changed, len(got.Messages))
	}
}

// TestDoCompressTWithStoredBrief（P3/P4）：简化区预算 T **累加预存 brief_tokens**（不重算）——
// 同一快照、同一 (N,M,T)，预存 brief 把简化区预算降下来 → 简化区纳入更多轮 → 无摘要区（不压缩）。
func TestDoCompressTWithStoredBrief(t *testing.T) {
	snap := facade.Snapshot{Messages: seed(6, x100()), Turn: "t12"}
	opts := compress.Options{RetainTurns: 2, KeepFullTokens: 1_000_000, TokenMax: 250}
	sum := func(string) (string, error) { return "SUMMARY", nil }
	// 基准：实时估算 brief 每轮 100 → 简化区 2 轮、摘要区 2 轮 → 压缩（9 条）。
	if got, changed := compress.DoCompress(snap, opts, sum, nil); !changed || len(got.Messages) != 9 {
		t.Fatalf("基准应压缩（9 条）：changed=%v n=%d", changed, len(got.Messages))
	}
	// 预存 brief 全为 10 → 更早 4 轮共 40 <= 250 → 简化区吞掉全部 → 无摘要区 → 不压缩。
	storedBrief := []int{10, 10, 10, 10, 10, 10}
	if _, changed := compress.DoCompressWith(snap, opts, nil, storedBrief, sum, nil); changed {
		t.Fatal("T 用预存 brief：简化区 40 <= 250 → 无摘要区 → 不应压缩")
	}
}

// TestDoCompressStoredFullBoundary（P3）：完整态边界 **累加预存 full_tokens**（不重算）。
func TestDoCompressStoredFullBoundary(t *testing.T) {
	snap := facade.Snapshot{Messages: seed(6, x100()), Turn: "t12"}
	opts := compress.Options{RetainTurns: 10, KeepFullTokens: 250, TokenMax: 1}
	sum := func(string) (string, error) { return "SUMMARY", nil }
	// 基准（实时 100/轮）：M=250 容 2 轮 → 完整区 = 末 2 轮 + 摘要 = 5 条。
	if got, changed := compress.DoCompress(snap, opts, sum, nil); !changed || len(got.Messages) != 5 {
		t.Fatalf("基准 M=250 → 完整区末 2 轮（5 条）：changed=%v n=%d", changed, len(got.Messages))
	}
	// 预存 full 全为 50 → M=250 容 5 轮 → 完整区 = 末 5 轮 + 摘要 = 11 条。
	storedFull := []int{50, 50, 50, 50, 50, 50}
	if got, changed := compress.DoCompressWith(snap, opts, storedFull, nil, sum, nil); !changed || len(got.Messages) != 11 {
		t.Fatalf("预存 full → 完整区末 5 轮（11 条）：changed=%v n=%d", changed, len(got.Messages))
	}
}

// fallbackSnap：prelude 既有摘要 + 4 轮（每轮 100 token，无工具/思维 → 简化态 = 完整态）。
func fallbackSnap() facade.Snapshot {
	msgs := append([]facade.Message{{Role: "system", Content: "[已压缩早前对话] OLD"}}, seed(4, x100())...)
	return facade.Snapshot{Messages: msgs, Turn: "t4"}
}

// TestDoCompressFallbackMerge（P2 口径 Y/Z3，2026-09-25）：三段 + 输出预留（maxOutputToken）
// > 真窗口（maxContextToken）→ 合并【摘要 + 简化区原内容】→ **重新摘要**（目标 = maxOutputToken/2）
// → **简化区清空**（= [新摘要] + 完整区）。
func TestDoCompressFallbackMerge(t *testing.T) {
	snap := fallbackSnap()
	// N=1 → 完整区 = 末 1 轮；T=100 → 简化区 = 1 轮（turn3）；摘要区 = turn1+turn2。
	// 真窗口 500、输出预留（maxOutputToken）500 → 发送量 + 500 > 500 → 触发兜底。
	opts := compress.Options{RetainTurns: 1, TokenMax: 100, MaxContextToken: 500, MaxOutputToken: 500}
	var got string
	gotSnap, changed := compress.DoCompress(snap, opts, func(c string) (string, error) {
		got = c
		return "MERGED", nil
	}, nil)
	if !changed {
		t.Fatal("三段 + 预留超窗口 → 应触发兜底归并")
	}
	if len(gotSnap.Messages) != 3 || gotSnap.Messages[0].Role != "system" ||
		!strings.Contains(gotSnap.Messages[0].Content, "MERGED") {
		t.Fatalf("归并结果 = [新摘要] + 完整区（1+2 条）：%+v", gotSnap.Messages)
	}
	// 简化区清空：结果里不含简化区原文（turn3）。
	for _, m := range gotSnap.Messages[1:] {
		if m.Role != "user" && m.Role != "assistant" {
			t.Fatalf("完整区消息角色异常：%+v", m)
		}
	}
	// 目标长度 = maxOutputToken 1/2 = 250 → 摘要输入须带长度要求。
	if !strings.Contains(got, "不超过约 250 tokens") {
		t.Fatalf("归并要求应含目标长度（maxOutputToken/2）：%q", got)
	}
	if !strings.Contains(got, "[已压缩早前对话] OLD") {
		t.Fatalf("归并输入应含既有摘要（合并来源）：%q", got)
	}
}

// TestDoCompressFallbackReserveFromOutputToken（P2 口径 Z3）：输出预留 = **maxOutputToken**
// （不再是常量 4096）——发送量本身未超窗口，但 **+ maxOutputToken** 后超窗口 → 触发兜底；
// 目标长度 = maxOutputToken/2。
func TestDoCompressFallbackReserveFromOutputToken(t *testing.T) {
	snap := fallbackSnap()
	// 发送量 ≈ 407（< 窗口 500）→ 仅因预留 200 才越界（407+200=607 > 500）→ 证明预留取自 maxOutputToken。
	opts := compress.Options{RetainTurns: 1, TokenMax: 100, MaxContextToken: 500, MaxOutputToken: 200}
	var got string
	_, changed := compress.DoCompress(snap, opts, func(c string) (string, error) {
		got = c
		return "MERGED", nil
	}, nil)
	if !changed {
		t.Fatal("发送量 + maxOutputToken 超窗口 → 应触发兜底（预留 = maxOutputToken）")
	}
	if !strings.Contains(got, "不超过约 100 tokens") {
		t.Fatalf("目标长度应 = maxOutputToken/2 = 100：%q", got)
	}
	// 反证（证明越界确实源于预留）：同窗口、预留 = 0 → 发送量本身未越界 → 走常规压缩（5 条）。
	opts.MaxOutputToken = 0
	gotSnap, changed2 := compress.DoCompress(snap, opts, func(c string) (string, error) {
		return "SUMMARY", nil
	}, nil)
	if !changed2 || len(gotSnap.Messages) != 5 {
		t.Fatalf("预留=0 且发送量未越界 → 应常规压缩（5 条）：changed=%v n=%d", changed2, len(gotSnap.Messages))
	}
}

// TestDoCompressFallbackDisabledWithoutContextToken（P2 口径 Z3）：maxContextToken <= 0（未配置）
// → **不启用兜底**（仅常规三层压缩），即使 maxOutputToken 很大。
func TestDoCompressFallbackDisabledWithoutContextToken(t *testing.T) {
	snap := fallbackSnap()
	opts := compress.Options{RetainTurns: 1, TokenMax: 100, MaxContextToken: 0, MaxOutputToken: 1_000_000}
	var got string
	gotSnap, changed := compress.DoCompress(snap, opts, func(c string) (string, error) {
		got = c
		return "SUMMARY", nil
	}, nil)
	if !changed {
		t.Fatal("有摘要区应压缩（即使兜底不启用）")
	}
	// 常规压缩：摘要 + 简化区(2 条) + 完整区(2 条) = 5 条；不带兜底长度要求。
	if len(gotSnap.Messages) != 5 {
		t.Fatalf("maxContextToken<=0 → 常规压缩（5 条）：%+v", gotSnap.Messages)
	}
	if strings.Contains(got, "不超过约") {
		t.Fatalf("兜底未启用不应带长度要求：%q", got)
	}
}

// TestDoCompressFallbackNotTriggered：窗口足够大 → 不触发兜底，走**常规压缩**（摘要区 → 摘要）。
func TestDoCompressFallbackNotTriggered(t *testing.T) {
	snap := fallbackSnap()
	opts := compress.Options{RetainTurns: 1, TokenMax: 100, MaxContextToken: 1_000_000, MaxOutputToken: 500}
	var got string
	gotSnap, changed := compress.DoCompress(snap, opts, func(c string) (string, error) {
		got = c
		return "SUMMARY", nil
	}, nil)
	if !changed {
		t.Fatal("有摘要区应压缩")
	}
	// 常规压缩：摘要 + 简化区(2 条) + 完整区(2 条) = 5 条。
	if len(gotSnap.Messages) != 5 {
		t.Fatalf("常规压缩 = 摘要 + 简化区 + 完整区（5 条）：%+v", gotSnap.Messages)
	}
	if strings.Contains(got, "不超过约") {
		t.Fatalf("未触发兜底不应带长度要求：%q", got)
	}
}

// TestDoCompressFallbackIdempotent：归并结果**再跑一次** → 无摘要区 → 不再变更（幂等/不重复膨胀）。
func TestDoCompressFallbackIdempotent(t *testing.T) {
	opts := compress.Options{RetainTurns: 1, TokenMax: 100, MaxContextToken: 500, MaxOutputToken: 500}
	sum := func(string) (string, error) { return "MERGED", nil }
	first, changed := compress.DoCompress(fallbackSnap(), opts, sum, nil)
	if !changed {
		t.Fatal("首次应归并")
	}
	second, changed2 := compress.DoCompress(first, opts, sum, nil)
	if changed2 {
		t.Fatalf("归并结果应幂等（无摘要区 → 不再变更）：%+v", second.Messages)
	}
	if len(second.Messages) != len(first.Messages) {
		t.Fatalf("幂等：条数应不变 %d -> %d", len(first.Messages), len(second.Messages))
	}
}

// TestDoCompressFallbackFailureKeepsOriginal：归并摘要失败 → **回退未归并状态**（原文不丢）。
func TestDoCompressFallbackFailureKeepsOriginal(t *testing.T) {
	snap := fallbackSnap()
	opts := compress.Options{RetainTurns: 1, TokenMax: 100, MaxContextToken: 500, MaxOutputToken: 500}
	logged := ""
	got, changed := compress.DoCompress(snap, opts, func(string) (string, error) {
		return "", errors.New("no llm")
	}, func(format string, args ...any) { logged = fmt.Sprintf(format, args...) })
	if changed {
		t.Fatal("归并失败不应改写快照")
	}
	if len(got.Messages) != len(snap.Messages) {
		t.Fatalf("失败应回退未归并状态：%d -> %d", len(snap.Messages), len(got.Messages))
	}
	if !strings.Contains(logged, "fallback merge summarize failed") {
		t.Fatalf("失败应留日志：%q", logged)
	}
}

// TestDoCompressFallbackEvolution（P2 演进路径）：归并后**下一轮**（追加 1 轮）→
// 简化区从 0 重新累积 → 发送 = 完整原文 + 1 轮简化 + 摘要（不再压缩）。
func TestDoCompressFallbackEvolution(t *testing.T) {
	opts := compress.Options{RetainTurns: 1, TokenMax: 100, MaxContextToken: 500, MaxOutputToken: 500}
	merged, changed := compress.DoCompress(fallbackSnap(), opts, func(string) (string, error) { return "MERGED", nil }, nil)
	if !changed {
		t.Fatal("首次应归并")
	}
	// 下一轮：追加 turn5（2 条消息）。
	next := merged
	next.Messages = append(append([]facade.Message{}, merged.Messages...),
		facade.Message{Role: "user", Kind: "text", Content: x100()},
		facade.Message{Role: "assistant", Content: x100()},
	)
	z := compress.LocateThreeZones(next.Messages, opts.RetainTurns, 0, opts.TokenMax, nil, nil)
	// 无摘要区（不再压缩）；简化区 = 1 轮（turn4）；完整区 = 1 轮（turn5）。
	if z.SummaryStart != z.BriefStart {
		t.Fatalf("演进后应无摘要区：zones=%+v", z)
	}
	if z.FullStart-z.BriefStart != 2 {
		t.Fatalf("简化区应含 1 轮（2 条消息）：zones=%+v", z)
	}
}

// TestDoCompressSummarizeFailSkips：摘要失败 → 直接返回不压缩（2026-09-02 决策：不降级）。
func TestDoCompressSummarizeFailSkips(t *testing.T) {
	opts := compress.Options{RetainTurns: 2, TokenMax: 50}
	snap := facade.Snapshot{Messages: seed(6, bigContent()), Turn: "t12"}
	got, changed := compress.DoCompress(snap, opts, func(c string) (string, error) { return "", errors.New("no llm") }, nil)
	if changed {
		t.Fatal("摘要失败不应压缩")
	}
	if got.Turn != snap.Turn || len(got.Messages) != len(snap.Messages) {
		t.Fatal("失败时快照应原样返回")
	}
}

func TestCompressorStartSubscribes(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	defer bus.Close()
	c := compress.New(compress.Options{}, inline.New(bus))
	if err := c.Start(plugin.Deps{Bus: bus, Logf: t.Logf}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if c.Name() != "compress" {
		t.Fatalf("Name: %s", c.Name())
	}
}

// TestCompressorNotifyOnSummarizeFailure（2026-09-20，B 批：插件失败用户可见）：
// 真链路（session-compress 事件 → 读快照 → 摘要失败）→ 除日志外**上报一次用户可见提示**；
// 且压缩不降级（快照未被改写）。
func TestCompressorNotifyOnSummarizeFailure(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	defer bus.Close()

	const instanceID, session, workDir, dataDir = "ins-cmp", "s-cmp", `C:\ws`, `C:\ws\.chonkpilot`
	api := inline.New(bus)
	data.Reset()
	data.Register(instanceID, workDir, dataDir)
	prj, err := data.Prj(instanceID)
	if err != nil {
		t.Fatalf("data.Prj: %v", err)
	}
	if err := data.SetConfig(prj, "keep_full_max_turns", "2"); err != nil {
		t.Fatalf("SetConfig keep_full_max_turns: %v", err)
	}
	if err := data.SetConfig(prj, "keep_full_max_tokens", "50"); err != nil {
		t.Fatalf("SetConfig keep_full_max_tokens: %v", err)
	}
	if err := data.SetConfig(prj, "compress_token_threshold", "50"); err != nil {
		t.Fatalf("SetConfig compress_token_threshold: %v", err)
	}
	snap := facade.Snapshot{SessionID: session, Turn: "t12", Messages: seed(6, bigContent())}
	if _, err := api.SnapshotSet(facade.SnapshotSetRequest{
		InstanceID: instanceID, Snapshot: snap,
	}); err != nil {
		t.Fatalf("SnapshotSet: %v", err)
	}
	t.Cleanup(data.Reset)

	// llm-simple 订阅者显式报错 → 摘要失败（插件不得降级）。
	if _, err := bus.On("llm-simple", 0, func(_ context.Context, _ string, _ *mq.Value) error {
		return errors.New("mock llm unavailable")
	}); err != nil {
		t.Fatalf("stub llm-simple: %v", err)
	}

	notices := make(chan plugin.Notice, 4)
	var logged []string
	c := compress.New(compress.Options{RetainTurns: 1, TokenMax: 50}, inline.New(bus))
	if err := c.Start(plugin.Deps{
		Bus:    bus,
		Logf:   func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
		Notify: func(n plugin.Notice) { notices <- n },
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// 触发压缩（server.finish 发出的同主题事件；总线同步派发 → Emit 返回时已处理完）。
	body, _ := json.Marshal(map[string]any{
		"instance_id": instanceID, "work_dir": workDir, "data_dir": dataDir,
		"session": session, "snapshot_turn": "t12",
	})
	bus.Emit(context.Background(), "session-compress", body).Wait()

	select {
	case n := <-notices:
		if n.Plugin != "compress" || n.Kind != "llm" {
			t.Fatalf("上报字段不符：%+v", n)
		}
		if n.InstanceID != instanceID || n.Session != session || n.Turn != "t12" {
			t.Fatalf("上报归属字段不符（判重键）：%+v", n)
		}
		if !strings.Contains(n.Reason, "mock llm unavailable") {
			t.Fatalf("上报原因应含真实失败原因：%q", n.Reason)
		}
		t.Logf("raw evidence: 用户可见提示=%+v", n)
	default:
		t.Fatalf("摘要失败应上报一次用户可见提示（日志：%v）", logged)
	}
	if len(notices) != 0 {
		t.Fatal("一次失败应只上报一次")
	}
	joined := strings.Join(logged, "\n")
	if !strings.Contains(joined, "summarize failed") || !strings.Contains(joined, "mock llm unavailable") {
		t.Fatalf("失败原因应进日志（可查），实际：%v", logged)
	}
	t.Logf("raw evidence: 插件日志行=%q", joined)
	gr, err := api.SnapshotGet(facade.SnapshotGetRequest{InstanceID: instanceID, SessionID: session})
	if err != nil || !gr.Found {
		t.Fatalf("SnapshotGet: found=%v err=%v", gr.Found, err)
	}
	got := gr.Snapshot
	if len(got.Messages) != len(snap.Messages) {
		t.Fatalf("摘要失败不应改写快照：%d -> %d", len(snap.Messages), len(got.Messages))
	}
}

// TestCompressedSummaryPersistedInSnapshot：真链路（session-compress → 读快照 → 摘要成功 → 回写快照）后，
// 压缩产物唯一落点 = 会话快照（首条 = system + `[已压缩早前对话] ` + 摘要全文）；snapshot_turn 不变。
func TestCompressedSummaryPersistedInSnapshot(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	defer bus.Close()

	const instanceID, session, workDir, dataDir = "ins-snap", "s-snap", `C:\ws`, `C:\ws\.chonkpilot`
	api := inline.New(bus)
	data.Reset()
	data.Register(instanceID, workDir, dataDir)
	prj, err := data.Prj(instanceID)
	if err != nil {
		t.Fatalf("data.Prj: %v", err)
	}
	// **读时兼容覆盖（D1）**：写**旧键** `keep_full_turns`（新键缺失）→ resolveOpts 应回落命中它。
	if err := data.DeleteConfig(prj, "keep_full_max_turns"); err != nil {
		t.Fatalf("DeleteConfig keep_full_max_turns: %v", err)
	}
	if err := data.SetConfig(prj, "keep_full_turns", "1"); err != nil {
		t.Fatalf("SetConfig keep_full_turns(legacy): %v", err)
	}
	if err := data.SetConfig(prj, "keep_full_max_tokens", "100000"); err != nil {
		t.Fatalf("SetConfig keep_full_max_tokens: %v", err)
	}
	if err := data.SetConfig(prj, "compress_token_threshold", "50"); err != nil {
		t.Fatalf("SetConfig compress_token_threshold: %v", err)
	}
	snap := facade.Snapshot{SessionID: session, Turn: "t12", Messages: seed(6, bigContent())}
	if _, err := api.SnapshotSet(facade.SnapshotSetRequest{
		InstanceID: instanceID, Snapshot: snap,
	}); err != nil {
		t.Fatalf("SnapshotSet: %v", err)
	}
	t.Cleanup(data.Reset)

	const summaryText = "SNAPSHOT-SUMMARY-SENTINEL"
	if _, err := bus.On("llm-simple", 0, func(_ context.Context, _ string, v *mq.Value) error {
		v.Result = map[string]any{"text": summaryText}
		return nil
	}); err != nil {
		t.Fatalf("stub llm-simple: %v", err)
	}

	c := compress.New(compress.Options{RetainTurns: 1, TokenMax: 50}, inline.New(bus))
	if err := c.Start(plugin.Deps{Bus: bus, Logf: t.Logf}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	body, _ := json.Marshal(map[string]any{
		"instance_id": instanceID, "work_dir": workDir, "data_dir": dataDir,
		"session": session, "snapshot_turn": "t12",
	})
	bus.Emit(context.Background(), "session-compress", body).Wait()

	gr, err := api.SnapshotGet(facade.SnapshotGetRequest{InstanceID: instanceID, SessionID: session})
	if err != nil || !gr.Found {
		t.Fatalf("SnapshotGet: found=%v err=%v", gr.Found, err)
	}
	got := gr.Snapshot
	if len(got.Messages) >= len(snap.Messages) {
		t.Fatalf("压缩后快照应更短：%d -> %d", len(snap.Messages), len(got.Messages))
	}
	head := got.Messages[0]
	if head.Role != "system" || !strings.Contains(head.Content, summaryText) {
		t.Fatalf("压缩产物应落库于快照首条：%+v", head)
	}
	if !strings.Contains(head.Content, "[已压缩早前对话]") {
		t.Fatalf("摘要应带既有压缩标记：%q", head.Content)
	}
	if got.Turn != "t12" {
		t.Fatalf("触发范围（snapshot_turn）应落库：%q", got.Turn)
	}
	// 简化区预算 50 容不下单轮 brief（280）→ 简化区空 → 完整区 = 末 1 轮（2 条）。
	if kept := len(got.Messages) - 1; kept != 2 {
		t.Fatalf("完整区应为最后 1 轮（2 条消息），实际 %d", kept)
	}
	t.Logf("raw evidence: snapshot_turn=%s head=%q kept=%d", got.Turn, head.Content, len(got.Messages)-1)
}

// TestCompressorPayloadLegacyReadCompat（口径 Z4，2026-09-25）：对外载荷字段统一 snake_case 后，
// 插件对**上批 camelCase**（`maxContextToken` / `maxOutputToken`）与**更早** `window`（仅上下文窗口）
// 仍**只读兼容** —— 旧名同样启用兜底归并（只读不写：写入侧 server 只发 snake_case，见 turn_test）。
// 反证：`session-compress` 不带任何窗口字段 → 不启用兜底（常规三层压缩），见同文件其余用例。
func TestCompressorPayloadLegacyReadCompat(t *testing.T) {
	cases := []struct {
		name   string
		legacy map[string]any
	}{
		{"camelCase 旧名", map[string]any{"maxContextToken": 500, "maxOutputToken": 500}},
		{"最早期 window 名", map[string]any{"window": 300}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus, err := mq.New(mq.Options{Prefix: "chonk."})
			if err != nil {
				t.Fatalf("mq.New: %v", err)
			}
			defer bus.Close()

			const instanceID, session, workDir, dataDir = "ins-legacy", "s-legacy", `C:\ws`, `C:\ws\.chonkpilot`
			api := inline.New(bus)
			data.Reset()
			data.Register(instanceID, workDir, dataDir)
			prj, err := data.Prj(instanceID)
			if err != nil {
				t.Fatalf("data.Prj: %v", err)
			}
			for k, v := range map[string]string{
				"keep_full_max_turns": "1", "keep_full_max_tokens": "100000", "compress_token_threshold": "100",
			} {
				if err := data.SetConfig(prj, k, v); err != nil {
					t.Fatalf("SetConfig %s: %v", k, err)
				}
			}
			snap := fallbackSnap()
			snap.SessionID = session
			if _, err := api.SnapshotSet(facade.SnapshotSetRequest{InstanceID: instanceID, Snapshot: snap}); err != nil {
				t.Fatalf("SnapshotSet: %v", err)
			}
			t.Cleanup(data.Reset)

			if _, err := bus.On("llm-simple", 0, func(_ context.Context, _ string, v *mq.Value) error {
				v.Result = map[string]any{"text": "MERGED"}
				return nil
			}); err != nil {
				t.Fatalf("stub llm-simple: %v", err)
			}
			c := compress.New(compress.Options{}, inline.New(bus))
			if err := c.Start(plugin.Deps{Bus: bus, Logf: t.Logf}); err != nil {
				t.Fatalf("Start: %v", err)
			}
			p := map[string]any{
				"instance_id": instanceID, "work_dir": workDir, "data_dir": dataDir,
				"session": session, "snapshot_turn": "t4",
			}
			for k, v := range tc.legacy {
				p[k] = v
			}
			body, _ := json.Marshal(p)
			bus.Emit(context.Background(), "session-compress", body).Wait()

			gr, err := api.SnapshotGet(facade.SnapshotGetRequest{InstanceID: instanceID, SessionID: session})
			if err != nil || !gr.Found {
				t.Fatalf("SnapshotGet: found=%v err=%v", gr.Found, err)
			}
			got := gr.Snapshot
			if len(got.Messages) != 3 || got.Messages[0].Role != "system" ||
				!strings.Contains(got.Messages[0].Content, "MERGED") {
				t.Fatalf("旧名 %q 应只读兼容并启用兜底归并（[新摘要] + 完整区 = 3 条）：%+v", tc.name, got.Messages)
			}
			t.Logf("raw evidence: legacy=%v -> merged head=%q n=%d", tc.legacy, got.Messages[0].Content, len(got.Messages))
		})
	}
}
