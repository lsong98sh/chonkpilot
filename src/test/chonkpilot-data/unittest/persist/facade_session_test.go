// session / turn / message 三域门面（inline 绑定）与既有 MQ 面（`data-session-*`）的
// **行为等价**单测（阶段 4 第三批验收 / 41 G-34：一份定义 → 两种绑定，同一落点、同一语义、
// 同一应答形状）。
//
// 两条路径（同一实例、同一 prjusr 数据层）：
//   - 门面 = inline 直调（`chonkpilot-data/facade/inline`；同进程函数调用，不经 MQ，见 23 §7）
//   - MQ   = persist 的 `data-session-*`（消息面，61 §3.2 / §3.2a；既有驱动方式）
//
// 覆盖：写读交叉等价（门面写→MQ 读 / MQ 写→门面读）/ 应答形状**逐字一致**（同域两路径
// 经同一份 facade/wire 翻译 → map 深比较）/ 无记录语义（get 不存在 = Found=false）/
// 订阅面（session-new 广播由实现侧发出）/ 回落（未登记实例凭 Scope 自登记）/
// **该域在总线上零请求**（门面直调不发 MQ 请求）。
package persist_test

import (
	"context"
	"encoding/json"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// countSessionReqs 统计 session 域全部请求主题上的**请求**条数（应答带 ok 字段 → 排除）。
func countSessionReqs(t *testing.T, bus mq.Bus) *int64 {
	t.Helper()
	var n int64
	for _, subj := range []string{
		"data-session-list", "data-session-get", "data-session-history", "data-session-latest",
		"data-session-title", "data-session-delete", "data-session-active-set", "data-session-active-get",
		"data-session-content", "data-session-ensure-session", "data-session-ensure-turn",
		"data-session-append-message", "data-session-set-summary", "data-session-complete-turn",
		"data-session-cleanup-stale", "data-session-load-messages", "data-session-context",
	} {
		if _, err := bus.On(subj, 0, func(_ context.Context, _ string, v *mq.Value) error {
			var m map[string]any
			if json.Unmarshal(v.Payload, &m) == nil {
				if _, isReply := m["ok"]; isReply {
					return nil // 应答
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

// normJSON 经 JSON 归一（MQ 路径载荷是"序列化往返"后的 map/[]any，门面路径是 Go 结构体；
// 比较"消息面形状"前统一形状）。
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

// mqCall 发一条 session 域 MQ 请求并取 result 载荷。
func mqCall(t *testing.T, bus mq.Bus, action string, payload map[string]any) map[string]any {
	t.Helper()
	payload["instance_id"] = facadeInstance
	return dataResult(t, dataCall(t, bus, "data-session-"+action, payload))
}

// TestFacadeSessionReadPathsEqualWire：读类动作两路径**应答形状逐字一致**（深比较 map）：
// list / get（含不存在）/ history / latest / active-get / content。
func TestFacadeSessionReadPathsEqualWire(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	seedSessions(t, bus)
	api := inline.New(bus)

	// ① list：门面 DTO → wire 结果 == MQ 应答 result（同一份翻译 ⇒ 逐字一致）
	list, err := api.SessionList(facade.SessionListRequest{InstanceID: facadeInstance})
	if err != nil {
		t.Fatalf("facade SessionList: %v", err)
	}
	if len(list.List) != 2 || list.List[0].Title != "会话B" {
		t.Fatalf("门面 list 内容不符（应 2 条、最近活动在前）：%+v", list.List)
	}
	if got := wire.SessionListResult(list.List); !reflect.DeepEqual(got, mqCall(t, bus, "list", map[string]any{})) {
		t.Fatalf("两路径 list 应答不一致：\n门面=%+v\nMQ  =%+v", got, mqCall(t, bus, "list", map[string]any{}))
	}

	// ② get：命中 + 不存在（Found=false ↔ data:null）
	get, err := api.SessionGet(facade.SessionGetRequest{InstanceID: facadeInstance, SessionID: "s1"})
	if err != nil || !get.Found {
		t.Fatalf("facade SessionGet(s1): found=%v err=%v", get.Found, err)
	}
	if got := wire.SessionGetResult(get.Found, get.Session); !reflect.DeepEqual(got, mqCall(t, bus, "get", map[string]any{"id": "s1"})) {
		t.Fatalf("两路径 get 应答不一致：\n门面=%+v\nMQ  =%+v", got, mqCall(t, bus, "get", map[string]any{"id": "s1"}))
	}
	ghost, err := api.SessionGet(facade.SessionGetRequest{InstanceID: facadeInstance, SessionID: "ghost"})
	if err != nil || ghost.Found {
		t.Fatalf("不存在会话应 Found=false：%+v err=%v", ghost, err)
	}
	mqGhost := mqCall(t, bus, "get", map[string]any{"id": "ghost"})
	if v, ok := mqGhost["data"]; !ok || v != nil {
		t.Fatalf("MQ 不存在会话应 {data:null}：%+v", mqGhost)
	}

	// ③ history：turns/messages/has_more
	hist, err := api.TurnHistory(facade.TurnHistoryRequest{InstanceID: facadeInstance, SessionID: "s1"})
	if err != nil || len(hist.Turns) != 2 || len(hist.Messages) != 3 || hist.HasMore {
		t.Fatalf("facade TurnHistory 内容不符：%+v err=%v", hist, err)
	}
	histWire := wire.TurnHistoryResult(hist)
	if !reflect.DeepEqual(histWire, mqCall(t, bus, "history", map[string]any{"session_id": "s1"})) {
		t.Fatalf("两路径 history 应答不一致：\n门面=%+v\nMQ  =%+v", histWire, mqCall(t, bus, "history", map[string]any{"session_id": "s1"}))
	}

	// ④ latest / active-get（未设置 → 回落最近活动顶层会话 s2）
	latest, err := api.SessionLatest(facade.SessionLatestRequest{InstanceID: facadeInstance})
	if err != nil || latest.SessionID != "s2" {
		t.Fatalf("facade SessionLatest=%q err=%v", latest.SessionID, err)
	}
	if got := wire.SessionIDResult(latest.SessionID); !reflect.DeepEqual(got, mqCall(t, bus, "latest", map[string]any{})) {
		t.Fatalf("两路径 latest 应答不一致：%+v", got)
	}
	act, err := api.SessionActiveGet(facade.SessionActiveGetRequest{InstanceID: facadeInstance})
	if err != nil || act.SessionID != "s2" {
		t.Fatalf("facade SessionActiveGet=%q err=%v", act.SessionID, err)
	}
	if got := wire.SessionIDResult(act.SessionID); !reflect.DeepEqual(got, mqCall(t, bus, "active-get", map[string]any{})) {
		t.Fatalf("两路径 active-get 应答不一致：%+v", got)
	}

	// ⑤ content：三 key 形态（message:/tool_call:/tool_result:）两路径一致
	keys := []string{"message:m1", "tool_call:tc1", "tool_result:missing"}
	content, err := api.MessageContent(facade.MessageContentRequest{
		InstanceID: facadeInstance, SessionID: "s1", Keys: keys,
	})
	if err != nil || content.Contents["message:m1"] != "hello" || content.Contents["tool_call:tc1"] != `{"x":1}` {
		t.Fatalf("facade MessageContent 内容不符：%+v err=%v", content.Contents, err)
	}
	contentWire := wire.MessageContentResult(content.Contents)
	if !reflect.DeepEqual(contentWire, mqCall(t, bus, "content", map[string]any{
		"session_id": "s1", "keys": []any{"message:m1", "tool_call:tc1", "tool_result:missing"},
	})) {
		t.Fatalf("两路径 content 应答不一致：%+v", contentWire)
	}
}

// TestFacadeSessionWritesCrossReadable：**写读交叉等价**——
// 门面写（ensure-session / ensure-turn / append-message / set-summary / complete-turn）→
// MQ 读；MQ 写 → 门面读；两侧落点同一（prjusr 库）。
func TestFacadeSessionWritesCrossReadable(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	api := inline.New(bus)

	// ① 门面写会话/轮次/消息 → MQ 读（history/load-messages/content）
	if _, err := api.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: facadeInstance, SessionID: "s-fac",
	}); err != nil {
		t.Fatalf("facade SessionEnsure: %v", err)
	}
	if _, err := api.TurnEnsure(facade.TurnEnsureRequest{
		InstanceID: facadeInstance, TurnID: "t-fac", SessionID: "s-fac",
	}); err != nil {
		t.Fatalf("facade TurnEnsure: %v", err)
	}
	if _, err := api.MessageAppend(facade.MessageAppendRequest{
		InstanceID: facadeInstance, TurnID: "t-fac", SessionID: "s-fac",
		Message: facade.Message{Role: "user", Kind: "text", Content: "门面提问"},
	}); err != nil {
		t.Fatalf("facade MessageAppend: %v", err)
	}
	if _, err := api.MessageAppend(facade.MessageAppendRequest{
		InstanceID: facadeInstance, TurnID: "t-fac", SessionID: "s-fac",
		Message: facade.Message{
			Role: "assistant", Content: "回答",
			ToolCalls: []facade.ToolCall{{ID: "c1", Type: "function", Name: "tool_x", Arguments: "{}"}},
		},
	}); err != nil {
		t.Fatalf("facade MessageAppend(assistant): %v", err)
	}
	lm := mqCall(t, bus, "load-messages", map[string]any{"turn_id": "t-fac"})
	msgs, _ := lm["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("MQ load-messages 应读到门面写入的 2 条：%+v", lm)
	}
	if m0, _ := msgs[0].(map[string]any); m0["role"] != "user" || m0["kind"] != "text" || m0["content"] != "门面提问" {
		t.Fatalf("MQ 读回首条不符：%+v", msgs[0])
	}
	// 消息面形状 = 内核嵌套 tool_calls（门面 DTO 的平铺命名不外溢）
	m1, _ := msgs[1].(map[string]any)
	tcs, _ := m1["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("MQ 读回 tool_calls 不符：%+v", m1)
	}
	if tc0, _ := tcs[0].(map[string]any); tc0["id"] != "c1" {
		t.Fatalf("tool_call 形状变化：%+v", tc0)
	}
	if fn, _ := tcs[0].(map[string]any)["function"].(map[string]any); fn["name"] != "tool_x" {
		t.Fatalf("tool_call function 形状变化（应为嵌套 name/arguments）：%+v", tcs[0])
	}
	if got := mqCall(t, bus, "content", map[string]any{
		"session_id": "s-fac", "keys": []any{"message:m1"},
	}); got["contents"] == nil {
		t.Fatalf("MQ content 应可读门面写入消息：%+v", got)
	}

	// ② MQ 写（ensure-turn + append-message + set-summary + complete-turn）→ 门面读
	mqCall(t, bus, "ensure-turn", map[string]any{"turn_id": "t-mq", "session_id": "s-fac"})
	mqCall(t, bus, "append-message", map[string]any{"turn_id": "t-mq", "msg": map[string]any{
		"role": "assistant", "content": "MQ 回答", "reasoning": "先想一下",
	}})
	if got := mqCall(t, bus, "set-summary", map[string]any{"turn_id": "t-mq", "summary": "MQ 摘要"}); got["ok"] != true {
		t.Fatalf("MQ set-summary 应答形状变化：%+v", got)
	}
	if got := mqCall(t, bus, "complete-turn", map[string]any{
		"turn_id": "t-mq", "status": "done", "finish_reason": "stop",
	}); got["ok"] != true {
		t.Fatalf("MQ complete-turn 应答形状变化：%+v", got)
	}
	loaded, err := api.MessageLoad(facade.MessageLoadRequest{InstanceID: facadeInstance, TurnID: "t-mq"})
	if err != nil || len(loaded.Messages) != 1 {
		t.Fatalf("门面 MessageLoad 应读到 MQ 写入：%+v err=%v", loaded, err)
	}
	if loaded.Messages[0].Content != "MQ 回答" || loaded.Messages[0].Reasoning != "先想一下" {
		t.Fatalf("门面读回内容不符（reasoning 应过面）：%+v", loaded.Messages[0])
	}
	hist, err := api.TurnHistory(facade.TurnHistoryRequest{InstanceID: facadeInstance, SessionID: "s-fac"})
	if err != nil {
		t.Fatalf("facade TurnHistory: %v", err)
	}
	var sawSummary, sawFinish bool
	for _, tr := range hist.Turns {
		if tr.Summary != nil && *tr.Summary == "MQ 摘要" {
			sawSummary = true
		}
		if tr.FinishReason != nil && *tr.FinishReason == "stop" {
			sawFinish = true
		}
	}
	if !sawSummary || !sawFinish {
		t.Fatalf("门面 history 应读回 MQ 写的 summary/finish_reason：%+v", hist.Turns)
	}
	// 两路径 history 应答逐字一致（含 summary 指针保真的"有键"语义）
	if got := wire.TurnHistoryResult(hist); !reflect.DeepEqual(got, mqCall(t, bus, "history", map[string]any{"session_id": "s-fac"})) {
		t.Fatalf("两路径 history 应答不一致：%+v", got)
	}

	// ③ context（include_snapshot=false）两路径一致（P3：**只增**伴随数组 turn_tokens 亦须一致）
	ctxResp, err := api.MessageContext(facade.MessageContextRequest{
		InstanceID: facadeInstance, SessionID: "s-fac",
	})
	if err != nil {
		t.Fatalf("facade MessageContext: %v", err)
	}
	if got := normJSON(t, wire.ContextResult(ctxResp.Messages, ctxResp.TurnTokens)); !reflect.DeepEqual(got, normJSON(t, mqCall(t, bus, "context", map[string]any{"session_id": "s-fac"}))) {
		t.Fatalf("两路径 context 应答不一致：\n门面=%+v\nMQ  =%+v", got, mqCall(t, bus, "context", map[string]any{"session_id": "s-fac"}))
	}
}

// TestFacadeSessionTitleDeleteActiveAndCleanup：改名 / 删除（级联 + 幂等）/ 活动态读写 /
// 遗留轮次清理：两路径语义一致（门面写 → MQ 读，MQ 写 → 门面读）。
func TestFacadeSessionTitleDeleteActiveAndCleanup(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	seedSessions(t, bus)
	api := inline.New(bus)

	// ① 门面改名 → MQ get 读到；MQ 改名 → 门面 get 读到
	if _, err := api.SessionTitle(facade.SessionTitleRequest{
		InstanceID: facadeInstance, SessionID: "s1", Title: "门面改名",
	}); err != nil {
		t.Fatalf("facade SessionTitle: %v", err)
	}
	if d, _ := mqCall(t, bus, "get", map[string]any{"id": "s1"})["data"].(map[string]any); d["title"] != "门面改名" {
		t.Fatalf("MQ 未读到门面改名：%+v", d)
	}
	if got := mqCall(t, bus, "title", map[string]any{"data": map[string]any{"id": "s1", "title": "MQ 改名"}}); got["ok"] != true {
		t.Fatalf("MQ title 应答形状变化：%+v", got)
	}
	g, err := api.SessionGet(facade.SessionGetRequest{InstanceID: facadeInstance, SessionID: "s1"})
	if err != nil || g.Session.Title != "MQ 改名" {
		t.Fatalf("门面未读到 MQ 改名：%+v err=%v", g.Session, err)
	}
	// 不存在会话改名 → 两路径同报错
	if _, err := api.SessionTitle(facade.SessionTitleRequest{
		InstanceID: facadeInstance, SessionID: "ghost", Title: "x",
	}); err == nil {
		t.Fatal("不存在会话改名应报错")
	}
	if ok, _ := dataCall(t, bus, "data-session-title", map[string]any{
		"instance_id": facadeInstance, "id": "ghost", "title": "x",
	})["ok"].(bool); ok {
		t.Fatal("MQ 不存在会话改名应失败")
	}

	// ② 活动态：门面写 → MQ 读；MQ 写 → 门面读
	if _, err := api.SessionActiveSet(facade.SessionActiveSetRequest{
		InstanceID: facadeInstance, SessionID: "s1",
	}); err != nil {
		t.Fatalf("facade SessionActiveSet: %v", err)
	}
	if got := mqCall(t, bus, "active-get", map[string]any{}); got["session_id"] != "s1" {
		t.Fatalf("MQ active-get 未读到门面写入：%+v", got)
	}
	if got := mqCall(t, bus, "active-set", map[string]any{"session_id": "s2"}); got["ok"] != true {
		t.Fatalf("MQ active-set 应答形状变化：%+v", got)
	}
	act, err := api.SessionActiveGet(facade.SessionActiveGetRequest{InstanceID: facadeInstance})
	if err != nil || act.SessionID != "s2" {
		t.Fatalf("门面 active-get 未读到 MQ 写入：%+v err=%v", act, err)
	}

	// ③ 遗留 running 轮次清理：门面调用 → count 与 MQ 语义同（幂等：再调为 0）
	if got := mqCall(t, bus, "ensure-turn", map[string]any{"turn_id": "t-stale", "session_id": "s1"}); got["ok"] != true {
		t.Fatalf("MQ ensure-turn 应答形状变化：%+v", got)
	}
	clean, err := api.TurnCleanupStale(facade.TurnCleanupStaleRequest{InstanceID: facadeInstance})
	if err != nil || clean.Count != 1 || !clean.OK {
		t.Fatalf("facade TurnCleanupStale=%+v err=%v", clean, err)
	}
	if got := mqCall(t, bus, "cleanup-stale", map[string]any{}); got["count"].(float64) != 0 {
		t.Fatalf("MQ cleanup-stale 应为 0（已清理）：%+v", got)
	}

	// ④ 删除：门面删 → MQ 读不到（幂等 ok）
	if _, err := api.SessionDelete(facade.SessionDeleteRequest{
		InstanceID: facadeInstance, SessionID: "s1",
	}); err != nil {
		t.Fatalf("facade SessionDelete: %v", err)
	}
	if v, ok := mqCall(t, bus, "get", map[string]any{"id": "s1"})["data"]; !ok || v != nil {
		t.Fatalf("门面删除后 MQ 仍读到：%+v", v)
	}
	if got := mqCall(t, bus, "delete", map[string]any{"id": "s1"}); got["ok"] != true {
		t.Fatalf("MQ 幂等删除应答形状变化：%+v", got)
	}
	if _, err := api.SessionDelete(facade.SessionDeleteRequest{
		InstanceID: facadeInstance, SessionID: "s-none",
	}); err != nil {
		t.Fatalf("门面幂等删除: %v", err)
	}
}

// TestFacadeSessionEnsureBroadcastsSessionNew：**订阅面**（23 §7 请求面 + 订阅面成对）——
// 幂等建会话经门面路径同样广播 session-new（{instance_id, session_id[, parent_session_id]}），
// 且**只在首次落库**广播一次（重复 ensure 不重复广播）。
func TestFacadeSessionEnsureBroadcastsSessionNew(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	api := inline.New(bus)

	ch := make(chan map[string]any, 4)
	if _, err := subRaw(bus, "session-new", func(_ string, p []byte) {
		var m map[string]any
		if json.Unmarshal(p, &m) == nil {
			select {
			case ch <- m:
			default:
			}
		}
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := api.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: facadeInstance, SessionID: "s-new", ParentSessionID: "s-top",
	}); err != nil {
		t.Fatalf("facade SessionEnsure: %v", err)
	}
	select {
	case m := <-ch:
		if m["instance_id"] != facadeInstance || m["session_id"] != "s-new" || m["parent_session_id"] != "s-top" {
			t.Fatalf("session-new 载荷字段不符：%+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未收到 session-new 广播（订阅面缺失）")
	}
	// 幂等：二次 ensure 不再广播
	if _, err := api.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: facadeInstance, SessionID: "s-new", ParentSessionID: "s-top",
	}); err != nil {
		t.Fatalf("facade SessionEnsure(二次): %v", err)
	}
	select {
	case m := <-ch:
		t.Fatalf("幂等建会话不应重复广播 session-new：%+v", m)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestFacadeSessionSelfRegistersFromScope：**回落**——实例未在本进程登记时，门面按调用方带入的
// Scope（事件载荷里的 work_dir/data_dir 事实）自登记后读写成功（语义等价于 compress 的
// "凭事件载荷自给"）。
func TestFacadeSessionSelfRegistersFromScope(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus) // 登记 ins-test；本用例用**另一个**未登记的实例
	api := inline.New(bus)

	const inst = "ins-session-scope"
	wd := t.TempDir()
	scope := facade.Scope{WorkDir: wd}

	if _, err := api.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: inst, SessionID: "s-scope", Scope: scope,
	}); err != nil {
		t.Fatalf("SessionEnsure（自登记）: %v", err)
	}
	// 自登记后：不带 Scope 也能读
	got, err := api.SessionGet(facade.SessionGetRequest{InstanceID: inst, SessionID: "s-scope"})
	if err != nil || !got.Found {
		t.Fatalf("SessionGet（自登记后）：found=%v err=%v", got.Found, err)
	}
	// 未登记且无 Scope → 明确报错（不静默）
	if _, err := api.SessionGet(facade.SessionGetRequest{
		InstanceID: "ins-ghost-session", SessionID: "s-x",
	}); err == nil {
		t.Fatal("未登记实例且无 Scope 应报错")
	}
}

// TestFacadeSessionDomainZeroBusRequests：**该域在总线上零请求**——门面（inline 绑定）
// 全部读写动作直调实现，不在 `data-session-*` 上发任何请求（应答广播 session-new 除外，
// 它是 fire-and-forget 事件且载荷无 ok 字段，不计入请求）。
func TestFacadeSessionDomainZeroBusRequests(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	api := inline.New(bus)
	reqs := countSessionReqs(t, bus)

	if _, err := api.SessionList(facade.SessionListRequest{InstanceID: facadeInstance}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.SessionEnsure(facade.SessionEnsureRequest{InstanceID: facadeInstance, SessionID: "s-zero"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.TurnEnsure(facade.TurnEnsureRequest{InstanceID: facadeInstance, TurnID: "t-zero", SessionID: "s-zero"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.MessageAppend(facade.MessageAppendRequest{
		InstanceID: facadeInstance, TurnID: "t-zero",
		Message: facade.Message{Role: "user", Content: "x"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.MessageLoad(facade.MessageLoadRequest{InstanceID: facadeInstance, TurnID: "t-zero"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.TurnHistory(facade.TurnHistoryRequest{InstanceID: facadeInstance, SessionID: "s-zero"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.SessionActiveSet(facade.SessionActiveSetRequest{InstanceID: facadeInstance, SessionID: "s-zero"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.MessageContent(facade.MessageContentRequest{
		InstanceID: facadeInstance, SessionID: "s-zero", Keys: []string{"message:x"},
	}); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt64(reqs); n != 0 {
		t.Fatalf("session 域仍走了总线（请求数=%d）：应全部经 data 门面", n)
	}
}
