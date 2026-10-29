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
