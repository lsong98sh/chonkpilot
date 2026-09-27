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

// TestLoadDomainToolsMeta：域工具契约 [meta] 被解析并携带（category/async/timeout），
// 使注册载荷能透出为工具 `_meta`（工具异步页据此显示正确模式；见 domainmcp.go registerDomainTools）。
// 用户口径（2026-09-27）：系统工具（category=server）执行硬上限 = 无（timeout=0 或未声明）。
// timeout=0 者 TimeoutSet=true（显式声明 = 无上限，透出 _meta.timeout=0）；未声明者（llm_run）false。
func TestLoadDomainToolsMeta(t *testing.T) {
	defs, err := loadDomainTools()
	if err != nil {
		t.Fatalf("loadDomainTools: %v", err)
	}
	wantAsync := map[string]string{
		"ask_user": "never", "tool_result": "never", "tool_stop": "never", "llm_run": "always",
	}
	// 契约显式声明 timeout=0（= 无上限）者；llm_run 契约未声明 timeout → TimeoutSet=false。
	wantTimeoutSet := map[string]bool{
		"ask_user": true, "tool_result": true, "tool_stop": true, "llm_run": false,
	}
	got := map[string]ToolDef{}
	for _, d := range defs {
		got[d.Name] = d
		if d.Category != "server" {
			t.Errorf("%s: category = %q, 期望 server", d.Name, d.Category)
		}
		if d.Timeout != 0 {
			t.Errorf("%s: timeout = %d, 期望 0（系统工具不单独设执行硬上限）", d.Name, d.Timeout)
		}
		if want, ok := wantTimeoutSet[d.Name]; ok && d.TimeoutSet != want {
			t.Errorf("%s: TimeoutSet = %v, 期望 %v", d.Name, d.TimeoutSet, want)
		}
		if d.AsyncTh != 0 {
			t.Errorf("%s: async-threshold = %d, 期望 0（契约未声明）", d.Name, d.AsyncTh)
		}
	}
	for name, as := range wantAsync {
		d, ok := got[name]
		if !ok {
			t.Errorf("缺少域工具契约 %s", name)
			continue
		}
		if d.Async != as {
			t.Errorf("%s: async = %q, 期望 %q", name, d.Async, as)
		}
	}
}
