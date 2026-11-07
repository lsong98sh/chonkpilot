// 后台任务完成通知四段链路（§5.3）单测：
//   - idle → 落库（同一 message_id）+ tool-notify + 自动通知轮；
//   - busy → 先挂账（不落库/不广播），轮末冲刷；
//   - 结果已被 tool_result 取走 → 不通知；
//   - 通知轮内派生的任务完成 → 只落库/广播，不再级联触发。
package server

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
)

// toolNotifyRec 收集 tool-notify 的 completion 载荷（并发安全）。
type toolNotifyRec struct {
	mu  sync.Mutex
	evs []map[string]any
}

func newToolNotifyRec(t *testing.T, bus mq.Bus) *toolNotifyRec {
	t.Helper()
	r := &toolNotifyRec{}
	sub, err := bus.On("tool-notify", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil {
			return nil
		}
		if str(m["notice"]) != "completion" {
			return nil
		}
		r.mu.Lock()
		r.evs = append(r.evs, m)
		r.mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe tool-notify: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	return r
}

func (r *toolNotifyRec) all() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]map[string]any, len(r.evs))
	copy(out, r.evs)
	return out
}

func (r *toolNotifyRec) waitID(t *testing.T, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range r.all() {
			if str(m["message_id"]) == id {
				return m
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("超时等待 tool-notify message_id=%s（已收 %+v）", id, r.all())
	return nil
}

// msgRowByID 在全部对话库中按落库主键查 messages 行。
func msgRowByID(t *testing.T, id string) (data.Record, bool) {
	t.Helper()
	for _, db := range allDialogDBs(t) {
		var rec data.Record
		ok, err := db.Table("messages").Get(id, &rec)
		if err != nil {
			t.Fatalf("读 messages(%s): %v", id, err)
		}
		if ok {
			return rec, true
		}
	}
	return nil, false
}

func waitMsgRow(t *testing.T, id string) data.Record {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if rec, ok := msgRowByID(t, id); ok {
			return rec
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("超时等待消息落库 id=%s", id)
	return nil
}

func newNotifyNode(t *testing.T, s *Server, session, turn, tool, callID string) *TaskNode {
	t.Helper()
	node, err := s.tasks.start(&TaskNode{
		Tool: tool, Kind: TaskKindTool, ToolCallID: callID,
		SessionID: session, TurnID: turn, InstanceID: "ins-test", Name: tool,
	})
	if err != nil {
		t.Fatalf("tasks.start: %v", err)
	}
	return node
}

// TestNotifyTurnNotInStartJSON：内部通知轮标记不得进入 llm-start 的 JSON 消息面（契约冻结）。
func TestNotifyTurnNotInStartJSON(t *testing.T) {
	b, err := json.Marshal(StartReq{InstanceID: "i", Session: "s", Turn: "t", NotifyTurn: true})
	if err != nil {
		t.Fatalf("marshal StartReq: %v", err)
	}
	if strings.Contains(string(b), "notify_turn") || strings.Contains(string(b), "NotifyTurn") {
		t.Fatalf("NotifyTurn 不应出现在 llm-start JSON 契约中: %s", b)
	}
}

// TestNotifyCompletionIdlePersistsAndStartsTurn：idle → 同 message_id 落库 + 广播 + 通知轮续跑。
func TestNotifyCompletionIdlePersistsAndStartsTurn(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	const sess, turn = "s-nc-idle", "t-nc-idle"
	store := newSessionStore(s.bus, "ins-test")
	if err := store.EnsureSession(sess); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if err := store.EnsureTurn(turn, sess); err != nil {
		t.Fatalf("EnsureTurn: %v", err)
	}
	node := newNotifyNode(t, s, sess, turn, "script_run", "tc-nc-idle")
	mid := "msg-notify-" + node.TaskID

	notify := newToolNotifyRec(t, s.bus)
	rec := newInboxRec(t, s.bus)

	s.notifyTaskCompletion(node, sess, turn, "ins-test", "script_run", node.TaskID, "done")

	ev := notify.waitID(t, mid)
	if str(ev["session_id"]) != sess || str(ev["instance_id"]) != "ins-test" || str(ev["task_id"]) != node.TaskID {
		t.Fatalf("tool-notify 归属/字段不符: %+v", ev)
	}
	if _, ok := ev["persisted"]; ok {
		t.Fatalf("tool-notify payload 不得新增字段（消息面冻结）: %+v", ev)
	}

	row := waitMsgRow(t, mid)
	if str(row["role"]) != "user" || str(row["kind"]) != "notify" || str(row["turn_id"]) != turn {
		t.Fatalf("通知消息形状不符: %+v", row)
	}
	if str(row["session_id"]) != sess {
		t.Fatalf("通知消息应写入调用方会话: %+v", row)
	}

	// 自动通知轮：新 turn-start（同会话、非任务归属 turn）且最终完成。
	rec.wait(t, "session-turn-start", func(m map[string]any) bool {
		return str(m["session"]) == sess && str(m["turn"]) != turn
	}, 5*time.Second)
}

// TestNotifyCompletionBusyFlushesAtTurnEnd：运行轮内完成 → 仅挂账（不落库/不广播）；轮末冲刷。
func TestNotifyCompletionBusyFlushesAtTurnEnd(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	const sess, turn = "s-nc-busy", "t-nc-busy"
	startTurn(t, s, sess, turn)
	node := newNotifyNode(t, s, sess, turn, "script_run", "tc-nc-busy")
	mid := "msg-notify-" + node.TaskID
	notify := newToolNotifyRec(t, s.bus)

	s.notifyTaskCompletion(node, sess, turn, "ins-test", "script_run", node.TaskID, "done")

	if len(notify.all()) != 0 {
		t.Fatalf("busy 期间不得广播完成通知: %+v", notify.all())
	}
	if _, ok := msgRowByID(t, mid); ok {
		t.Fatalf("busy 期间不得落库: %s", mid)
	}

	// 轮末（finish）→ 冲刷：落库 + 广播。
	tc := s.lookupTurnIn("ins-test", turn)
	if tc == nil {
		t.Fatal("turn ctx not found")
	}
	s.complete(tc, "complete", "stop", "done")
	tc.Close()

	notify.waitID(t, mid)
	row := waitMsgRow(t, mid)
	if str(row["role"]) != "user" || str(row["kind"]) != "notify" || str(row["turn_id"]) != turn {
		t.Fatalf("冲刷消息形状不符: %+v", row)
	}
}

// TestNotifyCompletionSkippedWhenResultConsumed：tool_result 已取走结果 → 不再发通知。
func TestNotifyCompletionSkippedWhenResultConsumed(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	const sess, turn = "s-nc-used", "t-nc-used"
	node := newNotifyNode(t, s, sess, turn, "script_run", "tc-nc-used")
	s.tasks.done(node.TaskID, TaskStateDone, "result-body", "")
	// 模拟 tool_result 成功取回（生产路径：runToolResult → markTaskResultConsumed）。
	if text, isErr := s.runToolResult(nil, node, map[string]any{"id": node.TaskID, "timeout": 1}, context.Background()); isErr || text != "result-body" {
		t.Fatalf("runToolResult: text=%q isErr=%v", text, isErr)
	}

	notify := newToolNotifyRec(t, s.bus)
	s.notifyTaskCompletion(node, sess, turn, "ins-test", "script_run", node.TaskID, "done")

	time.Sleep(100 * time.Millisecond)
	if len(notify.all()) != 0 {
		t.Fatalf("结果已取走 → 不得通知: %+v", notify.all())
	}
	if _, ok := msgRowByID(t, "msg-notify-"+node.TaskID); ok {
		t.Fatalf("结果已取走 → 不得落库通知消息")
	}
}

// TestNotifyCompletionInNotifyTurnDoesNotCascade：通知轮内派生的任务完成 → 只落库/广播，不再起新轮。
func TestNotifyCompletionInNotifyTurnDoesNotCascade(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	const sess = "s-nc-nested"
	store := newSessionStore(s.bus, "ins-test")
	if err := store.EnsureSession(sess); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	// 内部通知轮（不投递输入；只验证级联抑制）。
	const notifyTurn = "t-nc-notify"
	res := s.acceptStart(StartReq{InstanceID: "ins-test", Session: sess, Turn: notifyTurn, NotifyTurn: true})
	if ok, _ := res["accepted"].(bool); !ok {
		t.Fatalf("通知轮受理失败: %+v", res)
	}

	node := newNotifyNode(t, s, sess, notifyTurn, "script_run", "tc-nc-nested")
	s.markTaskNotifyOrigin("ins-test", node.TaskID)
	notify := newToolNotifyRec(t, s.bus)
	rec := newInboxRec(t, s.bus)

	s.notifyTaskCompletion(node, sess, notifyTurn, "ins-test", "script_run", node.TaskID, "done")
	if len(notify.all()) != 0 {
		t.Fatalf("通知轮运行期间不得广播（应挂账）: %+v", notify.all())
	}

	// 通知轮结束 → 冲刷（只落库/广播）。
	tc := s.lookupTurnIn("ins-test", notifyTurn)
	if tc == nil {
		t.Fatal("通知轮 ctx not found")
	}
	s.complete(tc, "complete", "stop", "")
	tc.Close()

	notify.waitID(t, "msg-notify-"+node.TaskID)
	waitMsgRow(t, "msg-notify-"+node.TaskID)
	if got := rec.count("session-turn-start", func(m map[string]any) bool { return str(m["session"]) == sess }); got != 0 {
		t.Fatalf("通知轮结束后不得级联起新轮（turn-start=%d）", got)
	}

	// 通知轮已结束、该任务结果之后到达 idle → origin 标记仍应抑制级联。
	node2 := newNotifyNode(t, s, sess, notifyTurn, "script_run", "tc-nc-nested-2")
	s.markTaskNotifyOrigin("ins-test", node2.TaskID)
	s.notifyTaskCompletion(node2, sess, notifyTurn, "ins-test", "script_run", node2.TaskID, "done")
	notify.waitID(t, "msg-notify-"+node2.TaskID)
	waitMsgRow(t, "msg-notify-"+node2.TaskID)
	if got := rec.count("session-turn-start", func(m map[string]any) bool { return str(m["session"]) == sess }); got != 0 {
		t.Fatalf("通知轮派生任务（idle 到达）不得级联起新轮（turn-start=%d）", got)
	}
}
