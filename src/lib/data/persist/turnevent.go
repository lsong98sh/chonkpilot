// data-turn-event-* 信封层：TURN EVENT 内部面（llm-server → persist；MOD-1 M3）。
//
// 仅承担 prjusr `turn_events` 的持久化（TurnState 事件日志）；对客户端不暴露。
package persist

import (
	"encoding/json"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

// handleTurnEvent 分发 data-turn-event-<action>（append / cleanup）。
func (s *Service) handleTurnEvent(action string, payload []byte) {
	method := "data-turn-event-" + action
	var p TurnEventPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		s.failID(method, p.ReqID, err)
		return
	}
	f := p.fields()

	switch action {
	case "append":
		resp, err := s.TurnEventAppend(facade.TurnEventAppendRequest{
			InstanceID: p.InstanceID,
			SessionID:  f.SessionID,
			TurnID:     f.TurnID,
			Seq:        uint64(f.Seq),
			Type:       f.Type,
			At:         f.At,
			Payload:    nonNullRaw(f.Payload),
		})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, map[string]any{"ok": resp.OK})
	case "cleanup":
		resp, err := s.TurnEventCleanup(facade.TurnEventCleanupRequest{
			InstanceID:    p.InstanceID,
			RetentionDays: int(f.RetentionDays),
		})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, map[string]any{"ok": resp.OK, "deleted": resp.Deleted})
	}
}
