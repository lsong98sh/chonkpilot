package pluginbridge

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/transport"
)

// startReadyBridge 起一个真实插件桥（临时 UDS），并返回服务端。
func startReadyBridge(t *testing.T) *Server {
	t.Helper()
	bus, err := mq.New(mq.Options{})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	srv := New(Options{Bus: bus, Addr: filepath.Join(t.TempDir(), "plugin.sock"), Version: "test"})
	if err := srv.Start(); err != nil {
		bus.Close()
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { srv.Close(); bus.Close() })
	return srv
}

// dialReadyPlugin 连接插件桥并完成 plugin.hello。
func dialReadyPlugin(t *testing.T, srv *Server, id string) transport.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cli, err := transport.Dial(ctx, transport.Options{Backend: transport.BackendUDS, Addr: srv.Addr()})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { cli.Close() })
	res, err := cli.Call(ctx, "plugin.hello", map[string]any{"id": id})
	if err != nil || !res.OK {
		t.Fatalf("hello: %+v %v", res, err)
	}
	return cli
}

// waitEvent 在给定连接上等待指定名字的 evt 帧。
func waitEvent(t *testing.T, cli transport.Conn, name string, timeout time.Duration) transport.Frame {
	t.Helper()
	type got struct {
		f   transport.Frame
		err error
	}
	ch := make(chan got, 1)
	go func() {
		for {
			f, err := cli.Recv()
			if err != nil {
				ch <- got{err: err}
				return
			}
			if f.Kind == transport.KindEvt && f.Name == name {
				ch <- got{f: f}
				return
			}
		}
	}()
	select {
	case g := <-ch:
		if g.err != nil {
			t.Fatalf("等待 %s 时连接断开: %v", name, g.err)
		}
		return g.f
	case <-time.After(timeout):
		t.Fatalf("未收到 %s 事件", name)
		return transport.Frame{}
	}
}

func decodePayload(t *testing.T, f transport.Frame) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(f.Payload, &out); err != nil {
		t.Fatalf("事件载荷解析失败: %v (raw=%s)", err, f.Payload)
	}
	return out
}

// TestServerReadyBroadcast：已连接插件在 MarkReady 后收到 evt server-ready。
func TestServerReadyBroadcast(t *testing.T) {
	srv := startReadyBridge(t)
	cli := dialReadyPlugin(t, srv, "test.ready.bot")

	srv.MarkReady(map[string]any{"form": "gui", "seq": 1})
	f := waitEvent(t, cli, "server-ready", 3*time.Second)
	got := decodePayload(t, f)
	if got["form"] != "gui" {
		t.Fatalf("载荷不符: %v", got)
	}
	if v, _ := got["seq"].(float64); v != 1 {
		t.Fatalf("seq=%v，期望 1", got["seq"])
	}
}

// TestServerReadyLateConnect：MarkReady 之后才连上的插件在 hello 后立即收到补发。
func TestServerReadyLateConnect(t *testing.T) {
	srv := startReadyBridge(t)
	srv.MarkReady(map[string]any{"form": "server"})

	cli := dialReadyPlugin(t, srv, "test.ready.late")
	f := waitEvent(t, cli, "server-ready", 3*time.Second)
	got := decodePayload(t, f)
	if got["form"] != "server" {
		t.Fatalf("载荷不符: %v", got)
	}
}

// TestServerReadyRepeatAndNilPayload：重复 MarkReady 更新载荷并再次广播；nil 载荷 = `{}`。
func TestServerReadyRepeatAndNilPayload(t *testing.T) {
	srv := startReadyBridge(t)
	cli := dialReadyPlugin(t, srv, "test.ready.repeat")

	srv.MarkReady(map[string]any{"seq": 1})
	if got := decodePayload(t, waitEvent(t, cli, "server-ready", 3*time.Second)); got["seq"] != float64(1) {
		t.Fatalf("第一次载荷不符: %v", got)
	}
	srv.MarkReady(nil)
	if got := decodePayload(t, waitEvent(t, cli, "server-ready", 3*time.Second)); len(got) != 0 {
		t.Fatalf("nil 载荷应为 {}，实际 %v", got)
	}
	if ready, payload := srv.Ready(); !ready || payload == nil {
		t.Fatalf("Ready()=%v,%v", ready, payload)
	}
}
