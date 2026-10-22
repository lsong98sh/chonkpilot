// SDK 契约测试：帧传输与 HTTP 传输共用同一 Client 接口（pluginclient 已下沉 core 模块）。
package pluginbridge_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/pluginclient"
	"github.com/chonkpilot/chonkpilot-llm/pluginbridge"
)

// startBridge 起一个真实插件桥（帧前门恒开；withHTTP 时另开 HTTP 随机端口）。
func startBridge(t *testing.T) *pluginbridge.Server {
	t.Helper()
	bus, err := mq.New(mq.Options{})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	opts := pluginbridge.Options{
		Bus:     bus,
		Addr:    filepath.Join(t.TempDir(), "plugin.sock"),
		Version: "test",
	}
	srv := pluginbridge.New(opts)
	srv.Register("test.echo", func(_ context.Context, _ *pluginbridge.Session, args json.RawMessage) (any, error) {
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

func waitEvent(t *testing.T, ch <-chan pluginclient.Event, kind, name string, timeout time.Duration) pluginclient.Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("事件通道已关闭（等待 %s/%s）", kind, name)
			}
			if ev.Kind == kind && ev.Name == name {
				return ev
			}
		case <-deadline:
			t.Fatalf("等待 %s/%s 超时", kind, name)
		}
	}
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

// runClientContract 是两种传输共用的契约用例。
func runClientContract(t *testing.T, cli pluginclient.Client, srv *pluginbridge.Server, pluginID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if cli.PluginID() != pluginID {
		t.Fatalf("PluginID=%q，期望 %q", cli.PluginID(), pluginID)
	}
	if !strings.HasPrefix(cli.InstanceID(), "plugin:"+pluginID+":") {
		t.Fatalf("InstanceID=%q", cli.InstanceID())
	}

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

	if err := cli.Subscribe(ctx, []pluginclient.Subscription{{
		ID: "s1", Pattern: "session-receive", Mode: "async",
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	srv.Broadcast("server-ready", map[string]any{"form": "test"})
	ev := waitEvent(t, cli.Events(), "evt", "server-ready", 3*time.Second)
	var payload map[string]any
	if err := json.Unmarshal(ev.Payload, &payload); err != nil || payload["form"] != "test" {
		t.Fatalf("server-ready payload=%s err=%v", ev.Payload, err)
	}

	if !srv.ShutdownPlugin(pluginID) {
		t.Fatal("ShutdownPlugin 未找到会话")
	}
	ev = waitEvent(t, cli.Events(), "ctl", "plugin.shutdown", 3*time.Second)
	if err := json.Unmarshal(ev.Payload, &payload); err != nil || payload["grace_ms"] != float64(5000) {
		t.Fatalf("plugin.shutdown payload=%s err=%v", ev.Payload, err)
	}

	// Close 幂等。
	if err := cli.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := cli.Close(); err != nil {
		t.Fatalf("close 第二次: %v", err)
	}
}

// TestFrameClient：帧传输 hello / call / subscribe / 事件 / ctl / Close。
func TestFrameClient(t *testing.T) {
	srv := startBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cli, err := pluginclient.Dial(ctx, pluginclient.Options{
		Addr:     srv.Addr(),
		PluginID: "test.frame.bot",
		Name:     "frame-bot",
		Version:  "1.0",
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	runClientContract(t, cli, srv, "test.frame.bot")
}

// PRODSIM-1：插件 HTTP 前门已删除（能力外溢统一走 UDS 帧传输），原 TestHTTPClient /
// TestHTTPClientToken / TestHTTPClientServerClose 三个用例随之移除。
