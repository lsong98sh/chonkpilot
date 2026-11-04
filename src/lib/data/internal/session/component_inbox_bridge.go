// component_inbox_bridge.go —— P4-2 批次 B（2026-10-31）：收件箱与 next_run 计数器的组件调用桥。
//
// 分工（与批次 A 同口径）：**存储语义归组件**（单次扫描 / 单事务 RMW / 幂等键）；
// **输入校验与 DTO 整形留门面**（错误文案、`facade.InboxItem` 字段映射、消息行构造）。
package session

import (
	"errors"
	"fmt"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
	"github.com/lsong98sh/chonkpilot-agent/session/component"
)

// componentInboxAppend 是 `InboxAppend` 的组件版（上限 / 幂等命中语义由组件判定）。
func (s *Service) componentInboxAppend(prj *data.DB, req facade.InboxAppendRequest) (facade.InboxAppendResponse, error) {
	comp, err := componentBridge(prj)
	if err != nil {
		return facade.InboxAppendResponse{}, err
	}
	id, err := comp.InboxAppend(component.InboxAppendCmd{
		DialogID:       req.DialogID,
		SessionID:      req.SessionID,
		TurnID:         req.TurnID,
		Type:           req.Type,
		Content:        req.Content,
		LLM:            req.LLM,
		Think:          req.Think,
		Effort:         req.Effort,
		ScenarioID:     req.ScenarioID,
		IdempotencyKey: req.IdempotencyKey,
		Source:         req.Source,
	})
	if err != nil {
		if errors.Is(err, component.ErrInboxFull) {
			return facade.InboxAppendResponse{}, errors.New("inbox: full（每会话 pending 上限 50）")
		}
		return facade.InboxAppendResponse{}, err
	}
	return facade.InboxAppendResponse{OK: true, ID: id}, nil
}

// componentInboxList 是 `InboxList` 的组件版：组件回原始行，字段映射留在本函数。
func (s *Service) componentInboxList(prj *data.DB, req facade.InboxListRequest) (facade.InboxListResponse, error) {
	comp, err := componentBridge(prj)
	if err != nil {
		return facade.InboxListResponse{}, err
	}
	recs, err := comp.InboxList(component.InboxListCmd{
		DialogID:  req.DialogID,
		SessionID: req.SessionID,
		Status:    req.Status,
	})
	if err != nil {
		return facade.InboxListResponse{}, err
	}
	out := make([]facade.InboxItem, 0, len(recs))
	for _, r := range recs {
		out = append(out, facade.InboxItem{
			ID:             kernel.Sval(r[data.KeyField]),
			SessionID:      kernel.Sval(r["session_id"]),
			TurnID:         kernel.Sval(r["turn_id"]),
			Type:           kernel.Sval(r["type"]),
			LLM:            kernel.Sval(r["llm"]),
			Content:        kernel.Sval(r["content"]),
			Think:          r["think"],
			Effort:         r["effort"],
			ScenarioID:     kernel.Sval(r["scenario_id"]),
			Status:         kernel.Sval(r["status"]),
			IdempotencyKey: kernel.Sval(r["idempotency_key"]),
			CreatedAt:      kernel.Sval(r["created_at"]),
			Source:         kernel.Sval(r["source"]),
		})
	}
	return facade.InboxListResponse{OK: true, Items: out}, nil
}

// componentInboxCommit 是 `InboxCommit` 的组件版（消息行由门面构造后传入）。
func (s *Service) componentInboxCommit(prj *data.DB, req facade.InboxCommitRequest, rec data.Record) (facade.InboxCommitResponse, error) {
	comp, err := componentBridge(prj)
	if err != nil {
		return facade.InboxCommitResponse{}, err
	}
	key, err := comp.InboxCommit(component.InboxCommitCmd{
		DialogID:  req.DialogID,
		InboxID:   req.InboxID,
		TurnID:    req.TurnID,
		SessionID: req.SessionID,
		Message:   rec,
	})
	if err != nil {
		switch {
		case errors.Is(err, component.ErrInboxNotFound):
			return facade.InboxCommitResponse{}, errors.New("inbox: not found")
		case errors.Is(err, component.ErrInboxDiscarded):
			return facade.InboxCommitResponse{}, errors.New("inbox: discarded")
		}
		return facade.InboxCommitResponse{}, err
	}
	return facade.InboxCommitResponse{OK: true, ID: key}, nil
}

// componentInboxUpdate 是 `InboxUpdate` 的组件版（字段白名单校验留门面）。
func (s *Service) componentInboxUpdate(prj *data.DB, req facade.InboxUpdateRequest) (facade.InboxUpdateResponse, error) {
	comp, err := componentBridge(prj)
	if err != nil {
		return facade.InboxUpdateResponse{}, err
	}
	if err := comp.InboxUpdate(component.InboxUpdateCmd{
		DialogID: req.DialogID,
		ID:       req.ID,
		Status:   req.Status,
		Type:     req.Type,
		TurnID:   req.TurnID,
		Content:  req.Content,
	}); err != nil {
		return facade.InboxUpdateResponse{}, err
	}
	return facade.InboxUpdateResponse{OK: true}, nil
}

// componentNextRunCheckAndAdd 是 `SessionNextRunCheckAndAdd` 的组件版。
func (s *Service) componentNextRunCheckAndAdd(prj *data.DB, req facade.SessionNextRunCheckAndAddRequest) (facade.SessionNextRunCheckAndAddResponse, error) {
	comp, err := componentBridge(prj)
	if err != nil {
		return facade.SessionNextRunCheckAndAddResponse{}, err
	}
	res, err := comp.NextRunCheckAndAdd(component.NextRunCheckAndAddCmd{
		DialogID:  req.DialogID,
		SessionID: req.SessionID,
		Max:       req.Max,
	})
	if err != nil {
		return facade.SessionNextRunCheckAndAddResponse{}, err
	}
	if !res.Found {
		return facade.SessionNextRunCheckAndAddResponse{}, fmt.Errorf("session not found: %s", req.SessionID)
	}
	return facade.SessionNextRunCheckAndAddResponse{Count: res.Count, Allowed: res.Allowed}, nil
}

// componentNextRunReset 是 `SessionNextRunReset` 的组件版。
func (s *Service) componentNextRunReset(prj *data.DB, req facade.SessionNextRunResetRequest) (facade.SessionNextRunResetResponse, error) {
	comp, err := componentBridge(prj)
	if err != nil {
		return facade.SessionNextRunResetResponse{}, err
	}
	if err := comp.NextRunReset(component.NextRunResetCmd{
		DialogID: req.DialogID, SessionID: req.SessionID,
	}); err != nil {
		return facade.SessionNextRunResetResponse{}, err
	}
	return facade.SessionNextRunResetResponse{OK: true}, nil
}

// componentNextRunGet 是 `SessionNextRunGet` 的组件版。
func (s *Service) componentNextRunGet(prj *data.DB, req facade.SessionNextRunGetRequest) (facade.SessionNextRunGetResponse, error) {
	comp, err := componentBridge(prj)
	if err != nil {
		return facade.SessionNextRunGetResponse{}, err
	}
	count, err := comp.NextRunGet(component.NextRunGetCmd{
		DialogID: req.DialogID, SessionID: req.SessionID,
	})
	if err != nil {
		return facade.SessionNextRunGetResponse{}, err
	}
	return facade.SessionNextRunGetResponse{Count: count}, nil
}

// componentNextRunDec 是 `SessionNextRunDec` 的组件版。
func (s *Service) componentNextRunDec(prj *data.DB, req facade.SessionNextRunDecRequest) (facade.SessionNextRunDecResponse, error) {
	comp, err := componentBridge(prj)
	if err != nil {
		return facade.SessionNextRunDecResponse{}, err
	}
	count, err := comp.NextRunDec(component.NextRunDecCmd{
		DialogID: req.DialogID, SessionID: req.SessionID,
	})
	if err != nil {
		return facade.SessionNextRunDecResponse{}, err
	}
	return facade.SessionNextRunDecResponse{Count: count}, nil
}
