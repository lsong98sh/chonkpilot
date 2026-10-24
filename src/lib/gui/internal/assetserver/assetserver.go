// Package assetserver 提供**只读静态资产端口**（UIBRIDGE-1，2026-10-23）。
//
// 用途：第三方页面 / 外部工具（非本仓 iframe）以 `<link>` + `<script>` 两行 include 公共前端
// 资产（`/themes.css`、`/shared/ck-theme.js`、`/shared/ck-theme.css`）。
//
// 口径（定案，见 [49 UIBRIDGE-1]）：
//   - **只读**：仅 `GET` / `HEAD`，其余方法一律 405（带 `Allow: GET, HEAD`）；
//   - **硬白名单**：`/themes.css` + `/shared/<枚举文件名>`，**无目录服务**，未命中即 404；
//     含 `..` / 编码后的 `%2e` / 反斜杠 → 403（目录穿越一律拒绝，不靠 Clean 兜底）；
//   - **`/shared/*` 只从 GUI 前端 dist（embed）取**，不读磁盘任意路径（磁盘仅用于 `<exeDir>/themes`）；
//   - **仅 loopback**：非 loopback 监听地址**拒启**（fail-closed，不静默降级为 0.0.0.0）；
//   - **Host 校验**：仅 `127.0.0.1` / `localhost` / `[::1]`（含端口），其余 403（防 DNS rebinding）；
//   - **响应头与宿主静态服务同口径**：`no-cache, no-store, must-revalidate` + `nosniff`
//     （WebView2 / 浏览器持久缓存旧资产会导致宿主新、第三方旧）；
//   - **零动态面**：不承载消息 / 令牌 / 绑定接口，**不开 CORS**（经典脚本不需要）、
//     不加 ETag / Last-Modified。能力面一律走既有 UDS `pluginbridge`（`plugin/v1/*`）。
package assetserver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-gui/internal/themefs"
)

const (
	// DefaultAddr 是静态资产端口的默认监听地址（仅 loopback）。
	DefaultAddr = "127.0.0.1:5670"

	themesPath   = "/themes.css"
	sharedPrefix = "/shared/"
	// distSharedDir 是前端 dist 内的公共资产目录（`src/frontend/public/shared/` 经 Vite 原样产出）。
	distSharedDir = "shared/"
)

// sharedAssets 是 `/shared/<文件名>` 的**硬白名单**（文件名 → Content-Type）。
//
// 只允许已存在的公共资产：新增文件必须在此显式登记（白名单外一律 404），
// 且必须同时存在于前端 dist 的 `shared/` 下（否则同样 404）。
var sharedAssets = map[string]string{
	"ck-theme.js":      "application/javascript; charset=utf-8",
	"ck-theme.css":     "text/css; charset=utf-8",
	"plugin-bridge.js": "application/javascript; charset=utf-8",
}

// Options 是静态资产服务构造参数。
type Options struct {
	// Addr 监听地址；空 / `off` / `-` → 不启动（New 返回 nil, nil）。
	// 裸端口（如 `5670`）→ `127.0.0.1:5670`；`localhost` → `127.0.0.1`（确定性 loopback）。
	Addr string
	// DistFS 是 GUI 前端 dist 根 FS（embed）；`/shared/*` **只从这里读**（禁止磁盘任意路径）。
	DistFS fs.FS
	// ExeDir 是 exe 所在目录：`/themes.css` 磁盘优先（`<exeDir>/themes/themes.css`），
	// 缺失 / 不可读 → 回落 embed 副本（`themefs.Load`，永不 404）。空 → 直接用 embed。
	ExeDir string
}

// Server 是只读静态资产服务（监听 + 硬白名单 handler）。
type Server struct {
	addr   string
	dist   fs.FS
	exeDir string
	ln     net.Listener
	srv    *http.Server
}

// Disabled 报告 addr 是否表示「不启动」：空串 / `off` / `-`。
func Disabled(addr string) bool {
	a := strings.TrimSpace(addr)
	return a == "" || a == "off" || a == "-"
}

// New 构造服务（未监听；Start 才监听）。
//
// 返回 `(nil, nil)` = 显式关闭（`Disabled(addr)`）；非 loopback 地址 / 缺 DistFS →
// `(nil, err)`（**拒启 + 明确错误**，由调用方记 Warn，不静默绑定到全接口）。
func New(opts Options) (*Server, error) {
	if Disabled(opts.Addr) {
		return nil, nil
	}
	addr := normalizeAddr(opts.Addr)
	if !addrIsLoopback(addr) {
		return nil, fmt.Errorf("assetserver: 拒绝启动：%s 非 loopback（只读静态资产端口必须仅本机访问）", addr)
	}
	if opts.DistFS == nil {
		return nil, errors.New("assetserver: DistFS 必填（/shared/* 只允许取自 GUI 前端 dist）")
	}
	return &Server{addr: addr, dist: opts.DistFS, exeDir: opts.ExeDir}, nil
}

// Addr 返回实际监听地址（Start 之后为真实地址）。
func (s *Server) Addr() string {
	if s.ln != nil {
		return s.ln.Addr().String()
	}
	return s.addr
}

// Start 监听并开始服务（调用前已由 New 完成 loopback 校验）。
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("assetserver: listen %s: %w", s.addr, err)
	}
	// 二道防线：实际监听地址仍非 loopback（如解析漂移）→ 关闭并拒启。
	if !isLoopbackAddr(ln.Addr()) {
		_ = ln.Close()
		return fmt.Errorf("assetserver: 拒绝监听非 loopback 地址 %s", ln.Addr())
	}
	s.ln = ln
	s.srv = &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() { _ = s.srv.Serve(ln) }()
	return nil
}

// Shutdown 关闭监听（无其它资源；nil 接收者 / 未启动 → 无操作）。
func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil || s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}

// Handler 返回硬白名单路由。
//
// 顺序：方法 → Host → 目录穿越 → 白名单。方法先行，保证 `POST` 恒为 405（不因 Host 缺失
// 变成 403，测试 / 外部工具诊断更直观）。
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !hostAllowed(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if traversalRequested(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		path := r.URL.Path
		switch {
		case path == themesPath:
			s.serveThemes(w, r)
		case strings.HasPrefix(path, sharedPrefix):
			s.serveShared(w, r, strings.TrimPrefix(path, sharedPrefix))
		default:
			http.NotFound(w, r)
		}
	})
}

// serveThemes 提供 `/themes.css`（磁盘优先 + embed 兜底，永不 404）。
func (s *Server) serveThemes(w http.ResponseWriter, r *http.Request) {
	data, err := themefs.Load(s.exeDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeAsset(w, r, "text/css; charset=utf-8", data)
}

// serveShared 提供 `/shared/<name>`：name 必须命中硬白名单且存在于前端 dist 的 `shared/`。
func (s *Server) serveShared(w http.ResponseWriter, r *http.Request, name string) {
	ct, ok := sharedAssets[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	b, err := fs.ReadFile(s.dist, distSharedDir+name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	writeAsset(w, r, ct, b)
}

// writeAsset 统一写响应头（no-store + nosniff，与宿主静态服务同口径）与正文。
// HEAD 只回头不写体（Recorder 直调也不会留下 body）。
func writeAsset(w http.ResponseWriter, r *http.Request, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}

// normalizeAddr 归一监听地址（与 llmbridge.New 同规则）：
// 裸端口 → `127.0.0.1:<port>`；`localhost` → `127.0.0.1`（避免解析漂移）。
func normalizeAddr(addr string) string {
	a := strings.TrimSpace(addr)
	if !strings.Contains(a, ":") {
		a = "127.0.0.1:" + a
	}
	if host, port, err := net.SplitHostPort(a); err == nil && strings.EqualFold(host, "localhost") {
		a = net.JoinHostPort("127.0.0.1", port)
	}
	return a
}

// addrIsLoopback 判定监听地址是否**仅绑定本机**（裸端口已归一）。
// 无法解析 / 空 host / 显式全接口（`:5670`）→ 非 loopback（fail-closed）。
func addrIsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}
	return isLoopbackHost(host)
}

// isLoopbackAddr 判定实际监听地址（net.Addr）是否 loopback。
func isLoopbackAddr(a net.Addr) bool {
	if a == nil {
		return false
	}
	host, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return false
	}
	return isLoopbackHost(host)
}

// isLoopbackHost 判定 host 是否 loopback 字面量（`127.0.0.1` / `localhost` / `::1`）。
func isLoopbackHost(host string) bool {
	h := strings.Trim(host, "[]")
	if strings.EqualFold(h, "localhost") || h == "127.0.0.1" || h == "::1" {
		return true
	}
	return false
}

// hostAllowed 校验请求 `Host` 头（防 DNS rebinding）：仅 `127.0.0.1` / `localhost` / `[::1]`，
// 含或不含端口均可；空 Host / 其它域名 / 其它 IP → 拒。
func hostAllowed(hostport string) bool {
	h := strings.TrimSpace(hostport)
	if h == "" {
		return false
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		return isLoopbackHost(host)
	}
	// 无端口形态（HTTP/1.0 或省略端口）：`localhost` / `127.0.0.1` / `[::1]` / `::1`
	return isLoopbackHost(h)
}

// traversalRequested 判定请求是否含目录穿越意图：解码后路径含 `..` / 反斜杠，
// 或原始（未解码）路径含 `%2e`（含 `%2E`，即编码点）——一律 403，不做 Clean 归一后放行
// （白名单本身也不会命中，但显式拒绝更不易被后续改动破坏）。
func traversalRequested(r *http.Request) bool {
	if strings.Contains(r.URL.Path, "..") || strings.Contains(r.URL.Path, `\`) {
		return true
	}
	raw := strings.ToLower(r.URL.EscapedPath())
	if raw == "" {
		raw = strings.ToLower(r.RequestURI)
	}
	return strings.Contains(raw, "%2e") || strings.Contains(raw, "%5c")
}
