// data-session-* 消息面测试（B 类随迁）：种子写**对话库**（一树一库：会话正文只在
// `dialogs/dialog-<hex>.db`）→ 总线请求（一律带 `dialog_id`）校验应答。
// 外部模块黑盒（package persist_test）：经 persist.New/Start/Stop + 总线 data-* 主题驱动。
package persist_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
)

// seedSessions 造两个对话（s1 有历史：t1/t2 + m1..m3；s2 无历史，活动更晚），
// 并写 `dialogs` 目录行（list / latest / 活动位回落由目录派生）。
// 返回 s1 的 dialog_id、s2 的 dialog_id 与 s1 对话库句柄。
func seedSessions(t *testing.T, bus mq.Bus) (string, string, *data.DB) {
	t.Helper()
	regInstance(t, bus)
	base := time.Now().UTC().Add(-time.Hour)
	rfc := func(d time.Time) string { return d.Format(time.RFC3339) }

	d1, db1 := newDialog(t, "ins-test", "s1")
	_ = db1.Table("sessions").Upsert("s1", data.Record{
		"session_id": "s1", "title": "会话A", "parent_id": "", "kind": "main",
		"created_at": rfc(base), "updated_at": rfc(base.Add(time.Minute)),
	})
	d2, db2 := newDialog(t, "ins-test", "s2")
	_ = db2.Table("sessions").Upsert("s2", data.Record{
		"session_id": "s2", "title": "会话B", "parent_id": "", "kind": "main",
		"created_at": rfc(base.Add(2 * time.Minute)), "updated_at": rfc(base.Add(3 * time.Minute)),
	})
	putDialogRow(t, "ins-test", d1, "s1", "会话A", rfc(base.Add(time.Minute)))
	putDialogRow(t, "ins-test", d2, "s2", "会话B", rfc(base.Add(3*time.Minute)))

	// s1 历史：turn t1/t2（升序），消息 m1(text)/m2(reasoning)/m3(role=tool)
	_ = db1.Table("turns").Upsert("t1", data.Record{
		"session_id": "s1", "turn_id": "t1", "created_at": rfc(base.Add(10 * time.Minute)),
	})
	_ = db1.Table("turns").Upsert("t2", data.Record{
		"session_id": "s1", "turn_id": "t2", "created_at": rfc(base.Add(20 * time.Minute)),
	})
	_ = db1.Table("messages").Upsert("m1", data.Record{
		"session_id": "s1", "turn_id": "t1", "role": "assistant", "type": "text",
		"content": "hello", "created_at": rfc(base.Add(11 * time.Minute)),
	})
	_ = db1.Table("messages").Upsert("m2", data.Record{
		"session_id": "s1", "turn_id": "t2", "role": "assistant", "type": "reasoning",
		"content": "line1\nline2\nline3\nline4", "brief": "line1",
		"created_at": rfc(base.Add(21 * time.Minute)),
	})
	_ = db1.Table("messages").Upsert("m3", data.Record{
		"session_id": "s1", "turn_id": "t2", "role": "tool",
		"tool_call_id": "tc1", "tool_call_status": "completed", "brief": "调用 llm_call",
		"content": `{"call":{"tool_call_id":"tc1","name":"llm_call","arguments":{"x":1}},` +
			`"result":{"content":"ok","status":"completed"}}`,
		"created_at": rfc(base.Add(22 * time.Minute)),
	})
	return d1, d2, db1
}

// TestDataSessionListGetTitleDelete：list（仅顶层、活动排序）/ get / title 更新 / delete（整对话删除）。
func TestDataSessionListGetTitleDelete(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	d1, _, _ := seedSessions(t, bus)

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
	r = dataCall(t, bus, "data-session-get", map[string]any{
		"req_id": "r2", "instance_id": "ins-test", "id": "s1", "dialog_id": d1,
	})
	d, _ := dataResult(t, r)["data"].(map[string]any)
	if d == nil || d["title"] != "会话A" {
		t.Fatalf("get data=%+v", dataResult(t, r))
	}
	// get 不存在 → data null（ok）
	r = dataCall(t, bus, "data-session-get", map[string]any{
		"req_id": "r3", "instance_id": "ins-test", "id": "ghost", "dialog_id": d1,
	})
	if v, ok := dataResult(t, r)["data"]; !ok || v != nil {
		t.Fatalf("ghost get=%+v", dataResult(t, r))
	}

	// title 更新
	r = dataCall(t, bus, "data-session-title", map[string]any{
		"req_id": "r4", "instance_id": "ins-test",
		"data": map[string]any{"id": "s1", "title": "会话A改", "dialog_id": d1},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("title failed: %+v", r)
	}
	r = dataCall(t, bus, "data-session-get", map[string]any{
		"req_id": "r5", "instance_id": "ins-test", "id": "s1", "dialog_id": d1,
	})
	if d, _ := dataResult(t, r)["data"].(map[string]any); d["title"] != "会话A改" {
		t.Fatalf("title not persisted: %+v", d)
	}

	// delete s1（主 session = 对话本体）→ 对话库文件与目录行一并删除
	r = dataCall(t, bus, "data-session-delete", map[string]any{
		"req_id": "r6", "instance_id": "ins-test", "id": "s1", "dialog_id": d1,
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("delete failed: %+v", r)
	}
	pudb, _ := data.PrjUsr("ins-test")
	if _, err := os.Stat(data.DialogDBPath(pudb.Path(), d1)); !os.IsNotExist(err) {
		t.Fatalf("对话库应删除: %v", err)
	}
	if ok, _ := pudb.Table("dialogs").Get(d1, &data.Record{}); ok {
		t.Fatal("dialogs 目录行应删除")
	}
	// delete 不存在（幂等 ok）
	r = dataCall(t, bus, "data-session-delete", map[string]any{
		"req_id": "r7", "instance_id": "ins-test", "id": "s1", "dialog_id": d1,
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("idempotent delete failed: %+v", r)
	}
}

// TestDataSessionActiveSetGet：active-set 写 prj config → active-get 读取（缺省回落最近活动对话）。
func TestDataSessionActiveSetGet(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	d1, _, _ := seedSessions(t, bus)

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

	// 删除活动会话 → active 清空 → 回落最近活动
	_ = dataCall(t, bus, "data-session-delete", map[string]any{
		"req_id": "r4", "instance_id": "ins-test", "id": "s1", "dialog_id": d1,
	})
	r = dataCall(t, bus, "data-session-active-get", map[string]any{"req_id": "r5", "instance_id": "ins-test"})
	if id, _ := dataResult(t, r)["session_id"].(string); id != "s2" {
		t.Fatalf("active-get after delete=%+v", dataResult(t, r))
	}
}

// TestDataSessionHistoryContent：history 分页（turns+messages+has_more）与 content 批量取全文。
func TestDataSessionHistoryContent(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	d1, _, _ := seedSessions(t, bus)

	r := dataCall(t, bus, "data-session-history", map[string]any{
		"req_id": "r1", "instance_id": "ins-test",
		"data": map[string]any{"session_id": "s1", "dialog_id": d1},
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
		"data": map[string]any{"session_id": "s1", "dialog_id": d1, "keys": []any{"message:m1", "tool_call:tc1"}},
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
	d1, _, _ := seedSessions(t, bus)

	// 写路径：append-message（role=tool，content = {call,result,async} 原文，由调用方给出）
	w := dataCall(t, bus, "data-session-append-message", map[string]any{
		"req_id": "rw", "instance_id": "ins-test",
		"data": map[string]any{"turn_id": "t2", "dialog_id": d1, "msg": map[string]any{
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
		"data": map[string]any{"session_id": "s1", "dialog_id": d1,
			"keys": []any{"tool_result:tc2", "tool_result:missing"}},
	})
	contents, _ := dataResult(t, r)["contents"].(map[string]any)
	raw, _ := contents["tool_result:tc2"].(string)
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

// TestDataSessionDialogRequired：一树一库口径下，**除根会话自举外**会话类调用缺 `dialog_id`
// 一律明确失败（`dialog_required:` 前缀；旧布局已退场，无「置 off 走旧布局」分支）。
func TestDataSessionDialogRequired(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)

	for _, tc := range []struct {
		topic   string
		payload map[string]any
	}{
		{"data-session-get", map[string]any{"req_id": "r1", "instance_id": "ins-test", "id": "s1"}},
		{"data-session-history", map[string]any{"req_id": "r2", "instance_id": "ins-test", "session_id": "s1"}},
		{"data-session-content", map[string]any{"req_id": "r3", "instance_id": "ins-test",
			"data": map[string]any{"session_id": "s1", "keys": []any{"message:m1"}}}},
		{"data-session-ensure-turn", map[string]any{"req_id": "r4", "instance_id": "ins-test",
			"turn_id": "t1", "session_id": "s1"}},
		{"data-inbox-list", map[string]any{"req_id": "r5", "instance_id": "ins-test",
			"data": map[string]any{"session": "s1"}}},
		{"data-tasktree-list", map[string]any{"req_id": "r6", "instance_id": "ins-test",
			"data": map[string]any{"top_session": "top1"}}},
		{"data-snapshot-get", map[string]any{"req_id": "r7", "instance_id": "ins-test",
			"data": map[string]any{"session_id": "s1"}}},
	} {
		r := dataCall(t, bus, tc.topic, tc.payload)
		if ok, _ := r["ok"].(bool); ok {
			t.Fatalf("%s 缺 dialog_id 应失败：%+v", tc.topic, r)
		}
		if msg, _ := r["error"].(string); !strings.HasPrefix(msg, "dialog_required:") {
			t.Fatalf("%s 错误应以 dialog_required: 开头：%q", tc.topic, msg)
		}
	}

	// 根会话自举（唯一例外）：缺 dialog_id → 确定性建对话并**回带** dialog_id。
	r := dataCall(t, bus, "data-session-ensure-session", map[string]any{
		"req_id": "r8", "instance_id": "ins-test", "session_id": "s-root",
	})
	res := dataResult(t, r)
	dlg, _ := res["dialog_id"].(string)
	if dlg == "" {
		t.Fatalf("根会话自举应回带 dialog_id：%+v", res)
	}
	if _, err := os.Stat(data.DialogDBPath(mustPrjUsrPath(t, "ins-test"), dlg)); err != nil {
		t.Fatalf("自举对话库未落盘：%v", err)
	}
	// 再次自举 → 同一 dialog_id（确定性）
	r = dataCall(t, bus, "data-session-ensure-session", map[string]any{
		"req_id": "r9", "instance_id": "ins-test", "session_id": "s-root",
	})
	if again, _ := dataResult(t, r)["dialog_id"].(string); again != dlg {
		t.Fatalf("自举应确定性：%q vs %q", dlg, again)
	}
	// 子会话（带 parent）缺 dialog_id → 不享自举例外
	r = dataCall(t, bus, "data-session-ensure-session", map[string]any{
		"req_id": "r10", "instance_id": "ins-test", "session_id": "s-sub", "parent_session_id": "s-root",
	})
	if ok, _ := r["ok"].(bool); ok {
		t.Fatalf("子会话缺 dialog_id 应失败：%+v", r)
	}
}

// mustPrjUsrPath 取实例的 prjusr 数据根（目录行 / 对话库所在目录）。
func mustPrjUsrPath(t *testing.T, inst string) string {
	t.Helper()
	pudb, err := data.PrjUsr(inst)
	if err != nil {
		t.Fatalf("PrjUsr(%s): %v", inst, err)
	}
	return pudb.Path()
}
