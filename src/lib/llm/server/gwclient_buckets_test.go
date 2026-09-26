// RB-5 L2 白盒：`gwClient.ToolsFor(instance)` = **global 桶 ∪ 归属该 instance 的桶**
// （顺序 = tools/list 顺序，global 在前；其它 instance 的桶**不可见**；instance 为空 → 仅 global）。
//
// 背景（[42 §2 (139)](../../../../docs/spec/40-roadmap/42-决策记录.md)）：RB-5 前 `toolsForLLM(inst)`
// 每次对**全量切片**过滤；RB-5 改为 `buckets`（scope → 工具，与全量同源）后直取两桶拼接。
// 本用例锁定「分桶取用」的等价语义（逐项同序、无重复、不串 instance）。
package server

import "testing"

func TestGWClientToolsForBuckets(t *testing.T) {
	g := newGWClient(nil)
	// 模拟一次 tools/list 结果（listTools 的落点：tools=全量 + buckets=按 scope 分组）
	g.mu.Lock()
	g.tools = []ToolDef{
		{Name: "self_a", Scope: ""},
		{Name: "ins1_x", Scope: "ins-1"},
		{Name: "self_b", Scope: ""},
		{Name: "ins2_y", Scope: "ins-2"},
	}
	g.buckets = map[string][]ToolDef{
		"":      {{Name: "self_a", Scope: ""}, {Name: "self_b", Scope: ""}},
		"ins-1": {{Name: "ins1_x", Scope: "ins-1"}},
		"ins-2": {{Name: "ins2_y", Scope: "ins-2"}},
	}
	g.mu.Unlock()

	names := func(in string) []string {
		var out []string
		for _, d := range g.ToolsFor(in) {
			out = append(out, d.Name)
		}
		return out
	}

	// ins-1 可见 = global ∪ 自身（global 在前）；ins-2 的桶不可见
	if got := names("ins-1"); len(got) != 3 || got[0] != "self_a" || got[1] != "self_b" || got[2] != "ins1_x" {
		t.Fatalf("ToolsFor(ins-1) = %v want [self_a self_b ins1_x]（global 在前 + 不越 instance）", got)
	}
	if got := names("ins-2"); len(got) != 3 || got[2] != "ins2_y" {
		t.Fatalf("ToolsFor(ins-2) = %v want 3 项且末项 ins2_y", got)
	}
	// instance 为空 → 仅 global（旧口径 scope==""）
	if got := names(""); len(got) != 2 || got[0] != "self_a" || got[1] != "self_b" {
		t.Fatalf("ToolsFor(\"\") = %v want [self_a self_b]", got)
	}
	// 全量缓存不受分桶影响（UI/管理面按名反查用）
	if got := g.Tools(); len(got) != 4 {
		t.Fatalf("Tools() 全量 = %d 项 want 4（分桶不改全量缓存）", len(got))
	}
}
