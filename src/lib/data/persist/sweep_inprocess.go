//go:build !split

package persist

// startStaleSweep 桌面单体形态（**默认构建**）：空实现——不起扫描 goroutine、不做心跳
// 超时判定，实例失效只走显式 instance-exit 的 instanceGone（用户口径：desktop 不需要心跳）。
// 返回 nil 供 Stop 判空跳过。分离形态实现见 sweep_split.go（`-tags split`）。
func (s *Service) startStaleSweep() func() { return nil }
