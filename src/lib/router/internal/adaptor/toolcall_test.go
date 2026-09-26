// LR-6 白盒：按 `index` 的 tool_call 增量聚合器（toolcall.go）——含 LR-5 fixture 驱动，
// 以及四条反面错法各自的回归用例。
package adaptor

import (
	"encoding/json"
	"testing"

	"github.com/chonkpilot/chonkpilot-router/internal/canon"
)

// addFixtureToolCalls 把一条 OpenAI 兼容流式载荷里的 tool_calls 增量喂给聚合器（按 index）。
func addFixtureToolCalls(t *testing.T, acc *ToolCallAccumulator, payload string) {
	t.Helper()
	var chunk struct {
		Choices []struct {
			Delta struct {
				ToolCalls []struct {
					Index    *int   `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		t.Fatalf("载荷非 JSON: %v (payload=%s)", err, payload)
	}
	for _, ch := range chunk.Choices {
		for _, tc := range ch.Delta.ToolCalls {
			idx := 0
			if tc.Index != nil {
				idx = *tc.Index
			}
			acc.Add(idx, tc.ID, tc.Function.Name, tc.Function.Arguments)
		}
	}
}

// TestToolCallAccumulatorFixtureIndex：用 LR-5 金样本 `openai_toolcall_index` 驱动 ——
// 两枚并行调用（index 0/1）交叉到达、arguments 断在 JSON 中间，定稿后各自完整。
func TestToolCallAccumulatorFixtureIndex(t *testing.T) {
	var fx *Fixture
	for _, f := range loadTestFixtures(t) {
		if f.Name == "openai_toolcall_index" {
			fx = f
		}
	}
	if fx == nil {
		t.Fatal("缺素材 openai_toolcall_index")
	}
	payloads, err := decodeAll(t, &chunkReader{chunks: append([]string(nil), fx.Chunks...)})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	acc := NewToolCallAccumulator()
	for _, p := range payloads {
		if IsDone([]byte(p)) {
			continue
		}
		addFixtureToolCalls(t, acc, p)
	}
	calls, cerr := acc.Complete()
	if cerr != nil {
		t.Fatalf("Complete: %v", cerr)
	}
	if len(calls) != 2 {
		t.Fatalf("定稿条数=%d want 2（按 index 各自成一条）", len(calls))
	}
	want := []canon.ToolCall{
		{ID: "call_a", Name: "core_file_read", Arguments: `{"path":"a.txt"}`, Index: 0},
		{ID: "call_b", Name: "core_file_write", Arguments: `{"path":"b.txt"}`, Index: 1},
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls[%d]=%+v want %+v", i, calls[i], want[i])
		}
	}
}

// TestToolCallAccumulatorAvoidsPitfalls 覆盖四条反面错法 + 边界：
// ① 按 ID 聚合（id 后到/为空）② 分片一到就 parse ③ 空 name 覆盖 ④ 忽略 index。
func TestToolCallAccumulatorAvoidsPitfalls(t *testing.T) {
	// ① id 首片为空、第二片才到：按 index 仍能落位（若按 ID 聚合则两条会并成一条）。
	acc := NewToolCallAccumulator()
	acc.Add(0, "", "read", `{"p`)
	acc.Add(1, "", "write", `{"p`)
	acc.Add(0, "call_1", "", `ath":`)
	acc.Add(1, "call_2", "", `ath":`)
	acc.Add(0, "", "", `"a"}`)
	acc.Add(1, "", "", `"b"}`)
	calls, err := acc.Complete()
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(calls) != 2 || calls[0].ID != "call_1" || calls[1].ID != "call_2" {
		t.Fatalf("calls=%+v want 两条且 id 各自补齐", calls)
	}

	// ② 分片断在 JSON 中间：中途不得 parse（此处只需证明最终能拼成合法 JSON）
	if calls[0].Arguments != `{"path":"a"}` || calls[1].Arguments != `{"path":"b"}` {
		t.Fatalf("arguments 未拼装完成：%+v", calls)
	}

	// ③ 空 name 不覆盖已落位 name（后续分片只带 arguments）。
	if calls[0].Name != "read" || calls[1].Name != "write" {
		t.Fatalf("name 被空分片冲掉：%+v", calls)
	}

	// ④ 忽略 index：index 交叉到达（0,1,0,1）不能串味 —— 上面已断言两枚各自完整；再验顺序按首现。
	if calls[0].Index != 0 || calls[1].Index != 1 {
		t.Fatalf("index 记录错：%+v", calls)
	}
}

// TestToolCallAccumulatorEmptyAndEvents：无分片 → Empty / 静默；有分片 → Events 形状为
// `EvToolCall`（D-32：完整值）+ 完整 arguments + Index/ID/Name。
func TestToolCallAccumulatorEmptyAndEvents(t *testing.T) {
	empty := NewToolCallAccumulator()
	if !empty.Empty() {
		t.Fatal("空聚合器应报 Empty")
	}
	if calls, err := empty.Complete(); err != nil || calls != nil {
		t.Fatalf("空聚合器 Complete=(%+v,%v) want (nil,nil)", calls, err)
	}
	if evs, err := empty.Events("m"); err != nil || evs != nil {
		t.Fatalf("空聚合器 Events=(%+v,%v) want (nil,nil)", evs, err)
	}

	acc := NewToolCallAccumulator()
	acc.Add(2, "call_c", "core_file_read", "")
	if acc.Empty() {
		t.Fatal("有分片时不应报 Empty")
	}
	evs, err := acc.Events("gpt-x")
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("事件条数=%d want 1", len(evs))
	}
	ev := evs[0]
	if ev.Type != canon.EvToolCall || ev.Model != "gpt-x" || ev.ToolCallFull == nil {
		t.Fatalf("事件形状=%+v want tool_call + ToolCallFull（完整值）", ev)
	}
	tc := ev.ToolCallFull
	if tc.Index != 2 || tc.ID != "call_c" || tc.Name != "core_file_read" || tc.Arguments != emptyArguments {
		t.Fatalf("ToolCall=%+v want {2, call_c, core_file_read, %s}", tc, emptyArguments)
	}
}

// TestToolCallAccumulatorSetComplete：`Set` 以**完整值**收口（覆盖片段累加结果；空值不动）——
// Responses 协议的 `function_call_arguments.done` / `output_item.done` 走此路径。
func TestToolCallAccumulatorSetComplete(t *testing.T) {
	acc := NewToolCallAccumulator()
	acc.Add(0, "call_1", "f", `{"path":`)
	acc.Set(0, `{"path":"a.txt"}`) // 完整值收口
	acc.Set(0, "")                 // 空值不动（不把已落位值冲成空）
	calls, err := acc.Complete()
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(calls) != 1 || calls[0].Arguments != `{"path":"a.txt"}` {
		t.Fatalf("Complete=%+v want 完整值覆盖片段", calls)
	}
}

// TestToolCallAccumulatorInvalidJSON：arguments 拼完仍非合法 JSON → protocol（不静默交付半截参数）。
func TestToolCallAccumulatorInvalidJSON(t *testing.T) {
	acc := NewToolCallAccumulator()
	acc.Add(0, "call_1", "core_file_read", `{"path":`)
	if _, err := acc.Complete(); err == nil || err.Kind != canon.ErrorProtocol || err.Retryable {
		t.Fatalf("Complete err=%+v want protocol/不可重试", err)
	}
	if _, err := acc.Events("m"); err == nil {
		t.Fatal("Events 应透出定稿错误")
	}
}

// TestToolCallAccumulatorIndexAscendingOrder：输出顺序按 index **升序**（与 spec §LR §6-1 一致；
// 不依赖到达顺序 → 输出稳定）。
func TestToolCallAccumulatorIndexAscendingOrder(t *testing.T) {
	acc := NewToolCallAccumulator()
	acc.Add(3, "c3", "f3", "{}")
	acc.Add(1, "c1", "f1", "{}")
	calls, err := acc.Complete()
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(calls) != 2 || calls[0].Index != 1 || calls[1].Index != 3 {
		t.Fatalf("顺序=%+v want [1 3]（升序）", calls)
	}
}
