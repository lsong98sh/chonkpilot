package llmtest

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-llm/pluginbridge"
	"github.com/chonkpilot/chonkpilot-plugin-sdk/pluginsdk"
)

// TestPluginSDKBindSubjectRoundTrip 是「插件订阅宿主主题（含请求-应答）」的 L2 用例：
// 真实插件桥 + pluginsdk 客户端（进程内直连 UDS）。
//
// 覆盖 2026-10-24 新增的 `pluginsdk.BindSubject`（复用宿主既有 plugin.tool.bind）：
//   - 宿主总线上的主题请求 → 转发为 plugin.tool.call → 插件 handler 返回值写回 v.Result；
//   - handler 返回 error → 请求方 v.Errors 非空（**前置打点钩子失败 = 拒绝该工具调用**的语义基础）；
//   - 事件订阅（plugin.register subscriptions）仍为异步单向。
func TestPluginSDKBindSubjectRoundTrip(t *testing.T) {
	bus := newBus(t)
	addr := filepath.Join(t.TempDir(), "plugin.sock")
	pb := pluginbridge.New(pluginbridge.Options{Bus: bus, Addr: addr, Version: "test"})
	if err := pb.Start(); err != nil {
		t.Fatalf("pluginbridge start: %v", err)
	}
	t.Cleanup(func() { _ = pb.Close() })

	p, err := pluginsdk.New(pluginsdk.Options{
		ID: "t.sdk", Name: "sdk-test", Version: "0.0.0", Addr: pb.Addr(), Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("pluginsdk.New: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })

	if err := p.BindSubject("t-req", func(_ context.Context, payload json.RawMessage) (any, error) {
		var m map[string]any
		_ = json.Unmarshal(payload, &m)
		if m["fail"] == true {
			return nil, errors.New("pre-hook rejected")
		}
		return map[string]any{"echo": m["tool"], "has_call_id": m["call_id"] != nil}, nil
	}); err != nil {
		t.Fatalf("BindSubject: %v", err)
	}

	evt := make(chan string, 4)
	p.Subscribe("t-evt", func(_ context.Context, _ string, payload []byte) {
		var m map[string]any
		if json.Unmarshal(payload, &m) != nil {
			return
		}
		if k, ok := m["k"].(string); ok {
			evt <- k
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	})

	// 请求-应答：宿主发主题请求 → 插件 handler 返回值写回（宿主侧会补 call_id）。
	v := bus.Emit(context.Background(), "t-req", map[string]any{"tool": "file_write"}).Wait()
	if err := v.Err(); err != nil {
		t.Fatalf("主题绑定请求失败: %v", err)
	}
	res, _ := v.Result.(map[string]any)
	if res["echo"] != "file_write" {
		t.Fatalf("绑定 handler 结果未写回: %#v", v.Result)
	}

	// handler error → 请求方收到错误（gateway 据此拒绝工具调用）。
	if v := bus.Emit(context.Background(), "t-req", map[string]any{"tool": "x", "fail": true}).Wait(); v.Err() == nil {
		t.Fatal("handler error 应回传为请求失败")
	}

	// 事件订阅：重试投递直到插件完成 plugin.register（订阅表就绪）。
	deadline := time.Now().Add(5 * time.Second)
	for {
		bus.Emit(context.Background(), "t-evt", map[string]any{"k": "hello"})
		select {
		case got := <-evt:
			if got != "hello" {
				t.Fatalf("事件载荷不符: %q", got)
			}
			return
		case <-time.After(50 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("事件未送达插件")
		}
	}
}
