// groupchat_test.go — SUBCHAT-3 阶段 H · 群聊端到端黑盒（**MQ 驱动 + MQ 监听断言**，50-测试体系 §1）。
//
// 只经**公开总线相对主题**驱动真实 llm server（含**真实内嵌 gateway**），对 payload 断言：
// 一条**连续链路** —— ① 发起（N≥2 → 群聊）→ ② 依次认领（含 `@` 定向过滤）→ ③ 结论上屏
// （含「无关」不上屏、`mentions` 落库）→ ④ 本轮屏障 → 下一轮合并投递（`@` 按人过滤）→
// ⑤ 收敛点亮（主 agent「【已收敛】」→ `subchat-state{ready}`）→ ⑥ 用户 `subchat-finish` → `finished`。
//
// 同链路其余环节由既有测试覆盖（本用例**不重复堆**，仅补黑盒**连续链路 + ready→finish 尾段**）：
//   - 白盒调度器分片：`src/lib/llm/server/groupchat_sched_test.go`（发起/名册 · `@` 过滤与「无关」·
//     到顶/继续 · ask 定向 · 驱动实例与**关窗即停** · 终止 · work_session 工具面收窄 · 断点补跑计划）；
//   - 数据面两表：`src/test/chonkpilot-data/unittest/persist/group_tables_test.go`；
//   - 消息面字段/桥转发：`persist/group_message_fields_test.go` · `gui/unittest/bridge/*`。
//
// 消息面形状只依据 `docs/spec/60-reference/61-消息一览.md`（唯一准则）。用 httptest mock LLM，
// **不触发真实模型**。
package llmtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// sysAgentRefBB 从 system 消息文本里解析「子会话 agent」名（生产侧 `childAgentSystem` 的第 0 行）。
// 非空 = 该请求来自某参与者的 `work_session` 轮；空 = 主会话轮。
func sysAgentRefBB(sys string) string {
	const p = "[子会话 agent: "
	i := strings.Index(sys, p)
	if i < 0 {
		return ""
	}
	rest := sys[i+len(p):]
	j := strings.IndexByte(rest, ']')
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:j])
}

// evtSnapshot 返回某主题下已观测事件的副本（与 `evtRec` 同包 → 直读其归集表；供逐条断言用）。
func evtSnapshot(rec *evtRec, subject string) []map[string]any {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]map[string]any{}, rec.evs[subject]...)
}

// dataReply 经总线发 `data-*` 请求并按 `req_id` 匹配应答（persist 应答发布到**同一主题**，
// 载荷 `{req_id, ok, result}`；口径同 `src/lib/llm/server/dataclient.go`）。失败/超时 = 红。
func dataReply(t *testing.T, bus mq.Bus, subject string, payload map[string]any) map[string]any {
	t.Helper()
	reqID := fmt.Sprintf("bb-%d", time.Now().UnixNano())
	payload["req_id"] = reqID
	ch := make(chan map[string]any, 1)
	sub, err := bus.On(subject, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil {
			return nil
		}
		if s, _ := m["req_id"].(string); s != reqID {
			return nil
		}
		if _, has := m["ok"]; !has {
			return nil
		}
		select {
		case ch <- m:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe %s: %v", subject, err)
	}
	defer sub.Unsubscribe()
	if err := bus.Emit(context.Background(), subject, jb(payload)).Wait().Err(); err != nil {
		t.Fatalf("%s emit: %v", subject, err)
	}
	select {
	case m := <-ch:
		if ok, _ := m["ok"].(bool); !ok {
			t.Fatalf("%s failed: %v", subject, m["error"])
		}
		res, _ := m["result"].(map[string]any)
		return res
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting reply for %s", subject)
	}
	return nil
}

// TestGroupChatEndToEndBlackBox（SUBCHAT-3 阶段 H · 群聊 §3/§4/§7）：一条连续黑盒链路，
// 驱动 = `session-send`（主轮 → mock LLM 调 `self_subchat_open`）/ `subchat-finish`（用户拍板）；
// 断言 = 监听 `subchat-opened` / `subchat-state` / `subchat-participant-state` + 读
// `data-session-history`（展示板）/ `data-session-group-participants-load`（名册）/
// `data-session-group-round-targets-load`（大轮次投递）/ `data-session-get`（会话行）。
func TestGroupChatEndToEndBlackBox(t *testing.T) {
	const purpose = "代码审查"
	const opening = "审查这次改动"

	var (
		mu         sync.Mutex
		seenAgent  = map[string]int{}
		mergedMain bool // 主 agent 的 work_session 收到过「本轮群聊发言」合并条（屏障 → 下一轮合并投递）
		cInBInput  bool // 定向条（@c）是否误投给了非被 @ 者 b（应为 false，D33）
	)

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
		hasTool, wantOpen, sys, lastUser := false, false, "", ""
		for _, m := range body.Messages {
			switch m.Role {
			case "tool":
				hasTool = true
			case "system":
				sys += "\n" + m.Content
			case "user":
				lastUser = m.Content
				if strings.Contains(m.Content, "请开个群聊") {
					wantOpen = true
				}
			}
		}

		// 参与者 `work_session` 轮：按 agent 分支（判定 + 执行同一次调用，D20）。
		if agent := sysAgentRefBB(sys); agent != "" {
			merged := strings.Contains(lastUser, "【本轮群聊发言】")
			mu.Lock()
			seenAgent[agent]++
			if agent == "main" && merged {
				mergedMain = true
			}
			if agent == "b" && strings.Contains(lastUser, "请 c 复核") {
				cInBInput = true
			}
			mu.Unlock()
			text := ""
			switch agent {
			case "main":
				if merged {
					text = "【已收敛】\n评审通过" // 主 agent 判收敛（D10/D52）
				} else {
					text = "整体检查完毕，请 c 复核\n@c" // 定向发言（D27/D33）
				}
			case "b":
				if merged {
					text = "b：隐患已修复"
				} else {
					text = "b：发现一个隐患" // 公开发言（无 @ → 全体）
				}
			case "c":
				if merged {
					text = "c：复核通过"
				} else {
					text = "无关" // 无关（D13/D21）：不上屏、不入下一轮
				}
			}
			writeSSE(w, []map[string]any{{"content": text}}, "stop")
			return
		}

		// 主会话轮：首问 → 调 `self_subchat_open`（N=3 → 群聊）；工具结果回填后再答文本。
		if wantOpen && !hasTool {
			args := jb(map[string]any{
				"agent": "main", "scenario_id": "scn-1", "llm": "llm-1", "prompt": opening,
				"participants": []any{
					map[string]any{"agent": "main", "role": "审查员"},
					map[string]any{"agent": "b", "role": "程序员"},
					map[string]any{"agent": "c", "role": "测试员"},
				},
				"goal": "审查本次改动", "playbook": "发现问题→@相关人", "convergence": "连续2轮无新问题即收敛",
				"tool_call_display_name": purpose,
			})
			writeSSE(w, []map[string]any{
				{"content": ""},
				{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-gc-open", "type": "function",
					"function": map[string]any{"name": "self_subchat_open", "arguments": string(args)},
				}}},
			}, "tool_calls")
			return
		}
		writeSSE(w, []map[string]any{{"content": "已开启群聊"}}, "stop")
	}))
	defer llm.Close()

	bus := newBus(t)
	startLLM(t, bus, llm.URL, false) // DisableMCP=false：真实内嵌 gateway + 域工具注册
	registerInstance(t, bus, "ins-bb")
	time.Sleep(500 * time.Millisecond) // 实例注册 / 域工具注册异步生效

	rec := newEvtRec(t, bus)
	const mainSession, mainTurn = "s-gc-main", "t-gc-main"
	startTurn(t, bus, mainSession, mainTurn, "ins-bb")

	// ① 发起（N≥2 → 群聊）：主轮调 self_subchat_open → 建群聊 + 发 subchat-opened（主轮不挂起）。
	w := watch(t, bus, mainTurn)
	bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-bb", "session": mainSession, "turn": mainTurn,
		"type": "text-user", "content": "请开个群聊",
	}))
	if _, status := waitComplete(t, w, 15*time.Second); status != "complete" {
		t.Fatalf("主轮 status = %q, want complete（subchat_open 不挂起主轮）", status)
	}
	opened := rec.wait(t, "subchat-opened", func(m map[string]any) bool { return str(m, "purpose") == purpose }, 10*time.Second)
	gid := str(opened, "session_id")
	if gid == "" || str(opened, "parent_session_id") != mainSession || str(opened, "instance_id") != "ins-bb" {
		t.Fatalf("subchat-opened 归属字段不符：%+v", opened)
	}
	if str(opened, "title") != purpose {
		t.Fatalf("subchat-opened title 应 = 群聊名字：%+v", opened)
	}

	// ⑤ 收敛点亮：主 agent「【已收敛】」→ lifecycle=ready（复用 subchat-state，D10）。
	ready := rec.wait(t, "subchat-state", func(m map[string]any) bool {
		return str(m, "session_id") == gid && str(m, "lifecycle") == "ready"
	}, 25*time.Second)
	if str(ready, "parent_session_id") != mainSession {
		t.Fatalf("subchat-state 归属字段不符：%+v", ready)
	}

	// ② 名册（名单固定顺序 = 入参顺序，D3；含角色）。
	rosterRes := dataReply(t, bus, "data-session-group-participants-load", map[string]any{
		"instance_id": "ins-bb", "session_id": gid,
	})
	roster, _ := rosterRes["list"].([]any)
	if len(roster) != 3 {
		t.Fatalf("名册应 3 人：%+v", rosterRes)
	}
	for i, want := range []string{"main", "b", "c"} {
		row, _ := roster[i].(map[string]any)
		if str(row, "agent") != want || row["seq"] != float64(i+1) {
			t.Fatalf("名册第 %d 项不符（期望 %s seq=%d）：%+v", i, want, i+1, roster)
		}
	}
	if r0, _ := roster[0].(map[string]any); str(r0, "role") != "审查员" || str(r0, "session_id") != gid {
		t.Fatalf("名册角色/外键不符：%+v", r0)
	}

	// ④ 大轮次投递对象落库（每大轮次一条，D44 ②）：第 1 轮 = 全体。
	tgtRes := dataReply(t, bus, "data-session-group-round-targets-load", map[string]any{
		"instance_id": "ins-bb", "session_id": gid,
	})
	targets, _ := tgtRes["list"].([]any)
	if len(targets) < 2 {
		t.Fatalf("应至少落 2 个大轮次投递对象（屏障 → 下一轮）：%+v", tgtRes)
	}
	r1, _ := targets[0].(map[string]any)
	r1Agents, _ := r1["agents"].([]any)
	if len(r1Agents) != 3 {
		t.Fatalf("第 1 轮投递对象应为全体（3）：%+v", r1)
	}

	// ③ 结论上屏（展示板：`sender_agent` / `mentions` 落库；「无关」不上屏）。
	histRes := dataReply(t, bus, "data-session-history", map[string]any{
		"instance_id": "ins-bb", "session_id": gid,
	})
	msgsWrap, _ := histRes["messages"].(map[string]any)
	rawMsgs, _ := msgsWrap["messages"].([]any)
	if len(rawMsgs) == 0 {
		t.Fatalf("展示板应有消息：%+v", histRes)
	}
	first, _ := rawMsgs[0].(map[string]any)
	if str(first, "role") != "user" || str(first, "content") != opening || str(first, "sender_agent") != "main" {
		t.Fatalf("展示板首条应为 主 agent 布置任务（role=user/sender_agent=main）：%+v", first)
	}
	// 展示板业务发言仅应含 main/b/c（无系统提示：本链路收敛结束、未到顶）。
	byAgent := map[string][]map[string]any{}
	for _, e := range rawMsgs[1:] {
		m, _ := e.(map[string]any)
		a := str(m, "sender_agent")
		if a == "" {
			t.Fatalf("展示板不应出现无 sender_agent 的行（本链路无系统提示）：%+v", m)
		}
		byAgent[a] = append(byAgent[a], m)
		if strings.Contains(str(m, "content"), "无关") {
			t.Fatalf("「无关」不得上屏：%+v", m)
		}
	}
	// main 有定向发言（带 mentions=[c]）+ 收敛发言（【已收敛】）。
	var directed, converged map[string]any
	for _, m := range byAgent["main"] {
		if strings.Contains(str(m, "content"), "@c") {
			directed = m
		}
		if strings.Contains(str(m, "content"), "【已收敛】") {
			converged = m
		}
	}
	if directed == nil || converged == nil {
		t.Fatalf("主 agent 应既有定向发言、又有收敛发言：%+v", byAgent["main"])
	}
	ment, _ := directed["mentions"].([]any)
	if len(ment) != 1 {
		t.Fatalf("定向发言应带 mentions=[c]：%+v", directed)
	}
	if m0, _ := ment[0].(string); m0 != "c" {
		t.Fatalf("定向发言 mentions[0] 应为 c：%+v", directed)
	}
	if str(converged, "role") != "user" {
		t.Fatalf("主 agent ≡ 用户（D47）：其发言应落 role=user：%+v", converged)
	}
	if len(byAgent["b"]) == 0 || len(byAgent["c"]) == 0 {
		t.Fatalf("b / c 均应有结论上屏：b=%d c=%d", len(byAgent["b"]), len(byAgent["c"]))
	}
	if b0 := byAgent["b"][0]; b0["mentions"] != nil {
		t.Fatalf("b 的公开发言不应带 mentions：%+v", b0)
	}

	// ② 参与者状态三态（C3 / D16）：main thinking+spoken；b spoken；c 首轮 idle（无关）后 spoken。
	states := evtSnapshot(rec, "subchat-participant-state")
	hasState := func(agent, state string) bool {
		for _, m := range states {
			if str(m, "session_id") == gid && str(m, "agent") == agent && str(m, "state") == state {
				return true
			}
		}
		return false
	}
	for _, want := range [][2]string{{"main", "thinking"}, {"main", "spoken"}, {"b", "spoken"}, {"c", "idle"}, {"c", "spoken"}} {
		if !hasState(want[0], want[1]) {
			t.Fatalf("参与者状态应含 %s/%s：%+v", want[0], want[1], states)
		}
	}

	// ②/④ 调度（依次认领 + 屏障 + 下一轮合并投递 + `@` 按人过滤）：各参与者都被叫到，
	// 主 agent 的 work_session 收到「本轮群聊发言」合并条，定向条 @c 未误投给 b。
	mu.Lock()
	gotAgents, gotMerged, gotCInB := seenAgent, mergedMain, cInBInput
	mu.Unlock()
	for _, a := range []string{"main", "b", "c"} {
		if gotAgents[a] < 1 {
			t.Fatalf("参与者 %s 应被叫到（依次认领）：%+v", a, gotAgents)
		}
	}
	if !gotMerged {
		t.Fatal("下一轮应把本轮发言合并成一条喂给参与者（屏障 → 合并投递，D31/D32）")
	}
	if gotCInB {
		t.Fatal("定向条（@c）不应投给非被 @ 者 b（D33）")
	}

	// ⑥ 用户拍板 subchat-finish → lifecycle=finished + subchat-state。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v := bus.Emit(ctx, "subchat-finish", jb(map[string]any{"instance_id": "ins-bb", "session_id": gid})).Wait()
	if err := v.Err(); err != nil {
		t.Fatalf("subchat-finish: %v", err)
	}
	if res, _ := v.Result.(map[string]any); res["ok"] != true {
		t.Fatalf("subchat-finish 应返回 {ok:true}：%+v", v.Result)
	}
	rec.wait(t, "subchat-state", func(m map[string]any) bool {
		return str(m, "session_id") == gid && str(m, "lifecycle") == "finished"
	}, 5*time.Second)
	got := dataReply(t, bus, "data-session-get", map[string]any{"instance_id": "ins-bb", "id": gid})
	sess, _ := got["data"].(map[string]any)
	if str(sess, "lifecycle") != "finished" {
		t.Fatalf("会话行 lifecycle 应为 finished：%+v", got)
	}
	if str(sess, "kind") != "group_chat" {
		t.Fatalf("会话行 kind 应为 group_chat：%+v", sess)
	}
}
