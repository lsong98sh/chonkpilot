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
	allowed := false
	if withinDir(filePath, h.WorkDir) {
		allowed = true
	} else {
		for _, root := range h.DataDirs {
			if withinDir(filePath, root) {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		http.Error(w, "path outside work directory", http.StatusForbidden)
		return
	}

	// Open the file
	f, err := os.Open(filePath)
	if err != nil {
		http.Error(w, fmt.Sprintf("cannot open file: %v", err), http.StatusNotFound)
		return
	}
	defer f.Close()

	// Get file info for Content-Length and Content-Type
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
	// rel == "." is the root itself; anything else must not start with "..".
	return rel == "." || !strings.HasPrefix(rel, ".."+string(filepath.Separator))
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
