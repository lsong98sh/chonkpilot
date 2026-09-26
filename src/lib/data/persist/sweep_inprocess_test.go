//go:build !split

// 合并单进程形态（**默认构建**）回归：实例不发布心跳（无发布方），故**不得**起心跳超时扫描
// goroutine、不得调用 sweepStale——实例绑定只在显式 instance-exit 时释放
// （分离形态用例见 sweep_split_test.go，`-tags split`）。
package persist

import (
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
)

// TestNoStaleSweepInMergedForm：默认构建 Start 后无扫描 goroutine（sweepStop = nil）；
// 即使实例心跳陈旧（远超 90s 阈值），等待数个"若存在扫描即会触发"的窗口后绑定依然保留。
func TestNoStaleSweepInMergedForm(t *testing.T) {
	// 数据目录先于服务创建：清理顺序 LIFO → data.Reset（关连接）先于 TempDir RemoveAll 执行。
	wd, dd := t.TempDir(), t.TempDir()
	s := newSweepTestService(t)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(s.Stop)

	if s.sweepStop != nil {
		t.Fatal("合并单进程形态不应起心跳超时扫描 goroutine")
	}

	s.onInstanceRegister(instanceRegister, instancePayload("i-merged", wd, dd))
	if _, err := s.prjUsrDB("i-merged"); err != nil {
		t.Fatalf("prjUsrDB: %v", err)
	}
	backdate(t, s, "i-merged", time.Hour) // 明显陈旧（远超分离形态 90s 阈值）

	time.Sleep(200 * time.Millisecond) // 无扫描 → 不作超时判定

	if _, ok := s.lookupInstance("i-merged"); !ok {
		t.Fatal("合并单进程形态不应超时清理实例")
	}
	if _, err := data.PrjUsr("i-merged"); err != nil {
		t.Fatalf("合并单进程形态不应释放数据层绑定: %v", err)
	}
}
