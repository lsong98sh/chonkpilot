package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// newPromptTestServer 建 prompt-optimise 测试环境：s.llm 指向 llmSrv（usr config 缺失时回落），
// 内嵌 persist 数据服务（UsrPath 已注入临时路径，newTestServer） + 注册测试实例。
func newPromptTestServer(t *testing.T, llmSrv *httptest.Server) *Server {
	t.Helper()
	s := newTestServer(t, llmSrv)
	registerTestInstance(t, s)
	return s
}

// collectPromptOptimised 订阅 prompt-optimised 广播（onPromptOptimise 后台 goroutine 异步发）。
func collectPromptOptimised(t *testing.T, s *Server) chan map[string]any {
	t.Helper()
	ch := make(chan map[string]any, 8)
	_, err := s.bus.On("prompt-optimised", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) == nil {
			select {
			case ch <- m:
			default:
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

func waitPromptOptimised(t *testing.T, ch chan map[string]any) map[string]any {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting prompt-optimised")
		return nil
	}
}

// llmRequestMessages 解码 LLM chat 请求的消息列表（断言指令内容用）。
func llmRequestMessages(t *testing.T, r *http.Request) []string {
	t.Helper()
	var body struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode llm request: %v", err)
	}
	msgs := make([]string, 0, len(body.Messages))
	for _, m := range body.Messages {
		msgs = append(msgs, m.Content)
	}
	return msgs
}

// TestPromptOptimiseGenerateBroadcast：type=tool_usage 无 content → 全新生成；
// LLM 流式返回 → 广播 prompt-optimised{instance_id, type, content}，指令要求生成工具使用说明。
func TestPromptOptimiseGenerateBroadcast(t *testing.T) {
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		msgs := llmRequestMessages(t, r)
		if len(msgs) != 1 {
			t.Errorf("want 1 message, got %d", len(msgs))
		}
		if !strings.Contains(msgs[0], "tool usage guide") {
			t.Errorf("instruction should ask for tool usage guide, got: %s", msgs[0])
		}
		if strings.Contains(msgs[0], "Current Content") {
			t.Errorf("generate (no content) should not contain Current Content: %s", msgs[0])
		}
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": "optimised "}, ""),
			sseChunk(map[string]any{"content": "tool usage"}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	}))
	defer llmSrv.Close()

	s := newPromptTestServer(t, llmSrv)
	ch := collectPromptOptimised(t, s)

	s.bus.Emit(context.Background(), "prompt-optimise", jb(map[string]any{
		"req_id": "r1", "instance_id": "ins-test", "type": "tool_usage",
	}))

	ev := waitPromptOptimised(t, ch)
	if ev["instance_id"] != "ins-test" || ev["type"] != "tool_usage" {
		t.Fatalf("pair mismatch: %+v", ev)
	}
	if ev["content"] != "optimised tool usage" {
		t.Fatalf("content=%q", ev["content"])
	}
	if _, hasErr := ev["error"]; hasErr {
		t.Fatalf("unexpected error field: %+v", ev)
	}
}

// TestPromptOptimiseOptimiseExisting：content 存在 → 优化已有（指令含原内容），广播返回优化结果。
func TestPromptOptimiseOptimiseExisting(t *testing.T) {
	orig := "You are a helpful coding assistant."
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		msgs := llmRequestMessages(t, r)
		if !strings.Contains(msgs[0], "Current Content") || !strings.Contains(msgs[0], orig) {
			t.Errorf("instruction should embed existing content: %s", msgs[0])
		}
		if !strings.Contains(msgs[0], "agent system prompt") {
			t.Errorf("instruction should target agent type: %s", msgs[0])
		}
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": "You are a ", "reasoning": ""}, ""),
			sseChunk(map[string]any{"content": "senior coding assistant."}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	}))
	defer llmSrv.Close()

	s := newPromptTestServer(t, llmSrv)
	ch := collectPromptOptimised(t, s)

	s.bus.Emit(context.Background(), "prompt-optimise", jb(map[string]any{
		"req_id": "r2", "instance_id": "ins-test", "type": "agent", "content": orig,
	}))

	ev := waitPromptOptimised(t, ch)
	if ev["instance_id"] != "ins-test" || ev["type"] != "agent" {
		t.Fatalf("pair mismatch: %+v", ev)
	}
	if ev["content"] != "You are a senior coding assistant." {
		t.Fatalf("content=%q", ev["content"])
	}
}

// TestPromptOptimiseRequiresType：type 缺失 → 广播错误（content 为空 + error 字段）。
func TestPromptOptimiseRequiresType(t *testing.T) {
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		llmSSE(w, []string{sseChunk(map[string]any{}, "stop")})
	}))
	defer llmSrv.Close()

	s := newPromptTestServer(t, llmSrv)
	ch := collectPromptOptimised(t, s)

	s.bus.Emit(context.Background(), "prompt-optimise", jb(map[string]any{
		"req_id": "r4", "instance_id": "ins-test",
	}))

	ev := waitPromptOptimised(t, ch)
	if ev["instance_id"] != "ins-test" || ev["type"] != "" {
		t.Fatalf("pair mismatch: %+v", ev)
	}
	if ev["content"] != "" {
		t.Fatalf("content should be empty: %+v", ev)
	}
	if errMsg, _ := ev["error"].(string); !strings.Contains(errMsg, "type required") {
		t.Fatalf("error=%q", ev["error"])
	}
}

// TestPromptOptimiseLLMError：LLM 端点 500 → 广播错误（content 为空 + error 字段）。
func TestPromptOptimiseLLMError(t *testing.T) {
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer llmSrv.Close()

	s := newPromptTestServer(t, llmSrv)
	ch := collectPromptOptimised(t, s)

	s.bus.Emit(context.Background(), "prompt-optimise", jb(map[string]any{
		"req_id": "r5", "instance_id": "ins-test", "type": "agent", "content": "old",
	}))

	ev := waitPromptOptimised(t, ch)
	if ev["instance_id"] != "ins-test" || ev["type"] != "agent" {
		t.Fatalf("pair mismatch: %+v", ev)
	}
	if ev["content"] != "" {
		t.Fatalf("content should be empty on error: %+v", ev)
	}
	if errMsg, _ := ev["error"].(string); !strings.Contains(errMsg, "llm http 500") {
		t.Fatalf("error=%q", ev["error"])
	}
}

// TestPromptOptimiseUsesPromptOptimiseLLM：SL-4——prompt-optimise 的 provider 读**子系统默认 LLM**
// `llm.promptOptimise`（与 GUI 桥 activeLLM 统一读同一键）：
//   - 显式 name → 命中该 provider；旧 int 索引 → 经 data.LLMRefName 折算 llms[idx].name → 命中该 provider；
//   - 键缺失 → 数据层读侧回落 defaultLLM（SL-1）→ 命中 defaultLLM 指向的 provider。
//
// 断言：请求打到目标 provider 的 baseUrl（body.model = 该 provider 的 model，非 exe 默认 model），
// 非目标 provider（含 exe flags 隐含默认端点）**零请求**。
func TestPromptOptimiseUsesPromptOptimiseLLM(t *testing.T) {
	for _, tc := range []struct {
		name      string
		extra     map[string]any
		wantModel string
	}{
		{"显式 name → 命中该 provider", map[string]any{"llm.promptOptimise": "opt"}, "opt-model"},
		{"旧 int 索引 → 折算 llms[idx]", map[string]any{"llm.promptOptimise": float64(1)}, "opt-model"},
		{"未配置 → 回落 defaultLLM", nil, "def-model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defRec, defSrv := newProviderServer(t) // exe flags 隐含默认（-llm-base）
			optRec, optSrv := newProviderServer(t)
			s := newPromptTestServer(t, defSrv)

			data := map[string]any{
				"llms": []any{
					map[string]any{"name": "def", "baseUrl": defSrv.URL, "model": "def-model"},
					map[string]any{"name": "opt", "baseUrl": optSrv.URL, "model": "opt-model"},
				},
				"defaultLLM": "def",
			}
			for k, v := range tc.extra {
				data[k] = v
			}
			saveTestUserConfig(t, s, data)

			ch := collectPromptOptimised(t, s)
			s.bus.Emit(context.Background(), "prompt-optimise", jb(map[string]any{
				"req_id": "r6", "instance_id": "ins-test", "type": "agent", "content": "old",
			}))
			ev := waitPromptOptimised(t, ch)
			if _, hasErr := ev["error"]; hasErr {
				t.Fatalf("unexpected error: %+v", ev)
			}

			wantRec, otherRec := optRec, defRec
			if tc.wantModel == "def-model" { // 回落 defaultLLM → 目标 = def provider
				wantRec, otherRec = defRec, optRec
			}
			if wantRec.count() != 1 {
				t.Fatalf("目标 provider 请求数=%d want 1", wantRec.count())
			}
			body, _, _ := wantRec.first(t, 0)
			if got, _ := body["model"].(string); got != tc.wantModel {
				t.Fatalf("命中 provider 的 model=%q want %q（非 exe 默认）", got, tc.wantModel)
			}
			if otherRec.count() != 0 {
				t.Fatalf("非目标 provider 收到 %d 次请求", otherRec.count())
			}
		})
	}
}

// TestPromptOptimiseInstruction：指令构造（生成 vs 优化 × 各 type 描述）。
func TestPromptOptimiseInstruction(t *testing.T) {
	for _, tc := range []struct {
		typ     string
		content string
		wantHas []string
		wantNo  []string
	}{
		{"tool_usage", "", []string{"tool usage guide", "Write a", "Return ONLY the prompt text"}, []string{"Current Content", "Optimize the following"}},
		{"summary", "old summary", []string{"summary prompt", "Optimize the following", "Current Content", "old summary", "Return ONLY the optimized prompt text"}, []string{"Write a"}},
		{"agent", "be nice", []string{"agent system prompt", "Optimize the following"}, []string{"Write a"}},
		{"unknown-type", "", []string{"AI prompt", "Write a"}, []string{"Current Content"}},
		{"unknown-type", "x", []string{"AI prompt", "Optimize the following"}, []string{"Write a"}},
	} {
		got := optimiseInstruction(tc.typ, tc.content)
		for _, s := range tc.wantHas {
			if !strings.Contains(got, s) {
				t.Errorf("type=%q content=%q: instruction missing %q:\n%s", tc.typ, tc.content, s, got)
			}
		}
		for _, s := range tc.wantNo {
			if strings.Contains(got, s) {
				t.Errorf("type=%q content=%q: instruction should not contain %q:\n%s", tc.typ, tc.content, s, got)
			}
		}
	}
}
