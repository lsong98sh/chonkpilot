// Package assembly 是 ChonkPilot 的**入口装配器**（RB-5 L5，2026-09-22）：把「能力源官方
// go-sdk server（capability 契约）+ 执行配置 + 内嵌 gateway」的**静态装配**收敛到一处，
// 供 **gui / server 两个入口共用一份**（二者都经 `chonkpilot-llm/server.New` → 本包），
// 避免装配漂移（改前这段装配内联在 llm/server.New 中，仅 llm 一处可改）。
//
// 顺序约束（与改前 `llm/server.New` 内联装配逐行同序，零行为变更）：
//  1. 建官方 go-sdk server instance（`mcp.NewServer`，无传输、不 spawn）
//  2. `RegisterContracts` 扫 capability 契约根把原语注册进**这个 instance**
//  3. 记录 app 根已注册工具名（T-21②：运行期重扫时回收删除/改名者）
//  4. 以 `opts.Servers`（外部 server 列表）构建内嵌 gateway（GATEWAY-2 L5：gateway 只接
//     Transport + Servers，不再持有能力源/执行配置）
//
// **不越权**：域工具 / 域 agent 的注册（`tools/register` / `prompts/register`）仍由
// `llm/server.(*Server).Start` 自注册 —— 必须在 `gateway.Start` 之后（顺序约束），本包只做
// 静态装配、不订阅任何主题、不启动任何 goroutine。
package assembly

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/chonkpilot/chonkpilot-lib/exedir"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	mcpgateway "github.com/lsong98sh/chonkpilot-mcp-gateway/gateway"
	"github.com/chonkpilot/chonkpilot-gwadapter/mqadapter"
	mcpms "github.com/chonkpilot/chonkpilot-mcp-server/server"
)

// Options 是装配参数（缺省口径与改前 `llm/server.New` 内联装配一致）。
type Options struct {
	// Bus 上游总线（必填；内嵌 gateway 与调用方共用同一进程内总线）。
	Bus mq.Bus
	// Root 契约根（空 → `<exe 目录>/capability`）。
	Root string
	// Servers 额外下游（外部 proxied / usr mcps 转成的 ServerEntry，与 self 并存）。
	Servers []mcpgateway.ServerEntry
	// AsyncMode 工具异步模式（"" = 默认 auto；"never" = 强制同步，用于 CLI）。
	AsyncMode string
	// SkipTools（P6 2026-10-19）true = 系统级 app 根**只注册资产**（prompts/skills/resources），
	// 工具改由外部进程（chonkpilot-core-mcp-server，经 `--stdio`/http 端点）注册/执行。
	SkipTools bool
	// ExecSink 任务层执行池接入（nil = 层未注入 → gateway 侧全部 no-op）。
	ExecSink mcpgateway.ExecSink
	// Logf 日志（nil → fmt.Printf）。
	Logf func(format string, args ...any)
}

// Stack 是装配产物：能力源 + 执行配置 + 内嵌 gateway。
type Stack struct {
	Server   *mcp.Server     // 能力源（prompts/skills/resources 资产面；域工具/域 agent 后续 AddTool 注入其上）
	Config   *mcpms.Config   // 执行配置（mcp-server / core-mcp-server 消费；I-82 统一施加点）
	AppTools map[string]bool // app 根已注册工具名（T-21② 重扫回收用）
	Gateway  *mcpgateway.Gateway
	// Close 关闭进程内能力源会话（Stop 时调用；nil = 未建立会话）。
	Close func()
}

// Build 装配「能力源 + 执行配置 + 内嵌 gateway」。
// 返回的 Stack **永不为 nil**：gateway 构建失败 → 返回非空 Stack + error（能力源/执行配置
// 仍可用，与改前「gateway 失败仍保留 mcpServer/mcpCfg」的行为一致）。
func Build(opts Options) (*Stack, error) {
	logf := opts.Logf
	if logf == nil {
		logf = func(format string, args ...any) { fmt.Printf(format, args...) }
	}
	root := opts.Root
	if root == "" {
		if dir, err := exedir.Dir(); err == nil {
			root = filepath.Join(dir, "capability")
		} else {
			root = filepath.Join("capability")
		}
	}
	ms := mcp.NewServer(&mcp.Implementation{Name: "chonkpilot-server", Version: "1.0.0"}, nil)
	// 执行配置单源 = mcpms 内置默认（300s / 16 / 跳过目录清单；P0-D 与前端镜像同源，
	// 由 llm/server 的 turn_test.go TestSystemDefaultParamsMirror 守护），仅注入契约根。
	cfg := mcpms.DefaultConfig()
	cfg.Root = root
	cfg.SkipTools = opts.SkipTools // P6：外部工具模式 → app 根只注册资产
	// OP-15：capability 契约扫描/注册耗时（含 AppToolNames 全树 WalkDir；仅插桩）
	tScan := time.Now()
	if err := mcpms.RegisterContracts(ms, root, cfg); err != nil {
		logf("[chonkpilot-server] capability 契约注册失败（空能力面继续）: %v\n", err)
	}
	st := &Stack{Server: ms, Config: cfg, AppTools: AppToolNames(root)}
	logf("[startup] capability 契约扫描/注册 耗时 %dms\n", time.Since(tScan).Milliseconds())

	// 能力源接入（GATEWAY-2 L5 收口）：把进程内 mcp.Server 经内存传输连成一个**标准 MCP
	// ClientSession**，作为普通下游 ServerEntry（ID=self）交给 gateway —— gateway 只走
	// 标准 client（ListTools/Call/prompts/resources），不持有能力源/契约扫描；
	// 暴露名仍 = self_<原名>（对外零变更），app 根热重扫（reloadAppContracts + gateway/reload）
	// 经同一会话重拉 list 即可生效。
	servers := append([]mcpgateway.ServerEntry{}, opts.Servers...)
	if ss, closeSelf := connectSelf(ms, logf); ss != nil {
		servers = append(servers, mcpgateway.ServerEntry{
			ID: "self", Name: "内置能力源", Category: "core", Enabled: true,
			Origin: mcpgateway.OriginBuiltin, Session: ss,
		})
		st.Close = closeSelf
	}

	// OP-15：gateway 构建耗时（仅插桩）
	tGW := time.Now()
	// MOD-1 M0.5：gateway 内核只认 Transport 端口；mq 适配在宿主。
	// Bus 缺省（nil）→ 不注入 Transport → gateway 构建按必填校验失败（与改前一致）。
	// 出站通知（任务回报 / 接入变化 / 超时裁决）主题名归 mqadapter：这里只装配 Hooks。
	var gwTransport mcpgateway.Transport
	var gwHooks mcpgateway.Hooks
	if opts.Bus != nil {
		adapter := mqadapter.New(opts.Bus)
		gwTransport = adapter
		gwHooks = adapter.Hooks()
	}
	gw, err := mcpgateway.New(mcpgateway.Params{
		Transport:   gwTransport,
		Hooks:       gwHooks,
		Servers:     servers,
		AsyncMode:   opts.AsyncMode,
		CallTimeout: 60 * time.Second,
		MaxTasks:    8,
		ExecSink:    opts.ExecSink,
		Logf:        opts.Logf, // C-53：透传日志出口，否则 gateway 回落 log.Printf，desktop(windowsgui) 下诊断日志丢失
	})
	if err != nil {
		return st, err
	}
	logf("[startup] gateway 构建 耗时 %dms\n", time.Since(tGW).Milliseconds())
	st.Gateway = gw
	return st, nil
}

// connectSelf 把进程内能力源 server 经内存传输连成一个**标准 MCP ClientSession**（GATEWAY-2 L5
// 收口）：gateway 侧据此把它当普通下游（ListTools/Call/prompts/resources 全走标准 client），
// 不感知实现、不持有能力源。返回 (nil, nil) = 连接失败（已记日志；工具面退化为仅外部下游）。
func connectSelf(ms *mcp.Server, logf func(format string, args ...any)) (*mcp.ClientSession, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	srvTr, cliTr := mcp.NewInMemoryTransports()
	ss, err := ms.Connect(ctx, srvTr, nil)
	if err != nil {
		cancel()
		logf("[chonkpilot-server] 能力源会话建立失败（自工具面缺失）: %v\n", err)
		return nil, nil
	}
	cli := mcp.NewClient(&mcp.Implementation{Name: "chonkpilot-gateway", Version: "1.0.0"}, nil)
	cs, err := cli.Connect(ctx, cliTr, nil)
	if err != nil {
		_ = ss.Close()
		cancel()
		logf("[chonkpilot-server] 能力源会话建立失败（自工具面缺失）: %v\n", err)
		return nil, nil
	}
	return cs, func() {
		_ = cs.Close()
		_ = ss.Close()
		cancel()
	}
}

// AppToolNames 递归收集 app capability 根下全部 `*.tool.md` 的原语名（T-21② 运行期重扫时回收
// 删除/改名者用；口径与 `chonkpilot-mcp-server/server/contract.go` 一致：原语名 = 文件名去后缀）。
// 根缺失 / 不可读 → 空集。
func AppToolNames(root string) map[string]bool {
	names := map[string]bool{}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if name := strings.TrimSuffix(d.Name(), ".tool.md"); name != d.Name() {
			names[name] = true
		}
		return nil
	})
	return names
}
