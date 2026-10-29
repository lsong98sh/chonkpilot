package mqpolicy

import (
	"sort"
	"testing"

	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
	"github.com/lsong98sh/chonkpilot-plugin-framework/runtime/dispatch"
)

// 本文件是 S2（49 §2.22.7「workdir 级主题只投同 workdir」）的策略级证据：
// 两窗口不同 workdir → 只到同 workdir；同 workdir 双窗 → 都收到；workdir 未知 → 保守全投；
// S1 三条（自发布 / data-* 应答 / instance 归属）**回归不变**。

// sinkIDs 把 Deliver 结果压成 id 列表（稳定序，便于逐项断言）。
func sinkIDs(in []dispatch.Sink) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, s.ID)
	}
	sort.Strings(out)
	return out
}

func eqIDs(a []string, b ...string) bool {
	if len(a) != len(b) {
		return false
	}
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// twoWorkDirSinks 构造「wd1 两窗 + wd2 一窗」的标准 sink 集。
func twoWorkDirSinks() []dispatch.Sink {
	return []dispatch.Sink{
		{ID: "a1", WorkDir: "E:/ws1", Kind: dispatch.SinkWindow},
		{ID: "a2", WorkDir: "e:\\WS1", Kind: dispatch.SinkWindow}, // 大小写 / 分隔符不同 = 同一 workdir
		{ID: "b1", WorkDir: "E:/ws2", Kind: dispatch.SinkWindow},
	}
}

// TestWorkDirTopicNarrowedToSameWorkDir：workdir 级主题 + 载荷带 work_dir → 只投同 workdir 的窗口。
func TestWorkDirTopicNarrowedToSameWorkDir(t *testing.T) {
	p := New()
	for _, topic := range []string{msgkeys.TopicFilesysChanged, msgkeys.TopicFilesysWatchError} {
		evt := dispatch.Event{Topic: topic, Payload: []byte(`{"work_dir":"E:\\ws1","path":"E:/ws1/a.txt"}`)}
		if got := sinkIDs(p.Deliver(evt, twoWorkDirSinks())); !eqIDs(got, "a1", "a2") {
			t.Fatalf("%s: 应只投 wd1 的两个窗口，实际 %v", topic, got)
		}
	}
}

// TestWorkDirTopicReachesBothWindowsInSameWorkDir：同 workdir 双窗都收到（S2 验收口径）。
func TestWorkDirTopicReachesBothWindowsInSameWorkDir(t *testing.T) {
	p := New()
	sinks := twoWorkDirSinks()
	evt := dispatch.Event{Topic: msgkeys.TopicDataPrjConfigRefresh,
		Payload: []byte(`{"work_dir":"E:/ws1","id":"k","op":"save"}`)}
	got := sinkIDs(p.Deliver(evt, sinks))
	if !eqIDs(got, "a1", "a2") {
		t.Fatalf("同 workdir 双窗应都收到，实际 %v", got)
	}
}

// TestWorkDirUnknownConservativeBroadcast：workdir 级主题但 workdir 未知（无 work_dir、无 instance、
// 无反查）→ 保守全投（宁可少收紧，不可漏投）。
func TestWorkDirUnknownConservativeBroadcast(t *testing.T) {
	p := New()
	evt := dispatch.Event{Topic: msgkeys.TopicFilesysChanged, Payload: []byte(`{"path":"E:/ws1/a.txt"}`)}
	if got := sinkIDs(p.Deliver(evt, twoWorkDirSinks())); !eqIDs(got, "a1", "a2", "b1") {
		t.Fatalf("workdir 未知应保守全投，实际 %v", got)
	}
	// 反查注入了但未命中 → 同样视为未知。
	p.SetInstanceWorkDir(func(string) (string, bool) { return "", false })
	if got := sinkIDs(p.Deliver(evt, twoWorkDirSinks())); !eqIDs(got, "a1", "a2", "b1") {
		t.Fatalf("反查未命中应保守全投，实际 %v", got)
	}
}

// TestWorkDirFromInstanceReverseLookup：载荷无 work_dir → 按 instance_id 反查 workdir
// （视图 = framework instance.Manager 的注入），据此收紧。
func TestWorkDirFromInstanceReverseLookup(t *testing.T) {
	p := New()
	resolved := map[string]string{"ins-a1": "E:/ws1", "ins-b1": "E:/ws2"}
	p.SetInstanceWorkDir(func(id string) (string, bool) {
		wd, ok := resolved[id]
		return wd, ok
	})
	// 事件归属 ins-b1（wd2）：instance 规则先只选 b1；workdir 判定与之一致 → 投 b1。
	evt := dispatch.Event{Topic: msgkeys.TopicFilesysChanged, Payload: []byte(`{"instance_id":"ins-b1","path":"E:/ws2/x"}`)}
	sinks := []dispatch.Sink{
		{ID: "ins-a1", WorkDir: "E:/ws1", Kind: dispatch.SinkWindow},
		{ID: "ins-b1", WorkDir: "E:/ws2", Kind: dispatch.SinkWindow},
	}
	if got := sinkIDs(p.Deliver(evt, sinks)); !eqIDs(got, "ins-b1") {
		t.Fatalf("应只投 ins-b1，实际 %v", got)
	}
	// 反查说该实例在 ws2，但事件自带 work_dir=ws1（错配）→ 不投（workdir 级主题按 workdir 收口）。
	bad := dispatch.Event{Topic: msgkeys.TopicFilesysChanged,
		Payload: []byte(`{"instance_id":"ins-b1","work_dir":"E:/ws1","path":"E:/ws1/x"}`)}
	if got := sinkIDs(p.Deliver(bad, sinks)); len(got) != 0 {
		t.Fatalf("work_dir 与目标窗口 workdir 错配时不应投递，实际 %v", got)
	}
}

// TestWorkDirSinkUnknownPasses：sink 自身 workdir 未知（空串，如未标识窗口）→ 不按 workdir 丢。
func TestWorkDirSinkUnknownPasses(t *testing.T) {
	p := New()
	sinks := []dispatch.Sink{
		{ID: "", WorkDir: "", Kind: dispatch.SinkWindow},
		{ID: "a1", WorkDir: "E:/ws1", Kind: dispatch.SinkWindow},
		{ID: "b1", WorkDir: "E:/ws2", Kind: dispatch.SinkWindow},
	}
	evt := dispatch.Event{Topic: msgkeys.TopicFilesysChanged, Payload: []byte(`{"work_dir":"E:/ws1"}`)}
	got := sinkIDs(p.Deliver(evt, sinks))
	if !eqIDs(got, "", "a1") {
		t.Fatalf("sink workdir 未知应放行，实际 %v", got)
	}
}

// TestWorkDirTopicTable：单个 workdir 主题逐条覆盖（表驱动，防止清单漏收紧 / 误收紧）。
func TestWorkDirTopicTable(t *testing.T) {
	p := New()
	for topic := range workDirTopics {
		t.Run(topic, func(t *testing.T) {
			evt := dispatch.Event{Topic: topic, Payload: []byte(`{"work_dir":"E:/ws1"}`)}
			if got := sinkIDs(p.Deliver(evt, twoWorkDirSinks())); !eqIDs(got, "a1", "a2") {
				t.Fatalf("%s 应收紧到 wd1，实际 %v", topic, got)
			}
		})
	}
}

// TestServerTopicWhitelistNotNarrowed：显式列白的 server 级主题**不得**被收紧（跨窗语义：
// 主题/语言同步、会话标题跟随）。
func TestServerTopicWhitelistNotNarrowed(t *testing.T) {
	p := New()
	for topic := range serverTopics {
		if isWorkDirTopic(topic) {
			t.Fatalf("%s 在 server 白名单内，不得判为 workdir 级", topic)
		}
		if _, dup := workDirTopics[topic]; dup {
			t.Fatalf("%s 同时出现在 workDirTopics 与 serverTopics（白名单应优先，但清单须自洽）", topic)
		}
		evt := dispatch.Event{Topic: topic, Payload: []byte(`{"data":{"theme":"dark"}}`)}
		if got := sinkIDs(p.Deliver(evt, twoWorkDirSinks())); !eqIDs(got, "a1", "a2", "b1") {
			t.Fatalf("%s 应全投（服务端级广播），实际 %v", topic, got)
		}
	}
}

// TestNonWorkDirTopicNotNarrowed：非 workdir 级主题（即便载荷带 work_dir）不做收紧。
func TestNonWorkDirTopicNotNarrowed(t *testing.T) {
	p := New()
	for _, topic := range []string{"task-started", "llm-receive", "plugin-ui-event"} {
		evt := dispatch.Event{Topic: topic, Payload: []byte(`{"work_dir":"E:/ws1"}`)}
		if got := sinkIDs(p.Deliver(evt, twoWorkDirSinks())); !eqIDs(got, "a1", "a2", "b1") {
			t.Fatalf("%s 非 workdir 级，应全投，实际 %v", topic, got)
		}
	}
}

// TestS1RulesUnchanged：S1 三条回归（instance 精确 / 自发布 / data-* 应答），在 S2 后不变。
func TestS1RulesUnchanged(t *testing.T) {
	sinks := twoWorkDirSinks()

	// instance 归属：不匹配 → 空；匹配 → 只投该实例。
	p := New()
	mismatch := dispatch.Event{Topic: "mcp-tools-timeout", Payload: []byte(`{"instance_id":"zzz"}`)}
	if got := sinkIDs(p.Deliver(mismatch, sinks)); len(got) != 0 {
		t.Fatalf("instance 不匹配应全丢，实际 %v", got)
	}
	match := dispatch.Event{Topic: "mcp-tools-timeout", Payload: []byte(`{"instance_id":"a1"}`)}
	if got := sinkIDs(p.Deliver(match, sinks)); !eqIDs(got, "a1") {
		t.Fatalf("instance 匹配应只投该窗，实际 %v", got)
	}

	// 自发布：发布方自己那条不回投。
	p2 := New()
	payload := []byte(`{"work_dir":"E:/ws1","path":"E:/ws1/a.txt"}`)
	p2.Mark("a1", msgkeys.TopicFilesysChanged, payload)
	evt := dispatch.Event{Topic: msgkeys.TopicFilesysChanged, Payload: payload}
	if got := sinkIDs(p2.Deliver(evt, sinks)); !eqIDs(got, "a2") {
		t.Fatalf("自发布应被吞（只剩同 workdir 的 a2），实际 %v", got)
	}

	// data-* 应答：整条丢弃。
	p3 := New()
	reply := dispatch.Event{Topic: "data-session-get",
		Payload: []byte(`{"req_id":"r1","ok":true,"result":{}}`)}
	if got := p3.Deliver(reply, sinks); len(got) != 0 {
		t.Fatalf("persist 应答应整体丢弃，实际 %v", got)
	}
}
