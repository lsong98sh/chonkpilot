// 运行期 turn 的最小生命周期接口（A4-P3a-0，2026-10-20）：
// 仅供 `s.turns` 容器 / 会话查找 / 实例退出等**只关心身份与关闭**的路径使用，
// 使后续 engineTurn 可与 turnCtx 并列；需要内部字段的旧路径继续用 *turnCtx 类型化助手。
//
// 行为零变更：现有 *turnCtx 实现两接口，类型化助手（lookupTurnLocked / turnBySessionLocked 等）
// 内部做类型断言，命中即与原实现逐字节等价；断言失败分支在现行代码路径不可达（防御性留痕）。
package server

// turnSession 是运行期 turn 的最小生命周期句柄（P3a-0）：
// 身份三键（instance / session / turn）+ 关闭。
type turnSession interface {
	InstanceID() string
	SessionID() string
	TurnID() string
	Close()
}

// turnInput 是投递输入的句柄（llm-send / inbox 消费路径）。
type turnInput interface {
	turnSession
	FeedText(content string)
	FeedContinue(content string)
	FeedNotify(content string)
	FeedToolResult(toolCallID, content string)
	FeedToolResultStatus(toolCallID, content, status string)
	Kick()
}

// 编译期断言：turnCtx 同时满足生命周期与输入投递接口。
var (
	_ turnSession = (*turnCtx)(nil)
	_ turnInput   = (*turnCtx)(nil)
)
