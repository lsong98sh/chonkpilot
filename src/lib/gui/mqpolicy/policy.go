// Package mqpolicy 是主仓侧的 **MQ 分发出口策略**（清单 49 §2.22.7）——**薄封装**。
//
// 分层：framework `runtime/dispatch` 只做机制（单订阅 ">" → 组装 Event → 问 Policy → 逐目标
// 投递 + sink 注册表）；**workdir / instance 路由属于业务**，其**唯一实现已收归 PM**
// （`workspace.Router.Route`，SANDBOX-WRITE-1 P5 步骤 3 / C15）。本包只剩两件事：
//
//	① 把 dispatch.Sink ↔ workspace.Sink 做无损转换；
//	② 把 Mark / Deliver 转发给 PM 的路由器（`SetRouter` 注入同一份发布账本与反查视图）。
//
// 规则（逐字沿用迁移前，详见 workspace/router.go 包注释）：
//
//	① 自发布丢弃（topic + 载荷指纹，1s 窗口）；
//	② `data-*` 的 persist 同主题应答丢弃；
//	③ 载荷 `instance_id` 非空 → 只投该实例的 sink（未标识放行）；
//	④ workdir 级主题只投同 workdir（事件 / sink workdir 未知 → 保守放行）；
//	⑤ 其余全投。
//
// 收紧**只会缩小**投递集合；任何未知情形一律放行 —— 只缩小，不放大。
package mqpolicy

import (
	"github.com/lsong98sh/chonkpilot-plugin-framework/runtime/dispatch"

	"github.com/chonkpilot/chonkpilot-workspace"
)

// WorkDirPolicy 实现 dispatch.Policy：全部业务规则委托 PM 的 `workspace.Router`
// （本类型只做 sink 类型转换，**不得**再实现一份路由逻辑）。
type WorkDirPolicy struct {
	router *workspace.Router
}

// New 构造策略（自带一个独立路由器；宿主可用 SetRouter 换成 PM 的同一份）。
func New() *WorkDirPolicy {
	return &WorkDirPolicy{router: workspace.NewRouter()}
}

// SetRouter 注入 PM 的路由器（宿主装配期调用一次；nil = 忽略）。
// 注入后 Mark / Deliver 与 PM 的 `Manager.Route` 共用同一份发布账本与实例反查视图。
func (p *WorkDirPolicy) SetRouter(r *workspace.Router) {
	if p == nil || r == nil {
		return
	}
	p.router = r
}

// SetInstanceWorkDir 注入 `instance_id → work_dir` 反查（转发 PM；fn 为 nil = 不反查）。
func (p *WorkDirPolicy) SetInstanceWorkDir(fn func(instanceID string) (string, bool)) {
	if p == nil {
		return
	}
	p.router.SetInstanceWorkDir(fn)
}

// Mark 记录一次发布（**发布前**调用；owner = 该窗口实例 id）。
func (p *WorkDirPolicy) Mark(owner, subject string, payload []byte) {
	if p == nil {
		return
	}
	p.router.Mark(owner, subject, payload)
}

// Deliver 实现 dispatch.Policy：转换为 PM 的 sink 描述 → `Router.Route` → 还原 dispatch.Sink。
func (p *WorkDirPolicy) Deliver(evt dispatch.Event, sinks []dispatch.Sink) []dispatch.Sink {
	in := make([]workspace.Sink, len(sinks))
	for i, s := range sinks {
		in[i] = workspace.Sink{
			ID:       s.ID,
			Instance: s.Instance,
			WorkDir:  s.WorkDir,
			Plugin:   s.Kind == dispatch.SinkPlugin,
		}
	}
	targets := p.router.Route(evt.Topic, evt.Payload, in)
	if len(targets) == 0 {
		return nil
	}
	out := make([]dispatch.Sink, 0, len(targets))
	for _, t := range targets {
		out = append(out, sinks[t.Index])
	}
	return out
}
