// RB-4 ①（2026-09-22）白盒：ResourceDoc 内容不驻留 —— 扫描期只留 Path，正文按需读盘。
package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestResourceDocReadsLiveNoResidentContent：扫描 *.resource.md → ResourceDoc **不驻留** content
// （Content 空、Path = 来源契约文件）；makeResourceHandler 按 Path **实时读盘**取正文（改文件后取新内容）。
func TestResourceDocReadsLiveNoResidentContent(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "kb.resource.md")
	body := func(v string) []byte {
		return []byte("# 基本信息\n```yaml\nname: kb\n```\n\n# 摘要\n知识库资源\n\n# 正文\n" + v + "\n")
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

// TestResourceExternalFile：资源 meta `path` 指向外部内容文件（2026-10-14）——无「正文」，
// mimetype 按该文件后缀识别（json → application/json、png → image/png）；
// makeResourceHandler 实时读盘：文本 → Text、二进制 → Blob。
func TestResourceExternalFile(t *testing.T) {
	dir := t.TempDir()
	// ① 文本外部文件（json）
	jsonFile := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(jsonFile, []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}
	mdJSON := filepath.Join(dir, "cfg.resource.md")
	contract := "# 基本信息\n```yaml\nname: cfg\npath: cfg.json\n```\n\n# 摘要\n配置资源\n"
	if err := os.WriteFile(mdJSON, []byte(contract), 0o644); err != nil {
		t.Fatalf("write contract: %v", err)
	}
	// ② 二进制外部文件（png）
	pngFile := filepath.Join(dir, "logo.png")
	pngBytes := []byte{0x89, 0x50, 0x4e, 0x47, 0x00, 0xff}
	if err := os.WriteFile(pngFile, pngBytes, 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}
	mdPNG := filepath.Join(dir, "logo.resource.md")
	if err := os.WriteFile(mdPNG, []byte("# 基本信息\n```yaml\nname: logo\npath: logo.png\n```\n\n# 摘要\n图标资源\n"), 0o644); err != nil {
		t.Fatalf("write contract: %v", err)
	}

	docs, err := loadResources(dir)
	if err != nil {
		t.Fatalf("loadResources: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("docs = %d, want 2", len(docs))
	}
	byName := map[string]*ResourceDoc{}
	for _, d := range docs {
		byName[d.Name] = d
	}

	cfg := byName["cfg"]
	if cfg == nil || cfg.File != jsonFile || cfg.MIMEType != "application/json" {
		t.Fatalf("cfg: file=%q mime=%q want file=%q mime=application/json", cfg.File, cfg.MIMEType, jsonFile)
	}
	res, err := makeResourceHandler(cfg)(context.Background(), nil)
	if err != nil || len(res.Contents) == 0 {
		t.Fatalf("cfg read: err=%v res=%+v", err, res)
	}
	if res.Contents[0].Text != `{"a":1}` || len(res.Contents[0].Blob) != 0 {
		t.Fatalf("cfg content = %q (blob=%d), want text json", res.Contents[0].Text, len(res.Contents[0].Blob))
	}

	logo := byName["logo"]
	if logo == nil || logo.MIMEType != "image/png" {
		t.Fatalf("logo mime = %q, want image/png", logo.MIMEType)
	}
	res2, err := makeResourceHandler(logo)(context.Background(), nil)
	if err != nil || len(res2.Contents) == 0 {
		t.Fatalf("logo read: err=%v res=%+v", err, res2)
	}
	if string(res2.Contents[0].Blob) != string(pngBytes) || res2.Contents[0].Text != "" {
		t.Fatalf("logo 应以 Blob 返回二进制，got text=%q blob=%v", res2.Contents[0].Text, res2.Contents[0].Blob)
	}
}
