// client.go：codegraph 引擎子进程客户端（官方 go-sdk MCP client，stdio transport）。
//
// 每 workdir 一个 client（上层 p.clients[workdir] 持有），engine exe 以 `--stdio` 常驻子进程
// 形态懒拉起：首次该 workdir 的工具调用时 spawn + initialize 握手，之后复用；传输层失败自动
// 重连一次；该 workdir 无活跃实例超 childIdleTimeout 后由上层（sweepIdleClient）close 回收，
// 下次调用懒重建。多 workdir 各自独立子进程 → 索引内存/调用串行互不干扰。
package codegraph

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"

	"github.com/chonkpilot/chonkpilot-lib/winproc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// engineClient 引擎客户端契约（生产实现 = *client；包内测试可注入桩，
// 用于断言多 workdir 的调用路由与状态隔离）。
type engineClient interface {
	call(ctx context.Context, name string, args map[string]any) (text string, isErr bool, err error)
	close()
}

// client 单个引擎子进程客户端（线程安全；同一 client 调用串行）。
type client struct {
	exe string

	mu sync.Mutex // 保护 ss 与调用
	ss *mcp.ClientSession
}

// newClient 构建客户端（惰性，未 spawn）。
func newClient(exe string) *client {
	return &client{exe: exe}
}

// call 调用引擎工具，返回（文本, isErr, err）。
//   - isErr：引擎在协议内返回的失败（CallToolResult.IsError=true，文本为错误说明）；
//   - err：传输层/握手失败（会话不可用）。err != nil 时自动重连一次再试。
func (c *client) call(ctx context.Context, name string, args map[string]any) (string, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	text, isErr, err := c.callLocked(ctx, name, args)
	if err == nil || ctx.Err() != nil {
		return text, isErr, err // 成功 / 调用方超时（不再重试）
	}
	// 传输失败：丢弃死会话，重连一次
	c.closeLocked()
	return c.callLocked(ctx, name, args)
}

// callLocked 已持锁的单次调用（含懒连接）。
func (c *client) callLocked(ctx context.Context, name string, args map[string]any) (string, bool, error) {
	if err := c.connectLocked(ctx); err != nil {
		return "", false, err
	}
	res, err := c.ss.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return "", false, err
	}
	return resultText(res), res.IsError, nil
}

// connectLocked 确保已连接：无会话则 spawn `exe --stdio` 并完成 initialize 握手。
func (c *client) connectLocked(ctx context.Context) error {
	if c.ss != nil {
		return nil
	}
	cli := mcp.NewClient(&mcp.Implementation{Name: "chonkpilot-plugin-codegraph", Version: "0.1.0"}, nil)
	cmd := exec.Command(c.exe, "--stdio")
	// 引擎 exe 为 console 子系统：隐藏控制台窗口（不影响 stdio 传输管道）。
	cmd.SysProcAttr = winproc.SysProcAttr()
	ss, err := cli.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return fmt.Errorf("spawn %s --stdio: %w", c.exe, err)
	}
	c.ss = ss
	return nil
}

// close 关闭会话并结束子进程（幂等；空闲回收/停用时调用）。
func (c *client) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeLocked()
}

func (c *client) closeLocked() {
	if c.ss != nil {
		_ = c.ss.Close()
	}
	c.ss = nil
}

// resultText 提取 CallToolResult 的首段文本 content（引擎统一 textResult/errResult）。
func resultText(res *mcp.CallToolResult) string {
	for _, ct := range res.Content {
		if tc, ok := ct.(*mcp.TextContent); ok && tc.Text != "" {
			return tc.Text
		}
	}
	if len(res.Content) == 0 {
		return ""
	}
	if b, err := json.Marshal(res.Content); err == nil {
		return string(b)
	}
	return ""
}
