// 能力矩阵的别名再导出（定义处 = `internal/canon/caps.go`）。
package router

import "github.com/chonkpilot/chonkpilot-router/internal/canon"

// Caps 是一个协议 / provider 的能力声明（LR-7 的声明式降级据此进行）。
type Caps = canon.Caps
