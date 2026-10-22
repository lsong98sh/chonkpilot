// Command chonkpilot-gateway：gateway exe —— 把 chonkpilot-mcp-gateway（lib，上游 =
// 进程内 mq）经标准 MCP 协议独立对外提供能力（门面 = 官方 modelcontextprotocol/go-sdk）。
//
// 用法：
//
//	chonkpilot-gateway.exe [-transport stdio|http://addr|sse://addr]  (默认 stdio)
//	                       [-servers-file=] [-capability=<dir>] [-exec-dir=<dir>]
//	                       [-call-timeout=60] [-max-tasks=8]
//
// 装配：自持进程内 Bus → [可选] capability 根（以 stdio 下游接入：spawn
// chonkpilot-core-mcp-server --stdio --root <dir>）→ mcpgateway lib（Servers 参数传入）→
// facade（官方 SDK 门面：标准方法直供 + 扩展方法裸名 tools/register 等 + 差异同步）。
// 就绪 marker 打到 stderr（stdio 形态 stdout 是协议通道，不可污染）。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/chonkpilot/chonkpilot-lib/exedir"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-mcp-gateway/facadeapi"
	mcpgateway "github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
	"github.com/chonkpilot/chonkpilot-mcp-gateway/mqadapter"
)

func usage() {
	fmt.Fprintf(os.Stderr, `chonkpilot-gateway — 把 gateway lib 以标准 MCP 协议独立对外提供能力

用法:
  -transport stdio|http://<addr>|sse://<addr>   对外传输（默认 stdio；http 端 /mcp）
  -service install|remove|run                   Windows 服务（与 chonkpilot-mcp-server 同参数；
                                                服务运行 = HTTP transport，install 可附 -transport=http://<addr>）
  -servers-file <file>                          接入列表 servers.list（mcp.alias= 分组格式）
  -capability <dir>                             capability 契约根（缺省自动探测 exe 目录/capability；
                                                以 chonkpilot-core-mcp-server --stdio 下游接入）
  -call-timeout <sec>                           默认调用超时（默认 60）
  -max-tasks <n>                                并发任务上限（默认 8）
`)
}

func main() {
	transport := flag.String("transport", "stdio", "stdio | http://addr | sse://addr")
	serviceCmd := flag.String("service", "", "windows service: install | remove | run (service runs HTTP)")
	serversFile := flag.String("servers-file", "", "servers.list 接入列表路径")
	capRoot := flag.String("capability", "", "capability 契约根（空 = exe 目录/capability 存在时自动启用）")
	configFile := flag.String("config", "", "config json 文件（透传给 capability 下游 core-mcp-server）")
	callTimeout := flag.Int("call-timeout", 60, "默认调用超时（秒）")
	maxTasks := flag.Int("max-tasks", 8, "并发任务上限")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		log.Fatalf("gateway: new bus: %v", err)
	}
	defer bus.Close()

	// ── 接入源：capability（GATEWAY-2 L5：以普通 stdio 下游接入，core-mcp-server 用同一
	//   RegisterContracts 暴露契约根）+ servers.list 外部下游（proxied/spawned）──
	var servers []mcpgateway.ServerEntry

	if root := resolveCapabilityRoot(*capRoot); root != "" {
		coreExe, err := resolveCoreMCP()
		if err != nil {
			log.Fatalf("gateway: capability root %s: %v", root, err)
		}
		args := []string{"--stdio", "--root", root}
		if *configFile != "" {
			args = append(args, "--config", *configFile)
		}
		servers = append(servers, mcpgateway.ServerEntry{
			ID:        "dir:" + root,
			Name:      "dir:" + root,
			Runtime:   coreExe,
			Args:      args,
			Transport: "stdio",
			Enabled:   true,
			Aliases:   map[string]string{},
			Category:  "dir",
			Origin:    "builtin",
		})
		log.Printf("[gateway] capability root attached as stdio server: root=%s exe=%s", root, coreExe)
	}
	if *serversFile != "" {
		raw, err := os.ReadFile(*serversFile)
		if err != nil {
			log.Fatalf("gateway: read servers.list: %v", err)
		}
		entries, err := mcpgateway.ParseServersList(raw)
		if err != nil {
			log.Fatalf("gateway: parse servers.list: %v", err)
		}
		servers = append(servers, entries...)
	}
	if len(servers) == 0 {
		// 无接入源（未给 -capability 且未给/空 -servers-file）= 空列表，**不是错误**：
		// 进程照常启动，工具面为空（客户端 list 得到空集），待 -capability 或 servers 提供后自然有内容。
		log.Printf("[gateway] 无 servers 配置源：接入列表为空，不中断启动")
	}

	// ── gateway lib（聚合 + 生命周期，上游 = 本进程 Bus；下游 = servers）──
	gw, err := mcpgateway.New(mcpgateway.Params{
		Transport:   mqadapter.New(bus), // MOD-1 M0.5：gateway 内核只认端口，mq 适配在宿主
		Servers:     servers,
		CallTimeout: time.Duration(*callTimeout) * time.Second,
		MaxTasks:    *maxTasks,
	})
	if err != nil {
		log.Fatalf("gateway: new: %v", err)
	}
	if err := gw.Start(ctx); err != nil {
		log.Fatalf("gateway: start: %v", err)
	}
	defer gw.Stop(context.Background())

	// ── 官方 SDK 门面（mq ↔ mcp）──
	ad := facadeapi.New(bus)
	if err := ad.Start(ctx); err != nil {
		log.Fatalf("gateway: facade start: %v", err)
	}
	defer ad.Stop()

	tr := *transport

	// ── Windows 服务形态（--service install|remove|run；服务运行 = HTTP transport，
	//  与 chonkpilot-mcp-server 参数一致）──
	if *serviceCmd != "" {
		switch *serviceCmd {
		case "install":
			args := []string{"--service", "run"}
			if strings.HasPrefix(tr, "http") {
				args = append(args, "--transport="+tr)
			} else {
				args = append(args, "--transport=http://127.0.0.1:5556")
			}
			if err := gatewayService(nil, args...).Install(); err != nil {
				log.Fatalf("gateway: install service: %v", err)
			}
			fmt.Printf("service chonkpilot-mcp-gateway installed (run args: %s)\n", strings.Join(args, " "))
		case "remove":
			if err := gatewayService(nil).Remove(); err != nil {
				log.Fatalf("gateway: remove service: %v", err)
			}
			fmt.Println("service chonkpilot-mcp-gateway removed")
		case "run":
			addr := strings.TrimPrefix(strings.TrimPrefix(tr, "https://"), "http://")
			if !strings.HasPrefix(tr, "http") {
				addr = "127.0.0.1:5556"
			}
			runGatewayService(addr, ad)
		default:
			log.Fatalf("unknown --service=%q (want install|remove|run)", *serviceCmd)
		}
		return
	}

	switch {
	case tr == "stdio":
		fmt.Fprintln(os.Stderr, "GATEWAY_READY stdio")
		if err := ad.Server().Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
			log.Printf("gateway: stdio session ended: %v", err)
		}
		return // stdio 形态随客户端断开退出（对齐 chonkpilot-mcp-server --stdio）

	case strings.HasPrefix(tr, "http://") || strings.HasPrefix(tr, "https://"):
		addr := strings.TrimPrefix(strings.TrimPrefix(tr, "https://"), "http://")
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ad.Server() }, nil)
		mux := http.NewServeMux()
		mux.Handle("/mcp", handler)
		mux.Handle("/", handler)
		runHTTP(ctx, addr, mux)

	case strings.HasPrefix(tr, "sse://"):
		addr := strings.TrimPrefix(tr, "sse://")
		handler := mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return ad.Server() }, nil)
		mux := http.NewServeMux()
		mux.Handle("/sse", handler)
		mux.Handle("/", handler)
		runHTTP(ctx, addr, mux)

	default:
		usage()
		log.Fatalf("gateway: 未知 -transport %q", tr)
	}
}

// resolveCoreMCP 定位 chonkpilot-core-mcp-server 可执行文件（GATEWAY-2 L5：capability 目录根
// 以普通 stdio 下游接入，不再由 gateway 进程内自建能力源）。查找口径与 coretools 监管器一致：
// `<exe目录>/<name>` 与 `<exe目录>/mcps/core-mcp/<name>`。
func resolveCoreMCP() (string, error) {
	dir, err := exedir.Dir()
	if err != nil {
		return "", fmt.Errorf("exe 目录: %w", err)
	}
	name := "chonkpilot-core-mcp-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	for _, p := range []string{
		filepath.Join(dir, name),
		filepath.Join(dir, "mcps", "core-mcp", name),
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("未找到 %s（查找 %s 与 %s）", name, dir, filepath.Join(dir, "mcps", "core-mcp"))
}

// resolveCapabilityRoot 返回能力源契约根：显式参数优先；空则探测 exe 目录/capability。
func resolveCapabilityRoot(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if dir, err := exedir.Dir(); err == nil {
		probe := filepath.Join(dir, "capability")
		if fi, err := os.Stat(probe); err == nil && fi.IsDir() {
			return probe
		}
	}
	return ""
}

// runHTTP 前台 HTTP/SSE：监听 + 就绪 marker，信号/ctx 优雅关闭。
func runHTTP(ctx context.Context, addr string, h http.Handler) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("gateway: listen %s: %v", addr, err)
	}
	fmt.Fprintf(os.Stderr, "GATEWAY_READY http://%s\n", ln.Addr())
	srv := &http.Server{Handler: h}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sig:
		log.Printf("[gateway] signal, graceful shutdown")
		ctx2, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = srv.Shutdown(ctx2)
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			log.Printf("[gateway] http error: %v", err)
		}
	}
}
