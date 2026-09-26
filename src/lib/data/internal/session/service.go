// Package session 是 session / turn / message 三域门面实现（`chonkpilot-data/facade` 的
// SessionAPI / TurnAPI / MessageAPI）的落地包（阶段 4「internal 下沉」：由
// `chonkpilot-data/persist` 下沉至此）。
//
// 覆盖 = `data-session-*` 全量 promise 面（会话 CRUD/活动态 · 轮次 · 消息）。
// 存储形状（`history` JSON 字符串、`{call,result,async}`、视图行键集）与门面 DTO 的翻译
// 收在 `facade/wire`（一份翻译，三条路径逐字一致）；落库形状与 helper 收在 internal/kernel。
//
// 单一实现两处绑定（同一份代码，不并列两套写法）：
//   - inline 绑定：`facade/inline.New` 直接构造本服务（同进程直调，不经 MQ）；
//   - mq  绑定：persist 的 `data-session-*` handler **委托本文件的同一批方法**，
//     只负责信封（reply/fail）——故两条路径行为等价（有测试）。
//
// 变更广播（订阅面；23 §7）：`SessionEnsure` **首次**落库后广播既有 `session-new`
// 事件（61 §4.1；主题与载荷不变）；`SessionTitle` 写库成功后广播 `data-session-title-changed`
// （61 §3.2，**不带 instance_id** = 全局 → 已开对话窗口标题跟随），任何绑定下订阅方照旧收到。
//
// internal 门禁：本包不导出给模块外（见 internal/kernel 包注释）。
package session

import "github.com/chonkpilot/chonkpilot-data/internal/kernel"

// Service 是 session / turn / message 三域门面实现（inline 绑定与 MQ 信封层共用）。
type Service struct {
	*kernel.Base
}

// New 构造 session 域服务（共享内核由装配侧注入）。
func New(base *kernel.Base) *Service { return &Service{Base: base} }
