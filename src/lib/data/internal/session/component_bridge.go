// component_bridge.go —— P4-2 批次二（2026-10-28）：组件调用 + 事件映射的**薄适配注入点**
// （批次三 2026-10-31：`sessionEnsureInDialog` / `sessionTitleInDialog` 已切换到本文件实现）。
//
// 目的：P4-2 批次三「逐项切 data 调用方」时，只需把 `sessionEnsureInDialog` /
// `sessionTitleInDialog` 的实现体换成下面两个函数（调用点、MQ 载荷、幂等语义全不变），
// 不必重新设计事件时序。
//
// 边界（**组件不碰 MQ**）：`session-new` / `data-session-title-changed` 的广播由本文件发出
// （与既有 emit 助手同源，payload 形状逐字不变，61 零变更）；删主 session 的**活动位补偿**
// 不在本文件：组件 `DeleteSession(主)` 只删目录行 + 库文件，`active_session_id` 由宿主调用点
// 用 `clearActiveIfMatches` 清（两库无跨库事务）。
package session

import (
	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
	"github.com/chonkpilot/chonkpilot-data/internal/sessionstore"
	"github.com/chonkpilot/chonkpilot-data/internal/storeadapter"
	"github.com/lsong98sh/chonkpilot-agent/session/component"
)

// componentBridge 打开会话组件（存储 = 本模块的 bbolt 适配器；同一 prjusr 路径 → 同源数据）。
func componentBridge(prj *data.DB) (*component.Component, error) {
	return component.New(component.Options{Store: storeadapter.New(prj.Path())})
}

// componentEnsureSession 是 `sessionEnsureInDialog` 的**组件版**（同一语义）：
// 调 `component.EnsureSession`（已存在不覆盖、缺失写一行、不建库），
// **只有 `Created`（首次落库）才广播 `session-new`**（幂等二次调用不重复广播）。
func (s *Service) componentEnsureSession(prj *data.DB, req facade.SessionEnsureRequest) (facade.SessionEnsureResponse, error) {
	comp, err := componentBridge(prj)
	if err != nil {
		return facade.SessionEnsureResponse{}, err
	}
	res, err := comp.EnsureSession(component.EnsureSessionCmd{
		DialogID:        req.DialogID,
		SessionID:       req.SessionID,
		ParentSessionID: req.ParentSessionID,
		Title:           req.Title,
		Kind:            req.Kind,
		SessionAttrs: component.SessionAttrs{
			Lifecycle:   req.Lifecycle,
			Agent:       req.Agent,
			ScenarioID:  req.ScenarioID,
			LLM:         req.LLM,
			Purpose:     req.Purpose,
			Goal:        req.Goal,
			Playbook:    req.Playbook,
			Convergence: req.Convergence,
			Origin:      req.Origin,
		},
	})
	if err != nil {
		return facade.SessionEnsureResponse{}, err
	}
	if res.Created {
		s.emitSessionNew(req.SessionID, req.ParentSessionID, req.InstanceID, res.DialogID)
	}
	return facade.SessionEnsureResponse{OK: true, DialogID: res.DialogID}, nil
}

// componentSetTitle 是 `sessionTitleInDialog` 的**组件版**：组件无独立 `SetTitle`，
// **既有能力 = `RenameSession`**（已覆盖「主 session 同步 dialogs.title、子会话不同步」，
// 并额外做标题校验：≤128 字符、不含控制字符）→ 写成功后广播 `data-session-title-changed`。
func (s *Service) componentSetTitle(prj *data.DB, req facade.SessionTitleRequest) (facade.SessionTitleResponse, error) {
	comp, err := componentBridge(prj)
	if err != nil {
		return facade.SessionTitleResponse{}, err
	}
	if err := comp.RenameSession(component.RenameSessionCmd{
		DialogID:  req.DialogID,
		SessionID: req.SessionID,
		Title:     req.Title,
	}); err != nil {
		return facade.SessionTitleResponse{}, err
	}
	s.emitSessionTitleChanged(req.SessionID, req.Title, req.DialogID)
	return facade.SessionTitleResponse{OK: true}, nil
}

// componentTurnHistory 是 `TurnHistory` 的**组件版**（P4-2 批次 A）：分页/选择逻辑归组件，
// 视图整形（`wire.TurnFromWire` / `messageViewToFacade`）留在本文件 —— 与改前**逐字等价**
// （同一组 kernel helper、同一 `brief=true` 口径、空结果时 Messages 仍为 nil）。
func (s *Service) componentTurnHistory(prj *data.DB, req facade.TurnHistoryRequest) (facade.TurnHistoryResponse, error) {
	comp, err := componentBridge(prj)
	if err != nil {
		return facade.TurnHistoryResponse{}, err
	}
	res, err := comp.TurnHistory(component.TurnHistoryCmd{
		DialogID:       req.DialogID,
		SessionID:      req.SessionID,
		BeforeTurnID:   req.BeforeTurnID,
		TargetMessages: req.TargetMessages,
		TargetBytes:    req.TargetBytes,
	})
	if err != nil {
		return facade.TurnHistoryResponse{}, err
	}
	resp := facade.TurnHistoryResponse{Turns: make([]facade.Turn, 0, len(res.Turns)), HasMore: res.HasMore}
	for _, t := range res.Turns {
		resp.Turns = append(resp.Turns, wire.TurnFromWire(kernel.RecordView(data.Record(t), "turn_id")))
	}
	for _, m := range res.Messages {
		resp.Messages = append(resp.Messages, messageViewToFacade(data.Record(m), true))
	}
	return resp, nil
}

// componentCleanupStale 是 `TurnCleanupStale` 的**组件版**（P4-2 批次 A）：
// 逐库调用组件 `CleanupStale`（目录库 + 每个对话库；单库打不开 → 跳过不阻断，同既有口径），
// 返回被标记 interrupted 的轮次数总和（`count` 语义不变）。
func (s *Service) componentCleanupStale(prj *data.DB) int {
	comp, err := componentBridge(prj)
	if err != nil {
		if s.Warnf != nil {
			s.Warnf("session: 打开会话组件失败（stale 清理跳过）: %v", err)
		}
		return 0
	}
	n := 0
	if cnt, err := comp.CleanupStale(component.CleanupStaleCmd{}); err != nil {
		if s.Warnf != nil {
			s.Warnf("session: 目录库 stale 清理失败: %v", err)
		}
	} else {
		n += cnt
	}
	files, err := sessionstore.ListDialogFiles(prj.Path())
	if err != nil {
		if s.Warnf != nil {
			s.Warnf("session: 枚举对话库失败（stale 清理仅覆盖部分库）: %v", err)
		}
		return n
	}
	for _, f := range files {
		dialogID, ok := data.DialogIDFromFile(f)
		if !ok {
			continue
		}
		cnt, err := comp.CleanupStale(component.CleanupStaleCmd{DialogID: dialogID})
		if err != nil {
			continue // 打不开的库跳过、不阻断其它对话（与既有 forEachDialogDB 同口径）
		}
		n += cnt
	}
	return n
}
