// session_ask_delivery_test.go — `ask_user` 定向投递（session→instance 反查）（mq 驱动 + 监听断言；
// SUBCHAT-3 阶段 F-2，2026-10-16）。
//
// 依据：群聊 §13 D42（`ask_user` 形态不改、**只按 instance 投递**到该会话所在窗口）· §15.4 N23 /
// 41 D-62（补 session→instance 反查）。链路：
//
//	① 写侧：对话/群聊窗口建窗时随 `instance-register.session_id` 上报其承载会话；
//	② 反查：`instance.Manager.InstanceForSession(sessionID)` → 该会话所在窗口实例 id；
//	③ 定向：把反查到的实例 id 填进 `session-ask.instance_id` → 桥按精确相等过滤，天然只投该窗口；
//	④ 广播：「父 + 子都要」= 不带 `instance_id` → 桥的「唯一例外」放行 → 投给所有窗口。
//
// 断言层 = 相对主题事件（总线）→ 各窗口桥 forwardEvent → 本窗口前端 eval（定向 = 目标窗口收到
// **且** 非目标窗口收不到；广播 = 两窗口都收到）。
package bridge_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-gui/bridge"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/chonkpilot/chonkpilot-plugin/instance"
)

// newSessionBridge 建「指定实例 id + 承载会话」的桥并 Start（等价窗口工厂 SetSessionID）。
func newSessionBridge(t *testing.T, bus mq.Bus, id, sessionID string) (*bridge.Bridge, chan string) {
	t.Helper()
	evals := make(chan string, 64)
	br := bridge.New(id, "wd", "", evalCollect(evals), bus)
	br.SetSessionID(sessionID)
	if err := br.Start(); err != nil {
		t.Fatalf("Start(%s): %v", id, err)
	}
	return br, evals
}

// TestAskTargetedDeliveryBySessionReverseLookup（F2-1 · 群聊 §13 D42）：会话→实例反查 →
// `session-ask` 定向投到**该会话所在窗口**（群聊窗口收到、其他窗口收不到）；广播分支两窗口都收到。
func TestAskTargetedDeliveryBySessionReverseLookup(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	defer func() { _ = bus.Close() }()

	im := instance.New(bus)
	if err := im.Start(); err != nil {
		t.Fatalf("im.Start: %v", err)
	}
	defer im.Stop()

	// 主窗口（不承载会话）+ 群聊窗口（承载群聊会话 g-1）。
	_, mainEvals := newInstBridge(t, bus, "ins-main", "")
	_, groupEvals := newSessionBridge(t, bus, "ins-group", "g-1")

	waitCond(t, time.Second, func() bool {
		ins, ok := im.InstanceForSession("g-1")
		return ok && ins == "ins-group"
	})

	ask := map[string]any{
		"ask_id": "ask-1", "question": "确认继续？", "session": "work-1", "turn": "t-1",
		"expires_at": "2026-01-01T00:00:00Z",
	}

	// ① 定向投递：反查目标 = 群聊窗口实例 → 只有群聊窗口收到。
	target, ok := im.InstanceForSession("g-1")
	if !ok || target != "ins-group" {
		t.Fatalf("InstanceForSession(g-1)=(%q,%v)，want (ins-group,true)", target, ok)
	}
	pubFire(bus, msgkeys.TopicSessionAsk, withInstance(ask, target))

	env := recvEnvelopeTyp(t, groupEvals, "ask-user", time.Second)
	var got map[string]any
	if err := json.Unmarshal([]byte(env.Payload), &got); err != nil {
		t.Fatalf("session-ask payload 解析失败: %v (%s)", err, env.Payload)
	}
	if got[msgkeys.SessionAskEventInstanceId] != "ins-group" {
		t.Fatalf("群聊窗口收到的 session-ask.instance_id=%v，want ins-group", got[msgkeys.SessionAskEventInstanceId])
	}
	expectNoTyp(t, mainEvals, "ask-user", 200*time.Millisecond)

	// ② 广播分支（「父 + 子都要」= 不带 instance_id）→ 两窗口都收到。
	pubFire(bus, msgkeys.TopicSessionAsk, ask)
	_ = recvEnvelopeTyp(t, groupEvals, "ask-user", time.Second)
	_ = recvEnvelopeTyp(t, mainEvals, "ask-user", time.Second)
}
