// 委派终态屏障 e2e（2026-11-06）：**子 agent 先终态，再判定自身终态**。
//
// 覆盖真实链路：MQ 总线 → 会话轮次 → 工具派发 → plugin/v1/agent.delegate →
// 子轮次（可再委派孙轮次）→ session-complete 事件顺序。
// 同时校验：非 complete 的子轮次绝不以 done/text 交给主 LLM（incomplete / cancelled）。
package server

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

	mcpgateway "github.com/lsong98sh/chonkpilot-agent/gateway"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
)

// ── 事件探针 ─────────────────────────────────────────────────────────

type completeRec struct {
	Session string
	Turn    string
	Status  string
	Text    string
}

type completeLog struct {
	mu   sync.Mutex
	list []completeRec
}

func watchCompletes(t *testing.T, bus mq.Bus) *completeLog {
	t.Helper()
	cl := &completeLog{}
	sub, err := bus.On("session-complete", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil {
			return nil
		}
		cl.mu.Lock()
		cl.list = append(cl.list, completeRec{
			Session: str(m["session"]), Turn: str(m["turn"]),
			Status: str(m["status"]), Text: str(m["text"]),
		})
		cl.mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("watch session-complete: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	return cl
}

func (l *completeLog) snapshot() []completeRec {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]completeRec(nil), l.list...)
}

func (l *completeLog) waitSession(t *testing.T, session string, timeout time.Duration) completeRec {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		for _, r := range l.snapshot() {
			if r.Session == session {
				return r
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待 session=%s 的 session-complete 超时（已收到 %+v）", session, l.snapshot())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func indexOfSession(list []completeRec, session string) int {
	for i, r := range list {
		if r.Session == session {
			return i
		}
	}
	return -1
}

// ── LLM 桩（按最后一条消息分派父 / 子 / 孙轮次）──────────────────────

type delegationScenario struct {
	childPrompt string        // 父代理传给子代理的 prompt
	grandPrompt string        // 子代理传给孙代理的 prompt（空 = "grand work"）
	detach      bool          // 父调用是否 detach（子代理后台继续）
	childDelay  time.Duration // child slow 的延迟
}

type delegationProbe struct {
	mu        sync.Mutex
	toolCalls []string
}

func (p *delegationProbe) record(s string) {
	p.mu.Lock()
	p.toolCalls = append(p.toolCalls, s)
	p.mu.Unlock()
}

func (p *delegationProbe) snapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.toolCalls...)
}

func toolCallChunks(id, name string, args map[string]any) []string {
	b, _ := json.Marshal(args)
	return []string{
		sseChunk(map[string]any{"content": ""}, ""),
		sseChunk(map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "id": id, "type": "function",
			"function": map[string]any{"name": name, "arguments": string(b)},
		}}}, "tool_calls"),
	}
}

func stopChunks(text string) []string {
	return []string{sseChunk(map[string]any{"content": text}, "stop")}
}

func delegationLLM(t *testing.T, sc delegationScenario, probe *delegationProbe) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ChatMsg `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Messages) == 0 {
			llmSSE(w, stopChunks("empty context"))
			return
		}
		last := body.Messages[len(body.Messages)-1]
		text := strings.TrimSpace(last.Content)

		// 工具结果：父 / 子轮次的第二次 LLM 调用。
		if last.Role == "tool" {
			probe.record(text)
			var res map[string]any
			_ = json.Unmarshal([]byte(text), &res)
			switch str(res["state"]) {
			case "running":
				llmSSE(w, stopChunks("parent final"))
			case "error":
				llmSSE(w, stopChunks("parent saw child error"))
			case "cancelled":
				llmSSE(w, stopChunks("parent saw child cancelled"))
			default:
				if strings.Contains(text, "grand final") {
					llmSSE(w, stopChunks("child final"))
				} else {
					llmSSE(w, stopChunks("parent final"))
				}
			}
			return
		}

		lower := strings.ToLower(text)
		switch {
		case strings.Contains(lower, "parent delegate"):
			args := map[string]any{
				"prompt": sc.childPrompt, "session_id": "s-child",
				"tool_call_display_name": "委派子代理",
			}
			if sc.detach {
				args["detach"] = true
				args["waitSec"] = 30
			}
			llmSSE(w, toolCallChunks("tc-child", "task_llm", args))
		case strings.Contains(lower, "child delegate"):
			gp := sc.grandPrompt
			if gp == "" {
				gp = "grand work"
			}
			args := map[string]any{
				"prompt": gp, "session_id": "s-grand",
				"tool_call_display_name": "孙代理",
			}
			llmSSE(w, toolCallChunks("tc-grand", "task_llm", args))
		case strings.Contains(lower, "grand work"):
			llmSSE(w, stopChunks("grand final"))
		case strings.Contains(lower, "grand block"):
			// 等父级取消级联关闭孙轮次；兜底 5s。
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		case strings.Contains(lower, "child slow"):
			if sc.childDelay > 0 {
				time.Sleep(sc.childDelay)
			}
			llmSSE(w, stopChunks("child final"))
		case strings.Contains(lower, "child length"):
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": "part"}, ""),
				sseChunk(map[string]any{}, "length"),
			})
		case strings.Contains(lower, "child work"):
			llmSSE(w, stopChunks("child final"))
		case strings.Contains(lower, "child block"):
			// 等 llm-cancel 取消子轮次；兜底 5s 后正常返回（避免用例挂死）。
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		default:
			llmSSE(w, stopChunks("unexpected"))
		}
	}))
}

// setupDelegation 把 task_llm 的 fake gateway 钩子接到真实 plugin bridge 能力。
func setupDelegation(t *testing.T, s *Server) {
	t.Helper()
	reg := pluginbridge.NewRegistry()
	s.SetPluginRegistry(reg)
	fakeAgentDelegate = func(req mcpgateway.CallReq) (map[string]any, error) {
		in := map[string]any{
			"instance_id": req.InstanceID, "turn": req.Turn, "session": req.Session,
			"tool_call_id": req.ToolCallID,
		}
		for k, v := range req.Arguments {
			in[k] = v
		}
		raw, _ := json.Marshal(in)
		h, ok := reg.Lookup("plugin/v1/agent.delegate")
		if !ok {
			return nil, fmt.Errorf("plugin/v1/agent.delegate 未注册")
		}
		out, err := h(context.Background(), nil, raw)
		if err != nil {
			return nil, err
		}
		m, _ := out.(map[string]any)
		return m, nil
	}
	t.Cleanup(func() { fakeAgentDelegate = nil })
}

func startDelegationTurn(t *testing.T, s *Server, session, turn, prompt string) {
	t.Helper()
	startTurn(t, s, session, turn)
	engineTextTurn(s, session, turn, prompt)
}

// waitLLMTask 等 kind=llm 任务节点建好（用于取消子轮次）。
func waitLLMTask(t *testing.T, te *taskEvents, session string) (taskID, turn string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		te.mu.Lock()
		for id, m := range te.started {
			if str(m["kind"]) == "llm" && str(m["session_id"]) == session {
				turn = str(m["turn_id"])
				te.mu.Unlock()
				return id, turn
			}
		}
		te.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("等待 kind=llm 任务节点（session=%s）超时", session)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitTaskState(t *testing.T, te *taskEvents, kind, session, state string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		te.mu.Lock()
		for _, m := range te.done {
			if str(m["kind"]) == kind && str(m["session_id"]) == session && str(m["state"]) == state {
				te.mu.Unlock()
				return m
			}
		}
		te.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("等待任务终态（kind=%s session=%s state=%s）超时", kind, session, state)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ── e2e 用例 ─────────────────────────────────────────────────────────

// TestDelegationBarrierE2E_GrandchildFirst：孙代理终态 → 子代理终态 → 父轮次终态。
func TestDelegationBarrierE2E_GrandchildFirst(t *testing.T) {
	probe := &delegationProbe{}
	llm := delegationLLM(t, delegationScenario{childPrompt: "child delegate"}, probe)
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	setupDelegation(t, s)

	cl := watchCompletes(t, s.bus)
	startDelegationTurn(t, s, "s-parent", "t-parent", "parent delegate")

	cl.waitSession(t, "s-parent", 30*time.Second)
	cl.waitSession(t, "s-child", 10*time.Second)
	cl.waitSession(t, "s-grand", 10*time.Second)

	list := cl.snapshot()
	gi, ci, pi := indexOfSession(list, "s-grand"), indexOfSession(list, "s-child"), indexOfSession(list, "s-parent")
	if gi < 0 || ci < 0 || pi < 0 {
		t.Fatalf("三层 session-complete 缺失：%+v", list)
	}
	if !(gi < ci && ci < pi) {
		t.Fatalf("终态顺序应为 孙→子→父，实际：%+v", list)
	}
	if got := list[pi].Text; got != "parent final" {
		t.Fatalf("父轮次文本应为 parent final，实际 %q", got)
	}
	if got := list[ci].Text; got != "child final" {
		t.Fatalf("子轮次文本应为 child final，实际 %q", got)
	}
	if got := list[gi].Text; got != "grand final" {
		t.Fatalf("孙轮次文本应为 grand final，实际 %q", got)
	}
}

// TestDelegationBarrierE2E_DetachWaitsChild：detach 立即返回 running，但父轮次终态
// 必须等子代理终态；running 结果不得携带 partial 正文。
func TestDelegationBarrierE2E_DetachWaitsChild(t *testing.T) {
	probe := &delegationProbe{}
	llm := delegationLLM(t, delegationScenario{
		childPrompt: "child slow", detach: true, childDelay: 400 * time.Millisecond,
	}, probe)
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	setupDelegation(t, s)

	cl := watchCompletes(t, s.bus)
	startDelegationTurn(t, s, "s-parent", "t-parent", "parent delegate")

	child := cl.waitSession(t, "s-child", 30*time.Second)
	parent := cl.waitSession(t, "s-parent", 10*time.Second)
	list := cl.snapshot()
	if indexOfSession(list, "s-child") > indexOfSession(list, "s-parent") {
		t.Fatalf("detach 场景父轮次不应先于子代理终态：%+v", list)
	}
	if parent.Text != "parent final" || child.Text != "child final" {
		t.Fatalf("终态文本异常：parent=%q child=%q", parent.Text, child.Text)
	}
	if parent.Status != "complete" || child.Status != "complete" {
		t.Fatalf("终态状态异常：parent=%q child=%q", parent.Status, child.Status)
	}
	for _, tr := range probe.snapshot() {
		if strings.Contains(tr, `"state":"running"`) && strings.Contains(tr, `"partial"`) {
			t.Fatalf("running 工具结果不得携带 partial 正文：%s", tr)
		}
	}
}

// TestDelegationBarrierE2E_IncompleteNotDone：子代理 length 截断（incomplete）→
// 主 LLM 收到 error/LLM_INCOMPLETE（而非 done/text），任务节点不得 done。
func TestDelegationBarrierE2E_IncompleteNotDone(t *testing.T) {
	probe := &delegationProbe{}
	llm := delegationLLM(t, delegationScenario{childPrompt: "child length"}, probe)
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	setupDelegation(t, s)
	te := watchTasks(t, s.bus)

	cl := watchCompletes(t, s.bus)
	startDelegationTurn(t, s, "s-parent", "t-parent", "parent delegate")

	child := cl.waitSession(t, "s-child", 30*time.Second)
	parent := cl.waitSession(t, "s-parent", 10*time.Second)
	if child.Status != "incomplete" {
		t.Fatalf("子轮次应为 incomplete，实际 %q", child.Status)
	}
	if parent.Text != "parent saw child error" {
		t.Fatalf("主 LLM 应看到子代理错误（实际文本 %q）", parent.Text)
	}
	var saw bool
	for _, tr := range probe.snapshot() {
		if strings.Contains(tr, `"code":"LLM_INCOMPLETE"`) &&
			strings.Contains(tr, `"state":"error"`) &&
			!strings.Contains(tr, `"text"`) {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("主 LLM 未收到 LLM_INCOMPLETE 非成功结果：%v", probe.snapshot())
	}
	// kind=llm 节点必须是 error，不能 done（done 只允许 complete）。
	waitTaskState(t, te, "llm", "s-child", "error")
}

// TestDelegationBarrierE2E_CancelNotDone：取消子代理 → 父主 LLM 收到 cancelled；
// 子轮次自身终态 interrupted，且仍先于父轮次。
func TestDelegationBarrierE2E_CancelNotDone(t *testing.T) {
	probe := &delegationProbe{}
	llm := delegationLLM(t, delegationScenario{childPrompt: "child block"}, probe)
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	setupDelegation(t, s)
	te := watchTasks(t, s.bus)

	cl := watchCompletes(t, s.bus)
	startDelegationTurn(t, s, "s-parent", "t-parent", "parent delegate")

	_, childTurn := waitLLMTask(t, te, "s-child")
	if v := s.bus.Emit(context.Background(), "session-cancel", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-child", "turn": childTurn,
	})).Wait(); v.Err() != nil {
		t.Fatalf("llm-cancel(child): %v", v.Err())
	}

	child := cl.waitSession(t, "s-child", 15*time.Second)
	parent := cl.waitSession(t, "s-parent", 15*time.Second)
	if child.Status != "interrupted" {
		t.Fatalf("子轮次应为 interrupted，实际 %q", child.Status)
	}
	if parent.Text != "parent saw child cancelled" {
		t.Fatalf("主 LLM 应看到子代理 cancelled（实际文本 %q）", parent.Text)
	}
	if indexOfSession(cl.snapshot(), "s-child") > indexOfSession(cl.snapshot(), "s-parent") {
		t.Fatalf("取消场景父轮次不应先于子代理终态：%+v", cl.snapshot())
	}
}

// TestDelegationBarrierE2E_CancelRecursive：取消子代理时递归关闭孙代理 ——
// 孙、子、父三级 interrupted 顺序必须保持 孙→子→父。
func TestDelegationBarrierE2E_CancelRecursive(t *testing.T) {
	probe := &delegationProbe{}
	llm := delegationLLM(t, delegationScenario{
		childPrompt: "child delegate", grandPrompt: "grand block",
	}, probe)
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	setupDelegation(t, s)
	te := watchTasks(t, s.bus)

	cl := watchCompletes(t, s.bus)
	startDelegationTurn(t, s, "s-parent", "t-parent", "parent delegate")

	_, childTurn := waitLLMTask(t, te, "s-child")
	waitLLMTask(t, te, "s-grand") // 孙轮次已登记（child 的 delegated 表已写入）
	if v := s.bus.Emit(context.Background(), "session-cancel", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-child", "turn": childTurn,
	})).Wait(); v.Err() != nil {
		t.Fatalf("llm-cancel(child): %v", v.Err())
	}

	grand := cl.waitSession(t, "s-grand", 15*time.Second)
	child := cl.waitSession(t, "s-child", 15*time.Second)
	parent := cl.waitSession(t, "s-parent", 15*time.Second)
	if grand.Status != "interrupted" || child.Status != "interrupted" {
		t.Fatalf("孙/子轮次应为 interrupted：grand=%q child=%q", grand.Status, child.Status)
	}
	list := cl.snapshot()
	gi, ci, pi := indexOfSession(list, "s-grand"), indexOfSession(list, "s-child"), indexOfSession(list, "s-parent")
	if !(gi < ci && ci < pi) {
		t.Fatalf("递归取消顺序应为 孙→子→父，实际：%+v", list)
	}
	if parent.Text != "parent saw child cancelled" {
		t.Fatalf("主 LLM 应看到子代理 cancelled（实际文本 %q）", parent.Text)
	}
}
