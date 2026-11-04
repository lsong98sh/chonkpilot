package workspace

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
	"github.com/lsong98sh/chonkpilot-plugin-framework/runtime/instance"
	"github.com/lsong98sh/chonkpilot-util/pathx"

	"github.com/chonkpilot/chonkpilot-workspace/layout"
)

// instanceOrder 是工作区对 `instance-register` / `instance-exit` 的订阅顺序（P5 步骤 1）。
//
// 取 **-1** = 先于共通实例视图（`instance.Manager.Start` 的 order 0）执行：本包在
// 自己的回调里驱动 `instance.Manager.HandleRegister/HandleExit`，因此读到的实例状态
// **恒为本条事件处理后的确定值**（不依赖两个订阅者的注册先后）；后者随后的同名处理
// 幂等（同 id 重复登记 / 重复退出均无副作用）。
const instanceOrder = -1

// Options 是 Manager 构造参数。
type Options struct {
	// Logf 诊断输出（nil = slog.Warn）。
	Logf func(format string, args ...any)
	// Instances 是共通的实例视图（单一数据源，P5 步骤 1 / C15）。nil = 本包自建一个
	// **不自订阅**的 `instance.Manager`（由本包的回调驱动），行为与注入共享视图一致。
	// 宿主装配期应注入与 server / mqpolicy 相同的实例视图，避免多份投影。
	Instances *instance.Manager
}

// Space 是一个工作区快照（**不持有实例集合**：实例视图唯一来源 = instance.Manager）。
type Space struct {
	Layout
	OpenedAt time.Time
}

// Manager 是工作区管理器（进程内单例）。
type Manager struct {
	bus  mq.Bus
	logf func(format string, args ...any)

	// inst 是实例视图（唯一数据源）：Resolve / InstanceIDs / 开闭判定全部读它。
	inst *instance.Manager
	// ownsInst = true 表示 inst 由本包自建（Stop 时一并停用）。
	ownsInst bool

	// router 是 MQ 分发业务策略（PM 的 `Route`，P5 步骤 3）；与 host 的 mqpolicy 共享同一份。
	router *Router

	mu     sync.Mutex
	spaces map[string]*Space // key = normKey(work_dir)
	ops    ProjectOps        // 窗口/会话动作端口（nil = 未接线；见 pm.go）

	subs []mq.Sub
}

// New 构造工作区管理器（未启动，调用 Start 后开始订阅）。
func New(bus mq.Bus, opts Options) *Manager {
	logf := opts.Logf
	if logf == nil {
		logf = func(format string, args ...any) { slog.Warn(format, args...) }
	}
	inst := opts.Instances
	owns := false
	if inst == nil {
		inst = instance.New(bus)
		owns = true
	}
	m := &Manager{
		bus:      bus,
		logf:     logf,
		inst:     inst,
		ownsInst: owns,
		router:   NewRouter(),
		spaces:   make(map[string]*Space),
	}
	// 路由反查视图默认 = 本 PM 的实例视图（单源；宿主可经 SetInstanceWorkDir 覆盖为等价实现）。
	m.router.SetInstanceWorkDir(func(instanceID string) (string, bool) {
		info, ok := m.inst.Lookup(instanceID)
		if !ok || info.WorkDir == "" {
			return "", false
		}
		return info.WorkDir, true
	})
	return m
}

// Instances 返回本管理器使用的实例视图（单一数据源）。
// 宿主可据此把同一视图注入其它消费者（mqpolicy / 插件桥）。
func (m *Manager) Instances() *instance.Manager { return m.inst }

// Start 订阅实例生命周期（instance-register / instance-exit），开始维护工作区打开态。
func (m *Manager) Start(_ context.Context) error {
	for _, s := range []struct {
		subject string
		h       func(context.Context, string, *mq.Value) error
	}{
		{msgkeys.TopicInstanceRegister, m.onRegister},
		{msgkeys.TopicInstanceExit, m.onExit},
	} {
		sub, err := m.bus.On(s.subject, instanceOrder, s.h)
		if err != nil {
			m.Stop()
			return err
		}
		m.subs = append(m.subs, sub)
	}
	return nil
}

// Stop 退订并清空内存态（不删任何磁盘内容）。
func (m *Manager) Stop() {
	for _, s := range m.subs {
		_ = s.Unsubscribe()
	}
	m.subs = nil
	m.mu.Lock()
	m.spaces = make(map[string]*Space)
	m.mu.Unlock()
	if m.ownsInst && m.inst != nil {
		m.inst.Stop()
	}
}

// Resolve 按实例解析工作区（未登记 → false）。
func (m *Manager) Resolve(instanceID string) (Space, bool) {
	if instanceID == "" {
		return Space{}, false
	}
	// 实例 → work_dir 的唯一来源（本包不再自建 byInst 投影）。
	info, ok := m.inst.Lookup(instanceID)
	if !ok || info.WorkDir == "" {
		return Space{}, false
	}
	m.mu.Lock()
	s := m.spaces[normKey(info.WorkDir)]
	var sp Space
	if s != nil {
		sp = s.snapshot()
	} else {
		ok = false
	}
	m.mu.Unlock()
	return sp, ok
}

// Get 按工作目录取工作区快照（大小写不敏感，Windows 口径）。
func (m *Manager) Get(workDir string) (Space, bool) {
	m.mu.Lock()
	s := m.spaces[normKey(workDir)]
	var sp Space
	ok := s != nil
	if ok {
		sp = s.snapshot()
	}
	m.mu.Unlock()
	return sp, ok
}

// List 返回全部打开中的工作区快照（无序）。
func (m *Manager) List() []Space {
	m.mu.Lock()
	out := make([]Space, 0, len(m.spaces))
	for _, s := range m.spaces {
		out = append(out, s.snapshot())
	}
	m.mu.Unlock()
	return out
}

// InstanceIDs 返回某工作区当前活动实例 id（无序；事件定向投递用）。
// 唯一来源 = instance.Manager（同 work_dir，大小写 / 分隔符不敏感）。
func (m *Manager) InstanceIDs(workDir string) []string {
	if strings.TrimSpace(workDir) == "" {
		return nil
	}
	all := m.inst.List()
	ids := make([]string, 0, len(all))
	for _, in := range all {
		if pathx.Same(in.WorkDir, workDir) {
			ids = append(ids, in.ID)
		}
	}
	return ids
}

// Check 校验 p 在该工作区内（词法 + 真实路径），返回词法绝对路径。
func (m *Manager) Check(sp Space, p string) (string, bool) {
	return layout.AbsWithin(sp.WorkDir, p)
}

// ─── 实例生命周期 ───────────────────────────────────────

func (m *Manager) onRegister(_ context.Context, _ string, v *mq.Value) error {
	var req struct {
		InstanceID string `json:"instance_id"`
		WorkDir    string `json:"work_dir"`
		DataDir    string `json:"data_dir"`
	}
	_ = json.Unmarshal(v.Payload, &req)
	if req.InstanceID == "" || req.WorkDir == "" {
		return nil
	}
	// 单一写者：由本回调驱动唯一实例视图（order -1，先于 instance.Manager 自订阅）。
	if err := m.inst.HandleRegister(v.Payload); err != nil {
		m.logf("workspace: register %q rejected: %v", req.InstanceID, err)
		return nil
	}
	key := normKey(req.WorkDir)

	m.mu.Lock()
	if m.spaces[key] == nil {
		lay := layout.Of(req.WorkDir, req.DataDir)
		layout.EnsureDirs(lay, m.logf)
		m.spaces[key] = &Space{Layout: lay, OpenedAt: time.Now()}
		m.mu.Unlock()
		m.emit(msgkeys.TopicWorkdirOpened, lay.WorkDir, lay.DataDir, req.InstanceID)
		return nil
	}
	m.mu.Unlock()
	// 实例改绑工作区（少见）：旧工作区若无实例则摘除（**不发 closed**，与改前一致）。
	m.reapSilently()
	return nil
}

func (m *Manager) onExit(_ context.Context, _ string, v *mq.Value) error {
	var req struct {
		InstanceID string `json:"instance_id"`
	}
	_ = json.Unmarshal(v.Payload, &req)
	if req.InstanceID == "" {
		return nil
	}
	// 先取退出实例的工作目录，再从唯一实例视图摘除（order -1：此刻记录仍在）。
	info, known := m.inst.Lookup(req.InstanceID)
	_ = m.inst.HandleExit(v.Payload)
	if !known || info.WorkDir == "" {
		return nil
	}
	key := normKey(info.WorkDir)

	m.mu.Lock()
	sp := m.spaces[key]
	// 同 work_dir 仍有其它实例 → 工作区保持打开。
	if sp == nil || m.hasInstancesLocked(info.WorkDir) {
		m.mu.Unlock()
		return nil
	}
	delete(m.spaces, key)
	m.mu.Unlock()
	m.emit(msgkeys.TopicWorkdirClosed, sp.Layout.WorkDir, sp.Layout.DataDir, req.InstanceID)
	return nil
}

// reapSilently 摘除「已无实例」的工作区（不发事件；register 改绑场景用）。
func (m *Manager) reapSilently() {
	m.mu.Lock()
	for key, sp := range m.spaces {
		if !m.hasInstancesLocked(sp.WorkDir) {
			delete(m.spaces, key)
		}
	}
	m.mu.Unlock()
}

// hasInstancesLocked 判定某 work_dir 是否仍有活动实例（唯一来源 = instance.Manager）。
// 调用方须持 m.mu。
func (m *Manager) hasInstancesLocked(workDir string) bool {
	for _, in := range m.inst.List() {
		if pathx.Same(in.WorkDir, workDir) {
			return true
		}
	}
	return false
}

// emit 广播工作区事件（61 §2.5）：{work_dir, instance_id, data_dir}。
func (m *Manager) emit(topic, workDir, dataDir, instanceID string) {
	payload := map[string]any{
		msgkeys.FieldWorkDir:    workDir,
		msgkeys.FieldInstanceId: instanceID,
	}
	if dataDir != "" {
		payload[msgkeys.FieldDataDir] = dataDir
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		m.logf("workspace: marshal %s failed: %v", topic, err)
		return
	}
	_ = m.bus.Emit(context.Background(), topic, raw)
}

func (s *Space) snapshot() Space {
	return Space{Layout: s.Layout, OpenedAt: s.OpenedAt}
}

// normKey 归一工作目录键（Clean + 大小写不敏感，Windows 口径）。
func normKey(workDir string) string {
	return strings.ToLower(strings.TrimSpace(workDir))
}
