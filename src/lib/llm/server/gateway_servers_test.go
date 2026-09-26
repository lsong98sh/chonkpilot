// 用户维护 MCP 启动装配单测（gateway_servers.go / T-25 / D-18）：
// 覆盖 mcps → ServerEntry 映射、enabled 过滤、URL 空跳过、usr 覆盖系统级同名；
// 以及「保存即生效」（T-25：data-user-config-refresh → servers/register|unregister 增量对账）。
package server

import (
	"context"
	"encoding/json"
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
	system := []usrMCPEntry{
		{Name: "sys1", URL: "http://sys1", Enabled: true, Description: "s", Transport: "sse"},
		{Name: "shared", URL: "http://sys-shared", Enabled: true},
		{Name: "sysdisabled", URL: "http://sd", Enabled: false},
		{Name: "sysnourl", URL: "", Enabled: true},
	}
	usr := []usrMCPEntry{
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
	usr := []usrMCPEntry{
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

// boolPtr 返回布尔字面量指针（usrMCPEntry.Isolate 三态：nil = 未设置 → 由 gateway 按 transport 推断）。
func boolPtr(b bool) *bool { return &b }

// TestMergeGatewayServersFieldPassthrough 规范字段全透传（含 runtime/args + env/headers/cwd/
// timeout/category/namespace/hot_tools/description）。
func TestMergeGatewayServersFieldPassthrough(t *testing.T) {
	usr := []usrMCPEntry{{
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
	none := mergeGatewayServers(nil, []usrMCPEntry{{Name: "n", Runtime: "node", Enabled: true}})
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

// TestLoadUserMCPEntries 覆盖 usr mcps 专用表读取 + 字段映射（含 enabled=false 保留，
// 由 merge 阶段过滤）。种子/读取**均经 data 门面**（不再从 data 根包取库句柄）。
func TestLoadUserMCPEntries(t *testing.T) {
	data.Reset()
	t.Cleanup(data.Reset)

	path := t.TempDir() + "/usr.db"
	api := inline.NewWithOptions(nil, persist.Options{UsrPath: path})
	if _, err := api.UserConfigSet(facade.UserConfigSetRequest{Entries: map[string]any{
		"mcpServers": []any{
			map[string]any{"name": "a", "url": "http://a", "enabled": true, "description": "desc-a", "transport": "direct"},
			map[string]any{"name": "b", "url": "http://b", "enabled": false},
			map[string]any{"name": "c", "url": "", "enabled": true},
			map[string]any{"name": "d", "runtime": "node", "args": []string{"d.js"}, "url": "", "enabled": true,
				"env": []string{"K=V"}, "category": "cat", "timeout": 9},
		},
	}}); err != nil {
		t.Fatalf("seed usr mcps via facade: %v", err)
	}

	s := &Server{cfg: api, opts: Options{UsrPath: path}}
	entries := s.loadUserMCPEntries()
	if len(entries) != 4 {
		t.Fatalf("want 4 raw entries, got %d: %+v", len(entries), entries)
	}
	if entries[0].Name != "a" || entries[0].URL != "http://a" || !entries[0].Enabled ||
		entries[0].Description != "desc-a" || entries[0].Transport != "direct" {
		t.Fatalf("entry a mismatch: %+v", entries[0])
	}
	if entries[1].Name != "b" || entries[1].Enabled {
		t.Fatalf("entry b mismatch: %+v", entries[1])
	}
	if entries[2].Name != "c" || entries[2].URL != "" {
		t.Fatalf("entry c mismatch: %+v", entries[2])
	}
	// record → struct 透传 runtime/args/【env[]】/category/timeout（落库读出后仍带全字段）。
	if entries[3].Name != "d" || entries[3].Runtime != "node" || entries[3].URL != "" ||
		len(entries[3].Args) != 1 || entries[3].Args[0] != "d.js" ||
		len(entries[3].Env) != 1 || entries[3].Env[0] != "K=V" ||
		entries[3].Category != "cat" || entries[3].TimeoutSec != 9 {
		t.Fatalf("entry d (runtime) mismatch: %+v", entries[3])
	}

	// 端到端：读表 → 合并过滤（b 未启用、c runtime/url 皆空 → 只余 a 与 d）；usr 来源恒为 user。
	got := mergeGatewayServers(nil, entries)
	if len(got) != 2 || got[0].ID != "a" || got[0].Transport != "" {
		t.Fatalf("merged result mismatch: %+v", got)
	}
	if got[1].ID != "d" || got[1].Runtime != "node" || got[1].URL != "" || got[1].TimeoutSec != 9 {
		t.Fatalf("merged runtime entry mismatch: %+v", got[1])
	}
	if got[0].Origin != mcpgateway.OriginUser || got[0].IsBuiltin() {
		t.Fatalf("usr mcps entry origin should be user: %+v", got[0])
	}
}

// TestLoadUserMCPEntriesMissingDB 缺失库路径也能安全返回（不 panic；门面开库会建库）。
func TestLoadUserMCPEntriesMissingDB(t *testing.T) {
	data.Reset()
	t.Cleanup(data.Reset)
	path := t.TempDir() + "/nope.db"
	s := &Server{cfg: inline.NewWithOptions(nil, persist.Options{UsrPath: path}), opts: Options{UsrPath: path}}
	if got := s.loadUserMCPEntries(); len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}

// ─── 保存即生效（T-25：enabled 开关 / 增删改 无需重启）────────────────

// saveUserMCPs 经真实保存面写 usr mcps：data-user-config-save（persist 落库 + 广播
// data-user-config-refresh，既有消息面，零新增主题）；返回时保存应答已回（对账后台异步进行）。
func saveUserMCPs(t *testing.T, s *Server, items []map[string]any) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"data": map[string]any{"mcpServers": items}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := s.bus.Emit(context.Background(), "data-user-config-save", payload).Wait().Err(); err != nil {
		t.Fatalf("data-user-config-save: %v", err)
	}
}

// TestUserMCPHotReload：usr mcps 新增 → 关闭 → 再启用，工具面**秒级**收敛（无需重启）。
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

	const exposed = "usr-hot_echo_hot"
	entry := func(enabled bool) []map[string]any {
		return []map[string]any{{"name": "usr-hot", "url": ts.URL, "enabled": enabled, "transport": "http"}}
	}

	// ① 新增（enabled=true）→ 工具面出现（保存后无需重启）。
	start := time.Now()
	saveUserMCPs(t, s, entry(true))
	elapsed, ok := waitTool(t, s, exposed, true, 10*time.Second)
	if !ok {
		t.Fatalf("① 新增 usr mcps 后工具未热生效（无重启应即生效）")
	}
	t.Logf("① 新增 → 工具面生效耗时 %v（起始 %v）", elapsed, time.Since(start))

	// ② 关闭（enabled=false）→ 工具退出工具面。
	start = time.Now()
	saveUserMCPs(t, s, entry(false))
	elapsed, ok = waitTool(t, s, exposed, false, 10*time.Second)
	if !ok {
		t.Fatalf("② 关闭 enabled 后工具未热移除")
	}
	t.Logf("② 关闭 → 工具面退出耗时 %v（起始 %v）", elapsed, time.Since(start))

	// ③ 再启用 → 工具回来（同一入口幂等，无残留路由冲突）。
	start = time.Now()
	saveUserMCPs(t, s, entry(true))
	elapsed, ok = waitTool(t, s, exposed, true, 10*time.Second)
	if !ok {
		t.Fatalf("③ 再启用后工具未热生效（可能残留旧路由冲突）")
	}
	t.Logf("③ 再启用 → 工具面生效耗时 %v（起始 %v）", elapsed, time.Since(start))

	// ④ 删除条目（整体替换为空）→ 工具退出。
	start = time.Now()
	saveUserMCPs(t, s, []map[string]any{})
	elapsed, ok = waitTool(t, s, exposed, false, 10*time.Second)
	if !ok {
		t.Fatalf("④ 删除条目后工具未热移除")
	}
	t.Logf("④ 删除 → 工具面退出耗时 %v（起始 %v）", elapsed, time.Since(start))
}
