// LLMRefName 折算白盒（12-数据层 / 40-演进计划 §SL）：`llmref` 取值二态
// （provider name 字符串 / 旧 int 索引）→ provider name；llms 兼容
// `[]any`（MQ/JSON 往返）与 `[]map[string]any`（data 门面 inline 直返）两形态。
package data_test

import (
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
)

func TestLLMRefName(t *testing.T) {
	// []any：MQ / JSON 往返后的解码形态。
	wire := []any{
		map[string]any{"name": "prov-0"},
		map[string]any{"name": "prov-1"},
	}
	// []map[string]any：data 门面 inline 绑定直返的内核形态。
	inlineList := []map[string]any{
		{"name": "prov-0"},
		{"name": "prov-1"},
	}
	cases := []struct {
		name string
		v    any
		llms any
		want string
	}{
		{"name 字符串原样", "prov-x", wire, "prov-x"},
		{"name 字符串去首尾空白", " prov-x ", wire, "prov-x"},
		{"空串=显式「系统默认（启动参数）」→不指定", "", wire, ""},
		{"int 索引命中", 1, wire, "prov-1"},
		{"float64 索引（JSON 往返）", 1.0, wire, "prov-1"},
		{"int 越界→首个可用", 9, wire, "prov-0"},
		{"int 负数（无可用 LLM 的 -1）→首个可用", -1, wire, "prov-0"},
		{"键缺失（nil）→首个可用", nil, wire, "prov-0"},
		{"无 llms→空串", 0, []any{}, ""},
		{"llms 键缺失→空串", 0, nil, ""},
		{"门面直返 []map[string]any 索引命中", 1, inlineList, "prov-1"},
		{"门面直返 []map 越界→首个可用", -3, inlineList, "prov-0"},
		{"项名缺失→空串", 0, []any{map[string]any{"model": "m"}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := data.LLMRefName(tc.v, tc.llms); got != tc.want {
				t.Fatalf("LLMRefName(%#v, %#v) = %q, want %q", tc.v, tc.llms, got, tc.want)
			}
		})
	}
}
