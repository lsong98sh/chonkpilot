// promptvars_test.go — gui.prompt-vars（OP-12）只读面的形状守卫：
// 分组目录的 id/键集/dslOnly 与「单一数据源 = 后端常量」一致；分派入口返回 {groups}。
package bridge

import (
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// TestPromptVarGroupsShape：三组（toolchain/path/env）齐备；键集与常量源一致。
func TestPromptVarGroupsShape(t *testing.T) {
	groups := promptVarGroups()
	if len(groups) != 3 {
		t.Fatalf("应有三组变量（toolchain/path/env），实际 %d：%+v", len(groups), groups)
	}
	byID := map[string]promptVarGroup{}
	for _, g := range groups {
		byID[g.ID] = g
	}
	for _, id := range []string{"toolchain", "path", "env"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("缺少分组 %q", id)
		}
	}

	// toolchain 组：逐项对应工具链探测候选清单（7 项），key = {{toolchain.<id>}}。
	tc := byID["toolchain"]
	if len(tc.Items) != len(toolchainCandidates) {
		t.Fatalf("toolchain 项数 %d 应等于候选清单 %d", len(tc.Items), len(toolchainCandidates))
	}
	gotKeys := map[string]bool{}
	for _, it := range tc.Items {
		gotKeys[it.Key] = true
		if it.DSLOnly {
			t.Fatalf("toolchain 项不应为 dslOnly：%+v", it)
		}
	}
	for _, id := range []string{"java", "python", "node", "go", "rust", "c", "chrome"} {
		if !gotKeys["{{toolchain."+id+"}}"] {
			t.Fatalf("toolchain 缺键 {{toolchain.%s}}：%+v", id, tc.Items)
		}
	}

	// path 组：四种根路径。
	p := byID["path"]
	if len(p.Items) != 4 {
		t.Fatalf("path 项数应为 4，实际 %d", len(p.Items))
	}
	for _, k := range []string{"{{path.exeDir}}", "{{path.userDir}}", "{{path.dataDir}}", "{{path.workDir}}"} {
		found := false
		for _, it := range p.Items {
			if it.Key == k {
				found = true
			}
		}
		if !found {
			t.Fatalf("path 缺键 %s", k)
		}
	}

	// env 组：CHONKPILOT_* 五项，全部 dslOnly=true。
	e := byID["env"]
	if len(e.Items) != 5 {
		t.Fatalf("env 项数应为 5，实际 %d", len(e.Items))
	}
	for _, it := range e.Items {
		if !it.DSLOnly {
			t.Fatalf("env 项必须 dslOnly=true：%+v", it)
		}
	}
}

// TestGuiDoPromptVars：分派入口 guiDo("prompt-vars") 返回 {groups:[...]}（本地只读面，无实例依赖）。
func TestGuiDoPromptVars(t *testing.T) {
	if msgkeys.TopicGuiPromptVars != "gui.prompt-vars" {
		t.Fatalf("主题常量不一致：%q", msgkeys.TopicGuiPromptVars)
	}
	b := &Bridge{}
	result, errs := b.guiDo("prompt-vars", nil)
	if len(errs) != 0 {
		t.Fatalf("gui.prompt-vars 不应报错：%v", errs)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("结果应为对象，实际 %T", result)
	}
	arr, ok := m["groups"].([]any)
	if !ok || len(arr) != 3 {
		t.Fatalf("groups 应为 3 组数组，实际 %T（%v）", m["groups"], m["groups"])
	}
}
