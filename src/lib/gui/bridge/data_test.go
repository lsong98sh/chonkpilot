// usr 用户配置兜底默认值白盒：`defaultScenario` 类型与 persist 一致（string，空 = 未配置）。
package bridge

import "testing"

// TestDefaultUserConfigDefaultScenarioString：桥兜底默认 `defaultScenario` 必须是 **string**
// （场景目录名引用），与 persist `userConfigSystemDefaults` 的 "" 对齐（2026-09-19 修正原 int 0）。
// 前端 ChatPanel.vue 以 `(uc.defaultScenario) || ”` 消费并按字符串与场景 id 比较 → 类型须为 string。
func TestDefaultUserConfigDefaultScenarioString(t *testing.T) {
	cfg := defaultUserConfig()
	v, ok := cfg["defaultScenario"].(string)
	if !ok {
		t.Fatalf("defaultScenario 应为 string，实际 %T", cfg["defaultScenario"])
	}
	if v != "" {
		t.Fatalf("defaultScenario 默认应为空串，实际 %q", v)
	}
}
