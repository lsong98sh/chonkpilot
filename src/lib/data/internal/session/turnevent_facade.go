// TURN EVENT 门面实现（MOD-1 M3；prjusr 级 `turn_events` 表）。
//
// 主键 `<turn_id>/<seq 12 位零填充>`：同 turn 事件按 Seq 顺序连续存放，前缀扫描即可重放。
// 写入幂等（同 turn + seq 视为成功；type 不一致 → 失败），避免网络重试产生重复行。
// 清理仅针对**终态且超过保留天数**的 turn（running / pending 的事件永不按时间删）。
package session

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

const turnEventTable = "turn_events"

// 编译期断言：Service 实现 TURN EVENT 门面面。
var _ facade.TurnEventAPI = (*Service)(nil)

// turnEventKey 生成事件主键（turn 内按 seq 字典序 = 数值序）。
func turnEventKey(turnID string, seq uint64) string {
	return fmt.Sprintf("%s/%012d", turnID, seq)
}

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

	key := turnEventKey(req.TurnID, req.Seq)
	var existing data.Record
	if ok, err := prj.Table(turnEventTable).Get(key, &existing); err != nil {
		return facade.TurnEventAppendResponse{}, err
	} else if ok {
		if got := kernel.Sval(existing["type"]); got != req.Type {
			return facade.TurnEventAppendResponse{}, fmt.Errorf("turn-event: seq %d already used by %q (want %q)", req.Seq, got, req.Type)
		}
		return facade.TurnEventAppendResponse{OK: true}, nil // 幂等
	}

	at := req.At
	if at == "" {
		at = time.Now().UTC().Format(time.RFC3339Nano)
	}
	rec := data.Record{
		"session_id": req.SessionID,
		"turn_id":    req.TurnID,
		"seq":        req.Seq,
		"type":       req.Type,
		"at":         at,
	}
	if len(req.Payload) > 0 {
		rec["payload"] = req.Payload
	}
	if err := prj.Table(turnEventTable).Insert(key, rec); err != nil {
		return facade.TurnEventAppendResponse{}, err
	}
	return facade.TurnEventAppendResponse{OK: true}, nil
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
	oldTurns := map[string]bool{}
	if err := prj.Table("turns").ForEach(func(k string, rec data.Record) error {
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
	}); err != nil {
		return facade.TurnEventCleanupResponse{}, err
	}
	if len(oldTurns) == 0 {
		return facade.TurnEventCleanupResponse{OK: true}, nil
	}

	// ② 删除这些 turn 的全部事件。
	var keys []string
	if err := prj.Table(turnEventTable).ForEach(func(k string, rec data.Record) error {
		if oldTurns[kernel.Sval(rec["turn_id"])] {
			keys = append(keys, k)
		}
		return nil
	}); err != nil {
		return facade.TurnEventCleanupResponse{}, err
	}
	deleted := 0
	for _, k := range keys {
		if err := prj.Table(turnEventTable).Delete(k); err != nil && !errors.Is(err, data.ErrNotFound) {
			return facade.TurnEventCleanupResponse{OK: false, Deleted: deleted}, err
		}
		deleted++
	}
	return facade.TurnEventCleanupResponse{OK: true, Deleted: deleted}, nil
}
