package appwire

import (
	"context"
	"fmt"
	"io"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-filesys"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/chonkpilot/chonkpilot-llm/server"
	"github.com/lsong98sh/chonkpilot-plugin-framework/runtime/instance"
	"github.com/chonkpilot/chonkpilot-workspace"
	mcpgateway "github.com/lsong98sh/chonkpilot-agent/gateway"
)

// BuildOptions 是 Build 的**非快照**入参（含进程内句柄，不能进 BootConfig）。
type BuildOptions struct {
	// GatewayServers 外部下游（servers.list 解析结果；lib 不读源，源解析在面层）。
	GatewayServers []mcpgateway.ServerEntry
	// LogWriter 额外日志 sink（nil = 仅 stdout）。
	LogWriter io.Writer
}

// App 是共用脊柱的装配产物（构造完成、**尚未启动** llm server）。
//
// 生命周期由宿主就地编排（gui 宿主还要按序收尾插件桥 / 索引器，server 入口有自己的
// 托盘 / UDS / HTTP 收尾），本包只保证**构造顺序**与「谁依赖谁」：Bus → Workspace(PM) →
// Facade → Filesys(可选) → Server(可选)。
type App struct {
	Cfg       BootConfig
	Bus       mq.Bus
	Workspace *workspace.Manager // 工作区管理（PM）：基座内核，先于 filesys/server
	Facade    facade.API
	Filesys   *filesys.Filesys // nil = 未启动（cfg.Filesys=false）
	Server    *server.Server   // nil = 未装配（cfg.NoServer=true）
}

// Build 装配共用脊柱。失败时已申请的资源就地释放（bus 关闭），返回 nil App。
func Build(cfg BootConfig, opts BuildOptions) (*App, error) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		return nil, fmt.Errorf("mq new: %w", err)
	}
	app := &App{Cfg: cfg, Bus: bus, Facade: inline.New(bus)}

	// 工作区管理（PM）：必须在 filesys / server 之前就绪 —— 它订阅 instance-register 并
	// 翻译为 workdir-opened/closed（61 §2.5），filesys 的监听范围由它驱动。
	// P5 步骤 1（C15）：实例视图**单源** —— 先建，再同时注入 workspace 与 server，二者只读同一份。
	im := instance.New(bus)
	ws := workspace.New(bus, workspace.Options{Instances: im})
	if err := ws.Start(context.Background()); err != nil {
		_ = bus.Close()
		return nil, fmt.Errorf("workspace start: %w", err)
	}
	app.Workspace = ws

	if cfg.Filesys {
		fsys := filesys.New(bus, ws)
		if err := fsys.Start(); err != nil {
			ws.Stop()
			_ = bus.Close()
			return nil, fmt.Errorf("filesys start: %w", err)
		}
		app.Filesys = fsys
	}

	if !cfg.NoServer {
		app.Server = server.New(bus, server.Options{
			LLMBase:         cfg.LLMBase,
			LLMModel:        cfg.LLMModel,
			GatewayServers:  opts.GatewayServers,
			LogWriter:       opts.LogWriter,
			Form:            cfg.Form,
			Plugins:         DefaultPlugins(app.Facade),
			InstanceManager: im,
		})
	}
	return app, nil
}

// Start 启动内嵌 llm server（Filesys 已在 Build 内启动）。NoServer 时为空操作。
func (a *App) Start(ctx context.Context) error {
	if a == nil || a.Server == nil {
		return nil
	}
	return a.Server.Start(ctx)
}

// Shutdown 按依赖序停止脊柱（Server → Filesys → Workspace → Bus）。
// 宿主若有额外子系统（插件桥 / 索引器 / 外部工具进程），须在调用本方法**之前**按序收尾。
func (a *App) Shutdown() {
	if a == nil {
		return
	}
	if a.Server != nil {
		a.Server.Stop()
	}
	if a.Filesys != nil {
		a.Filesys.Stop()
	}
	if a.Workspace != nil {
		a.Workspace.Stop()
	}
	if a.Bus != nil {
		_ = a.Bus.Close()
	}
}
