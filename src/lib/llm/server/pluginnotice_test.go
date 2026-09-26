// 插件失败的用户可见提示（pluginnotice.go，2026-09-20）白盒：
// ① 首次失败 → 经既有通知面 tool-notify 投一条 notice=plugin-failure（字段齐备、措辞含原因）；
// ② 同一 (实例, 会话, 轮次, 插件, 类别) 重复上报 → **只提示一次**（同轮不去重刷屏）；
// ③ 轮次 / 类别变化 → 重新提示；④ 实例字段缺失 → 不广播（61 §0 实例字段必带）；
// ⑤ 装配链路：插件的 Deps.Notify 确实接在宿主提示上（pluginEnv 真实 Start 路径）。
package server

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-plugin"
)

// noticeCollector 收集 tool-notify 事件载荷（总线同步派发 → 调用返回后已收敛）。
type noticeCollector struct {
	mu     sync.Mutex
	events []map[string]any
}

func (c *noticeCollector) snapshot() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]map[string]any(nil), c.events...)
}

func collectNotices(t *testing.T, bus mq.Bus) *noticeCollector {
	t.Helper()
	c := &noticeCollector{}
	sub, err := bus.On("tool-notify", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) == nil {
			c.mu.Lock()
			c.events = append(c.events, m)
			c.mu.Unlock()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe tool-notify: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	return c
}

func newTestBusForNotice(t *testing.T) mq.Bus {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	return bus
}

// TestPluginNotifyOncePerTurn：同轮同类只提示一次；轮次/类别变化重新提示；字段与措辞齐备。
func TestPluginNotifyOncePerTurn(t *testing.T) {
	bus := newTestBusForNotice(t)
	col := collectNotices(t, bus)
	s := &Server{bus: bus, noticeSeen: newNoticeDedup()}

	n := plugin.Notice{
		Plugin: "memory", Kind: "save", InstanceID: "ins-1", Session: "s1", Turn: "t1",
		Reason: "开发规范：data-memory-save 应答超时",
	}
	s.pluginNotify(n)
	s.pluginNotify(n) // 同轮同类重复上报 → 去重
	if got := len(col.snapshot()); got != 1 {
		t.Fatalf("同轮同类失败应只提示一次，实际 %d 次", got)
	}
	ev := col.snapshot()[0]
	for k, want := range map[string]string{
		"notice":      "plugin-failure",
		"plugin":      "memory",
		"kind":        "save",
		"instance_id": "ins-1",
		"session_id":  "s1",
		"turn_id":     "t1",
		"reason":      "开发规范：data-memory-save 应答超时",
	} {
		if got, _ := ev[k].(string); got != want {
			t.Errorf("载荷字段 %s = %q，want %q（载荷=%v）", k, got, want, ev)
		}
	}
	// 用户可读措辞：插件短语 + 失败原因 + 非阻塞提示（详见日志文件）
	msg, _ := ev["message"].(string)
	if !strings.Contains(msg, "记忆沉淀失败") || !strings.Contains(msg, "应答超时") || !strings.Contains(msg, "详情见日志文件") {
		t.Fatalf("提示措辞不可理解/未含原因：%q", msg)
	}
	if mid, _ := ev["message_id"].(string); mid == "" {
		t.Fatal("提示缺 message_id（前端按 id 幂等去重）")
	}

	// 轮次变化 → 重新提示
	n.Turn = "t2"
	s.pluginNotify(n)
	// 类别变化 → 重新提示
	n.Kind = "llm"
	s.pluginNotify(n)
	if got := len(col.snapshot()); got != 3 {
		t.Fatalf("轮次/类别变化应各自再提示，实际 %d 次", got)
	}
}

// TestPluginNotifySkipsWithoutInstance：实例字段缺失（61 §0 必带）→ 不广播。
func TestPluginNotifySkipsWithoutInstance(t *testing.T) {
	bus := newTestBusForNotice(t)
	col := collectNotices(t, bus)
	s := &Server{bus: bus, noticeSeen: newNoticeDedup()}
	s.pluginNotify(plugin.Notice{Plugin: "memory", Kind: "save", Reason: "x"})
	if got := len(col.snapshot()); got != 0 {
		t.Fatalf("缺 instance_id 不应广播，实际 %d 次", got)
	}
}

// TestNoticeDedupEvictsOldest：判重表有界（超出上限按插入序淘汰最旧键）。
func TestNoticeDedupEvictsOldest(t *testing.T) {
	d := newNoticeDedup()
	const first = "k-0"
	if !d.mark(first) {
		t.Fatal("首次应返回 true（应提示）")
	}
	if d.mark(first) {
		t.Fatal("重复应返回 false（去重）")
	}
	for i := 1; i <= pluginNoticeDedupCap; i++ {
		d.mark("k-" + strconv.Itoa(i))
	}
	if !d.mark(first) {
		t.Fatalf("最旧键应已被淘汰（表上限 %d）", pluginNoticeDedupCap)
	}
}

// TestPluginDepsNotifyWiredToHost：装配链路——server.Start 注入的 Deps.Notify 即宿主提示入口
// （stub 插件在真实 Start 路径上取回 Deps 并调用 → tool-notify(notice=plugin-failure) 到达）。
func TestPluginDepsNotifyWiredToHost(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	stub := &depsProbe{}
	var col *noticeCollector
	pluginEnv(t, llm, []plugin.Hook{stub}, func(bus mq.Bus) { col = collectNotices(t, bus) })

	if stub.deps.Notify == nil {
		t.Fatal("插件 Deps.Notify 未接线（宿主应注入 pluginNotify）")
	}
	if stub.deps.Logf == nil {
		t.Fatal("插件 Deps.Logf 未接线")
	}
	stub.deps.Notify(plugin.Notice{
		Plugin: "compress", Kind: "llm", InstanceID: "ins-1", Session: "s1", Turn: "t1", Reason: "boom",
	})
	evs := col.snapshot()
	if len(evs) != 1 {
		t.Fatalf("宿主提示应经 tool-notify 投递一次，实际 %d", len(evs))
	}
	if got, _ := evs[0]["notice"].(string); got != "plugin-failure" {
		t.Fatalf("notice = %q，want plugin-failure（载荷=%v）", got, evs[0])
	}
}

// depsProbe 是仅用于取回 Deps 的 stub 插件（不订阅任何主题）。
type depsProbe struct{ deps plugin.Deps }

func (p *depsProbe) Name() string { return "deps-probe" }

func (p *depsProbe) Start(d plugin.Deps) error { p.deps = d; return nil }
