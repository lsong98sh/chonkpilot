package udsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/chonkpilot/chonkpilot-lib/uds"
)

// fakeRegistry 模拟插件 instance.Manager 的实例视图（由总线 instance-register 驱动）。
type fakeRegistry struct {
	mu   sync.Mutex
	byID map[string]instRecord
	seq  int
}

type instRecord struct {
	workDir string
	dataDir string
	user    string
}

func newFakeRegistry(prefix string) (*fakeRegistry, mq.Bus, error) {
	bus, err := mq.New(mq.Options{Prefix: prefix})
	if err != nil {
		return nil, nil, err
	}
	r := &fakeRegistry{byID: map[string]instRecord{}}
	// 模拟服务端 claim 处理（含 __local 免鉴权取 work_dir 提议）。
	_, _ = bus.On(msgkeys.TopicInstanceClaim, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		_ = v.JSON(&m)
		id, _ := m[msgkeys.FieldInstanceId].(string)
		local, _ := m["__local"].(bool)
		if !local || id == "" {
			v.Errors = append(v.Errors, fmt.Errorf("instance-forbidden"))
			return nil
		}
		wd, _ := m["work_dir"].(string)
		user, _ := m["user"].(string)
		rec := instRecord{workDir: wd, dataDir: wd + "/.chonkpilot", user: user}
		r.mu.Lock()
		r.byID[id] = rec
		r.mu.Unlock()
		v.Result = map[string]any{msgkeys.FieldInstanceId: id, "work_dir": wd, "data_dir": rec.dataDir}
		return nil
	})
	_, _ = bus.On(msgkeys.TopicInstanceExit, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		_ = v.JSON(&m)
		id, _ := m[msgkeys.FieldInstanceId].(string)
		r.mu.Lock()
		delete(r.byID, id)
		r.mu.Unlock()
		return nil
	})
	return r, bus, nil
}

func (r *fakeRegistry) instanceOf(id string) (string, string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.byID[id]
	return rec.workDir, rec.user, ok
}

func (r *fakeRegistry) topByDataDir(dataDir string) (string, string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, rec := range r.byID {
		if rec.dataDir == dataDir {
			return id, rec.user, true
		}
	}
	return "", "", false
}

func startGateway(t *testing.T, r *fakeRegistry, bus mq.Bus) (*Gateway, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.sock")
	g := New(Options{
		Bus:          bus,
		Path:         path,
		InstanceOf:   r.instanceOf,
		TopByDataDir: r.topByDataDir,
	})
	if err := g.Start(); err != nil {
		t.Fatalf("gateway start: %v", err)
	}
	t.Cleanup(g.Stop)
	return g, path
}

func dial(t *testing.T, path string) *uds.Client {
	t.Helper()
	c, err := uds.Dial(uds.ClientOptions{Path: path})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// claimLocal 走前门 claim（`__local` 由前门注入；此处仅传 work_dir/user）。
func claimLocal(t *testing.T, c *uds.Client, id, workDir, user string) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		msgkeys.FieldInstanceId: id,
		"work_dir":              workDir,
		"user":                  user,
	})
	v := c.Emit(context.Background(), msgkeys.TopicInstanceClaim, raw).Wait()
	if e := v.Err(); e != nil {
		t.Fatalf("claim: %v", e)
	}
	m, _ := v.Result.(map[string]any)
	if m == nil {
		t.Fatalf("claim result = %#v", v.Result)
	}
	return m
}

func TestGatewayClaimBindsAndRoutes(t *testing.T) {
	r, bus, err := newFakeRegistry("chonk.")
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	_, path := startGateway(t, r, bus)

	cli := dial(t, path)
	res := claimLocal(t, cli, "A", "E:/proj", "alice")
	if res[msgkeys.FieldInstanceId] != "A" {
		t.Fatalf("claim id = %v", res[msgkeys.FieldInstanceId])
	}

	// 下行事件按 instance_id 定向路由。
	got := make(chan string, 4)
	_, _ = cli.On(">", 0, func(_ context.Context, s string, _ *mq.Value) error { got <- s; return nil })
	if v := bus.Emit(context.Background(), "session-receive", map[string]any{msgkeys.FieldInstanceId: "A"}).Wait(); v.Err() != nil {
		t.Fatalf("emit: %v", v.Err())
	}
	select {
	case s := <-got:
		if s != "session-receive" {
			t.Fatalf("event subject = %q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("event not routed")
	}

	// 无归属事件广播。
	if v := bus.Emit(context.Background(), "server-starting", map[string]any{}).Wait(); v.Err() != nil {
		t.Fatalf("emit broadcast: %v", v.Err())
	}
	select {
	case s := <-got:
		if s != "server-starting" {
			t.Fatalf("broadcast subject = %q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("broadcast event not delivered")
	}
}

func TestGatewayRejectsForeignInstanceAndMissing(t *testing.T) {
	r, bus, _ := newFakeRegistry("chonk.")
	_, path := startGateway(t, r, bus)
	cli := dial(t, path)

	// 未 claim → 普通上行因无实例绑定被拒。
	v := cli.Emit(context.Background(), "session-send", map[string]any{"x": 1}).Wait()
	if v.Err() == nil {
		t.Fatalf("expected rejection before claim")
	}

	// claim 后：自报其它 instance_id 被覆盖（以本连接实例为准）。
	claimLocal(t, cli, "A", "E:/proj", "alice")
	saw := make(chan string, 1)
	_, _ = bus.On("session-send", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		_ = v.JSON(&m)
		id, _ := m[msgkeys.FieldInstanceId].(string)
		select {
		case saw <- id:
		default:
		}
		v.Result = map[string]any{msgkeys.FieldOk: true}
		return nil
	})
	_ = cli.Emit(context.Background(), "session-send", map[string]any{msgkeys.FieldInstanceId: "B"}).Wait()
	select {
	case id := <-saw:
		if id != "A" {
			t.Fatalf("instance_id not overridden: %q", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("server did not receive session-send")
	}
}

func TestGatewayBringToTopSameUser(t *testing.T) {
	r, bus, _ := newFakeRegistry("chonk.")
	_, path := startGateway(t, r, bus)

	ctlCh := make(chan string, 2)
	cliA := dial(t, path)
	cliB, err := uds.Dial(uds.ClientOptions{Path: path, OnControl: func(typ string, _ []byte) { ctlCh <- typ }})
	if err != nil {
		t.Fatalf("dial B: %v", err)
	}
	t.Cleanup(func() { _ = cliB.Close() })

	_ = claimLocal(t, cliA, "A", "E:/proj2", "alice") // 发起方：自己的项目
	_ = claimLocal(t, cliB, "B", "E:/proj", "alice")  // 目标：同 data_dir + 同 user

	raw, _ := json.Marshal(map[string]any{msgkeys.InstanceBringToTopPayloadDataDir: "E:/proj/.chonkpilot"})
	v := cliA.Emit(context.Background(), msgkeys.TopicInstanceBringToTop, raw).Wait()
	if v.Err() != nil {
		t.Fatalf("bring-to-top: %v", v.Err())
	}
	select {
	case typ := <-ctlCh:
		if typ != msgkeys.TopicInstanceGotoTop {
			t.Fatalf("ctl = %q", typ)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("goto-top control not delivered")
	}
}

func TestGatewayBringToTopCrossUserRejected(t *testing.T) {
	r, bus, _ := newFakeRegistry("chonk.")
	_, path := startGateway(t, r, bus)

	cliA := dial(t, path)
	cliB := dial(t, path)
	_ = claimLocal(t, cliA, "A", "E:/proj2", "alice")
	_ = claimLocal(t, cliB, "B", "E:/proj", "bob") // 同 data_dir，不同 user

	raw, _ := json.Marshal(map[string]any{msgkeys.InstanceBringToTopPayloadDataDir: "E:/proj/.chonkpilot"})
	v := cliA.Emit(context.Background(), msgkeys.TopicInstanceBringToTop, raw).Wait()
	if v.Err() == nil {
		t.Fatalf("cross-user bring-to-top should be rejected")
	}
}

func TestGatewayDisconnectPublishesExit(t *testing.T) {
	r, bus, _ := newFakeRegistry("chonk.")
	_, path := startGateway(t, r, bus)

	gone := make(chan string, 1)
	_, _ = bus.On(msgkeys.TopicInstanceExit, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		_ = v.JSON(&m)
		id, _ := m[msgkeys.FieldInstanceId].(string)
		select {
		case gone <- id:
		default:
		}
		return nil
	})

	cli := dial(t, path)
	_ = claimLocal(t, cli, "A", "E:/proj", "alice")
	_ = cli.Close()

	select {
	case id := <-gone:
		if id != "A" {
			t.Fatalf("exit id = %q", id)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("instance-exit not published on disconnect")
	}
}
