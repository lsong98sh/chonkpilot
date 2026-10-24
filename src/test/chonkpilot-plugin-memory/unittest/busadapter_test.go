// 宿主总线 → 插件框架总线的测试适配器。
//
// 背景（PLUGIN-SPLIT-1）：插件仓只依赖 `chonkpilot-plugin-framework`，其 `Deps.Bus`
// 类型 = 框架 `runtime/bus.Bus`；而本模块的 persist 用的是主仓 `lib/mq.Bus`。
// 两者方法面同名但类型不同（Value/Future/Handler 各自包内定义），故集成测试在同一
// 真实总线上加一层薄适配（生产形态由插件桥 bridgebus 承担等价职责）。
package memory_test

import (
	"context"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/lsong98sh/chonkpilot-plugin-framework/runtime/bus"
)

// hostBus 把主仓 mq.Bus 适配为框架 bus.Bus（同步派发语义一致，逐值搬运）。
type hostBus struct{ mq.Bus }

func (a hostBus) Emit(ctx context.Context, subject string, payload any) *bus.Future {
	hv := a.Bus.Emit(ctx, subject, payload).Wait()
	bv := &bus.Value{Subject: hv.Subject, Payload: hv.Payload, Result: hv.Result, Errors: hv.Errors}
	f := bus.NewFuture(bv)
	f.Resolve()
	return f
}

func (a hostBus) On(subject string, order int, h bus.VHandler) (bus.Sub, error) {
	return a.Bus.On(subject, order, func(ctx context.Context, s string, v *mq.Value) error {
		bv := &bus.Value{Subject: v.Subject, Payload: v.Payload, Result: v.Result, Errors: v.Errors}
		err := h(ctx, s, bv)
		v.Result = bv.Result
		v.Errors = bv.Errors
		return err
	})
}

func (a hostBus) Subscribe(subject string, h bus.Handler) (bus.Sub, error) {
	return a.Bus.Subscribe(subject, func(s string, payload []byte) { h(s, payload) })
}
