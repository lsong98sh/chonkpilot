// Package httpapi 提供「服务端 ↔ 浏览器客户端」的跨进程入口（HTTP + SSE）：静态面 +
// `POST /publish`（上行，等价 GUI 桥的 `/publish`）+ `GET /events`（下行，SSE）。
// 设计见
// docs/spec/10-architecture/19-多租户服务端形态.md §8.3/§8.5（方案 A 的最小可交付切片 D：
// 先跑通「1 服务端 + 1 浏览器客户端」）。
//
// 与 GUI 桥（chonkpilot-gui/bridge）的关系：
//   - 上行语义**逐字对齐**：`{type,payload}` → 白名单校验 → 注入**服务端进程内总线** →
//     应答信封 `{ok,result,errors}`（61-消息一览 §9.1）；
//   - 下行载体不同：GUI = WebView2 `w.Eval` 调 `window.mq.emitRemote`；此处 = SSE，
//     由服务端注入的前端 shim（ck_http_bridge.js）转投**同一入口**；
//   - instance 归属过滤复用桥的同名判据（载荷 `instance_id` 非空且 ≠ 本实例 → 丢弃；
//     单 instance 恒真）——本切片 1 服务端 1 instance；
//   - **托管 filesys 组件**（同进程同总线）：`filesys.*` 请求由服务端应答，`work_dir` 由
//     服务端绑定（`bindFilesys` 覆盖请求载荷，浏览器端不得自报任意目录）；filesys 语义不变；
//   - **多客户端**：本 instance 的全部 SSE 客户端共享同一广播表，事件 fan-out 到所有客户端
//     （非本 instance 事件按上述判据不投递）；
//   - native 能力（`gui.dir.open-dialog` / `pick-executable` / `dir.open` / `console.open` /
//     `reveal` / `open-with` / `capture` / `toolchain.detect` / `system.builtins` /
//     `window.status`）**明确返回"不支持"**（不静默失败、不假成功，见 19 §6）；其中
//     `dir.open-dialog` 另提供**非 MQ 的服务端等价面** `GET /dirs`（列出本 instance
//     允许目录；61 §1 的 result `{path?}` 不变，见 §8.9）；**不复用** testserver 的
//     `/eval`·`/click`。
//
// 消息面：**零新增主题/payload**（61-消息一览 为唯一准则）；本包只发既有
// `instance-register` / `instance-exit`（载荷字段与 GUI 桥一致，`client_type=browser`）。
package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-filesys"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// DefaultAddr 是入口的推荐监听地址（仅本机；19 §4「端口默认 5668」）。
const DefaultAddr = "127.0.0.1:5668"

// 路由（前端 mq 客户端既有约定：上行 `/publish`；其余为本入口新增的 HTTP 面，非 MQ 主题）。
const (
	publishPath = "/publish"
	eventsPath  = "/events"
	shimPath    = "/__ck_bridge.js"
	dirsPath    = "/dirs"
)

//go:embed ck_http_bridge.js
var shimJS []byte

// Options 是入口构造参数（单 instance：work_dir/data_dir 由启动参数派生，19 §8.5 第 2 条）。
type Options struct {
	// Bus 是服务端进程内总线（必填；与 server/persist/gateway 同一实例）。
	Bus mq.Bus
	// InstanceID 单 instance 标识；空 = 自动生成（uuid v4）。
	InstanceID string
	// WorkDir 工作目录（前端文件树根；经 instance-register 下发数据面）。
	WorkDir string
	// DataDir 项目数据根（同上；空 = 数据面按 <WorkDir>/.chonkpilot 解析）。
	DataDir string
	// WebRoot 静态面根目录（前端 dist 外部目录）。**显式指定（非空）= 覆盖内嵌静态面**
	// （见 WebFS）；空 = 用内嵌面。两者皆缺 → `/` 返回 503 并提示。
	WebRoot string
	// WebFS 内嵌静态面（browser 形态前端 go:embed 进 server exe；
	// 装配点 = `src/server/frontend.go` 的 webFS()）。WebRoot 为空时静态面读本 FS
	// （路径相对 dist 根，如 index.html）；WebRoot 非空 → 本 FS 不参与（外部目录优先）。
	WebFS fs.FS
	// AuthCheck 是「令牌有效性判定」回调（阶段 2b-2；宿主注入 = llm server 的
	// `Authenticated`）—— 首屏注入 `authed` 的**服务端判定**来源（61 §4.6：读凭证判定，
	// 不读 UA、不接受前端自报）。nil = 未接线 → `authed` 恒 false（未认证）。
	AuthCheck func(token string) bool
	// Facade 是 data 门面绑定（阶段 4 第三批 / 41 G-34；装配点 = 宿主 `inline.New(bus)`）。
	// 非空 → `data-session-*` 上行**优先走门面**（服务端进程内直调，不经 MQ；见
	// facade_session.go），未命中/未注入 → 回落总线 persist 路径（dataViaPersist）。
	// 两条路径的应答载荷逐字一致（同一份 `facade/wire` 翻译）。
	Facade facade.API
	// Addr 监听地址；空 = DefaultAddr。裸端口（如 "5668"）= 127.0.0.1:5668。
	// 绑定非本机地址必须显式写全（如 "0.0.0.0:5668"）——**要求认证（requireAuth=true）**，
	// 且必须自备 HTTPS 与网络暴露面限制（61 §4.6 硬前提）。
	Addr string
}

// Server 是 HTTP + SSE 入口（单 instance）。
type Server struct {
	bus        mq.Bus
	instanceID string
	workDir    string
	dataDir    string
	webRoot    string
	// webFS 是内嵌静态面（browser 形态前端 go:embed 进 server exe）；webRoot 非空时不使用。
	webFS fs.FS
	addr  string
	// authCheck 是宿主注入的令牌有效性判定（nil = 未接线 → authed 恒 false）。
	authCheck func(token string) bool
	// facade 是 data 门面绑定（非空 → data-session-* 优先走门面，见 facade_session.go）。
	facade facade.API

	// published 记录本实例近期发布的主题（防环：本包订阅了 ">"，自身 emit 的事件
	// 不应再经 SSE 回投前端造成二次 dispatch；与桥 markPublished 同语义）。
	publishedMu sync.Mutex
	published   map[string]time.Time

	// clients 是已连接的 SSE 客户端（广播入口；本 instance 的全部客户端都收到同一事件）。
	mu      sync.Mutex
	clients map[chan []byte]struct{}

	// fsys 是服务端托管的文件服务（应答 filesys.*；work_dir 由服务端绑定，见 bindFilesys）。
	fsys *filesys.Filesys

	sub mq.Sub
	ln  net.Listener
	srv *http.Server
}

// New 构造入口（未监听；Start 才注册 instance + 监听）。
func New(bus mq.Bus, opts Options) *Server {
	id := opts.InstanceID
	if id == "" {
		id = newUUID()
	}
	addr := opts.Addr
	if addr == "" {
		addr = DefaultAddr
	}
	if !strings.Contains(addr, ":") {
		addr = "127.0.0.1:" + addr // 裸端口 = 仅本机（默认安全绑定）
	}
	return &Server{
		bus:        bus,
		instanceID: id,
		workDir:    opts.WorkDir,
		dataDir:    opts.DataDir,
		webRoot:    opts.WebRoot,
		webFS:      opts.WebFS,
		addr:       addr,
		authCheck:  opts.AuthCheck,
		facade:     opts.Facade,
		published:  make(map[string]time.Time),
		clients:    make(map[chan []byte]struct{}),
	}
}

// InstanceID 返回单 instance 标识（日志用）。
func (s *Server) InstanceID() string { return s.instanceID }

// Addr 返回实际监听地址（Start 之后为真实地址）。
func (s *Server) Addr() string {
	if s.ln != nil {
		return s.ln.Addr().String()
	}
	return s.addr
}

// Start 订阅总线（下行）+ 注册 instance + 监听地址。
func (s *Server) Start() error {
	if s.bus == nil {
		return fmt.Errorf("httpapi: bus required")
	}
	sub, err := s.bus.On(">", 0, func(_ context.Context, subject string, v *mq.Value) error {
		s.forwardEvent(subject, v.Payload)
		return nil
	})
	if err != nil {
		return fmt.Errorf("httpapi: subscribe: %w", err)
	}
	s.sub = sub

	// 托管 filesys 组件（服务端 exe 内应答 filesys.*；此前服务端无应答方 → 文件树展开为空）。
	// work_dir 由服务端绑定（bindFilesys 覆盖请求载荷，浏览器端不得自报任意目录），
	// filesys 自身语义不变（仍以 work_dir 为管理单位、withinWorkDir 为第二道）。
	fsys := filesys.New(s.bus)
	if err := fsys.Start(); err != nil {
		_ = sub.Unsubscribe()
		s.sub = nil
		return fmt.Errorf("httpapi: filesys start: %w", err)
	}
	s.fsys = fsys

	// instance 注册：载荷字段与 GUI 桥一致（work_dir/data_dir 由服务端启动参数下发；
	// 19 §5「instance-register 已带 work_dir/data_dir，消息面零变更」），client_type 标形态。
	s.publish(msgkeys.TopicInstanceRegister, map[string]any{
		"instance_id": s.instanceID,
		"work_dir":    s.workDir,
		"data_dir":    s.dataDir,
		"client_type": "browser",
	})

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		if s.fsys != nil {
			s.fsys.Stop()
			s.fsys = nil
		}
		_ = sub.Unsubscribe()
		s.sub = nil
		return fmt.Errorf("httpapi: listen %s: %w", s.addr, err)
	}
	s.ln = ln
	s.srv = &http.Server{Handler: s.Handler()}
	go func() { _ = s.srv.Serve(ln) }()

	if !isLoopback(ln.Addr()) {
		log.Printf("[server] WARN httpapi 监听非本机地址 %s：本入口 requireAuth=true（认证域已接线），但**不会**自动提供 HTTPS/CORS —— 请自备 HTTPS 并限制网络暴露面（61 §4.6 硬前提）", ln.Addr())
	}
	return nil
}

// Shutdown 注销 instance + 停止 filesys + 关闭监听（总线由调用方持有，本包不 Close）。
func (s *Server) Shutdown(ctx context.Context) error {
	if s.sub != nil {
		_ = s.sub.Unsubscribe()
		s.sub = nil
	}
	if s.fsys != nil {
		s.fsys.Stop()
		s.fsys = nil
	}
	s.publish(msgkeys.TopicInstanceExit, map[string]any{"instance_id": s.instanceID})
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}

// Handler 返回路由（静态面 / 上行 / 下行 / shim），最外层套**服务端鉴权中间件**（见 withAuth）。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(publishPath, s.handlePublish)
	mux.HandleFunc(eventsPath, s.handleEvents)
	mux.HandleFunc(shimPath, s.handleShim)
	mux.HandleFunc(dirsPath, s.handleDirs)
	mux.HandleFunc("/", s.handleStatic)
	return s.withAuth(mux)
}

// withAuth 是 HTTP 面的**服务端鉴权中间件**（browser 形态 requireAuth；61 §4.6）：未认证时对
// **受保护面**（写操作 `/publish`、下行 `/events`、`/dirs`）返回 401 —— 令牌只从**连接层**读取
// （cookie `chonkpilot-token` → AuthCheck，与首屏 `authed` 判定同源），**不接受前端自报**（22 §1）。
//
// 豁免（**必须未认证可达，否则把自己锁死**）：
//   - 静态面 + shim：首屏 HTML 与静态资源须能加载以渲染**登录视图**（`injectBootstrap` 注入
//     `window.__ck.authed=false` 供前端分派登录 / 主视图），若 401 则登录页无从加载 = 锁死；
//   - `/publish` 的 `login-*` 上行：登录 / 注册 / 登出本身即鉴权入口。
//
// 未接线（`AuthCheck=nil`：desktop 形态不经本入口 / 未配置认证域）→ **不启用**：不因"无法校验"
// 而拒绝全部请求（与首屏 `authed` 恒 false 解耦，行为同改前）；desktop 形态不受影响。
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authCheck == nil || !s.protectedRequest(r) || s.authed(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			msgkeys.FieldOk:     false,
			msgkeys.FieldErrors: []string{"instance-unauthorized: 未认证（browser 形态要求登录）"},
		})
	})
}

// protectedRequest 判定请求是否落在**受鉴权保护**的 HTTP 面（未认证 → 401）：
// `/events`（SSE 下行）、`/dirs`（目录清单）、`/publish`（**除 login-* 上行**）。
// 静态面 / shim 面为豁免（见 withAuth 注释）。
func (s *Server) protectedRequest(r *http.Request) bool {
	switch r.URL.Path {
	case eventsPath, dirsPath:
		return true
	case publishPath:
		return !isLoginPublish(r)
	}
	return false
}

// isLoginPublish 判定 `/publish` body 的 `type` 是否认证域（`login-*`，未认证可达）。
// 只读取**前缀**探测（`type` 恒在 JSON 对象开头；login 载荷为小对象），再把「已读前缀 + 剩余流」
// **拼回 body**（下游 handlePublish 仍需完整解析，不得丢字节）。
func isLoginPublish(r *http.Request) bool {
	if r.Body == nil {
		return false
	}
	orig := r.Body
	prefix, err := io.ReadAll(io.LimitReader(orig, 64<<10))
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(prefix), orig), orig}
	if err != nil || len(prefix) == 0 {
		return false
	}
	var probe struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(prefix, &probe) != nil {
		return false
	}
	return isLoginTopic(probe.Type)
}

// handleDirs 是 native 目录选择器的**服务端等价面**（非 MQ HTTP 面）：
// 列出本 instance 允许的目录（work_dir 及其子目录，规模受限）→
// `{instance_id, work_dir, dirs:[{path,name,depth}]}`。
// 只读、只暴露本 instance 作用域；**不下发清单外路径**（work_dir 由服务端绑定，客户端不可改）。
func (s *Server) handleDirs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "GET required"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"instance_id": s.instanceID,
		"work_dir":    filepath.ToSlash(s.workDir),
		"dirs":        s.allowedDirs(),
	})
}

// handleShim 提供前端桥 shim（SSE → window.mq.emitRemote）。
func (s *Server) handleShim(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_, _ = w.Write(shimJS)
}

// handleEvents 是 SSE 下行：订阅期间把桥信封 `{type,payload,src}` 逐条推送。
// 每 20s 发一行注释保活（非消息面，不进 61）。
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "httpapi: streaming unsupported", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// 先登记客户端再发首帧：客户端收到 `: connected` 即保证已在广播表内
	// （测试/前端据此确认订阅已生效，避免首帧事件丢失的竞态）。
	ch := make(chan []byte, 64)
	s.addClient(ch)
	defer s.removeClient(ch)

	fmt.Fprintf(w, ": connected instance=%s\n\n", s.instanceID)
	fl.Flush()

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case b := <-ch:
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return
			}
			fl.Flush()
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

// handleStatic 提供静态面：**内嵌静态面（go:embed 进 server exe）为默认**，
// `--web-root` 显式指定时**覆盖**为外部目录（见 readStatic）；index.html 注入实例 id +
// shim 脚本（与 GUI main.go:423-432 同法：内联经典脚本先于模块脚本执行，早于任何前端 emit）。
//
// 两路径都带 `Cache-Control: no-cache, no-store, must-revalidate`（WebView2/浏览器持久缓存
// 旧页面 → 必须在响应头声明不缓存；见项目规则「静态文件服务」）。
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if s.webRoot == "" && s.webFS == nil {
		http.Error(w, "httpapi: 静态面未配置（内嵌静态面缺失；可用 --web-root=<前端 dist 目录> 覆盖）", http.StatusServiceUnavailable)
		return
	}
	rel := strings.TrimPrefix(filepath.ToSlash(r.URL.Path), "/")
	if rel == "" {
		rel = "index.html"
	}
	full, b, err := s.readStatic(rel)
	if err != nil {
		if errors.Is(err, errStaticBadPath) {
			http.Error(w, "bad path", http.StatusBadRequest) // 目录穿越防护
			return
		}
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Content-Type", contentTypeFor(full))
	if filepath.Base(full) == "index.html" {
		b = injectBootstrap(b, s.instanceID, browserRequireAuth, s.authed(r))
	}
	_, _ = w.Write(b)
}

// errStaticBadPath 静态面路径越界（目录穿越防护；调用方按 400 处理）。
var errStaticBadPath = errors.New("httpapi: bad static path")

// readStatic 读静态资源（rel = 相对 dist 根的路径，如 `assets/app.js`）：
//   - **webRoot 非空（--web-root 显式指定）→ 外部目录优先**（保留原目录穿越防护与 os.ReadFile 语义）；
//   - 否则读**内嵌静态面** webFS（browser 形态前端 go:embed 进 server exe）。
//
// 返回（用于 MIME 判定的路径, 内容字节, 错误）；越界 → errStaticBadPath，未命中 → fs 错误。
func (s *Server) readStatic(rel string) (string, []byte, error) {
	if s.webRoot != "" {
		root := filepath.Clean(s.webRoot)
		full := filepath.Join(root, filepath.FromSlash(rel))
		if full != root && !strings.HasPrefix(full, root+string(os.PathSeparator)) {
			return "", nil, errStaticBadPath
		}
		b, err := os.ReadFile(full)
		return full, b, err
	}
	// 内嵌 FS：path.Clean 归一（`../` 越界归一后落到根外 → 读不到 → 404，不越出 FS 根）
	clean := strings.TrimPrefix(path.Clean("/"+rel), "/")
	b, err := fs.ReadFile(s.webFS, clean)
	return clean, b, err
}

// injectBootstrap 在 index.html 的 <head> 后插入内联脚本（内联经典脚本同步执行 → 任何前端
// emit 之前已就绪）：
//
//   - `window.__chonkpilotInstanceId`（既有，兼容保留）：前端 mq.emit 据此给业务 payload 统一补
//     instance_id（61 §0 第 21/45 行）；
//   - `window.__ck`（61 §4.6 首屏注入）：`{form, requireAuth, authed, instanceId}`；
//   - shim 脚本（SSE → window.mq.emitRemote）。
//
// 注：**未认证时 `instanceId` 仍注入入口绑定值**（既有兼容值）—— 它是事件过滤 / `/show` URL /
// 前端 payload 注入的既有依赖，且**不构成凭据**（22 §2）；前端据 `requireAuth && !authed`
// 分派登录视图，**不据 instanceId 判身份**。
func injectBootstrap(html []byte, instanceID string, requireAuth, authed bool) []byte {
	tag := "<script>window.__chonkpilotInstanceId=" + strconv.Quote(instanceID) + ";" +
		"window.__ck=" + bootstrapJSON(instanceID, requireAuth, authed) + ";</script>" +
		"<script src=\"" + shimPath + "\"></script>"
	s := string(html)
	if i := strings.Index(s, "<head>"); i >= 0 {
		j := i + len("<head>")
		return []byte(s[:j] + tag + s[j:])
	}
	return []byte(tag + s)
}

// browserRequireAuth 是本入口的 requireAuth 取值（61 §4.6：**形态决定** —— browser 形态
// 要求认证；desktop 才免鉴权）。
const browserRequireAuth = true

// authed 判定当前连接是否已认证（61 §4.6：**服务端读凭证判定** —— 读 cookie `chonkpilot-token`
// 并校验其有效性，**绝不读 UA、不接受前端自报**）。未注入判定回调（AuthCheck=nil）→ 未认证。
func (s *Server) authed(r *http.Request) bool {
	if s.authCheck == nil {
		return false
	}
	return s.authCheck(cookieToken(r))
}

// bootstrapJSON 生成 `window.__ck` 字面量（61 §4.6：{form, requireAuth, authed, instanceId}）。
//
// 口径（**服务端判定，绝不读 UA** —— UA 客户端可伪造，仅可作日志诊断）：
//   - form = "browser"（本入口 = 浏览器形态；三形态 = desktop / gui / browser，
//     gui = GUI 客户端 + 独立 server）；
//   - requireAuth = browserRequireAuth（形态决定，见上）；
//   - authed = 本连接凭证有效性（读 cookie → AuthCheck；见 authed()）。
func bootstrapJSON(instanceID string, requireAuth, authed bool) string {
	b, err := json.Marshal(map[string]any{
		"form":        "browser",
		"requireAuth": requireAuth,
		"authed":      authed,
		"instanceId":  instanceID,
	})
	if err != nil {
		return "{}"
	}
	return string(b)
}

// ── 下行（总线事件 → SSE）──

// envelope 是前端事件信封（与桥 envelope 逐字段一致：payload 为 JSON 字符串）。
type envelope struct {
	Type    string `json:"type"`
	Payload string `json:"payload"`
	Source  string `json:"src"`
}

// forwardEvent 把总线事件转发到 SSE：跳过本实例自发布（防环）+ 按 instance 归属过滤 +
// 主题 → 前端 type 映射（与桥 mqTypeMap 同表）。
func (s *Server) forwardEvent(subject string, payload []byte) {
	if s.isSelfPublished(subject) {
		return
	}
	if !s.acceptEventInstance(eventInstanceID(payload)) {
		return
	}
	env, err := json.Marshal(envelope{Type: eventType(subject), Payload: string(payload), Source: "mq"})
	if err != nil {
		return
	}
	s.broadcast(env)
}

// acceptEventInstance 判定事件归属是否属本实例（判据与桥 bridge.go:181-186 一致）：
// 载荷无 instance_id 或本实例未标识 → 放行；否则要求完全相等。
func (s *Server) acceptEventInstance(evInstance string) bool {
	if s.instanceID == "" || evInstance == "" {
		return true
	}
	return evInstance == s.instanceID
}

// eventInstanceID 从事件载荷提取 `instance_id`（无字段/空串/非对象 → "" = 无归属）。
func eventInstanceID(payload []byte) string {
	var probe struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return ""
	}
	return probe.InstanceID
}

// mqTypeMap 相对主题 → 前端 type（与 chonkpilot-gui/bridge/bridge.go 的 mqTypeMap 同表；
// 未命中的相对主题原名直通 —— data-*/filesys.* 等点分主题本就是稳定前端 type）。
var mqTypeMap = map[string]string{
	msgkeys.TopicSessionReceive:   "llm-receive",
	msgkeys.TopicSessionComplete:  "llm-complete",
	msgkeys.TopicSessionCompress:  "llm-compress",
	msgkeys.TopicSessionAsk:       "ask-user",
	msgkeys.TopicSessionTurnStart: "turn-start",
	msgkeys.TopicTaskStarted:      "tasks.started",
	msgkeys.TopicTaskUpdated:      "tasks.updated",
	msgkeys.TopicTaskDone:         "tasks.done",
	msgkeys.TopicServerStarting:   msgkeys.TopicServerStarting,
	"server-status-changed":       "servers.status_changed",
	"tool-changed":                "tools.list_changed",
	msgkeys.TopicPromptOptimised:  msgkeys.TopicPromptOptimised,
	msgkeys.TopicInstanceRegister: msgkeys.TopicInstanceRegister,
	msgkeys.TopicInstanceHeartbeat: msgkeys.TopicInstanceHeartbeat,
	msgkeys.TopicInstanceExit:     msgkeys.TopicInstanceExit,
}

// eventType 取前端 type（显式映射优先；兜底原名直通）。
func eventType(subject string) string {
	if typ, ok := mqTypeMap[subject]; ok {
		return typ
	}
	return subject
}

// addClient / removeClient / broadcast 是 SSE 客户端注册与广播（广播非阻塞：
// 慢客户端丢帧，事件流不保证不丢，与 GUI 的 Eval 投递语义近似）。
func (s *Server) addClient(ch chan []byte) {
	s.mu.Lock()
	s.clients[ch] = struct{}{}
	s.mu.Unlock()
}

func (s *Server) removeClient(ch chan []byte) {
	s.mu.Lock()
	delete(s.clients, ch)
	s.mu.Unlock()
}

func (s *Server) broadcast(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.clients {
		select {
		case ch <- b:
		default:
		}
	}
}

// ── 发布（防环）──

// markPublished 记录一次本实例发布（发布前调用）。
func (s *Server) markPublished(subject string) {
	s.publishedMu.Lock()
	defer s.publishedMu.Unlock()
	s.published[subject] = time.Now()
}

// isSelfPublished 命中本实例刚发布的主题（1s 窗口，一次性消耗）→ 跳过转发。
func (s *Server) isSelfPublished(subject string) bool {
	s.publishedMu.Lock()
	defer s.publishedMu.Unlock()
	t, ok := s.published[subject]
	if !ok {
		return false
	}
	delete(s.published, subject)
	return time.Since(t) < time.Second
}

// publish 发一条事件（不等结果；用于 instance-register / instance-exit）。
func (s *Server) publish(subject string, payload any) {
	if s.bus == nil {
		return
	}
	s.markPublished(subject)
	if v := s.bus.Emit(context.Background(), subject, payload).Wait(); v.Err() != nil {
		log.Printf("[server] httpapi publish %s: %v", subject, v.Err())
	}
}

// publishV 发布并等待派发结果（promise 语义：订阅者写回的 Result + 收集的 Errors）。
func (s *Server) publishV(subject string, payload any) (result any, errs []error) {
	s.markPublished(subject)
	v := s.bus.Emit(context.Background(), subject, payload).Wait()
	return v.Result, v.Errors
}

// injectInstance 给载荷补 instance_id（幂等；与桥 injectInstance 同语义）。
// 2026-09-20（阶段 2b-1/2b-2）：同时补**入口从连接层取**的当前令牌（cookie）——
// **连接层为准**（无条件覆盖前端自报的同名字段；未持有 = 置空）—— 身份由入口承载、
// **不采信前端 payload 里的身份字段**（61 §4.6 · 22 §1）；前端不可见该字段的来源。
func (s *Server) injectInstance(payloadJSON string, token string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &m); err != nil {
		return payloadJSON
	}
	if _, ok := m["instance_id"]; !ok {
		m["instance_id"] = s.instanceID
	}
	m["token"] = token
	raw, err := json.Marshal(m)
	if err != nil {
		return payloadJSON
	}
	return string(raw)
}

// ── 工具 ──

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// contentTypeFor 按扩展名给静态资源 MIME。
func contentTypeFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "application/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	case ".wasm":
		return "application/wasm"
	case ".txt", ".md":
		return "text/plain; charset=utf-8"
	}
	return "application/octet-stream"
}

// isLoopback 判定监听地址是否仅本机（非本机 → 调用方记风险告警）。
func isLoopback(addr net.Addr) bool {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return false
	}
	return tcp.IP.IsLoopback()
}

// newUUID 生成 RFC 4122 v4 风格 UUID（不引入额外依赖；同 GUI bridge）。
func newUUID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("ins-%d", time.Now().UnixNano())
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}
