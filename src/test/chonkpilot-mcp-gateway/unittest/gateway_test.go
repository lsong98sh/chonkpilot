// Package mcpgatewaytest — chonkpilot-mcp-gateway 黑盒回归测试
// （工程目录 chonkpilot-test/chonkpilot-mcp-gateway/unittest）。
//
// 通过公开包 github.com/chonkpilot/chonkpilot-mcp-gateway/gateway 集成测试：
// 内存总线 + 自备能力源官方 go-sdk server（mock 工具，经 Params.MCPServer 参数传入）
// → New/Start → 经总线方法面（mcp-* 相对主题，chonk. 前缀由总线注入）驱动：
// 覆盖工具执行、自动异步（meta/请求级）、手动转后台、异步完成通知（mcp-tasks-report）、
// 任务取消、任务异常（不串扰返回数据）。
//
// 2026-09-18（用户决定）：`mcp-tasks-status|result|list|cancel` 与 `mcp-tools-background`
// 方法面**已移除** → 本套件改为：
//   - 取消 / 转后台经**执行侧公开方法**（`Gateway.CancelExec` / `Gateway.DetachExec`，进程内直调，
//     即装配方 sink 的落地入口）；
//   - 任务状态 / 结果以 **mcp-tasks-report 通知面**（`state` + `result_summary`=结果全文）断言。
package mcpgatewaytest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpgw "github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
)

// mockSpec 描述测试用 mock 工具（handler 本地执行，经 self 能力源 server 注册）。
type mockSpec struct {
	name   string
	desc   string
	meta   map[string]any
	handle func(ctx context.Context) (*mcp.CallToolResult, error)
}

// startTestGW 建内存总线 + 自备能力源官方 server（含 mock 工具）+ 真实 Gateway 装配。
func startTestGW(t *testing.T, specs []mockSpec) (mq.Bus, *mcpgw.Gateway) {
	t.Helper()
	ctx := context.Background()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	ms := mcp.NewServer(&mcp.Implementation{Name: "test-cap", Version: "1.0.0"}, nil)
	for _, s := range specs {
		h := s.handle
		ms.AddTool(&mcp.Tool{
			Name:        s.name,
			Description: s.desc,
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Meta:        s.meta,
		},
			func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return h(ctx)
			})
	}
	gw, err := mcpgw.New(mcpgw.Params{Bus: bus, MCPServer: ms, CallTimeout: 3 * time.Second, MaxTasks: 4, Logf: t.Logf})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := gw.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		_ = gw.Stop(context.Background())
		bus.Close()
	})
	return bus, gw
}

// subj 协议相对主题（对齐公开方法面：method → mcp-<组>-<动作>）。
func subj(method string) string { return "mcp-" + strings.ReplaceAll(method, "/", "-") }

// callMethod 经总线调用方法面并返回结果 map。
func callMethod(t *testing.T, bus mq.Bus, method string, payload map[string]any) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	v := bus.Emit(ctx, subj(method), payload).Wait()
	if err := v.Err(); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	m, _ := v.Result.(map[string]any)
	return m
}

// textOf 提取结果 content 首个 text 段。
func textOf(t *testing.T, res map[string]any) string {
	t.Helper()
	if content, ok := res["content"].([]any); ok && len(content) > 0 {
		if c, ok := content[0].(map[string]any); ok {
			if text, ok := c["text"].(string); ok {
				return text
			}
		}
	}
	return ""
}

// subReport 订阅异步完成回报（mcp-tasks-report；任务状态/结果的唯一可观测面）。
func subReport(t *testing.T, bus mq.Bus) chan map[string]any {
	t.Helper()
	ch := make(chan map[string]any, 8)
	sub, err := bus.On(mcpgw.SubjectTaskReport, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var r map[string]any
		if err := json.Unmarshal(v.Payload, &r); err == nil {
			ch <- r
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe report: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	return ch
}

// awaitReport 等待指定 task_id 的完成回报（5s）。
func awaitReport(t *testing.T, ch chan map[string]any, taskID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case r := <-ch:
			if r["task_id"] == taskID {
				return r
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatalf("task %s 未在 5s 内回报（mcp-tasks-report）", taskID)
	return nil
}

// pendingTaskID 从 pending 结果取 task_id。
func pendingTaskID(t *testing.T, res map[string]any) string {
	t.Helper()
	if sc, ok := res["structuredContent"].(map[string]any); ok {
		if id, ok := sc["task_id"].(string); ok && id != "" {
			return id
		}
	}
	t.Fatalf("result not pending: %v", res)
	return ""
}

func mockResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// ── 用例 ──────────────────────────────────────────────

// TestSyncCall 同步执行：tools/call 返回文本结果。
func TestSyncCall(t *testing.T) {
	bus, _ := startTestGW(t, []mockSpec{{
		name: "tool_sync", desc: "sync mock",
		handle: func(ctx context.Context) (*mcp.CallToolResult, error) {
			time.Sleep(30 * time.Millisecond)
			return mockResult("sync-ok"), nil
		},
	}})
	res := callMethod(t, bus, "tools/call", map[string]any{"name": "self_tool_sync"})
	if got := textOf(t, res); got != "sync-ok" {
		t.Fatalf("sync result = %q, want sync-ok (res=%v)", got, res)
	}
}

// TestAsyncByMeta 工具 meta async=always → tools/call 自动转后台（pending + 完成回报带结果全文）。
func TestAsyncByMeta(t *testing.T) {
	bus, _ := startTestGW(t, []mockSpec{{
		name: "tool_meta_async", desc: "async by meta", meta: map[string]any{"async": "always"},
		handle: func(ctx context.Context) (*mcp.CallToolResult, error) {
			time.Sleep(120 * time.Millisecond)
			return mockResult("meta-async-ok"), nil
		},
	}})
	ch := subReport(t, bus)
	res := callMethod(t, bus, "tools/call", map[string]any{"name": "self_tool_meta_async"})
	taskID := pendingTaskID(t, res)
	r := awaitReport(t, ch, taskID)
	if r["state"] != "done" || r["result_summary"] != "meta-async-ok" {
		t.Fatalf("report = %v, want state=done result_summary=meta-async-ok", r)
	}
}

// TestAsyncByRequest 请求级 async=true → 同步工具转后台。
func TestAsyncByRequest(t *testing.T) {
	bus, _ := startTestGW(t, []mockSpec{{
		name: "tool_sync2", desc: "sync2",
		handle: func(ctx context.Context) (*mcp.CallToolResult, error) {
			time.Sleep(60 * time.Millisecond)
			return mockResult("req-async-ok"), nil
		},
	}})
	ch := subReport(t, bus)
	res := callMethod(t, bus, "tools/call", map[string]any{
		"name": "self_tool_sync2", "arguments": map[string]any{"async": "true"},
	})
	taskID := pendingTaskID(t, res)
	r := awaitReport(t, ch, taskID)
	if r["state"] != "done" || r["result_summary"] != "req-async-ok" {
		t.Fatalf("report = %v, want state=done result_summary=req-async-ok", r)
	}
}

// TestBackgroundManual：manual 同步调用进行中 → **DetachExec**（执行侧公开方法，取代
// `tools/background` 方法面）解绑转后台 → 原同步调用返回 pending{task_id} → 完成回报 state=done。
func TestBackgroundManual(t *testing.T) {
	started := make(chan struct{})
	bus, gw := startTestGW(t, []mockSpec{{
		name: "tool_bg", desc: "manual background",
		handle: func(ctx context.Context) (*mcp.CallToolResult, error) {
			close(started)
			time.Sleep(400 * time.Millisecond)
			return mockResult("bg-ok"), nil
		},
	}})
	ch := subReport(t, bus)

	// manual 同步调用（缺省契约 manual：同步等待）→ 在 goroutine 内发射，主协程解绑
	type callOutcome struct {
		res map[string]any
		err error
	}
	outCh := make(chan callOutcome, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		v := bus.Emit(ctx, subj("tools/call"), map[string]any{
			"name": "self_tool_bg", "tool_call_id": "call-bg-1",
			"arguments": map[string]any{"async": "manual"},
		}).Wait()
		res, _ := v.Result.(map[string]any)
		outCh <- callOutcome{res: res, err: v.Err()}
	}()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("上游调用未开始")
	}
	// 解绑转后台（按 tool_call_id 命中）→ 返回 task_id
	taskID, err := gw.DetachExec("", "call-bg-1")
	if err != nil || taskID == "" {
		t.Fatalf("DetachExec: task=%q err=%v", taskID, err)
	}
	// 原同步调用应转为 pending{task_id}
	select {
	case out := <-outCh:
		if out.err != nil {
			t.Fatalf("manual call err: %v", out.err)
		}
		if got := pendingTaskID(t, out.res); got != taskID {
			t.Fatalf("manual pending task = %q, want %q (res=%v)", got, taskID, out.res)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("manual call did not detach within 3s")
	}
	r := awaitReport(t, ch, taskID)
	if r["state"] != "done" || r["result_summary"] != "bg-ok" {
		t.Fatalf("report = %v, want state=done result_summary=bg-ok", r)
	}
}

// TestBackgroundManualCompleted：manual 同步调用**未解绑** → 就地返回结果、无 pending、无完成回报。
func TestBackgroundManualCompleted(t *testing.T) {
	bus, _ := startTestGW(t, []mockSpec{{
		name: "tool_manual_sync", desc: "manual sync",
		handle: func(ctx context.Context) (*mcp.CallToolResult, error) {
			time.Sleep(30 * time.Millisecond)
			return mockResult("manual-sync-ok"), nil
		},
	}})
	reported := make(chan map[string]any, 1)
	sub, err := bus.On(mcpgw.SubjectTaskReport, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var r map[string]any
		if err := json.Unmarshal(v.Payload, &r); err == nil {
			reported <- r
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe report: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	res := callMethod(t, bus, "tools/call", map[string]any{
		"name": "self_tool_manual_sync", "tool_call_id": "call-manual-sync",
		"arguments": map[string]any{"async": "manual"},
	})
	if got := textOf(t, res); got != "manual-sync-ok" {
		t.Fatalf("manual sync result = %q, want manual-sync-ok (res=%v)", got, res)
	}
	if _, pending := res["structuredContent"]; pending {
		t.Fatalf("未解绑的 manual 调用不应返回 pending: %v", res)
	}
	select {
	case r := <-reported:
		t.Fatalf("未解绑的 manual 调用不应回报 mcp-tasks-report: %v", r)
	case <-time.After(150 * time.Millisecond):
	}
}

// TestBackgroundManualAfterDone：任务已终态 → DetachExec 失败（非终态才可解绑）。
func TestBackgroundManualAfterDone(t *testing.T) {
	bus, gw := startTestGW(t, []mockSpec{{
		name: "tool_done_bg", desc: "done",
		handle: func(ctx context.Context) (*mcp.CallToolResult, error) {
			time.Sleep(20 * time.Millisecond)
			return mockResult("done-ok"), nil
		},
	}})
	ch := subReport(t, bus)
	res := callMethod(t, bus, "tools/call", map[string]any{
		"name": "self_tool_done_bg", "tool_call_id": "call-done-bg",
		"arguments": map[string]any{"async": "true"},
	})
	taskID := pendingTaskID(t, res)
	awaitReport(t, ch, taskID) // 终态
	if _, err := gw.DetachExec("", "call-done-bg"); err == nil {
		t.Fatal("终态任务解绑应失败")
	}
}

// TestNeverNotOverridable 回归：契约 async=never 不可被调用级 async 覆盖（仍同步返回，不转后台）。
func TestNeverNotOverridable(t *testing.T) {
	bus, _ := startTestGW(t, []mockSpec{{
		name: "tool_never", desc: "never",
		meta: map[string]any{"async": "never"},
		handle: func(ctx context.Context) (*mcp.CallToolResult, error) {
			return mockResult("never-ok"), nil
		},
	}})
	res := callMethod(t, bus, "tools/call", map[string]any{
		"name": "self_tool_never", "arguments": map[string]any{"async": "always"},
	})
	if got := textOf(t, res); got != "never-ok" {
		t.Fatalf("never 应同步返回，got %q (res=%v)", got, res)
	}
	if _, pending := res["structuredContent"]; pending {
		t.Fatalf("never 不应转后台: %v", res)
	}
}

// TestNeverTimeoutAwaitsDecision 统一异步模型：never 到超时点 → 发 mcp-tools-timeout
// （options=[wait,cancel]，**不自动失败/不自动转后台**）→ tools/wait 撤销超时继续等并交付结果。
func TestNeverTimeoutAwaitsDecision(t *testing.T) {
	release := make(chan struct{})
	bus, _ := startTestGW(t, []mockSpec{{
		name: "tool_slow", desc: "slow",
		handle: func(ctx context.Context) (*mcp.CallToolResult, error) {
			select {
			case <-release:
				return mockResult("slow-ok"), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}})
	evCh := make(chan map[string]any, 1)
	sub, err := bus.On(mcpgw.SubjectToolsTimeout, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if err := json.Unmarshal(v.Payload, &m); err == nil {
			evCh <- m
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe mcp-tools-timeout: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	outCh := make(chan map[string]any, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		v := bus.Emit(ctx, subj("tools/call"), map[string]any{
			"name": "self_tool_slow", "tool_call_id": "call-never",
			"arguments": map[string]any{"async": "never", "timeout": 0.1},
		}).Wait()
		res, _ := v.Result.(map[string]any)
		outCh <- res
	}()

	var ev map[string]any
	select {
	case ev = <-evCh:
	case <-time.After(5 * time.Second):
		t.Fatalf("never 超时应发 mcp-tools-timeout")
	}
	if ev["reason"] != "timeout" {
		t.Fatalf("event reason = %v, want timeout (ev=%v)", ev["reason"], ev)
	}
	opts, _ := ev["options"].([]any)
	if len(opts) != 2 || opts[0] != "wait" || opts[1] != "cancel" {
		t.Fatalf("never options = %v, want [wait,cancel]", ev["options"])
	}
	taskID, _ := ev["task_id"].(string)
	if taskID == "" {
		t.Fatalf("event 缺 task_id: %v", ev)
	}
	// wait：撤销超时继续等
	w := callMethod(t, bus, "tools/wait", map[string]any{"task_id": taskID})
	if w["waiting"] != true {
		t.Fatalf("tools/wait 回执 = %v", w)
	}
	close(release)
	select {
	case res := <-outCh:
		if got := textOf(t, res); got != "slow-ok" {
			t.Fatalf("wait 后应继续等并交付结果, got %q (res=%v)", got, res)
		}
		if res["isError"] == true {
			t.Fatalf("wait 后不应失败: %v", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("wait 后调用未返回")
	}
}

// TestAutoTimeoutConverts 回归：auto 超 async-threshold 自动转后台（行为不变）。
func TestAutoTimeoutConverts(t *testing.T) {
	bus, _ := startTestGW(t, []mockSpec{{
		name: "tool_auto", desc: "auto",
		meta: map[string]any{"async": "auto", "async-threshold": 0.1},
		handle: func(ctx context.Context) (*mcp.CallToolResult, error) {
			time.Sleep(300 * time.Millisecond)
			return mockResult("auto-ok"), nil
		},
	}})
	ch := subReport(t, bus)
	res := callMethod(t, bus, "tools/call", map[string]any{"name": "self_tool_auto"})
	taskID := pendingTaskID(t, res)
	r := awaitReport(t, ch, taskID)
	if r["state"] != "done" || r["result_summary"] != "auto-ok" {
		t.Fatalf("report = %v, want state=done result_summary=auto-ok", r)
	}
}

// TestTaskReport 异步完成 → mcp-tasks-report 回报（state done + **result_summary = 结果全文**）。
func TestTaskReport(t *testing.T) {
	bus, _ := startTestGW(t, []mockSpec{{
		name: "tool_report", desc: "report",
		handle: func(ctx context.Context) (*mcp.CallToolResult, error) {
			time.Sleep(40 * time.Millisecond)
			return mockResult("report-ok"), nil
		},
	}})
	got := subReport(t, bus)
	// I-90：任务树归属随 tools/call 上下文下发 → 记入执行池 → 随完成回报回传（缺省为空）
	res := callMethod(t, bus, "tools/call", map[string]any{
		"name": "self_tool_report", "instance_id": "ins-report",
		"top_session": "top-report", "parent": "tk-report-parent",
		"arguments": map[string]any{"async": "true"},
	})
	taskID := pendingTaskID(t, res)
	r := awaitReport(t, got, taskID)
	if r["state"] != "done" {
		t.Fatalf("report = %v, want state=done", r)
	}
	if r["tool"] != "self_tool_report" {
		t.Fatalf("report tool = %v", r["tool"])
	}
	if r["top_session"] != "top-report" || r["parent"] != "tk-report-parent" {
		t.Fatalf("report 树归属字段错: top_session=%v parent=%v (payload=%v)", r["top_session"], r["parent"], r)
	}
	// 字段统一为 instance_id（用户裁定：不带 owner_instance_id）。
	if r["instance_id"] != "ins-report" {
		t.Fatalf("report instance_id = %v, want ins-report (payload=%v)", r["instance_id"], r)
	}
	if _, bad := r["owner_instance_id"]; bad {
		t.Fatalf("report 残留 owner_instance_id: %v", r)
	}
	// 2026-09-18：result_summary 承载**结果全文**（`tasks/result` 方法面移除后该通知面是唯一交付通道）
	if r["result_summary"] != "report-ok" {
		t.Fatalf("result_summary = %v, want report-ok（结果全文）", r["result_summary"])
	}
}

// TestMCPChangedCarriesInstanceID：目录/接入变化通知（mcp-gateway-changed）payload 必带
// instance_id（61-消息一览 §0 实例字段一律必带；注册方 instance_id 透传）。
func TestMCPChangedCarriesInstanceID(t *testing.T) {
	bus, _ := startTestGW(t, nil)
	ch := make(chan map[string]any, 4)
	sub, err := bus.On(mcpgw.SubjectMCPChanged, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var r map[string]any
		if err := json.Unmarshal(v.Payload, &r); err == nil {
			ch <- r
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe mcp-gateway-changed: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	callMethod(t, bus, "prompts/register", map[string]any{"name": "t-ins-prompt", "instance_id": "ins-change"})
	select {
	case r := <-ch:
		if r["instance_id"] != "ins-change" {
			t.Fatalf("mcp-gateway-changed instance_id = %v, want ins-change (payload=%v)", r["instance_id"], r)
		}
		if r["kind"] != "prompt" {
			t.Fatalf("mcp-gateway-changed kind = %v, want prompt (payload=%v)", r["kind"], r)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("no mcp-gateway-changed within 3s")
	}
}

// TestTaskCancel 任务终止：async running → **CancelExec**（执行侧公开方法，取代 `tasks/cancel`
// 方法面）→ cancelled 终态（完成回报 state=cancelled）。
func TestTaskCancel(t *testing.T) {
	bus, gw := startTestGW(t, []mockSpec{{
		name: "tool_long", desc: "long running",
		handle: func(ctx context.Context) (*mcp.CallToolResult, error) {
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return mockResult("long-ok"), nil
		},
	}})
	ch := subReport(t, bus)
	res := callMethod(t, bus, "tools/call", map[string]any{"name": "self_tool_long", "arguments": map[string]any{"async": "true"}})
	taskID := pendingTaskID(t, res)
	time.Sleep(50 * time.Millisecond) // 确保已 running
	ok, err := gw.CancelExec("", taskID)
	if err != nil || !ok {
		t.Fatalf("CancelExec: ok=%v err=%v", ok, err)
	}
	r := awaitReport(t, ch, taskID)
	if r["state"] != "cancelled" {
		t.Fatalf("report state = %v, want cancelled", r["state"])
	}
}

// TestTaskError 任务异常：工具返回 error → 回报 state=error + result_summary 含 error 信息；
// 同步调用返回 isError（不串其它数据）。
func TestTaskError(t *testing.T) {
	bus, _ := startTestGW(t, []mockSpec{{
		name: "tool_err", desc: "erroring",
		handle: func(ctx context.Context) (*mcp.CallToolResult, error) {
			return nil, fmt.Errorf("boom")
		},
	}})
	ch := subReport(t, bus)
	res := callMethod(t, bus, "tools/call", map[string]any{"name": "self_tool_err", "arguments": map[string]any{"async": "true"}})
	taskID := pendingTaskID(t, res)
	r := awaitReport(t, ch, taskID)
	if r["state"] != "error" {
		t.Fatalf("report state = %v, want error", r["state"])
	}
	if e, _ := r["result_summary"].(string); !strings.Contains(e, "boom") {
		t.Fatalf("report result_summary = %v, want contains boom", r["result_summary"])
	}
	sync := callMethod(t, bus, "tools/call", map[string]any{"name": "self_tool_err"})
	if sync["isError"] != true {
		t.Fatalf("sync error result not isError: %v", sync)
	}
	if got := textOf(t, sync); !strings.Contains(got, "boom") {
		t.Fatalf("sync error text = %q, want contains boom", got)
	}
}

// TestExecControlRemovedFaces：已移除的方法面（tasks/status|result|list|cancel、
// tools/background）**无消费者** → 经总线调用恒无回执数据（空 Result），且不影响主路径。
func TestExecControlRemovedFaces(t *testing.T) {
	bus, _ := startTestGW(t, []mockSpec{{name: "tool_x", desc: "x"}})
	for _, m := range []string{"tasks/status", "tasks/result", "tasks/list", "tasks/cancel", "tools/background"} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		v := bus.Emit(ctx, subj(m), map[string]any{"task_id": "tk-0001"}).Wait()
		cancel()
		if v.Err() != nil {
			t.Fatalf("%s 应无消费者（静默无回执），got err=%v", m, v.Err())
		}
		if v.Result != nil {
			t.Fatalf("%s 已移除，不应有回执: %v", m, v.Result)
		}
	}
}

// TestMCPFindAllNoQuery：mcp_find 无 query/purpose → 不再报缺参，返回该类型全部条目。
// 25 §5/T2（2026-09-25）：type 收敛为 tool|skill|resource|all → 本用例以 type=skill 列全部技能，
// 并断言 type=agent 已移出检索面（明确报错）。
func TestMCPFindAllNoQuery(t *testing.T) {
	bus, _ := startTestGW(t, nil)
	// 注册一个 skill 资产（scope 缺省 = global）
	callMethod(t, bus, "prompts/register", map[string]any{
		"name": "coder-sk", "description": "编码技能", "asset_kind": "skill", "content": "你是编码技能",
	})
	// 无 query / 无 purpose，仅 type=skill
	res := callMethod(t, bus, "tools/call", map[string]any{
		"name": "self_mcp_find", "arguments": map[string]any{"type": "skill"},
	})
	if res["isError"] == true {
		t.Fatalf("mcp_find 缺 query/purpose 不应报错: %v", textOf(t, res))
	}
	txt := textOf(t, res)
	if !strings.Contains(txt, `"coder-sk"`) {
		t.Fatalf("mcp_find(type=skill) 无 query 应返回全部技能，got %q", txt)
	}
	// 对照：带不匹配关键词 → 不返回该资产（关键词过滤仍生效）
	res2 := callMethod(t, bus, "tools/call", map[string]any{
		"name": "self_mcp_find", "arguments": map[string]any{"type": "skill", "query": "不存在的技能"},
	})
	if txt2 := textOf(t, res2); strings.Contains(txt2, `"coder-sk"`) {
		t.Fatalf("mcp_find 带关键词应过滤，got %q", txt2)
	}
	// 25 §5/T2：type=agent 已移出 LLM 检索面 → 明确报错（不再返回 agent 条目）
	res3 := callMethod(t, bus, "tools/call", map[string]any{
		"name": "self_mcp_find", "arguments": map[string]any{"type": "agent"},
	})
	if res3["isError"] != true {
		t.Fatalf("mcp_find(type=agent) 应明确报错（agent 已退出资产面），got %v", textOf(t, res3))
	}
}

// TestMCPFindPurposeAllTypes：purpose 语义推荐对**所有 type** 生效（非仅 tool/all）——
// type=skill 时同样走 llm-simple 推荐，且候选清单按 type 收敛（仅 skill 资产、不含普通工具）。
// 25 §5/T2（2026-09-25）：原 type=agent 改为 type=skill（agent 已移出检索面）。
func TestMCPFindPurposeAllTypes(t *testing.T) {
	bus, _ := startTestGW(t, []mockSpec{{name: "tool_x", desc: "普通工具"}})
	callMethod(t, bus, "prompts/register", map[string]any{
		"name": "coder-sk", "description": "编码技能", "asset_kind": "skill", "content": "你是编码技能",
	})
	// 假 llm-simple：捕获候选清单（system），返回带标记的推荐 JSON
	var gotSystem string
	if _, err := bus.On("llm-simple", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var req struct{ Prompt, System string }
		_ = json.Unmarshal(v.Payload, &req)
		gotSystem = req.System
		v.Result = map[string]any{"text": `{"tools":[{"name":"coder-sk","reason":"LLM-PICK"}]}`}
		return nil
	}); err != nil {
		t.Fatalf("subscribe llm-simple: %v", err)
	}
	res := callMethod(t, bus, "tools/call", map[string]any{
		"name": "self_mcp_find", "arguments": map[string]any{"type": "skill", "purpose": "写一段 Go 代码"},
	})
	if txt := textOf(t, res); !strings.Contains(txt, "LLM-PICK") {
		t.Fatalf("purpose 应走 LLM 语义推荐（type=skill），got %q", txt)
	}
	if !strings.Contains(gotSystem, "coder-sk(skill)") {
		t.Fatalf("候选清单应含 skill 资产，system=%q", gotSystem)
	}
	if strings.Contains(gotSystem, "tool_x") {
		t.Fatalf("type=skill 候选不应含普通工具，system=%q", gotSystem)
	}
}
