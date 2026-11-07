// D-65 auto 档位调度（L1）：能力值解析（同档次 ±0.5 含端点）、`auto` → 默认 LLM 解析、
// 以及重试期换 provider 的端到端链路（router `AutoCandidates` 的接线）。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/lsong98sh/chonkpilot-agent/chat"
	"github.com/lsong98sh/chonkpilot-agent/router"
)

func autoIntp(v int) *int { return &v }

// autoTestConfig 构造 usr 配置快照（llms 按给定顺序）。
func autoTestConfig(defaultLLM string, llms ...map[string]any) map[string]any {
	items := make([]any, 0, len(llms))
	for _, l := range llms {
		items = append(items, l)
	}
	return map[string]any{"defaultLLM": defaultLLM, "llms": items}
}

// autoTestCaps 构造 `capability{reasoning,vision}` 双维度（D-65，2026-11-04）。
func autoTestCaps(reasoning, vision float64) map[string]any {
	return map[string]any{"reasoning": reasoning, "vision": vision}
}

// TestAutoCandidatesTier：同档次 = |reasoning − 基准| ≤ 0.5（含端点）；显式 0 不参与；缺省按默认 LLM。
func TestAutoCandidatesTier(t *testing.T) {
	cfg := autoTestConfig("def",
		map[string]any{"name": "def", "capability": autoTestCaps(4.5, 0)},
		map[string]any{"name": "a", "capability": autoTestCaps(4.0, 0)}, // 差 0.5（含端点）→ 候选
		map[string]any{"name": "b", "capability": autoTestCaps(5.0, 0)}, // 差 0.5（含端点）→ 候选
		map[string]any{"name": "c", "capability": autoTestCaps(3.9, 0)}, // 差 0.6 → 不同档
		map[string]any{"name": "d", "capability": autoTestCaps(0, 5)},   // 显式 reasoning=0 = 不具备 → 不参与
		map[string]any{"name": "e"},                                     // 缺省 = 按默认 LLM 的能力值 → 候选
	)
	got := autoCandidates(cfg, "def")
	want := []string{"a", "b", "e"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("候选=%v want %v（顺序 = 配置顺序，不含首选）", got, want)
	}

	// 基准未声明档位 → 不做档位限制（所有可用 provider 都是候选，显式 0 仍排除）。
	cfg2 := autoTestConfig("def",
		map[string]any{"name": "def"},
		map[string]any{"name": "a", "capability": autoTestCaps(0.5, 0)},
		map[string]any{"name": "b", "capability": autoTestCaps(5.0, 0)},
		map[string]any{"name": "c", "capability": autoTestCaps(0, 5)},
	)
	if got, want := autoCandidates(cfg2, "def"), []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("基准未声明 → 候选=%v want %v", got, want)
	}

	// base 为空（系统默认，不在 llms 内）→ 同样不限档位；base 自身显式 0 → 不作基准。
	cfg3 := autoTestConfig("",
		map[string]any{"name": "x", "capability": autoTestCaps(0, 0)},
		map[string]any{"name": "y", "capability": autoTestCaps(2.5, 0)},
	)
	if got, want := autoCandidates(cfg3, ""), []string{"y"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("系统默认基准 → 候选=%v want %v", got, want)
	}
}

// TestLLMCapabilityDimsLegacy：旧 `capabilities:["reasoning","vision"]` → 各映射为中位档 3.0；
// 空数组 → 两项 0（显式不具备）；两者都缺省 → 未声明。
func TestLLMCapabilityDimsLegacy(t *testing.T) {
	dims, declared := llmCapabilityDims(map[string]any{"capabilities": []any{"reasoning", "vision"}})
	if !declared || dims.Reasoning != capabilityLegacyDim || dims.Vision != capabilityLegacyDim {
		t.Fatalf("旧双能力 → %+v declared=%v want 3.0/3.0", dims, declared)
	}
	dims, declared = llmCapabilityDims(map[string]any{"capabilities": []any{"vision"}})
	if !declared || dims.Reasoning != 0 || dims.Vision != capabilityLegacyDim {
		t.Fatalf("旧单能力 → %+v declared=%v want 0/3.0", dims, declared)
	}
	dims, declared = llmCapabilityDims(map[string]any{"capabilities": []any{}})
	if !declared || dims.Reasoning != 0 || dims.Vision != 0 {
		t.Fatalf("旧空数组 → %+v declared=%v want 0/0（显式不具备）", dims, declared)
	}
	if _, declared := llmCapabilityDims(map[string]any{}); declared {
		t.Fatalf("两项均缺省 → 应视为未声明")
	}
	// 新形态优先；缺失维度按 0 处理。
	dims, declared = llmCapabilityDims(map[string]any{
		"capability":   autoTestCaps(4.5, 0),
		"capabilities": []any{"reasoning", "vision"},
	})
	if !declared || dims.Reasoning != 4.5 || dims.Vision != 0 {
		t.Fatalf("新形态优先 → %+v declared=%v want 4.5/0", dims, declared)
	}

	// 旧记录参与 auto 档位判定：同为中位档 3.0 → 同档候选。
	cfg := autoTestConfig("def",
		map[string]any{"name": "def", "capabilities": []any{"reasoning", "vision"}},
		map[string]any{"name": "old", "capabilities": []any{"reasoning"}},
		map[string]any{"name": "high", "capability": autoTestCaps(5, 0)},
	)
	if got, want := autoCandidates(cfg, "def"), []string{"old"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("旧记录档位 → 候选=%v want %v", got, want)
	}
}

// TestAutoVisionCandidates：默认 LLM vision=0 → 同档且 vision>0 的候选（先 reasoning 最接近，
// 再 vision 高者）；默认已具备识图 / 无合格候选 → nil。
func TestAutoVisionCandidates(t *testing.T) {
	cfg := autoTestConfig("def",
		map[string]any{"name": "def", "capability": autoTestCaps(4.0, 0)},   // 默认：不具备识图
		map[string]any{"name": "a", "capability": autoTestCaps(4.0, 2.0)},   // 同档（差 0）vision 2
		map[string]any{"name": "b", "capability": autoTestCaps(4.0, 5.0)},   // 同档（差 0）vision 5 → 优先
		map[string]any{"name": "c", "capability": autoTestCaps(4.5, 4.0)},   // 差 0.5（含端点）
		map[string]any{"name": "far", "capability": autoTestCaps(3.0, 5.0)}, // 差 1.0 → 不同档
		map[string]any{"name": "novis", "capability": autoTestCaps(4.0, 0)}, // vision=0 → 排除
		map[string]any{"name": "norea", "capability": autoTestCaps(0, 5.0)}, // reasoning=0 → 排除
		map[string]any{"name": "undec"},                                     // 未声明 → 不具备识图
	)
	got := autoVisionCandidates(cfg, "def")
	want := []string{"b", "a", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("图像候选=%v want %v（先 reasoning 最接近、再 vision 高者）", got, want)
	}

	// 默认 LLM 已具备识图能力 → 不替换。
	cfg2 := autoTestConfig("def",
		map[string]any{"name": "def", "capability": autoTestCaps(4.0, 3.0)},
		map[string]any{"name": "a", "capability": autoTestCaps(4.0, 5.0)},
	)
	if got := autoVisionCandidates(cfg2, "def"); len(got) != 0 {
		t.Fatalf("默认已具备识图 → 不应有替换候选：%v", got)
	}

	// 默认档位未声明 → 不限档（按 vision 降序）。
	cfg3 := autoTestConfig("def",
		map[string]any{"name": "def"},
		map[string]any{"name": "x", "capability": autoTestCaps(1, 1)},
		map[string]any{"name": "y", "capability": autoTestCaps(5, 4)},
	)
	if got, want := autoVisionCandidates(cfg3, "def"), []string{"y", "x"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("默认未声明档位 → %v want %v", got, want)
	}

	// 无合格候选（同档都不具备识图）→ 空（降级仍用默认 LLM）。
	cfg4 := autoTestConfig("def",
		map[string]any{"name": "def", "capability": autoTestCaps(4.0, 0)},
		map[string]any{"name": "a", "capability": autoTestCaps(4.0, 0)},
	)
	if got := autoVisionCandidates(cfg4, "def"); len(got) != 0 {
		t.Fatalf("无合格候选 → 应降级（空），got %v", got)
	}
}

// TestRequestHasImage：「含图像」= 最后一条 user 消息的图片附件引用，或文本命中识图关键词表。
func TestRequestHasImage(t *testing.T) {
	cases := []struct {
		name string
		msgs []ChatMsg
		want bool
	}{
		{"附件图片", []ChatMsg{{Role: "system", Content: "sys"}, {Role: "user", Content: "看下 ![图](tmp/uploads/a.PNG)"}}, true},
		{"非图片附件", []ChatMsg{{Role: "user", Content: "看下 ![doc](tmp/uploads/a.pdf)"}}, false},
		{"关键词-识图", []ChatMsg{{Role: "user", Content: "帮我识图一下这个报错"}}, true},
		{"关键词-看图", []ChatMsg{{Role: "user", Content: "看图说话"}}, true},
		{"关键词-根据图", []ChatMsg{{Role: "user", Content: "根据图里的结构写代码"}}, true},
		{"普通文本", []ChatMsg{{Role: "user", Content: "hello"}}, false},
		{"无 user 消息", []ChatMsg{{Role: "system", Content: "sys"}}, false},
		// 只看**最后一条** user 消息：更早轮次的图片不计入本次判定。
		{"历史轮次图片不算", []ChatMsg{
			{Role: "user", Content: "![图](tmp/uploads/a.png)"},
			{Role: "assistant", Content: "收到"},
			{Role: "user", Content: "继续"},
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := requestHasImage(c.msgs); got != c.want {
				t.Fatalf("requestHasImage=%v want %v", got, c.want)
			}
		})
	}
}

// TestResolveAutoLLMName：`auto` 首次解析到默认 LLM；默认值缺失 / 仍是 auto → 系统默认（""）。
func TestResolveAutoLLMName(t *testing.T) {
	cfg := autoTestConfig("def", map[string]any{"name": "def", "model": "def-model"})
	if got := resolveAutoLLMName(cfg, "auto"); got != "def" {
		t.Fatalf("auto → %q want def", got)
	}
	if got := resolveAutoLLMName(cfg, "AUTO"); got != "def" {
		t.Fatalf("大小写不敏感：auto → %q want def", got)
	}
	if got := resolveAutoLLMName(cfg, "p1"); got != "p1" {
		t.Fatalf("非 auto 应原样返回，got %q", got)
	}
	if got := resolveAutoLLMName(cfg, ""); got != "" {
		t.Fatalf("空名应原样返回，got %q", got)
	}
	// 默认值指向 auto（退化配置）→ 系统默认，避免自引用。
	cfgSelf := autoTestConfig("auto", map[string]any{"name": "def"})
	if got := resolveAutoLLMName(cfgSelf, "auto"); got != "" {
		t.Fatalf("defaultLLM=auto 应回落系统默认，got %q", got)
	}
	if got := resolveAutoLLMName(nil, "auto"); got != "" {
		t.Fatalf("无配置应回落系统默认，got %q", got)
	}

	p := providerFromConfig(cfg, "auto")
	if p == nil || p.Name != "def" || p.Model != "def-model" {
		t.Fatalf("providerFromConfig(auto)=%+v want def/def-model", p)
	}
}

// TestChatAutoSwitchOnRetryableFailure：auto 候选链路端到端（llm → router → 换 provider）。
func TestChatAutoSwitchOnRetryableFailure(t *testing.T) {
	var p1Calls, p2Calls int
	var p2Body map[string]any

	p1 := newSSEServer(t, func(w http.ResponseWriter, _ *http.Request) {
		p1Calls++
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"boom"}`)
	})
	p2 := newSSEServer(t, func(w http.ResponseWriter, r *http.Request) {
		p2Calls++
		_ = json.NewDecoder(r.Body).Decode(&p2Body)
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": "from-p2"}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	})

	s := &Server{rt: router.New(router.Config{})}
	spec1 := openAITestSpec(p1.URL, "m1")
	spec1.Name = "p1"
	spec2 := openAITestSpec(p2.URL, "m2")
	spec2.Name = "p2"
	if err := s.rt.Register(spec2); err != nil {
		t.Fatalf("Register(p2): %v", err)
	}

	evs, err := s.chat(context.Background(), spec1, []ChatMsg{{Role: "user", Content: "q"}}, nil, ChatOptions{
		MaxRetries: autoIntp(1), RetryBudget: 20 * time.Millisecond,
		AutoCandidates: []string{"p2"},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	text, _, _, finish, serr := collectStreamEvents(evs)
	if serr != nil || text != "from-p2" || finish != "stop" {
		t.Fatalf("text=%q finish=%q err=%v（应换到候选 p2 完成）", text, finish, serr)
	}
	if p1Calls != 1 || p2Calls != 1 {
		t.Fatalf("请求数 p1=%d p2=%d want 1/1", p1Calls, p2Calls)
	}
	if model, _ := p2Body["model"].(string); model != "m2" {
		t.Fatalf("换 provider 后 body.model=%v want m2（新 provider 默认模型）", p2Body["model"])
	}
}

// TestChatAutoNoSwitchOnAuthError：鉴权错误不可重试 → 不换 provider、只请求一次。
func TestChatAutoNoSwitchOnAuthError(t *testing.T) {
	var p1Calls, p2Calls int
	p1 := newSSEServer(t, func(w http.ResponseWriter, _ *http.Request) {
		p1Calls++
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"bad key"}`)
	})
	p2 := newSSEServer(t, func(w http.ResponseWriter, _ *http.Request) {
		p2Calls++
		llmSSE(w, []string{sseChunk(map[string]any{}, "stop")})
	})

	s := &Server{rt: router.New(router.Config{})}
	spec1 := openAITestSpec(p1.URL, "m1")
	spec1.Name = "p1"
	spec2 := openAITestSpec(p2.URL, "m2")
	spec2.Name = "p2"
	if err := s.rt.Register(spec2); err != nil {
		t.Fatalf("Register(p2): %v", err)
	}

	evs, err := s.chat(context.Background(), spec1, []ChatMsg{{Role: "user", Content: "q"}}, nil, ChatOptions{
		MaxRetries: autoIntp(2), RetryBudget: 20 * time.Millisecond,
		AutoCandidates: []string{"p2"},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	_, _, _, _, serr := collectStreamEvents(evs)
	if serr == nil {
		t.Fatalf("want auth 错误终态")
	}
	if p1Calls != 1 || p2Calls != 0 {
		t.Fatalf("请求数 p1=%d p2=%d want 1/0（鉴权错误不换 provider）", p1Calls, p2Calls)
	}
}

// TestTurnAutoLLMSwitchesProviderOnRetry：整轮链路（`llm: "auto"` → 默认 LLM → 可重试失败换同档）。
func TestTurnAutoLLMSwitchesProviderOnRetry(t *testing.T) {
	var p1Calls, p2Calls int
	p1 := newSSEServer(t, func(w http.ResponseWriter, _ *http.Request) {
		p1Calls++
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"boom"}`)
	})
	p2 := newSSEServer(t, func(w http.ResponseWriter, _ *http.Request) {
		p2Calls++
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": "from-p2"}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	})

	s := newTestServer(t, p1)
	registerTestInstance(t, s)
	// usr 配置：默认 p1；两 provider 同档（reasoning 4.5 / 4.5）→ auto 失败后切 p2；重试 1 次（小预算）。
	saveTestUserConfig(t, s, map[string]any{
		"defaultLLM":     "p1",
		"retryCount":     1,
		"retryBudgetSec": 1,
		"llms": []any{
			map[string]any{"name": "p1", "baseUrl": p1.URL, "model": "m1", "apiKey": "sk-test-key", "capability": autoTestCaps(4.5, 0)},
			map[string]any{"name": "p2", "baseUrl": p2.URL, "model": "m2", "apiKey": "sk-test-key", "capability": autoTestCaps(4.5, 0)},
		},
	})
	if err := newSessionStore(s.bus, "ins-test").EnsureSession("s-auto"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	res, _ := s.bus.Emit(context.Background(), "session-start", jb(map[string]any{
		"req_id": "r-t-auto", "instance_id": "ins-test", "session": "s-auto", "turn": "t-auto",
		"llm": "auto",
	})).Wait().Result.(map[string]any)
	if res["accepted"] != true {
		t.Fatalf("llm-start rejected: %+v", res)
	}
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-auto", "turn": "t-auto",
		"type": "text-user", "content": "hello auto",
	}))
	evs := collectTurn(t, s.bus, "t-auto", 20*time.Second)
	if c := lastComplete(evs); c == nil || c["status"] != "complete" {
		t.Fatalf("want complete（auto 换档后成功）：%+v", evs)
	}
	if p1Calls != 1 || p2Calls != 1 {
		t.Fatalf("请求数 p1=%d p2=%d want 1/1（auto 首选默认 LLM，失败后切同档候选）", p1Calls, p2Calls)
	}
}

// TestChatImageFallbackSwitchesOnFirstCall：auto 图像感知 —— 默认 LLM 不具备识图能力且本次请求
// 含图像 → **首次调用**即改选候选 provider（默认 LLM 不被请求）；无候选 / 不含图像 → 不替换。
func TestChatImageFallbackSwitchesOnFirstCall(t *testing.T) {
	var p1Calls, p2Calls int
	var p2Body map[string]any
	p1 := newSSEServer(t, func(w http.ResponseWriter, _ *http.Request) {
		p1Calls++
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": "from-p1"}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	})
	p2 := newSSEServer(t, func(w http.ResponseWriter, r *http.Request) {
		p2Calls++
		_ = json.NewDecoder(r.Body).Decode(&p2Body)
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": "from-p2"}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	})

	s := &Server{rt: router.New(router.Config{})}
	spec1 := openAITestSpec(p1.URL, "m1")
	spec1.Name = "p1"
	spec2 := openAITestSpec(p2.URL, "m2")
	spec2.Name = "p2"

	newClient := func(fallbacks []ImageFallback) chat.LLMClient {
		return newChatLLMClient(s, spec1, ChatOptions{MaxRetries: autoIntp(0), ImageFallbacks: fallbacks})
	}
	run := func(cl chat.LLMClient, content string) {
		ch, err := cl.Step(context.Background(), chat.StepRequest{
			Messages: []ChatMsg{{Role: "user", Content: content}},
		}, chat.StepOptions{})
		if err != nil {
			t.Fatalf("Step: %v", err)
		}
		for range ch {
		}
	}

	// ① 附件图片 → 首次即切到 p2（含其专属 model）。
	run(newClient([]ImageFallback{{Name: "p2", Spec: spec2, Model: "m2"}}), "看下 ![图](tmp/uploads/a.png)")
	if p1Calls != 0 || p2Calls != 1 {
		t.Fatalf("请求数 p1=%d p2=%d want 0/1（首次即改选具备识图能力的候选）", p1Calls, p2Calls)
	}
	if model, _ := p2Body["model"].(string); model != "m2" {
		t.Fatalf("替换后 body.model=%v want m2（候选 provider 的模型）", p2Body["model"])
	}

	// ② 关键词命中同样生效（默认 LLM 仍不被调用）。
	p1Calls, p2Calls = 0, 0
	run(newClient([]ImageFallback{{Name: "p2", Spec: spec2, Model: "m2"}}), "帮我识图一下这张报错截图")
	if p1Calls != 0 || p2Calls != 1 {
		t.Fatalf("关键词命中：p1=%d p2=%d want 0/1", p1Calls, p2Calls)
	}

	// ③ 无合格候选（ImageFallbacks 空 + 降级标记）→ 降级仍用默认 LLM，不阻断。
	p1Calls, p2Calls = 0, 0
	cl := newChatLLMClient(s, spec1, ChatOptions{MaxRetries: autoIntp(0), ImageFallbackUnavailable: true})
	run(cl, "看下 ![图](tmp/uploads/a.png)")
	if p1Calls != 1 || p2Calls != 0 {
		t.Fatalf("无候选应降级用默认：p1=%d p2=%d want 1/0", p1Calls, p2Calls)
	}

	// ④ 不含图像 → 不替换。
	p1Calls, p2Calls = 0, 0
	run(newClient([]ImageFallback{{Name: "p2", Spec: spec2, Model: "m2"}}), "hello")
	if p1Calls != 1 || p2Calls != 0 {
		t.Fatalf("不含图像不应替换：p1=%d p2=%d want 1/0", p1Calls, p2Calls)
	}
}
