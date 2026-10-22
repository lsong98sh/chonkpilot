// 端口（MOD-1 §3.1）：chat 内核只经这些接口与外界交互；实现方 = 宿主装配层（llm/server）。
package chat

import (
	"context"
	"time"
)

// ContextProvider 构建本轮已拼好的上下文（读库 + 压缩 + system prompt 拼装归宿主）。
// chat 不读库、不拼上下文、不压缩——只消费结果。
type ContextProvider interface {
	Build(ctx context.Context, req ContextRequest) (Context, error)
}

// ContextRequest 是本轮上下文的定位信息。
type ContextRequest struct {
	InstanceID string
	SessionID  string
	TurnID     string
}

// Context 是已拼好的本轮上下文。
type Context struct {
	SystemPrompt string    // 全局 / 目录 / 场景 / agent / 注入层（宿主拼装）
	Messages     []Message // 已压缩的历史（快照 + 最近 N 轮）
}

// TurnStore 是写侧端口：消息落库 + inbox 原子消费 + 事件日志。
type TurnStore interface {
	SaveMessage(ctx context.Context, msg Message) error
	// CommitInbox 在同一事务内落消息并置 inbox consumed（幂等：已 consumed 直接返回）。
	CommitInbox(ctx context.Context, inboxID string, msg Message) error
	// AppendEvent 追加 TurnState 事件（重放游标）；实现方可与 messages 同库不同表。
	AppendEvent(ctx context.Context, ev TurnEvent) error
}

// Inbox 是 turn 中途输入端口（插话 / 后续的 ask 回执、异步结果）。宿主实现队列与落库顺序。
// Poll 非阻塞：ok=false = 当前无输入。
//
// 引擎侧判定（M6f-1 / M6f-2）：
//   - `Type == InboxTypeInsert`：内容去空白为空 → 丢弃；帧深（`len(Frames)`）已达 maxInsertDepth →
//     不消费（留痕，由宿主持久化为 next_turn）；正常 → 经 TurnStore.CommitInbox 同事务落消息 +
//     置 consumed。
//   - `Type == InboxTypeToolResult` 且 `Result != nil`：仅在 `RuntimeConfig.PendingWait > 0` 的
//     轮内等待中消费；CallID 为空时补为当前待回填调用，不匹配当前调用 → 忽略并继续等待。
//   - 其余类型（含 `next_turn` / ask 回执）→ 引擎原样忽略（由宿主保留或丢弃）。
type Inbox interface {
	Poll(ctx context.Context) (InboxItem, bool, error)
}

// LLMClient 是单次 LLM step 端口（chat 与 router 的唯一接触面）。
// 报文级重试 / 超时 / 协议解析归 router；chat 只消费 StepEvent。
type LLMClient interface {
	Step(ctx context.Context, req StepRequest, opts StepOptions) (<-chan StepEvent, error)
}

// StepOptions 是单次 step 的传输层选项（报文级重试；语义级续写归 chat）。
type StepOptions struct {
	MaxRetries  *int                                  // nil = 宿主默认；&0 = 不重试；&n = n 次
	RetryBudget time.Duration                         // 0 = 宿主默认；<0 = 关闭预算
	OnRetry     func(attempt int, wait time.Duration) // 非阻塞上报（宿主转 EventLLMRetry）
}

// Clock 是时钟端口（续写宽限期 / 超时 / 事件时间；测试可注入）。
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// EventSink 是对外事件广播端口（只广播、不写库；消息落库走 TurnStore）。
// 实现必须非阻塞，且同一实例内按调用顺序投递（禁止静默丢事件）。
type EventSink interface {
	Emit(ctx context.Context, ev Event)
}

// ToolInvoker 是工具执行端口（同步 / 异步策略由宿主消化，chat 不做策略判断）。
// 执行期间的阶段变化经 EventSink（EventToolProgress）输出。
type ToolInvoker interface {
	Dispatch(ctx context.Context, call ToolCall) (ToolOutcome, error)
	Cancel(ctx context.Context, instanceID, opID string) (bool, error)
	Detach(ctx context.Context, instanceID, opID string) (string, error)
}

// ToolOutcome 是一次 Dispatch 的结果：Pending=true 表示稍后由宿主回填。
type ToolOutcome struct {
	Pending bool
	OpID    string     // Pending=true 时有效
	Result  ToolResult // Pending=false 时有效
}

// ToolCatalog 是目录视图（服务列表 / 工具列表；gateway 类型由宿主转换）。
type ToolCatalog interface {
	Servers(ctx context.Context, instanceID string) ([]ServerInfo, error)
	Tools(ctx context.Context, instanceID string) ([]ToolSpec, error)
}
