// Package instance 是 instanceManager——共通实例视图（对齐 61-消息一览 §4.1）：
//
// 订阅 `instance-register` / `instance-heartbeat` / `instance-exit` 三个**相对主题**
// （GUI 客户端实例消息；总线命名空间前缀 "chonk." 在 mq 初始化时注入一次，
// 业务层不出现 chonk. 字面），维护 `instance_id ↔ {work_dir, data_dir, client_type, alive}` 映射。
//
// 用途：server 内嵌用于路由/退出清理；插件按需引用（如要按实例定位/回话）。
// 不在 chonkpilot-data（数据层）——监听 MQ 维护映射是通信/上下文层职责。
//
// work_dir/data_dir 可重复（多 CLI 同 workdir），instance_id 是唯一路由标识，不可去掉。
package instance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// 订阅主题（GUI 客户端实例消息，相对主题 instance-*；对齐 61-消息一览 §4.1。
// 2026-09-03 增补：chonk. 前缀已移入总线初始化 Options.Prefix，此处只写相对名）。
const (
	SubjectRegister  = "instance-register"
	SubjectHeartbeat = "instance-heartbeat"
	SubjectExit      = "instance-exit"
)

// DefaultMaxInstances 是单进程可登记的实例数缺省上限（2026-09-19，缺口 7）。
// 同进程服务多 instance（split 服务端 exe / 未来 browser）时防无界增长；
// 超限注册被**明确拒绝**（HandleRegister 返回错误 + stderr 告警），已登记实例的刷新不受限。
const DefaultMaxInstances = 64

// Info 是单个实例的注册视图。
type Info struct {
	ID         string
	WorkDir    string
	DataDir    string
	ClientType string
	LastBeat   time.Time // 最近心跳/注册时间（仅记录；合并单进程形态不据此判退出）
}

// Manager 监听注册/心跳/退出，维护实例视图（线程安全；自订阅需 Start）。
type Manager struct {
	bus  mq.Bus
	mu   sync.RWMutex
	recs map[string]*Info
	subs []mq.Sub
	now  func() time.Time // 可注入时钟（测试）
	max  int              // 实例数上限（<=0 = 不限；DefaultMaxInstances 为缺省）
}

// New 构建 Manager（不订阅；需 Start 或由宿主路由驱动 Handle*）。上限 = DefaultMaxInstances。
func New(bus mq.Bus) *Manager {
	return &Manager{bus: bus, recs: make(map[string]*Info), now: time.Now, max: DefaultMaxInstances}
}

// NewWithLimit 构建 Manager 并指定实例数上限（<=0 = 不限；测试/宿主可配）。
func NewWithLimit(bus mq.Bus, max int) *Manager {
	m := New(bus)
	m.SetMaxInstances(max)
	return m
}

// SetMaxInstances 设置实例数上限（线程安全；<=0 = 不限）。仅限制**新增**实例：
// 已登记实例的注册刷新/心跳/退出不受影响。
func (m *Manager) SetMaxInstances(max int) {
	m.mu.Lock()
	m.max = max
	m.mu.Unlock()
}

// MaxInstances 返回当前上限（<=0 = 不限）。
func (m *Manager) MaxInstances() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.max
}

// SetClock 注入时钟（测试口：供独立测试模块注入确定时间，验证心跳刷新/注册时间；
// 传 nil 恢复默认 time.Now）。生产代码不调用本方法，行为与默认时钟完全一致。
func (m *Manager) SetClock(now func() time.Time) {
	if now == nil {
		now = time.Now
	}
	m.now = now
}

// Start 订阅三个主题（自驱动模式；server 内嵌、插件引用均可用）。
// 合并单进程形态（GUI/CLI 内嵌）下实例不发布心跳、随进程生命周期结束，退出只由显式
// `instance-exit` 处理——不做心跳超时清理（无超时判定；分离形态将来按形态标识再加）。
// 注册超限（缺口 7）→ HandleRegister 返回错误，此处**告警到 stderr**（不静默丢）。
func (m *Manager) Start() error {
	for _, s := range []struct {
		subject string
		h       func(string, []byte) error
	}{
		{SubjectRegister, m.onRegister},
		{SubjectHeartbeat, m.onHeartbeat},
		{SubjectExit, m.onExit},
	} {
		sub, err := m.bus.On(s.subject, 0, func(_ context.Context, subj string, v *mq.Value) error {
			if herr := s.h(subj, v.Payload); herr != nil {
				fmt.Fprintf(os.Stderr, "[instance] %v\n", herr)
			}
			return nil
		})
		if err != nil {
			m.Stop()
			return err
		}
		m.subs = append(m.subs, sub)
	}
	return nil
}

// Stop 退订全部。
func (m *Manager) Stop() {
	for _, s := range m.subs {
		_ = s.Unsubscribe()
	}
	m.subs = nil
}

// Lookup 查实例（不存在 → ok=false）。
func (m *Manager) Lookup(id string) (Info, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.recs[id]
	if !ok {
		return Info{}, false
	}
	return *r, true
}

// List 返回全部实例（无序）。
func (m *Manager) List() []Info {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Info, 0, len(m.recs))
	for _, r := range m.recs {
		out = append(out, *r)
	}
	return out
}

// Count 实例数。
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.recs)
}

// HandleRegister 处理 instance-register（宿主路由模式可直调；自驱动模式由订阅触发）。
// 返回错误仅表示**注册被拒**（缺口 7：实例数已达上限）——已登记实例的刷新与空 id 忽略**不报错**
// （保持既有宽容语义）。检查与插入同在写锁内完成（并发注册不会绕过上限）。
func (m *Manager) HandleRegister(payload []byte) error {
	var req struct {
		InstanceID string `json:"instance_id"`
		ClientType string `json:"client_type,omitempty"`
		WorkDir    string `json:"work_dir"`
		DataDir    string `json:"data_dir,omitempty"`
	}
	_ = json.Unmarshal(payload, &req)
	if req.InstanceID == "" {
		return nil
	}
	now := m.now()
	m.mu.Lock()
	current, exists := m.recs[req.InstanceID]
	if !exists {
		if m.max > 0 && len(m.recs) >= m.max {
			limit := m.max
			m.mu.Unlock()
			return fmt.Errorf("实例数已达上限（%d），拒绝注册 %s", limit, req.InstanceID)
		}
		current = &Info{ID: req.InstanceID}
		m.recs[req.InstanceID] = current
	}
	current.WorkDir = req.WorkDir
	current.DataDir = req.DataDir
	current.ClientType = req.ClientType
	current.LastBeat = now
	m.mu.Unlock()
	return nil
}

// HandleHeartbeat 处理心跳（刷新 LastBeat）。
func (m *Manager) HandleHeartbeat(payload []byte) error {
	var msg struct {
		InstanceID string `json:"instance_id"`
	}
	_ = json.Unmarshal(payload, &msg)
	if msg.InstanceID == "" {
		return nil
	}
	m.mu.Lock()
	if r, ok := m.recs[msg.InstanceID]; ok {
		r.LastBeat = m.now()
	}
	m.mu.Unlock()
	return nil
}

// HandleExit 处理退出（移除实例）。
func (m *Manager) HandleExit(payload []byte) error {
	var msg struct {
		InstanceID string `json:"instance_id"`
	}
	_ = json.Unmarshal(payload, &msg)
	if msg.InstanceID == "" {
		return nil
	}
	m.mu.Lock()
	delete(m.recs, msg.InstanceID)
	m.mu.Unlock()
	return nil
}

func (m *Manager) onRegister(subj string, p []byte) error  { return m.HandleRegister(p) }
func (m *Manager) onHeartbeat(subj string, p []byte) error { return m.HandleHeartbeat(p) }
func (m *Manager) onExit(subj string, p []byte) error      { return m.HandleExit(p) }
