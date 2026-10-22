// sandbox_test.go — chonkpilot-mcp-tools 的沙箱规则单测（L2，工程目录
// chonkpilot-test/chonkpilot-mcp-tools/unittest）。
//
// 覆盖 `sandbox.WritableRoots` 从 `sandbox.Rule` 生成「允许写目录」集的结果：
// TrimSpace、跳过空 Dir、去重、保序。
// 注：应用层路径校验（env `CHONKPILOT_SANDBOX` + agentbox 拦截）已废弃——进程级沙箱
// 已移到 gateway（spawn 时以 Low IL 施加），不在 mcp-tools 单测范围内。
package chonkpilotmcptools

import (
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/sandbox"
)

// TestWritableRoots：只取可写目录，去空白、跳空、去重、保序。
func TestWritableRoots(t *testing.T) {
	rules := []sandbox.Rule{
		{Dir: `C:\work\proj`, Writable: true},
		{Dir: `C:\work\ro`, Writable: false},
		{Dir: "   ", Writable: true},
		{Dir: `  C:\work\proj  `, Writable: true},
		{Dir: `C:\work\other`, Writable: true},
	}
	got := sandbox.WritableRoots(rules)
	want := []string{`C:\work\proj`, `C:\work\other`}
	if len(got) != len(want) {
		t.Fatalf("WritableRoots = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("WritableRoots = %v, want %v", got, want)
		}
	}

	if roots := sandbox.WritableRoots(nil); len(roots) != 0 {
		t.Fatalf("空规则集应返回空目录集，got %v", roots)
	}
}
