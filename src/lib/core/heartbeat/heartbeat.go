// Package heartbeat 是**实例心跳契约常量的单一来源**（分离形态 `-tags split`）。
//
// 契约（61-消息一览 §4.1）：主题 `instance-heartbeat`、payload `{instance_id}`；
// 取值沿用 61-消息一览 §4.1（客户端周期保活 30s）与 28-plugins §2（heartbeatTimeout 90s）的历史约定。
//
// 为什么要上提到 lib：同一组取值有三处消费方——发布侧（`chonkpilot-plugin/instance`）、
// 服务端扫描侧（`chonkpilot-llm/server`）、数据层清理侧（`chonkpilot-data/persist`）。
// 其中数据层**不得依赖 `chonkpilot-plugin`**（分层约束，见 10-分层与依赖），
// 故原先把 30s/90s 就地复制到 data 层；上提到 lib（所有模块的依赖终点）后三处引用同一常量，
// 改值只需一处，依赖方向仍合法（lib → 无本仓库依赖）。
package heartbeat

import "time"

// 实例心跳契约取值（分离形态）。
const (
	// Interval 是客户端心跳发布周期（GUI/CLI 按此周期发布 `instance-heartbeat` 保活）。
	Interval = 30 * time.Second
	// Timeout 是服务端/数据层的心跳超时阈值 = 3× 周期（超过即视同实例退出）。
	Timeout = 90 * time.Second
)
