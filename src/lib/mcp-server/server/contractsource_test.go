// 契约来源分层（GATEWAY-2 C）白盒：多层按顺序合并、同名后者覆盖 + shadowed 审计、
// 磁盘根存在 → 磁盘单层（权威）、磁盘根缺失 → 内置 embed 兜底、dir 节点不做 embed 兜底。
package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// TestScanAllLayerOverride 两层同名覆盖 + 未覆盖项保留 + shadowed 审计。
func TestScanAllLayerOverride(t *testing.T) {
	low := t.TempDir()
	high := t.TempDir()
	writeToolDocForTest(t, low, "same_tool", "category=core\nruntime=low.exe\n")
	writeToolDocForTest(t, high, "same_tool", "category=core\nruntime=high.exe\n")

	// 低层独有（prompt）：应保留；高层独有（resource）：应出现。
	lowPrompt := filepath.Join(low, "prompts")
	if err := os.MkdirAll(lowPrompt, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	promptBody := "# 基本信息\n```yaml\nname: only_low\n```\n\n# 摘要\n低层提示\n\n# 参数\n```json\n{\"type\":\"object\",\"properties\":{}}\n```\n\n# 正文\nhi\n"
	if err := os.WriteFile(filepath.Join(lowPrompt, "only_low.prompt.md"), []byte(promptBody), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	highRes := filepath.Join(high, "resources")
	if err := os.MkdirAll(highRes, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	resBody := "# 基本信息\n```yaml\nname: only_high\n```\n\n# 摘要\n高层资源\n\n# 正文\nbody\n"
	if err := os.WriteFile(filepath.Join(highRes, "only_high.resource.md"), []byte(resBody), 0o644); err != nil {
		t.Fatalf("write resource: %v", err)
	}

	files, shadowed, err := ScanAll([]Source{
		{Label: "low", Dir: low},
		{Label: "high", Dir: high},
	})
	if err != nil {
		t.Fatalf("ScanAll: %v", err)
	}

	byKey := map[string]*ContractFile{}
	for _, f := range files {
		byKey[f.Kind+"/"+f.Name] = f
	}
	if len(files) != 3 {
		t.Fatalf("合并后应有 3 项（same_tool/only_low/only_high），got %d: %v", len(files), keysOf(byKey))
	}
	if _, ok := byKey["prompt/only_low"]; !ok {
		t.Fatalf("低层独有 prompt 应保留: %v", keysOf(byKey))
	}
	if _, ok := byKey["resource/only_high"]; !ok {
		t.Fatalf("高层独有 resource 应出现: %v", keysOf(byKey))
	}
	same := byKey["tool/same_tool"]
	if same == nil || !strings.Contains(string(same.Data), "high.exe") {
		t.Fatalf("同名工具应以后层（high）为准: %+v", same)
	}
	if len(shadowed) != 1 || shadowed[0].Kind != kindTool || shadowed[0].Name != "same_tool" ||
		shadowed[0].Winner != "high" || shadowed[0].Loser != "low" {
		t.Fatalf("shadowed 审计不符: %+v", shadowed)
	}
}

// TestScanAllEmbedSource 内嵌来源（fs.FS）按相对路径扫描并可被更高层覆盖。
func TestScanAllEmbedSource(t *testing.T) {
	fsys := fstest.MapFS{
		"tools/a.tool.md":      &fstest.MapFile{Data: []byte(toolDocBody("a", "category=core\nruntime=../../executors/x.exe\n"))},
		"skills/note.skill.md": &fstest.MapFile{Data: []byte("# 基本信息\n```yaml\nname: note\n```\n\n# 摘要\n说明\n\n# 正文\nbody\n")},
		"contracts/.gitkeep":   &fstest.MapFile{Data: []byte("x")},
	}
	disk := t.TempDir()
	writeToolDocForTest(t, disk, "a", "category=core\nruntime=disk.exe\n")

	files, shadowed, err := ScanAll([]Source{
		{Label: "app-embed", FS: fsys},
		{Label: "app", Dir: disk},
	})
	if err != nil {
		t.Fatalf("ScanAll: %v", err)
	}
	var tool *ContractFile
	for _, f := range files {
		if f.Kind == kindTool && f.Name == "a" {
			tool = f
		}
	}
	if tool == nil {
		t.Fatalf("内嵌工具未被扫描: %d files", len(files))
	}
	if !strings.Contains(string(tool.Data), "disk.exe") {
		t.Fatalf("磁盘层应覆盖内嵌层: %s", tool.Data)
	}
	if tool.Path == "" || tool.Dir == "" {
		t.Fatalf("磁盘层文件应带 Path/Dir: %+v", tool)
	}
	if len(shadowed) != 1 || shadowed[0].Loser != "app-embed" {
		t.Fatalf("内嵌层被覆盖应记 shadow: %+v", shadowed)
	}
	// 内嵌独有项（skill）保留，且 Path 为空（registerContracts 再补 Dir）。
	for _, f := range files {
		if f.Kind == kindSkill && f.Name == "note" && f.Path != "" {
			t.Fatalf("内嵌文件 Path 应为空: %+v", f)
		}
	}
}

// TestContractSourcesSelection 来源选择：磁盘存在 → 磁盘单层；缺失 → app 走 embed 兜底，
// dir 节点（forceTools=true）不兜底。
func TestContractSourcesSelection(t *testing.T) {
	disk := t.TempDir()
	srcs := contractSources(disk, false)
	if len(srcs) != 1 || srcs[0].Label != "app" || srcs[0].Dir != disk {
		t.Fatalf("磁盘存在应为磁盘单层: %+v", srcs)
	}

	missing := filepath.Join(t.TempDir(), "not-exist")
	srcs = contractSources(missing, false)
	if len(srcs) != 1 || srcs[0].Label != "app-embed" || srcs[0].FS == nil {
		t.Fatalf("磁盘缺失应回落内置 embed: %+v", srcs)
	}
	if srcs = contractSources(missing, true); len(srcs) != 0 {
		t.Fatalf("dir 节点不应回落 embed: %+v", srcs)
	}
}

// TestFillEmbeddedDirs 内嵌文件按部署根同位目录补 executor 相对解析基准。
func TestFillEmbeddedDirs(t *testing.T) {
	root := filepath.Join("D:", "deploy", "capability")
	files := []*ContractFile{
		{Kind: kindTool, Name: "a", Rel: "tools/core/a.tool.md"},
		{Kind: kindTool, Name: "b", Rel: "tools/core/b.tool.md", Path: "x", Dir: filepath.Join("D:", "deploy", "capability", "tools", "core")},
	}
	fillEmbeddedDirs(files, root)
	if want := filepath.Join(root, "tools", "core"); files[0].Dir != want {
		t.Fatalf("内嵌文件 Dir = %q，期望 %q", files[0].Dir, want)
	}
	if files[1].Dir == "" {
		t.Fatalf("磁盘文件 Dir 不应被改写")
	}
}

// toolDocBody 构造最小 v2 tool 契约（测试辅助）。
func toolDocBody(name, meta string) string {
	yamlMeta := ""
	for _, ln := range strings.Split(meta, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if i := strings.IndexByte(ln, '='); i > 0 {
			yamlMeta += strings.TrimSpace(ln[:i]) + ": " + strings.TrimSpace(ln[i+1:]) + "\n"
		}
	}
	return "# 基本信息\n```yaml\nname: " + name + "\n" + yamlMeta + "```\n\n# 摘要\n" + name + " 描述\n\n# 参数\n```json\n{\"type\":\"object\",\"properties\":{}}\n```\n"
}

func keysOf(m map[string]*ContractFile) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
