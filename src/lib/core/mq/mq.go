// Package mq 是**宿主侧总线别名**：唯一实现 = 插件框架 `runtime/bus`
// （2026-10-25，PLUGIN-SPLIT-1「统一总线」）。
//
// 背景：框架与主仓此前各有一份同形实现（framework/runtime/bus 与
// chonkpilot-lib/mq），导致插件契约 `hook.Deps.Bus` 与宿主 `*mq.Bus` 类型不同、
// 无法互相赋值。现把实现收敛到框架（插件可依赖的唯一共享层），主仓以**类型别名**
// 转发——148 处 `mq.*` 调用点无需改动，且宿主与插件操作的是同一套类型。
//
// 约束：本包只做转发，不得新增实现；行为/测试以 framework `runtime/bus` 为准。
package mq

import bus "github.com/lsong98sh/chonkpilot-plugin-framework/runtime/bus"

// 类型别名（与 framework runtime/bus 完全同型）。
type (
	// Bus 事件总线接口。
	Bus = bus.Bus
	// Sub 订阅句柄。
	Sub = bus.Sub
	// Value 消息值（payload 的结构化封装）。
	Value = bus.Value
	// Future 一次 Emit 的异步结果。
	Future = bus.Future
	// Handler 原始 payload 处理器。
	Handler = bus.Handler
	// VHandler 结构化 Value 处理器。
	VHandler = bus.VHandler
	// Options 总线构造选项。
	Options = bus.Options
)

// New 构造总线（实现 = framework runtime/bus）。
func New(opts Options) (Bus, error) { return bus.New(opts) }

// NewFuture 构造一个已完成/待完成的 Future（对齐 framework 口径）。
func NewFuture(v *Value) *Future { return bus.NewFuture(v) }
