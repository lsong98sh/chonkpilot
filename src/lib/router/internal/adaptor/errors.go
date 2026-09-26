// 错误体识别 + 错误分类共享件（LR-5 / LR-8）—— 三协议适配器共用，纯标准库实现。
//
// 背景（见 docs/spec/40-roadmap/40-演进计划.md §LR §6-2）：非 2xx 响应体可能是 **JSON**，也可能是
// **纯文本或 HTML**（网关 / 代理兜底页）。适配器需要把两者都转成可读信息，再归类（Kind / Status）。
//
// 分工：
//   - 本文件 = 「取值」（ErrorMessage）+ 「归类」（StatusError / TransportError / ErrorTypeKind）
//   - 「Retry-After 解析」（ParseRetryAfter）；
//   - Kind → Retryable 的唯一判定（`canon.RetryableFor`）与 Kind 常量归 `internal/canon`；
//   - **退避值计算归根包 `router.RetryWait`，执行重试循环归 llm**（本文件只出判定与退避入参）。
package adaptor

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-router/internal/canon"
)

// errorBodyLimit 是错误响应体的读取上限（错误信息足够可读即可，避免异常大体积兜底页）。
const errorBodyLimit = 16 * 1024

// ErrorMessage 从错误响应体中提取可读信息：
//
//   - JSON（`{"error":{"message":…}}` / `{"error":"…"}` / `{"message":…}` / `{"detail":…}`）→ 取其中的信息串；
//   - **非 JSON**（纯文本 / HTML）→ 原样返回去首尾空白后的文本；
//   - 空体 → 空串。
func ErrorMessage(body []byte) string {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return ""
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(s), &obj) == nil {
		for _, key := range []string{"error", "message", "detail"} {
			if msg := messageFromRaw(obj[key]); msg != "" {
				return msg
			}
		}
	}
	return s
}

// StatusError 把非 2xx 响应（状态码 + 头 + 原始体）归一为分类错误：
// Kind / Status 由 `canon.NewStatusError` 同源判定（401/403 → auth、429 → rate_limit、5xx →
// server、**其余（含 4xx）→ protocol**；`invalid` 不经状态码产生，见 I-132），
// Retryable 随 Kind 固化；`Retry-After` 头解析为 RetryAfter；信息串 = `ErrorMessage`
// （非 JSON 体原样兜底），体为空时回落 `HTTP <status>`。
func StatusError(status int, header http.Header, body []byte) *canon.Error {
	msg := ErrorMessage(body)
	if msg == "" {
		if text := strings.TrimSpace(http.StatusText(status)); text != "" {
			msg = text
		} else {
			msg = "unknown status"
		}
	}
	e := canon.NewStatusError(status, "llm http "+strconv.Itoa(status)+": "+msg)
	if header != nil {
		e.RetryAfter = ParseRetryAfter(header.Get("Retry-After"))
	}
	return e
}

// TransportError 归类**传输 / 读取层**错误：超时（context 期限 / net.Error 超时）→ timeout；
// 其余（连接失败 / 重置 / 断链，含 SSE 的 `ErrTruncated` 半截事件与 `io.ErrUnexpectedEOF`）→
// network。两者都可重试（Kind → Retryable 由 canon 固化）。
//
// 契约：本函数**只用于传输层**；协议 / 解析失败（如 JSON 非法）由适配器用
// `canon.NewError(canon.ErrorProtocol, …)` 明确报错。
func TransportError(err error) *canon.Error {
	if err == nil {
		return nil
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return canon.NewError(canon.ErrorTimeout, err.Error())
	}
	return canon.NewError(canon.ErrorNetwork, err.Error())
}

// ErrorTypeKind 把**流内错误对象**的 `error.type`（OpenAI 兼容与 Anthropic 两套词表的并集）映射为
// 错误分类：`*_rate_limit*` / `insufficient_quota` → rate_limit；`overloaded_error` /
// `api_error` / `server_error` → server；`authentication_error` / `permission_error` → auth；
// `invalid_request_error` / `not_found_error` / `request_too_large` → invalid；其余 → protocol。
// 词表外的取值一律回落 protocol（不猜、不静默当作成功）。
func ErrorTypeKind(typ string) canon.ErrorKind {
	t := strings.ToLower(strings.TrimSpace(typ))
	switch {
	case strings.Contains(t, "rate_limit"), t == "insufficient_quota":
		return canon.ErrorRateLimit
	case t == "overloaded_error", t == "api_error", t == "server_error":
		return canon.ErrorServer
	case t == "authentication_error", t == "permission_error":
		return canon.ErrorAuth
	case t == "invalid_request_error", t == "not_found_error", t == "request_too_large":
		return canon.ErrorInvalid
	default:
		return canon.ErrorProtocol
	}
}

// ParseRetryAfter 解析 `Retry-After` 头：**秒数**（非负整数）或 **HTTP-date**（RFC 1123 / RFC 850 /
// ANSI C，由 `http.ParseTime` 兼容）。无法解析 / 非正值 / 时刻已过 → 0（无需等待）。
func ParseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// messageFromRaw 从单个 JSON 字段取值：字符串直取；对象取 `message`。
func messageFromRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.Message
	}
	return ""
}
