// chonkpilot-mcp-server 独立 exe（无 build tag；lib 形态由 chonkpilot-server import server/ 子包内嵌）。
// 递归扫描契约根（四原语平铺）构建单个 MCP 实例。**必须显式指定运行形态**（无参数退出）：
//   - --http[=<addr>]：前台 Streamable HTTP（官方 SDK 原生 handler，端点 /mcp）；addr 缺省 127.0.0.1:5700
//   - --stdio：MCP 客户端（Trae 等）spawn 本 exe，经 stdin/stdout 全双工 JSON-RPC（常驻）
//   - --service install|remove|run：注册/运行 Windows 服务（服务内部 = HTTP 形态）
//
// 核心逻辑在 server/ 包；本文件仅为参数解析 + 传输层生命周期薄壳。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/exedir"
	"github.com/chonkpilot/chonkpilot-lib/winsvc"
	mcpms "github.com/chonkpilot/chonkpilot-mcp-server/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const defaultAddr = "127.0.0.1:5700"

// optionalString 是 flag.Value：`--http`（无值，Set("true")）与 `--http=addr`（有值）都合法。
type optionalString struct {
	set bool
	val string
}

func (o *optionalString) String() string { return o.val }
func (o *optionalString) Set(s string) error {
	o.set = true
	if s == "true" { // --http 无值：按 bool 语义触发，addr 用默认
		o.val = ""
	} else {
		o.val = s
	}
	return nil
}

// IsBoolFlag 使 --http 可以不带值单独出现（flag 包按 bool flag 处理）。
func (o *optionalString) IsBoolFlag() bool { return true }

func usage() {
	fmt.Fprintf(os.Stderr, `chonkpilot-mcp-server — ChonkPilot MCP server (contract scan root + executors)

必须指定一种运行形态：
  -http[=<addr>]          前台 Streamable HTTP（默认 %s，端点 /mcp）
  -stdio                  stdio transport（MCP 客户端 spawn 本 exe）
  -service install|remove|run   Windows 服务（服务运行 = HTTP；install 可附 -http=<addr> 指定监听地址）

其他参数：
  -root <dir>             契约扫描根（默认 exe 目录/capability）
  -timeout <sec>          tools/call 执行超时（默认 300）
  -config <file>          config json（默认自动探测 exe 同目录 config.json）
`, defaultAddr)
}

func main() {
	cfg := mcpms.DefaultConfig()

	httpMode := &optionalString{}
	flag.Var(httpMode, "http", "run Streamable HTTP at [addr] (default "+defaultAddr+")")
	stdioMode := flag.Bool("stdio", false, "run as stdio transport (spawned by MCP client)")
	serviceCmd := flag.String("service", "", "windows service: install | remove | run (service runs HTTP)")
	flag.StringVar(&cfg.Root, "root", "", "contract scan root (default: exe dir/capability)")
	flag.IntVar(&cfg.TimeoutSec, "timeout", cfg.TimeoutSec, "tool call timeout seconds (default 300)")
	configPath := flag.String("config", "", "config json file (default: exe dir/config.json if present)")
	flag.Parse()

	// 形态仲裁：stdio / service / http 前台三选一；
	// --http 仅在与 --service 组合时作为「服务监听地址」附加（不构成冲突）。
	hasMode := httpMode.set || *stdioMode || *serviceCmd != ""
	if !hasMode {
		usage()
		os.Exit(2)
	}
	if *stdioMode && *serviceCmd != "" {
		log.Fatalf("运行形态互斥：-stdio 与 -service 不能同时指定")
	}
	if *stdioMode && httpMode.set {
		log.Fatalf("运行形态互斥：-stdio 不接收 -http（stdio 无监听地址）")
	}
	addr := httpMode.val
	if addr == "" {
		addr = defaultAddr
	}

	// 默认：契约在 exe 目录下的 capability/ 子目录（dist 布局；executor 随契约 tools/<cat>/ 同目录部署）
	exeDir, err := exedir.Dir()
	if err != nil {
		log.Fatalf("resolve exe dir: %v", err)
	}
	if cfg.Root == "" {
		cfg.Root = filepath.Join(exeDir, "capability")
	}
	// 配置加载：显式 --config 优先；未指定时自动探测 exe 同目录 config.json（存在即加载，缺省用内置默认）
	cfgFile := *configPath
	if cfgFile == "" {
		probe := filepath.Join(exeDir, "config.json")
		if _, err := os.Stat(probe); err == nil {
			cfgFile = probe
		}
	}
	if cfgFile != "" {
		raw, err := os.ReadFile(cfgFile)
		if err != nil {
			log.Fatalf("read config %s: %v", cfgFile, err)
		}
		if err := cfg.Apply(raw); err != nil {
			log.Fatalf("parse config %s: %v", cfgFile, err)
		}
		log.Printf("[mcp-server] config loaded: %s", cfgFile)
	}

	// 自建官方 go-sdk server + 契约扫描注册（root 缺失 → 空能力面启动）
	ms := mcp.NewServer(&mcp.Implementation{Name: "chonkpilot-mcp-server", Version: "1.0.0"}, nil)
	if err := mcpms.RegisterContracts(ms, cfg.Root, cfg); err != nil {
		log.Fatalf("register contracts: %v", err)
	}

	// stdio 模式：MCP 客户端（Trae 等）spawn 本 exe，经 stdin/stdout 全双工 JSON-RPC（常驻，直到客户端断开）。
	// 日志走 stderr（Go log 默认），不污染 stdout 协议通道。
	if *stdioMode {
		if err := runStdio(ms); err != nil {
			log.Fatalf("stdio server: %v", err)
		}
		return
	}

	// HTTP 形态（前台 console / Windows 服务内部共用）：Streamable HTTP，端点 /mcp
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, nil)
	httpSrv := &http.Server{Addr: addr, Handler: handler}
	life := &httpLifecycle{srv: httpSrv}

	if *serviceCmd != "" {
		switch *serviceCmd {
		case "install":
			// 服务进程参数：--service run [--http=<addr>]（带 --http 则服务监听自定义地址，SCM 拉起时生效）
			runArgs := []string{"--service", "run"}
			if httpMode.set {
				runArgs = append(runArgs, "--http="+addr)
			}
			svc := mcpService(life, runArgs...)
			if err := svc.Install(); err != nil {
				log.Fatalf("install service: %v", err)
			}
			fmt.Printf("service %s installed (binPath: %s %s)\n", svc.Name, exeDir, strings.Join(runArgs, " "))
		case "remove":
			if err := mcpService(life).Remove(); err != nil {
				log.Fatalf("remove service: %v", err)
			}
			fmt.Printf("service %s removed\n", "chonkpilot-mcp-server")
		case "run":
			if winsvc.IsWindowsService() {
				if err := runAsService(life); err != nil {
					log.Fatalf("service run: %v", err)
				}
			} else {
				log.Fatalf("--service run 需由 SCM 拉起（服务模式下运行）")
			}
		default:
			log.Fatalf("unknown --service=%q (want install|remove|run)", *serviceCmd)
		}
		return
	}

	// --http 前台模式
	if err := runConsole(life, addr); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// runConsole 前台运行：阻塞监听，收到 Ctrl+C / SIGTERM 优雅关闭。
func runConsole(l *httpLifecycle, addr string) error {
	log.Printf("[mcp-server] listening on %s", addr)
	errCh := make(chan error, 1)
	go func() { errCh <- l.Start(context.Background()) }()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
		log.Printf("[mcp-server] signal received, graceful shutdown")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return l.Stop(ctx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
