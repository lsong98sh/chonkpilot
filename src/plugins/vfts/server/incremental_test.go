// 增量索引冒烟（真实执行 zvec C-API）：建 3 个文件 → 全量索引 → 改 1 个文件 → 再次增量 →
// 断言只重建 1 个文件（其余跳过）、被删文件块被移除、旧内容检索不到。
//
// 运行需 CGO 环境 + `zvec_c_api.dll` 在 PATH（唯一入口 = docs/spec/50-testing/50-测试体系.md §5.1 的可复制命令）：
//
//	$env:CGO_ENABLED="1"; $env:CGO_CFLAGS="-I<...>/third_party/zvec/include"
//	$env:CGO_LDFLAGS="-L<...>/third_party/zvec/windows_amd64 -lzvec_c_api"
//	PATH += <...>/third_party/zvec/windows_amd64（zvec_c_api.dll 运行期加载；缺失则 exit status 0xc0000135）
package server

import (
	"os"
	"path/filepath"
	"testing"
)

func writeText(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestIncrementalIndexSmoke(t *testing.T) {
	dir := t.TempDir()
	writeText(t, dir, "a.txt", "alpha apple\n")
	writeText(t, dir, "b.txt", "bravo berry\n")
	writeText(t, dir, "c.txt", "charlie cherry\n")

	w, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { Drop(dir); CloseAll() }()

	// 1) 全量首建
	full, err := w.Initialize(nil, nil)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	t.Logf("首建：mode=%s added=%d chunks=%d indexed=%d", full.Mode, full.Added, full.Chunks, len(full.Indexed))
	if full.Mode != "full" || full.Added != 3 {
		t.Fatalf("首建应全量索引 3 文件：%+v", full)
	}
	byPath := map[string]IndexedFile{}
	for _, f := range full.Indexed {
		byPath[f.Path] = f
		if len(f.DocIDs) == 0 {
			t.Fatalf("%s 应有 doc_ids：%+v", f.Path, f)
		}
	}
	aLive, cLive := byPath["a.txt"], byPath["c.txt"]
	if len(aLive.DocIDs) == 0 || len(cLive.DocIDs) == 0 {
		t.Fatalf("a/c 应有块：a=%+v c=%+v", aLive, cLive)
	}

	// 2) 只改 a.txt → 增量：updated=1、其余跳过（未出现在 files 即不动）
	writeText(t, dir, "a.txt", "beta banana\n")
	inc, err := w.Incremental([]IncrementalFile{{
		Path: filepath.Join(dir, "a.txt"), Key: "k-a", DocIDs: aLive.DocIDs,
	}}, nil)
	if err != nil {
		t.Fatalf("incremental: %v", err)
	}
	t.Logf("改 a.txt 增量：mode=%s added=%d updated=%d removed=%d removedChunks=%d newChunks=%d",
		inc.Mode, inc.Added, inc.Updated, inc.Removed, inc.RemovedChunks, inc.Chunks)
	if inc.Mode != "incremental" || inc.Updated != 1 || inc.Added != 0 {
		t.Fatalf("应只更新 1 个文件：%+v", inc)
	}
	if inc.RemovedChunks != len(aLive.DocIDs) {
		t.Fatalf("应删除 a.txt 旧块 %d 个，实为 %d", len(aLive.DocIDs), inc.RemovedChunks)
	}
	if len(inc.Indexed) != 1 || inc.Indexed[0].Path != "a.txt" {
		t.Fatalf("增量结果应仅含 a.txt：%+v", inc.Indexed)
	}
	for _, id := range aLive.DocIDs {
		for _, nid := range inc.Indexed[0].DocIDs {
			if id == nid {
				t.Fatalf("主键复用：%s", id)
			}
		}
	}

	// 3) 被改文件：旧内容检索不到、新内容命中；未动的 b.txt 仍可命中
	if hits, err := w.Query("alpha", "", 20, ""); err != nil || len(hits) != 0 {
		t.Fatalf("a.txt 旧内容应已移除：hits=%d err=%v", len(hits), err)
	}
	if hits, err := w.Query("banana", "", 20, ""); err != nil || len(hits) != 1 ||
		hits[0].Path != filepath.ToSlash(filepath.Join(dir, "a.txt")) {
		t.Fatalf("a.txt 新内容应命中 1 处：%+v err=%v", hits, err)
	}
	if hits, err := w.Query("berry", "", 20, ""); err != nil || len(hits) != 1 {
		t.Fatalf("b.txt 应保持命中：%+v err=%v", hits, err)
	}

	// 4) 删除 c.txt → 增量仅按 doc_ids 删块
	if err := os.Remove(filepath.Join(dir, "c.txt")); err != nil {
		t.Fatal(err)
	}
	rem, err := w.Incremental(nil, []IncrementalRemove{{Key: "k-c", DocIDs: cLive.DocIDs}})
	if err != nil {
		t.Fatalf("incremental remove: %v", err)
	}
	t.Logf("删 c.txt 增量：mode=%s removed=%d removedChunks=%d newChunks=%d",
		rem.Mode, rem.Removed, rem.RemovedChunks, rem.Chunks)
	if rem.Removed != 1 || rem.RemovedChunks != len(cLive.DocIDs) {
		t.Fatalf("应移除 c.txt 的 %d 块：%+v", len(cLive.DocIDs), rem)
	}
	if hits, err := w.Query("charlie", "", 20, ""); err != nil || len(hits) != 0 {
		t.Fatalf("c.txt 内容应已移除：hits=%d err=%v", len(hits), err)
	}
	t.Logf("状态：files=%d chunks=%d state=%s", w.Status().IndexedFiles, w.Status().ChunkCount, w.Status().State)
}
