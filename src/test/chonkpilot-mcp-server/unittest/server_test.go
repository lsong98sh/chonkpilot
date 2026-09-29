// Package mcpmservertest — chonkpilot-mcp-server 黑盒回归测试
// （工程目录 chonkpilot-test/chonkpilot-mcp-server/unittest）。
//
// 通过公开包 github.com/chonkpilot/chonkpilot-mcp-server/server 集成测试：
// 合并 mcp-server 自身契约（skills/prompts/resources）与 mcp-tools 工具契约到临时根
// → 自建官方 go-sdk server + RegisterContracts 注册 → 官方 client（in-memory transport）
// 走 ListTools/ListPrompts/ListResources 断言（对齐收敛后 lib 形态：scan → srv.Add*）。
// 断言严谨：精确名称集合 + 计数 + 必填字段非空。
package mcpmservertest

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpms "github.com/chonkpilot/chonkpilot-mcp-server/server"
)

// repoRoot 定位 chonkpilot 仓库根（相对本测试源文件：unittest → chonkpilot-mcp-server →
// test → src → chonkpilot）。
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
}

// copyDir 递归复制目录。
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy %s -> %s: %v", src, dst, err)
	}
}

// mergedContracts 合并出厂契约（唯一源 `src/initdata/capability`）到临时根
// （root/tools + root/knowledge/{skills,prompts,resources}，mcp-server 单根递归扫描）。
func mergedContracts(t *testing.T, root *string) func() {
	t.Helper()
	repo := repoRoot(t)
	tmp, err := os.MkdirTemp("", "ck-ms-tests-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	initCap := filepath.Join(repo, "src", "initdata", "capability")
	for _, prim := range []string{"skills", "prompts", "resources"} {
		copyDir(t, filepath.Join(initCap, "knowledge", prim),
			filepath.Join(tmp, "knowledge", prim))
	}
	copyDir(t, filepath.Join(initCap, "tools"),
		filepath.Join(tmp, "tools"))
	*root = tmp
	return func() { _ = os.RemoveAll(tmp) }
}

// newClient 以合并契约根自建官方 go-sdk server + RegisterContracts，
// 返回已连接的官方 client session（t.Cleanup 自动关闭）。
func newClient(t *testing.T, root string) *mcp.ClientSession {
	t.Helper()
	ms := mcp.NewServer(&mcp.Implementation{Name: "mcp-server-test", Version: "1.0.0"}, nil)
	if err := mcpms.RegisterContracts(ms, root, nil); err != nil {
		t.Fatalf("RegisterContracts failed: %v", err)
	}
	ctx := context.Background()
	srvTr, cliTr := mcp.NewInMemoryTransports()
	ss, err := ms.Connect(ctx, srvTr, nil)
	if err != nil {
		t.Fatalf("server connect failed: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	c := mcp.NewClient(&mcp.Implementation{Name: "mcp-server-test-client", Version: "1.0.0"}, nil)
	cs, err := c.Connect(ctx, cliTr, nil)
	if err != nil {
		t.Fatalf("client connect failed: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// assertNameSet 断言精确工具名集合（数量 + 成员一致）。
func assertNameSet(t *testing.T, got []string, want []string) {
	t.Helper()
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("数量不符：got %d %v，want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("集合不符：got %v，want %v", got, want)
		}
	}
}

func TestListToolsContracts(t *testing.T) {
	var root string
	cleanup := mergedContracts(t, &root)
	defer cleanup()
	cs := newClient(t, root)
	ctx := context.Background()

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	names := []string{}
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
		if tl.Description == "" {
			t.Errorf("tool %s: description empty", tl.Name)
		}
		if tl.InputSchema == nil {
			t.Errorf("tool %s: inputSchema nil", tl.Name)
		}
	}
	want := []string{"browser_run", "desktop_run", "file_diff", "file_find", "file_read",
		"filesys_run", "script_run", "web_fetch"}
	assertNameSet(t, names, want)
}

// TestPromptSkillMeta 验证 prompts/list 透出 _meta.type（prompt/skill 由后缀推导）。
func TestPromptSkillMeta(t *testing.T) {
	var root string
	cleanup := mergedContracts(t, &root)
	defer cleanup()
	cs := newClient(t, root)
	ctx := context.Background()

	res, err := cs.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatalf("ListPrompts failed: %v", err)
	}
	if len(res.Prompts) == 0 {
		t.Fatal("no prompts listed")
	}
	metaType := map[string]string{}
	for _, p := range res.Prompts {
		if p.Meta != nil {
			if tv, ok := p.Meta["type"].(string); ok {
				metaType[p.Name] = tv
			}
		}
	}
	// 契约根：prompts/core/code_review.prompt.md（type=prompt）；skills/core/{debug,explore,sandbox-escape}.skill.md
	// 与 skills/ux/*.skill.md（来源 claude-ux/skills/）均 type=skill。
	for name, want := range map[string]string{
		"code_review": "prompt",
		"explore":     "skill",
		"debug":       "skill",
		// skills/ux/（14 个 UX/前端设计技能）
		"wireframe":                    "skill",
		"polish-pass":                  "skill",
		"make-tweakable":               "skill",
		"make-a-prototype":             "skill",
		"make-a-deck":                  "skill",
		"interaction-states-pass":      "skill",
		"hierarchy-rhythm-review":      "skill",
		"generate-variations":          "skill",
		"frontend-aesthetic-direction": "skill",
		"discovery-questions":          "skill",
		"design-system-extract":        "skill",
		"component-extract":            "skill",
		"ai-slop-check":                "skill",
		"accessibility-audit":          "skill",
	} {
		if got := metaType[name]; got != want {
			t.Errorf("%s _meta.type = %q，want %q（got: %v）", name, got, want, metaType)
		}
	}
}

// TestListResources 验证资源契约均加载且 uri 非空。
func TestListResources(t *testing.T) {
	var root string
	cleanup := mergedContracts(t, &root)
	defer cleanup()
	cs := newClient(t, root)
	ctx := context.Background()

	res, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatalf("ListResources failed: %v", err)
	}
	if len(res.Resources) < 3 {
		t.Fatalf("resources = %d，want ≥ 3（3 个契约资源）", len(res.Resources))
	}
	for _, r := range res.Resources {
		if r.URI == "" {
			t.Errorf("resource name=%q：uri empty", r.Name)
		}
	}
}
