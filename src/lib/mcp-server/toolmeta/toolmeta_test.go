package toolmeta

import (
	"context"
	"sync"

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

// 2026-11-08：`tool_result` 已删除；其轮询/等待实现与对应单测（TestToolResult* /
// TestNextTaskResultPollBackoff）随实现一并移除。本文件只保留插件桥 fakeConn 夹具。