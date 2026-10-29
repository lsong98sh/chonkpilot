// component_bridge.go —— P4-2 批次二（2026-10-28）：组件调用 + 事件映射的**薄适配注入点**。
//
// 目的：P4-2 批次三「逐项切 data 调用方」时，只需把 `sessionEnsureInDialog` /
// `sessionTitleInDialog` 的实现体换成下面两个函数（调用点、MQ 载荷、幂等语义全不变），
// 不必重新设计事件时序。
//
// 本轮边界（**只加能力，不切调用方**）：
//   - `sessionEnsureInDialog` / `sessionTitleInDialog` 的现有实现**保留不动**（并行实现不删）；
//   - 组件不碰 MQ：`session-new` / `data-session-title-changed` 的广播仍由本文件发出
//     （与既有 emit 助手同源，payload 形状逐字不变，61 零变更）；
//   - 删主 session 的**活动位补偿**不在本文件：组件 `DeleteSession(主)` 只删目录行 + 库文件，
//     `active_session_id` 由宿主调用点用 `clearActiveIfMatches` 清（两库无跨库事务）。
package session

import (
	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
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
//
// 待 P4-2 批次三切换：届时 `sessionEnsureInDialog` 的实现体改为直接调本函数。
func (s *Service) componentEnsureSession(prj *data.DB, req facade.SessionEnsureRequest) (facade.SessionEnsureResponse, error) {
	comp, err := componentBridge(prj)
	if err != nil {
		return facade.SessionEnsureResponse{}, err
	}
	res, err := comp.EnsureSession(component.EnsureSessionCmd{
		DialogID:        req.DialogID,
		SessionID:       req.SessionID,
		ParentSessionID: req.ParentSessionID,
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
//
// 待 P4-2 批次三切换：届时 `sessionTitleInDialog` 的实现体改为直接调本函数。
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
