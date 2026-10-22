package gui

import "testing"

// TestClassifyGUIAction：插件面 GUI 动作分类（BRIDGE-2 ⑤）——
// backend（gui.* / data-*）走 PublishEvent；纯前端事件走 EmitFrontend；未知动作明确报错。
func TestClassifyGUIAction(t *testing.T) {
	cases := []struct {
		action string
		mode   string
		err    bool
	}{
		{"gui.window.status", "backend", false},
		{"gui.window.resize", "backend", false},
		{"gui.ui.save", "backend", false},
		{"data-prj-config-save", "backend", false},
		{"theme-select", "event", false},
		{"lang-select", "event", false},
		{"scenario-open", "event", false},
		{"preview-tab-open", "event", false},
		{"file-open", "event", false},
		{"session-select", "event", false},
		{"lf-layout-toggle-drawer", "event", false},
		{"login-in", "", true},     // 认证域不开放
		{"no-such-action", "", true},
	}
	for _, tc := range cases {
		mode, err := classifyGUIAction(tc.action)
		if tc.err {
			if err == nil {
				t.Errorf("%s: 应报错（未知动作不得静默）", tc.action)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: 意外错误 %v", tc.action, err)
		}
		if mode != tc.mode {
			t.Errorf("%s: mode=%q，期望 %q", tc.action, mode, tc.mode)
		}
	}
}
