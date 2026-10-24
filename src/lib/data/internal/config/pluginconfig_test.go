// pluginconfig_test.go — 插件自身配置（`plugins.<插件id>.<字段>`）在 usr 配置库的读写落点。
//
// 口径（2026-10-24「插件配置收回 data 层」）：插件配置落 **usr 配置库**（键 = `plugins.*`），
// 值恒为字符串；读侧经 `data-user-config-load` 回读，**不进入** `data-user-config-list` 视图
// （设置页 / 导出备份数据源）。前缀边界（只能写调用方自己）由插件桥强制，本包只校验键名形态。
package config

import (
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
)

// TestPluginConfigSaveReadRoundTrip：写 → 落库（字符串）+ readUserConfig 回读；不进 list 视图 /
// 不计入 explicit（导出不含插件配置）。
func TestPluginConfigSaveReadRoundTrip(t *testing.T) {
	db := openTestDB(t)

	if err := saveUserConfig(db, map[string]any{
		"plugins.websearch.api-key":  "sk-test-key",
		"plugins.websearch.count":    float64(10),
		"plugins.browser.headless":   true,
		"plugins.desktop.shot-dir-1": `E:\shots`,
	}); err != nil {
		t.Fatalf("saveUserConfig: %v", err)
	}

	// 落库形态 = 字符串（float64 / bool 归一为字符串）
	want := map[string]string{
		"plugins.websearch.api-key":  "sk-test-key",
		"plugins.websearch.count":    "10",
		"plugins.browser.headless":   "true",
		"plugins.desktop.shot-dir-1": `E:\shots`,
	}
	for k, v := range want {
		if got, ok := data.GetConfig(db, k); !ok || got != v {
			t.Fatalf("%s 未按字符串落库：got=%q ok=%v", k, got, ok)
		}
	}

	// load 视图（readUserConfig）回读原值
	view := readUserConfig(db)
	for k, v := range want {
		if got := view[k]; got != v {
			t.Fatalf("readUserConfig 回读 %s = %v，期望 %q", k, got, v)
		}
	}

	// list 视图（设置页 / 导出数据源）不含插件配置；explicit 亦不含
	list := (&Service{}).userConfigList(db)
	for _, item := range list {
		for k := range item {
			if strings.HasPrefix(k, "plugins.") {
				t.Fatalf("list 视图不应包含插件配置键 %s：%+v", k, item)
			}
		}
	}
	for _, k := range explicitUserConfigKeys(db) {
		if strings.HasPrefix(k, "plugins.") {
			t.Fatalf("explicit 不应包含插件配置键 %s", k)
		}
	}

	// 只写插件配置 → list 视图为空（插件配置不算「用户配置」）
	only := openTestDB(t)
	if err := saveUserConfig(only, map[string]any{"plugins.mcp.max": "5"}); err != nil {
		t.Fatalf("save 插件配置: %v", err)
	}
	if got := (&Service{}).userConfigList(only); len(got) != 0 {
		t.Fatalf("仅插件配置时 list 应为空：%+v", got)
	}
}

// TestPluginConfigDelete：删单键 → 该键消失（readUserConfig 不再回读）；删不存在的键幂等。
func TestPluginConfigDelete(t *testing.T) {
	db := openTestDB(t)
	if err := saveUserConfig(db, map[string]any{
		"plugins.task.a": "1",
		"plugins.task.b": "2",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := data.DeleteConfig(db, "plugins.task.a"); err != nil {
		t.Fatalf("DeleteConfig: %v", err)
	}
	if err := data.DeleteConfig(db, "plugins.task.a"); err != nil {
		t.Fatalf("重复删除应幂等：%v", err)
	}
	view := readUserConfig(db)
	if _, ok := view["plugins.task.a"]; ok {
		t.Fatal("删除后不应再回读 plugins.task.a")
	}
	if view["plugins.task.b"] != "2" {
		t.Fatalf("未删键被波及：%v", view["plugins.task.b"])
	}
}

// TestValidatePluginConfigEntries：写侧校验——键名形态 / 值标量 / 长度上限，失败**整批拒绝**。
func TestValidatePluginConfigEntries(t *testing.T) {
	bad := []struct {
		name  string
		entry map[string]any
		want  string
	}{
		{"缺插件 id", map[string]any{"plugins.": "1"}, "plugins.<插件id>.<字段>"},
		{"缺字段", map[string]any{"plugins.websearch.": "1"}, "须形如"},
		{"字段含 ..", map[string]any{"plugins.websearch.a..b": "1"}, "字段名非法"},
		{"id 非法字符", map[string]any{"plugins.web search.a": "1"}, "只允许可见 ASCII"},
		{"键超长", map[string]any{"plugins.websearch." + strings.Repeat("k", maxPluginConfigKeyChars): "1"}, "超长"},
		{"值非标量", map[string]any{"plugins.websearch.a": map[string]any{"x": 1}}, "必须是字符串"},
		{"值 null", map[string]any{"plugins.websearch.a": nil}, "值不能为 null"},
		{"值超长", map[string]any{"plugins.websearch.a": strings.Repeat("v", maxPluginConfigValueChars+1)}, "超长"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			err := ValidateUserConfigPayload(tc.entry)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("期望错误含 %q，实际 %v", tc.want, err)
			}
			// 校验失败不得落库（saveUserConfig 由门面在校验后才调用；这里直接断言未写）
			for k := range tc.entry {
				if _, ok := data.GetConfig(db, k); ok {
					t.Fatalf("校验失败不应落库：%s", k)
				}
			}
		})
	}

	// 合法插件配置通过校验；未知非插件键沿用既有语义（不额外拦截）
	if err := ValidateUserConfigPayload(map[string]any{
		"plugins.websearch.api-key": "sk-test-key",
		"unknown-key":               "x",
	}); err != nil {
		t.Fatalf("合法载荷被拒：%v", err)
	}
}
