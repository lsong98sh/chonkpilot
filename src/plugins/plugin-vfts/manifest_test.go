// 增量判定白盒：diffManifest 的 4 种情形（新文件/未变跳过/md5 一致仅更 mtime/内容变更待重建）
// + 表里有、本次扫描没有 → 待删除；并断言 md5 仅在「size+mtime 变化」时才计算（省 IO）。
package vfts

import (
	"sort"
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

	d := diffManifest(scanned, prior, hashFn)

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

// TestKeyOfStable：key = sha1(绝对路径) 前 16 hex，与内容无关、可重复。
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
