// `next_run` 自动续跑计数器（`sessions.next_run_count`）门面实现。
//
// 设计见 docs/next-run/next_run自动续跑设计方案.md §3.3：
//   - 计数 = 会话行可选列（**仅 > 0 才写键**，普通会话行形状不变）；
//   - 上限判定 + 自增在**同一 bbolt 写事务**内完成（单事务 RMW，避免并发绕过）；
//   - 用户发送即重置 0；每次成功受理 +1；inbox 落库失败 → 补偿 -1。
package session

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
)

// nextRunCountKey 是 sessions 行的计数器键名。
const nextRunCountKey = "next_run_count"

// SessionNextRunCheckAndAdd 判上限 + 自增（单事务）。已达上限 / 关闭（max<=0）→ 不增。
func (s *Service) SessionNextRunCheckAndAdd(req facade.SessionNextRunCheckAndAddRequest) (facade.SessionNextRunCheckAndAddResponse, error) {
	if req.SessionID == "" {
		return facade.SessionNextRunCheckAndAddResponse{}, errors.New("session: id required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.SessionNextRunCheckAndAddResponse{}, err
	}
	count, allowed, notFound := 0, false, false
	if err := prj.Table("sessions").UpdateIn(req.SessionID, func(rec data.Record) data.Record {
		if len(rec) == 0 {
			notFound = true
			return nil
		}
		cur := recordInt(rec[nextRunCountKey])
		if req.Max <= 0 || cur >= req.Max {
			count, allowed = cur, false
			return nil // 不写：判定与自增同事务，拒绝时不产生任何变更
		}
		cur++
		count, allowed = cur, true
		rec[nextRunCountKey] = cur
		return rec
	}); err != nil {
		return facade.SessionNextRunCheckAndAddResponse{}, err
	}
	if notFound {
		return facade.SessionNextRunCheckAndAddResponse{}, fmt.Errorf("session not found: %s", req.SessionID)
	}
	return facade.SessionNextRunCheckAndAddResponse{Count: count, Allowed: allowed}, nil
}

// SessionNextRunReset 计数器归零（会话不存在 → 幂等成功）。
func (s *Service) SessionNextRunReset(req facade.SessionNextRunResetRequest) (facade.SessionNextRunResetResponse, error) {
	if req.SessionID == "" {
		return facade.SessionNextRunResetResponse{}, errors.New("session: id required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.SessionNextRunResetResponse{}, err
	}
	if err := prj.Table("sessions").UpdateIn(req.SessionID, func(rec data.Record) data.Record {
		if len(rec) == 0 {
			return nil // 会话不存在 → 幂等成功（不建残行）
		}
		if recordInt(rec[nextRunCountKey]) == 0 {
			return nil // 已是 0：不写键（保持「仅 > 0 才写键」的行形状）
		}
		delete(rec, nextRunCountKey)
		return rec
	}); err != nil {
		return facade.SessionNextRunResetResponse{}, err
	}
	return facade.SessionNextRunResetResponse{OK: true}, nil
}

// SessionNextRunGet 读当前计数（会话不存在 / 未写键 → 0）。
func (s *Service) SessionNextRunGet(req facade.SessionNextRunGetRequest) (facade.SessionNextRunGetResponse, error) {
	if req.SessionID == "" {
		return facade.SessionNextRunGetResponse{}, errors.New("session: id required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.SessionNextRunGetResponse{}, err
	}
	var rec data.Record
	ok, err := prj.Table("sessions").Get(req.SessionID, &rec)
	if err != nil {
		return facade.SessionNextRunGetResponse{}, err
	}
	if !ok {
		return facade.SessionNextRunGetResponse{Count: 0}, nil
	}
	return facade.SessionNextRunGetResponse{Count: recordInt(rec[nextRunCountKey])}, nil
}

// SessionNextRunDec 计数器补偿 -1（inbox 落库失败回滚本次自增；不小于 0）。
func (s *Service) SessionNextRunDec(req facade.SessionNextRunDecRequest) (facade.SessionNextRunDecResponse, error) {
	if req.SessionID == "" {
		return facade.SessionNextRunDecResponse{}, errors.New("session: id required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.SessionNextRunDecResponse{}, err
	}
	count := 0
	if err := prj.Table("sessions").UpdateIn(req.SessionID, func(rec data.Record) data.Record {
		if len(rec) == 0 {
			return nil
		}
		cur := recordInt(rec[nextRunCountKey])
		if cur <= 0 {
			count = 0
			return nil
		}
		cur--
		count = cur
		if cur == 0 {
			delete(rec, nextRunCountKey) // 归零即删键（行形状与「从未计数」一致）
		} else {
			rec[nextRunCountKey] = cur
		}
		return rec
	}); err != nil {
		return facade.SessionNextRunDecResponse{}, err
	}
	return facade.SessionNextRunDecResponse{Count: count}, nil
}

// recordInt 解析会话行的数值列（bbolt JSON 回读可能是 float64 / int / 字符串）。
func recordInt(v any) int {
	switch t := v.(type) {
	case nil:
		return 0
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}
