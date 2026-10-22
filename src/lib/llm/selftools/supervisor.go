// supervisor.go —— self-tools 子进程监督（P6 2026-10-19）：异常退出后按退避重启。
//
// 口径（与 servertools.Supervisor 一致）：
//   - 启动失败或异常退出 → 1s / 2s / 4s … 封顶 30s 退避重启；
//   - **连续 3 次**重启仍失败 → 放弃（记日志，工具面降级，核心服务不受影响）；
//   - Stop 后不再重启（宿主退出 / 收尾路径）。
package selftools

import (
	"io"
	"os/exec"
	"sync"
	"time"
)

// 重启退避参数（var 而非 const：单测可缩短，避免真等 1s/2s/4s）。
var (
	restartBaseDelay = time.Second
	restartMaxDelay  = 30 * time.Second
)

const restartMaxFails = 3

// spawnFn 是 Spawn 的可注入入口（单测替换为失败桩，覆盖退避 / 放弃分支；生产恒为 Spawn）。
var spawnFn = Spawn

// Supervisor 持有 self-tools 子进程并监督其存活。
type Supervisor struct {
	addr string
	root string
	out  io.Writer
	logf func(format string, args ...any)

	mu      sync.Mutex
	cmd     *exec.Cmd
	stopped bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

// StartSupervisor 启动 self-tools 并开始监督（首次启动失败即返回错误；后续重启失败只记日志）。
// logf 为 nil 时丢弃重启日志。
func StartSupervisor(addr, capabilityRoot string, output io.Writer, logf func(format string, args ...any)) (*Supervisor, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	cmd, err := spawnFn(addr, capabilityRoot, output)
	if err != nil {
		return nil, err
	}
	s := &Supervisor{
		addr: addr, root: capabilityRoot, out: output, logf: logf,
		cmd: cmd, stopCh: make(chan struct{}), doneCh: make(chan struct{}),
	}
	go s.supervise(cmd)
	return s, nil
}

// supervise 等待当前子进程退出并按退避重启（Stop 后直接返回）。
func (s *Supervisor) supervise(cmd *exec.Cmd) {
	defer close(s.doneCh)
	fails := 0
	delay := restartBaseDelay
	for {
		if cmd != nil {
			_ = cmd.Wait()
		}
		if s.isStopped() {
			return
		}
		if fails >= restartMaxFails {
			s.logf("self-tools: 连续 %d 次重启失败，放弃监管（工具面降级，需重启宿主恢复）", fails)
			return
		}
		state := "<未启动>"
		if cmd != nil && cmd.ProcessState != nil {
			state = cmd.ProcessState.String()
		}
		s.logf("self-tools: 子进程退出（%s），%s 后重启（第 %d 次）", state, delay, fails+1)
		timer := time.NewTimer(delay)
		select {
		case <-s.stopCh:
			timer.Stop()
			return
		case <-timer.C:
		}
		next, err := spawnFn(s.addr, s.root, s.out)
		if err != nil {
			fails++
			if delay < restartMaxDelay {
				delay *= 2
				if delay > restartMaxDelay {
					delay = restartMaxDelay
				}
			}
			s.logf("self-tools: 重启失败: %v", err)
			cmd = nil
			continue
		}
		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			if next.Process != nil {
				_ = next.Process.Kill()
			}
			return
		}
		s.cmd = next
		s.mu.Unlock()
		fails = 0
		delay = restartBaseDelay
		cmd = next
	}
}

func (s *Supervisor) isStopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}

// Stop 终止子进程并关闭监督（幂等；等待监督 goroutine 退出）。
func (s *Supervisor) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	cmd := s.cmd
	close(s.stopCh)
	s.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	<-s.doneCh
}
