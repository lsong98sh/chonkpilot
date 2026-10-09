package fileserver

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// FileShowHandler handles GET /show/<filepath> requests.
// The file path is embedded in the URL path, e.g.:
//
//	/show/d:/bookmarks_2024_11_12.html
//	/show/e:/GoDev/chonkpilot/README.md
//
// It reads the file from disk and streams it back to the client.
// This is used to serve local files inside an iframe.
type FileShowHandler struct {
	// WorkDir is the only directory whose files may be served.
	// Requests resolving outside it are rejected (path traversal guard).
	WorkDir string
	// DataDirs 是额外放行根（如 --data-dir 项目数据根）：请求可从中服务文件
	//（ide.db 预览等 /show/），路径越界防护与 WorkDir 一致。
	DataDirs []string
}

func (h *FileShowHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Never cache served files: WebView2 would otherwise persist stale content.
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	// Mitigate MIME-sniffing attacks when serving user files.
	w.Header().Set("X-Content-Type-Options", "nosniff")

	// Must start with /show/
	if !strings.HasPrefix(r.URL.Path, "/show/") {
		http.NotFound(w, r)
		return
	}

	// Extract file path: "/show/d:/file.html" → "d:/file.html"
	filePath := r.URL.Path[len("/show/"):]
	if filePath == "" {
		http.Error(w, "missing file path after /show/", http.StatusBadRequest)
		return
	}

	// Reject paths that resolve outside the allowed roots (work dir + data dirs).
	if !h.pathAllowed(filePath) {
		http.Error(w, "path outside work directory", http.StatusForbidden)
		return
	}

	// 符号链接逃逸复检（D-27）：withinDir 是纯词法校验，放行根内指向根外的 symlink 会被
	// os.Open 跟随读出根外任意文件。打开**前**解析真实路径并复检：
	// ① EvalSymlinks 失败（断链等）→ 403；
	// ② 解析结果须真实存在（go1.26 下 junction 自身的 EvalSymlinks 不报错且不解析，
	//    断链 junction 由存在性检查拦截）→ 403；
	// ③ 真实路径再跑一次放行根判定，越界 → 403（越界目标根本不打开）。
	// 目录同样经此路径（先于下方 IsDir 判定，指向根外目录的链接同样 403）。
	realPath, err := filepath.EvalSymlinks(filePath)
	if err == nil {
		_, err = os.Stat(realPath)
	}
	if err != nil || !h.realPathAllowed(realPath) {
		http.Error(w, "path outside work directory", http.StatusForbidden)
		return
	}

	// Open the file。用 realPath 打开（D-29）：放行判定基于 EvalSymlinks 结果，若仍打开
	// 词法路径 filePath，则判定与打开之间存在 symlink 被替换的 TOCTOU 窗口，恶意仓库仍可
	// 读出根外文件；改为直接打开复检通过的真实路径，消除该窗口（断链/不存在仍在上方复检
	// 处先行 403，此处错误分支语义不变）。
	f, err := os.Open(realPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("cannot open file: %v", err), http.StatusNotFound)
		return
	}
	defer f.Close()

	// Get file info for Content-Length and Content-Type（跟随链接语义，确认目标存在）
	stat, err := f.Stat()
	if err != nil {
		http.Error(w, fmt.Sprintf("cannot stat file: %v", err), http.StatusInternalServerError)
		return
	}

	if stat.IsDir() {
		http.Error(w, "path is a directory, not a file", http.StatusBadRequest)
		return
	}

	// Set Content-Type based on file extension
	contentType := mimeByExtension(filepath.Ext(filePath))
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))

	// Stream the file content. HEAD requests carry headers only, no body.
	if r.Method == http.MethodHead {
		return
	}
	_, err = io.Copy(w, f)
	if err != nil {
		// Client likely disconnected; nothing we can do.
		return
	}
}

// pathAllowed 判定路径（词法）是否落在任一放行根内（WorkDir + DataDirs）。
func (h *FileShowHandler) pathAllowed(p string) bool {
	if withinDir(p, h.WorkDir) {
		return true
	}
	for _, root := range h.DataDirs {
		if withinDir(p, root) {
			return true
		}
	}
	return false
}

// realPathAllowed 判定「已解析符号链接的真实路径」是否仍落在任一放行根内（D-27 复检）。
// 每个放行根同样先解析符号链接再比较（根自身经过 junction/链接时，若与目标真实路径
// 失配会把合法请求误判越界）；根解析失败（不存在等）回落词法根比较。
func (h *FileShowHandler) realPathAllowed(realPath string) bool {
	roots := make([]string, 0, len(h.DataDirs)+1)
	roots = append(roots, h.WorkDir)
	roots = append(roots, h.DataDirs...)
	for _, root := range roots {
		if r, err := filepath.EvalSymlinks(root); err == nil {
			root = r
		}
		if withinDir(realPath, root) {
			return true
		}
	}
	return false
}

// withinDir reports whether target resolves to a path inside root,
// comparing case-insensitively on Windows.
func withinDir(target, root string) bool {
	if root == "" {
		return false
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(absRoot), filepath.Clean(absTarget))
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		rel = strings.ToLower(rel)
	}
	// rel == "." 是根自身；rel == ".." 是根的父目录（越界，必须拒绝——原判定
	// `HasPrefix(rel, "..\\")` 对裸 ".." 不成立会误放行）；其余不得以 ".." 开头。
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// mimeByExtension returns a MIME type for common web file extensions.
func mimeByExtension(ext string) string {
	switch strings.ToLower(ext) {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".xml":
		return "application/xml; charset=utf-8"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".pdf":
		return "application/pdf"
	case ".txt":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
