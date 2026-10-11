package flash

// flash 自动拉起服务端（🆕 2026-10-10；62 §3A / 19 §8.11）。
//
// 口径：flash（GUI 与 flash-cli 共用）启动时先探测约定 UDS 是否连通 —— 已连上 = 常驻 server
// 在跑，直接复用；连不上 → 定位 `chonkpilot-server.exe`（**同级 `server/` 优先**、或同目录、
// 或 `--server-exe` 指定）→ spawn `chonkpilot-server.exe --autoexit` → **带超时重试 dial**；
// 成功继续认领，仍失败给明确错误。
//
//   - server 已运行（UDS 连通）→ **不重复拉起**（同一份常驻 server 服务多实例）；
//   - `--autoexit` 仅在 flash 发起 spawn 时传入（语义见 `src/server/main.go`，本包不改其语义）；
//   - 本包只做「探测 + 拉起 + 等待」的纯逻辑：dial / spawn / 时钟均**依赖注入**，便于单测。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/uds"
)

// ServerExeName 返回服务端可执行文件名（Windows 带 `.exe`；与 build-gui.ps1 产物命名一致）。
func ServerExeName() string {
	if runtime.GOOS == "windows" {
		return "chonkpilot-server.exe"
	}
	return "chonkpilot-server"
}

// ServerOptions 是「探测 → 拉起 → 等待」的参数（零值可用；依赖注入点仅测试使用）。
type ServerOptions struct {
	// UDSPath 覆盖 UDS 前门路径；空 = uds.DefaultPath()（含 CHONK_UDS 环境变量）。
	UDSPath string
	// ServerExe 显式指定服务端 exe 路径（--server-exe）；空 = 由 ExePath 同目录 / 同级 server/ 推导。
	ServerExe string
	// ExePath 是 flash 自身 exe 路径（用于推导同目录 server）；空 = os.Executable()。
	ExePath string
	// Spawn 拉起进程（exe + args）；nil = 平台默认（Windows 隐藏控制台窗口）。
	Spawn func(exe string, args []string) error
	// Probe 探测 UDS 是否可连（nil = uds.Dial + 立即 Close）。
	Probe func(path string) error
	// Sleep 等待（nil = time.Sleep）；测试可注入空实现以加速。
	Sleep func(time.Duration)
	// Timeout 是 spawn 后等待 UDS 就绪的总时长；0 = 15s。
	Timeout time.Duration
	// Interval 是重试间隔；0 = 200ms。
	Interval time.Duration
	// Logf 诊断输出；nil = 不输出。
	Logf func(format string, args ...any)
}

// Ensure 确保常驻服务端就绪：返回 (是否本进程拉起了新进程, error)。
//   - UDS 已连通 → (false, nil)，复用现有 server；
//   - 未连通 → spawn server → 有界重试探测；成功 (true, nil)，超时 (true, err)。
func (o ServerOptions) Ensure() (bool, error) {
	path := o.UDSPath
	if path == "" {
		path = uds.DefaultPath()
	}
	probe := o.Probe
	if probe == nil {
		probe = defaultProbe
	}
	if probe(path) == nil {
		return false, nil // 已有 server 在跑 → 直接复用，不重复拉起
	}

	exe, err := o.resolveServerExe()
	if err != nil {
		return false, err
	}
	spawn := o.Spawn
	if spawn == nil {
		env := o.serverEnv()
		spawn = func(exe string, args []string) error { return launchDetached(exe, args, env) }
	}
	o.logf("UDS %s 未连通 → 拉起服务端 %s --autoexit", path, exe)
	if err := spawn(exe, []string{"--autoexit"}); err != nil {
		return false, fmt.Errorf("flash: 拉起服务端 %s 失败: %w", exe, err)
	}

	timeout, interval := o.Timeout, o.Interval
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if interval <= 0 {
		interval = 200 * time.Millisecond
	}
	sleep := o.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	attempts := int(timeout / interval)
	if attempts < 1 {
		attempts = 1
	}
	for i := 0; i < attempts; i++ {
		sleep(interval)
		if probe(path) == nil {
			o.logf("服务端就绪（UDS %s）", path)
			return true, nil
		}
	}
	return true, fmt.Errorf("flash: 已拉起服务端 %s，但等待 %s 后仍无法连接 UDS %s", exe, timeout, path)
}

// resolveServerExe 定位服务端 exe：
//   - 显式 `--server-exe` 优先（不做存在性校验，交由 spawn 报错）；
//   - 否则按 flash exe 位置推测候选：**同级 `server/` 子目录** → **同目录**
//     （随 `build-gui.ps1` 产物布局：flash 在 `dist/gui\`、server 在 `dist/server\`）。
//     **同级优先**：server 需其随附资产（capability/mcps/plugins 等在其 exe 同目录/）——
//     同目录若只有裸 server exe（无资产）会拉起失败，故完整部署（同级 server/）优先。
//     取第一个存在的候选；都不存在 → 明确错误（提示 `--server-exe`）。
func (o ServerOptions) resolveServerExe() (string, error) {
	if p := o.ServerExe; p != "" {
		return p, nil
	}
	self := o.ExePath
	if self == "" {
		if p, err := os.Executable(); err == nil {
			self = p
		}
	}
	if self == "" {
		return "", errors.New("flash: 未定位到服务端 exe（可用 --server-exe 指定）")
	}
	dir := filepath.Dir(self)
	candidates := []string{
		filepath.Join(dir, "..", "server", ServerExeName()),
		filepath.Join(dir, ServerExeName()),
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("flash: 未找到服务端 exe（候选 %v）；可用 --server-exe 指定", candidates)
}

// defaultProbe 探测 UDS 是否可连：dial 成功后立即关闭（即时断开不构成实例，server 侧忽略）。
func defaultProbe(path string) error {
	c, err := uds.Dial(uds.ClientOptions{Path: path})
	if err != nil {
		return err
	}
	return c.Close()
}

func (o ServerOptions) logf(format string, args ...any) {
	if o.Logf != nil {
		o.Logf(format, args...)
	}
}

// serverEnv 构造被拉起 server 的子进程环境：非空 opts.UDSPath 经 `CHONK_UDS` 下传
// （server 恒读 uds.DefaultPath()，其含 CHONK_UDS 覆盖）→ 自定义 UDS 的测试/多实例场景
// 能让子进程监听同一路径（否则父进程连自定义路径、子进程却监听约定 DefaultPath，永连不上）。
// UDSPath 为空 → 返回 nil（继承父环境，子进程走约定 DefaultPath，生产无影响）。
// 追加前**剔除父环境同名项**（避免 `CHONK_UDS` 出现重复项，Windows 下重复环境变量取值不确定）。
func (o ServerOptions) serverEnv() []string {
	if o.UDSPath == "" {
		return nil
	}
	env := os.Environ()
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if name, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(name, uds.EnvUDSPath) {
			continue // 剔除同名项（Windows 环境变量名不区分大小写）
		}
		out = append(out, kv)
	}
	return append(out, uds.EnvUDSPath+"="+o.UDSPath)
}
