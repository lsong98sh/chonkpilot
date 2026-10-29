// data-inbox-* 信封层：INBOX 内部面（llm-server → persist）。
//
// 设计见 `docs/agent-chat/中途插话设计方案.md` §6.2.3；对客户端只暴露 `llm-inbox-*`
// （由 llm-server 处理），本层仅承担 prjusr `inbox_items` 的持久化。
package persist

import (
	"encoding/json"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
)

// handleInbox 分发 data-inbox-<action>（append / list / update / commit）。
func (s *Service) handleInbox(action string, payload []byte) {
	method := "data-inbox-" + action
	var p InboxPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		s.failDecode(method, payload, err)
		return
	}
	f := p.fields()

	switch action {
	case "append":
		resp, err := s.InboxAppend(facade.InboxAppendRequest{
			InstanceID:     p.InstanceID,
			DialogID:       f.DialogID,
			SessionID:      f.Session,
			TurnID:         f.TurnID,
			Type:           f.Type,
			LLM:            f.LLM,
			Content:        f.Content,
			Think:          f.Think,
			Effort:         f.Effort,
			ScenarioID:     f.ScenarioID,
			IdempotencyKey: f.IdempotencyKey,
			Source:         f.Source,
		})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.OKIDPayload{OK: resp.OK, ID: resp.ID})
	case "list":
		resp, err := s.InboxList(facade.InboxListRequest{
			InstanceID: p.InstanceID,
			DialogID:   f.DialogID,
			SessionID:  f.Session,
			Status:     f.Status,
		})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		items := make([]map[string]any, 0, len(resp.Items))
		for _, it := range resp.Items {
			m := map[string]any{
				"id":         it.ID,
				"type":       it.Type,
				"content":    it.Content,
				"status":     it.Status,
				"created_at": it.CreatedAt,
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
			if it.IdempotencyKey != "" {
				m["idempotency_key"] = it.IdempotencyKey
			}
			// 来源标识（NEXTRUN-1 §3.4）：仅 `source=llm` 的行带该键（用户行形状不变）。
			if it.Source != "" {
				m["source"] = it.Source
			}
			items = append(items, m)
		}
		s.replyID(method, p.ReqID, wire.InboxItemsPayload{OK: true, Items: items})
	case "update":
		resp, err := s.InboxUpdate(facade.InboxUpdateRequest{
			InstanceID: p.InstanceID,
			DialogID:   f.DialogID,
			ID:         string(f.ID),
			Status:     f.Status,
			Type:       f.Type,
			TurnID:     f.TurnID,
			Content:    f.Content,
		})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.OKPayload{OK: resp.OK})
	case "commit":
		// MOD-1 M6：同事务写消息 + 置 consumed（幂等；消息主键由 inbox_id 确定性推出）。
		inboxID := f.InboxID
		if inboxID == "" {
			inboxID = string(f.ID)
		}
		// msg 是 ChatMsg 多态体：原样 JSON 解码（session_id / brief / tool_call_status
		// 与 ChatMsg 同层，取 msg 内的值；msg 内缺省时回落到载荷顶层同名字段——61 §3.2
		// 两个位置都声明了这三个键）。
		var mp struct {
			data.ChatMsg
			SessionID      string `json:"session_id,omitempty"`
			Brief          string `json:"brief,omitempty"`
			ToolCallStatus string `json:"tool_call_status,omitempty"`
		}
		if raw := nonNullRaw(f.Msg); len(raw) > 0 {
			_ = json.Unmarshal(raw, &mp)
		}
		resp, err := s.InboxCommit(facade.InboxCommitRequest{
			InstanceID:     p.InstanceID,
			DialogID:       f.DialogID,
			InboxID:        inboxID,
			TurnID:         f.TurnID,
			SessionID:      firstNonEmpty(mp.SessionID, f.SessionID),
			Message:        data.MessageToFacade(mp.ChatMsg),
			Brief:          firstNonEmpty(mp.Brief, f.Brief),
			ToolCallStatus: firstNonEmpty(mp.ToolCallStatus, f.ToolCallStatus),
		})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.OKIDPayload{OK: resp.OK, ID: resp.ID})
	}
}
