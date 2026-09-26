// 来源标注驱动的 `_meta` 注入用例（R-11 边界，2026-09-12）：
//   - Origin=builtin（本仓自有/内置）→ 下游收到 `_meta["chonkpilot"]`；
//   - Origin=user / 缺省（第三方，用户定义）→ 不携带 `_meta`；
//   - servers/register 载荷可选 `origin`：缺省 user（安全默认），显式 builtin 才注入。
//
// 被测下游 = httptest 承载的 streamable HTTP MCP server（工具 echo_meta 回显收到的 _meta）。
package mcpgatewaytest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpgw "github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
)

// metaEcho 是回显 `_meta["chonkpilot"]` 的下游 server（记录最近一次收到的命名空间）。
type metaEcho struct {
	ts       *httptest.Server
	captured atomic.Value // map[string]any（无 _meta → nil）
}

// startMetaEcho 启动回显 server（streamable HTTP，工具名 echo_meta）。
func startMetaEcho(t *testing.T) *metaEcho {
	t.Helper()
	e := &metaEcho{}
	ms := mcp.NewServer(&mcp.Implementation{Name: "meta-echo", Version: "1.0.0"}, nil)
	ms.AddTool(
		&mcp.Tool{
			Name:        "echo_meta",
			Description: "echo _meta",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var ns map[string]any
			if req.Params != nil && req.Params.Meta != nil {
				ns, _ = req.Params.Meta["chonkpilot"].(map[string]any)
			}
			e.captured.Store(ns)
			b, _ := json.Marshal(ns)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil
		})
	e.ts = httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, nil))
	t.Cleanup(e.ts.Close)
	return e
}

// metaOf 取最近一次回显的 `_meta["chonkpilot"]`（nil = 下游未收到 _meta）。
func (e *metaEcho) metaOf(t *testing.T) map[string]any {
	t.Helper()
	v, _ := e.captured.Load().(map[string]any)
	return v
}

// startOriginGW 装配一个只接入 `entries` 下游的 gateway（无 self 能力源）。
func startOriginGW(t *testing.T, entries []mcpgw.ServerEntry) mq.Bus {
	t.Helper()
	ctx := context.Background()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	gw, err := mcpgw.New(mcpgw.Params{
		Bus: bus, Servers: entries, CallTimeout: 5 * time.Second, MaxTasks: 4, Logf: t.Logf,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := gw.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		_ = gw.Stop(context.Background())
		bus.Close()
	})
	return bus
}

// callWithCtx 以带调用上下文的 tools/call 触发一次下游调用。
func callWithCtx(t *testing.T, bus mq.Bus, name string) map[string]any {
	t.Helper()
	return callMethod(t, bus, "tools/call", map[string]any{
		"name":        name,
		"instance_id": "ins-1",
		"work_dir":    `C:\work\proj`,
		"data_dir":    `C:\data`,
	})
}

// TestMetaInjectedByOrigin 来源标注为 builtin → 注入 `_meta`；user/缺省 → 不注入。
func TestMetaInjectedByOrigin(t *testing.T) {
	// builtin
	{
		echo := startMetaEcho(t)
		bus := startOriginGW(t, []mcpgw.ServerEntry{
			{ID: "bi", Name: "bi", URL: echo.ts.URL, Enabled: true, Origin: mcpgw.OriginBuiltin},
		})
		res := callWithCtx(t, bus, "bi_echo_meta")
		if res["isError"] == true {
			t.Fatalf("builtin 调用失败: %v", textOf(t, res))
		}
		meta := echo.metaOf(t)
		if meta == nil {
			t.Fatalf("builtin 下游应收到 _meta，got nil（result=%v）", textOf(t, res))
		}
		if meta["work_dir"] != `C:\work\proj` || meta["instance_id"] != "ins-1" {
			t.Fatalf("builtin _meta 内容不符: %v", meta)
		}
	}
	// user
	{
		echo := startMetaEcho(t)
		bus := startOriginGW(t, []mcpgw.ServerEntry{
			{ID: "us", Name: "us", URL: echo.ts.URL, Enabled: true, Origin: mcpgw.OriginUser},
		})
		res := callWithCtx(t, bus, "us_echo_meta")
		if res["isError"] == true {
			t.Fatalf("user 调用失败: %v", textOf(t, res))
		}
		if meta := echo.metaOf(t); meta != nil {
			t.Fatalf("user 下游不应收到 _meta，got %v", meta)
		}
	}
	// 缺省（未标注）→ 安全默认 user
	{
		echo := startMetaEcho(t)
		bus := startOriginGW(t, []mcpgw.ServerEntry{
			{ID: "dflt", Name: "dflt", URL: echo.ts.URL, Enabled: true},
		})
		res := callWithCtx(t, bus, "dflt_echo_meta")
		if res["isError"] == true {
			t.Fatalf("缺省调用失败: %v", textOf(t, res))
		}
		if meta := echo.metaOf(t); meta != nil {
			t.Fatalf("缺省来源应按 user（不注入），got %v", meta)
		}
	}
}

// TestServersRegisterOrigin servers/register 载荷 origin：缺省 user（不注入）；显式 builtin 注入。
func TestServersRegisterOrigin(t *testing.T) {
	echo := startMetaEcho(t)
	bus := startOriginGW(t, nil) // 无静态下游，经消息面动态接入

	// 缺省（不带 origin）→ user
	callMethod(t, bus, "servers/register", map[string]any{"name": "u1", "url": echo.ts.URL})
	res := callWithCtx(t, bus, "u1_echo_meta")
	if res["isError"] == true {
		t.Fatalf("u1 调用失败: %v", textOf(t, res))
	}
	if meta := echo.metaOf(t); meta != nil {
		t.Fatalf("servers/register 缺省 origin 应为 user（不注入），got %v", meta)
	}
	callMethod(t, bus, "servers/unregister", map[string]any{"name": "u1"})

	// 显式 builtin → 注入
	callMethod(t, bus, "servers/register", map[string]any{"name": "b1", "url": echo.ts.URL, "origin": "builtin"})
	res = callWithCtx(t, bus, "b1_echo_meta")
	if res["isError"] == true {
		t.Fatalf("b1 调用失败: %v", textOf(t, res))
	}
	meta := echo.metaOf(t)
	if meta == nil || meta["work_dir"] != `C:\work\proj` {
		t.Fatalf("servers/register origin=builtin 应注入 _meta，got %v", meta)
	}
	callMethod(t, bus, "servers/unregister", map[string]any{"name": "b1"})

	// mcp_server 内嵌 origin 亦可
	callMethod(t, bus, "servers/register", map[string]any{
		"name": "b2", "mcp_server": map[string]any{"url": echo.ts.URL, "origin": "builtin"},
	})
	res = callWithCtx(t, bus, "b2_echo_meta")
	if res["isError"] == true {
		t.Fatalf("b2 调用失败: %v", textOf(t, res))
	}
	if meta := echo.metaOf(t); meta == nil || meta["instance_id"] != "ins-1" {
		t.Fatalf("mcp_server.origin=builtin 应注入 _meta，got %v", meta)
	}
}

// TestOriginUnknownTreatedAsUser 未知 origin 值 → user（不注入）。
func TestOriginUnknownTreatedAsUser(t *testing.T) {
	echo := startMetaEcho(t)
	bus := startOriginGW(t, []mcpgw.ServerEntry{
		{ID: "x", Name: "x", URL: echo.ts.URL, Enabled: true, Origin: "SOMETHING"},
	})
	res := callWithCtx(t, bus, "x_echo_meta")
	if res["isError"] == true {
		t.Fatalf("调用失败: %v", textOf(t, res))
	}
	if meta := echo.metaOf(t); meta != nil {
		t.Fatalf("未知 origin 应回退 user（不注入），got %v", meta)
	}
}

// TestServersListStaticOrigin servers.list 静态条目：缺省 builtin（部署方文件 = 本仓自有）；
// 显式 `origin=user` 覆盖为第三方（不注入）。
func TestServersListStaticOrigin(t *testing.T) {
	entries, err := mcpgw.ParseServersList([]byte(`
# 静态接入列表
mcp.alias=core
core.url=http://127.0.0.1:5700/core/tools/mcp
core.category=core

mcp.alias=ext
ext.url=http://127.0.0.1:5701/mcp
ext.origin=user
`))
	if err != nil {
		t.Fatalf("parse servers.list: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("应解析 2 组，got %d", len(entries))
	}
	if !entries[0].IsBuiltin() {
		t.Fatalf("静态条目缺省应为 builtin，got origin=%q", entries[0].Origin)
	}
	if entries[1].IsBuiltin() {
		t.Fatalf("显式 origin=user 应非 builtin，got origin=%q", entries[1].Origin)
	}
}
