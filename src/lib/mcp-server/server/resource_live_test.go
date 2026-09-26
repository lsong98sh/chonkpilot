// RB-4 ①（2026-09-22）白盒：ResourceDoc 内容不驻留 —— 扫描期只留 Path，[content] 按需读盘。
package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestResourceDocReadsLiveNoResidentContent：扫描 *.resource.md → ResourceDoc **不驻留** content
// （Content 空、Path = 来源契约文件）；makeResourceHandler 按 Path **实时读盘**取 [content]（改文件后取新内容）。
func TestResourceDocReadsLiveNoResidentContent(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "kb.resource.md")
	body := func(v string) []byte {
		return []byte("# kb\n\n[meta]\nuri=file://kb\nmimetype=text/plain\n\n[description]\n知识库资源\n\n[content]\n" + v + "\n")
	}
	if err := os.WriteFile(f, body("v1"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	docs, err := loadResources(dir)
	if err != nil {
		t.Fatalf("loadResources: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("docs = %d, want 1", len(docs))
	}
	d := docs[0]
	if d.Content != "" || d.Path != f {
		t.Fatalf("ResourceDoc 不应驻留 content: %+v", d)
	}
	h := makeResourceHandler(d)
	read := func() string {
		t.Helper()
		res, err := h(context.Background(), nil)
		if err != nil || len(res.Contents) == 0 {
			t.Fatalf("read: err=%v res=%+v", err, res)
		}
		return res.Contents[0].Text
	}
	if got := read(); got != "v1" {
		t.Fatalf("首次读取 = %q, want v1", got)
	}
	if err := os.WriteFile(f, body("v2"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if got := read(); got != "v2" {
		t.Fatalf("改文件后应取新内容（实时读盘），got %q", got)
	}
}
