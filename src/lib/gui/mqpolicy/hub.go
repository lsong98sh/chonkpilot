package mqpolicy

import (
	"sync"

	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-plugin-framework/runtime/dispatch"
)

// Hub 是一条总线上的「分发器 + 策略」对：多窗口共享**同一个** ">" 订阅（改造前是每窗口
// 各自订阅、各自过滤）。
type Hub struct {
	// Dispatch 是 framework 侧分发器（单订阅 ">"；只做机制）。
	Dispatch *dispatch.Dispatcher
	// Policy 是主仓侧策略（业务规则；窗口 sink 的发布账本也在这里）。
	Policy *WorkDirPolicy
}

// hubs 是「总线 → Hub」进程级表：**一条总线一份**，首次使用时建立。
//
// 不回收的原因：Hub 的生命周期 = 总线生命周期（进程内恒一条总线），窗口开关只做
// sink 注册/注销；回收会在「关窗与开窗并发」时把新窗口的订阅一并停掉。键为 mq.Bus
// 接口值，要求实现可比较（当前全部实现均为指针类型）。
var hubs sync.Map

// HubFor 返回该总线对应的 Hub（首次调用创建；bus == nil → nil，供「无总线」形态调用方
// 直接空转）。
func HubFor(bus mq.Bus) *Hub {
	if bus == nil {
		return nil
	}
	if v, ok := hubs.Load(bus); ok {
		return v.(*Hub)
	}
	p := New()
	v, _ := hubs.LoadOrStore(bus, &Hub{
		Dispatch: dispatch.New(dispatch.Options{Bus: bus, Policy: p}),
		Policy:   p,
	})
	return v.(*Hub)
}
