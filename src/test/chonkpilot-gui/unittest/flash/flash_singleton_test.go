// flash_singleton_test.go — flash 窗口单例的**具名窗口类名**推导（`flash.WindowClassName`）
// 验证（🆕 2026-10-10；19 §8.11 / 62 §3A）。
//
// 口径：类名由 (user, workDir) 稳定哈希 —— 同用户同项目 → 同名（跨进程 FindWindow 判重），
// Windows 路径大小写不敏感（统一小写 + Clean）。
package flash_test

import (
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-gui/flash"
)

// TestWindowClassNameStable：同 (user, workDir) → 同名；不同输入 → 不同名。
func TestWindowClassNameStable(t *testing.T) {
	a := flash.WindowClassName("alice", `E:\proj\a`)
	b := flash.WindowClassName("alice", `E:\proj\a`)
	if a != b {
		t.Fatalf("同名输入应得同名类：%q vs %q", a, b)
	}
	if c := flash.WindowClassName("alice", `E:\proj\b`); c == a {
		t.Fatal("不同项目应得不同类名")
	}
	if d := flash.WindowClassName("bob", `E:\proj\a`); d == a {
		t.Fatal("不同用户应得不同类名")
	}
}

// TestWindowClassNameCaseInsensitive：Windows 路径大小写不敏感 + Clean 归一。
func TestWindowClassNameCaseInsensitive(t *testing.T) {
	a := flash.WindowClassName("alice", `E:\Proj\A`)
	b := flash.WindowClassName("alice", `e:\proj\a`)
	if a != b {
		t.Fatalf("大小写差异应得同名类：%q vs %q", a, b)
	}
	c := flash.WindowClassName("alice", `E:\proj\a\`)
	if c != b {
		t.Fatalf("尾随分隔符应 Clean 归一：%q vs %q", c, b)
	}
}

// TestWindowClassNamePrefix：类名带 flash 专用前缀（与 server 托盘类区分）。
func TestWindowClassNamePrefix(t *testing.T) {
	if got := flash.WindowClassName("alice", `E:\p`); !strings.HasPrefix(got, "ChonkPilotFlash_") {
		t.Fatalf("类名应带 ChonkPilotFlash_ 前缀，得 %q", got)
	}
}
