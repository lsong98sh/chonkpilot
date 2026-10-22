package appwire

import (
	"context"
	"fmt"
	"io"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-filesys"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-llm/server"
	mcpgateway "github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
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
// 托盘 / UDS / HTTP 收尾），本包只保证**构造顺序**与「谁依赖谁」：Bus → Facade →
// Filesys(可选) → Server(可选)。
type App struct {
	Cfg     BootConfig
	Bus     mq.Bus
	Facade  facade.API
	Filesys *filesys.Filesys // nil = 未启动（cfg.Filesys=false）
	Server  *server.Server   // nil = 未装配（cfg.NoServer=true）
}

// Build 装配共用脊柱。失败时已申请的资源就地释放（bus 关闭），返回 nil App。
func Build(cfg BootConfig, opts BuildOptions) (*App, error) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		return nil, fmt.Errorf("mq new: %w", err)
	}
	app := &App{Cfg: cfg, Bus: bus, Facade: inline.New(bus)}

	if cfg.Filesys {
		fsys := filesys.NewWithOptions(bus, filesys.Options{IndexQueue: indexQueue(app.Facade)})
		if err := fsys.Start(); err != nil {
			_ = bus.Close()
			return nil, fmt.Errorf("filesys start: %w", err)
		}
		app.Filesys = fsys
	}

	if !cfg.NoServer {
		app.Server = server.New(bus, server.Options{
			LLMBase:        cfg.LLMBase,
			LLMModel:       cfg.LLMModel,
			GatewayServers: opts.GatewayServers,
			LogWriter:      opts.LogWriter,
			WorkDir:        cfg.WorkDir,
			DataDir:        cfg.DataDir,
			Form:           cfg.Form,
			AuthDBPath:     cfg.Auth.DBPath,
			UserDataRoot:   cfg.Auth.UserDataRoot,
			AllowRegister:  cfg.Auth.AllowRegister,
			ProjectRoots:   cfg.Auth.ProjectRoots,
			Plugins:        DefaultPlugins(app.Facade),
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

// Shutdown 按依赖序停止脊柱（Server → Filesys → Bus）。
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
	if a.Bus != nil {
		_ = a.Bus.Close()
	}
}
