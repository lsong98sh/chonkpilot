package bridge

import (
	"context"
	"encoding/json"
	"strings"
)

// GUIController 是宿主注入的 GUI 反向通道（一窗口一实例）。
// List 返回当前可用实例；Apply 在目标窗口执行一条 GUI 命令（与前端同一条 PublishEvent 白名单）。
type GUIController interface {
	List() []string
	Apply(instanceID, action string, payload json.RawMessage) (any, error)
}

func registerGUICapabilities(reg *Registry, g GUIController) {
	if g == nil {
		return
	}
	reg.Register("plugin/v1/gui.list", func(_ context.Context, _ *Session, _ json.RawMessage) (any, error) {
		return map[string]any{"instances": g.List()}, nil
	})
	reg.Register("plugin/v1/gui.apply", func(_ context.Context, _ *Session, args json.RawMessage) (any, error) {
		var in struct {
			InstanceID       string          `json:"instance_id"`
			TargetInstanceID string          `json:"target_instance_id"`
			Action           string          `json:"action"`
			Payload          json.RawMessage `json:"payload"`
		}
		if err := decode(args, &in); err != nil {
			return nil, err
		}
		if in.Action == "" {
			return nil, errf(CodeArgsInvalid, "gui.apply 需要 action")
		}
		target := in.InstanceID
		if target == "" {
			target = in.TargetInstanceID
		}
		res, err := g.Apply(target, in.Action, unwrapPayload(in.Payload))
		if err != nil {
			return nil, &Error{Code: CodeInternal, Msg: err.Error()}
		}
		return map[string]any{"ok": true, "result": res}, nil
	})
}

// unwrapPayload 兼容插件把 payload 作为 JSON 字符串传入（如 `"{\"a\":1}"`）：解一层后再交给宿主。
func unwrapPayload(p json.RawMessage) json.RawMessage {
	if len(p) == 0 || p[0] != '"' {
		return p
	}
	var s string
	if json.Unmarshal(p, &s) != nil || strings.TrimSpace(s) == "" {
		return p
	}
	return json.RawMessage(s)
}
