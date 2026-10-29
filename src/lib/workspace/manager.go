package workspace

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
)

// Options 是 Manager 构造参数。
type Options struct {
	// Logf 诊断输出（nil = slog.Warn）。
	Logf func(format string, args ...any)
}

// Instance 是一个活动实例的工作区属性（仅用于计数与归属，不承担实例身份服务）。
type Instance struct {
	ID             string
	WorkDir        string
	DataDir        string
	ClientType     string
	User           string
	ParentInstance string
}

// Space 是一个工作区快照。
type Space struct {
	Layout
	Instances map[string]Instance
	OpenedAt  time.Time
}

// Manager 是工作区管理器（进程内单例）。
type Manager struct {
	bus  mq.Bus
	logf func(format string, args ...any)

	mu     sync.Mutex
	spaces map[string]*Space // key = normKey(work_dir)
	byInst map[string]string // instance_id → key

	subs []mq.Sub
}

// New 构造工作区管理器（未启动，调用 Start 后开始订阅）。
func New(bus mq.Bus, opts Options) *Manager {
	logf := opts.Logf
	if logf == nil {
		logf = func(format string, args ...any) { slog.Warn(format, args...) }
	}
	return &Manager{
		bus:    bus,
		logf:   logf,
		spaces: make(map[string]*Space),
		byInst: make(map[string]string),
	}
}

// Start 订阅实例生命周期（instance-register / instance-exit），开始维护工作区引用计数。
func (m *Manager) Start(_ context.Context) error {
	for _, s := range []struct {
		subject string
		h       func(context.Context, string, *mq.Value) error
	}{
		{msgkeys.TopicInstanceRegister, m.onRegister},
		{msgkeys.TopicInstanceExit, m.onExit},
	} {
		sub, err := m.bus.On(s.subject, 0, s.h)
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
	m.byInst = make(map[string]string)
	m.mu.Unlock()
}

// Resolve 按实例解析工作区（未登记 → false）。
func (m *Manager) Resolve(instanceID string) (Space, bool) {
	if instanceID == "" {
		return Space{}, false
	}
	m.mu.Lock()
	key, ok := m.byInst[instanceID]
	var sp Space
	if ok {
		if s := m.spaces[key]; s != nil {
			sp = s.snapshot()
		} else {
			ok = false
		}
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
func (m *Manager) InstanceIDs(workDir string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.spaces[normKey(workDir)]
	if s == nil {
		return nil
	}
	ids := make([]string, 0, len(s.Instances))
	for id := range s.Instances {
		ids = append(ids, id)
	}
	return ids
}

// Check 校验 p 在该工作区内（词法 + 真实路径），返回词法绝对路径。
func (m *Manager) Check(sp Space, p string) (string, bool) {
	return AbsWithin(sp.WorkDir, p)
}

// ─── 实例生命周期 ───────────────────────────────────────

func (m *Manager) onRegister(_ context.Context, _ string, v *mq.Value) error {
	var req struct {
		InstanceID     string `json:"instance_id"`
		WorkDir        string `json:"work_dir"`
		DataDir        string `json:"data_dir"`
		ClientType     string `json:"client_type"`
		User           string `json:"user"`
		ParentInstance string `json:"parent_instance"`
	}
	_ = json.Unmarshal(v.Payload, &req)
	if req.InstanceID == "" || req.WorkDir == "" {
		return nil
	}
	inst := Instance{
		ID:             req.InstanceID,
		WorkDir:        req.WorkDir,
		DataDir:        req.DataDir,
		ClientType:     req.ClientType,
		User:           req.User,
		ParentInstance: req.ParentInstance,
	}
	key := normKey(req.WorkDir)

	m.mu.Lock()
	// 同实例重复注册：幂等（不重复计数）。
	if prev, ok := m.byInst[inst.ID]; ok && prev == key {
		if s := m.spaces[key]; s != nil {
			s.Instances[inst.ID] = inst
		}
		m.mu.Unlock()
		return nil
	}
	// 实例改绑工作区（少见）：先从旧工作区摘除。
	if prev, ok := m.byInst[inst.ID]; ok {
		m.detachLocked(inst.ID, prev, "")
	}
	sp := m.spaces[key]
	if sp == nil {
		lay := LayoutOf(req.WorkDir, req.DataDir)
		m.ensureDirs(lay)
		sp = &Space{Layout: lay, Instances: make(map[string]Instance), OpenedAt: time.Now()}
		m.spaces[key] = sp
		sp.Instances[inst.ID] = inst
		m.byInst[inst.ID] = key
		m.mu.Unlock()
		m.emit(msgkeys.TopicWorkdirOpened, inst)
		return nil
	}
	sp.Instances[inst.ID] = inst
	m.byInst[inst.ID] = key
	m.mu.Unlock()
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

	m.mu.Lock()
	key, ok := m.byInst[req.InstanceID]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	closed, inst := m.detachLocked(req.InstanceID, key, req.InstanceID)
	m.mu.Unlock()
	if closed {
		m.emit(msgkeys.TopicWorkdirClosed, inst)
	}
	return nil
}

// detachLocked 从工作区摘除实例；归零时删除工作区并返回 closed=true 与事件 payload。
// 调用方须持 m.mu（emit 在解锁后执行）。
func (m *Manager) detachLocked(instanceID, key string, _ string) (bool, Instance) {
	sp := m.spaces[key]
	delete(m.byInst, instanceID)
	if sp == nil {
		return false, Instance{ID: instanceID}
	}
	inst, had := sp.Instances[instanceID]
	delete(sp.Instances, instanceID)
	if len(sp.Instances) > 0 || !had {
		return false, Instance{ID: instanceID}
	}
	delete(m.spaces, key)
	inst.ID = instanceID // 触发归零的实例 id
	return true, inst
}

// ensureDirs 在工作区打开时初始化目录（**只建不删**）。失败仅留痕，不阻断。
func (m *Manager) ensureDirs(l Layout) {
	for _, dir := range []string{l.PrjDir, l.PrjUsrDir} {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			m.logf("workspace: mkdir %q failed: %v", dir, err)
		}
	}
}

// emit 广播工作区事件（61 §2.5）：{work_dir, instance_id, data_dir}。
func (m *Manager) emit(topic string, inst Instance) {
	payload := map[string]any{
		msgkeys.FieldWorkDir:    inst.WorkDir,
		msgkeys.FieldInstanceId: inst.ID,
	}
	if inst.DataDir != "" {
		payload[msgkeys.FieldDataDir] = inst.DataDir
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		m.logf("workspace: marshal %s failed: %v", topic, err)
		return
	}
	_ = m.bus.Emit(context.Background(), topic, raw)
}

func (s *Space) snapshot() Space {
	cp := Space{Layout: s.Layout, OpenedAt: s.OpenedAt, Instances: make(map[string]Instance, len(s.Instances))}
	for k, v := range s.Instances {
		cp.Instances[k] = v
	}
	return cp
}

// normKey 归一工作目录键（Clean + 大小写不敏感，Windows 口径）。
func normKey(workDir string) string {
	return strings.ToLower(strings.TrimSpace(workDir))
}
