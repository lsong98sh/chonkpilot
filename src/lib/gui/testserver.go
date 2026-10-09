//go:build windows

package gui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chonkpilot/chonkpilot-gui/bridge"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	webview2 "github.com/jchv/go-webview2"
)

// consoleCaptureJS 注入到每个文档创建时，重写 console.* 并挂 error/unhandledrejection
// 监听，捕获前端日志与 Vue 初始化错误到 window.__chonkConsole。仅 --test-port 模式注入。
const consoleCaptureJS = `(() => {
  if (window.__chonkConsole) return;
  const buf = { entries: [], max: 1000 };
  const push = (level, text) => {
    buf.entries.push({ t: Date.now(), level, text });
    if (buf.entries.length > buf.max)
      buf.entries.splice(0, buf.entries.length - buf.max);
  };
  const fmt = args => args.map(a => {
    if (typeof a === 'string') return a;
    if (a instanceof Error) return a.name + ': ' + a.message + '\n' + (a.stack || '');
    try { return JSON.stringify(a); } catch { return String(a); }
  }).join(' ');
  ['log', 'info', 'warn', 'error', 'debug'].forEach(lv => {
    const orig = console[lv];
    console[lv] = (...args) => { push(lv, fmt(args)); orig.apply(console, args); };
  });
  window.addEventListener('error', e => {
    const t = e.error instanceof Error
      ? e.error.name + ': ' + e.error.message + '\n' + (e.error.stack || '')
      : (e.message || 'script/asset load error: ' + (e.filename || '') + ':' + (e.lineno || ''));
    push('error', '[uncaught] ' + t);
  });
  window.addEventListener('unhandledrejection', e => {
    const r = e.reason;
    const t = r instanceof Error ? r.name + ': ' + r.message + '\n' + (r.stack || '') : String(r);
    push('error', '[unhandledrejection] ' + t);
  });
  window.__chonkConsole = buf;
})();`

// testServer 提供外部脚本（python 等）驱动 GUI 的测试通道（--test-port=2345）。
//
//	GET  /ping          → {ok, app, ready}
//	POST /eval          → {js, timeout} → EvalWithResult 原始 JSON 结果
//	POST /click         → {selector, timeout} 模拟点击（真实 DOM click，v-mq 可响应）
//	POST /input         → {selector, value, timeout} 模拟输入（原生 setter，触发 v-model）
//	POST /text          → {selector, timeout} 读取 textContent/value（压缩空白）
//	POST /html          → {selector, timeout} 读取 outerHTML
//	POST /exists        → {selector, timeout} → {count, visible}
//	POST /console       → {clear} 读取/清空 window.__chonkConsole.entries
//	GET  /screenshot    → PNG bytes
//	POST /publish       → {type, payload} 透传前端事件（桥发布 mq；窗口面消息由宿主侧处理）；
//	                      响应信封 {ok, result, errors}（请求-响应消息面 data-*/gui.* 走这里）
//	POST /wait-event    → {type, timeout} 阻塞等待指定 type 的事件到达；超时返回 error
//
// 多窗口（24 §4.5）：请求体可带 `window_id`（取自 `gui.window.list`）把上述端点指向某个
// **对话窗口**；缺省（不带）= 主窗口。对话窗口目标由窗口工厂经 RegisterChat 登记、关闭时注销。
type testServer struct {
	se      webview2.ScriptEval
	shot    webview2.Screenshot
	br      *bridge.Bridge
	hwnd    uintptr
	destroy func()
	// handler 是**主窗口**的 appHandler（窗口面消息 open-chat/list/set-title 由宿主侧处理，
	// 与 gui.window.status 同口径）。
	handler *appHandler
	mu      sync.Mutex // 串行化 eval（WebView2 UI 线程单飞）
	ready   atomic.Bool
	srv     *http.Server
	// ── 多窗口（24 §4.5 U-3 补齐）──────────────────────────────────────
	// chats 是**对话窗口**目标表（window_id → 目标）。请求体带 `window_id` 时，/eval、
	// /publish、/wait-event、/screenshot 作用于该窗口；缺省（不带该字段）= 主窗口 →
	// 单窗口行为与引入本表前逐字一致（既有套件零影响）。
	chatsMu sync.Mutex
	chats   map[string]*testWinTarget
}

// testWinTarget 是一个对话窗口的测试执行目标（主窗口对应 testServer 自身的字段）。
type testWinTarget struct {
	se      webview2.ScriptEval
	shot    webview2.Screenshot
	br      *bridge.Bridge
	hwnd    uintptr
	destroy func()
	handler *appHandler
	ready   atomic.Bool
}

// RegisterChat 把对话窗口登记为 test-port 可选目标（窗口工厂 onWired 调用，本窗口线程）。
// 同时把该窗口的就绪门控挂上（导航完成 → 该目标可用），与主窗口同款（主窗口用 s.ready）。
func (s *testServer) RegisterChat(h *windowHost) {
	if s == nil || h == nil || h.windowID == "" {
		return
	}
	se, ok := h.w.(webview2.ScriptEval)
	if !ok {
		return // 无 ScriptEval 能力 → 不登记（等同于该窗口不可被测试通道驱动）
	}
	shot, _ := h.w.(webview2.Screenshot)
	t := &testWinTarget{
		se: se, shot: shot, br: h.br, hwnd: h.hwnd, destroy: h.destroy, handler: h.handler,
	}
	s.chatsMu.Lock()
	if s.chats == nil {
		s.chats = make(map[string]*testWinTarget)
	}
	s.chats[h.windowID] = t
	s.chatsMu.Unlock()
	h.onReady = func() { t.ready.Store(true) }
}

// UnregisterChat 注销对话窗口目标（窗口关闭后该 window_id 不再可用）。
func (s *testServer) UnregisterChat(windowID string) {
	if s == nil || windowID == "" {
		return
	}
	s.chatsMu.Lock()
	delete(s.chats, windowID)
	s.chatsMu.Unlock()
}

// target 解析请求目标：windowID 为空 → (nil, nil) = 主窗口（用 testServer 自身字段）；
// 非空 → 对应对话窗口目标；未登记（已关闭 / 未知 id）→ error。
func (s *testServer) target(windowID string) (*testWinTarget, error) {
	if windowID == "" {
		return nil, nil
	}
	s.chatsMu.Lock()
	t := s.chats[windowID]
	s.chatsMu.Unlock()
	if t == nil {
		return nil, errors.New("unknown window_id: " + windowID)
	}
	return t, nil
}

// newTestServer 断言 webview 具备 ScriptEval 能力；Screenshot 可选。
func newTestServer(w interface{}, br *bridge.Bridge) *testServer {
	se, ok := w.(webview2.ScriptEval)
	if !ok {
		return nil
	}
	shot, _ := w.(webview2.Screenshot)
	return &testServer{se: se, shot: shot, br: br}
}

// InjectConsoleCapture 注入 console 捕获（导航前调用；Chromium.Init = AddScriptToExecuteOnDocumentCreated）。
func (s *testServer) InjectConsoleCapture(chromium interface{ Init(script string) }) {
	if s != nil && chromium != nil {
		chromium.Init(consoleCaptureJS)
	}
}

// Start 监听 127.0.0.1:port（不绑定 0.0.0.0）。
//
// 安全（D-03）：仅绑回环**仍不足**以防 DNS rebinding / 恶意网页——浏览器可带 `Host: evil.com`
// 访问 127.0.0.1 端口。故对所有端点统一做 Host 白名单校验（见 hostGuard / hostAllowed）：
// 仅接受 127.0.0.1 / localhost / app.localhost（端口不限），其余一律 403。
func (s *testServer) Start(port int) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", s.handlePing)
	mux.HandleFunc("/eval", s.handleEval)
	mux.HandleFunc("/click", s.handleClick)
	mux.HandleFunc("/input", s.handleInput)
	mux.HandleFunc("/text", s.handleText)
	mux.HandleFunc("/html", s.handleHTML)
	mux.HandleFunc("/exists", s.handleExists)
	mux.HandleFunc("/console", s.handleConsole)
	mux.HandleFunc("/screenshot", s.handleScreenshot)
	mux.HandleFunc("/publish", s.handlePublish)
	mux.HandleFunc("/wait-event", s.handleWaitEvent)
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	// D-44：补齐读头/空闲超时（原裸 http.Server 无任何超时，B-20 在 httpapi 已修的同族漏网）。
	// `/wait-event` 为最长 30s 的长轮询，故**不设** WriteTimeout，避免误断长轮询/截图响应。
	s.srv = &http.Server{
		Handler:           s.hostGuard(mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() { _ = s.srv.Serve(ln) }()
	return nil
}

// hostGuard 包裹全部端点：Host 不在白名单 → 403（挡 DNS rebinding / 恶意页面直连回环端口）。
func (s *testServer) hostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostAllowed(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostAllowed 判定 Host 头是否属白名单主机（仅比对主机名，忽略端口）：
// 127.0.0.1 / localhost / app.localhost。端口缺失（`127.0.0.1`）亦接受。
func hostAllowed(host string) bool {
	h := host
	if hp, _, err := net.SplitHostPort(host); err == nil {
		h = hp
	}
	switch strings.ToLower(h) {
	case "127.0.0.1", "localhost", "app.localhost":
		return true
	}
	return false
}

// Shutdown 优雅关闭（进程退出前调用）。
func (s *testServer) Shutdown(ctx context.Context) error {
	if s == nil || s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}

// SetReady 首次导航成功后置位，页面未就绪时 /eval 返回 503。
func (s *testServer) SetReady() { s.ready.Store(true) }

type testReq struct {
	Selector string `json:"selector"`
	Value    string `json:"value"`
	JS       string `json:"js"`
	Timeout  int    `json:"timeout"`
	Clear    bool   `json:"clear"`
	Type     string `json:"type"`
	Payload  string `json:"payload"`
	// WindowID 是请求目标窗口（`gui.window.list` 的 window_id）；空 = 主窗口（缺省）。
	WindowID string `json:"window_id"`
}

// timeoutMs 钳制到 [1s, 30s]，默认 5s。
func (s *testServer) timeoutMs(v int) time.Duration {
	if v <= 0 {
		v = 5000
	}
	if v > 30000 {
		v = 30000
	}
	return time.Duration(v) * time.Millisecond
}

// doEval 串行执行 JS 并返回 ExecuteScript 的原始 JSON 编码结果。
// windowID 非空 → 在对应对话窗口执行（各自就绪门控）。
func (s *testServer) doEval(js string, timeout time.Duration, windowID string) (string, error) {
	t, err := s.target(windowID)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if t == nil {
		if !s.ready.Load() {
			return "", errors.New("page not ready")
		}
		return s.se.EvalWithResult(js, timeout)
	}
	if !t.ready.Load() {
		return "", errors.New("page not ready")
	}
	return t.se.EvalWithResult(js, timeout)
}

// jsString 将 Go 字符串编码为 JS 字符串字面量（防注入）。
func jsString(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// unwrapString 便捷指令结果多为 JSON 编码字符串（"..."），解包成纯文本。
func unwrapString(res string) string {
	var s string
	if err := json.Unmarshal([]byte(res), &s); err == nil {
		return s
	}
	return res
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *testServer) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "app": "chonkpilot-gui", "ready": s.ready.Load()})
}

func (s *testServer) handleEval(w http.ResponseWriter, r *http.Request) {
	var req testReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad body: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.JS) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "js required"})
		return
	}
	res, err := s.doEval(req.JS, s.timeoutMs(req.Timeout), req.WindowID)
	s.respond(w, res, err)
}

func (s *testServer) handleClick(w http.ResponseWriter, r *http.Request) {
	var req testReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Selector) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "selector required"})
		return
	}
	sel := jsString(req.Selector)
	js := `(()=>{const el=document.querySelector(` + sel + `);if(!el)throw new Error('not found: '+` + sel + `);el.click();return 'ok';})()`
	res, err := s.doEval(js, s.timeoutMs(req.Timeout), req.WindowID)
	s.respond(w, unwrapString(res), err)
}

func (s *testServer) handleInput(w http.ResponseWriter, r *http.Request) {
	var req testReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Selector) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "selector required"})
		return
	}
	sel, val := jsString(req.Selector), jsString(req.Value)
	js := `(()=>{const el=document.querySelector(` + sel + `);if(!el)throw new Error('not found: '+` + sel + `);const proto=el instanceof HTMLTextAreaElement?HTMLTextAreaElement.prototype:HTMLInputElement.prototype;Object.getOwnPropertyDescriptor(proto,'value').set.call(el,` + val + `);el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));return 'ok';})()`
	res, err := s.doEval(js, s.timeoutMs(req.Timeout), req.WindowID)
	s.respond(w, unwrapString(res), err)
}

func (s *testServer) handleText(w http.ResponseWriter, r *http.Request) {
	var req testReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Selector) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "selector required"})
		return
	}
	sel := jsString(req.Selector)
	js := `(()=>{const el=document.querySelector(` + sel + `);if(!el)throw new Error('not found: '+` + sel + `);const v=(el instanceof HTMLTextAreaElement||el instanceof HTMLInputElement)?(el.value||''):(el.textContent||'');return v.replace(/\s+/g,' ').trim();})()`
	res, err := s.doEval(js, s.timeoutMs(req.Timeout), req.WindowID)
	s.respond(w, unwrapString(res), err)
}

func (s *testServer) handleHTML(w http.ResponseWriter, r *http.Request) {
	var req testReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Selector) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "selector required"})
		return
	}
	sel := jsString(req.Selector)
	js := `(()=>{const el=document.querySelector(` + sel + `);if(!el)throw new Error('not found: '+` + sel + `);return el.outerHTML;})()`
	res, err := s.doEval(js, s.timeoutMs(req.Timeout), req.WindowID)
	s.respond(w, unwrapString(res), err)
}

func (s *testServer) handleExists(w http.ResponseWriter, r *http.Request) {
	var req testReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Selector) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "selector required"})
		return
	}
	sel := jsString(req.Selector)
	js := `(()=>{const els=document.querySelectorAll(` + sel + `);const el=els[0];return JSON.stringify({count:els.length,visible:!!(el&&el.getClientRects().length>0)});})()`
	res, err := s.doEval(js, s.timeoutMs(req.Timeout), req.WindowID)
	if err != nil {
		s.respond(w, "", err)
		return
	}
	res = unwrapString(res)
	var info struct {
		Count   int  `json:"count"`
		Visible bool `json:"visible"`
	}
	_ = json.Unmarshal([]byte(res), &info)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": info})
}

func (s *testServer) handleConsole(w http.ResponseWriter, r *http.Request) {
	var req testReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	js := `JSON.stringify(window.__chonkConsole?window.__chonkConsole.entries:[])`
	if req.Clear {
		js = `(()=>{const b=window.__chonkConsole;if(b)b.entries.length=0;return JSON.stringify([]);})()`
	}
	res, err := s.doEval(js, s.timeoutMs(req.Timeout), req.WindowID)
	if err != nil {
		s.respond(w, "", err)
		return
	}
	res = unwrapString(res)
	var entries []map[string]any
	_ = json.Unmarshal([]byte(res), &entries)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": map[string]any{"entries": entries, "truncated": len(entries) >= 1000}})
}

func (s *testServer) handleScreenshot(w http.ResponseWriter, r *http.Request) {
	// GET 无请求体 → 目标窗口经查询参数 `window_id` 指定（缺省 = 主窗口）。
	shot, ready := s.shot, s.ready.Load()
	if t, err := s.target(r.URL.Query().Get("window_id")); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	} else if t != nil {
		shot, ready = t.shot, t.ready.Load()
	}
	if shot == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"ok": false, "error": "screenshot not supported"})
		return
	}
	if !ready {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "page not ready"})
		return
	}
	data, err := shot.Screenshot(5 * time.Second)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(data)
}

// handlePublish 外部 /publish 透传：本地事件优先，否则桥发布 mq（对齐 main appHandler）。
// `window_id` 指定目标窗口（缺省 = 主窗口）→ 窗口定向命令 / 窗口面消息作用于该窗口。
func (s *testServer) handlePublish(w http.ResponseWriter, r *http.Request) {
	var req testReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Type == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "type required"})
		return
	}
	t, terr := s.target(req.WindowID)
	if terr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": terr.Error()})
		return
	}
	hwnd, destroy, handler, br := s.hwnd, s.destroy, s.handler, s.br
	if t != nil {
		hwnd, destroy, handler, br = t.hwnd, t.destroy, t.handler, t.br
	}
	var result any
	var errs []error
	// gui.window.status：统一窗口控制/查询（command 语义见 applyWindowCommand）。
	if req.Type == msgkeys.TopicGuiWindowStatus {
		result, errs = applyWindowCommand(hwnd, destroy, br, req.Payload)
	} else if isWindowMessage(req.Type) && handler != nil {
		// 窗口面消息（open-chat/list/set-title）：宿主侧处理（同 appHandler.handlePublish）。
		result, errs = handler.handleWindowMessage(req.Type, req.Payload)
	} else {
		result, errs = br.PublishEvent(req.Type, req.Payload)
	}
	var emsg []string
	for _, e := range errs {
		emsg = append(emsg, e.Error())
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     len(emsg) == 0,
		"result": result,
		"errors": emsg,
	})
}

// handleWaitEvent 阻塞等待指定 type 的事件到达（桥 forwardEvent 途经时通知）。
// `window_id` 指定目标窗口的桥（缺省 = 主窗口）。
func (s *testServer) handleWaitEvent(w http.ResponseWriter, r *http.Request) {
	var req testReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Type == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "type required"})
		return
	}
	br := s.br
	if t, err := s.target(req.WindowID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	} else if t != nil {
		br = t.br
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.timeoutMs(req.Timeout))
	defer cancel()
	ev, err := br.WaitForEvent(ctx, req.Type)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": ev})
}

func (s *testServer) respond(w http.ResponseWriter, result string, err error) {
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": result})
}
