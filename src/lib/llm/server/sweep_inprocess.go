//go:build !split

package server

// startInstanceSweep 桌面单体形态（**默认构建**）：空实现——不起扫描 goroutine、
// 不调用 instanceManager.Sweep（实例失效只走显式 instance-exit 的 exitInstance；
// 用户口径：desktop 不需要心跳）。返回 nil 供 Stop 判空跳过。
// 分离形态实现见 sweep_split.go（`-tags split`）。
func (s *Server) startInstanceSweep() func() { return nil }
