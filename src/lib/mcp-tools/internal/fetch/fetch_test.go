package fetch

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFetchRejectsRelativeSaveAs R-11：save_as 相对路径 → 顶层错误（在发请求前拒绝）。
func TestFetchRejectsRelativeSaveAs(t *testing.T) {
	res := HandleFetch("", map[string]interface{}{
		"url": "http://127.0.0.1:1/never", "save_as": "out.bin",
	})
	if res.Success {
		t.Fatalf("相对 save_as 应失败，got %+v", res)
	}
	if !strings.Contains(res.Error, "save_as") || !strings.Contains(res.Error, "out.bin") {
		t.Fatalf("错误消息应指明 save_as 与原值，got %q", res.Error)
	}
	if !strings.Contains(res.Error, "绝对路径") {
		t.Fatalf("错误消息应含路径约束说明，got %q", res.Error)
	}
}

// TestFetchRejectsRelativeFormFilePath R-11：form_files[].path 相对 → 顶层错误（在发请求前拒绝）。
func TestFetchRejectsRelativeFormFilePath(t *testing.T) {
	res := HandleFetch("", map[string]interface{}{
		"url":  "http://127.0.0.1:1/never",
		"form": map[string]interface{}{"k": "v"},
		"form_files": []interface{}{
			map[string]interface{}{"field": "f", "path": "rel.txt"},
		},
	})
	if res.Success {
		t.Fatalf("相对 form_files[].path 应失败，got %+v", res)
	}
	if !strings.Contains(res.Error, "form_files[0].path") || !strings.Contains(res.Error, "rel.txt") {
		t.Fatalf("错误消息应指明位置与原值，got %q", res.Error)
	}
}

// TestFetchAcceptsAbsoluteSaveAs 绝对 save_as → 正常下载落盘。
func TestFetchAcceptsAbsoluteSaveAs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello-r11"))
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "out.bin")
	res := HandleFetch("", map[string]interface{}{"url": srv.URL, "save_as": dst})
	if !res.Success {
		t.Fatalf("绝对 save_as 应成功，got %+v", res)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("save_as 未落盘: %v", err)
	}
	if string(data) != "hello-r11" {
		t.Fatalf("落盘内容 = %q, want hello-r11", string(data))
	}
}
