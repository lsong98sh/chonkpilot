package bridge

import (
	"testing"
	"time"

	transport "github.com/lsong98sh/chonkpilot-plugin-framework/protocol"
)

// waitCtl 在给定连接上等待指定名字的 ctl 帧。
func waitCtl(t *testing.T, cli transport.Conn, name string, timeout time.Duration) transport.Frame {
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
			if f.Kind == transport.KindCtl && f.Name == name {
				ch <- got{f: f}
				return
			}
		}
	}()
	select {
	case g := <-ch:
		if g.err != nil {
			t.Fatalf("等待 ctl %s 时连接断开: %v", name, g.err)
		}
		return g.f
	case <-time.After(timeout):
		t.Fatalf("未收到 ctl %s", name)
		return transport.Frame{}
	}
}

// TestShutdownPluginSendsCtl：hello 后按 plugin_id 命中 → 收到 ctl plugin.shutdown{grace_ms:5000}。
func TestShutdownPluginSendsCtl(t *testing.T) {
	srv := startReadyBridge(t)
	cli := dialReadyPlugin(t, srv, "test.shutdown.bot")

	if !srv.ShutdownPlugin("test.shutdown.bot") {
		t.Fatal("ShutdownPlugin 未找到已 hello 的会话")
	}
	f := waitCtl(t, cli, "plugin.shutdown", 3*time.Second)
	got := decodePayload(t, f)
	if v, _ := got["grace_ms"].(float64); v != 5000 {
		t.Fatalf("grace_ms=%v，期望 5000", got["grace_ms"])
	}
}

// TestShutdownPluginUnknownID：未知 / 空 plugin_id → false。
func TestShutdownPluginUnknownID(t *testing.T) {
	srv := startReadyBridge(t)
	_ = dialReadyPlugin(t, srv, "test.shutdown.known")

	if srv.ShutdownPlugin("test.shutdown.unknown") {
		t.Fatal("未知 plugin_id 应返回 false")
	}
	if srv.ShutdownPlugin("") {
		t.Fatal("空 plugin_id 应返回 false")
	}
}
