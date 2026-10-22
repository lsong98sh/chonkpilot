package mcpgateway

import (
	"os/exec"
	"strings"
	"testing"
)

// TestKernelDepsZeroMQ 是 MOD-1 M0.5 的 L3 import 断言：gateway **内核包**（gateway 目录）
// 的包级依赖图不得出现 mq / msgkeys（宿主适配外壳 internal/facade 与 mqadapter 不在此断言内）。
func TestKernelDepsZeroMQ(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for _, dep := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		switch dep {
		case "github.com/chonkpilot/chonkpilot-lib/mq",
			"github.com/chonkpilot/chonkpilot-lib/msgkeys":
			t.Fatalf("gateway kernel must not import %s (MOD-1 zero-MQ)", dep)
		}
	}
}
