//go:build windows

package sandbox

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestStart_DeniesWriteOutside 验证：Low IL 子进程写「未打标的普通目录」会被 OS 拒绝。
// t.TempDir() 位于 %TEMP%（Medium IL），是天然的「越界」目标；本用例不修改任何 ACL。
func TestStart_DeniesWriteOutside(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "escaped.txt")

	cmd := exec.Command("cmd", "/c", `echo x > "`+out+`"`)
	cleanup, err := Start(cmd, Spec{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cleanup()

	_ = cmd.Wait()
	if _, statErr := os.Stat(out); statErr == nil {
		t.Fatalf("越界写未被拦截：%s 被创建（Low IL 未生效）", out)
	}
}

// TestStart_AllowsWriteInLowRoot 验证：给允许目录打 Low IL 标签后，Low IL 子进程可写。
// 打标需要 WRITE_OWNER / SeRelabelPrivilege；无权限时跳过（不视为失败）。
func TestStart_AllowsWriteInLowRoot(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureLowIL(dir); err != nil {
		t.Skipf("无法给目录打 Low IL 标签（权限不足）：%v", err)
	}
	out := filepath.Join(dir, "ok.txt")

	cmd := exec.Command("cmd", "/c", `echo x > "`+out+`"`)
	cleanup, err := Start(cmd, Spec{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cleanup()

	_ = cmd.Wait()
	if _, statErr := os.Stat(out); statErr != nil {
		t.Fatalf("允许目录内写入失败（应为可写）：%v", statErr)
	}
}

// TestStart_ReadStillAllowed 验证：Low IL 只限写，读不受限。
func TestStart_ReadStillAllowed(t *testing.T) {
	var buf bytes.Buffer
	cmd := exec.Command("cmd", "/c", `type C:\Windows\win.ini`)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	cleanup, err := Start(cmd, Spec{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cleanup()

	runErr := cmd.Wait()
	if runErr != nil || buf.Len() == 0 {
		t.Fatalf("Low IL 下读取系统文件失败（应为可读）：err=%v out=%q", runErr, buf.String())
	}
}
