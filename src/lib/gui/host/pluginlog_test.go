//go:build windows

package host

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
)

// PLUGIN-LOG-1：镜像路径 = prjusr 数据根 `logs/plugins/<id>.log`；只对已打开项目（`.chonkpilot` 存在）。
func TestPluginProjectLogPath(t *testing.T) {
	wd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wd, ".chonkpilot"), 0o755); err != nil {
		t.Fatalf("mkdir .chonkpilot: %v", err)
	}
	usr, err := data.PrjUsrDir(wd, "")
	if err != nil {
		t.Fatalf("PrjUsrDir: %v", err)
	}
	want := filepath.Join(usr, "logs", "plugins", "codegraph.log")
	if got := pluginProjectLogPath(wd, "codegraph", ""); got != want {
		t.Fatalf("镜像路径 = %q，期望 %q", got, want)
	}

	// 未打开项目（无 .chonkpilot）→ 不镜像
	if got := pluginProjectLogPath(t.TempDir(), "codegraph", ""); got != "" {
		t.Fatalf("未打开项目不应镜像，实际 %q", got)
	}
	// 相对路径 / 空插件 id → 不镜像
	if got := pluginProjectLogPath("relative/proj", "codegraph", ""); got != "" {
		t.Fatalf("相对路径不应镜像，实际 %q", got)
	}
	if got := pluginProjectLogPath(wd, "", ""); got != "" {
		t.Fatalf("空插件 id 不应镜像，实际 %q", got)
	}
}
