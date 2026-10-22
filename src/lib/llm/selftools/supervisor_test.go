package selftools

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestSupervisorGivesUpAfterConsecutiveFailures：连续 3 次重启失败 → 放弃监管。
func TestSupervisorGivesUpAfterConsecutiveFailures(t *testing.T) {
	oldSpawn, oldBase := spawnFn, restartBaseDelay
	defer func() { spawnFn, restartBaseDelay = oldSpawn, oldBase }()
	restartBaseDelay = 5 * time.Millisecond
	var spawnCalls int
	spawnFn = func(string, string, io.Writer) (*exec.Cmd, error) {
		spawnCalls++
		return nil, errors.New("boom")
	}

	var logs []string
	s := &Supervisor{
		addr: "x", root: "r", logf: func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) },
		stopCh: make(chan struct{}), doneCh: make(chan struct{}),
	}
	go s.supervise(nil)
	select {
	case <-s.doneCh:
	case <-time.After(3 * time.Second):
		t.Fatal("监督未在连续失败后放弃（疑似无限重启）")
	}
	if spawnCalls != restartMaxFails {
		t.Fatalf("重启尝试次数 = %d，期望 %d", spawnCalls, restartMaxFails)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "放弃监管") {
		t.Fatalf("缺少放弃监管日志: %v", logs)
	}
}

// TestSupervisorStopNoRestart：Stop 后监督立即退出、不再重启。
func TestSupervisorStopNoRestart(t *testing.T) {
	oldSpawn, oldBase := spawnFn, restartBaseDelay
	defer func() { spawnFn, restartBaseDelay = oldSpawn, oldBase }()
	restartBaseDelay = time.Hour // 若真等待则会超时失败
	var spawnCalls int
	spawnFn = func(string, string, io.Writer) (*exec.Cmd, error) {
		spawnCalls++
		return nil, errors.New("boom")
	}

	s := &Supervisor{
		addr: "x", root: "r", logf: func(string, ...any) {},
		stopCh: make(chan struct{}), doneCh: make(chan struct{}),
	}
	go s.supervise(nil)
	time.Sleep(20 * time.Millisecond) // 进入退避等待
	s.Stop()
	if spawnCalls != 0 {
		t.Fatalf("Stop 后不应再尝试重启（spawn 次数=%d）", spawnCalls)
	}
}
