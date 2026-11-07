package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	mcpgateway "github.com/lsong98sh/chonkpilot-agent/gateway"
	sessionrunner "github.com/lsong98sh/chonkpilot-agent/session/runner"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
)

// retryCallTimeout 是 tool-retry 经 gateway 重跑一次调用的超时（B-03：对齐本包 gateway 直调
// 既有 15s 口径，如 New 期 servers/register、各 emitGateway 调用；超时 → 走失败终态）。
const retryCallTimeout = 15 * time.Second

// onToolRetry 重试工具（不经 LLM，msg-ref §4.2 完整实现）：定位（task_id tk-* 内存直取校验
// 会话归属；回退 {session, turn}(+tool_call_id / 前端漂移 task_id=tool_call_id) 取该轮最后一条
// interrupted）→ gateway tools/call 重跑 → 终态广播 → 结果喂回该轮续轮（tool 结果进入下一条
// LLM 消息）。进程重启/死 turn（内存无节点/无运行轮次）→ 按库重建上下文（retryRecover）。
func (s *Server) onToolRetry(_ context.Context, _ string, v *mq.Value) error {
	var raw map[string]any
	_ = json.Unmarshal(v.Payload, &raw)
	session := str(raw["session"])
	if session == "" {
		session = str(raw["session_id"])
	}
	turn := str(raw["turn"])
	toolCallID := str(raw["tool_call_id"])
	taskID := str(raw["task_id"])
	instanceID := str(raw["instance_id"])
	req := retryReq{Session: session, Turn: turn, ToolCallID: toolCallID, TaskID: taskID, InstanceID: instanceID}

	node := s.retryTarget(req)
	tc := s.retryRunningTurn(node)
	if node == nil || tc == nil {
		// 内存未完整命中（节点丢失 / 轮次已死 / 进程重启）→ 按库重建（节点 + 续轮上下文）
		var herr error
		node, tc, herr = s.retryRecover(req, node)
		if herr != nil {
			v.Result = map[string]any{"ok": false, "error": herr.Error()}
			return nil
		}
	}
	if node == nil || node.Tool == "" {
		v.Result = map[string]any{"ok": false, "error": "tool not found"}
		return nil
	}
	// 上下文按节点归属还原（work_dir/data_dir 取 instance 绑定）；树归属同下发（I-90：
	// top_session = 节点所属主会话，parent = 被重试的 llm 侧节点本身）。
	ctxMsg := mcpgateway.Context{
		Session: node.SessionID, Turn: node.TurnID,
		InstanceID: node.InstanceID, ToolCallID: node.ToolCallID,
		TopSession: node.TopSession, Parent: node.TaskID,
	}
	if rec, ok := s.im.Lookup(node.InstanceID); ok {
		ctxMsg.WorkDir = rec.WorkDir
		ctxMsg.DataDir = rec.DataDir
	}
	args := node.ToolArgs()
	if args == nil {
		args = map[string]any{}
	}
	// B-03：gateway 重跑带超时（原来 context.Background() 无界 → 死连接可永久挂住本 handler）。
	ctx, cancel := context.WithTimeout(context.Background(), retryCallTimeout)
	defer cancel()
	// A5：非主会话强制同步（重跑同属"该会话里的工具调用"）——tc 缺失（按库重建失败）时不改参数。
	callArgs := args
	if tc != nil {
		callArgs = tc.gatewayArgs(node.Tool, args)
	}
	res, gwTaskID, err := s.gc.Call(ctx, node.Tool, callArgs, ctxMsg)
	if err != nil {
		// 失败 / 超时均走失败终态（errMsg 携带原因，超时 = context deadline exceeded）。
		s.tasks.done(node.TaskID, TaskStateError, "", err.Error())
		v.Result = map[string]any{"ok": false, "error": err.Error()}
		return nil
	}
	if gwTaskID != "" {
		// 转异步 / 待裁决（pending 或 awaiting）：节点挂 gateway 任务等待完成回报
		// （onGatewayTaskDone 续轮/广播）；不得在此落 done 终态（结果仍经 mcp-tasks-report 交付）。
		s.tasks.setGwTask(node.TaskID, gwTaskID)
		s.tasks.setState(node.TaskID, TaskStatePending)
		v.Result = map[string]any{"ok": true, "task_id": gwTaskID}
		return nil
	}
	// 同步完成：终态广播 + 结果喂回续轮（下一条 LLM 消息含 role=tool + 原 tool-call-id）
	summary := resultTextFromMap(res)
	s.tasks.done(node.TaskID, TaskStateDone, summary, "")
	if tc != nil {
		tc.FeedToolResult(node.ToolCallID, summary)
	}
	v.Result = map[string]any{"ok": true, "task_id": node.TaskID}
	return nil
}

// retryReq 是 tool-retry 定位输入（多形态兼容：{session, turn} 契约 /
// {session_id, task_id=tool_call_id} 前端现状）。
type retryReq struct {
	Session    string
	Turn       string
	ToolCallID string
	TaskID     string
	InstanceID string
}

// retryTarget 内存定位重试目标（msg-ref §4.2）：task_id（tk-* 内存 TaskID）直取并校验会话归属；
// 否则按 {session, turn}(+tool_call_id；前端漂移时 task_id 装载 tool_call_id) 取该轮最后一条
// interrupted（findRetryTarget）。未命中 → nil。
func (s *Server) retryTarget(req retryReq) *TaskNode {
	if strings.HasPrefix(req.TaskID, "tk-") {
		n := s.tasks.find(req.TaskID)
		if n == nil {
			return nil
		}
		if req.InstanceID != "" && n.InstanceID != req.InstanceID {
			return nil // 实例归属校验（缺口 3：不跨 instance 直取他人节点）
		}
		if req.Session != "" && n.SessionID != req.Session {
			return nil // 会话归属校验
		}
		return n
	}
	tcid := req.ToolCallID
	if tcid == "" {
		tcid = req.TaskID
	}
	return s.tasks.findRetryTarget(req.InstanceID, req.Session, req.Turn, tcid)
}

// retryRunningTurn 按重试目标定位运行中的轮次（turnCtx；无 → nil）。
// 按节点归属 instance 取（缺口 5：不跨 instance 命中同 turn id 的轮次）。
func (s *Server) retryRunningTurn(node *TaskNode) *turnCtx {
	if node == nil || node.TurnID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lookupTurnLocked(node.InstanceID, node.TurnID)
}

// retryRecover 重启/死 turn 恢复（msg-ref §4.2 retryStaleTarget/retryRecover）：
//   - node 为空 → 从库 tasktree 找 interrupted 节点（该 session；tool_call_id 匹配优先）；
//   - 轮次消息（user + 末条 assistant.tool_calls）为续轮上下文；该轮遗留的 tool_pair 已由启动
//     清理标 interrupted，重试结果**就地回填同一行**（I-176）；
//     缺失 assistant pair → 明确错误（tool not found：消息记录不完整）；
//   - 目标节点已 done / 库无 interrupted 节点 → tool not found（不重跑已完成 pair）。
//
// 返回 (恢复节点, 恢复的 turnCtx, error)；error 非 nil 时不建任何内存态。
func (s *Server) retryRecover(req retryReq, known *TaskNode) (*TaskNode, *turnCtx, error) {
	instanceID := req.InstanceID
	if instanceID == "" {
		if recs := s.im.List(); len(recs) == 1 {
			instanceID = recs[0].ID
		}
	}
	if instanceID == "" {
		return nil, nil, errors.New("tool not found（实例不可定位，无法恢复）")
	}
	if req.Session == "" || req.Turn == "" {
		return nil, nil, errors.New("tool not found（恢复需 session+turn）")
	}
	// ① 节点：内存已知直接用；否则从库 tasktree 定位 interrupted 节点
	node := known
	if node == nil {
		n, err := s.dbRetryNode(instanceID, req)
		if err != nil {
			return nil, nil, err
		}
		node = n
	}
	if node.TurnID == "" {
		node.TurnID = req.Turn // tasktree 记录不存 turn_id；续轮消息按请求 turn 读取
	}
	// ② 续轮上下文 = 该轮消息（末条 assistant.tool_calls 无 tool 结果）
	st := s.newSessionStore(instanceID)
	msgs := st.LoadMessages(req.Turn)
	toolName, args, toolCallID, ok := lastToolCallPair(msgs, req.ToolCallID)
	if !ok {
		return nil, nil, errors.New("tool not found（消息记录不完整：该轮缺少 assistant.tool_calls pair，无法恢复续轮上下文）")
	}
	if node.Tool == "" {
		node.Tool = toolName
	}
	if node.ToolArgs() == nil {
		node.SetToolArgs(args)
	}
	if node.ToolCallID == "" {
		node.ToolCallID = toolCallID
	}
	// ③ 登记内存任务表（沿用原 task_id；不广播，随后 done 广播终态）
	s.tasks.restoreStale(node)
	// ④ 重建运行中轮次（hist 留空——msgs() 从库读当前轮消息；续轮工具结果与
	// 该轮 assistant.tool_calls 成对进入下一条 LLM 消息）
	tc := s.recoverTurnCtx(instanceID, req.Session, req.Turn)
	return node, tc, nil
}

// dbRetryNode 从库 tasktree 定位 interrupted 目标节点（data-tasktree-list 按 top_session
// 枚举；会话内取 tool_call_id 匹配优先，其次最近一条 interrupted；无 interrupted → 不重跑）。
func (s *Server) dbRetryNode(instanceID string, req retryReq) (*TaskNode, error) {
	payload := map[string]any{"top_session": req.Session}
	if dlg := s.dialogOf(instanceID, req.Session); dlg != "" {
		payload["dialog_id"] = dlg
	}
	res, err := dataRequest(s.bus, msgkeys.TopicDataTasktreeList, map[string]any{
		"instance_id": instanceID, "data": payload,
	})
	if err != nil {
		return nil, errors.New("tool not found（tasktree 读取失败: " + err.Error() + "）")
	}
	var match *TaskNode
	raw, _ := res["nodes"].([]any)
	for _, it := range raw {
		m, _ := it.(map[string]any)
		if m == nil {
			continue
		}
		if str(m["session_id"]) != req.Session {
			continue
		}
		if str(m["status"]) != TaskStateInterrupted {
			continue
		}
		if req.ToolCallID != "" && str(m["tool_call_id"]) != req.ToolCallID {
			continue
		}
		n := nodeFromDBRec(m, instanceID)
		if n == nil {
			continue
		}
		if match == nil || (req.ToolCallID == "" && n.ToolCallID == req.TaskID) {
			match = n
		}
		if req.ToolCallID != "" && match.ToolCallID == req.ToolCallID {
			break
		}
	}
	if match == nil {
		return nil, errors.New("tool not found（无 interrupted 目标节点）")
	}
	return match, nil
}

// nodeFromDBRec 把 data-tasktree-list 节点记录还原为内存 TaskNode（可重试中断节点）。
func nodeFromDBRec(m map[string]any, instanceID string) *TaskNode {
	taskID := str(m["node_id"])
	if taskID == "" {
		taskID = str(m["task_id"])
	}
	if taskID == "" {
		return nil
	}
	title := str(m["title"])
	if title == "" {
		title = str(m["name"])
	}
	n := &TaskNode{
		TaskID:     taskID,
		ToolCallID: str(m["tool_call_id"]),
		ParentID:   str(m["parent_node_id"]),
		TopSession: str(m["top_session"]),
		Kind:       str(m["kind"]),
		SessionID:  str(m["session_id"]),
		InstanceID: instanceID,
		Name:       title,
		Simplified: title,
		State:      TaskStateInterrupted,
	}
	if k := n.Kind; k == "" {
		n.Kind = TaskKindTool
	}
	return n
}

// recoverTurnCtx 重建运行中轮次（进程重启/死 turn 恢复专用）：hist 仅含**系统提示词层**
// （与新建轮同一构造，见 resolveTurnScenario）——msgs() 再从库加载当前轮（= 被重试的 turn）
// 全部消息，后续 FeedToolResult 的 tool 结果与该轮 assistant.tool_calls 成对进入下一条 LLM
// 消息；终态 llm-complete 沿用原 {session, turn}。该轮标记 forceFull=true：即使被重试 turn
// 落在"非维持轮"范围也必须全量拼接。
func (s *Server) recoverTurnCtx(instanceID, session, turn string) *turnCtx {
	// 去重（P3）：同 {instance, turn} 已有存活轮次 → 直接复用，避免另起 loop 双跑
	//（此前去重命中仍返回**未登记**的新 tc，其 loop 亦在跑 → 同 {session,turn} 双执行）。
	tk := instKey(instanceID, turn)
	s.mu.Lock()
	existing := s.turnCtxByKeyLocked(tk)
	s.mu.Unlock()
	if existing != nil {
		return existing
	}
	// ctx 绑定 instance_id（同 newTurnCtx）：恢复后的 gateway 方法调用同样携带实例归属。
	ctx, cancel := context.WithCancel(withInstance(context.Background(), instanceID))
	// WorkDir/DataDir 随 instance-id 绑定（同 onLLMStart）：恢复轮的 gateway 上下文与
	// **记忆 / 资产指引**（msgs() 的 memoryGuide/assetGuide 依赖 tc.req.WorkDir）依赖它
	// ——此前为空 → 指引缺失。
	req := StartReq{InstanceID: instanceID, Session: session, Turn: turn}
	if rec, ok := s.im.Lookup(instanceID); ok {
		req.WorkDir = rec.WorkDir
		req.DataDir = rec.DataDir
	}
	// 系统提示词五层（**与新建轮同一构造**，见 resolveTurnScenario）：恢复轮请求里无 scenario/agent
	// **入参**，但会话固定参数（子聊天的 agent / scenario_id，落库在会话行）经同一构造**取回**
	// → 人格与第五层（会话注入层）在重启后不丢（SUBCHAT-2 阶段 A）。
	sc := s.resolveTurnScenario(instanceID, session, "", "")
	var hist []ChatMsg
	if sc.sysPrompt != "" {
		hist = []ChatMsg{{Role: "system", Content: sc.sysPrompt}}
	}
	// LLM 配置：恢复轮无 provider name（llm-start.llm 未落库）→ temperature/maxOutputToken 无来源
	// （不发送）；base/apiKey/model 走 turnCtx 默认（回落 exe flags / 客户端默认模型）。
	// 工具循环上限回落缺省 20，否则 0 会让首个 LLM step 立即命中 TOOL_LOOP_LIMIT。
	llmMaxIter := defaultMaxToolIterations
	// 超时/重试同源加载（T-27 接线；缺省已回落旧硬编码常量）。
	respTO, streamTO, retryBudget, retryCnt := s.loadLLMRuntimeConfig(instanceID)
	contCfg, _ := s.loadUserConfig(instanceID)
	contEnabled, contDelay, contText := llmContinueConfigFrom(contCfg)
	tc := &turnCtx{
		server:    s,
		req:       req,
		ctx:       ctx,
		life:      sessionrunner.NewLifecycle(cancel),
		in:        make(chan ChatMsg, 64),
		hist:      hist,
		asyncDone: make(chan asyncDoneMsg, 8),

		llmSpec:           s.exeDefaultSpec(), // 无 provider name → exe flags 隐含默认；request model 走 spec.DefaultModel
		responseTimeout:   respTO,
		streamTimeout:     streamTO,
		retryCount:        retryCnt,
		retryBudget:       retryBudget,
		contCfg:           sessionrunner.ContinueConfig{Enabled: contEnabled, Delay: contDelay, Text: contText},
		maxToolIterations: llmMaxIter,
		keepFullTurns:     s.loadKeepFullTurns(instanceID),
		keepFullTokens:    s.loadKeepFullTokens(instanceID),
		briefBudget:       s.loadBriefBudget(instanceID),
		// 异步看门狗超时同源加载（否则恢复轮回落缺省、忽略 prj 键 pending_task_timeout_sec）。
		pendingTaskTimeout: pendingTaskTimeoutOf(s.prjConfigValues(instanceID, pendingTaskTimeoutKey)),
		// 重试/恢复的 turn 强制全量拼接（即使落在"非完整区"范围）。
		forceFull: true,
		// 恢复轮标记（A4-P4b-0：恢复轮由引擎执行；断链由
		// `engineSynthesizeDanglingToolResults` 在 RunTurn 前补齐）。
		isRecovery:  true,
		engineInbox: newChatInbox(),
		// 系统提示词层（组装结果 Usage.Fixed 的固定开销来源；与 hist 首位同源）。
		sysPromptText: sc.sysPrompt,
	}
	// 子轮次（父链非空 / 子会话 / 干活子会话）不做自动续写（见 engineRuntimeConfig）。
	tc.autoContinueOff = subTurnNoAutoContinue(req, sc)
	s.launchTurnLoop(tc)
	s.mu.Lock()
	// 竞态二次确认（P3）：构造期间可能已被并发恢复登记 → 回收本新 tc（Close 停其 loop）并复用
	// 已存在者，保证同 {instance,turn} 只跑一个 loop。
	if existing := s.turnCtxByKeyLocked(tk); existing != nil {
		s.mu.Unlock()
		tc.Close()
		return existing
	}
	// 登记键 = instKey(instance, id)（缺口 5：按 instance 分桶）。
	bk := instKey(instanceID, session)
	if _, ok := s.busy[bk]; !ok {
		s.busy[bk] = turn
	}
	s.registerTurnLocked(tk, tc)
	s.mu.Unlock()
	return tc
}

// lastToolCallPair 从轮次消息取最后一条 assistant 的 tool_call（配对重跑）：
// 返回 (工具名, 参数 map, tool-call-id, ok)。toolCallID 给定 → 精确匹配。
func lastToolCallPair(msgs []ChatMsg, toolCallID string) (string, map[string]any, string, bool) {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "assistant" || len(msgs[i].ToolCalls) == 0 {
			continue
		}
		for j := len(msgs[i].ToolCalls) - 1; j >= 0; j-- {
			tc := msgs[i].ToolCalls[j]
			if toolCallID != "" && tc.ID != toolCallID {
				continue
			}
			return tc.Name, parseArgs(tc.Arguments), tc.ID, true
		}
	}
	return "", nil, "", false
}
