// 子会话压缩门控（DSL-4，42 §2 (253)「压缩子会话」，默认关闭）：
//   - 判定 = 会话行 parent_id != ""（经 data 门面 SessionGet，权威口径）；
//   - 子会话且开关未开 → 跳过压缩；主会话恒不跳过。
package compress

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-plugin"
)

// gateAPI 仅实现门控与 process 首步所需门面方法（其余经嵌入 nil 门面占位，不应被调用）。
type gateAPI struct {
	facade.API
	parent string
	found  bool
	cfg    map[string]string
	gets   int32 // SnapshotGet 次数（process 首步 = 1；跳过则不调）
}

func (a *gateAPI) SessionGet(req facade.SessionGetRequest) (facade.SessionGetResponse, error) {
	if !a.found {
		return facade.SessionGetResponse{}, nil
	}
	return facade.SessionGetResponse{Found: true, Session: facade.Session{ID: req.SessionID, ParentID: a.parent}}, nil
}

func (a *gateAPI) ConfigKVGet(req facade.ConfigKVGetRequest) (facade.ConfigKVGetResponse, error) {
	return facade.ConfigKVGetResponse{Values: a.cfg}, nil
}

func (a *gateAPI) SnapshotGet(req facade.SnapshotGetRequest) (facade.SnapshotGetResponse, error) {
	atomic.AddInt32(&a.gets, 1)
	return facade.SnapshotGetResponse{Found: false}, nil // 无快照 → process 首步即返回
}

// TestSkipSubsessionCompress 逐案核对门控判定：主会话恒不跳过；子会话仅在开关开启时不跳过；
// 会话不存在 / 读失败按主会话（保守不误跳过）。
func TestSkipSubsessionCompress(t *testing.T) {
	cases := []struct {
		name   string
		found  bool
		parent string
		cfg    map[string]string
		want   bool
	}{
		{"主会话恒不跳过（开关缺失）", true, "", nil, false},
		{"主会话恒不跳过（开关开启）", true, "", map[string]string{subsessionKey: "true"}, false},
		{"子会话 + 开关缺失 → 跳过", true, "s-main", nil, true},
		{"子会话 + 开关 false → 跳过", true, "s-main", map[string]string{subsessionKey: "false"}, true},
		{"子会话 + 开关开启 → 不跳过", true, "s-main", map[string]string{subsessionKey: "true"}, false},
		{"会话不存在 → 按主会话（不跳过）", false, "", nil, false},
	}
	for _, tc := range cases {
		c := New(DefaultOptions(), &gateAPI{found: tc.found, parent: tc.parent, cfg: tc.cfg})
		if got := c.skipSubsession(compressEvent{InstanceID: "ins", Session: "s"}); got != tc.want {
			t.Fatalf("%s：skipSubsession = %v，want %v", tc.name, got, tc.want)
		}
	}
}

// TestWorkerSkipsSubsessionCompress 端到端（worker 回路）：子会话事件默认**不压缩**
// （SnapshotGet 不被调用）；开关开启后照常进入压缩回路（调用 1 次 SnapshotGet）。
func TestWorkerSkipsSubsessionCompress(t *testing.T) {
	run := func(parent string, cfg map[string]string) int32 {
		bus, err := mq.New(mq.Options{Prefix: "chonk."})
		if err != nil {
			t.Fatalf("mq.New: %v", err)
		}
		defer bus.Close()
		api := &gateAPI{found: true, parent: parent, cfg: cfg}
		c := New(Options{RetainTurns: 1, TokenMax: 50}, api)
		if err := c.Start(plugin.Deps{Bus: bus, Logf: t.Logf}); err != nil {
			t.Fatalf("Start: %v", err)
		}
		body, _ := json.Marshal(map[string]any{
			"instance_id": "ins", "work_dir": `C:\ws`, "data_dir": `C:\ws\.chonkpilot`,
			"session": "s", "snapshot_turn": "t1",
		})
		bus.Emit(context.Background(), "session-compress", body).Wait()
		waitFor(t, func() bool {
			c.mu.Lock()
			defer c.mu.Unlock()
			return len(c.running) == 0 && len(c.pending) == 0
		}, 2*time.Second, "队列未排空")
		return atomic.LoadInt32(&api.gets)
	}

	if n := run("s-main", nil); n != 0 {
		t.Fatalf("子会话 + 开关未开：不应压缩（SnapshotGet 次数 = %d，want 0）", n)
	}
	if n := run("s-main", map[string]string{subsessionKey: "true"}); n != 1 {
		t.Fatalf("子会话 + 开关开启：应进入压缩回路（SnapshotGet 次数 = %d，want 1）", n)
	}
	if n := run("", nil); n != 1 {
		t.Fatalf("主会话：应进入压缩回路（SnapshotGet 次数 = %d，want 1）", n)
	}
}
