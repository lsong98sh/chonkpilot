package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
)

// askTimeoutKey 是项目级「ask_user 缺省超时（秒）」配置键。
// 0 / 缺省 / 非正 = 不超时；LLM 在工具参数显式给 timeout 时覆盖本值。
const askTimeoutKey = "ask_timeout_sec"

// askAutoTimeoutAnswer 是无推荐项时的超时自动回复文案（有 recommended 时优先取推荐值）。
const askAutoTimeoutAnswer = "用户未在时限内回复，请按推荐做法继续。"

// askAsyncHandoffResult 是 ask_user **立即返回**给 LLM 的占位工具结果（AskUser S4）：
// 提问不是阻塞调用，先快速转异步——LLM 可继续完成其它可独立完成的任务，或直接收尾本轮；
// 用户答复到达后作为**后续用户消息**投递（同一轮插入或新轮），不会回填到本次 tool result。
const askAsyncHandoffResult = "已向用户提问，正在等待回复。你可以继续完成其他可独立完成的任务；如果暂时没有其他可做的，请直接回复文本「等待用户回复」并结束本轮对话。用户答复到达后会作为后续消息送达。"

// askChatResolvedResult 是「聊一聊」后点「完成」写入 tool-result 的文案（AskUser S4）：
// 用户已在会话里讨论过，由用户显式结束该提问；**不**向 LLM 投递消息。
const askChatResolvedResult = "参考后续的聊天记录"

// askCenterSub 是 Ask 中心订阅（user 作用域；workDir 非空 = 只订该目录）。
type askCenterSub struct {
	instanceID string
	workDir    string
}

// askAnswer 是批次内一条已答问题（全部答完后按 seq 顺序一次性投递）。
type askAnswer struct {
	askID    string
	question string
	answer   string
	seq      int64
}

// askBatch 是**同一会话**的提问批次（AskUser S4 要求 5：全部回复后一次性发送）。
//   - open     = 尚未回答的提问（含跳过 / 超时未结算）；
//   - answered = 已回答但批次尚未齐（先收着，等最后一条答复到达再投递）；
//   - 最后一条答复到达 → 合并为一条 user 消息投递并删除批次；
//   - 轮次正常结束不删除（非阻塞 ask 允许先收尾）；取消 / 实例退出整批丢弃。
type askBatch struct {
	key      string
	open     map[string]*askWaiter
	answered map[string]askAnswer
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

// onAskUserReply ask 应答 → 记入会话批次；同会话全部答完后作为**用户消息**一次性投递
// （AskUser S4：提问是快速转异步，答复不占用原 tool-result 配对）+ ask 任务节点 → tasks.done。
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
	// AskUser S4：直接回答同样就地更新 tool-result（历史/重载可见）；答复本身按批次投递。
	s.updateAskToolResult(w, answer)
	s.notifyAskCenterRemove(w, "answered")
	// 同会话多问：先收着；最后一条答复到达才一次性投递（要求 5）。
	if text, ready := s.recordAskAnswer(w, answer); ready {
		s.deliverAskAnswers(w, text)
	}
	return nil
}

// recordAskAnswer 把一条答复记入会话批次；返回 (可投递文本, 是否已全部答完)。
// 批次缺失（旧登记 / 测试直插 s.asks）→ 视为单问批次立即投递。
func (s *Server) recordAskAnswer(w *askWaiter, answer string) (string, bool) {
	if w == nil {
		return "", false
	}
	s.mu.Lock()
	b := s.askBatches[w.batchKey]
	if b == nil {
		s.mu.Unlock()
		return formatAskAnswers([]askAnswer{{
			askID: w.askID, question: w.question, answer: answer, seq: w.seq,
		}}), true
	}
	delete(b.open, w.askID)
	b.answered[w.askID] = askAnswer{
		askID: w.askID, question: w.question, answer: answer, seq: w.seq,
	}
	if len(b.open) > 0 {
		s.mu.Unlock()
		return "", false
	}
	answers := make([]askAnswer, 0, len(b.answered))
	for _, a := range b.answered {
		answers = append(answers, a)
	}
	delete(s.askBatches, b.key)
	s.mu.Unlock()
	sort.SliceStable(answers, func(i, j int) bool { return answers[i].seq < answers[j].seq })
	return formatAskAnswers(answers), true
}

// formatAskAnswers 把同会话的问答对合成一条用户消息（LLM 侧可明确对应问题与答复）。
func formatAskAnswers(answers []askAnswer) string {
	if len(answers) == 0 {
		return ""
	}
	if len(answers) == 1 {
		return "【用户回答】\n问：" + answers[0].question + "\n答：" + answers[0].answer
	}
	var b strings.Builder
	b.WriteString("【用户回答】")
	for i, a := range answers {
		fmt.Fprintf(&b, "\n%d. 问：%s\n   答：%s", i+1, a.question, a.answer)
	}
	return b.String()
}

// onAskResolve「聊一聊」后点「完成」：解除提问、**就地更新 tool-result**、任务节点 done、
// 摘除 Ask 中心；不向 LLM 投递任何消息（用户已通过会话讨论）。
//
// 语义（AskUser S4）：
//   - 只改写历史记录（原 `ask_user` 行的 result 文案），不回填当前 LLM 轮；
//   - 若该 ask 是同会话批次里最后一个未答项 → 暂存的**真实答复**按原口径一次性投递；
//     「参考后续的聊天记录」本身不进入答复集合；
//   - 幂等：已解除 / 不存在的 ask → ok:true。
func (s *Server) onAskResolve(_ context.Context, _ string, v *mq.Value) error {
	var raw map[string]any
	_ = json.Unmarshal(v.Payload, &raw)
	askID := str(raw["ask_id"])
	if askID == "" {
		v.Result = map[string]any{"ok": false, "error": "ask_id is required"}
		return nil
	}
	s.mu.Lock()
	w, ok := s.takeAskLocked(str(raw["instance_id"]), askID)
	var text string
	var ready bool
	if ok {
		text, ready = s.resolveAskBatchLocked(w)
	}
	s.mu.Unlock()
	if !ok {
		v.Result = map[string]any{"ok": true} // 幂等：已解除 / 已不存在
		return nil
	}
	s.updateAskToolResult(w, askChatResolvedResult)
	if w.taskID != "" {
		s.tasks.done(w.taskID, TaskStateDone, askChatResolvedResult, "")
	}
	s.notifyAskCenterRemove(w, "resolved")
	if ready {
		s.deliverAskAnswers(w, text)
	}
	v.Result = map[string]any{"ok": true}
	return nil
}

// resolveAskBatchLocked 从会话批次摘除一条 ask（「完成」语义：**不写入 answered**）。
// 批次没有未答项且存在真实答复暂存 → 返回合并文本（ready=true）；否则空文本。
// **须持 s.mu**。
func (s *Server) resolveAskBatchLocked(w *askWaiter) (string, bool) {
	if w == nil || w.batchKey == "" {
		return "", false
	}
	b := s.askBatches[w.batchKey]
	if b == nil {
		return "", false
	}
	delete(b.open, w.askID)
	if len(b.open) > 0 {
		return "", false
	}
	answers := make([]askAnswer, 0, len(b.answered))
	for _, a := range b.answered {
		answers = append(answers, a)
	}
	delete(s.askBatches, b.key)
	if len(answers) == 0 {
		return "", false
	}
	sort.SliceStable(answers, func(i, j int) bool { return answers[i].seq < answers[j].seq })
	return formatAskAnswers(answers), true
}

// updateAskToolResult 就地更新已收口的 ask_user tool 行 result 文案（AskUser S4）：
// 同一 turn + tool_call_id 的既有行由 data 面按 `(turn_id, tool_call_id)` 反查复用
// （不新增行）；`tool_call_status` 恒 completed。**不向 LLM 投递**（仅历史/重载可见）。
func (s *Server) updateAskToolResult(w *askWaiter, text string) {
	if w == nil || w.turnID == "" || w.toolID == "" {
		return
	}
	content := persist.ToolContent{
		Call:   &persist.ToolCallContent{ToolCallID: w.toolID, Name: "ask_user", Arguments: w.args},
		Result: &persist.ToolResultContent{Content: text, Status: persist.ToolStatusCompleted},
	}
	b, err := json.Marshal(content)
	if err != nil {
		return
	}
	msg := map[string]any{
		"role":             "tool",
		"content":          string(b),
		"tool_call_id":     w.toolID,
		"tool_call_status": persist.ToolStatusCompleted,
		"ask_id":           w.askID,
	}
	if meta := s.gc.ToolMeta("ask_user"); len(meta) > 0 {
		msg["_meta"] = meta
	}
	if _, err := s.newSessionStore(w.instanceID).AppendMsgMap(w.turnID, msg, ""); err != nil {
		logf("[chonkpilot-server] ask tool-result 更新失败（turn=%s tool_call_id=%s）: %v\n",
			w.turnID, w.toolID, err)
	}
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
	question := askQuestion(args)
	payload := map[string]any{
		"instance_id":    deliverInstance,
		"ask_id":         askID,
		"question":       question,
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
	// 会话批次（要求 5）：同会话的多个提问共用一个批次；会话未知 → 以 ask 唯一键自成一档。
	batchKey := instKey(turnInstance, sess)
	if sess == "" {
		batchKey = instKey(turnInstance, askID)
	}
	b := s.askBatches[batchKey]
	if b == nil {
		b = &askBatch{
			key:      batchKey,
			open:     make(map[string]*askWaiter),
			answered: make(map[string]askAnswer),
		}
		s.askBatches[batchKey] = b
	}
	w := &askWaiter{
		askID: askID, instanceID: turnInstance, deliverInstance: deliverInstance,
		turnID: turnID, toolID: toolID, taskID: taskID,
		sessionID: sess, batchKey: batchKey, question: question, args: args,
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
	b.open[askID] = w
	s.asks[instKey(turnInstance, askID)] = w
	if timeoutSec > 0 {
		w.timer = time.AfterFunc(time.Duration(timeoutSec)*time.Second, func() {
			s.postAskExpired(askID)
		})
	}
	s.mu.Unlock()
	s.publish("ask-user", payload)
	s.pushAskCenter(w)
	return askID
}

// postAskExpired 投递一条「ask 到点」消息（定时器 goroutine 只投递，不直接改状态）。
// worker 由首次投递惰性启动：串行调用 expireAsk，保证 ask 状态只由一个执行体写。
func (s *Server) postAskExpired(askID string) {
	s.askExpireOnce.Do(func() {
		go func() {
			for {
				select {
				case id := <-s.askExpireCh:
					s.expireAsk(id)
				case <-s.askExpireStop:
					return
				}
			}
		}()
	})
	select {
	case s.askExpireCh <- askID:
	case <-s.askExpireStop:
	}
}

// expireAsk 结算显式超时的 ask：取走登记 → 任务节点 done → 超时答复记入会话批次
// （同批全部答完则一次性投递）→ 通知 Ask 中心摘除（reason=timeout）。
// 与用户作答同一条路径：答复是**后续用户消息**，不再回填本次 tool-result。
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
	s.updateAskToolResult(w, w.autoAnswer)
	s.notifyAskCenterRemove(w, "timeout")
	if text, ready := s.recordAskAnswer(w, w.autoAnswer); ready {
		s.deliverAskAnswers(w, text)
	}
	logf("[chonkpilot-server] ask 超时自动回复：ask_id=%s session=%s answer=%q\n",
		w.askID, w.sessionID, w.autoAnswer)
}

// deliverAskAnswers 把「同会话全部问答」投递回 LLM（要求 4/5：结果传给 LLM，且一次只发一条）：
//   - 主会话：写持久化 inbox（**next_turn**）——有运行轮则等本轮结束自动消费；
//     无运行轮立即起新一轮。进程重启 / 相位错过都不丢；
//   - 群聊 / 子会话（kind 非空）：沿用既有口径——有运行轮直接喂入，否则起新一轮
//     （这些会话由各自调度器驱动，不经 consumeNextTurn）。
func (s *Server) deliverAskAnswers(w *askWaiter, text string) {
	if w == nil || strings.TrimSpace(text) == "" {
		return
	}
	if w.sessionID == "" {
		logf("[chonkpilot-server] ask 答复无法投递（缺会话标识）: ask_id=%s\n", w.askID)
		return
	}
	inst := s.askAnswerInstance(w)
	if inst == "" {
		logf("[chonkpilot-server] ask 答复无法投递（缺实例归属）: ask_id=%s session=%s\n", w.askID, w.sessionID)
		return
	}
	// 非主会话：有在飞轮次 → 直接喂入；否则起新轮（与 replyExpiredAsk 同口径）。
	if kind := s.sessionRow(inst, w.sessionID).Kind; kind != "" {
		if tc := s.turnBySessionForAdd(inst, w.sessionID); tc != nil {
			s.sendTextToTurn(inst, w.sessionID, tc.req.Turn, text)
			return
		}
		s.startAskAnswerTurn(inst, w.sessionID, text)
		return
	}
	// 主会话：持久化 inbox（next_turn）——有运行轮时等其正常结束再起新一轮；
	// 不走 insert（未消费的 insert 项会挡住 consumeNextTurn，答复可能滞留）。
	payload, err := json.Marshal(map[string]any{
		"instance_id": inst,
		"session":     w.sessionID,
		"content":     text,
	})
	if err != nil {
		logf("[chonkpilot-server] ask 答复序列化失败: ask_id=%s err=%v\n", w.askID, err)
		return
	}
	if err := s.onInboxAdd(context.Background(), "", &mq.Value{Payload: payload}); err != nil {
		logf("[chonkpilot-server] ask 答复投递失败: ask_id=%s session=%s err=%v\n", w.askID, w.sessionID, err)
	}
}

// askAnswerInstance 选择答复投递实例：源会话所在窗口 → 发起 turn 的实例。
func (s *Server) askAnswerInstance(w *askWaiter) string {
	if s.im != nil && w.sessionID != "" {
		if ins, ok := s.im.InstanceForSession(w.sessionID); ok && ins != "" {
			return ins
		}
	}
	if w.instanceID != "" {
		return w.instanceID
	}
	return w.ownerInstance
}

// sendTextToTurn 向已存在的运行轮投递一条用户文本（群聊 / 子会话答复路径）。
func (s *Server) sendTextToTurn(inst, sess, turn, text string) {
	body, err := json.Marshal(SendReq{
		InstanceID: inst, Session: sess, Turn: turn, Type: SendTypeTextUser, Content: text,
	})
	if err != nil {
		return
	}
	if err := s.onLLMSend(context.Background(), "", &mq.Value{Payload: body}); err != nil {
		logf("[chonkpilot-server] ask 答复喂入运行轮失败: session=%s turn=%s err=%v\n", sess, turn, err)
	}
}

// startAskAnswerTurn 在无运行轮时以答复起新一轮（群聊 / 子会话路径）。
func (s *Server) startAskAnswerTurn(inst, sess, text string) {
	res := s.acceptStart(StartReq{InstanceID: inst, Session: sess, Turn: "turn-" + newID()})
	if ok, _ := res["accepted"].(bool); !ok {
		logf("[chonkpilot-server] ask 答复起新轮被拒: session=%s target=%s res=%v\n", sess, inst, res)
		return
	}
	turnID, _ := res["turn"].(string)
	s.sendTextToTurn(inst, sess, turnID, text)
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
