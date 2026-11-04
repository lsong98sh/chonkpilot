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
	"slices"
	"sort"
	"strings"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	sessioninbox "github.com/lsong98sh/chonkpilot-agent/session/inbox"
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
	// Source = 入队来源（可选）。`mobile`（H5/远程接入）与空（桌面）语义一致：
	// 均可排队 / 插话；仅用于前端标识来源。
	Source string `json:"source,omitempty"`
	// Insert = 用户显式「插话」意向（2026-11-01）：true → 登记 `type=insert` pending，
	// 当前会话处于可插入相位（有在飞工具）时把**全部意向项一次性批量插入**；否则保持 pending
	// 等相位。缺省 / false → `next_turn` 排队等本轮结束。
	Insert bool `json:"insert,omitempty"`
}

// inboxIDReq 是 discard / revert / promote 载荷（promote 支持 ids[] 多选）。
type inboxIDReq struct {
	InstanceID string   `json:"instance_id"`
	ID         string   `json:"id"`
	IDs        []string `json:"ids,omitempty"`
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
	payload := map[string]any{
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
	}
	if dlg := s.dialogForWrite(instanceID, req.SessionID); dlg != "" {
		payload["dialog_id"] = dlg
	}
	res, err := dataRequest(s.bus, "data-inbox-append", map[string]any{
		"instance_id": instanceID,
		"data":        payload,
	})
	if err != nil {
		return "", err
	}
	return str(res["id"]), nil
}

// inboxList 读 inbox 项（status 缺省 = pending，按 created_at 升序）。
func (s *Server) inboxList(instanceID, sessionID, status string) ([]facade.InboxItem, error) {
	payload := map[string]any{"session": sessionID, "status": status}
	if dlg := s.ensureDialogIndex(instanceID, sessionID); dlg != "" {
		payload["dialog_id"] = dlg
	}
	res, err := dataRequest(s.bus, "data-inbox-list", map[string]any{
		"instance_id": instanceID,
		"data":        payload,
	})
	if err != nil {
		// 一树一库：会话行 / 对话未知（`dialog_required`）→ 该会话必然没有 inbox 项：
		// 按空返回，不把该错误抛给前端（避免「加载消息失败」噪声）。
		// （`dialog_id` 恒必填 —— 旧布局已退场，见 61 §0.6。）
		if strings.Contains(err.Error(), "dialog_required") {
			return nil, nil
		}
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
	payload := map[string]any{"id": id, "status": status, "type": typ, "content": content}
	// 一树一库：按内存索引回查该 inbox 项所属会话 → 补 dialog_id（未知则不写键）。
	if item, ok := s.inboxLookup(id); ok {
		if dlg := s.ensureDialogIndex(instanceID, item.SessionID); dlg != "" {
			payload["dialog_id"] = dlg
		}
	}
	_, err := dataRequest(s.bus, "data-inbox-update", map[string]any{
		"instance_id": instanceID,
		"data":        payload,
	})
	return err
}

// ── 内存索引（promote/discard 仅携带 id，需回查 session / type）─────────────
//
// LLM-P4（inboxIndex 分桶 / 有界）：
//   - **分桶**：`inboxPending[session] = {id → type}` 只装 pending 项 → `inboxHasPendingNextTurn`
//     由「遍历全部 inbox 项」变为「只看该会话的 pending 项」（长跑进程下 O(总项数) → O(本会话项数)）；
//   - **有界**：终态项（consumed / discarded）按 FIFO 只保留最近 `inboxTerminalKeep` 条，
//     超出即从主索引淘汰最旧条目（幂等消费/撤回守卫只对近期项有意义；重启后索引本就从空建立，
//     淘汰后的行为与该情形等价：`inboxLookup` 未命中）。

// inboxTerminalKeep 是终态项在主索引中的保留条数上限（超出淘汰最旧；见文件内说明）。
const inboxTerminalKeep = 1024

// inboxRemember 写入/刷新索引项（状态与类型取 item 当前值）。
func (s *Server) inboxRemember(item facade.InboxItem) {
	if item.ID == "" {
		return
	}
	s.inboxMu.Lock()
	if s.inboxIndex == nil {
		s.inboxIndex = map[string]facade.InboxItem{}
	}
	if s.inboxPending == nil {
		s.inboxPending = map[string]map[string]string{}
	}
	s.inboxRememberLocked(item)
	s.inboxMu.Unlock()
}

// inboxRememberLocked 索引写入的加锁实现（调用方持 s.inboxMu）。
func (s *Server) inboxRememberLocked(item facade.InboxItem) {
	prev, existed := s.inboxIndex[item.ID]
	s.inboxIndex[item.ID] = item
	if item.Status == sessioninbox.StatusPending {
		bucket := s.inboxPending[item.SessionID]
		if bucket == nil {
			bucket = map[string]string{}
			s.inboxPending[item.SessionID] = bucket
		}
		bucket[item.ID] = item.Type
		return
	}
	// 终态：移出 pending 桶；**首次**进入终态时登记 FIFO（重复 remember 不重复登记）。
	s.inboxDropPendingLocked(item.SessionID, item.ID)
	if !existed || prev.Status == sessioninbox.StatusPending {
		s.inboxTerminal = append(s.inboxTerminal, item.ID)
		s.inboxTrimTerminalLocked()
	}
}

// inboxDropPendingLocked 从 pending 桶移除一项（桶空则删桶）。
func (s *Server) inboxDropPendingLocked(sessionID, id string) {
	bucket := s.inboxPending[sessionID]
	if bucket == nil {
		return
	}
	delete(bucket, id)
	if len(bucket) == 0 {
		delete(s.inboxPending, sessionID)
	}
}

// inboxTrimTerminalLocked 淘汰最旧的终态条目（主索引有界）。
func (s *Server) inboxTrimTerminalLocked() {
	for len(s.inboxTerminal) > inboxTerminalKeep {
		oldest := s.inboxTerminal[0]
		s.inboxTerminal = s.inboxTerminal[1:]
		if cur, ok := s.inboxIndex[oldest]; ok && cur.Status != sessioninbox.StatusPending {
			delete(s.inboxIndex, oldest)
		}
	}
	// 长跑下 FIFO 与索引同步增长/裁短：两者规模同阶，切片残留不影响正确性（容量由 GC 回收）。
	if len(s.inboxTerminal) == 0 {
		s.inboxTerminal = nil
	}
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
		s.inboxRememberLocked(it)
	}
	if status != sessioninbox.StatusPending {
		delete(s.inboxQueued, id) // 终态 → 不再需要「已投递待消费」标记
	}
	s.inboxMu.Unlock()
}

// inboxHasPendingNextTurn 报告该会话是否有待消费的 next_turn 项（内存索引分桶直取；避免无项时也
// 发起数据面请求——测试/关停期间不必要地打开 prjusr 库）。
func (s *Server) inboxHasPendingNextTurn(sessionID string) bool {
	s.inboxMu.Lock()
	defer s.inboxMu.Unlock()
	for _, typ := range s.inboxPending[sessionID] {
		if typ == string(sessioninbox.NextTurn) {
			return true
		}
	}
	return false
}

// ── 方法面 handler ──────────────────────────────────────────────────────

// onInboxAdd 统一发送入口（2026-11-01 语义调整）：
//   - 无运行轮 → `next_turn` 落库后立即消费，起新一轮；
//   - 有运行轮 + `insert=true`（用户显式「插话」）→ 登记 `insert` 意向（pending），
//     相位可插入时把该会话**全部意向项一次性批量插入**；否则等相位（由轮内 ticker 重试）；
//   - 有运行轮 + 缺省 → `next_turn` 排队，本轮结束自动消费。
//
// 不再按相位自动插话（原 ClassifyAdd 行为）：插话是用户显式动作。
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
	typ := string(sessioninbox.NextTurn)
	turnID := ""
	// 插话意向只在**有运行轮**时有效（无轮 = 直接起新一轮）。
	if req.Insert && tc != nil {
		typ = string(sessioninbox.Insert)
		turnID = tc.req.Turn
	}

	id, err := s.inboxAppend(req.InstanceID, facade.InboxAppendRequest{
		SessionID: req.Session, TurnID: turnID, Type: typ,
		LLM: req.LLM, Content: req.Content, Think: req.Think, Effort: req.Effort,
		ScenarioID: req.ScenarioID, IdempotencyKey: req.IdempotencyKey, Source: req.Source,
	})
	if err != nil {
		logf("[chonkpilot-server] llm-inbox-add 落库失败（session=%s）: %v\n", req.Session, err)
		v.Result = map[string]any{"ok": false, "id": "", "type": "next_turn"}
		return nil
	}
	item := facade.InboxItem{
		ID: id, SessionID: req.Session, TurnID: turnID, Type: typ,
		LLM: req.LLM, Content: req.Content, ScenarioID: req.ScenarioID,
		Status: sessioninbox.StatusPending, IdempotencyKey: req.IdempotencyKey, Source: req.Source,
	}
	s.inboxRemember(item)
	s.publishInboxChanged(req.InstanceID, req.Session, "added", []string{id})
	if typ == string(sessioninbox.Insert) {
		// 相位满足 → 该会话全部意向项一次性批量投递（本次新增项也在其中）。
		s.flushInsertIntents(tc)
		v.Result = map[string]any{"ok": true, "id": id, "type": typ}
		return nil
	}
	// 未进入插入帧（排队 / 无运行轮）：等本轮结束或无轮时由 consumeNextTurn 消费。
	v.Result = map[string]any{"ok": true, "id": id, "type": typ}
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
	if cur, ok := s.inboxLookup(req.ID); ok && !sessioninbox.CanDiscard(cur.Status) {
		v.Result = map[string]any{"ok": false}
		return nil
	}
	if err := s.inboxUpdate(req.InstanceID, req.ID, sessioninbox.StatusDiscarded, "", ""); err != nil {
		logf("[chonkpilot-server] llm-inbox-discard 失败（id=%s）: %v\n", req.ID, err)
		v.Result = map[string]any{"ok": false}
		return nil
	}
	s.inboxSetStatus(req.ID, sessioninbox.StatusDiscarded)
	s.inboxUnqueue(req.ID)
	item, _ := s.inboxLookup(req.ID)
	s.publishInboxChanged(req.InstanceID, item.SessionID, "discarded", []string{req.ID})
	v.Result = map[string]any{"ok": true}
	return nil
}

// onInboxPromote 登记「插话」意向（`next_turn → insert`，2026-11-01）：
//   - 支持 `id` 或 `ids[]`（多选插话）；
//   - 相位可插入 → 该会话**全部**意向项一次性批量插入；否则保持 pending 等相位；
//   - 无运行轮 → 起新一轮消费全部 pending（重启后「继续」）。
func (s *Server) onInboxPromote(_ context.Context, _ string, v *mq.Value) error {
	var req inboxIDReq
	_ = json.Unmarshal(v.Payload, &req)
	ids := req.IDs
	if req.ID != "" && !slices.Contains(ids, req.ID) {
		ids = append([]string{req.ID}, ids...)
	}
	if len(ids) == 0 {
		v.Result = map[string]any{"ok": false, "type": "next_turn"}
		return nil
	}
	okAll := true
	anyInsert := false
	sessionID := ""
	for _, id := range ids {
		item, ok := s.inboxLookup(id)
		if !ok || item.Status != sessioninbox.StatusPending {
			// 已消费 / 已撤回 → 不可再登记意向（消息已落库或已明确丢弃）。
			okAll = false
			continue
		}
		sessionID = item.SessionID
		// 用户显式插话 = 用户动作 → 重置 next_run 连续计数（NEXTRUN-1 §3.3.3）。
		s.resetNextRunCount(req.InstanceID, item.SessionID)
		if item.Type == string(sessioninbox.Insert) {
			anyInsert = true // 已是意向 → 幂等
			continue
		}
		if err := s.inboxUpdate(req.InstanceID, id, "", string(sessioninbox.Promote(sessioninbox.NextTurn)), ""); err != nil {
			logf("[chonkpilot-server] llm-inbox-promote 失败（id=%s）: %v\n", id, err)
			okAll = false
			continue
		}
		item.Type = string(sessioninbox.Insert)
		s.inboxRemember(item)
		anyInsert = true
	}
	if sessionID == "" {
		for _, id := range ids {
			if it, ok := s.inboxLookup(id); ok {
				sessionID = it.SessionID
				break
			}
		}
	}
	if sessionID == "" {
		v.Result = map[string]any{"ok": false, "type": "next_turn"}
		return nil
	}
	s.publishInboxChanged(req.InstanceID, sessionID, "promoted", ids)
	tc := s.turnBySessionForAdd(req.InstanceID, sessionID)
	if tc == nil {
		// 无运行轮（重启后用户点「继续」）：以该会话全部 pending 项起新一轮（仍不自动发送——
		// 由用户显式动作触发）。返回 ok 表示已受理。
		go s.consumeNextTurn(req.InstanceID, sessionID)
		v.Result = map[string]any{"ok": true, "type": string(sessioninbox.Insert)}
		return nil
	}
	// 相位满足 → 全部意向项批量投递（不满足则由轮内 ticker 在相位到达时重试）。
	s.flushInsertIntents(tc)
	typ := string(sessioninbox.NextTurn)
	if anyInsert {
		typ = string(sessioninbox.Insert)
	}
	v.Result = map[string]any{"ok": okAll, "type": typ}
	return nil
}

// onInboxRevert 取消插话意向（`insert → next_turn`，仍 pending；2026-11-01）：
// 仅当该项仍 pending 且为 insert 意向时生效（已消费 / 已删除 → 不可取消）。
func (s *Server) onInboxRevert(_ context.Context, _ string, v *mq.Value) error {
	var req inboxIDReq
	_ = json.Unmarshal(v.Payload, &req)
	if req.ID == "" {
		v.Result = map[string]any{"ok": false, "type": string(sessioninbox.NextTurn)}
		return nil
	}
	item, ok := s.inboxLookup(req.ID)
	if !ok || item.Status != sessioninbox.StatusPending || item.Type != string(sessioninbox.Insert) {
		v.Result = map[string]any{"ok": false, "type": string(sessioninbox.NextTurn)}
		return nil
	}
	if err := s.inboxUpdate(req.InstanceID, req.ID, "", string(sessioninbox.Degrade(sessioninbox.Insert)), ""); err != nil {
		logf("[chonkpilot-server] llm-inbox-revert 失败（id=%s）: %v\n", req.ID, err)
		v.Result = map[string]any{"ok": false, "type": string(sessioninbox.Insert)}
		return nil
	}
	item.Type = string(sessioninbox.NextTurn)
	s.inboxRemember(item)
	s.inboxUnqueue(req.ID)
	s.publishInboxChanged(req.InstanceID, item.SessionID, "reverted", []string{req.ID})
	v.Result = map[string]any{"ok": true, "type": string(sessioninbox.NextTurn)}
	return nil
}

// flushInsertIntents 把握手会话的全部 pending `insert` 意向一次性投递给运行轮（2026-11-01 批量插话）：
//   - 相位不可插入 / 无运行轮 → 不动（等轮内 ticker 重试或轮末降级为 next_turn）；
//   - 已投递过的项（`inboxQueued`）跳过，避免 ticker 重复投递；
//   - 引擎侧按 FIFO 连续消费 → 同一插入帧内逐条落 messages、一次 LLM 请求（见 agent chat engine）。
func (s *Server) flushInsertIntents(tc *turnCtx) {
	if tc == nil || !tc.insertable() {
		return
	}
	sessionID := tc.req.Session
	s.inboxMu.Lock()
	if s.inboxQueued == nil {
		s.inboxQueued = map[string]bool{}
	}
	bucket := s.inboxPending[sessionID]
	items := make([]facade.InboxItem, 0, len(bucket))
	for id, typ := range bucket {
		if typ != string(sessioninbox.Insert) || s.inboxQueued[id] {
			continue
		}
		if it, ok := s.inboxIndex[id]; ok {
			items = append(items, it)
		}
	}
	// 排序 = (created_at, id)：内存 remember 的项（onInboxAdd 直记 / 测试夹具）可能不带
	// CreatedAt；主键 = `I`+UTC秒+微秒+随机（字典序即时间序）→ tie-break 保证合并顺序
	// 确定（FIFO），不随 map 遍历随机。
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt != items[j].CreatedAt {
			return items[i].CreatedAt < items[j].CreatedAt
		}
		return items[i].ID < items[j].ID
	})
	for _, it := range items {
		s.inboxQueued[it.ID] = true
	}
	s.inboxMu.Unlock()
	if len(items) == 0 {
		return
	}
	for _, it := range items {
		if !tc.pushInbox(it) {
			// 队列满 / 轮次刚关 → 撤回本次投递标记，等下一次 ticker 重试。
			s.inboxUnqueue(it.ID)
			break
		}
	}
}

// inboxUnqueue 摘除「已投递待消费」标记（消费 / 撤回 / 取消意向时调用）。
func (s *Server) inboxUnqueue(id string) {
	if id == "" {
		return
	}
	s.inboxMu.Lock()
	delete(s.inboxQueued, id)
	s.inboxMu.Unlock()
}

// publishInboxChanged 广播 inbox 变更（桌面 / H5 两端实时刷新；61 `session-inbox-changed`）。
func (s *Server) publishInboxChanged(instanceID, session, action string, ids []string) {
	if session == "" {
		return
	}
	s.publish("inbox-changed", map[string]any{
		"instance_id": instanceID, "session": session, "action": action, "ids": ids,
	})
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
	items, err := s.inboxList(instanceID, sessionID, sessioninbox.StatusPending)
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
		if it.Type == string(sessioninbox.Insert) {
			_ = s.inboxUpdate(instanceID, it.ID, "", string(sessioninbox.Degrade(sessioninbox.Insert)), "")
			it.Type = string(sessioninbox.NextTurn)
			s.inboxRemember(it)
		}
		if it.Type == string(sessioninbox.NextTurn) {
			batch = append(batch, it)
		}
	}
	if len(batch) == 0 {
		return
	}
	// 撤回优先（Q6/删除语义）：起轮前再读一次 pending 快照 —— list 与本批写库之间可能发生 discard，
	// 非 pending 的行**绝不**再落 messages。
	pendingSet := map[string]bool{}
	if cur, err := s.inboxList(instanceID, sessionID, sessioninbox.StatusPending); err == nil {
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
		if cur, ok := s.inboxLookup(it.ID); ok && !sessioninbox.Consumable(cur.Status) {
			continue
		}
		// NEXTRUN-1：LLM 登记项失效（turn_id ≠ 最新 turn）→ discarded，不落消息、不起轮。
		kind := "text"
		if it.Source == "llm" {
			if latestTurn != "" && it.TurnID != latestTurn {
				_ = s.inboxUpdate(instanceID, it.ID, sessioninbox.StatusDiscarded, "", "")
				s.inboxSetStatus(it.ID, sessioninbox.StatusDiscarded)
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
		if err := s.inboxUpdate(instanceID, it.ID, sessioninbox.StatusConsumed, "", ""); err != nil {
			logf("[chonkpilot-server] consumeNextTurn: 置 consumed 失败（id=%s）: %v\n", it.ID, err)
			continue
		}
		s.inboxSetStatus(it.ID, sessioninbox.StatusConsumed)
	}
	s.mu.Lock()
	sess := s.lookupTurnSessionLocked(instanceID, turnID)
	s.mu.Unlock()
	if in, ok := sess.(turnInput); ok {
		in.Kick() // 以库内既有上下文跑一次 LLM 循环（无新输入、不重复落库）
	}
}
