// fileserver_test.go — `/show/` 放行根（[24 §3.2] MW-8）：除 WorkDir 外，额外放行根
// （项目数据根 + prjusr 数据根）内文件可经 `/show/` 取回；所有根之外一律 403。
package fileserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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

// TestShowRejectsSymlinkEscape symlink 逃逸（D-27）：放行根内指向根外的 symlink 不得经
// os.Open 跟随读出根外文件/目录——词法校验放行、真实路径复检拒绝（403）。
// Windows 建符号链接需特权/开发者模式，失败则降级用**目录 junction**（mklink /J 免特权，
// EvalSymlinks 同样解析 reparse point）覆盖目录逃逸与断链；二者均不可用才跳过。
func TestShowRejectsSymlinkEscape(t *testing.T) {
	workDir := t.TempDir()
	outside := t.TempDir()
	secret := writeFile(t, filepath.Join(outside, "secret.txt"), "secret")
	outDir := filepath.Join(outside, "sub")
	writeFile(t, filepath.Join(outDir, "nested.txt"), "nested")

	h := &FileShowHandler{WorkDir: workDir}

	// ① 根内文件 symlink → 根外文件 → 403（改前会跟随读出 secret）
	linkFile := filepath.Join(workDir, "leak.txt")
	if err := os.Symlink(secret, linkFile); err != nil {
		t.Logf("文件 symlink 不可用（Windows 需特权/开发者模式），该子项跳过：%v", err)
	} else if rec := get(t, h, linkFile); rec.Code != http.StatusForbidden {
		t.Fatalf("指向根外的文件 symlink 应 403：code=%d body=%s", rec.Code, rec.Body.String())
	}

	// ② 根内目录链接 → 根外目录 → 403（目录句柄同样处理；先于 IsDir 400 判定）
	linkDir := filepath.Join(workDir, "leakdir")
	if mkDirLink(t, linkDir, outDir) != nil {
		t.Skipf("目录链接（symlink/junction）均不可用")
	}
	if rec := get(t, h, filepath.Join(linkDir, "nested.txt")); rec.Code != http.StatusForbidden {
		t.Fatalf("指向根外的目录链接应 403：code=%d body=%s", rec.Code, rec.Body.String())
	}

	// ③ 断链（目标不存在）→ EvalSymlinks 失败 → 403（不 500/404 泄露差异）
	dangling := filepath.Join(workDir, "danglingdir")
	if err := exec.Command("cmd", "/c", "mklink", "/J", dangling, filepath.Join(outside, "no-such-dir")).Run(); err != nil {
		t.Skipf("junction 不可用：%v", err)
	}
	if rec := get(t, h, dangling); rec.Code != http.StatusForbidden {
		t.Fatalf("断链应 403：code=%d body=%s", rec.Code, rec.Body.String())
	}
}

// mkDirLink 建目录链接：优先真 symlink，无特权时降级 junction（免特权，reparse point
// 语义对 EvalSymlinks 等价）。
func mkDirLink(t *testing.T, link, target string) error {
	t.Helper()
	if err := os.Symlink(target, link); err == nil {
		return nil
	}
	return exec.Command("cmd", "/c", "mklink", "/J", link, target).Run()
}
