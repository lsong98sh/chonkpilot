// 增量判定白盒：diffManifest 的 4 种情形（新文件/未变跳过/md5 一致仅更 mtime/内容变更待重建）
// + 表里有、本次扫描没有 → 待删除；并断言 md5 仅在「size+mtime 变化」时才计算（省 IO）。
package vfts

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestDiffManifestCases(t *testing.T) {
	now := time.Now()
	scanned := map[string]scanEntry{
		"k-new":   {path: "C:/ws/new.txt", size: 10, mtime: now},
		"k-same":  {path: "C:/ws/same.txt", size: 20, mtime: now},
		"k-touch": {path: "C:/ws/touch.txt", size: 30, mtime: now},
		"k-mod":   {path: "C:/ws/mod.txt", size: 40, mtime: now},
	}
	prior := map[string]fileRec{
		// ② size+mtime 完全一致 → 跳过
		"k-same": {Key: "k-same", Path: "C:/ws/same.txt", Size: 20, MTime: mtimeStr(now), MD5: "aaa", DocIDs: []string{"1"}, Chunks: 1},
		// ③ size 变化，但内容 md5 未变 → 仅更新 mtime
		"k-touch": {Key: "k-touch", Path: "C:/ws/touch.txt", Size: 25, MTime: "old", MD5: "bbb", DocIDs: []string{"2"}, Chunks: 1},
		// ③ size 变化且内容 md5 变化 → 待重建（带旧 doc_ids）
		"k-mod": {Key: "k-mod", Path: "C:/ws/mod.txt", Size: 35, MTime: "old", MD5: "ccc", DocIDs: []string{"3", "4"}, Chunks: 2},
		// ④ 表里有、本次扫描没有 → 待删除
		"k-gone": {Key: "k-gone", Path: "C:/ws/gone.txt", Size: 5, MTime: "x", MD5: "ddd", DocIDs: []string{"5"}, Chunks: 1},
	}

	var hashCalls []string
	hashFn := func(p string) (string, error) {
		hashCalls = append(hashCalls, p)
		switch p {
		case "C:/ws/touch.txt":
			return "bbb", nil // 内容未变
		case "C:/ws/mod.txt":
			return "zzz", nil // 内容已变
		}
		return "", nil
	}

	d := diffManifest(scanned, prior, hashFn, docScanCtx{})

	// ① 新文件 + ③ 内容变化 → 待索引
	if len(d.toIndex) != 2 {
		t.Fatalf("toIndex=%+v want 2（新文件 + 内容变化）", d.toIndex)
	}
	byKey := map[string]indexTask{}
	for _, task := range d.toIndex {
		byKey[task.key] = task
	}
	if nw, ok := byKey["k-new"]; !ok || len(nw.old) != 0 {
		t.Fatalf("新文件应为待索引且无旧块：%+v", byKey["k-new"])
	}
	if md, ok := byKey["k-mod"]; !ok || len(md.old) != 2 || md.md5 != "zzz" {
		t.Fatalf("内容变化文件应带旧 doc_ids 与新 md5：%+v", byKey["k-mod"])
	}
	// ② 未变 → 跳过
	if d.skipped != 1 {
		t.Fatalf("skipped=%d want 1", d.skipped)
	}
	// ③ md5 一致 → 仅更新 mtime（保留 doc_ids/chunks/md5）
	if len(d.toTouch) != 1 || d.toTouch[0].Key != "k-touch" {
		t.Fatalf("toTouch=%+v want 仅 k-touch", d.toTouch)
	}
	if tc := d.toTouch[0]; tc.MTime != mtimeStr(now) || tc.Size != 30 || tc.MD5 != "bbb" || len(tc.DocIDs) != 1 {
		t.Fatalf("仅 mtime 更新应保留其余字段并刷新 size/mtime：%+v", tc)
	}
	// ④ 表里有、扫描没有 → 待删除
	if len(d.toRemove) != 1 || d.toRemove[0].Key != "k-gone" {
		t.Fatalf("toRemove=%+v want 仅 k-gone", d.toRemove)
	}
	// md5 仅对「size+mtime 变化」的两个文件计算（新文件与未变文件不读内容）
	sort.Strings(hashCalls)
	if len(hashCalls) != 2 || hashCalls[0] != "C:/ws/mod.txt" || hashCalls[1] != "C:/ws/touch.txt" {
		t.Fatalf("md5 计算范围错（应只含变化文件）：%v", hashCalls)
	}
}

// TestDiffManifestDocParserVersion：文档类行 md5 携带解析器版本（复用 md5 口径）——
// 版本变化 → 该行失效重建（即便 size+mtime 未变）；版本未变 → 跳过；非文档类不受影响。
func TestDiffManifestDocParserVersion(t *testing.T) {
	now := time.Now()
	scanned := map[string]scanEntry{
		"k-doc":  {path: "C:/ws/a.docx", size: 100, mtime: now},
		"k-text": {path: "C:/ws/a.txt", size: 10, mtime: now},
	}
	prior := map[string]fileRec{
		"k-doc":  {Key: "k-doc", Path: "C:/ws/a.docx", Size: 100, MTime: mtimeStr(now), MD5: tagMD5("abc", "v1"), DocIDs: []string{"1"}, Chunks: 1},
		"k-text": {Key: "k-text", Path: "C:/ws/a.txt", Size: 10, MTime: mtimeStr(now), MD5: "def", DocIDs: []string{"2"}, Chunks: 1},
	}
	hashFn := func(p string) (string, error) { return "abc", nil }

	// v1 == 表内版本 → 全跳过（含文档类）
	d := diffManifest(scanned, prior, hashFn, docScanCtx{enabled: true, parserVersion: "v1"})
	if len(d.toIndex) != 0 || d.skipped != 2 {
		t.Fatalf("版本未变应全跳过：toIndex=%+v skipped=%d", d.toIndex, d.skipped)
	}

	// v2 != 表内版本 → 文档类行失效重建（非文档类仍跳过）
	d = diffManifest(scanned, prior, hashFn, docScanCtx{enabled: true, parserVersion: "v2"})
	if len(d.toIndex) != 1 || d.toIndex[0].key != "k-doc" {
		t.Fatalf("版本变化应仅文档类失效：toIndex=%+v", d.toIndex)
	}
	if md5hex, pv := splitMD5Tag(d.toIndex[0].md5); md5hex != "abc" || pv != "v2" {
		t.Fatalf("待重建行 md5 应携带新版本：%q", d.toIndex[0].md5)
	}
}

// TestMD5TagRoundTrip：md5 标签拼接/拆分（非文档类不加后缀，语义不变）。
func TestMD5TagRoundTrip(t *testing.T) {
	if tagMD5("abc", "") != "abc" {
		t.Fatal("空版本不应加后缀")
	}
	if got := tagMD5("abc", "v1"); got != "abc@v1" {
		t.Fatalf("tagMD5=%q", got)
	}
	if h, v := splitMD5Tag("abc@v1"); h != "abc" || v != "v1" {
		t.Fatalf("splitMD5Tag=%q,%q", h, v)
	}
	if h, v := splitMD5Tag("abc"); h != "abc" || v != "" {
		t.Fatalf("无后缀拆分=%q,%q", h, v)
	}
}

// TestScanFilesDocGroup：清单扫描的文档类分组与阈值——docs 关不收集；docs 开按独立上限收集。
func TestScanFilesDocGroup(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.txt", "text")
	writeTestFile(t, dir, "a.docx", "doc-small")
	writeTestFile(t, dir, "big.pdf", strings.Repeat("x", 300))

	// docs 关：仅 .txt
	got, err := scanFiles(dir, []string{".txt"}, nil, false, docScanCtx{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("docs 关应收 1 个（a.txt），实际 %d", len(got))
	}

	// docs 开 + 上限 100 字节：.txt + a.docx（small），排除 big.pdf（300 > 100）
	got, err = scanFiles(dir, []string{".txt"}, nil, false, docScanCtx{enabled: true, maxBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range got {
		names = append(names, filepath.Base(e.path))
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "a.docx" || names[1] != "a.txt" {
		t.Fatalf("docs 开应按独立上限收集：%v", names)
	}
}

// writeTestFile 测试辅助：写一个文件（自动建父目录）。
func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestKeyOfStable(t *testing.T) {
	k1 := keyOf("C:/ws/a.txt")
	k2 := keyOf("C:/ws/a.txt")
	if k1 != k2 || len(k1) != 16 {
		t.Fatalf("keyOf 不稳定/长度错：%q %q", k1, k2)
	}
	if k1 == keyOf("C:/ws/b.txt") {
		t.Fatal("不同路径不应同 key")
	}
}

// TestAbsOf：引擎返回的相对路径 → workdir 下绝对路径。
func TestAbsOf(t *testing.T) {
	if got := absOf("C:/ws", "sub/a.txt"); got != "C:/ws/sub/a.txt" {
		t.Fatalf("absOf=%q", got)
	}
	if got := absOf("C:/ws", "C:/ws/sub/a.txt"); got != "C:/ws/sub/a.txt" {
		t.Fatalf("absOf 绝对路径原样=%q", got)
	}
}
