// 会话快照消息面单测（data-snapshot-get / data-snapshot-set，61-消息一览 §3 数据面）。
//
// 背景（阶段 4 试点）：原 `persist.snapshot_store` 的导出访问器 SetSnapshot/GetSnapshot
// 已下沉 `chonkpilot-data/internal/snapshot`（模块外不可达），本文件改为**经 MQ 驱动**
// 覆盖同一批语义（字段保真 / 保留记录其他字段 / 覆盖写）；门面（inline 绑定）与 MQ 面的
// 等价性见同目录 facade_snapshot_test.go。
package persist_test

import (
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// TestSnapshotMQFieldFidelity：快照往返保真——kind / tool_calls（含 function.name+arguments）/
// reasoning / _meta 逐项不丢（压缩的"读改写"依赖这些字段原样过面）。
func TestSnapshotMQFieldFidelity(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)

	snap := map[string]any{
		"snapshot_turn": "t3",
		"history": []any{
			map[string]any{"role": "system", "content": "[已压缩早前对话] 用户问过 A"},
			map[string]any{"role": "user", "kind": "text", "content": "继续"},
			map[string]any{"role": "assistant", "reasoning": "先看文件", "content": "好", "tool_calls": []any{
				map[string]any{"id": "c1", "type": "function", "function": map[string]any{
					"name": "file_read", "arguments": `{"path":"a.go"}`,
				}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "c1", "content": "package main", "_meta": map[string]any{
				"category": "fs", "async": "never",
			}},
		},
	}
	dataCall(t, bus, "data-snapshot-set", map[string]any{
		"instance_id": "ins-test", "data": map[string]any{"session_id": "s-fid", "snapshot": snap},
	})
	res := dataResult(t, dataCall(t, bus, "data-snapshot-get", map[string]any{
		"instance_id": "ins-test", "data": map[string]any{"session_id": "s-fid"},
	}))
	got, _ := res["snapshot"].(map[string]any)
	if got == nil {
		t.Fatalf("snapshot missing: %+v", res)
	}
	if got["snapshot_turn"] != "t3" {
		t.Fatalf("snapshot_turn=%v, want t3", got["snapshot_turn"])
	}
	hist, _ := got["history"].([]any)
	if len(hist) != 4 {
		t.Fatalf("history len=%d, want 4", len(hist))
	}
	userMsg, _ := hist[1].(map[string]any)
	if userMsg["kind"] != "text" || userMsg["role"] != "user" {
		t.Fatalf("kind 丢失: %+v", hist[1])
	}
	assistantMsg, _ := hist[2].(map[string]any)
	if assistantMsg["reasoning"] != "先看文件" {
		t.Fatalf("reasoning 丢失: %+v", hist[2])
	}
	tcs, _ := assistantMsg["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("tool_calls 丢失: %+v", hist[2])
	}
	tc, _ := tcs[0].(map[string]any)
	fn, _ := tc["function"].(map[string]any)
	if tc["id"] != "c1" || fn["name"] != "file_read" || fn["arguments"] != `{"path":"a.go"}` {
		t.Fatalf("tool_call 字段丢失: %+v", tc)
	}
	toolMsg, _ := hist[3].(map[string]any)
	if toolMsg["tool_call_id"] != "c1" {
		t.Fatalf("tool_call_id 丢失: %+v", hist[3])
	}
	if toolMsg["_meta"] == nil {
		t.Fatalf("_meta 丢失: %+v", hist[3])
	}
}

// TestSnapshotMQKeepsSessionFields：快照写回**只动 history/snapshot_turn 两列**，
// 会话记录的其他字段（title/created_at）原样保留（sessions 表是会话与快照共用的记录）。
func TestSnapshotMQKeepsSessionFields(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)

	dataCall(t, bus, "data-session-ensure-session", map[string]any{
		"instance_id": "ins-test", "data": map[string]any{"session_id": "s-keep"},
	})
	before := snapshotSessionRecord(t, bus, "s-keep")
	if before == nil || before["title"] == nil || before["created_at"] == nil {
		t.Fatalf("ensure-session 未落 title/created_at: %+v", before)
	}

	dataCall(t, bus, "data-snapshot-set", map[string]any{
		"instance_id": "ins-test", "data": map[string]any{"session_id": "s-keep", "snapshot": map[string]any{
			"snapshot_turn": "t1",
			"history":       []any{map[string]any{"role": "user", "content": "hi"}},
		}},
	})

	after := snapshotSessionRecord(t, bus, "s-keep")
	if after == nil {
		t.Fatal("快照写回后会话记录丢失")
	}
	for _, k := range []string{"title", "created_at"} {
		if after[k] != before[k] {
			t.Fatalf("快照写回改动了记录其他字段 %s：before=%v after=%v", k, before[k], after[k])
		}
	}
}

// TestSnapshotMQOverwrite：二次写回覆盖（turn 与 history 一并替换，非合并）。
func TestSnapshotMQOverwrite(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)

	set := func(turn, content string) {
		t.Helper()
		dataCall(t, bus, "data-snapshot-set", map[string]any{
			"instance_id": "ins-test", "data": map[string]any{"session_id": "s-ow", "snapshot": map[string]any{
				"snapshot_turn": turn,
				"history":       []any{map[string]any{"role": "user", "content": content}},
			}},
		})
	}
	set("t1", "a")
	set("t2", "b")

	res := dataResult(t, dataCall(t, bus, "data-snapshot-get", map[string]any{
		"instance_id": "ins-test", "data": map[string]any{"session_id": "s-ow"},
	}))
	got, _ := res["snapshot"].(map[string]any)
	if got == nil || got["snapshot_turn"] != "t2" {
		t.Fatalf("覆盖写失败: %+v", res)
	}
	hist, _ := got["history"].([]any)
	if len(hist) != 1 {
		t.Fatalf("history 应为覆盖后的 1 条，实际 %d", len(hist))
	}
	if m, _ := hist[0].(map[string]any); m["content"] != "b" {
		t.Fatalf("history 未覆盖: %+v", hist[0])
	}
}

// snapshotSessionRecord 经 data-session-get 读会话记录（result.data 视图；不存在 → nil）。
func snapshotSessionRecord(t *testing.T, bus mq.Bus, id string) map[string]any {
	t.Helper()
	res := dataResult(t, dataCall(t, bus, "data-session-get", map[string]any{
		"instance_id": "ins-test", "data": map[string]any{"session_id": id},
	}))
	rec, _ := res["data"].(map[string]any)
	return rec
}
