package workspace

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
	"github.com/lsong98sh/chonkpilot-plugin-framework/runtime/instance"
)

// eventSink 收集 workdir-opened / workdir-closed。
type eventSink struct {
	mu     sync.Mutex
	opened []map[string]any
	closed []map[string]any
}

func (s *eventSink) handler(dst *[]map[string]any) mq.VHandler {
	return func(_ context.Context, _ string, v *mq.Value) error {
		var p map[string]any
		_ = json.Unmarshal(v.Payload, &p)
		s.mu.Lock()
		*dst = append(*dst, p)
		s.mu.Unlock()
		return nil
	}
}

func (s *eventSink) counts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.opened), len(s.closed)
}

func newTestManager(t *testing.T) (*Manager, mq.Bus, *eventSink) {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	sink := &eventSink{}
	if _, err := bus.On(msgkeys.TopicWorkdirOpened, 0, sink.handler(&sink.opened)); err != nil {
		t.Fatalf("subscribe opened: %v", err)
	}
	if _, err := bus.On(msgkeys.TopicWorkdirClosed, 0, sink.handler(&sink.closed)); err != nil {
		t.Fatalf("subscribe closed: %v", err)
	}
	m := New(bus, Options{Logf: t.Logf})
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(m.Stop)
	return m, bus, sink
}

func emit(t *testing.T, bus mq.Bus, topic string, payload map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(payload)
	bus.Emit(context.Background(), topic, raw).Wait()
	// 事件为异步派发：给订阅回调一点收敛时间（无锁轮询由调用方断言）。
	time.Sleep(20 * time.Millisecond)
}

func TestLayoutOf(t *testing.T) {
	wd := filepath.Join(t.TempDir(), "proj")
	l := LayoutOf(wd, "")
	if l.PrjDir != filepath.Join(wd, ".chonkpilot") {
		t.Fatalf("PrjDir = %q", l.PrjDir)
	}
	wantUsr := filepath.Join(wd, ".chonkpilot", "users", UserDirName())
	if l.PrjUsrDir != wantUsr {
		t.Fatalf("PrjUsrDir = %q, want %q", l.PrjUsrDir, wantUsr)
	}

	// dataDir 非空 → prjusr 与 prj 同根。
	dd := t.TempDir()
	l2 := LayoutOf(wd, dd)
	if l2.PrjUsrDir != dd {
		t.Fatalf("dataDir layout = %q, want %q", l2.PrjUsrDir, dd)
	}

	// 根覆盖。
	root := t.TempDir()
	SetUsrRootOverride(root)
	t.Cleanup(func() { SetUsrRootOverride("") })
	l3 := LayoutOf(wd, "")
	if l3.PrjUsrDir != filepath.Join(root, "users", UserDirName()) {
		t.Fatalf("override layout = %q", l3.PrjUsrDir)
	}
}

func TestRefCountLifecycle(t *testing.T) {
	m, bus, sink := newTestManager(t)
	wd := filepath.Join(t.TempDir(), "ws")

	emit(t, bus, msgkeys.TopicInstanceRegister, map[string]any{
		"instance_id": "i-1", "work_dir": wd, "user": "u",
	})
	if o, c := sink.counts(); o != 1 || c != 0 {
		t.Fatalf("after first register: opened=%d closed=%d", o, c)
	}
	if sp, ok := m.Resolve("i-1"); !ok || sp.WorkDir != filepath.Clean(wd) {
		t.Fatalf("Resolve = %+v ok=%v", sp, ok)
	}

	// 第二个实例：只加计数，不发事件。
	emit(t, bus, msgkeys.TopicInstanceRegister, map[string]any{
		"instance_id": "i-2", "work_dir": wd, "parent_instance": "i-1",
	})
	if o, c := sink.counts(); o != 1 || c != 0 {
		t.Fatalf("after second register: opened=%d closed=%d", o, c)
	}

	// 第一个退出：仍有实例，不发 closed。
	emit(t, bus, msgkeys.TopicInstanceExit, map[string]any{"instance_id": "i-1"})
	if o, c := sink.counts(); o != 1 || c != 0 {
		t.Fatalf("after first exit: opened=%d closed=%d", o, c)
	}
	if _, ok := m.Get(wd); !ok {
		t.Fatal("workspace should stay open")
	}

	// 最后一个退出：发 closed 并清内存。
	emit(t, bus, msgkeys.TopicInstanceExit, map[string]any{"instance_id": "i-2"})
	if o, c := sink.counts(); o != 1 || c != 1 {
		t.Fatalf("after last exit: opened=%d closed=%d", o, c)
	}
	if _, ok := m.Get(wd); ok {
		t.Fatal("workspace should be closed")
	}
}

func TestRegisterIdempotent(t *testing.T) {
	m, bus, sink := newTestManager(t)
	wd := filepath.Join(t.TempDir(), "ws")
	for i := 0; i < 3; i++ {
		emit(t, bus, msgkeys.TopicInstanceRegister, map[string]any{
			"instance_id": "i-1", "work_dir": wd,
		})
	}
	if o, c := sink.counts(); o != 1 || c != 0 {
		t.Fatalf("opened=%d closed=%d (repeat register must not recount)", o, c)
	}
	if ids := m.InstanceIDs(wd); len(ids) != 1 {
		t.Fatalf("InstanceIDs = %v", ids)
	}
}

func TestExitUnknownIgnored(t *testing.T) {
	_, bus, sink := newTestManager(t)
	emit(t, bus, msgkeys.TopicInstanceExit, map[string]any{"instance_id": "nobody"})
	if o, c := sink.counts(); o != 0 || c != 0 {
		t.Fatalf("opened=%d closed=%d", o, c)
	}
}

func TestCheckBoundary(t *testing.T) {
	m, bus, _ := newTestManager(t)
	wd := filepath.Join(t.TempDir(), "ws")
	emit(t, bus, msgkeys.TopicInstanceRegister, map[string]any{
		"instance_id": "i-1", "work_dir": wd,
	})
	sp, ok := m.Resolve("i-1")
	if !ok {
		t.Fatal("workspace not found")
	}

	if p, ok := m.Check(sp, "a/b.txt"); !ok || p != filepath.Join(filepath.Clean(wd), "a", "b.txt") {
		t.Fatalf("relative Check = %q ok=%v", p, ok)
	}
	if _, ok := m.Check(sp, filepath.Join(wd, "..", "evil.txt")); ok {
		t.Fatal("../ escape must be rejected")
	}
	if _, ok := m.Check(sp, filepath.Join(t.TempDir(), "x")); ok {
		t.Fatal("outside absolute path must be rejected")
	}
}

func TestWorkdirEventsPayload(t *testing.T) {
	_, bus, sink := newTestManager(t)
	wd := filepath.Join(t.TempDir(), "ws")
	emit(t, bus, msgkeys.TopicInstanceRegister, map[string]any{
		"instance_id": "i-1", "work_dir": wd, "data_dir": "D:/dd",
	})
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.opened) != 1 {
		t.Fatalf("opened=%d", len(sink.opened))
	}
	p := sink.opened[0]
	if p[msgkeys.FieldWorkDir] != wd || p[msgkeys.FieldInstanceId] != "i-1" || p[msgkeys.FieldDataDir] != "D:/dd" {
		t.Fatalf("payload = %v", p)
	}
	if _, ok := p[msgkeys.FieldClientType]; ok {
		t.Fatalf("payload must not carry client_type: %v", p)
	}
	if !strings.HasPrefix(msgkeys.TopicWorkdirOpened, "workdir-") {
		t.Fatalf("topic = %q", msgkeys.TopicWorkdirOpened)
	}
}

// TestInstancesSingleSource（P5 步骤 1）：PM **不保存实例副本** —— 注入的 `instance.Manager`
// 就是唯一数据源，PM 的 Resolve / InstanceIDs 全部读它（外部直接改动即生效）。
func TestInstancesSingleSource(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	im := instance.New(bus)
	m := New(bus, Options{Logf: t.Logf, Instances: im})
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(m.Stop)
	if m.Instances() != im {
		t.Fatal("PM 必须使用注入的实例视图（单一数据源）")
	}
	wd := filepath.Join(t.TempDir(), "ws")
	emit(t, bus, msgkeys.TopicInstanceRegister, map[string]any{"instance_id": "i-1", "work_dir": wd})
	if info, ok := im.Lookup("i-1"); !ok || info.WorkDir != wd {
		t.Fatalf("共享视图未登记: %+v ok=%v", info, ok)
	}
	if ids := m.InstanceIDs(wd); len(ids) != 1 || ids[0] != "i-1" {
		t.Fatalf("InstanceIDs 必须读共享视图: %v", ids)
	}
	// 外部直接改共享视图（不经 MQ）→ PM 立即跟随（证明无副本）。
	_ = im.HandleExit([]byte(`{"instance_id":"i-1"}`))
	if ids := m.InstanceIDs(wd); len(ids) != 0 {
		t.Fatalf("PM 仍返回已退出实例（存在副本）: %v", ids)
	}
	if _, ok := m.Resolve("i-1"); ok {
		t.Fatal("PM 仍能解析已退出实例（存在副本）")
	}
}

// TestWorkdirLifecycleMatchesInstanceView（L2 / mq 驱动）：`workdir-opened/closed` 与实例视图
// 投影一致（register → opened + 视图有记录；最后一个 exit → closed + 视图清空）。
func TestWorkdirLifecycleMatchesInstanceView(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	sink := &eventSink{}
	if _, err := bus.On(msgkeys.TopicWorkdirOpened, 0, sink.handler(&sink.opened)); err != nil {
		t.Fatalf("subscribe opened: %v", err)
	}
	if _, err := bus.On(msgkeys.TopicWorkdirClosed, 0, sink.handler(&sink.closed)); err != nil {
		t.Fatalf("subscribe closed: %v", err)
	}
	im := instance.New(bus)
	m := New(bus, Options{Logf: t.Logf, Instances: im})
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(m.Stop)
	wd := filepath.Join(t.TempDir(), "ws")

	emit(t, bus, msgkeys.TopicInstanceRegister, map[string]any{"instance_id": "i-1", "work_dir": wd})
	emit(t, bus, msgkeys.TopicInstanceRegister, map[string]any{"instance_id": "i-2", "work_dir": wd})
	if o, c := sink.counts(); o != 1 || c != 0 {
		t.Fatalf("register: opened=%d closed=%d", o, c)
	}
	if got, want := im.Count(), 2; got != want {
		t.Fatalf("实例视图 = %d, want %d", got, want)
	}
	if ids := m.InstanceIDs(wd); len(ids) != 2 {
		t.Fatalf("InstanceIDs = %v", ids)
	}

	emit(t, bus, msgkeys.TopicInstanceExit, map[string]any{"instance_id": "i-1"})
	if o, c := sink.counts(); o != 1 || c != 0 {
		t.Fatalf("first exit: opened=%d closed=%d", o, c)
	}
	if got := im.Count(); got != 1 {
		t.Fatalf("首个退出后实例视图 = %d, want 1", got)
	}

	emit(t, bus, msgkeys.TopicInstanceExit, map[string]any{"instance_id": "i-2"})
	if o, c := sink.counts(); o != 1 || c != 1 {
		t.Fatalf("last exit: opened=%d closed=%d", o, c)
	}
	if got := im.Count(); got != 0 {
		t.Fatalf("最后一个退出后实例视图 = %d, want 0", got)
	}
	if _, ok := m.Get(wd); ok {
		t.Fatal("工作区应已关闭")
	}
	if _, ok := m.Resolve("i-2"); ok {
		t.Fatal("已退出实例不得再解析")
	}
}

// TestRouteWorkDirNarrowing（P5 步骤 3）：PM 的 `Route` 是唯一路由实现 —— workdir 级主题只投
// 同 workdir；instance 级按载荷精确匹配；`data-*` 应答整体丢弃；自发布吞掉自己那条。
func TestRouteWorkDirNarrowing(t *testing.T) {
	r := NewRouter()
	sinks := []Sink{
		{ID: "a1", WorkDir: "E:/ws1"},
		{ID: "a2", WorkDir: "e:\\WS1"}, // 大小写 / 分隔符不同 = 同一 workdir
		{ID: "b1", WorkDir: "E:/ws2"},
	}
	got := r.Route(msgkeys.TopicFilesysChanged, []byte(`{"work_dir":"E:\\ws1"}`), sinks)
	if len(got) != 2 || got[0].Index != 0 || got[1].Index != 1 {
		t.Fatalf("workdir 收紧结果 = %+v", got)
	}

	// workdir 未知 → 保守全投。
	if got := r.Route(msgkeys.TopicFilesysChanged, []byte(`{"path":"x"}`), sinks); len(got) != 3 {
		t.Fatalf("workdir 未知应全投: %+v", got)
	}

	// instance 级：载荷 instance_id 精确匹配（未标识 sink 放行）。
	got = r.Route("mcp-tools-timeout", []byte(`{"instance_id":"b1"}`), sinks)
	if len(got) != 1 || got[0].ID != "b1" {
		t.Fatalf("instance 匹配结果 = %+v", got)
	}

	// data-* 应答整体丢弃。
	if got := r.Route("data-session-get", []byte(`{"req_id":"r1","ok":true,"result":{}}`), sinks); len(got) != 0 {
		t.Fatalf("persist 应答应整体丢弃: %+v", got)
	}

	// 自发布：发布方自己那条不回投。
	payload := []byte(`{"work_dir":"E:/ws1"}`)
	r.Mark("a1", msgkeys.TopicFilesysChanged, payload)
	got = r.Route(msgkeys.TopicFilesysChanged, payload, sinks)
	if len(got) != 1 || got[0].ID != "a2" {
		t.Fatalf("自发布应被吞: %+v", got)
	}

	// 插件 sink 的实例归属取 Instance（窗口取 ID）。
	pluginSinks := []Sink{{ID: "p1", Instance: "i-9", Plugin: true}}
	got = r.Route("mcp-tools-timeout", []byte(`{"instance_id":"i-9"}`), pluginSinks)
	if len(got) != 1 || got[0].ID != "p1" {
		t.Fatalf("插件 sink 匹配结果 = %+v", got)
	}
	if got := r.Route("mcp-tools-timeout", []byte(`{"instance_id":"i-8"}`), pluginSinks); len(got) != 0 {
		t.Fatalf("插件 sink 实例不匹配应丢弃: %+v", got)
	}
}
