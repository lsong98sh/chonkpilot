// Windows service 接入（复用 chonkpilot-lib/winsvc）：安装/删除/运行桥接 HTTP 生命周期；
// 服务模式下日志落 Windows 事件日志（chonkpilot-lib/winlog）。
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"

	"github.com/chonkpilot/chonkpilot-lib/winlog"
	"github.com/chonkpilot/chonkpilot-lib/winsvc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// runStdio 以 stdio transport 运行（阻塞直到客户端关闭 stdin）：
// MCP 客户端（Trae 等）spawn 本 exe 时经 stdin/stdout 走完整 JSON-RPC 生命周期。
// 并发上限仍由 lib 的 makeToolHandler sem 控制（SDK 内部仅负责读消息）。
func runStdio(ms *mcp.Server) error {
	log.Printf("[mcp-server] stdio mode (root=capability)")
	return ms.Run(context.Background(), &mcp.StdioTransport{})
}

// httpLifecycle 封装 Streamable HTTP 服务的启停（Start 阻塞直到服务退出）。
type httpLifecycle struct {
	srv *http.Server
}

// Start 启动监听（阻塞直到 Shutdown/错误；正常关闭返回 nil）。
func (l *httpLifecycle) Start(_ context.Context) error {
	err := l.srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Stop 优雅关闭 HTTP 服务。
func (l *httpLifecycle) Stop(ctx context.Context) error {
	return l.srv.Shutdown(ctx)
}

// mcpService 描述 chonkpilot-mcp-server 的 Windows 服务。
// runArgs 为服务进程参数（SCM 拉起时拼在 exe 后；缺省 ["--service","run"]）。
func mcpService(l *httpLifecycle, runArgs ...string) *winsvc.Service {
	return &winsvc.Service{
		Name:        "chonkpilot-mcp-server",
		DisplayName: "ChonkPilot MCP Server",
		Description: "ChonkPilot MCP server (Streamable HTTP, contract scan root)",
		RunArgs:     runArgs,
		Start:       l.Start,
		Stop:        l.Stop,
	}
}

// runAsService 以 Windows 服务身份运行；日志切换为 Windows 事件日志。
func runAsService(l *httpLifecycle) error {
	w := winlog.NewWriter("chonkpilot-mcp-server", true, os.Stderr)
	defer w.Close()
	log.SetOutput(w)
	return mcpService(l).Run()
}
