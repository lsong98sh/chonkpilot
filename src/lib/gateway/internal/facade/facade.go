// Package facade 把 chonkpilot-mcp-gateway（lib，上游 = 进程内 mq）以标准 MCP server
// 形态对外包装（门面用官方 modelcontextprotocol/go-sdk）。
//
// 方向（对齐 draft：gateway exe = lib/gateway 经 mcp 协议独立对外提供能力）：
//   - 入向：MCP 请求（tools/list、tools/call 及扩展 chonk.* 方法）→ Bus 方法面
//     （相对主题 tool-*/task-*/server-*，publish + 同主题 promise 取回）
//   - 出向：Bus 事件 mcp-changed → 重拉 tools/list 与已暴露集 diff →
//     AddTool / RemoveTools（官方 SDK 自动向已订阅客户端发 tools/list_changed）
//   - 列表差异同步不依赖 filesys：只认 gateway 方法面返回的聚合工具集
//
// tools-only 面（prompts/resources/skills 由内嵌能力源 mcp-server 提供，
// 经 gateway 同进程节点登记——但聚合仍是 tools；详见 main.go 装配说明）。
package facade

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// Adapter 是 mq ↔ mcp 双向适配器。
type Adapter struct {
	bus  mq.Bus
	srv  *mcp.Server
	logf func(string, ...any)

	mu        sync.Mutex
	tools     map[string]string // name → 上次 inputSchema（JSON 文本）；变化才重注册
	prompts   map[string]promptInfo
	resources map[string]resourceInfo
	stop      chan struct{}
	stopOnce  sync.Once
	wg        sync.WaitGroup
}

// New 构建适配器（不含网关：gateway lib 与适配器共享同一条 Bus，由调用方装配后 Start）。
func New(bus mq.Bus) *Adapter {
	return &Adapter{
		bus:       bus,
		srv:       mcp.NewServer(&mcp.Implementation{Name: "chonkpilot-gateway", Version: "0.1.0"}, nil),
		logf:      log.Printf,
		tools:     map[string]string{},
		prompts:   map[string]promptInfo{},
		resources: map[string]resourceInfo{},
		stop:      make(chan struct{}),
	}
}

// Server 返回对外 MCP server（挂传输层：stdio/http/sse）。
func (a *Adapter) Server() *mcp.Server { return a.srv }

// Start 注册工具/提示/资源回调 + 扩展方法，执行首次同步，并订阅 mcp-changed 做差异同步。
func (a *Adapter) Start(ctx context.Context) error {
	a.registerExtMethods()
	if err := a.reconcileTools(ctx); err != nil {
		return fmt.Errorf("initial tools sync: %w", err)
	}
	if err := a.reconcilePrompts(ctx); err != nil {
		return fmt.Errorf("initial prompts sync: %w", err)
	}
	if err := a.reconcileResources(ctx); err != nil {
		return fmt.Errorf("initial resources sync: %w", err)
	}
	// 订阅 gateway 目录/接入变化通知（相对主题 mcp-gateway-changed，2026-09-06 与 lib 同步）。
	// 句柄由 goroutine 捕获，Stop 触发后**退订**（避免 Stop 后回调仍注册）。
	sub, err := a.bus.On(msgkeys.TopicMcpGatewayChanged, 0, func(_ context.Context, _ string, _ *mq.Value) error {
		_ = a.reconcileTools(context.Background())
		_ = a.reconcilePrompts(context.Background())
		_ = a.reconcileResources(context.Background())
		return nil
	})
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", msgkeys.TopicMcpGatewayChanged, err)
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		<-a.stop
		_ = sub.Unsubscribe()
	}()
	return nil
}

// Stop 停止差异同步订阅。**幂等**（重复调用无副作用）：close 由 sync.Once 包裹；
// wg.Wait 保证退订已完成，Stop 返回后不再有回调注册。
func (a *Adapter) Stop() {
	a.stopOnce.Do(func() { close(a.stop) })
	a.wg.Wait()
}
