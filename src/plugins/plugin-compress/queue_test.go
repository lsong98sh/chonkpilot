// 每会话串行 worker + 合并队列的行为断言（2026-10-06，OP-03）。
//
// 覆盖：
//  1. **非阻塞**：订阅回调只入队、立即返回 —— 压缩（摘要 LLM）阻塞期间 `session-compress`
//     的 Emit 仍瞬时返回（改前同步执行会阻塞到摘要返回）。
//  2. **同会话合并**：压缩进行中再来的同会话事件只合并为最后一次（不排队 N 次）。
package compress

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-plugin"
)

// countingAPI 包装 data 门面以统计 SnapshotGet 次数（每次 process = 1 次读快照）。
type countingAPI struct {
	facade.API
	mu   sync.Mutex
	gets int
}

func (a *countingAPI) SnapshotGet(req facade.SnapshotGetRequest) (facade.SnapshotGetResponse, error) {
	a.mu.Lock()
	a.gets++
	a.mu.Unlock()
	return a.API.SnapshotGet(req)
}

func (a *countingAPI) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.gets
}

// queueSeed 生成 n 轮（user + assistant 一对）历史；content 固定长（保证可压缩）。
func queueSeed(n int) []facade.Message {
	content := strings.Repeat("很长的历史内容", 40)
	var out []facade.Message
	for i := 0; i < n; i++ {
		out = append(out,
			facade.Message{Role: "user", Kind: "text", Content: content},
			facade.Message{Role: "assistant", Content: content},
		)
	}
	return out
}

// waitFor 轮询 cond 直到为真或超时（异步断言用）。
func waitFor(t *testing.T, cond func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("超时未满足：%s", msg)
}

// TestCompressorNonBlockingAndCoalesce（OP-03）：非阻塞 + 同会话合并。
func TestCompressorNonBlockingAndCoalesce(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	defer bus.Close()

	const ins, sess, wd, dd = "ins-q", "s-q", `C:\ws`, `C:\ws\.chonkpilot`
	api := &countingAPI{API: inline.New(bus)}
	data.Reset()
	t.Cleanup(data.Reset)
	data.Register(ins, wd, dd)
	prj, releasePrj, err := data.Prj(ins)
	if err != nil {
		t.Fatalf("data.Prj: %v", err)
	}
	defer releasePrj() // 短开（D-45）：用完即释
	for k, v := range map[string]string{
		"keep_full_max_turns": "1", "keep_full_max_tokens": "100000", "compress_token_threshold": "50",
	} {
		if err := data.SetConfig(prj, k, v); err != nil {
			t.Fatalf("SetConfig %s: %v", k, err)
		}
	}
	snap := facade.Snapshot{SessionID: sess, Turn: "t12", Messages: queueSeed(6)}
	if _, err := api.SnapshotSet(facade.SnapshotSetRequest{InstanceID: ins, Snapshot: snap}); err != nil {
		t.Fatalf("SnapshotSet: %v", err)
	}

	// llm-simple：首次调用阻塞（制造「压缩进行中」），后续正常返回。
	var mu sync.Mutex
	llmCalls := 0
	gate := make(chan struct{})
	if _, err := bus.On("llm-simple", 0, func(_ context.Context, _ string, v *mq.Value) error {
		mu.Lock()
		llmCalls++
		n := llmCalls
		mu.Unlock()
		if n == 1 {
			<-gate // 首次压缩摘要阻塞，直到放行
		}
		v.Result = map[string]any{"text": "SUMMARY"}
		return nil
	}); err != nil {
		t.Fatalf("stub llm-simple: %v", err)
	}

	c := New(Options{RetainTurns: 1, TokenMax: 50}, api)
	if err := c.Start(plugin.Deps{Bus: bus, Logf: t.Logf}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	body, _ := json.Marshal(map[string]any{
		"instance_id": ins, "work_dir": wd, "data_dir": dd, "session": sess, "snapshot_turn": "t12",
	})

	// ① 非阻塞：门未放行（压缩进行中）时 Emit 必须立即返回。
	start := time.Now()
	bus.Emit(context.Background(), "session-compress", body).Wait()
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("session-compress 的订阅回调阻塞了 %v（应「立即返回」）", d)
	}
	// 等首次压缩确实进行中（首次 llm-simple 已进入并被门阻塞）。
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return llmCalls >= 1 }, 2*time.Second, "首次压缩未启动")

	// ② 合并：压缩进行中连发 4 次同会话事件 → 合并为最后一次（不排队 5 次）。
	for i := 0; i < 4; i++ {
		bus.Emit(context.Background(), "session-compress", body).Wait()
	}
	close(gate) // 放行首次摘要

	// 等队列排空（running / pending 清空）。
	waitFor(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.running) == 0 && len(c.pending) == 0
	}, 3*time.Second, "队列未排空")

	// 同会话 5 次事件 → 至多 2 次 process（首次 + 合并后最后一次）。
	if got := api.count(); got > 2 {
		t.Fatalf("同会话重复事件未合并：process 执行 %d 次（应 <= 2）", got)
	} else if got < 1 {
		t.Fatal("应至少执行一次压缩")
	}
	t.Logf("raw evidence: process(SnapshotGet) 次数=%d（同会话 5 次事件合并后）", api.count())
}
