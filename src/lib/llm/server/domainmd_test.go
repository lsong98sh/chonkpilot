package server

import "testing"

// 域工具契约 embed 加载：4 个齐全（阶段 1 收敛后终态 = ask_user/llm_run/tool_stop/tool_result）
// + parameters 为合法 JSON schema。
func TestLoadDomainTools(t *testing.T) {
	defs, err := loadDomainTools()
	if err != nil {
		t.Fatalf("loadDomainTools: %v", err)
	}
	want := map[string]bool{
		"ask_user": false, "llm_run": false, "tool_stop": false, "tool_result": false,
	}
	for _, d := range defs {
		if _, ok := want[d.Name]; !ok {
			t.Errorf("意外工具 %q", d.Name)
			continue
		}
		want[d.Name] = true
		if d.Description == "" {
			t.Errorf("%s: description 为空", d.Name)
		}
		if d.Parameters == nil {
			t.Errorf("%s: parameters 缺失", d.Name)
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("缺少域工具契约 %s", name)
		}
	}
}
