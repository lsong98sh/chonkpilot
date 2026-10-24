// data-inbox-* 信封层：INBOX 内部面（llm-server → persist）。
//
// 设计见 `docs/agent-chat/中途插话设计方案.md` §6.2.3；对客户端只暴露 `llm-inbox-*`
// （由 llm-server 处理），本层仅承担 prjusr `inbox_items` 的持久化。
package persist

import (
	"fmt"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
)

// handleInbox 分发 data-inbox-<action>（append / list / update）。
func (s *Service) handleInbox(action string, req dataReq) {
	method := "data-inbox-" + action
	okf := func(result map[string]any) { s.reply(method, req, result) }
	failf := func(format string, args ...any) {
		s.fail(method, req, fmt.Errorf(format, args...))
	}
	d := req.Data
	str := func(k string) string {
		if v, ok := d[k].(string); ok {
			return v
		}
		return ""
	}

	switch action {
	case "append":
		resp, err := s.InboxAppend(facade.InboxAppendRequest{
			InstanceID:     req.InstanceID,
			DialogID:       str("dialog_id"),
			SessionID:      str("session"),
			TurnID:         str("turn_id"),
			Type:           str("type"),
			LLM:            str("llm"),
			Content:        str("content"),
			Think:          d["think"],
			Effort:         d["effort"],
			ScenarioID:     str("scenario_id"),
			IdempotencyKey: str("idempotency_key"),
			Source:         str("source"),
		})
		if err != nil {
			failf("%v", err)
			return
		}
		okf(map[string]any{"ok": resp.OK, "id": resp.ID})
	case "list":
		resp, err := s.InboxList(facade.InboxListRequest{
			InstanceID: req.InstanceID,
			DialogID:   str("dialog_id"),
			SessionID:  str("session"),
			Status:     str("status"),
		})
		if err != nil {
			failf("%v", err)
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
		okf(map[string]any{"ok": true, "items": items})
	case "update":
		id := idKey(req.ID)
		if id == "" {
			id = str("id")
		}
		resp, err := s.InboxUpdate(facade.InboxUpdateRequest{
			InstanceID: req.InstanceID,
			DialogID:   str("dialog_id"),
			ID:         id,
			Status:     str("status"),
			Type:       str("type"),
			TurnID:     str("turn_id"),
			Content:    str("content"),
		})
		if err != nil {
			failf("%v", err)
			return
		}
		okf(map[string]any{"ok": resp.OK})
	case "commit":
		// MOD-1 M6：同事务写消息 + 置 consumed（幂等；消息主键由 inbox_id 确定性推出）。
		inboxID := str("inbox_id")
		if inboxID == "" {
			inboxID = idKey(req.ID)
		}
		mr := wire.MessageAppendFromWire(map[string]any{
			"turn_id":          str("turn_id"),
			"session_id":       str("session_id"),
			"msg":              d["msg"],
			"brief":            str("brief"),
			"tool_call_status": str("tool_call_status"),
		})
		resp, err := s.InboxCommit(facade.InboxCommitRequest{
			InstanceID:     req.InstanceID,
			DialogID:       str("dialog_id"),
			InboxID:        inboxID,
			TurnID:         mr.TurnID,
			SessionID:      mr.SessionID,
			Message:        mr.Message,
			Brief:          mr.Brief,
			ToolCallStatus: mr.ToolCallStatus,
		})
		if err != nil {
			failf("%v", err)
			return
		}
		okf(map[string]any{"ok": resp.OK, "id": resp.ID})
	}
}
