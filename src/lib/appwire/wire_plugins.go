package appwire

import (
	"context"

	"github.com/lsong98sh/chonkpilot-plugin-framework/runtime/lifecycle"
)

// inprocPlugin 是 **进程内** 插件适配器：把已有模块的构造/启动/停止闭包接到
// lifecycle.Plugin 契约上（不做进程外拆包）。
//
// start 可为 nil（纯占位插件，如 persist：Facade 已构造，无 Start/Stop 资源）。
// stop 可为 nil；Kernel.Stop 对「未 Start 也调用 Stop」的插件要求 stop 自行守卫
// （本包闭包以 started 标记实现，未成功启动时为空操作）。
type inprocPlugin struct {
	id    string
	deps  []string
	start func(ctx context.Context, sc *lifecycle.Scope) error
	stop  func(ctx context.Context) error
}

func (p *inprocPlugin) ID() string        { return p.id }
func (p *inprocPlugin) Depends() []string { return p.deps }

func (p *inprocPlugin) Start(ctx context.Context, sc *lifecycle.Scope) error {
	if p.start == nil {
		return nil
	}
	return p.start(ctx, sc)
}

func (p *inprocPlugin) Stop(ctx context.Context) error {
	if p.stop == nil {
		return nil
	}
	return p.stop(ctx)
}

// 进程内插件 ID / 依赖（拓扑：bus → workspace → persist → files → agent）。
const (
	pluginBus       = "bus"
	pluginWorkspace = "workspace"
	pluginPersist   = "persist"
	pluginFiles     = "files"
	pluginAgent     = "agent"
)
