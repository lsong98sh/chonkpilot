// 生命周期扩展点（MOD-1 M1c）：Hook + LifecycleManager。
//
// 定位：chat 内核只保留核心能力（插话帧栈 / 工具循环 / 续写编排）；登录、配置建议、
// 群聊调度、记忆、计费等**非核心能力一律以 Hook 挂载**，内核不认识它们。
//
// 与既有 filter chain 的对齐（复用既有实现，不新造插件体系；由宿主 adapter 做包装）：
//
//	llm.before_start  → BeforeTurn
//	llm.before_send   → BeforeStep
//	llm.before_simple → BeforeStep（simple 子轮次，同样带 ExecContext）
//	tool.before_call  → BeforeTool
//
// 触发规则（由 Lifecycle 分发器强制，不依赖调用方自觉）：
//
//	BeforeTurn / AfterTurn：仅主帧（插入帧 FrameKind=insert → 不触发；插话不是新 turn）
//	BeforeStep / AfterStep / BeforeTool / AfterTool：主帧 + 插入帧都触发（带 ExecContext）
//
// 硬约束：Hook 非阻塞；顺序按 priority 稳定排序；失败策略注册时声明；
// 默认 fail-open，fail-closed 必须显式声明；After* 必达（Before* 返回 Drop 时调用方仍须调 After*）。
package chat

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
)

// Action 是 Before* 钩子的流控结果。
type Action int

const (
	// ActionContinue 继续当前节点。
	ActionContinue Action = iota
	// ActionDrop 跳过当前节点（BeforeStep = 不执行本 step；BeforeTool = 不执行该工具；
	// BeforeTurn = 不开轮）。After* 仍须执行（清理语义）。
	ActionDrop
)

// FailPolicy 是 Hook 失败策略（注册时声明；默认 FailOpen）。
type FailPolicy int

const (
	// FailOpen 失败不阻断主流程（记录日志后继续）。
	FailOpen FailPolicy = iota
	// FailClosed 失败即阻断（Before* → 视为 Drop；After* → 返回错误）。
	FailClosed
)

// FrameKind 取值（ExecContext.FrameKind）。
const (
	FrameMain   = "main"   // 主帧（空值等价 main）
	FrameInsert = "insert" // 插入帧（FrameID = inbox item id）
)

// ExecContext 描述「当前在哪个 turn 的哪个帧」。
type ExecContext struct {
	TurnID    string // 同一 turn（插话不产生新 turn）
	FrameID   string // 主帧为空；插入帧 = inbox item id
	FrameKind string // main | insert（空 = main）
	Depth     int    // 插入深度（主帧 0）
}

// IsInsert 报告是否插入帧。
func (c *ExecContext) IsInsert() bool { return c != nil && c.FrameKind == FrameInsert }

// TurnInfo 是 turn 级钩子的入参（BeforeTurn）。
type TurnInfo struct {
	InstanceID string
	SessionID  string
	TurnID     string
}

// TurnResult 是 turn 终态汇总（AfterTurn；单聊 / 群聊差异由宿主策略承担）。
type TurnResult struct {
	SessionID string
	TurnID    string
	FinalText string // 完整最终文本
	Messages  []Message
	ToolCalls []ToolCallRecord
	Finish    string // stop | length | error | canceled
	Usage     Usage
	Frames    int // 插入帧数
}

// ToolCallRecord 是一次工具调用的记录（TurnResult 汇总用）。
type ToolCallRecord struct {
	CallID string
	Name   string
	Result ToolResult
}

// Usage 是一次 step 的 token 用量。
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// StepRequest 是一次 LLM 请求（chat 自有；宿主 adapter 转 router.Request）。
type StepRequest struct {
	InstanceID string
	SessionID  string
	TurnID     string
	Model      string
	Messages   []Message
	Tools      []ToolSpec
}

// StepResult 是一次 step 的汇总结果（AfterStep）。
type StepResult struct {
	Text      string
	Reasoning string
	ToolCalls []ToolCall
	Finish    string // stop | length | tool_calls | error | canceled
	Usage     Usage
	Err       error
}

// Hook 是单个生命周期节点的回调（6 个 = 3 组 Before/After）。
type Hook interface {
	BeforeTurn(ctx context.Context, t *TurnInfo) (Action, error)
	AfterTurn(ctx context.Context, r *TurnResult) error
	BeforeStep(ctx context.Context, c *ExecContext, req *StepRequest) (Action, error)
	AfterStep(ctx context.Context, c *ExecContext, res *StepResult) error
	BeforeTool(ctx context.Context, c *ExecContext, call *ToolCall) (Action, error)
	AfterTool(ctx context.Context, c *ExecContext, res *ToolResult) error
}

// LifecycleManager 是注册入口（宿主挂载 Hook）。
type LifecycleManager interface {
	Register(h Hook, priority int, policy FailPolicy)
}

// Lifecycle 是分发器：注册 / 稳定排序 / 失败策略 / 帧区分。
// 实现方 = chat（纯内存、零 MQ）；宿主只调用 Register 与各节点方法。
type Lifecycle interface {
	LifecycleManager
	// BeforeTurn 仅主帧执行；插入帧直接返回 ActionContinue（插话不是新 turn）。
	BeforeTurn(ctx context.Context, c *ExecContext, t *TurnInfo) (Action, error)
	// AfterTurn 仅主帧执行；插入帧直接返回 nil。
	AfterTurn(ctx context.Context, c *ExecContext, r *TurnResult) error
	BeforeStep(ctx context.Context, c *ExecContext, req *StepRequest) (Action, error)
	AfterStep(ctx context.Context, c *ExecContext, res *StepResult) error
	BeforeTool(ctx context.Context, c *ExecContext, call *ToolCall) (Action, error)
	AfterTool(ctx context.Context, c *ExecContext, res *ToolResult) error
}

// lifecycleEntry 是一条注册记录。
type lifecycleEntry struct {
	hook     Hook
	priority int
	policy   FailPolicy
	order    int
}

// lifecycle 是 Lifecycle 的实现：priority 升序（同 priority 按注册序）。
type lifecycle struct {
	mu      sync.RWMutex
	entries []lifecycleEntry
	next    int
	log     *slog.Logger
}

// NewLifecycle 构造分发器（logger nil → slog.Default()）。
func NewLifecycle(logger *slog.Logger) Lifecycle {
	if logger == nil {
		logger = slog.Default()
	}
	return &lifecycle{log: logger}
}

// Register 注册 Hook（priority 升序执行；同 priority 按注册序）。
// nil hook 忽略；policy 缺省 FailOpen。
func (l *lifecycle) Register(h Hook, priority int, policy FailPolicy) {
	if h == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, lifecycleEntry{hook: h, priority: priority, policy: policy, order: l.next})
	l.next++
	sort.SliceStable(l.entries, func(i, j int) bool {
		return l.entries[i].priority < l.entries[j].priority
	})
}

// snapshot 返回稳定序快照（注册后不再变更顺序）。
func (l *lifecycle) snapshot() []lifecycleEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]lifecycleEntry(nil), l.entries...)
}

// BeforeTurn 主帧才触发；插入帧 no-op。
func (l *lifecycle) BeforeTurn(ctx context.Context, c *ExecContext, t *TurnInfo) (Action, error) {
	if c.IsInsert() {
		return ActionContinue, nil
	}
	for _, e := range l.snapshot() {
		act, err := e.hook.BeforeTurn(ctx, t)
		if err != nil {
			if e.policy == FailClosed {
				return ActionDrop, fmt.Errorf("hook BeforeTurn (priority=%d): %w", e.priority, err)
			}
			l.log.Warn("hook BeforeTurn failed (fail-open)", "priority", e.priority, "err", err)
			continue
		}
		if act == ActionDrop {
			return ActionDrop, nil
		}
	}
	return ActionContinue, nil
}

// AfterTurn 主帧才触发；插入帧 no-op。
func (l *lifecycle) AfterTurn(ctx context.Context, c *ExecContext, r *TurnResult) error {
	if c.IsInsert() {
		return nil
	}
	for _, e := range l.snapshot() {
		if err := e.hook.AfterTurn(ctx, r); err != nil {
			if e.policy == FailClosed {
				return fmt.Errorf("hook AfterTurn (priority=%d): %w", e.priority, err)
			}
			l.log.Warn("hook AfterTurn failed (fail-open)", "priority", e.priority, "err", err)
		}
	}
	return nil
}

// BeforeStep 主帧 + 插入帧都触发（ExecContext 由调用方给出，宿主可自行过滤）。
func (l *lifecycle) BeforeStep(ctx context.Context, c *ExecContext, req *StepRequest) (Action, error) {
	for _, e := range l.snapshot() {
		act, err := e.hook.BeforeStep(ctx, c, req)
		if err != nil {
			if e.policy == FailClosed {
				return ActionDrop, fmt.Errorf("hook BeforeStep (priority=%d): %w", e.priority, err)
			}
			l.log.Warn("hook BeforeStep failed (fail-open)", "priority", e.priority, "err", err)
			continue
		}
		if act == ActionDrop {
			return ActionDrop, nil
		}
	}
	return ActionContinue, nil
}

// AfterStep 主帧 + 插入帧都触发。
func (l *lifecycle) AfterStep(ctx context.Context, c *ExecContext, res *StepResult) error {
	for _, e := range l.snapshot() {
		if err := e.hook.AfterStep(ctx, c, res); err != nil {
			if e.policy == FailClosed {
				return fmt.Errorf("hook AfterStep (priority=%d): %w", e.priority, err)
			}
			l.log.Warn("hook AfterStep failed (fail-open)", "priority", e.priority, "err", err)
		}
	}
	return nil
}

// BeforeTool 主帧 + 插入帧都触发。
func (l *lifecycle) BeforeTool(ctx context.Context, c *ExecContext, call *ToolCall) (Action, error) {
	for _, e := range l.snapshot() {
		act, err := e.hook.BeforeTool(ctx, c, call)
		if err != nil {
			if e.policy == FailClosed {
				return ActionDrop, fmt.Errorf("hook BeforeTool (priority=%d): %w", e.priority, err)
			}
			l.log.Warn("hook BeforeTool failed (fail-open)", "priority", e.priority, "err", err)
			continue
		}
		if act == ActionDrop {
			return ActionDrop, nil
		}
	}
	return ActionContinue, nil
}

// AfterTool 主帧 + 插入帧都触发。
func (l *lifecycle) AfterTool(ctx context.Context, c *ExecContext, res *ToolResult) error {
	for _, e := range l.snapshot() {
		if err := e.hook.AfterTool(ctx, c, res); err != nil {
			if e.policy == FailClosed {
				return fmt.Errorf("hook AfterTool (priority=%d): %w", e.priority, err)
			}
			l.log.Warn("hook AfterTool failed (fail-open)", "priority", e.priority, "err", err)
		}
	}
	return nil
}
