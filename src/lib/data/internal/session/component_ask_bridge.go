// component_ask_bridge.go —— P4-2 批次 D（2026-10-31）：未答 ask 回查的组件调用桥。
//
// 分工：扫描语义（ask_id 过滤 / 命中即收 / PendingOnly）归组件；提问文本提取与排序留门面。
package session

import (
	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
	"github.com/lsong98sh/chonkpilot-agent/session/component"
)

// componentAskPending 是单库 `askPendingIn` 的组件版：组件回原始行，本函数做 DTO 映射。
func (s *Service) componentAskPending(prj *data.DB, dialogID string, req facade.AskPendingRequest) ([]facade.AskPendingItem, error) {
	comp, err := componentBridge(prj)
	if err != nil {
		return nil, err
	}
	recs, err := comp.AskPending(component.AskPendingCmd{
		DialogID:    dialogID,
		AskID:       req.AskID,
		PendingOnly: req.PendingOnly,
	})
	if err != nil {
		return nil, err
	}
	items := make([]facade.AskPendingItem, 0, len(recs))
	for _, r := range recs {
		items = append(items, facade.AskPendingItem{
			AskID:      kernel.Sval(r["ask_id"]),
			DialogID:   dialogID,
			SessionID:  kernel.Sval(r["session_id"]),
			TurnID:     kernel.Sval(r["turn_id"]),
			ToolCallID: kernel.Sval(r["tool_call_id"]),
			Status:     kernel.Sval(r["tool_call_status"]),
			Question:   askQuestionOf(kernel.Sval(r["content"])),
			CreatedAt:  kernel.Sval(r["created_at"]),
		})
	}
	return items, nil
}
