package appwire

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-filesys"
	"github.com/chonkpilot/chonkpilot-llm/server"
	"github.com/chonkpilot/chonkpilot-workspace"
	mcpgateway "github.com/lsong98sh/chonkpilot-agent/gateway"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-plugin-framework/runtime/instance"
	"github.com/lsong98sh/chonkpilot-plugin-framework/runtime/lifecycle"
	"github.com/lsong98sh/chonkpilot-plugin-framework/runtime/topics"
)

// BuildOptions 是 Build 的**非快照**入参（含进程内句柄，不能进 BootConfig）。
type BuildOptions struct {
	// GatewayServers 外部下游（servers.list 解析结果；lib 不读源，源解析在面层）。
	GatewayServers []mcpgateway.ServerEntry
	// LogWriter 额外日志 sink（nil = 仅 stdout）。
	LogWriter io.Writer
}

// App 是共用脊柱的装配产物（构造完成、**尚未启动**任何资源）。
//
// 生命周期由 Kernel 编排：Build 只构造 → App.Start 按拓扑序启动 → App.Shutdown 逆序停止。
// 宿主额外子系统（插件平台 / 插件桥 / 索引器等）仍由宿主按依赖序就地收尾，须在
// 【App.Shutdown 之前】完成（它们依赖 agent/workspace 在线）。
type App struct {
	Bus       mq.Bus
	Topics    *topics.Registry   // MQ 主题注册表（61 全集预注册 + 控制主题内置）
	Workspace *workspace.Manager // 工作区管理（PM）：基座内核，先于 filesys/server
	Facade    facade.API
	Filesys   *filesys.Filesys // nil = 未装配（cfg.Filesys=false）
	Server    *server.Server   // nil = 未装配（cfg.NoServer=true）
	// Kernel 编排进程内插件拓扑：bus → workspace/persist → files → agent。
	Kernel *lifecycle.Kernel
	// Scope 保留 = Kernel 根 Scope（宿主可继续登记进程级 goroutine / disposer）。
	Scope *lifecycle.Scope
}

// Build 装配共用脊柱（**只构造，不启动**）。失败时经 Kernel.Stop 逆序清理已构造资源
// （未 Start 的插件 Stop 由闭包守卫；bus 恒会关闭），返回 nil App。
func Build(cfg BootConfig, opts BuildOptions) (*App, error) {
	kernel := lifecycle.NewKernel("app")
	root := kernel.Scope()

	// 主题注册表：启动期一次性预注册 61 契约全集，避免 strict 校验误报；
	// 控制主题（topic-register/unregister/seal/...）由 Registry 内置。
	reg := topics.NewRegistry(topics.BuiltinSpecs...)
	bus, err := mq.New(mq.Options{Prefix: "chonk.", Registry: reg})
	if err != nil {
		stopKernel(kernel)
		return nil, fmt.Errorf("mq new: %w", err)
	}
	app := &App{Bus: bus, Topics: reg, Facade: inline.New(bus), Kernel: kernel, Scope: root}
	fail := func(err error) (*App, error) {
		stopKernel(kernel)
		return nil, err
	}

	// 工作区管理（PM）：必须在 filesys / server 之前就绪 —— 它订阅 instance-register 并
	// 翻译为 workdir-opened/closed（61 §2.5），filesys 的监听范围由它驱动。
	// P5 步骤 1（C15）：实例视图**单源** —— 先建，再同时注入 workspace 与 server，二者只读同一份。
	im := instance.New(bus)
	ws := workspace.New(bus, workspace.Options{Instances: im})
	app.Workspace = ws

	// persist 数据服务（真节点）：由 Kernel persist 插件编排 Start/Stop；同一份实例同时作为
	// ① App.Facade（inline 绑定）② 注入 agent（server 借用不持有）③ data-* MQ 应答面。
	persistSvc := persist.New(bus, persist.Options{Warnf: func(format string, args ...any) {
		if opts.LogWriter != nil {
			fmt.Fprintf(opts.LogWriter, "[persist] "+format+"\n", args...)
		}
	}})

	var fsys *filesys.Filesys
	if cfg.Filesys {
		fsys = filesys.New(bus, ws)
		app.Filesys = fsys
	}

	if !cfg.NoServer {
		app.Server = server.New(bus, server.Options{
			LLMBase:         cfg.LLMBase,
			LLMModel:        cfg.LLMModel,
			GatewayServers:  opts.GatewayServers,
			LogWriter:       opts.LogWriter,
			Form:            cfg.Form,
			Plugins:         DefaultPlugins(),
			InstanceManager: im,
			// persist 归 Kernel persist 节点所有（agent 借用不持有）：
			// Start/Stop 由 Kernel 编排（拓扑序 persist 早于 agent、晚于 workspace）。
			Data: persistSvc,
		})
	}

	// 进程内插件图（拓扑：bus → workspace → persist → files → agent）。
	// 每个 Start/Stop 闭包自带 started 标记：Kernel.Stop 会对未 Start 的插件也调用
	// Stop（Build 失败 / 未 Start 即 Shutdown），此时闭包为空操作。
	if err := kernel.Add(&inprocPlugin{
		id: pluginBus,
		// 总线构造即可用，Start 无副作用；Stop 关闭总线（Kernel 去重保证只关一次）。
		stop: func(context.Context) error { return bus.Close() },
	}); err != nil {
		return fail(fmt.Errorf("kernel add bus: %w", err))
	}

	wsStarted := false
	if err := kernel.Add(&inprocPlugin{
		id:   pluginWorkspace,
		deps: []string{pluginBus},
		start: func(ctx context.Context, _ *lifecycle.Scope) error {
			if err := ws.Start(ctx); err != nil {
				return err
			}
			wsStarted = true
			return nil
		},
		stop: func(context.Context) error {
			if wsStarted {
				ws.Stop()
				wsStarted = false
			}
			return nil
		},
	}); err != nil {
		return fail(fmt.Errorf("kernel add workspace: %w", err))
	}

	// persist 真插件：Start 订阅 data-* 应答面 + instance-* 生命周期；Stop 退订并等
	// 在飞对账收口（返回后无 goroutine 触及数据层连接）。同一份 persistSvc 同时是
	// App.Facade（inline 绑定）与 agent 的注入实现。
	persistStarted := false
	if err := kernel.Add(&inprocPlugin{
		id:   pluginPersist,
		deps: []string{pluginBus},
		start: func(context.Context, *lifecycle.Scope) error {
			if err := persistSvc.Start(); err != nil {
				return err
			}
			persistStarted = true
			return nil
		},
		stop: func(context.Context) error {
			if persistStarted {
				persistSvc.Stop()
				persistStarted = false
			}
			return nil
		},
	}); err != nil {
		return fail(fmt.Errorf("kernel add persist: %w", err))
	}

	if fsys != nil {
		fsStarted := false
		if err := kernel.Add(&inprocPlugin{
			id:   pluginFiles,
			deps: []string{pluginBus, pluginWorkspace},
			start: func(context.Context, *lifecycle.Scope) error {
				if err := fsys.Start(); err != nil {
					return err
				}
				fsStarted = true
				return nil
			},
			stop: func(context.Context) error {
				if fsStarted {
					fsys.Stop()
					fsStarted = false
				}
				return nil
			},
		}); err != nil {
			return fail(fmt.Errorf("kernel add files: %w", err))
		}
	}

	if app.Server != nil {
		srvStarted := false
		if err := kernel.Add(&inprocPlugin{
			id:   pluginAgent,
			deps: []string{pluginBus, pluginWorkspace, pluginPersist},
			start: func(ctx context.Context, _ *lifecycle.Scope) error {
				if err := app.Server.Start(ctx); err != nil {
					// 与旧宿主失败路径等价：Start 中途失败也执行 Stop 收口。
					app.Server.Stop()
					return err
				}
				srvStarted = true
				return nil
			},
			stop: func(context.Context) error {
				if srvStarted {
					app.Server.Stop()
					srvStarted = false
				}
				return nil
			},
		}); err != nil {
			return fail(fmt.Errorf("kernel add agent: %w", err))
		}
	}

	return app, nil
}

// stopKernel 以 10s 上限停止 Kernel（Build 失败路径 / Shutdown 共用，幂等）。
// 未 Start / Start 失败时同样执行：逆序 Stop 已登记插件 + root Scope（bus.Close 收口）。
func stopKernel(kernel *lifecycle.Kernel) {
	if kernel == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = kernel.Stop(ctx)
}

// Start 按 Kernel 拓扑启动全部进程内插件（bus → workspace → persist → files → agent）。
// NoServer / Filesys=false 时对应插件未登记，自然跳过；幂等（重复 Start 为空操作）。
func (a *App) Start(ctx context.Context) error {
	if a == nil || a.Kernel == nil {
		return nil
	}
	return a.Kernel.Start(ctx)
}

// Shutdown 按 Kernel 逆序停止脊柱（agent → files → persist → workspace → bus）。
// 宿主若有额外子系统（插件平台 / 插件桥 / 索引器 / 外部工具进程），须在调用本方法
// **之前**按序收尾（它们依赖 agent/workspace 在线）。
// 幂等：重复调用复用首次 Stop 结果；未 Start / Start 失败时也会关闭已构造资源（含 bus）。
func (a *App) Shutdown() {
	if a == nil || a.Kernel == nil {
		return
	}
	stopKernel(a.Kernel)
}
