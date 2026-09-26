// Package mq 是进程内消息总线（对称 promise 语义，对齐 61-消息一览 §9）。
//
// 2026-09-03 决策：去掉 NATS，mq 仅进程内内存实现；多进程去中心化 mq 留待
// 功能稳定后自研（桥 = 传输门面，为将来预留）。
//
// 主题前缀（2026-09-03 增补）：命名空间前缀（如 "chonk."）只在总线初始化时以
// Options.Prefix 注入一次。业务 publish/subscribe 一律使用**相对主题**
// （如 "session-start"、"tool-call-reply"），总线内部自动补前缀用于注册/匹配；
// handler 收到的 subject 统一为去前缀后的相对形式。
// 兼容保护：若传入主题已带该前缀（如遗留全主题 chonk.srv.notify.data-*）则原样
// 透传、不重复加前缀，少量遗留全主题调用方可继续工作。
//
// 内核语义（Go/JS 对称）：
//
//	Emit(ctx, subject, payload) *Future   —— 统一入口：按 order 派发全部订阅者后 resolve
//	  · 不 Await = 事件（fire-and-forget；同步派发，行为等价旧 Publish）
//	  · Await    = 请求-响应（订阅者可写回 v.Result，publish 侧取回）
//	On(subject, order, handler)           —— v2 订阅：order 升序执行（同 order 按注册序）
//	  · handler 收 *Value：可写回 Result；返回 error / panic → 收集进 v.Errors
//	  · errors 只收集、恒 accept（Future 不 reject），发送端自行检查 v.Errors
//	Subscribe / Publish                   —— v1 兼容（不写回、order=0）；迁移完成后移除
package mq

import (
	"context"
	"encoding/json"
	"sync"
)

// Handler v1 订阅回调（仅收 payload，不参与结果写回；迁移期兼容，见 package 注释）。
type Handler func(subject string, payload []byte)

// VHandler v2 订阅回调：按 order 执行；返回 error 或 panic 会被收集进 v.Errors。
type VHandler func(ctx context.Context, subject string, v *Value) error

// Sub 是订阅句柄。
type Sub interface {
	Unsubscribe() error
}

// Value 一次派发的共享值：Payload 为原始载荷；订阅者可写回 Result、追加 Errors。
type Value struct {
	Subject string
	Payload []byte // 原始载荷（JSON 文本；map/struct 载荷由 Emit 序列化）
	Result  any    // 订阅者写回的结果（请求-响应语义）
	Errors  []error
}

// JSON 把 Payload 反序列化到 out（无载荷/解析失败时返回 error）。
func (v *Value) JSON(out any) error {
	if len(v.Payload) == 0 {
		return json.Unmarshal([]byte("null"), out)
	}
	return json.Unmarshal(v.Payload, out)
}

// Err 返回第一个收集到的错误（无则 nil）——发送端自查入口。
func (v *Value) Err() error {
	if len(v.Errors) == 0 {
		return nil
	}
	return v.Errors[0]
}

// Future 派发结果（promise）。errors 恒收集不 reject，Await 始终拿到 *Value。
type Future struct {
	v    *Value
	done chan struct{}
	once sync.Once
}

// newFuture 构造未完成 Future。
func newFuture(v *Value) *Future {
	return &Future{v: v, done: make(chan struct{})}
}

// resolve 完成 Future（幂等）。
func (f *Future) resolve() { f.once.Do(func() { close(f.done) }) }

// Done 返回完成信号（await 语义）。
func (f *Future) Done() <-chan struct{} { return f.done }

// Wait 阻塞直到完成并返回 Value（进程内同步派发 = 立即返回）。
func (f *Future) Wait() *Value {
	<-f.done
	return f.v
}

// Options 构造参数。
type Options struct {
	// Prefix 是总线命名空间前缀（如 "chonk."），在初始化时注入一次。
	// 业务 publish/subscribe 一律写相对主题（如 "session-start"），总线内部自动
	// 补前缀用于订阅注册与匹配；下发给 handler 的 subject 为去前缀后的相对形式。
	// 兼容保护：传入主题已带该前缀（如 chonk.srv.notify.data-*）则原样透传、不重复加。
	// 空 = 不加前缀（主题原样注册/派发，handler 收到原主题）。
	Prefix string
}

// Bus 消息总线门面：
//   - v1：Publish / Subscribe（兼容，迁移后移除）
//   - v2：Emit（promise）/ On（order）
//   - subject 语义：业务侧一律相对主题；前缀（Options.Prefix）注入于总线内。
//     handler 收到的 subject 统一为去前缀后的相对形式（见 package 注释）。
type Bus interface {
	// Prefix 返回本总线命名空间前缀（未注入 = 空串）。
	Prefix() string
	// v1 兼容：fire-and-forget 发布（内部 = Emit 不 await；已关闭返回 errClosed）。
	Publish(subject string, payload []byte) error
	// v1 兼容：订阅（等价 On(subject, 0, 包装)，不参与结果写回）。
	Subscribe(subject string, h Handler) (Sub, error)
	// Emit 派发一条消息：按 order 同步执行全部订阅者，返回 *Future（结果/errors 在 Value）。
	Emit(ctx context.Context, subject string, payload any) *Future
	// On 订阅主题（支持 `*` 单段 / `>` 多段通配），order 升序执行（同 order 按注册序）。
	On(subject string, order int, h VHandler) (Sub, error)
	// Close 关闭总线（清空订阅）。
	Close() error
}

// New 构造进程内内存总线。
func New(opts Options) (Bus, error) {
	return newBus(opts.Prefix)
}

// 主题段匹配：subjectTokens 对 patternTokens 匹配。
// `>` 匹配任意剩余多段；`*` 匹配恰好一段。
func matchTokens(pattern, subject []string) bool {
	if len(pattern) == 0 {
		return len(subject) == 0
	}
	switch pattern[0] {
	case ">":
		return true
	case "*":
		return len(subject) > 0 && matchTokens(pattern[1:], subject[1:])
	default:
		return len(subject) > 0 && pattern[0] == subject[0] && matchTokens(pattern[1:], subject[1:])
	}
}
