// 提示词优化 LLM 解析白盒（SL-4：改读 `llm.promptOptimise`，llmref 形态同 defaultLLM；
// 2026-09-15：llmref 改 name 字符串 + 旧 int 索引兼容）：
//   - 提示词优化（activeLLM）只支持 usr llms 中的真实 provider；取值空串（未设置）与已失效名
//     必须明确报错，不得猜测端点（否则会向 api.openai.com 等未知地址发请求）。
//   - 键缺失 / 空串 → 由数据层读侧回落 defaultLLM（SL-1），桥侧不重复回落。
package bridge

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// TestResolveLLMRefIndex：name 命中 / 旧 int 索引兼容（int 内核形态与 float64 JSON 形态都认）/
// 空串与失效名报错（D-41：静态错误必经 code）。
func TestResolveLLMRefIndex(t *testing.T) {
	llms := []any{
		map[string]any{"name": "openai", "model": "gpt-4"},
		map[string]any{"name": "deepseek", "model": "deepseek-chat"},
	}
	cases := []struct {
		name     string
		value    any
		wantIdx  int
		wantErr  string // 非空 = 期望报错且错误文本包含该子串
		wantCode string // 期望错误码（wantErr 非空时校验）
	}{
		{"name 命中 usr llms", "deepseek", 1, "", ""},
		{"旧 int 索引（float64）", float64(1), 1, "", ""},
		{"旧 int 索引（内核 int）", 1, 1, "", ""},
		{"旧 int 索引（int64）", int64(1), 1, "", ""},
		{"旧 int 索引越界 → 回落首个", float64(9), 0, "", ""},
		{"旧 int -1（未配置）→ 回落首个", float64(-1), 0, "", ""},
		{"旧 int -1（内核 int）→ 回落首个", -1, 0, "", ""},
		{"键缺失（nil）→ 回落首个", nil, 0, "", ""},
		{"空串（未设置）→ 报错", "", 0, "未设置", optimizeCodeLLMNotSet},
		{"已失效名 → 报错", "gone", 0, "不存在", optimizeCodeLLMNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			idx, err := resolveLLMRefIndex(c.value, llms)
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
			if c.wantCode != "" && err.Code != c.wantCode {
				t.Fatalf("code=%q want %q", err.Code, c.wantCode)
			}
		})
	}
}

// TestResolveLLMRefIndexNotFoundParams：已失效名错误带 name 参数（供前端 optimizeError.llm_not_found
// 的 {name} 插值）。
func TestResolveLLMRefIndexNotFoundParams(t *testing.T) {
	llms := []any{map[string]any{"name": "openai"}}
	_, err := resolveLLMRefIndex("gone", llms)
	if err == nil {
		t.Fatal("期望报错")
	}
	if got, _ := err.Params[msgkeys.FieldName].(string); got != "gone" {
		t.Fatalf("name 参数=%v want %q", err.Params[msgkeys.FieldName], "gone")
	}
}

// TestActiveLLMErrorCodes：activeLLM 的**静态错误必经 code**（D-41 守卫）——未配置 LLM / LLM 名失效
// / 缺 model 各自返回稳定错误码（前端据此翻译）。
func TestActiveLLMErrorCodes(t *testing.T) {
	br, _, _, _ := newFacadeBridgeEnv(t)
	save := func(t *testing.T, llms []any, extra map[string]any) {
		t.Helper()
		if _, errs := br.PublishEvent("data-user-config-delete", `{"id":"llm.promptOptimise"}`); len(errs) > 0 {
			t.Fatalf("delete llm.promptOptimise: %v", errs)
		}
		data := map[string]any{"llms": llms}
		for k, v := range extra {
			data[k] = v
		}
		body, _ := json.Marshal(map[string]any{"data": data})
		if _, errs := br.PublishEvent("data-user-config-save", string(body)); len(errs) > 0 {
			t.Fatalf("save user-config: %v", errs)
		}
	}
	cases := []struct {
		name     string
		llms     []any
		extra    map[string]any
		wantCode string
	}{
		{
			"未配置 LLM",
			[]any{},
			nil,
			optimizeCodeNoLLMConfig,
		},
		{
			"LLM 名失效",
			[]any{map[string]any{"name": "openai", "model": "gpt-4"}},
			map[string]any{"llm.promptOptimise": "gone"},
			optimizeCodeLLMNotFound,
		},
		{
			"缺 model",
			[]any{map[string]any{"name": "openai", "model": ""}},
			map[string]any{"llm.promptOptimise": "openai"},
			optimizeCodeLLMModelMissing,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			save(t, c.llms, c.extra)
			_, oerr := activeLLM(br)
			if oerr == nil {
				t.Fatal("期望报错")
			}
			if oerr.Code != c.wantCode {
				t.Fatalf("code=%q want %q（message=%q）", oerr.Code, c.wantCode, oerr.Message)
			}
		})
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
