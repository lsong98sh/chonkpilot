// local_test.go — 文件树初始化预载单测（I-52）：
// readDirNodesExpanded 只对展开键命中的目录递归预载 children，未展开目录不带 children。
// 另含 gui.search file 源（searchFileSource）命中与预算（D-25）单测。
package bridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TestSearchFileSourceBudget file 源遍历预算（D-25）：节点数超限 / 时间超限即止——
// 返回已命中部分（或空）、不报错不冻结；预算恢复后不残留影响。
func TestSearchFileSourceBudget(t *testing.T) {
	root := t.TempDir()
	mkFile(t, filepath.Join(root, "aa", "target.txt"))
	mkFile(t, filepath.Join(root, "bb", "target.txt"))
	mkFile(t, filepath.Join(root, "cc", "target.txt"))

	restore := func() func() {
		origNodes, origDeadline := searchFileMaxNodes, searchFileDeadline
		return func() { searchFileMaxNodes, searchFileDeadline = origNodes, origDeadline }
	}

	// ① 节点预算收紧到 4（根 + aa + aa/target + bb 即耗尽）→ 只命中部分，且安全返回
	done := restore()
	searchFileMaxNodes, searchFileDeadline = 4, time.Hour
	hits := searchFileSource(root, "target", searchMaxResults)
	done()
	if len(hits) == 0 || len(hits) > 3 {
		t.Fatalf("节点预算内应返回部分命中（1~3 个）：%v", hits)
	}

	// ② 时间预算立即超时 → 空结果且立即返回（不报错）
	done = restore()
	searchFileMaxNodes, searchFileDeadline = 20000, -time.Second
	hits2 := searchFileSource(root, "target", searchMaxResults)
	done()
	if len(hits2) != 0 {
		t.Fatalf("时间预算耗尽应返回空结果：%v", hits2)
	}

	// ③ 预算恢复 → 全部命中（预算不残留）
	if hits3 := searchFileSource(root, "target", searchMaxResults); len(hits3) != 3 {
		t.Fatalf("预算恢复后应命中全部 3 个：%v", hits3)
	}
}

// TestConsoleDir 打开控制台的目标目录解析（E5-①）：文件 → 所在目录；目录 → 自身；
// 不存在 / 空 → 回落 workDir；恶意目录名（含 `&`）原样保留（不经命令行，无注入面）。
func TestConsoleDir(t *testing.T) {
	root := t.TempDir()
	evil := filepath.Join(root, "a&calc")
	if err := os.MkdirAll(evil, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(evil, "x.txt")
	mkFile(t, f)

	if got := consoleDir(f, root); got != evil {
		t.Fatalf("文件应解析为其所在目录（含 & 亦原样）：got=%q want=%q", got, evil)
	}
	if got := consoleDir(evil, root); got != evil {
		t.Fatalf("目录应解析为自身：got=%q want=%q", got, evil)
	}
	if got := consoleDir("", root); got != root {
		t.Fatalf("空路径应回落 workDir：got=%q", got)
	}
	if got := consoleDir(filepath.Join(root, "nope"), root); got != root {
		t.Fatalf("不存在的路径应回落 workDir：got=%q", got)
	}
}

// TestConsoleCmdNoInjection dir **不得出现在命令行参数**（E5-① 命令注入修复）：
// 目录作为子进程工作目录传入，cmd 不二次解析命令行 → 含 `&`/`^` 的目录名无从注入。
func TestConsoleCmdNoInjection(t *testing.T) {
	evil := `a&calc`
	cmd := consoleCmd(evil)
	if cmd.Dir != evil {
		t.Fatalf("目录须作为子进程工作目录：got Dir=%q", cmd.Dir)
	}
	if len(cmd.Args) != 1 || cmd.Args[0] != "cmd" {
		t.Fatalf("命令行不得携带目录（应仅 cmd）：%v", cmd.Args)
	}
	for _, a := range cmd.Args {
		if strings.ContainsAny(a, "&^|<>%\"") {
			t.Fatalf("命令行参数不得含 cmd 元字符：%q", a)
		}
	}
}
