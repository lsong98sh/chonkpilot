// Package llmbridge 实现 **LLM 桥接入口**（BRIDGE-1）——一个「选 provider + 注入 key」的
// **透明反向代理**：本机脚本（Python 等）用自选协议的 SDK 指向
// `http://127.0.0.1:5669/llm/<name>/...`，即可**免 api-key** 调用本机已配置的 LLM。
//
// 设计依据：docs/llm-bridge/LLM桥接端口设计方案.md（§2 总体方案 · §3 详细设计 · §4 规则 ·
// §5 与既有机制的关系 · §6 边界）。要点：
//
//   - **端口/开关**：`127.0.0.1:5669`，默认开（开关 = `--llm-bridge-addr`，见 `src/server/main.go`）；
//     裸端口（如 `5669`）→ `127.0.0.1:5669`（与 httpapi 同规则）；
//   - **仅 loopback**：非 loopback **拒启**（免鉴权 → fail-closed，比 httpapi 的 WARN 更严）；
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
// 已知威胁面（设计稿 §4/§6，如实登记）：免鉴权 + 仅 loopback = **本机任意进程可白嫖已配置 key**，
// 系「脚本零配置可用」的取舍。
package llmbridge

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// DefaultAddr 是入口的推荐监听地址（仅本机；设计稿 §3.1/§4「端口 127.0.0.1:5669」）。
const DefaultAddr = "127.0.0.1:5669"

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
	// Addr 监听地址；空 = DefaultAddr；裸端口（如 "5669"）= 127.0.0.1:5669。
	Addr string
	// Resolve 是 provider 解析回调（必填；nil → Start 报错）。
	Resolve Resolver
}

// Server 是 LLM 桥接入口（无状态：每次请求独立解析 provider，天然并发安全）。
type Server struct {
	addr    string
	resolve Resolver
	ln      net.Listener
	srv     *http.Server
}

// New 构造入口（未监听；Start 才监听）。地址归一（与 httpapi.New 同规则）：
// 空 → DefaultAddr；裸端口 → 127.0.0.1:<port>；`localhost` → `127.0.0.1`（确定性 loopback）。
func New(opts Options) *Server {
	addr := strings.TrimSpace(opts.Addr)
	if addr == "" {
		addr = DefaultAddr
	}
	if !strings.Contains(addr, ":") {
		addr = "127.0.0.1:" + addr // 裸端口 = 仅本机（默认安全绑定）
	}
	if host, port, err := net.SplitHostPort(addr); err == nil && strings.EqualFold(host, "localhost") {
		addr = net.JoinHostPort("127.0.0.1", port) // 避免 localhost 解析漂移，确定性 loopback
	}
	return &Server{addr: addr, resolve: opts.Resolve}
}

// Addr 返回实际监听地址（Start 之后为真实地址）。
func (s *Server) Addr() string {
	if s.ln != nil {
		return s.ln.Addr().String()
	}
	return s.addr
}

// Start 校验 loopback（**非 loopback 拒启**，fail-closed）→ 监听。
func (s *Server) Start() error {
	if s.resolve == nil {
		return fmt.Errorf("llmbridge: resolve callback required")
	}
	if !addrIsLoopback(s.addr) {
		return fmt.Errorf("llmbridge: 拒绝启动：%s 非 loopback（免鉴权入口必须仅本机，设计稿 §3.1/§3.5）", s.addr)
	}
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("llmbridge: listen %s: %w", s.addr, err)
	}
	// 二道防线：实际监听地址仍非 loopback（如解析漂移）→ 关闭并拒启。
	if !isLoopback(ln.Addr()) {
		_ = ln.Close()
		return fmt.Errorf("llmbridge: 拒绝监听非 loopback 地址 %s", ln.Addr())
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

// Shutdown 关闭监听（无其它资源；总线不涉及）。
func (s *Server) Shutdown(ctx context.Context) error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
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

// addrIsLoopback 判定监听地址字符串是否**仅绑定本机**（裸端口已由 New 归一）。
// 无法解析 / 空 host / 显式全接口（`:5669`）→ 非 loopback（fail-closed，宁可多拦）。
func addrIsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// isLoopback 判定**实际监听地址**是否仅本机（Start 的二道防线）。
func isLoopback(addr net.Addr) bool {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return false
	}
	return tcp.IP.IsLoopback()
}
