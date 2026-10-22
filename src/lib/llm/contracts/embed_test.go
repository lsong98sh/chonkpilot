package contracts

import (
	"sort"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/contractmd"
)

// TestDomainToolContracts 守卫域工具契约集合与可解析性。
//
// 契约已收敛为**单一副本**（本包 embed；server 进程内注册与
// chonkpilot-server-tools.exe 都读这里，不再需要一致性镜像测试）。
// 集合须恰好为这 6 个：新增/改名时 LLM 看到的工具面与 handler 行为必须同批。
func TestDomainToolContracts(t *testing.T) {
	entries, err := FS.ReadDir("tools")
	if err != nil {
		t.Fatalf("read embed contracts: %v", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tool.md") {
			continue
		}
		names = append(names, e.Name())
		raw, err := FS.ReadFile("tools/" + e.Name())
		if err != nil {
			t.Fatalf("read embed %s: %v", e.Name(), err)
		}
		c, err := contractmd.Parse(string(raw))
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		if got, want := strings.TrimSpace(c.Name), strings.TrimSuffix(e.Name(), ".tool.md"); got != want {
			t.Errorf("%s 的 name 字段 = %q，须与文件名一致（%q）", e.Name(), got, want)
		}
	}
	sort.Strings(names)
	want := []string{
		"ask_user.tool.md",
		"next_run.tool.md",
		"subchat_finish.tool.md",
		"subchat_open.tool.md",
		"tool_result.tool.md",
		"tool_stop.tool.md",
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("域工具契约集合 = %v，期望 %v（域工具全部由 core executor 承接，集合须恰好为这 6 个）", names, want)
	}
}
