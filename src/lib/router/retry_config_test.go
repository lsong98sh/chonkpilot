// RETRY-2 白盒：配置化报文级重试（Emitted 判据 / Kind+Phase 分流 / 预算 / Observer）。
package router

import (
	"context"
	"sync"
	"testing"
	"time"
)

// recObserver 记录重试回调（测试用）。
type recObserver struct {
	mu       sync.Mutex
	retries  []RetryContext
	waits    []time.Duration
	exhausts []RetryContext
	panicOn  bool
}

func (o *recObserver) OnRetry(_ context.Context, rc RetryContext, wait time.Duration) {
	if o.panicOn {
		panic("observer panic")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.retries = append(o.retries, rc)
	o.waits = append(o.waits, wait)
}

func (o *recObserver) OnExhausted(_ context.Context, rc RetryContext, _ *Error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.exhausts = append(o.exhausts, rc)
}

// newConfiguredRouter 构造一个用 fakeAdapter 覆盖 openai 协议的 router + 已注册 provider。
func newConfiguredRouter(t *testing.T, fa *fakeAdapter) *Router {
	t.Helper()
	r := New(Config{})
	if err := r.registerAdapter(fa); err != nil {
		t.Fatalf("registerAdapter: %v", err)
	}
	if err := r.Register(Spec{Name: "p1", Protocol: ProtocolOpenAI, DefaultModel: "m"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return r
}

func intp(v int) *int { return &v }

// TestConfiguredRetryRateLimitThenSuccess：429 两次后成功；次数/等待由预算归一化（3ms → 1ms/2ms）。
func TestConfiguredRetryRateLimitThenSuccess(t *testing.T) {
	obs := &recObserver{}
	fa := &fakeAdapter{proto: ProtocolOpenAI}
	fa.streamFn = func(_ context.Context, _ Spec, req Request, out chan<- Event) error {
		switch fa.callCount() {
		case 1, 2:
			return &Error{Kind: ErrorRateLimit, Message: "429", Retryable: true, Status: 429}
		default:
			out <- Event{Type: EvTextDelta, Text: "ok", Model: req.Options.Model}
			out <- Event{Type: EvDone, FinishReason: "stop", Model: req.Options.Model}
			return nil
		}
	}
	r := newConfiguredRouter(t, fa)

	ch, err := r.Call(context.Background(), Request{Provider: "p1", Options: CallOptions{
		MaxRetries: intp(2), Budget: 3 * time.Millisecond, Observer: obs,
	}})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	evs := drain(t, ch)
	if len(evs) != 2 || evs[0].Type != EvTextDelta || evs[1].Type != EvDone {
		t.Fatalf("事件=%+v want 一份 [text_delta ok, done]", evs)
	}
	if got := fa.callCount(); got != 3 {
		t.Fatalf("调用次数=%d want 3（首次 + 2 次重试）", got)
	}
	if len(obs.retries) != 2 {
		t.Fatalf("OnRetry 次数=%d want 2", len(obs.retries))
	}
	if obs.waits[0] != time.Millisecond || obs.waits[1] != 2*time.Millisecond {
		t.Fatalf("等待=%v want [1ms 2ms]（预算 3ms 按 1:2 归一化）", obs.waits)
	}
	if len(obs.exhausts) != 0 {
		t.Fatalf("不应有 OnExhausted，实得 %d", len(obs.exhausts))
	}
}

// TestConfiguredRetryNoRetryAfterEmit：已输出事件 → 永不重发（即使 MaxRetries > 0）。
func TestConfiguredRetryNoRetryAfterEmit(t *testing.T) {
	obs := &recObserver{}
	fa := &fakeAdapter{proto: ProtocolOpenAI, streamFn: func(_ context.Context, _ Spec, req Request, out chan<- Event) error {
		out <- Event{Type: EvTextDelta, Text: "part", Model: req.Options.Model}
		return &Error{Kind: ErrorNetwork, Message: "断链", Retryable: true, Phase: PhaseStream}
	}}
	r := newConfiguredRouter(t, fa)

	ch, err := r.Call(context.Background(), Request{Provider: "p1", Options: CallOptions{
		MaxRetries: intp(5), Budget: time.Millisecond, Observer: obs,
	}})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	evs := drain(t, ch)
	if len(evs) != 2 || evs[1].Type != EvError {
		t.Fatalf("事件=%+v want [text_delta, error]", evs)
	}
	if got := fa.callCount(); got != 1 {
		t.Fatalf("调用次数=%d want 1（Emitted>0 不重发）", got)
	}
	if len(obs.retries) != 0 {
		t.Fatalf("不应有 OnRetry，实得 %d", len(obs.retries))
	}
	if len(obs.exhausts) != 1 {
		t.Fatalf("OnExhausted 次数=%d want 1", len(obs.exhausts))
	}
}

// TestConfiguredRetryStreamCapOne：stream 断连独立硬帽 = 最多 1 次（总调用 2 次）。
func TestConfiguredRetryStreamCapOne(t *testing.T) {
	obs := &recObserver{}
	fa := &fakeAdapter{proto: ProtocolOpenAI, streamFn: func(_ context.Context, _ Spec, _ Request, _ chan<- Event) error {
		return &Error{Kind: ErrorNetwork, Message: "断链", Retryable: true, Phase: PhaseStream}
	}}
	r := newConfiguredRouter(t, fa)

	ch, err := r.Call(context.Background(), Request{Provider: "p1", Options: CallOptions{
		MaxRetries: intp(5), Budget: 2 * time.Millisecond, Observer: obs,
	}})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	evs := drain(t, ch)
	if len(evs) != 1 || evs[0].Type != EvError {
		t.Fatalf("事件=%+v want 单条 error", evs)
	}
	if got := fa.callCount(); got != 2 {
		t.Fatalf("调用次数=%d want 2（stream 最多重发 1 次）", got)
	}
	if len(obs.retries) != 1 || len(obs.exhausts) != 1 {
		t.Fatalf("OnRetry=%d OnExhausted=%d want 1/1", len(obs.retries), len(obs.exhausts))
	}
}

// TestConfiguredRetryDialUsesMaxRetries：dial 阶段按 retryCount 重试（n 次 → 总调用 n+1）。
func TestConfiguredRetryDialUsesMaxRetries(t *testing.T) {
	fa := &fakeAdapter{proto: ProtocolOpenAI, streamFn: func(_ context.Context, _ Spec, _ Request, _ chan<- Event) error {
		return &Error{Kind: ErrorNetwork, Message: "dial tcp: refused", Retryable: true, Phase: PhaseDial}
	}}
	r := newConfiguredRouter(t, fa)

	ch, err := r.Call(context.Background(), Request{Provider: "p1", Options: CallOptions{
		MaxRetries: intp(5), Budget: 5 * time.Millisecond,
	}})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	drain(t, ch)
	if got := fa.callCount(); got != 6 {
		t.Fatalf("调用次数=%d want 6（首次 + retryCount=5）", got)
	}
}

// TestConfiguredRetryZeroDisables：MaxRetries=&0 → 明确不重试（含 dial）。
func TestConfiguredRetryZeroDisables(t *testing.T) {
	fa := &fakeAdapter{proto: ProtocolOpenAI, streamFn: func(_ context.Context, _ Spec, _ Request, _ chan<- Event) error {
		return &Error{Kind: ErrorNetwork, Message: "dial tcp: refused", Retryable: true, Phase: PhaseDial}
	}}
	r := newConfiguredRouter(t, fa)

	ch, err := r.Call(context.Background(), Request{Provider: "p1", Options: CallOptions{MaxRetries: intp(0)}})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	drain(t, ch)
	if got := fa.callCount(); got != 1 {
		t.Fatalf("调用次数=%d want 1（显式 0 = 不重试）", got)
	}
}

// TestConfiguredRetryObserverPanicIsolated：Observer panic 不影响重试决策。
func TestConfiguredRetryObserverPanicIsolated(t *testing.T) {
	obs := &recObserver{panicOn: true}
	fa := &fakeAdapter{proto: ProtocolOpenAI}
	fa.streamFn = func(_ context.Context, _ Spec, req Request, out chan<- Event) error {
		if fa.callCount() == 1 {
			return &Error{Kind: ErrorNetwork, Message: "dial", Retryable: true, Phase: PhaseDial}
		}
		out <- Event{Type: EvDone, FinishReason: "stop", Model: req.Options.Model}
		return nil
	}
	r := newConfiguredRouter(t, fa)

	ch, err := r.Call(context.Background(), Request{Provider: "p1", Options: CallOptions{
		MaxRetries: intp(1), Budget: time.Millisecond, Observer: obs,
	}})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	evs := drain(t, ch)
	if len(evs) != 1 || evs[0].Type != EvDone {
		t.Fatalf("事件=%+v want [done]（panic 不得阻断重试）", evs)
	}
	if got := fa.callCount(); got != 2 {
		t.Fatalf("调用次数=%d want 2", got)
	}
}

// TestConfiguredRetryBudgetGate：Retry-After 超过剩余预算 → 不等待、直接终态。
func TestConfiguredRetryBudgetGate(t *testing.T) {
	obs := &recObserver{}
	fa := &fakeAdapter{proto: ProtocolOpenAI, streamFn: func(_ context.Context, _ Spec, _ Request, _ chan<- Event) error {
		return &Error{Kind: ErrorRateLimit, Message: "429", Retryable: true, Status: 429, RetryAfter: 10 * time.Second, Phase: PhaseFirstByte}
	}}
	r := newConfiguredRouter(t, fa)

	ch, err := r.Call(context.Background(), Request{Provider: "p1", Options: CallOptions{
		MaxRetries: intp(5), Budget: time.Second, Observer: obs,
	}})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	drain(t, ch)
	if got := fa.callCount(); got != 1 {
		t.Fatalf("调用次数=%d want 1（Retry-After 超预算不等待）", got)
	}
	if len(obs.retries) != 0 || len(obs.exhausts) != 1 {
		t.Fatalf("OnRetry=%d OnExhausted=%d want 0/1", len(obs.retries), len(obs.exhausts))
	}
}

// TestConfiguredRetryAuthNotRetried：auth / protocol 等不可重试类直接终态。
func TestConfiguredRetryAuthNotRetried(t *testing.T) {
	fa := &fakeAdapter{proto: ProtocolOpenAI, streamFn: func(_ context.Context, _ Spec, _ Request, _ chan<- Event) error {
		return &Error{Kind: ErrorAuth, Message: "401", Retryable: false, Status: 401, Phase: PhaseFirstByte}
	}}
	r := newConfiguredRouter(t, fa)

	ch, err := r.Call(context.Background(), Request{Provider: "p1", Options: CallOptions{MaxRetries: intp(5)}})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	drain(t, ch)
	if got := fa.callCount(); got != 1 {
		t.Fatalf("调用次数=%d want 1（auth 不重试）", got)
	}
}

// TestDefaultPolicyWeightedBudget：默认策略按 2 倍指数权重归一化（N=5、预算 60s）。
func TestDefaultPolicyWeightedBudget(t *testing.T) {
	p := DefaultPolicy{MaxRetries: 5, Budget: 60 * time.Second}
	err := &Error{Kind: ErrorNetwork, Message: "x", Retryable: true, Phase: PhaseDial}
	var sum time.Duration
	var last time.Duration
	for i := 1; i <= 5; i++ {
		ok, wait := p.Decide(context.Background(), RetryContext{Attempt: i, Phase: PhaseDial, Err: err, Budget: 60 * time.Second})
		if !ok {
			t.Fatalf("attempt %d：应可重试", i)
		}
		sum += wait
		last = wait
	}
	// k = 60/31 ≈ 1.935s；5 次合计应等于预算（允许整秒截断误差）。
	if sum < 59*time.Second || sum > 61*time.Second {
		t.Fatalf("5 次等待合计=%v want ≈60s", sum)
	}
	if last < 30*time.Second {
		t.Fatalf("第 5 次等待=%v want ≈31s（2 倍指数权重）", last)
	}
}
