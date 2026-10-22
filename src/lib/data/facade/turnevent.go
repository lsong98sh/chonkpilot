// TURN EVENT（MOD-1 M3）数据面契约：prjusr 级 `turn_events` 表。
//
// 语义：TurnState 事件日志（单调 Seq，作用域 per-turn），供崩溃恢复 / 重放。
//   - 写入 = 每事件一行；主键 `<turn_id>/<seq 12 位零填充>` → 同 turn 顺序扫描；
//   - 幂等：同 (turn_id, seq) 重复写入视为成功（type 不一致 → 明确失败）；
//   - 清理：仅删除**终态且超过保留天数**的 turn 的事件（`RetentionDays=0` = 永久）。
package facade

import "encoding/json"

// TurnEventRow 是 turn_events 行（数据面形态）。
type TurnEventRow struct {
	SessionID string          `json:"session_id,omitempty"`
	TurnID    string          `json:"turn_id"`
	Seq       uint64          `json:"seq"`
	Type      string          `json:"type"`
	At        string          `json:"at,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// TurnEventAppendRequest 追加一条事件（幂等：同 turn + seq）。
type TurnEventAppendRequest struct {
	InstanceID string          `json:"instance_id"`
	SessionID  string          `json:"session_id,omitempty"`
	TurnID     string          `json:"turn_id"`
	Seq        uint64          `json:"seq"`
	Type       string          `json:"type"`
	At         string          `json:"at,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Scope      Scope           `json:"scope,omitempty"`
}

// TurnEventAppendResponse 追加结果。
type TurnEventAppendResponse struct {
	OK bool `json:"ok"`
}

// TurnEventCleanupRequest 清理终态 turn 的事件日志。
type TurnEventCleanupRequest struct {
	InstanceID string `json:"instance_id"`
	// RetentionDays 保留天数（<0 = 不清理；0 = 永久保留；>0 = 删除终态且早于 N 天的事件）。
	RetentionDays int   `json:"retention_days"`
	Scope         Scope `json:"scope,omitempty"`
}

// TurnEventCleanupResponse 清理结果。
type TurnEventCleanupResponse struct {
	OK      bool `json:"ok"`
	Deleted int  `json:"deleted"`
}
