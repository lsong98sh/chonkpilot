// Package pluginhost 负责 **resident plugin**（常驻插件，如 indexer）的进程生命周期：
// 随 server 启动、随 server 退出；单实例锁；崩溃重启（1s→30s 退避，最多 3 次）。
//
// 设计见 docs/bridge-plugin/插件桥接端口设计方案.md §11.8。
// 注意：pluginhost **不**管理 MCP server（后者由 gateway 池调度）；两者产物可同工程。
package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/lockfile"
)

// DefaultMaxRestarts 是默认重启次数上限（超过 → plugin-failed，不再拉起）。
const DefaultMaxRestarts = 3

// Spec 描述一个常驻插件。
type Spec struct {
	ID       string   // 插件 id（也是锁名 / 实例 id 片段）
	Exe      string   // 可执行文件
	Args     []string // 启动参数
	Env      []string // 额外环境变量（KEY=VALUE）
	DataDir  string   // <用户数据根>
	WorkDir  string   // 工作目录
	IndexDir string   // 索引库目录（每个 workdir 一份）
	// NoLock=true → 不获取 IndexDir 下的进程级单实例文件锁（2026-10-19）：
	// 单实例改由插件自身判定（Windows = 注册全局窗口类），多 workdir 并发由
	// 各 workdir 自己的锁保护（每个项目索引库不同，无需全局互斥）。
	NoLock bool
	// PluginAddr 是插件前门地址（UDS / tcp://），子进程用它连回 server。
	PluginAddr string
	// MaxRestarts 缺省 3。
	MaxRestarts int
	// Backoff 是首次重启等待（缺省 1s；后续翻倍，封顶 30s）。
	Backoff time.Duration
	// Shutdown 由宿主注入：经插件桥给该插件发 plugin.shutdown 并等待其退出（可选）。
	Shutdown func(ctx context.Context) error
	// ShutdownTimeout 是优雅关闭的等待上限（缺省 5s）。
	ShutdownTimeout time.Duration
	Logf            func(format string, args ...any)
}

// DefaultShutdownTimeout 是优雅关闭的默认等待上限。
const DefaultShutdownTimeout = 5 * time.Second

// Host 是一个常驻插件的监管器。
type Host struct {
	spec Spec

	mu       sync.Mutex
	cmd      *exec.Cmd
	exited   chan struct{}
	lock     *lockfile.Lock
	started  bool
	stopping bool
	running  bool
	restarts int
	done     chan struct{}
}

// New 构造监管器（未启动）。
func New(spec Spec) *Host {
	if spec.MaxRestarts <= 0 {
		spec.MaxRestarts = DefaultMaxRestarts
	}
	if spec.Backoff <= 0 {
		spec.Backoff = time.Second
	}
	if spec.ShutdownTimeout <= 0 {
		spec.ShutdownTimeout = DefaultShutdownTimeout
	}
	return &Host{spec: spec, done: make(chan struct{})}
}

// Start 获取单实例锁并拉起插件；监管协程随 ctx / Stop 结束。
// NoLock=false 时同 IndexDir 已有活实例 → 返回 lockfile.ErrBusy（调用方据此报“已在运行”）。
func (h *Host) Start(ctx context.Context) error {
	if h.spec.Exe == "" {
		return errors.New("pluginhost: Exe 不能为空")
	}
	if h.spec.WorkDir != "" {
		if err := os.MkdirAll(h.spec.WorkDir, 0o755); err != nil {
			return fmt.Errorf("pluginhost: 创建工作目录: %w", err)
		}
	}
	var lk *lockfile.Lock
	if !h.spec.NoLock {
		if h.spec.IndexDir == "" {
			return errors.New("pluginhost: IndexDir 不能为空")
		}
		if err := os.MkdirAll(h.spec.IndexDir, 0o755); err != nil {
			return fmt.Errorf("pluginhost: 创建索引目录: %w", err)
		}
		acquired, err := lockfile.Acquire(filepath.Join(h.spec.IndexDir, "indexer.lock"))
		if err != nil {
			return err
		}
		lk = acquired
	}
	h.mu.Lock()
	h.lock = lk
	h.started = true
	h.mu.Unlock()
	go h.supervise(ctx)
	return nil
}

// Stop 优雅停止监管并结束子进程（幂等）：
//   - 置 stopping（监管协程不再重启）；
//   - 若注入了 Shutdown，则以 ShutdownTimeout 为上限调用（错误忽略，继续等待/Kill）；
//   - 等待子进程退出，超时则 Kill；最后等待监管协程结束并释放锁。
func (h *Host) Stop() error {
	h.mu.Lock()
	if h.stopping {
		started := h.started
		h.mu.Unlock()
		if started {
			<-h.done
		}
		return nil
	}
	h.stopping = true
	started := h.started
	cmd := h.cmd
	exited := h.exited
	h.mu.Unlock()
	if !started {
		return nil
	}
	timeout := h.spec.ShutdownTimeout
	if timeout <= 0 {
		timeout = DefaultShutdownTimeout
	}
	if h.spec.Shutdown != nil {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		done := make(chan struct{})
		go func() {
			defer close(done)
			if err := h.spec.Shutdown(ctx); err != nil {
				h.logf("pluginhost: 插件 %s 优雅关闭返回错误: %v（继续等待/Kill）", h.spec.ID, err)
			}
		}()
		select {
		case <-done:
		case <-ctx.Done():
			h.logf("pluginhost: 插件 %s 优雅关闭超时（%s），等待子进程退出", h.spec.ID, timeout)
		}
		cancel()
	}
	if cmd != nil && cmd.Process != nil && exited != nil {
		if h.spec.Shutdown != nil {
			select {
			case <-exited:
			case <-time.After(timeout):
				h.logf("pluginhost: 插件 %s 未在 %s 内退出，强制结束", h.spec.ID, timeout)
				_ = cmd.Process.Kill()
			}
		} else {
			// 未注入优雅关闭钩子 → 保持既有行为：立即结束子进程。
			_ = cmd.Process.Kill()
		}
	}
	<-h.done
	h.releaseLock()
	return nil
}

// Running 返回当前子进程是否在运行。
func (h *Host) Running() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.running
}

// Restarts 返回已重启次数。
func (h *Host) Restarts() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.restarts
}

func (h *Host) supervise(ctx context.Context) {
	defer close(h.done)
	defer h.releaseLock()
	backoff := h.spec.Backoff
	for {
		if h.isStopping() || ctx.Err() != nil {
			return
		}
		err := h.runOnce(ctx)
		if h.isStopping() || ctx.Err() != nil {
			return
		}
		if err == nil {
			// 正常退出（code 0）→ 不再拉起。
			h.logf("pluginhost: 插件 %s 正常退出", h.spec.ID)
			return
		}
		h.mu.Lock()
		h.restarts++
		n := h.restarts
		h.mu.Unlock()
		if n > h.spec.MaxRestarts {
			h.logf("plugin-failed plugin=%s restarts=%d err=%v", h.spec.ID, n-1, err)
			return
		}
		h.logf("pluginhost: 插件 %s 退出（%v），%s 后重启（%d/%d）", h.spec.ID, err, backoff, n, h.spec.MaxRestarts)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		if h.isStopping() {
			return
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func (h *Host) runOnce(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, h.spec.Exe, h.spec.Args...)
	cmd.Dir = h.spec.WorkDir
	cmd.Env = append(os.Environ(), h.spec.Env...)
	cmd.Env = append(cmd.Env,
		"CHONKPILOT_PLUGIN_ID="+h.spec.ID,
		"CHONKPILOT_DATA_DIR="+h.spec.DataDir,
		"CHONKPILOT_WORK_DIR="+h.spec.WorkDir,
		"CHONKPILOT_INDEX_DIR="+h.spec.IndexDir,
		"CHONKPILOT_PLUGIN_ADDR="+h.spec.PluginAddr,
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	exited := make(chan struct{})
	h.mu.Lock()
	if h.stopping {
		h.mu.Unlock()
		return nil
	}
	h.cmd = cmd
	h.exited = exited
	h.mu.Unlock()
	if err := cmd.Start(); err != nil {
		close(exited)
		h.mu.Lock()
		if h.exited == exited {
			h.exited = nil
		}
		if h.cmd == cmd {
			h.cmd = nil
		}
		h.mu.Unlock()
		return fmt.Errorf("start: %w", err)
	}
	h.mu.Lock()
	h.running = true
	stopNow := h.stopping
	h.mu.Unlock()
	if stopNow {
		// Stop 在本进程启动窗口内置位：立即结束，避免 Wait 悬挂。
		_ = cmd.Process.Kill()
	}
	err := cmd.Wait()
	close(exited)
	h.mu.Lock()
	h.running = false
	if h.exited == exited {
		h.exited = nil
	}
	if h.cmd == cmd {
		h.cmd = nil
	}
	h.mu.Unlock()
	return err
}

func (h *Host) isStopping() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stopping
}

func (h *Host) releaseLock() {
	h.mu.Lock()
	lk := h.lock
	h.lock = nil
	h.mu.Unlock()
	if lk != nil {
		lk.Release()
	}
}

func (h *Host) logf(format string, args ...any) {
	if h.spec.Logf != nil {
		h.spec.Logf(format, args...)
	}
}
