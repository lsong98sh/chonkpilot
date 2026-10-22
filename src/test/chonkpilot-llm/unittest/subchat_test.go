// subchat_test.go — SUBCHAT-2 阶段 D · 单聊端到端黑盒（**MQ 驱动 + MQ 监听断言**，50-测试体系 §1）。
//
// 只经**公开总线相对主题**驱动真实 llm server（含**真实内嵌 gateway**），对 payload 断言：
//   - ① `subchat_open`（LLM 调 `self_subchat_open`）→ 建固定参数子聊天会话 + 发 `subchat-opened`；
//   - ② 子 agent 自报（LLM 调 `self_subchat_finish`）→ `lifecycle=ready` + 发 `subchat-state`；
//   - ③ `subchat-list`（带 / 不带 `parent_id`）结果形状；
//   - ④ `subchat-finish`（用户拍板）→ `lifecycle=finished` + 发 `subchat-state`；
//   - ⑤ 新一轮开始把 `ready` 回写 `chatting`（`subchat-state{chatting}`）；
//   - ⑥ 非主会话（子聊天）工具调用经 `mcp-tools-call` 观测到调用级 `async=never`；主会话不带。
//
// 一条连续链路（开 → 自报 → 清单 → 续聊回写 → 用户收口）+ 逐环节 mq 断言；不做「仅存在」断言。
// 消息面形状只依据 `docs/spec/60-reference/61-消息一览.md`（唯一准则）。不造真实 LLM 依赖：
// 用 httptest mock（与既有 `textLLMServer` 同款「mock LLM」夹具方式）。
package llmtest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// evtRec 订阅全通配（相对主题）并按主题归集事件（mq 监听断言；对齐白盒 busRecorder）。
type evtRec struct {
	mu  sync.Mutex
	evs map[string][]map[string]any
}

func newEvtRec(t *testing.T, bus mq.Bus) *evtRec {
	t.Helper()
	r := &evtRec{evs: map[string][]map[string]any{}}
	_, err := bus.On(">", 0, func(_ context.Context, subject string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil {
			return nil
		}
		r.mu.Lock()
		r.evs[subject] = append(r.evs[subject], m)
		r.mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("evtRec subscribe: %v", err)
	}
	return r
}

// wait 轮询等待某主题的首条满足谓词的事件（超时 = 红）。
func (r *evtRec) wait(t *testing.T, subject string, pred func(map[string]any) bool, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for _, m := range r.evs[subject] {
			if pred == nil || pred(m) {
				r.mu.Unlock()
				return m
			}
		}
		r.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	r.mu.Lock()
	got := append([]map[string]any{}, r.evs[subject]...)
	r.mu.Unlock()
	t.Fatalf("超时未观测到事件 %s（pred 未满足）；已收 %d 条: %+v", subject, len(got), got)
	return nil
}

// TestSubChatEndToEndBlackBox（D1 · 单聊 §3.9 验收口径）：一条连续黑盒链路，
// 驱动 = `session-send` / `subchat-list` / `subchat-finish`（公开相对主题），
// 断言 = 监听 `subchat-opened` / `subchat-state` / `mcp-tools-call`。
func TestSubChatEndToEndBlackBox(t *testing.T) {
	const purpose = "需求讨论"
	const handoff = "我想要一份方案"
	// mock LLM：主会话首问 → 调 `self_subchat_open`；子聊天轮（含交接内容）→ 调 `self_subchat_finish`；
	// 其余（工具结果回填后的收尾）→ 纯文本。避免子聊天首轮再落回开子聊天分支而递归。
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			return
		}
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		hasTool, wantOpen, wantFinish := false, false, false
		for _, m := range body.Messages {
			if m.Role == "tool" {
				hasTool = true
			}
			if m.Role == "user" && strings.Contains(m.Content, "请开个子聊天") {
				wantOpen = true
			}
			if m.Role == "user" && strings.Contains(m.Content, handoff) {
				wantFinish = true
			}
		}
		switch {
		case wantOpen && !hasTool:
			args := jb(map[string]any{
				"agent": "ag-1", "scenario_id": "scn-1", "llm": "llm-1",
				"prompt": handoff, "tool_call_display_name": purpose,
			})
			writeSSE(w, []map[string]any{
				{"content": ""},
				{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-bb-open", "type": "function",
					"function": map[string]any{"name": "self_subchat_open", "arguments": string(args)},
				}}},
			}, "tool_calls")
		case wantFinish && !hasTool:
			args := jb(map[string]any{"note": "我认为可以结束了", "tool_call_display_name": purpose})
			writeSSE(w, []map[string]any{
				{"content": ""},
				{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-bb-finish", "type": "function",
					"function": map[string]any{"name": "self_subchat_finish", "arguments": string(args)},
				}}},
			}, "tool_calls")
		default:
			writeSSE(w, []map[string]any{{"content": "好的"}}, "stop")
		}
	}))
	defer llm.Close()

	bus := newBus(t)
	startLLM(t, bus, llm.URL, false) // DisableMCP=false：真实内嵌 gateway + 域工具注册
	registerInstance(t, bus, "ins-bb")
	time.Sleep(500 * time.Millisecond) // 实例注册 / 域工具注册异步生效

	rec := newEvtRec(t, bus)
	const mainSession, mainTurn = "s-bb-main", "t-bb-main"
	startTurn(t, bus, mainSession, mainTurn, "ins-bb")

	// ① 主会话首问 → mock LLM 调 self_subchat_open → 建子聊天 + 发 subchat-opened
	w := watch(t, bus, mainTurn)
	bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-bb", "session": mainSession, "turn": mainTurn,
		"type": "text-user", "content": "请开个子聊天",
	}))
	if _, status := waitComplete(t, w, 15*time.Second); status != "complete" {
		t.Fatalf("主轮 status = %q, want complete（subchat_open 不挂起主轮）", status)
	}
	opened := rec.wait(t, "subchat-opened", func(m map[string]any) bool { return str(m, "title") != "" }, 10*time.Second)
	sid := str(opened, "session_id")
	if sid == "" || str(opened, "parent_session_id") != mainSession || str(opened, "instance_id") != "ins-bb" {
		t.Fatalf("subchat-opened 归属字段不符：%+v", opened)
	}
	if str(opened, "title") != purpose || str(opened, "purpose") != purpose {
		t.Fatalf("subchat-opened title/purpose 应 = 子聊天名字：%+v", opened)
	}

	// ② 子 agent 自报（子聊天首轮调 self_subchat_finish）→ ready + subchat-state
	// （先等 ready：它证明子聊天首轮已跑到工具调用，后续 ⑥ 方能观测到该工具调用）
	ready := rec.wait(t, "subchat-state", func(m map[string]any) bool {
		return str(m, "session_id") == sid && str(m, "lifecycle") == "ready"
	}, 15*time.Second)
	if str(ready, "parent_session_id") != mainSession {
		t.Fatalf("subchat-state 归属字段不符：%+v", ready)
	}
	if _, has := ready["instance_id"]; has {
		t.Fatalf("subchat-state 当前为广播退化路径（不带 instance_id）：%+v", ready)
	}

	// ⑥ INBOX（2026-10-18）：工具全量强制异步（原 A5「非主会话 async=never」已废止），但
	// **进程内域工具**（subchat_open / subchat_finish）不注入 async 覆盖（同步编排、契约返回体不变）。
	subCall := rec.wait(t, "mcp-tools-call", func(m map[string]any) bool { return str(m, "session") == sid }, 5*time.Second)
	if args, _ := subCall["arguments"].(map[string]any); args != nil {
		if _, has := args["async"]; has {
			t.Fatalf("进程内域工具（subchat_finish）不得附 async 覆盖：%+v", args)
		}
	}
	mainCall := rec.wait(t, "mcp-tools-call", func(m map[string]any) bool { return str(m, "session") == mainSession }, 5*time.Second)
	if args, _ := mainCall["arguments"].(map[string]any); args != nil {
		if _, has := args["async"]; has {
			t.Fatalf("进程内域工具（subchat_open）不得附 async 覆盖：%+v", args)
		}
	}

	// 等子聊天首轮收尾（否则异步 goroutine 仍持有 prj 库句柄，TempDir 清理失败）
	rec.wait(t, "session-complete", func(m map[string]any) bool { return str(m, "session") == sid }, 15*time.Second)

	// ③ subchat-list：带 parent_id → 该父会话子聊天（含生命周期）；缺省 → 全局未结束
	list := func(payload map[string]any) []any {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		v := bus.Emit(ctx, "subchat-list", jb(payload)).Wait()
		if err := v.Err(); err != nil {
			t.Fatalf("subchat-list: %v", err)
		}
		res, _ := v.Result.(map[string]any)
		if e, has := res["error"]; has {
			t.Fatalf("subchat-list 失败：%v", e)
		}
		items, _ := res["list"].([]any)
		return items
	}
	find := func(items []any, id string) map[string]any {
		for _, it := range items {
			m, _ := it.(map[string]any)
			if str(m, "session_id") == id {
				return m
			}
		}
		return nil
	}
	mine := find(list(map[string]any{"instance_id": "ins-bb", "parent_id": mainSession}), sid)
	if mine == nil {
		t.Fatalf("带 parent_id 应含该子聊天 %s", sid)
	}
	if str(mine, "purpose") != purpose || str(mine, "parent_session_id") != mainSession || str(mine, "lifecycle") == "" {
		t.Fatalf("子聊天清单项字段不符：%+v", mine)
	}
	if find(list(map[string]any{"instance_id": "ins-bb"}), sid) == nil {
		t.Fatalf("缺省（全局）应含未结束子聊天 %s", sid)
	}

	// ⑤ 新一轮开始：ready 回写 chatting + subchat-state
	startTurn(t, bus, sid, "t-bb-sub2", "ins-bb")
	rec.wait(t, "subchat-state", func(m map[string]any) bool {
		return str(m, "session_id") == sid && str(m, "lifecycle") == "chatting"
	}, 5*time.Second)

	// ④ 用户拍板 subchat-finish → finished + subchat-state
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v := bus.Emit(ctx, "subchat-finish", jb(map[string]any{"instance_id": "ins-bb", "session_id": sid})).Wait()
	if err := v.Err(); err != nil {
		t.Fatalf("subchat-finish: %v", err)
	}
	if res, _ := v.Result.(map[string]any); res["ok"] != true {
		t.Fatalf("subchat-finish 应返回 {ok:true}：%+v", v.Result)
	}
	rec.wait(t, "subchat-state", func(m map[string]any) bool {
		return str(m, "session_id") == sid && str(m, "lifecycle") == "finished"
	}, 5*time.Second)
	// finished 仍在「带 parent_id」清单里（回看入口），但不入全局未结束清单
	if f := find(list(map[string]any{"instance_id": "ins-bb", "parent_id": mainSession}), sid); f == nil || str(f, "lifecycle") != "finished" {
		t.Fatalf("finished 子聊天应仍挂在父会话清单（含已结束）：%+v", f)
	}
	if find(list(map[string]any{"instance_id": "ins-bb"}), sid) != nil {
		t.Fatalf("全局未结束清单不应含 finished 子聊天 %s", sid)
	}
}
