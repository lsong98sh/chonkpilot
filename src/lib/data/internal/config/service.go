// Package config 是 config 域门面实现（`chonkpilot-data/facade` 的 ConfigAPI）的落地包
// （阶段 4「internal 下沉」：由 `chonkpilot-data/persist` 下沉至此）。
//
// 覆盖：同族 kv 域（prj-config / prompt / prj-security）+ 用户配置（usr 主库视图 / 合并有效值 /
// 增量写 / 删）。存储事实（config 表键前缀、llmref/int 类型还原、llms·mcps 专用表、
// summary_prompt 文件化）一律不出本包（门面只承载领域键 → 值）。
//
// 单一实现两处绑定（同一份代码，不并列两套写法）：
//   - inline 绑定：`facade/inline.New` 直接构造本服务（同进程直调，不经 MQ）；
//   - mq  绑定：persist 的 `data-<domain>-*` handler **委托本文件的同一批方法**，
//     只负责信封（reply/fail）——故两条路径行为等价（有测试）。
//
// 变更广播（订阅面；23 §7「请求面 + 订阅面成对」）：写入方法自身在成功后广播
// `data-<domain>-refresh`（user-config 另兼容 `config-refresh`），故**任何绑定**下订阅方
// （前端 / 插件 / llm server 热生效）都照旧收到——不依赖 MQ 请求-应答往返；
// user-config 另在 save/delete 成功后广播**配置类下行事件** `data-user-config-changed`
// （不带 instance_id = 全局，61 §3.1；多窗口 theme/locale 即时同步，24 §6.5 · WIN-021）。
//
// internal 门禁：本包不导出给模块外（见 internal/kernel 包注释）。
package config

import "github.com/chonkpilot/chonkpilot-data/internal/kernel"

// Service 是 config 域门面实现（inline 绑定与 MQ 信封层共用）。
type Service struct {
	*kernel.Base
}

// New 构造 config 域服务（共享内核由装配侧注入）。
func New(base *kernel.Base) *Service { return &Service{Base: base} }
