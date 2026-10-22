package pluginbridge

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/transport"
)

func TestFilterChainActions(t *testing.T) {
	bus, _ := mq.New(mq.Options{})
	defer bus.Close()
	addr := filepath.Join(t.TempDir(), "plugin.sock")
	srv := New(Options{Bus: bus, Addr: addr})
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cli, err := transport.Dial(ctx, transport.Options{Backend: transport.BackendUDS, Addr: srv.Addr()})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cli.Close()

	if res, err := cli.Call(ctx, "plugin.hello", map[string]any{"id": "filter.bot"}); err != nil || !res.OK {
		t.Fatalf("hello: %+v %v", res, err)
	}
	if res, err := cli.Call(ctx, "plugin.filter.register", map[string]any{"stage": "llm.before_send", "id": "f1"}); err != nil || !res.OK {
		t.Fatalf("filter register: %+v %v", res, err)
	}

	var action atomic.Value
	action.Store("pass")
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			f, rerr := cli.Recv()
			if rerr != nil {
				return
			}
			if f.Kind != transport.KindReq {
				continue
			}
			act, _ := action.Load().(string)
			switch act {
			case "timeout":
				continue
			case "error":
				_ = cli.Send(transport.Frame{Kind: transport.KindRes, ID: f.ID, Errors: []string{"boom"}})
			case "drop":
				_ = cli.Send(transport.Frame{Kind: transport.KindRes, ID: f.ID, OK: true, Result: map[string]any{"action": "drop"}})
			case "replace":
				_ = cli.Send(transport.Frame{Kind: transport.KindRes, ID: f.ID, OK: true, Result: map[string]any{
					"action": "replace", "payload": json.RawMessage(`{"content":"filtered"}`),
				}})
			default:
				_ = cli.Send(transport.Frame{Kind: transport.KindRes, ID: f.ID, OK: true, Result: map[string]any{"action": "pass"}})
			}
		}
	}()

	chain := srv.Filters()
	chain.FilterTimeout = 100 * time.Millisecond
	chain.ChainTimeout = time.Second
	in := json.RawMessage(`{"content":"original"}`)

	action.Store("pass")
	out, err := chain.Run(ctx, "llm.before_send", in)
	if err != nil || string(out) != string(in) {
		t.Fatalf("pass: %s %v", out, err)
	}

	action.Store("replace")
	out, err = chain.Run(ctx, "llm.before_send", in)
	if err != nil || !strings.Contains(string(out), "filtered") {
		t.Fatalf("replace: %s %v", out, err)
	}

	action.Store("drop")
	if _, err = chain.Run(ctx, "llm.before_send", in); err == nil || !strings.Contains(err.Error(), CodeFilterDropped) {
		t.Fatalf("drop: %v", err)
	}

	action.Store("error")
	if _, err = chain.Run(ctx, "llm.before_send", in); err == nil || !strings.Contains(err.Error(), CodeFilterFailed) {
		t.Fatalf("error: %v", err)
	}

	action.Store("timeout")
	if _, err = chain.Run(ctx, "llm.before_send", in); err == nil || !strings.Contains(err.Error(), CodeFilterTimeout) {
		t.Fatalf("timeout: %v", err)
	}

	// 无 filter 的 stage = 原样返回
	action.Store("pass")
	out, err = chain.Run(ctx, "llm.before_start", in)
	if err != nil || string(out) != string(in) {
		t.Fatalf("no filter: %s %v", out, err)
	}
}
