// I-83 探针单测（2026-09-19）：go-sdk **in-memory 传输**是否把 **client 侧 ctx cancel
// 传到 server 侧 handler**。
//
// 背景（18-工具异步超时与取消 §1「待验证项」/ §3.5 ④ / §7 B5）：取消/终止「是否可经 ctx
// 下沉」决定 in-memory 线（类① self 闭包 / 类②③④ llm 域工具 / 类⑤ 内嵌 executor）能否真终止。
// 本文件**只增事实、不改生产逻辑**：以与 memnode.go newMemNode 相同的建连顺序（server 先、
// client 后）复现同进程 in-memory 会话，令 handler 阻塞等 ctx.Done()，随后从 client 侧取消
// 该**调用** ctx，观察 server handler 是否被取消。
//
// 结论（实跑见 18 §1 待验证项 / 42 §2 决策记录）：**能传递** —— client 取消 → MCP 层发
// `notifications/cancelled`（mcp/transport.go:call → cancelCall）→ server 侧 canceller.Preempter
//
//	（mcp/transport.go）→ jsonrpc2 Connection.Cancel(reqID) → incomingRequest.cancel() →
//	handler 的请求 ctx 取消（internal/jsonrpc2/conn.go:acceptRequest/ handleAsync 用 req.ctx）。
//
// 注：in-memory 传输**不传 client ctx 的 value**（故 memnode.go 用协议 _meta 往返调用上下文），
// 但**取消信号**经 MCP 协议层显式通知传递，与 ctx value 是两条不同通路。
package mcpgateway

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestInMemoryCtxCancelReachesHandler 断言：client 侧取消调用 ctx → server handler 的 ctx 被取消。
func TestInMemoryCtxCancelReachesHandler(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "probe-server", Version: "1.0.0"}, nil)

	entered := make(chan struct{})  // handler 已进入
	observed := make(chan error, 1) // handler 观察到的取消错误（nil = 超时未取消）
	mcp.AddTool(srv, &mcp.Tool{Name: "block"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		close(entered)
		select {
		case <-ctx.Done():
			observed <- ctx.Err()
			return nil, nil, ctx.Err()
		case <-time.After(5 * time.Second):
			observed <- nil
			return nil, nil, nil
		}
	})

	// 与 memnode.go newMemNode 相同：server 先连（客户端连接即 initialize 握手）。
	connCtx, connCancel := context.WithCancel(context.Background())
	defer connCancel()
	srvTr, cliTr := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(connCtx, srvTr, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer func() { _ = ss.Close() }()
	cli := mcp.NewClient(&mcp.Implementation{Name: "probe-client", Version: "1.0.0"}, nil)
	cs, err := cli.Connect(connCtx, cliTr, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = cs.Close() }()

	// 每次调用一份可取消的调用 ctx（模拟 gateway 侧在飞调用的 ctx）。
	callCtx, callCancel := context.WithCancel(context.Background())
	defer callCancel()
	done := make(chan struct{})
	go func() {
		_, _ = cs.CallTool(callCtx, &mcp.CallToolParams{Name: "block"})
		close(done)
	}()

	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("handler 未在 3s 内进入（建连/调用失败）")
	}

	// 从 client 侧取消调用 ctx。
	callCancel()

	select {
	case err := <-observed:
		if err == nil {
			t.Fatal("结论=不能传递：client ctx cancel 未传到 server handler（handler 超时才退出）")
		}
		t.Logf("结论=能传递：server handler 观察到取消 err=%v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("结论=不能传递：client ctx cancel 后 3s 内 handler 未观察到取消")
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("client 调用未在取消后返回")
	}
}
