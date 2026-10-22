// GATEWAY-2 L5：dir 节点（user / project / prjusr capability）在 gateway 侧是普通 stdio 下游
// `chonkpilot-core-mcp-server --stdio --root <dir>`。测试环境（go test 临时目录）没有产物 exe，
// 故由 TestMain 构建到临时目录并经 `CHONKPILOT_CORE_MCP` 指定（见 coreMCPPath 覆盖）。
// 构建失败不中止测试：依赖 dir 节点的用例会自行失败并带原因日志。
package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	binDir, err := os.MkdirTemp("", "ck-core-mcp-")
	if err == nil {
		name := "chonkpilot-core-mcp-server"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		exe := filepath.Join(binDir, name)
		cmd := exec.Command("go", "build", "-o", exe,
			"github.com/chonkpilot/chonkpilot-mcp-server/cmd/core-mcp-server")
		cmd.Env = os.Environ()
		if out, berr := cmd.CombinedOutput(); berr == nil {
			_ = os.Setenv("CHONKPILOT_CORE_MCP", exe)
		} else {
			println("[test] core-mcp-server 构建失败（dir 节点用例将失败）: " + string(out))
		}
	}
	code := m.Run()
	// 仅清理自己创建在系统临时目录下的子目录（防误删；路径来自 MkdirTemp）。
	if err == nil && strings.HasPrefix(binDir, filepath.Clean(os.TempDir())) {
		_ = os.RemoveAll(binDir)
	}
	os.Exit(code)
}
