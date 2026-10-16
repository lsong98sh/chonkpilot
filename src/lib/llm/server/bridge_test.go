// ResolveBridgeTarget 白盒（BRIDGE-1 步骤 2）：只读解析 provider name → {baseURL, apiKey, protocol}。
//
// 断言口径：命中 usr `llms[].name` → 该记录；`protocol` 返回**原始值**（anthropic **不被**归一为
// openai —— 桥接入口按 protocol 选鉴权头，归一化只属 router 出网口径）；baseUrl 空 → 回落 exe flag
// `--llm-base`；保留段 `default` / 空 name → 系统默认（openai + exe base，无 apiKey）；未命中 →
// ok=false（不静默回落）。
package server

import "testing"

func TestResolveBridgeTarget(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm) // Options.LLMBase = llm.URL（exe flag --llm-base）
	registerTestInstance(t, s)
	saveTestUserConfig(t, s, map[string]any{"llms": []any{
		map[string]any{"name": "prov-openai", "protocol": "openai",
			"baseUrl": "http://up.example/v1", "apiKey": "sk-oa", "model": "m1"},
		map[string]any{"name": "prov-claude", "protocol": "anthropic",
			"baseUrl": "http://ant.example", "apiKey": "sk-ant"},
		map[string]any{"name": "prov-nobase", "apiKey": "sk-nb"}, // baseUrl 空 → 回落 --llm-base
	}})

	// ① 命中 openai 型：baseUrl/apiKey/protocol 逐字。
	got, ok := s.ResolveBridgeTarget("prov-openai")
	if !ok || got.BaseURL != "http://up.example/v1" || got.APIKey != "sk-oa" || got.Protocol != "openai" {
		t.Fatalf("prov-openai → %+v ok=%v，want {http://up.example/v1 sk-oa openai}", got, ok)
	}

	// ② anthropic：protocol 返回**原值**（关键——不被 NormalizeLLMProtocol 折叠为 openai）。
	got, ok = s.ResolveBridgeTarget("prov-claude")
	if !ok || got.Protocol != "anthropic" || got.BaseURL != "http://ant.example" || got.APIKey != "sk-ant" {
		t.Fatalf("prov-claude → %+v ok=%v，want protocol=anthropic（原值）", got, ok)
	}

	// ③ baseUrl 空 → 回落 exe flag --llm-base（与 specFor 同口径）。
	got, ok = s.ResolveBridgeTarget("prov-nobase")
	if !ok || got.BaseURL != llm.URL {
		t.Fatalf("prov-nobase → baseURL=%q ok=%v，want 回落 %q", got.BaseURL, ok, llm.URL)
	}

	// ④ 未命中 → ok=false（不静默回落）。
	if _, ok := s.ResolveBridgeTarget("no-such-provider"); ok {
		t.Fatal("未命中 provider 应返回 ok=false")
	}

	// ⑤ 保留段 default / 空 name → 系统默认（openai + exe base，无 apiKey）。
	for _, name := range []string{"default", ""} {
		got, ok := s.ResolveBridgeTarget(name)
		if !ok || got.BaseURL != llm.URL || got.Protocol != ProtocolOpenAI || got.APIKey != "" {
			t.Fatalf("name=%q → %+v ok=%v，want 系统默认（%s/openai/无 key）", name, got, ok, llm.URL)
		}
	}
}
