// 用户维护 MCP 启动装配单测（gateway_servers.go / T-25 / D-18）：
// 覆盖四级文件化 MCP → ServerEntry 映射、enabled 过滤、URL 空跳过、文件定义覆盖基底同名；
// 以及「保存即生效」（T-25：data-mcp-refresh → servers/register|unregister 增量对账）。
package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-data/persist"
	mcpgateway "github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMergeGatewayServers 覆盖合并/映射/过滤规则（纯函数）。
func TestMergeGatewayServers(t *testing.T) {
	system := []mcpEntry{
		{Name: "sys1", URL: "http://sys1", Enabled: true, Description: "s", Transport: "sse"},
		{Name: "shared", URL: "http://sys-shared", Enabled: true},
		{Name: "sysdisabled", URL: "http://sd", Enabled: false},
		{Name: "sysnourl", URL: "", Enabled: true},
	}
	usr := []mcpEntry{
		{Name: "usr1", URL: "http://usr1", Enabled: true, Description: "u", Transport: "direct"},
		{Name: "shared", URL: "http://usr-shared", Enabled: true, Description: "override"},
		{Name: "usrdisabled", URL: "http://ud", Enabled: false},
		{Name: "usrnourl", Enabled: true},
	}

	got := mergeGatewayServers(system, usr)

	// 过滤后只余 3 条：sysdisabled/usrdisabled（未启用）、sysnourl/usrnourl（runtime/url 皆空）被跳过。
	if len(got) != 3 {
		t.Fatalf("want 3 entries, got %d: %+v", len(got), got)
	}
	// 顺序 = 系统级在前、usr 新增在后（shared 已被 usr 覆盖仍占系统级位置）。
	if got[0].ID != "sys1" || got[1].ID != "shared" || got[2].ID != "usr1" {
		t.Fatalf("unexpected ids order: %s,%s,%s", got[0].ID, got[1].ID, got[2].ID)
	}
	// 映射：ID/Name ← name、URL、Description、Transport、Scope 留空=global、Enabled=true。
	if got[0].Name != "sys1" || got[0].URL != "http://sys1" || got[0].Description != "s" ||
		got[0].Transport != "sse" || got[0].Scope != "" || !got[0].Enabled {
		t.Fatalf("sys1 mapping mismatch: %+v", got[0])
	}
	// 来源（Origin）：系统级 config.json = builtin；usr mcps = user（第三方）。
	if got[0].Origin != mcpgateway.OriginBuiltin || !got[0].IsBuiltin() {
		t.Fatalf("system entry origin should be builtin: %+v", got[0])
	}
	if got[2].Origin != mcpgateway.OriginUser || got[2].IsBuiltin() {
		t.Fatalf("usr entry origin should be user: %+v", got[2])
	}
	// usr 覆盖系统级同名（shared）。
	if got[1].URL != "http://usr-shared" || got[1].Description != "override" {
		t.Fatalf("usr should override system entry: %+v", got[1])
	}
	// 同名 usr 覆盖系统级 → 来源随之为 user（安全默认，不得残留 builtin）。
	if got[1].Origin != mcpgateway.OriginUser || got[1].IsBuiltin() {
		t.Fatalf("usr-overridden entry origin should be user: %+v", got[1])
	}
	// transport "direct" 归一为空（gateway 按 URL 推断 http/proxied）。
	if got[2].Transport != "" {
		t.Fatalf("direct transport should normalize to empty, got %q", got[2].Transport)
	}
	if got[2].Description != "u" {
		t.Fatalf("usr1 description mismatch: %q", got[2].Description)
	}
}

// TestMergeGatewayServersEmpty 空来源 → 空列表（不 panic）。
func TestMergeGatewayServersEmpty(t *testing.T) {
	if got := mergeGatewayServers(nil, nil); len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}

// TestMergeGatewayServersSpawned 覆盖 runtime/url 分流（D-18 扩展）：
// 仅 runtime → spawned（Runtime 有值、URL 空）；仅 url → proxied（传统行为不变）；
// 两者皆空 → 跳过。
func TestMergeGatewayServersSpawned(t *testing.T) {
	usr := []mcpEntry{
		{Name: "spawned1", Runtime: "npx", Args: []string{"-y", "foo-mcp"}, Enabled: true}, // 仅 runtime
		{Name: "proxied1", URL: "http://p", Enabled: true},                                 // 仅 url
		{Name: "both", Runtime: "run-mcp", URL: "http://b", Enabled: true},                 // 两者皆有
		{Name: "neither", Enabled: true},                                                   // 皆空 → 跳过
		{Name: "blankrt", Runtime: "   ", Enabled: true},                                   // 空白 runtime 视同空 → 跳过
	}
	got := mergeGatewayServers(nil, usr)
	if len(got) != 3 {
		t.Fatalf("want 3 entries (neither/blankrt skipped), got %d: %+v", len(got), got)
	}
	// 仅 runtime：Runtime/Args 有值且 URL 空（网关 spawned 分支）。
	if got[0].ID != "spawned1" || got[0].Runtime != "npx" || got[0].URL != "" ||
		len(got[0].Args) != 2 || got[0].Args[0] != "-y" || got[0].Args[1] != "foo-mcp" {
		t.Fatalf("runtime-only should map to Runtime/Args only: %+v", got[0])
	}
	// 仅 url：传统 proxied 行为不变。
	if got[1].ID != "proxied1" || got[1].URL != "http://p" || got[1].Runtime != "" {
		t.Fatalf("url-only should map to URL only: %+v", got[1])
	}
	// 两者皆有：Runtime 与 URL 同时保留（网关按 Runtime 判 spawned）。
	if got[2].ID != "both" || got[2].Runtime != "run-mcp" || got[2].URL != "http://b" {
		t.Fatalf("both runtime+url should both map: %+v", got[2])
	}
}

// boolPtr 返回布尔字面量指针（mcpEntry.Isolate 三态：nil = 未设置 → 由 gateway 按 transport 推断）。
func boolPtr(b bool) *bool { return &b }

// TestMergeGatewayServersFieldPassthrough 规范字段全透传（含 runtime/args + env/headers/cwd/
// timeout/category/namespace/hot_tools/description）。
func TestMergeGatewayServersFieldPassthrough(t *testing.T) {
	usr := []mcpEntry{{
		Name:        "full",
		Runtime:     "node",
		Args:        []string{"server.js", "--port", "1"},
		URL:         "http://full",
		Enabled:     true,
		Description: "d",
		Category:    "cat",
		Namespace:   "ns",
		Env:         []string{"A=1", "B=2"},
		Headers:     map[string]string{"X-K": "v"},
		Cwd:         "e:/work",
		HotTools:    []string{"t1", "t2"},
		TimeoutSec:  42,
		Transport:   "stdio",
		Isolate:     boolPtr(false), // 显式「不隔离」须透传（三态：nil = 未设置）
	}}
	got := mergeGatewayServers(nil, usr)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %+v", got)
	}
	e := got[0]
	if e.Isolate == nil || *e.Isolate || e.IsolateEnabled() {
		t.Fatalf("isolate=false 未透传：%+v", e)
	}
	// 未设置（nil）→ 按 transport 推断（stdio → 隔离）
	none := mergeGatewayServers(nil, []mcpEntry{{Name: "n", Runtime: "node", Enabled: true}})
	if len(none) != 1 || none[0].Isolate != nil || !none[0].IsolateEnabled() {
		t.Fatalf("缺 isolate 应保持未设置并按 transport 推断：%+v", none)
	}
	if e.Runtime != "node" || e.URL != "http://full" || e.Description != "d" ||
		e.Category != "cat" || e.Namespace != "ns" || e.Cwd != "e:/work" ||
		e.TimeoutSec != 42 || e.Transport != "stdio" {
		t.Fatalf("scalar fields passthrough mismatch: %+v", e)
	}
	if len(e.Args) != 3 || e.Args[0] != "server.js" || e.Args[1] != "--port" || e.Args[2] != "1" {
		t.Fatalf("args passthrough mismatch: %+v", e.Args)
	}
	if len(e.Env) != 2 || e.Env[0] != "A=1" || e.Env[1] != "B=2" {
		t.Fatalf("env passthrough mismatch: %+v", e.Env)
	}
	if e.Headers["X-K"] != "v" {
		t.Fatalf("headers passthrough mismatch: %+v", e.Headers)
	}
	if len(e.HotTools) != 2 || e.HotTools[0] != "t1" || e.HotTools[1] != "t2" {
		t.Fatalf("hot_tools passthrough mismatch: %+v", e.HotTools)
	}
}

// TestLoadMcpFileEntries 覆盖四级文件化 MCP 读取 + 字段映射（含 enabled=false 保留，
// 由 merge 阶段过滤）。种子/读取**均经 data 门面**（不持库句柄）。
func TestLoadMcpFileEntries(t *testing.T) {
	data.Reset()
	t.Cleanup(data.Reset)

	path := t.TempDir() + "/usr.db"
	api := inline.NewWithOptions(nil, persist.Options{UsrPath: path, AppDir: t.TempDir()})
	for _, srv := range []facade.McpServer{
		{Name: "a", URL: "http://a", Enabled: true, Description: "desc-a", Transport: "direct", Level: "user"},
		{Name: "b", URL: "http://b", Enabled: false, Level: "user"},
		{Name: "c", Enabled: true, Level: "user"},
		{Name: "d", Runtime: "node", Args: []string{"d.js"}, Enabled: true, Env: []string{"K=V"},
			Category: "cat", Timeout: 9, Level: "user"},
	} {
		if _, err := api.McpSave(facade.McpSaveRequest{Server: srv}); err != nil {
			t.Fatalf("McpSave(%s): %v", srv.Name, err)
		}
	}

	s := &Server{cfg: api, opts: Options{UsrPath: path}}
	entries := s.loadMcpFileEntries("")
	if len(entries) != 4 {
		t.Fatalf("want 4 raw entries, got %d: %+v", len(entries), entries)
	}
	byName := map[string]mcpEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	if e := byName["a"]; e.URL != "http://a" || !e.Enabled || e.Description != "desc-a" || e.Transport != "direct" {
		t.Fatalf("entry a mismatch: %+v", e)
	}
	if e := byName["b"]; e.Enabled {
		t.Fatalf("entry b mismatch: %+v", e)
	}
	if e := byName["c"]; e.URL != "" {
		t.Fatalf("entry c mismatch: %+v", e)
	}
	// 文件 → struct 透传 runtime/args/env/category/timeout（读出后仍带全字段）。
	if e := byName["d"]; e.Runtime != "node" || e.URL != "" ||
		len(e.Args) != 1 || e.Args[0] != "d.js" ||
		len(e.Env) != 1 || e.Env[0] != "K=V" ||
		e.Category != "cat" || e.TimeoutSec != 9 {
		t.Fatalf("entry d (runtime) mismatch: %+v", e)
	}

	// 端到端：读文件 → 合并过滤（b 未启用、c runtime/url 皆空 → 只余 a 与 d）；来源恒为 user。
	got := mergeGatewayServers(nil, entries)
	if len(got) != 2 {
		t.Fatalf("merged result count mismatch: %+v", got)
	}
	byID := map[string]mcpgateway.ServerEntry{}
	for _, e := range got {
		byID[e.ID] = e
	}
	if e := byID["a"]; e.Transport != "" || e.Origin != mcpgateway.OriginUser || e.IsBuiltin() {
		t.Fatalf("merged a mismatch: %+v", e)
	}
	if e := byID["d"]; e.Runtime != "node" || e.URL != "" || e.TimeoutSec != 9 {
		t.Fatalf("merged runtime entry mismatch: %+v", e)
	}
}

// ─── 保存即生效（T-25：enabled 开关 / 增删改 无需重启）────────────────

// saveFileMCP 经 mcp 域保存（落 `<级别>/capability/mcps/<名>.json` + 广播 data-mcp-refresh，
// 既有消息面，零新增主题）；返回时保存应答已回（对账后台异步进行）。
func saveFileMCP(t *testing.T, s *Server, srv facade.McpServer) {
	t.Helper()
	if _, err := s.cfg.McpSave(facade.McpSaveRequest{Server: srv}); err != nil {
		t.Fatalf("McpSave(%s): %v", srv.Name, err)
	}
}

// TestUserMCPHotReload：四级文件化 MCP 条目新增 → 关闭 → 再启用，工具面**秒级**收敛（无需重启）。
// 下游 = httptest 承载的 streamable HTTP MCP server（工具 echo_hot，暴露名 <name>_echo_hot）。
func TestUserMCPHotReload(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServerMCP(t, llm) // 内嵌 gateway + 真实内嵌 persist（UsrPath = 临时库）

	ms := mcp.NewServer(&mcp.Implementation{Name: "hot-mock", Version: "1.0.0"}, nil)
	ms.AddTool(
		&mcp.Tool{Name: "echo_hot", Description: "hot", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
		})
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, nil))
	defer ts.Close()

	const exposed = "usrhot_echo_hot"
	entry := func(enabled bool) facade.McpServer {
		return facade.McpServer{Name: "usrhot", URL: ts.URL, Enabled: enabled, Transport: "http"}
	}

	// ① 新增（enabled=true）→ 工具面出现（保存后无需重启）。
	start := time.Now()
	saveFileMCP(t, s, entry(true))
	elapsed, ok := waitTool(t, s, exposed, true, 10*time.Second)
	if !ok {
		t.Fatalf("① 新增 MCP 后工具未热生效（无重启应即生效）")
	}
	t.Logf("① 新增 → 工具面生效耗时 %v（起始 %v）", elapsed, time.Since(start))

	// ② 关闭（enabled=false）→ 工具退出工具面。
	start = time.Now()
	saveFileMCP(t, s, entry(false))
	elapsed, ok = waitTool(t, s, exposed, false, 10*time.Second)
	if !ok {
		t.Fatalf("② 关闭 enabled 后工具未热移除")
	}
	t.Logf("② 关闭 → 工具面退出耗时 %v（起始 %v）", elapsed, time.Since(start))

	// ③ 再启用 → 工具回来（同一入口幂等，无残留路由冲突）。
	start = time.Now()
	saveFileMCP(t, s, entry(true))
	elapsed, ok = waitTool(t, s, exposed, true, 10*time.Second)
	if !ok {
		t.Fatalf("③ 再启用后工具未热生效（可能残留旧路由冲突）")
	}
	t.Logf("③ 再启用 → 工具面生效耗时 %v（起始 %v）", elapsed, time.Since(start))

	// ④ 删除条目（删文件）→ 工具退出。
	start = time.Now()
	if _, err := s.cfg.McpDelete(facade.McpDeleteRequest{Name: "usrhot"}); err != nil {
		t.Fatalf("McpDelete: %v", err)
	}
	elapsed, ok = waitTool(t, s, exposed, false, 10*time.Second)
	if !ok {
		t.Fatalf("④ 删除条目后工具未热移除")
	}
	t.Logf("④ 删除 → 工具面退出耗时 %v（起始 %v）", elapsed, time.Since(start))
}

// ─── A 缺陷修复：instance_id 一路透传到数据层 McpList ──────────────────

// recordingMcpAPI 只覆写 McpList 记录收到的 instance_id（其余方法经内嵌 nil 接口，未被调用）。
type recordingMcpAPI struct {
	facade.API
	called     bool
	instanceID string
}

func (r *recordingMcpAPI) McpList(req facade.McpListRequest) (facade.McpListResponse, error) {
	r.called = true
	r.instanceID = req.InstanceID
	return facade.McpListResponse{}, nil
}

// TestLoadMcpFileEntriesPassesInstanceID 覆盖 A：loadMcpFileEntries / loadGatewayServers 必须把
// instance_id 透传到数据层 McpList（否则多实例下落到"唯一实例回退"串库/失败）；启动期传空。
func TestLoadMcpFileEntriesPassesInstanceID(t *testing.T) {
	rec := &recordingMcpAPI{}
	if got := (&Server{cfg: rec}).loadMcpFileEntries("ins-42"); len(got) != 0 {
		t.Fatalf("桩返回空列表，条目应为 0：%+v", got)
	}
	if !rec.called || rec.instanceID != "ins-42" {
		t.Fatalf("loadMcpFileEntries 应把 instance_id 透传 McpList：called=%v got=%q", rec.called, rec.instanceID)
	}

	// 启动期（Server.New 尚无实例）→ 空 instance_id。
	rec2 := &recordingMcpAPI{}
	(&Server{cfg: rec2}).loadGatewayServers("")
	if !rec2.called || rec2.instanceID != "" {
		t.Fatalf("启动期应传空 instance_id：called=%v got=%q", rec2.called, rec2.instanceID)
	}
}
