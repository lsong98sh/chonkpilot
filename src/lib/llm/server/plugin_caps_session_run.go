// plugin_caps_session_run.go —— 会话写能力（SESSION-RUN-1，2026-11-15）。
//
// `plugin/v1/session.run` 是 `session_create` / `session_send` 的共同原子能力：
//   - session_id 缺省 → **新建顶层对话**（新 dialog，kind=main）+ 首轮；
//   - session_id 给定 → 向**既有会话**追发一轮（顶层优先）；
//   - 两种形态都：建 turn → 建 kind=llm 任务节点 → 登记**取消级联**（barrier=false，
//     不阻塞父轮终态）→ 立即可返回 running，或按 wait_sec 等终态。
//
// 与 agent.run 的差异：agent.run 是 scheduler 语义（origin=scheduler、不建任务节点、不挂父轮）；
// 本能力面向 LLM 会话工具：任务节点挂在调用方工具节点下，父轮取消 / task-stop 可级联关闭。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade"
	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
)

// sessionRunMaxKey 是并发上限配置键（usr 优先，prj 作默认；缺省 3；<=0 = 不限）。
const sessionRunMaxKey = "plugins.session.run.max-concurrent"

type sessionRunCapIn struct {
	InstanceID  string  `json:"instance_id"`
	WorkDir     string  `json:"work_dir"`
	SessionID   string  `json:"session_id"`
	Title       string  `json:"title"`
	Prompt      string  `json:"prompt"`
	Agent       string  `json:"agent"`
	ScenarioID  string  `json:"scenario_id"`
	WaitSec     float64 `json:"wait_sec"`
	Turn        string  `json:"turn"`
	ToolCallID  string  `json:"tool_call_id"`
	DisplayName string  `json:"display_name"`
}

// sessionRunCap 新建 / 续跑一个顶层会话轮次。
func (s *Server) sessionRunCap(ctx context.Context, _ *pluginbridge.Session, args json.RawMessage) (any, error) {
	if s.cfg == nil {
		return nil, &pluginbridge.Error{Code: pluginbridge.CodeInternal, Msg: "数据门面不可用（无法建会话）"}
	}
	var in sessionRunCapIn
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "session.run: 参数解析失败"}
	}
	prompt := strings.TrimSpace(in.Prompt)
	if prompt == "" {
		return nil, &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "session.run: prompt 必填"}
	}
	instanceID := s.resolveInstanceForWorkDir(in.InstanceID, in.WorkDir)
	if instanceID == "" {
		return nil, &pluginbridge.Error{Code: "instance_not_found", Msg: "未找到该项目的主窗口实例"}
	}
	rec, ok := s.im.Lookup(instanceID)
	if !ok {
		return nil, &pluginbridge.Error{Code: "instance_not_found", Msg: "实例未登记"}
	}

	creating := strings.TrimSpace(in.SessionID) == ""
	sessionID := strings.TrimSpace(in.SessionID)
	if creating {
		sessionID = "split-" + newID()
	}

	// 父轮（工具调用所在轮次）用于任务树归属与取消级联；无父轮（定时任务 / 外部调用）允许。
	var parentTC *turnCtx
	scenarioID := strings.TrimSpace(in.ScenarioID)
	if strings.TrimSpace(in.Turn) != "" {
		parentTC = s.lookupTurnIn(instanceID, in.Turn)
		if parentTC != nil && scenarioID == "" {
			scenarioID = parentTC.req.ScenarioID
		}
	}
	agentName := strings.TrimSpace(in.Agent)
	if err := s.validateSessionRunAgent(instanceID, scenarioID, agentName); err != nil {
		return nil, err
	}

	// 标题：缺省取 prompt 首行（会话列表与任务节点同名，便于用户辨认）。
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = firstLine(prompt, 60)
	}

	ensureReq := facade.SessionEnsureRequest{
		InstanceID: instanceID, Scope: s.cfgScope(instanceID),
		SessionID: sessionID, Title: title,
	}
	if creating {
		// 顶层：不带 dialog_id / parent_session_id → 宿主自举新对话（kind=main）。
		ensureReq.Kind = ""
	} else {
		dlg := s.ensureDialogIndex(instanceID, sessionID)
		if dlg == "" {
			return nil, &pluginbridge.Error{Code: pluginbridge.CodeNotFound, Msg: "会话不存在: " + sessionID}
		}
		ensureReq.DialogID = dlg
	}
	resp, err := s.cfg.SessionEnsure(ensureReq)
	if err != nil {
		return nil, &pluginbridge.Error{Code: "session_ensure_failed", Msg: err.Error()}
	}
	s.rememberDialog(instanceID, sessionID, resp.DialogID)

	// 并发上限（项目级信号量）：超限时阻塞排队，直到有空位或调用 ctx 结束。
	release, aerr := s.acquireSessionRunSlot(ctx, instanceID)
	if aerr != nil {
		return nil, aerr
	}

	turnID := newID()
	startReq := StartReq{
		InstanceID: instanceID, Session: sessionID, Turn: turnID,
		WorkDir: rec.WorkDir, DataDir: rec.DataDir,
		Agent: agentName, ScenarioID: scenarioID,
	}
	// 独立 ctx：父轮正常收尾不取消派生会话；取消经 llm-cancel / task-stop 的级联路径。
	h, err := s.startScheduledTurn(context.Background(), startReq, prompt)
	if err != nil {
		release()
		return nil, &pluginbridge.Error{Code: "session_busy", Msg: err.Error()}
	}

	// 任务节点：**归属新会话自己的任务树**（TopSession = sessionID，让该会话内的工具节点
	// 经 LatestLLMSessionNode 能挂到它下面）；ParentID 仍指向调用方工具节点，保证 task-stop
	// 的子树级联可命中。
	topSession, parentID := sessionID, ""
	if parentTC != nil {
		_, parentID = s.taskNodeBase(parentTC)
	}
	if tcID := strings.TrimSpace(in.ToolCallID); tcID != "" {
		if n := s.tasks.findByToolCall(instanceID, tcID); n != nil {
			parentID = n.TaskID
		}
	}
	name := title
	toolName := "session_create"
	if !creating {
		toolName = "session_send"
	}
	node, nerr := s.tasks.start(&TaskNode{
		Tool: toolName, ToolCallID: strings.TrimSpace(in.ToolCallID), ParentID: parentID,
		TopSession: topSession, Kind: TaskKindLLM, SessionID: sessionID, TurnID: turnID,
		InstanceID: instanceID, Name: name, Purpose: strings.TrimSpace(in.DisplayName),
		Simplified: name,
	})
	if nerr != nil {
		h.tc.Close()
		release()
		return nil, &pluginbridge.Error{Code: "session_run_failed", Msg: nerr.Error()}
	}

	// 取消级联（不阻塞父轮终态）；后台收尾推进任务节点 + 释放并发槽。
	parentTurn := ""
	if parentTC != nil {
		parentTurn = parentTC.req.Turn
		s.registerDetached(instanceID, parentTurn, h)
	}
	go func() {
		out, werr := h.wait(context.Background())
		s.finalizeDelegatedNode(node, out, werr)
		if parentTurn != "" {
			s.forgetDelegated(instanceID, parentTurn, turnID)
		}
		release()
	}()

	running := map[string]any{
		"ok": true, "state": "running",
		"session_id": sessionID, "dialog_id": resp.DialogID,
		"turn_id": turnID, "task_id": node.TaskID,
		"message": "会话已启动；结果请用 session_status(task_id=…) 或 task_status 获取",
	}
	waitSec := in.WaitSec
	if waitSec <= 0 {
		return running, nil
	}
	if !delegatedWait(h, time.Duration(waitSec*float64(time.Second))) {
		return running, nil
	}
	out, werr := h.wait(context.Background())
	res := delegatedWireResult(h, node, out, werr)
	res["dialog_id"] = resp.DialogID
	return res, nil
}

// validateSessionRunAgent 与 agent.delegate 同口径：拼错的 agent 不静默退化为全能力。
func (s *Server) validateSessionRunAgent(instanceID, scenarioID, agentName string) error {
	if agentName == "" {
		return nil
	}
	_, _, agents := s.loadScenario(instanceID, scenarioID)
	if a, _ := s.resolveAgentDef(scenarioID, agents, agentName); a == nil {
		if _, ok := s.registeredAgentDef(agentName); !ok {
			names := make([]string, 0, len(agents))
			for _, ag := range agents {
				if n := strings.TrimSpace(ag.Name); n != "" {
					names = append(names, n)
				}
			}
			hint := "场景内无可用 agent"
			if len(names) > 0 {
				hint = "可用：" + strings.Join(names, ", ")
			}
			return &pluginbridge.Error{Code: "agent_not_found",
				Msg: fmt.Sprintf("未知 agent %q（%s；留空 = 全能力）", agentName, hint)}
		}
	}
	return nil
}

// acquireSessionRunSlot 取项目级并发槽（默认上限 3；<=0 = 不限）。
// 超限时阻塞等待（排队），直到有空位或调用 ctx 结束。
func (s *Server) acquireSessionRunSlot(ctx context.Context, instanceID string) (func(), error) {
	limit := s.sessionRunMaxConcurrent(instanceID)
	if limit <= 0 {
		return func() {}, nil
	}
	s.sessionRunMu.Lock()
	if s.sessionRunSlots == nil {
		s.sessionRunSlots = map[string]chan struct{}{}
	}
	ch := s.sessionRunSlots[instanceID]
	if ch == nil {
		ch = make(chan struct{}, limit)
		s.sessionRunSlots[instanceID] = ch
	}
	s.sessionRunMu.Unlock()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, &pluginbridge.Error{Code: "session_busy",
			Msg: fmt.Sprintf("并发会话已达上限（%d），排队等待超时；请稍后重试", limit)}
	}
}

// sessionRunMaxConcurrent 读并发上限（usr 有效值 → prj 配置 → 缺省 3）。
//
// 用 UserConfigGet（含 `plugins.*`），不能用 UserConfigView（设置页视图会剔除插件键）。
func (s *Server) sessionRunMaxConcurrent(instanceID string) int {
	const def = 3
	if s.cfg == nil {
		return def
	}
	if res, err := s.cfg.UserConfigGet(facade.UserConfigGetRequest{
		InstanceID: instanceID, Scope: s.cfgScope(instanceID),
	}); err == nil {
		if raw, ok := res.Config[sessionRunMaxKey]; ok {
			return parseRunLimit(raw, def)
		}
	}
	if res, err := s.cfg.ConfigKVList(facade.ConfigKVListRequest{
		Domain: facade.DomainPrjConfig, InstanceID: instanceID, Scope: s.cfgScope(instanceID),
	}); err == nil {
		if raw, ok := res.List[sessionRunMaxKey]; ok {
			return parseRunLimit(raw, def)
		}
	}
	return def
}

func parseRunLimit(raw any, def int) int {
	s := strings.TrimSpace(fmt.Sprint(raw))
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		// 兼容 JSON 数字（float64 经 fmt 可能带小数点）。
		if f, ferr := strconv.ParseFloat(s, 64); ferr == nil {
			return int(f)
		}
		return def
	}
	return n
}
