// procsingleton_test.go — server 进程单例的**纯逻辑**验证（🆕 2026-10-10；19 §8.11 / 62 §3A）。
//
// 口径：命名互斥体名由 user 稳定哈希（同用户同名 → 跨进程判重；`Global\` 前缀 = 跨登录会话全局 +
// 按用户隔离，R7-B-3）；`ShouldExitAsDuplicate(already)` 决定第二实例是否退出。
// Windows 系统调用桩（`Acquire`）不在此测（靠 `go build` / `go vet`）。
package procsingleton_test

import (
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/procsingleton"
)

// TestNameStable：同 user → 同名；不同 user → 不同名。
func TestNameStable(t *testing.T) {
	a := procsingleton.Name("alice")
	if b := procsingleton.Name("alice"); a != b {
		t.Fatalf("同用户应得同名：%q vs %q", a, b)
	}
	if c := procsingleton.Name("bob"); c == a {
		t.Fatal("不同用户应得不同名")
	}
}

// TestNamePrefix：名带 `Global\ChonkPilotServer_` 前缀（命名空间 + 与其它进程对象区分）。
func TestNamePrefix(t *testing.T) {
	if got := procsingleton.Name("alice"); !strings.HasPrefix(got, `Global\ChonkPilotServer_`) {
		t.Fatalf("名应带 Global\\ChonkPilotServer_ 前缀，得 %q", got)
	}
}

// TestNameNormalized：大小写 / 首尾空白归一（同用户不同书写 → 同名）。
func TestNameNormalized(t *testing.T) {
	a := procsingleton.Name(" Alice ")
	b := procsingleton.Name("alice")
	if a != b {
		t.Fatalf("大小写 / 空白应归一：%q vs %q", a, b)
	}
}

// TestNameEmpty：空 user 仍得确定名（不 panic，兜底单例）。
func TestNameEmpty(t *testing.T) {
	if got := procsingleton.Name(""); !strings.HasPrefix(got, `Global\ChonkPilotServer_`) {
		t.Fatalf("空 user 应得带前缀的确定名，得 %q", got)
	}
}

// TestShouldExitAsDuplicate：已存在 → 退出；不存在 → 不退出。
func TestShouldExitAsDuplicate(t *testing.T) {
	if !procsingleton.ShouldExitAsDuplicate(true) {
		t.Fatal("已存在 → 应退出")
	}
	if procsingleton.ShouldExitAsDuplicate(false) {
		t.Fatal("不存在 → 不应退出")
	}
}
