// Package plugin 定义 server 内嵌插件钩子契约（chonkpilot-plugin-* 独立 module 实现）。
//
// 形态（2026-09-02 决策）：内嵌 lib——server 进程内、同一 mq.Bus 加载；
// 插件在 server.Start 全部就绪后按序 Start（订阅所需事件 + 自检），全部成功
// 后才广播 server-starting（用户要求：插件加载完成 = 就绪信号）。
//
// 内嵌插件自给原则：数据访问**经 data 门面**（`facade.API` 的 inline 绑定，宿主注入；
// 不持库句柄——2026-09-21 订正：原「data.Prj(instanceID) / data.PrjUsr(instanceID) 取层」的
// 直开库写法已废）；work_dir 解析走本包 instance.Manager
// （共享订阅 instance-register/heartbeat/exit）——插件不感知 server 内部结构。
package plugin

import "github.com/chonkpilot/chonkpilot-lib/mq"

// Deps 是宿主（server）注入内嵌插件的最小依赖。
type Deps struct {
	// Bus 事件总线（插件在此订阅/调用消息面——唯一通讯通道，2026-09-08 定稿：
	// 插件不依赖宿主函数注入（原 Summarize 已移除），统一经 llm-simple 等 mq 方法面）。
	Bus mq.Bus
	// Logf 插件日志（宿主统一转发；格式同 log.Printf）。
	Logf func(format string, args ...any)
	// Notify 插件**失败**的用户可见上报（可选；nil = 不上报，行为同改前）。
	// 2026-09-20 新增：插件失败（跳过类）原先只 Logf → 用户不可见（GUI 无控制台时连日志
	// 也不可见）。宿主（server）负责**去重/限频 + 呈现**（经既有通知面，不新增 MQ 主题）；
	// 插件侧只负责"如实上报一次失败"，不阻塞流程、不改终态、不抛出。
	Notify func(Notice)
}

// Notice 是插件上报的**用户可见**失败提示（非阻塞、不打扰；宿主决定是否/如何呈现）。
type Notice struct {
	// Plugin 插件名（memory / compress / …；宿主据此选择措辞）。
	Plugin string
	// Kind 失败类别（稳定 key，如 read / llm / save / config；宿主判重键之一）。
	Kind string
	// InstanceID 实例 id（61-消息一览 §0：实例字段必带；宿主判重键之一）。
	InstanceID string
	// Session 会话 id（前端据此把提示归到对应会话窗口；空 = 不限定）。
	Session string
	// Turn 轮次 id（宿主判重键之一：同一轮同一插件同一类别只提示一次）。
	Turn string
	// Reason 失败原因（人类可读；同时由插件写日志，落文件可查）。
	Reason string
}

// Hook 是 server 内嵌插件钩子（chonkpilot-plugin-* 独立 module 实现）。
type Hook interface {
	// Name 插件唯一名（日志/去重）。
	Name() string
	// Start 在 server 启动（gateway/路由/实例视图就绪后）按序调用：
	// 插件在此订阅所需事件并自检。全部插件 Start 成功后 server 广播 server-starting。
	Start(d Deps) error
}
