package chat

import (
	"os/exec"
	"strings"
	"testing"
)

// TestDepsZeroMQ 是 MOD-1 的 L3 import 断言：chat 内核只依赖标准库，
// 不得出现 mq / msgkeys / router / gateway / data / facade / llm。
func TestDepsZeroMQ(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	banned := []string{
		"github.com/chonkpilot/chonkpilot-lib/mq",
		"github.com/chonkpilot/chonkpilot-lib/msgkeys",
		"github.com/chonkpilot/chonkpilot-lib/data",
		"github.com/chonkpilot/chonkpilot-lib/facade",
		"github.com/chonkpilot/chonkpilot-router",
		"github.com/chonkpilot/chonkpilot-mcp-gateway",
		"github.com/chonkpilot/chonkpilot-llm",
	}
	for _, dep := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		for _, b := range banned {
			if dep == b || strings.HasPrefix(dep, b+"/") {
				t.Fatalf("chat kernel must not import %s (MOD-1 component boundary)", dep)
			}
		}
	}
}
