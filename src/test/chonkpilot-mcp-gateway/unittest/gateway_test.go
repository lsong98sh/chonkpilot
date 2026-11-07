// Package mcpgatewaytest — chonkpilot-mcp-gateway 黑盒回归测试
// （工程目录 chonkpilot-test/chonkpilot-mcp-gateway/unittest）。
//
// 通过公开包 github.com/lsong98sh/chonkpilot-agent/gateway 集成测试：
// 内存总线 + mock 工具经公开方法面 `tools/register`（handler_subject 回调）接入
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
	"sync"
	"testing"
	"time"

	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpgw "github.com/lsong98sh/chonkpilot-agent/gateway"
	"github.com/chonkpilot/chonkpilot-gwadapter/mqadapter"
)

// mockSpec 描述测试用 mock 工具（handler 本地执行，经 self 能力源 server 注册）。
type mockSpec struct {
	name   string
	desc   string
	meta   map[string]any
	handle func(ctx context.Context) (*mcp.CallToolResult, error)
}

// startTestGW 建内存总线 + 真实 Gateway 装配；mock 工具经**公开方法面** `tools/register`
// （handler_subject = 本测试订阅的回调主题）接入。
// GATEWAY-2 L5：gateway 只接 Transport + Servers，不再接受进程内能力源（Params.MCPServer 已删除）；
// 注册工具以**原名**暴露（P0a 2026-10-29：注册提供方 entry.ID=self 为内置 → 不再加 `self_` 前缀）。
func startTestGW(t *testing.T, specs []mockSpec) (mq.Bus, *mcpgw.Gateway) {
	t.Helper()
	ctx := context.Background()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	adapter := mqadapter.New(bus)
	gw, err := mcpgw.New(mcpgw.Params{Transport: adapter, Hooks: adapter.Hooks(), CallTimeout: 3 * time.Second, MaxTasks: 4, Logf: t.Logf})
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
	for _, s := range specs {
		spec := s
		handler := spec.handle
		subject := "test-tool-call-" + spec.name
		sub, err := bus.On(subject, 0, func(_ context.Context, _ string, v *mq.Value) error {
			res, herr := handler(ctx)
			if herr != nil {
				return herr
			}
			text := ""
			if res != nil {
				for _, c := range res.Content {
					if tc, ok := c.(*mcp.TextContent); ok {
						text = tc.Text
						break
					}
				}
			}
			v.Result = map[string]any{
				"content": []any{map[string]any{"type": "text", "text": text}},
				"isError": res != nil && res.IsError,
			}
			return nil
		})
		if err != nil {
			t.Fatalf("subscribe %s: %v", subject, err)
		}
		t.Cleanup(func() { _ = sub.Unsubscribe() })

		payload := map[string]any{
			"name": spec.name, "description": spec.desc, "handler_subject": subject,
		}
		for k, v := range spec.meta {
			switch k {
			case "async":
				payload["async"] = v
			case "async-threshold":
				payload["async_threshold"] = v
			case "timeout":
				payload["timeout"] = v
			}
		}
		callMethod(t, bus, "tools/register", payload)
	}
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
	sub, err := bus.On(msgkeys.TopicMcpTasksReport, 0, func(_ context.Context, _ string, v *mq.Value) error {
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
	res := callMethod(t, bus, "tools/call", map[string]any{"name": "tool_sync"})
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
	res := callMethod(t, bus, "tools/call", map[string]any{"name": "tool_meta_async"})
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
		"name": "tool_sync2", "arguments": map[string]any{"async": "true"},
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
			"name": "tool_bg", "tool_call_id": "call-bg-1",
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
	sub, err := bus.On(msgkeys.TopicMcpTasksReport, 0, func(_ context.Context, _ string, v *mq.Value) error {
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
		"name": "tool_manual_sync", "tool_call_id": "call-manual-sync",
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
		"name": "tool_done_bg", "tool_call_id": "call-done-bg",
		"arguments": map[string]any{"async": "true"},
	})
	taskID := pendingTaskID(t, res)
	awaitReport(t, ch, taskID) // 终态
	if _, err := gw.DetachExec("", "call-done-bg"); err == nil {
		t.Fatal("终态任务解绑应失败")
	}
}

// TestContractNeverOverridableByCallAlways（INBOX Q1）：契约 async=never **允许**被调用级
// `async=always` 覆盖 → 派发即转后台（pending + task_id），主循环让出以便插话消费。
func TestContractNeverOverridableByCallAlways(t *testing.T) {
	bus, _ := startTestGW(t, []mockSpec{{
		name: "tool_never", desc: "never",
		meta: map[string]any{"async": "never"},
		handle: func(ctx context.Context) (*mcp.CallToolResult, error) {
			return mockResult("never-ok"), nil
		},
	}})
	res := callMethod(t, bus, "tools/call", map[string]any{
		"name": "tool_never", "arguments": map[string]any{"async": "always"},
	})
	sc, _ := res["structuredContent"].(map[string]any)
	if sc == nil || sc["status"] != "pending" || sc["task_id"] == "" {
		t.Fatalf("契约 never + 调用级 always 应转后台（pending+task_id），got %v", res)
	}
}

// subTimeoutEvent 订阅超时裁决事件（mcp-tools-timeout）。
func subTimeoutEvent(t *testing.T, bus mq.Bus) chan map[string]any {
	t.Helper()
	ch := make(chan map[string]any, 4)
	sub, err := bus.On(msgkeys.TopicMcpToolsTimeout, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if err := json.Unmarshal(v.Payload, &m); err == nil {
			ch <- m
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe mcp-tools-timeout: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	return ch
}

// callAsync 在 goroutine 内发出 tools/call 并返回结果通道（避免阻塞主协程的裁决动作）。
func callAsync(t *testing.T, bus mq.Bus, payload map[string]any) chan map[string]any {
	t.Helper()
	outCh := make(chan map[string]any, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		v := bus.Emit(ctx, subj("tools/call"), payload).Wait()
		res, _ := v.Result.(map[string]any)
		outCh <- res
	}()
	return outCh
}

// awaitAwaiting 等待 tools/call 返回 awaiting 载荷并按新契约（2026-11-06）校验形状：
// **超时点立即返回占位**（不等用户裁决），字段 = status/task_id/reason/timeout_s/options，
// 文本 = 任务等待用户裁决，不置 isError；真实结果此后只经 mcp-tasks-report 交付一次。
func awaitAwaiting(t *testing.T, outCh chan map[string]any) (string, map[string]any) {
	t.Helper()
	var res map[string]any
	select {
	case res = <-outCh:
	case <-time.After(2 * time.Second):
		t.Fatal("manual/never 到超时点后 tools/call 未立即返回 awaiting（仍在阻塞等裁决）")
	}
	sc, ok := res["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("awaiting 返回缺 structuredContent: %v", res)
	}
	if sc["status"] != "awaiting" {
		t.Fatalf("structuredContent.status = %v, want awaiting（res=%v）", sc["status"], res)
	}
	if sc["reason"] != "timeout" {
		t.Fatalf("awaiting reason = %v, want timeout (sc=%v)", sc["reason"], sc)
	}
	if ts, _ := sc["timeout_s"].(float64); ts != 0.1 {
		t.Fatalf("awaiting timeout_s = %v, want 0.1 (sc=%v)", sc["timeout_s"], sc)
	}
	if opts, _ := sc["options"].([]any); len(opts) != 2 || opts[0] != "wait" || opts[1] != "cancel" {
		t.Fatalf("never awaiting options = %v, want [wait,cancel]", sc["options"])
	}
	taskID, _ := sc["task_id"].(string)
	if taskID == "" {
		t.Fatalf("awaiting 返回缺 task_id: %v", res)
	}
	if res["isError"] == true {
		t.Fatalf("awaiting 不是失败，不应置 isError: %v", res)
	}
	if got := textOf(t, res); got != "任务等待用户裁决" {
		t.Fatalf("awaiting 文本 = %q, want 任务等待用户裁决 (res=%v)", got, res)
	}
	return taskID, sc
}

// TestNeverTimeoutAwaitsDecision 统一异步模型（**2026-11-06 起「裁决不再阻塞」**）：
// never 到超时点 → 发 mcp-tools-timeout{options:[wait,cancel]} 且 tools/call **立即返回
// awaiting 占位**（不是最终结果、不置 isError）；tools/wait = 撤销 awaiting（不产生第二次
// 交付、不重新计时）；真实结果最终经 mcp-tasks-report **恰一次**交付。
//
// 旧契约（wait 唤醒原阻塞调用、由 tools/call 交付最终结果）已被规格 18 §4「裁决不再阻塞」
// 与 agent 仓 async_await_test.go（TestAwaitingWaitRevokesThenReport）取代。
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
	evCh := subTimeoutEvent(t, bus)
	reportCh := subReport(t, bus)

	outCh := callAsync(t, bus, map[string]any{
		"name": "tool_slow", "tool_call_id": "call-never",
		"arguments": map[string]any{"async": "never", "timeout": 0.1},
	})

	// ① 超时点：调用**立即**交付 awaiting 占位，事件同步给出 wait/cancel 选项。
	taskID, _ := awaitAwaiting(t, outCh)
	select {
	case ev := <-evCh:
		if ev["reason"] != "timeout" {
			t.Fatalf("event reason = %v, want timeout (ev=%v)", ev["reason"], ev)
		}
		if ev["task_id"] != taskID {
			t.Fatalf("event task_id = %v, want %s（与 awaiting 一致）", ev["task_id"], taskID)
		}
		if opts, _ := ev["options"].([]any); len(opts) != 2 || opts[0] != "wait" || opts[1] != "cancel" {
			t.Fatalf("never options = %v, want [wait,cancel]", ev["options"])
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("never 超时应发 mcp-tools-timeout")
	}

	// ② wait：确认继续等待 → 撤销 awaiting；不产生第二次返回（占位已交付）。
	w := callMethod(t, bus, "tools/wait", map[string]any{"task_id": taskID})
	if w["waiting"] != true || w["task_id"] != taskID {
		t.Fatalf("tools/wait 回执 = %v, want {waiting:true, task_id:%s}", w, taskID)
	}
	select {
	case res := <-outCh:
		t.Fatalf("wait 后不应再交付占位/结果（占位已在超时点交付）: %v", res)
	case <-time.After(100 * time.Millisecond):
	}

	// ③ 超时已撤销：再次 wait → 拒绝（已非待裁决态）。
	if err := callMethodErr(t, bus, "tools/wait", map[string]any{"task_id": taskID}); err == nil {
		t.Fatal("wait 后 awaiting 应已撤销，第二次 tools/wait 应被拒绝")
	}
	// ④ 撤销后不重新计时：继续等待超过原超时点也不再发裁决（不重复询问）。
	select {
	case ev := <-evCh:
		t.Fatalf("wait 撤销后不应再次发 mcp-tools-timeout: %v", ev)
	case <-time.After(400 * time.Millisecond):
	}

	// ⑤ 最终结果按规格经 mcp-tasks-report 恰一次到达（全文 + 归属字段）。
	close(release)
	r := awaitReport(t, reportCh, taskID)
	if r["state"] != "done" {
		t.Fatalf("report state = %v, want done (report=%v)", r["state"], r)
	}
	if r["result_summary"] != "slow-ok" {
		t.Fatalf("result_summary = %v, want slow-ok（结果全文）", r["result_summary"])
	}
	if r["tool"] != "tool_slow" {
		t.Fatalf("report tool = %v, want tool_slow", r["tool"])
	}
	if r["tool_call_id"] != "call-never" {
		t.Fatalf("report tool_call_id = %v, want call-never", r["tool_call_id"])
	}
	select {
	case dup := <-reportCh:
		t.Fatalf("终态只应回报一次，却收到重复: %v", dup)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestNeverTimeoutCancelDeliversImmediately 负例（选 cancel）：awaiting 后取消 → CancelExec
// 立即成功回执，执行终态 cancelled 经 mcp-tasks-report **立即**交付（不为等待执行体响应而
// 阻塞——工具 handler 仍在等外部释放）；不产生 done 回报、也不再有第二次占位交付。
func TestNeverTimeoutCancelDeliversImmediately(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	closeRelease := func() { once.Do(func() { close(release) }) }
	t.Cleanup(closeRelease) // LIFO：先于 gateway Stop 释放 handler，避免 goroutine 泄漏到 Stop

	bus, gw := startTestGW(t, []mockSpec{{
		name: "tool_slow_cancel", desc: "slow cancel",
		handle: func(ctx context.Context) (*mcp.CallToolResult, error) {
			select {
			case <-release:
				return mockResult("slow-cancel-ok"), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}})
	evCh := subTimeoutEvent(t, bus)
	reportCh := subReport(t, bus)

	outCh := callAsync(t, bus, map[string]any{
		"name": "tool_slow_cancel", "tool_call_id": "call-cancel",
		"arguments": map[string]any{"async": "never", "timeout": 0.1},
	})
	taskID, _ := awaitAwaiting(t, outCh)
	select {
	case <-evCh:
	case <-time.After(5 * time.Second):
		t.Fatalf("never 超时应发 mcp-tools-timeout")
	}

	// 选 cancel：立即回执（不等待执行体退出）。
	start := time.Now()
	ok, err := gw.CancelExec("", taskID)
	if err != nil || !ok {
		t.Fatalf("CancelExec: ok=%v err=%v", ok, err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("CancelExec 耗时 %v（应即时返回）", d)
	}
	// 终态立即经 report 交付（执行体未响应也不拖住回报）。
	r := awaitReport(t, reportCh, taskID)
	if r["state"] != "cancelled" {
		t.Fatalf("report state = %v, want cancelled (report=%v)", r["state"], r)
	}
	select {
	case dup := <-reportCh:
		t.Fatalf("取消终态只应回报一次，却收到重复: %v", dup)
	case <-time.After(300 * time.Millisecond):
	}
	select {
	case res := <-outCh:
		t.Fatalf("cancel 不应再向原调用交付第二次结果: %v", res)
	case <-time.After(100 * time.Millisecond):
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
	res := callMethod(t, bus, "tools/call", map[string]any{"name": "tool_auto"})
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
		"name": "tool_report", "instance_id": "ins-report",
		"top_session": "top-report", "parent": "tk-report-parent",
		"arguments": map[string]any{"async": "true"},
	})
	taskID := pendingTaskID(t, res)
	r := awaitReport(t, got, taskID)
	if r["state"] != "done" {
		t.Fatalf("report = %v, want state=done", r)
	}
	if r["tool"] != "tool_report" {
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
	sub, err := bus.On(msgkeys.TopicMcpGatewayChanged, 0, func(_ context.Context, _ string, v *mq.Value) error {
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
	res := callMethod(t, bus, "tools/call", map[string]any{"name": "tool_long", "arguments": map[string]any{"async": "true"}})
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
	res := callMethod(t, bus, "tools/call", map[string]any{"name": "tool_err", "arguments": map[string]any{"async": "true"}})
	taskID := pendingTaskID(t, res)
	r := awaitReport(t, ch, taskID)
	if r["state"] != "error" {
		t.Fatalf("report state = %v, want error", r["state"])
	}
	if e, _ := r["result_summary"].(string); !strings.Contains(e, "boom") {
		t.Fatalf("report result_summary = %v, want contains boom", r["result_summary"])
	}
	sync := callMethod(t, bus, "tools/call", map[string]any{"name": "tool_err"})
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
