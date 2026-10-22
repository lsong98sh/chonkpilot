// Package coretools 启动并监管 chonkpilot-core-mcp-server（2026-10-19）。
//
// 口径：
//   - 由 chonkpilot-server（desktop 宿主 / server 形态）在启动期拉起，常驻；
//   - 固定 loopback 端口 + 随机 Bearer token（重启复用同一 URL/token，gateway 连接不失效）；
//   - 异常退出按 1s/2s/4s… 封顶 30s 退避重启，连续 3 次失败放弃（工具面降级，核心服务不受影响）；
//   - Stop 终止进程树并停止监督（幂等）。
package coretools

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/exedir"
	"github.com/chonkpilot/chonkpilot-lib/winproc"
)

// 重启退避参数（var：单测可缩短）。
var (
	restartBaseDelay = time.Second
	restartMaxDelay  = 30 * time.Second
	readyTimeout     = 20 * time.Second
)

const restartMaxFails = 3

// Options 是 core-mcp-server 的启动参数（由宿主编排）。
type Options struct {
	// Addr 监听地址（空 → 自动挑一个 loopback 空闲端口）。
	Addr string
	// Token Bearer token（空 → 随机生成）。
	Token string
	// Root 契约根（空 → 按 exe 目录推断）。
	Root string
	// WorkDir / DataDir 缺省调用上下文目录（头/_meta 未给出时使用）。
	WorkDir, DataDir string
	// ConfigPath 可选 config.json。
	ConfigPath string
	// PluginAddr / PluginToken 插件桥地址与 HTTP token（透传给 executor）。
	PluginAddr, PluginToken string
	// Output 子进程 stdout/stderr 落点（nil → os.Stderr）。
	Output io.Writer
	// Logf 监督日志（nil → 丢弃）。
	Logf func(format string, args ...any)
}

// Supervisor 持有 core-mcp-server 子进程并监督其存活。
type Supervisor struct {
	opts  Options
	addr  string
	token string

	mu      sync.Mutex
	cmd     *exec.Cmd
	stopped bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

// Start 启动 core-mcp-server 并等待就绪（首次启动失败即返回错误）。
func Start(opts Options) (*Supervisor, error) {
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	if opts.Addr == "" {
		addr, err := freeLoopbackAddr()
		if err != nil {
			return nil, fmt.Errorf("core-mcp: 选择端口: %w", err)
		}
		opts.Addr = addr
	}
	if opts.Token == "" {
		var buf [32]byte
		if _, err := rand.Read(buf[:]); err != nil {
			return nil, fmt.Errorf("core-mcp: 生成 token: %w", err)
		}
		opts.Token = hex.EncodeToString(buf[:])
	}
	s := &Supervisor{
		opts:   opts,
		addr:   opts.Addr,
		token:  opts.Token,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
	cmd, err := s.spawn()
	if err != nil {
		return nil, err
	}
	if err := s.waitReady(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	s.mu.Lock()
	s.cmd = cmd
	s.mu.Unlock()
	go s.supervise(cmd)
	return s, nil
}

// URL 返回 MCP 端点（http://host:port/mcp）。
func (s *Supervisor) URL() string {
	if s == nil {
		return ""
	}
	return "http://" + s.addr + "/mcp"
}

// Token 返回 Bearer token。
func (s *Supervisor) Token() string {
	if s == nil {
		return ""
	}
	return s.token
}

// PushRuntime 把运行期执行配置快照推给 core-mcp-server（usr/prj 热生效）。
// payload = mcpms.RuntimeConfig（保持本包对 mcp-server 无依赖，按 JSON 透传）。
func (s *Supervisor) PushRuntime(payload any) error {
	if s == nil || payload == nil {
		return nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, "http://"+s.addr+"/internal/runtime", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.token)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("core-mcp: runtime push HTTP %d", resp.StatusCode)
	}
	return nil
}

// Stop 终止子进程并关闭监督（幂等）。
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
		_ = winproc.KillTree(cmd.Process.Pid)
		_ = cmd.Wait()
	}
	<-s.doneCh
}

// supervise 等待子进程退出并按退避重启（Stop 后直接返回）。
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
			s.opts.Logf("core-mcp: 连续 %d 次重启失败，放弃监管（工具面降级，需重启宿主恢复）", fails)
			return
		}
		state := "<未启动>"
		if cmd != nil && cmd.ProcessState != nil {
			state = cmd.ProcessState.String()
		}
		s.opts.Logf("core-mcp: 子进程退出（%s），%s 后重启（第 %d 次）", state, delay, fails+1)
		timer := time.NewTimer(delay)
		select {
		case <-s.stopCh:
			timer.Stop()
			return
		case <-timer.C:
		}
		next, err := s.spawn()
		if err == nil {
			err = s.waitReady()
		}
		if err != nil {
			fails++
			if delay < restartMaxDelay {
				delay *= 2
				if delay > restartMaxDelay {
					delay = restartMaxDelay
				}
			}
			s.opts.Logf("core-mcp: 重启失败: %v", err)
			if next != nil && next.Process != nil {
				_ = winproc.KillTree(next.Process.Pid)
				_ = next.Wait()
			}
			cmd = nil
			continue
		}
		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			_ = winproc.KillTree(next.Process.Pid)
			_ = next.Wait()
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

// spawn 启动一次 core-mcp-server 进程。
func (s *Supervisor) spawn() (*exec.Cmd, error) {
	dir, err := exedir.Dir()
	if err != nil {
		return nil, fmt.Errorf("core-mcp: exe 目录: %w", err)
	}
	name := "chonkpilot-core-mcp-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := ""
	for _, p := range []string{
		filepath.Join(dir, name),
		filepath.Join(dir, "mcps", "core-mcp", name),
	} {
		if _, err := os.Stat(p); err == nil {
			path = p
			break
		}
	}
	if path == "" {
		return nil, fmt.Errorf("core-mcp: 未找到 %s（查找 %s 与 %s）", name, dir, filepath.Join(dir, "mcps", "core-mcp"))
	}
	out := s.opts.Output
	if out == nil {
		out = os.Stderr
	}
	args := []string{"--addr", s.addr, "--token", s.token}
	if s.opts.Root != "" {
		args = append(args, "--root", s.opts.Root)
	}
	if s.opts.WorkDir != "" {
		args = append(args, "--work-dir", s.opts.WorkDir)
	}
	if s.opts.DataDir != "" {
		args = append(args, "--data-dir", s.opts.DataDir)
	}
	if s.opts.ConfigPath != "" {
		args = append(args, "--config", s.opts.ConfigPath)
	}
	if s.opts.PluginAddr != "" {
		args = append(args, "--plugin-addr", s.opts.PluginAddr)
	}
	if s.opts.PluginToken != "" {
		args = append(args, "--plugin-token", s.opts.PluginToken)
	}
	cmd := exec.Command(path, args...)
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.SysProcAttr = winproc.SysProcAttr()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("core-mcp: start %s: %w", path, err)
	}
	return cmd, nil
}

// waitReady 轮询 /healthz 直到就绪（Bearer token）。
func (s *Supervisor) waitReady() error {
	deadline := time.Now().Add(readyTimeout)
	url := "http://" + s.addr + "/healthz"
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("Authorization", "Bearer "+s.token)
		if resp, err := client.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if s.isStopped() {
			return fmt.Errorf("core-mcp: 已停止")
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("core-mcp: 就绪超时（%s）", url)
}

// freeLoopbackAddr 选一个 loopback 空闲端口（listen 后立即释放；重启复用同一端口）。
func freeLoopbackAddr() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr, nil
}
