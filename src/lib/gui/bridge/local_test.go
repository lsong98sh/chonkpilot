// local_test.go — 文件树初始化预载单测（I-52）：
// readDirNodesExpanded 只对展开键命中的目录递归预载 children，未展开目录不带 children。
package bridge

import (
	"path/filepath"
	"testing"
)

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
