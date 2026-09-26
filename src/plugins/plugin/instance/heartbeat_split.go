//go:build split

// 分离形态（`-tags split` 编译）实例心跳：发布侧 + 超时判定侧（本文件默认构建**不编入**）。
//
// 形态口径（用户 2026-09-16：「心跳看来还是需要。用编译开关。」，见 42 §2 (72) · 40 T-24）：
//   - 合并单进程形态（**默认构建，无 tag**）：不发布心跳、不做超时判定
//     （空实现见 heartbeat_inprocess.go，保持"零心跳、零超时判定"现状）。
//   - 分离形态（客户端 = 前端 + GUI；服务端 = llm/gateway/filesys/data 合并为一个 exe，
//     两端经 bridge 路由）：客户端按周期发布心跳，判定侧据超时视同实例退出。
//
// 契约不变（61-消息一览 §4.1）：主题 `instance-heartbeat`、payload `{instance_id}`。
package instance

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/heartbeat"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// 心跳契约（分离形态）：周期 30s、超时 90s（= 3× 周期）。
// 取值**单一来源** = chonkpilot-lib/heartbeat（发布侧 / llm 扫描侧 / data 清理侧共用）；此处保留
// 既有导出名（HeartbeatInterval / HeartbeatTimeout）作别名，调用方无需改动。
const (
	HeartbeatInterval = heartbeat.Interval
	HeartbeatTimeout  = heartbeat.Timeout
)

// heartbeatInterval 是实际发布周期（可注入：测试置短周期；产品路径恒 = HeartbeatInterval）。
var heartbeatInterval = HeartbeatInterval

// Publisher 是分离形态心跳发布器（注册实例后按周期发布 instance-heartbeat 保活）。
type Publisher struct {
	bus        mq.Bus
	instanceID string
	busURL     string // 分离形态对端地址（--bridge-url；本形态与订阅方同处一总线，故仅登记）
	stop       chan struct{}
	once       sync.Once
}

// StartHeartbeat 起心跳发布 goroutine（周期 HeartbeatInterval），返回停止函数
// （GUI/CLI 退出时调用；幂等、可重复调用）。bus 空 / instanceID 空 → 空实现。
// busURL = 启动参数 --bridge-url（分离形态 bridge 对端地址，见 62-命令行与参数 / 64-配置项一览）。
func StartHeartbeat(bus mq.Bus, instanceID, busURL string) func() {
	if bus == nil || instanceID == "" {
		return func() {}
	}
	return startPublisher(bus, instanceID, busURL).Stop
}

// startPublisher 构造并启动发布器（测试直取 *Publisher 断言周期/payload/地址登记）。
func startPublisher(bus mq.Bus, instanceID, busURL string) *Publisher {
	p := &Publisher{bus: bus, instanceID: instanceID, busURL: busURL, stop: make(chan struct{})}
	go p.loop()
	return p
}

// URL 返回分离形态 bridge 对端地址（--bridge-url；只读诊断口）。
func (p *Publisher) URL() string { return p.busURL }

func (p *Publisher) loop() {
	t := time.NewTicker(heartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
			p.beat()
		}
	}
}

// beat 发布一次心跳（payload 仅 {instance_id}，对齐既有订阅方契约；不新增字段）。
func (p *Publisher) beat() {
	raw, err := json.Marshal(map[string]string{"instance_id": p.instanceID})
	if err != nil {
		return
	}
	p.bus.Emit(context.Background(), SubjectHeartbeat, raw)
}

// Stop 停止心跳（幂等）。
func (p *Publisher) Stop() { p.once.Do(func() { close(p.stop) }) }

// Sweep 分离形态专用超时清理：LastBeat（最近注册/心跳时刻）距今 ≥ timeout 的实例视为退出，
// 从实例视图移除并返回（调用方据返回列表走各自的既有实例退出清理路径）。
// timeout <= 0 → HeartbeatTimeout。
func (m *Manager) Sweep(timeout time.Duration) []Info {
	if timeout <= 0 {
		timeout = HeartbeatTimeout
	}
	now := m.now()
	m.mu.Lock()
	var gone []Info
	for id, r := range m.recs {
		if r.LastBeat.IsZero() || now.Sub(r.LastBeat) < timeout {
			continue
		}
		gone = append(gone, *r)
		delete(m.recs, id)
	}
	m.mu.Unlock()
	return gone
}
