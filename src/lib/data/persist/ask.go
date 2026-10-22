// data-ask-* 信封层：ask 未答 / 失效回查（MOD-1 M6b；llm-server → persist）。
//
// 只读查询：来源 = `messages` 中带 `ask_id` 的行（不建 ask 表）。
package persist

import (
	"fmt"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

// handleAsk 分发 data-ask-<action>（当前仅 pending）。
func (s *Service) handleAsk(action string, req dataReq) {
	method := "data-ask-" + action
	failf := func(format string, args ...any) {
		s.fail(method, req, fmt.Errorf(format, args...))
	}
	d := req.Data

	switch action {
	case "pending":
		askID, _ := d["ask_id"].(string)
		pendingOnly, _ := d["pending_only"].(bool)
		resp, err := s.AskPending(facade.AskPendingRequest{
			InstanceID:  req.InstanceID,
			AskID:       askID,
			PendingOnly: pendingOnly,
		})
		if err != nil {
			failf("%v", err)
			return
		}
		s.reply(method, req, map[string]any{"ok": resp.OK, "items": resp.Items})
	}
}
