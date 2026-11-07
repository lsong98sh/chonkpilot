// 后台任务完成通知四段链路（docs/spec/00-overview/01-端到端数据流.md §5.3）：
//
//	① 落库：通知消息写入调用方会话（role=user, kind=notify），message_id 与事件同值（落库主键）；
//	② 挂账/轮末冲刷：会话有运行轮次 → 先入队（不落库、不显示），轮末按完成顺序落库 + 广播；
//	   会话 idle → 立即落库 + 广播；
//	③ text-notify 续轮：idle 落库后起一个内部通知轮（llm-start + llm-send{type:text-notify}）；
//	④ 结果已被 tool_result 取走 → 不发通知；通知轮内派生的任务完成 → 只落库/广播，不再级联。
//
// 状态与锁：
//   - 全部字段由 `notifyMu` 保护；需要在锁内读运行轮时按固定锁序 notifyMu → s.mu；
//   - 落库/广播/起轮一律在锁外执行（总线 Emit 同步派发，持锁 Emit 会自锁）。
package server

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
)

// nodeToolCallID 取任务节点关联的 LLM tool-call id（nil 安全；用于 consumed 的 message 表兜底）。
func nodeToolCallID(n *TaskNode) string {
	if n == nil {
		return ""
	}
	return n.ToolCallID
}

// notifyTurnStarting 是「通知轮正在受理」的占位值（防止并发重复起轮）。
const notifyTurnStarting = "\x01starting"

// completionNotice 是一条待落库/待广播的异步任务完成通知。
type completionNotice struct {
	instanceID string
	sessionID  string
	turnID     string // 任务归属 turn（空 = 冲刷时取该会话最近一轮）
	taskID     string
	toolCallID string
	tool       string
	state      string
	message    string
	messageID  string
	// fromNotifyTurn = 该任务派生自通知轮（§5.3 第 4 条）：只落库/广播，不再级联触发新通知轮。
	fromNotifyTurn bool
}

// ensureNotifyMapsLocked 懒建通知面 map（兼容测试里直接 &Server{} 构造、未经 New 的实例）。
// **须持 notifyMu**。
func (s *Server) ensureNotifyMapsLocked() {
	if s.notifyQueues == nil {
		s.notifyQueues = make(map[string][]completionNotice)
	}
	if s.notifyHandled == nil {
		s.notifyHandled = make(map[string]bool)
	}
	if s.notifyConsumed == nil {
		s.notifyConsumed = make(map[string]bool)
	}
	if s.notifyOrigin == nil {
		s.notifyOrigin = make(map[string]bool)
	}
	if s.notifyTurns == nil {
		s.notifyTurns = make(map[string]string)
	}
	if s.notifyProcessing == nil {
		s.notifyProcessing = make(map[string]bool)
	}
}

// enqueueCompletionNotice 受理一条完成通知：重复 message_id 幂等丢弃；会话忙碌时入队，
// idle 时立即冲刷（并按需起通知轮）。instance/session 缺失时退化为仅广播（旧行为，无法落库）。
func (s *Server) enqueueCompletionNotice(n completionNotice) {
	if n.instanceID == "" || n.taskID == "" {
		return
	}
	if n.messageID == "" {
		n.messageID = "msg-notify-" + n.taskID
	}
	if n.sessionID == "" {
		s.publishCompletionNotice(n)
		return
	}
	qKey := instKey(n.instanceID, n.sessionID)
	handledKey := instKey(n.instanceID, n.messageID)

	s.notifyMu.Lock()
	s.ensureNotifyMapsLocked()
	if s.notifyHandled[handledKey] {
		s.notifyMu.Unlock()
		return
	}
	s.notifyHandled[handledKey] = true
	// 通知轮内派生的任务节点在创建时已打标（task origin）；这里再以「当前运行轮是否通知轮」
	// 兜底（打标缺失 / 子轮次节点）。
	if s.notifyOrigin[instKey(n.instanceID, n.taskID)] {
		n.fromNotifyTurn = true
	}
	if tc := s.runningTurnFor(n.instanceID, n.sessionID); tc != nil && tc.isNotifyTurn {
		n.fromNotifyTurn = true
	}
	s.notifyQueues[qKey] = append(s.notifyQueues[qKey], n)
	s.notifyMu.Unlock()

	s.flushNoticeQueue(n.instanceID, n.sessionID, true)
}

// runningTurnFor 取该会话当前运行轮（**不持 notifyMu 时也可调用**；锁序 s.mu 单独获取）。
func (s *Server) runningTurnFor(instanceID, sessionID string) *turnCtx {
	s.mu.Lock()
	defer s.mu.Unlock()
	if turn := s.busy[instKey(instanceID, sessionID)]; turn != "" {
		return s.turnCtxByKeyLocked(instKey(instanceID, turn))
	}
	return nil
}

// sessionBusy 报告该会话当前是否有运行轮次（busy 注册表 = 同 instance 同 session 互斥）。
func (s *Server) sessionBusy(instanceID, sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.busy[instKey(instanceID, sessionID)] != ""
}

// markTaskNotifyOrigin 登记「任务派生自通知轮」（§5.3 防循环；任务完成时读取）。
func (s *Server) markTaskNotifyOrigin(instanceID, taskID string) {
	if instanceID == "" || taskID == "" {
		return
	}
	s.notifyMu.Lock()
	s.ensureNotifyMapsLocked()
	s.notifyOrigin[instKey(instanceID, taskID)] = true
	s.notifyMu.Unlock()
}

// markTaskResultConsumed 登记「任务结果已被取走」（§5.3 第 4 条；tool_result / 异步回执消费时调用）。
func (s *Server) markTaskResultConsumed(instanceID string, ids ...string) {
	if instanceID == "" {
		return
	}
	s.notifyMu.Lock()
	s.ensureNotifyMapsLocked()
	for _, id := range ids {
		if id != "" {
			s.notifyConsumed[instKey(instanceID, id)] = true
		}
	}
	s.notifyMu.Unlock()
}

// clearNotifyTurn 摘除会话的通知轮占位（仅当仍指向本 turn）。
func (s *Server) clearNotifyTurn(instanceID, sessionID, turn string) {
	qKey := instKey(instanceID, sessionID)
	s.notifyMu.Lock()
	if s.notifyTurns != nil {
		if cur := s.notifyTurns[qKey]; cur == turn || cur == notifyTurnStarting {
			delete(s.notifyTurns, qKey)
		}
	}
	s.notifyMu.Unlock()
}

// flushNoticeQueue 冲刷该会话的挂账通知：会话仍忙碌 → 保留队列等待轮末；否则按完成顺序
// 逐条「落库 + 广播」。allowAuto=true 时，若有实际通知且非通知轮派生 → 起内部通知轮续跑。
// 同一会话的冲刷串行化（notifyProcessing），保证并发完成回报的落库顺序 = 完成顺序。
func (s *Server) flushNoticeQueue(instanceID, sessionID string, allowAuto bool) {
	if sessionID == "" {
		return
	}
	qKey := instKey(instanceID, sessionID)

	s.notifyMu.Lock()
	s.ensureNotifyMapsLocked()
	if len(s.notifyQueues[qKey]) == 0 || s.notifyProcessing[qKey] {
		s.notifyMu.Unlock()
		return
	}
	if s.sessionBusy(instanceID, sessionID) {
		s.notifyMu.Unlock()
		return
	}
	s.notifyProcessing[qKey] = true
	s.notifyMu.Unlock()

	var prompts []string
	suppressAuto := false
	for {
		s.notifyMu.Lock()
		batch := s.notifyQueues[qKey]
		if len(batch) == 0 {
			delete(s.notifyProcessing, qKey)
			s.notifyMu.Unlock()
			break
		}
		if s.sessionBusy(instanceID, sessionID) {
			// 冲刷期间用户开了新轮 → 剩余项留在队列，由该轮结束后的 finish 冲刷。
			delete(s.notifyProcessing, qKey)
			s.notifyMu.Unlock()
			return
		}
		s.notifyQueues[qKey] = nil
		s.notifyMu.Unlock()

		for i, n := range batch {
			// 逐条复核：冲刷途中用户开了新轮 → 把本条及剩余项按原顺序放回队首，等轮末再冲刷。
			if s.sessionBusy(instanceID, sessionID) {
				s.notifyMu.Lock()
				rest := append([]completionNotice{}, batch[i:]...)
				s.notifyQueues[qKey] = append(rest, s.notifyQueues[qKey]...)
				delete(s.notifyProcessing, qKey)
				s.notifyMu.Unlock()
				return
			}
			// 通知轮派生标记（节点创建时打标）在本次消费后即失效。
			originKey := instKey(n.instanceID, n.taskID)
			s.notifyMu.Lock()
			if s.notifyOrigin[originKey] {
				n.fromNotifyTurn = true
				delete(s.notifyOrigin, originKey)
			}
			s.notifyMu.Unlock()

			if s.noticeConsumed(n) {
				continue
			}
			ok := s.persistNotice(n)
			if !ok {
				// 落库失败 → 撤销 handled 标记，允许后续重复 completion 重试（事件仍先广播提醒）。
				s.notifyMu.Lock()
				delete(s.notifyHandled, instKey(n.instanceID, n.messageID))
				s.notifyMu.Unlock()
			}
			s.publishCompletionNotice(n)
			prompts = append(prompts, n.message)
			if n.fromNotifyTurn {
				suppressAuto = true
			}
		}
	}

	if allowAuto && len(prompts) > 0 && !suppressAuto {
		s.startNotifyTurn(instanceID, sessionID, strings.Join(prompts, "\n\n"))
	}
}

// noticeConsumed 判定该任务结果是否已被取走（§5.3 第 4 条）：
//  1. 进程内 consumed 集合（runToolResult 成功取回 / handleAsyncDone 回填时登记）；
//  2. message 表已有该 tool_call 的**终态结果行**（进程重启后集合丢失时的可靠兜底）。
func (s *Server) noticeConsumed(n completionNotice) bool {
	s.notifyMu.Lock()
	consumed := s.notifyConsumed[instKey(n.instanceID, n.taskID)]
	if !consumed && n.toolCallID != "" {
		consumed = s.notifyConsumed[instKey(n.instanceID, n.toolCallID)]
	}
	s.notifyMu.Unlock()
	if consumed {
		return true
	}
	if n.toolCallID == "" || n.sessionID == "" {
		return false
	}
	raw, ok := s.newSessionStore(n.instanceID).LoadToolContent(n.sessionID, n.toolCallID)
	if !ok {
		return false
	}
	tc, ok := persist.ParseToolContent(raw)
	if !ok || tc.Result == nil {
		return false
	}
	switch tc.Result.Status {
	case persist.ToolStatusCompleted, persist.ToolStatusFailed, persist.ToolStatusCancelled:
		return true
	}
	return false
}

// persistNotice 把通知落成调用方会话的一条 role=user / kind=notify 消息：
// 目标 turn = 任务归属 turn，缺失则取该会话最近一轮（再缺失则建一个通知专用 turn）；
// 落库主键 = message_id → 重复 completion / 重复冲刷同一 id 恒只一行（幂等）。
func (s *Server) persistNotice(n completionNotice) bool {
	store := s.newSessionStore(n.instanceID)
	if err := store.EnsureSession(n.sessionID); err != nil {
		logf("[chonkpilot-server] 完成通知落库失败（ensure session=%s）: %v\n", n.sessionID, err)
		return false
	}
	turn := n.turnID
	if turn == "" {
		turn = store.LatestTurnID(n.sessionID)
	}
	if turn == "" {
		turn = "turn-" + newID()
	}
	if err := store.EnsureTurn(turn, n.sessionID); err != nil {
		logf("[chonkpilot-server] 完成通知落库失败（ensure turn=%s session=%s）: %v\n", turn, n.sessionID, err)
		return false
	}
	if _, err := store.AppendFullKeyed(turn, ChatMsg{Role: "user", Kind: "notify", Content: n.message}, n.messageID); err != nil {
		logf("[chonkpilot-server] 完成通知落库失败（turn=%s message_id=%s）: %v\n", turn, n.messageID, err)
		return false
	}
	return true
}

// publishCompletionNotice 广播 tool-notify（notice=completion）：payload 保持既有字段**逐字不变**
// （不新增字段；前端仍按 message_id 幂等去重）。落库成功与否只由 persistNotice 记日志。
func (s *Server) publishCompletionNotice(n completionNotice) {
	s.publish(msgkeys.TopicToolNotify, map[string]any{
		"instance_id": n.instanceID,
		"session_id":  n.sessionID,
		"turn_id":     n.turnID,
		"task_id":     n.taskID,
		"notice":      "completion",
		"message":     n.message,
		"message_id":  n.messageID,
	})
}

// startNotifyTurn 起一个**内部通知轮**（§5.3 第 3 条）：llm-start（NotifyTurn 标记）+ llm-send
// {type:text-notify}。仅主会话（子聊天 / 群聊 / 子会话由各自调度器驱动，与 consumeNextTurn 同口径）。
// 会话忙 / 实例未注册 / 已有通知轮 → 放弃（通知已落库/广播，用户仍可看到）。
func (s *Server) startNotifyTurn(instanceID, sessionID, content string) {
	if strings.TrimSpace(content) == "" {
		return
	}
	if kind := s.sessionRow(instanceID, sessionID).Kind; kind != "" {
		return
	}
	qKey := instKey(instanceID, sessionID)
	s.notifyMu.Lock()
	s.ensureNotifyMapsLocked()
	if s.notifyTurns[qKey] != "" {
		s.notifyMu.Unlock()
		return
	}
	s.notifyTurns[qKey] = notifyTurnStarting
	s.notifyMu.Unlock()

	res := s.acceptStart(StartReq{
		InstanceID: instanceID,
		Session:    sessionID,
		Turn:       "turn-" + newID(),
		NotifyTurn: true,
	})
	if ok, _ := res["accepted"].(bool); !ok {
		s.notifyMu.Lock()
		if s.notifyTurns[qKey] == notifyTurnStarting {
			delete(s.notifyTurns, qKey)
		}
		s.notifyMu.Unlock()
		return
	}
	turn, _ := res["turn"].(string)
	s.notifyMu.Lock()
	if turn != "" {
		s.notifyTurns[qKey] = turn
	} else {
		delete(s.notifyTurns, qKey)
	}
	s.notifyMu.Unlock()

	body, _ := json.Marshal(SendReq{
		InstanceID: instanceID,
		Session:    sessionID,
		Turn:       turn,
		Type:       SendTypeTextNotify,
		Content:    content,
	})
	v := &mq.Value{Payload: body}
	if err := s.onLLMSend(context.Background(), "", v); err != nil || v.Result != nil {
		// 投递被拒 / 异常：通知已落库+广播，关闭这个空转的通知轮，避免其永久占住 busy。
		logf("[chonkpilot-server] 通知轮投递失败（session=%s turn=%s）: err=%v result=%v\n", sessionID, turn, err, v.Result)
		if tc := s.lookupTurnIn(instanceID, turn); tc != nil {
			s.complete(tc, "error", "", "通知轮投递失败")
			tc.Close()
		}
	}
}
