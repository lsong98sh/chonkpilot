// local_test.go — 文件树初始化预载单测（I-52）：
// readDirNodesExpanded 只对展开键命中的目录递归预载 children，未展开目录不带 children。
package bridge

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSearchRelPath 检索结果路径归一（2026-09-27 修 bug）：三源结果必须为
// workdir 相对 + 斜杠（与 file-open 契约一致）；workdir 之外的绝对路径不做 `..` 归一。
func TestSearchRelPath(t *testing.T) {
	root := t.TempDir()
	// ① workdir 内绝对路径 → 相对 + 斜杠
	if got := searchRelPath(root, filepath.Join(root, "a", "b.txt")); got != "a/b.txt" {
		t.Fatalf("workdir 内绝对路径应归一为相对：got=%q", got)
	}
	// ② 已是相对路径 → 仅斜杠归一
	if got := searchRelPath(root, "a\\b.txt"); got != "a/b.txt" {
		t.Fatalf("相对路径应做斜杠归一：got=%q", got)
	}
	// ③ workdir 之外 → 不产生 `..` 越界写法（保持绝对）
	outside := filepath.Join(root, "..", "other", "c.txt")
	if got := searchRelPath(root, outside); strings.HasPrefix(got, "..") {
		t.Fatalf("workdir 之外不应归一为 .. 相对：got=%q", got)
	}
	// ④ 空/空白 → 空串
	if got := searchRelPath(root, "   "); got != "" {
		t.Fatalf("空白应返回空串：got=%q", got)
	}
}

// TestReadDirNodesExpanded 展开键命中的目录必须预载 children，未命中目录保持懒加载。
func TestReadDirNodesExpanded(t *testing.T) {
	root := t.TempDir()
	mkFile(t, filepath.Join(root, "a", "x.txt"))
	mkFile(t, filepath.Join(root, "a", "b", "y.txt"))
	mkFile(t, filepath.Join(root, "c", "z.txt"))

	expandedA := filepath.ToSlash(filepath.Join(root, "a"))
	nodes := readDirNodesExpanded(root, map[string]bool{expandedA: true})

	byName := map[string]map[string]any{}
	for _, n := range nodes {
		name, _ := n["name"].(string)
		byName[name] = n
	}

	a := byName["a"]
	if a == nil {
		t.Fatalf("根级缺少目录 a：%v", byName)
	}
	children, ok := a["children"].([]map[string]any)
	if !ok || len(children) != 2 {
		t.Fatalf("展开目录 a 应预载 2 个 children，实际 %v", a["children"])
	}
	// a 的子孙 b 未命中展开集 → 不应预载 children
	byChild := map[string]map[string]any{}
	for _, c := range children {
		name, _ := c["name"].(string)
		byChild[name] = c
	}
	if b := byChild["b"]; b == nil || b["children"] != nil {
		t.Fatalf("未展开目录 a/b 不应预载 children：%v", byChild["b"])
	}
	// 根级 c 未命中展开集 → 不应预载 children
	if c := byName["c"]; c != nil && c["children"] != nil {
		t.Fatalf("未展开目录 c 不应预载 children：%v", c["children"])
	}
}

// TestReadDirNodesExpandedNested 展开链命中时逐层预载（a → a/b）。
func TestReadDirNodesExpandedNested(t *testing.T) {
	root := t.TempDir()
	mkFile(t, filepath.Join(root, "a", "b", "y.txt"))

	expanded := map[string]bool{
		filepath.ToSlash(filepath.Join(root, "a")):      true,
		filepath.ToSlash(filepath.Join(root, "a", "b")): true,
	}
	nodes := readDirNodesExpanded(root, expanded)
	var a map[string]any
	for _, n := range nodes {
		if n["name"] == "a" {
			a = n
		}
	}
	if a == nil {
		t.Fatal("根级缺少目录 a")
	}
	children, _ := a["children"].([]map[string]any)
	var b map[string]any
	for _, c := range children {
		if c["name"] == "b" {
			b = c
		}
	}
	if b == nil {
		t.Fatal("a 下缺少目录 b")
	}
	if _, ok := b["children"].([]map[string]any); !ok {
		t.Fatalf("展开目录 a/b 应预载 children，实际 %v", b["children"])
	}
}
