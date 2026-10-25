// SDK 契约测试：框架 sdk 客户端连真实插件桥（hello / call / 订阅事件 / 关闭）。
package bridge_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	mq "github.com/lsong98sh/chonkpilot-plugin-framework/runtime/bus"
	sdk "github.com/lsong98sh/chonkpilot-plugin-framework/sdk"

	"github.com/chonkpilot/chonkpilot-lib/pluginhost/bridge"
)

// startBridge 起一个真实插件桥（帧前门；随机 UDS）。
func startBridge(t *testing.T) *bridge.Server {
	t.Helper()
	bus, err := mq.New(mq.Options{})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	opts := bridge.Options{
		Bus:     bus,
		Addr:    filepath.Join(t.TempDir(), "plugin.sock"),
		Version: "test",
	}
	srv := bridge.New(opts)
	srv.Register("test.echo", func(_ context.Context, _ *bridge.Session, args json.RawMessage) (any, error) {
		var in struct {
			Text string `json:"text"`
		}
		if len(args) > 0 {
			if err := json.Unmarshal(args, &in); err != nil {
				return nil, err
			}
		}
		return map[string]any{"echo": in.Text}, nil
	})
	if err := srv.Start(); err != nil {
		bus.Close()
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close(); _ = bus.Close() })
	return srv
}

func assertEcho(t *testing.T, raw json.RawMessage, want string) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("结果解析失败: %v (raw=%s)", err, raw)
	}
	if got["echo"] != want {
		t.Fatalf("echo=%v，期望 %q", got["echo"], want)
	}
}

// TestSDKClientContract：hello / call / 订阅事件 / 关闭 的最小契约。
func TestSDKClientContract(t *testing.T) {
	srv := startBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cli, err := sdk.New(sdk.Options{
		ID:      "test.frame.bot",
		Name:    "frame-bot",
		Version: "1.0",
		Addr:    srv.Addr(),
	})
	if err != nil {
		t.Fatalf("sdk.New: %v", err)
	}

	events := make(chan string, 8)
	cli.Subscribe("session-receive", func(_ context.Context, topic string, _ []byte) {
		select {
		case events <- topic:
		default:
		}
	})
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	done := make(chan error, 1)
	go func() { done <- cli.Run(runCtx) }()

	raw, err := cli.Call(ctx, "test.echo", map[string]any{"text": "hi"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	assertEcho(t, raw, "hi")

	if raw, err = cli.Call(ctx, "test.echo", nil); err != nil {
		t.Fatalf("call nil args: %v", err)
	} else {
		assertEcho(t, raw, "")
	}

	// 订阅在 Run 内注册，重试广播直到收到（有界等待）。
	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	received := false
	for !received {
		select {
		case topic := <-events:
			if topic == "session-receive" {
				received = true
			}
		case <-tick.C:
			srv.Broadcast("session-receive", map[string]any{"x": 1})
		case <-deadline:
			t.Fatal("等待 session-receive 事件超时")
		}
	}

	if !srv.ShutdownPlugin("test.frame.bot") {
		t.Fatal("ShutdownPlugin 未找到会话")
	}
	select {
	case err := <-done:
		if err != nil && ctx.Err() == nil {
			t.Fatalf("Run 退出错误: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("收到 plugin.shutdown 后 Run 未退出")
	}

	if err := cli.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := cli.Close(); err != nil {
		t.Fatalf("close 第二次: %v", err)
	}
}
