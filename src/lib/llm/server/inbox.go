// INBOX 方法面（中途插话 / 等下一轮的统一发送入口）。
//
// 设计见 `docs/agent-chat/中途插话设计方案.md` §3.3 / §5.1.1 / §6.2.3：
//   - 所有发送先落 prjusr `inbox_items`（status=pending），由 llm-server 按当前相位分类：
//     `toolPending`/`awaitJoin` → `insert`（立即插入并消费）；`streaming`/无运行轮 → `next_turn`；
//   - 消费 = 落 `messages` + 置 `consumed`（本文件负责调度，落库走既有 session 数据面）；
//   - 撤回 → `discarded`；`next_turn → insert` 提升仅当该会话处于可插入相位。
//
// 消息面（61 冻结）：仅 `llm-inbox-add/list/discard/promote` 四条方法面；持久化经
// `data-inbox-append/list/update` 内部面（llm-server 不直连 prjusr 库）。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// inboxAddReq 是 llm-inbox-add 载荷（61 §4.3）。
type inboxAddReq struct {
	InstanceID     string `json:"instance_id"`
	Session        string `json:"session"`
	Turn           string `json:"turn,omitempty"`
	LLM            string `json:"llm,omitempty"`
	Content        string `json:"content"`
	Think          any    `json:"think,omitempty"`
	Effort         any    `json:"effort,omitempty"`
	ScenarioID     string `json:"scenario_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// inboxIDReq 是 discard / promote 载荷。
type inboxIDReq struct {
	InstanceID string `json:"instance_id"`
	ID         string `json:"id"`
}

// inboxListReq 是 llm-inbox-list 载荷。
type inboxListReq struct {
	InstanceID string `json:"instance_id"`
	Session    string `json:"session"`
	Status     string `json:"status,omitempty"`
}

// ── 数据面助手（data-inbox-*）────────────────────────────────────────────

// inboxAppend 写入一条待处理发送，返回 id。
func (s *Server) inboxAppend(instanceID string, req facade.InboxAppendRequest) (string, error) {
	res, err := dataRequest(s.bus, "data-inbox-append", map[string]any{
		"instance_id": instanceID,
		"data": map[string]any{
			"session":         req.SessionID,
			"turn_id":         req.TurnID,
			"type":            req.Type,
			"llm":             req.LLM,
			"content":         req.Content,
			"think":           req.Think,
			"effort":          req.Effort,
			"scenario_id":     req.ScenarioID,
			"idempotency_key": req.IdempotencyKey,
			"source":          req.Source,
		},
	})
	if err != nil {
		return "", err
	}
	return str(res["id"]), nil
}

// inboxList 读 inbox 项（status 缺省 = pending，按 created_at 升序）。
func (s *Server) inboxList(instanceID, sessionID, status string) ([]facade.InboxItem, error) {
	res, err := dataRequest(s.bus, "data-inbox-list", map[string]any{
		"instance_id": instanceID,
		"data":        map[string]any{"session": sessionID, "status": status},
	})
	if err != nil {
		return nil, err
	}
	raw, _ := res["items"].([]any)
	items := make([]facade.InboxItem, 0, len(raw))
	for _, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		item := facade.InboxItem{
			ID:             str(m["id"]),
			SessionID:      sessionID,
			TurnID:         str(m["turn_id"]),
			Type:           str(m["type"]),
			LLM:            str(m["llm"]),
			Content:        str(m["content"]),
			ScenarioID:     str(m["scenario_id"]),
			Status:         str(m["status"]),
			IdempotencyKey: str(m["idempotency_key"]),
			CreatedAt:      str(m["created_at"]),
			Source:         str(m["source"]),
		}
		items = append(items, item)
	}
	return items, nil
}

// inboxUpdate 更新 inbox 项（空字段 = 不改；content 非空 = 就地覆盖正文，
// 供 NEXTRUN-1 §3.5「同轮多次调用只保留最后一条」）。
func (s *Server) inboxUpdate(instanceID, id, status, typ, content string) error {
	_, err := dataRequest(s.bus, "data-inbox-update", map[string]any{
		"instance_id": instanceID,
		"data":        map[string]any{"id": id, "status": status, "type": typ, "content": content},
	})
	return err
}

// ── 内存索引（promote/discard 仅携带 id，需回查 session / type）─────────────

func (s *Server) inboxRemember(item facade.InboxItem) {
	if item.ID == "" {
		return
	}
	s.inboxMu.Lock()
	if s.inboxIndex == nil {
		s.inboxIndex = map[string]facade.InboxItem{}
	}
	s.inboxIndex[item.ID] = item
	s.inboxMu.Unlock()
}

func (s *Server) inboxLookup(id string) (facade.InboxItem, bool) {
	s.inboxMu.Lock()
	item, ok := s.inboxIndex[id]
	s.inboxMu.Unlock()
	return item, ok
}

// inboxSetStatus 同步内存索引中的状态（consumed / discarded）。
func (s *Server) inboxSetStatus(id, status string) {
	s.inboxMu.Lock()
	if it, ok := s.inboxIndex[id]; ok {
		it.Status = status
		s.inboxIndex[id] = it
	}
	s.inboxMu.Unlock()
}

// inboxHasPendingNextTurn 报告该会话是否有待消费的 next_turn 项（内存索引；避免无项时也发起
// 数据面请求——测试/关停期间不必要地打开 prjusr 库）。
func (s *Server) inboxHasPendingNextTurn(sessionID string) bool {
	s.inboxMu.Lock()
	defer s.inboxMu.Unlock()
	for _, it := range s.inboxIndex {
		if it.SessionID == sessionID && it.Status == "pending" && it.Type == "next_turn" {
			return true
		}
	}
	return false
}

// ── 方法面 handler ──────────────────────────────────────────────────────

// onInboxAdd 统一发送入口：按当前相位分类 insert / next_turn 并落库。
func (s *Server) onInboxAdd(_ context.Context, _ string, v *mq.Value) error {
	var req inboxAddReq
	if err := json.Unmarshal(v.Payload, &req); err != nil {
		v.Result = map[string]any{"ok": false, "id": "", "type": "next_turn"}
		return nil
	}
	if strings.TrimSpace(req.InstanceID) == "" || strings.TrimSpace(req.Session) == "" ||
		strings.TrimSpace(req.Content) == "" {
		v.Result = map[string]any{"ok": false, "id": "", "type": "next_turn"}
		return nil
	}
	// INPUTVAL-1：后端复验单条输入长度（超限明确拒绝，不静默截断）。
	if n := len([]rune(req.Content)); n > maxChatInputChars {
		v.Result = map[string]any{"ok": false, "id": "", "type": "next_turn",
			"error": fmt.Sprintf("消息超长（%d 字符，上限 %d）", n, maxChatInputChars)}
		return nil
	}
	// 用户排队输入 = 用户动作 → 重置 next_run 连续计数（NEXTRUN-1 §3.3.3）。
	s.resetNextRunCount(req.InstanceID, req.Session)

	tc := s.turnBySessionForAdd(req.InstanceID, req.Session)
	typ := "next_turn"
	turnID := ""
	if tc != nil && tc.insertable() {
		typ = "insert"
		turnID = tc.req.Turn
	}

	id, err := s.inboxAppend(req.InstanceID, facade.InboxAppendRequest{
		SessionID: req.Session, TurnID: turnID, Type: typ,
		LLM: req.LLM, Content: req.Content, Think: req.Think, Effort: req.Effort,
		ScenarioID: req.ScenarioID, IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		logf("[chonkpilot-server] llm-inbox-add 落库失败（session=%s）: %v\n", req.Session, err)
		v.Result = map[string]any{"ok": false, "id": "", "type": "next_turn"}
		return nil
	}
	item := facade.InboxItem{
		ID: id, SessionID: req.Session, TurnID: turnID, Type: typ,
		LLM: req.LLM, Content: req.Content, ScenarioID: req.ScenarioID,
		Status: "pending", IdempotencyKey: req.IdempotencyKey,
	}
	s.inboxRemember(item)
	if typ == "insert" {
		if tc.pushInbox(item) {
			v.Result = map[string]any{"ok": true, "id": id, "type": "insert"}
			return nil
		}
		// 入插入帧失败（队列满 / 轮次刚关）→ 落库同步降级 next_turn，避免留下"insert 却永不被消费"。
		_ = s.inboxUpdate(req.InstanceID, id, "", "next_turn", "")
		item.Type = "next_turn"
		item.TurnID = ""
		s.inboxRemember(item)
		v.Result = map[string]any{"ok": true, "id": id, "type": "next_turn"}
		return nil
	}
	// 未进入插入帧（无运行轮 / 流式期）：等本轮结束或无轮时由 consumeNextTurn 消费。
	v.Result = map[string]any{"ok": true, "id": id, "type": "next_turn"}
	if tc == nil {
		// 无运行轮 → 立即以该条起新一轮（设计 §3.3「done / 无 turn → 立即消费」）。
		go s.consumeNextTurn(req.InstanceID, req.Session)
	}
	return nil
}

// onInboxList 读回 inbox 项（队列指示器 / 重启拉回）。
func (s *Server) onInboxList(_ context.Context, _ string, v *mq.Value) error {
	var req inboxListReq
	if err := json.Unmarshal(v.Payload, &req); err != nil {
		v.Result = map[string]any{"ok": false, "items": []any{}}
		return nil
	}
	items, err := s.inboxList(req.InstanceID, req.Session, req.Status)
	if err != nil {
		logf("[chonkpilot-server] llm-inbox-list 失败（session=%s）: %v\n", req.Session, err)
		v.Result = map[string]any{"ok": false, "items": []any{}}
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		s.inboxRemember(it)
		m := map[string]any{
			"id": it.ID, "type": it.Type, "content": it.Content,
			"status": it.Status, "created_at": it.CreatedAt,
		}
		if it.TurnID != "" {
			m["turn_id"] = it.TurnID
		}
		if it.LLM != "" {
			m["llm"] = it.LLM
		}
		if it.ScenarioID != "" {
			m["scenario_id"] = it.ScenarioID
		}
		// 来源标识（NEXTRUN-1 §3.4）：`llm` = 模型 `next_run` 登记（前端可显示「自动接续」）。
		if it.Source != "" {
			m["source"] = it.Source
		}
		out = append(out, m)
	}
	v.Result = map[string]any{"ok": true, "items": out}
	return nil
}

// onInboxDiscard 撤回 / 删除一条 inbox 项（置 discarded，不入 messages）。
func (s *Server) onInboxDiscard(_ context.Context, _ string, v *mq.Value) error {
	var req inboxIDReq
	_ = json.Unmarshal(v.Payload, &req)
	if req.ID == "" {
		v.Result = map[string]any{"ok": false}
		return nil
	}
	// 删除只作用于 pending：已 consumed（消息已落库）/ 已 discarded 的项不再变更，
	// 避免「撤回已发送消息」造成镜像与历史不一致。
	if cur, ok := s.inboxLookup(req.ID); ok && cur.Status != "pending" {
		v.Result = map[string]any{"ok": false}
		return nil
	}
	if err := s.inboxUpdate(req.InstanceID, req.ID, "discarded", "", ""); err != nil {
		logf("[chonkpilot-server] llm-inbox-discard 失败（id=%s）: %v\n", req.ID, err)
		v.Result = map[string]any{"ok": false}
		return nil
	}
	v.Result = map[string]any{"ok": true}
	s.inboxSetStatus(req.ID, "discarded")
	return nil
}

// onInboxPromote 把 next_turn 提升为 insert（「立刻插入」）。
// 仅当该会话存在运行轮且处于可插入相位（当前帧有在飞工具）时生效；否则保持 next_turn。
func (s *Server) onInboxPromote(_ context.Context, _ string, v *mq.Value) error {
	var req inboxIDReq
	_ = json.Unmarshal(v.Payload, &req)
	item, ok := s.inboxLookup(req.ID)
	if req.ID == "" || !ok {
		v.Result = map[string]any{"ok": false, "type": "next_turn"}
		return nil
	}
	if item.Status != "pending" {
		// 已消费 / 已撤回 → 不可再提升（消息已落库或已明确丢弃）。
		v.Result = map[string]any{"ok": false, "type": item.Type}
		return nil
	}
	// 用户显式提升 = 用户动作 → 重置 next_run 连续计数（NEXTRUN-1 §3.3.3）。
	s.resetNextRunCount(req.InstanceID, item.SessionID)
	tc := s.turnBySessionForAdd(req.InstanceID, item.SessionID)
	if tc == nil {
		// 无运行轮（重启后用户点「继续」）：以该会话全部 pending 项起新一轮（仍不自动发送——
		// 由用户显式动作触发）。返回 ok 表示已受理。
		go s.consumeNextTurn(req.InstanceID, item.SessionID)
		v.Result = map[string]any{"ok": true, "type": item.Type}
		return nil
	}
	if item.Type == "insert" || !tc.insertable() {
		v.Result = map[string]any{"ok": false, "type": item.Type}
		return nil
	}
	if err := s.inboxUpdate(req.InstanceID, req.ID, "", "insert", ""); err != nil {
		logf("[chonkpilot-server] llm-inbox-promote 失败（id=%s）: %v\n", req.ID, err)
		v.Result = map[string]any{"ok": false, "type": "next_turn"}
		return nil
	}
	item.Type = "insert"
	item.TurnID = tc.req.Turn
	if !tc.pushInbox(item) {
		// 入帧失败 → 落库回滚为 next_turn（否则留下"insert 却永不被消费"的行）。
		_ = s.inboxUpdate(req.InstanceID, req.ID, "", "next_turn", "")
		item.Type = "next_turn"
		s.inboxRemember(item)
		v.Result = map[string]any{"ok": false, "type": "next_turn"}
		return nil
	}
	s.inboxRemember(item)
	v.Result = map[string]any{"ok": true, "type": "insert"}
	return nil
}

// turnBySessionForAdd 定位该会话的运行轮（带 instance 归属）。
func (s *Server) turnBySessionForAdd(instanceID, sessionID string) *turnCtx {
	s.mu.Lock()
	tc := s.turnBySessionLocked(instanceID, sessionID)
	s.mu.Unlock()
	return tc
}

// consumeNextTurn 本轮结束后消费 `next_turn` 项：起新一轮 + 逐条落 messages + Kick。
//
// 仅主会话自动续轮（子会话 / 群聊由各自调度器驱动）。失败（会话仍忙 / 无项）→ 保持 pending，
// 等下一次轮次结束或用户「继续」。
func (s *Server) consumeNextTurn(instanceID, sessionID string) {
	if sessionID == "" {
		return
	}
	// 无待消费项（内存索引）→ 不发起数据面请求（避免无谓打开 prjusr 库 / 关停期噪音）。
	if !s.inboxHasPendingNextTurn(sessionID) {
		return
	}
	if kind := s.sessionRow(instanceID, sessionID).Kind; kind != "" {
		return
	}
	items, err := s.inboxList(instanceID, sessionID, "pending")
	if err != nil {
		logf("[chonkpilot-server] consumeNextTurn: inbox 读取失败（session=%s）: %v\n", sessionID, err)
		return
	}
	// 读回的行先入内存索引：后续撤回（discard）会同步内存状态，下面两次 pending 复核才能看见。
	for _, it := range items {
		s.inboxRemember(it)
	}
	batch := make([]facade.InboxItem, 0, len(items))
	for _, it := range items {
		// insert 项的目标轮已结束 → 降级为 next_turn（设计 §3.3「类型互转」）。
		if it.Type == "insert" {
			_ = s.inboxUpdate(instanceID, it.ID, "", "next_turn", "")
			it.Type = "next_turn"
			s.inboxRemember(it)
		}
		if it.Type == "next_turn" {
			batch = append(batch, it)
		}
	}
	if len(batch) == 0 {
		return
	}
	// 撤回优先（Q6/删除语义）：起轮前再读一次 pending 快照 —— list 与本批写库之间可能发生 discard，
	// 非 pending 的行**绝不**再落 messages。
	pendingSet := map[string]bool{}
	if cur, err := s.inboxList(instanceID, sessionID, "pending"); err == nil {
		for _, it := range cur {
			pendingSet[it.ID] = true
		}
	} else {
		logf("[chonkpilot-server] consumeNextTurn: 复核 pending 失败（session=%s）: %v\n", sessionID, err)
		return // 无法复核 → 保守不发（保持 pending，等下次）
	}
	// 失效判定（NEXTRUN-1 §4）：LLM 登记项的 `turn_id` 必须等于会话**最新 turn**，否则丢弃
	// （崩溃 / 被用户新轮抢占后不复活）。用户项不受此约束（沿用既有语义）。
	latestTurn := newSessionStore(s.bus, instanceID).LatestTurnID(sessionID)
	req := StartReq{InstanceID: instanceID, Session: sessionID, Turn: newID()}
	if llm := batch[0].LLM; llm != "" {
		req.LLMModel = llm
	}
	if sc := batch[0].ScenarioID; sc != "" {
		req.ScenarioID = sc
	}
	if t := str(batch[0].Think); t != "" {
		req.Think = t
	}
	if e := str(batch[0].Effort); e != "" {
		req.Effort = e
	}
	res := s.acceptStart(req)
	if ok, _ := res["accepted"].(bool); !ok {
		return // 会话忙 / 实例未注册 → 保持 pending
	}
	turnID, _ := res["turn"].(string)
	st := newSessionStore(s.bus, instanceID)
	for _, it := range batch {
		// 逐条最后复核：复核快照之后、本条写库之前仍可能被撤回（内存由 discard 同步更新）。
		if !pendingSet[it.ID] {
			continue
		}
		if cur, ok := s.inboxLookup(it.ID); ok && cur.Status != "pending" {
			continue
		}
		// NEXTRUN-1：LLM 登记项失效（turn_id ≠ 最新 turn）→ discarded，不落消息、不起轮。
		kind := "text"
		if it.Source == "llm" {
			if latestTurn != "" && it.TurnID != latestTurn {
				_ = s.inboxUpdate(instanceID, it.ID, "discarded", "", "")
				s.inboxSetStatus(it.ID, "discarded")
				continue
			}
			kind = "next_run" // 来源可识别：落库消息 kind=next_run（仍是新 turn 边界的 user 消息）
		}
		// 逐条落 messages（前端展示不合并）；同一轮内合并为一次 LLM 请求（Kick 触发一次）。
		if _, err := st.AppendFullKeyed(turnID, ChatMsg{Role: "user", Kind: kind, Content: it.Content}, ""); err != nil {
			logf("[chonkpilot-server] consumeNextTurn: 落库失败（id=%s）: %v\n", it.ID, err)
			continue
		}
		// 先落 message、再置 consumed（Q6）；置位失败仅告警，下次 pending 复核会再看到该行
		// （at-least-once：宁可重发一次，也不静默丢用户输入）。
		if err := s.inboxUpdate(instanceID, it.ID, "consumed", "", ""); err != nil {
			logf("[chonkpilot-server] consumeNextTurn: 置 consumed 失败（id=%s）: %v\n", it.ID, err)
			continue
		}
		s.inboxSetStatus(it.ID, "consumed")
	}
	s.mu.Lock()
	sess := s.lookupTurnSessionLocked(instanceID, turnID)
	s.mu.Unlock()
	if in, ok := sess.(turnInput); ok {
		in.Kick() // 以库内既有上下文跑一次 LLM 循环（无新输入、不重复落库）
	}
}
