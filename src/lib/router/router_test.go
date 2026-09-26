// LR-2 白盒：Provider 注册 / 注销（幂等）、协议分派、Call 事件通道形态、能力查询、并发安全。
package router

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeAdapter 是测试用适配器（白盒验证注册表与 Call 契约；协议名与内置适配器同名时后者被覆盖）。
type fakeAdapter struct {
	proto string
	capsV Caps
	// streamFn 为空 → 投递一条 text_delta + 一条 done。
	streamFn func(ctx context.Context, spec Spec, req Request, out chan<- Event) error

	mu    sync.Mutex
	calls int
	last  Request
}

func (f *fakeAdapter) Protocol() string { return f.proto }
func (f *fakeAdapter) Caps() Caps       { return f.capsV }

func (f *fakeAdapter) Stream(ctx context.Context, spec Spec, req Request, out chan<- Event) error {
	f.mu.Lock()
	f.calls++
	f.last = req
	f.mu.Unlock()
	if f.streamFn != nil {
		return f.streamFn(ctx, spec, req, out)
	}
	out <- Event{Type: EvTextDelta, Text: "hi"}
	out <- Event{Type: EvDone, FinishReason: "stop", Model: req.Options.Model}
	return nil
}

func (f *fakeAdapter) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// drain 读空通道并把事件按序收集（同时验证「done/error 之后通道关闭」）。
func drain(t *testing.T, ch <-chan Event) []Event {
	t.Helper()
	var evs []Event
	for {
		ev, ok := <-ch
		if !ok {
			return evs
		}
		evs = append(evs, ev)
	}
}

// TestRegisterRejectsEmptyNameOrProtocol：Name / Protocol 为空 → *Error{Kind: invalid}。
func TestRegisterRejectsEmptyNameOrProtocol(t *testing.T) {
	r := New(Config{})
	for _, spec := range []Spec{
		{Protocol: ProtocolOpenAI}, // 无名
		{Name: "  ", Protocol: ProtocolOpenAI},
		{Name: "p1"}, // 无协议
		{Name: "p1", Protocol: " "},
	} {
		err := r.Register(spec)
		var e *Error
		if !errors.As(err, &e) || e.Kind != ErrorInvalid {
			t.Fatalf("Register(%+v) err=%v want *Error{invalid}", spec, err)
		}
	}
}

// TestRegisterIsIdempotentAndOverwrites：同 Name 重复注册 = 幂等覆盖（不报错、不重复登记）。
func TestRegisterIsIdempotentAndOverwrites(t *testing.T) {
	r := New(Config{})
	if err := r.Register(Spec{Name: "p1", Protocol: ProtocolOpenAI, DefaultModel: "m1"}); err != nil {
		t.Fatalf("首次 Register: %v", err)
	}
	if err := r.Register(Spec{Name: "p1", Protocol: ProtocolOpenAI, DefaultModel: "m2"}); err != nil {
		t.Fatalf("重复 Register 应幂等，实得 %v", err)
	}
	if n := len(r.specs); n != 1 {
		t.Fatalf("注册表条数=%d want 1（同 Name 覆盖）", n)
	}
	if got := r.specs["p1"].DefaultModel; got != "m2" {
		t.Fatalf("覆盖后 DefaultModel=%q want m2", got)
	}
	// 协议名归一（大小写 / 空白）后再登记。
	if err := r.Register(Spec{Name: "p2", Protocol: " OpenAI "}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got := r.specs["p2"].Protocol; got != ProtocolOpenAI {
		t.Fatalf("协议归一=%q want %q", got, ProtocolOpenAI)
	}
}

// TestUnregister：未注册 → invalid；已注册 → 注销成功且再调用变 invalid。
func TestUnregister(t *testing.T) {
	r := New(Config{})
	var e *Error
	if err := r.Unregister("ghost"); !errors.As(err, &e) || e.Kind != ErrorInvalid {
		t.Fatalf("Unregister(未注册) err=%v want *Error{invalid}", err)
	}
	if err := r.Register(Spec{Name: "p1", Protocol: ProtocolOpenAI}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.Unregister("p1"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if _, err := r.Call(context.Background(), Request{Provider: "p1"}); !errors.As(err, &e) || e.Kind != ErrorInvalid {
		t.Fatalf("注销后 Call err=%v want *Error{invalid}", err)
	}
}

// TestCallUnregisteredProvider：provider 未注册 → 不返回通道 + *Error{invalid}。
func TestCallUnregisteredProvider(t *testing.T) {
	r := New(Config{})
	ch, err := r.Call(context.Background(), Request{Provider: "nope"})
	var e *Error
	if !errors.As(err, &e) || e.Kind != ErrorInvalid || e.Retryable {
		t.Fatalf("Call err=%v want *Error{invalid, 不可重试}", err)
	}
	if ch != nil {
		t.Fatalf("前置校验失败不应返回通道，实得 %v", ch)
	}
}

// TestCallUnknownProtocolWithoutAdapter：provider 已注册但协议无适配器（未知协议名）→ invalid，且
// 不静默回落 openai。（`responses` 自 D-29 起已有内置适配器，见 TestBuiltinAdaptersRegistered。）
func TestCallUnknownProtocolWithoutAdapter(t *testing.T) {
	r := New(Config{})
	if err := r.Register(Spec{Name: "p2", Protocol: "brand-new-proto"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	ch, err := r.Call(context.Background(), Request{Provider: "p2"})
	var e *Error
	if !errors.As(err, &e) || e.Kind != ErrorInvalid {
		t.Fatalf("Call err=%v want *Error{invalid}", err)
	}
	if ch != nil {
		t.Fatalf("协议未注册适配器不应返回通道，实得 %v", ch)
	}
}

// TestCallStreamsEventsAndClosesChannel：事件按序到达、终态后通道关闭、Model 回落 DefaultModel。
func TestCallStreamsEventsAndClosesChannel(t *testing.T) {
	r := New(Config{})
	fa := &fakeAdapter{proto: ProtocolOpenAI, capsV: Caps{Stream: true, Tools: true}}
	if err := r.registerAdapter(fa); err != nil {
		t.Fatalf("registerAdapter: %v", err)
	}
	if err := r.Register(Spec{Name: "p1", Protocol: ProtocolOpenAI, DefaultModel: "m-default"}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ch, err := r.Call(context.Background(), Request{Provider: "p1", Messages: []Message{{Role: RoleUser, Content: []Part{{Type: PartText, Text: "q"}}}}})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	evs := drain(t, ch)
	if len(evs) != 2 || evs[0].Type != EvTextDelta || evs[0].Text != "hi" || evs[1].Type != EvDone || evs[1].FinishReason != "stop" {
		t.Fatalf("事件序列=%+v want [text_delta hi, done stop]", evs)
	}
	if evs[1].Model != "m-default" {
		t.Fatalf("Model=%q want m-default（Options.Model 空 → 回落 Spec.DefaultModel）", evs[1].Model)
	}
	if got := fa.callCount(); got != 1 {
		t.Fatalf("适配器被调用 %d 次 want 1", got)
	}
	if fa.last.Options.Model != "m-default" {
		t.Fatalf("适配器收到 Options.Model=%q want m-default", fa.last.Options.Model)
	}
	// 显示指定 Model 时不被回落覆盖。
	ch2, err := r.Call(context.Background(), Request{Provider: "p1", Options: CallOptions{Model: "m-explicit"}})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	drain(t, ch2)
	if fa.last.Options.Model != "m-explicit" {
		t.Fatalf("适配器收到 Options.Model=%q want m-explicit", fa.last.Options.Model)
	}
}

// TestCallAdapterErrorBecomesErrorEvent：适配器返回的分类错误原样随 EvError 事件投递。
func TestCallAdapterErrorBecomesErrorEvent(t *testing.T) {
	r := New(Config{})
	want := &Error{Kind: ErrorRateLimit, Message: "429 too many", Retryable: true, Status: 429, RetryAfter: 3 * time.Second}
	fa := &fakeAdapter{proto: ProtocolOpenAI, streamFn: func(_ context.Context, _ Spec, _ Request, _ chan<- Event) error {
		return want
	}}
	if err := r.registerAdapter(fa); err != nil {
		t.Fatalf("registerAdapter: %v", err)
	}
	if err := r.Register(Spec{Name: "p1", Protocol: ProtocolOpenAI}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	ch, err := r.Call(context.Background(), Request{Provider: "p1"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	evs := drain(t, ch)
	if len(evs) != 1 || evs[0].Type != EvError || evs[0].Err != want {
		t.Fatalf("事件=%+v want 单条 error 且保留原错误", evs)
	}
}

// TestCallUnclassifiedErrorBecomesProtocol：适配器返回未分类 error → 归为 protocol（不可重试）。
func TestCallUnclassifiedErrorBecomesProtocol(t *testing.T) {
	r := New(Config{})
	fa := &fakeAdapter{proto: ProtocolOpenAI, streamFn: func(_ context.Context, _ Spec, _ Request, _ chan<- Event) error {
		return errors.New("boom")
	}}
	if err := r.registerAdapter(fa); err != nil {
		t.Fatalf("registerAdapter: %v", err)
	}
	if err := r.Register(Spec{Name: "p1", Protocol: ProtocolOpenAI}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	ch, err := r.Call(context.Background(), Request{Provider: "p1"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	evs := drain(t, ch)
	if len(evs) != 1 || evs[0].Type != EvError {
		t.Fatalf("事件=%+v want 单条 error", evs)
	}
	if e := evs[0].Err; e == nil || e.Kind != ErrorProtocol || e.Retryable || e.Message != "boom" {
		t.Fatalf("err=%+v want {protocol, 不可重试}", evs[0].Err)
	}
}

// TestCallNilContextDoesNotPanic：ctx 为 nil → 等价 Background（不 panic）。
func TestCallNilContextDoesNotPanic(t *testing.T) {
	r := New(Config{})
	if err := r.registerAdapter(&fakeAdapter{proto: ProtocolOpenAI}); err != nil {
		t.Fatalf("registerAdapter: %v", err)
	}
	if err := r.Register(Spec{Name: "p1", Protocol: ProtocolOpenAI}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	ch, err := r.Call(nil, Request{Provider: "p1"}) // nil ctx：覆盖兜底分支
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if evs := drain(t, ch); len(evs) != 2 {
		t.Fatalf("事件=%+v want 2", evs)
	}
}

// TestCapabilities：已注册且有适配器 → 适配器矩阵；未注册 / 无适配器 → 零值。
func TestCapabilities(t *testing.T) {
	r := New(Config{})
	caps := Caps{Stream: true, Tools: true, Images: true}
	if err := r.registerAdapter(&fakeAdapter{proto: ProtocolOpenAI, capsV: caps}); err != nil {
		t.Fatalf("registerAdapter: %v", err)
	}
	if err := r.Register(Spec{Name: "p1", Protocol: ProtocolOpenAI}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.Register(Spec{Name: "p2", Protocol: "brand-new-proto"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got := r.Capabilities("p1"); got != caps {
		t.Fatalf("Capabilities(p1)=%+v want %+v", got, caps)
	}
	if got := r.Capabilities("ghost"); got != (Caps{}) {
		t.Fatalf("Capabilities(未注册)=%+v want 零值", got)
	}
	if got := r.Capabilities("p2"); got != (Caps{}) {
		t.Fatalf("Capabilities(无适配器)=%+v want 零值", got)
	}
}

// TestBuiltinAdaptersRegistered：New 装配内置协议适配器（LR-3 openai / D-29 responses / LR-4 anthropic
// / D-30 echo）—— 注册对应协议的 provider 后能力矩阵即来自内置适配器（非零值），且各自声明不同。
func TestBuiltinAdaptersRegistered(t *testing.T) {
	r := New(Config{})
	if err := r.Register(Spec{Name: "oai", Protocol: ProtocolOpenAI}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.Register(Spec{Name: "ant", Protocol: " Anthropic "}); err != nil { // 协议名归一
		t.Fatalf("Register: %v", err)
	}
	if err := r.Register(Spec{Name: "resp", Protocol: ProtocolResponses}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	oai, ant, resp := r.Capabilities("oai"), r.Capabilities("ant"), r.Capabilities("resp")
	if !oai.Stream || !oai.Tools || !oai.UsageInStream {
		t.Fatalf("openai 能力矩阵=%+v want 流式 / 工具 / 流内 usage", oai)
	}
	if !ant.Stream || !ant.Tools || !ant.MaxTokensRequired {
		t.Fatalf("anthropic 能力矩阵=%+v want 流式 / 工具 / max_tokens 必填", ant)
	}
	if !resp.Stream || !resp.Tools || !resp.UsageInStream {
		t.Fatalf("responses 能力矩阵=%+v want 流式 / 工具 / 流内 usage（D-29 已装配）", resp)
	}
	if oai != resp {
		t.Fatalf("responses 与 openai 同族，能力矩阵应等价：oai=%+v resp=%+v", oai, resp)
	}
	if oai == ant {
		t.Fatalf("anthropic 与 OpenAI 家族的能力矩阵不应相同：%+v", oai)
	}
}

// TestCallBuiltinEchoFallback：D-30 —— **无可用 provider** 时回落内置 echo（`Provider` 空串 = 无可用
// provider；内置保留名 `echo` 同路由，兼容既有 usr `defaultLLM: "echo"`）：不发 HTTP、按**固定文案**
// 回复（取文 = 最后一条**真实用户消息**，排除 notify / continue / resume 系统注入消息），并投一条 done。
func TestCallBuiltinEchoFallback(t *testing.T) {
	r := New(Config{}) // 未注册任何 provider
	// 尾部两条 user 消息是系统注入（notify / continue）→ 不算真实用户消息，取文应取更早的「第一问」。
	msgs := []Message{
		{Role: RoleUser, Content: []Part{{Type: PartText, Text: "第一问"}}},
		{Role: RoleAssistant, Content: []Part{{Type: PartText, Text: "第一答"}}},
		{Role: RoleUser, Kind: "notify", Content: []Part{{Type: PartText, Text: "工具通知（不算真实用户消息）"}}},
		{Role: RoleUser, Kind: "continue", Content: []Part{{Type: PartText, Text: "自动续写"}}},
	}
	const wantText = "收到第一问，目前无法回复，请设置LLM。"
	for _, provider := range []string{"", "echo", "  echo  "} {
		ch, err := r.Call(context.Background(), Request{Provider: provider, Messages: msgs})
		if err != nil {
			t.Fatalf("provider=%q Call: %v（无可用 provider 应回落内置 echo）", provider, err)
		}
		evs := drain(t, ch)
		if len(evs) != 2 || evs[0].Type != EvTextDelta || evs[0].Text != wantText || evs[1].Type != EvDone || evs[1].FinishReason != "stop" {
			t.Fatalf("provider=%q 事件=%+v want [text_delta %q, done stop]", provider, evs, wantText)
		}
		if evs[1].Model != "echo" {
			t.Fatalf("provider=%q Model=%q want echo（回落内置 DefaultModel）", provider, evs[1].Model)
		}
	}
	// 兜底不因带工具/图片而失败（忽略），不影响「未注册名仍报错」。
	ch, err := r.Call(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: []Part{{Type: PartText, Text: "q"}}}},
		Tools:    []ToolDef{{Name: "t"}},
	})
	if err != nil {
		t.Fatalf("带工具的兜底调用不应失败：%v", err)
	}
	if evs := drain(t, ch); len(evs) != 2 || evs[0].Type != EvTextDelta {
		t.Fatalf("事件=%+v want [text_delta, done]", evs)
	}
	var e *Error
	if _, err := r.Call(context.Background(), Request{Provider: "ghost"}); !errors.As(err, &e) || e.Kind != ErrorInvalid {
		t.Fatalf("未注册名 err=%v want *Error{invalid}（兜底只认空名 / 内置保留名）", err)
	}
}

// TestCallTransparentRetryOnConnectionFailure：口径例① —— **连接层失败 1 次后成功**：透明重发 1 次，
// 上层只看到**一份**结果（判据 = 尚未向上层输出过任何事件）。
func TestCallTransparentRetryOnConnectionFailure(t *testing.T) {
	restore := lowerRetryBackoff(t)
	defer restore()

	r := New(Config{})
	fa := &fakeAdapter{proto: ProtocolOpenAI}
	fa.streamFn = func(_ context.Context, _ Spec, req Request, out chan<- Event) error {
		if fa.callCount() == 1 {
			return &Error{Kind: ErrorNetwork, Message: "dial tcp: connection refused", Retryable: true}
		}
		out <- Event{Type: EvTextDelta, Text: "hi", Model: req.Options.Model}
		out <- Event{Type: EvDone, FinishReason: "stop", Model: req.Options.Model}
		return nil
	}
	if err := r.registerAdapter(fa); err != nil {
		t.Fatalf("registerAdapter: %v", err)
	}
	if err := r.Register(Spec{Name: "p1", Protocol: ProtocolOpenAI, DefaultModel: "m"}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ch, err := r.Call(context.Background(), Request{Provider: "p1"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	evs := drain(t, ch)
	if len(evs) != 2 || evs[0].Type != EvTextDelta || evs[0].Text != "hi" || evs[1].Type != EvDone {
		t.Fatalf("事件=%+v want 一份 [text_delta hi, done]（重发不产生重复事件）", evs)
	}
	if got := fa.callCount(); got != 2 {
		t.Fatalf("适配器被调用 %d 次 want 2（首次连接失败 + 透明重发 1 次）", got)
	}
}

// TestCallTransparentRetryOnConnectionFailure 的**反面**：超时同为可重试类，但**不属连接层**
// → router **不**透明重发（归 llm 的可见重试）。
func TestCallTransparentRetrySkipsTimeout(t *testing.T) {
	restore := lowerRetryBackoff(t)
	defer restore()

	r := New(Config{})
	fa := &fakeAdapter{proto: ProtocolOpenAI, streamFn: func(_ context.Context, _ Spec, _ Request, _ chan<- Event) error {
		return &Error{Kind: ErrorTimeout, Message: "首字节超时", Retryable: true}
	}}
	if err := r.registerAdapter(fa); err != nil {
		t.Fatalf("registerAdapter: %v", err)
	}
	if err := r.Register(Spec{Name: "p1", Protocol: ProtocolOpenAI}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	ch, err := r.Call(context.Background(), Request{Provider: "p1"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	evs := drain(t, ch)
	if len(evs) != 1 || evs[0].Type != EvError || evs[0].Err.Kind != ErrorTimeout {
		t.Fatalf("事件=%+v want 单条 error{timeout} 原样透出", evs)
	}
	if got := fa.callCount(); got != 1 {
		t.Fatalf("适配器被调用 %d 次 want 1（超时不透明重发）", got)
	}
}

// TestCallNoTransparentRetryAfterOutput：口径例② —— **已输出 `text_delta` 后中断**（`network`）：
// **不重发**、立即透出错误（已可见的内容不能重复 / 不能回退）。
func TestCallNoTransparentRetryAfterOutput(t *testing.T) {
	restore := lowerRetryBackoff(t)
	defer restore()

	r := New(Config{})
	fa := &fakeAdapter{proto: ProtocolOpenAI, streamFn: func(_ context.Context, _ Spec, _ Request, out chan<- Event) error {
		out <- Event{Type: EvTextDelta, Text: "半截"}
		return &Error{Kind: ErrorNetwork, Message: "流读取中断", Retryable: true}
	}}
	if err := r.registerAdapter(fa); err != nil {
		t.Fatalf("registerAdapter: %v", err)
	}
	if err := r.Register(Spec{Name: "p1", Protocol: ProtocolOpenAI}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	ch, err := r.Call(context.Background(), Request{Provider: "p1"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	evs := drain(t, ch)
	if len(evs) != 2 || evs[0].Type != EvTextDelta || evs[0].Text != "半截" || evs[1].Type != EvError || evs[1].Err.Kind != ErrorNetwork {
		t.Fatalf("事件=%+v want [text_delta 半截, error{network}]（已输出 → 不重发）", evs)
	}
	if got := fa.callCount(); got != 1 {
		t.Fatalf("适配器被调用 %d 次 want 1（已输出任何事件 → 绝不重发）", got)
	}
}

// TestCallTransparentRetryCapped：口径例③ —— **重试 1 次仍失败**：次数封顶（最多 2 次尝试），
// 透出错误且不再重发。
func TestCallTransparentRetryCapped(t *testing.T) {
	restore := lowerRetryBackoff(t)
	defer restore()

	r := New(Config{})
	counts := 0
	fa := &fakeAdapter{proto: ProtocolOpenAI, streamFn: func(_ context.Context, _ Spec, _ Request, _ chan<- Event) error {
		counts++
		return &Error{Kind: ErrorNetwork, Message: "connection refused", Retryable: true}
	}}
	if err := r.registerAdapter(fa); err != nil {
		t.Fatalf("registerAdapter: %v", err)
	}
	if err := r.Register(Spec{Name: "p1", Protocol: ProtocolOpenAI}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	ch, err := r.Call(context.Background(), Request{Provider: "p1"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	evs := drain(t, ch)
	if len(evs) != 1 || evs[0].Type != EvError || evs[0].Err.Kind != ErrorNetwork {
		t.Fatalf("事件=%+v want 单条 error{network}", evs)
	}
	if counts != 1+transparentRetryMax {
		t.Fatalf("适配器被调用 %d 次 want %d（1 次 + 透明重试上限 %d）", counts, 1+transparentRetryMax, transparentRetryMax)
	}
}

// TestCallTransparentRetryCancelDuringBackoff：退避等待期间消费方取消 → 立即收手（不投错误事件、
// 不再重发）。
func TestCallTransparentRetryCancelDuringBackoff(t *testing.T) {
	transparentRetryBackoff = 500 * time.Millisecond
	defer func() { transparentRetryBackoff = 300 * time.Millisecond }()

	r := New(Config{})
	fa := &fakeAdapter{proto: ProtocolOpenAI, streamFn: func(_ context.Context, _ Spec, _ Request, _ chan<- Event) error {
		return &Error{Kind: ErrorNetwork, Message: "connection refused", Retryable: true}
	}}
	if err := r.registerAdapter(fa); err != nil {
		t.Fatalf("registerAdapter: %v", err)
	}
	if err := r.Register(Spec{Name: "p1", Protocol: ProtocolOpenAI}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := r.Call(ctx, Request{Provider: "p1"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	time.Sleep(50 * time.Millisecond) // 让首次尝试完成、进入退避
	cancel()
	if evs := drain(t, ch); len(evs) != 0 {
		t.Fatalf("取消后不应投事件：%+v", evs)
	}
	if got := fa.callCount(); got != 1 {
		t.Fatalf("适配器被调用 %d 次 want 1（退避期取消 → 不再重发）", got)
	}
}

// lowerRetryBackoff 把透明重试退避降到毫秒级（用例提速），返回恢复函数。
func lowerRetryBackoff(t *testing.T) func() {
	t.Helper()
	old := transparentRetryBackoff
	transparentRetryBackoff = time.Millisecond
	return func() { transparentRetryBackoff = old }
}

// TestConcurrentRegisterCallCapabilities：并发注册 / 注销 / 调用 / 查能力不 panic、不数据竞争
// （配合 -race 验证）。
func TestConcurrentRegisterCallCapabilities(t *testing.T) {
	r := New(Config{})
	if err := r.registerAdapter(&fakeAdapter{proto: ProtocolOpenAI}); err != nil {
		t.Fatalf("registerAdapter: %v", err)
	}
	if err := r.Register(Spec{Name: "p1", Protocol: ProtocolOpenAI}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("p-%d", i)
			for n := 0; n < 20; n++ {
				_ = r.Register(Spec{Name: name, Protocol: ProtocolOpenAI})
				_ = r.Capabilities(name)
				_ = r.Capabilities("p1")
				ch, err := r.Call(context.Background(), Request{Provider: "p1"})
				if err == nil {
					for range ch { // 读空至关闭
					}
				}
				_ = r.Unregister(name)
			}
		}(i)
	}
	wg.Wait()
}
