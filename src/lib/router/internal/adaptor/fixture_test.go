// LR-5 白盒：fixture 规范与加载器（fixture.go + testdata/*.sse.json）——
// 素材加载、SSE 解码期望比对、错误体期望比对、tool_call 多段分片按 index 聚合、场景覆盖。
package adaptor

import (
	"encoding/json"
	"errors"
	"testing"
)

// loadTestFixtures 加载 testdata 下全部素材（不存在 → 直接失败）。
func loadTestFixtures(t *testing.T) []*Fixture {
	t.Helper()
	fixtures, err := LoadDir("testdata")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("testdata 下无素材")
	}
	return fixtures
}

// TestFixturesLoadAll：全部素材可加载、name 唯一、kind 合法。
func TestFixturesLoadAll(t *testing.T) {
	fixtures := loadTestFixtures(t)
	seen := map[string]bool{}
	for _, f := range fixtures {
		if f.Name == "" {
			t.Fatalf("素材缺 name：%+v", f)
		}
		if seen[f.Name] {
			t.Fatalf("素材 name 重复：%s", f.Name)
		}
		seen[f.Name] = true
		switch f.EffectiveKind() {
		case KindSSE, KindError:
		default:
			t.Fatalf("素材 %s kind=%q 非法", f.Name, f.Kind)
		}
	}
}

// TestFixturesCoverScenarios：素材集必须覆盖 LR-5 要求的全部场景（文本 / reasoning / tool_call 多段 /
// usage + [DONE] / 非 JSON 错误体 / 流中断）。
func TestFixturesCoverScenarios(t *testing.T) {
	fixtures := loadTestFixtures(t)
	need := map[string]bool{
		"openai_text_reason_usage_done": false,
		"openai_toolcall_index":         false,
		"openai_stream_truncated":       false,
		"error_body_nonjson":            false,
		"error_body_json":               false,
	}
	for _, f := range fixtures {
		if _, ok := need[f.Name]; ok {
			need[f.Name] = true
		}
	}
	for name, ok := range need {
		if !ok {
			t.Fatalf("缺素材 %s（场景覆盖不全）", name)
		}
	}
}

// TestFixturesSSEDecode：kind=sse 素材按原分片解码 → 载荷序列与期望逐条一致；
// truncated=true → 末尾得 ErrTruncated。
func TestFixturesSSEDecode(t *testing.T) {
	for _, f := range loadTestFixtures(t) {
		if f.EffectiveKind() != KindSSE {
			continue
		}
		got, err := decodeAll(t, &chunkReader{chunks: append([]string(nil), f.Chunks...)})
		switch {
		case f.Truncated:
			if !errors.Is(err, ErrTruncated) {
				t.Fatalf("%s: err=%v want ErrTruncated", f.Name, err)
			}
		case err != nil:
			t.Fatalf("%s: decode err=%v", f.Name, err)
		}
		eq(t, got, f.Payloads)
	}
}

// TestFixturesErrorMessages：kind=error 素材 → ErrorMessage 期望值（含非 JSON 原样兜底）。
func TestFixturesErrorMessages(t *testing.T) {
	for _, f := range loadTestFixtures(t) {
		if f.EffectiveKind() != KindError {
			continue
		}
		if got := ErrorMessage([]byte(f.Body)); got != f.ExpectError {
			t.Fatalf("%s: ErrorMessage=%q want %q", f.Name, got, f.ExpectError)
		}
	}
}

// TestFixtureToolCallsGroupByIndex：tool_call 的多段分片按 `index` 聚合 —— 首个非空分片带 id/name、
// 后续仅 arguments 片段；两枚并行调用（index 0/1）交叉到达仍各自正确拼接（LR-6 口径的最小验证）。
func TestFixtureToolCallsGroupByIndex(t *testing.T) {
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
	eq(t, payloads, fx.Payloads)

	type acc struct{ id, name, args string }
	got := map[int]*acc{}
	var order []int
	for _, p := range payloads {
		if IsDone([]byte(p)) {
			continue
		}
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
		if err := json.Unmarshal([]byte(p), &chunk); err != nil {
			t.Fatalf("载荷非 JSON: %v (payload=%s)", err, p)
		}
		for _, ch := range chunk.Choices {
			for _, tc := range ch.Delta.ToolCalls {
				idx := 0
				if tc.Index != nil {
					idx = *tc.Index
				}
				a := got[idx]
				if a == nil {
					a = &acc{}
					got[idx] = a
					order = append(order, idx)
				}
				if tc.ID != "" { // 空分片不覆盖已落位值
					a.id = tc.ID
				}
				if tc.Function.Name != "" {
					a.name = tc.Function.Name
				}
				a.args += tc.Function.Arguments
			}
		}
	}

	if len(order) != 2 || order[0] != 0 || order[1] != 1 {
		t.Fatalf("index 首现顺序=%v want [0 1]", order)
	}
	if a := got[0]; a.id != "call_a" || a.name != "core_file_read" || a.args != `{"path":"a.txt"}` {
		t.Fatalf("index=0 聚合=%+v", a)
	}
	if a := got[1]; a.id != "call_b" || a.name != "core_file_write" || a.args != `{"path":"b.txt"}` {
		t.Fatalf("index=1 聚合=%+v", a)
	}
}

// TestLoadDirMissingReturnsEmpty：目录不存在 → 空切片、无错误。
func TestLoadDirMissingReturnsEmpty(t *testing.T) {
	got, err := LoadDir("testdata/__nope__")
	if err != nil {
		t.Fatalf("err=%v want nil", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d want 0", len(got))
	}
}
