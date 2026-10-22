package router

import (
	"os/exec"
	"strings"
	"testing"
)

// TestDepsPureLib 是 MOD-1 M0.5 的 L3 import 断言：router 纯 lib 不得依赖
// mq / msgkeys / gateway / llm（组件两两无 import）。
func TestDepsPureLib(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	banned := map[string]bool{
		"github.com/chonkpilot/chonkpilot-lib/mq":              true,
		"github.com/chonkpilot/chonkpilot-lib/msgkeys":         true,
		"github.com/chonkpilot/chonkpilot-mcp-gateway/gateway": true,
		"github.com/chonkpilot/chonkpilot-llm":                 true,
	}
	for _, dep := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if banned[dep] {
			t.Fatalf("router must not import %s (MOD-1 component boundary)", dep)
		}
	}
}
