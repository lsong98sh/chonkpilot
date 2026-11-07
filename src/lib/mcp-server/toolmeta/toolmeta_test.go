package toolmeta

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lsong98sh/chonkpilot-framework/host/core/transport"
)

// fakeConn 是 toolmeta 测试用的最小 transport.Conn（只实现 Call）。
type fakeConn struct {
	mu    sync.Mutex
	calls []string
	// records 与 calls 一一对应，保留每次调用的载荷（断言桥参数字段用）。
	records []recordedCall
	fn      func(ctx context.Context, method string) transport.Result
}

// recordedCall 是一次插件桥调用（方法名 + 原样载荷）。
type recordedCall struct {
	Method  string
	Payload any
}

func (c *fakeConn) Send(transport.Frame) error { return nil }
func (c *fakeConn) Recv() (transport.Frame, error) {
	return transport.Frame{}, nil
}
func (c *fakeConn) Call(ctx context.Context, name string, payload any) (transport.Result, error) {
	c.mu.Lock()
	c.calls = append(c.calls, name)
	c.records = append(c.records, recordedCall{Method: name, Payload: payload})
	fn := c.fn
	c.mu.Unlock()
	if fn == nil {
		return transport.Result{OK: true, Value: map[string]any{}}, nil
	}
	return fn(ctx, name), nil
}
func (c *fakeConn) Principal() transport.Principal { return transport.NilPrincipal }
func (c *fakeConn) Remote() string                 { return "fake" }
func (c *fakeConn) Closed() <-chan struct{}        { return nil }
func (c *fakeConn) Close() error                   { return nil }

func (c *fakeConn) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

// callCountOf 统计某方法的调用次数（区分 task.result 与 task.wait）。
func (c *fakeConn) callCountOf(method string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, name := range c.calls {
		if name == method {
			n++
		}
	}
	return n
}

// withFastTaskResultPoll 缩短轮询退避（测试不真等 500ms/2s）。
func withFastTaskResultPoll(t *testing.T) {
	t.Helper()
	oldInitial, oldMax := taskResultPollInitial, taskResultPollMax
	taskResultPollInitial = 5 * time.Millisecond
	taskResultPollMax = 10 * time.Millisecond
	t.Cleanup(func() {
		taskResultPollInitial, taskResultPollMax = oldInitial, oldMax
	})
}

// 任务从 running → 终态：宿主 `task.wait` 事件回执直接返回终态（本进程不再轮询探测）。
func TestToolResultWaitsOnTerminalEvent(t *testing.T) {
	cli := &fakeConn{fn: func(_ context.Context, method string) transport.Result {
		switch method {
		case "plugin/v1/task.result":
			return transport.Result{OK: true, Value: map[string]any{"found": true, "state": "running"}}
		case "plugin/v1/task.wait":
			return transport.Result{OK: true, Value: map[string]any{"found": true, "state": "done", "text": "finished"}}
		default: // task.node.done
			return transport.Result{OK: true, Value: map[string]any{}}
		}
	}}

	start := time.Now()
	got := toolResult(context.Background(), cli, Context{}, map[string]any{"id": "tk-1", "timeout": 5.0}, "own-node")
	if got != "finished" {
		t.Fatalf("toolResult = %q, want finished", got)
	}
	if n := cli.callCountOf("plugin/v1/task.wait"); n != 1 {
		t.Fatalf("task.wait 调用次数 = %d, want 1", n)
	}
	if n := cli.callCountOf("plugin/v1/task.result"); n != 1 {
		t.Fatalf("task.result 应只探测一次（之后由事件回执驱动），got %d", n)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("事件回执应立即返回，耗时 %v", elapsed)
	}
}

// 旧宿主无 task.wait（方法缺失）→ 回落指数退避探测（兼容），running → done 后返回终态。
func TestToolResultFallsBackWhenWaitUnsupported(t *testing.T) {
	withFastTaskResultPoll(t)
	calls := 0
	cli := &fakeConn{fn: func(_ context.Context, method string) transport.Result {
		switch method {
		case "plugin/v1/task.wait":
			return transport.Result{OK: false, Errors: []string{"method_not_found"}}
		case "plugin/v1/task.node.done":
			return transport.Result{OK: true, Value: map[string]any{}}
		}
		calls++
		if calls < 3 {
			return transport.Result{OK: true, Value: map[string]any{"found": true, "state": "running"}}
		}
		return transport.Result{OK: true, Value: map[string]any{"found": true, "state": "done", "text": "finished"}}
	}}

	got := toolResult(context.Background(), cli, Context{}, map[string]any{"id": "tk-1", "timeout": 5.0}, "own-node")
	if got != "finished" {
		t.Fatalf("toolResult = %q, want finished", got)
	}
	if calls != 3 {
		t.Fatalf("task.result 调用次数 = %d, want 3", calls)
	}
}

// timeout=0 → 立即查询一次；running 即返回「任务尚未结束」（不等待）。
func TestToolResultZeroTimeoutImmediate(t *testing.T) {
	withFastTaskResultPoll(t)
	cli := &fakeConn{fn: func(_ context.Context, method string) transport.Result {
		if method == "plugin/v1/task.node.done" {
			return transport.Result{OK: true, Value: map[string]any{}}
		}
		return transport.Result{OK: true, Value: map[string]any{"found": true, "state": "running"}}
	}}

	start := time.Now()
	got := toolResult(context.Background(), cli, Context{}, map[string]any{"id": "tk-2", "timeout": 0.0}, "own-node")
	if !strings.Contains(got, "任务尚未结束") {
		t.Fatalf("timeout=0 running 应返回「任务尚未结束」: %q", got)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("timeout=0 不应等待，耗时 %v", elapsed)
	}
}

// ctx 取消 → 等待中的 task.wait / 退避 timer 及时返回「已取消」。
func TestToolResultCancelDuringWait(t *testing.T) {
	withFastTaskResultPoll(t)
	cli := &fakeConn{fn: func(ctx context.Context, method string) transport.Result {
		switch method {
		case "plugin/v1/task.node.done":
			return transport.Result{OK: true, Value: map[string]any{}}
		case "plugin/v1/task.wait":
			<-ctx.Done() // 模拟宿主事件等待被取消
			return transport.Result{OK: true, Value: map[string]any{"found": true, "state": "running"}}
		}
		return transport.Result{OK: true, Value: map[string]any{"found": true, "state": "running"}}
	}}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	got := toolResult(ctx, cli, Context{}, map[string]any{"id": "tk-3", "timeout": 30.0}, "own-node")
	if !strings.Contains(got, "已取消") {
		t.Fatalf("取消应返回「已取消」: %q", got)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("取消应及时打断等待，耗时 %v", elapsed)
	}
}

// 退避序列：500ms → 1s → 2s 封顶。
func TestNextTaskResultPollBackoff(t *testing.T) {
	oldInitial, oldMax := taskResultPollInitial, taskResultPollMax
	taskResultPollInitial = 500 * time.Millisecond
	taskResultPollMax = 2 * time.Second
	defer func() {
		taskResultPollInitial, taskResultPollMax = oldInitial, oldMax
	}()
	if got := nextTaskResultPoll(0); got != 500*time.Millisecond {
		t.Fatalf("首次退避 = %v", got)
	}
	if got := nextTaskResultPoll(500 * time.Millisecond); got != time.Second {
		t.Fatalf("第二次退避 = %v", got)
	}
	if got := nextTaskResultPoll(time.Second); got != 2*time.Second {
		t.Fatalf("第三次退避 = %v", got)
	}
	if got := nextTaskResultPoll(2 * time.Second); got != 2*time.Second {
		t.Fatalf("退避封顶 = %v", got)
	}
}
