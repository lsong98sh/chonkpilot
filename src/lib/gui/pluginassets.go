//go:build windows

package gui

import (
	"context"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-gui/internal/fileserver"
)

// 插件自持 webui 资源路由（PLUGIN-UI-1 第一段，28-plugins §3A.2）。
//
// `GET /plugins/<id>/<path>`（同源）：宿主不再直读磁盘 `capability/plugins/<id>/webui`，
// 改为经 UDS 向**已就绪插件**请求资源（`plugin/v1/ui.asset.get`），原样回吐字节。
// 兼容：旧 `/plugin-ui/<id>/*`（宿主直读磁盘）**已下线**（一律 404）；仅保留
// `/plugin-ui/_sdk/plugin.js` 共享桥（见 main.go servePluginUISDK）。
const pluginsPrefix = "/plugins/"

// pluginAssetFetchFunc 向插件请求一条资源；found=false 表示未命中（含插件未就绪）。
type pluginAssetFetchFunc func(ctx context.Context, pluginID, path string) ([]byte, bool, error)

// pluginAssetVersionFunc 返回缓存版本分量（插件版本 + 重启代次）；空 = 不参与失效判断。
type pluginAssetVersionFunc func(pluginID string) string

// pluginAssetHandler 是 `/plugins/<id>/<path>` 的处理器 + 进程级短缓存。
//
// 缓存键 = (plugin_id, path)，值内带版本分量；版本变化（插件升级 / 重启）→ 重新拉取并覆盖。
// 简单可关：fetch 为 nil 时整条路由 404（未启用插件平台）。
type pluginAssetHandler struct {
	fetch   pluginAssetFetchFunc
	version pluginAssetVersionFunc

	mu    sync.Mutex
	cache map[string]pluginAssetEntry
}

type pluginAssetEntry struct {
	version string
	data    []byte
}

// newPluginAssetHandler 构造处理器（fetch 为 nil → 路由恒 404）。
func newPluginAssetHandler(fetch pluginAssetFetchFunc, version pluginAssetVersionFunc) *pluginAssetHandler {
	return &pluginAssetHandler{fetch: fetch, version: version, cache: map[string]pluginAssetEntry{}}
}

func (h *pluginAssetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.fetch == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, pluginsPrefix)
	id, sub, ok := strings.Cut(rest, "/")
	if !ok || id == "" || sub == "" {
		http.NotFound(w, r)
		return
	}
	// 目录穿越：`..` / 编码后的 `%2e` / 反斜杠 / 绝对路径形态 → 403（不做 Clean 兜底放行）。
	if pluginAssetTraversal(r) || strings.HasPrefix(sub, "/") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	data, found, err := h.get(r.Context(), id, sub)
	if err != nil || !found {
		http.NotFound(w, r)
		return
	}
	// MIME 由宿主按扩展名推断（与静态服务同一张表）；插件不决定 MIME。
	w.Header().Set("Content-Type", fileserver.MIMEByExtension(path.Ext(sub)))
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(data)
}

// get 先查短缓存（同版本命中即返回），未命中再经 UDS 向插件拉取并写入缓存。
func (h *pluginAssetHandler) get(ctx context.Context, pluginID, sub string) ([]byte, bool, error) {
	version := ""
	if h.version != nil {
		version = h.version(pluginID)
	}
	key := pluginID + "\x00" + sub
	h.mu.Lock()
	if e, ok := h.cache[key]; ok && e.version == version {
		data := e.data
		h.mu.Unlock()
		return data, true, nil
	}
	h.mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	data, found, err := h.fetch(cctx, pluginID, sub)
	if err != nil || !found {
		return nil, false, err
	}
	cp := append([]byte(nil), data...)
	h.mu.Lock()
	h.cache[key] = pluginAssetEntry{version: version, data: cp}
	h.mu.Unlock()
	return cp, true, nil
}

// pluginAssetTraversal 判定请求是否含目录穿越意图（与 assetserver 同口径）：
// 解码后路径含 `..` / 反斜杠，或原始路径含 `%2e` / `%5c`。
func pluginAssetTraversal(r *http.Request) bool {
	if strings.Contains(r.URL.Path, "..") || strings.Contains(r.URL.Path, `\`) {
		return true
	}
	raw := strings.ToLower(r.URL.EscapedPath())
	if raw == "" {
		raw = strings.ToLower(r.RequestURI)
	}
	return strings.Contains(raw, "%2e") || strings.Contains(raw, "%5c")
}
