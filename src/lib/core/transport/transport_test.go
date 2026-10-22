package transport

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestTransportMatrix(t *testing.T) {
	for _, backend := range []Backend{BackendUDS, BackendTCP} {
		backend := backend
		t.Run(string(backend), func(t *testing.T) {
			testTransport(t, backend)
		})
	}
}

func testTransport(t *testing.T, backend Backend) {
	t.Helper()
	addr := "127.0.0.1:0"
	if backend == BackendUDS {
		addr = filepath.Join(t.TempDir(), "transport.sock")
	}
	ep, err := Listen(Options{
		Backend: backend,
		Addr:    addr,
		Authenticator: func(_ net.Conn) (Principal, error) {
			return Principal{ID: "tester"}, nil
		},
	})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ep.Close()

	dialAddr := addr
	if backend == BackendTCP {
		dialAddr = ep.Addr()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cli, err := Dial(ctx, Options{Backend: backend, Addr: dialAddr})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cli.Close()

	type accepted struct {
		c   Conn
		err error
	}
	ch := make(chan accepted, 1)
	go func() {
		c, err := ep.Accept()
		ch <- accepted{c, err}
	}()
	a := <-ch
	if a.err != nil {
		t.Fatalf("accept: %v", a.err)
	}
	srv := a.c
	defer srv.Close()

	if got := srv.Principal().ID; got != "tester" {
		t.Fatalf("principal=%q", got)
	}
	if cli.Remote() == "" || srv.Remote() == "" {
		t.Fatalf("remote addr empty")
	}

	// 客户端 → 服务端事件
	if err := cli.Send(Frame{Kind: KindEvt, Name: "hello", Payload: json.RawMessage(`{"n":1}`)}); err != nil {
		t.Fatalf("send evt: %v", err)
	}
	f, err := srv.Recv()
	if err != nil {
		t.Fatalf("recv evt: %v", err)
	}
	if f.Kind != KindEvt || f.Name != "hello" || string(f.Payload) != `{"n":1}` {
		t.Fatalf("unexpected frame: %+v", f)
	}

	// 服务端 → 客户端事件
	if err := srv.Send(Frame{Kind: KindEvt, Name: "down", Payload: json.RawMessage(`{"ok":true}`)}); err != nil {
		t.Fatalf("send down: %v", err)
	}
	f, err = cli.Recv()
	if err != nil || f.Name != "down" {
		t.Fatalf("recv down: %+v %v", f, err)
	}

	// 客户端 Call → 服务端应答
	go func() {
		req, rerr := srv.Recv()
		if rerr != nil {
			return
		}
		_ = srv.Send(Frame{Kind: KindRes, ID: req.ID, OK: true, Result: map[string]any{"pong": true}})
	}()
	res, err := cli.Call(ctx, "plugin.sys.ping", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.OK || res.Value == nil {
		t.Fatalf("call result: %+v", res)
	}

	// 服务端 Call → 客户端应答
	go func() {
		req, rerr := cli.Recv()
		if rerr != nil {
			return
		}
		_ = cli.Send(Frame{Kind: KindRes, ID: req.ID, OK: true, Result: "ok"})
	}()
	res, err = srv.Call(ctx, "plugin/v1/thing", nil)
	if err != nil || !res.OK || res.Value != "ok" {
		t.Fatalf("reverse call: %+v %v", res, err)
	}
}
