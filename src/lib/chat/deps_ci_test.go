package chat

import (
	"os/exec"
	"strings"
	"testing"
)

// TestDepsCI 是 MOD-1 M8-C4 的 CI 级 L3 断言（`go list -deps ./...`，含子包 `chatest`）：
// chat 组件包级依赖图不得出现 mq / msgkeys / router / gateway / data / facade / llm。
//
// 与源码级 `deps_test.go`（只看当前包）互补：本断言按禁前缀覆盖 `./...` 全部子包。
// `go` 不在 PATH（测试环境裁剪）→ 跳过并打印原因，不伪造通过。
func TestDepsCI(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go 不在 PATH，跳过依赖断言（不伪造通过）：%v", err)
	}
	out, err := exec.Command("go", "list", "-deps", "./...").Output()
	if err != nil {
		t.Fatalf("go list -deps ./...: %v", err)
	}
	banned := []string{
		"chonkpilot-lib/mq",
		"msgkeys",
		"chonkpilot-router",
		"chonkpilot-mcp-gateway",
		"chonkpilot-data",
		"facade",
		"chonkpilot-llm",
	}
	for _, dep := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		for _, b := range banned {
			if strings.Contains(dep, b) {
				t.Fatalf("chat 组件不得依赖 %s（命中禁前缀 %q；MOD-1 C1/C3 L3 边界）", dep, b)
			}
		}
	}
}
