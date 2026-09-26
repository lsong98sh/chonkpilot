// 桥 session 域 data-* 走 **data 门面** 的接线与行为等价单测（阶段 4 第三批 / 41 G-34）：
//
//   - **不再"再发一条 MQ 给 persist"**：注入门面后，`data-session-*` 在**总线上不出现**请求
//     （计数器断言 = 0），而数据操作**确实生效**（经 MQ 面/persist 直调同一落点回读）；
//   - **前端消息面一字不变**：入参（主题名 + payload）与应答载荷**逐字一致**——桥应答与
//     「同一份 DTO 经 facade/wire 生成的结果」深比较相等；
//   - **未注入门面**（-no-server 薄客户端/分离形态）→ 回落总线转发（行为同改前）。
package bridge

import (
	"encoding/json"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
)

// normJSON 经 JSON 归一（门面侧是 Go 结构体、MQ 侧是序列化往返后的 map/[]any → 统一形状）。
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

// TestSessionDataGoesThroughFacade：session 域 data-* 由桥走门面（总线上零请求），
// 数据操作与 MQ 面**同一落点**；应答载荷与门面 DTO → wire 结果逐字一致。
func TestSessionDataGoesThroughFacade(t *testing.T) {
	br, svc, bus, instanceID := newFacadeBridgeEnv(t)
	reqs := countSubjects(t, bus,
		"data-session-ensure-session", "data-session-ensure-turn", "data-session-append-message",
		"data-session-load-messages", "data-session-list", "data-session-get",
		"data-session-history", "data-session-content", "data-session-title",
		"data-session-delete", "data-session-active-set", "data-session-active-get",
	)
	scope := facade.Scope{}

	// ① ensure-session / ensure-turn / append-message：应答 = {ok:true}（与 MQ 路径逐字一致）
	for _, req := range []struct{ typ, payload string }{
		{"data-session-ensure-session", `{"session_id":"s-br"}`},
		{"data-session-ensure-turn", `{"turn_id":"t-br","session_id":"s-br"}`},
		{"data-session-append-message", `{"turn_id":"t-br","msg":{"role":"user","kind":"text","content":"桥提问"}}`},
		{"data-session-append-message", `{"turn_id":"t-br","msg":{"role":"assistant","content":"桥回答",` +
			`"tool_calls":[{"id":"c1","type":"function","function":{"name":"tool_x","arguments":"{}"}}]}}`},
	} {
		res, errs := br.PublishEvent(req.typ, req.payload)
		if len(errs) != 0 {
			t.Fatalf("%s errs=%v", req.typ, errs)
		}
		if !reflect.DeepEqual(normJSON(t, res), normJSON(t, wire.OKResult())) {
			t.Fatalf("%s 应答形状变化：%+v", req.typ, res)
		}
	}

	// ② 落点断言：MQ 面（persist 服务）读到桥经门面写入的同一份（同一 prjusr 库）
	got, err := svc.SessionGet(facade.SessionGetRequest{InstanceID: instanceID, SessionID: "s-br", Scope: scope})
	if err != nil || !got.Found {
		t.Fatalf("门面写入未被 MQ 面读到：found=%v err=%v", got.Found, err)
	}
	lm, err := svc.MessageLoad(facade.MessageLoadRequest{InstanceID: instanceID, TurnID: "t-br", Scope: scope})
	if err != nil || len(lm.Messages) != 2 {
		t.Fatalf("门面写入消息未被 MQ 面读到：%+v err=%v", lm, err)
	}

	// ③ load-messages：应答 = 消息面形状（内核嵌套 tool_calls + _meta），与门面 DTO → wire 逐字一致
	res, errs := br.PublishEvent("data-session-load-messages", `{"turn_id":"t-br"}`)
	if len(errs) != 0 {
		t.Fatalf("load-messages errs=%v", errs)
	}
	if want := wire.MessageLoadResult(lm.Messages); !reflect.DeepEqual(normJSON(t, res), normJSON(t, want)) {
		t.Fatalf("load-messages 应答与门面 DTO 不一致：\n桥  =%+v\n门面=%+v", res, want)
	}
	// 消息面形状未变：tool_calls 仍为嵌套 function{name,arguments}
	// （经 JSON 归一看序列化后的形状；桥直调返回的是 Go 值，MQ 路径是序列化往返）
	msgs, _ := normJSON(t, res).(map[string]any)["messages"].([]any)
	m1, _ := msgs[1].(map[string]any)
	tcs, _ := m1["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("tool_calls 形状变化：%+v", m1)
	}
	fn, _ := tcs[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "tool_x" || fn["arguments"] != "{}" {
		t.Fatalf("tool_calls.function 形状变化：%+v", tcs[0])
	}

	// ④ list / get / history / content：应答与门面 DTO → wire 逐字一致
	list, err := svc.SessionList(facade.SessionListRequest{InstanceID: instanceID, Scope: scope})
	if err != nil {
		t.Fatalf("svc.SessionList: %v", err)
	}
	res, _ = br.PublishEvent("data-session-list", `{}`)
	if !reflect.DeepEqual(normJSON(t, res), normJSON(t, wire.SessionListResult(list.List))) {
		t.Fatalf("list 应答与门面 DTO 不一致：%+v", res)
	}
	res, _ = br.PublishEvent("data-session-get", `{"id":"s-br"}`)
	if !reflect.DeepEqual(normJSON(t, res), normJSON(t, wire.SessionGetResult(got.Found, got.Session))) {
		t.Fatalf("get 应答与门面 DTO 不一致：%+v", res)
	}
	hist, err := svc.TurnHistory(facade.TurnHistoryRequest{InstanceID: instanceID, SessionID: "s-br", Scope: scope})
	if err != nil {
		t.Fatalf("svc.TurnHistory: %v", err)
	}
	res, _ = br.PublishEvent("data-session-history", `{"session_id":"s-br"}`)
	if !reflect.DeepEqual(normJSON(t, res), normJSON(t, wire.TurnHistoryResult(hist))) {
		t.Fatalf("history 应答与门面 DTO 不一致：%+v", res)
	}

	// ⑤ 活动态 + 改名 + 删除（写类应答与语义）
	res, _ = br.PublishEvent("data-session-active-set", `{"session_id":"s-br"}`)
	if !reflect.DeepEqual(normJSON(t, res), normJSON(t, wire.OKResult())) {
		t.Fatalf("active-set 应答形状变化：%+v", res)
	}
	res, _ = br.PublishEvent("data-session-active-get", `{}`)
	if got := normJSON(t, res).(map[string]any)["session_id"]; got != "s-br" {
		t.Fatalf("active-get 未读到活动会话：%+v", res)
	}
	res, _ = br.PublishEvent("data-session-title", `{"id":"s-br","title":"桥改名"}`)
	if !reflect.DeepEqual(normJSON(t, res), normJSON(t, wire.OKResult())) {
		t.Fatalf("title 应答形状变化：%+v", res)
	}
	res, _ = br.PublishEvent("data-session-delete", `{"id":"s-br"}`)
	if !reflect.DeepEqual(normJSON(t, res), normJSON(t, wire.OKResult())) {
		t.Fatalf("delete 应答形状变化：%+v", res)
	}
	if g, _ := svc.SessionGet(facade.SessionGetRequest{InstanceID: instanceID, SessionID: "s-br", Scope: scope}); g.Found {
		t.Fatal("删除后 MQ 面仍读到会话")
	}

	// ⑥ 关键断言：session 域**一次 MQ 请求都没发**（全部经 data 门面）
	if n := atomic.LoadInt64(reqs); n != 0 {
		t.Fatalf("session 域仍走了总线（请求数=%d）：应全部经 data 门面", n)
	}
}

// TestNoFacadeSessionFallsBackToMQ：未注入门面（-no-server 薄客户端/分离形态）→ session 域仍走总线。
func TestNoFacadeSessionFallsBackToMQ(t *testing.T) {
	br, _, bus, instanceID := newFacadeBridgeEnv(t)
	br.SetFacade(nil) // 模拟未接线
	reqs := countSubjects(t, bus, "data-session-list")

	res, errs := br.PublishEvent("data-session-list", `{"instance_id":"`+instanceID+`"}`)
	if len(errs) > 0 {
		t.Fatalf("未接线时应回落总线（persist 在线）：errs=%v", errs)
	}
	m, _ := res.(map[string]any)
	if _, ok := m["list"]; !ok {
		t.Fatalf("回落路径应答形状变化（应 {list:[...]}）：%+v", res)
	}
	if atomic.LoadInt64(reqs) == 0 {
		t.Fatal("未注入门面时未走总线：应保留 MQ 转发路径")
	}
}
