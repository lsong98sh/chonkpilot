// PM 门面（Project Manager，SANDBOX-WRITE-1 §7 / C15，P5 步骤 2–3）。
//
// 四层模型（只存 ID + 关系 + 状态，不存消息 / 文件内容）：
//
//	L1  work_dir + user  → 数据访问面（layout：prj 共享 / prjusr 按用户）
//	L2  instance(window) → 可见性、承载关系（实例视图 = instance.Manager，本包单源）
//	L3  dialog           → 该实例/项目下的对话
//	L4  session          → 对话内的会话（轮次挂此）
//
// 边界（§7.4）：
//   - PM **不持有窗口句柄**，否则无 GUI 形态跑不了 → 窗口 / 对话 / 会话动作一律经
//     `ProjectOps` 端口委托（GUI 宿主用窗口工厂实现，无 GUI 形态可注入空实现）；
//   - `create*` 是**薄门面**，不重复实现业务动作（动作仍在工厂 / 既有 MQ）；
//   - MQ 调度策略 = `Route`（规则见 router.go，唯一实现）。
package workspace

import "errors"

// ErrOpsNotWired 表示 PM 尚未接线窗口/会话动作端口（非 GUI 形态或装配未完成）。
var ErrOpsNotWired = errors.New("workspace: project ops not wired")

// 窗口种类（CreateWindow 的 kind；与既有 `gui.window.open-chat` 的 kind 口径一致）。
const (
	WindowMain = "main"
	WindowChat = "chat"
)

// ProjectOps 是 PM 的**动作端口**（PM 不持有窗口句柄 / 桥 / 会话状态）。
//
// 实现方 = GUI 宿主装配层（窗口工厂 + 既有 MQ 消息）；无 GUI 形态可注入返回
// `ErrOpsNotWired` 的空实现，PM 的只读能力（Get/List/InstanceIDs/Route）不受影响。
type ProjectOps interface {
	// OpenProject 打开（或激活）项目主窗口；hidden = 隐藏打开（C13/C17）。
	OpenProject(workDir string, hidden bool) (instanceID string, alreadyOpen bool, err error)
	// CreateWindow 在项目下建窗口（kind = WindowMain / WindowChat）。
	// 对话窗口需 `params["session_id"]`（窗口绑定会话）；主窗口忽略 params。
	CreateWindow(workDir, kind string, visible bool, params map[string]any) (windowID string, err error)
	// Hide 隐藏项目主窗口（不关闭实例）。
	Hide(workDir string) error
	// Close 关闭项目主窗口（含实例退出 → workdir-closed）。
	Close(workDir string) error
	// CreateDialog 在项目（或实例）下新建对话。
	CreateDialog(workDir string, params map[string]any) (dialogID string, err error)
	// CreateSession 在对话下新建会话。
	CreateSession(dialogID string, params map[string]any) (sessionID string, err error)
}

// SetProjectOps 注入动作端口（装配期调用一次；nil = 清除）。
func (m *Manager) SetProjectOps(ops ProjectOps) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.ops = ops
	m.mu.Unlock()
}

// opsOrErr 取动作端口（未接线 → ErrOpsNotWired）。
func (m *Manager) opsOrErr() (ProjectOps, error) {
	m.mu.Lock()
	ops := m.ops
	m.mu.Unlock()
	if ops == nil {
		return nil, ErrOpsNotWired
	}
	return ops, nil
}

// OpenProject 打开（或激活）项目主窗口（薄门面 → ProjectOps）。
func (m *Manager) OpenProject(workDir string, hidden bool) (string, bool, error) {
	ops, err := m.opsOrErr()
	if err != nil {
		return "", false, err
	}
	return ops.OpenProject(workDir, hidden)
}

// CreateWindow 在项目下建窗口（薄门面 → ProjectOps）。
func (m *Manager) CreateWindow(workDir, kind string, visible bool, params map[string]any) (string, error) {
	ops, err := m.opsOrErr()
	if err != nil {
		return "", err
	}
	return ops.CreateWindow(workDir, kind, visible, params)
}

// Hide 隐藏项目主窗口（薄门面 → ProjectOps）。
func (m *Manager) Hide(workDir string) error {
	ops, err := m.opsOrErr()
	if err != nil {
		return err
	}
	return ops.Hide(workDir)
}

// Close 关闭项目主窗口（薄门面 → ProjectOps）。
func (m *Manager) Close(workDir string) error {
	ops, err := m.opsOrErr()
	if err != nil {
		return err
	}
	return ops.Close(workDir)
}

// CreateDialog 在项目下新建对话（薄门面 → ProjectOps）。
func (m *Manager) CreateDialog(workDir string, params map[string]any) (string, error) {
	ops, err := m.opsOrErr()
	if err != nil {
		return "", err
	}
	return ops.CreateDialog(workDir, params)
}

// CreateSession 在对话下新建会话（薄门面 → ProjectOps）。
func (m *Manager) CreateSession(dialogID string, params map[string]any) (string, error) {
	ops, err := m.opsOrErr()
	if err != nil {
		return "", err
	}
	return ops.CreateSession(dialogID, params)
}

// Router 返回 MQ 分发策略（宿主把它注入 mqpolicy，二者共用同一份发布账本 / 反查视图）。
func (m *Manager) Router() *Router { return m.router }

// SetInstanceWorkDir 注入 `instance_id → work_dir` 反查（默认 = 本 PM 的实例视图）。
func (m *Manager) SetInstanceWorkDir(fn func(instanceID string) (string, bool)) {
	m.router.SetInstanceWorkDir(fn)
}

// Mark 记录一次发布（**发布前**调用；owner = 该窗口实例 id）。
func (m *Manager) Mark(owner, subject string, payload []byte) {
	m.router.Mark(owner, subject, payload)
}

// Route 计算一条 MQ 事件应投递的目标（唯一实现，见 router.go）。
func (m *Manager) Route(topic string, payload []byte, sinks []Sink) []Target {
	return m.router.Route(topic, payload, sinks)
}
