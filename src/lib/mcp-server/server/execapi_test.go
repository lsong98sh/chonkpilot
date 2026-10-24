// P6（2026-10-19）：LoadExecTools 从契约根加载可执行工具（跳过 category=server），
// 并与 RegisterContracts 共用同一份 buildTool / handler 语义。
package server

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// initdataCapRoot 返回仓库出厂 capability 根（测试从 server 包目录回溯 4 级到仓库根）。
func initdataCapRoot() string {
	return filepath.Join("..", "..", "..", "..", "src", "initdata", "capability")
}

// TestLoadExecToolsInitdata：加载出厂契约根中的可执行工具；
// 名称/描述/schema/_meta（hot/category/async/async-threshold）与契约一致。
//
// 2026-10-23（PLUGIN-1）：codegraph_* / vfts_query / script_run / web_* / mcp_* 契约已随各自
// manifest 插件投放（`capability/plugins/<id>/tools/`），**不再位于 core capability 根**；
// src/initdata/capability/tools 仅剩 file 系列（file 插件契约的唯一源）。
func TestLoadExecToolsInitdata(t *testing.T) {
	tools, err := LoadExecTools(initdataCapRoot(), nil)
	if err != nil {
		t.Fatalf("LoadExecTools: %v", err)
	}
	want := []string{
		"file_copy",
		"file_delete",
		"file_detect",
		"file_diff",
		"file_find",
		"file_iconv",
		"file_move",
		"file_patch",
		"file_read",
		"file_replace",
		"file_write",
	}
	if len(tools) != len(want) {
		t.Fatalf("工具数 = %d，期望 %d：%v", len(tools), len(want), toolNames(tools))
	}
	for i, name := range want {
		if tools[i].Name != name {
			t.Fatalf("tools[%d] = %q，期望 %q（全量 %v）", i, tools[i].Name, name, toolNames(tools))
		}
	}
	// 2026-10-21（GATEWAY-2/L1）：DSL 退役 —— dsl_run / filesys_run / browser_run / desktop_run 均已删除；
	// 文件域改为 11 个原子工具（file_*），统一由 chonkpilot-tools-executor 承载。
	// 2026-10-21（GATEWAY-2/L3）：file/meta/domain 契约 runtime 改为 builtin（进程内执行）。
	for _, et := range tools {
		if strings.HasPrefix(et.Name, "file_") {
			if !strings.EqualFold(strings.TrimSpace(et.Doc.Runtime), "builtin") {
				t.Fatalf("%s runtime = %q（应为 builtin）", et.Name, et.Doc.Runtime)
			}
			if et.Doc.Category != "file" {
				t.Fatalf("%s category = %q（应为 file）", et.Name, et.Doc.Category)
			}
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
		if et.Doc.Category == "" {
			t.Fatalf("%s: category 缺失", et.Name)
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
	if fr == nil {
		t.Fatal("缺少 file_read")
	}
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
