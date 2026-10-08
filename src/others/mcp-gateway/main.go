// Command chonkpilot-gateway：gateway exe —— 把 chonkpilot-mcp-gateway（lib，上游 =
// 进程内 mq）经标准 MCP 协议独立对外提供能力（门面 = 官方 modelcontextprotocol/go-sdk）。
//
// 用法：
//
//	chonkpilot-gateway.exe [-transport stdio|http://addr|sse://addr]  (默认 stdio)
//	                       [-servers-file=] [-capability=<dir>] [-exec-dir=<dir>]
//	                       [-call-timeout=60] [-max-tasks=8]
//
// 装配：自持进程内 Bus → [可选] 内嵌 chonkpilot-mcp-server（capability 根，全原语 +
// executor 契约）→ mcpgateway lib（Servers 参数传入）→
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
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/chonkpilot/chonkpilot-lib/exedir"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-mcp-gateway/facadeapi"
	mcpgateway "github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
	ckmcpserver "github.com/chonkpilot/chonkpilot-mcp-server/server"
)

func usage() {
	fmt.Fprintf(os.Stderr, `chonkpilot-gateway — 把 gateway lib 以标准 MCP 协议独立对外提供能力

用法:
  -transport stdio|http://<addr>|sse://<addr>   对外传输（默认 stdio；http 端 /mcp）
  -service install|remove|run                   Windows 服务（与 chonkpilot-mcp-server 同参数；
                                                服务运行 = HTTP transport，install 可附 -transport=http://<addr>）
  -servers-file <file>                          接入列表 servers.list（mcp.alias= 分组格式）
  -capability <dir>                             内嵌能力源契约根（缺省自动探测 exe 目录/capability）
  -call-timeout <sec>                           默认调用超时（默认 60）
  -max-tasks <n>                                并发任务上限（默认 8）
`)
}

func main() {
	transport := flag.String("transport", "stdio", "stdio | http://addr | sse://addr")
	serviceCmd := flag.String("service", "", "windows service: install | remove | run (service runs HTTP)")
	serversFile := flag.String("servers-file", "", "servers.list 接入列表路径")
	capRoot := flag.String("capability", "", "capability 契约根（空 = exe 目录/capability 存在时自动启用）")
	configFile := flag.String("config", "", "config json 文件（默认自动探测 exe 同目录 config.json，同 mcp-server）")
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

	// ── 接入源：capability（自建官方 go-sdk server，作 Params.MCPServer 传入）
	//   + servers.list 外部下游（proxied/spawned）──
	var servers []mcpgateway.ServerEntry
	var capServer *mcp.Server // 能力源官方 server（capability 契约 RegisterContracts；nil = 未提供）
	var capContractRoot string // 能力源契约根（ServerTools 单源读取用）
	var capCfg *ckmcpserver.Config

	if root := resolveCapabilityRoot(*capRoot); root != "" {
		ms := mcp.NewServer(&mcp.Implementation{Name: "chonkpilot-gateway", Version: "1.0.0"}, nil)
		cfg := ckmcpserver.DefaultConfig()
		cfg.Root = root
		// config.json 加载（对齐 mcp-server main：显式 --config 优先，否则探测 exe 同目录；
		// 可覆盖 timeout/max_concurrency/defaults 等；契约根已由探测先行赋值）
		applyConfigFile(cfg, *configFile)
		if err := ckmcpserver.RegisterContracts(ms, root, cfg); err != nil {
			log.Fatalf("gateway: capability register: %v", err)
		}
		capServer = ms
		capContractRoot = root
		capCfg = cfg
		log.Printf("[gateway] capability scanned: root=%s", root)
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
	if capServer == nil && len(servers) == 0 {
		// 无接入源（未给 -capability 且未给/空 -servers-file）= 空列表，**不是错误**：
		// 进程照常启动，工具面为空（客户端 list 得到空集），待 -capability 或 servers 提供后自然有内容。
		log.Printf("[gateway] 无 servers 配置源：接入列表为空（工具面仅含 self/meta），不中断启动")
	}

	// ── gateway lib（聚合 + 生命周期，上游 = 本进程 Bus；能力源 = capServer 参数）──
	// category=server 契约工具（如 dsl_run）定义单源：不注册为 executor 工具（见 ServerTools），
	// 由本 exe 桥接注入 gateway（gateway lib 不依赖 mcp-server 包，RB-2）。
	var serverTools map[string]*mcp.Tool
	if capContractRoot != "" {
		st, serr := ckmcpserver.ServerTools(capContractRoot, capCfg)
		if serr != nil {
			log.Printf("[gateway] server 类别工具契约加载失败（回落内置定义）: %v", serr)
		} else {
			serverTools = st
		}
	}
	gw, err := mcpgateway.New(mcpgateway.Params{
		Bus:       bus,
		MCPServer: capServer,
		Servers:   servers,
		// RB-2：dir 节点扫描依赖倒置 —— 扫契约根/建官方 server 由本 exe（dirScanner）完成，
		// gateway lib 不读「源」、不依赖 mcp-server 包。
		ContractScanner: dirScanner{},
		ServerTools:     serverTools,
		CallTimeout:     time.Duration(*callTimeout) * time.Second,
		MaxTasks:        *maxTasks,
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

// dirScanner 实现 mcpgateway.ContractScanner（RB-2 依赖倒置）：独立 exe 侧扫 dir 契约根 →
// 官方 go-sdk server（供 gateway 作 dir 节点接入）。执行配置 = mcp-server 内置默认
// （与改前 gateway 侧 `Params.MCPConfig == nil → DefaultConfig()` 的回落逐字等价）。
type dirScanner struct{}

func (dirScanner) Scan(root string) (*mcp.Server, error) {
	ms := mcp.NewServer(&mcp.Implementation{Name: "chonkpilot-gateway-dir", Version: "1.0.0"}, nil)
	if err := ckmcpserver.RegisterContracts(ms, root, ckmcpserver.DefaultConfig()); err != nil {
		return nil, err
	}
	return ms, nil
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

// applyConfigFile 加载 config json（对齐 mcp-server main.go）：
// 显式 --config 优先；未指定时自动探测 exe 同目录 config.json（存在即加载，缺省用内置默认）。
func applyConfigFile(cfg *ckmcpserver.Config, flagVal string) {
	p := flagVal
	if p == "" {
		if dir, err := exedir.Dir(); err == nil {
			probe := filepath.Join(dir, "config.json")
			if _, err := os.Stat(probe); err == nil {
				p = probe
			}
		}
	}
	if p == "" {
		return
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		log.Fatalf("gateway: read config %s: %v", p, err)
	}
	if err := cfg.Apply(raw); err != nil {
		log.Fatalf("gateway: parse config %s: %v", p, err)
	}
	log.Printf("[gateway] config loaded: %s", p)
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
