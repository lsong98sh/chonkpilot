// P6（2026-10-19）：LoadExecTools 从契约根加载可执行工具（跳过 category=server），
// 并与 RegisterContracts 共用同一份 buildTool / handler 语义。
package server

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestLoadExecToolsFromCapRoot：加载契约根中的可执行工具；
// 名称/描述/schema/_meta（hot/category/async/async-threshold）与契约一致。
//
// 2026-10-23（PLUGIN-1）：codegraph_* / vfts_query / script_run / web_* / mcp_* 契约已随各自
// manifest 插件投放（`capability/plugins/<id>/tools/`），**不再位于 core capability 根**。
// 2026-10-25（PLUGIN-SPLIT-1）：file 系列 11 工具契约随 file 插件迁至
// `chonkpilot-plugins/file/tools/file`——主仓 capability 根已无 file 契约，故本用例改为
// **自建临时契约根**（不再依赖 `src/initdata/capability` 的内容）。
func TestLoadExecToolsFromCapRoot(t *testing.T) {
	root := t.TempDir()
	writeToolDocForTest(t, root, "file_read", "category=file\nruntime=builtin\nhot=true\nasync=never\ntimeout=120")
	writeToolDocForTest(t, root, "file_write", "category=file\nruntime=builtin\nhot=true\nasync=never\ntimeout=120")

	tools, err := LoadExecTools(root, nil)
	if err != nil {
		t.Fatalf("LoadExecTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("工具数 = %d，期望 2：%v", len(tools), toolNames(tools))
	}
	for _, name := range []string{"file_read", "file_write"} {
		if findExecTool(tools, name) == nil {
			t.Fatalf("缺少 %s（全量 %v）", name, toolNames(tools))
		}
	}
	for _, et := range tools {
		if !strings.HasPrefix(et.Name, "file_") {
			t.Fatalf("%s: 前缀应为 file_", et.Name)
		}
		if !strings.EqualFold(strings.TrimSpace(et.Doc.Runtime), "builtin") {
			t.Fatalf("%s runtime = %q（应为 builtin）", et.Name, et.Doc.Runtime)
		}
		if et.Doc.Category != "file" {
			t.Fatalf("%s category = %q（应为 file）", et.Name, et.Doc.Category)
		}
		if et.Tool == nil || et.Doc == nil || et.Call == nil {
			t.Fatalf("%s: Tool/Doc/Call 不得为空", et.Name)
		}
		if et.Tool.Name != et.Name {
			t.Fatalf("%s: Tool.Name = %q", et.Name, et.Tool.Name)
		}
		if strings.TrimSpace(et.Tool.Description) == "" {
			t.Fatalf("%s: 描述为空", et.Name)
		}
		schema, _ := et.Tool.InputSchema.(map[string]any)
		if schema == nil || schema["type"] != "object" {
			t.Fatalf("%s: InputSchema 非法: %#v", et.Name, et.Tool.InputSchema)
		}
		if et.Doc.Async == "" {
			t.Fatalf("%s: async 缺失", et.Name)
		}
		if hot, _ := et.Tool.Meta["hot"].(bool); !hot {
			t.Fatalf("%s: _meta.hot = %v，期望 true", et.Name, et.Tool.Meta["hot"])
		}
		if cat, _ := et.Tool.Meta["category"].(string); cat != et.Doc.Category {
			t.Fatalf("%s: _meta.category = %v，期望 %q", et.Name, et.Tool.Meta["category"], et.Doc.Category)
		}
	}
	// file_read 契约显式 async: never / timeout: 120 → buildTool 透出 _meta。
	fr := findExecTool(tools, "file_read")
	if fr.Tool.Meta["async"] != "never" {
		t.Fatalf("file_read _meta.async = %v，期望 never", fr.Tool.Meta["async"])
	}
	if fr.Tool.Meta["timeout"] != 120 {
		t.Fatalf("file_read _meta.timeout = %v，期望 120 (int)", fr.Tool.Meta["timeout"])
	}
}

// TestLoadExecToolsSkipsServerCategory：category=server **且无 runtime** 的自持工具被跳过；
// 有 runtime 的 server 类别（core-only 域工具）作为可执行工具加载。
func TestLoadExecToolsSkipsServerCategory(t *testing.T) {
	root := t.TempDir()
	writeToolDocForTest(t, root, "exec_ok", "category=core\nruntime=../../executors/core.exe\n")
	writeToolDocForTest(t, root, "srv_owned", "category=server\n")
	writeToolDocForTest(t, root, "srv_exec", "category=server\ncore-only=true\nruntime=../../executors/core.exe\n")

	tools, err := LoadExecTools(root, nil)
	if err != nil {
		t.Fatalf("LoadExecTools: %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "exec_ok" || tools[1].Name != "srv_exec" {
		t.Fatalf("tools = %v，期望 exec_ok + srv_exec（srv_owned 无 runtime 跳过）", toolNames(tools))
	}
	if !tools[1].Doc.CoreOnly {
		t.Fatal("srv_exec: core-only 未解析")
	}
}

// TestLoadExecToolsCallErrorPath：Call 构造请求（args + _meta 调用上下文）后走 handler；
// runtime 缺失 → 明确失败 JSON（验证 handler 构造与执行错误路径，不 spawn 真实 executor）。
func TestLoadExecToolsCallErrorPath(t *testing.T) {
	root := t.TempDir()
	writeToolDocForTest(t, root, "broken", "category=core\nruntime=../../executors/not-exist.exe\nargs=broken --input={RAW-INPUT-FILE}\n")

	tools, err := LoadExecTools(root, nil)
	if err != nil {
		t.Fatalf("LoadExecTools: %v", err)
	}
	res, err := tools[0].Call(context.Background(), map[string]any{"x": 1}, CallContext{
		InstanceID: "ins-1", WorkDir: `C:\work`, DataDir: `C:\data`, Purpose: "test",
	})
	if err != nil {
		t.Fatalf("Call 不应返回 transport error: %v", err)
	}
	if res == nil || !res.IsError || len(res.Content) == 0 {
		t.Fatalf("期望 IsError=true 且带失败文本，got %#v", res)
	}
	text := ""
	if tc, ok := res.Content[0].(*mcp.TextContent); ok {
		text = tc.Text
	}
	if !strings.Contains(text, "runtime not found") {
		t.Fatalf("失败文本 = %q，期望包含 runtime not found", text)
	}
}

func toolNames(tools []*ExecTool) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}

func findExecTool(tools []*ExecTool, name string) *ExecTool {
	for _, t := range tools {
		if t.Name == name {
			return t
		}
	}
	return nil
}
