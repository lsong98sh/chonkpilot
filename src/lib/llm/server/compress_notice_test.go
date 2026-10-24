// 压缩内建的状态通知（2026-10-24）：后台压缩在**既有**通知面 tool-notify 上发
// `compress-queued` / `compress-start` / `compress-done`（不新增主题，61-消息一览 §4.3），
// 失败另发既有 `plugin-failure`（plugin=compress）。字段形状沿用原 compress 插件 →
// 前端 `useCompressStatus` / `useQueueStatus` 零改动即可消费。
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/lsong98sh/chonkpilot-session"
)

// compressNotices 收集 tool-notify 上 plugin=compress 的载荷（并发安全）。
type compressNotices struct {
	mu   sync.Mutex
	list []map[string]any
}

func (c *compressNotices) subscribe(t *testing.T, bus mq.Bus) {
	t.Helper()
	_, err := bus.On(msgkeys.TopicToolNotify, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil || m["plugin"] != "compress" {
			return nil
		}
		c.mu.Lock()
		c.list = append(c.list, m)
		c.mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("订阅 tool-notify: %v", err)
	}
}

// of 取指定 notice 取值的全部载荷（保序）。
func (c *compressNotices) of(notice string) []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]map[string]any, 0, len(c.list))
	for _, m := range c.list {
		if m["notice"] == notice {
			out = append(out, m)
		}
	}
	return out
}

// all 返回全部已收载荷的副本（日志/断言用）。
func (c *compressNotices) all() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]map[string]any{}, c.list...)
}

// waitDone 等 `compress-done` 达到 want 条（超时即失败）。
func (c *compressNotices) waitDone(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if len(c.of("compress-done")) >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等 compress-done 达 %d 条超时（已收 %d 条 tool-notify）", want, len(c.all()))
}

// compressFailLLMServer 恒 500：用于压缩失败路径（失败也必须有终态）。
func compressFailLLMServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"boom"}}`, http.StatusInternalServerError)
	}))
}

// compressJobFor 由真实组装结果构造压缩任务（与生产触发点同形：payload 含当前轮用户输入）。
func compressJobFor(t *testing.T, s *Server, sess, anchorTurn, curTurn string) session.CompressionJob {
	t.Helper()
	tc := &turnCtx{
		server:        s,
		req:           StartReq{InstanceID: "ins-test", Session: sess, Turn: curTurn},
		hist:          newSessionStore(s.bus, "ins-test").LoadMessages(anchorTurn),
		turnTokens:    []facade.TurnToken{{TurnID: anchorTurn}},
		keepFullTurns: 1,
		briefBudget:   1,
	}
	a := tc.buildAssembly()
	if a.LastTurnID != anchorTurn {
		t.Fatalf("LastTurnID = %q, want %q", a.LastTurnID, anchorTurn)
	}
	job := session.NewJob(a, "ins-test", sess, curTurn)
	job.Payload = append(append([]ChatMsg{}, a.Context.Messages...),
		ChatMsg{Role: "user", Kind: "text", Content: "q-now"})
	return job
}

// TestCompressNoticesQueuedStartDone：三个时点各一次（入队 / 开始 / 结束），
// 且载荷字段沿用原 compress 插件形状（session_id / turn_id / message / message_id / plugin）。
func TestCompressNoticesQueuedStartDone(t *testing.T) {
	llm := slowLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	completeTurnOne(t, s, "s-cn", "t1", "hello notice")

	var rec compressNotices
	rec.subscribe(t, s.bus)

	job := compressJobFor(t, s, "s-cn", "t1", "t2")
	if !s.startCompress(job) {
		t.Fatal("首个压缩应启动")
	}
	if s.startCompress(job) {
		t.Fatal("同会话已有任务在飞 → 第二个应入队（返回 false）")
	}
	waitCompressIdle(t, s, "ins-test", "s-cn")
	rec.waitDone(t, 2) // 首个 + 入队项各一个终态

	if n := len(rec.of("compress-queued")); n != 1 {
		t.Fatalf("compress-queued 应恰 1 次，got %d（全部=%v）", n, rec.all())
	}
	if n := len(rec.of("compress-start")); n != 2 {
		t.Fatalf("compress-start 应 2 次（排队项也须开始），got %d", n)
	}
	if n := len(rec.of("compress-done")); n != 2 {
		t.Fatalf("compress-done 应 2 次（每次结束一次），got %d", n)
	}

	for _, notice := range []string{"compress-queued", "compress-start", "compress-done"} {
		m := rec.of(notice)[0]
		if m["session_id"] != "s-cn" {
			t.Errorf("%s 缺/错 session_id: %v", notice, m["session_id"])
		}
		if m["instance_id"] != "ins-test" {
			t.Errorf("%s 缺/错 instance_id: %v", notice, m["instance_id"])
		}
		if m["turn_id"] != "t1" {
			t.Errorf("%s 的 turn_id 应为压缩基准 t1: %v", notice, m["turn_id"])
		}
		if msg, _ := m["message"].(string); msg == "" {
			t.Errorf("%s 缺可读 message", notice)
		}
		if id, _ := m["message_id"].(string); !strings.HasPrefix(id, "msg-compress-"+notice+"-s-cn-") {
			t.Errorf("%s 的 message_id 形状不符（前端按此幂等去重）: %q", notice, id)
		}
	}
}

// TestCompressFailureNotice：压缩失败 → 既有 plugin-failure（plugin=compress）先落错误态，
// 随后仍发 compress-done（终态必有；前端「失败优先」→ done 不覆盖错误态）。
func TestCompressFailureNotice(t *testing.T) {
	llm := compressFailLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	var rec compressNotices
	rec.subscribe(t, s.bus)

	// 不必跑真实轮次：压缩输入 = payload 复用，摘要调用失败即进入失败终态（重试 2 次后收口）。
	job := compressJob{
		InstanceID: "ins-test", SessionID: "s-cf", TurnID: "t2", Anchor: "t1",
		Payload: []ChatMsg{
			{Role: "system", Content: "sys"},
			{Role: "user", Kind: "text", Content: "q-old"},
			{Role: "assistant", Content: "a-old"},
			{Role: "user", Kind: "text", Content: "q-now"},
		},
	}
	if !s.startCompress(job) {
		t.Fatal("压缩应启动")
	}
	rec.waitDone(t, 1)

	failures := rec.of("plugin-failure")
	if len(failures) != 1 {
		t.Fatalf("失败应恰 1 条 plugin-failure，got %d（全部=%v）", len(failures), rec.all())
	}
	if kind, _ := failures[0]["kind"].(string); kind == "" {
		t.Errorf("plugin-failure 应带失败类别 kind: %v", failures[0])
	}
	if reason, _ := failures[0]["reason"].(string); reason == "" {
		t.Errorf("plugin-failure 应带失败原因 reason: %v", failures[0])
	}

	// 顺序：plugin-failure 必须**先于** compress-done（前端据此保留错误态）。
	rec.mu.Lock()
	defer rec.mu.Unlock()
	failAt, doneAt := -1, -1
	for i, m := range rec.list {
		if m["notice"] == "plugin-failure" && failAt < 0 {
			failAt = i
		}
		if m["notice"] == "compress-done" && doneAt < 0 {
			doneAt = i
		}
	}
	if failAt < 0 || doneAt < 0 || failAt > doneAt {
		t.Fatalf("失败态应先于 done 发出：failure@%d done@%d（全部=%v）", failAt, doneAt, rec.list)
	}
}
