// MCP 配置四级文件化（2026-10-01）白盒：
//   - 冷缓存退化放行：filterWhitelistByLevel 在可见工具缓存为空（未预热）时**原样返回**白名单
//     （不误剔合法工具）；缓存非空时按场景级别矩阵静默剔除（对照）；
//   - gateway 配置来源：四级文件化视图（McpList）**唯一**来源（旧 usr KV `mcpServers` 已彻底废弃、
//     代码零兼容）；**同名跨级只生效一份**（数据层按名整条覆盖 → 对账输入恒一名一条）。
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

// TestLoadGatewayServersFileView 四级文件化视图 = 唯一来源；同名跨级只一份（最具体级整条覆盖）。
func TestLoadGatewayServersFileView(t *testing.T) {
	data.Reset()
	t.Cleanup(data.Reset)
	path := t.TempDir() + "/usr.db"
	api := inline.NewWithOptions(nil, persist.Options{UsrPath: path, AppDir: t.TempDir()})

	// app 级 shared + file1；user 级 shared 同名（应整条覆盖 app 级）。
	for _, srv := range []facade.McpServer{
		{Name: "shared", URL: "http://app-shared", Enabled: true, Level: "app"},
		{Name: "shared", URL: "http://user-shared", Enabled: true, Level: "user"},
		{Name: "file1", URL: "http://file1", Enabled: true, Level: "user"},
	} {
		if _, err := api.McpSave(facade.McpSaveRequest{Server: srv}); err != nil {
			t.Fatalf("McpSave(%s): %v", srv.Name, err)
		}
	}

	s := &Server{cfg: api, opts: Options{UsrPath: path}}
	entries := s.loadMcpFileEntries("")

	byName := map[string]mcpEntry{}
	for _, e := range entries {
		if _, dup := byName[e.Name]; dup {
			t.Fatalf("同名跨级应只生效一份：%q 重复", e.Name)
		}
		byName[e.Name] = e
	}
	if len(entries) != 2 {
		t.Fatalf("生效条目应 = shared + file1 = 2，got %d：%+v", len(entries), entries)
	}
	if byName["shared"].URL != "http://user-shared" {
		t.Fatalf("同名应以最具体级（user）为准：%+v", byName["shared"])
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
	if len(gatewayEntries) != 2 || seen["shared"] != 1 || seen["file1"] != 1 {
		t.Fatalf("gateway 生效集应一名一条：%+v", gatewayEntries)
	}
}
