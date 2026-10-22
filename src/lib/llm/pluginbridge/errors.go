// Package pluginbridge 是插件前门的注册层（机制层）：身份 / 订阅 / 能力方法表 / 稳定错误码。
//
// 设计见 docs/bridge-plugin/插件桥接端口设计方案.md §4–§5、§8、§11。
// 本包不含具体业务能力：能力是挂在 Registry 上的 handler，一个文件叠加一个。
package pluginbridge

import "fmt"

// 稳定错误码（对齐 httpapi 的 publishError 口径）。
const (
	CodeArgsInvalid        = "args_invalid"
	CodeMethodNotFound     = "method_not_found"
	CodePluginUnregistered = "plugin_unregistered"
	CodePluginUnauthorized = "plugin_unauthorized"
	CodePluginDisabled     = "plugin_disabled"
	CodeInstanceForbidden  = "instance_id_forbidden"
	CodeSubscriptionDenied = "subscription_denied"
	CodeFilterDropped      = "filter_dropped"
	CodeFilterFailed       = "filter_failed"
	CodeFilterTimeout      = "filter_timeout"
	CodeInternal           = "internal"
)

// Error 是带稳定错误码的错误。
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string {
	if e.Code == "" {
		return e.Msg
	}
	if e.Msg == "" {
		return e.Code
	}
	return e.Code + ": " + e.Msg
}

func errf(code, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// codeOf 提取错误码（非 *Error → internal）。
func codeOf(err error) string {
	if e, ok := err.(*Error); ok && e.Code != "" {
		return e.Code
	}
	return CodeInternal
}
