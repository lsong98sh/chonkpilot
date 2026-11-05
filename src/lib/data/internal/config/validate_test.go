// INPUTVAL-1 写侧校验纯函数单测（L1）。
package config

import (
	"strings"
	"testing"
)

func TestValidateUserConfigPayloadLLMs(t *testing.T) {
	ok := map[string]any{"llms": []any{
		map[string]any{"name": "a", "model": "m", "baseUrl": "https://x/v1", "apiKey": "sk-1",
			"temperature": 1.0, "maxOutputToken": 1024, "maxContextToken": 0, "maxToolIterations": 20,
			"capability": map[string]any{"reasoning": 4.5, "vision": 3.0}},
		// model / baseUrl 空 = 显式继承系统默认（既有语义）→ 允许；
		// maxOutputToken=0 = 显式未设置（0 原样下发 max_tokens，既有语义）→ 允许。
		// capability 缺省 / 显式 0（不具备）均允许；维度可缺省（按 0 处理）。
		map[string]any{"name": "b", "maxOutputToken": 0},
		map[string]any{"name": "c", "capability": map[string]any{"reasoning": 0, "vision": 0}},
		map[string]any{"name": "d", "capability": map[string]any{"reasoning": 5, "vision": 5}},
		map[string]any{"name": "e", "capability": map[string]any{"reasoning": 3.5}},
		// 旧声明式能力（字符串数组）仍可写 → 读侧映射为档位 3.0。
		map[string]any{"name": "f", "capabilities": []any{"reasoning", "vision"}},
		map[string]any{"name": "g", "capabilities": []any{}},
	}}
	if err := ValidateUserConfigPayload(ok); err != nil {
		t.Fatalf("合法载荷不应报错：%v", err)
	}

	cases := []struct {
		name string
		item map[string]any
		want string
	}{
		{"缺 name", map[string]any{"model": "m"}, "name"},
		{"name 超长", map[string]any{"name": strings.Repeat("x", MaxNameChars+1)}, "超长"},
		{"name 控制字符", map[string]any{"name": "a\x00b"}, "控制字符"},
		{"baseUrl 非 http", map[string]any{"name": "c", "baseUrl": "ftp://x"}, "http"},
		{"apiKey 含空白", map[string]any{"name": "d", "apiKey": "sk 1"}, "空白"},
		{"temperature 越界", map[string]any{"name": "e", "temperature": 2.5}, "temperature"},
		{"maxOutputToken 越界", map[string]any{"name": "f", "maxOutputToken": 1}, "maxOutputToken"},
		{"maxToolIterations 越界", map[string]any{"name": "g", "maxToolIterations": MaxLLMIter + 1}, "maxToolIterations"},
		{"capability 旧形态（数值）", map[string]any{"name": "h", "capability": 4.5}, "capability"},
		{"capability.reasoning 越界", map[string]any{"name": "i", "capability": map[string]any{"reasoning": 5.1}}, "capability.reasoning"},
		{"capability.vision 负数", map[string]any{"name": "j", "capability": map[string]any{"vision": -0.1}}, "capability.vision"},
		{"capability 两位小数", map[string]any{"name": "k", "capability": map[string]any{"reasoning": 3.25}}, "capability.reasoning"},
		{"capabilities 非数组", map[string]any{"name": "l", "capabilities": "vision"}, "capabilities"},
		{"capabilities 含非字符串", map[string]any{"name": "m", "capabilities": []any{1}}, "capabilities"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateUserConfigPayload(map[string]any{"llms": []any{c.item}})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("应报错含 %q：%v", c.want, err)
			}
		})
	}

	// 整表重名 → 带 index 定位。
	err := ValidateUserConfigPayload(map[string]any{"llms": []any{
		map[string]any{"name": "dup"}, map[string]any{"name": "dup"},
	}})
	if err == nil || !strings.Contains(err.Error(), "llms[1].name") {
		t.Fatalf("重名应带 index 定位：%v", err)
	}
}

// TestDeriveLLMCapabilities：按 capability 双维度派生旧 capabilities（>0 即含该项）；未写 capability
// 的条目保持原样（不把旧记录清空）。
func TestDeriveLLMCapabilities(t *testing.T) {
	payload := map[string]any{"llms": []any{
		map[string]any{"name": "a", "capability": map[string]any{"reasoning": 4.0, "vision": 2.0}},
		map[string]any{"name": "b", "capability": map[string]any{"reasoning": 3.0, "vision": 0.0}},
		map[string]any{"name": "c", "capability": map[string]any{"reasoning": 0}},
		map[string]any{"name": "d", "capabilities": []any{"vision"}},
	}}
	DeriveLLMCapabilities(payload)

	items := payload["llms"].([]any)
	want := [][]string{{"reasoning", "vision"}, {"reasoning"}, {}}
	for i, w := range want {
		item := items[i].(map[string]any)
		arr, ok := item["capabilities"].([]any)
		if !ok || len(arr) != len(w) {
			t.Fatalf("llms[%d] capabilities=%v want %v", i, item["capabilities"], w)
		}
		for j, name := range w {
			if arr[j] != name {
				t.Fatalf("llms[%d] capabilities=%v want %v", i, arr, w)
			}
		}
	}
	// 未写 capability 的旧条目保持原样（不被清空）。
	legacy, _ := items[3].(map[string]any)["capabilities"].([]any)
	if len(legacy) != 1 || legacy[0] != "vision" {
		t.Fatalf("旧条目 capabilities 不应被改写：%v", legacy)
	}
	// 未写 llms / 形态非法 → 不 panic、不改写。
	DeriveLLMCapabilities(nil)
	DeriveLLMCapabilities(map[string]any{"llms": "oops"})
}

func TestValidateUserConfigPayloadScalars(t *testing.T) {
	if err := ValidateUserConfigPayload(map[string]any{"responseTimeout": 0}); err == nil {
		t.Fatalf("responseTimeout=0 应拒绝（已拍板 >0）")
	}
	if err := ValidateUserConfigPayload(map[string]any{"streamTimeout": 601}); err == nil {
		t.Fatalf("streamTimeout 超上限应拒绝")
	}
	if err := ValidateUserConfigPayload(map[string]any{"responseTimeout": 120, "retryCount": 0}); err != nil {
		t.Fatalf("合法数值不应报错：%v", err)
	}
	if err := ValidateUserConfigPayload(map[string]any{"retryCount": -1}); err == nil {
		t.Fatalf("负数应拒绝")
	}
	if err := ValidateUserConfigPayload(map[string]any{"turnEventLog": "bogus"}); err == nil {
		t.Fatalf("非法枚举应拒绝")
	}
	// 未注册键沿用既有语义（白名单外静默忽略，不报错）。
	if err := ValidateUserConfigPayload(map[string]any{"unknown-key": "x"}); err != nil {
		t.Fatalf("未知键不应报错：%v", err)
	}
}

func TestValidateConfigKV(t *testing.T) {
	dir := t.TempDir()
	if err := ValidateConfigKV("prj-security", map[string]string{
		"k1": `{"dir":"` + strings.ReplaceAll(dir, `\`, `\\`) + `","writable":true}`,
	}); err != nil {
		t.Fatalf("合法安全目录不应报错：%v", err)
	}
	if err := ValidateConfigKV("prj-security", map[string]string{"k1": `{"dir":"","writable":true}`}); err == nil {
		t.Fatalf("空目录应拒绝")
	}
	if err := ValidateConfigKV("prj-security", map[string]string{"k1": `not-json`}); err == nil {
		t.Fatalf("非 JSON 应拒绝")
	}
	if err := ValidateConfigKV("prj-security", map[string]string{"k1": `{"dir":"relative","writable":true}`}); err == nil {
		t.Fatalf("相对路径应拒绝")
	}
	// C1（2026-11-03）：安全配置 = 只写白名单，`writable` 不再是必填（写入只写 {dir}）。
	if err := ValidateConfigKV("prj-security", map[string]string{"k1": `{"dir":"` + strings.ReplaceAll(dir, `\`, `\\`) + `"}`}); err != nil {
		t.Fatalf("只写 {dir} 不应报错：%v", err)
	}

	if err := ValidateConfigKV("prj-config", map[string]string{"timeout_sec": "0"}); err == nil {
		t.Fatalf("timeout_sec=0 应拒绝")
	}
	if err := ValidateConfigKV("prj-config", map[string]string{"timeout_sec": "300"}); err != nil {
		t.Fatalf("合法 timeout_sec 不应报错：%v", err)
	}
	if err := ValidateConfigKV("prj-config", map[string]string{"max_concurrency": "2048"}); err == nil {
		t.Fatalf("max_concurrency 超上限应拒绝")
	}
	if err := ValidateConfigKV("prj-config", map[string]string{"ask_timeout_sec": "-1"}); err == nil {
		t.Fatalf("ask_timeout_sec 负数应拒绝")
	}
	if err := ValidateConfigKV("prj-config", map[string]string{"ask_timeout_sec": "0"}); err != nil {
		t.Fatalf("ask_timeout_sec=0（不超时）应合法：%v", err)
	}
	if err := ValidateConfigKV("prj-config", map[string]string{"project-id": "bad id!"}); err == nil {
		t.Fatalf("非法 project-id 应拒绝")
	}
	if err := ValidateConfigKV("prj-config", map[string]string{"project-id": "0123456789abcdef"}); err != nil {
		t.Fatalf("合法 project-id 不应报错：%v", err)
	}

	if err := ValidateConfigKV("prompt", map[string]string{"summary_prompt": strings.Repeat("x", MaxTextChars+1)}); err == nil {
		t.Fatalf("提示词超长应拒绝")
	}
	if err := ValidateConfigKV("prompt", map[string]string{"summary_prompt": "ok"}); err != nil {
		t.Fatalf("合法提示词不应报错：%v", err)
	}
}
