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
