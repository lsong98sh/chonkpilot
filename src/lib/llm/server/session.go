// server 服务多 workdir：A3 总线化后 sessionStore 不再直连 prj 库——会话写/读原语经总线
// persist 数据面（chonkpilot-data/persist，61-消息一览 §3.2a/3.2b），请求带 instance_id，
// persist 按实例定位 prj 主库应答。数据访问一律经总线（40-演进计划 目标态）。
package server

import (
	"encoding/json"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// sessionStore 封装会话数据读写（总线 persist 面）。
type sessionStore struct {
	bus        mq.Bus
	instanceID string
}

// newSessionStore 构造会话数据客户端（bus 由宿主注入；persist 应答 data-* 面）。
func newSessionStore(bus mq.Bus, instanceID string) *sessionStore {
	return &sessionStore{bus: bus, instanceID: instanceID}
}

// req 发 data-session-<action> 请求（带 instance_id），返回 result。
func (st *sessionStore) req(action string, data map[string]any) (map[string]any, error) {
	return dataRequest(st.bus, "data-session-"+action, map[string]any{
		"instance_id": st.instanceID, "data": data,
	})
}

// EnsureSession 幂等建会话（已存在不覆盖）。主会话（无父）。
func (st *sessionStore) EnsureSession(sessionID string) error {
	return st.EnsureSessionWithParent(sessionID, "")
}

// EnsureSessionWithParent 幂等建会话；parentSessionID 非空 = 子会话（落 sessions.parent_id，
// 创建事件 session-new 带 parent_session_id）。会话行首次落库时由 persist 广播事件，
// 幂等由 persist 的存在性检查保证（同 id 复用不重复广播）。
func (st *sessionStore) EnsureSessionWithParent(sessionID, parentSessionID string) error {
	data := map[string]any{"session_id": sessionID}
	if parentSessionID != "" {
		data["parent_session_id"] = parentSessionID
	}
	_, err := st.req("ensure-session", data)
	return err
}

// EnsureTurn 幂等建轮次（已存在复用）。
func (st *sessionStore) EnsureTurn(turnID, sessionID string) error {
	_, err := st.req("ensure-turn", map[string]any{"turn_id": turnID, "session_id": sessionID})
	return err
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
		return ""
	}
	msgs, _ := res["messages"].(map[string]any)
	turns, _ := msgs["turns"].([]any)
	if len(turns) == 0 {
		return ""
	}
	last, _ := turns[len(turns)-1].(map[string]any)
	return str(last["turn_id"])
}

// AppendFullKeyed 落一条完整消息（含 tool_calls / kind，LLM 协议重放需要；persist 生成 m-<id>，
// created_at 固定 9 位纳秒 RFC3339 保证同轮顺序稳定，对齐 persist 实现），另指定回填主键
// （key 非空 = 就地更新该行；空 = 新键）；返回落库主键（回填时回传为 key）。
// 用于 assistant 增量落库（同段落库到同一行）。
func (st *sessionStore) AppendFullKeyed(turnID string, m ChatMsg, key string) (string, error) {
	var msg map[string]any
	if b, err := json.Marshal(m); err == nil {
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
		return nil
	}
	var msgs []ChatMsg
	if raw, ok := res["messages"].([]any); ok {
		if b, err := json.Marshal(raw); err == nil {
			_ = json.Unmarshal(b, &msgs)
		}
	}
	return msgs
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
	data := map[string]any{"session_id": sessionID, "exclude_turn": excludeTurn}
	if includeSnapshot {
		data["include_snapshot"] = "true"
	}
	res, err := st.req("context", data)
	if err != nil {
		return nil, nil
	}
	var msgs []ChatMsg
	if raw, ok := res["messages"].([]any); ok {
		if b, err := json.Marshal(raw); err == nil {
			_ = json.Unmarshal(b, &msgs)
		}
	}
	var tokens []facade.TurnToken
	if raw, ok := res["turn_tokens"].([]any); ok {
		if b, err := json.Marshal(raw); err == nil {
			_ = json.Unmarshal(b, &tokens)
		}
	}
	return msgs, tokens
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

// CleanupStaleTurns 启动/首次使用清理：遗留 running 的 turn 标 interrupted
// （进程崩溃/重启后防同 session 双开）。
func (st *sessionStore) CleanupStaleTurns() error {
	_, err := st.req("cleanup-stale", map[string]any{})
	return err
}

// SetSnapshot 写会话历史快照（data-snapshot-set，§3.2b；唯一终态 llm-complete 写，
// snapshot_turn = 覆盖到最后一条 turn）。
func (st *sessionStore) SetSnapshot(sessionID string, history []ChatMsg, snapshotTurn string) error {
	_, err := dataRequest(st.bus, msgkeys.TopicDataSnapshotSet, map[string]any{
		"instance_id": st.instanceID,
		"data": map[string]any{
			"session_id": sessionID,
			"snapshot":   map[string]any{"history": history, "snapshot_turn": snapshotTurn},
		},
	})
	return err
}
