//go:build !split

// 合并单进程形态（**默认构建**）回归：实例心跳不发布（无发布方），故**不得**起心跳超时
// 扫描 goroutine、不得调用 instanceManager.Sweep——GUI 启动后实例绑定与工具面必须保持
// （分离形态用例见 sweep_split_test.go，`-tags split`）。
package server

import (
	"testing"
	"time"
)

// TestNoInstanceSweepInMergedForm：默认构建 Start 后无扫描 goroutine（sweepStop = nil）；
// 等待数个"若存在扫描即会触发"的窗口后，实例视图与 capability dir 节点均不变。
func TestNoInstanceSweepInMergedForm(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServerMCP(t, llm)

	if s.sweepStop != nil {
		t.Fatal("合并单进程形态不应起心跳超时扫描 goroutine")
	}

	s.im.HandleRegister(jb(map[string]any{
		"instance_id": "ins-merged", "client_type": "unittest", "work_dir": testWorkDir,
	}))
	s.onInstanceRegister("instance-register", jb(map[string]any{
		"instance_id": "ins-merged", "client_type": "unittest", "work_dir": testWorkDir,
	}))
	time.Sleep(200 * time.Millisecond) // 无扫描 → 陈旧/新鲜判定都不存在

	if _, ok := s.im.Lookup("ins-merged"); !ok {
		t.Fatal("合并单进程形态不应超时清理实例")
	}
	if _, ok := s.capWorkDirs["ins-merged"]; !ok {
		t.Fatal("合并单进程形态实例注册态应保持（无超时清理）")
	}
}
