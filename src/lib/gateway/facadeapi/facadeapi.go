// Package facadeapi 是 chonkpilot-mcp-gateway 的**跨 module 门面转发面**。
//
// 背景（D-28 src 结构重整）：gateway 的 exe 外壳已迁至 `src/others/mcp-gateway`
// （独立 module），而门面实现按「非门面的一切下沉 `<组件>/internal/`」（23 §2）保留在
// `internal/facade` —— Go 的 internal 规则**不允许跨 module 直接 import**。
// 本包只做**符号转发**（类型别名 + New），不含任何逻辑、不改任何行为：外壳 import 本包，
// internal 的编译期隔离语义保持不变。
package facadeapi

import (
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-mcp-gateway/internal/facade"
)

// Adapter 是 internal/facade.Adapter 的类型别名（同一类型，非包装类型）。
type Adapter = facade.Adapter

// New 构建适配器（转发 internal/facade.New）。
func New(bus mq.Bus) *Adapter { return facade.New(bus) }
