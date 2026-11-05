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
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chonkpilot/chonkpilot-data/facade"
	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
)

// InstanceManager 返回**活动实例视图**（framework `instance.Manager`，本服务唯一内存表；
// 登记/退出由 `s.im` 的 MQ 订阅维护）。
//
// 用途：宿主把它注入插件桥（`pluginbridge.Options.Instances`）——插件的
// `MainInstanceForWorkDir` / `plugin/v1/tools.call` 判据 3 / 晚连接插件补发都查**同一份**视图，
// 桥不再自持实例登记副本（2026-10-29 内存单源化）。
func (s *Server) InstanceManager() pluginbridge.InstanceView {
	return s.im
}

// MainInstanceForWorkDir 按 `work_dir` 反查**主窗口实例 id**（SANDBOX-WRITE-1 P3）：
// 供宿主「隐藏打开」门面（`plugin/v1/workdir.ensure`）判定「项目是否已在册」并在隐藏建窗后
// 等待 `instance-register`。口径 = 共通实例视图 `s.im`（`instance.Manager.MainInstanceForWorkDir`，
// 同 work_dir 且 `parent_instance` 为空；未命中 → ("", false)）。
func (s *Server) MainInstanceForWorkDir(workDir string) (string, bool) {
	return s.im.MainInstanceForWorkDir(workDir)
}

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
		text, state, partial, found := s.taskResultDetail(tc, in.ID)
		return map[string]any{"text": text, "state": state, "found": found, "partial": partial}, nil
	})

	// agent.delegate：**全能力委派子代理**（task_llm 的原子能力，2026-11-04）。
	//
	// 起一个完整子轮次（子会话 + 全工具面 + 同构 turnCtx 状态机，可再委派下一级），
	// 调用方可等 wait_sec 后取结果（0 = 一直等到收尾；detach=true = 立即返回 running）。
	// 子轮次使用独立 ctx —— 父轮次正常收尾不取消它；取消经 task_stop/tool_stop 关闭。
	//
	// 任务树：父调用节点下挂一个 kind=llm 子节点（SessionID/TurnID = 子轮次），
	// 子轮次内的工具节点再挂在该节点下（多层工具调用可见、可级联取消）。
	reg.Register("plugin/v1/agent.delegate", func(_ context.Context, _ *pluginbridge.Session, args json.RawMessage) (any, error) {
		var in struct {
			InstanceID string `json:"instance_id"`
			Turn       string `json:"turn"`
			Session    string `json:"session"`
			ToolCallID string `json:"tool_call_id"`
			Prompt     string `json:"prompt"`
			Agent      string `json:"agent"`
			SessionID  string `json:"session_id"`
			Title      string `json:"title"`
			Purpose    string `json:"purpose"`
			WaitSec    float64 `json:"wait_sec"`
			Detach     bool   `json:"detach"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		tc := s.lookupTurnIn(in.InstanceID, in.Turn)
		if tc == nil {
			return nil, &pluginbridge.Error{Code: "turn_not_found", Msg: "turn 不存在或已结束"}
		}
		prompt := strings.TrimSpace(in.Prompt)
		if prompt == "" {
			return nil, &pluginbridge.Error{Code: "args_invalid", Msg: "prompt 必填"}
		}
		subSession := strings.TrimSpace(in.SessionID)
		if subSession == "" {
			subSession = "sub-" + newID()
		}
		h, err := s.startChildTurn(context.Background(), tc.req, subSession, prompt, strings.TrimSpace(in.Agent))
		if err != nil {
			return nil, &pluginbridge.Error{Code: "delegate_failed", Msg: err.Error()}
		}
		// 父取消 / task-stop 级联：登记「父轮次 → 子轮次」（子轮次 ctx 独立于父收尾）。
		s.registerDelegated(tc.req.InstanceID, tc.req.Turn, h)
		// 子轮次任务节点：挂在发起该调用的工具节点之下（找不到则按树归属顶层）。
		topSession, parentID := s.taskNodeBase(tc)
		if in.ToolCallID != "" {
			if n := s.tasks.findByToolCall(in.InstanceID, in.ToolCallID); n != nil {
				parentID = n.TaskID
			}
		}
		title := strings.TrimSpace(in.Title)
		if title == "" {
			title = oneLine(prompt, 60)
		}
		node, nerr := s.tasks.start(&TaskNode{
			Tool: "task_llm", ToolCallID: in.ToolCallID, ParentID: parentID,
			TopSession: topSession, Kind: TaskKindLLM, SessionID: h.Session, TurnID: h.Turn,
			InstanceID: tc.req.InstanceID, Name: title, Purpose: strings.TrimSpace(in.Purpose),
			Simplified: title,
		})
		if nerr != nil {
			h.tc.Close()
			return nil, &pluginbridge.Error{Code: "delegate_failed", Msg: nerr.Error()}
		}
		// 后台收尾：子轮次结束后推进节点终态（被 task_stop 取消时节点已终态，done 幂等）。
		go func() {
			defer s.forgetDelegated(tc.req.InstanceID, tc.req.Turn, h.Turn)
			text, werr := h.wait(context.Background())
			if werr != nil {
				s.tasks.done(node.TaskID, TaskStateError, "", werr.Error())
				return
			}
			s.tasks.done(node.TaskID, TaskStateDone, text, "")
		}()

		running := map[string]any{
			"ok": true, "session_id": h.Session, "turn_id": h.Turn, "task_id": node.TaskID,
			"state": "running", "partial": s.partialTurnText(tc.req.InstanceID, h.Turn),
		}
		if in.Detach {
			return running, nil
		}
		wait := 0 * time.Second
		if in.WaitSec > 0 {
			wait = time.Duration(in.WaitSec * float64(time.Second))
		}
		if !delegatedWait(h, wait) {
			running["partial"] = s.partialTurnText(tc.req.InstanceID, h.Turn)
			return running, nil
		}
		// 收尾竞态：后台 goroutine 可能刚把结果写回节点，等待其可见。
		text, werr := h.wait(context.Background())
		if werr != nil {
			return map[string]any{
				"ok": false, "session_id": h.Session, "turn_id": h.Turn, "task_id": node.TaskID,
				"state": "error", "error": werr.Error(),
			}, nil
		}
		return map[string]any{
			"ok": true, "session_id": h.Session, "turn_id": h.Turn, "task_id": node.TaskID,
			"state": "done", "text": text,
		}, nil
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

	// ── 定时任务会话归属（SANDBOX-WRITE-1 P4 / C11，2026-11-03）────────────────
	//
	// crontab 插件**不直接开库**（C8）：经这三个能力建会话 / 建轮次 / 收轮次，落库仍由 persist
	// 数据面完成。层级 = 项目根会话（kind=crontab，parent_id=""）→ 任务子会话
	// （kind=crontab_task，parent_id=根会话）→ 每轮一个 turn。
	//
	// **建完 `rememberDialog`**（父/子会话 → dialog）：与 subchat.go 同口径 —— 该路径不经
	// sessionStore 自动学 `dialog_id`，不记则后续 turn/message 写入缺 `dialog_id` 被拒。
	// 执行主体标记 `origin=scheduler`（审计 / UI；**不冒充用户**）。
	reg.Register("plugin/v1/session.ensure", func(_ context.Context, _ *pluginbridge.Session, args json.RawMessage) (any, error) {
		var in struct {
			InstanceID      string `json:"instance_id"`
			WorkDir         string `json:"work_dir"`
			SessionID       string `json:"session_id"`
			ParentSessionID string `json:"parent_session_id"`
			DialogID        string `json:"dialog_id"`
			Kind            string `json:"kind"`
			Title           string `json:"title"`
			Origin          string `json:"origin"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		sessionID := strings.TrimSpace(in.SessionID)
		if sessionID == "" {
			return nil, &pluginbridge.Error{Code: "args_invalid", Msg: "session_id 必填"}
		}
		if s.cfg == nil {
			return nil, &pluginbridge.Error{Code: "internal", Msg: "数据门面不可用（无法建会话）"}
		}
		instanceID := s.resolveInstanceForWorkDir(in.InstanceID, in.WorkDir)
		if instanceID == "" {
			return nil, &pluginbridge.Error{Code: "instance_not_found", Msg: "未找到该项目的主窗口实例（请先 workdir.ensure）"}
		}
		resp, err := s.cfg.SessionEnsure(facade.SessionEnsureRequest{
			InstanceID:      instanceID,
			Scope:           s.cfgScope(instanceID),
			DialogID:        strings.TrimSpace(in.DialogID),
			SessionID:       sessionID,
			ParentSessionID: strings.TrimSpace(in.ParentSessionID),
			Kind:            strings.TrimSpace(in.Kind),
			Title:           in.Title,
			Origin:          strings.TrimSpace(in.Origin),
		})
		if err != nil {
			return nil, &pluginbridge.Error{Code: "session_ensure_failed", Msg: err.Error()}
		}
		// 子会话沿用根会话的对话；根会话自举后由服务端回带 dialog_id —— 两者都记进备忘。
		s.rememberDialog(instanceID, sessionID, resp.DialogID)
		return map[string]any{"ok": true, "session_id": sessionID, "dialog_id": resp.DialogID}, nil
	})
	reg.Register("plugin/v1/session.ensure-turn", func(_ context.Context, _ *pluginbridge.Session, args json.RawMessage) (any, error) {
		var in struct {
			InstanceID string `json:"instance_id"`
			WorkDir    string `json:"work_dir"`
			SessionID  string `json:"session_id"`
			DialogID   string `json:"dialog_id"`
			TurnID     string `json:"turn_id"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.SessionID) == "" || strings.TrimSpace(in.TurnID) == "" {
			return nil, &pluginbridge.Error{Code: "args_invalid", Msg: "session_id / turn_id 必填"}
		}
		instanceID := s.resolveInstanceForWorkDir(in.InstanceID, in.WorkDir)
		if instanceID == "" {
			return nil, &pluginbridge.Error{Code: "instance_not_found", Msg: "未找到该项目的主窗口实例"}
		}
		dialogID := strings.TrimSpace(in.DialogID)
		if dialogID == "" {
			dialogID = s.dialogOf(instanceID, in.SessionID)
		}
		if err := newSessionStore(s.bus, instanceID).EnsureTurnInDialog(in.TurnID, in.SessionID, dialogID); err != nil {
			return nil, &pluginbridge.Error{Code: "turn_ensure_failed", Msg: err.Error()}
		}
		return map[string]any{"ok": true, "turn_id": in.TurnID, "session_id": in.SessionID, "dialog_id": dialogID}, nil
	})
	reg.Register("plugin/v1/session.complete-turn", func(_ context.Context, _ *pluginbridge.Session, args json.RawMessage) (any, error) {
		var in struct {
			InstanceID   string `json:"instance_id"`
			WorkDir      string `json:"work_dir"`
			TurnID       string `json:"turn_id"`
			DialogID     string `json:"dialog_id"`
			Status       string `json:"status"`
			FinishReason string `json:"finish_reason"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.TurnID) == "" {
			return nil, &pluginbridge.Error{Code: "args_invalid", Msg: "turn_id 必填"}
		}
		status := strings.TrimSpace(in.Status)
		if status == "" {
			status = "done"
		}
		instanceID := s.resolveInstanceForWorkDir(in.InstanceID, in.WorkDir)
		if instanceID == "" {
			return nil, &pluginbridge.Error{Code: "instance_not_found", Msg: "未找到该项目的主窗口实例"}
		}
		if err := newSessionStore(s.bus, instanceID).CompleteTurnInDialog(in.TurnID, strings.TrimSpace(in.DialogID), status, in.FinishReason); err != nil {
			return nil, &pluginbridge.Error{Code: "turn_complete_failed", Msg: err.Error()}
		}
		return map[string]any{"ok": true}, nil
	})
}

// resolveInstanceForWorkDir 解析能力调用应归属的实例 id（P4）：显式 `instance_id` 优先；
// 否则按 `work_dir` 取主窗口实例（crontab 执行前先经 `workdir.ensure`，正常情况下必然命中）。
func (s *Server) resolveInstanceForWorkDir(instanceID, workDir string) string {
	if id := strings.TrimSpace(instanceID); id != "" {
		return id
	}
	wd := strings.TrimSpace(workDir)
	if wd == "" || s.im == nil {
		return ""
	}
	if id, ok := s.im.MainInstanceForWorkDir(wd); ok {
		return id
	}
	return ""
}

// oneLine 把文本压成单行并按 rune 截断（委派子代理任务节点标题用；不改变原文语义）。
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}
