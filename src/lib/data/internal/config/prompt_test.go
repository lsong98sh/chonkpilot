// prompt_test.go — prompt 域文件化键解析白盒（A-37：记忆类别名合法性校验，防路径穿越）。
package config

import "testing"

// TestSystemDocKindCategoryValidation systemDocKind 必须拒绝含路径穿越 / 非法字符 / 保留设备名 /
// 空类别的类别名（A-37：非法类别名拼进 `memory/<类别名>.md` 会越过 system 目录读写）；
// summary 键与合法（含中文）类别名照常解析。
func TestSystemDocKindCategoryValidation(t *testing.T) {
	for _, key := range []string{
		"memory_prompt...",           // 类别名 = `..`
		"memory_prompt.../../secret", // 分隔符 + `..`
		"memory_prompt.a/b",          // 含 `/`
		`memory_prompt.a\b`,          // 含 `\`
		"memory_prompt.",             // 空类别名
		"memory_prompt. .x",          // 含空白
		"memory_prompt.CON",          // Windows 保留设备名
	} {
		if kind, ok := systemDocKind(key); ok {
			t.Fatalf("非法类别名键 %q 应被拒，却得 kind=%q", key, kind)
		}
	}
	for key, want := range map[string]string{
		"summary_prompt":     "summary",
		"memory_prompt.用户偏好": "memory/用户偏好",
		"memory_prompt.项目概要": "memory/项目概要",
	} {
		if kind, ok := systemDocKind(key); !ok || kind != want {
			t.Fatalf("systemDocKind(%q) = (%q, %v)，want (%q, true)", key, kind, ok, want)
		}
	}
}
