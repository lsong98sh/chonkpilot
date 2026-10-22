// G-45 ②③（42 §2 (165)，2026-09-25）白盒：
//
//	② 场景子 agent 可发现 —— newTurnCtx 把当前场景的**子 agent**注入 gateway 资产目录
//	   （asset_kind=agent，instance 级 scope）→ 使 `mcp_find(type=agent)` 能列出并可经
//	   `llm_run` 委派（SCEN-007-S04）；同一场景反复开轮幂等不堆积、场景切换即更替、
//	   prompt 变更即更新。
//	③ 工具白名单**调用处硬拒** —— 白名单外工具（模型幻觉调用）在调用处分发前被拒（不落
//	   gateway、不建任务节点），结果以失败文本回喂；白名单内放行；空 / `[]` = 不限制；
//	   主 agent（未配 tools）不受影响。
//
// 项 5a（2026-09-25）追加：白名单**旁路加固** —— 白名单非空时，经 mcp_invoke 转调的
// **目标工具**也必须落在同一白名单内（白名单是「调用面」过滤而非沙箱）。
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/persist"
)

// gatewayToolName 按后缀定位 gateway 工具面中的**暴露名**（如 tool_stop → self_tool_stop）。
func gatewayToolName(t *testing.T, s *Server, suffix string) string {
	t.Helper()
	for _, d := range s.gc.Tools() {
		if strings.HasSuffix(d.Name, suffix) {
			return d.Name
		}
	}
	t.Fatalf("工具 %q 未在 gateway 工具面", suffix)
	return ""
}

// toolResultText 读某会话内一次工具调用的 role=tool 结果文本（未落库 → 失败）。
func toolResultText(t *testing.T, s *Server, session, toolCallID string) string {
	t.Helper()
	raw, ok := newSessionStore(s.bus, "ins-test").LoadToolContent(session, toolCallID)
	if !ok {
		t.Fatalf("未找到 %s/%s 的 role=tool 结果", session, toolCallID)
	}
	tc, ok := persist.ParseToolContent(raw)
	if !ok || tc.Result == nil {
		t.Fatalf("role=tool 结果不可解析：%s", raw)
	}
	return tc.Result.Content
}

// TestToolWhitelistHardReject（③）：白名单外工具在**调用处**被拒（不落 gateway、不建任务节点）；
// 白名单内放行；`[]` 与主 agent（未配 tools）= 不限制。
func TestToolWhitelistHardReject(t *testing.T) {
	// mock LLM：首回合发 `self_tool_result` 工具调用（预建节点 id）；拿到 tool 结果后收尾。
	var preNodeID string
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
			args := jb(map[string]any{"id": preNodeID, "timeout": 0})
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": ""}, ""),
				sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-wl", "type": "function",
					"function": map[string]any{"name": "self_tool_result", "arguments": string(args)},
				}}}, "tool_calls"),
			})
			return
		}
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": "收尾"}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	}))
	defer llm.Close()

	s := newTestServerMCP(t, llm)
	registerTestInstance(t, s)
	// 预建已终态任务节点：白名单放行时 tool_result 取回其摘要（= 「真正执行」的可观测证据）。
	pre, err := s.tasks.start(&TaskNode{
		Tool: "core_file_read", Kind: TaskKindTool, SessionID: "s-wl", TurnID: "t-wl",
		InstanceID: "ins-test", TopSession: "s-wl", Name: "pre", Purpose: "p",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	preNodeID = pre.TaskID
	s.tasks.done(pre.TaskID, TaskStateDone, "白名单放行结果", "")

	agSaveScenario(t, s, "ag-wl", []any{
		map[string]any{"name": "main", "isMain": true, "prompt": "主"},
		agSubAgent("sub-deny", "子", map[string]any{"tools": []string{"self_tool_stop"}}),
		agSubAgent("sub-allow", "子", map[string]any{"tools": []string{"self_tool_result"}}),
		agSubAgent("sub-free", "子", map[string]any{"tools": []string{}}), // `[]` = 不限制
	})
	parentReq := StartReq{InstanceID: "ins-test", Session: "s-wl", Turn: "t-wl", ScenarioID: "ag-wl"}

	// ① 白名单非空且不含被调工具 → 拒绝执行：结果 = 拒绝文案，且**未建任务节点**（未落 gateway）
	agRunChild(t, s, parentReq, "s-wl-deny", "do", "sub-deny")
	if got := toolResultText(t, s, "s-wl-deny", "tc-wl"); !strings.Contains(got, "白名单") {
		t.Fatalf("白名单外调用应被拒（结果 = %q）", got)
	}
	if got := toolResultText(t, s, "s-wl-deny", "tc-wl"); strings.Contains(got, "白名单放行结果") {
		t.Fatalf("白名单外工具被真正执行：%q", got)
	}
	if n := s.tasks.findByToolCall("ins-test", "tc-wl"); n != nil {
		t.Fatalf("白名单外调用不应建任务节点（未执行），实得 %+v", n)
	}

	// ② 白名单内 → 放行执行（取回预建节点摘要）
	agRunChild(t, s, parentReq, "s-wl-allow", "do", "sub-allow")
	if got := toolResultText(t, s, "s-wl-allow", "tc-wl"); !strings.Contains(got, "白名单放行结果") {
		t.Fatalf("白名单内工具应放行执行（结果 = %q）", got)
	}

	// ③ `[]` = 不限制
	agRunChild(t, s, parentReq, "s-wl-free", "do", "sub-free")
	if got := toolResultText(t, s, "s-wl-free", "tc-wl"); !strings.Contains(got, "白名单放行结果") {
		t.Fatalf("空白名单应不限制（结果 = %q）", got)
	}

	// ④ 主 agent（未配 tools）不受影响：顶层轮次同样放行
	agSaveScenario(t, s, "ag-wl-main", []any{
		map[string]any{"name": "main", "isMain": true, "prompt": "主"},
	})
	agStartTurn(t, s, "s-wl-main", "t-wl-main", "", "ag-wl-main")
	sendAndWait(t, s, "s-wl-main", "t-wl-main", "do")
	if got := toolResultText(t, s, "s-wl-main", "tc-wl"); !strings.Contains(got, "白名单放行结果") {
		t.Fatalf("主 agent（无白名单）应放行（结果 = %q）", got)
	}
}

// TestToolWhitelistIncludesNonHotTool（T1 / 25 §4.3，2026-09-25）：白名单语义修正的回归 ——
//
//	① 白名单含**非 hot** 工具 → 该工具**出现在**下发的 `tools` 参数
//	   （旧缺陷：`llmTools` = hot 集 ∩ 白名单 → 白名单里的非 hot 工具被吞掉，配了却永远传不到 LLM）；
//	② 空白名单（`[]`）→ 缺省下发面 = **hot 集**（非 hot 工具不出现；hot 工具在）。
//
// 驱动：注册一个非 hot 工具 → 刷新工具缓存 → 发子轮次（session-start + session-send）→
// 断言 mock LLM **请求体**的 tools 清单（发/收两侧均经 MQ）。
func TestToolWhitelistIncludesNonHotTool(t *testing.T) {
	rec, srv := newProviderServer(t)
	s := newTestServerMCP(t, srv)
	registerTestInstance(t, s)

	// 注册**非 hot** 工具（hot 缺省 false）→ gateway self 节点暴露名 = self_<name>
	if _, err := s.emitGateway(context.Background(), "tools/register", map[string]any{
		"name": "probe_nonhot", "description": "非 hot 探针工具",
		"handler_subject": SubjectDomainToolCall, "owner": "server",
	}); err != nil {
		t.Fatalf("tools/register(非 hot): %v", err)
	}
	s.refreshTools()
	nonHot := gatewayToolName(t, s, "probe_nonhot") // 未进入工具缓存即在此失败

	agSaveScenario(t, s, "ag-wl2", []any{
		map[string]any{"name": "main", "isMain": true, "prompt": "主"},
		agSubAgent("sub-wl", "子", map[string]any{"tools": []string{nonHot}}),
		agSubAgent("sub-free", "子", map[string]any{"tools": []string{}}), // `[]` = 不限制
	})
	parentReq := StartReq{InstanceID: "ins-test", Session: "s-wl2", Turn: "t-wl2", ScenarioID: "ag-wl2"}

	// ① 白名单含非 hot 工具 → 出现在下发 tools（T1 缺陷修正的正面证据）
	agRunChild(t, s, parentReq, "s-wl2-wl", "do", "sub-wl")
	got := agToolNames(mustBody(t, rec, 0))
	if !containsStr(got, nonHot) {
		t.Fatalf("白名单中的非 hot 工具未下发（T1 缺陷未修）：tools=%v want 含 %s", got, nonHot)
	}

	// ② 空白名单 → 缺省 hot 集：非 hot 工具不出现；hot 工具在
	agRunChild(t, s, parentReq, "s-wl2-free", "do", "sub-free")
	got2 := agToolNames(mustBody(t, rec, 1))
	if containsStr(got2, nonHot) {
		t.Fatalf("空白名单应只下 hot 集：非 hot 工具不应出现（tools=%v）", got2)
	}
	if !containsStr(got2, gatewayToolName(t, s, "tool_stop")) {
		t.Fatalf("空白名单应含 hot 工具（tool_stop）：tools=%v", got2)
	}
}

// TestToolWhitelistLevelMatrix（P4 2026-10-01，25 §4）：白名单按**场景级别的可用工具级别矩阵**
// 过滤（filterWhitelistByLevel）——同级/更高级保留、越权剔除、不存在剔除、场景级别不可判定则
// 原样返回（行为不变）、空白名单（nil）语义不变。
//
// 工具面就绪：app（tools/register → self_<name>）+ user / project / prjusr（hot 契约 + dir 节点）
// 四级各注册一个；断言直接读 filterWhitelistByLevel 返回值（与 llmTools / toolAllowed 同一输入集）。
func TestToolWhitelistLevelMatrix(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServerMCP(t, llm)
	registerTestInstance(t, s)

	// app 级：注册 self（系统）域工具 → 暴露名 self_<name>
	if _, err := s.emitGateway(context.Background(), "tools/register", map[string]any{
		"name": "lv_app", "description": "app 级探针", "handler_subject": SubjectDomainToolCall, "owner": "server",
	}); err != nil {
		t.Fatalf("tools/register(lv_app): %v", err)
	}
	s.refreshTools()
	appTool := gatewayToolName(t, s, "lv_app")

	// user / project 级：hot 契约 + 重扫接入（实例已注册）
	writeHotToolContract(t, persist.CapUserRoot(s.opts.UsrPath), "lv_user")
	writeHotToolContract(t, persist.CapProjectRoot(testWorkDir), "lv_prj")

	// prjusr 级：根由门面解析（重试容忍总线异步派发）
	var prjUsrRoot string
	for i := 0; i < 25 && prjUsrRoot == ""; i++ {
		prjUsrRoot = s.prjUsrCapRoot("ins-test")
		if prjUsrRoot == "" {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if prjUsrRoot == "" {
		t.Fatal("prjusr 根解析失败")
	}
	writeHotToolContract(t, prjUsrRoot, "lv_prjusr")
	s.registerCapabilityNodes("ins-test", testWorkDir) // 一次性重扫接入 user/project/prjusr
	waitCapabilityQuiescent(t, s)                     // 避开热重扫重建 dir 节点的间隙

	userTool := "ins-test-user_lv_user"
	prjTool := "ins-test-project_lv_prj"
	prjUsrTool := "ins-test-prjusr_lv_prjusr"

	visible := map[string]bool{}
	for _, d := range s.visibleTools("ins-test") {
		visible[d.Name] = true
	}
	for _, n := range []string{appTool, userTool, prjTool, prjUsrTool} {
		if !visible[n] {
			t.Fatalf("工具面未就绪，缺 %s（got %v）", n, visible)
		}
	}

	raw := func(names ...string) map[string]struct{} {
		out := map[string]struct{}{}
		for _, n := range names {
			out[n] = struct{}{}
		}
		return out
	}
	sortedNames := func(set map[string]struct{}) []string {
		out := make([]string, 0, len(set))
		for k := range set {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	// all 含一个**不存在**的工具名（self_no_such_tool）—— 应被静默剔除。
	all := []string{appTool, userTool, prjTool, prjUsrTool, "self_no_such_tool"}

	check := func(scenarioLevel string, want ...string) {
		t.Helper()
		got := sortedNames(s.filterWhitelistByLevel("ins-test", scenarioLevel, raw(all...)))
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("场景级别 %q 过滤错：got %v, want %v", scenarioLevel, got, want)
		}
	}

	check("prjusr", appTool, userTool, prjTool, prjUsrTool) // 四级全可用（不存在者剔除）
	check("project", appTool, prjTool)                      // 越权 user/prjusr 剔除
	check("user", appTool, userTool)                        // 越权 project/prjusr 剔除
	check("app", appTool)                                   // 越权 user/project/prjusr 剔除

	// 场景级别不可判定（空）→ 原样返回（含不存在者，不误剔除 = 行为不变）
	rawAll := raw(all...)
	if got := s.filterWhitelistByLevel("ins-test", "", rawAll); len(got) != len(rawAll) {
		t.Fatalf("场景级别空应原样返回：got %v", sortedNames(got))
	}
	// 空白名单（`""` / `[]`）→ parseToolWhitelist 返回 nil（= 不限制，调用方不进入过滤）
	if parseToolWhitelist("") != nil || parseToolWhitelist("[]") != nil {
		t.Fatal("空白名单应解析为 nil（不限制）")
	}
}
