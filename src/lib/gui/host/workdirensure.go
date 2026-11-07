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
//   - **未在册** → `env.OpenProject{..., Hidden:true}` 建隐藏主窗口 → **等实例视图变更事件**
//     （`instance-register` / `instance-exit` 广播，见 main.go 接线）直到登记（≤ `timeout`）。
//     超时 / 建窗失败 → **明确错误**（调用方跳本轮 + 记错，不静默成功）。
//
// 事件等待（异步改造 §10.1，2026-11-06）：**不再轮询**（原 100ms × 120 次）。注入
// `changed()` 返回「下一次实例视图变更」的通道（宿主实现 = 总线订阅 + close-and-replace
// 广播）；等待外壳 = **单个** `time.NewTimer(timeout)` + select(changed / timer / ctx)，
// 每次唤醒重查 `registered(wd)`（取通道后再查 → 防丢唤醒）。
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
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// workDirEnsureTimeout 是隐藏打开后等待 `instance-register` 的默认上限
// （C13：10~15s；取 **12s** —— 必须小于插件 SDK 的单次宿主调用超时（`sdk.Options.Timeout` 缺省 15s），
// 否则客户端先超时会丢掉宿主的「明确错误」；典型建窗 + 认领 < 3s）。
const workDirEnsureTimeout = 12 * time.Second

// instanceChangeBroadcaster 是**进程内**实例视图变更的轻量广播（不新增 MQ 主题）：
// 每次 notify = close 当前通道并换新（close-and-replace）——等待方在**取通道之后**再查一次
// 权威状态，即可保证「登记发生在取通道前 → 查询命中；发生在取通道后 → 通道被 close」，
// 不会丢唤醒。notify 由总线订阅回调（派发线程）调用，只做非阻塞的 close/换新。
type instanceChangeBroadcaster struct {
	mu sync.Mutex
	ch chan struct{}
}

// newInstanceChangeBroadcaster 构造广播（初值 = 一个永不关闭的通道）。
func newInstanceChangeBroadcaster() *instanceChangeBroadcaster {
	return &instanceChangeBroadcaster{ch: make(chan struct{})}
}

// notify 广播一次「实例视图可能已变」（close-and-replace；幂等由调用点自行合并）。
func (b *instanceChangeBroadcaster) notify() {
	if b == nil {
		return
	}
	b.mu.Lock()
	close(b.ch)
	b.ch = make(chan struct{})
	b.mu.Unlock()
}

// changed 返回**当前**这一代变更通道；调用后必须立刻重查状态再 select。
func (b *instanceChangeBroadcaster) changed() <-chan struct{} {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ch
}

// workdirEnsurer 是 `plugin/v1/workdir.ensure` 的可测核心（P3）：
// 依赖以函数注入（真实实现 = 建窗 + 实例视图反查；测试 = fake 窗口工厂 + fake 广播）。
type workdirEnsurer struct {
	// open 以指定可见性打开项目（真实实现 = `env.OpenProject{Hidden:hidden}`）。
	open func(workDir string, hidden bool) error
	// registered 反查该 work_dir 的**主实例 id**（真实实现 = `srv.MainInstanceForWorkDir`）。
	registered func(workDir string) (instanceID string, ok bool)
	logf       func(format string, args ...any)
	// timeout 可注入（测试不真等默认 12s）。
	timeout time.Duration
	// changed 返回下一次实例视图变更的通道（nil = 无事件源，只能等超时）。
	// 宿主实现 = instanceChangeBroadcaster.changed；测试 = 直接注入可控广播。
	changed func() <-chan struct{}

	// mu 串行化「查 → 开 → 等」：同一 work_dir 并发 ensure 不得重复建窗
	// （`Env.OpenProject` 的 lookup→startWindow 之间无锁，仅 UI 线程串行时可靠）。
	mu sync.Mutex
}

// ensure 返回该项目的**主实例 id**；opened=true 表示本次调用触发了隐藏打开。
//
// ctx 取消 → 明确取消错误（宿主当前以 `context.Background()` 调用；保留注入位以支持
// 插件桥将来的请求 ctx 透传，行为不变）。
func (e *workdirEnsurer) ensure(ctx context.Context, workDir string, hidden bool) (instanceID string, opened bool, err error) {
	wd := strings.TrimSpace(workDir)
	if wd == "" {
		return "", false, errors.New("workdir.ensure: work_dir 必填")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	if id, ok := e.registered(wd); ok {
		return id, false, nil
	}
	if err := e.open(wd, hidden); err != nil {
		return "", false, fmt.Errorf("workdir.ensure: 打开项目失败（%s）: %w", wd, err)
	}
	// 建窗可能**同步**完成登记（实例视图立即命中）→ 先重查一次；若未命中则进入事件等待。
	if id, ok := e.registered(wd); ok {
		if e.logf != nil {
			e.logf("workdir.ensure: 项目已%s打开（work_dir=%s instance=%s）", visibility(hidden), wd, id)
		}
		return id, true, nil
	}

	timeout := e.timeout
	if timeout <= 0 {
		timeout = workDirEnsureTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		// 顺序关键：先取通道快照，再查权威状态——登记若发生在两者之间，通道会被 close。
		var changed <-chan struct{}
		if e.changed != nil {
			changed = e.changed()
		}
		if id, ok := e.registered(wd); ok {
			if e.logf != nil {
				e.logf("workdir.ensure: 项目已%s打开（work_dir=%s instance=%s）", visibility(hidden), wd, id)
			}
			return id, true, nil
		}
		select {
		case <-ctx.Done():
			return "", true, fmt.Errorf("workdir.ensure: 等待 instance-register 已取消（%s，work_dir=%s）: %w", timeout, wd, ctx.Err())
		case <-changed:
			// 实例视图变更（登记/注销）→ 重查；防抖：多次变更合并为一次循环。
		case <-timer.C:
			// 超时前最后再查一次（变更可能与 timer 同时就绪）。
			if id, ok := e.registered(wd); ok {
				if e.logf != nil {
					e.logf("workdir.ensure: 项目已%s打开（work_dir=%s instance=%s）", visibility(hidden), wd, id)
				}
				return id, true, nil
			}
			return "", true, fmt.Errorf("workdir.ensure: 等待 instance-register 超时（%s，work_dir=%s）", timeout, wd)
		}
	}
}

// visibility 把 hidden 转成日志用词。
func visibility(hidden bool) string {
	if hidden {
		return "隐藏"
	}
	return "可见"
}
