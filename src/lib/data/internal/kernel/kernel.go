// Package kernel 是 data 组件门面实现的**共享内核**（阶段 4「internal 下沉」）。
//
// 位置含义（23-工程与部署拓扑 §2「隔离」）：`internal` 是**编译器强制**的可见性边界——
// `chonkpilot-data/internal/...` 只允许 `chonkpilot-data/...` 之内的包引用，模块外的调用方
// （插件 / 测试 / 外壳）一旦直接 import 即编译失败（"use of internal package ... not allowed"）。
//
// 本包只装**各域共用**的实现内核（不含任何域的业务规则）：
//   - 实例数据根视图（View：instance_id → {work_dir, data_dir, 最近心跳}）
//   - 数据根解析与三层库打开（root.go）
//   - config 表原语 + 订阅面（data-<domain>-refresh）广播（conf.go）
//   - 记录/消息视图 helper（viewdata.go；原 persist/view.go 逐字下移）
//
// 对外可见面仍是门面（`chonkpilot-data/facade`）——本包**不导出给模块外**（internal 门禁）。
package kernel

import (
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// Options 是数据服务的构造参数（原 `persist.Options` 逐字下移；persist 保留同名别名转发）。
type Options struct {
	// UsrPath usr 层 db 路径（空 = data.UserPath()；测试注入避免污染用户配置）。
	UsrPath string
	// AppDir app 级知识库根（data-knowledge-* level=app；空 = 宿主可执行目录/capability，
	// 与内嵌 mcp-server 契约根语义一致；测试注入避免依赖 exe 目录）。
	AppDir string
	// Warnf 是告警出口（MW-11：`View.Resolve` 的唯一实例回退 / 缺 instance_id 时输出告警，
	// 让遗漏显式暴露）。空 = 不输出（默认行为：单实例回退照旧工作，**不改变**任何解析语义）。
	Warnf func(format string, args ...any)
}

// Base 是各域门面实现共用的运行态：宿主总线 + 数据根参数 + 实例视图 + 订阅面列表回调。
//
// 各域服务（internal/<域>）各自持有同一个 *Base（装配侧构造一次并注入），故实例视图、
// 数据根参数、变更广播三者在各域之间**同源**（与下沉前 persist.Service 单结构体时等价）。
type Base struct {
	// Bus 宿主总线（订阅面广播用；nil = 不广播，仅测试/无总线场景）。
	Bus mq.Bus
	// UsrPath / AppDir 见 Options。
	UsrPath string
	AppDir  string
	// View 实例数据根视图（自持；订阅 instance-register/heartbeat/exit 维护）。
	View *View
	// DomainList 是 refresh 广播附带「域当前列表」的回调：由装配侧（persist）注入，
	// 各域与对应 list 应答**同源**（同一批门面方法）。nil = 不附带 list 字段。
	DomainList func(domain, instanceID string, scope facade.Scope) any
	// Warnf 是告警出口（同 Options.Warnf）：各域实现输出非致命异常（如规则读取失败降级）用；
	// nil = 静默（行为与不输出等价）。
	Warnf func(format string, args ...any)
}

// NewBase 构造共享内核（不订阅；订阅由 persist 负责）。
func NewBase(bus mq.Bus, opts Options) *Base {
	view := NewView()
	view.warnf = opts.Warnf
	return &Base{Bus: bus, UsrPath: opts.UsrPath, AppDir: opts.AppDir, View: view, Warnf: opts.Warnf}
}

// Info 是单个实例的数据根绑定（work_dir/data_dir，61-消息一览 §4.1 payload）。
type Info struct {
	WorkDir string
	DataDir string
	// LastBeat 最近注册/心跳时刻（仅分离形态的超时扫描消费；默认构建只记录不判定，
	// 与 plugin-codegraph/vfts 的 ir.last 同口径）。
	LastBeat time.Time
}

// View 是实例数据根视图（自持；原 persist.Service 的 insts + mu 逐字下移）。
//
// 并发安全：全部方法持内部互斥锁（与装配前的 s.mu 同口径）。
type View struct {
	mu    sync.Mutex
	insts map[string]Info
	// warnf 告警出口（见 Options.Warnf；nil = 不输出，行为不变）。
	warnf func(format string, args ...any)
}

// NewView 构造空视图。
func NewView() *View { return &View{insts: make(map[string]Info)} }

// Register 登记/刷新实例绑定（空 instance_id 忽略；幂等）。
func (v *View) Register(instanceID, workDir, dataDir string) {
	if v == nil || instanceID == "" {
		return
	}
	v.mu.Lock()
	v.insts[instanceID] = Info{WorkDir: workDir, DataDir: dataDir, LastBeat: time.Now()}
	v.mu.Unlock()
	// 同源登记 data 组件绑定表（两视图恒一致）：使 data.Register 成为**在册实例的完整登记**，
	// 供 data.InstancesByWorkDir 按 work_dir 枚举同项目全部实例（G-41-b）。空键已被上方守卫拦下。
	_ = data.Register(instanceID, workDir, dataDir)
}

// Touch 心跳保活：刷新 LastBeat（仅已登记实例；未登记不新建——与既有心跳口径一致）。
func (v *View) Touch(instanceID string) { v.TouchAt(instanceID, time.Now()) }

// TouchAt 以指定时刻刷新 LastBeat（心跳用 Touch；测试可借此模拟心跳停摆）。
// 未登记实例 → ok=false。
func (v *View) TouchAt(instanceID string, at time.Time) bool {
	if v == nil || instanceID == "" {
		return false
	}
	v.mu.Lock()
	info, ok := v.insts[instanceID]
	if ok {
		info.LastBeat = at
		v.insts[instanceID] = info
	}
	v.mu.Unlock()
	return ok
}

// Exit 释放实例数据根绑定：移除实例视图 + data.Unregister（使后续按该 instance 解析库连接
// 报"未登记"）。显式 `instance-exit` 与分离形态心跳超时**共用同一实现**；幂等（空键无害）。
func (v *View) Exit(instanceID string) {
	if v == nil || instanceID == "" {
		return
	}
	v.mu.Lock()
	delete(v.insts, instanceID)
	v.mu.Unlock()
	dataUnregister(instanceID)
}

// Lookup 查实例数据根绑定（未登记 → ok=false）。
func (v *View) Lookup(instanceID string) (Info, bool) {
	if v == nil {
		return Info{}, false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	info, ok := v.insts[instanceID]
	return info, ok
}

// warn 输出告警（warnf 未注入 → 静默；行为与下沉前逐字等价）。
func (v *View) warn(format string, args ...any) {
	if v.warnf != nil {
		v.warnf(format, args...)
	}
}

// Resolve 定位实例绑定：显式 instance_id 优先；缺省 = 唯一实例回退（合并单进程
// GUI 形态单实例，B 类三域请求可不带 instance_id；多实例时须显式携带，否则报错）。
//
// MW-11：唯一实例回退**保持可用**（单实例场景行为逐条不变），但两条路径都输出告警 ——
// 让「未显式携带 instance_id」的遗漏显式暴露（多实例上线前逐步清零）。
//
// 严格路径（需暴露遗漏）用本方法；**容错探测**路径（解析失败由调用方兜底，属预期内）用
// ResolveQuiet —— 否则会在实例未登记 / 多实例的正常探测路径刷"拒绝回退"告警。
func (v *View) Resolve(instanceID string) (string, Info, error) {
	return v.resolve(instanceID, true)
}

// ResolveQuiet 语义与 Resolve **逐条一致**，但不输出告警：供容错探测调用方（如
// InstBindingFor）使用，避免正常探测路径刷告警噪音（见 Resolve 注）。
func (v *View) ResolveQuiet(instanceID string) (string, Info, error) {
	return v.resolve(instanceID, false)
}

// resolve 是 Resolve / ResolveQuiet 的共用实现（warn = 是否输出告警）。
func (v *View) resolve(instanceID string, warn bool) (string, Info, error) {
	if instanceID != "" {
		info, ok := v.Lookup(instanceID)
		if !ok {
			return "", Info{}, ErrInstanceNotRegistered
		}
		return instanceID, info, nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.insts) == 1 {
		for id, info := range v.insts {
			if warn {
				v.warn("persist: 未带 instance_id，回退唯一实例 %s（调用方应显式携带 instance_id）", id)
			}
			return id, info, nil
		}
	}
	if warn {
		v.warn("persist: 未带 instance_id 且实例视图非唯一（已登记 %d 个），拒绝回退", len(v.insts))
	}
	return "", Info{}, ErrInstanceIDRequired
}

// Stale 返回最近心跳距今 ≥ timeout 的实例 id（心跳时刻未记录的不判超时，与
// instance.Manager.Sweep 同口径）；先快照再清理的口径不变（幂等由调用方保证）。
func (v *View) Stale(now time.Time, timeout time.Duration) []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	var stale []string
	for id, info := range v.insts {
		if info.LastBeat.IsZero() || now.Sub(info.LastBeat) < timeout {
			continue
		}
		stale = append(stale, id)
	}
	return stale
}
