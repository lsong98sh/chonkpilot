//go:build windows

// workdirensure.go —— 「确保项目已打开」宿主门面（SANDBOX-WRITE-1 P3 / 方案 §5.5.2 ② · §7 · §8）。
//
// 用途：定时任务（crontab 插件）执行时项目可能**未打开** → 没有在册实例（拿不到 prj
// `security-*`、没有 `workdir-opened` 通知与索引）。本门面把该项目**隐藏打开**
// （C13/C17：走**正常打开流程**，只是窗口不显示），并等待 `instance-register` 后返回主实例 id。
//
// 口径（不猜）：
//   - **已在册**（`instance.Manager` 里同 work_dir 的顶层实例）→ 直接返回该实例 id，
//     `opened=false`（不建窗、不改可见性）；
//   - **未在册** → `env.OpenProject{..., Hidden:true}` 建隐藏主窗口 → 轮询实例视图直到登记
//     （≤ `timeout`）。超时 / 建窗失败 → **明确错误**（调用方跳本轮 + 记错，不静默成功）。
//
// 生命周期（与「普通打开的项目」**完全一致**，见 13-生命周期）：
//   - 隐藏实例**不**随窗口关闭而退出——它与正常项目一样常驻到**用户关闭该项目主窗口**
//     （托盘点开 → 显形 → 关闭）或**进程退出**（托盘「退出」/ `/quit`）；关闭时走既有
//     `finish`：`instance-exit` → server 注销实例 → 工作区管理发 `workdir-closed`；
//   - **不做**使用者引用计数**自动释放**（P3 不做自动关窗）：本项目常驻代价 = 一个 WebView2，
//     与用户自己打开的项目相同；「最后一个使用者释放即关」需要任务↔项目引用表，属残留项
//     （41 G-98）。
package host

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// workDirEnsureTimeout / workDirEnsurePoll 是隐藏打开后等待 `instance-register` 的默认上限与轮询间隔
// （C13：10~15s；取 **12s** —— 必须小于插件 SDK 的单次宿主调用超时（`sdk.Options.Timeout` 缺省 15s），
// 否则客户端先超时会丢掉宿主的「明确错误」；典型建窗 + 认领 < 3s）。
const (
	workDirEnsureTimeout = 12 * time.Second
	workDirEnsurePoll    = 100 * time.Millisecond
)

// workdirEnsurer 是 `plugin/v1/workdir.ensure` 的可测核心（P3）：
// 依赖以函数注入（真实实现 = 建窗 + 实例视图反查；测试 = fake 窗口工厂）。
type workdirEnsurer struct {
	// open 以指定可见性打开项目（真实实现 = `env.OpenProject{Hidden:hidden}`）。
	open func(workDir string, hidden bool) error
	// registered 反查该 work_dir 的**主实例 id**（真实实现 = `srv.MainInstanceForWorkDir`）。
	registered func(workDir string) (instanceID string, ok bool)
	logf       func(format string, args ...any)
	// timeout / poll / sleep 可注入（测试不真等）。
	timeout time.Duration
	poll    time.Duration
	sleep   func(time.Duration)

	// mu 串行化「查 → 开 → 等」：同一 work_dir 并发 ensure 不得重复建窗
	// （`Env.OpenProject` 的 lookup→startWindow 之间无锁，仅 UI 线程串行时可靠）。
	mu sync.Mutex
}

// ensure 返回该项目的**主实例 id**；opened=true 表示本次调用触发了隐藏打开。
func (e *workdirEnsurer) ensure(workDir string, hidden bool) (instanceID string, opened bool, err error) {
	wd := strings.TrimSpace(workDir)
	if wd == "" {
		return "", false, errors.New("workdir.ensure: work_dir 必填")
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	if id, ok := e.registered(wd); ok {
		return id, false, nil
	}
	if err := e.open(wd, hidden); err != nil {
		return "", false, fmt.Errorf("workdir.ensure: 打开项目失败（%s）: %w", wd, err)
	}
	// 轮询实例视图：窗口建窗即经桥发 `instance-register`（随后前端 `instance-claim` 幂等刷新）。
	attempts := e.attempts()
	for i := 0; i <= attempts; i++ {
		if id, ok := e.registered(wd); ok {
			if e.logf != nil {
				e.logf("workdir.ensure: 项目已%s打开（work_dir=%s instance=%s）", visibility(hidden), wd, id)
			}
			return id, true, nil
		}
		if i < attempts && e.sleep != nil {
			e.sleep(e.poll)
		}
	}
	return "", true, fmt.Errorf("workdir.ensure: 等待 instance-register 超时（%s，work_dir=%s）",
		e.timeout, wd)
}

// attempts 返回轮询次数（timeout / poll；非法值回落到默认）。
func (e *workdirEnsurer) attempts() int {
	timeout, poll := e.timeout, e.poll
	if timeout <= 0 {
		timeout = workDirEnsureTimeout
	}
	if poll <= 0 {
		poll = workDirEnsurePoll
	}
	n := int(timeout / poll)
	if n < 0 {
		return 0
	}
	return n
}

// visibility 把 hidden 转成日志用词。
func visibility(hidden bool) string {
	if hidden {
		return "隐藏"
	}
	return "可见"
}
