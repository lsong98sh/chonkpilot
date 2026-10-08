// workdirlock_test.go — I-74：同 work-dir 单实例占用校验（桥侧「选目录 / 最近目录」入口）。
//
// 口径来源 [42 §2 (240)]：同 work-dir 只允许单实例；已打开 → **明确报错**（不再被 bbolt 独占
// flock 卡到超时）。此处只驱动**占用命中**分支（不 spawn 新进程 → 可在单测内跑通）。
package bridge_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/lockfile"
)

// TestDirOpenRejectsBusyWorkDir：目标目录已被占用 → gui.dir.open 明确拒绝（原因含 "workdir busy"）。
func TestDirOpenRejectsBusyWorkDir(t *testing.T) {
	dir := t.TempDir()
	l, err := lockfile.AcquireWorkDir(dir)
	if err != nil {
		t.Fatalf("占用 work-dir 失败: %v", err)
	}
	defer l.Release()

	br, bus := newTestBridge(t, func(string) {})
	defer bus.Close()

	payload, _ := json.Marshal(map[string]string{"path": dir})
	_, errs := br.PublishEvent("gui.dir.open", string(payload))
	if len(errs) == 0 {
		t.Fatal("占用中的 work-dir 应被拒绝（I-74）")
	}
	if !strings.Contains(errs[0].Error(), "workdir busy") {
		t.Fatalf("拒绝原因应含 workdir busy，got %v", errs[0])
	}
}

// TestDirOpenBlockedByRunningInstance：模拟「另一个实例已打开该 work-dir」的最常见路径
// （GUI 启动取锁 → 用户从最近目录再选同一目录）——锁释放后同目录恢复可用。
func TestDirOpenBlockedByRunningInstance(t *testing.T) {
	dir := t.TempDir()
	l, err := lockfile.AcquireWorkDir(dir)
	if err != nil {
		t.Fatalf("占用 work-dir 失败: %v", err)
	}
	if lockfile.WorkDirFree(dir) {
		t.Fatal("占用期间 WorkDirFree 应为 false")
	}
	l.Release()
	if !lockfile.WorkDirFree(dir) {
		t.Fatal("释放后 WorkDirFree 应为 true")
	}
}
