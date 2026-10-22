// data-turn-event-* 信封层：TURN EVENT 内部面（llm-server → persist；MOD-1 M3）。
//
// 仅承担 prjusr `turn_events` 的持久化（TurnState 事件日志）；对客户端不暴露。
package persist

import (
	"encoding/json"
	"fmt"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

// handleTurnEvent 分发 data-turn-event-<action>（append / cleanup）。
func (s *Service) handleTurnEvent(action string, req dataReq, payload []byte) {
	method := "data-turn-event-" + action
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
		// payload 只取 data.payload（避免与信封 payload 混淆）。
		var evPayload json.RawMessage
		if v, ok := d["payload"]; ok && v != nil {
			if b, err := json.Marshal(v); err == nil {
				evPayload = b
			}
		}
		seq := int64(0)
		switch v := d["seq"].(type) {
		case float64:
			seq = int64(v)
		case int64:
			seq = v
		case json.Number:
			if n, err := v.Int64(); err == nil {
				seq = n
			}
		}
		resp, err := s.TurnEventAppend(facade.TurnEventAppendRequest{
			InstanceID: req.InstanceID,
			SessionID:  str("session_id"),
			TurnID:     str("turn_id"),
			Seq:        uint64(seq),
			Type:       str("type"),
			At:         str("at"),
			Payload:    evPayload,
		})
		if err != nil {
			failf("%v", err)
			return
		}
		okf(map[string]any{"ok": resp.OK})
	case "cleanup":
		days := 0
		switch v := d["retention_days"].(type) {
		case float64:
			days = int(v)
		case int64:
			days = int(v)
		case json.Number:
			if n, err := v.Int64(); err == nil {
				days = int(n)
			}
		}
		resp, err := s.TurnEventCleanup(facade.TurnEventCleanupRequest{
			InstanceID:    req.InstanceID,
			RetentionDays: days,
		})
		if err != nil {
			failf("%v", err)
			return
		}
		okf(map[string]any{"ok": resp.OK, "deleted": resp.Deleted})
	}
}
