// llm_run 端到端（dsl-core 语法，引擎 = chonkpilot-lib/dsl）：任务节点 / 落盘 / 捕获 / 断点续跑。
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runJobCase 起一个 mock LLM：主轮次提示词命中 trigger → llm_run(file)；
// 子轮次直接回显提示词（"mock 完成"）→ 输出 = 收到的提示词，可验证插值拼接。
// prepare(dir) 在 server 建好后（testWorkDir 生效）写入数据文件并返回脚本内容。
func runJobCase(t *testing.T, session, turn, trigger string, prepare func(dir string) string) string {
	t.Helper()
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ChatMsg `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		text := lastText(body.Messages)
		if strings.Contains(text, trigger) {
			args := jb(map[string]any{"file": filepath.Join(testWorkDir, "job.dsl"), "tool_call_display_name": "批量作业"})
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": ""}, ""),
				sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-job", "type": "function",
					"function": map[string]any{"name": "llm_run", "arguments": string(args)},
				}}}, "tool_calls"),
			})
			return
		}
		var chunks []string
		for _, ch := range text { // 子轮次回显提示词
			chunks = append(chunks, sseChunk(map[string]any{"content": string(ch)}, ""))
		}
		chunks = append(chunks, sseChunk(map[string]any{}, "stop"))
		llmSSE(w, chunks)
	}))
	defer llm.Close()
	s := newTestServer(t, llm)

	script := prepare(testWorkDir)
	if err := os.WriteFile(filepath.Join(testWorkDir, "job.dsl"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	startTurn(t, s, session, turn)
	text := jobTurn(t, s, session, turn, trigger)
	return text
}

// jobTurn 在既有 server/workdir 上跑一轮（resume 复用同一数据目录）。
func jobTurn(t *testing.T, s *Server, session, turn, trigger string) string {
	t.Helper()
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": session, "turn": turn,
		"type": "text-user", "content": trigger,
	}))
	evs := collectTurn(t, s.bus, turn, 20*time.Second)
	c := lastComplete(evs)
	if c == nil || c["status"] != "complete" {
		t.Fatalf("no complete: %+v", evs)
	}
	text, _ := c["text"].(string)
	return text
}

// TestTaskJobDSL：llm_run → LOOP 2 条目 → 每条目一个 LLM 子轮次，结果写 out/<name>.txt；
// 父轮次汇总含落盘路径。路径经宿主注入的 {{env.CHONKPILOT_WORKDIR}} 拼绝对路径（R-11）。
func TestTaskJobDSL(t *testing.T) {
	text := runJobCase(t, "s-job", "t-job", "job please", func(dir string) string {
		if err := os.WriteFile(filepath.Join(dir, "items.json"),
			[]byte(`[{"name":"A","count":1},{"name":"B","count":2}]`), 0o644); err != nil {
			t.Fatal(err)
		}
		return `LOOP item=#"{{env.CHONKPILOT_WORKDIR}}/items.json".array
   LLM "处理{{item.name}}" "do {{item.name}} {{item.count}}" => #"{{env.CHONKPILOT_WORKDIR}}/out/{{item.name}}.txt"
END
`
	})
	if strings.Contains(text, "失败") {
		t.Fatalf("作业不应有失败: %q", text)
	}
	if !strings.Contains(text, "out/A.txt") || !strings.Contains(text, "out/B.txt") {
		t.Fatalf("汇总缺落盘路径: %q", text)
	}
	for name, want := range map[string]string{"A": "do A 1", "B": "do B 2"} {
		raw, err := os.ReadFile(filepath.Join(testWorkDir, "out", name+".txt"))
		if err != nil {
			t.Fatalf("读 out/%s.txt: %v", name, err)
		}
		if strings.TrimSpace(string(raw)) != want {
			t.Fatalf("out/%s.txt = %q want %q", name, raw, want)
		}
	}
}

// TestTaskJobDSLCapture：顶层 LLM => 捕获变量 → LOOP 内 {{计划}} 拼接 → 子轮次收到拼好的文本。
func TestTaskJobDSLCapture(t *testing.T) {
	text := runJobCase(t, "s-job-cap", "t-job-cap", "cap please", func(dir string) string {
		if err := os.WriteFile(filepath.Join(dir, "items.json"),
			[]byte(`[{"name":"A"},{"name":"B"}]`), 0o644); err != nil {
			t.Fatal(err)
		}
		d := filepath.ToSlash(dir) // DSL 字符串内 \r/\n/\t 会被转义解码，正斜杠绝对路径避免误伤
		return `LLM "规划" "产出一段计划文本：先做 A 再做 B" => 计划
LOOP item=#"` + d + `/items.json".array
   LLM "执行{{item.name}}" "按计划（{{计划}}）处理 {{item.name}}" => #"` + d + `/out/{{item.name}}.txt"
END
`
	})
	if strings.Contains(text, "失败") {
		t.Fatalf("作业不应失败: %q", text)
	}
	for name := range map[string]bool{"A": true, "B": true} {
		want := "按计划（产出一段计划文本：先做 A 再做 B）处理 " + name
		raw, err := os.ReadFile(filepath.Join(testWorkDir, "out", name+".txt"))
		if err != nil {
			t.Fatalf("读 out/%s.txt: %v", name, err)
		}
		if strings.TrimSpace(string(raw)) != want {
			t.Fatalf("out/%s.txt = %q want %q", name, raw, want)
		}
	}
}

// TestTaskJobDSLResume：同一 server/workdir 跑两轮——第一轮处理全部并 SET done=true 写回
// items.json；第二轮 IF 全跳过（无写入、无失败、汇总含"跳过"）。
func TestTaskJobDSLResume(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ChatMsg `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		text := lastText(body.Messages)
		if strings.Contains(text, "resume please") {
			args := jb(map[string]any{"file": filepath.Join(testWorkDir, "job.dsl"), "tool_call_display_name": "续跑作业"})
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": ""}, ""),
				sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-resume", "type": "function",
					"function": map[string]any{"name": "llm_run", "arguments": string(args)},
				}}}, "tool_calls"),
			})
			return
		}
		var chunks []string
		for _, ch := range text {
			chunks = append(chunks, sseChunk(map[string]any{"content": string(ch)}, ""))
		}
		chunks = append(chunks, sseChunk(map[string]any{}, "stop"))
		llmSSE(w, chunks)
	}))
	defer llm.Close()
	s := newTestServer(t, llm)

	d := filepath.ToSlash(testWorkDir) // 正斜杠绝对路径（避免 DSL 内 \r/\n/\t 转义误伤）
	script := `LOOP item=#"` + d + `/items.json".array concurrency=2
   IF item.done != true
      LLM "处理{{item.name}}" "do {{item.name}}" => #"` + d + `/out/{{item.name}}.txt"
      SET item.done => true
   END
END
`
	if err := os.WriteFile(filepath.Join(testWorkDir, "items.json"),
		[]byte(`[{"name":"A"},{"name":"B"},{"name":"C"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testWorkDir, "job.dsl"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	startTurn(t, s, "s-resume-1", "t-resume-1")
	first := jobTurn(t, s, "s-resume-1", "t-resume-1", "resume please")
	if strings.Contains(first, "失败") {
		t.Fatalf("第一轮不应失败: %q", first)
	}
	for _, name := range []string{"A", "B", "C"} {
		raw, err := os.ReadFile(filepath.Join(testWorkDir, "out", name+".txt"))
		if err != nil || strings.TrimSpace(string(raw)) != "do "+name {
			t.Fatalf("out/%s.txt = %q err=%v", name, raw, err)
		}
	}
	startTurn(t, s, "s-resume-2", "t-resume-2")
	second := jobTurn(t, s, "s-resume-2", "t-resume-2", "resume please")
	if strings.Contains(second, "已写入") || strings.Contains(second, "失败") {
		t.Fatalf("第二轮应全部跳过: %q", second)
	}
	if !strings.Contains(second, "跳过") {
		t.Fatalf("第二轮应报告全部跳过: %q", second)
	}
	raw, err := os.ReadFile(filepath.Join(testWorkDir, "items.json"))
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatalf("items.json 写回非法: %v", err)
	}
	for _, it := range items {
		if it["done"] != true {
			t.Fatalf("条目 done 未写回: %+v", it)
		}
	}
	entries, err := os.ReadDir(filepath.Join(testWorkDir, "out"))
	if err != nil || len(entries) != 3 {
		t.Fatalf("out/ 应仍 3 个文件: %v %d", err, len(entries))
	}
}

// TestJobScriptMissingArgs：script/file 全缺 → 工具报错回填。
func TestJobScriptMissingArgs(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ChatMsg `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		text := lastText(body.Messages)
		if strings.Contains(text, "job bad please") {
			args := jb(map[string]any{"tool_call_display_name": "缺脚本"})
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": ""}, ""),
				sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-job-bad", "type": "function",
					"function": map[string]any{"name": "llm_run", "arguments": string(args)},
				}}}, "tool_calls"),
			})
			return
		}
		var chunks []string
		for _, ch := range text {
			chunks = append(chunks, sseChunk(map[string]any{"content": string(ch)}, ""))
		}
		chunks = append(chunks, sseChunk(map[string]any{}, "stop"))
		llmSSE(w, chunks)
	}))
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-job-bad", "t-job-bad")
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-job-bad", "turn": "t-job-bad",
		"type": "text-user", "content": "job bad please",
	}))
	evs := collectTurn(t, s.bus, "t-job-bad", 15*time.Second)
	c := lastComplete(evs)
	if c == nil || c["status"] != "complete" {
		t.Fatalf("no complete: %+v", evs)
	}
	if text, _ := c["text"].(string); !strings.Contains(text, "script/file") {
		t.Fatalf("缺参错误未回填: %q", text)
	}
}

// runJobScriptCase：mock LLM 命中 trigger → llm_run(script=<给定脚本>)；返回父轮汇总文本。
func runJobScriptCase(t *testing.T, session, turn, trigger, script string) string {
	t.Helper()
	text, _ := runJobScriptTasks(t, session, turn, trigger, script)
	return text
}

// runJobScriptTasks：runJobScriptCase 的变体，额外返回任务事件采集器
// （断言 LLM 子任务节点的 purpose/name/simplified 展示字段）。
func runJobScriptTasks(t *testing.T, session, turn, trigger, script string) (string, *taskEvents) {
	t.Helper()
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ChatMsg `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		text := lastText(body.Messages)
		if strings.Contains(text, trigger) {
			args := jb(map[string]any{"script": script, "tool_call_display_name": "内联作业"})
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": ""}, ""),
				sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-inline", "type": "function",
					"function": map[string]any{"name": "llm_run", "arguments": string(args)},
				}}}, "tool_calls"),
			})
			return
		}
		var chunks []string
		for _, ch := range text {
			chunks = append(chunks, sseChunk(map[string]any{"content": string(ch)}, ""))
		}
		chunks = append(chunks, sseChunk(map[string]any{}, "stop"))
		llmSSE(w, chunks)
	}))
	defer llm.Close()
	s := newTestServer(t, llm)
	te := watchTasks(t, s.bus)
	startTurn(t, s, session, turn)
	return jobTurn(t, s, session, turn, trigger), te
}

// findChildLLM 取任务事件中的子 LLM 节点（llm_run 派生：kind=llm 且 parent_id 非空）。
func findChildLLM(te *taskEvents) map[string]any {
	te.mu.Lock()
	defer te.mu.Unlock()
	for _, m := range te.started {
		if m["kind"] == TaskKindLLM {
			if pid, _ := m["parent_id"].(string); pid != "" {
				return m
			}
		}
	}
	return nil
}

// TestJobScriptRelativePathFails：llm_run DSL 内相对路径 #"path" → 顶层失败（含原值与路径约束）。
func TestJobScriptRelativePathFails(t *testing.T) {
	text := runJobScriptCase(t, "s-rel", "t-rel", "rel please",
		"LOOP item=#\"items.json\".array\n   SET \"x\" => last\nEND\n")
	if !strings.Contains(text, "错误") {
		t.Fatalf("相对路径应顶层失败: %q", text)
	}
	if !strings.Contains(text, "items.json") || !strings.Contains(text, "绝对路径") {
		t.Fatalf("失败消息应含原值与路径约束: %q", text)
	}
}

// TestJobScriptEnvWorkdir：{{env.CHONKPILOT_WORKDIR}} 拼绝对路径读数据源 + 落盘 → 成功（R-11 方案 A）。
func TestJobScriptEnvWorkdir(t *testing.T) {
	text := runJobCase(t, "s-env", "t-env", "env please", func(dir string) string {
		if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte("A\nB\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return `LOOP line=#"{{env.CHONKPILOT_WORKDIR}}/in.txt".lines
   LLM "回显" "line={{line}}" => #"{{env.CHONKPILOT_WORKDIR}}/out/{{line}}.txt"
END
`
	})
	if strings.Contains(text, "失败") {
		t.Fatalf("env 拼绝对路径应成功: %q", text)
	}
	for name, want := range map[string]string{"A": "line=A", "B": "line=B"} {
		raw, err := os.ReadFile(filepath.Join(testWorkDir, "out", name+".txt"))
		if err != nil {
			t.Fatalf("读 out/%s.txt: %v", name, err)
		}
		if strings.TrimSpace(string(raw)) != want {
			t.Fatalf("out/%s.txt = %q want %q", name, raw, want)
		}
	}
}

// TestTaskJobDSLLLMThirdArgPurpose：LLM 第三参 = 目的 → 子 LLM 任务节点的
// purpose/name/simplified（tasktree label）都等于该目的值。
func TestTaskJobDSLLLMThirdArgPurpose(t *testing.T) {
	const purpose = "整理当日日志要点"
	text, te := runJobScriptTasks(t, "s-llm-purpose", "t-llm-purpose", "purpose please",
		"LLM \"回显\" \"这条提示词很长很长很长很长很长很长很长用于验证回退截断\" \""+purpose+"\"\n")
	if strings.Contains(text, "失败") {
		t.Fatalf("作业不应失败: %q", text)
	}
	te.wait(t, "LLM 子任务节点", func() bool { return findChildLLM(te) != nil })
	n := findChildLLM(te)
	for _, k := range []string{"purpose", "name", "simplified"} {
		if n[k] != purpose {
			t.Fatalf("子节点 %s = %v want %q (%+v)", k, n[k], purpose, n)
		}
	}
}

// TestTaskJobDSLLLMDefaultLabel（回归）：LLM 无第三参 → 子节点展示名仍 = 提示词截断（≤24 字符）。
func TestTaskJobDSLLLMDefaultLabel(t *testing.T) {
	const prompt = "验证缺省展示名回退为提示词截断并且这条提示词要超过二十四个字符才行"
	want := promptLabel(prompt)
	text, te := runJobScriptTasks(t, "s-llm-label", "t-llm-label", "label please",
		"LLM \"回显\" \""+prompt+"\"\n")
	if strings.Contains(text, "失败") {
		t.Fatalf("作业不应失败: %q", text)
	}
	te.wait(t, "LLM 子任务节点", func() bool { return findChildLLM(te) != nil })
	n := findChildLLM(te)
	for _, k := range []string{"purpose", "name", "simplified"} {
		if n[k] != want {
			t.Fatalf("子节点 %s = %v want %q (%+v)", k, n[k], want, n)
		}
	}
}

// TestJobScriptTooManyArgsFails：LLM 参数多于 3 个 → 顶层失败（文案含参数过多与语法示例）。
func TestJobScriptTooManyArgsFails(t *testing.T) {
	text := runJobScriptCase(t, "s-args", "t-args", "args please",
		"LLM \"a\" \"b\" \"c\" \"d\"\n")
	if !strings.Contains(text, "错误") {
		t.Fatalf("参数过多应顶层失败: %q", text)
	}
	if !strings.Contains(text, "参数过多") || !strings.Contains(text, `["目的"]`) {
		t.Fatalf("失败文案应含参数过多与语法示例: %q", text)
	}
}

// rootLLMNode 取任务事件中的**根** llm 节点（kind=llm 且 parent_id 空；llm_run 根节点）。
func rootLLMNode(te *taskEvents) map[string]any {
	te.mu.Lock()
	defer te.mu.Unlock()
	for _, m := range te.started {
		if m["kind"] == TaskKindLLM {
			if pid, _ := m["parent_id"].(string); pid == "" {
				return m
			}
		}
	}
	return nil
}

// TestLLMRunRootNodeKind（G-15）：网关聚合暴露名 self_llm_run → turn.go 根节点 kind 应按
// **契约名** llm_run 判定 = llm（此前误用暴露名 → 落 tool）。
func TestLLMRunRootNodeKind(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ChatMsg `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		text := lastText(body.Messages)
		if strings.Contains(text, "root kind please") {
			args := jb(map[string]any{"script": "LLM \"回显\" \"hi\"\n", "tool_call_display_name": "委派作业"})
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": ""}, ""),
				sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-root", "type": "function",
					"function": map[string]any{"name": "self_llm_run", "arguments": string(args)},
				}}}, "tool_calls"),
			})
			return
		}
		var chunks []string
		for _, ch := range text {
			chunks = append(chunks, sseChunk(map[string]any{"content": string(ch)}, ""))
		}
		chunks = append(chunks, sseChunk(map[string]any{}, "stop"))
		llmSSE(w, chunks)
	}))
	defer llm.Close()
	s := newTestServer(t, llm)
	te := watchTasks(t, s.bus)
	startTurn(t, s, "s-root-kind", "t-root-kind")
	jobTurn(t, s, "s-root-kind", "t-root-kind", "root kind please")

	te.wait(t, "llm_run root node", func() bool { return rootLLMNode(te) != nil })
	n := rootLLMNode(te)
	if n["kind"] != TaskKindLLM {
		t.Fatalf("llm_run 根节点 kind=%v want %s（%+v）", n["kind"], TaskKindLLM, n)
	}
	if n["tool"] != "self_llm_run" {
		t.Fatalf("根节点 tool=%v want self_llm_run（暴露名）", n["tool"])
	}
}

// TestJobScriptRuntimeErrorReported（I-19）：DSL 运行时错误（写保留字 env）不再被静默丢弃，
// 必须出现在作业回填文本中（此前恒为空/误导性"全部跳过"）。
func TestJobScriptRuntimeErrorReported(t *testing.T) {
	text := runJobScriptCase(t, "s-runtime-err", "t-runtime-err", "runtime err please",
		"SET 1 => env\n")
	if !strings.Contains(text, "DSL 错误") || !strings.Contains(text, "env") {
		t.Fatalf("DSL 运行时错误未上报: %q", text)
	}
}
