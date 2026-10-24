package bridgebus

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// fakeBridge 记录适配器对插件桥的调用，并按需返回结果。
type fakeBridge struct {
	subs    map[string]func(ctx context.Context, topic string, payload []byte)
	binds   map[string]func(ctx context.Context, payload json.RawMessage) (any, error)
	read    []dataCall
	write   []dataCall
	calls   []methodCall
	readRet json.RawMessage
	readErr error
	callRet json.RawMessage
	callErr error
}

type dataCall struct {
	resource, op, instanceID string
	params                   map[string]any
}

type methodCall struct {
	method string
	args   map[string]any
}

func newFake() *fakeBridge {
	return &fakeBridge{
		subs:  map[string]func(context.Context, string, []byte){},
		binds: map[string]func(context.Context, json.RawMessage) (any, error){},
	}
}

func (f *fakeBridge) opts() Options {
	return Options{
		Read: func(_ context.Context, resource, op, instanceID string, params map[string]any) (json.RawMessage, error) {
			f.read = append(f.read, dataCall{resource, op, instanceID, params})
			return f.readRet, f.readErr
		},
		Write: func(_ context.Context, resource, op, instanceID string, params map[string]any) (json.RawMessage, error) {
			f.write = append(f.write, dataCall{resource, op, instanceID, params})
			return f.readRet, f.readErr
		},
		Call: func(_ context.Context, method string, args any) (json.RawMessage, error) {
			m, _ := args.(map[string]any)
			f.calls = append(f.calls, methodCall{method: method, args: m})
			return f.callRet, f.callErr
		},
		Subscribe: func(pattern string, fn func(ctx context.Context, topic string, payload []byte)) {
			f.subs[pattern] = fn
		},
		Bind: func(subject string, fn func(ctx context.Context, payload json.RawMessage) (any, error)) error {
			f.binds[subject] = fn
			return nil
		},
	}
}

func newTestBus(t *testing.T, f *fakeBridge) *Bus {
	t.Helper()
	local, err := mq.New(mq.Options{})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	b := New(local, f.opts())
	t.Cleanup(func() { _ = b.Close() })
	return b
}

// TestOnEventRoutesToHostSubscription：事件主题 → 宿主异步订阅；事件到达 → 本地 handler 被调用。
func TestOnEventRoutesToHostSubscription(t *testing.T) {
	f := newFake()
	b := newTestBus(t, f)

	got := make(chan map[string]any, 1)
	if _, err := b.On("session-complete", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		_ = json.Unmarshal(v.Payload, &m)
		got <- m
		return nil
	}); err != nil {
		t.Fatalf("On(session-complete): %v", err)
	}
	sub, ok := f.subs["session-complete"]
	if !ok {
		t.Fatal("session-complete 应注册为宿主事件订阅")
	}
	sub(context.Background(), "session-complete", []byte(`{"session":"s1","turn":"t1"}`))
	select {
	case m := <-got:
		if m["session"] != "s1" || m["turn"] != "t1" {
			t.Fatalf("事件载荷不符: %+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("事件未派发到本地 handler")
	}
}

// TestOnRequestRoutesToHostBind：请求-应答主题 → plugin.tool.bind；返回值/错误原样回传。
func TestOnRequestRoutesToHostBind(t *testing.T) {
	f := newFake()
	b := newTestBus(t, f)

	if _, err := b.On("memory.flush", 0, func(_ context.Context, _ string, v *mq.Value) error {
		v.Result = map[string]any{"ok": true, "queued": true}
		return nil
	}); err != nil {
		t.Fatalf("On(memory.flush): %v", err)
	}
	bind, ok := f.binds["memory.flush"]
	if !ok {
		t.Fatal("memory.flush 应经 plugin.tool.bind 绑定")
	}
	out, err := bind(context.Background(), json.RawMessage(`{"instance_id":"i1","session":"s1"}`))
	if err != nil {
		t.Fatalf("bind handler: %v", err)
	}
	m, _ := out.(map[string]any)
	if m == nil || m["queued"] != true {
		t.Fatalf("绑定 handler 返回值不符: %#v", out)
	}

	// handler 返回 error → 传播给请求方（gateway 据此拒绝工具调用）。
	if _, err := b.On("history-pre-tool-hook", 0, func(_ context.Context, _ string, _ *mq.Value) error {
		return errors.New("checkpoint failed")
	}); err != nil {
		t.Fatalf("On(history-pre-tool-hook): %v", err)
	}
	if _, err := f.binds["history-pre-tool-hook"](context.Background(), nil); err == nil {
		t.Fatal("handler error 应回传（前置钩子失败 = 拒绝工具调用）")
	}
}

// TestEmitDataReadSynthesizesPersistReply：data-* 请求 → plugin/v1/data.*；应答合成 persist 同形信封。
func TestEmitDataReadSynthesizesPersistReply(t *testing.T) {
	f := newFake()
	f.readRet = json.RawMessage(`{"ok":true,"result":{"data":"true"}}`)
	b := newTestBus(t, f)

	reply := make(chan map[string]any, 1)
	if _, err := b.On("data-prj-config-load", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		_ = json.Unmarshal(v.Payload, &m)
		if m["ok"] != nil && m["req_id"] == "r1" {
			reply <- m
		}
		return nil
	}); err != nil {
		t.Fatalf("On(data-prj-config-load): %v", err)
	}
	b.Emit(context.Background(), "data-prj-config-load", map[string]any{
		"instance_id": "i1",
		"data":        map[string]any{"id": "history.enabled"},
		"req_id":      "r1",
	})
	if len(f.read) != 1 {
		t.Fatalf("应调用一次 data.read，got %d", len(f.read))
	}
	c := f.read[0]
	if c.resource != "config" || c.op != "prj.get" || c.instanceID != "i1" {
		t.Fatalf("data 路由不符: %+v", c)
	}
	if c.params["req_id"] != "r1" {
		t.Fatalf("req_id 应逐字透传（MQ 载荷不变）: %+v", c.params)
	}
	d, _ := c.params["data"].(map[string]any)
	if d["id"] != "history.enabled" {
		t.Fatalf("data 参数应逐字透传: %+v", c.params)
	}
	select {
	case m := <-reply:
		if m["ok"] != true || m["req_id"] != "r1" {
			t.Fatalf("合成应答不符: %+v", m)
		}
		if r, _ := m["result"].(map[string]any); r["data"] != "true" {
			t.Fatalf("合成应答 result 不符: %+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("合成应答未派发")
	}
}

// TestEmitDataWriteError：数据面失败 → 合成 ok=false + error（调用方按失败处理）。
func TestEmitDataWriteError(t *testing.T) {
	f := newFake()
	f.readErr = errors.New("persist 拒绝")
	b := newTestBus(t, f)

	reply := make(chan map[string]any, 1)
	if _, err := b.On("data-memory-save", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		_ = json.Unmarshal(v.Payload, &m)
		if m["ok"] != nil {
			reply <- m
		}
		return nil
	}); err != nil {
		t.Fatalf("On(data-memory-save): %v", err)
	}
	b.Emit(context.Background(), "data-memory-save", map[string]any{
		"instance_id": "i1", "data": map[string]any{"category": "开发规范"}, "req_id": "r2",
	})
	if len(f.write) != 1 || f.write[0].op != "write" {
		t.Fatalf("应走 data.write(memory.write): %+v", f.write)
	}
	select {
	case m := <-reply:
		if m["ok"] != false || m["error"] != "persist 拒绝" {
			t.Fatalf("失败应答不符: %+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("失败应答未派发")
	}
}

// TestEmitCallRoutesAndResult：llm-simple / tools.register 走宿主方法面，结果写入 Value.Result。
func TestEmitCallRoutesAndResult(t *testing.T) {
	f := newFake()
	f.callRet = json.RawMessage(`{"text":"rewritten"}`)
	b := newTestBus(t, f)

	v := b.Emit(context.Background(), "llm-simple", map[string]any{"prompt": "p", "instance_id": "i1"}).Wait()
	if err := v.Err(); err != nil {
		t.Fatalf("llm-simple: %v", err)
	}
	m, _ := v.Result.(map[string]any)
	if m == nil || m["text"] != "rewritten" {
		t.Fatalf("llm-simple 结果不符: %#v", v.Result)
	}
	if len(f.calls) != 1 || f.calls[0].method != "plugin/v1/llm.step" {
		t.Fatalf("llm-simple 应走 plugin/v1/llm.step: %+v", f.calls)
	}

	f.callRet = json.RawMessage(`{"accepted":true}`)
	v = b.Emit(context.Background(), "mcp-tools-register", map[string]any{
		"name": "history_status", "handler_subject": "history-tool-call", "pre_hook_subject": "history-pre-tool-hook",
	}).Wait()
	if v.Result == nil {
		t.Fatal("tools.register 应返回非空结果（插件据此判定已注册）")
	}
	if f.calls[1].method != "plugin/v1/tools.register" {
		t.Fatalf("mcp-tools-register 应走 plugin/v1/tools.register: %+v", f.calls[1])
	}
	if f.calls[1].args["pre_hook_subject"] != "history-pre-tool-hook" {
		t.Fatalf("注册载荷应逐字透传（含 pre_hook_subject）: %+v", f.calls[1].args)
	}
}

// TestUnknownSubjectRejected：未映射主题 → On/Emit 明确报错（不隐式直通）。
func TestUnknownSubjectRejected(t *testing.T) {
	f := newFake()
	b := newTestBus(t, f)
	if _, err := b.On("some-other-topic", 0, func(context.Context, string, *mq.Value) error { return nil }); err == nil {
		t.Fatal("未映射主题的 On 应报错")
	}
	if v := b.Emit(context.Background(), "some-other-topic", nil).Wait(); v.Err() == nil {
		t.Fatal("未映射主题的 Emit 应报错")
	}
}

// TestInstanceHintFromEventPayload：载荷自带 instance_id + work_dir 时，本地实例视图先补一次登记
// （进程外插件启动/重启晚于 instance-register 时的自愈；正常路径为幂等重复登记）。
func TestInstanceHintFromEventPayload(t *testing.T) {
	f := newFake()
	b := newTestBus(t, f)

	regs := make(chan map[string]any, 2)
	if _, err := b.On("instance-register", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		_ = json.Unmarshal(v.Payload, &m)
		regs <- m
		return nil
	}); err != nil {
		t.Fatalf("On(instance-register): %v", err)
	}
	if _, err := b.On("session-complete", 0, func(context.Context, string, *mq.Value) error { return nil }); err != nil {
		t.Fatalf("On(session-complete): %v", err)
	}
	f.subs["session-complete"](context.Background(), "session-complete",
		[]byte(`{"instance_id":"i9","work_dir":"E:/ws","session":"s1","turn":"t1"}`))
	select {
	case m := <-regs:
		if m["instance_id"] != "i9" || m["work_dir"] != "E:/ws" {
			t.Fatalf("实例补登记载荷不符: %+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("实例补登记未派发")
	}
}
