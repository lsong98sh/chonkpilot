package server

import (
	"context"
	"encoding/json"
	"errors"

	sessionrunner "github.com/lsong98sh/chonkpilot-agent/session/runner"
)

// finish 发送唯一终态（对齐 21-llm-server）：
// llm-complete（唯一终态）落库 → 写快照（hist + 本轮消息，经 persist data-snapshot-set）
// → 发 llm-complete（压缩为内部方法面，不再有独立 MQ 事件；2026-10-31）。
// retryable 为**可选**字段（S21）：非 nil → 随 llm-complete 携带真实可重试分类；nil → 不带该字段。
func (s *Server) finish(tc *turnCtx, status, finishReason, code, message, text string, retryable *bool) {
	// 终态屏障（2026-11-06）：**先等全部子 agent 终态，再判定自身终态**。
	// 放在 BeginFinish 之前 —— 子代理未收尾时不得占用「唯一终态权」，取消路径仍可先落 interrupted。
	s.awaitDelegatedChildren(tc)
	// 取消竞态：自然完成路径等待子代理期间，用户可能已发起取消（llm-cancel / task-stop）。
	// 取消优先：不抢终态权，交给取消路径落 interrupted（否则父轮会误发 complete）。
	if tc.cancelRequested.Load() && status != "interrupted" {
		return
	}
	// 原子终态（E2-2）：正常完成路径与 onLLMCancel（取消）可能并发到达同一轮次——唯一终态权由
	// 生命周期闸门授予（`session/runner.Lifecycle.BeginFinish`，TURNRUN-1 批次 5）：
	// 终态（落库 CompleteTurnTokens + 快照 + llm-complete 广播）**仅发生一次**；
	// 其后到达者（如取消命中已终态轮次）静默跳过，不二次写库、不二次广播（覆盖已 complete 的 turn 行）。
	if !tc.life.BeginFinish() {
		return
	}
	// 真实终态（子轮次递归时父侧读取；Done 关闭后可见）。
	tc.turnStatus = status
	tc.turnFinish = finishReason
	// 通知轮结束 → 摘除会话的通知轮占位（后续再到达的完成回报可按普通 idle 规则处理）。
	if tc.isNotifyTurn {
		s.clearNotifyTurn(tc.req.InstanceID, tc.req.Session, tc.req.Turn)
	}
	// 终态结果记录（递归子轮次父侧在 <-tc.Done() 后读取；先于 Close 写入可见）
	tc.result = text
	if status == "error" {
		msg := message
		if msg == "" {
			msg = code
		}
		tc.turnErr = errors.New(msg)
	}
	st := s.newSessionStore(tc.req.InstanceID)
	// 本轮全部消息（最后一段；快照拼接与预存 token 共用一份，避免重复读库）。
	cur := st.LoadMessages(tc.req.Turn)
	// P3：轮次结束时计算并**预存**本轮完整态/简化态 token 数（判定/拼接后续直接累加，免重复估算）。
	fullTok, briefTok := turnTokenCounts(cur)
	// 终态落库失败仅留痕（B-32）：不重试、不改流程（终态已由 BeginFinish 保证不会二次写）；
	// 否则 DB 中该 turn 行仍为 running 而 UI 已显示 complete，下次启动 CleanupStaleTurns 会
	// 将其标 interrupted，状态与用户所见错乱。
	if err := st.CompleteTurnTokens(tc.req.Turn, status, finishReason, &fullTok, &briefTok); err != nil {
		logf("[chonkpilot-server] CompleteTurnTokens 失败（session=%s turn=%s）: %v\n", tc.req.Session, tc.req.Turn, err)
	}
	// 写快照（唯一终态写）：snapshot_turn = 本轮（快照覆盖到最后一条 turn）
	// 基 = 当前已提交快照（压缩内建可能在本轮期间回填过；详见 snapshotWriteBase）；
	// 不衔接 / 无快照 / 同轮继续 → 回落改前口径（tc.hist + 本轮消息）。
	msgs := append(append([]ChatMsg{}, tc.hist...), cur...)
	if base, ok := s.snapshotWriteBase(tc); ok {
		msgs = append(append([]ChatMsg{}, base...), cur...)
	}
	if err := st.SetSnapshot(tc.req.Session, msgs, tc.req.Turn); err != nil {
		logf("[chonkpilot-server] SetSnapshot 失败（session=%s turn=%s）: %v\n", tc.req.Session, tc.req.Turn, err)
	} else {
		// 压缩内建（2026-11-07 修正）：终态快照落库**之后**再起本轮暂存的压缩（锚点 = 本轮）。
		// 启动点从组装期后移到此，避免摘要回填与本次终态写快照竞争（覆盖 / CAS 丢弃）；
		// cur 追加进保留段，压缩快照仍包含刚落库的本轮消息。
		s.startPendingCompress(tc, cur)
	}
	ev := map[string]any{
		"instance_id": tc.req.InstanceID, "session": tc.req.Session, "turn": tc.req.Turn,
		"status": status, "finish_reason": finishReason,
		// parents：主轮次空、子轮次（递归）为父链——history 插件据此只提交主 session 快照
		"parents": tc.req.Parents,
		// work_dir / data_dir（2026-10-24，**只增字段**）：轮末订阅方（memory 插件每轮沉淀）
		// 需要实例绑定的目录；取值 = instance 绑定（onLLMStart 解析，见 server.go req.WorkDir）。
		// 语义与既有字段不变，旧订阅方忽略即可（61-消息一览 §4.3 session-complete）。
		"work_dir": tc.req.WorkDir, "data_dir": tc.req.DataDir,
	}
	if code != "" {
		ev["code"] = code
	}
	// 一树一库（P3-4b，61 §0.6）：轮末订阅方（memory 等）需按对话定位会话库。
	if dlg := s.dialogOf(tc.req.InstanceID, tc.req.Session); dlg != "" {
		ev["dialog_id"] = dlg
	}
	if message != "" {
		ev["message"] = message
	}
	if text != "" {
		ev["text"] = text
	}
	// retryable（S21 定稿新增**可选**字段）：错误终态携带该 turn 的真实可重试分类
	//（= *LLMError.Retryable：超时/网络/429/5xx → true；协议/鉴权等 → false）；
	// 正常完成/取消不带（旧订阅方按缺省处理，向后兼容；语义见 61-消息一览 §4.3 session-complete）。
	if retryable != nil {
		ev["retryable"] = *retryable
		// RETRY-1（2026-10-17）：错误终态携带**真实**重试计数（旧版为桥侧占位常量 1/0）。
		// retry_attempt = 已执行的重试次数（0 = 未重试；次数用尽时 = retry_count）；
		// retry_count   = 允许的最大重试次数（usr retryCount，0 = 不重试）；
		// retry_wait_ms = 0（终态，不再等待）→ 与中间态 session-retry 的 wait_ms>0 区分。
		ev["retry_attempt"] = tc.retryAttempt
		ev["retry_count"] = tc.retryCount
		ev["retry_wait_ms"] = 0
	}
	s.publish("llm-complete", ev)
	// 压缩（SESSION-1，2026-10-23 定案）：**纯内部方法面**，不再作为 MQ 事件广播
	// （`session-compress` 已于 2026-10-31 从消息面移除；61 同步）。压缩触发 = 组装后按
	// `Usage` 判阈值 → 后台压缩（见 compress.go），与本函数的终态落库无耦合。
	// 终态后的续轮动作由 runner 决策（`session/runner.DecideFollowUp`，TURNRUN-1 批次 5）：
	//   - complete / incomplete → 消费 `next_turn` 起新一轮（INBOX）；
	//   - 其余终态（error / interrupted / cancelled）→ 丢弃**本 turn** 的 LLM 排队项（NEXTRUN-1 终态收敛），
	//     防止用户结束本轮后被"僵尸提示词"自动触发下一轮（计数保留，等用户下次发送归零）。
	// 等 tc.Done 后再执行，确保 busy 已释放；仅主轮次自动续轮（子会话 / 群聊由各自调度器驱动）。
	followUp := sessionrunner.DecideFollowUp(status, len(tc.req.Parents) > 0)
	if followUp.ConsumeNextTurn {
		go func(inst, sess string) {
			<-tc.Done()
			s.consumeNextTurn(inst, sess)
		}(tc.req.InstanceID, tc.req.Session)
	}
	if followUp.DiscardNextRun {
		tc.discardNextRun.Store(true)
		go func(inst, sess, turn string) {
			<-tc.Done()
			s.discardNextRunForTurn(inst, sess, turn)
		}(tc.req.InstanceID, tc.req.Session, tc.req.Turn)
	}
	// §5.3 轮末冲刷：本轮运行期间挂账的完成通知，在本轮真正结束后按完成顺序落库 + 广播。
	// 此时会话即将 idle，但**不**自动发起通知轮（item 3 的自动续轮只针对“到达时已 idle”的完成回报，
	// 避免与用户紧接着发出的新轮竞争；通知仍进入会话历史）。
	go func(inst, sess string) {
		<-tc.Done()
		s.flushNoticeQueue(inst, sess, false)
	}(tc.req.InstanceID, tc.req.Session)
}

// complete 正常/取消终态（无错误码；不带 retryable——正常完成/取消无重试语义）。
func (s *Server) complete(tc *turnCtx, status, finishReason, text string) {
	s.finish(tc, status, finishReason, "", "", text, nil)
}

// retryableField 构造 llm-complete 的可选 retryable 指针（nil = 不带该字段）。
func retryableField(v bool) *bool { return &v }

// llmError 错误终态（不可重试语义）：收敛到 llm-complete{status:error, code, message,
// retryable:false}（llm-error 已移除）。用于无 *LLMError 分类来源的错误
// （EMPTY_REPLY / TOOL_LOOP_LIMIT / DB_ERROR）——空回复应"提示 + 手动继续"，不静默续写。
func (s *Server) llmError(tc *turnCtx, code, message string) {
	s.finish(tc, "error", "", code, message, "", retryableField(false))
}

// llmErrorRetryable 错误终态（带真实分类，S21）：retryable = *LLMError.Retryable
// （超时/网络/429/5xx → true；协议/鉴权 → false），前端据此决定是否自动续写。
func (s *Server) llmErrorRetryable(tc *turnCtx, code, message string, retryable bool) {
	s.finish(tc, "error", "", code, message, "", retryableField(retryable))
}

func (s *Server) publish(typ string, payload any) {
	b, _ := json.Marshal(payload)
	s.bus.Emit(context.Background(), domainSubject(typ), b)
}

// emitTurnStart 广播轮次开始：session-turn-start{instance_id, session, turn, parents}（相对主题）。
// 主轮次（llm-start 受理）与子轮次（llm_run 的 LLM 委派递归，runChildTurn）都广播；
// parents：主轮次空、子轮次为父会话链。前端据此维护会话树（parents 定位插入子节点）；
// history 插件据此（parents 空 = 主 session）触发 git 快照提交。
// 注：不能用 session-start（方法面 llm-start）广播——它由域方法面订阅（Start → onLLMStart）
// 受理，广播同主题会被自身方法路由误受理；turn-start 是独立轮次事件。
func (s *Server) emitTurnStart(req StartReq) {
	s.publish("turn-start", map[string]any{
		"instance_id": req.InstanceID,
		"session":     req.Session,
		"turn":        req.Turn,
		"parents":     req.Parents,
	})
}
