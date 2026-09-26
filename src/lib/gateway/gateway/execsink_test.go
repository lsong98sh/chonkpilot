// P3 执行态纳入任务层（21-任务层设计方案 §9.3 P3）白盒测试（package mcpgateway 内部）：
//   - P3-①：五处上报点（started / detached{auto} / detached{manual} / awaiting / 终态）经注入的
//     ExecSink 断言 phase/state/detail；未注入 sink（既有全部用例）→ no-op（行为不变）。
//   - 执行侧控制面（2026-09-18，取代 `tasks/status|list|cancel` + `tools/background`）：
//     `Gateway.CancelExec` / `Gateway.DetachExec` 复用 tm.cancel / tm.detach；取消前**先问层**
//     （层判不可逆终态 → 拒绝）；层未注入 / 未登记 → 回落池判定（行为等价）。
package mcpgateway

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// errUpstream 模拟上游调用失败（error 执行态上报的 message 来源）。
var errUpstream = errors.New("upstream call failed")

// fakeExecSink 记录执行态上报（P3-①）+ 可注入层权威状态（取消前问层断言）。
// CancelExec / DetachExec 由**装配方（llm）**实现为「转调 gateway 公开方法」；本 fake 只用于
// gateway 侧测试，故仅满足接口（不参与网关内部调用路径）。
type fakeExecSink struct {
	mu    sync.Mutex
	evs   []ExecState
	state map[string]string // gw_task_id → 层权威状态（注入）
}

func (f *fakeExecSink) OnExecState(ev ExecState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.evs = append(f.evs, ev)
}

func (f *fakeExecSink) ExecStateOf(gwTaskID string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.state[gwTaskID]
	return st, ok
}

// CancelExec / DetachExec：gateway 侧 fake（非装配方）——执行侧控制由测试直调 gateway 公开方法，
// 故此处不实现转发语义（返回明确错误，绝不静默成功）。
func (f *fakeExecSink) CancelExec(instanceID, gwTaskID string) (bool, error) {
	return false, errors.New("fake sink: CancelExec not wired (gateway-side test)")
}

func (f *fakeExecSink) DetachExec(instanceID, idOrCall string) (string, error) {
	return "", errors.New("fake sink: DetachExec not wired (gateway-side test)")
}

// setState 注入层权威状态；清空（第二返回 false）= 模拟层未登记。
func (f *fakeExecSink) setState(gwTaskID, st string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state == nil {
		f.state = map[string]string{}
	}
	if st == "" {
		delete(f.state, gwTaskID)
		return
	}
	f.state[gwTaskID] = st
}

// find 取某 phase 的首条上报（无 → nil）。
func (f *fakeExecSink) find(phase string) *ExecState {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.evs {
		if f.evs[i].Phase == phase {
			return &f.evs[i]
		}
	}
	return nil
}

// findWhere 取某 phase 下首个满足条件的上报（无 → nil）。
func (f *fakeExecSink) findWhere(phase string, pred func(ExecState) bool) *ExecState {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.evs {
		if f.evs[i].Phase == phase && pred(f.evs[i]) {
			return &f.evs[i]
		}
	}
	return nil
}

// waitPhase 等待某 phase 上报到达（5s）。
func (f *fakeExecSink) waitPhase(t *testing.T, phase string) ExecState {
	t.Helper()
	return f.waitWhere(t, phase, func(ExecState) bool { return true })
}

// waitWhere 等待某 phase 下首个满足条件的上报到达（5s）。
func (f *fakeExecSink) waitWhere(t *testing.T, phase string, pred func(ExecState) bool) ExecState {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ev := f.findWhere(phase, pred); ev != nil {
			return *ev
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.mu.Lock()
	evs := append([]ExecState{}, f.evs...)
	f.mu.Unlock()
	t.Fatalf("未收到符合条件的执行态上报（phase=%s，evs=%+v）", phase, evs)
	return ExecState{}
}

// newTestGWSink 同 newTestGW，但注入 ExecSink。
func newTestGWSink(t *testing.T, sink ExecSink) (mq.Bus, *Gateway) {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	g, err := New(Params{Bus: bus, CallTimeout: 5 * time.Second, MaxTasks: 8, Logf: t.Logf, ExecSink: sink})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := g.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		_ = g.Stop(context.Background())
		bus.Close()
	})
	return bus, g
}

// detailOf 取上报明细字段（int 值按 float64 断言）。
func detailOf(ev ExecState, key string) any {
	if ev.Detail == nil {
		return nil
	}
	return ev.Detail[key]
}

// TestExecStateReportPoints：P3-① 执行态上报点：① started（spawn 后）· ④ awaiting（超时待裁决）·
// ③ detached{trigger:manual}（DetachExec）· ② detached{trigger:auto,threshold_s} ·
// ⑤ error（上游调用失败，带简短 message）。done / cancelled 终态**不走**本通道（仍由既有
// mcp-tasks-report 承担 → 不造第二条终态通道）。
func TestExecStateReportPoints(t *testing.T) {
	sink := &fakeExecSink{}
	bus, g := newTestGWSink(t, sink)
	fk := newFakeProvider("sink", OriginBuiltin)
	addFakeProvider(t, g, fk, "run")
	evCh := subTimeout(t, bus)

	// ① manual：started → awaiting → detached{manual} → （放行后）终态 done
	out := gwCallAsync(bus, "tools/call", map[string]any{
		"name": "sink_run", "instance_id": "ins-sink", "tool_call_id": "tc-a", "top_session": "top-1",
		"arguments": map[string]any{"async": "manual", "timeout": float64(1)},
	})
	ev := sink.waitWhere(t, ExecPhaseStarted, func(ev ExecState) bool { return ev.ToolCallID == "tc-a" })
	// state = gateway 执行池快照（spawn 置 pending、执行 goroutine 随后置 running；层按 phase 落 running）
	if ev.GWTaskID == "" || (ev.State != "pending" && ev.State != "running") || ev.Tool != "sink_run" ||
		ev.ToolCallID != "tc-a" || ev.TopSession != "top-1" || ev.InstanceID != "ins-sink" || ev.At == "" {
		t.Fatalf("① started 上报字段错: %+v", ev)
	}
	// 启动点附 mode / threshold_s（层落 exec_json；provider_key 由池内任务快照带出）
	if detailOf(ev, "mode") != "manual" || detailOf(ev, "threshold_s") != float64(1) {
		t.Fatalf("① started 明细错: %+v", ev.Detail)
	}
	if ev.ProviderKey == "" {
		t.Fatalf("① started 应带 provider_key: %+v", ev)
	}

	to := waitTimeoutEvent(t, evCh)
	taskID, _ := to["task_id"].(string)
	ev = sink.waitWhere(t, ExecPhaseAwaiting, func(ev ExecState) bool { return ev.GWTaskID == taskID })
	if ev.GWTaskID != taskID || ev.State != "running" {
		t.Fatalf("④ awaiting 上报状态错（应保持 running 语义）: %+v", ev)
	}
	if detailOf(ev, "mode") != "manual" || detailOf(ev, "timeout_s") != float64(1) {
		t.Fatalf("④ awaiting 明细错: %+v", ev.Detail)
	}
	if opts, _ := detailOf(ev, "options").([]string); len(opts) != 2 || opts[0] != "detach" || opts[1] != "cancel" {
		t.Fatalf("④ awaiting.options 错: %+v", ev.Detail)
	}

	bgTaskID, derr := g.DetachExec("ins-sink", "tc-a")
	if derr != nil || bgTaskID != taskID {
		t.Fatalf("DetachExec: task=%q err=%v（want %q）", bgTaskID, derr, taskID)
	}
	ev = sink.waitWhere(t, ExecPhaseDetached, func(ev ExecState) bool { return ev.GWTaskID == taskID })
	if ev.GWTaskID != taskID || detailOf(ev, "trigger") != "manual" {
		t.Fatalf("③ detached{manual} 上报错: %+v", ev)
	}
	if got := <-out; got.err != nil {
		t.Fatalf("detach 后原调用应返回 pending: %v", got.err)
	}
	close(fk.release) // 放行上游调用 → 终态 done（经 mcp-tasks-report，不经执行态通道）
	if st := awaitTerminal(t, g, taskID); st["state"] != "done" {
		t.Fatalf("终态 state = %v, want done", st["state"])
	}
	if e := sink.find(ExecPhaseDone); e != nil {
		t.Fatalf("done 终态不得经执行态通道（应走 mcp-tasks-report）: %+v", e)
	}

	// ② auto：阈值命中 → detached{auto, threshold_s}
	fk2 := newFakeProvider("sink2", OriginBuiltin)
	addFakeProvider(t, g, fk2, "run")
	setToolMeta(t, g, "sink2_run", map[string]any{"async": "auto", "async-threshold": float64(1)})
	out2 := gwCallAsync(bus, "tools/call", map[string]any{
		"name": "sink2_run", "instance_id": "ins-sink", "tool_call_id": "tc-b",
	})
	deadline := time.Now().Add(6 * time.Second)
	var auto *ExecState
	for time.Now().Before(deadline) {
		if e := sink.findWhere(ExecPhaseDetached, func(ev ExecState) bool { return detailOf(ev, "trigger") == "auto" }); e != nil {
			auto = e
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if auto == nil {
		t.Fatal("② 未收到 detached{trigger:auto} 上报")
	}
	if detailOf(*auto, "threshold_s") != float64(1) || auto.State != "running" {
		t.Fatalf("② detached{auto} 明细错: %+v", auto)
	}
	if got := <-out2; got.err != nil {
		t.Fatalf("auto 转后台后原调用应返回 pending: %v", got.err)
	}
	close(fk2.release)

	// ⑤ error：上游调用失败 → 执行态 error（带简短 message，不含堆栈 → 层记 exec.error）
	fk3 := newFakeProvider("sink3", OriginBuiltin)
	fk3.setCallErr(errUpstream)
	addFakeProvider(t, g, fk3, "run")
	out3 := gwCallAsync(bus, "tools/call", map[string]any{
		"name": "sink3_run", "instance_id": "ins-sink", "tool_call_id": "tc-err",
		"arguments": map[string]any{"async": "manual", "timeout": float64(30)},
	})
	ev3 := sink.waitWhere(t, ExecPhaseError, func(ev ExecState) bool { return ev.ToolCallID == "tc-err" })
	if ev3.Message != errUpstream.Error() {
		t.Fatalf("⑤ error 上报缺 message（应=上游错误摘要）: %+v", ev3)
	}
	if got := <-out3; got.res == nil || got.res["isError"] != true {
		t.Fatalf("⑤ 上游失败应返回 isError 结果: %+v", got.res)
	}

	// ⑥ 取消：终态 cancelled 亦不经执行态通道（已有 mcp-tasks-report 回报路径）
	fk4 := newFakeProvider("sink4", OriginBuiltin)
	addFakeProvider(t, g, fk4, "run")
	out4 := gwCallAsync(bus, "tools/call", map[string]any{
		"name": "sink4_run", "instance_id": "ins-sink", "tool_call_id": "tc-c",
		"arguments": map[string]any{"async": "manual", "timeout": float64(30)},
	})
	ev4 := sink.waitWhere(t, ExecPhaseStarted, func(ev ExecState) bool { return ev.ToolCallID == "tc-c" })
	ok4, cerr := g.CancelExec("ins-sink", ev4.GWTaskID)
	if cerr != nil || !ok4 {
		t.Fatalf("CancelExec: ok=%v err=%v", ok4, cerr)
	}
	if st := awaitTerminal(t, g, ev4.GWTaskID); st["state"] != "cancelled" {
		t.Fatalf("取消后 state = %v, want cancelled", st["state"])
	}
	if e := sink.findWhere(ExecPhaseCancelled, func(ev ExecState) bool { return ev.GWTaskID == ev4.GWTaskID }); e != nil {
		t.Fatalf("cancelled 终态不得经执行态通道: %+v", e)
	}
	<-out4
}

// TestCancelExecLayerAuthoritative：控制面以层为权威（2026-09-18 口径）—— 层判不可逆终态 →
// `CancelExec` 拒绝（gateway 池仍非终态，证明以层为准）；层未登记 → 回落池判定（行为等价）。
// 状态查询（`tasks/status|list`）方法面已移除 → 状态归任务层 + message 表，本用例只覆盖取消判定。
func TestCancelExecLayerAuthoritative(t *testing.T) {
	sink := &fakeExecSink{}
	bus, g := newTestGWSink(t, sink)
	fk := newFakeProvider("cp", OriginBuiltin)
	addFakeProvider(t, g, fk, "run")

	out := gwCallAsync(bus, "tools/call", map[string]any{
		"name": "cp_run", "instance_id": "ins-cp", "tool_call_id": "tc-cp",
		"arguments": map[string]any{"async": "manual", "timeout": float64(30)},
	})
	ev := sink.waitPhase(t, ExecPhaseStarted)
	taskID := ev.GWTaskID

	// ① 层判终态 → 取消被拒（gateway 池仍 running：以层为准）
	sink.setState(taskID, "done")
	if ok, err := g.CancelExec("ins-cp", taskID); err == nil || ok {
		t.Fatalf("层终态应拒绝取消，got ok=%v err=%v", ok, err)
	} else if !strings.Contains(err.Error(), "terminal state: done") {
		t.Fatalf("拒绝原因应含层状态，got err=%v", err)
	}
	if st := poolStatus(t, g, taskID); st["state"] != "running" {
		t.Fatalf("gateway 池应仍为 running（证明取消被层拦下）: %v", st)
	}

	// ② 层未登记 → 回落 gateway 池判定（行为等价）+ 取消正常生效
	sink.setState(taskID, "")
	if ok, err := g.CancelExec("ins-cp", taskID); err != nil || !ok {
		t.Fatalf("层未登记取消应回落既有行为: ok=%v err=%v", ok, err)
	}
	<-out
}

// TestExecControlEdgeCases：执行侧控制面的边界（取代已移除的 `tasks/*` 方法面用例）：
//   - 空引用 / 未知引用 → 明确错误（不静默成功）；
//   - 重复 detach → 拒绝（已转后台）；
//   - 终态任务 cancel / detach → 拒绝（非终态才可操作）。
//
// instance 归属（2026-09-19 缺口 2）：控制面为**进程内调用**（调用方 = 任务层 / 装配方，按层节点
// 解析执行 id），仍不对总线开放；但执行池按 instance 分桶 → 调用方须带 instance 归属，
// 引用只在该 instance 桶内解析（跨实例不可见/不可取消）。
func TestExecControlEdgeCases(t *testing.T) {
	sink := &fakeExecSink{}
	bus, g := newTestGWSink(t, sink)
	fk := newFakeProvider("edge", OriginBuiltin)
	addFakeProvider(t, g, fk, "run")

	if _, err := g.CancelExec("", ""); err == nil {
		t.Fatal("CancelExec 空引用应报错")
	}
	if _, err := g.DetachExec("", ""); err == nil {
		t.Fatal("DetachExec 空引用应报错")
	}
	if _, err := g.CancelExec("", "tk-nope"); err == nil {
		t.Fatal("CancelExec 未知引用应报错")
	}
	if _, err := g.DetachExec("", "tc-nope"); err == nil {
		t.Fatal("DetachExec 未知引用应报错")
	}

	// manual：转后台两次 → 第二次拒绝
	out := gwCallAsync(bus, "tools/call", map[string]any{
		"name": "edge_run", "tool_call_id": "tc-edge",
		"arguments": map[string]any{"async": "manual", "timeout": float64(30)},
	})
	ev := sink.waitWhere(t, ExecPhaseStarted, func(ev ExecState) bool { return ev.ToolCallID == "tc-edge" })
	if tid, err := g.DetachExec("", "tc-edge"); err != nil || tid != ev.GWTaskID {
		t.Fatalf("DetachExec: task=%q err=%v", tid, err)
	}
	if _, err := g.DetachExec("", "tc-edge"); err == nil {
		t.Fatal("重复 detach 应被拒（已在后台）")
	}
	close(fk.release)
	if st := awaitTerminal(t, g, ev.GWTaskID); st["state"] != "done" {
		t.Fatalf("state = %v, want done", st["state"])
	}
	// 终态后 cancel / detach → 拒绝
	if ok, err := g.CancelExec("", ev.GWTaskID); err == nil || ok {
		t.Fatalf("终态 cancel 应被拒: ok=%v err=%v", ok, err)
	}
	if _, err := g.DetachExec("", ev.GWTaskID); err == nil {
		t.Fatal("终态 detach 应被拒")
	}
	<-out
}

// TestCancelExecByBothRefs：取消/转后台接受 **task_id 与 tool_call_id 双引用**（与既有方法面同口径）；
// 执行池按 instance 分桶 → **引用只在归属 instance 桶内解析**（2026-09-19 缺口 2）。
func TestCancelExecByBothRefs(t *testing.T) {
	sink := &fakeExecSink{}
	bus, g := newTestGWSink(t, sink)
	fk := newFakeProvider("refs", OriginBuiltin)
	addFakeProvider(t, g, fk, "run")

	outA := gwCallAsync(bus, "tools/call", map[string]any{
		"name": "refs_run", "instance_id": "ins-a", "tool_call_id": "tc-a",
		"arguments": map[string]any{"async": "manual", "timeout": float64(30)},
	})
	evA := sink.waitWhere(t, ExecPhaseStarted, func(ev ExecState) bool { return ev.ToolCallID == "tc-a" })
	if evA.InstanceID != "ins-a" {
		t.Fatalf("执行池归属 instance 错: %+v", evA)
	}
	outB := gwCallAsync(bus, "tools/call", map[string]any{
		"name": "refs_run", "instance_id": "ins-b", "tool_call_id": "tc-b",
		"arguments": map[string]any{"async": "manual", "timeout": float64(30)},
	})
	evB := sink.waitWhere(t, ExecPhaseStarted, func(ev ExecState) bool { return ev.ToolCallID == "tc-b" })

	// ① 按 tool_call_id 取消 A（归属 ins-a）
	if ok, err := g.CancelExec("ins-a", "tc-a"); err != nil || !ok {
		t.Fatalf("按 tool_call_id 取消: ok=%v err=%v", ok, err)
	}
	if st := awaitTerminal(t, g, evA.GWTaskID); st["state"] != "cancelled" {
		t.Fatalf("A state = %v, want cancelled", st["state"])
	}
	// ② 按 task_id 取消 B（归属 ins-b）
	if ok, err := g.CancelExec("ins-b", evB.GWTaskID); err != nil || !ok {
		t.Fatalf("按 task_id 取消: ok=%v err=%v", ok, err)
	}
	if st := awaitTerminal(t, g, evB.GWTaskID); st["state"] != "cancelled" {
		t.Fatalf("B state = %v, want cancelled", st["state"])
	}
	<-outA
	<-outB
}

// TestExecSinkNilNoPanic：未注入 ExecSink（nil）→ 全部执行态上报为 no-op：started / detached /
// awaiting / error 四条路径均**不 panic**、不改变既有行为；执行侧控制面（CancelExec / DetachExec）
// 不经 sink、直接可用（gateway 可独立运行，如 L3 / 单体 exe）。
func TestExecSinkNilNoPanic(t *testing.T) {
	bus, g := newTestGW(t, 5*time.Second) // newTestGW 不注入 sink
	fk := newFakeProvider("nilsink", OriginBuiltin)
	addFakeProvider(t, g, fk, "run")
	evCh := subTimeout(t, bus)

	// manual：started → awaiting → detached（DetachExec，不经 sink）
	out := gwCallAsync(bus, "tools/call", map[string]any{
		"name": "nilsink_run", "tool_call_id": "tc-nil",
		"arguments": map[string]any{"async": "manual", "timeout": float64(1)},
	})
	to := waitTimeoutEvent(t, evCh)
	taskID, _ := to["task_id"].(string)
	if tid, err := g.DetachExec("", "tc-nil"); err != nil || tid != taskID {
		t.Fatalf("DetachExec: task=%q err=%v（want %q）", tid, err, taskID)
	}
	<-out
	close(fk.release)
	if st := awaitTerminal(t, g, taskID); st["state"] != "done" {
		t.Fatalf("state = %v, want done", st["state"])
	}

	// 上游失败 → error 路径；取消 → cancelled 路径
	fkErr := newFakeProvider("nilsinkerr", OriginBuiltin)
	fkErr.setCallErr(errUpstream)
	addFakeProvider(t, g, fkErr, "run")
	if res := gwCall(t, bus, "tools/call", map[string]any{
		"name": "nilsinkerr_run", "arguments": map[string]any{"async": "manual", "timeout": float64(5)},
	}); res["isError"] != true {
		t.Fatalf("上游失败应返回 isError: %v", res)
	}
	fk2 := newFakeProvider("nilsink2", OriginBuiltin)
	addFakeProvider(t, g, fk2, "run")
	out2 := gwCallAsync(bus, "tools/call", map[string]any{
		"name": "nilsink2_run", "tool_call_id": "tc-nil2",
		"arguments": map[string]any{"async": "manual", "timeout": float64(30)},
	})
	select { // 等任务已在执行池登记（spawn 后上游调用开始）再转后台
	case <-fk2.started:
	case <-time.After(5 * time.Second):
		t.Fatal("上游调用未开始（任务未登记）")
	}
	tid, derr := g.DetachExec("", "tc-nil2")
	<-out2
	if derr != nil || tid == "" {
		t.Fatalf("DetachExec: task=%q err=%v", tid, derr)
	}
	if ok, err := g.CancelExec("", tid); err != nil || !ok {
		t.Fatalf("CancelExec: ok=%v err=%v", ok, err)
	}
	if st := awaitTerminal(t, g, tid); st["state"] != "cancelled" {
		t.Fatalf("state = %v, want cancelled", st["state"])
	}
}
