// Package inline 是 data 门面的 **inline 绑定**：同进程直接函数调用，**不经 MQ**（23 §7）。
//
// 装配方式（23 §7「编译期决定编入哪些绑定、装配期决定用哪个」）：宿主在自己的装配处
// `inline.New(bus)` 并注入给调用方（本期注入点：
//   - 插件装配处：`src/gui/main.go` · `src/llm/main.go` · `src/cli/main.go`（compress.New）
//   - 入口桥：`src/gui/main.go`（bridge.SetFacade —— config 类 data-* 走门面）
//
// 组成：各域面实现**分别**由所属包提供（snapshot / config / session（含 turn·message）/
// tasktree / knowledge / filelist / scenario / memory = `chonkpilot-data/internal/<域>`，
// 见 23 §7 与 41 G-36）；本绑定只做合成（Go 无部分导出；域面 interface 见 facade/api.go）。
// 装配 = persist.New（persist 现为「MQ 信封 + 装配」：构造各域实现并共享同一 internal/kernel）。
//
// 变更广播（订阅面，23 §7「请求面 + 订阅面成对」）：config 域实现持有宿主的 MQ 总线，
// 写入后自行广播 data-<domain>-refresh；session 域实现同样持有总线，幂等建会话**首次**落库后
// 广播既有 `session-new` → 订阅方（前端 / server）照旧收到。
// 故 bus 必须传**宿主实际使用的总线**（同进程内存总线）；测试/no-bus 场景可传 nil（不广播）。
//
// 后续 http / mq 绑定是同一 `facade.API` 的另一些实现，调用方与业务代码零改动。
package inline

import (
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/snapshot"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// binding 合成各域面实现（域面方法名不相交，指针嵌入即可提升为完整 facade.API）。
type binding struct {
	facade.SnapshotAPI
	facade.ConfigAPI
	facade.SessionAPI
	facade.TurnAPI
	facade.MessageAPI
	facade.TasktreeAPI
	facade.KnowledgeAPI
	facade.FileListAPI
	facade.ScenarioAPI
	facade.MemoryAPI
}

// 编译期断言：合成结果 = 完整门面。
var _ facade.API = (*binding)(nil)

// New 构造 inline 绑定（直接持有各域实现；同进程、无序列化、无跨进程错误语义）。
//
// bus：宿主总线（config 域刷新广播 / session-new 广播 / task-deleted / scenario·memory 刷新
// 广播用；nil = 不广播，仅测试/无总线场景）。
// usrPath / appDir 由 data 组件全局事实决定（data.UserPath / 可执行目录），无需在此传入。
func New(bus mq.Bus) facade.API {
	return NewWithOptions(bus, persist.Options{})
}

// NewWithOptions 同 New，但允许注入 persist 选项（**测试隔离**：临时 usr 库 / 自定义 app 级
// 知识库根，使 inline 绑定与同进程的 MQ 面指向同一数据根；生产装配一律用 New）。
func NewWithOptions(bus mq.Bus, opts persist.Options) facade.API {
	svc := persist.New(bus, opts)
	return &binding{
		SnapshotAPI:  snapshot.New(),
		ConfigAPI:    svc,
		SessionAPI:   svc,
		TurnAPI:      svc,
		MessageAPI:   svc,
		TasktreeAPI:  svc,
		KnowledgeAPI: svc,
		FileListAPI:  svc,
		ScenarioAPI:  svc,
		MemoryAPI:    svc,
	}
}
