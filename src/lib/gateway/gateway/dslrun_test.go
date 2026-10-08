// dsl_run 协议泵单测（不含真实 exe / 真实 LLM）：验证「喂入 llm_call → 调注入 LLM 回调 →
// 写回 llm_result」与「收 result 即返回」两条核心路径，另覆盖错误透传 / ctx 取消 /
// stdout 提前关闭。见 dslrun.go dslPump。
package mcpgateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// dslEvent 是测试捕获的一条上报事件（subject = task-* 相对主题）。
type dslEvent struct {
	subject string
	payload map[string]any
}

// TestDSLPumpLLMCallAndResult：一条 llm_call → 注入回调被调用并写回 llm_result；result 收尾返回。
func TestDSLPumpLLMCallAndResult(t *testing.T) {
	stdout := strings.NewReader(
		`{"t":"llm_call","call":"c1","agent":"后端开发","prompt":"实现 X","purpose":"实现 X","session":"job-1"}` + "\n" +
			`{"t":"log","level":"info","msg":"step done"}` + "\n" +
			`{"t":"result","job":"job-1","ok":true,"text":"全部完成","file":"","size":0,"error":"","overflow":false}` + "\n")
	var stdin strings.Builder
	calls := 0
	llm := func(_ context.Context, call dslLLMCallMsg) (string, error) {
		calls++
		if call.Call != "c1" || call.Agent != "后端开发" || call.Session != "job-1" {
			t.Fatalf("llm_call 解析错误: %+v", call)
		}
		return "hello-from-llm", nil
	}
	res, err := dslPump(context.Background(), stdout, &stdin, nil, llm, nil)
	if err != nil {
		t.Fatalf("dslPump err: %v", err)
	}
	if calls != 1 {
		t.Fatalf("LLM 回调调用次数 = %d，期望 1", calls)
	}
	if !res.OK || res.Text != "全部完成" {
		t.Fatalf("result 解析错误: %+v", res)
	}
	// 写回 stdin 的 llm_result
	var got dslLLMResultMsg
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdin.String())), &got); err != nil {
		t.Fatalf("llm_result 非合法 JSON: %q (%v)", stdin.String(), err)
	}
	if got.T != "llm_result" || got.Call != "c1" || got.Text != "hello-from-llm" || got.Error != "" {
		t.Fatalf("llm_result 内容错误: %+v", got)
	}
}

// TestDSLPumpLLMError：LLM 回调返回错误 → llm_result.error 落地，泵继续收到 result 正常返回。
func TestDSLPumpLLMError(t *testing.T) {
	stdout := strings.NewReader(
		`{"t":"llm_call","call":"c2","session":"job-2"}` + "\n" +
			`{"t":"result","job":"job-2","ok":false,"error":"子步骤失败"}` + "\n")
	var stdin strings.Builder
	llm := func(_ context.Context, _ dslLLMCallMsg) (string, error) { return "", errors.New("boom") }
	res, err := dslPump(context.Background(), stdout, &stdin, nil, llm, nil)
	if err != nil {
		t.Fatalf("dslPump err: %v", err)
	}
	if res.OK || res.Error != "子步骤失败" {
		t.Fatalf("result 解析错误: %+v", res)
	}
	var got dslLLMResultMsg
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdin.String())), &got); err != nil {
		t.Fatalf("llm_result 非合法 JSON: %q (%v)", stdin.String(), err)
	}
	if got.Error != "boom" {
		t.Fatalf("llm_result.error = %q，期望 boom", got.Error)
	}
}

// TestDSLPumpNoResult：stdout 关闭而未出 result → 明确错误。
func TestDSLPumpNoResult(t *testing.T) {
	var stdin strings.Builder
	_, err := dslPump(context.Background(), strings.NewReader(`{"t":"log","level":"info","msg":"x"}`+"\n"), &stdin, nil, nil, nil)
	if err == nil {
		t.Fatal("期望错误（未返回 result）")
	}
}

// TestDSLPumpCtxCancel：ctx 已取消 → 立即返回 ctx.Err（不误吞）。
func TestDSLPumpCtxCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdin strings.Builder
	_, err := dslPump(ctx, strings.NewReader(`{"t":"result","job":"j","ok":true}`+"\n"), &stdin, nil, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v，期望 context.Canceled", err)
	}
}

// TestDSLPumpTreeStepToTasktree：执行器发 tree + 2 次 step → 上报器转出正确的 task-* 事件
// （作业根 dsl_job + 容器 dsl_loop 的 loop_current 更新 + steps[]）——用注入的 emit 回调断言，
// 不连真实 data/任务层。
func TestDSLPumpTreeStepToTasktree(t *testing.T) {
	stdout := strings.NewReader(
		`{"t":"tree","job":"j","nodes":[{"id":"c1","parent":"","kind":"dsl_loop","label":"LOOP item","loop_current":0,"loop_total":0}]}` + "\n" +
			`{"t":"step","job":"j","no":1,"status":"running","purpose":"处理一项","session":"j-1","statement":"s1"}` + "\n" +
			`{"t":"step","job":"j","no":1,"status":"done","purpose":"处理一项","elapsed_ms":7,"session":"j-1","statement":"s1"}` + "\n" +
			`{"t":"tree","job":"j","nodes":[{"id":"c1","parent":"","kind":"dsl_loop","label":"LOOP item","loop_current":2,"loop_total":0}]}` + "\n" +
			`{"t":"step","job":"j","no":2,"status":"running","purpose":"处理一项","session":"j-2","statement":"s1"}` + "\n" +
			`{"t":"step","job":"j","no":2,"status":"done","purpose":"处理一项","elapsed_ms":9,"session":"j-2","statement":"s1"}` + "\n" +
			`{"t":"result","job":"j","ok":true,"text":"done","return_used":true}` + "\n")

	var stdin strings.Builder
	var events []dslEvent
	emit := func(subject string, payload map[string]any) { events = append(events, dslEvent{subject, payload}) }

	rep := newDSLJobReporter("j", "ins1", "top1", "tk-parent", "sess1", "turn1", "C:\\w", "tc1", emit)
	if _, err := dslPump(context.Background(), stdout, &stdin, nil, nil, rep); err != nil {
		t.Fatalf("dslPump err: %v", err)
	}

	rootID := "dsljob-j"
	loopID := "dsljob-j-c1"
	// ① 作业根 dsl_job（挂在 parent 下）。
	rootStart := findEvent(t, events, msgkeys.TopicTaskStarted, rootID)
	if rootStart["kind"] != dslJobKind || rootStart["parent_id"] != "tk-parent" ||
		rootStart["top_session"] != "top1" || rootStart["session_id"] != "sess1" ||
		rootStart["instance_id"] != "ins1" || rootStart["work_dir"] != "C:\\w" || rootStart["tool_call_id"] != "tc1" {
		t.Fatalf("作业根节点字段不符：%v", rootStart)
	}
	// ② 容器 dsl_loop（parent = 作业根）。
	loopStart := findEvent(t, events, msgkeys.TopicTaskStarted, loopID)
	if loopStart["kind"] != dslLoopKind || loopStart["parent_id"] != rootID || loopStart["loop_current"] != 0 {
		t.Fatalf("容器节点字段不符：%v", loopStart)
	}
	// ③ 容器 loop_current 更新（2，且为 task-updated）。
	loopUpd := findLastEvent(t, events, msgkeys.TopicTaskUpdated, loopID)
	if loopUpd["loop_current"] != 2 {
		t.Fatalf("容器 loop_current 未更新为 2：%v", loopUpd)
	}
	// ④ 作业根终态 task-done：state=done + steps[]（2 条）+ return_kind=inline。
	rootDone := findEvent(t, events, msgkeys.TopicTaskDone, rootID)
	if rootDone["state"] != "done" || rootDone["return_kind"] != "inline" || rootDone["return_inline"] != "done" {
		t.Fatalf("作业根终态字段不符：%v", rootDone)
	}
	steps, _ := rootDone["steps"].([]any)
	if len(steps) != 2 {
		t.Fatalf("steps 应含 2 条：%v", rootDone["steps"])
	}
	if s0, _ := steps[0].(map[string]any); s0["no"] != 1 || s0["status"] != "done" || s0["session_id"] != "j-1" || s0["statement_id"] != "s1" {
		t.Fatalf("步骤 1 行不符：%v", steps[0])
	}
	if s1, _ := steps[1].(map[string]any); s1["no"] != 2 || s1["session_id"] != "j-2" || s1["elapsed_ms"] != int64(9) {
		t.Fatalf("步骤 2 行不符：%v", steps[1])
	}
}

// findEvent 取首个 (subject, task_id) 命中的事件载荷（无 → Fatal）。
func findEvent(t *testing.T, events []dslEvent, subject, taskID string) map[string]any {
	t.Helper()
	for _, e := range events {
		if e.subject == subject && e.payload["task_id"] == taskID {
			return e.payload
		}
	}
	t.Fatalf("未找到事件 subject=%s task_id=%s（events=%v）", subject, taskID, events)
	return nil
}

// findLastEvent 取最后一个 (subject, task_id) 命中的事件载荷。
func findLastEvent(t *testing.T, events []dslEvent, subject, taskID string) map[string]any {
	t.Helper()
	var out map[string]any
	for _, e := range events {
		if e.subject == subject && e.payload["task_id"] == taskID {
			out = e.payload
		}
	}
	if out == nil {
		t.Fatalf("未找到事件 subject=%s task_id=%s", subject, taskID)
	}
	return out
}

// TestPickFinalAnswerOnlyFinalText：LLM 步骤返回值只含最终回答正文 ——
// 去 reasoning（独立字段）、去工具调用轮中间正文（带 tool_calls 的 assistant 行）、去 role=tool。
func TestPickFinalAnswerOnlyFinalText(t *testing.T) {
	msgs := []any{
		map[string]any{"role": "user", "content": "帮我看下文件"},
		// 工具轮：assistant 带 tool_calls（中间正文须剔除）——落库实现里本行同时含 content 与 tool_calls
		map[string]any{"role": "assistant", "content": "我先看下文件…",
			"reasoning": "让我读一下", "tool_calls": []any{map[string]any{"name": "file_read"}}},
		map[string]any{"role": "tool", "content": "文件内容 XYZ"},
		// 最终回答：无 tool_calls 的 assistant，reasoning 独立字段（不入正文）
		map[string]any{"role": "assistant", "content": "结论：文件是 XYZ", "reasoning": "综合分析后"},
	}
	got := pickFinalAnswer(msgs)
	if got != "结论：文件是 XYZ" {
		t.Fatalf("应只返回最终正文, got %q", got)
	}
	if strings.Contains(got, "我先看下文件") || strings.Contains(got, "XYZ\"") ||
		strings.Contains(got, "让我读一下") || strings.Contains(got, "综合分析后") {
		t.Fatalf("返回值混入了 reasoning / 工具内容: %q", got)
	}
}

// TestPickFinalAnswerMultiSegment：多段最终正文按换行拼接；纯 reasoning 行（content 空）被跳过。
func TestPickFinalAnswerMultiSegment(t *testing.T) {
	msgs := []any{
		map[string]any{"role": "assistant", "content": "第一段", "reasoning": "想"},
		map[string]any{"role": "assistant", "content": ""}, // 纯工具/空行 → 跳过
		map[string]any{"role": "assistant", "content": "第二段"},
	}
	if got := pickFinalAnswer(msgs); got != "第一段\n第二段" {
		t.Fatalf("多段拼接错: %q", got)
	}
}

// TestPickFinalAnswerEmpty：无 assistant 正文明 → 空串（不 panic）。
func TestPickFinalAnswerEmpty(t *testing.T) {
	if got := pickFinalAnswer(nil); got != "" {
		t.Fatalf("nil → 空串, got %q", got)
	}
	if got := pickFinalAnswer([]any{map[string]any{"role": "user", "content": "x"}}); got != "" {
		t.Fatalf("无 assistant → 空串, got %q", got)
	}
}
