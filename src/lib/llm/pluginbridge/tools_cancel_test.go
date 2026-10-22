package pluginbridge

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/transport"
)

// toolCallSeen 是测试插件收到的一次 plugin.tool.call。
type toolCallSeen struct {
	frameID   int64
	payload   map[string]any
	callID    string
	noTimeout bool
}

// startToolBridge 起一个插件桥 + 一个测试插件连接（hello + tool.bind）。
// 返回总线、绑定后的 cancel 通道、调用观察通道。
func startToolBridge(t *testing.T, subject string) (mq.Bus, chan string, chan toolCallSeen) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cli, err := transport.Dial(ctx, transport.Options{Backend: transport.BackendUDS, Addr: srv.Addr()})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { cli.Close() })

	calls := make(chan toolCallSeen, 4)
	cancels := make(chan string, 4)
	go func() {
		for {
			f, rerr := cli.Recv()
			if rerr != nil {
				return
			}
			switch {
			case f.Kind == transport.KindReq && f.Name == "plugin.tool.call":
				var outer struct {
					Payload map[string]any `json:"payload"`
				}
				_ = json.Unmarshal(f.Payload, &outer)
				seen := toolCallSeen{frameID: f.ID, payload: outer.Payload}
				seen.callID, _ = outer.Payload["call_id"].(string)
				seen.noTimeout, _ = outer.Payload["no_timeout"].(bool)
				calls <- seen
			case f.Kind == transport.KindCtl && f.Name == "plugin.tool.cancel":
				var in struct {
					CallID string `json:"call_id"`
				}
				_ = json.Unmarshal(f.Payload, &in)
				cancels <- in.CallID
			}
		}
	}()

	if res, err := cli.Call(ctx, "plugin.hello", map[string]any{"id": "test.tools"}); err != nil || !res.OK {
		t.Fatalf("hello: %+v %v", res, err)
	}
	if res, err := cli.Call(ctx, "plugin.tool.bind", map[string]any{"subject": subject}); err != nil || !res.OK {
		t.Fatalf("bind: %+v %v", res, err)
	}
	return bus, cancels, calls
}

// TestToolCallNoTimeoutCancelPropagation：契约 timeout=0（no_timeout=true）时跨进程调用
// **不施加 11 分钟兜底**；gateway 侧 ctx 取消 → 插件收到 plugin.tool.cancel（同 call_id），
// 调用方拿到 context.Canceled（BRIDGE-2 ⑥：取消即时传播）。
func TestToolCallNoTimeoutCancelPropagation(t *testing.T) {
	const subject = "test-meta-ext"
	bus, cancels, calls := startToolBridge(t, subject)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(250 * time.Millisecond)
		cancel()
	}()
	fut := bus.Emit(ctx, subject, map[string]any{"tool": "mcp_invoke", "no_timeout": true})

	var seen toolCallSeen
	select {
	case seen = <-calls:
	case <-time.After(3 * time.Second):
		t.Fatal("插件未收到 plugin.tool.call")
	}
	if seen.callID == "" {
		t.Fatalf("载荷缺 call_id：%v", seen.payload)
	}
	if !seen.noTimeout {
		t.Fatalf("no_timeout 未透传：%v", seen.payload)
	}

	// 不响应 → 等 gateway 取消；插件必须收到同 call_id 的取消帧。
	select {
	case id := <-cancels:
		if id != seen.callID {
			t.Fatalf("取消帧 call_id=%q，期望 %q", id, seen.callID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("取消后插件未收到 plugin.tool.cancel")
	}

	err := fut.Wait().Err()
	if err == nil {
		t.Fatal("调用方应返回取消错误")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("错误应为 context.Canceled，实际 %v", err)
	}
}

// TestToolCallTimeoutStillApplies：未声明 timeout=0 的外部工具仍受跨进程兜底上限约束，
// 超时同样发出 plugin.tool.cancel（避免插件侧孤儿处理器）。
func TestToolCallTimeoutStillApplies(t *testing.T) {
	const subject = "test-limited-ext"
	bus, cancels, calls := startToolBridge(t, subject)

	old := toolCallTimeout
	toolCallTimeout = 150 * time.Millisecond
	t.Cleanup(func() { toolCallTimeout = old })

	start := time.Now()
	fut := bus.Emit(context.Background(), subject, map[string]any{"tool": "slow"})
	select {
	case <-calls:
	case <-time.After(3 * time.Second):
		t.Fatal("插件未收到 plugin.tool.call")
	}
	select {
	case <-cancels:
	case <-time.After(3 * time.Second):
		t.Fatal("超时后插件未收到 plugin.tool.cancel")
	}
	err := fut.Wait().Err()
	if err == nil {
		t.Fatal("超时后调用方应返回错误")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("错误应为 DeadlineExceeded，实际 %v", err)
	}
	if d := time.Since(start); d < 100*time.Millisecond || d > 3*time.Second {
		t.Fatalf("超时应在 ~150ms 生效，实际 %v", d)
	}
}
