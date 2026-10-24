// 单聊（子聊天）域：两个域工具 handler + `subchat-*` 业务消息的生产 / 消费
// （SUBCHAT-2 阶段 B-2；设计见 `docs/agent-chat/单聊会话设计方案.md` §3.4 / §3.5 / §3.7 / §5.2）。
//
// 覆盖：
//   - `subchat_open`   handler：建**固定参数**子聊天会话（kind=sub_chat + parent_id + agent/
//     scenario_id/llm + lifecycle=chatting + purpose）→ 交接内容**以用户口吻**写为首条 user
//     消息 → 发 `subchat-opened`（父窗口据此自动开窗）→ **异步**发起子会话首轮（子 agent 自动
//     先开口，主轮**不挂起**）→ 返回 `{ok, session_id}`（**结果即终态，不回流**，§3.4.3）。
//   - `subchat_finish` handler：子 agent 自报「我认为已完成」→ `lifecycle=ready`（**建议态，
//     不等于结束**）+ 发 `subchat-state`；不关窗、不回流。
//   - `subchat-list`   （client→server）：`parent_id` 有值 → 该父会话的**全部**子聊天（含
//     finished）；缺省 → **全局未结束**子聊天（启动提醒，§3.4.7）。补 `parent_session_title`。
//   - `subchat-finish` （client→server）：用户**拍板**结束 → `lifecycle=finished` + 发
//     `subchat-state`；返回 `{ok}`（**不区分**正常完成 / 强制中止，D25）。
//
// 消息面形状 = `docs/spec/60-reference/61-消息一览.md` §4.2/§4.3/§4.5/§4.8（**唯一准则**；
// 阶段 B-1 已登记契约 + msgkeys + 桥/入口白名单）——本文件只做生产/消费，不改载荷形状。
//
// **`subchat-state` 的产生方实例来源（B2-6 裁决）**：llm-server **不持有** session→instance
// 映射（instance 表只登记窗口实例，无会话归属；定向投递工具 `plugin/instance.DeliveryTarget`
// 在 GUI 插件层，llm-server 不可达）。故本层一律**广播**：`subchat-state` **不带 `instance_id`**，
// 由前端按 `parent_session_id` / `session_id` 过滤（§5.3 明确允许的退化路径）。**不为此新增映射表**。
package server

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// 单聊域工具名（契约名；gateway 暴露名 = `self_<契约名>`，回调经 `route.Original` 还原为契约名）。
const (
	subchatOpenTool   = "subchat_open"
	subchatFinishTool = "subchat_finish"
)

// isSubChatOpenTool / isSubChatFinishTool / isSubChatTool 判定域工具是否为单聊工具。
func isSubChatOpenTool(name string) bool   { return name == subchatOpenTool }
func isSubChatFinishTool(name string) bool { return name == subchatFinishTool }
func isSubChatTool(name string) bool       { return isSubChatOpenTool(name) || isSubChatFinishTool(name) }

// execSubChatTool 执行单聊域工具（**同步**；结果返回即终态）。返回 (结果文本 JSON, isErr)。
// purpose = 本次调用的运行目的（C1 注入通道送达，args 内的展示名已被剥离）——`subchat_open`
// 据此落 `sessions.purpose` 并随 `subchat-opened` 下发（§5.4）。
func (s *Server) execSubChatTool(tc *turnCtx, tool string, args map[string]any, purpose string) (string, bool) {
	switch {
	case isSubChatOpenTool(tool):
		return s.runSubChatOpen(tc, args, purpose)
	case isSubChatFinishTool(tool):
		return s.runSubChatFinish(tc)
	default:
		return `{"ok":false,"error":"未知单聊工具 ` + tool + `"}`, true
	}
}

// runSubChatOpen 实现 `subchat_open`（§3.4.1 / §2 / §10 Q2）。返回 {ok, session_id}（失败 →
// {ok:false, error}，**必须明确回给 LLM**）。
//
// 注意（§6 E5）：本工具**感知不到窗口状态**（它不负责开窗）→ 照常建会话；开窗失败发生在前端
// （`open-chat` 返回 ok=false）→ 此处**不做窗口上限判定**。
func (s *Server) runSubChatOpen(tc *turnCtx, args map[string]any, purpose string) (string, bool) {
	agent := strings.TrimSpace(str(args["agent"]))
	scenarioID := strings.TrimSpace(str(args["scenario_id"]))
	llmRef := strings.TrimSpace(str(args["llm"]))
	prompt := strings.TrimSpace(str(args["prompt"]))
	switch {
	case agent == "":
		return subchatToolErr("agent 必填（子聊天固定角色）"), true
	case scenarioID == "":
		return subchatToolErr("scenario_id 必填（子聊天固定场景）"), true
	case llmRef == "":
		return subchatToolErr("llm 必填（子聊天固定 LLM）"), true
	case prompt == "":
		return subchatToolErr("prompt 必填（交接内容，以用户口吻撰写）"), true
	}
	if s.cfg == nil {
		return subchatToolErr("数据门面不可用（无法建子聊天会话）"), true
	}
	// **群聊分支**（SUBCHAT-3 阶段 F · 群聊方案 §5 / D39）：参与者 ≥ 2 → 建 `kind=group_chat`
	// 会话 + 写名册 + 目标/剧本/收敛条件 + 布置首条消息 → 起「依次认领」调度器。**1 个参与者
	// （或省略 `participants`）= 单聊**，走下方既有路径（行为逐字节不变）。
	if parts, ok := groupParticipantsArg(args); ok {
		return s.runGroupChatOpen(tc, agent, scenarioID, llmRef, prompt, purpose, parts,
			str(args["goal"]), str(args["playbook"]), str(args["convergence"]))
	}
	instanceID := tc.req.InstanceID
	parentSession := tc.req.Session
	sessionID := "subchat-" + newID()
	scope := s.cfgScope(instanceID)

	// ① 建会话：kind=sub_chat + parent_id（= 发起该调用的主会话）+ 固定参数 + lifecycle=chatting
	// + purpose（purpose 仅非空才写键；单聊方案 §3.2 / §5.4）。
	if _, err := s.cfg.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: instanceID, Scope: scope,
		DialogID:  s.dialogOf(instanceID, parentSession), // 子会话沿用父会话的对话（同一棵树）
		SessionID: sessionID, ParentSessionID: parentSession,
		Kind: facade.SessionKindSubChat, Lifecycle: facade.SessionLifecycleChatting,
		Agent: agent, ScenarioID: scenarioID, LLM: llmRef, Purpose: purpose,
	}); err != nil {
		return subchatToolErr("建子聊天会话失败: " + err.Error()), true
	}
	// 一树一库：回填新子会话 → 对话映射（后续 turn / message 写入按其补 dialog_id）。
	s.rememberDialog(instanceID, sessionID, s.dialogOf(instanceID, parentSession))
	// 会话标题 = 子聊天名字（= purpose）：窗口标题栏 / 会话列表子节点据此显示（§3.9.2）。
	// 失败仅留痕——标题非关键路径（回退 = 会话行初值 session_id）。
	// 一树一库：新建子会话尚未进入本进程备忘 → 沿用**父会话**的 dialog_id。
	title := sessionID
	if purpose != "" {
		if _, err := s.cfg.SessionTitle(facade.SessionTitleRequest{
			InstanceID: instanceID, SessionID: sessionID, Title: purpose, Scope: scope,
			DialogID: s.dialogOf(instanceID, parentSession),
		}); err != nil {
			logf("[chonkpilot-server] subchat_open: 设子聊天标题失败（session=%s）: %v\n", sessionID, err)
		} else {
			title = purpose
		}
	}

	// ② 建首轮 + 以用户口吻写入交接内容为首条 user 消息（§10 Q2 / D30）。**同一条消息只落一次**：
	// 异步首轮（startSubChatFirstTurn）以**库内既有上下文**触发一次 LLM 循环（Kick），不再喂新输入。
	store := newSessionStore(s.bus, instanceID)
	turnID := newID()
	if err := store.EnsureTurn(turnID, sessionID); err != nil {
		return subchatToolErr("建子聊天首轮失败: " + err.Error()), true
	}
	if _, err := store.AppendFullKeyed(turnID, ChatMsg{Role: "user", Kind: "text", Content: prompt}, ""); err != nil {
		return subchatToolErr("写交接内容失败: " + err.Error()), true
	}

	// ③ 发 subchat-opened（server→client）：父窗口据此自动开窗。**在写完交接首轮之后发出**
	//（§6 E9：顺序可控，不再有"空窗"）。instance_id = 发起该子聊天的窗口实例（= 本 turn 的
	// instance，即父窗口）→ 桥按精确相等过滤天然投给它（§12.4 / 群聊 §14.2）。
	s.emitSubchatOpened(instanceID, sessionID, parentSession, title, purpose)

	// ④ 异步发起子会话首轮（D30：子 agent 自动先开口；**主轮不挂起**）。
	go s.startSubChatFirstTurn(instanceID, sessionID, turnID)

	// ⑤ 工具结果即终态（§3.4.3）：返回 {ok, session_id}，返回后不再改写。
	return subchatToolOK(sessionID), false
}

// runSubChatFinish 实现 `subchat_finish`（§3.4.2）：把**本 turn 所属子聊天会话**的 lifecycle
// 置 `ready`（建议态）+ 发 `subchat-state`；返回 `{ok}`。不关窗、不回流、不等于结束。
func (s *Server) runSubChatFinish(tc *turnCtx) (string, bool) {
	sessionID := tc.req.Session
	if sessionID == "" || s.cfg == nil {
		return subchatToolErr("子聊天会话不可用"), true
	}
	if _, err := s.cfg.SessionLifecycleSet(facade.SessionLifecycleSetRequest{
		InstanceID: tc.req.InstanceID, SessionID: sessionID,
		DialogID:  s.dialogOf(tc.req.InstanceID, sessionID),
		Lifecycle: facade.SessionLifecycleReady, Scope: s.cfgScope(tc.req.InstanceID),
	}); err != nil {
		return subchatToolErr("置 ready 失败: " + err.Error()), true
	}
	s.emitSubchatState(tc.req.InstanceID, sessionID)
	return `{"ok":true}`, false
}

// startSubChatFirstTurn 异步发起子聊天首轮（D30：主轮不挂起；复用 `runChildTurn` 同族路径 ——
// 建 turnCtx → 登记 busy/turns → 广播 turn-start → 触发一次 LLM 循环）。
//
// 与 runChildTurn 的差别：**不带 parents**（子聊天不进任务执行树，D19/D39）——固定参数
// （agent/scenario_id/llm）取自**会话行**（resolveTurnScenario 会话优先），故本处 StartReq
// 不带 agent/scenario。首轮内容（交接）已由 handler 落库 → 用 `Kick()` 以库内上下文触发，
// **不喂新输入、不重复落库**（§6 E9 顺序可控）。
func (s *Server) startSubChatFirstTurn(instanceID, sessionID, turnID string) {
	defer func() {
		if r := recover(); r != nil {
			logf("[chonkpilot-server] startSubChatFirstTurn panic（已恢复）: session=%s turn=%s: %v\n", sessionID, turnID, r)
		}
	}()
	req := StartReq{InstanceID: instanceID, Session: sessionID, Turn: turnID}
	if rec, ok := s.im.Lookup(instanceID); ok {
		req.WorkDir, req.DataDir = rec.WorkDir, rec.DataDir
	}
	tc := newTurnCtx(context.Background(), s, req)
	bk, tk := instKey(instanceID, sessionID), instKey(instanceID, turnID)
	s.mu.Lock()
	if _, busy := s.busy[bk]; busy {
		s.mu.Unlock()
		tc.Close()
		return // 会话已有活动轮次（异常/并发）→ 不双开
	}
	s.busy[bk] = turnID
	s.registerTurnLocked(tk, tc)
	s.mu.Unlock()
	s.emitTurnStart(req)
	tc.Kick() // 触发一次 LLM 循环（库内交接内容 → 子 agent 先开口）
}

// ── `subchat-*` 业务消息：生产 ────────────────────────────────────────

// emitSubchatOpened 发 `subchat-opened`（server→client，61 §4.2）：父窗口据此自动开窗（F2）。
// payload = {instance_id, session_id, parent_session_id, title, purpose?}。
func (s *Server) emitSubchatOpened(instanceID, sessionID, parentSessionID, title, purpose string) {
	payload := map[string]any{
		msgkeys.SubchatOpenedEventInstanceId:      instanceID,
		msgkeys.SubchatOpenedEventSessionId:       sessionID,
		msgkeys.SubchatOpenedEventParentSessionId: parentSessionID,
		msgkeys.SubchatOpenedEventTitle:           title,
	}
	if purpose != "" {
		payload[msgkeys.SubchatOpenedEventPurpose] = purpose
	}
	s.publish(msgkeys.TopicSubchatOpened, payload)
}

// emitSubchatState 发 `subchat-state`（server→client，61 §4.8）：状态实时同步（ready 点亮 /
// finished 消失 / ready→chatting 撤销点亮）。
//
// **不带 `instance_id` = 广播**（本层不持有 session→instance 映射，见文件头 B2-6 裁决）→
// 由前端按 `parent_session_id` / `session_id` 过滤（§5.3 允许的退化路径）。
// lifecycle / parent_session_id 取**写入后**的会话行（单一事实源）。
func (s *Server) emitSubchatState(instanceID, sessionID string) {
	if s.cfg == nil || sessionID == "" {
		return
	}
	resp, err := s.cfg.SessionGet(facade.SessionGetRequest{
		InstanceID: instanceID, SessionID: sessionID, Scope: s.cfgScope(instanceID),
		DialogID: s.dialogOf(instanceID, sessionID), // 一树一库：会话行在对话库内
	})
	if err != nil {
		logf("[chonkpilot-server] emitSubchatState: SessionGet(%s): %v\n", sessionID, err)
		return
	}
	if !resp.Found {
		return
	}
	s.publish(msgkeys.TopicSubchatState, map[string]any{
		msgkeys.SubchatStateEventSessionId:       sessionID,
		msgkeys.SubchatStateEventParentSessionId: resp.Session.ParentID,
		msgkeys.SubchatStateEventLifecycle:       resp.Session.Lifecycle,
	})
}

// ── `subchat-*` 业务消息：消费（client→server）────────────────────────

// onSubchatList 处理 `subchat-list`（61 §4.3）：`parent_id` 有值 → 该父会话的**全部**子聊天
// （`SessionSubChats`，**含 finished**，前端各取所需过滤）；缺省 → **全局未结束**子聊天
// （`lifecycle ∈ {chatting, ready}`，启动提醒 §3.4.7）。结果 = `{list:[...]}`，每项
// `{session_id, purpose, title, lifecycle, parent_session_id, parent_session_title}`。
func (s *Server) onSubchatList(_ context.Context, _ string, v *mq.Value) error {
	var raw map[string]any
	_ = json.Unmarshal(v.Payload, &raw)
	instanceID := str(raw[msgkeys.FieldInstanceId])
	parentID := str(raw[msgkeys.SubchatListPayloadParentId])
	if instanceID == "" {
		v.Result = map[string]any{"error": "instance_id is required"}
		return nil
	}
	if s.cfg == nil {
		v.Result = map[string]any{"error": "数据门面不可用"}
		return nil
	}
	scope := s.cfgScope(instanceID)
	var chats []facade.Session
	if parentID != "" {
		res, err := s.cfg.SessionSubChats(facade.SessionSubChatsRequest{
			InstanceID: instanceID, ParentSessionID: parentID, Scope: scope,
			DialogID: s.dialogOf(instanceID, parentID), // 子聊天与其父同库
		})
		if err != nil {
			v.Result = map[string]any{"error": err.Error()}
			return nil
		}
		chats = res.List
	} else {
		res, err := s.cfg.SessionSubChatsAll(facade.SessionSubChatsAllRequest{InstanceID: instanceID, Scope: scope})
		if err != nil {
			v.Result = map[string]any{"error": err.Error()}
			return nil
		}
		for _, c := range res.List {
			if subchatUnfinished(c.Lifecycle) { // 全局分支只给"未结束"（启动提醒）
				chats = append(chats, c)
			}
		}
	}
	// 补 `parent_session_title`（同一父只查一次；查不到 → 空串）。
	parentTitles := map[string]string{}
	parentTitleOf := func(pid string) string {
		if pid == "" {
			return ""
		}
		if t, ok := parentTitles[pid]; ok {
			return t
		}
		t := ""
		if r, err := s.cfg.SessionGet(facade.SessionGetRequest{
			InstanceID: instanceID, SessionID: pid, Scope: scope, DialogID: s.dialogOf(instanceID, pid),
		}); err == nil && r.Found {
			t = r.Session.Title
		}
		parentTitles[pid] = t
		return t
	}
	list := make([]any, 0, len(chats))
	for _, c := range chats {
		list = append(list, map[string]any{
			msgkeys.FieldSessionId:       c.ID,
			msgkeys.FieldPurpose:         c.Purpose,
			msgkeys.FieldTitle:           c.Title,
			msgkeys.FieldLifecycle:       c.Lifecycle,
			msgkeys.FieldParentSessionId: c.ParentID,
			// `parent_session_title` 是 61 §4.3 载明的 subchat-list 结果项字段（字典键，无
			// msgkeys 生成常量 → 用字面量）。
			"parent_session_title": parentTitleOf(c.ParentID),
		})
	}
	v.Result = map[string]any{msgkeys.SubchatListResultList: list}
	return nil
}

// onSubchatFinish 处理 `subchat-finish`（61 §4.5）：用户**拍板**结束 → `lifecycle=finished`
// + 发 `subchat-state`；返回 `{ok}`。**不区分**正常完成 / 强制中止（D25）；不关窗、不回流。
func (s *Server) onSubchatFinish(_ context.Context, _ string, v *mq.Value) error {
	var raw map[string]any
	_ = json.Unmarshal(v.Payload, &raw)
	instanceID := str(raw[msgkeys.FieldInstanceId])
	sessionID := str(raw[msgkeys.SubchatFinishPayloadSessionId])
	if sessionID == "" {
		v.Result = map[string]any{"error": "session_id is required"}
		return nil
	}
	if instanceID == "" {
		v.Result = map[string]any{"error": "instance_id is required"}
		return nil
	}
	if s.cfg == nil {
		v.Result = map[string]any{"error": "数据门面不可用"}
		return nil
	}
	if _, err := s.cfg.SessionLifecycleSet(facade.SessionLifecycleSetRequest{
		InstanceID: instanceID, SessionID: sessionID,
		DialogID:  s.dialogOf(instanceID, sessionID),
		Lifecycle: facade.SessionLifecycleFinished, Scope: s.cfgScope(instanceID),
	}); err != nil {
		v.Result = map[string]any{"error": err.Error()}
		return nil
	}
	// 终止 = 停掉（若在跑的）群聊调度器（SUBCHAT-3 阶段 F-2 · §15.4 N22）：群聊 `finished` 后
	// 不应继续空转（单聊无运行调度器 → 空操作）。
	s.stopGroupRun(sessionID)
	s.emitSubchatState(instanceID, sessionID)
	v.Result = map[string]any{msgkeys.SubchatFinishResultOk: true}
	return nil
}

// ── 生命周期回写（§3.7 / §3.5）────────────────────────────────────────

// reopenSubChatOnNewTurn 在**子会话新一轮开始处**把 `ready` 回写为 `chatting`（§3.7：`ready`
// 只是"最近一轮的建议"、**非终态**；用户继续聊 → 新一轮即回 `chatting`；只有用户点「结束」才进
// `finished`）。判定依据 = 会话行 kind + lifecycle（随 `resolveTurnScenario` 的**同一次**会话行
// 读取带回，零额外查询）。回写成功后发 `subchat-state`（撤销前端"结束"按钮点亮）。
//
// 只作用于子聊天（`kind ∈ {sub_chat, group_chat}`）；主会话 / 子会话（sub_session）零开销。
func (s *Server) reopenSubChatOnNewTurn(instanceID, sessionID string, sc turnScenario) {
	if s.cfg == nil || !isSubChatKind(sc.kind) || sc.lifecycle != facade.SessionLifecycleReady {
		return
	}
	if _, err := s.cfg.SessionLifecycleSet(facade.SessionLifecycleSetRequest{
		InstanceID: instanceID, SessionID: sessionID,
		DialogID:  s.dialogOf(instanceID, sessionID),
		Lifecycle: facade.SessionLifecycleChatting, Scope: s.cfgScope(instanceID),
	}); err != nil {
		logf("[chonkpilot-server] reopenSubChatOnNewTurn(%s): %v\n", sessionID, err)
		return
	}
	s.emitSubchatState(instanceID, sessionID)
}

// ── 小工具 ────────────────────────────────────────────────────────────

// subchatUnfinished 判定子聊天是否"未结束"（状态条 / 启动提醒列出口径，§3.4.4）：
// `lifecycle ∈ {chatting, ready}` 列出；`finished` 不列（转会话列表回看）。
func subchatUnfinished(lifecycle string) bool {
	return lifecycle == facade.SessionLifecycleChatting || lifecycle == facade.SessionLifecycleReady
}

// subchatToolOK / subchatToolErr 构造工具结果 JSON 文本（终态；`{ok, session_id}` /
// `{ok:false, error}`）。
func subchatToolOK(sessionID string) string {
	b, _ := json.Marshal(map[string]any{"ok": true, "session_id": sessionID})
	return string(b)
}

func subchatToolErr(reason string) string {
	b, _ := json.Marshal(map[string]any{"ok": false, "error": reason})
	return string(b)
}
