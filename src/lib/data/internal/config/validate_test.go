// INPUTVAL-1 写侧校验纯函数单测（L1）。
package config

import (
	"strings"
	"testing"
)

func TestValidateUserConfigPayloadLLMs(t *testing.T) {
	ok := map[string]any{"llms": []any{
		map[string]any{"name": "a", "model": "m", "baseUrl": "https://x/v1", "apiKey": "sk-1",
			"temperature": 1.0, "maxOutputToken": 1024, "maxContextToken": 0, "maxToolIterations": 20},
		// model / baseUrl 空 = 显式继承系统默认（既有语义）→ 允许；
		// maxOutputToken=0 = 显式未设置（0 原样下发 max_tokens，既有语义）→ 允许。
		map[string]any{"name": "b", "maxOutputToken": 0},
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
	if err := ValidateConfigKV("prj-security", map[string]string{"k1": `{"dir":"` + strings.ReplaceAll(dir, `\`, `\\`) + `"}`}); err == nil {
		t.Fatalf("缺 writable 应拒绝")
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
