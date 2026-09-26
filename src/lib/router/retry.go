// 连接层**透明重试**（2026-09-22 用户定案口径；口径唯一登记处 = docs/spec/40-roadmap/40-演进计划.md §LR §1.1）。
//
// 分层判据（一句话）：**「透明重发」（对上层不可见、语义不变）归 router；「需要决策或可见的重试」
// （影响对话内容 / UI / 取消）归 llm**。
//
// 唯一触发判据 = **「尚未向上层输出过任何事件」**：一旦已向上层投出任何事件（哪怕一个
// `text_delta`），本层**绝不重发**（已可见的内容不能重复、不能回退）；重发仅限
//
//   - **连接层失败**（DNS / 连接被拒 / TLS 握手失败 → `*canon.Error{Kind: network}`）；
//   - **流已开始但中断 / 半包**（SSE 的 `ErrTruncated` / `io.ErrUnexpectedEOF` → 亦归 `network`）
//     且**未输出过任何事件**。
//
// 此二者等价于「还没向上层输出」。
//
// **不覆盖**（明确交给 llm）：
//
//   - 超时（首字节 `ResponseTimeout` / 流间隔 `StreamTimeout`）→ `Kind: timeout`，不透明重发；
//   - 已输出部分内容后的失败 → 不透明重发（归 llm 的断链续写 / 可见重试）；
//   - 429 / 5xx 等**需要决策**的失败（上报「重试中」/ 退避取值 / 次数用尽处置）→ 归 llm。
//
// **次数**：本层上限 **1 次**（`transparentRetryMax`），且**累计计入总次数** —— router ≤1 次 +
// llm 侧 `retryCount` = 总额上限（**总量归 llm 掌管**，不放大、不叠成乘积；llm 侧
// `retryCount` / `retryDelay` 四级配置语义不变）。退避为**毫秒级**
// （`transparentRetryBackoff`，口径区间 200–500ms）。
package router

import (
	"context"
	"time"

	"github.com/chonkpilot/chonkpilot-router/internal/adaptor"
	"github.com/chonkpilot/chonkpilot-router/internal/canon"
)

// transparentRetryMax 是**单次 `Call`** 内允许的透明重发次数上限（口径：≤1 次）。
const transparentRetryMax = 1

// transparentRetryBackoff 是透明重发前的退避时长（毫秒级；测试可临时下调）。
var transparentRetryBackoff = 300 * time.Millisecond

// transparentRetryable 判定一次失败是否属于**连接层**（本层可透明重发）。
// 仅 `Kind: network`（连接失败 / 重置 / 断链 / 半包）在此列；timeout / rate_limit / server /
// auth / protocol / invalid 一律 false（归 llm 决策或直接透出，见文件头「不覆盖」）。
func transparentRetryable(err *Error) bool {
	return err != nil && err.Kind == canon.ErrorNetwork
}

// attemptOnce 跑**一次**适配器调用，把事件按序转发给上层 out；返回「已向上层输出的事件数」与
// 归一后的错误（nil = 成功）。计数即透明重试的判据来源：`emitted == 0` 才允许重发（见文件头）。
//
// 通道纪律：适配器**不关闭**内部通道（契约见 `internal/adaptor.Adapter`），仅在其 `Stream`
// 返回后由本函数关闭（此时它不可能再写）；ctx 取消时**不关闭**（适配器可能仍在写，关闭会
// `send on closed channel` panic），由适配器自行按 ctx 收敛。
func attemptOnce(ctx context.Context, ad adaptor.Adapter, spec Spec, req Request, out chan<- Event) (int, *Error) {
	inner := make(chan Event)
	errCh := make(chan error, 1)
	go func() { errCh <- ad.Stream(ctx, spec, req, inner) }()

	emitted := 0
	for {
		select {
		case <-ctx.Done():
			return emitted, errorOf(ctx.Err())
		case err := <-errCh:
			close(inner)
			return emitted, errorOf(err)
		case ev := <-inner:
			emitted++
			select {
			case out <- ev:
			case <-ctx.Done():
				return emitted, errorOf(ctx.Err())
			}
		}
	}
}

// errorOf 归一适配器返回值（nil 保持 nil —— 与「已分类错误」区分开）。
func errorOf(err error) *Error {
	if err == nil {
		return nil
	}
	return toError(err)
}

// sleepCtx 是可取消的退避等待；返回 false = 上下文已结束（调用方应直接收手）。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
