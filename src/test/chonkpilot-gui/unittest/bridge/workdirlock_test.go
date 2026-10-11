// workdirlock_test.go — I-74：同 work-dir 单实例占用校验（桥侧「选目录 / 最近目录」入口）。
//
// 口径来源 [42 §2 (240)]：同 work-dir 只允许单实例；已打开 → **明确报错**（不再被 bbolt 独占
// flock 卡到超时）。此处只驱动**占用命中**分支（不 spawn 新进程 → 可在单测内跑通）。
//
// 锁 = **prjusr 单例锁**（`<work_dir>/.chonkpilot/chonkpilot.lock`）：gui.dir.open 预检
// 与桌面单体 / flash 持有的是同一把锁，故此处用 flash.AcquireProject 占锁（同源），
// 不再用 lockfile.AcquireWorkDir（workdir 锁，另一锁文件 → 预检永不命中）。
package bridge_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-gui/flash"
)

// TestDirOpenRejectsBusyWorkDir：目标目录已被占用（prjusr 锁）→ gui.dir.open 明确拒绝
// （原因含 "workdir busy"）。
func TestDirOpenRejectsBusyWorkDir(t *testing.T) {
	dir := t.TempDir()
	l, busy, err := flash.AcquireProject(dir) // prjusr 单例锁（与 gui.dir.open 预检同源）
	if err != nil || busy {
		t.Fatalf("占用 work-dir(prjusr 锁) 失败: busy=%v err=%v", busy, err)
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
// （GUI 启动取 prjusr 锁 → 用户从最近目录再选同一目录）——锁释放后同目录恢复可用。
func TestDirOpenBlockedByRunningInstance(t *testing.T) {
	dir := t.TempDir()
	l, busy, err := flash.AcquireProject(dir) // GUI/flash 持的 prjusr 单例锁
	if err != nil || busy {
		t.Fatalf("占用 work-dir(prjusr 锁) 失败: busy=%v err=%v", busy, err)
	}
	if flash.WorkDirFree(dir) {
		t.Fatal("占用期间 WorkDirFree 应为 false")
	}
	l.Release()
	if !flash.WorkDirFree(dir) {
		t.Fatal("释放后 WorkDirFree 应为 true")
	}
}
