// scope.go —— 三个作用域键（SCOPE-1，2026-10-28）。
//
//	instance_id  → **窗口**（WebView2 窗口实例；61 §4.1 `instance-register`）
//	work_dir     → **项目**（归一化后即项目身份；数据根 `<workDir>/.chonkpilot`，[19 §4]）
//	dialog_id    → **对话框**（一树一库的文件身份；[2G §3]）
//
// 硬约束：三者**不得复用同一字段**。项目级运行态（工具目录 / MCP 下游 / sandbox 根 /
// 配额类缓存）一律以 `projectKey(work_dir)` 为键，**不得**以 `instance_id` 分片——否则同一
// 项目的两个窗口各自一份（重复注册 / 重复取数），且单例配额会被一个项目挤占另一个。
// 窗口 → 项目 → 对话框的映射表**单源在宿主**（`src/lib/gui` 的 `hostEnv.projects`）。
package server

import (
	"path/filepath"
	"strings"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

// projectKey 归一项目身份（= 归一化 work_dir）：绝对化 + 清理 + 小写（Windows 大小写不敏感）。
// work_dir 为空（未登记实例 / 无项目上下文）→ 空串（调用方据此**不缓存**，不以窗口冒充项目）。
func projectKey(workDir string) string {
	if strings.TrimSpace(workDir) == "" {
		return ""
	}
	p, err := filepath.Abs(workDir)
	if err != nil {
		p = workDir
	}
	return strings.ToLower(filepath.Clean(p))
}

// projectScopeOf 把窗口实例解析为**项目键**（项目级运行态的取值入口）：实例未登记 → 空串。
func (s *Server) projectScopeOf(instanceID string) string {
	if instanceID == "" || s.im == nil {
		return ""
	}
	info, ok := s.im.Lookup(instanceID)
	if !ok {
		return ""
	}
	return projectKey(info.WorkDir)
}

// rememberScope 记录 (instance, session/turn) 的显式项目作用域。
func (s *Server) rememberScope(instanceID, sessionID, turnID string, scope facade.Scope) {
	if s == nil || scope.Empty() {
		return
	}
	s.scopeMu.Lock()
	if s.scopeBySession == nil {
		s.scopeBySession = map[string]facade.Scope{}
	}
	if s.scopeByTurn == nil {
		s.scopeByTurn = map[string]facade.Scope{}
	}
	if sessionID != "" {
		s.scopeBySession[scopeKey(instanceID, sessionID)] = scope
	}
	if turnID != "" {
		s.scopeByTurn[scopeKey(instanceID, turnID)] = scope
	}
	s.scopeMu.Unlock()
}

// rememberCanonicalWorkDir 记录实例注册时上报的绑定项目。
func (s *Server) rememberCanonicalWorkDir(instanceID, workDir string) {
	if instanceID == "" || workDir == "" {
		return
	}
	s.scopeMu.Lock()
	if s.canonicalWorkDir == nil {
		s.canonicalWorkDir = map[string]string{}
	}
	s.canonicalWorkDir[instanceID] = workDir
	s.scopeMu.Unlock()
}

func (s *Server) canonicalWorkDirOf(instanceID string) string {
	s.scopeMu.RLock()
	wd := s.canonicalWorkDir[instanceID]
	s.scopeMu.RUnlock()
	return wd
}

// scopeForSession 取会话**已登记**的显式项目作用域；未登记 → 空 scope（门面按实例绑定解析）。
//
// 不得回落 `cfgScope`：空 scope（= 实例默认项目）与显式 scope（即使 work_dir 相同）在会话索引 /
// dialog 备忘里是**两个键**；把默认项目写成显式 scope 会让既有会话找不到 dialog。
func (s *Server) scopeForSession(instanceID, sessionID string) facade.Scope {
	s.scopeMu.RLock()
	scope := s.scopeBySession[scopeKey(instanceID, sessionID)]
	s.scopeMu.RUnlock()
	return scope
}

func (s *Server) scopeForTurn(instanceID, turnID string) facade.Scope {
	s.scopeMu.RLock()
	scope := s.scopeByTurn[scopeKey(instanceID, turnID)]
	s.scopeMu.RUnlock()
	return scope
}

// runKey 返回运行态键：显式项目作用域存在 → projectKey+id；否则保持旧 instKey。
func (s *Server) runKey(instanceID, id string) string {
	// 轮次 id 全局唯一（fork full 会重映射 turn/task id，见 2G §5）→ 先取 turn 作用域
	// （同窗口同名 session 时不会被 session 表覆盖）；再取 session 作用域（busy / owner / inbox 等会话级键）。
	scope := s.scopeForTurn(instanceID, id)
	if scope.Empty() {
		scope = s.scopeForSession(instanceID, id)
	}
	if scope.Empty() && s.im != nil {
		if info, ok := s.im.Lookup(instanceID); ok {
			scope.WorkDir = info.WorkDir
		}
	}
	if scope.WorkDir != "" {
		return projectKey(scope.WorkDir) + "\x00" + id
	}
	return instKey(instanceID, id)
}

func scopeKey(instanceID, id string) string { return instanceID + "\x00" + id }

// applySessionScope 把已登记的项目作用域填进内部 StartReq（llm-inbox / notify / ask 等
// 内部起轮路径共用于；这些路径没有外部 work_dir 载荷）。
// addScopeToEvent 给事件载荷补 work_dir/data_dir（订阅路由用；空 scope 不写键）。
func (s *Server) addScopeToEvent(payload map[string]any, instanceID, sessionID, turnID string) map[string]any {
	if payload == nil {
		return payload
	}
	scope := s.scopeForSession(instanceID, sessionID)
	if scope.Empty() {
		scope = s.scopeForTurn(instanceID, turnID)
	}
	if scope.WorkDir != "" {
		payload["work_dir"] = scope.WorkDir
	}
	if scope.DataDir != "" {
		payload["data_dir"] = scope.DataDir
	}
	return payload
}

func (s *Server) applySessionScope(req *StartReq, instanceID, sessionID string) {
	if req == nil {
		return
	}
	scope := s.scopeForSession(instanceID, sessionID)
	if scope.Empty() {
		return
	}
	req.WorkDir = scope.WorkDir
	req.DataDir = scope.DataDir
	req.ScopeExplicit = true
}

// sessionStoreFor 取某会话对应的数据客户端（自动带已登记的项目作用域）。
func (s *Server) sessionStoreFor(instanceID, sessionID string) *sessionStore {
	return s.newSessionStore(instanceID, s.scopeForSession(instanceID, sessionID))
}

// turnStoreFor 取某轮次对应的数据客户端（自动带已登记的项目作用域）。
func (s *Server) turnStoreFor(instanceID, turnID string) *sessionStore {
	return s.newSessionStore(instanceID, s.scopeForTurn(instanceID, turnID))
}
