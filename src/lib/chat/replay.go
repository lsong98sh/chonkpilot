// L1 重放（MOD-1 M8-C1）：按事件日志重建 TurnState。
//
// 语义与 `Apply` 完全一致（同一转移函数、同一失败判据），本层只负责：
//   - 从初始状态（pending）起步，逐条推进 `Seq` 游标；
//   - 空事件 → 初始状态（pending）；
//   - 不修改入参事件切片；
//   - 任何不可应用的事件（未知类型 / `Seq` 非单调 / 版本不符 / 载荷非法）→ 返回错误，
//     **不静默跳过**（重放不得"看似成功、实际丢步"）。
package chat

import "fmt"

// Replay 按事件日志重建 TurnState（L1 重放）：与 Apply 同一语义；
// 遇到未知事件 / Seq 不单调 / 版本不符 → 返回错误（不静默跳过）。
func Replay(sessionID, turnID string, events []TurnEvent) (TurnState, error) {
	st := NewTurnState(sessionID, turnID)
	for i, ev := range events {
		next, err := Apply(st, ev)
		if err != nil {
			return TurnState{}, fmt.Errorf("chat: replay event[%d] %s seq=%d: %w", i, ev.Type, ev.Seq, err)
		}
		st = next
	}
	return st, nil
}
