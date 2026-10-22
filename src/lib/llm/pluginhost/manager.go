package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// Manager 按 work_dir 监管多个常驻插件实例：同一 work_dir 至多一个 Host。
type Manager struct {
	mu    sync.Mutex
	logf  func(format string, args ...any)
	hosts map[string]*Host // key = 绝对化 + Clean 后的 WorkDir
}

// NewManager 构造多实例监管器。
func NewManager(logf func(format string, args ...any)) *Manager {
	return &Manager{logf: logf, hosts: make(map[string]*Host)}
}

// Ensure 确保 spec.WorkDir 对应插件在运行：已运行 → 直接返回；已停止 → 重建。
func (m *Manager) Ensure(ctx context.Context, spec Spec) error {
	key, err := workDirKey(spec.WorkDir)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if h, ok := m.hosts[key]; ok {
		if !h.stopped() {
			return nil
		}
		delete(m.hosts, key)
	}
	h := New(spec)
	if err := h.Start(ctx); err != nil {
		return err
	}
	m.hosts[key] = h
	m.log("pluginhost: 已监管插件 %s（work_dir=%s）", spec.ID, key)
	return nil
}

// StopAll 停止全部 Host（幂等）。
func (m *Manager) StopAll() {
	m.mu.Lock()
	list := make([]*Host, 0, len(m.hosts))
	for _, h := range m.hosts {
		list = append(list, h)
	}
	m.mu.Unlock()
	for _, h := range list {
		if err := h.Stop(); err != nil {
			m.log("pluginhost: 停止插件失败: %v", err)
		}
	}
}

// Hosts 返回当前 Host 快照（观测 / 测试用）。
func (m *Manager) Hosts() []*Host {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Host, 0, len(m.hosts))
	for _, h := range m.hosts {
		out = append(out, h)
	}
	return out
}

func (m *Manager) log(format string, args ...any) {
	if m.logf != nil {
		m.logf(format, args...)
	}
}

// workDirKey 归一化 work_dir（Clean + 绝对化）；空值报错。
func workDirKey(wd string) (string, error) {
	if strings.TrimSpace(wd) == "" {
		return "", errors.New("pluginhost: WorkDir 不能为空")
	}
	abs, err := filepath.Abs(filepath.Clean(wd))
	if err != nil {
		return "", fmt.Errorf("pluginhost: work_dir 绝对化失败: %w", err)
	}
	return abs, nil
}

// stopped 报告该 Host 是否已停止（显式 Stop 或监管协程已结束）。
func (h *Host) stopped() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopping {
		return true
	}
	select {
	case <-h.done:
		return true
	default:
		return false
	}
}
