// subchat_delivery_test.go — 单聊**跨窗口定向投递**（mq 驱动 + 监听断言；2026-10-16，SUBCHAT-2 B-1）。
//
// 依据：单聊 §12.4 B4 / 群聊 §14.2 · §14.4 N18–N21（用户已裁决）——
//
//	① 写侧：开窗路径带 `parent_instance`（"谁开的指谁"·**逐层链**）→ 桥随 instance-register 上报；
//	② 定向投递：查 instance 表取**父实例 id** → 填进要投递消息的 `instance_id` 字段
//	   （**不改桥** —— 桥按 instance_id 精确相等过滤，天然投给该窗口）；
//	③ 广播：不带 `instance_id`（「父 + 子都要」）→ 桥的「唯一例外」放行 → 投给**所有**窗口。
//
// 断言层 = 相对主题事件（总线）→ 各窗口桥 forwardEvent → 本窗口前端 eval（禁仅存在级断言：
// 定向 = 目标窗口收到 **且** 非目标窗口收不到；广播 = 两个窗口都收到）。
package bridge_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-gui/bridge"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/lsong98sh/chonkpilot-plugin-framework/runtime/instance"
)

// newInstBridge 建「指定实例 id」的桥并 Start（各自 instance-register + ">" 转发订阅）；
// parent 非空 = 按写侧语义先注入直接父实例（等价窗口工厂 SetParentInstance）。
func newInstBridge(t *testing.T, bus mq.Bus, id, parent string) (*bridge.Bridge, chan string) {
	t.Helper()
	evals := make(chan string, 64)
	br := bridge.New(id, "wd", "", evalCollect(evals), bus)
	if parent != "" {
		br.SetParentInstance(parent)
	}
	if err := br.Start(); err != nil {
		t.Fatalf("Start(%s): %v", id, err)
	}
	return br, evals
}

// waitCond 轮询等待条件成立（跨桥上报 → server 侧实例表登记为异步链路）。
func waitCond(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("等待条件超时")
}

// expectNoTyp 断言 d 内本窗口**未**收到该前端 type（定向投递的「非目标窗口」侧）。
func expectNoTyp(t *testing.T, ch <-chan string, typ string, d time.Duration) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case script := <-ch:
			const p = "window.mq.emitRemote("
			var env evalEnvelope
			body := strings.TrimSuffix(strings.TrimPrefix(script, p), ")")
			if err := json.Unmarshal([]byte(body), &env); err != nil {
				t.Fatalf("envelope 解析失败: %v (%s)", err, script)
			}
			if env.Type == typ {
				t.Fatalf("非目标窗口不应收到 %s: %s", typ, env.Payload)
			}
		case <-deadline:
			return
		}
	}
}

// withInstance 复制载荷并注入 `instance_id`（定向投递 = 把**目标**实例 id 填进该字段）。
func withInstance(base map[string]any, instanceID string) map[string]any {
	out := make(map[string]any, len(base)+1)
	for k, v := range base {
		out[k] = v
	}
	out[msgkeys.FieldInstanceId] = instanceID
	return out
}

// TestSubchatTargetedDelivery 单聊跨窗口投递全链（写侧建链 → 定向 → 广播）。
func TestSubchatTargetedDelivery(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	defer func() { _ = bus.Close() }()

	// server 侧实例视图（订阅 instance-register/-heartbeat/-exit，维护 parent_instance 链）。
	im := instance.New(bus)
	if err := im.Start(); err != nil {
		t.Fatalf("im.Start: %v", err)
	}
	defer im.Stop()

	// 父窗口（顶层实例 main）+ 子窗口（直接父 = main），各自桥已 Start。
	_, parentEvals := newInstBridge(t, bus, "main", "")
	_, childEvals := newInstBridge(t, bus, "child", "main")

	// ① 写侧：桥上报的 parent_instance 驱动实例表建链（只取**直接父**）。
	waitCond(t, time.Second, func() bool {
		p, ok := im.ParentOfInstance("child")
		return ok && p == "main"
	})

	state := map[string]any{
		msgkeys.SubchatStateEventSessionId:       "s-sub",
		msgkeys.SubchatStateEventParentSessionId: "s-main",
		msgkeys.SubchatStateEventLifecycle:       "ready",
	}

	// ② 定向投递：查表取目标（父）实例 → 填进 instance_id → 只有父窗口收到。
	target, targeted := im.DeliveryTarget("child", true)
	if !targeted || target != "main" {
		t.Fatalf("DeliveryTarget(child, 投父)=(%q,%v)，want (main,true)", target, targeted)
	}
	pubFire(bus, msgkeys.TopicSubchatState, withInstance(state, target))

	env := recvEnvelopeTyp(t, parentEvals, msgkeys.TopicSubchatState, time.Second)
	var got map[string]any
	if err := json.Unmarshal([]byte(env.Payload), &got); err != nil {
		t.Fatalf("subchat-state payload 解析失败: %v (%s)", err, env.Payload)
	}
	if got[msgkeys.SubchatStateEventLifecycle] != "ready" ||
		got[msgkeys.SubchatStateEventParentSessionId] != "s-main" {
		t.Fatalf("父窗口收到的 subchat-state 载荷不符: %s", env.Payload)
	}
	expectNoTyp(t, childEvals, msgkeys.TopicSubchatState, 200*time.Millisecond)

	// ③ 广播分支：父 + 子都要 → 不带 instance_id → 两个窗口都收到。
	pubFire(bus, msgkeys.TopicSubchatState, state)
	_ = recvEnvelopeTyp(t, parentEvals, msgkeys.TopicSubchatState, time.Second)
	_ = recvEnvelopeTyp(t, childEvals, msgkeys.TopicSubchatState, time.Second)

	// ④ 顶层实例投父：无父可投（targeted=false）→ 调用方按广播投递，仍应送达。
	if _, hit := im.DeliveryTarget("main", true); hit {
		t.Fatal("顶层实例不应有定向父目标")
	}
}
