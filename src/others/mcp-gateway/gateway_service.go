// Windows service 接入（winsvc/winlog，与 chonkpilot-mcp-server --service 参数一致）：
// 服务运行 = facade Streamable HTTP（/mcp 与 /），默认 127.0.0.1:5556；
// 服务模式下日志落 Windows 事件日志。
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/chonkpilot/chonkpilot-lib/winlog"
	"github.com/chonkpilot/chonkpilot-lib/winsvc"
	"github.com/chonkpilot/chonkpilot-mcp-gateway/facadeapi"
)

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

// gatewayService 描述 chonkpilot-mcp-gateway 的 Windows 服务。
// runArgs 为服务进程参数（SCM 拉起时拼在 exe 后；缺省 ["--service","run"]）。
// l 可为 nil（仅 install/remove 时不需要生命周期回调）。
func gatewayService(l *httpLifecycle, runArgs ...string) *winsvc.Service {
	svc := &winsvc.Service{
		Name:        "chonkpilot-mcp-gateway",
		DisplayName: "ChonkPilot MCP Gateway",
		Description: "ChonkPilot MCP gateway (Streamable HTTP facade, aggregate + mcp)",
		RunArgs:     runArgs,
	}
	if l != nil {
		svc.Start = l.Start
		svc.Stop = l.Stop
	}
	return svc
}

// runGatewayService 以 Windows 服务身份运行 facade HTTP（SCM 拉起 --service run）。
func runGatewayService(addr string, ad *facadeapi.Adapter) {
	if !winsvc.IsWindowsService() {
		log.Fatalf("--service run 需由 SCM 拉起（服务模式下运行）")
	}
	w := winlog.NewWriter("chonkpilot-mcp-gateway", true, os.Stderr)
	defer w.Close()
	log.SetOutput(w)

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ad.Server() }, nil)
	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	mux.Handle("/", handler)
	life := &httpLifecycle{srv: &http.Server{Addr: addr, Handler: mux}}
	log.Printf("[gateway] service listening on %s", addr)
	if err := gatewayService(life).Run(); err != nil {
		log.Fatalf("gateway: service run: %v", err)
	}
}
