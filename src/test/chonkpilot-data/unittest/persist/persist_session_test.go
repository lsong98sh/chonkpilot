// data-session-* 消息面测试（B 类随迁）：种子写 **prjusr 库**（v6：会话数据属项目用户级）
// → 总线请求校验应答。
// 外部模块黑盒（package persist_test）：经 persist.New/Start/Stop + 总线 data-* 主题驱动；
// 种子落库经 data.PrjUsr 直连（regInstance 已 data.Register）。
package persist_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// seedSessions 造两条顶层会话 + turns/messages（t1/t2 挂 s1），供会话域用例用。
func seedSessions(t *testing.T, bus mq.Bus) *data.DB {
	t.Helper()
	regInstance(t, bus)
	prj, err := data.PrjUsr("ins-test")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-time.Hour)
	rfc := func(d time.Time) string { return d.Format(time.RFC3339) }
	// 顶层会话 s1（有历史）/ s2（无历史，更新更晚 → 最近活动最前）
	_ = prj.Table("sessions").Upsert("s1", data.Record{
		"title": "会话A", "parent_id": "", "created_at": rfc(base), "updated_at": rfc(base.Add(time.Minute)),
	})
	_ = prj.Table("sessions").Upsert("s2", data.Record{
		"title": "会话B", "parent_id": "", "created_at": rfc(base.Add(2 * time.Minute)), "updated_at": rfc(base.Add(3 * time.Minute)),
	})
	// s1 历史：turn t1/t2（升序），消息 m1(text)/m2(reasoning)/m3(role=tool)
	_ = prj.Table("turns").Upsert("t1", data.Record{
		"session_id": "s1", "turn_id": "t1", "created_at": rfc(base.Add(10 * time.Minute)),
	})
	_ = prj.Table("turns").Upsert("t2", data.Record{
		"session_id": "s1", "turn_id": "t2", "created_at": rfc(base.Add(20 * time.Minute)),
	})
	_ = prj.Table("messages").Upsert("m1", data.Record{
		"session_id": "s1", "turn_id": "t1", "role": "assistant", "type": "text",
		"content": "hello", "created_at": rfc(base.Add(11 * time.Minute)),
	})
	_ = prj.Table("messages").Upsert("m2", data.Record{
		"session_id": "s1", "turn_id": "t2", "role": "assistant", "type": "reasoning",
		"content": "line1\nline2\nline3\nline4", "brief": "line1",
		"created_at": rfc(base.Add(21 * time.Minute)),
	})
	_ = prj.Table("messages").Upsert("m3", data.Record{
		"session_id": "s1", "turn_id": "t2", "role": "tool",
		"tool_call_id": "tc1", "tool_call_status": "completed", "brief": "调用 llm_call",
		"content": `{"call":{"tool_call_id":"tc1","name":"llm_call","arguments":{"x":1}},` +
			`"result":{"content":"ok","status":"completed"}}`,
		"created_at": rfc(base.Add(22 * time.Minute)),
	})
	return prj
}

// TestDataSessionListGetTitleDelete：list（仅顶层、活动排序）/ get / title 更新 / delete 级联清理。
func TestDataSessionListGetTitleDelete(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	seedSessions(t, bus)

	r := dataCall(t, bus, "data-session-list", map[string]any{"req_id": "r1", "instance_id": "ins-test"})
	res := dataResult(t, r)
	list, _ := res["list"].([]any)
	if len(list) != 2 {
		t.Fatalf("list=%+v", list)
	}
	first := list[0].(map[string]any)
	if first["title"] != "会话B" { // 最近活动（updated_at 晚）在前
		t.Fatalf("first=%+v", first)
	}

	// get
	r = dataCall(t, bus, "data-session-get", map[string]any{"req_id": "r2", "instance_id": "ins-test", "id": "s1"})
	d, _ := dataResult(t, r)["data"].(map[string]any)
	if d == nil || d["title"] != "会话A" {
		t.Fatalf("get data=%+v", dataResult(t, r))
	}
	// get 不存在 → data null（ok）
	r = dataCall(t, bus, "data-session-get", map[string]any{"req_id": "r3", "instance_id": "ins-test", "id": "ghost"})
	if v, ok := dataResult(t, r)["data"]; !ok || v != nil {
		t.Fatalf("ghost get=%+v", dataResult(t, r))
	}

	// title 更新
	r = dataCall(t, bus, "data-session-title", map[string]any{
		"req_id": "r4", "instance_id": "ins-test", "data": map[string]any{"id": "s1", "title": "会话A改"},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("title failed: %+v", r)
	}
	r = dataCall(t, bus, "data-session-get", map[string]any{"req_id": "r5", "instance_id": "ins-test", "id": "s1"})
	if d, _ := dataResult(t, r)["data"].(map[string]any); d["title"] != "会话A改" {
		t.Fatalf("title not persisted: %+v", d)
	}

	// delete s1 → turns/messages 清理
	r = dataCall(t, bus, "data-session-delete", map[string]any{"req_id": "r6", "instance_id": "ins-test", "id": "s1"})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("delete failed: %+v", r)
	}
	prj, _ := data.PrjUsr("ins-test")
	for _, kv := range [][2]string{{"sessions", "s1"}, {"turns", "t1"}, {"turns", "t2"}, {"messages", "m1"}, {"messages", "m3"}} {
		if ok, _ := prj.Table(kv[0]).Get(kv[1], &data.Record{}); ok {
			t.Fatalf("%s/%s not deleted", kv[0], kv[1])
		}
	}
	// delete 不存在（幂等 ok）
	r = dataCall(t, bus, "data-session-delete", map[string]any{"req_id": "r7", "instance_id": "ins-test", "id": "s1"})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("idempotent delete failed: %+v", r)
	}
}

// TestDataSessionActiveSetGet：active-set 写 prj config → active-get 读取（缺省回落最新会话）。
func TestDataSessionActiveSetGet(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	seedSessions(t, bus)

	// 未设置 → 回落最近活动顶层会话
	r := dataCall(t, bus, "data-session-active-get", map[string]any{"req_id": "r1", "instance_id": "ins-test"})
	if id, _ := dataResult(t, r)["session_id"].(string); id != "s2" {
		t.Fatalf("active-get fallback=%+v", dataResult(t, r))
	}

	r = dataCall(t, bus, "data-session-active-set", map[string]any{
		"req_id": "r2", "instance_id": "ins-test", "data": map[string]any{"session_id": "s1"},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("active-set failed: %+v", r)
	}
	r = dataCall(t, bus, "data-session-active-get", map[string]any{"req_id": "r3", "instance_id": "ins-test"})
	if id, _ := dataResult(t, r)["session_id"].(string); id != "s1" {
		t.Fatalf("active-get=%+v", dataResult(t, r))
	}

	// 删除活动会话 → active 清空 → 回落最新
	_ = dataCall(t, bus, "data-session-delete", map[string]any{"req_id": "r4", "instance_id": "ins-test", "id": "s1"})
	r = dataCall(t, bus, "data-session-active-get", map[string]any{"req_id": "r5", "instance_id": "ins-test"})
	if id, _ := dataResult(t, r)["session_id"].(string); id != "s2" {
		t.Fatalf("active-get after delete=%+v", dataResult(t, r))
	}
}

// TestDataSessionHistoryContent：history 分页（turns+messages+has_more）与 content 批量取全文。
func TestDataSessionHistoryContent(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	seedSessions(t, bus)

	r := dataCall(t, bus, "data-session-history", map[string]any{
		"req_id": "r1", "instance_id": "ins-test", "data": map[string]any{"session_id": "s1"},
	})
	messages, _ := dataResult(t, r)["messages"].(map[string]any)
	turns, _ := messages["turns"].([]any)
	msgs, _ := messages["messages"].([]any)
	if len(turns) != 2 || len(msgs) != 3 {
		t.Fatalf("history=%+v", messages)
	}
	if hasMore, _ := messages["has_more"].(bool); hasMore {
		t.Fatal("unexpected has_more")
	}

	// content：message:<id> 全文 + tool_call:<id> 完整参数 JSON
	r = dataCall(t, bus, "data-session-content", map[string]any{
		"req_id": "r2", "instance_id": "ins-test",
		"data": map[string]any{"session_id": "s1", "keys": []any{"message:m1", "tool_call:tc1"}},
	})
	contents, _ := dataResult(t, r)["contents"].(map[string]any)
	if contents["message:m1"] != "hello" {
		t.Fatalf("contents=%+v", contents)
	}
	if args, _ := contents["tool_call:tc1"].(string); args != `{"x":1}` {
		t.Fatalf("tool_call content=%v", contents["tool_call:tc1"])
	}
}

// TestDataSessionContentToolResult：`data-session-content` 的 **tool_result:<tool_call_id>**
// key 形态（2026-09-18 新增，供「异步结果读 message 表」）：返回该 role=tool 消息的 content
// **原文**（{call,result,async} JSON 字符串）；未命中的 key 省略（不报错）。
func TestDataSessionContentToolResult(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	seedSessions(t, bus)

	// 写路径：append-message（role=tool，content = {call,result,async} 原文，由调用方给出）
	w := dataCall(t, bus, "data-session-append-message", map[string]any{
		"req_id": "rw", "instance_id": "ins-test",
		"data": map[string]any{"turn_id": "t2", "msg": map[string]any{
			"role": "tool", "tool_call_id": "tc2", "tool_call_status": "completed",
			"content": `{"call":{"tool_call_id":"tc2","name":"script_run"},` +
				`"result":{"content":"hello async","status":"completed"},` +
				`"async":{"task_id":"tk-0001","moved_at":"2026-09-18T00:00:00Z"}}`,
		}},
	})
	if ok, _ := dataResult(t, w)["ok"].(bool); !ok {
		t.Fatalf("append-message: %+v", w)
	}

	r := dataCall(t, bus, "data-session-content", map[string]any{
		"req_id": "r3", "instance_id": "ins-test",
		"data": map[string]any{"session_id": "s1", "keys": []any{"tool_result:tc2", "tool_result:missing"}},
	})
	contents, _ := dataResult(t, r)["contents"].(map[string]any)
	raw, _ := contents["tool_result:tc2"].(string)
	if raw == "" {
		t.Fatalf("tool_result:tc2 未取到 content 原文: %+v", contents)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("tool_result content 应为 {call,result,async} JSON: %v（%q）", err, raw)
	}
	result, _ := m["result"].(map[string]any)
	if result == nil || result["content"] != "hello async" || result["status"] != "completed" {
		t.Fatalf("result 段错: %+v", m)
	}
	async, _ := m["async"].(map[string]any)
	if async == nil || async["task_id"] != "tk-0001" {
		t.Fatalf("async 段错: %+v", m)
	}
	if _, ok := contents["tool_result:missing"]; ok {
		t.Fatalf("未命中的 key 应省略: %+v", contents)
	}
}
