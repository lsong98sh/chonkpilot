// 一致性校验（21 §9.2 P2 · verify.go）单测：零差异 / 有差异（仅层有、仅权威有、字段差异）。
package task

import (
	"encoding/json"
	"strings"
	"testing"
)

// seedTree 造一棵最小树（根 llm 节点 + 工具子节点，均落权威表）。
func seedTree(t *testing.T, l *Layer) {
	t.Helper()
	if err := l.Apply(subjTaskStarted, nodePayload(map[string]any{
		"task_id": "tk-root", "kind": "llm", "tool": "llm_run",
		"parent_id": "", "tool_call_id": "", "name": "主任务", "simplified": "主任务",
	})); err != nil {
		t.Fatalf("Apply(root): %v", err)
	}
	if err := l.Apply(subjTaskStarted, nodePayload(nil)); err != nil {
		t.Fatalf("Apply(tool): %v", err)
	}
	if err := l.Apply(subjTaskDone, nodePayload(map[string]any{
		"state": "done", "finished_at": "2026-09-18T00:01:00Z",
	})); err != nil {
		t.Fatalf("Apply(done): %v", err)
	}
}

// TestVerifyZeroDiff：层内记录 == 权威表行 → 零差异（single-writer 一致性证据）。
func TestVerifyZeroDiff(t *testing.T) {
	l, fake := newLayer(t)
	seedTree(t, l)

	rep, err := l.Verify("ins-test", "top-1")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !rep.Match() {
		t.Fatalf("应零差异:\n%s", rep.String())
	}
	if rep.LayerRows != 2 || rep.AuthoritativeRows != 2 {
		t.Fatalf("行数 layer=%d auth=%d want 2/2", rep.LayerRows, rep.AuthoritativeRows)
	}
	if !strings.Contains(rep.String(), "零差异") {
		t.Fatalf("报告文本=%q", rep.String())
	}
	if sval(fake.row("tk-1")["workdir"]) != "E:/ws" {
		t.Fatalf("权威行 workdir 应非空: %+v", fake.row("tk-1"))
	}
}

// TestVerifyDiff：层 / 权威两侧互相缺失 + 字段差异（诊断输出可读）。
func TestVerifyDiff(t *testing.T) {
	l, fake := newLayer(t)
	seedTree(t, l)

	// ① 字段差异：权威行 status 被外部改写（模拟两侧不一致）
	fake.setRowField("tk-1", "status", "error")
	// ② 仅权威有：权威多一行（非本层写入）
	fake.putRow(map[string]any{
		"node_id": "tk-auth-only", "task_id": "tk-auth-only", "node_type": "tool",
		"top_session": "top-1", "status": "done", "title": "权威独有",
	})
	// ③ 仅层有：新事件只进层视图（桩丢弃 upsert 失败分支不便制造 → 直接删权威行）
	fake.mu.Lock()
	delete(fake.rows, "tk-1")
	fake.mu.Unlock()

	rep, err := l.Verify("ins-test", "top-1")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.Match() {
		t.Fatal("应报告差异")
	}
	if len(rep.OnlyInLayer) != 1 || rep.OnlyInLayer[0] != "tk-1" {
		t.Fatalf("仅层有=%+v", rep.OnlyInLayer)
	}
	if len(rep.OnlyInAuthoritative) != 1 || rep.OnlyInAuthoritative[0] != "tk-auth-only" {
		t.Fatalf("仅权威有=%+v", rep.OnlyInAuthoritative)
	}
	s := rep.String()
	for _, want := range []string{"仅层有: tk-1", "仅权威有: tk-auth-only"} {
		if !strings.Contains(s, want) {
			t.Fatalf("报告缺 %q:\n%s", want, s)
		}
	}
}

// TestVerifyFieldDiff：字段级差异（层 vs 权威）。
func TestVerifyFieldDiff(t *testing.T) {
	l, fake := newLayer(t)
	seedTree(t, l)
	fake.setRowField("tk-1", "status", "error")
	rep, err := l.Verify("ins-test", "top-1")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	found := false
	for _, d := range rep.Diffs {
		if d.TaskID == "tk-1" && d.Field == "status" && d.Layer == "done" && d.Authoritative == "error" {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺 status 字段差异: %+v", rep.Diffs)
	}
	if !strings.Contains(rep.String(), "字段差异 tk-1.status") {
		t.Fatalf("报告文本=%q", rep.String())
	}
}

// TestVerifyPayloadMethod：`task-verify` 方法面载荷 / 返回（I-87，层内实现 `VerifyPayload`）：
// 零差异 → match=true 且 diffs=[]（JSON 为空数组，非 null）；构造一处字段不一致 → match=false 且
// diffs 非空；缺 top_session / 空载荷 → error 文本（match=false，不 panic）。
func TestVerifyPayloadMethod(t *testing.T) {
	l, fake := newLayer(t)
	seedTree(t, l)

	res := l.VerifyPayload(&VerifyRequest{InstanceID: "ins-test", TopSession: "top-1"})
	if res == nil || !res.Match || len(res.Diffs) != 0 || res.Rows != 2 || res.CachedRows != 2 || res.Error != "" {
		t.Fatalf("零差异应答错: %+v", res)
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"diffs":[]`) {
		t.Fatalf("diffs 应为空数组（非 null）: %s", b)
	}

	// 一处不一致：权威行 status 被外部改写（不经层事件）→ 字段级差异
	fake.setRowField("tk-1", "status", "error")
	res = l.VerifyPayload(&VerifyRequest{TopSession: "top-1"})
	if res.Match || len(res.Diffs) == 0 || res.Error != "" {
		t.Fatalf("应报告差异: %+v", res)
	}
	d := res.Diffs[0]
	if d.TaskID != "tk-1" || d.Field != "status" || d.Layer != "done" || d.Authoritative != "error" {
		t.Fatalf("差异条目错: %+v", res.Diffs)
	}

	// 缺 top_session / 空载荷 → error 文本（不 panic）
	for _, req := range []*VerifyRequest{nil, {}} {
		r := l.VerifyPayload(req)
		if r == nil || r.Match || r.Error == "" || r.Diffs == nil {
			t.Fatalf("非法载荷应答错: %+v", r)
		}
	}
}

// TestVerifyClosedNotMissing：逻辑删除行（closed）在校验读取（include_closed）中仍可见 →
// 不误报「仅权威有」。
func TestVerifyClosedNotMissing(t *testing.T) {
	l, fake := newLayer(t)
	seedTree(t, l)
	fake.mu.Lock()
	if row := fake.rows["tk-1"]; row != nil {
		row["closed"] = true
		row["deleted_at"] = "2026-09-18T09:00:00Z"
	}
	fake.mu.Unlock()
	// 层视图同步（task-deleted 事件即真实路径）
	if err := l.Apply(subjTaskDeleted, []byte(`{"node_id":"tk-1"}`)); err != nil {
		t.Fatalf("Apply(deleted): %v", err)
	}
	rep, err := l.Verify("ins-test", "top-1")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !rep.Match() {
		t.Fatalf("closed 行不应报差异:\n%s", rep.String())
	}
}
