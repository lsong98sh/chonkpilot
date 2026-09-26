// 提示词优化 LLM 解析白盒（SL-4：改读 `llm.promptOptimise`，llmref 形态同 defaultLLM；
// 2026-09-15：llmref 改 name 字符串 + 旧 int 索引兼容）：
//   - 提示词优化（activeLLM）只支持 usr llms 中的真实 provider；内置项（echo / 系统默认（启动参数））
//     与已失效名必须明确报错，不得猜测端点（否则会向 api.openai.com 等未知地址发请求）。
//   - 键缺失 / 空串 → 由数据层读侧回落 defaultLLM（SL-1），桥侧不重复回落。
package bridge

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestResolveLLMRefIndex：name 命中 / 旧 int 索引兼容（int 内核形态与 float64 JSON 形态都认）/
// 内置项与失效名报错。
func TestResolveLLMRefIndex(t *testing.T) {
	llms := []any{
		map[string]any{"name": "openai", "model": "gpt-4"},
		map[string]any{"name": "deepseek", "model": "deepseek-chat"},
	}
	builtins := []BuiltinLLM{
		{Kind: "builtin", Name: "echo", Protocol: "echo", Model: "echo"},
		{Kind: "default", Model: "mock"}, // 启动参数隐含默认：无 name
	}
	cases := []struct {
		name    string
		value   any
		wantIdx int
		wantErr string // 非空 = 期望报错且错误文本包含该子串
	}{
		{"name 命中 usr llms", "deepseek", 1, ""},
		{"旧 int 索引（float64）", float64(1), 1, ""},
		{"旧 int 索引（内核 int）", 1, 1, ""},
		{"旧 int 索引（int64）", int64(1), 1, ""},
		{"旧 int 索引越界 → 回落首个", float64(9), 0, ""},
		{"旧 int -1（未配置）→ 回落首个", float64(-1), 0, ""},
		{"旧 int -1（内核 int）→ 回落首个", -1, 0, ""},
		{"键缺失（nil）→ 回落首个", nil, 0, ""},
		{"内置 provider echo → 报错", "echo", 0, "系统内置项"},
		{"空串（系统默认（启动参数））→ 报错", "", 0, "系统默认（启动参数）"},
		{"已失效名 → 报错", "gone", 0, "不存在"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			idx, err := resolveLLMRefIndex(c.value, llms, builtins)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected err: %v", err)
				}
				if idx != c.wantIdx {
					t.Fatalf("idx=%d want %d", idx, c.wantIdx)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("err=%v want contains %q", err, c.wantErr)
			}
		})
	}
}

// TestResolveLLMRefNameShadowsBuiltin：同名 usr 记录优先于内置项（与后端 loadLLMProvider
// 命中顺序一致）——usr 里显式配了名为 echo 的记录时，llmref=echo 指向该记录（可优化）。
func TestResolveLLMRefNameShadowsBuiltin(t *testing.T) {
	llms := []any{map[string]any{"name": "other"}, map[string]any{"name": "echo", "model": "gpt-4"}}
	builtins := []BuiltinLLM{{Kind: "builtin", Name: "echo", Protocol: "echo", Model: "echo"}}
	idx, err := resolveLLMRefIndex("echo", llms, builtins)
	if err != nil || idx != 1 {
		t.Fatalf("idx=%d err=%v want 1/nil（同名 usr 记录优先）", idx, err)
	}
}

// TestActiveLLMReadsPromptOptimise：SL-4 端到端——GUI 桥提示词优化读 `llm.promptOptimise`
// （不再是 defaultLLM）：显式配置 → 命中该 provider；键缺失 / 空串 → 数据层读侧回落 defaultLLM（SL-1）；
// 旧 int 索引 → 折算 llms[idx]。每次调用现读配置（SL-C9 热生效）。
func TestActiveLLMReadsPromptOptimise(t *testing.T) {
	br, _, _, _ := newFacadeBridgeEnv(t)
	llms := []any{
		map[string]any{"name": "def", "model": "def-model", "baseUrl": "https://def.test/v1"},
		map[string]any{"name": "opt", "model": "opt-model", "baseUrl": "https://opt.test/v1"},
	}
	// set 先清 `llm.promptOptimise` 键（拿回「键缺失」语义），再写基线 llms/defaultLLM + 本用例增量。
	set := func(t *testing.T, extra map[string]any) {
		t.Helper()
		if _, errs := br.PublishEvent("data-user-config-delete", `{"id":"llm.promptOptimise"}`); len(errs) > 0 {
			t.Fatalf("delete llm.promptOptimise: %v", errs)
		}
		data := map[string]any{"llms": llms, "defaultLLM": "def"}
		for k, v := range extra {
			data[k] = v
		}
		body, _ := json.Marshal(map[string]any{"data": data})
		res, errs := br.PublishEvent("data-user-config-save", string(body))
		if len(errs) > 0 {
			t.Fatalf("save user-config: %v", errs)
		}
		if m, _ := res.(map[string]any); m["ok"] != true {
			t.Fatalf("save user-config 应答异常: %+v", res)
		}
	}
	for _, c := range []struct {
		name  string
		extra map[string]any
		want  string // 期望命中的 provider model
	}{
		{"键缺失 → 回落 defaultLLM", nil, "def-model"},
		{"空串 → 回落 defaultLLM", map[string]any{"llm.promptOptimise": ""}, "def-model"},
		{"显式 name → 命中该 provider", map[string]any{"llm.promptOptimise": "opt"}, "opt-model"},
		{"旧 int 索引 → 折算 llms[idx]", map[string]any{"llm.promptOptimise": 1}, "opt-model"},
	} {
		t.Run(c.name, func(t *testing.T) {
			set(t, c.extra)
			llm, err := activeLLM(br)
			if err != nil {
				t.Fatalf("activeLLM: %v", err)
			}
			if llm.Model != c.want {
				t.Fatalf("model=%q want %q（name=%q）", llm.Model, c.want, llm.Name)
			}
		})
	}
}
