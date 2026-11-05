package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
)

// askTimeoutKey 是项目级「ask_user 缺省超时（秒）」配置键。
// 0 / 缺省 / 非正 = 不超时；LLM 在工具参数显式给 timeout 时覆盖本值。
const askTimeoutKey = "ask_timeout_sec"

// askAutoTimeoutAnswer 是无推荐项时的超时自动回复文案（有 recommended 时优先取推荐值）。
const askAutoTimeoutAnswer = "用户未在时限内回复，请按推荐做法继续。"

// askCenterSub 是 Ask 中心订阅（user 作用域；workDir 非空 = 只订该目录）。
type askCenterSub struct {
	instanceID string
	workDir    string
}

// intFromAny 把 JSON 数字/字符串折成 int（失败 → 0）。
func intFromAny(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return int(n)
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n
		}
	}
	return 0
}

// recommendedValues 归一 recommended（字符串 / []any / []string → 去空 []string）。
func recommendedValues(args map[string]any) []string {
	rec, ok := args["recommended"]
	if !ok {
		return nil
	}
	switch rv := rec.(type) {
	case string:
		if rv != "" {
			return []string{rv}
		}
	case []any:
		out := make([]string, 0, len(rv))
		for _, it := range rv {
			if s, sok := it.(string); sok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return rv
	}
	return nil
}

// askAutoAnswer 是超时自动回复：有推荐值 → 单选取首个、多选取推荐值（「、」拼接）；否则统一提示。
func askAutoAnswer(args map[string]any) string {
	rec := recommendedValues(args)
	if len(rec) == 0 {
		return askAutoTimeoutAnswer
	}
	if multi, _ := args["multi"].(bool); multi {
		return strings.Join(rec, "、")
	}
	return rec[0]
}

// askTimeoutSeconds 解析本次 ask 的有效超时（秒；0 = 不超时）：
//   - 工具参数未给 timeout → 项目全局 ask_timeout_sec（缺省 0）；
//   - timeout = 0 → 用全局；
//   - timeout < 0 → 明确不超时（覆盖全局）；
//   - timeout > 0 → 覆盖全局。
func (s *Server) askTimeoutSeconds(instanceID string, args map[string]any) int {
	global := 0
	if vals := s.prjConfigValues(instanceID, askTimeoutKey); vals != nil {
		if n, err := strconv.Atoi(strings.TrimSpace(vals[askTimeoutKey])); err == nil && n > 0 {
			global = n
		}
	}
	v, ok := args["timeout"]
	if !ok {
		return global
	}
	n := intFromAny(v)
	if n == 0 {
		return global
	}
	if n < 0 {
		return 0
	}
	return n
}

// askSessionTitle 读会话标题（随 ask 下发；全局 Ask 中心免跨项目 data 取数）。失败返回空串。
func (s *Server) askSessionTitle(instanceID, dialogID, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	res, err := s.cfg.SessionGet(facade.SessionGetRequest{
		InstanceID: instanceID, DialogID: dialogID, SessionID: sessionID,
		Scope: s.cfgScope(instanceID),
	})
	if err != nil || !res.Found {
		return ""
	}
	return strings.TrimSpace(res.Session.Title)
}

// expiredReplyTarget 选择**失效 ask 转新轮**的投递实例（2026-11-04，Ask 中心）：
//   - 源会话所在窗口（`InstanceForSession`；work_session 先映射到父群聊）；
//   - 否则同 work_dir 的**顶层实例**（主窗口；Ask 中心是子实例 / 对话窗口是子实例，均排除）；
//   - 再否则回落调用方实例（旧行为，不静默失败）。
//
// 背景：Ask 中心是独立窗口（不承载会话）；超时后作答仍应回到源会话窗口续跑，不能把新轮
// 注册到 Ask 中心实例上（那会让回复/流式输出落到无 chat 面板的窗口）。
func (s *Server) expiredReplyTarget(callerInstance, session string) string {
	if s.im != nil && session != "" {
		row := s.sessionRow(callerInstance, session)
		lookup := session
		if row.Kind == facade.SessionKindWorkSession && row.ParentID != "" {
			lookup = row.ParentID
		}
		if ins, ok := s.im.InstanceForSession(lookup); ok && ins != "" {
			return ins
		}
	}
	if s.im != nil {
		wd := ""
		if info, ok := s.im.Lookup(callerInstance); ok {
			wd = info.WorkDir
		}
		for _, info := range s.im.ListTopLevel() {
			if info.ID == callerInstance {
				continue
			}
			if wd == "" || sameWorkDirPath(info.WorkDir, wd) {
				return info.ID
			}
		}
	}
	return callerInstance
}

// sameWorkDirPath 比较两个工作目录（Windows 大小写不敏感；路径归一后比较）。
func sameWorkDirPath(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

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
	s.notifyAskCenterRemove(w, "answered")
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
	// 作用域（后端唯一分发 / 每 wd 有序队列）：workdir + user 取自实例视图；workdir 缺失回落
	// 轮次请求的 work_dir（分离形态）。
	workDir, user := "", ""
	if s.im != nil {
		if info, ok := s.im.Lookup(turnInstance); ok {
			workDir, user = info.WorkDir, info.User
		}
	}
	if workDir == "" {
		if tc := s.lookupTurn(turnID); tc != nil {
			workDir = tc.req.WorkDir
		}
	}
	// 超时：工具参数优先，缺省用项目全局 ask_timeout_sec；0 = 不超时（一直等）。
	timeoutSec := s.askTimeoutSeconds(turnInstance, args)
	now := time.Now()
	var expiresAt time.Time
	if timeoutSec > 0 {
		expiresAt = now.Add(time.Duration(timeoutSec) * time.Second)
	}
	payload := map[string]any{
		"instance_id":    deliverInstance,
		"ask_id":         askID,
		"question":       askQuestion(args),
		"dialog_id":      dialogID,
		"session":        sess,
		"turn":           turnID,
		"owner_instance": turnInstance,
	}
	if title := s.askSessionTitle(turnInstance, dialogID, sess); title != "" {
		payload["session_title"] = title
	}
	if !expiresAt.IsZero() {
		payload["expires_at"] = expiresAt.UTC().Format(time.RFC3339)
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
	if rec := recommendedValues(args); len(rec) > 0 {
		payload["recommended"] = rec
	}
	// context（ask_user 的提问背景说明）：仅非空下发。
	if ctxText := strings.TrimSpace(str(args["context"])); ctxText != "" {
		payload["context"] = ctxText
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		rawPayload = []byte("{}")
	}
	s.mu.Lock()
	// 每 workdir 单调序号（有序队列）。
	s.askSeq[workDir]++
	seq := s.askSeq[workDir]
	w := &askWaiter{
		askID: askID, instanceID: turnInstance, deliverInstance: deliverInstance,
		turnID: turnID, toolID: toolID, taskID: taskID,
		dialogID:      dialogID,
		expiresAt:     expiresAt,
		ownerInstance: turnInstance,
		workDir:       workDir,
		user:          user,
		seq:           seq,
		receivedAt:    now.UnixMilli(),
		payload:       rawPayload,
		autoAnswer:    askAutoAnswer(args),
	}
	s.asks[instKey(turnInstance, askID)] = w
	if timeoutSec > 0 {
		w.timer = time.AfterFunc(time.Duration(timeoutSec)*time.Second, func() {
			s.expireAsk(askID)
		})
	}
	s.mu.Unlock()
	s.publish("ask-user", payload)
	s.pushAskCenter(w)
	return askID
}

// expireAsk 结算显式超时的 ask：取走登记 → 任务节点 done → 自动回复喂回轮次 →
// 通知 Ask 中心摘除（reason=timeout）。**不中断轮次**（引擎按普通 tool-result 续跑）。
func (s *Server) expireAsk(askID string) {
	if askID == "" {
		return
	}
	s.mu.Lock()
	w, ok := s.takeAskLocked("", askID)
	s.mu.Unlock()
	if !ok {
		return
	}
	if w.taskID != "" {
		s.tasks.done(w.taskID, TaskStateDone, w.autoAnswer, "")
	}
	s.mu.Lock()
	tc := s.lookupTurnLocked(w.instanceID, w.turnID)
	s.mu.Unlock()
	if tc != nil {
		tc.FeedToolResult(w.toolID, w.autoAnswer)
	}
	s.notifyAskCenterRemove(w, "timeout")
	logf("[chonkpilot-server] ask 超时自动回复：ask_id=%s session=%s answer=%q\n",
		w.askID, s.turnSession(w.turnID), w.autoAnswer)
}

// askCenterSubscriber 取 Ask 中心该 user 的订阅实例（workdir 作用域匹配；无 → ok=false）。
func (s *Server) askCenterSubscriber(user, workDir string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.askCenterSubs[user]
	if !ok {
		return "", false
	}
	if sub.workDir != "" && !sameWorkDirPath(sub.workDir, workDir) {
		return "", false
	}
	return sub.instanceID, true
}

// pushAskCenter 把一条新 ask 推给已登记的 Ask 中心（后端唯一分发；未订阅 → 留在队列等快照）。
func (s *Server) pushAskCenter(w *askWaiter) {
	if w == nil {
		return
	}
	inst, ok := s.askCenterSubscriber(w.user, w.workDir)
	if !ok || inst == "" {
		return
	}
	s.publish("ask-center-add", map[string]any{
		"instance_id": inst,
		"ask":         json.RawMessage(w.payload),
		"work_dir":    w.workDir,
		"received_at": w.receivedAt,
		"seq":         w.seq,
	})
}

// notifyAskCenterRemove 通知 Ask 中心摘除（reason = answered/timeout/turn_closed/instance_exit）。
func (s *Server) notifyAskCenterRemove(w *askWaiter, reason string) {
	if w == nil {
		return
	}
	inst, ok := s.askCenterSubscriber(w.user, w.workDir)
	if !ok || inst == "" {
		return
	}
	s.publish("ask-center-remove", map[string]any{
		"instance_id": inst,
		"ask_id":      w.askID,
		"reason":      reason,
	})
}

// askCenterSnapshot 返回该 user（可按 workdir 收窄）的待办快照：按 workdir + seq 升序。
func (s *Server) askCenterSnapshot(user, workDir string) []map[string]any {
	s.mu.Lock()
	items := make([]*askWaiter, 0, len(s.asks))
	for _, w := range s.asks {
		if w.user != user {
			continue
		}
		if workDir != "" && !sameWorkDirPath(w.workDir, workDir) {
			continue
		}
		items = append(items, w)
	}
	s.mu.Unlock()
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].workDir != items[j].workDir {
			return items[i].workDir < items[j].workDir
		}
		return items[i].seq < items[j].seq
	})
	out := make([]map[string]any, 0, len(items))
	for _, w := range items {
		var obj map[string]any
		if json.Unmarshal(w.payload, &obj) != nil {
			continue
		}
		obj["work_dir"] = w.workDir
		obj["received_at"] = w.receivedAt
		obj["seq"] = w.seq
		out = append(out, obj)
	}
	return out
}

// onAskCenterSubscribe Ask 中心订阅登记（user + 可选 workdir 作用域）+ 回快照。
// 同一 user 只保留一个订阅实例（后订阅覆盖），避免多端重复分发。
func (s *Server) onAskCenterSubscribe(_ context.Context, _ string, v *mq.Value) error {
	var raw map[string]any
	_ = json.Unmarshal(v.Payload, &raw)
	instanceID := str(raw["instance_id"])
	workDir := str(raw["work_dir"])
	if instanceID == "" {
		v.Result = map[string]any{"ok": false, "asks": []any{}}
		return nil
	}
	user := ""
	if s.im != nil {
		if info, ok := s.im.Lookup(instanceID); ok {
			user = info.User
			if workDir == "" && str(raw["scope"]) == "workdir" {
				workDir = info.WorkDir
			}
		}
	}
	s.mu.Lock()
	s.askCenterSubs[user] = askCenterSub{instanceID: instanceID, workDir: workDir}
	s.mu.Unlock()
	v.Result = map[string]any{"ok": true, "asks": s.askCenterSnapshot(user, workDir)}
	return nil
}

// onAskCenterUnsubscribe 退订（窗口卸载/关闭；幂等）。
func (s *Server) onAskCenterUnsubscribe(_ context.Context, _ string, v *mq.Value) error {
	var raw map[string]any
	_ = json.Unmarshal(v.Payload, &raw)
	instanceID := str(raw["instance_id"])
	if instanceID != "" {
		s.mu.Lock()
		for user, sub := range s.askCenterSubs {
			if sub.instanceID == instanceID {
				delete(s.askCenterSubs, user)
			}
		}
		s.mu.Unlock()
	}
	v.Result = map[string]any{"ok": true}
	return nil
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
