//go:build split

// 分离形态（`-tags split` 编译）实例心跳超时扫描（本文件默认构建**不编入**）。
//
// 形态口径（用户 2026-09-16：「split 是要心跳，standalone 不需要」，见 42 §2 (72) · 40 T-24）：
//   - 分离形态（gui）：客户端（前端 + GUI）发布 instance-heartbeat 保活；本（服务端）按周期扫描，
//     实例最近注册/心跳时刻超 HeartbeatTimeout（90s = 3× 周期）→ 视同实例退出。
//   - 桌面单体形态（**默认构建，无 tag**）：GUI 启动注册、进程随会话存续，不做超时判定
//     （空实现见 sweep_inprocess.go：零 ticker、零 Sweep 调用）。
//
// 清理走**既有退出清理路径**（s.exitInstance = instance-exit 同一实现：取消名下 running turn
// + 释放 session 锁 + 注销 capability dir 节点），**不新增 MQ 主题**、payload 契约不变
// （61-消息一览 §4.1：instance-heartbeat {instance_id}）。
//
// G-29（2026-09-20）：超时回收**与显式退出同口径 —— 补发 `instance-exit`**（主题与 payload
// 一字不改，61-消息一览 §4.1 ③ {instance_id}）→ 全部订阅方（filesys 的 instance → work_dir
// 绑定、persist 数据根绑定、插件实例视图）随之解绑。此前只在本地走 exitInstance、不广播 →
// 已回收实例的旧 instance_id 仍能通过 filesys 的越界校验读写其 work_dir（安全缺口）。
package server

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/heartbeat"
)

// sweepInterval 是实际扫描周期（可注入：测试置短周期；产品路径恒 = heartbeat.Interval，
// 取值**单一来源** = chonkpilot-lib/heartbeat：周期 30s、超时 90s）。
var sweepInterval = heartbeat.Interval

// startInstanceSweep 起心跳超时扫描 goroutine（周期 sweepInterval），返回停止函数
// （幂等：可重复调用；Server.Stop 调用。停止时等待 goroutine 退出——Stop 返回后无扫描在跑）。
func (s *Server) startInstanceSweep() func() {
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
				s.sweepStaleInstances()
			}
		}
	}()
	return func() { once.Do(func() { close(stop); <-done }) }
}

// sweepStaleInstances 扫描一次：instanceManager.Sweep 返回的陈旧实例（LastBeat 距 now ≥
// HeartbeatTimeout）逐个走既有退出清理（exitInstance）；实例视图移除由 Sweep 自身完成。
// 清理后**补发 `instance-exit`**（G-29：超时回收 = 视同退出，与显式退出同一广播路径）——
// 订阅方（含 filesys）据此解绑，见文件头说明。
// 幂等：重复扫描无副作用（Sweep 已把陈旧实例移出视图 → 不再重复清理/重复广播）；
// 订阅方重复收到同一 id（如本包与 persist 两处扫描同时判定）亦无副作用。
func (s *Server) sweepStaleInstances() {
	for _, info := range s.im.Sweep(heartbeat.Timeout) {
		logf("[chonkpilot-server] 实例 %s 心跳超时 %v → 视同退出（分离形态）\n",
			info.ID, heartbeat.Timeout)
		s.exitInstance(info.ID)
		s.publishInstanceExit(info.ID)
	}
}

// publishInstanceExit 广播 `instance-exit{instance_id}`（61-消息一览 §4.1 ③；payload 契约
// 与显式退出**完全一致**，不新增主题/字段）。本包 onExit 同订阅该主题 → exitInstance 会再跑
// 一次（幂等，见 exitInstance 注释），语义上"超时回收 = 一次退出广播"。
func (s *Server) publishInstanceExit(instanceID string) {
	b, _ := json.Marshal(map[string]string{"instance_id": instanceID})
	_ = s.bus.Emit(context.Background(), SubjectExit, b)
}
