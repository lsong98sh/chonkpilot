// 文档索引「停服降级」清单一致性回归（配合引擎侧 collectFiles 的整批跳过门）：
// 转换服务不可用 → 引擎未索引文档类（应答 indexed 不含文档）→ 清单重建必须**以引擎实际
// Indexed 为准**（保留集只含已索引项），把上次遗留的文档类行从清单移除，而不是保留。
package vfts

import (
	"path/filepath"
	"testing"
)

// TestRebuildManifestDropsUnindexedDocs：服务不可用（docScanCtx.enabled=false，与引擎
// 「整批跳过」同口径）→ 引擎应答 Indexed 不含文档类 → 清单重建把**上次遗留的文档类行移除**，
// 非文档类保留。即清单保留集严格以引擎实际 Indexed 为准。
func TestRebuildManifestDropsUnindexedDocs(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "note.txt", "plain note")
	writeTestFile(t, dir, "报告.docx", "fake docx")

	noteAbs := filepath.ToSlash(filepath.Join(dir, "note.txt"))
	docAbs := filepath.ToSlash(filepath.Join(dir, "报告.docx"))
	noteKey, docKey := keyOf(noteAbs), keyOf(docAbs)

	// 上次清单：两类都在（服务曾可用）
	if err := saveManifest(dir, map[string]fileRec{
		noteKey: {Key: noteKey, Path: noteAbs, Size: 10, MTime: "t0", MD5: "aaa", DocIDs: []string{"1"}, Chunks: 1},
		docKey:  {Key: docKey, Path: docAbs, Size: 9, MTime: "t0", MD5: "bbb@v1", DocIDs: []string{"2"}, Chunks: 1},
	}); err != nil {
		t.Fatalf("saveManifest: %v", err)
	}

	p := New(Options{Exe: "dummy-not-spawned.exe"})
	p.logf = func(string, ...any) {}
	wd := filepath.Clean(dir)
	r := &workRec{workDir: wd, refs: 1, enabled: true, docs: true}

	// 引擎应答只含 note.txt（服务不可用 → 文档类未索引）
	res := engineIndexResult{Mode: "full", Indexed: []engineIndexedFile{
		{Path: "note.txt", Key: noteKey, DocIDs: []string{"1"}, Chunks: 1},
	}}
	if err := p.rebuildManifest(r, res, []string{".txt"}, nil, false, docScanCtx{enabled: false}); err != nil {
		t.Fatalf("rebuildManifest: %v", err)
	}
	got := loadManifest(wd)
	if _, ok := got[docKey]; ok {
		t.Fatalf("服务不可用：未索引的文档类应从清单移除，实际仍在：%+v", got[docKey])
	}
	if _, ok := got[noteKey]; !ok {
		t.Fatalf("非文档类应保留在清单：%+v", got)
	}
}

// TestDocServiceAppearedIncrementalAddsDocs：转换服务「后启动」容错（maybeDocServiceAppeared →
// 增量 force=false）必须能纳入文档类——服务缺席时文档类未入清单（docScanCtx.enabled=false，
// 与引擎 collectFiles「整批跳过」同口径），服务上线后同一文件被 diff 判为**新增**（prior 无该 key、
// old 为空 → incrementalSync 计入 Added）→ 增量纳入，无需全量重建（回归：曾担心「docs 开 + 服务缺席
// → 服务上线」被 force=false 静默漏掉）。
func TestDocServiceAppearedIncrementalAddsDocs(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "note.txt", "plain note")
	writeTestFile(t, dir, "报告.docx", "fake docx")

	docAbs := filepath.ToSlash(filepath.Join(dir, "报告.docx"))
	docKey := keyOf(docAbs)

	// ① 服务缺席：扫描不收集文档类
	scannedAbsent, err := scanFiles(dir, []string{".txt"}, nil, false, docScanCtx{enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := scannedAbsent[docKey]; ok {
		t.Fatal("服务缺席时不应收集文档类")
	}
	// prior = 服务缺席时的清单（无文档类行）
	prior := map[string]fileRec{}
	for k, e := range scannedAbsent {
		prior[k] = fileRec{Key: k, Path: e.path, Size: e.size, MTime: mtimeStr(e.mtime), MD5: "x"}
	}

	// ② 服务上线：扫描收集文档类 → diff 应为新增（toIndex，old 为空 = 计入 added）
	doc := docScanCtx{enabled: true, maxBytes: 1 << 20, parserVersion: "v1"}
	scannedUp, err := scanFiles(dir, []string{".txt"}, nil, false, doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := scannedUp[docKey]; !ok {
		t.Fatal("服务上线后应收集文档类")
	}
	d := diffManifest(scannedUp, prior, md5File, doc)
	var docTask *indexTask
	for i := range d.toIndex {
		if d.toIndex[i].key == docKey {
			docTask = &d.toIndex[i]
		}
	}
	if docTask == nil {
		t.Fatalf("服务上线后文档类应被判为待索引（新增），实际 toIndex=%+v", d.toIndex)
	}
	if len(docTask.old) != 0 {
		t.Fatalf("新接入的文档类不应带旧块（old 为空 → 计入 Added）：%+v", docTask)
	}
}
