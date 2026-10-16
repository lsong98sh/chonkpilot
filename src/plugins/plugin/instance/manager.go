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
	"path/filepath"
	"strings"
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
// 超限注册被**明确拒绝**（HandleRegister 返回错误 + 告警上报：宿主日志，无宿主时回落 stderr），
// 已登记实例的刷新不受限。
const DefaultMaxInstances = 64

// Info 是单个实例的注册视图。
type Info struct {
	ID         string
	WorkDir    string
	DataDir    string
	ClientType string
	// User 是实例归属用户（OS 用户名；instance-register 的 `user` 字段，61-消息一览 §4.1 ⑤，
	// 🆕 2026-10-09，**必填**）。用途：instance-bring-to-top 的**跨用户判定**（RDS 多用户下，
	// 同 data_dir 的顶层实例若属其他用户 → 不置顶、返回 instance-forbidden）。
	User string
	// ParentInstance 是子窗口实例的宿主顶层实例 id（空 = 顶层实例）。
	// 由 instance-register 的 `parent_instance` 字段解析（61-消息一览 §4.1 ④，🆕 2026-10-09）：
	// flash 形态下，同一个 instance 多开的窗口（共享同一 data_dir）注册时必带；server 托盘
	// 「列举实例」据此过滤（仅顶层实例入菜单，见 ListTopLevel）。
	ParentInstance string
	// SessionID 是本实例当前承载的会话 id（空 = 未指定，如主窗口）。
	// 由 instance-register 的 `session_id` 字段解析（61-消息一览 §4.1 ④，🆕 2026-10-16 SUBCHAT-3
	// 阶段 F-2）：对话/群聊窗口建窗时由窗口工厂随桥上报（= 该窗口绑定的会话）→ 支持
	// **session → instance 反查**（见 InstanceForSession），供 llm-server 把定向消息
	// （如 `session-ask`）投到**该会话所在窗口**。
	SessionID string
	LastBeat  time.Time // 最近心跳/注册时间（仅记录；合并单进程形态不据此判退出）
}

// Manager 监听注册/心跳/退出，维护实例视图（线程安全；自订阅需 Start）。
type Manager struct {
	bus  mq.Bus
	mu   sync.RWMutex
	recs map[string]*Info
	subs []mq.Sub
	now  func() time.Time // 可注入时钟（测试）
	max  int              // 实例数上限（<=0 = 不限；DefaultMaxInstances 为缺省）
	// logf 告警上报（宿主注入；nil = 回落 stderr，见 reportWarn）。Start 前经 SetLogf 注入。
	logf func(format string, args ...any)
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

// SetLogf 注入告警上报函数（宿主在 Start 前调用；传 nil = 回落 stderr，自力/无宿主模式）。
// 注册超限等告警经此上报宿主日志（GUI 形态 stderr 不可见，必须落宿主日志）。
func (m *Manager) SetLogf(logf func(format string, args ...any)) {
	m.mu.Lock()
	m.logf = logf
	m.mu.Unlock()
}

// reportWarn 上报一条告警：已注入 logf → 走宿主日志；否则回落 stderr（兜底，不静默丢）。
func (m *Manager) reportWarn(format string, args ...any) {
	m.mu.RLock()
	logf := m.logf
	m.mu.RUnlock()
	if logf != nil {
		logf(format, args...)
		return
	}
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// Start 订阅三个主题（自驱动模式；server 内嵌、插件引用均可用）。
// 合并单进程形态（GUI/CLI 内嵌）下实例不发布心跳、随进程生命周期结束，退出只由显式
// `instance-exit` 处理——不做心跳超时清理（无超时判定；分离形态将来按形态标识再加）。
// 注册超限（缺口 7）→ HandleRegister 返回错误，此处**告警上报**（经 SetLogf 注入的宿主日志；
// 未注入时回落 stderr，不静默丢）。
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
				m.reportWarn("[instance] %v", herr)
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

// TopLevelByDataDir 按 `data_dir` 反查**顶层实例**（ParentInstance 为空），供
// `instance-bring-to-top` 使用（61-消息一览 §4.1 ⑥，🆕 2026-10-09）：
// 同一 `data_dir` 可能同时存在顶层实例与其子窗口实例（共享 data_dir）→ 只取顶层。
// 命中多个（异常装配 / 跨用户重开）→ 返回**首个**（调用方再按 `User` 判定，不一致即拒绝）。
// 未命中 → (Info{}, false)。
func (m *Manager) TopLevelByDataDir(dataDir string) (Info, bool) {
	if dataDir == "" {
		return Info{}, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, r := range m.recs {
		if r.ParentInstance == "" && samePath(r.DataDir, dataDir) {
			return *r, true
		}
	}
	return Info{}, false
}

// samePath 比较两个目录是否等价（大小写不敏感 + 分隔符归一；Windows 路径语义）。
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// ListTopLevel 返回**顶层实例**（ParentInstance 为空；不含子窗口实例），无序。
// server 托盘「列举实例」据此过滤（61-消息一览 §4.1 ④：子窗口实例不入菜单）。
func (m *Manager) ListTopLevel() []Info {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Info, 0, len(m.recs))
	for _, r := range m.recs {
		if r.ParentInstance != "" {
			continue
		}
		out = append(out, *r)
	}
	return out
}

// ParentOfInstance 返回实例的**直接父实例** id（"谁开的指谁"·**逐层链**，群聊 §14.4 N20）：
// 未登记 / 顶层实例（无父）→ ("", false)。
//
// 用途 = **跨窗口定向投递**的取表环节（单聊 §12.4 B4 / 群聊 §14.2）：子窗口实例要投给父窗口时，
// 先由它取到父实例 id，再填进消息的 `instance_id` 字段（见 DeliveryTarget）。
func (m *Manager) ParentOfInstance(instanceID string) (string, bool) {
	if instanceID == "" {
		return "", false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if r, ok := m.recs[instanceID]; ok && r.ParentInstance != "" {
		return r.ParentInstance, true
	}
	return "", false
}

// InstanceForSession 按**会话 id 反查承载它的窗口实例 id**（session → instance 反查，🆕 2026-10-16
// SUBCHAT-3 阶段 F-2 · 群聊 §13 D42 / §15.4 N23；闭环 41 D-62）：
// 实例注册时随 `instance-register.session_id` 自报其承载的会话（对话窗口 = 绑定会话，群聊窗口 =
// 群聊会话）→ 此处反查命中「该会话所在的窗口实例」。未登记 / 无实例承载该会话 → ("", false)。
//
// 用途 = llm-server 把**定向消息**（如 `session-ask`）投到该会话所在窗口（把返回的实例 id 填进
// 消息 `instance_id`；桥按精确相等过滤天然投给它，**不改桥**）。与 ParentOfInstance（子→父）反向互补。
//
// 同一会话被多个实例登记（异常装配）→ 返回**首个**命中（与 TopLevelByDataDir 同口径）。
func (m *Manager) InstanceForSession(sessionID string) (string, bool) {
	if sessionID == "" {
		return "", false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, r := range m.recs {
		if r.SessionID != "" && r.SessionID == sessionID {
			return r.ID, true
		}
	}
	return "", false
}

// DeliveryTarget 解析「把产生自 origin 实例的一条跨窗口事件**定向投递**给谁」——返回应写入该
// 消息 `instance_id` 字段的值（群聊 §14.2 / §14.4 N19 裁决：**不改桥** —— 桥按 instance_id
// **精确相等**过滤，填目标 id 即天然投给它；「父 + 子都要」时不带 `instance_id`，走桥的
// 「唯一例外」放行给所有窗口）。单聊 / 群聊共用（单聊 §12.4 B4）。
//
// 语义（targeted=false ⇒ 调用方按**广播**投递，即消息不带 `instance_id`）：
//   - toParent=false → ("", false)：父 + 子都要 → 广播；
//   - toParent=true 且 origin 有直接父 → (父实例 id, true)：定向投给**父窗口**；
//   - toParent=true 但 origin 未登记 / 顶层无父 → ("", false)：无定向目标，退化为广播。
func (m *Manager) DeliveryTarget(origin string, toParent bool) (target string, targeted bool) {
	if !toParent {
		return "", false
	}
	if p, ok := m.ParentOfInstance(origin); ok {
		return p, true
	}
	return "", false
}

// MenuInstances 过滤托盘「列举实例」菜单项（61-消息一览 §4.1 ⑥ · 62 §3A）。
//
// 收录规则（2026-10-10 定）：
//   - 仅 **顶层实例**（`ParentInstance` 为空；子窗口实例不入菜单）——调用方通常已用
//     ListTopLevel 过滤，此处再判一次以保证纯函数自洽；
//   - **排除 browser 形态**：判据 = 该实例**是否持有 UDS 前门连接**（`connected(id)`）——
//     browser 走 httpapi、无 UDS 连接；flash 经 UDS 前门接入 → connected=true。
//     连接判据经参数注入（只读、无副作用，生产 = `udsapi.Gateway.Connected`）；
//   - **仅收录 `user == 当前用户`** 的实例（跨用户不列，避免向他人暴露项目）。
//
// 纯函数（无副作用、不涉 MQ）：供宿主组装托盘菜单 + 独立单测。`connected` 为 nil →
// 视为无任何连接（返回空）。结果保持入参顺序（调用方自行排序）。
func MenuInstances(list []Info, currentUser string, connected func(instanceID string) bool) []Info {
	out := make([]Info, 0, len(list))
	user := strings.TrimSpace(currentUser)
	for _, in := range list {
		if in.ParentInstance != "" {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(in.User), user) {
			continue
		}
		if connected == nil || !connected(in.ID) {
			continue
		}
		out = append(out, in)
	}
	return out
}

// HandleRegister 处理 instance-register（宿主路由模式可直调；自驱动模式由订阅触发）。
// 返回错误仅表示**注册被拒**（缺口 7：实例数已达上限）——已登记实例的刷新与空 id 忽略**不报错**
// （保持既有宽容语义）。检查与插入同在写锁内完成（并发注册不会绕过上限）。
func (m *Manager) HandleRegister(payload []byte) error {
	var req struct {
		InstanceID     string `json:"instance_id"`
		ClientType     string `json:"client_type,omitempty"`
		WorkDir        string `json:"work_dir"`
		DataDir        string `json:"data_dir,omitempty"`
		User           string `json:"user,omitempty"`
		ParentInstance string `json:"parent_instance,omitempty"`
		SessionID      string `json:"session_id,omitempty"`
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
	current.User = req.User
	current.ParentInstance = req.ParentInstance
	current.SessionID = req.SessionID
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
