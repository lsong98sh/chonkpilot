// plugin_caps.go —— 把 llm-server 的**原子能力**注册进插件桥（BRIDGE-2 T7 前置）。
//
// 目的：外部工具进程（server-tools）不再需要把 turnCtx / 任务树 / ask 通道搬出进程，
// 而是经 `plugin/v1/*` 请求这些原子能力；server 保持唯一的 turn 状态与任务层。
//
// 这些 handler **不是工具**（不注册到 LLM 工具面），只对已 hello 的插件可见。
package server

import (
	"context"
	"encoding/json"

	pluginbridge "github.com/chonkpilot/chonkpilot-lib/pluginhost/bridge"
)

// SetPluginRegistry 把 server 原子能力注册到插件桥（在插件桥 Start 之后调用）。
func (s *Server) SetPluginRegistry(reg *pluginbridge.Registry) {
	if reg == nil {
		return
	}

	// turn.context：外部工具按 (instance, turn) 取运行上下文（只读快照）。
	reg.Register("plugin/v1/turn.context", func(_ context.Context, _ *pluginbridge.Session, args json.RawMessage) (any, error) {
		var in struct {
			InstanceID string `json:"instance_id"`
			Turn       string `json:"turn"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		tc := s.lookupTurnIn(in.InstanceID, in.Turn)
		if tc == nil {
			return map[string]any{"found": false}, nil
		}
		return map[string]any{
			"found":       true,
			"session":     tc.req.Session,
			"turn":        tc.req.Turn,
			"instance_id": tc.req.InstanceID,
			"parents":     tc.req.Parents,
		}, nil
	})

	// task.node.start / done：外部工具创建 / 收尾任务节点（任务树唯一写者仍是 server）。
	reg.Register("plugin/v1/task.node.start", func(_ context.Context, _ *pluginbridge.Session, args json.RawMessage) (any, error) {
		var in struct {
			InstanceID string         `json:"instance_id"`
			Turn       string         `json:"turn"`
			Tool       string         `json:"tool"`
			ToolCallID string         `json:"tool_call_id"`
			Purpose    string         `json:"purpose"`
			Args       map[string]any `json:"args"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		tc := s.lookupTurnIn(in.InstanceID, in.Turn)
		if tc == nil {
			return nil, &pluginbridge.Error{Code: "turn_not_found", Msg: "turn 不存在或已结束"}
		}
		node, err := s.domainNode(tc, in.ToolCallID, in.Tool, in.Args, in.Purpose)
		if err != nil {
			return nil, err
		}
		return map[string]any{"task_id": node.TaskID}, nil
	})
	reg.Register("plugin/v1/task.node.done", func(_ context.Context, _ *pluginbridge.Session, args json.RawMessage) (any, error) {
		var in struct {
			TaskID string `json:"task_id"`
			State  string `json:"state"`
			Result string `json:"result"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		st := TaskStateDone
		if in.State == "error" {
			st = TaskStateError
		}
		s.tasks.done(in.TaskID, st, in.Result, in.Error)
		return map[string]any{"ok": true}, nil
	})

	// task.cancel / task.result：tool_stop / tool_result 的原子能力。
	reg.Register("plugin/v1/task.cancel", func(_ context.Context, _ *pluginbridge.Session, args json.RawMessage) (any, error) {
		var in struct {
			InstanceID string `json:"instance_id"`
			Turn       string `json:"turn"`
			TaskID     string `json:"task_id"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		tc := s.lookupTurnIn(in.InstanceID, in.Turn)
		if tc == nil {
			return nil, &pluginbridge.Error{Code: "turn_not_found", Msg: "turn 不存在或已结束"}
		}
		return map[string]any{"cancelled": s.tasks.cancelSubtree(tc.req.InstanceID, in.TaskID)}, nil
	})
	reg.Register("plugin/v1/task.result", func(_ context.Context, _ *pluginbridge.Session, args json.RawMessage) (any, error) {
		var in struct {
			InstanceID string `json:"instance_id"`
			Turn       string `json:"turn"`
			ID         string `json:"id"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		tc := s.lookupTurnIn(in.InstanceID, in.Turn)
		if tc == nil {
			return nil, &pluginbridge.Error{Code: "turn_not_found", Msg: "turn 不存在或已结束"}
		}
		text, state, found := s.taskResult(tc, in.ID)
		return map[string]any{"text": text, "state": state, "found": found}, nil
	})

	// tool.stop：tool_stop 的完整语义（终态判定 / 执行池门控 / process_id 拒绝）留在
	// server（任务层是唯一权威）；外部进程只转发 args 并采用返回文本。
	reg.Register("plugin/v1/tool.stop", func(_ context.Context, _ *pluginbridge.Session, args json.RawMessage) (any, error) {
		var in struct {
			InstanceID string         `json:"instance_id"`
			Turn       string         `json:"turn"`
			Args       map[string]any `json:"args"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		tc := s.lookupTurnIn(in.InstanceID, in.Turn)
		if tc == nil {
			return nil, &pluginbridge.Error{Code: "turn_not_found", Msg: "turn 不存在或已结束"}
		}
		text, isErr := s.runToolStop(tc, nil, in.Args)
		return map[string]any{"text": text, "is_error": isErr}, nil
	})

	// ask.user：外部工具把提问交回 server 的 ask 通道（等用户回答 / 回填 turn）。
	reg.Register("plugin/v1/ask.user", func(_ context.Context, _ *pluginbridge.Session, args json.RawMessage) (any, error) {
		var in struct {
			Turn       string         `json:"turn"`
			ToolCallID string         `json:"tool_call_id"`
			TaskID     string         `json:"task_id"`
			Args       map[string]any `json:"args"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		return map[string]any{"text": s.ask(in.Turn, in.ToolCallID, in.Args, in.TaskID)}, nil
	})

	// next.run（NEXTRUN-1）：进程外 `next_run` executor → server 内登记下一轮排队输入。
	reg.Register("plugin/v1/next.run", func(_ context.Context, _ *pluginbridge.Session, args json.RawMessage) (any, error) {
		var in struct {
			InstanceID string         `json:"instance_id"`
			Turn       string         `json:"turn"`
			Args       map[string]any `json:"args"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		tc := s.lookupTurnIn(in.InstanceID, in.Turn)
		if tc == nil {
			return nil, &pluginbridge.Error{Code: "turn_not_found", Msg: "turn 不存在或已结束"}
		}
		text, isErr := s.runNextRun(tc, in.Args)
		return map[string]any{"text": text, "is_error": isErr}, nil
	})

	// subchat.open / finish：单聊域工具的原子能力（会话创建 / lifecycle 由 server 落库）。
	reg.Register("plugin/v1/subchat.open", func(_ context.Context, _ *pluginbridge.Session, args json.RawMessage) (any, error) {
		var in struct {
			InstanceID string         `json:"instance_id"`
			Turn       string         `json:"turn"`
			Purpose    string         `json:"purpose"`
			Args       map[string]any `json:"args"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		tc := s.lookupTurnIn(in.InstanceID, in.Turn)
		if tc == nil {
			return nil, &pluginbridge.Error{Code: "turn_not_found", Msg: "turn 不存在或已结束"}
		}
		text, isErr := s.runSubChatOpen(tc, in.Args, in.Purpose)
		return map[string]any{"text": text, "is_error": isErr}, nil
	})
	reg.Register("plugin/v1/subchat.finish", func(_ context.Context, _ *pluginbridge.Session, args json.RawMessage) (any, error) {
		var in struct {
			InstanceID string `json:"instance_id"`
			Turn       string `json:"turn"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		tc := s.lookupTurnIn(in.InstanceID, in.Turn)
		if tc == nil {
			return nil, &pluginbridge.Error{Code: "turn_not_found", Msg: "turn 不存在或已结束"}
		}
		text, isErr := s.runSubChatFinish(tc)
		return map[string]any{"text": text, "is_error": isErr}, nil
	})
}
