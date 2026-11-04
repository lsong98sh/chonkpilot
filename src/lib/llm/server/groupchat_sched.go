// 群聊「依次认领」调度器 + `work_session` 干活子会话 + 参与者状态推送（SUBCHAT-3 阶段 F）。
//
// 设计见 `docs/agent-chat/群聊会话设计方案.md` §2/§3/§5/§6/§7/§18（D2–D5 · D11 · D13 · D20–D24 ·
// D27/D31–D33 · D43/D46/D47 · D49–D52）。硬约束：**群聊本体不跑 LLM、不挂工具**（D11 / §10 C4）——
// 所有 LLM 工作都在各参与者的 `work_session` 里。
//
// 一轮（大轮次）时序：
//  1. 按 `group_participants.seq` 顺序依次投递（**不抢、串行**，D2/D3/D4）；
//  2. 轮到者在自己 `work_session` 里跑一次 LLM（判定 + 执行同一次调用，D20）：
//     - **有关** → 结论（内联文本，D24）→ **立即上屏**（追加群聊展示板消息，带 `sender_agent` /
//     `mentions`）+ 入下一轮候选；
//     - **无关** → 落库（在它自己的 `work_session` 历史里）、**不上屏、不入下一轮**（D13/D21）；
//  3. 本轮全部走完（**屏障只挡下一轮启动**，不挡上屏，D31）→ 由本轮发言的 `@` 名单算出
//     下一轮的投递对象（D27/D32/D33）；**无人认领 → 主 agent 兜底**（D5）。
//  4. 主 agent 判「达标」→ 点亮 `ready`（复用 `subchat-state`，D10）；硬上限到顶 → 终审 + 停（D49/D50）。
//
// ── 机读约定（不得新增消息主题；用既有 `data-session-append-message` 字段 + 结论文本约定）──
//   - **「无关」** = 该参与者本轮工作子会话的**最终 assistant 文本**，其**首个非空行**恰为「无关」
//     （`groupIrrelevantMarker`）→ 判无关；
//   - **「收敛」** = 主 agent 的结论文本中**单独一行**为「【已收敛】」（`groupConvergedMarker`）→ 判目标达成；
//   - **到顶终审「未达成」** = 主 agent 终审文本中出现「【未达成】」（`groupNotMetMarker`）。
//
// **定向投递** = `@` 解析后落到消息的 `mentions`（D27/D33 的投递过滤依据）；`@全体` / `@所有人` /
// 不写 `@` = 公开条（投给全体）。下一轮投递对象写入 `group_round_targets`（每大轮次一条）。
//
// ── 阶段 F-2（2026-10-16 · 群聊 §13/§15 · N22–N25 · D42/D45）──
//   - **驱动归属 = 群聊窗口的 instance**（N23）：每次叫号解析 `driverInstance()`（`InstanceForSession`
//     命中 → 该窗口实例；未命中 → 回落发起窗口），子轮次以它执行（父 ctx = `sc.ctx`，**不再裸
//     `context.Background()`**）→ ① ask_user 落对窗口；② **关窗即停**（该实例 `instance-exit` →
//     `stopGroupRunsForInstance` 取消运行 ctx，**停机不收口、不落库**）。
//   - **「继续」= 断点补跑**（D45 甲案）：`groupResumePlan` 读本轮 `group_round_targets` → 逐个比对
//     「本轮是否已产结论」（`boardConclusionCounts`）→ **有结论跳过、只补无结论者**（不覆盖该轮记录）；
//     本轮已齐 → 从下一轮起（到顶续跑语义）。**终止**（`subchat-finish`）→ `stopGroupRun` 停调度。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
	"github.com/lsong98sh/chonkpilot-agent/session/component"
)

// 群聊机读约定常量（取值唯一来源 = 组件 `component.Group*`；保留包内别名，调用点不变）。
const (
	groupIrrelevantMarker = component.GroupIrrelevantMarker // 「无关」判定：回复首个非空行恰为此
	groupConvergedMarker  = component.GroupConvergedMarker  // 主 agent 判收敛：回复中单独一行
	groupNotMetMarker     = component.GroupNotMetMarker     // 到顶终审「未达成」
	groupAllMention       = component.GroupAllMention       // `@全体` 归一标记（`@所有人` / `@all` 亦归一为此）
)

// groupParticipant 是参与者（名册项；`seq` = 名单固定顺序，D3）。
type groupParticipant struct {
	agent string
	role  string
	seq   int
}

// groupSched 是一次群聊运行（一个 goroutine）的全部状态。
type groupSched struct {
	s          *Server
	instanceID string // 发起窗口实例（data 面 scope 基准；驱动实例解析失败时的回落）
	scope      facade.Scope
	groupID    string
	parentID   string
	scenarioID string
	llmRef     string
	mainAgent  string
	opening    string // 群聊首条消息（= 主 agent 布置任务，D18）
	resume     bool   // 本次运行是否为「继续」续跑（首轮输入加续跑标记）

	// ctx / cancel：调度器**运行态**上下文（F2-2 §15.4 N23）——子轮次以它为父 ctx（不再裸
	// context.Background()），「关窗即停」经 cancel 停机（**不引入 `paused` 落库态**：停机只
	// 停止进程内运行，lifecycle 保持原值；重开点「继续」再续跑）。
	ctx    context.Context
	cancel context.CancelFunc
	// driverMu / driver：当前**驱动实例**（= 群聊窗口的 instance；每轮刷新）。空 = 未解析出。
	// 用途 = ① ask_user 落对窗口（work_session 轮次以它执行）；② 关窗即停按它匹配。
	driverMu sync.Mutex
	driver   string

	parts   []groupParticipant // 按 seq 升序（D3）
	board   string             // 群聊展示板 turn id（承载所有上屏消息）
}

// startGroupChat 启动群聊调度器（新开群聊）：
// 建 sched（含名册）→ 起 goroutine 跑 first..budget 轮。**调用方已建好群聊会话 + 名册 + 首条消息**。
func (s *Server) startGroupChat(instanceID, groupID string) {
	sc, err := s.buildGroupSched(instanceID, groupID)
	if err != nil {
		logf("[chonkpilot-server] startGroupChat(%s): %v\n", groupID, err)
		return
	}
	s.launchGroupRun(sc, 1, s.loadGroupChatMaxRounds(instanceID), sc.allParts(), nil, true)
}

// launchGroupRun 起一次群聊运行：建运行 ctx / 登记运行句柄 → go run（不再裸 context.Background）。
//   - startRound = 起始大轮次；budget = 最大轮数；
//   - targets / prev = 首轮投递对象与输入（nil → 全体 / 布置任务）；saveFirst = 首轮是否写 round_targets。
func (s *Server) launchGroupRun(sc *groupSched, startRound, budget int, targets []groupParticipant, prev []groupBoardMsg, saveFirst bool) {
	sc.ctx, sc.cancel = context.WithCancel(context.Background())
	sc.driverInstance() // 初始化驱动实例（未开群聊窗口 → 回落发起窗口；后续每轮刷新）
	s.registerGroupRun(sc)
	go sc.run(startRound, budget, targets, prev, saveFirst)
}

// registerGroupRun 登记运行中的群聊调度器（同 groupID 新运行**顶替**旧运行：取消旧的，避免并发双跑）。
func (s *Server) registerGroupRun(sc *groupSched) {
	if sc == nil || sc.groupID == "" {
		return
	}
	s.groupMu.Lock()
	old := s.groupRuns[sc.groupID]
	s.groupRuns[sc.groupID] = sc
	s.groupMu.Unlock()
	if old != nil && old != sc && old.cancel != nil {
		old.cancel()
	}
}

// unregisterGroupRun 注销运行（仅当本 sched 仍是当前登记，防顶替后误删新运行）。
func (s *Server) unregisterGroupRun(sc *groupSched) {
	if sc == nil || sc.groupID == "" {
		return
	}
	s.groupMu.Lock()
	if s.groupRuns[sc.groupID] == sc {
		delete(s.groupRuns, sc.groupID)
	}
	s.groupMu.Unlock()
}

// stopAllGroupRuns 停机全部群聊调度（`Server.Stop` 兜底）：与 stopGroupRunsForInstance 同法，
// 只是不按驱动实例过滤。
func (s *Server) stopAllGroupRuns() {
	s.groupMu.Lock()
	runs := make([]*groupSched, 0, len(s.groupRuns))
	for _, sc := range s.groupRuns {
		runs = append(runs, sc)
	}
	s.groupMu.Unlock()
	for _, sc := range runs {
		if sc.cancel != nil {
			sc.cancel()
		}
	}
}

// stopGroupRunsForInstance 停机由某实例**驱动**的全部群聊（关窗即停，§15.4 N23）：`instance-exit`
// （显式退出 / 心跳超时回收）→ 取消其群聊运行 ctx（进程内停机；**不改 lifecycle**，重开点「继续」再续）。
func (s *Server) stopGroupRunsForInstance(instanceID string) {
	if instanceID == "" {
		return
	}
	s.groupMu.Lock()
	var hit []*groupSched
	for _, sc := range s.groupRuns {
		if sc.currentDriver() == instanceID {
			hit = append(hit, sc)
		}
	}
	s.groupMu.Unlock()
	for _, sc := range hit {
		if sc.cancel != nil {
			sc.cancel()
		}
	}
}

// stopGroupRun 停机某群聊的调度器（若在跑）：用户「终止」（`subchat-finish` → `finished`）后
// 群聊不应继续空转（F2-3 · §15.4 N22）。
func (s *Server) stopGroupRun(groupID string) {
	if groupID == "" {
		return
	}
	s.groupMu.Lock()
	sc := s.groupRuns[groupID]
	s.groupMu.Unlock()
	if sc != nil && sc.cancel != nil {
		sc.cancel()
	}
}

// currentDriver 取当前驱动实例（并发安全）。
func (sc *groupSched) currentDriver() string {
	sc.driverMu.Lock()
	defer sc.driverMu.Unlock()
	return sc.driver
}

// setDriver 记录当前驱动实例。
func (sc *groupSched) setDriver(id string) {
	sc.driverMu.Lock()
	sc.driver = id
	sc.driverMu.Unlock()
}

// driverInstance 返回本轮应使用的**驱动实例**（= 群聊窗口的 instance，§15.4 N23）：
// 优先 = 承载该群聊会话的窗口实例（`InstanceForSession`，由窗口建窗时随 instance-register.session_id
// 登记）；未命中（群聊窗口尚未开 / 无 UI）→ 回落**发起窗口实例**（后端仍可跑，与 F-1 行为一致）。
// 结果记入 sc.driver（关窗即停按它匹配）；每次调用刷新（窗口开/关可被感知）。
func (sc *groupSched) driverInstance() string {
	if ins, ok := sc.s.im.InstanceForSession(sc.groupID); ok && ins != "" {
		sc.setDriver(ins)
		return ins
	}
	sc.setDriver(sc.instanceID)
	return sc.instanceID
}

// stopped 判定调度器是否已被停机（关窗即停 / 顶替取消）——各轮边界检查，停机后不再推进。
func (sc *groupSched) stopped() bool {
	return sc.ctx != nil && sc.ctx.Err() != nil
}

// resumeGroupChat 续跑群聊（用户点「继续」；D50 到顶续跑 + F2-4 **断点补跑**）：
//
//   - 读**最后一个大轮次**的 `group_round_targets` 投递对象 → 逐个比对「本轮是否已产结论」
//     （展示板已有该 `sender_agent` 的结论）→ **有结论跳过、只补无结论者** → 补齐后本轮收口、续下一轮；
//   - 本轮若已齐（无缺失）→ 与 F-1 到顶续跑一致：从**下一个大轮次**起另给一轮预算（targets = 全体）；
//   - **续法 = (甲) 从断点续**（D45 第 1 道防线）：`work_session` **跨轮复用** → 其历史即「断点」，
//     保留了已发生的对话/工具记录；被中断的工具由启动清理（`data-session-cleanup-stale` /
//     `cleanupStaleToolPairs`）标 `interrupted` → LLM 自行判断要不要重调（**不保证**不重调，
//     故另有「工具幂等」第 2 道防线 —— 不属本阶段，见残余风险登记）。
func (s *Server) resumeGroupChat(instanceID, groupID string) {
	sc, err := s.buildGroupSched(instanceID, groupID)
	if err != nil {
		logf("[chonkpilot-server] resumeGroupChat(%s): %v\n", groupID, err)
		return
	}
	start, targets, prev, saveFirst := s.groupResumePlan(sc)
	sc.resume = true
	s.launchGroupRun(sc, start, s.loadGroupChatMaxRounds(instanceID), targets, prev, saveFirst)
}

// groupResumePlan 计算续跑计划（F2-4 断点补跑；纯读 + 纯计算，便于单测）：
//   - 读最后一个大轮次的 `group_round_targets` → 逐个比对「本轮是否已产结论」（展示板结论计数
//     vs 该 agent 截至该轮的累计被投递次数）→ 无结论者 = 待补跑；
//   - 有待补跑者 → (start=该轮, targets=待补跑者, prev=已产结论者末条发言, saveFirst=false)；
//   - 无待补跑者（本轮已齐/无轮次记录）→ (start=该轮+1 或 1, targets=nil(全体), prev=nil, saveFirst=true)。
func (s *Server) groupResumePlan(sc *groupSched) (int, []groupParticipant, []groupBoardMsg, bool) {
	res, err := s.cfg.GroupRoundTargetsLoad(facade.GroupRoundTargetsLoadRequest{
		InstanceID: sc.instanceID, SessionID: sc.groupID, Scope: sc.scope,
	})
	if err != nil || len(res.List) == 0 {
		return 1, nil, nil, true
	}
	rounds := make([]component.GroupRound, 0, len(res.List))
	for _, t := range res.List {
		rounds = append(rounds, component.GroupRound{Round: t.Round, Agents: t.Agents})
	}
	start, targets, prev, saveFirst := component.GroupResumePlan(
		rounds, groupRows(sc.boardView()), groupMembers(sc.parts), sc.mainAgent)
	return start, groupParticipants(targets), groupBoardMsgs(prev), saveFirst
}

// buildGroupSched 读群聊会话行 + 参与者名册构造调度器；群聊会话不存在 / 名册为空 → 错误。
func (s *Server) buildGroupSched(instanceID, groupID string) (*groupSched, error) {
	group := s.sessionRow(instanceID, groupID)
	if group.Kind != facade.SessionKindGroupChat {
		return nil, errors.New("not a group chat session: " + groupID)
	}
	res, err := s.cfg.GroupParticipantsLoad(facade.GroupParticipantsLoadRequest{
		InstanceID: instanceID, SessionID: groupID, Scope: s.cfgScope(instanceID),
	})
	if err != nil {
		return nil, err
	}
	parts := make([]groupParticipant, 0, len(res.List))
	for _, p := range res.List {
		parts = append(parts, groupParticipant{agent: p.Agent, role: p.Role, seq: p.Seq})
	}
	if len(parts) == 0 {
		return nil, errors.New("group chat has no participants: " + groupID)
	}
	sc := &groupSched{
		s: s, instanceID: instanceID, scope: s.cfgScope(instanceID),
		groupID: groupID, parentID: group.ParentID,
		scenarioID: group.ScenarioID, llmRef: group.LLM,
		mainAgent: strings.TrimSpace(group.Agent),
		parts:     parts,
	}
	sc.opening = sc.loadOpening()
	sc.board = sc.ensureBoardTurn()
	return sc, nil
}

// loadOpening 取群聊展示板的首条消息文本（= 主 agent 布置任务，D18；取不到 → 空串）。
func (sc *groupSched) loadOpening() string {
	store := newSessionStore(sc.s.bus, sc.instanceID)
	turn := store.LatestTurnID(sc.groupID)
	if turn == "" {
		return ""
	}
	for _, m := range store.LoadMessages(turn) {
		if strings.TrimSpace(m.Content) != "" {
			return m.Content
		}
	}
	return ""
}

// ensureBoardTurn 取（或建）群聊展示板 turn：群聊所有上屏消息挂在这一条 turn 下
// （群聊本体不跑 LLM，故该 turn 只承载消息行，无轮次终态）。
func (sc *groupSched) ensureBoardTurn() string {
	store := newSessionStore(sc.s.bus, sc.instanceID)
	if turn := store.LatestTurnID(sc.groupID); turn != "" {
		return turn
	}
	turn := newID()
	if err := store.EnsureTurn(turn, sc.groupID); err != nil {
		logf("[chonkpilot-server] groupEnsureBoardTurn(%s): %v\n", sc.groupID, err)
	}
	return turn
}

// run 跑大轮次循环：从 startRound 起、最多 budget 轮；每轮按 targets 串行叫号。
//   - targets = 首轮投递对象（nil → 全体）；
//   - prev = 首轮输入用的「上一轮发言」（nil → 布置任务 opening）；
//   - saveFirst = 首轮是否写 group_round_targets（**断点补跑** = false：该轮已有记录，不覆盖）。
//
// 停机检查（F2-2：「关窗即停」）落在每轮边界与每次叫号之间：停机后**立即返回**、不收口（lifecycle 不变）。
func (sc *groupSched) run(startRound, budget int, targets []groupParticipant, prev []groupBoardMsg, saveFirst bool) {
	defer func() {
		if r := recover(); r != nil {
			logf("[chonkpilot-server] groupSched.run panic（已恢复）: group=%s: %v\n", sc.groupID, r)
		}
		sc.s.unregisterGroupRun(sc)
	}()
	round := startRound
	if round < 1 {
		round = 1
	}
	if targets == nil {
		targets = sc.allParts() // 首轮 / 续跑首轮：全体
	}
	for i := 0; i < budget; i++ {
		if sc.stopped() || len(targets) == 0 {
			return
		}
		if i > 0 || saveFirst {
			sc.saveRoundTargets(round, targets)
		}
		var spoken []groupBoardMsg
		if i == 0 && !saveFirst {
			// 断点补跑：已产结论者的本轮发言并入（供本轮结束后算下一轮投递，屏障语义不变）。
			spoken = append(spoken, prev...)
		}
		converged := false
		for _, p := range targets {
			if sc.stopped() {
				return // 关窗即停：本轮中途停机 → 不再推进（不清落库，重开「继续」据 round_targets 补跑）
			}
			sc.emitParticipantState(p.agent, "thinking")
			input := sc.roundInput(p.agent, prev)
			if i == 0 && sc.resume {
				input = "（用户要求继续推进本次任务）\n" + input
			}
			text := sc.runWorkTurn(p.agent, input)
			if strings.TrimSpace(text) == "" || isGroupIrrelevant(text) {
				// 无关（D13/D21）：落库（在其自身 work_session 历史里）、不上屏、不入下一轮。
				sc.emitParticipantState(p.agent, "idle")
				continue
			}
			mentions := sc.parseMentions(text)
			sc.emitParticipantState(p.agent, "spoken")
			sc.appendBoard(p.agent, text, mentions)
			m := groupBoardMsg{agent: p.agent, text: text, mentions: mentions, public: isPublicMention(mentions)}
			spoken = append(spoken, m)
			if p.agent == sc.mainAgent && isGroupConverged(text) {
				converged = true
			}
		}
		// 收敛（D10）：主 agent 判达标 → 点亮 ready（本轮走完 = 屏障满足后收口）。
		if converged {
			sc.lightReady()
			return
		}
		prev = spoken
		targets = sc.nextTargets(spoken)
		round++
	}
	// 到顶强制停 + 主 agent 终审（D49/D50）。
	sc.finalReview()
}

// roundInput 组装本轮喂给某参与者的输入（D32：上一轮发言合并成一条，按 `@` 过滤，D33）。
// prev 为空（首轮 / 无人认领后的兜底轮）→ 喂布置任务（`opening`）。
func (sc *groupSched) roundInput(agent string, prev []groupBoardMsg) string {
	return component.GroupRoundInput(agent, sc.opening, groupSays(prev))
}

// nextTargets 由本轮发言算下一轮投递对象（D27/D32/D33）：公开条 → 全体；定向条 → 仅被 @ 者。
// 无人认领（本轮无人发言）→ 主 agent 兜底（D5）；算不出 → 亦回落主 agent。
func (sc *groupSched) nextTargets(spoken []groupBoardMsg) []groupParticipant {
	return groupParticipants(component.GroupNextTargets(groupMembers(sc.parts), sc.mainAgent, groupSays(spoken)))
}

// mainOnly 返回主 agent 单项（兜底，D5）；主 agent 不在名册 → nil。
func (sc *groupSched) mainOnly() []groupParticipant {
	return groupParticipants(component.GroupMainOnly(groupMembers(sc.parts), sc.mainAgent))
}

// allParts 返回全部参与者（按 seq 升序副本）。
func (sc *groupSched) allParts() []groupParticipant {
	out := make([]groupParticipant, len(sc.parts))
	copy(out, sc.parts)
	return out
}

// ── 工作子会话（work_session，D12/D22/D23）──────────────────────────────

// runWorkTurn 让某参与者在**自己的 `work_session`** 里跑一轮（判定 + 执行同一次 LLM 调用，D20），
// 返回其结论文本。`work_session` **跨大轮次复用**（确定性 id + 幂等建会话 → 不重复建）。
// 失败 → 返回空串（调用方按「无关」处理，不阻断整场）。
func (sc *groupSched) runWorkTurn(agent, input string) string {
	workID := groupWorkSessionID(sc.groupID, agent)
	// 驱动实例（= 群聊窗口的 instance，F2-2 §15.4 N23）：子轮次以它执行 → 轮内 `ask_user`
	// 由 `askTargetInstance` 反查落到该窗口；data 面 scope 同步取该实例（未登记 → 回落发起窗口）。
	driver := sc.driverInstance()
	scope := sc.s.cfgScope(driver)
	if scope.WorkDir == "" {
		scope = sc.scope
	}
	if _, err := sc.s.cfg.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: driver, Scope: scope,
		SessionID: workID, ParentSessionID: sc.groupID,
		DialogID: sc.s.dialogOf(driver, sc.groupID),
		Kind:     facade.SessionKindWorkSession, Agent: agent,
		ScenarioID: sc.scenarioID, LLM: sc.llmRef,
	}); err != nil {
		logf("[chonkpilot-server] groupWorkTurn ensure(%s/%s): %v\n", sc.groupID, agent, err)
		return ""
	}
	// 一树一库：回填工作会话 → 对话映射（后续 turn / message 写入按其补 dialog_id）。
	sc.s.rememberDialog(driver, workID, sc.s.dialogOf(driver, sc.groupID))
	req := StartReq{InstanceID: driver, Session: sc.groupID, ScenarioID: sc.scenarioID, LLMModel: sc.llmRef}
	if rec, ok := sc.s.im.Lookup(driver); ok {
		req.WorkDir, req.DataDir = rec.WorkDir, rec.DataDir
	} else if rec, ok := sc.s.im.Lookup(sc.instanceID); ok {
		req.WorkDir, req.DataDir = rec.WorkDir, rec.DataDir
	}
	// 父 ctx = 调度器运行 ctx（**不再裸 context.Background()**）：关窗即停经它级联取消在飞子轮次。
	text, _, err := sc.s.runChildTurn(sc.ctx, req, workID, input, agent)
	if err != nil {
		logf("[chonkpilot-server] groupWorkTurn(%s/%s): %v\n", sc.groupID, agent, err)
		return ""
	}
	return text
}

// groupWorkSessionID 计算参与者干活子会话的**确定性 id**（跨轮复用；同一 `(群聊, agent)` 恒同 id）。
// 实现唯一 = 组件 `component.GroupWorkSessionID`（净化 + 短哈希防碰撞）。
func groupWorkSessionID(groupID, agent string) string {
	return component.GroupWorkSessionID(groupID, agent)
}

// ── 展示板（群聊会话消息面）────────────────────────────────────────────

// groupBoardMsg 是一条**上屏**的群聊发言（本轮「有关」结论）。
type groupBoardMsg struct {
	agent    string
	text     string
	mentions []string // 解析后的 @ 名单（归一到名册 agent；空 = 公开）
	public   bool     // 公开条（无 @ / @全体）
}

// ── 组件纯决策的类型互转（DTO 整形留宿主；语义唯一实现 = 组件 `component.Group*`）──────

func groupMembers(parts []groupParticipant) []component.GroupMember {
	out := make([]component.GroupMember, 0, len(parts))
	for _, p := range parts {
		out = append(out, component.GroupMember{Agent: p.agent, Role: p.role, Seq: p.seq})
	}
	return out
}

func groupParticipants(ms []component.GroupMember) []groupParticipant {
	if ms == nil {
		return nil
	}
	out := make([]groupParticipant, 0, len(ms))
	for _, m := range ms {
		out = append(out, groupParticipant{agent: m.Agent, role: m.Role, seq: m.Seq})
	}
	return out
}

func groupSays(msgs []groupBoardMsg) []component.GroupSay {
	out := make([]component.GroupSay, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, component.GroupSay{Agent: m.agent, Body: m.text, Mentions: m.mentions, Public: m.public})
	}
	return out
}

func groupBoardMsgs(ms []component.GroupSay) []groupBoardMsg {
	if ms == nil {
		return nil
	}
	out := make([]groupBoardMsg, 0, len(ms))
	for _, m := range ms {
		out = append(out, groupBoardMsg{agent: m.Agent, text: m.Body, mentions: m.Mentions, public: m.Public})
	}
	return out
}

// groupRows 把展示板消息视图（`data-session-history`）折成组件的原始行视图。
func groupRows(view []map[string]any) []component.GroupRow {
	out := make([]component.GroupRow, 0, len(view))
	for _, m := range view {
		raw, _ := m["mentions"].([]any)
		mentions := make([]string, 0, len(raw))
		for _, e := range raw {
			if s := str(e); s != "" {
				mentions = append(mentions, s)
			}
		}
		out = append(out, component.GroupRow{Agent: str(m["sender_agent"]), Body: str(m["content"]), Mentions: mentions})
	}
	return out
}

// appendBoard 把一条结论追加到群聊展示板（**上屏**，D31）：带 `sender_agent` + `mentions`。
// **主 agent ≡ 用户（D47）**：其发言按 `role=user` 落库（前端靠左显示）。
func (sc *groupSched) appendBoard(agent, text string, mentions []string) {
	role := "assistant"
	if agent == sc.mainAgent {
		role = "user"
	}
	msg := map[string]any{"role": role, "content": text, "sender_agent": agent}
	if len(mentions) > 0 {
		msg["mentions"] = mentions
	}
	if _, err := newSessionStore(sc.s.bus, sc.instanceID).AppendMsgMap(sc.board, msg, ""); err != nil {
		logf("[chonkpilot-server] groupAppendBoard(%s): %v\n", sc.groupID, err)
	}
}

// appendBoardNotice 追加一条系统提示到群聊展示板（如「因达上限终止」）。
func (sc *groupSched) appendBoardNotice(text string) {
	msg := map[string]any{"role": "system", "content": text}
	if _, err := newSessionStore(sc.s.bus, sc.instanceID).AppendMsgMap(sc.board, msg, ""); err != nil {
		logf("[chonkpilot-server] groupAppendBoardNotice(%s): %v\n", sc.groupID, err)
	}
}

// saveRoundTargets 记录本大轮次的投递对象（每大轮次一条，D44 ②）。
func (sc *groupSched) saveRoundTargets(round int, targets []groupParticipant) {
	agents := make([]string, 0, len(targets))
	for _, p := range targets {
		agents = append(agents, p.agent)
	}
	if _, err := sc.s.cfg.GroupRoundTargetsSave(facade.GroupRoundTargetsSaveRequest{
		InstanceID: sc.instanceID, SessionID: sc.groupID, Round: round, Agents: agents, Scope: sc.scope,
	}); err != nil {
		logf("[chonkpilot-server] groupSaveRoundTargets(%s, r=%d): %v\n", sc.groupID, round, err)
	}
}

// boardView 读群聊展示板消息视图（含 `sender_agent` / `mentions`；data-session-history）。
// 读失败 → nil（调用方按「无结论」保守处理）。
//
// 一树一库：会话类读**必须带 dialog_id**（缺 → persist `dialog_required`）。此前这里裸发
// 请求、缺该键 → 恒失败且被静默转成 nil → 「继续」把已产结论的参与者全部重跑。
func (sc *groupSched) boardView() []map[string]any {
	payload := map[string]any{"instance_id": sc.instanceID, "session_id": sc.groupID}
	if sc.s != nil {
		if dlg := sc.s.ensureDialogIndex(sc.instanceID, sc.groupID); dlg != "" {
			payload["dialog_id"] = dlg
		}
	}
	res, err := dataRequest(sc.s.bus, "data-session-history", payload)
	if err != nil {
		logf("[chonkpilot-server] groupBoardView(%s): %v\n", sc.groupID, err)
		return nil
	}
	msgs, _ := res["messages"].(map[string]any)
	raw, _ := msgs["messages"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, e := range raw {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// boardConclusionCounts 统计展示板上各 `sender_agent` 的**结论文数**（F2-4 断点补跑判定用）：
// 首条消息 = 主 agent 的布置任务（`role=user`，D18/D47，**不是结论**）→ 从主 agent 计数中扣除 1。
func (sc *groupSched) boardConclusionCounts() map[string]int {
	return component.GroupConclusionCounts(groupRows(sc.boardView()), sc.mainAgent)
}

// boardLastMsgs 返回展示板上各 `sender_agent` 的**末条**发言（F2-4：断点补跑重建「本轮已发生发言」）。
// 跳过首条（= 布置任务）；按名册顺序稳定输出。
func (sc *groupSched) boardLastMsgs() []groupBoardMsg {
	return groupBoardMsgs(component.GroupLastSays(groupRows(sc.boardView()), groupMembers(sc.parts)))
}

// emitParticipantState 发 `subchat-participant-state`（server→client，61 §4.3；阶段 E 已登记契约）。
// 广播（不带 instance_id，同 subchat-state 的退化路径）→ 前端按 parent_session_id 归属过滤。
func (sc *groupSched) emitParticipantState(agent, state string) {
	sc.s.publish(msgkeys.TopicSubchatParticipantState, map[string]any{
		msgkeys.SubchatParticipantStateEventSessionId:       sc.groupID,
		msgkeys.SubchatParticipantStateEventParentSessionId: sc.parentID,
		msgkeys.SubchatParticipantStateEventAgent:           agent,
		msgkeys.SubchatParticipantStateEventState:           state,
	})
}

// lightReady 点亮「可以结束了」（D10）：群聊 lifecycle=ready + 发 `subchat-state`（复用既有消息）。
func (sc *groupSched) lightReady() {
	if _, err := sc.s.cfg.SessionLifecycleSet(facade.SessionLifecycleSetRequest{
		InstanceID: sc.instanceID, SessionID: sc.groupID,
		DialogID:  sc.s.dialogOf(sc.instanceID, sc.groupID),
		Lifecycle: facade.SessionLifecycleReady, Scope: sc.scope,
	}); err != nil {
		logf("[chonkpilot-server] groupLightReady(%s): %v\n", sc.groupID, err)
		return
	}
	sc.s.emitSubchatState(sc.instanceID, sc.groupID)
}

// finalReview 到顶处置（D49/D50）：主 agent 终审表态 —— 判「达成」→ 点亮；判「未达成」（或未表态）
// → 强制 finish + 提示「因达上限终止，可能未完成」。
func (sc *groupSched) finalReview() {
	sc.emitParticipantState(sc.mainAgent, "thinking")
	text := sc.runWorkTurn(sc.mainAgent,
		"已达群聊轮次上限。请**终审**本次群聊：若目标已达成，在你回复里**单独一行**写「【已收敛】」；"+
			"若未达成，**单独一行**写「【未达成】」，并给出简短的进度摘要（已完成什么 / 还差什么）。")
	sc.emitParticipantState(sc.mainAgent, "spoken")
	if strings.TrimSpace(text) != "" {
		sc.appendBoard(sc.mainAgent, text, nil)
	}
	if isGroupConverged(text) {
		sc.lightReady()
		return
	}
	if _, err := sc.s.cfg.SessionLifecycleSet(facade.SessionLifecycleSetRequest{
		InstanceID: sc.instanceID, SessionID: sc.groupID,
		DialogID:  sc.s.dialogOf(sc.instanceID, sc.groupID),
		Lifecycle: facade.SessionLifecycleFinished, Scope: sc.scope,
	}); err != nil {
		logf("[chonkpilot-server] groupFinalReview finish(%s): %v\n", sc.groupID, err)
		return
	}
	sc.s.emitSubchatState(sc.instanceID, sc.groupID)
	sc.appendBoardNotice("（因达轮次上限终止，本次群聊可能未完成。可点「继续」在原线程上续跑。）")
}

// ── `@` 解析与机读判定（唯一实现 = 组件 `component.Group*`）────────────────

// parseMentions 解析发言**末行**的 `@` 名单并归一到名册 agent（D27/D33）：
// `@全体` / `@所有人` / `@all` → 归一为 `["全体"]`；未点名册内 → 忽略。
func (sc *groupSched) parseMentions(text string) []string {
	return component.GroupMentions(text, groupMembers(sc.parts))
}

// isPublicMention 判定一条发言是否公开（无 `@` 或 `@全体` → 投给全体，D33）。
func isPublicMention(mentions []string) bool { return component.GroupIsPublic(mentions) }

// isGroupIrrelevant 判定参与者回复是否「无关」：首个非空行恰为「无关」（可机读约定）。
func isGroupIrrelevant(text string) bool { return component.GroupIrrelevant(text) }

// isGroupConverged 判定主 agent 是否判「收敛」：文本中**单独一行**为「【已收敛】」。
func isGroupConverged(text string) bool { return component.GroupConverged(text) }

// ── 发起（扩展 `subchat_open`）与「继续」────────────────────────────────

// groupParticipantsArg 解析 `subchat_open` 的可选 `participants` 入参（元素 `{agent, role}`）。
// 返回 (名册, 是否走群聊)。**≥ 2 项才判群聊**（1 项或省略 = 单聊，走既有路径；D39）。
// 无 agent 名的项被丢弃；重复 agent 按首现保留。
func groupParticipantsArg(args map[string]any) ([]facade.GroupParticipant, bool) {
	raw, ok := args["participants"].([]any)
	if !ok || len(raw) < 2 {
		return nil, false
	}
	out := make([]facade.GroupParticipant, 0, len(raw))
	seen := map[string]bool{}
	for _, e := range raw {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		agent := strings.TrimSpace(str(m["agent"]))
		if agent == "" || seen[agent] {
			continue
		}
		seen[agent] = true
		out = append(out, facade.GroupParticipant{Agent: agent, Role: strings.TrimSpace(str(m["role"]))})
	}
	if len(out) < 2 {
		return nil, false
	}
	return out, true
}

// ensureMainParticipant 保证主 agent 在名册内（D46）：不在 → 置首位（角色标注「发起者 / 兜底裁判」）。
// 判定唯一实现 = 组件 `component.GroupEnsureMain`；本处只做 facade DTO 互转。
func ensureMainParticipant(list []facade.GroupParticipant, mainAgent string) []facade.GroupParticipant {
	members := make([]component.GroupMember, 0, len(list))
	for _, p := range list {
		members = append(members, component.GroupMember{Agent: p.Agent, Role: p.Role})
	}
	out := component.GroupEnsureMain(members, mainAgent)
	res := make([]facade.GroupParticipant, 0, len(out))
	for _, m := range out {
		res = append(res, facade.GroupParticipant{Agent: m.Agent, Role: m.Role})
	}
	return res
}

// runGroupChatOpen 处理 `subchat_open` 的**群聊**分支（N ≥ 2）：建 `kind=group_chat` 会话
// （lifecycle=chatting、parent_id、purpose、agent=主 agent、goal/playbook/convergence）→ 写名册
// （`group_participants`，含 seq 固定顺序）→ 写布置任务为首条展示板消息（D18，主 agent ≡ 用户 D47）
// → 发 `subchat-opened`（父窗口开窗）→ **异步**起调度器首轮。返回 `{ok, session_id}`（结果即终态）。
func (s *Server) runGroupChatOpen(tc *turnCtx, mainAgent, scenarioID, llmRef, prompt, purpose string,
	parts []facade.GroupParticipant, goal, playbook, convergence string) (string, bool) {
	instanceID := tc.req.InstanceID
	parentSession := tc.req.Session
	scope := s.cfgScope(instanceID)
	groupID := "group-" + newID()
	list := ensureMainParticipant(parts, mainAgent)

	// ① 建群聊会话（占位同一套会话行：kind/lifecycle/agent/scenario_id/llm/purpose + 目标/剧本/收敛）。
	if _, err := s.cfg.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: instanceID, Scope: scope,
		SessionID: groupID, ParentSessionID: parentSession,
		DialogID: s.dialogOf(instanceID, parentSession),
		Kind:     facade.SessionKindGroupChat, Lifecycle: facade.SessionLifecycleChatting,
		Agent: mainAgent, ScenarioID: scenarioID, LLM: llmRef, Purpose: purpose,
		Goal: goal, Playbook: playbook, Convergence: convergence,
	}); err != nil {
		return subchatToolErr("建群聊会话失败: " + err.Error()), true
	}
	// 一树一库：回填新群聊会话 → 对话映射（后续 turn / message 写入按其补 dialog_id）。
	s.rememberDialog(instanceID, groupID, s.dialogOf(instanceID, parentSession))
	title := groupID
	if purpose != "" {
		if _, err := s.cfg.SessionTitle(facade.SessionTitleRequest{
			InstanceID: instanceID, SessionID: groupID, Title: purpose, Scope: scope,
			DialogID: s.dialogOf(instanceID, parentSession),
		}); err == nil {
			title = purpose
		}
	}

	// ② 写参与者名册（`group_participants`；seq 按列表顺序 = 依次叫号的固定顺序，D3）。
	if _, err := s.cfg.GroupParticipantsSave(facade.GroupParticipantsSaveRequest{
		InstanceID: instanceID, SessionID: groupID, List: list, Scope: scope,
	}); err != nil {
		return subchatToolErr("写群聊参与者名单失败: " + err.Error()), true
	}

	// ③ 建展示板 turn + 写首条消息（= 主 agent 布置任务，D18；`role=user` 靠左，D47）。
	store := newSessionStore(s.bus, instanceID)
	boardTurn := newID()
	if err := store.EnsureTurn(boardTurn, groupID); err != nil {
		return subchatToolErr("建群聊展示板轮次失败: " + err.Error()), true
	}
	if _, err := store.AppendMsgMap(boardTurn, map[string]any{
		"role": "user", "content": prompt, "sender_agent": mainAgent,
	}, ""); err != nil {
		return subchatToolErr("写群聊布置任务失败: " + err.Error()), true
	}

	// ④ 发 subchat-opened（server→client）：父窗口据此自动开窗（群聊与单聊同一入口、只差图标）。
	s.emitSubchatOpened(instanceID, groupID, parentSession, title, purpose)

	// ⑤ 异步起「依次认领」调度器（主轮不挂起）。
	go s.startGroupChat(instanceID, groupID)

	return subchatToolOK(groupID), false
}

// onSubchatContinue 处理 `subchat-continue`（61 §4.2）：用户点「继续」→ 群聊 `lifecycle` 回
// `chatting` + 发 `subchat-state` + **在原线程上从上次大轮次之后续跑**（另给一轮最大轮次预算，
// D50）。**仅 `kind=group_chat`** 有效；返回 `{ok}`。
func (s *Server) onSubchatContinue(_ context.Context, _ string, v *mq.Value) error {
	var raw map[string]any
	_ = json.Unmarshal(v.Payload, &raw)
	instanceID := str(raw[msgkeys.FieldInstanceId])
	sessionID := str(raw[msgkeys.SubchatContinuePayloadSessionId])
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
	if s.sessionRow(instanceID, sessionID).Kind != facade.SessionKindGroupChat {
		v.Result = map[string]any{"error": "subchat-continue 仅支持群聊（kind=group_chat）"}
		return nil
	}
	if _, err := s.cfg.SessionLifecycleSet(facade.SessionLifecycleSetRequest{
		InstanceID: instanceID, SessionID: sessionID,
		DialogID:  s.dialogOf(instanceID, sessionID),
		Lifecycle: facade.SessionLifecycleChatting, Scope: s.cfgScope(instanceID),
	}); err != nil {
		v.Result = map[string]any{"error": err.Error()}
		return nil
	}
	s.emitSubchatState(instanceID, sessionID)
	go s.resumeGroupChat(instanceID, sessionID)
	v.Result = map[string]any{msgkeys.SubchatContinueResultOk: true}
	return nil
}
