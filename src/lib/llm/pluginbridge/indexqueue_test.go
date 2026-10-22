package pluginbridge

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/transport"
)

type fakeIndexQueue struct {
	mu sync.Mutex

	items   []IndexQueueItem
	takeErr error
	ackN    int
	ackErr  error
	pending int
	pendErr error

	lastWorkDir  string
	lastConsumer string
	lastLimit    int
	lastIDs      []string
	lastFailed   bool
	lastErrMsg   string
}

func (f *fakeIndexQueue) Take(workDir, consumer string, limit int) ([]IndexQueueItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastWorkDir = workDir
	f.lastConsumer = consumer
	f.lastLimit = limit
	if f.takeErr != nil {
		return nil, f.takeErr
	}
	return f.items, nil
}

func (f *fakeIndexQueue) Ack(workDir, consumer string, ids []string, failed bool, errMsg string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastWorkDir = workDir
	f.lastConsumer = consumer
	f.lastIDs = ids
	f.lastFailed = failed
	f.lastErrMsg = errMsg
	if f.ackErr != nil {
		return 0, f.ackErr
	}
	return f.ackN, nil
}

func (f *fakeIndexQueue) Pending(workDir, consumer string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastWorkDir = workDir
	f.lastConsumer = consumer
	if f.pendErr != nil {
		return 0, f.pendErr
	}
	return f.pending, nil
}

func startIndexBridge(t *testing.T, hooks IndexQueueHooks) *Server {
	t.Helper()
	srv := New(Options{Addr: filepath.Join(t.TempDir(), "plugin.sock"), Version: "test", IndexQueue: hooks})
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { srv.Close() })
	return srv
}

func dialIndexPlugin(t *testing.T, srv *Server, hello bool) transport.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cli, err := transport.Dial(ctx, transport.Options{Backend: transport.BackendUDS, Addr: srv.Addr()})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { cli.Close() })
	if hello {
		res, err := cli.Call(ctx, "plugin.hello", map[string]any{"id": "test.indexer"})
		if err != nil || !res.OK {
			t.Fatalf("hello: %+v %v", res, err)
		}
	}
	return cli
}

func callIndex(t *testing.T, cli transport.Conn, method string, args any) transport.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := cli.Call(ctx, method, args)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	return res
}

// TestIndexWorkDirsFrame 验证单实例多 workdir 的发现入口：宿主 WorkDirs 原样返回。
func TestIndexWorkDirsFrame(t *testing.T) {
	srv := New(Options{
		Addr:     filepath.Join(t.TempDir(), "plugin.sock"),
		Version:  "test",
		WorkDirs: func() []string { return []string{"E:/proj-a", "E:/proj-b"} },
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { srv.Close() })
	cli := dialIndexPlugin(t, srv, true)

	res := callIndex(t, cli, "plugin.index.workdirs", map[string]any{})
	if !res.OK {
		t.Fatalf("workdirs: %+v", res)
	}
	out, _ := res.Value.(map[string]any)
	dirs, _ := out["work_dirs"].([]any)
	if len(dirs) != 2 || dirs[0] != "E:/proj-a" || dirs[1] != "E:/proj-b" {
		t.Fatalf("work_dirs=%v", out["work_dirs"])
	}
}

// TestIndexWorkDirsNotConfigured 验证未装配 WorkDirs → 空列表（插件空转等待，不报错）。
func TestIndexWorkDirsNotConfigured(t *testing.T) {
	srv := startIndexBridge(t, &fakeIndexQueue{})
	cli := dialIndexPlugin(t, srv, true)
	res := callIndex(t, cli, "plugin.index.workdirs", map[string]any{})
	if !res.OK {
		t.Fatalf("workdirs: %+v", res)
	}
	out, _ := res.Value.(map[string]any)
	if dirs, _ := out["work_dirs"].([]any); len(dirs) != 0 {
		t.Fatalf("work_dirs=%v（期望空）", out["work_dirs"])
	}
}

func TestIndexQueueFrames(t *testing.T) {
	hooks := &fakeIndexQueue{
		items: []IndexQueueItem{{
			ID: "row-1", WorkDir: "E:/proj", Path: "a.txt", Consumer: "vfts", Operation: "upsert", Attempts: 2,
		}},
		ackN:    1,
		pending: 7,
	}
	srv := startIndexBridge(t, hooks)
	cli := dialIndexPlugin(t, srv, true)

	// take
	res := callIndex(t, cli, "plugin.index.take", map[string]any{"work_dir": "E:/proj", "consumer": "vfts", "limit": 5})
	if !res.OK {
		t.Fatalf("take: %+v", res)
	}
	out, _ := res.Value.(map[string]any)
	items, _ := out["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items=%v", out["items"])
	}
	item, _ := items[0].(map[string]any)
	if item["id"] != "row-1" || item["consumer"] != "vfts" || item["operation"] != "upsert" || item["attempts"] != float64(2) {
		t.Fatalf("item=%v", item)
	}
	hooks.mu.Lock()
	if hooks.lastWorkDir != "E:/proj" || hooks.lastConsumer != "vfts" || hooks.lastLimit != 5 {
		t.Fatalf("take 参数未透传: work_dir=%q consumer=%q limit=%d", hooks.lastWorkDir, hooks.lastConsumer, hooks.lastLimit)
	}
	hooks.mu.Unlock()

	// ack
	res = callIndex(t, cli, "plugin.index.ack", map[string]any{
		"work_dir": "E:/proj", "consumer": "codegraph", "ids": []string{"row-1"}, "failed": true, "error": "boom",
	})
	if !res.OK {
		t.Fatalf("ack: %+v", res)
	}
	out, _ = res.Value.(map[string]any)
	if out["updated"] != float64(1) {
		t.Fatalf("updated=%v", out["updated"])
	}
	hooks.mu.Lock()
	if hooks.lastWorkDir != "E:/proj" || hooks.lastConsumer != "codegraph" || len(hooks.lastIDs) != 1 || hooks.lastIDs[0] != "row-1" || !hooks.lastFailed || hooks.lastErrMsg != "boom" {
		t.Fatalf("ack 参数未透传: work_dir=%q consumer=%q ids=%v failed=%v err=%q", hooks.lastWorkDir, hooks.lastConsumer, hooks.lastIDs, hooks.lastFailed, hooks.lastErrMsg)
	}
	hooks.mu.Unlock()

	// pending
	res = callIndex(t, cli, "plugin.index.pending", map[string]any{"work_dir": "E:/proj", "consumer": "vfts"})
	if !res.OK {
		t.Fatalf("pending: %+v", res)
	}
	out, _ = res.Value.(map[string]any)
	if out["pending"] != float64(7) {
		t.Fatalf("pending=%v", out["pending"])
	}
	hooks.mu.Lock()
	if hooks.lastConsumer != "vfts" {
		t.Fatalf("pending consumer 未透传: %q", hooks.lastConsumer)
	}
	hooks.mu.Unlock()
}

func TestIndexQueueRequiresHello(t *testing.T) {
	srv := startIndexBridge(t, &fakeIndexQueue{})
	cli := dialIndexPlugin(t, srv, false)

	for _, method := range []string{"plugin.index.take", "plugin.index.ack", "plugin.index.pending"} {
		res := callIndex(t, cli, method, map[string]any{})
		if res.OK || len(res.Errors) == 0 || !strings.Contains(res.Errors[0], CodePluginUnregistered) {
			t.Fatalf("%s 期望 plugin_unregistered，实际 %+v", method, res)
		}
	}
}

func TestIndexQueueNotConfigured(t *testing.T) {
	srv := startIndexBridge(t, nil)
	cli := dialIndexPlugin(t, srv, true)

	for _, method := range []string{"plugin.index.take", "plugin.index.ack", "plugin.index.pending"} {
		res := callIndex(t, cli, method, map[string]any{})
		if res.OK || len(res.Errors) == 0 || !strings.Contains(res.Errors[0], "index queue not configured") {
			t.Fatalf("%s 期望未装配错误，实际 %+v", method, res)
		}
	}
}

func TestIndexQueueHookError(t *testing.T) {
	hooks := &fakeIndexQueue{
		takeErr: errors.New("take boom"),
		ackErr:  errors.New("ack boom"),
		pendErr: errors.New("pending boom"),
	}
	srv := startIndexBridge(t, hooks)
	cli := dialIndexPlugin(t, srv, true)

	cases := []struct {
		method string
		want   string
	}{
		{"plugin.index.take", "take boom"},
		{"plugin.index.ack", "ack boom"},
		{"plugin.index.pending", "pending boom"},
	}
	for _, c := range cases {
		res := callIndex(t, cli, c.method, map[string]any{})
		if res.OK || len(res.Errors) == 0 || !strings.Contains(res.Errors[0], c.want) {
			t.Fatalf("%s 期望错误 %q，实际 %+v", c.method, c.want, res)
		}
	}
}
