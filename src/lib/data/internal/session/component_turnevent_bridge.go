// component_turnevent_bridge.go —— P4-2 批次 D（2026-10-31）：轮次事件的组件调用桥。
//
// 分工：事件表读写（幂等追加 / 单事务批删）归组件（目录库层）；「哪些 turn 已终态且超期」的
// 跨库枚举留门面（需要逐个对话库扫 `turns`）。
package session

import (
	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/lsong98sh/chonkpilot-agent/session/component"
)

// componentTurnEventAppend 是 `TurnEventAppend` 的组件版（校验与错误文案留在门面）。
func (s *Service) componentTurnEventAppend(prj *data.DB, req facade.TurnEventAppendRequest) (facade.TurnEventAppendResponse, error) {
	comp, err := componentBridge(prj)
	if err != nil {
		return facade.TurnEventAppendResponse{}, err
	}
	if err := comp.TurnEventAppend(component.TurnEventAppendCmd{
		SessionID: req.SessionID,
		TurnID:    req.TurnID,
		Seq:       req.Seq,
		Type:      req.Type,
		At:        req.At,
		Payload:   req.Payload,
	}); err != nil {
		return facade.TurnEventAppendResponse{}, err
	}
	return facade.TurnEventAppendResponse{OK: true}, nil
}

// componentTurnEventCleanup 是 `TurnEventCleanup` 的组件版（删除集合由门面判定后传入）。
func (s *Service) componentTurnEventCleanup(prj *data.DB, oldTurnIDs map[string]bool) (int, error) {
	comp, err := componentBridge(prj)
	if err != nil {
		return 0, err
	}
	return comp.TurnEventCleanup(component.TurnEventCleanupCmd{OldTurnIDs: oldTurnIDs})
}
