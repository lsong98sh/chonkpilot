// Scope 是 MQ 消息的订阅作用域（SESSION-SCOPE-1）。
//
// 层级（全部扁平字段，不递归）：
//
//	work_dir                          → 整个项目
//	work_dir + dialog_id              → 整棵对话树
//	work_dir + session_id             → 精确会话（不含 sub-session）
//	work_dir + turn_id                → 精确轮次
//	work_dir + task_id/tool_call_id   → 精确任务/工具节点
//
// 空 Scope 在订阅侧 = wildcard（所有 scope + 全局）；在消息侧 = 全局消息。
// 订阅字段为通配：未填写的字段不参与比较。会话/轮次事件必须带齐可用的父级 id，
// 以便 dialog/session 级订阅能命中；不做树形展开。
package workspace

import (
	"encoding/json"
	"strings"

	"github.com/lsong98sh/chonkpilot-util/pathx"
)

// Scope 是一条消息/订阅的作用域。
type Scope struct {
	WorkDir    string `json:"work_dir,omitempty"`
	DialogID   string `json:"dialog_id,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	TurnID     string `json:"turn_id,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// Empty 判定是否未带任何作用域字段。
func (s Scope) Empty() bool {
	return s.WorkDir == "" && s.DialogID == "" && s.SessionID == "" && s.TurnID == "" &&
		s.TaskID == "" && s.ToolCallID == ""
}

// Same 判定两个 scope 语义相等（work_dir 按 Windows 路径口径比较）。
func (s Scope) Same(o Scope) bool {
	return sameWorkDir(s.WorkDir, o.WorkDir) &&
		s.DialogID == o.DialogID && s.SessionID == o.SessionID && s.TurnID == o.TurnID &&
		s.TaskID == o.TaskID && s.ToolCallID == o.ToolCallID
}

// Match 判定消息 scope 是否命中订阅（订阅字段为空 = 通配；消息空 = 全局）。
func (s Scope) Match(sub Scope) bool {
	if s.Empty() {
		return true // 全局消息投所有订阅
	}
	if sub.Empty() {
		return true // wildcard 订阅收所有
	}
	if sub.WorkDir != "" && !sameWorkDir(sub.WorkDir, s.WorkDir) {
		return false
	}
	if sub.DialogID != "" && sub.DialogID != s.DialogID {
		return false
	}
	if sub.SessionID != "" && sub.SessionID != s.SessionID {
		return false
	}
	if sub.TurnID != "" && sub.TurnID != s.TurnID {
		return false
	}
	if sub.TaskID != "" && sub.TaskID != s.TaskID {
		return false
	}
	if sub.ToolCallID != "" && sub.ToolCallID != s.ToolCallID {
		return false
	}
	return true
}

// ScopeFromPayload 从事件载荷提取 scope（缺字段 = 空）。
func ScopeFromPayload(payload []byte) Scope {
	var p struct {
		WorkDir    string `json:"work_dir"`
		DialogID   string `json:"dialog_id"`
		SessionID  string `json:"session_id"`
		TurnID     string `json:"turn_id"`
		TaskID     string `json:"task_id"`
		ToolCallID string `json:"tool_call_id"`
	}
	if json.Unmarshal(payload, &p) != nil {
		return Scope{}
	}
	return Scope{
		WorkDir: p.WorkDir, DialogID: p.DialogID, SessionID: p.SessionID,
		TurnID: p.TurnID, TaskID: p.TaskID, ToolCallID: p.ToolCallID,
	}
}

func sameWorkDir(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	return pathx.Same(a, b)
}

// HasWorkDir 判定 scope 是否带项目作用域。
func (s Scope) HasWorkDir() bool { return strings.TrimSpace(s.WorkDir) != "" }
