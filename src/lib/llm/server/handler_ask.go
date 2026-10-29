package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
)

// onAskUserReply ask 应答 → 作为 tool-result 喂回 LLM 循环；ask 任务节点 → tasks.done。
func (s *Server) onAskUserReply(_ context.Context, _ string, v *mq.Value) error {
	// 载荷字段 = ask_id（61 §4.2；I-152 已闭环，旧连字符名不再回落）。
	var raw map[string]any
	_ = json.Unmarshal(v.Payload, &raw)
	askID := str(raw["ask_id"])
	answer := str(raw["answer"])
	// dialog_id（61 §4.2，§0.6 必带；AskUser S1-4）：前端原样回带 `session-ask` 下发的同一值。
	// 仅用于一致性留痕（路由权威仍是 ask_id：ask_id 全局唯一、登记取走即销）——
	// 缺失（旧前端 / 历史载荷）或与登记值不一致 → 记 Warn，**不阻断**应答。
	replyDialog := str(raw["dialog_id"])
	// 按 instance 归属取走等待登记（缺口 5）：带 instance_id → 只在本 instance 桶内命中；
	// 未带（旧载荷/旧调用方）→ 全桶回退（ask id 全局唯一，旧语义等价）。
	s.mu.Lock()
	w, ok := s.takeAskLocked(str(raw["instance_id"]), askID)
	s.mu.Unlock()
	if !ok {
		// MOD-1 M6b：等待登记缺失（超时摘除 / 进程重启）→ 回查落库行，按 61 §4.2
		// 「已超时关闭 → 转普通 user 消息 = 新 turn」；回查不到 → 明确失败（不静默）。
		return s.replyExpiredAsk(str(raw["instance_id"]), askID, answer)
	}
	if replyDialog == "" {
		logf("[chonkpilot-server] ask-user-reply 缺 dialog_id（61 §4.2 §0.6 必带）: ask_id=%s\n", askID)
	} else if w.dialogID != "" && replyDialog != w.dialogID {
		logf("[chonkpilot-server] ask-user-reply 的 dialog_id 与提问不一致: ask_id=%s 提问=%s 回带=%s\n",
			askID, w.dialogID, replyDialog)
	}
	if w.taskID != "" {
		s.tasks.done(w.taskID, TaskStateDone, answer, "")
	}
	s.mu.Lock()
	tc := s.lookupTurnLocked(w.instanceID, w.turnID)
	s.mu.Unlock()
	if tc != nil {
		tc.FeedToolResult(w.toolID, answer)
	}
	return nil
}

// ask 登记等待（tool-call 命中 ask_user 时调用）：广播 ask-user
// （对齐 msg-ref §4.3：ask_id/question/options/custom/session/turn/expires_at + multi/recommended）。
func (s *Server) ask(turnID, toolID string, args map[string]any, taskID string) string {
	return s.askWithID(turnID, toolID, args, taskID, "ask-"+newID())
}

// askWithID 同 ask，但 ask_id 由调用方给定（MOD-1 M6b：turn 循环先落库 ask_id，再广播卡片）。
func (s *Server) askWithID(turnID, toolID string, args map[string]any, taskID, askID string) string {
	if askID == "" {
		askID = "ask-" + newID()
	}
	expiresAt := time.Now().Add(askTTL)
	// 实例归属从 turn 取（业务 payload 一律必带 instance_id，见 61-消息一览 §0.1）；
	// 登记键 = instKey(instance, ask)（缺口 5：按 instance 分桶，不与其他 instance 串味）。
	turnInstance := ""
	if tc := s.lookupTurn(turnID); tc != nil {
		turnInstance = tc.req.InstanceID
	}
	// 定向投递（SUBCHAT-3 阶段 F-2 · 群聊 §13 D42）：卡片投到**该会话所在窗口**（`work_session`
	// → 父群聊窗口；群聊/单聊本体 → 承载窗口），解析不到 → 回落 turn 实例（旧行为，零变化）。
	deliverInstance := s.askTargetInstance(turnInstance, turnID)
	// 会话标识 dialog_id（61 §4.3 session-ask `dialog_id`，§0.6 必带；AskUser S1-3）：
	// 按「会话 → 对话」**既有映射**解析（sessionStore 备忘 / data-session-get 预热，见
	// `ensureDialogIndex`），与 turn / message 落库用的是同一份映射。
	// 取不到（历史遗留会话、映射缺失）时**不编造**：记 Warn 并按空串发送 ——
	// **这是 `session-ask.dialog_id` 唯一允许的空值场景**（前端只会原样回带）。
	sess := s.turnSession(turnID)
	dialogID := s.ensureDialogIndex(turnInstance, sess)
	if dialogID == "" {
		logf("[chonkpilot-server] session-ask 取不到 dialog_id（会话 → 对话映射缺失，按空串发送）: session=%s ask_id=%s\n",
			sess, askID)
	}
	s.mu.Lock()
	// 惰性回收过期等待登记（E2-1）：无答复的 ask 到期即摘除，避免常驻内存。
	s.purgeExpiredAsksLocked(time.Now())
	s.asks[instKey(turnInstance, askID)] = &askWaiter{
		askID: askID, instanceID: turnInstance, deliverInstance: deliverInstance,
		turnID: turnID, toolID: toolID, taskID: taskID,
		dialogID:  dialogID,
		expiresAt: expiresAt,
	}
	s.mu.Unlock()
	payload := map[string]any{
		"instance_id": deliverInstance,
		"ask_id":      askID, "question": askQuestion(args),
		"dialog_id": dialogID,
		"session":   sess, "turn": turnID,
		"expires_at": expiresAt.UTC().Format(time.RFC3339),
	}
	if opts, ok := args["options"]; ok {
		payload["options"] = opts
	}
	if multi, ok := args["multi"].(bool); ok {
		payload["multi"] = multi
	}
	// custom：是否允许在选项之外填「其他」自定义文本（与 options 并存，multi 时可与多选并存）。
	// 显式 true 才下发（缺省/非 true 不下发）；无 options 时前端本就默认 custom=true（自由文本问题）。
	if cust, ok := args["custom"].(bool); ok && cust {
		payload["custom"] = true
	}
	// recommended 统一为**数组**（前端 AskUser 按 Array 渲染高亮）；兼容单值字符串
	// → 单元素数组（I-41：契约/server/前端三处类型对齐）。
	if rec, ok := args["recommended"]; ok {
		switch rv := rec.(type) {
		case string:
			if rv != "" {
				payload["recommended"] = []string{rv}
			}
		case []any:
			arr := make([]string, 0, len(rv))
			for _, it := range rv {
				if s, sok := it.(string); sok && s != "" {
					arr = append(arr, s)
				}
			}
			if len(arr) > 0 {
				payload["recommended"] = arr
			}
		case []string:
			if len(rv) > 0 {
				payload["recommended"] = rv
			}
		}
	}
	s.publish("ask-user", payload)
	return askID
}

// askTargetInstance 解析 `ask_user` 卡片的**定向投递目标窗口实例**（= `session-ask.instance_id`；
// SUBCHAT-3 阶段 F-2 · 群聊 §13 D42 「按 instance 投递，不集中到主窗口」· §15.4 N23）：
// 目标 = **该会话所在窗口**，按会话类型推导：
//   - `work_session`（参与者干活子会话，无专属窗口）→ 其**父 group_chat** → 群聊窗口实例；
//   - `group_chat` / `sub_chat` 本体 → 承载该会话的窗口实例（窗口建窗时随 `instance-register.session_id` 登记）；
//   - 未命中（窗口未开 / 普通会话 / 无门面）→ **回落 turn 实例**（= 现状，零行为变化）。
//
// 「父 + 子都要」的广播分支（不带 `instance_id`）**不适用于 ask**：单问单答只需**抛问的那个会话窗口**
// 作答，故恒定向（本函数恒返回非空——最次回落 turn 实例）。
func (s *Server) askTargetInstance(turnInstance, turnID string) string {
	sess := s.turnSession(turnID)
	if sess == "" {
		return turnInstance
	}
	row := s.sessionRow(turnInstance, sess)
	lookup := sess
	if row.Kind == facade.SessionKindWorkSession && row.ParentID != "" {
		lookup = row.ParentID // 干活子会话 → 父群聊会话 → 群聊窗口实例
	}
	if ins, ok := s.im.InstanceForSession(lookup); ok && ins != "" {
		return ins
	}
	return turnInstance
}
