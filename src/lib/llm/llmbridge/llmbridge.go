// Package llmbridge 实现 **LLM 桥接入口**（BRIDGE-3）——一个「选 provider + 注入 key」的
// **透明反向代理**：本机脚本（Python 等）把 SDK 指向 UDS 上的
// `http://<任意 host>/llm/<name>/...`，即可**免 api-key** 调用本机已配置的 LLM。
//
// 设计依据：docs/llm-bridge/LLM桥接端口设计方案.md（§2 总体方案 · §3 详细设计 · §4 规则 ·
// §5 与既有机制的关系 · §6 边界）+ [49 BRIDGE-3]（TCP 入口**整体删除**）。
//
//   - **只监听 UDS**：`<LOCALAPPDATA>\chonkpilot\llm.sock`（可 `CHONK_LLM_UDS` / `--llm-bridge-uds`
//     覆盖，`off` 关闭）；**不提供任何 TCP 监听**，需要 TCP 的调用方自建 loopback→UDS 字节代理；
//   - **socket 权限**：目录 `0700` + socket `0600`（唯一边界 = 文件系统权限）；
//   - **路由**：`/llm/<name>/<rest>`，路径段 `<name>` 选 provider（保留段 `default` = 系统默认 LLM）；
//     剩余路径 `<rest>` **原样拼接**到 `provider.base_url`（含查询串）；
//   - **鉴权覆盖**：出站按 `provider.protocol` **先删后设**鉴权头（openai/responses →
//     `Authorization: Bearer <key>`；anthropic → `x-api-key: <key>`；其它 → 400）——**客户端自带
//     的 key 头一律被覆盖**（使用侧免 key、防自报）；
//   - **不解析 body / 不翻译协议 / 不注入工具 / 不代执行 tool_calls** → 天然支持
//     openai / anthropic / responses；**流式（SSE）直通**（`FlushInterval=-1`）；
//   - **与既有机制零耦合**：只 **读** provider 配置（`Resolve` 回调注入，宿主 = llm server 的
//     `ResolveBridgeTarget`）；**不 Register / 不 Call / 不经 canon**；**不碰** router / turn /
//     gateway / MCP；**零新增 MQ 主题 / payload**（纯 HTTP 面）；
//   - **密钥**：apiKey 只随请求内存流转，**不落日志**（本包不打任何含 key 的日志）。
//
// 已知威胁面（设计稿 §4/§6，如实登记）：免鉴权 + 同用户可连 = **本机同用户任意进程可白嫖已配置
// key**，系「脚本零配置可用」的取舍；身份若需区分，只能在应用层显式声明（代理不传递身份）。
package llmbridge

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultSocketName 是入口 socket 的文件名（与 `plugin.sock` 同级、独立命名）。
const DefaultSocketName = "llm.sock"

// pathPrefix 是入口路由前缀：`/llm/<name>/<rest>`（设计稿 §3.2）。
const pathPrefix = "/llm/"

// provider.protocol 已知取值（设计稿 §3.3）。桥按此决定出站鉴权头；未识别 → 400（不静默猜）。
const (
	protocolOpenAI    = "openai"
	protocolResponses = "responses"
	protocolAnthropic = "anthropic"
)

// Target 是桥接转发的**只读目标快照**（由宿主 `Resolve` 回调给出；对应 llm server 的
// `BridgeTarget`——桥侧刻意不 import server 包，保持入口与库解耦，装配期适配，见 main.go）。
type Target struct {
	BaseURL  string // provider API 前缀（空 → 由宿主回落 --llm-base；仍空 → 502）
	APIKey   string // provider apiKey（空 = 不注入鉴权头；**不落日志**）
	Protocol string // 原始 protocol（openai / responses / anthropic / 空=openai / 其它=400）
}

// Resolver 解析 `/llm/<name>/...` 的 `<name>` 段 → 转发目标；未命中 → ok=false（桥侧 404）。
// 装配点 = 宿主注入 llm server 的 `ResolveBridgeTarget`。
type Resolver func(name string) (Target, bool)

// Options 是入口构造参数。
type Options struct {
	// Path 是 UDS 路径；空 = DefaultPath()。
	Path string
	// Resolve 是 provider 解析回调（必填；nil → Start 报错）。
	Resolve Resolver
}

// Server 是 LLM 桥接入口（无状态：每次请求独立解析 provider，天然并发安全）。
type Server struct {
	path    string
	resolve Resolver
	ln      net.Listener
	srv     *http.Server
}

// DefaultPath 返回缺省 UDS 路径：Windows = `%LOCALAPPDATA%\chonkpilot\llm.sock`；
// 其它 = `~/.chonkpilot/llm.sock`。
func DefaultPath() string {
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		return filepath.Join(base, "chonkpilot", DefaultSocketName)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	return filepath.Join(home, ".chonkpilot", DefaultSocketName)
}

// New 构造入口（未监听；Start 才监听）。
func New(opts Options) *Server {
	p := strings.TrimSpace(opts.Path)
	if p == "" {
		p = DefaultPath()
	}
	return &Server{path: p, resolve: opts.Resolve}
}

// Path 返回 UDS 路径（Start 之后即实际监听路径）。
func (s *Server) Path() string { return s.path }

// Start 监听 UDS：目录 `0700`、socket `0600`；陈旧 socket（dial 不通）先清理，活实例占位则拒启。
func (s *Server) Start() error {
	if s.resolve == nil {
		return fmt.Errorf("llmbridge: resolve callback required")
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("llmbridge: mkdir %s: %w", dir, err)
	}
	// 单例判定（按路径独立）：dial 得通 = 另一活实例 → 拒启；否则清掉陈旧文件。
	if c, err := net.DialTimeout("unix", s.path, 300*time.Millisecond); err == nil {
		_ = c.Close()
		return fmt.Errorf("llmbridge: %s 已被占用（另一实例在监听）", s.path)
	}
	if _, err := os.Stat(s.path); err == nil {
		_ = os.Remove(s.path)
	}
	ln, err := net.Listen("unix", s.path)
	if err != nil {
		return fmt.Errorf("llmbridge: listen %s: %w", s.path, err)
	}
	if err := os.Chmod(s.path, 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("llmbridge: chmod %s: %w", s.path, err)
	}
	s.ln = ln
	// 不设 WriteTimeout：SSE 流式长连接会被误断（同 httpapi）。
	s.srv = &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() { _ = s.srv.Serve(ln) }()
	return nil
}

// Shutdown 关闭监听并清理 socket 文件。
func (s *Server) Shutdown(ctx context.Context) error {
	if s.srv == nil {
		return nil
	}
	err := s.srv.Shutdown(ctx)
	_ = os.Remove(s.path)
	return err
}

// Handler 返回路由：`/llm/<name>/...` 走桥接；其余 → 404 提示。
// 刻意**不**用 http.ServeMux（其 cleanPath 会重写 `//` / `..`，与「剩余路径原样拼接」冲突）。
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, pathPrefix) {
			http.Error(w, "llm-bridge: not found (use /llm/<provider>/...) ", http.StatusNotFound)
			return
		}
		s.handleBridge(w, r)
	})
}

// handleBridge 解析 `/llm/<name>/<rest>` → 选 provider → 反代到 `base_url/<rest>`。
func (s *Server) handleBridge(w http.ResponseWriter, r *http.Request) {
	name, rest := splitBridgePath(r.URL.Path)
	if name == "" {
		http.Error(w, "llm-bridge: missing provider segment (use /llm/<provider>/...)", http.StatusNotFound)
		return
	}
	if s.resolve == nil {
		http.Error(w, "llm-bridge: resolver not configured", http.StatusInternalServerError)
		return
	}
	target, ok := s.resolve(name)
	if !ok {
		// 设计稿 §3.5：查不到 → 404 + 明确文案（不静默回落）。
		http.Error(w, fmt.Sprintf("llm-bridge: provider not found: %s", name), http.StatusNotFound)
		return
	}
	protocol := strings.ToLower(strings.TrimSpace(target.Protocol))
	if protocol == "" {
		protocol = protocolOpenAI // 缺省 → openai（与既有配置口径一致）
	}
	switch protocol {
	case protocolOpenAI, protocolResponses, protocolAnthropic:
	default:
		// 设计稿 §3.3：其它协议 → 400 明确不支持（无法判定鉴权方式，不静默猜）。
		http.Error(w, fmt.Sprintf("llm-bridge: unsupported protocol: %s", target.Protocol), http.StatusBadRequest)
		return
	}
	base, err := url.Parse(strings.TrimSpace(target.BaseURL))
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		http.Error(w, fmt.Sprintf("llm-bridge: invalid base_url for provider %s", name), http.StatusBadGateway)
		return
	}

	proxy := &httputil.ReverseProxy{
		// Rewrite（非 Director）：默认不追加 X-Forwarded-*，其余请求头/查询串/body 原样透传。
		Rewrite: func(pr *httputil.ProxyRequest) {
			out := pr.Out
			out.URL.Scheme = base.Scheme
			out.URL.Host = base.Host
			out.URL.Path = joinURLPath(base.Path, rest)
			out.URL.RawPath = "" // Path 已为解码形态，清 RawPath 防不一致
			out.URL.RawQuery = pr.In.URL.RawQuery
			out.Host = base.Host
			applyAuth(out.Header, protocol, target.APIKey)
		},
		// SSE 直通：立刻 flush，不缓冲（设计稿 §3.3「直 pipe，不缓冲、不改写」）。
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			// 上游错误 → 502；只记错误本身（**不含请求头，不落 apiKey**）。
			log.Printf("[llmbridge] upstream error (provider=%s protocol=%s): %v", name, protocol, err)
			http.Error(w, "llm-bridge: upstream error", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

// applyAuth 按 protocol **先删后设**鉴权头（设计稿 §3.3）：删除 `Authorization` / `x-api-key`
// 两族（**客户端自带头一律覆盖**，防自报），再按 protocol 注入服务端存的 key。
// apiKey 为空 → 只删不设（与既有「apiKey 空 → 不发送 Authorization」口径一致）。
func applyAuth(h http.Header, protocol, apiKey string) {
	h.Del("Authorization")
	h.Del("X-Api-Key") // http.Header canonical key for x-api-key
	switch protocol {
	case protocolOpenAI, protocolResponses:
		if apiKey != "" {
			h.Set("Authorization", "Bearer "+apiKey)
		}
	case protocolAnthropic:
		if apiKey != "" {
			h.Set("X-Api-Key", apiKey)
		}
	}
}

// splitBridgePath 把 `/llm/<name>/<rest>` 拆成 (name, rest)：
// rest = 首段之后的**剩余路径**（去前导 `/`；可含 `/`；空 = 无剩余）。**不做任何归一**。
func splitBridgePath(p string) (name, rest string) {
	p = strings.TrimPrefix(p, pathPrefix)
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i], p[i+1:]
	}
	return p, ""
}

// joinURLPath 把 provider.base_url 的路径与剩余路径**原样拼接**（设计稿 §3.4：
// `{base_url}/<rest>`；两侧各归一一个连接 `/`，不改动其余字符）。
func joinURLPath(basePath, rest string) string {
	b := strings.TrimSuffix(basePath, "/")
	r := strings.TrimPrefix(rest, "/")
	switch {
	case r == "":
		if b == "" {
			return "/"
		}
		return b
	case b == "":
		return "/" + r
	default:
		return b + "/" + r
	}
}
