// config.suggest（配置页 / 工具原语「AI 智能填充」，AICFG-1）单测：
//   - 各 kind 的白名单回填（§4：✅ 可填 / 🚫 禁填；非白名单字段与 apiKey 一律丢弃）；
//   - 错误分支（入参不合法 invalid / 模型输出非 JSON parse_failed / 无字段 parse_failed / LLM 既有分类）；
//   - 硬约束：**只回填待保存态**（不落库：不打 data-*/session-*/task-*）、**apiKey 不回传、不上送**。
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// suggestRec 记录 mock LLM 端点收到的请求消息（断言指令内容用）。
type suggestRec struct {
	mu   sync.Mutex
	msgs []string
}

func (r *suggestRec) first(t *testing.T) string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.msgs) == 0 {
		t.Fatal("mock LLM 未收到请求")
	}
	return strings.Join(r.msgs, "\n")
}

// suggestStub 起 mock LLM 端点：记录请求消息，把 reply 作为流式 content 返回。
func suggestStub(t *testing.T, reply string) (*suggestRec, *httptest.Server) {
	t.Helper()
	rec := &suggestRec{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		msgs := llmRequestMessages(t, r)
		rec.mu.Lock()
		rec.msgs = append(rec.msgs, msgs...)
		rec.mu.Unlock()
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": reply}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	}))
	t.Cleanup(srv.Close)
	return rec, srv
}

// suggestServer 建一个已接线 `llm.promptOptimise` → mock LLM 的测试 server。
func suggestServer(t *testing.T, rec *suggestRec, llmSrv *httptest.Server) *Server {
	t.Helper()
	defSrv := mockLLMServer()
	defer defSrv.Close()
	s := newPromptTestServer(t, defSrv)
	saveTestUserConfig(t, s, map[string]any{
		"llms": []any{
			map[string]any{"name": "opt", "baseUrl": llmSrv.URL, "model": "opt-model"},
		},
		"defaultLLM":         "opt",
		"llm.promptOptimise": "opt",
	})
	return s
}

// configSuggestCall 发 config.suggest（真实总线方法面）并取结果信封。
func configSuggestCall(t *testing.T, s *Server, payload map[string]any) map[string]any {
	t.Helper()
	v := s.bus.Emit(context.Background(), SubjectConfigSuggest, jb(payload)).Wait()
	if v.Err() != nil {
		t.Fatalf("config.suggest 总线错误: %v", v.Err())
	}
	res, _ := v.Result.(map[string]any)
	if res == nil {
		t.Fatalf("config.suggest 无结果信封（v.Result=%#v）", v.Result)
	}
	return res
}

// suggestFields 取成功信封的 fields。
func suggestFields(t *testing.T, res map[string]any) map[string]any {
	t.Helper()
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("预期成功，实得 %+v", res)
	}
	f, _ := res["fields"].(map[string]any)
	if f == nil {
		t.Fatalf("成功信封缺 fields: %+v", res)
	}
	return f
}

// suggestError 取失败信封的 (kind, message)。
func suggestError(t *testing.T, res map[string]any) (string, string) {
	t.Helper()
	if ok, _ := res["ok"].(bool); ok {
		t.Fatalf("预期失败，实得 ok=true: %+v", res)
	}
	e, _ := res["error"].(map[string]any)
	if e == nil {
		t.Fatalf("失败信封缺 error: %+v", res)
	}
	kind, _ := e["kind"].(string)
	msg, _ := e["message"].(string)
	return kind, msg
}

// TestConfigSuggestSubjectConst：主题常量 = 契约主题名（点分直通总线）。
func TestConfigSuggestSubjectConst(t *testing.T) {
	if SubjectConfigSuggest != "config.suggest" {
		t.Fatalf("SubjectConfigSuggest=%q want config.suggest", SubjectConfigSuggest)
	}
}

// TestConfigSuggestLLMWhitelist：kind=llm → 只回填 LLM 白名单；apiKey 与未知字段一律丢弃。
func TestConfigSuggestLLMWhitelist(t *testing.T) {
	reply := `{"name":"GLM","model":"glm-4.6","protocol":"openai","baseUrl":"https://open.bigmodel.cn/api/paas/v4",
		"maxContextToken":128000,"thinking":true,"reasoningEffort":"high","capabilities":["reasoning"],
		"apiKey":"sk-should-never-leak","totallyUnknown":123}`
	rec, srv := suggestStub(t, reply)
	s := suggestServer(t, rec, srv)

	res := configSuggestCall(t, s, map[string]any{"instance_id": "ins-test", "kind": "llm", "text": "用 GLM 的 glm-4.6"})
	fields := suggestFields(t, res)
	for _, k := range []string{"name", "model", "protocol", "baseUrl", "maxContextToken", "thinking", "reasoningEffort", "capabilities"} {
		if _, ok := fields[k]; !ok {
			t.Errorf("白名单字段 %q 应回填，实得 fields=%+v", k, fields)
		}
	}
	for _, k := range []string{"apiKey", "totallyUnknown"} {
		if _, ok := fields[k]; ok {
			t.Errorf("非白名单字段 %q 不得回填：%+v", k, fields)
		}
	}
	if raw, _ := json.Marshal(res); strings.Contains(string(raw), "sk-should-never-leak") {
		t.Fatalf("应答泄露 apiKey: %s", raw)
	}
}

// TestConfigSuggestMCPWhitelist：kind=mcp → 回填连接字段；level/enabled/hot_tools（禁填）一律丢弃。
func TestConfigSuggestMCPWhitelist(t *testing.T) {
	reply := `{"name":"github","transport":"stdio","description":"GitHub MCP",
		"runtime":"npx","args":["-y","@modelcontextprotocol/server-github"],"namespace":"gh",
		"level":"app","enabled":true,"isolate":true,"sandbox":true,"hot_tools":["*"]}`
	rec, srv := suggestStub(t, reply)
	s := suggestServer(t, rec, srv)

	res := configSuggestCall(t, s, map[string]any{"kind": "mcp", "text": "连一个 GitHub MCP"})
	fields := suggestFields(t, res)
	for _, k := range []string{"name", "transport", "description", "runtime", "args", "namespace"} {
		if _, ok := fields[k]; !ok {
			t.Errorf("白名单字段 %q 应回填，实得 fields=%+v", k, fields)
		}
	}
	for _, k := range []string{"level", "enabled", "isolate", "sandbox", "hot_tools"} {
		if _, ok := fields[k]; ok {
			t.Errorf("禁填字段 %q 不得回填：%+v", k, fields)
		}
	}
}

// TestConfigSuggestToolWhitelist：kind=tool → 回填整份契约主体（title/description/parameters/content/meta）。
func TestConfigSuggestToolWhitelist(t *testing.T) {
	reply := `{"title":"Excel 转 CSV","description":"把 xlsx 转为 csv",
		"parameters":"{\"type\":\"object\"}","content":"# 正文","meta":{"category":"dir"},
		"bogus":"x"}`
	rec, srv := suggestStub(t, reply)
	s := suggestServer(t, rec, srv)

	res := configSuggestCall(t, s, map[string]any{"kind": "tool", "text": "做一个 Excel 转 CSV 的工具"})
	fields := suggestFields(t, res)
	for _, k := range []string{"title", "description", "parameters", "content", "meta"} {
		if _, ok := fields[k]; !ok {
			t.Errorf("白名单字段 %q 应回填，实得 fields=%+v", k, fields)
		}
	}
	if _, ok := fields["bogus"]; ok {
		t.Errorf("非白名单字段 bogus 不得回填：%+v", fields)
	}
}

// TestConfigSuggestNotesAndCurrentSanitized：notes 走 result.notes（不进 fields）；
// current 上传时剔除白名单外字段（尤含 apiKey）——不得出现在送给上游的指令里。
func TestConfigSuggestNotesAndCurrentSanitized(t *testing.T) {
	reply := `{"model":"m1","notes":"baseUrl 需用【测试连接】验证"}`
	rec, srv := suggestStub(t, reply)
	s := suggestServer(t, rec, srv)

	res := configSuggestCall(t, s, map[string]any{
		"kind": "llm", "text": "补全模型",
		"current": map[string]any{"name": "keep", "apiKey": "sk-current-secret", "bogus": 1},
	})
	fields := suggestFields(t, res)
	if _, ok := fields["notes"]; ok {
		t.Errorf("notes 不应进 fields：%+v", fields)
	}
	if got, _ := res["notes"].(string); got != "baseUrl 需用【测试连接】验证" {
		t.Fatalf("notes=%q", got)
	}
	instruction := rec.first(t)
	if strings.Contains(instruction, "sk-current-secret") || strings.Contains(instruction, "bogus") {
		t.Fatalf("current 未按白名单过滤，指令含禁填字段：%s", instruction)
	}
	if !strings.Contains(instruction, `"name":"keep"`) {
		t.Fatalf("current 白名单字段应嵌入指令：%s", instruction)
	}
}

// TestConfigSuggestParseFailed：模型返回非 JSON → 稳定错误码 parse_failed（不部分回填）。
func TestConfigSuggestParseFailed(t *testing.T) {
	rec, srv := suggestStub(t, "抱歉，我无法完成。")
	s := suggestServer(t, rec, srv)

	res := configSuggestCall(t, s, map[string]any{"kind": "llm", "text": "x"})
	kind, msg := suggestError(t, res)
	if kind != string(configSuggestKindParse) {
		t.Fatalf("kind=%q want parse_failed（msg=%q）", kind, msg)
	}
	if _, has := res["fields"]; !has {
		t.Errorf("失败信封应含空 fields（同形）：%+v", res)
	}
}

// TestConfigSuggestNoFields：模型返回合法 JSON 但无任何白名单字段（只有 apiKey）→ parse_failed。
func TestConfigSuggestNoFields(t *testing.T) {
	rec, srv := suggestStub(t, `{"apiKey":"sk-only"}`)
	s := suggestServer(t, rec, srv)

	res := configSuggestCall(t, s, map[string]any{"kind": "llm", "text": "x"})
	if kind, _ := suggestError(t, res); kind != string(configSuggestKindParse) {
		t.Fatalf("kind=%q want parse_failed", kind)
	}
}

// TestConfigSuggestInvalidParams：kind 未知 / text 为空 → invalid（不发任何请求）。
func TestConfigSuggestInvalidParams(t *testing.T) {
	rec, srv := suggestStub(t, `{"model":"m"}`)
	s := suggestServer(t, rec, srv)

	for _, tc := range []map[string]any{
		{"kind": "unknown", "text": "x"},
		{"kind": "llm", "text": "   "},
		{"text": "x"},
	} {
		res := configSuggestCall(t, s, tc)
		if kind, _ := suggestError(t, res); kind != string(configSuggestKindInvalid) {
			t.Fatalf("payload=%+v kind=%q want invalid", tc, kind)
		}
	}
	rec.mu.Lock()
	n := len(rec.msgs)
	rec.mu.Unlock()
	if n != 0 {
		t.Fatalf("入参不合法不得发请求（实得 %d 条消息）", n)
	}
}

// TestConfigSuggestLLMError：上游 500 → 分类 server（不判成功，走 result 信封）。
func TestConfigSuggestLLMError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()
	rec := &suggestRec{}
	s := suggestServer(t, rec, srv)

	res := configSuggestCall(t, s, map[string]any{"kind": "llm", "text": "x"})
	kind, msg := suggestError(t, res)
	if kind != string(ErrServer) {
		t.Fatalf("kind=%q want server（msg=%q）", kind, msg)
	}
	if !strings.HasPrefix(msg, "[server] ") {
		t.Fatalf("message=%q 须为主路径形态 `[server] …`", msg)
	}
}

// TestConfigSuggestNoPersist：调用期间总线上不得出现 data-*/session-*/task-*（只回填待保存态、不落库）。
func TestConfigSuggestNoPersist(t *testing.T) {
	rec, srv := suggestStub(t, `{"model":"m1"}`)
	s := suggestServer(t, rec, srv)

	var mu sync.Mutex
	var subjects []string
	sub, err := s.bus.On(">", 0, func(_ context.Context, subject string, _ *mq.Value) error {
		mu.Lock()
		subjects = append(subjects, subject)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("订阅失败: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	if res := configSuggestCall(t, s, map[string]any{"kind": "llm", "text": "x"}); res["ok"] != true {
		t.Fatalf("应成功，实得 %+v", res)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, subj := range subjects {
		if strings.HasPrefix(subj, "data-") || strings.HasPrefix(subj, "session-") || strings.HasPrefix(subj, "task-") {
			t.Fatalf("config.suggest 期间出现会话/数据面消息 %q（须为纯建议、不落库）", subj)
		}
	}
}

// suggestProvider 起 mock LLM 端点：记录请求体（断言**命中哪个 provider**），把 reply 作为流式
// content 返回（供 provider 选择口径用例；reply 需是合法 JSON 对象以避免 parse_failed 噪声）。
func suggestProvider(t *testing.T, reply string) (*providerRec, *httptest.Server) {
	t.Helper()
	rec := &providerRec{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.mu.Lock()
		rec.bodies = append(rec.bodies, body)
		rec.mu.Unlock()
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": reply}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	}))
	t.Cleanup(srv.Close)
	return rec, srv
}

// TestConfigSuggestFallsBackToDefaultLLM：`llm.promptOptimise` **缺失** → 数据层读侧回落 `defaultLLM`
// （与 prompt-optimise 同口径）——请求打到 defaultLLM 指向的 provider，**不**落 exe flags 隐含默认。
func TestConfigSuggestFallsBackToDefaultLLM(t *testing.T) {
	exeRec, exeSrv := suggestProvider(t, `{"model":"exe-model"}`)
	defRec, defSrv := suggestProvider(t, `{"model":"def-model"}`)
	s := newPromptTestServer(t, exeSrv) // exe flags 隐含默认（-llm-base）
	saveTestUserConfig(t, s, map[string]any{
		"llms":       []any{map[string]any{"name": "def", "baseUrl": defSrv.URL, "model": "def-model"}},
		"defaultLLM": "def",
		// 故意不设 llm.promptOptimise → 读侧回落 defaultLLM
	})

	res := configSuggestCall(t, s, map[string]any{"kind": "llm", "text": "补全模型名"})
	if got, _ := suggestFields(t, res)["model"]; got != "def-model" {
		t.Fatalf("fields.model=%v want def-model（回落 defaultLLM）", got)
	}
	if defRec.count() != 1 {
		t.Fatalf("defaultLLM provider 请求数=%d want 1", defRec.count())
	}
	if exeRec.count() != 0 {
		t.Fatalf("exe flags 隐含默认不应被请求，实得 %d 次", exeRec.count())
	}
}

// TestConfigSuggestProviderMissFallsBackToExeDefault：`llm.promptOptimise` 指向**不存在**的 provider →
// 未命中 → 回落 exe flags 隐含默认（仍走成功信封，不报错）。
func TestConfigSuggestProviderMissFallsBackToExeDefault(t *testing.T) {
	exeRec, exeSrv := suggestProvider(t, `{"model":"exe-model"}`)
	defRec, defSrv := suggestProvider(t, `{"model":"def-model"}`)
	s := newPromptTestServer(t, exeSrv)
	saveTestUserConfig(t, s, map[string]any{
		"llms":               []any{map[string]any{"name": "def", "baseUrl": defSrv.URL, "model": "def-model"}},
		"defaultLLM":         "def",
		"llm.promptOptimise": "no-such-provider",
	})

	res := configSuggestCall(t, s, map[string]any{"kind": "llm", "text": "x"})
	if got, _ := suggestFields(t, res)["model"]; got != "exe-model" {
		t.Fatalf("fields.model=%v want exe-model（未命中 provider → 回落 exe flags）", got)
	}
	if exeRec.count() != 1 {
		t.Fatalf("exe 默认 provider 请求数=%d want 1", exeRec.count())
	}
	if defRec.count() != 0 {
		t.Fatalf("defaultLLM provider 不应被请求，实得 %d 次", defRec.count())
	}
}

// TestConfigSuggestTimeout（§6「生成耗时较长 → 明确错误」）：上游不响应 → 稳定超时分类（不悬挂、
// 不抛总线错误——configSuggestCall 已断言 v.Err()==nil）。
func TestConfigSuggestTimeout(t *testing.T) {
	old := llmOnceTimeout
	llmOnceTimeout = 150 * time.Millisecond
	t.Cleanup(func() { llmOnceTimeout = old })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(700 * time.Millisecond)
	}))
	defer srv.Close()
	rec := &suggestRec{}
	s := suggestServer(t, rec, srv)

	start := time.Now()
	res := configSuggestCall(t, s, map[string]any{"kind": "llm", "text": "x"})
	kind, msg := suggestError(t, res)
	if kind != string(ErrTimeout) {
		t.Fatalf("kind=%q want timeout（msg=%q）", kind, msg)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("超时上限未生效（耗时 %v）", elapsed)
	}
}

// TestConfigSuggestInstruction：指令按 kind 选字段清单；禁止项（apiKey）+ 仅输出 JSON 的硬要求常在。
func TestConfigSuggestInstruction(t *testing.T) {
	llm := configSuggestInstruction("llm", "用 GLM", nil)
	if !strings.Contains(llm, "reasoningEffort") || !strings.Contains(llm, "Never output secrets (apiKey") {
		t.Errorf("llm 指令缺字段清单/禁止项:\n%s", llm)
	}
	if !strings.Contains(llm, "Return ONLY a single valid JSON object") {
		t.Errorf("llm 指令缺「仅输出 JSON」硬要求:\n%s", llm)
	}
	if !strings.Contains(llm, "用 GLM") {
		t.Errorf("llm 指令须含用户描述:\n%s", llm)
	}
	mcp := configSuggestFieldSpec("mcp")
	if !strings.Contains(mcp, "transport") || !strings.Contains(mcp, "runtime") {
		t.Errorf("mcp 字段清单不全:\n%s", mcp)
	}
	tool := configSuggestFieldSpec("tool")
	if !strings.Contains(tool, "parameters") || !strings.Contains(tool, "content") {
		t.Errorf("tool 字段清单不全:\n%s", tool)
	}
}
