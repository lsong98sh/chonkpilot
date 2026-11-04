// `next_run` 自动续跑计数器（`sessions.next_run_count`）门面实现。
//
// 设计见 docs/next-run/next_run自动续跑设计方案.md §3.3：
//   - 计数 = 会话行可选列（**仅 > 0 才写键**，普通会话行形状不变）；
//   - 上限判定 + 自增在**同一 bbolt 写事务**内完成（单事务 RMW，避免并发绕过）；
//   - 用户发送即重置 0；每次成功受理 +1；inbox 落库失败 → 补偿 -1。
package session

import (
	"errors"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// next_run 计数器的存储语义（判上限 + 自增 / 归零 / 减一）实现唯一 = 会话组件
// （`component.NextRun*`，P4-2 批次 B，2026-10-31）；本包只保留输入校验。

// SessionNextRunCheckAndAdd 判上限 + 自增（单事务）。已达上限 / 关闭（max<=0）→ 不增。
//
// P4-2 批次 B（2026-10-31）：实现体切到会话组件（`componentNextRunCheckAndAdd`）。
func (s *Service) SessionNextRunCheckAndAdd(req facade.SessionNextRunCheckAndAddRequest) (facade.SessionNextRunCheckAndAddResponse, error) {
	if req.SessionID == "" {
		return facade.SessionNextRunCheckAndAddResponse{}, errors.New("session: id required")
	}
	if req.DialogID == "" {
		return facade.SessionNextRunCheckAndAddResponse{}, kernel.ErrDialogRequired
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.SessionNextRunCheckAndAddResponse{}, err
	}
	return s.componentNextRunCheckAndAdd(prj, req)
}

// SessionNextRunReset 计数器归零（会话不存在 → 幂等成功）。
func (s *Service) SessionNextRunReset(req facade.SessionNextRunResetRequest) (facade.SessionNextRunResetResponse, error) {
	if req.SessionID == "" {
		return facade.SessionNextRunResetResponse{}, errors.New("session: id required")
	}
	if req.DialogID == "" {
		return facade.SessionNextRunResetResponse{}, kernel.ErrDialogRequired
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.SessionNextRunResetResponse{}, err
	}
	return s.componentNextRunReset(prj, req)
}

// SessionNextRunGet 读当前计数（会话不存在 / 未写键 → 0）。
func (s *Service) SessionNextRunGet(req facade.SessionNextRunGetRequest) (facade.SessionNextRunGetResponse, error) {
	if req.SessionID == "" {
		return facade.SessionNextRunGetResponse{}, errors.New("session: id required")
	}
	if req.DialogID == "" {
		return facade.SessionNextRunGetResponse{}, kernel.ErrDialogRequired
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.SessionNextRunGetResponse{}, err
	}
	return s.componentNextRunGet(prj, req)
}

// SessionNextRunDec 计数器补偿 -1（inbox 落库失败回滚本次自增；不小于 0）。
func (s *Service) SessionNextRunDec(req facade.SessionNextRunDecRequest) (facade.SessionNextRunDecResponse, error) {
	if req.SessionID == "" {
		return facade.SessionNextRunDecResponse{}, errors.New("session: id required")
	}
	if req.DialogID == "" {
		return facade.SessionNextRunDecResponse{}, kernel.ErrDialogRequired
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.SessionNextRunDecResponse{}, err
	}
	return s.componentNextRunDec(prj, req)
}
