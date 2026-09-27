// 真实内嵌 MCP 链路集成测试（21-llm-server，v2 分组注册面）：
// server lib 内嵌 mcp-server + gateway（DisableMCP=false，无 fakeGateway）——
// 域工具/域 agent 经 tools/register / prompts/register（mcp-* 主题）注入 registered provider
// （handler_subject=domain-tool-call，hot=true）；LLM 工具列表 = gateway tools/list 过滤 hot；
// turn 内 task 型经 gateway 唯一执行入口 → 注册回调（domain-tool-call）→ execTaskTool。
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// newTestServerMCP 建真实内嵌 MCP 栈的测试环境（无 fakeGateway；MCPServerRoot 注入临时空目录
// 避免扫描真实 capability；UsrPath 注入临时 usr 库）。
func newTestServerMCP(t *testing.T, llmSrv *httptest.Server) *Server {
	t.Helper()
	data.Reset()
	testWorkDir = t.TempDir()
	t.Cleanup(func() { testWorkDir = "" })
	bus, err := mq.New(mq.Options{Prefix: testBusPrefix})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })

	s := New(bus, Options{LLMBase: llmSrv.URL, LLMModel: "mock",
		MCPServerRoot: t.TempDir(), UsrPath: t.TempDir() + "/usr.db"}) // DisableMCP=false → 内嵌
	s.locksDir = t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(s.Stop)
	return s
}

// newTestServerMCPAppRoot 同 newTestServerMCP，但 capability 契约根可注入：
// T-21② 系统级 app 根热生效测试用（真实 <exeDir>/capability 属构建产物目录，
// 用例以可写临时目录模拟同一根，等价于外部/构建期更新）。
func newTestServerMCPAppRoot(t *testing.T, llmSrv *httptest.Server, appRoot string) *Server {
	t.Helper()
	data.Reset()
	testWorkDir = t.TempDir()
	t.Cleanup(func() { testWorkDir = "" })
	bus, err := mq.New(mq.Options{Prefix: testBusPrefix})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })

	s := New(bus, Options{LLMBase: llmSrv.URL, LLMModel: "mock",
		MCPServerRoot: appRoot, UsrPath: t.TempDir() + "/usr.db"})
	s.locksDir = t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(s.Stop)
	return s
}

// TestEmbeddedGatewayDomainToolsHot：tools/list 应含全部注册域工具且 hot=true
// （tools/register hot=true，契约见 server/contracts/tools/*.tool.md）。
func TestEmbeddedGatewayDomainToolsHot(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServerMCP(t, llm)

	defs, err := s.gc.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	want := map[string]bool{}
	for _, d := range defs {
		if d.Hot {
			// 2026-09-07 list 全量后暴露名带能力源前缀（self_*，gateway self 节点 entry.ID 前缀）；
			// 断言按原始契约名归一（ask_user/llm_run/... = self_ 前缀剥离）。
			want[strings.TrimPrefix(d.Name, "self_")] = true
		}
	}
	for _, n := range []string{"ask_user", "llm_run", "tool_result", "tool_stop"} {
		if !want[n] {
			t.Fatalf("域工具 %s 未在 tools/list 中（hot got %v）", n, want)
		}
	}
}

// TestEmbeddedGatewaySystemToolsMeta：系统工具（域工具 category=server + gateway 元工具
// category=meta）经 tools/list 透出的 `_meta` 应含**正确的 async 模式**；契约 / 注入时**显式声明
// timeout=0**（= 无上限）者透出 `_meta.timeout=0`，未声明者不含 timeout（回落全局）。
// 用户口径 2026-09-27：`tool_*`/`ask_user` 契约声明 timeout=0 = 执行硬上限无（永远等，可取消）；
// 元工具（mcp_find/mcp_load/mcp_invoke）注入时**也显式声明 timeout=0**（无上限，绝对优先、可取消）；
// `llm_run` 契约未声明 timeout → 不含该键（回落全局）。
func TestEmbeddedGatewaySystemToolsMeta(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServerMCP(t, llm)

	defs, err := s.gc.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	byName := map[string]ToolDef{}
	for _, d := range defs {
		byName[strings.TrimPrefix(d.Name, "self_")] = d
	}
	check := func(name, wantAsync, wantCat string, wantTimeout any) {
		t.Helper()
		d, ok := byName[name]
		if !ok {
			t.Errorf("tools/list 缺少 %s（got %v）", name, keysOf(byName))
			return
		}
		if got := str(d.Meta["async"]); got != wantAsync {
			t.Errorf("%s: _meta.async = %q, 期望 %q", name, got, wantAsync)
		}
		if got := str(d.Meta["category"]); got != wantCat {
			t.Errorf("%s: _meta.category = %q, 期望 %q", name, got, wantCat)
		}
		if wantTimeout == nil {
			if v, has := d.Meta["timeout"]; has {
				t.Errorf("%s: _meta 不应含 timeout（契约未声明 → 回落全局），got %v", name, v)
			}
		} else if v, has := d.Meta["timeout"]; !has || v != wantTimeout {
			t.Errorf("%s: _meta.timeout = %v（has=%v），期望 %v（0 = 无上限）", name, v, has, wantTimeout)
		}
		t.Logf("%s: _meta = %v", name, d.Meta)
	}
	zero := float64(0)
	check("tool_stop", "never", "server", zero)
	check("tool_result", "never", "server", zero)
	check("ask_user", "never", "server", zero)
	check("llm_run", "always", "server", nil)
	check("mcp_find", "never", "meta", zero)
	check("mcp_load", "never", "meta", zero)
	check("mcp_invoke", "never", "meta", zero)
}

func keysOf(m map[string]ToolDef) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestEmbeddedGatewayTaskToolTurn：完整 turn 内 task 型工具（tool_result）经 gateway 唯一入口执行
// → 注册回调（domain-tool-call）→ execTaskTool → 结果回填 → complete。
func TestEmbeddedGatewayTaskToolTurn(t *testing.T) {
	// 预建一个已 done 的 server 任务节点（tool_result 按其 id 取结果文本）。
	var taskID string
	// 自建 responder：第一回合 tool_call 用内嵌 gateway 暴露名（self_tool_result，
	// 2026-09-07 self 节点 entry.ID 前缀；LLM 只见提交 defs 的暴露名）；
	// 第二回合（含工具结果）返回含节点结果摘要的文本（complete 文本断言）。
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ChatMsg `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		hasToolResult := false
		for _, m := range body.Messages {
			if m.Role == "tool" {
				hasToolResult = true
			}
		}
		if !hasToolResult {
			args := jb(map[string]any{"id": taskID, "timeout": 0})
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": ""}, ""),
				sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-tr", "type": "function",
					"function": map[string]any{"name": "self_tool_result", "arguments": string(args)},
				}}}, "tool_calls"),
			})
			return
		}
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": "结果: 预建任务完成"}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	}))
	defer llm.Close()
	s := newTestServerMCP(t, llm)
	te := watchTasks(t, s.bus)
	startTurn(t, s, "s-emb-tr", "t-emb-tr")

	pre, _ := s.tasks.start(&TaskNode{
		Tool: "core_file_read", Kind: TaskKindTool, SessionID: "s-emb-tr", TurnID: "t-emb-tr",
		InstanceID: "ins-test", TopSession: "s-emb-tr", Name: "pre", Purpose: "p",
	})
	taskID = pre.TaskID
	s.tasks.done(pre.TaskID, TaskStateDone, "预建任务完成", "")

	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-emb-tr", "turn": "t-emb-tr",
		"type": "text-user", "content": "tool result please",
	}))
	evs := collectTurn(t, s.bus, "t-emb-tr", 15*time.Second)
	c := lastComplete(evs)
	if c == nil || c["status"] != "complete" {
		t.Fatalf("no complete: %+v", evs)
	}
	text, _ := c["text"].(string)
	if !strings.Contains(text, "预建任务完成") {
		t.Fatalf("tool_result 结果异常: %q", text)
	}
	// tool_result 任务节点由注册回调建/广播并收尾 done
	te.wait(t, "tool_result node done", func() bool {
		te.mu.Lock()
		defer te.mu.Unlock()
		for _, m := range te.started {
			if m["tool"] == "tool_result" {
				return true
			}
		}
		return false
	})
}

// TestPrjExecConfigHotApply：prj 执行配置 timeout_sec / max_concurrency / skip_dirs **保存即生效**
// （P0-B）——不重新注册实例，persist save → data-prj-config-refresh 广播 → 重跑 loadExecConfig →
// 注入内嵌 mcp-server 运行时。原始证据 = mcpCfg 的生效值（TimeoutSec / MaxConcurrency /
// Defaults.SkipDirs：分别由 execTimeout / limiter 上限 / defaultsMap 在 tools/call 运行期读取）。
// delete（前端「重置」）→ 回落 mcpms 内置默认（300 / 16 / 12 项）。
func TestPrjExecConfigHotApply(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServerMCP(t, llm)
	registerTestInstance(t, s) // instance-register → loadExecConfig 注入默认
	cfg := s.mcpCfg

	// G-27（2026-09-22）：`mcpCfg` 的运行期字段由**热生效 goroutine** 在 `loadExecConfig`（持
	// `s.execCfgMu`）内写入（prj save/delete 广播 → 后台 goroutine）；测试直接读字段会构成
	// data race（`go test -race` 实测）。故本用例读取一律经**同一互斥**（写 `-读` 之间建立
	// happens-before），断言口径不变。
	readCfg := func() (int, int, []string) {
		s.execCfgMu.Lock()
		defer s.execCfgMu.Unlock()
		return cfg.TimeoutSec, cfg.MaxConcurrency, append([]string(nil), cfg.Defaults.SkipDirs...)
	}

	// 基线 = mcpms 内置默认（未配置任何 prj 执行键）
	if to, mc, skip := readCfg(); to != 300 || mc != 16 || len(skip) != 12 {
		t.Fatalf("基线 ≠ 内置默认: timeout=%d concurrency=%d skip=%d(%v)", to, mc, len(skip), skip)
	}

	savePrj := func(key, value string) {
		t.Helper()
		if res := dataCall(t, s, "data-prj-config-save", map[string]any{
			"instance_id": "ins-test", "data": map[string]any{"key": key, "value": value},
		}); res["ok"] != true {
			t.Fatalf("save prj %s failed: %+v", key, res)
		}
	}
	delPrj := func(key string) {
		t.Helper()
		if res := dataCall(t, s, "data-prj-config-delete", map[string]any{
			"instance_id": "ins-test", "data": map[string]any{"id": key},
		}); res["ok"] != true {
			t.Fatalf("delete prj %s failed: %+v", key, res)
		}
	}
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if ok() {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		to, mc, skip := readCfg()
		t.Fatalf("超时未热生效: %s（timeout=%d concurrency=%d skip=%v）", what, to, mc, skip)
	}

	// 保存 → 无需重新注册即生效
	savePrj("timeout_sec", "45")
	savePrj("max_concurrency", "3")
	savePrj("skip_dirs", `["custom_skip"]`)
	waitFor("timeout_sec=45", func() bool { to, _, _ := readCfg(); return to == 45 })
	waitFor("max_concurrency=3", func() bool { _, mc, _ := readCfg(); return mc == 3 })
	waitFor(`skip_dirs=["custom_skip"]`, func() bool {
		_, _, skip := readCfg()
		return len(skip) == 1 && skip[0] == "custom_skip"
	})

	// 重置（delete）→ 回到内置默认（不残留旧覆盖）
	delPrj("skip_dirs")
	delPrj("timeout_sec")
	delPrj("max_concurrency")
	waitFor("重置 timeout_sec/并发/skip_dirs", func() bool {
		to, mc, skip := readCfg()
		return to == 300 && mc == 16 && len(skip) == 12
	})
	// 收尾：热生效走异步 goroutine（每次广播一个），末次断言成立时可能仍有掉队 goroutine 在读
	// prj 库——等其结束后再退出，避免 TempDir 清理时句柄仍被占用（测试环境专有）。
	time.Sleep(300 * time.Millisecond)
}
