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
// llm 侧 `retryCount` = 总额上限（**总量归 llm 掌管**，不放大、不叠成乘积；llm 侧 `retryCount`
// 配置语义不变，退避值经 `RetryWait` 计算——见 `error.go`）。退避为**毫秒级**
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

// defaultRetryBudget 是启用配置化重试（MaxRetries 已设置）且 Budget == 0 时的默认退避总预算。
const defaultRetryBudget = 60 * time.Second

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
			// 不 close(inner)：适配器可能仍在写（close 会 send on closed channel panic）。
			// 依赖 `internal/adaptor.Adapter` 契约 —— Stream **必须响应 ctx 取消**（写 out 经
			// adaptor.Emit 的 select 兼顾 ctx.Done）→ Stream 会尽快返回，errCh（带缓冲 1）不会
			// 阻塞该 goroutine → 无泄漏。故本分支仅返回，交由适配器自收敛。
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

// runWithRetries 执行一次 `Call` 的完整报文级重试循环（RETRY-2）。
//
// 两种模式：
//   - **Legacy（`Options.MaxRetries == nil`）**：完全保持既有行为 —— 仅 `Kind: network` 且
//     `Emitted == 0` 时透明重发 ≤1 次（毫秒级退避）；其余错误直接透出。
//   - **Configured（`MaxRetries != nil`）**：按 `Emitted` + `Kind`/`Phase` 分流；次数上限
//     `*MaxRetries`（不含首次）；`stream` 另有「最多 1 次」硬帽；退避由 `Policy` 决定，
//     总预算 `Budget`（0 = 默认 60s；<0 = 显式关闭预算，回退固定指数）。
//
// 不变量：`Emitted > 0` → **永不重发**；`Observer` panic 不影响决策；预算不足或次数用尽 → 终态。
func runWithRetries(ctx context.Context, ad adaptor.Adapter, spec Spec, req Request, out chan<- Event) {
	opts := req.Options
	legacy := opts.MaxRetries == nil

	retries := 0
	streamRetries := 0
	budget := opts.Budget
	maxRetries := 0
	if !legacy {
		switch {
		case budget == 0:
			budget = defaultRetryBudget
		case budget < 0:
			budget = 0 // 显式关闭预算（DefaultPolicy 回退固定指数）
		}
		maxRetries = *opts.MaxRetries
		if maxRetries < 0 {
			maxRetries = 0
		}
	}
	// 默认策略在首次失败前固定（k 按初始预算计算一次，后续不重算）。
	policy := opts.Policy
	if policy == nil {
		policy = canon.DefaultPolicy{MaxRetries: maxRetries, Budget: budget}
	}

	for {
		emitted, err := attemptOnce(ctx, ad, spec, req, out)
		if err == nil {
			return // 成功（含已投 done）
		}
		if ctx.Err() != nil {
			return // 消费方已离开 / 已取消：不投错误事件
		}
		if emitted > 0 {
			// 已向上层输出过任何事件 → 永不重发（已可见内容不可撤回）。
			if !legacy {
				notifyExhausted(ctx, opts.Observer, newRetryContext(retries+1, err, emitted, budget))
			}
			emitError(ctx, out, opts.Model, err)
			return
		}
		if legacy {
			if retries < transparentRetryMax && transparentRetryable(err) {
				if !sleepCtx(ctx, transparentRetryBackoff) {
					return // 退避期被取消：不再投事件
				}
				retries++
				continue
			}
			emitError(ctx, out, opts.Model, err)
			return
		}

		rc := newRetryContext(retries+1, err, emitted, budget)
		streamFailure := isStreamFailure(err, rc.Phase)
		if retries >= maxRetries {
			notifyExhausted(ctx, opts.Observer, rc)
			emitError(ctx, out, opts.Model, err)
			return
		}
		// 断连硬帽：同一 Call 内 `network`/`timeout` 的 `stream` 阶段最多重发 1 次（占用 retryCount 名额）。
		if streamFailure && streamRetries >= 1 {
			notifyExhausted(ctx, opts.Observer, rc)
			emitError(ctx, out, opts.Model, err)
			return
		}

		ok, wait := policy.Decide(ctx, rc)
		if !ok {
			notifyExhausted(ctx, opts.Observer, rc)
			emitError(ctx, out, opts.Model, err)
			return
		}
		if budget > 0 && wait > budget {
			// 预算不足以下次等待 → 直接终态（不缩短等待）。
			notifyExhausted(ctx, opts.Observer, rc)
			emitError(ctx, out, opts.Model, err)
			return
		}

		notifyRetry(ctx, opts.Observer, rc, wait)
		if !sleepCtx(ctx, wait) {
			return
		}
		if budget > 0 {
			budget -= wait
			if budget < 0 {
				budget = 0
			}
		}
		retries++
		if streamFailure {
			streamRetries++
		}
	}
}

// isStreamFailure 判定是否属于「已开始收流后断连」（network/timeout + PhaseStream）；
// 429/5xx 等 Kind 虽有 Phase 但不属于断连硬帽范围。
func isStreamFailure(err *Error, phase Phase) bool {
	if err == nil {
		return false
	}
	if err.Kind != ErrorNetwork && err.Kind != ErrorTimeout {
		return false
	}
	return phase == canon.PhaseStream
}

// newRetryContext 归一失败阶段（空 = stream 保守处理）并构造决策上下文。
func newRetryContext(attempt int, err *Error, emitted int, budget time.Duration) RetryContext {
	phase := canon.PhaseStream
	if err != nil && err.Phase != "" {
		phase = err.Phase
	}
	return RetryContext{Attempt: attempt, Phase: phase, Err: err, Emitted: emitted, Budget: budget}
}

// emitError 向上层投递终态错误（消费方离开则不阻塞）。
func emitError(ctx context.Context, out chan<- Event, model string, err *Error) {
	select {
	case out <- Event{Type: EvError, Model: model, Err: err}:
	case <-ctx.Done():
	}
}

// notifyRetry 调用 OnRetry（实现方必须非阻塞；panic / 失败一律忽略，不影响决策）。
func notifyRetry(ctx context.Context, obs RetryObserver, rc RetryContext, wait time.Duration) {
	if obs == nil {
		return
	}
	defer func() { _ = recover() }()
	obs.OnRetry(ctx, rc, wait)
}

// notifyExhausted 调用 OnExhausted（同上：非阻塞 + panic 隔离）。
func notifyExhausted(ctx context.Context, obs RetryObserver, rc RetryContext) {
	if obs == nil {
		return
	}
	defer func() { _ = recover() }()
	obs.OnExhausted(ctx, rc, rc.Err)
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
