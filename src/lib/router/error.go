// 统一错误分类与退避值的**别名再导出 + 退避计算**（定义处 = `internal/canon/error.go`）。
//
// 对外签名逐字不变：`ErrorKind` / `ErrorXxx` 常量 / `Error` 类型由别名与常量转发；根包内部
// 调用点（`newError` / `kindFromStatus` / `newStatusError` / `toError` / `retryableFor`）以函数
// 转发保持逐字不变（适配器侧可直接用 canon 的导出名，见 `internal/adaptor/errors.go`）。
package router

import (
	"time"

	"github.com/chonkpilot/chonkpilot-router/internal/canon"
)

// ErrorKind 是统一错误分类（可重试：network / timeout / rate_limit / server；不可重试：auth /
// protocol / invalid）。
type ErrorKind = canon.ErrorKind

// 错误分类取值。
const (
	ErrorNetwork   = canon.ErrorNetwork
	ErrorTimeout   = canon.ErrorTimeout
	ErrorRateLimit = canon.ErrorRateLimit
	ErrorServer    = canon.ErrorServer
	ErrorAuth      = canon.ErrorAuth
	ErrorProtocol  = canon.ErrorProtocol
	ErrorInvalid   = canon.ErrorInvalid
)

// Error 是分类错误（适配器返回、事件携带、Call 前置校验共用同一类型）。
type Error = canon.Error

// 构造 / 分类转发（实现唯一处 = canon；根包白盒与调用点口径逐字不变）。
var (
	newError       = canon.NewError
	kindFromStatus = canon.KindFromStatus
	newStatusError = canon.NewStatusError
	toError        = canon.ToError
	retryableFor   = canon.RetryableFor
)

// 指数退避参数：第 attempt 次重试等待 = retryBaseWait << (attempt-1)，上限 retryMaxWait。
const (
	retryBaseWait = 1 * time.Second
	retryMaxWait  = 30 * time.Second
)

// RetryWait 返回第 attempt 次重试前的等待时长（attempt 从 1 起计）。
// Retry-After（err.RetryAfter）优先；否则指数退避并封顶 retryMaxWait；
// 不可重试（Retryable=false）或 err 为 nil → 0（调用方据此结束重试）。
func RetryWait(err *Error, attempt int) time.Duration {
	if err == nil || !err.Retryable {
		return 0
	}
	if err.RetryAfter > 0 {
		return err.RetryAfter
	}
	if attempt < 1 {
		attempt = 1
	}
	d := retryBaseWait
	for i := 1; i < attempt; i++ {
		if d >= retryMaxWait {
			return retryMaxWait
		}
		d *= 2
	}
	if d > retryMaxWait {
		return retryMaxWait
	}
	return d
}
