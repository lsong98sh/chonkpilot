// plugin_caps_session_open.go —— `plugin/v1/session.open`（SESSION-OPEN-1，2026-11-15）：
// 把"帮用户打开某个会话窗口"接给 LLM 工具（session_open）。
//
// 窗口打开复用宿主既有实现 `gui.Env.OpenChatWindow`（与托盘 / `gui.window.open-chat` 同一路径；
// G-105：带 dialog_id 时窗口直连该对话，免前端反查）。headless（无 GUI）→ `unsupported`。
package server

import (
	"context"
	"encoding/json"
	"strings"

	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
)

// SetChatWindowOpener 注入对话窗口打开口（宿主在 gui Env 就绪后调用）。
func (s *Server) SetChatWindowOpener(fn func(workDir, sessionID, dialogID string) (string, bool)) {
	s.chatWindowOpener = fn
}

type sessionOpenCapIn struct {
	InstanceID string `json:"instance_id"`
	WorkDir    string `json:"work_dir"`
	SessionID  string `json:"session_id"`
	DialogID   string `json:"dialog_id"`
	Session    string `json:"session"`
	TopSession string `json:"top_session"`
}

// sessionOpenCap 打开会话窗口（已打开则激活）。
func (s *Server) sessionOpenCap(_ context.Context, sess *pluginbridge.Session, args json.RawMessage) (any, error) {
	var in sessionOpenCapIn
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "session.open: 参数解析失败"}
	}
	if s.chatWindowOpener == nil {
		return nil, &pluginbridge.Error{Code: pluginbridge.CodeUnsupported, Msg: "当前宿主无 GUI 窗口能力"}
	}
	workDir := s.sessionCapWorkDir(in.InstanceID, in.WorkDir, sess)
	if workDir == "" {
		return nil, &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "session.open: work_dir 必填"}
	}
	sessionID, err := resolveSessionRef(in.SessionID, in.Session, in.TopSession)
	if err != nil {
		return nil, err
	}
	if sessionID == "" {
		return nil, &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "session.open: session_id 缺失且无注入上下文"}
	}
	scope, _ := s.pluginTargetScope(in.InstanceID, workDir)
	dialogID := strings.TrimSpace(in.DialogID)
	if dialogID == "" {
		dialogID = s.ensureDialogIndex(in.InstanceID, sessionID, scope)
	}
	if dialogID == "" {
		return nil, &pluginbridge.Error{Code: pluginbridge.CodeNotFound, Msg: "会话不存在: " + sessionID}
	}
	windowID, ok := s.chatWindowOpener(workDir, sessionID, dialogID)
	if !ok || windowID == "" {
		return nil, &pluginbridge.Error{Code: "window_open_failed", Msg: "打开对话窗口失败（窗口上限或会话不可用）"}
	}
	return map[string]any{
		"ok": true, "window_id": windowID,
		"session_id": sessionID, "dialog_id": dialogID,
	}, nil
}
