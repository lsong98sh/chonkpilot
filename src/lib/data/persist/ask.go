// data-ask-* 信封层：ask 未答 / 失效回查（MOD-1 M6b；llm-server → persist）。
//
// 只读查询：来源 = `messages` 中带 `ask_id` 的行（不建 ask 表）。
package persist

import (
	"encoding/json"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
)

// handleAsk 分发 data-ask-<action>（当前仅 pending）。
func (s *Service) handleAsk(action string, payload []byte) {
	method := "data-ask-" + action
	var p AskPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		s.failDecode(method, payload, err)
		return
	}
	f := p.fields()

	switch action {
	case "pending":
		resp, err := s.AskPending(facade.AskPendingRequest{
			InstanceID:  p.InstanceID,
			DialogID:    string(f.DialogID),
			AskID:       f.AskID,
			PendingOnly: bool(f.PendingOnly),
		})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.InboxItemsPayload{OK: resp.OK, Items: resp.Items})
	}
}
