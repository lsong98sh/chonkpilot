// plugin_caps_session_run_test.go —— SESSION-RUN-1 的 L1 用例：
// 顶层会话创建 / 续跑、任务节点归属、detached（barrier=false）不阻塞父轮终态。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade"
	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
)

func bridgeCode(err error) string {
	var e *pluginbridge.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func sessionRunHandler(t *testing.T, s *Server) pluginbridge.Handler {
	t.Helper()
	reg := pluginbridge.NewRegistry()
	s.SetPluginRegistry(reg)
	h, ok := reg.Lookup("plugin/v1/session.run")
	if !ok {
		t.Fatal("plugin/v1/session.run 未注册")
	}
	return h
}

// sessionRunParentCtx 建一个只用于能力定位的父轮次（不投递输入 → 不产生 LLM 调用）。
func sessionRunParentCtx(t *testing.T, s *Server, session, turn string) *turnCtx {
	t.Helper()
	store := newSessionStore(s.bus, "ins-test")
	if err := store.EnsureSession(session); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if err := store.EnsureTurn(turn, session); err != nil {
		t.Fatalf("EnsureTurn: %v", err)
	}
	parent := newTurnCtx(context.Background(), s, StartReq{
		InstanceID: "ins-test", Session: session, Turn: turn, WorkDir: testWorkDir,
	})
	t.Cleanup(parent.Close)
	s.mu.Lock()
	s.registerTurnLocked(instKey("ins-test", turn), parent)
	s.mu.Unlock()
	return parent
}

func waitSessionTaskState(t *testing.T, s *Server, taskID string, want string, timeout time.Duration) *TaskNode {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		node := s.tasks.get(taskID)
		if node != nil && node.State == want {
			return node
		}
		if time.Now().After(deadline) {
			t.Fatalf("任务节点未到 %s：%+v", want, node)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestSessionRunCreateAndSend：session_id 缺省 = 新建顶层对话 + 首轮；给定 = 向既有会话续跑。
func TestSessionRunCreateAndSend(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	sessionRunParentCtx(t, s, "s-parent", "t-parent")
	h := sessionRunHandler(t, s)

	// 先建调用方工具节点，验证子任务挂在其下（真实链路里由 gateway 工具调用建）。
	toolNode, terr := s.tasks.start(&TaskNode{
		Tool: "session_create", ToolCallID: "tc-split-1", TopSession: "s-parent",
		Kind: TaskKindTool, InstanceID: "ins-test", Name: "调用 session_create",
	})
	if terr != nil {
		t.Fatalf("建工具节点: %v", terr)
	}

	// ① 新建：返回顶层 session（split-*）+ dialog_id + task_id，首轮到 done。
	raw, _ := json.Marshal(map[string]any{
		"instance_id": "ins-test", "turn": "t-parent", "session": "s-parent",
		"tool_call_id": "tc-split-1", "title": "任务 A", "prompt": "处理任务 A", "wait_sec": 10,
	})
	got, err := h(context.Background(), nil, raw)
	if err != nil {
		t.Fatalf("session.run(create): %v", err)
	}
	res, _ := got.(map[string]any)
	if str(res["state"]) != "done" || str(res["text"]) != "处理任务 A" {
		t.Fatalf("新建会话首轮应完成：%+v", res)
	}
	sessionID, dialogID, taskID := str(res["session_id"]), str(res["dialog_id"]), str(res["task_id"])
	if !strings.HasPrefix(sessionID, "split-") || dialogID == "" || taskID == "" {
		t.Fatalf("新建应返回 split-* / dialog_id / task_id：%+v", res)
	}
	node := waitSessionTaskState(t, s, taskID, TaskStateDone, 5*time.Second)
	if node.Tool != "session_create" || node.Kind != TaskKindLLM ||
		node.SessionID != sessionID || node.TurnID == "" || node.TopSession != sessionID {
		t.Fatalf("新建任务节点异常：%+v", node)
	}
	if node.ParentID == "" || node.ParentID != toolNode.TaskID {
		t.Fatalf("有父轮时任务节点应挂调用工具节点之下：node=%+v tool=%s", node, toolNode.TaskID)
	}

	// ② 续跑：带既有 session_id → 同一对话、不新建；任务节点 Tool=session_send。
	raw2, _ := json.Marshal(map[string]any{
		"instance_id": "ins-test", "turn": "t-parent", "session": "s-parent",
		"session_id": sessionID, "prompt": "继续任务 A", "wait_sec": 10,
	})
	got2, err := h(context.Background(), nil, raw2)
	if err != nil {
		t.Fatalf("session.run(send): %v", err)
	}
	res2, _ := got2.(map[string]any)
	if str(res2["state"]) != "done" || str(res2["session_id"]) != sessionID {
		t.Fatalf("续跑应复用同一会话：%+v", res2)
	}
	if str(res2["dialog_id"]) != dialogID {
		t.Fatalf("续跑不应换对话：got %q want %q", str(res2["dialog_id"]), dialogID)
	}
	node2 := waitSessionTaskState(t, s, str(res2["task_id"]), TaskStateDone, 5*time.Second)
	if node2.Tool != "session_send" || node2.SessionID != sessionID {
		t.Fatalf("续跑任务节点异常：%+v", node2)
	}

	// ③ 显式未知会话 → not_found（不静默新建）。
	raw3, _ := json.Marshal(map[string]any{
		"instance_id": "ins-test", "turn": "t-parent", "session_id": "s-missing", "prompt": "x",
	})
	_, err = h(context.Background(), nil, raw3)
	if bridgeCode(err) != pluginbridge.CodeNotFound {
		t.Fatalf("未知会话应 not_found：%v", err)
	}
}

// TestSessionRunDetachedSkipsParentBarrier：派生轮次（barrier=false）不阻塞父轮终态，
// 但父轮取消仍能级联关闭它。
func TestSessionRunDetachedSkipsParentBarrier(t *testing.T) {
	gate := make(chan struct{})
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-gate:
		case <-r.Context().Done():
			return
		}
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": "后台完成"}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	}))
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	parent := sessionRunParentCtx(t, s, "s-parent2", "t-parent2")
	h := sessionRunHandler(t, s)

	raw, _ := json.Marshal(map[string]any{
		"instance_id": "ins-test", "turn": "t-parent2", "session": "s-parent2",
		"prompt": "后台任务", "title": "后台",
	})
	got, err := h(context.Background(), nil, raw)
	if err != nil {
		t.Fatalf("session.run: %v", err)
	}
	res, _ := got.(map[string]any)
	if str(res["state"]) != "running" {
		t.Fatalf("wait_sec=0 应立即返回 running：%+v", res)
	}

	// 子轮次仍在跑：父轮终态屏障必须**立即放行**（barrier=false）。
	returned := make(chan struct{})
	go func() { s.awaitDelegatedChildren(parent); close(returned) }()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("detached 派生轮次阻塞了父轮终态屏障")
	}

	// 父轮取消 → 级联关闭派生轮次（message 为 interrupted）。
	parent.cancelRequested.Store(true)
	s.closeDelegatedChildren("ins-test", "t-parent2")
	node := s.tasks.get(str(res["task_id"]))
	if node == nil {
		t.Fatalf("任务节点缺失")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		node = s.tasks.get(str(res["task_id"]))
		if node != nil && node.State == TaskStateCancelled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("取消级联未生效：%+v", node)
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(gate)
}

// TestSessionRunRejectsUnknownAgent：agent 拼错不得静默退化为全能力。
func TestSessionRunRejectsUnknownAgent(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	sessionRunParentCtx(t, s, "s-parent3", "t-parent3")
	h := sessionRunHandler(t, s)

	raw, _ := json.Marshal(map[string]any{
		"instance_id": "ins-test", "turn": "t-parent3", "session": "s-parent3",
		"prompt": "x", "agent": "no-such-agent",
	})
	_, err := h(context.Background(), nil, raw)
	if bridgeCode(err) != "agent_not_found" {
		t.Fatalf("未知 agent 应 agent_not_found：%v", err)
	}
}

// TestSessionOpenCap：session.open 解析 session→dialog 并调用注入的窗口打开口；无 GUI → unsupported。
func TestSessionOpenCap(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	resp, err := s.cfg.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: "ins-test", Scope: s.cfgScope("ins-test"), SessionID: "s-open", Title: "打开",
	})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	s.rememberDialog("ins-test", "s-open", resp.DialogID)

	var gotWD, gotSID, gotDID string
	s.SetChatWindowOpener(func(wd, sid, did string) (string, bool) {
		gotWD, gotSID, gotDID = wd, sid, did
		return "win-1", true
	})
	reg := pluginbridge.NewRegistry()
	s.SetPluginRegistry(reg)
	h, ok := reg.Lookup("plugin/v1/session.open")
	if !ok {
		t.Fatal("plugin/v1/session.open 未注册")
	}
	raw, _ := json.Marshal(map[string]any{"instance_id": "ins-test", "session_id": "s-open"})
	out, err := h(context.Background(), nil, raw)
	if err != nil {
		t.Fatalf("session.open: %v", err)
	}
	res, _ := out.(map[string]any)
	if str(res["window_id"]) != "win-1" || gotSID != "s-open" || gotDID != resp.DialogID || gotWD == "" {
		t.Fatalf("打开窗口参数异常: res=%+v wd=%q sid=%q did=%q", res, gotWD, gotSID, gotDID)
	}

	// headless（未注入 opener）→ unsupported。
	s.SetChatWindowOpener(nil)
	_, err = h(context.Background(), nil, raw)
	if bridgeCode(err) != pluginbridge.CodeUnsupported {
		t.Fatalf("无窗口能力应 unsupported：%v", err)
	}
}

// TestSessionEnsureIdempotentInDialog 覆盖续跑时 SessionEnsure 不新建对话（直接走 facade）。
func TestSessionEnsureIdempotentInDialog(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	resp, err := s.cfg.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: "ins-test", Scope: s.cfgScope("ins-test"), SessionID: "s-idem",
		Title: "幂等",
	})
	if err != nil {
		t.Fatalf("首次 ensure: %v", err)
	}
	again, err := s.cfg.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: "ins-test", Scope: s.cfgScope("ins-test"),
		SessionID: "s-idem", DialogID: resp.DialogID,
	})
	if err != nil || again.DialogID != resp.DialogID {
		t.Fatalf("二次 ensure 应复用同一对话：%+v %v", again, err)
	}
}
