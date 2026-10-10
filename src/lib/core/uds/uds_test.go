package uds

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// startTestServer 起一个回显前门：`echo` 主题回写 payload；`instance-claim` 绑定连接实例。
func startTestServer(t *testing.T, path string) *Server {
	t.Helper()
	srv := NewServer(ServerOptions{
		Path: path,
		OnRequest: func(c *Conn, typ string, payload json.RawMessage) Response {
			switch typ {
			case "instance-claim":
				c.SetInstance("inst-1")
				return Response{OK: true, Result: map[string]any{"instance_id": "inst-1"}}
			case "echo":
				var m map[string]any
				_ = json.Unmarshal(payload, &m)
				return Response{OK: true, Result: m}
			default:
				return Response{OK: false, Errors: []string{"unknown: " + typ}}
			}
		},
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("server start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func TestUDSEchoAndEventAndControl(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	srv := startTestServer(t, path)

	ctlCh := make(chan string, 4)
	cli, err := Dial(ClientOptions{
		Path:      path,
		OnControl: func(typ string, _ []byte) { ctlCh <- typ },
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })

	// 上行请求-响应。
	v := cli.Emit(context.Background(), "echo", map[string]any{"a": 1}).Wait()
	if e := v.Err(); e != nil {
		t.Fatalf("emit: %v", e)
	}
	m, _ := v.Result.(map[string]any)
	if m == nil || m["a"] != float64(1) {
		t.Fatalf("echo result = %#v", v.Result)
	}

	// claim 绑定实例（instance ↔ 连接 1:1）。
	cv := cli.Emit(context.Background(), "instance-claim", map[string]any{}).Wait()
	if e := cv.Err(); e != nil {
		t.Fatalf("claim: %v", e)
	}

	// 下行事件：本地 `>` 订阅收得到。
	var gotMu sync.Mutex
	var got []string
	sub, err := cli.On(">", 0, func(_ context.Context, subject string, v *mq.Value) error {
		gotMu.Lock()
		got = append(got, subject)
		gotMu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("on: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	if !srv.SendEvent("inst-1", "session-receive", map[string]any{"x": 2}) {
		t.Fatalf("SendEvent: target conn not found")
	}

	// 定向控制帧：OnControl 收得到（不进本地订阅）。
	if !srv.SendControl("inst-1", "instance-goto-top", nil) {
		t.Fatalf("SendControl: target conn not found")
	}

	// 事件：轮询等待本地 `>` 订阅收到。
	evDeadline := time.After(3 * time.Second)
	for {
		gotMu.Lock()
		n := len(got)
		gotMu.Unlock()
		if n > 0 {
			break
		}
		select {
		case <-evDeadline:
			t.Fatalf("downstream event not received")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	gotMu.Lock()
	first := got[0]
	gotMu.Unlock()
	if first != "session-receive" {
		t.Fatalf("event subject = %q", first)
	}

	// 定向控制帧：等待 OnControl 回调。
	select {
	case typ := <-ctlCh:
		if typ != "instance-goto-top" {
			t.Fatalf("ctl type = %q", typ)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("control frame not received")
	}
}

func TestUDSDisconnectCallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	done := make(chan struct{}, 1)
	srv := NewServer(ServerOptions{
		Path: path,
		OnRequest: func(c *Conn, typ string, payload json.RawMessage) Response {
			c.SetInstance("inst-x")
			return Response{OK: true}
		},
		OnDisconnect: func(c *Conn) {
			if c.Instance() == "inst-x" {
				select {
				case done <- struct{}{}:
				default:
				}
			}
		},
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("server start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	cli, err := Dial(ClientOptions{Path: path})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = cli.Emit(context.Background(), "any", map[string]any{}).Wait()
	_ = cli.Close()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("onDisconnect not called")
	}
}

// TestUDSBindReplacementNoFalseExit（R7-B-2）：第二连接同 id 顶替 → 旧连接被关闭、
// 其断开**不**补发 exit（IsCurrent=false）；当前绑定连接断开才补发。
func TestUDSBindReplacementNoFalseExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	exits := make(chan string, 8)
	var srv *Server
	srv = NewServer(ServerOptions{
		Path: path,
		OnRequest: func(c *Conn, typ string, payload json.RawMessage) Response {
			var m map[string]any
			_ = json.Unmarshal(payload, &m)
			id, _ := m["instance_id"].(string)
			c.SetInstance(id)
			return Response{OK: true, Result: map[string]any{"instance_id": id}}
		},
		// 模拟 udsapi：仅当前绑定连接才补发 instance-exit。
		OnDisconnect: func(c *Conn) {
			if c.Instance() != "" && srv.IsCurrent(c) {
				exits <- c.Instance()
			}
		},
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("server start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	cliA, err := Dial(ClientOptions{Path: path})
	if err != nil {
		t.Fatalf("dial A: %v", err)
	}
	t.Cleanup(func() { _ = cliA.Close() })
	_ = cliA.Emit(context.Background(), "instance-claim", map[string]any{"instance_id": "X"}).Wait()

	cliB, err := Dial(ClientOptions{Path: path})
	if err != nil {
		t.Fatalf("dial B: %v", err)
	}
	t.Cleanup(func() { _ = cliB.Close() })
	_ = cliB.Emit(context.Background(), "instance-claim", map[string]any{"instance_id": "X"}).Wait()

	if !srv.Connected("X") {
		t.Fatal("顶替后新连接应为当前绑定")
	}
	// 被顶替的 A 断开：短时间内不得补发 exit（否则会误杀新连接实例）。
	select {
	case id := <-exits:
		t.Fatalf("被顶替连接的断开不应补发 instance-exit: %s", id)
	case <-time.After(500 * time.Millisecond):
	}
	// 当前绑定 B 断开 → 补发 exit X。
	_ = cliB.Close()
	select {
	case id := <-exits:
		if id != "X" {
			t.Fatalf("当前连接断开应补发 instance-exit X, got %q", id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("当前连接断开未补发 instance-exit")
	}
}

func TestUDSMultiConnRouting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	srv := NewServer(ServerOptions{
		Path: path,
		OnRequest: func(c *Conn, typ string, payload json.RawMessage) Response {
			var m map[string]any
			_ = json.Unmarshal(payload, &m)
			id, _ := m["instance_id"].(string)
			c.SetInstance(id)
			return Response{OK: true, Result: m}
		},
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("server start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	chA, chB := make(chan string, 4), make(chan string, 4)
	cliA, err := Dial(ClientOptions{Path: path})
	if err != nil {
		t.Fatalf("dial A: %v", err)
	}
	t.Cleanup(func() { _ = cliA.Close() })
	cliB, err := Dial(ClientOptions{Path: path})
	if err != nil {
		t.Fatalf("dial B: %v", err)
	}
	t.Cleanup(func() { _ = cliB.Close() })
	_, _ = cliA.On(">", 0, func(_ context.Context, s string, _ *mq.Value) error { chA <- s; return nil })
	_, _ = cliB.On(">", 0, func(_ context.Context, s string, _ *mq.Value) error { chB <- s; return nil })

	_ = cliA.Emit(context.Background(), "instance-claim", map[string]any{"instance_id": "A"}).Wait()
	_ = cliB.Emit(context.Background(), "instance-claim", map[string]any{"instance_id": "B"}).Wait()

	// 定向投递只到目标连接。
	if !srv.SendEvent("A", "session-receive", map[string]any{"instance_id": "A"}) {
		t.Fatalf("SendEvent A: not found")
	}
	select {
	case s := <-chA:
		if s != "session-receive" {
			t.Fatalf("A subject = %q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("A did not receive")
	}
	select {
	case s := <-chB:
		t.Fatalf("B should not receive, got %q", s)
	case <-time.After(300 * time.Millisecond):
	}
}
