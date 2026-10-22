// P6（2026-10-19）：self-tools 注册载荷 / 热重载 diff / 调用分发的纯逻辑单测
// （不起真实 executor、不连插件桥）。
package main

import (
	"context"
	"encoding/json"
	"testing"

	mcpms "github.com/chonkpilot/chonkpilot-mcp-server/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func testExecTool() *mcpms.ExecTool {
	return &mcpms.ExecTool{
		Name: "filesys_run",
		Tool: &mcp.Tool{
			Name:        "filesys_run",
			Description: "文件系统编排",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"script": map[string]any{"type": "string"}},
			},
		},
		Doc: &mcpms.ToolDoc{
			Name: "filesys_run", Category: "core", Async: "auto", AsyncTh: 30,
			Timeout: 60, TimeoutSet: true,
		},
	}
}

// TestBuildRegisterPayload：注册载荷字段与契约一致（name 不带 self_ 前缀；handler_subject /
// owner / hot / category / async / async_threshold / timeout / request_id）。
func TestBuildRegisterPayload(t *testing.T) {
	p := buildRegisterPayload(testExecTool())
	if p["name"] != "filesys_run" {
		t.Fatalf("name = %v", p["name"])
	}
	if p["description"] != "文件系统编排" {
		t.Fatalf("description = %v", p["description"])
	}
	if p["handler_subject"] != handlerSubject {
		t.Fatalf("handler_subject = %v", p["handler_subject"])
	}
	if p["owner"] != "self-tools" {
		t.Fatalf("owner = %v", p["owner"])
	}
	if p["hot"] != true {
		t.Fatalf("hot = %v", p["hot"])
	}
	if p["category"] != "core" || p["async"] != "auto" {
		t.Fatalf("category/async = %v/%v", p["category"], p["async"])
	}
	if p["async_threshold"] != float64(30) {
		t.Fatalf("async_threshold = %v", p["async_threshold"])
	}
	if p["timeout"] != float64(60) {
		t.Fatalf("timeout = %v", p["timeout"])
	}
	if p["request_id"] != "self-tools:filesys_run" {
		t.Fatalf("request_id = %v", p["request_id"])
	}
	schema, _ := p["schema"].(string)
	var m map[string]any
	if err := json.Unmarshal([]byte(schema), &m); err != nil || m["type"] != "object" {
		t.Fatalf("schema = %q（err=%v）", schema, err)
	}
}

// TestBuildRegisterPayloadTimeoutSemantics：timeout 用指针语义 —— 未声明不写；显式 0/-1 写出。
func TestBuildRegisterPayloadTimeoutSemantics(t *testing.T) {
	et := testExecTool()
	et.Doc.TimeoutSet = false
	if _, ok := buildRegisterPayload(et)["timeout"]; ok {
		t.Fatal("未声明 timeout 时不应写该键")
	}
	et.Doc.TimeoutSet = true
	et.Doc.Timeout = 0
	if v, ok := buildRegisterPayload(et)["timeout"]; !ok || v != float64(0) {
		t.Fatalf("显式 timeout=0 应写出，got %v (ok=%v)", v, ok)
	}
	et.Doc.Timeout = -1
	if v, ok := buildRegisterPayload(et)["timeout"]; !ok || v != float64(-1) {
		t.Fatalf("显式 timeout=-1 应写出，got %v (ok=%v)", v, ok)
	}
}

// TestDiffTools：删除 → removed；新增/指纹变化 → upserted。
func TestDiffTools(t *testing.T) {
	prev := map[string]string{"a": "1", "b": "1", "c": "1"}
	next := map[string]string{"b": "1", "c": "2", "d": "1"}
	removed, upserted := diffTools(prev, next)
	if len(removed) != 1 || removed[0] != "a" {
		t.Fatalf("removed = %v，期望 [a]", removed)
	}
	if len(upserted) != 2 || upserted[0] != "c" || upserted[1] != "d" {
		t.Fatalf("upserted = %v，期望 [c d]", upserted)
	}
	removed, upserted = diffTools(nil, next)
	if len(removed) != 0 || len(upserted) != 3 {
		t.Fatalf("首次加载应全部 upsert：removed=%v upserted=%v", removed, upserted)
	}
}

// TestHandleCall：subject 校验、未知工具、参数/上下文（顶层优先，回退 context）与结果归一。
func TestHandleCall(t *testing.T) {
	var gotArgs map[string]any
	var gotCx mcpms.CallContext
	reg := &registry{
		tools: map[string]*mcpms.ExecTool{
			"t": {
				Name: "t",
				Call: func(_ context.Context, args map[string]any, cx mcpms.CallContext) (*mcp.CallToolResult, error) {
					gotArgs, gotCx = args, cx
					return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, nil
				},
			},
		},
		applied: map[string]string{},
	}

	if _, err := handleCall(context.Background(), "other-subject", toolCall{Tool: "t"}, reg); err == nil {
		t.Fatal("错误 subject 应报错")
	}
	if _, err := handleCall(context.Background(), handlerSubject, toolCall{Tool: "nope"}, reg); err == nil {
		t.Fatal("未知工具应报错")
	}

	// 仅 arguments + context 段 → 回退取值。
	out, err := handleCall(context.Background(), handlerSubject, toolCall{
		Tool:      "t",
		Arguments: map[string]any{"x": 1.0},
		Context:   &toolCallCtx{InstanceID: "ins-1", WorkDir: `C:\w`, DataDir: `C:\d`, Purpose: "p"},
	}, reg)
	if err != nil {
		t.Fatalf("handleCall: %v", err)
	}
	if gotArgs["x"] != 1.0 || gotCx.InstanceID != "ins-1" || gotCx.WorkDir != `C:\w` ||
		gotCx.DataDir != `C:\d` || gotCx.Purpose != "p" {
		t.Fatalf("参数/上下文未正确传递：args=%v cx=%+v", gotArgs, gotCx)
	}
	m := out.(map[string]any)
	if m["ok"] != true {
		t.Fatalf("ok = %v", m["ok"])
	}
	res := m["result"].(map[string]any)
	if res["isError"] != false {
		t.Fatalf("isError = %v", res["isError"])
	}
	content, _ := res["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["text"] != "done" {
		t.Fatalf("content = %#v", res["content"])
	}

	// 顶层字段优先于 context。
	_, _ = handleCall(context.Background(), handlerSubject, toolCall{
		Tool: "t", Args: map[string]any{}, InstanceID: "top", WorkDir: `C:\top`,
		Context: &toolCallCtx{InstanceID: "ctx", WorkDir: `C:\ctx`},
	}, reg)
	if gotCx.InstanceID != "top" || gotCx.WorkDir != `C:\top` {
		t.Fatalf("顶层字段应优先：%+v", gotCx)
	}
}
