// Package assembly 是 ChonkPilot 的**入口装配器**（RB-5 L5，2026-09-22）：把「能力源官方
// go-sdk server（capability 契约）+ 执行配置 + 内嵌 gateway」的**静态装配**收敛到一处，
// 供 **gui / server 两个入口共用一份**（二者都经 `chonkpilot-llm/server.New` → 本包），
// 避免装配漂移（改前这段装配内联在 llm/server.New 中，仅 llm 一处可改）。
//
// 顺序约束（与改前 `llm/server.New` 内联装配逐行同序，零行为变更）：
//  1. 建官方 go-sdk server instance（`mcp.NewServer`，无传输、不 spawn）
//  2. `RegisterContracts` 扫 capability 契约根把原语注册进**这个 instance**
//  3. 记录 app 根已注册工具名（T-21②：运行期重扫时回收删除/改名者）
//  4. 以该 instance 作 `Params.MCPServer`（self 节点）构建内嵌 gateway；`ContractScanner`
//     注入 = 本包 `(*Stack).Scan`（RB-2 依赖倒置：gateway 不读「源」）
//
// **不越权**：域工具 / 域 agent 的注册（`tools/register` / `prompts/register`）仍由
// `llm/server.(*Server).Start` 自注册 —— 必须在 `gateway.Start` 之后（顺序约束），本包只做
// 静态装配、不订阅任何主题、不启动任何 goroutine。
package assembly

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/chonkpilot/chonkpilot-lib/exedir"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	mcpgateway "github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
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
	// ManageAddr 管理 REST（空 = 禁用）。
	ManageAddr string
	// ExecSink 任务层执行池接入（nil = 层未注入 → gateway 侧全部 no-op）。
	ExecSink mcpgateway.ExecSink
	// Logf 日志（nil → fmt.Printf）。
	Logf func(format string, args ...any)
}

// Stack 是装配产物：能力源 + 执行配置 + 内嵌 gateway。
type Stack struct {
	Server   *mcp.Server     // 能力源（self 节点；域工具/域 agent 后续 AddTool 注入其上）
	Config   *mcpms.Config   // 执行配置（dir 节点与 self **共享**同一份；I-82 统一施加点）
	AppTools map[string]bool // app 根已注册工具名（T-21② 重扫回收用）
	Gateway  *mcpgateway.Gateway
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
	// OP-15：capability 契约扫描/注册耗时（含 AppToolNames 全树 WalkDir；仅插桩）
	tScan := time.Now()
	if err := mcpms.RegisterContracts(ms, root, cfg); err != nil {
		logf("[chonkpilot-server] capability 契约注册失败（空能力面继续）: %v\n", err)
	}
	st := &Stack{Server: ms, Config: cfg, AppTools: AppToolNames(root)}
	logf("[startup] capability 契约扫描/注册 耗时 %dms\n", time.Since(tScan).Milliseconds())

	// category=server 契约工具（如 dsl_run）**定义单源**：不注册为 executor 工具（见 ServerTools），
	// 由装配层经 mcpms.ServerTools 桥接注入 gateway（gateway lib 不依赖 mcp-server 包，RB-2）。
	serverTools, stErr := mcpms.ServerTools(root, cfg)
	if stErr != nil {
		logf("[chonkpilot-server] server 类别工具契约加载失败（回落内置定义）: %v\n", stErr)
	}

	// OP-15：gateway 构建耗时（仅插桩）
	tGW := time.Now()
	gw, err := mcpgateway.New(mcpgateway.Params{
		Bus:             opts.Bus,
		MCPServer:       ms,
		MCPConfig:       cfg, // dir 节点与 self 共用执行配置（工具级覆盖/沙箱/工具链统一施加点，I-82）
		ContractScanner: st,  // RB-2：dir 节点扫描依赖倒置 —— 扫目录/建官方 server 由装配方完成
		Servers:         opts.Servers,
		ServerTools:     serverTools, // category=server 契约工具定义单源（dsl_run 等）
		AsyncMode:       opts.AsyncMode,
		ManageAddr:      opts.ManageAddr,
		CallTimeout:     60 * time.Second,
		MaxTasks:        8,
		ExecSink:        opts.ExecSink,
		Logf:            opts.Logf, // C-53：透传日志出口，否则 gateway 回落 log.Printf，desktop(windowsgui) 下诊断日志丢失
	})
	if err != nil {
		return st, err
	}
	logf("[startup] gateway 构建 耗时 %dms\n", time.Since(tGW).Milliseconds())
	st.Gateway = gw
	return st, nil
}

// Scan 实现 `mcpgateway.ContractScanner`（RB-2 依赖倒置注入点）：扫契约根（tools/prompts/
// skills/resources 四原语）→ 已注册的官方 go-sdk server，供 gateway 作 dir 节点
// （`servers/register{dir}`）接入。与 self 能力源**共用同一执行配置** `Stack.Config`
// （工具级覆盖/沙箱/工具链统一施加点，I-82）；行为与改前 `llm/server.(*Server).Scan` 逐字等价。
func (s *Stack) Scan(root string) (*mcp.Server, error) {
	cfg := s.Config
	if cfg == nil {
		cfg = mcpms.DefaultConfig()
	}
	ms := mcp.NewServer(&mcp.Implementation{Name: "chonkpilot-gateway-dir", Version: "1.0.0"}, nil)
	if err := mcpms.RegisterContracts(ms, root, cfg); err != nil {
		return nil, err
	}
	return ms, nil
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
