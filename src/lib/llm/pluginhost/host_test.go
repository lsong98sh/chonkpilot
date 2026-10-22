package pluginhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/lockfile"
)

// TestHelperProcess 是子进程桩：CK_HELPER=1 时按 CK_HELPER_EXIT 退出、
// 监听 CK_HELPER_STOP_FILE 后退出，或常驻。
func TestHelperProcess(t *testing.T) {
	if os.Getenv("CK_HELPER") != "1" {
		return
	}
	if v := os.Getenv("CK_HELPER_EXIT"); v != "" {
		n, _ := strconv.Atoi(v)
		os.Exit(n)
	}
	if p := os.Getenv("CK_HELPER_STOP_FILE"); p != "" {
		for {
			if _, err := os.Stat(p); err == nil {
				os.Exit(0)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	select {}
}

func helperSpec(t *testing.T, dir string, exitCode int, maxRestarts int) Spec {
	t.Helper()
	env := []string{"CK_HELPER=1"}
	if exitCode >= 0 {
		env = append(env, "CK_HELPER_EXIT="+strconv.Itoa(exitCode))
	}
	return Spec{
		ID:          "test.indexer",
		Exe:         os.Args[0],
		Args:        []string{"-test.run=TestHelperProcess"},
		Env:         env,
		IndexDir:    dir,
		MaxRestarts: maxRestarts,
		Backoff:     10 * time.Millisecond,
	}
}

func TestHostLifecycle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "idx")
	h := New(helperSpec(t, dir, -1, 3))
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !waitFor(2*time.Second, h.Running) {
		t.Fatalf("helper 未启动")
	}
	// 单实例锁：同 IndexDir 第二个 host → ErrBusy
	h2 := New(helperSpec(t, dir, -1, 3))
	if err := h2.Start(context.Background()); !errors.Is(err, lockfile.ErrBusy) {
		t.Fatalf("second start err=%v, want ErrBusy", err)
	}
	if err := h.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if h.Running() {
		t.Fatalf("stop 后仍在运行")
	}
	// 锁释放后第二个 host 可启动
	if err := h2.Start(context.Background()); err != nil {
		t.Fatalf("restart after release: %v", err)
	}
	_ = h2.Stop()
}

func TestHostRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "idx")
	h := New(helperSpec(t, dir, 1, 2))
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	// 1 次原始 + 2 次重启，第 3 次退出后超限 → 停止
	if !waitFor(3*time.Second, func() bool { return !h.Running() && h.Restarts() >= 3 }) {
		t.Fatalf("restarts=%d running=%v", h.Restarts(), h.Running())
	}
	_ = h.Stop()
}

func waitFor(d time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fn()
}

// hostSpecFor 构造一个绑定到指定 work_dir 的常驻 helper spec（IndexDir 独立）。
func hostSpecFor(t *testing.T, id, wd string) Spec {
	t.Helper()
	if err := os.MkdirAll(wd, 0o755); err != nil {
		t.Fatalf("mkdir work_dir: %v", err)
	}
	spec := helperSpec(t, filepath.Join(t.TempDir(), "idx"), -1, 3)
	spec.ID = id
	spec.WorkDir = wd
	return spec
}

// TestHostGracefulShutdown：Shutdown 被调用 → 子进程自行退出 → 不触发 Kill（快速返回、无重启）。
func TestHostGracefulShutdown(t *testing.T) {
	wd := filepath.Join(t.TempDir(), "wd")
	stopFile := filepath.Join(t.TempDir(), "stop")
	spec := hostSpecFor(t, "test.indexer.graceful", wd)
	spec.Env = append(spec.Env, "CK_HELPER_STOP_FILE="+stopFile)
	called := make(chan struct{})
	spec.Shutdown = func(ctx context.Context) error {
		select {
		case <-called:
		default:
			close(called)
		}
		return os.WriteFile(stopFile, []byte("stop"), 0o644)
	}

	h := New(spec)
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !waitFor(2*time.Second, h.Running) {
		t.Fatalf("helper 未启动")
	}
	start := time.Now()
	if err := h.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	select {
	case <-called:
	default:
		t.Fatalf("Shutdown 未被调用")
	}
	// 远小于 ShutdownTimeout（5s）→ 说明走的是优雅退出，未等超时 Kill。
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("优雅停止耗时过长（疑似等待 Kill 超时）: %v", d)
	}
	if h.Running() {
		t.Fatalf("stop 后仍在运行")
	}
	if h.Restarts() != 0 {
		t.Fatalf("优雅退出不应重启，restarts=%d", h.Restarts())
	}
}

// TestHostShutdownTimeoutKills：Shutdown 阻塞超过 ShutdownTimeout → Stop 仍返回并最终 Kill。
func TestHostShutdownTimeoutKills(t *testing.T) {
	spec := hostSpecFor(t, "test.indexer.timeout", filepath.Join(t.TempDir(), "wd"))
	spec.ShutdownTimeout = 50 * time.Millisecond
	spec.Shutdown = func(ctx context.Context) error {
		// 故意忽略 ctx 长时间阻塞：Stop 不得被它拖住。
		time.Sleep(2 * time.Second)
		return nil
	}

	h := New(spec)
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !waitFor(2*time.Second, h.Running) {
		t.Fatalf("helper 未启动")
	}
	start := time.Now()
	if err := h.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("Stop 被阻塞过久: %v", d)
	}
	if h.Running() {
		t.Fatalf("超时后子进程应被 Kill")
	}
}

// TestManagerPerWorkDir：不同 work_dir 可同时监管；同 work_dir 重复 Ensure 不产生第二个进程；
// StopAll 后全部停止。
func TestManagerPerWorkDir(t *testing.T) {
	m := NewManager(nil)
	wd1 := filepath.Join(t.TempDir(), "p1")
	wd2 := filepath.Join(t.TempDir(), "p2")

	s1 := hostSpecFor(t, "test.indexer.1", wd1)
	s2 := hostSpecFor(t, "test.indexer.2", wd2)
	if err := m.Ensure(context.Background(), s1); err != nil {
		t.Fatalf("ensure 1: %v", err)
	}
	if err := m.Ensure(context.Background(), s2); err != nil {
		t.Fatalf("ensure 2: %v", err)
	}
	if got := len(m.Hosts()); got != 2 {
		t.Fatalf("hosts=%d，期望 2", got)
	}
	if !waitFor(3*time.Second, func() bool {
		for _, h := range m.Hosts() {
			if !h.Running() {
				return false
			}
		}
		return true
	}) {
		t.Fatalf("两个 work_dir 的插件未同时运行")
	}

	// 同一 work_dir（不同 IndexDir + 带 ./ 的路径写法）→ 归一化去重，不启动第二个进程。
	s1dup := hostSpecFor(t, "test.indexer.dup", wd1)
	s1dup.WorkDir = filepath.Join(wd1, ".")
	if err := m.Ensure(context.Background(), s1dup); err != nil {
		t.Fatalf("ensure dup: %v", err)
	}
	if got := len(m.Hosts()); got != 2 {
		t.Fatalf("同 work_dir 重复 Ensure 产生新 Host：hosts=%d", got)
	}

	// 空 work_dir → 报错。
	if err := m.Ensure(context.Background(), Spec{ID: "x", WorkDir: ""}); err == nil {
		t.Fatalf("空 WorkDir 应报错")
	}

	m.StopAll()
	for _, h := range m.Hosts() {
		if h.Running() {
			t.Fatalf("StopAll 后仍有进程运行")
		}
	}
	m.StopAll() // 幂等
}
