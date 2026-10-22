// INBOX 持久化（data-inbox-* 内部面）用例：写入 / 列表 / 状态更新 / 幂等 / 上限 / 重启保留。
//
// 设计：docs/agent-chat/中途插话设计方案.md §6.2.3。
package persist_test

import (
	"context"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

func inboxAppend(t *testing.T, bus mq.Bus, session, typ, content, idem string) map[string]any {
	t.Helper()
	data := map[string]any{"session": session, "type": typ, "content": content}
	if idem != "" {
		data["idempotency_key"] = idem
	}
	return respResult(dataCall(t, bus, "data-inbox-append", map[string]any{
		"instance_id": "ins-test", "data": data,
	}))
}

// respResult 取 persist 应答信封里的 result 子对象（{req_id, ok, result}）。
func respResult(res map[string]any) map[string]any {
	r, _ := res["result"].(map[string]any)
	return r
}

func inboxList(t *testing.T, bus mq.Bus, session, status string) []any {
	t.Helper()
	payload := map[string]any{
		"instance_id": "ins-test", "data": map[string]any{"session": session},
	}
	if status != "" {
		payload["data"].(map[string]any)["status"] = status
	}
	res := respResult(dataCall(t, bus, "data-inbox-list", payload))
	items, _ := res["items"].([]any)
	return items
}

func TestInboxAppendListUpdate(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)

	a := inboxAppend(t, bus, "s1", "next_turn", "第一条", "")
	b := inboxAppend(t, bus, "s1", "insert", "第二条", "")
	if strOf(a["id"]) == "" || strOf(b["id"]) == "" {
		t.Fatalf("append 应返回 id：%+v / %+v", a, b)
	}
	items := inboxList(t, bus, "s1", "")
	if len(items) != 2 {
		t.Fatalf("pending 应 2 条（got %d）", len(items))
	}
	first := items[0].(map[string]any)
	if strOf(first["content"]) != "第一条" || strOf(first["type"]) != "next_turn" {
		t.Fatalf("应按 created_at 升序返回：%+v", items)
	}

	// 消费第一条 → pending 只剩 1
	dataCall(t, bus, "data-inbox-update", map[string]any{
		"instance_id": "ins-test", "data": map[string]any{"id": strOf(a["id"]), "status": "consumed"},
	})
	if items := inboxList(t, bus, "s1", ""); len(items) != 1 {
		t.Fatalf("消费后 pending 应 1 条（got %d）", len(items))
	}
	if items := inboxList(t, bus, "s1", "consumed"); len(items) != 1 {
		t.Fatalf("consumed 应 1 条（got %d）", len(items))
	}

	// 类型互转（next_turn → insert）
	dataCall(t, bus, "data-inbox-update", map[string]any{
		"instance_id": "ins-test", "data": map[string]any{"id": strOf(b["id"]), "type": "insert"},
	})
	items = inboxList(t, bus, "s1", "")
	if len(items) != 1 || strOf(items[0].(map[string]any)["type"]) != "insert" {
		t.Fatalf("类型互转未生效：%+v", items)
	}
}

func TestInboxIdempotencyKey(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)

	a := inboxAppend(t, bus, "s1", "next_turn", "去重", "k-1")
	b := inboxAppend(t, bus, "s1", "next_turn", "去重", "k-1")
	if strOf(a["id"]) != strOf(b["id"]) {
		t.Fatalf("同幂等键应返回同一 id：%v vs %v", a["id"], b["id"])
	}
	if items := inboxList(t, bus, "s1", ""); len(items) != 1 {
		t.Fatalf("幂等不得重复入库（got %d）", len(items))
	}
}

func TestInboxLimit50(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)

	for i := 0; i < 50; i++ {
		if res := inboxAppend(t, bus, "s1", "next_turn", "m", ""); strOf(res["id"]) == "" {
			t.Fatalf("第 %d 条应成功：%+v", i+1, res)
		}
	}
	res := inboxAppend(t, bus, "s1", "next_turn", "m51", "")
	if ok, _ := res["ok"].(bool); ok {
		t.Fatalf("第 51 条应被拒绝：%+v", res)
	}
	if items := inboxList(t, bus, "s1", ""); len(items) != 50 {
		t.Fatalf("上限后应仍为 50（got %d）", len(items))
	}
}

// TestInboxPersistsAcrossRestart：服务重启（新总线 + 新 persist 实例、同一 usr/workdir）后
// pending 项仍在（「重启拉回」的数据基础）。
func TestInboxPersistsAcrossRestart(t *testing.T) {
	data.Reset()
	wd := t.TempDir()
	usrPath := t.TempDir() + "/usr.db"

	start := func() (mq.Bus, *persist.Service) {
		bus, err := mq.New(mq.Options{Prefix: "chonk."})
		if err != nil {
			t.Fatalf("mq.New: %v", err)
		}
		svc := persist.New(bus, persist.Options{UsrPath: usrPath})
		if err := svc.Start(); err != nil {
			t.Fatalf("persist.Start: %v", err)
		}
		pubFire(bus, "instance-register", jb(map[string]any{
			"instance_id": "ins-test", "client_type": "unittest", "work_dir": wd,
		}))
		time.Sleep(30 * time.Millisecond)
		return bus, svc
	}

	bus1, svc1 := start()
	inboxAppend(t, bus1, "s1", "insert", "重启前的插话", "")
	svc1.Stop()
	_ = bus1.Close()
	data.Reset()

	bus2, svc2 := start()
	defer func() { svc2.Stop(); _ = bus2.Close(); data.Reset() }()
	items := inboxList(t, bus2, "s1", "")
	if len(items) != 1 || strOf(items[0].(map[string]any)["content"]) != "重启前的插话" {
		t.Fatalf("重启后 pending 应保留：%+v", items)
	}
	_ = context.Background()
}

// TestInboxSessionDeleteCascade：`data-session-delete` 级联删除该会话 inbox（含 pending），
// 其他会话不受影响；删除不存在的会话也会收敛该 session_id 的孤儿行（G-65）。
func TestInboxSessionDeleteCascade(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)

	// ensure-turn 会建会话行（走主路径：会话存在 → 级联）。
	if got := mqCall(t, bus, "ensure-turn", map[string]any{"turn_id": "t1", "session_id": "s1"}); got["ok"] != true {
		t.Fatalf("ensure-turn: %+v", got)
	}
	inboxAppend(t, bus, "s1", "next_turn", "随会话删除", "")
	inboxAppend(t, bus, "s2", "next_turn", "其他会话保留", "")

	if got := mqCall(t, bus, "delete", map[string]any{"id": "s1"}); got["ok"] != true {
		t.Fatalf("data-session-delete: %+v", got)
	}
	if items := inboxList(t, bus, "s1", "all"); len(items) != 0 {
		t.Fatalf("会话删除应级联清空 inbox（got %d）", len(items))
	}
	if items := inboxList(t, bus, "s2", "all"); len(items) != 1 {
		t.Fatalf("其他会话 inbox 不应被波及（got %d）", len(items))
	}

	// 幂等删除不存在的会话 → 仍收敛其孤儿行。
	inboxAppend(t, bus, "s9", "next_turn", "孤儿行", "")
	if got := mqCall(t, bus, "delete", map[string]any{"id": "s9"}); got["ok"] != true {
		t.Fatalf("幂等删除: %+v", got)
	}
	if items := inboxList(t, bus, "s9", "all"); len(items) != 0 {
		t.Fatalf("删除不存在会话也应清理孤儿 inbox 行（got %d）", len(items))
	}
}

func strOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
