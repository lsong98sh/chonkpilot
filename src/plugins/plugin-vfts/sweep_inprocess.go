//go:build !split

package vfts

import "time"

// heartbeatJudged 形态标识：合并单进程形态（**默认构建**）不做心跳超时退出判定（无心跳发布方）。
const heartbeatJudged = false

// sweepInstances 合并单进程形态：空实现——实例失效只走显式 instance-exit 的 instanceGone
// （用户口径见 42 §2 (69)(72)）。分离形态实现见 sweep_split.go（`-tags split`）。
func (p *Vfts) sweepInstances(time.Time) {}
