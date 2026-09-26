// 出网与流式读取共用件（LR-3 / LR-4 共用）—— 纯标准库实现。
//
// 覆盖：
//   - 首字节超时（ResponseTimeout）与流 chunk 间隔超时（StreamTimeout）两级超时；
//   - 非 2xx → 分类错误（`StatusError`：Kind / Status / Retry-After），不做载荷回调；
//   - 2xx + `text/event-stream` → 逐条 SSE data 载荷（`sse.go` 解码：半包 / 多行 / 断链判定）；
//   - 2xx + 其它 Content-Type → **体首行像 SSE（`data:` / `event:`）则仍按 SSE 泵**（兼容不声明
//     头的端点与测试桩；旧 llm 侧按行解析、不作 Content-Type 判定），否则按「兼容端点忽略
//     `stream=true` 回单发 JSON」整体回调一次；
//   - 消费方取消（ctx）→ 立即返回，不静默继续读。
package adaptor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chonkpilot/chonkpilot-router/internal/canon"
)

// 超时回落默认（对齐 llm 侧既有口径 ResponseTimeout / StreamTimeout；LR-11 接线时由 llm 把
// usr 配置值经 CallOptions 传入）。
const (
	DefaultResponseTimeout = 120 * time.Second // 首字节超时（连接建立后第一个字节）
	DefaultStreamTimeout   = 60 * time.Second  // 流 chunk 间隔超时（两次 chunk 之间）
)

// Exchange 是一次「请求 → 流式响应」的共用通道。
type Exchange struct {
	httpClient      *http.Client
	responseTimeout time.Duration
	streamTimeout   time.Duration
}

// NewExchange 构造共用通道（httpClient nil → 自建默认客户端；超时 <=0 → 包级默认常量）。
func NewExchange(httpClient *http.Client, responseTimeout, streamTimeout time.Duration) Exchange {
	if httpClient == nil {
		httpClient = &http.Client{} // 无总超时：首字节与 chunk 间隔由本件分级控制
	}
	if responseTimeout <= 0 {
		responseTimeout = DefaultResponseTimeout
	}
	if streamTimeout <= 0 {
		streamTimeout = DefaultStreamTimeout
	}
	return Exchange{httpClient: httpClient, responseTimeout: responseTimeout, streamTimeout: streamTimeout}
}

// PostJSON 发送 JSON 请求体，并逐条把响应载荷交给 onPayload（返回 nil = 全部载荷已交付）。
//
// 返回 *canon.Error（nil = 成功）：编码 / 传输 / 分类 / 超时错误均已归类；onPayload 返回的错误
// 原样归一（已是 *canon.Error 则保留 Kind）。
func (e Exchange) PostJSON(ctx context.Context, url string, headers map[string]string, body any, onPayload func(payload []byte) error) *canon.Error {
	raw, jerr := json.Marshal(body)
	if jerr != nil {
		return canon.NewError(canon.ErrorProtocol, "编码请求体失败: "+jerr.Error())
	}
	resp, cancelReq, cerr := e.do(ctx, url, headers, raw)
	if cerr != nil {
		return cerr
	}
	defer cancelReq()
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		buf, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit))
		return StatusError(resp.StatusCode, resp.Header, buf)
	}

	if !isEventStream(resp.Header) {
		br := bufio.NewReader(resp.Body)
		// 兼容 ①「端点忽略 stream=true 回单发 JSON」（整体单发解析）；
		// 兼容 ②「体是 SSE 但未声明 text/event-stream 头」的端点与测试桩 —— 旧 llm 侧按行解析、
		// **不作 Content-Type 判定**，为保持替换前后行为一致，本件按体首行嗅探后仍走 SSE 泵。
		if looksLikeSSE(br) {
			return e.pump(ctx, br, onPayload)
		}
		payload, rerr := readAllWithin(ctx, br, e.streamTimeout)
		if rerr != nil {
			return rerr
		}
		payload = bytes.TrimSpace(payload)
		if len(payload) == 0 {
			return canon.NewError(canon.ErrorProtocol, "空响应体（Content-Type 既非 event-stream，载荷也为空）")
		}
		if err := onPayload(payload); err != nil {
			if errors.Is(err, ErrStreamDone) {
				return nil // 载荷已表达流结束（如兼容端点回单发 JSON 时带终止标记）
			}
			return canon.ToError(err)
		}
		return nil
	}
	return e.pump(ctx, resp.Body, onPayload)
}

// sseProbeLimit 是「体是否像 SSE」的嗅探字节数（够看首个字段名即可）。
const sseProbeLimit = 64

// looksLikeSSE 判定响应体（未声明 event-stream 时）是否仍是 SSE：跳过前导空白后以
// `data:` / `event:` 开头即认定为 SSE（不影响真正的单发 JSON —— JSON 以 `{` / `[` 开头）。
func looksLikeSSE(br *bufio.Reader) bool {
	peek, err := br.Peek(sseProbeLimit)
	if len(peek) == 0 && err != nil {
		return false
	}
	s := strings.TrimLeft(string(peek), " \t\r\n")
	return strings.HasPrefix(s, "data:") || strings.HasPrefix(s, "event:")
}

// do 发送请求并返回 2xx 之前的响应（首字节超时由 AfterFunc 触发取消，与消费方取消区分）。
// 调用方读完 resp.Body 后必须调用返回的 cancel（释放请求上下文）。
func (e Exchange) do(ctx context.Context, url string, headers map[string]string, raw []byte) (*http.Response, context.CancelFunc, *canon.Error) {
	reqCtx, cancelReq := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		cancelReq()
		return nil, nil, canon.NewError(canon.ErrorProtocol, err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	var firstByteTimeout atomic.Bool
	timer := time.AfterFunc(e.responseTimeout, func() {
		firstByteTimeout.Store(true)
		cancelReq()
	})
	defer timer.Stop()

	resp, err := e.httpClient.Do(req)
	timer.Stop()
	if err == nil && firstByteTimeout.Load() {
		// 首字节定时器与响应到达同时触发：请求上下文已被取消，resp.Body 不可再用 →
		// 明确报超时（不留半死响应，避免下游读到残缺流）。
		_ = resp.Body.Close()
		cancelReq()
		return nil, nil, canon.NewError(canon.ErrorTimeout, "首字节超时（"+e.responseTimeout.String()+"）")
	}
	if err != nil {
		cancelReq()
		switch {
		case firstByteTimeout.Load():
			return nil, nil, canon.NewError(canon.ErrorTimeout, "首字节超时（"+e.responseTimeout.String()+"）")
		case ctx.Err() != nil:
			return nil, nil, canon.NewError(canon.ErrorProtocol, ctx.Err().Error())
		default:
			return nil, nil, TransportError(err)
		}
	}
	return resp, cancelReq, nil
}

// pump 逐条读 SSE 载荷并回调：EOF = 正常收尾（**含无 `[DONE]` 的流**）；断链（ErrTruncated）/
// 读取失败 → network；间隔超过 streamTimeout → timeout。
//
// 读循环在独立 goroutine 里，主循环 select ctx / 定时器 / 载荷 —— 保证「间隔超时」与「消费方
// 取消」都能立刻中止；主循环退出即关 done 让读 goroutine 解阻塞（无泄漏）。
func (e Exchange) pump(ctx context.Context, body io.Reader, onPayload func(payload []byte) error) *canon.Error {
	type item struct {
		payload []byte
		err     error
	}
	ch := make(chan item)
	done := make(chan struct{})
	defer close(done)

	go func() {
		dec := NewSSEDecoder(body)
		for {
			payload, err := dec.Next()
			if err != nil {
				select {
				case ch <- item{err: err}:
				case <-done:
				}
				return
			}
			select {
			case ch <- item{payload: payload}:
			case <-done:
				return
			}
		}
	}()

	timer := time.NewTimer(e.streamTimeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return canon.NewError(canon.ErrorProtocol, ctx.Err().Error())
		case <-timer.C:
			return canon.NewError(canon.ErrorTimeout, "流 chunk 间隔超时（"+e.streamTimeout.String()+"）")
		case it := <-ch:
			if it.err != nil {
				if errors.Is(it.err, io.EOF) {
					return nil
				}
				return TransportError(it.err)
			}
			if err := onPayload(it.payload); err != nil {
				if errors.Is(err, ErrStreamDone) {
					return nil // 终止标记（[DONE] / 终态事件）→ 立即收尾，不等上游关连接（见 ErrStreamDone）
				}
				return canon.ToError(err)
			}
			timer.Reset(e.streamTimeout)
		}
	}
}

// readAllWithin 在时限内读完全部响应体（一次性响应，无 chunk 语义）。
func readAllWithin(ctx context.Context, body io.Reader, d time.Duration) ([]byte, *canon.Error) {
	type res struct {
		buf []byte
		err error
	}
	ch := make(chan res, 1) // 缓冲 1：超时返回后读 goroutine 不阻塞
	go func() {
		buf, err := io.ReadAll(body)
		ch <- res{buf, err}
	}()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, canon.NewError(canon.ErrorProtocol, ctx.Err().Error())
	case <-timer.C:
		return nil, canon.NewError(canon.ErrorTimeout, "响应读取超时（"+d.String()+"）")
	case r := <-ch:
		if r.err != nil {
			return nil, TransportError(r.err)
		}
		return r.buf, nil
	}
}

// isEventStream 判定响应是否为 SSE（Content-Type 含 `text/event-stream`）。
func isEventStream(h http.Header) bool {
	return strings.Contains(strings.ToLower(h.Get("Content-Type")), "text/event-stream")
}
