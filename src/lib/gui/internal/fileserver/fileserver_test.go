// fileserver_test.go — `/show/` 放行根（[24 §3.2] MW-8）：除 WorkDir 外，额外放行根
// （项目数据根 + prjusr 数据根）内文件可经 `/show/` 取回；所有根之外一律 403。
package fileserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// writeFile 写一个文件并返回其绝对路径。
func writeFile(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// get 发一次 /show/<abs> 请求并返回响应。
func get(t *testing.T, h *FileShowHandler, abs string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/show/"+filepath.ToSlash(abs), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestShowServesDataDirs：额外放行根（项目数据根 / prjusr 数据根）内文件可服务；根外 403。
func TestShowServesDataDirs(t *testing.T) {
	workDir := t.TempDir()
	projectDataDir := filepath.Join(workDir, ".chonkpilot")
	prjUsrRoot := t.TempDir()
	outside := t.TempDir()

	h := &FileShowHandler{WorkDir: workDir, DataDirs: []string{projectDataDir, prjUsrRoot}}

	// ① prjusr 数据根下（迁移后的附件/截图落点）→ 可服务
	uploaded := writeFile(t, filepath.Join(prjUsrRoot, "tmp", "uploads", "a.txt"), "prjusr-file")
	rec := get(t, h, uploaded)
	if rec.Code != http.StatusOK {
		t.Fatalf("prjusr 根内文件应可服务：code=%d body=%s", rec.Code, rec.Body.String())
	}
	if body, _ := io.ReadAll(rec.Body); string(body) != "prjusr-file" {
		t.Fatalf("内容不符：%q", body)
	}

	// ② 项目数据根下（迁走前的旧附件仍可回看）→ 可服务
	legacy := writeFile(t, filepath.Join(projectDataDir, "tmp", "uploads", "old.txt"), "legacy-file")
	if rec := get(t, h, legacy); rec.Code != http.StatusOK {
		t.Fatalf("项目数据根内文件应可服务：code=%d", rec.Code)
	}

	// ③ workDir 之外且不在任何放行根内 → 403
	rejected := writeFile(t, filepath.Join(outside, "secret.txt"), "secret")
	if rec := get(t, h, rejected); rec.Code != http.StatusForbidden {
		t.Fatalf("放行根外文件应 403：code=%d", rec.Code)
	}
}
