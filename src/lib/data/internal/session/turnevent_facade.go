// TURN EVENT 门面实现（MOD-1 M3；prjusr 级 `turn_events` 表）。
//
// 主键 `<turn_id>/<seq 12 位零填充>`：同 turn 事件按 Seq 顺序连续存放，前缀扫描即可重放。
// 写入幂等（同 turn + seq 视为成功；type 不一致 → 失败），避免网络重试产生重复行。
// 清理仅针对**终态且超过保留天数**的 turn（running / pending 的事件永不按时间删）。
package session

import (
	"errors"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// 事件表在目录库（prjusr）；主键 = `<turn_id>/<seq 12 位零填充>`（实现唯一 = 会话组件）。
const turnEventTable = "turn_events"

// 编译期断言：Service 实现 TURN EVENT 门面面。
var _ facade.TurnEventAPI = (*Service)(nil)

// terminalTurnStatus 报告 turns.status 是否终态（与 llm 侧实际写入值对齐）。
func terminalTurnStatus(st string) bool {
	switch st {
	case "done", "complete", "completed", "error", "failed", "interrupted", "canceled":
		return true
	}
	return false
}

// TurnEventAppend 追加一条事件。
func (s *Service) TurnEventAppend(req facade.TurnEventAppendRequest) (facade.TurnEventAppendResponse, error) {
	if strings.TrimSpace(req.TurnID) == "" {
		return facade.TurnEventAppendResponse{}, errors.New("turn-event: turn_id required")
	}
	if strings.Contains(req.TurnID, "/") {
		return facade.TurnEventAppendResponse{}, errors.New("turn-event: turn_id must not contain '/'")
	}
	if req.Seq == 0 {
		return facade.TurnEventAppendResponse{}, errors.New("turn-event: seq must be > 0")
	}
	if strings.TrimSpace(req.Type) == "" {
		return facade.TurnEventAppendResponse{}, errors.New("turn-event: type required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.TurnEventAppendResponse{}, err
	}
	return s.componentTurnEventAppend(prj, req)
}

// TurnEventCleanup 删除终态且超过保留天数的 turn 的事件。
func (s *Service) TurnEventCleanup(req facade.TurnEventCleanupRequest) (facade.TurnEventCleanupResponse, error) {
	if req.RetentionDays <= 0 { // 0 = 永久；<0 = 不清理
		return facade.TurnEventCleanupResponse{OK: true}, nil
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.TurnEventCleanupResponse{}, err
	}
	cutoff := time.Now().UTC().Add(-time.Duration(req.RetentionDays) * 24 * time.Hour)

	// ① 找出「终态 + 行更新时间早于 cutoff」的 turn。
	//
	// 一树一库（P3-4 残留回填）：`turns` 在对话库内 → 逐库收集；prjusr 仍扫一遍
	// （存量库遗留行一并收敛）。`turn_events` 表本身仍在 prjusr。
	oldTurns := map[string]bool{}
	collect := func(db *data.DB) error {
		return db.Table("turns").ForEach(func(k string, rec data.Record) error {
			if !terminalTurnStatus(kernel.Sval(rec["status"])) {
				return nil
			}
			ts := kernel.Sval(rec["updated_at"])
			if ts == "" {
				ts = kernel.Sval(rec["created_at"])
			}
			t, err := time.Parse(time.RFC3339Nano, ts)
			if err != nil || !t.Before(cutoff) {
				return nil // 时间不可解析 → 保守保留
			}
			oldTurns[k] = true
			return nil
		})
	}
	if err := collect(prj); err != nil {
		return facade.TurnEventCleanupResponse{}, err
	}
	// S-2（DATA-OPT-1 P3）：跨对话库枚举复用 forEachDialogDB（打不开的库跳过、错误上抛口径不变）。
	if _, err := s.forEachDialogDB(prj, func(_ string, db *data.DB) (bool, error) {
		return false, collect(db)
	}); err != nil {
		return facade.TurnEventCleanupResponse{}, err
	}
	if len(oldTurns) == 0 {
		return facade.TurnEventCleanupResponse{OK: true}, nil
	}

	// ② 删除这些 turn 的全部事件（单事务批删；失败整体回滚 → deleted 回 0）。
	deleted, err := s.componentTurnEventCleanup(prj, oldTurns)
	if err != nil {
		return facade.TurnEventCleanupResponse{OK: false}, err
	}
	return facade.TurnEventCleanupResponse{OK: true, Deleted: deleted}, nil
}
