// server 服务多 workdir：A3 总线化后 sessionStore 不再直连 prj 库——会话写/读原语经总线
// persist 数据面（chonkpilot-data/persist，61-消息一览 §3.2a/3.2b），请求带 instance_id，
// persist 按实例定位 prj 主库应答。数据访问一律经总线（40-演进计划 目标态）。
package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
)

// sessionStore 封装会话数据读写（总线 persist 面）。
type sessionStore struct {
	bus        mq.Bus
	instanceID string
	idx        *sessionIndex
}

// sessionIndex 是进程内 (session→dialog, turn→session) 备忘（一树一库 P3-4b）。
//
// 来源 = `data-session-ensure-session` 回带的 `dialog_id` 与本地建轮次；进程重启后为空，
// 由 `llm-start` 的 EnsureSession 重新回填。**不是权威数据**：仅用于给后续总线请求补
// `dialog_id`（持久化真相在 persist 侧）。
type sessionIndex struct {
	mu              sync.RWMutex
	dialogBySession map[string]string
	sessionByTurn   map[string]string
}

// sessionIndexes 按「总线实例 + instance_id」隔离（不同测试/宿主的总线互不串味）。
var sessionIndexes sync.Map // key: "<bus %p>|<instance_id>" → *sessionIndex

// sharedIndex 取（或建）该总线+实例的共享备忘。
func sharedIndex(bus mq.Bus, instanceID string) *sessionIndex {
	key := fmt.Sprintf("%p|%s", bus, instanceID)
	if v, ok := sessionIndexes.Load(key); ok {
		return v.(*sessionIndex)
	}
	idx := &sessionIndex{
		dialogBySession: map[string]string{},
		sessionByTurn:   map[string]string{},
	}
	actual, _ := sessionIndexes.LoadOrStore(key, idx)
	return actual.(*sessionIndex)
}

// flushSessionIndexes 清掉某总线的全部备忘（`Server.Stop` 调用）。
//
// 必要性：key 含总线指针 `%p`，总线被回收后指针可能被**后续测试/宿主复用** → 旧
// session→dialog 映射残留，一树一库生效时会指向已不存在的库（`对话库不存在`）。Stop 时
// 清空即消除该串味；运行期真删除仍由下方「失配自愈」兜底。
func flushSessionIndexes(bus mq.Bus) {
	if bus == nil {
		return
	}
	prefix := fmt.Sprintf("%p|", bus)
	sessionIndexes.Range(func(k, _ any) bool {
		if key, ok := k.(string); ok && strings.HasPrefix(key, prefix) {
			sessionIndexes.Delete(key)
		}
		return true
	})
}

// flushInstanceIndex 清掉某实例的备忘（`instance-exit` 调用）：长驻服务端下按实例回收，
// 避免 session/turn 映射随实例累积（B-38 同类泄漏）。
func flushInstanceIndex(bus mq.Bus, instanceID string) {
	if bus == nil || instanceID == "" {
		return
	}
	sessionIndexes.Delete(fmt.Sprintf("%p|%s", bus, instanceID))
}

// Prewarm 从 `data-session-list` 回填该实例的 session→dialog 备忘。
//
// 进程重启后备忘为空（见 sessionIndex 注释），而群聊「继续/结束」、子聊天完成、主会话判定
// 都不会经过 `llm-start` 的 EnsureSession → 索引长期缺失 → 这些入口拿不到 dialog_id。
// 实例注册后后台预热一次（61 会话列表已回带 dialog_id，零新增主题）。
func (st *sessionStore) Prewarm() {
	res, err := dataRequest(st.bus, msgkeys.TopicDataSessionList, map[string]any{
		"instance_id": st.instanceID, "data": map[string]any{},
	})
	if err != nil {
		return
	}
	list, _ := res["list"].([]any)
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		st.idx.setDialog(str(m["id"]), str(m["dialog_id"]))
	}
}

// ensureDialogIndex 返回会话所属 dialog_id；备忘缺失时用一次会话列表回填（重启后自愈）。
func (s *Server) ensureDialogIndex(instanceID, sessionID string) string {
	if dlg := s.dialogOf(instanceID, sessionID); dlg != "" {
		return dlg
	}
	if s == nil || s.bus == nil || sessionID == "" {
		return ""
	}
	newSessionStore(s.bus, instanceID).Prewarm()
	return s.dialogOf(instanceID, sessionID)
}

// dialogForWrite 返回会话 dialog_id；备忘缺失且会话行不存在时，幂等 ensure-session 自举对话库
// 后重取。用于「发送 / 落消息」这类**写**入口（读入口缺 dialog 应按空处理，不建行）。
func (s *Server) dialogForWrite(instanceID, sessionID string) string {
	if dlg := s.ensureDialogIndex(instanceID, sessionID); dlg != "" {
		return dlg
	}
	if s == nil || s.bus == nil || sessionID == "" {
		return ""
	}
	if err := newSessionStore(s.bus, instanceID).EnsureSession(sessionID); err != nil {
		return ""
	}
	return s.ensureDialogIndex(instanceID, sessionID)
}

func (x *sessionIndex) setDialog(sessionID, dialogID string) {
	if sessionID == "" || dialogID == "" {
		return
	}
	x.mu.Lock()
	x.dialogBySession[sessionID] = dialogID
	x.mu.Unlock()
}

func (x *sessionIndex) dialogOf(sessionID string) string {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.dialogBySession[sessionID]
}

func (x *sessionIndex) setTurnSession(turnID, sessionID string) {
	if turnID == "" || sessionID == "" {
		return
	}
	x.mu.Lock()
	x.sessionByTurn[turnID] = sessionID
	x.mu.Unlock()
}

func (x *sessionIndex) sessionOfTurn(turnID string) string {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.sessionByTurn[turnID]
}

// newSessionStore 构造会话数据客户端（bus 由宿主注入；persist 应答 data-* 面）。
func newSessionStore(bus mq.Bus, instanceID string) *sessionStore {
	return &sessionStore{bus: bus, instanceID: instanceID, idx: sharedIndex(bus, instanceID)}
}

// req 发 data-session-<action> 请求（带 instance_id），返回 result。
//
// 一树一库（P3-4b）：已知该会话/轮次的 `dialog_id` 时**自动补齐**（载荷本就显式带值时以显式为准）。
func (st *sessionStore) req(action string, data map[string]any) (map[string]any, error) {
	st.injectDialog(data)
	return dataRequest(st.bus, "data-session-"+action, map[string]any{
		"instance_id": st.instanceID, "data": data,
	})
}

// injectDialog 按 session_id / turn_id 补 `dialog_id`（两者都缺 / 未知 → 不写键；一树一库下由调用方显式带或走自举）。
func (st *sessionStore) injectDialog(data map[string]any) {
	if data == nil || st.idx == nil {
		return
	}
	if _, has := data["dialog_id"]; has {
		return
	}
	sessionID := str(data["session_id"])
	if sessionID == "" {
		sessionID = st.idx.sessionOfTurn(str(data["turn_id"]))
	}
	if dlg := st.idx.dialogOf(sessionID); dlg != "" {
		data["dialog_id"] = dlg
	}
}

// EnsureSession 幂等建会话（已存在不覆盖）。主会话（无父）。
func (st *sessionStore) EnsureSession(sessionID string) error {
	return st.EnsureSessionWithParent(sessionID, "")
}

// EnsureSessionWithParent 幂等建会话；parentSessionID 非空 = 子会话（落 sessions.parent_id，
// 创建事件 session-new 带 parent_session_id）。会话行首次落库时由 persist 广播事件，
// 幂等由 persist 的存在性检查保证（同 id 复用不重复广播）。
//
// 一树一库（P3-4b）：回带 `dialog_id` 记入备忘；子会话沿用父会话的对话（同一棵树）。
func (st *sessionStore) EnsureSessionWithParent(sessionID, parentSessionID string) error {
	data := map[string]any{"session_id": sessionID}
	if parentSessionID != "" {
		data["parent_session_id"] = parentSessionID
	}
	if dlg := st.dialogForSessionOrParent(sessionID, parentSessionID); dlg != "" {
		data["dialog_id"] = dlg
	}
	res, err := st.req("ensure-session", data)
	if err != nil {
		// 失配自愈：备忘里的对话已被删除 / 属另一份数据根（测试串味、用户删对话）→
		// 丢掉旧映射后**重试一次**（不带 dialog_id：根会话走自举；子会话因对话已删 → `dialog_required`）。
		if strings.Contains(err.Error(), "对话库不存在") {
			st.idx.forget(sessionID)
			delete(data, "dialog_id")
			if res, err = st.req("ensure-session", data); err != nil {
				return err
			}
		} else {
			return err
		}
	}
	st.idx.setDialog(sessionID, str(res["dialog_id"]))
	return nil
}

// forget 丢弃某会话的 dialog 映射（失配自愈用）。
func (x *sessionIndex) forget(sessionID string) {
	if x == nil || sessionID == "" {
		return
	}
	x.mu.Lock()
	delete(x.dialogBySession, sessionID)
	x.mu.Unlock()
}

// dialogForSessionOrParent 取会话自身或父会话的 dialog_id（父会话 = 同一棵树）。
func (st *sessionStore) dialogForSessionOrParent(sessionID, parentSessionID string) string {
	if st.idx == nil {
		return ""
	}
	if dlg := st.idx.dialogOf(sessionID); dlg != "" {
		return dlg
	}
	if parentSessionID != "" {
		return st.idx.dialogOf(parentSessionID)
	}
	return ""
}

// dialogOf 返回该实例下某会话的 dialog_id（进程内备忘；未知 → ""）。
// 供非 sessionStore 的总线请求（inbox / 任务树等）补 `dialog_id`。
func (s *Server) dialogOf(instanceID, sessionID string) string {
	if s == nil || s.bus == nil || sessionID == "" {
		return ""
	}
	if dlg := sharedIndex(s.bus, instanceID).dialogOf(sessionID); dlg != "" {
		return dlg
	}
	// 跨实例回落：群聊 / 子聊天的后续写入可能以**驱动窗口**（另一 instance）发起，而
	// session→dialog 映射只登记在**建会话那个实例**的备忘里（dialog_id 全局唯一，按 id 复用安全）。
	return lookupDialogAnyInstance(s.bus, sessionID)
}

// lookupDialogAnyInstance 跨实例查 session→dialog 备忘（同一总线；dialog_id 全实例唯一）。
func lookupDialogAnyInstance(bus mq.Bus, sessionID string) string {
	prefix := fmt.Sprintf("%p|", bus)
	found := ""
	sessionIndexes.Range(func(k, v any) bool {
		key, ok := k.(string)
		if !ok || !strings.HasPrefix(key, prefix) {
			return true
		}
		if dlg := v.(*sessionIndex).dialogOf(sessionID); dlg != "" {
			found = dlg
			return false
		}
		return true
	})
	return found
}

// rememberDialog 记录「会话 → 对话」映射。
//
// 用于 `cfg.SessionEnsure` 直建会话（子聊天 / 群聊）后回填备忘：该路径不经 `sessionStore`，
// 无法自动从应答里学 `dialog_id`；不记则后续 turn/message 写入缺 `dialog_id` 被拒。
func (s *Server) rememberDialog(instanceID, sessionID, dialogID string) {
	if s == nil || s.bus == nil || sessionID == "" || dialogID == "" {
		return
	}
	sharedIndex(s.bus, instanceID).setDialog(sessionID, dialogID)
}

// EnsureTurn 幂等建轮次（已存在复用）。
func (st *sessionStore) EnsureTurn(turnID, sessionID string) error {
	if _, err := st.req("ensure-turn", map[string]any{"turn_id": turnID, "session_id": sessionID}); err != nil {
		return err
	}
	st.idx.setTurnSession(turnID, sessionID)
	return nil
}

// EnsureTurnInDialog 幂等建轮次并**显式指定 `dialog_id`**（定时任务用，SANDBOX-WRITE-1 P4）。
//
// 与 EnsureTurn 的差别只在 `dialog_id` 来源：定时任务由插件显式带（宿主进程重启后本进程内的
// session→dialog 备忘为空，不能依赖注入）；成功后同样登记 turn→session 备忘。
func (st *sessionStore) EnsureTurnInDialog(turnID, sessionID, dialogID string) error {
	data := map[string]any{"turn_id": turnID, "session_id": sessionID}
	if dialogID != "" {
		data["dialog_id"] = dialogID
	}
	if _, err := st.req("ensure-turn", data); err != nil {
		return err
	}
	st.idx.setTurnSession(turnID, sessionID)
	return nil
}

// ReopenTurn 复用既有轮次：把 turn 行状态置回 running 并清空 finish_reason
// （同轮次继续 llm-start{continue:true} 用；复用 data-session-complete-turn 的 upsert
// 语义，不新增消息面）。原 finished/interrupted 记录随之复位，不留脏状态。
func (st *sessionStore) ReopenTurn(turnID string) error {
	_, err := st.req("complete-turn", map[string]any{
		"turn_id": turnID, "status": "running", "finish_reason": "",
	})
	return err
}

// LatestTurnID 取会话最近一轮 turn id（同轮次继续未带 turn 时的后端回退）：
// 经 data-session-history 的 turns 列表（created_at 升序）末项。无轮次返回 ""。
func (st *sessionStore) LatestTurnID(sessionID string) string {
	res, err := st.req("history", map[string]any{"session_id": sessionID})
	if err != nil {
		logf("[chonkpilot-server] LatestTurnID(%s): %v\n", sessionID, err) // B-06：不吞错（返回值语义不变）
		return ""
	}
	msgs, _ := res["messages"].(map[string]any)
	turns, _ := msgs["turns"].([]any)
	if len(turns) == 0 {
		return ""
	}
	last, _ := turns[len(turns)-1].(map[string]any)
	turnID := str(last["turn_id"])
	st.idx.setTurnSession(turnID, sessionID)
	return turnID
}

// AppendFullKeyed 落一条完整消息（含 tool_calls / kind，LLM 协议重放需要；persist 生成 m-<id>，
// created_at 固定 9 位纳秒 RFC3339 保证同轮顺序稳定，对齐 persist 实现），另指定回填主键
// （key 非空 = 就地更新该行；空 = 新键）；返回落库主键（回填时回传为 key）。
// 用于 assistant 增量落库（同段落库到同一行）。
func (st *sessionStore) AppendFullKeyed(turnID string, m ChatMsg, key string) (string, error) {
	var msg map[string]any
	if b, err := json.Marshal(ChatMessageToData(m)); err == nil { // M2：chat 自持类型 → 快照 wire 形态
		_ = json.Unmarshal(b, &msg)
	}
	return st.appendMsg(turnID, msg, key)
}

// AppendMsgMap 落一条消息（msg 为完整 map，可含 persist 扩展字段 brief/tool_call_status/
// session_id；用于 role=tool 结果——其 content 为 {call,result,async} JSON，无法经 ChatMsg
// 序列化携带 tool_call_status）。key 非空 = 就地更新该行（running → 终态回填）；返回落库主键。
func (st *sessionStore) AppendMsgMap(turnID string, msg map[string]any, key string) (string, error) {
	return st.appendMsg(turnID, msg, key)
}

// appendMsg 发 data-session-append-message（key 非空才带 key 字段，保持旧调用载荷逐字节等价）。
func (st *sessionStore) appendMsg(turnID string, msg map[string]any, key string) (string, error) {
	payload := map[string]any{"turn_id": turnID, "msg": msg}
	if key != "" {
		payload["key"] = key
	}
	res, err := st.req("append-message", payload)
	if err != nil {
		return "", err
	}
	return str(res["id"]), nil
}

// LoadMessages 读该 turn 全部消息（created_at 升序；LLM 会话重建）。
func (st *sessionStore) LoadMessages(turnID string) []ChatMsg {
	res, err := st.req("load-messages", map[string]any{"turn_id": turnID})
	if err != nil {
		logf("[chonkpilot-server] LoadMessages(%s): %v\n", turnID, err) // B-06：不吞错（返回值语义不变）
		return nil
	}
	var rawMsgs []data.ChatMsg
	if raw, ok := res["messages"].([]any); ok {
		if b, err := json.Marshal(raw); err == nil {
			_ = json.Unmarshal(b, &rawMsgs)
		}
	}
	return chatMessagesFromData(rawMsgs)
}

// LoadToolContent 按 tool_call_id 读该 tool 消息的 content **原文**（{call,result,async} JSON 字符串；
// 2026-09-18 用户决定：异步结果读 message 表）。经 data-session-content 的 key
// `tool_result:<tool_call_id>`（既有方法扩参：新增 key 形态，payload 与返回形状不变）。
// 未落库 / 已被清理 → 第二个返回 false（调用方据此给明确文案，不静默返回空）。
func (st *sessionStore) LoadToolContent(sessionID, toolCallID string) (string, bool) {
	if sessionID == "" || toolCallID == "" {
		return "", false
	}
	key := "tool_result:" + toolCallID
	res, err := st.req("content", map[string]any{"session_id": sessionID, "keys": []string{key}})
	if err != nil {
		return "", false
	}
	contents, _ := res["contents"].(map[string]any)
	s, ok := contents[key].(string)
	return s, ok && s != ""
}

// BuildContextTokens 组装会话历史上下文（data-session-context，61-消息一览 §3.2a）：
// includeSnapshot=true 且库快照 History 非空 → 快照前缀 + snapshot_turn 之后 turns 消息
// （跳过 interrupted/exclude_turn）；否则全量历史（无快照自动回退）。另回传 `data-session-context`
// 结果载荷里**只增**的 `turn_tokens` 伴随数组（P3，2026-09-25）：每项 {turn_id, full, brief}
// （升序、排除 exclude_turn）。消费方（组装侧三段定位）据此**直接取预存 token**（免重复实时估算）；
// 缺值/缺该键 → 返回 nil，调用方回退实时估算（见 data.ResolveStoredTokens）。
func (st *sessionStore) BuildContextTokens(sessionID, excludeTurn string, includeSnapshot bool) ([]ChatMsg, []facade.TurnToken) {
	reqData := map[string]any{"session_id": sessionID, "exclude_turn": excludeTurn}
	if includeSnapshot {
		reqData["include_snapshot"] = "true"
	}
	res, err := st.req("context", reqData)
	if err != nil {
		logf("[chonkpilot-server] BuildContextTokens(%s): %v\n", sessionID, err) // B-06：不吞错（返回值语义不变）
		return nil, nil
	}
	var rawMsgs []data.ChatMsg
	if raw, ok := res["messages"].([]any); ok {
		if b, err := json.Marshal(raw); err == nil {
			_ = json.Unmarshal(b, &rawMsgs)
		}
	}
	var tokens []facade.TurnToken
	if raw, ok := res["turn_tokens"].([]any); ok {
		if b, err := json.Marshal(raw); err == nil {
			_ = json.Unmarshal(b, &tokens)
		}
	}
	return chatMessagesFromData(rawMsgs), tokens
}

// CompleteTurnTokens 结束轮次（写 status/finish_reason；保留原字段）并**预存该轮 token 数**
// （P3；full/brief 为 nil = 不写该键）。预存值经 data-session-complete-turn 落 turns 行，
// 供后续判定/拼接**直接累加、免重复估算**；缺值（历史轮无该字段）由消费方回退实时估算
// （绝不把缺值当 0）。
func (st *sessionStore) CompleteTurnTokens(turnID, status, finishReason string, fullTokens, briefTokens *int) error {
	data := map[string]any{
		"turn_id": turnID, "status": status, "finish_reason": finishReason,
	}
	if fullTokens != nil {
		data["full_tokens"] = *fullTokens
	}
	if briefTokens != nil {
		data["brief_tokens"] = *briefTokens
	}
	_, err := st.req("complete-turn", data)
	return err
}

// CompleteTurnInDialog 结束轮次并**显式指定 `dialog_id`**（定时任务用，SANDBOX-WRITE-1 P4）：
// 同 CompleteTurnTokens 的语义，只是不依赖进程内 turn→session 备忘补 `dialog_id`。
func (st *sessionStore) CompleteTurnInDialog(turnID, dialogID, status, finishReason string) error {
	data := map[string]any{"turn_id": turnID, "status": status, "finish_reason": finishReason}
	if dialogID != "" {
		data["dialog_id"] = dialogID
	}
	_, err := st.req("complete-turn", data)
	return err
}

// CleanupStaleTurns 启动/首次使用清理：遗留 running 的 turn 标 interrupted
// （进程崩溃/重启后防同 session 双开）。
func (st *sessionStore) CleanupStaleTurns() error {
	_, err := st.req("cleanup-stale", map[string]any{})
	return err
}

// CleanupTurnEvents 清理终态 turn 的事件日志（MOD-1 M3；retentionDays=0 = 永久，<0 = 不清理）。
// 仅删除终态且超过保留期的 turn；running 的事件永不按时间删。失败由调用方告警、不阻断。
func (st *sessionStore) CleanupTurnEvents(retentionDays int) error {
	_, err := dataRequest(st.bus, msgkeys.TopicDataTurnEventCleanup, map[string]any{
		"instance_id": st.instanceID,
		"data":        map[string]any{"retention_days": retentionDays},
	})
	return err
}

// SetSnapshot 写会话历史快照（data-snapshot-set，§3.2b；唯一终态 llm-complete 写，
// snapshot_turn = 覆盖到最后一条 turn）。
func (st *sessionStore) SetSnapshot(sessionID string, history []ChatMsg, snapshotTurn string) error {
	payload := map[string]any{
		"session_id": sessionID,
		"snapshot":   map[string]any{"history": dataMessagesFromChat(history), "snapshot_turn": snapshotTurn},
	}
	st.injectDialog(payload) // 一树一库：快照与轮次/消息同库
	_, err := dataRequest(st.bus, msgkeys.TopicDataSnapshotSet, map[string]any{
		"instance_id": st.instanceID,
		"data":        payload,
	})
	return err
}
