package bridge

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	transport "github.com/lsong98sh/chonkpilot-plugin-framework/protocol"
	mq "github.com/lsong98sh/chonkpilot-plugin-framework/runtime/bus"
)

func TestPluginBridgeFlow(t *testing.T) {
	bus, err := mq.New(mq.Options{})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	defer bus.Close()

	addr := filepath.Join(t.TempDir(), "plugin.sock")
	srv := New(Options{
		Bus:                 bus,
		Addr:                addr,
		Version:             "test",
		ExistingInstanceIDs: func() []string { return []string{"gui-1"} },
	})
	srv.Register("plugin/v1/test.echo", func(_ context.Context, _ *Session, args json.RawMessage) (any, error) {
		var in struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, errf(CodeArgsInvalid, "bad args")
		}
		return map[string]any{"echo": in.Text}, nil
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cli, err := transport.Dial(ctx, transport.Options{Backend: transport.BackendUDS, Addr: srv.Addr()})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cli.Close()

	events := make(chan transport.Frame, 16)
	go func() {
		for {
			f, rerr := cli.Recv()
			if rerr != nil {
				close(events)
				return
			}
			if f.Kind == transport.KindReq {
				// 服务端 → 插件请求（如 plugin.tool.call）：应答后不进入事件通道。
				if f.Name == "plugin.tool.call" {
					_ = cli.Send(transport.Frame{Kind: transport.KindRes, ID: f.ID, OK: true, Result: map[string]any{
						"result": map[string]any{"echo": "tool-ok"},
					}})
				} else {
					_ = cli.Send(transport.Frame{Kind: transport.KindRes, ID: f.ID, Errors: []string{"unsupported"}})
				}
				continue
			}
			events <- f
		}
	}()

	// 1. 未 hello 前只能 ping / hello
	res, err := cli.Call(ctx, "plugin.sys.ping", nil)
	if err != nil || !res.OK {
		t.Fatalf("ping: %+v %v", res, err)
	}
	res, err = cli.Call(ctx, "plugin/v1/test.echo", map[string]any{"text": "x"})
	if err != nil {
		t.Fatalf("echo before hello: %v", err)
	}
	if res.OK || len(res.Errors) == 0 || !strings.Contains(res.Errors[0], CodePluginUnregistered) {
		t.Fatalf("expected plugin_unregistered, got %+v", res)
	}

	// 2. hello
	res, err = cli.Call(ctx, "plugin.hello", map[string]any{"id": "com.example.bot", "features": map[string]any{"fs": "read"}})
	if err != nil || !res.OK {
		t.Fatalf("hello: %+v %v", res, err)
	}
	m, _ := res.Value.(map[string]any)
	inst, _ := m["instance_id"].(string)
	if !strings.HasPrefix(inst, "plugin:com.example.bot:") {
		t.Fatalf("instance_id=%q", inst)
	}

	// hello 改 id → 拒绝
	res, _ = cli.Call(ctx, "plugin.hello", map[string]any{"id": "other.bot"})
	if res.OK || len(res.Errors) == 0 || !strings.Contains(res.Errors[0], CodePluginUnauthorized) {
		t.Fatalf("expected id mismatch, got %+v", res)
	}

	// 3. register + subscribe
	res, err = cli.Call(ctx, "plugin.register", map[string]any{
		"features": map[string]any{"fs": "none"},
		"subscriptions": []map[string]any{{
			"id":      "s1",
			"pattern": "session-receive",
			"mode":    "async",
			"filter":  map[string]any{"instance_id": "gui-1"},
		}},
	})
	if err != nil || !res.OK {
		t.Fatalf("register: %+v %v", res, err)
	}

	// 4. 能力调用
	res, err = cli.Call(ctx, "plugin/v1/test.echo", map[string]any{"text": "hi"})
	if err != nil || !res.OK {
		t.Fatalf("echo: %+v %v", res, err)
	}

	// 5. 事件过滤：命中
	bus.Emit(ctx, "session-receive", map[string]any{"instance_id": "gui-1", "text": "a"}).Wait()
	f := recvFrame(t, events, 2*time.Second)
	if f.Name != "session-receive" {
		t.Fatalf("event name=%q", f.Name)
	}

	// 未命中（实例不同）→ 不投递
	bus.Emit(ctx, "session-receive", map[string]any{"instance_id": "gui-2"}).Wait()
	mustNoFrame(t, events, 300*time.Millisecond)

	// 未命中（主题不同）→ 不投递
	bus.Emit(ctx, "task-updated", map[string]any{"instance_id": "gui-1"}).Wait()
	mustNoFrame(t, events, 300*time.Millisecond)

	// 6. 实例保护
	res, _ = cli.Call(ctx, "plugin/v1/instance.claim", map[string]any{"instance_id": inst})
	if !res.OK {
		t.Fatalf("claim own: %+v", res)
	}
	res, _ = cli.Call(ctx, "plugin/v1/instance.claim", map[string]any{"instance_id": "gui-1"})
	if res.OK || len(res.Errors) == 0 || !strings.Contains(res.Errors[0], CodeInstanceForbidden) {
		t.Fatalf("expected instance forbidden, got %+v", res)
	}
	res, _ = cli.Call(ctx, "plugin/v1/instance.list", nil)
	if !res.OK {
		t.Fatalf("instance list: %+v", res)
	}

	// 7. 未知方法
	res, _ = cli.Call(ctx, "plugin/v1/nope", nil)
	if res.OK || len(res.Errors) == 0 || !strings.Contains(res.Errors[0], CodeMethodNotFound) {
		t.Fatalf("expected method_not_found, got %+v", res)
	}

	// 8. 默认 MQ 能力映射（plugin/v1/llm.step → llm-simple）
	if _, err := bus.On("llm-simple", 0, func(_ context.Context, _ string, v *mq.Value) error {
		v.Result = map[string]any{"text": "pong"}
		return nil
	}); err != nil {
		t.Fatalf("bus on: %v", err)
	}
	res, err = cli.Call(ctx, "plugin/v1/llm.step", map[string]any{"prompt": "hi"})
	if err != nil || !res.OK {
		t.Fatalf("llm.step: %+v %v", res, err)
	}
	if m, _ := res.Value.(map[string]any); m["text"] != "pong" {
		t.Fatalf("llm.step result=%+v", res.Value)
	}

	// 9. 外部工具绑定：bus 主题 → 插件 plugin.tool.call → 结果写回
	res, err = cli.Call(ctx, "plugin.tool.bind", map[string]any{"subject": "ext-tool-call"})
	if err != nil || !res.OK {
		t.Fatalf("tool bind: %+v %v", res, err)
	}
	v := bus.Emit(ctx, "ext-tool-call", map[string]any{"name": "demo"}).Wait()
	if v.Err() != nil {
		t.Fatalf("ext tool err: %v", v.Err())
	}
	if m, _ := v.Result.(map[string]any); m["echo"] != "tool-ok" {
		t.Fatalf("ext tool result=%+v", v.Result)
	}

	// 10. filter 注册（只登记，执行链 B1）
	res, _ = cli.Call(ctx, "plugin.filter.register", map[string]any{"stage": "llm.before_send"})
	if !res.OK {
		t.Fatalf("filter register: %+v", res)
	}
}

func recvFrame(t *testing.T, events <-chan transport.Frame, d time.Duration) transport.Frame {
	t.Helper()
	select {
	case f, ok := <-events:
		if !ok {
			t.Fatalf("事件通道已关闭")
		}
		return f
	case <-time.After(d):
		t.Fatalf("等待事件超时")
		return transport.Frame{}
	}
}

func mustNoFrame(t *testing.T, events <-chan transport.Frame, d time.Duration) {
	t.Helper()
	select {
	case f := <-events:
		t.Fatalf("不应收到事件: %+v", f)
	case <-time.After(d):
	}
}
