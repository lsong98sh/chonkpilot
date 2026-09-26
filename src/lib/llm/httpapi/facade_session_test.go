// browser 入口（`POST /publish`）的 session 域**门面直调**接线单测（阶段 4 第三批 / 41 G-34）：
//
//   - `data-session-*` 上行**优先走 data 门面**（服务端进程内直调）→ 总线上**零请求**；
//   - 应答与 MQ 路径（persist 信封层用同一份 `facade/wire` 翻译）**逐字一致**；
//   - 数据操作与 MQ 面/persist **同一落点**（prjusr 库回读一致）。
package httpapi

import (
	"context"
	"encoding/json"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// countSessionReqs 统计 session 域请求主题上的**请求**条数（应答带 ok 字段 → 排除）。
func countSessionReqs(t *testing.T, bus mq.Bus) *int64 {
	t.Helper()
	var n int64
	for _, subj := range []string{
		"data-session-list", "data-session-get", "data-session-ensure-session",
		"data-session-ensure-turn", "data-session-append-message", "data-session-load-messages",
		"data-session-context", "data-session-content", "data-session-history",
	} {
		if _, err := bus.On(subj, 0, func(_ context.Context, _ string, v *mq.Value) error {
			var m map[string]any
			if json.Unmarshal(v.Payload, &m) == nil {
				if _, isReply := m["ok"]; isReply {
					return nil
				}
			}
			atomic.AddInt64(&n, 1)
			return nil
		}); err != nil {
			t.Fatalf("subscribe %s: %v", subj, err)
		}
	}
	return &n
}

// normJSON 经 JSON 归一（门面侧是 Go 结构体；/publish 应答与 MQ 侧是序列化往返后的形状）。
func normJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// TestSessionPublishGoesThroughFacade：browser 入口 `data-session-*` 经门面（总线上零请求），
// 应答形状与门面 DTO → wire 结果逐字一致，数据落点与 MQ 面同源。
func TestSessionPublishGoesThroughFacade(t *testing.T) {
	data.Reset()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	// MQ 面（persist 信封层；同时作为"落点回读"的同一实现）
	svc := persist.New(bus, persist.Options{UsrPath: t.TempDir() + "/usr.db"})
	if err := svc.Start(); err != nil {
		t.Fatalf("persist.Start: %v", err)
	}
	t.Cleanup(svc.Stop)
	t.Cleanup(data.Reset)

	srv, _, base := newTestServerOpts(t, Options{
		WorkDir: t.TempDir(),
		DataDir: t.TempDir(),
		Facade:  inline.New(bus),
	})
	reqs := countSessionReqs(t, bus)

	// ① ensure-session：应答 result = {ok:true}
	env := publish(t, base, "data-session-ensure-session", `{"session_id":"s-hx"}`)
	if ok, _ := env["ok"].(bool); !ok {
		t.Fatalf("/publish 应答失败：%+v", env)
	}
	if !reflect.DeepEqual(normJSON(t, env["result"]), normJSON(t, wire.OKResult())) {
		t.Fatalf("ensure-session 应答形状变化：%+v", env["result"])
	}
	// 落点：MQ 面（persist）读到门面写入的同一份（同一 prjusr 库）
	inst := srv.InstanceID()
	got, err := svc.SessionGet(facade.SessionGetRequest{InstanceID: inst, SessionID: "s-hx"})
	if err != nil || !got.Found {
		t.Fatalf("门面写入未被 MQ 面读到：found=%v err=%v", got.Found, err)
	}

	// ② ensure-turn + append-message → load-messages/context 与门面 DTO 逐字一致
	publish(t, base, "data-session-ensure-turn", `{"turn_id":"t-hx","session_id":"s-hx"}`)
	env = publish(t, base, "data-session-append-message",
		`{"turn_id":"t-hx","msg":{"role":"user","kind":"text","content":"入口提问"}}`)
	if !reflect.DeepEqual(normJSON(t, env["result"]), normJSON(t, wire.OKResult())) {
		t.Fatalf("append-message 应答形状变化：%+v", env["result"])
	}
	lm, err := svc.MessageLoad(facade.MessageLoadRequest{InstanceID: inst, TurnID: "t-hx"})
	if err != nil || len(lm.Messages) != 1 {
		t.Fatalf("门面写入消息未被 MQ 面读到：%+v err=%v", lm, err)
	}
	env = publish(t, base, "data-session-load-messages", `{"turn_id":"t-hx"}`)
	if !reflect.DeepEqual(normJSON(t, env["result"]), normJSON(t, wire.MessageLoadResult(lm.Messages))) {
		t.Fatalf("load-messages 应答与门面 DTO 不一致：%+v", env["result"])
	}

	// ③ list / get / history / content 与门面 DTO → wire 逐字一致
	list, err := svc.SessionList(facade.SessionListRequest{InstanceID: inst})
	if err != nil {
		t.Fatalf("svc.SessionList: %v", err)
	}
	env = publish(t, base, "data-session-list", `{}`)
	if !reflect.DeepEqual(normJSON(t, env["result"]), normJSON(t, wire.SessionListResult(list.List))) {
		t.Fatalf("list 应答与门面 DTO 不一致：%+v", env["result"])
	}
	env = publish(t, base, "data-session-get", `{"id":"s-hx"}`)
	if !reflect.DeepEqual(normJSON(t, env["result"]), normJSON(t, wire.SessionGetResult(got.Found, got.Session))) {
		t.Fatalf("get 应答与门面 DTO 不一致：%+v", env["result"])
	}
	hist, err := svc.TurnHistory(facade.TurnHistoryRequest{InstanceID: inst, SessionID: "s-hx"})
	if err != nil {
		t.Fatalf("svc.TurnHistory: %v", err)
	}
	env = publish(t, base, "data-session-history", `{"session_id":"s-hx"}`)
	if !reflect.DeepEqual(normJSON(t, env["result"]), normJSON(t, wire.TurnHistoryResult(hist))) {
		t.Fatalf("history 应答与门面 DTO 不一致：%+v", env["result"])
	}
	env = publish(t, base, "data-session-content", `{"session_id":"s-hx","keys":["message:none"]}`)
	if !reflect.DeepEqual(normJSON(t, env["result"]), normJSON(t, wire.MessageContentResult(nil))) {
		t.Fatalf("content 应答形状变化（未命中 → 空 contents）：%+v", env["result"])
	}

	// ④ 关键断言：session 域**一次 MQ 请求都没发**（全部经 data 门面）
	if n := atomic.LoadInt64(reqs); n != 0 {
		t.Fatalf("session 域仍走了总线（请求数=%d）：应全部经 data 门面", n)
	}

	// ⑤ 未注入门面（薄切片）→ 回落总线 persist 路径（persist 在线，行为同改前）。
	// 注：必须与 persist 共用**同一条总线**（本包 helper 会自建总线，故此处直接构造）。
	srv2 := New(bus, Options{WorkDir: t.TempDir(), DataDir: t.TempDir(), Addr: "127.0.0.1:0"})
	if err := srv2.Start(); err != nil {
		t.Fatalf("srv2.Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv2.Shutdown(ctx)
	})
	env = publish(t, "http://"+srv2.Addr(), "data-session-list", `{"instance_id":"`+inst+`"}`)
	if ok, _ := env["ok"].(bool); !ok {
		t.Fatalf("未接线时应回落总线（persist 在线）：%+v", env)
	}
	if atomic.LoadInt64(reqs) == 0 {
		t.Fatal("未注入门面时未走总线：应保留 MQ 转发路径")
	}
}
