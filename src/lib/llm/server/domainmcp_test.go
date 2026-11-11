// 真实内嵌 MCP 链路集成测试（21-llm-server，v2 分组注册面）：
// server lib 内嵌 mcp-server + gateway（DisableMCP=false，无 fakeGateway）——
// 域 agent 经场景注册；已迁 task 插件的四个会话编排工具由**测试适配**注册
// （见 moved_domain_test.go，模拟插件 tools/register + 宿主 plugin/v1/* 能力）；
// LLM 工具列表 = gateway tools/list 过滤 hot。
// 2026-11-08：core 已无域工具（`tool_result` 删除、四工具迁插件），故不再断言 core 契约 _meta。
package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
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
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// 2026-11-08：ask_user / next_run / subchat_* 已迁 task 插件；L1 用测试适配模拟插件
	// （见 moved_domain_test.go），保持 turn/gateway 链路覆盖。
	registerMigratedDomainTools(t, s)
	// data 层共享连接关闭须在 s.Stop 之后、TempDir 清理之前（LIFO；Windows 库句柄）。
	t.Cleanup(closeTestDataStore)
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
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	registerMigratedDomainTools(t, s)
	t.Cleanup(closeTestDataStore)
	t.Cleanup(s.Stop)
	return s
}

// TestEmbeddedGatewayDomainToolsHot：tools/list 应含全部已迁工具（测试适配注册）且 hot=true。
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
			// P0a 2026-10-29：暴露名 = 原名（内置 self 节点不再加前缀）；TrimPrefix 仅为兼容
			// 存量历史名（本轮已不再产生 self_*）。
			want[strings.TrimPrefix(d.Name, "self_")] = true
		}
	}
	for _, n := range []string{"ask_user", "next_run", "subchat_open", "subchat_finish"} {
		if !want[n] {
			t.Fatalf("已迁工具 %s 未在 tools/list 中（hot got %v）", n, want)
		}
	}
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

// TestDomainNodePurposeInjection（A4 · C1 / SUBCHAT-2 阶段 A）：外部工具的任务节点经**注入通道**
// 取本次调用的 purpose（`payload["context"]["purpose"]`，`plugin/v1/task.node.start` 载荷同源）——
// args 里的 `tool_call_display_name` 已被 gateway `doCall` 剥离，节点只能从这里兜底。
func TestDomainNodePurposeInjection(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServerMCP(t, llm)

	// ① 载荷解析（registered_provider.turnContextPayload 形状）
	const purpose = "整理当日要点汇总"
	payload := `{"tool":"subchat_open","args":{},` +
		`"context":{"instance_id":"ins-test","turn":"t-purpose","session":"s-purpose","tool_call_id":"tc-purpose","display_name":"` + purpose + `"}}`
	var msg movedDomainMsg
	if err := json.Unmarshal([]byte(payload), &msg); err != nil {
		t.Fatalf("unmarshal domain-tool-call: %v", err)
	}
	if msg.Context == nil || msg.Context.DisplayName != purpose {
		t.Fatalf("context.display_name 未解析：%+v", msg.Context)
	}

	// ② 注入值经 domainNode 兜底任务节 purpose（args 无展示名 → purpose 取注入值）
	tc := &turnCtx{server: s, req: StartReq{InstanceID: "ins-test", Session: "s-purpose", Turn: "t-purpose"},
		ctx: context.Background()}
	if _, err := s.domainNode(tc, "tc-purpose", "subchat_open", map[string]any{}, msg.Context.DisplayName); err != nil {
		t.Fatalf("domainNode: %v", err)
	}
	if n := s.tasks.findByToolCall("ins-test", "tc-purpose"); n == nil || n.Purpose != purpose {
		t.Fatalf("任务节 purpose 应由注入通道兜底：%+v", n)
	}
}
