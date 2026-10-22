// 统一错误分类与退避值（LR-1 类型；LR-8 的判定口径在此固化）。
//
// 归属：**判定与退避值归 router（本包 + 根包 `RetryWait`），执行重试循环归 llm**（llm 保留
// TCP 预检 probeBeforeRetry 与取消感知 sleepCtx，避免双层重试放大）。
//
// 落位：类型 / 常量 / 构造与分类函数定义在本包（`internal/canon`），根包以别名（`Error`、
// `ErrorXxx`）与转发（`newError` / `kindFromStatus` / `newStatusError` / `toError` /
// `retryableFor`）再导出 —— 对外签名与既有调用点逐字不变；退避值计算 `RetryWait` 按 LR-8
// 归属留根包。
package canon

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrorKind 是统一错误分类（可重试：network / timeout / rate_limit / server；不可重试：auth /
// protocol / invalid）。
type ErrorKind string

const (
	ErrorNetwork   ErrorKind = "network"    // 连接失败 / 重置 / 断链（可重试）
	ErrorTimeout   ErrorKind = "timeout"    // 首字节 / 流间隔超时（可重试）
	ErrorRateLimit ErrorKind = "rate_limit" // 429（可重试）
	ErrorServer    ErrorKind = "server"     // 5xx（可重试）
	ErrorAuth      ErrorKind = "auth"       // 401 / 403（不可重试）
	ErrorProtocol  ErrorKind = "protocol"   // 协议 / 解析失败、HTTP 4xx（非 401/403/429）（不可重试）
	// ErrorInvalid 仅表示**调用方用法错误**（未注册 provider、空 Name/Protocol、adapter 为 nil、
	// 协议不支持 tools/images 等）——**与「协议 / HTTP 类失败」不重叠**（I-132 口径统一，2026-09-22）：
	// HTTP 状态码一律不映射到本类（4xx → protocol），故 invalid 只可能来自前置校验。
	ErrorInvalid ErrorKind = "invalid"
)

// Phase 是**失败阶段**（仅内部用于重试分流，不进消息面）。
//
// 决策顺序（RETRY-2）：先按 Kind（auth/protocol/invalid 直接终态；rate_limit/server 按次数重试）；
// Kind 为 network/timeout 时再按 Phase 分流（dial / first_byte → 上限 retryCount；stream 且
// Emitted == 0 → 最多 1 次）。适配器未赋值（空串）按 PhaseStream 保守处理。
type Phase string

const (
	PhaseDial      Phase = "dial"       // DNS / TCP / TLS 未建立
	PhaseFirstByte Phase = "first_byte" // 请求已发出，未收到首字节（含首字节超时）
	PhaseStream    Phase = "stream"     // 已开始收流（已收到响应头/首字节）
)

// Error 是分类错误（适配器返回、事件携带、Call 前置校验共用同一类型）。
type Error struct {
	Kind       ErrorKind
	Message    string
	Retryable  bool
	Status     int           // HTTP 状态码（0 = 非 HTTP 失败）
	RetryAfter time.Duration // 0 = 未提供（解析 Retry-After）
	Phase      Phase         // 失败阶段（空 = 未标注，按 stream 保守处理）
}

// WithPhase 标注失败阶段（nil 安全，便于适配器在错误构造处链式调用）。
func (e *Error) WithPhase(p Phase) *Error {
	if e != nil {
		e.Phase = p
	}
	return e
}

func (e *Error) Error() string { return fmt.Sprintf("[%s] %s", e.Kind, e.Message) }

// RetryableFor 是 Kind → Retryable 的唯一判定（构造错误时固化，避免各处重复推导）。
func RetryableFor(kind ErrorKind) bool {
	switch kind {
	case ErrorNetwork, ErrorTimeout, ErrorRateLimit, ErrorServer:
		return true
	default:
		return false
	}
}

// NewError 构造按 Kind 判定 Retryable 的错误。
func NewError(kind ErrorKind, msg string) *Error {
	return &Error{Kind: kind, Message: msg, Retryable: RetryableFor(kind)}
}

// KindFromStatus 把 HTTP 状态码映射为错误分类：401 / 403 → auth；429 → rate_limit；5xx →
// server；其余 4xx → protocol（协议 / 请求类失败，**与 llm 侧既有状态码口径逐字一致**，
// 见 I-132：2026-09-22 由 `invalid` 回落为 `protocol`；llm 侧映射随 LR-11 删旧直连实现，
// 由本函数**唯一承担**）；非 4xx 非 5xx（如异常 2xx 落到错误分支）→ protocol。
// **`invalid` 不经本函数产生** —— 该分类专指调用方用法错误（未注册 provider / 空 Name、Protocol
// 等前置校验），两者语义不重叠。
func KindFromStatus(status int) ErrorKind {
	switch {
	case status == 401 || status == 403:
		return ErrorAuth
	case status == 429:
		return ErrorRateLimit
	case status >= 500:
		return ErrorServer
	default:
		return ErrorProtocol
	}
}

// NewStatusError 构造带 HTTP 状态码的分类错误（Status / Kind 同源）。
func NewStatusError(status int, msg string) *Error {
	e := NewError(KindFromStatus(status), msg)
	e.Status = status
	return e
}

// ToError 把适配器返回值归一为 *Error：已是 *Error 原样返回；context 超时 → timeout（可重试）；
// 其余 → protocol（不可重试；适配器应自行分类，此路径仅为兜底）。
func ToError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return NewError(ErrorTimeout, err.Error())
	}
	return NewError(ErrorProtocol, err.Error())
}
