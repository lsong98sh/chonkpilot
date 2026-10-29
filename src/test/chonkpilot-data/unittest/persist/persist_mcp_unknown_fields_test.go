// MCP 文件写入的「未知字段保留」（2026-10-29，[61-消息一览 §3.1]）契约单测：
//   - `data-mcp-save` 的 `data` 中本项目 DTO **未声明**的字段 → 原样落盘（round-trip 不丢）；
//   - 读侧 DTO（`data-mcp-load`）形状不变（忽略未知字段）；原始 map 走门面 `McpGetRaw`；
//   - 未知字段名与已知字段同名 → **已知字段优先**（未知侧丢弃）。
//
// 驱动方式 = MQ 消息（`data-mcp-save` / `data-mcp-load`）+ 订阅 `data-mcp-refresh` 断言广播。
package persist_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

// TestDataMcpSaveKeepsUnknownFields（L1）：带未知字段的 save → 落盘保留 → 读回（原始 map 路径）
// 含未知字段；DTO 读侧字段语义不变；未知字段与已知字段同名 → 已知优先。
func TestDataMcpSaveKeepsUnknownFields(t *testing.T) {
	bus, svc, usrPath := newTestPersist(t)
	regInstance(t, bus)
	ch := collectRefresh(t, bus, "mcp")

	// ① 带未知字段（含嵌套对象）保存：应答形状不变 + 1 条刷新广播
	res := dataResult(t, dataCall(t, bus, "data-mcp-save", map[string]any{
		"req_id": "r1", "instance_id": "ins-test",
		"data": map[string]any{
			"name": "ext_mcp", "level": "user", "url": "http://u", "enabled": true,
			"vendor_flag": "keep", "x_note": map[string]any{"a": 1},
			"old_name": "was_mcp", // 信封/路由键（前端放在 data 内）→ 不得当未知字段落盘
		},
	}))
	if res["ok"] != true || res["name"] != "ext_mcp" {
		t.Fatalf("save 应答形状不符：%+v", res)
	}
	waitRefresh(t, ch, "mcp")
	expectNoMoreRefresh(t, ch, 200*time.Millisecond)

	// 落盘文件（user 级 = <usr 库所在目录>/capability/mcps/<名>.json）含未知字段
	mcpFile := filepath.Join(filepath.Dir(usrPath), "capability", "mcps", "ext_mcp.json")
	body, err := os.ReadFile(mcpFile)
	if err != nil {
		t.Fatalf("定义文件未落盘 %s: %v", mcpFile, err)
	}
	if !strings.Contains(string(body), `"vendor_flag": "keep"`) || !strings.Contains(string(body), `"x_note"`) {
		t.Fatalf("未知字段应原样落盘：%s", body)
	}
	if strings.Contains(string(body), "old_name") {
		t.Fatalf("信封/路由键（old_name）不得当未知字段落盘：%s", body)
	}

	// 读回：原始 map 路径含未知字段；DTO 读侧（data-mcp-load）字段语义不变
	raw, err := svc.McpGetRaw(facade.McpGetRequest{InstanceID: facadeInstance, Name: "ext_mcp"})
	if err != nil || raw.Raw["vendor_flag"] != "keep" {
		t.Fatalf("McpGetRaw 应含未知字段：%+v err=%v", raw.Raw, err)
	}
	load := dataResult(t, dataCall(t, bus, "data-mcp-load", map[string]any{
		"req_id": "r2", "instance_id": facadeInstance, "name": "ext_mcp",
	}))
	data, _ := load["data"].(map[string]any)
	if data["url"] != "http://u" || data["enabled"] != true {
		t.Fatalf("DTO 读侧字段语义不该变化：%+v", data)
	}

	// ② 未知字段名与已知字段同名 → 已知字段优先（未知侧丢弃）
	ch2 := collectRefresh(t, bus, "mcp")
	if _, err := svc.McpSave(facade.McpSaveRequest{
		InstanceID: facadeInstance,
		Server: facade.McpServer{
			Name: "conflict_mcp", Level: "user", URL: "http://real", Enabled: true,
			Extra: map[string]any{"url": "http://evil", "vendor_flag": "keep2"},
		},
	}); err != nil {
		t.Fatalf("McpSave(conflict): %v", err)
	}
	waitRefresh(t, ch2, "mcp")
	raw2, err := svc.McpGetRaw(facade.McpGetRequest{InstanceID: facadeInstance, Name: "conflict_mcp"})
	if err != nil {
		t.Fatalf("McpGetRaw(conflict): %v", err)
	}
	if raw2.Raw["url"] != "http://real" {
		t.Fatalf("同名冲突须已知字段优先：%+v", raw2.Raw)
	}
	if raw2.Raw["vendor_flag"] != "keep2" {
		t.Fatalf("非冲突的未知字段仍应保留：%+v", raw2.Raw)
	}
}
