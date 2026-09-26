//go:build split

package codegraph

import (
	"time"

	"github.com/chonkpilot/chonkpilot-plugin/instance"
)

// heartbeatJudged 形态标识：分离形态（`-tags split`）启用**心跳超时退出判定**
// （测试据以分流"合并单进程形态不判超时"用例，见 codegraph_test.go）。
const heartbeatJudged = true

// sweepInstances 分离形态：实例最近注册/心跳（ir.last）超 instance.HeartbeatTimeout（90s = 3×
// 心跳周期）→ 视同实例退出，走**既有** instanceGone 清理路径（解绑 + 递减所属 workdir 引用；
// 引用归零 → 注销 gateway 工具面）。引擎子进程随后由 sweepIdleClient 按既有空闲规则回收。
func (p *Codegraph) sweepInstances(now time.Time) {
	p.mu.Lock()
	var stale []string
	for id, ir := range p.insts {
		if !ir.last.IsZero() && now.Sub(ir.last) >= instance.HeartbeatTimeout {
			stale = append(stale, id)
		}
	}
	p.mu.Unlock()
	for _, id := range stale {
		p.logf("codegraph: 实例 %s 心跳超时 %v → 视同退出（分离形态）", id, instance.HeartbeatTimeout)
		p.instanceGone(id)
	}
}
