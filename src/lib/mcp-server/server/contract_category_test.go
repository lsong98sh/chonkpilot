// 契约注册类别白盒：category=server 的 *.tool.md 不注册为 executor 工具（避免无 runtime 占位工具 /
// 热重扫幽灵工具 self_dsl_run）；其它 category（core/desktop/browser）与**无 category** 一律照常注册。
// 被过滤的 server 类别契约经 ServerTools 单源提供（供装配层桥接给 gateway 自持节点）。
package server

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// writeToolDocForTest 在 dir 下写一个最小 *.tool.md（name = 文件名；meta 为 [meta] 分区正文）。
func writeToolDocForTest(t *testing.T, dir, name, meta string) {
	t.Helper()
	body := "# " + name + "\n\n[meta]\n" + meta + "\n\n[description]\n" + name + " 描述\n\n[parameters]\ntype: object\nproperties: {}\n"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".tool.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// listToolNames 以官方 client（in-memory transport）列出已注册工具名（排序后返回）。
func listToolNames(t *testing.T, ms *mcp.Server) []string {
	t.Helper()
	ctx := context.Background()
	srvTr, cliTr := mcp.NewInMemoryTransports()
	ss, err := ms.Connect(ctx, srvTr, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	c := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1.0.0"}, nil)
	cs, err := c.Connect(ctx, cliTr, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := make([]string, 0, len(res.Tools))
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	sort.Strings(names)
	return names
}

// TestRegisterContractsSkipsServerCategory：category=server 不注册；其它 category / 无 category 照常注册。
func TestRegisterContractsSkipsServerCategory(t *testing.T) {
	root := t.TempDir()
	writeToolDocForTest(t, root, "exec_core", "category=core\nruntime=../../executors/core.exe\n")
	writeToolDocForTest(t, root, "plain_tool", "") // 无 category → 仍注册
	writeToolDocForTest(t, root, "srv_tool", "category=server\nasync=always\nhot=true\n")

	ms := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1.0.0"}, nil)
	if err := RegisterContracts(ms, root, nil); err != nil {
		t.Fatalf("RegisterContracts: %v", err)
	}
	got := listToolNames(t, ms)
	want := []string{"exec_core", "plain_tool"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("已注册工具 = %v，期望 %v（category=server 必须被过滤）", got, want)
	}
}

// TestServerToolsReturnsServerCategory：ServerTools 返回且仅返回 category=server 工具（含 _meta）。
func TestServerToolsReturnsServerCategory(t *testing.T) {
	root := t.TempDir()
	writeToolDocForTest(t, root, "exec_core", "category=core\nruntime=../../executors/core.exe\n")
	writeToolDocForTest(t, root, "plain_tool", "")
	writeToolDocForTest(t, root, "srv_tool", "category=server\nasync=always\nhot=true\n")

	tools, err := ServerTools(root, nil)
	if err != nil {
		t.Fatalf("ServerTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("ServerTools = %d 个，期望 1（仅 srv_tool）：%v", len(tools), tools)
	}
	srv, ok := tools["srv_tool"]
	if !ok {
		t.Fatalf("ServerTools 未返回 srv_tool：%v", tools)
	}
	if srv.Description == "" || srv.InputSchema == nil {
		t.Fatalf("srv_tool 定义不完整：%+v", srv)
	}
	if srv.Meta["category"] != "server" {
		t.Fatalf("srv_tool _meta.category = %v，期望 server", srv.Meta["category"])
	}
}

// TestServerToolsMissingRoot：契约根缺失 → 空 map（非错误）。
func TestServerToolsMissingRoot(t *testing.T) {
	tools, err := ServerTools(filepath.Join(t.TempDir(), "not-exist"), nil)
	if err != nil {
		t.Fatalf("ServerTools(missing): %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("缺失根应返回空 map，got %v", tools)
	}
}
