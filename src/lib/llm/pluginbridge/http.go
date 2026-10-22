package pluginbridge

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/transport"
)

// HTTP 前门：与帧面共用同一套 handler / 白名单 / 审计，只多一层传输转换。
//
//   - POST /plugin/v1/hello   创建/复用 HTTP 会话并 dispatch plugin.hello
//   - POST /plugin/v1/{method} 以 `X-Plugin-ID` 定位会话后直接 dispatch
//   - GET  /plugin/v1/events  SSE：输出该会话收到的 evt / ctl 帧
//   - POST /plugin/v1/close   关闭该插件的 HTTP 会话（幂等）
//
// hello / events / close 为保留名，不能作为业务 method。

const (
	httpPrefix       = "/plugin/v1/"
	httpHelloPath    = httpPrefix + "hello"
	httpEventsPath   = httpPrefix + "events"
	httpClosePath    = httpPrefix + "close"
	httpEventsQueue  = 256
	httpMaxBodyBytes = 64 << 20
	httpPingInterval = 15 * time.Second
	httpHeaderPlugin = "X-Plugin-ID"
)

// httpSession 是一条 HTTP 插件会话（一个 plugin_id 一条）。
type httpSession struct {
	sess *Session
	conn *httpConn
	// mu 串行化该会话的 dispatch（HTTP 请求并发到达时避免 handler 竞态）。
	mu sync.Mutex
}

// httpConn 是 HTTP 前门的 transport.Conn 适配器：
// Send → SSE 发送通道（有界，满则丢弃）；Recv/反向 Call 不使用。
type httpConn struct {
	pluginID string
	logf     func(format string, args ...any)

	ch     chan transport.Frame
	closed chan struct{}

	closeOnce sync.Once
	sseMu     sync.Mutex
	sseDone   chan struct{}
}

func newHTTPConn(pluginID string, logf func(format string, args ...any)) *httpConn {
	return &httpConn{
		pluginID: pluginID,
		logf:     logf,
		ch:       make(chan transport.Frame, httpEventsQueue),
		closed:   make(chan struct{}),
	}
}

func (c *httpConn) Send(f transport.Frame) error {
	select {
	case <-c.closed:
		return transport.ErrClosed
	default:
	}
	select {
	case c.ch <- f:
		return nil
	default:
		// 与帧面慢消费者口径一致：丢帧而不是阻塞宿主。
		if c.logf != nil {
			c.logf("pluginbridge: 慢 HTTP 插件 %s，丢弃帧 %s/%s", c.pluginID, f.Kind, f.Name)
		}
		return nil
	}
}

func (c *httpConn) Recv() (transport.Frame, error) {
	<-c.closed
	return transport.Frame{}, transport.ErrClosed
}

func (c *httpConn) Call(context.Context, string, any) (transport.Result, error) {
	return transport.Result{}, errors.New("pluginbridge: HTTP 传输不支持服务端反向调用")
}

func (c *httpConn) Principal() transport.Principal { return transport.NilPrincipal }
func (c *httpConn) Remote() string                 { return "http" }
func (c *httpConn) Closed() <-chan struct{}        { return c.closed }

func (c *httpConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		c.sseMu.Lock()
		if c.sseDone != nil {
			close(c.sseDone)
			c.sseDone = nil
		}
		c.sseMu.Unlock()
	})
	return nil
}

// attachSSE 绑定当前 SSE 连接；重复连接时踢掉旧连接（同一会话只保留一条 SSE）。
func (c *httpConn) attachSSE() chan struct{} {
	c.sseMu.Lock()
	if c.sseDone != nil {
		close(c.sseDone)
	}
	done := make(chan struct{})
	c.sseDone = done
	c.sseMu.Unlock()
	return done
}

func (c *httpConn) detachSSE(done chan struct{}) {
	c.sseMu.Lock()
	if c.sseDone == done {
		c.sseDone = nil
	}
	c.sseMu.Unlock()
}

// startHTTP 启动 HTTP 前门（仅 loopback；地址非法 → 明确错误）。
func (s *Server) startHTTP() error {
	addr, err := normalizeHTTPAddr(s.opts.HTTPAddr)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("pluginbridge: HTTP 监听 %s: %w", addr, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc(httpHelloPath, s.handleHTTPHello)
	mux.HandleFunc(httpEventsPath, s.handleHTTPEvents)
	mux.HandleFunc(httpClosePath, s.handleHTTPClose)
	mux.HandleFunc(httpPrefix, s.handleHTTPMethod)

	hsrv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.httpMu.Lock()
	s.httpLn = ln
	s.httpSrv = hsrv
	s.httpMu.Unlock()

	go func() {
		if err := hsrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logf("pluginbridge: HTTP 前门退出: %v", err)
		}
	}()
	return nil
}

// normalizeHTTPAddr 归一化并校验监听地址：仅允许 loopback（禁止 0.0.0.0 / LAN 绑定）。
// 裸端口（如 `:0` / `5671`）→ 127.0.0.1:<port>。
func normalizeHTTPAddr(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", errors.New("pluginbridge: HTTPAddr 为空")
	}
	if !strings.Contains(addr, ":") {
		addr = "127.0.0.1:" + addr
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("pluginbridge: HTTPAddr %q 非法: %w", addr, err)
	}
	if port == "" {
		return "", fmt.Errorf("pluginbridge: HTTPAddr %q 缺少端口", addr)
	}
	if host == "" {
		// `:5671` = 全接口监听 → 收敛到 loopback。
		return net.JoinHostPort("127.0.0.1", port), nil
	}
	if strings.EqualFold(host, "localhost") {
		ips, lerr := net.LookupIP(host)
		if lerr != nil {
			return "", fmt.Errorf("pluginbridge: 解析 localhost 失败: %w", lerr)
		}
		for _, ip := range ips {
			if !ip.IsLoopback() {
				return "", fmt.Errorf("pluginbridge: HTTPAddr %q 仅允许 loopback（localhost 解析到 %s）", addr, ip)
			}
		}
		return net.JoinHostPort("127.0.0.1", port), nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		ips, lerr := net.LookupIP(host)
		if lerr != nil {
			return "", fmt.Errorf("pluginbridge: 解析 HTTPAddr %q 失败: %w", addr, lerr)
		}
		for _, x := range ips {
			if !x.IsLoopback() {
				return "", fmt.Errorf("pluginbridge: HTTPAddr %q 仅允许 loopback（解析到 %s）", addr, x)
			}
		}
		if len(ips) == 0 {
			return "", fmt.Errorf("pluginbridge: HTTPAddr %q 无法解析", addr)
		}
		return net.JoinHostPort(ips[0].String(), port), nil
	}
	if !ip.IsLoopback() {
		return "", fmt.Errorf("pluginbridge: HTTPAddr %q 仅允许 loopback（127.0.0.1 / ::1）", addr)
	}
	return net.JoinHostPort(host, port), nil
}

// ── 路由 ──

func (s *Server) handleHTTPHello(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeHTTPJSON(w, http.StatusMethodNotAllowed, httpErrorResponse{Errors: []string{CodeMethodNotFound + ": hello 仅支持 POST"}})
		return
	}
	if !s.httpAuthorize(w, r) {
		return
	}
	body, err := readHTTPBody(r)
	if err != nil {
		writeHTTPJSON(w, http.StatusBadRequest, httpErrorResponse{Errors: []string{CodeArgsInvalid + ": " + err.Error()}})
		return
	}
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &in); err != nil || sanitizePluginID(in.ID) == "" {
		writeHTTPJSON(w, http.StatusBadRequest, httpErrorResponse{Errors: []string{CodeArgsInvalid + ": plugin.hello 需要非空 id"}})
		return
	}
	hs := s.ensureHTTPSession(in.ID)
	if hs == nil {
		writeHTTPJSON(w, http.StatusServiceUnavailable, httpErrorResponse{Errors: []string{CodePluginDisabled + ": 插件前门已关闭"}})
		return
	}
	hs.mu.Lock()
	_, derr := s.dispatch(r.Context(), hs.sess, "plugin.hello", body)
	hs.mu.Unlock()
	if derr != nil {
		s.audit(hs.sess, "plugin.hello", false, codeOf(derr))
		writeHTTPJSON(w, http.StatusBadRequest, httpErrorResponse{Errors: []string{derr.Error()}})
		return
	}
	s.audit(hs.sess, "plugin.hello", true, "")
	writeHTTPJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"plugin_id":   hs.sess.PluginID(),
		"instance_id": hs.sess.InstanceID(),
	})
}

func (s *Server) handleHTTPMethod(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeHTTPJSON(w, http.StatusMethodNotAllowed, httpErrorResponse{Errors: []string{CodeMethodNotFound + ": 仅支持 POST"}})
		return
	}
	if !s.httpAuthorize(w, r) {
		return
	}
	method := strings.TrimPrefix(r.URL.Path, httpPrefix)
	if method == "" || method == "hello" || method == "events" || method == "close" {
		writeHTTPJSON(w, http.StatusNotFound, httpErrorResponse{Errors: []string{CodeMethodNotFound + ": 保留路径 / 空方法"}})
		return
	}
	pluginID := strings.TrimSpace(r.Header.Get(httpHeaderPlugin))
	if pluginID == "" {
		writeHTTPJSON(w, http.StatusBadRequest, httpErrorResponse{Errors: []string{CodePluginUnregistered + ": 缺少 " + httpHeaderPlugin}})
		return
	}
	hs := s.lookupHTTPSession(pluginID)
	if hs == nil {
		writeHTTPJSON(w, http.StatusBadRequest, httpErrorResponse{Errors: []string{CodePluginUnregistered + ": 请先 POST " + httpHelloPath}})
		return
	}
	body, err := readHTTPBody(r)
	if err != nil {
		writeHTTPJSON(w, http.StatusBadRequest, httpErrorResponse{Errors: []string{CodeArgsInvalid + ": " + err.Error()}})
		return
	}
	hs.mu.Lock()
	v, derr := s.dispatch(r.Context(), hs.sess, method, body)
	hs.mu.Unlock()
	if derr != nil {
		s.audit(hs.sess, method, false, codeOf(derr))
		// 错误在 body，HTTP 200（与帧面 res.errors 同口径）。
		writeHTTPJSON(w, http.StatusOK, httpErrorResponse{Errors: []string{derr.Error()}})
		return
	}
	s.audit(hs.sess, method, true, "")
	writeHTTPJSON(w, http.StatusOK, map[string]any{"ok": true, "result": v})
}

func (s *Server) handleHTTPEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeHTTPJSON(w, http.StatusMethodNotAllowed, httpErrorResponse{Errors: []string{CodeMethodNotFound + ": events 仅支持 GET"}})
		return
	}
	if !s.httpAuthorize(w, r) {
		return
	}
	pluginID := strings.TrimSpace(r.Header.Get(httpHeaderPlugin))
	if pluginID == "" {
		writeHTTPJSON(w, http.StatusBadRequest, httpErrorResponse{Errors: []string{CodePluginUnregistered + ": 缺少 " + httpHeaderPlugin}})
		return
	}
	hs := s.lookupHTTPSession(pluginID)
	if hs == nil {
		writeHTTPJSON(w, http.StatusBadRequest, httpErrorResponse{Errors: []string{CodePluginUnregistered + ": 请先 POST " + httpHelloPath}})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeHTTPJSON(w, http.StatusInternalServerError, httpErrorResponse{Errors: []string{CodeInternal + ": 不支持流式响应"}})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	done := hs.conn.attachSSE()
	defer hs.conn.detachSSE(done)

	ping := time.NewTicker(httpPingInterval)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-done:
			return
		case <-hs.conn.Closed():
			return
		case f := <-hs.conn.ch:
			b, err := json.Marshal(f)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return
			}
			flusher.Flush()
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) handleHTTPClose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeHTTPJSON(w, http.StatusMethodNotAllowed, httpErrorResponse{Errors: []string{CodeMethodNotFound + ": close 仅支持 POST"}})
		return
	}
	if !s.httpAuthorize(w, r) {
		return
	}
	pluginID := strings.TrimSpace(r.Header.Get(httpHeaderPlugin))
	if pluginID == "" {
		writeHTTPJSON(w, http.StatusBadRequest, httpErrorResponse{Errors: []string{CodePluginUnregistered + ": 缺少 " + httpHeaderPlugin}})
		return
	}
	// 先取后关（不在持有 httpMu 时调用 Session.Close，避免与 removeSession 互锁）。
	if hs := s.lookupHTTPSession(pluginID); hs != nil {
		hs.sess.Close()
	}
	writeHTTPJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ── 会话管理 ──

func (s *Server) ensureHTTPSession(pluginID string) *httpSession {
	s.httpMu.Lock()
	defer s.httpMu.Unlock()
	if hs, ok := s.httpSessions[pluginID]; ok {
		return hs
	}
	conn := newHTTPConn(pluginID, s.logf)
	sess := newSession(s, conn)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = conn.Close()
		return nil
	}
	s.sessions[sess] = struct{}{}
	s.mu.Unlock()
	// HTTP 面不使用 serve()/worker()：请求由 HTTP handler 直接 dispatch；
	// 只启动 writer 消费 outQ（Broadcast / MarkReady / ShutdownPlugin 经此到 SSE）。
	go sess.writer()
	hs := &httpSession{sess: sess, conn: conn}
	s.httpSessions[pluginID] = hs
	return hs
}

func (s *Server) lookupHTTPSession(pluginID string) *httpSession {
	s.httpMu.Lock()
	defer s.httpMu.Unlock()
	return s.httpSessions[pluginID]
}

// ── 鉴权 / 响应 ──

func (s *Server) httpAuthorize(w http.ResponseWriter, r *http.Request) bool {
	if s.opts.HTTPToken == "" {
		return true
	}
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) ||
		subtle.ConstantTimeCompare([]byte(h[len(prefix):]), []byte(s.opts.HTTPToken)) != 1 {
		writeHTTPJSON(w, http.StatusUnauthorized, httpErrorResponse{
			Errors: []string{CodePluginUnauthorized + ": 缺少或错误的 Bearer token"},
		})
		return false
	}
	return true
}

type httpErrorResponse struct {
	OK     bool     `json:"ok"`
	Errors []string `json:"errors"`
}

func writeHTTPJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func readHTTPBody(r *http.Request) (json.RawMessage, error) {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, httpMaxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > httpMaxBodyBytes {
		return nil, errors.New("请求体超过上限")
	}
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) == 0 {
		return json.RawMessage("{}"), nil
	}
	var probe map[string]any
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, fmt.Errorf("body 必须是 JSON 对象: %w", err)
	}
	return json.RawMessage(body), nil
}
