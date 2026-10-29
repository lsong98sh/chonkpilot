package mqpolicy

import (
	"context"
	"sync"
	"testing"

	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
	"github.com/lsong98sh/chonkpilot-plugin-framework/runtime/dispatch"
)

// 本文件是 S3（49 §2.22.7「插件 sink 接入统一分发器」）的策略级证据：
//   - 插件 sink 与窗口 sink **同策略**：workdir 级主题只投同 workdir、instance 级只投该实例、
//     server 级全投；sink 未声明 workdir / instance → 放行（只缩小、不放大）；
//   - 走**真实分发器 + 真实总线**：一条事件只投一次、注销后不再投。

// sinkRec 记录各 sink 收到的事件（sink id → "topic|payload"）。
type sinkRec struct {
	mu  sync.Mutex
	got map[string][]string
}

func newSinkRec() *sinkRec { return &sinkRec{got: map[string][]string{}} }

func (r *sinkRec) send(id string) dispatch.SendFunc {
	return func(topic string, payload []byte) {
		r.mu.Lock()
		r.got[id] = append(r.got[id], topic+"|"+string(payload))
		r.mu.Unlock()
	}
}

func (r *sinkRec) of(id string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.got[id]...)
}

// newDispatchHub 建「总线 + 分发器（WorkDirPolicy）」并 Start（Cleanup 收尾）。
func newDispatchHub(t *testing.T) (mq.Bus, *dispatch.Dispatcher) {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	d := dispatch.New(dispatch.Options{Bus: bus, Policy: New()})
	if err := d.Start(); err != nil {
		t.Fatalf("dispatch start: %v", err)
	}
	t.Cleanup(func() {
		_ = d.Stop()
		_ = bus.Close()
	})
	return bus, d
}

// publish 发一条相对主题事件并等投递完成。
func publish(bus mq.Bus, topic, payload string) {
	bus.Emit(context.Background(), topic, []byte(payload)).Wait()
}

// TestPluginSinkWorkDirIsolation：插件 sink（workdir=A）收 A 的 `filesys.changed`，收不到 B 的；
// 未声明 workdir 的插件 sink 放行（保守，不静默丢事件）。
func TestPluginSinkWorkDirIsolation(t *testing.T) {
	bus, d := newDispatchHub(t)
	r := newSinkRec()
	d.RegisterSink(dispatch.Sink{ID: "wA", WorkDir: `E:\ws1`, Kind: dispatch.SinkWindow}, r.send("wA"))
	d.RegisterSink(dispatch.Sink{ID: "wB", WorkDir: `E:\ws2`, Kind: dispatch.SinkWindow}, r.send("wB"))
	d.RegisterSink(dispatch.Sink{ID: "plugin:a:1", WorkDir: `E:\ws1`, Kind: dispatch.SinkPlugin}, r.send("pA"))
	d.RegisterSink(dispatch.Sink{ID: "plugin:b:1", WorkDir: `e:/ws2`, Kind: dispatch.SinkPlugin}, r.send("pB"))
	d.RegisterSink(dispatch.Sink{ID: "plugin:c:1", Kind: dispatch.SinkPlugin}, r.send("pNone"))

	publish(bus, msgkeys.TopicFilesysChanged, `{"work_dir":"E:\\ws1","path":"E:/ws1/a.txt"}`)

	if got := r.of("pA"); len(got) != 1 {
		t.Fatalf("插件 sink（workdir=ws1）应收到 ws1 的事件，实际 %v", got)
	}
	if got := r.of("pB"); len(got) != 0 {
		t.Fatalf("插件 sink（workdir=ws2）不得收到 ws1 的事件，实际 %v", got)
	}
	if got := r.of("pNone"); len(got) != 1 {
		t.Fatalf("未声明 workdir 的插件 sink 应放行，实际 %v", got)
	}
	if got := r.of("wB"); len(got) != 0 {
		t.Fatalf("窗口 sink（workdir=ws2）不得收到 ws1 的事件，实际 %v", got)
	}
}

// TestPluginSinkWorkDirUnknownConservative：workdir 级主题但事件 workdir 未知 → 保守全投（含插件）。
func TestPluginSinkWorkDirUnknownConservative(t *testing.T) {
	bus, d := newDispatchHub(t)
	r := newSinkRec()
	d.RegisterSink(dispatch.Sink{ID: "wA", WorkDir: `E:\ws1`, Kind: dispatch.SinkWindow}, r.send("wA"))
	d.RegisterSink(dispatch.Sink{ID: "pA", WorkDir: `E:\ws1`, Kind: dispatch.SinkPlugin}, r.send("pA"))
	d.RegisterSink(dispatch.Sink{ID: "pB", WorkDir: `E:\ws2`, Kind: dispatch.SinkPlugin}, r.send("pB"))

	publish(bus, msgkeys.TopicFilesysChanged, `{"path":"E:/nowhere/a.txt"}`)

	for _, id := range []string{"wA", "pA", "pB"} {
		if got := r.of(id); len(got) != 1 {
			t.Fatalf("事件 workdir 未知时应保守全投，%s 实际 %v", id, got)
		}
	}
}

// TestPluginSinkInstanceLevel：`instance_id` 级事件——声明了匹配 instance 的插件收，其它插件不收；
// 未声明 instance 的插件放行（与窗口「未标识放行」同口径）。
func TestPluginSinkInstanceLevel(t *testing.T) {
	bus, d := newDispatchHub(t)
	r := newSinkRec()
	d.RegisterSink(dispatch.Sink{ID: "ins-1", Kind: dispatch.SinkWindow}, r.send("w1"))
	d.RegisterSink(dispatch.Sink{ID: "ins-2", Kind: dispatch.SinkWindow}, r.send("w2"))
	d.RegisterSink(dispatch.Sink{ID: "p1", Kind: dispatch.SinkPlugin, Instance: "ins-1"}, r.send("p1"))
	d.RegisterSink(dispatch.Sink{ID: "p2", Kind: dispatch.SinkPlugin, Instance: "ins-2"}, r.send("p2"))
	d.RegisterSink(dispatch.Sink{ID: "pNone", Kind: dispatch.SinkPlugin}, r.send("pNone"))

	publish(bus, "mcp-tools-timeout", `{"instance_id":"ins-1","request_id":"r1"}`)

	for _, id := range []string{"w1", "p1", "pNone"} {
		if got := r.of(id); len(got) != 1 {
			t.Fatalf("instance 级事件应投 %s，实际 %v", id, got)
		}
	}
	for _, id := range []string{"w2", "p2"} {
		if got := r.of(id); len(got) != 0 {
			t.Fatalf("instance 级事件不应投 %s，实际 %v", id, got)
		}
	}
}

// TestPluginSinkServerTopicAll：server 级（显式列白 / 无归属）主题全投（窗口 + 插件）。
func TestPluginSinkServerTopicAll(t *testing.T) {
	bus, d := newDispatchHub(t)
	r := newSinkRec()
	d.RegisterSink(dispatch.Sink{ID: "wA", WorkDir: `E:\ws1`, Kind: dispatch.SinkWindow}, r.send("wA"))
	d.RegisterSink(dispatch.Sink{ID: "pA", WorkDir: `E:\ws1`, Kind: dispatch.SinkPlugin}, r.send("pA"))
	d.RegisterSink(dispatch.Sink{ID: "pB", WorkDir: `E:\ws2`, Kind: dispatch.SinkPlugin}, r.send("pB"))

	publish(bus, msgkeys.TopicDataUserConfigChanged, `{"data":{"theme":"dark"}}`)

	for _, id := range []string{"wA", "pA", "pB"} {
		if got := r.of(id); len(got) != 1 {
			t.Fatalf("server 级主题应全投，%s 实际 %v", id, got)
		}
	}
}

// TestPluginSinkDataReplyDropped：`data-*` persist 同主题应答对插件同样整体丢弃。
func TestPluginSinkDataReplyDropped(t *testing.T) {
	bus, d := newDispatchHub(t)
	r := newSinkRec()
	d.RegisterSink(dispatch.Sink{ID: "pA", WorkDir: `E:\ws1`, Kind: dispatch.SinkPlugin}, r.send("pA"))

	publish(bus, "data-session-get", `{"req_id":"r1","ok":true,"result":{}}`)

	if got := r.of("pA"); len(got) != 0 {
		t.Fatalf("persist 应答不得投给插件 sink，实际 %v", got)
	}
}

// TestPluginSinkDeliveredOnceAndUnregister：一条事件只投 1 次；注销后不再投（幂等）。
func TestPluginSinkDeliveredOnceAndUnregister(t *testing.T) {
	bus, d := newDispatchHub(t)
	r := newSinkRec()
	d.RegisterSink(dispatch.Sink{ID: "pA", WorkDir: `E:\ws1`, Kind: dispatch.SinkPlugin}, r.send("pA"))

	publish(bus, msgkeys.TopicFilesysChanged, `{"work_dir":"E:\\ws1"}`)
	if got := r.of("pA"); len(got) != 1 {
		t.Fatalf("应恰好投递 1 次（不重复），实际 %v", got)
	}

	d.UnregisterSink("pA")
	d.UnregisterSink("pA") // 幂等
	publish(bus, msgkeys.TopicFilesysChanged, `{"work_dir":"E:\\ws1"}`)
	if got := r.of("pA"); len(got) != 1 {
		t.Fatalf("注销后不得再投，实际 %v", got)
	}
	if n := len(d.Sinks()); n != 0 {
		t.Fatalf("注销后注册表应为空，实际 %d", n)
	}
}

// TestPluginSinkCoexistsWithWindowRegression：窗口 sink 的 S1/S2 判定不因插件 sink 出现而变化
// （同一批判定里两类 sink 各按自身字段判定，互不影响）。
func TestPluginSinkCoexistsWithWindowRegression(t *testing.T) {
	p := New()
	sinks := []dispatch.Sink{
		{ID: "wA", WorkDir: `E:\ws1`, Kind: dispatch.SinkWindow},
		{ID: "plugin:p:1", WorkDir: `E:\ws1`, Kind: dispatch.SinkPlugin},
	}
	// 自发布账本按 sink ID 记：窗口自发布 → 只吞窗口那条，插件照收。
	payload := []byte(`{"work_dir":"E:\\ws1","path":"E:/ws1/a.txt"}`)
	p.Mark("wA", msgkeys.TopicFilesysChanged, payload)
	evt := dispatch.Event{Topic: msgkeys.TopicFilesysChanged, Payload: payload}
	got := sinkIDs(p.Deliver(evt, sinks))
	if !eqIDs(got, "plugin:p:1") {
		t.Fatalf("窗口自发布只吞窗口那条，实际 %v", got)
	}

	// instance 级：窗口 ID 与插件 Instance 各自判定（插件未声明 instance → 放行）。
	evt2 := dispatch.Event{Topic: "task-started", Payload: []byte(`{"instance_id":"wA"}`)}
	if got := sinkIDs(p.Deliver(evt2, sinks)); !eqIDs(got, "wA", "plugin:p:1") {
		t.Fatalf("窗口命中 + 插件未声明应放行，实际 %v", got)
	}
}

// TestPluginSinkEventWorkDirFromInstance：workdir 级主题按 `instance_id` 反查 workdir →
// 插件同样被收紧（与窗口一致）。
func TestPluginSinkEventWorkDirFromInstance(t *testing.T) {
	p := New()
	p.SetInstanceWorkDir(func(id string) (string, bool) {
		if id == "ins-ws2" {
			return `E:\ws2`, true
		}
		return "", false
	})
	sinks := []dispatch.Sink{
		{ID: "wA", WorkDir: `E:\ws1`, Kind: dispatch.SinkWindow},
		{ID: "pA", WorkDir: `E:\ws1`, Kind: dispatch.SinkPlugin},
		{ID: "pB", WorkDir: `E:\ws2`, Kind: dispatch.SinkPlugin},
	}
	evt := dispatch.Event{Topic: msgkeys.TopicFilesysChanged, Payload: []byte(`{"instance_id":"ins-ws2"}`)}
	// instance 级优先只选「声明了该 instance 的 sink」；本批 sink 都未声明 instance → 放行到
	// workdir 判定；反查得 ws2 → 只剩 pB。
	if got := sinkIDs(p.Deliver(evt, sinks)); !eqIDs(got, "pB") {
		t.Fatalf("应按反查 workdir 收紧到 pB，实际 %v", got)
	}
}
