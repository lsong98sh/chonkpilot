//go:build split

// 分离形态（`-tags split` 编译）实例心跳超时清理（本文件默认构建**不编入**）。
//
// 形态口径（用户 2026-09-16：「split 是要心跳，standalone 不需要」，见 42 §2 (72) · 40 T-24）：
// 分离形态下实例崩溃若未发 `instance-exit`，数据根绑定（insts）不会释放 → 后续按该
// instance_id 解析库连接会命中失效绑定。本文件按周期扫描，超时实例走**既有退出清理**：
// s.instanceGone（= instance-exit 同一实现：移除绑定 + data.Unregister）。
//
// 默认构建（合并单进程形态）为空实现（sweep_inprocess.go）：无 ticker、无超时判定。
// **不新增 MQ 主题**、payload 契约不变（61-消息一览 §4.1：instance-heartbeat {instance_id}）。
//
// G-29（2026-09-20）：超时回收**与显式退出同口径 —— 补发 `instance-exit`**（主题与 payload
// 一字不改，61-消息一览 §4.1 ③ {instance_id}）→ 全部订阅方（filesys 的 instance → work_dir
// 绑定、本层数据根绑定、插件实例视图）随之解绑。此前只在本地走 instanceGone、不广播 →
// 已回收实例的旧 instance_id 仍能通过 filesys 的越界校验读写其 work_dir（安全缺口）。
package persist

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/heartbeat"
)

// staleTimeout 是实例心跳超时阈值 = chonkpilot-lib/heartbeat.Timeout（90s = 3× 周期）——
// 取值**单一来源**（发布侧 instance / llm 扫描侧 / 本清理侧共用同一常量）。
// data 层只依赖 lib（不引 chonkpilot-plugin，分层约束见 10-分层与依赖），故经 lib 取同一来源。
const staleTimeout = heartbeat.Timeout

// sweepInterval 是实际扫描周期（可注入：测试置短周期；产品路径恒 = heartbeat.Interval = 30s）。
var sweepInterval = heartbeat.Interval

// startStaleSweep 起周期扫描 goroutine，返回停止函数（幂等：可重复调用；Stop 调用时等待
// goroutine 退出——停止返回后无扫描在跑）。
func (s *Service) startStaleSweep() func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(done)
		t := time.NewTicker(sweepInterval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				s.sweepStale()
			}
		}
	}()
	return func() { once.Do(func() { close(stop); <-done }) }
}

// sweepStale 扫描一次：最近注册/心跳（LastBeat）距今 ≥ staleTimeout 的实例逐个走
// instanceGone（既有退出清理：移除绑定 + data.Unregister）并**补发 `instance-exit`**
// （G-29：超时回收 = 视同退出，与显式退出同一广播路径 → filesys 等订阅方解绑），返回清理数。
//
// 判定在实例视图变更的空窗内成立即清理（与 instance.Manager.Sweep 同口径：先快照再清理；
// 快照由 kernel.View.Stale 提供）；幂等：重复扫描时陈旧实例已不在视图（不再清理/不再广播）、
// instanceGone 重复调用无副作用（delete 空键 + data.Unregister 空绑定皆无害）；订阅方重复
// 收到同一 id 亦无副作用。
func (s *Service) sweepStale() int {
	stale := s.View.Stale(time.Now(), staleTimeout)
	for _, id := range stale {
		s.instanceGone(id)
		s.publishInstanceExit(id)
	}
	return len(stale)
}

// publishInstanceExit 广播 `instance-exit{instance_id}`（61-消息一览 §4.1 ③；payload 契约与
// 显式退出**完全一致**，不新增主题/字段）。本层 onInstanceExit 同订阅该主题 → instanceGone
// 会再跑一次（幂等，见其注释）。主题名取本包既有常量 instanceExit（data 层不引 chonkpilot-plugin）。
func (s *Service) publishInstanceExit(instanceID string) {
	b, _ := json.Marshal(map[string]string{"instance_id": instanceID})
	_ = s.Bus.Emit(context.Background(), instanceExit, b)
}
