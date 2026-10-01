// MCP 配置四级文件化（2026-10-01）白盒：
//   - 冷缓存退化放行：filterWhitelistByLevel 在可见工具缓存为空（未预热）时**原样返回**白名单
//     （不误剔合法工具）；缓存非空时按场景级别矩阵静默剔除（对照）；
//   - gateway 配置来源：四级文件化视图（McpList）∪ 旧 usr KV `mcpServers` 回落
//     （四级里没有该名才回落 KV）；**同名跨级只生效一份**（对账输入恒一名一条）。
package server

import (
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-data/persist"
)

// namedTool 造一个带 `_meta.server.node` 的 ToolDef（capability 级别可判定）。
func namedTool(name, node string) ToolDef {
	return ToolDef{Name: name, Meta: map[string]any{"server": map[string]any{"node": node}}}
}

// TestFilterWhitelistColdCachePassThrough 冷缓存（可见工具面空）→ 放行；非空 → 按矩阵剔除。
func TestFilterWhitelistColdCachePassThrough(t *testing.T) {
	raw := map[string]struct{}{"some_legal_tool": {}, "self_x": {}}

	// ① 冷缓存：buckets 为空（未预热 tools/list）→ 原样返回（放行，不误剔）
	cold := &Server{gc: newGWClient(nil)}
	got := cold.filterWhitelistByLevel("ins-cold", "app", raw)
	if len(got) != len(raw) {
		t.Fatalf("冷缓存应原样放行：got=%v want=%v", got, raw)
	}
	for k := range raw {
		if _, ok := got[k]; !ok {
			t.Fatalf("冷缓存放行缺 %q（应原样返回）", k)
		}
	}

	// ② 缓存非空（含 app 级 + user 级工具）→ 按场景级别矩阵剔除：app 场景只保留 app 级工具
	g := newGWClient(nil)
	g.tools = []ToolDef{namedTool("self_app_tool", "self"), namedTool("ins-warm-user_user_tool", "ins-warm-user")}
	g.buckets = map[string][]ToolDef{
		"":         {namedTool("self_app_tool", "self")},
		"ins-warm": {namedTool("ins-warm-user_user_tool", "ins-warm-user")},
	}
	warm := &Server{gc: g}
	raw2 := map[string]struct{}{"self_app_tool": {}, "ins-warm-user_user_tool": {}}
	got2 := warm.filterWhitelistByLevel("ins-warm", "app", raw2)
	if _, ok := got2["self_app_tool"]; !ok {
		t.Fatalf("app 场景应保留 app 级工具：%v", got2)
	}
	if _, ok := got2["ins-warm-user_user_tool"]; ok {
		t.Fatalf("app 场景应剔除越权 user 级工具：%v", got2)
	}
}

// TestLoadEffectiveMcpEntriesKvFallback 四级文件化视图 + 旧 KV 回落；同名只一份（文件优先）。
func TestLoadEffectiveMcpEntriesKvFallback(t *testing.T) {
	data.Reset()
	t.Cleanup(data.Reset)
	path := t.TempDir() + "/usr.db"
	api := inline.NewWithOptions(nil, persist.Options{UsrPath: path, AppDir: t.TempDir()})

	// 旧 usr KV `mcpServers`：legacy1（仅 KV）+ shared（KV 版，将被文件版覆盖）
	if _, err := api.UserConfigSet(facade.UserConfigSetRequest{Entries: map[string]any{
		"mcpServers": []any{
			map[string]any{"name": "legacy1", "url": "http://legacy1", "enabled": true},
			map[string]any{"name": "shared", "url": "http://kv-shared", "enabled": true},
		},
	}}); err != nil {
		t.Fatalf("seed usr mcps KV: %v", err)
	}
	// 四级文件化：shared（user 级，覆盖 KV 同名）+ file1
	for _, srv := range []facade.McpServer{
		{Name: "shared", URL: "http://file-shared", Enabled: true, Level: "user"},
		{Name: "file1", URL: "http://file1", Enabled: true, Level: "user"},
	} {
		if _, err := api.McpSave(facade.McpSaveRequest{Server: srv}); err != nil {
			t.Fatalf("McpSave(%s): %v", srv.Name, err)
		}
	}

	s := &Server{cfg: api, opts: Options{UsrPath: path}}
	entries := s.loadEffectiveMCPEntries()

	byName := map[string]usrMCPEntry{}
	for _, e := range entries {
		if _, dup := byName[e.Name]; dup {
			t.Fatalf("同名跨级/跨源应只生效一份：%q 重复", e.Name)
		}
		byName[e.Name] = e
	}
	if len(entries) != 3 {
		t.Fatalf("生效条目应 = shared + file1 + legacy1 = 3，got %d：%+v", len(entries), entries)
	}
	if byName["shared"].URL != "http://file-shared" {
		t.Fatalf("同名应以文件为准（覆盖 KV）：%+v", byName["shared"])
	}
	if byName["legacy1"].URL != "http://legacy1" {
		t.Fatalf("四级里没有的 legacy1 应回落旧 KV：%+v", byName["legacy1"])
	}
	if byName["file1"].URL != "http://file1" {
		t.Fatalf("文件条目缺失：%+v", byName["file1"])
	}

	// 下发到 gateway 的 ServerEntry 亦恒一名一条
	gatewayEntries := mergeGatewayServers(nil, entries)
	seen := map[string]int{}
	for _, e := range gatewayEntries {
		seen[e.ID]++
	}
	if len(gatewayEntries) != 3 || seen["shared"] != 1 || seen["legacy1"] != 1 || seen["file1"] != 1 {
		t.Fatalf("gateway 生效集应一名一条：%+v", gatewayEntries)
	}
}
