// chonkpilot-core-mcp-server —— 常驻工具前门（2026-10-19）
//
// 设计（用户定稿）：
//   - 由 chonkpilot-server 拉起并监管，常驻；只做「鉴权 + 路由 + 进程监管 + 限额 + 取消 + 审计」，
//     **不承载业务执行**；
//   - 对 gateway 暴露 MCP Streamable HTTP（端点 /mcp）；每次 tools/call 按契约 spawn
//     **一个** executor exe（一工具一 exe）；
//   - HTTP 头携带调用上下文 / 安全信息（X-Chonk-*），协议 _meta 作为兼容回落；
//   - 取消 = 该次 HTTP 请求 ctx 取消 → callTool 的 ctx 取消 → 只 kill 本次 executor 进程树，
//     **永不杀 core-mcp-server**，也不影响其它并发调用。
//
// 用法（由宿主注入）：
//
//	chonkpilot-core-mcp-server.exe --addr 127.0.0.1:0 --root <capability> \
//	  --work-dir <wd> --data-dir <dd> --ready-file <path> [--token <hex>] \
//	  [--config <config.json>] [--plugin-addr <uds|tcp://...>]
//
// 就绪后向 ready-file 原子写入 {"url":"http://127.0.0.1:<port>/mcp","token":"..."}。
//
// `--stdio`（GATEWAY-2 L5）：以 stdio MCP server 运行（stdin/stdout 只走 JSON-RPC 帧，
// 日志一律 stderr），用**同一** RegisterContracts 把契约根（tools/prompts/skills/resources）
// 暴露给接入方；gateway 的 capability 目录节点（用户级/项目级/prjusr）即经此形态接入，
// 替代已删除的 gateway 进程内 dir 节点。
package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/chonkpilot/chonkpilot-lib/exedir"
	mcpms "github.com/chonkpilot/chonkpilot-mcp-server/server"
)

const (
	headerInstance = "X-Chonk-Instance-Id"
	headerSession  = "X-Chonk-Session-Id"
	headerTurn     = "X-Chonk-Turn-Id"
	headerCall     = "X-Chonk-Call-Id"
	headerWorkDir  = "X-Chonk-Work-Dir"
	headerDataDir  = "X-Chonk-Data-Dir"
	headerPurpose  = "X-Chonk-Purpose"
	headerScope    = "X-Chonk-Scope"
	headerDeadline = "X-Chonk-Deadline-Ms"
	headerNoLimit  = "X-Chonk-No-Timeout"
)

type readyInfo struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

// internalExecReq 是 POST /internal/exec 的载荷（插件 → core 的内部执行委托）。
type internalExecReq struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"`
	// Context 与协议 _meta.chonkpilot 同字段名（instance/session/turn/tool_call_id/
	// work_dir/data_dir/purpose），便于两条链路共用解析口径。
	Context struct {
		InstanceID string `json:"instance_id"`
		Session    string `json:"session"`
		Turn       string `json:"turn"`
		ToolCallID string `json:"tool_call_id"`
		WorkDir    string `json:"work_dir"`
		DataDir    string `json:"data_dir"`
		Purpose    string `json:"purpose"`
	} `json:"context"`
	DeadlineMS int64 `json:"deadline_ms"`
	NoTimeout  bool  `json:"no_timeout"`
}

// resultText 提取 CallToolResult 的首段文本（与 executor 结果封装口径一致）。
func resultText(res *mcp.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok && tc.Text != "" {
			return tc.Text
		}
	}
	b, _ := json.Marshal(res.Content)
	return string(b)
}

func main() {
	var (
		addr        = flag.String("addr", "127.0.0.1:0", "监听地址（缺省随机端口）")
		root        = flag.String("root", "", "契约根（缺省 <exe目录>/capability）")
		workDir     = flag.String("work-dir", "", "缺省工作目录（头未给出时使用）")
		dataDir     = flag.String("data-dir", "", "缺省数据目录（头未给出时使用）")
		readyFile   = flag.String("ready-file", "", "就绪信息输出文件（JSON：url/token/pid）")
		tokenFlag   = flag.String("token", "", "Bearer token（空 = 随机生成并写入 ready-file）")
		configPath  = flag.String("config", "", "config json（默认自动探测 <root>/../config.json）")
		pluginAddr  = flag.String("plugin-addr", "", "插件桥地址（进程内 meta/domain 工具经 plugin/v1/* 取能力）")
		pluginToken = flag.String("plugin-token", "", "插件桥 HTTP token（进程内 meta/domain 工具使用）")
		stdio       = flag.Bool("stdio", false, "以 stdio MCP server 运行（stdin/stdout 仅 JSON-RPC；用于 capability 目录节点）")
	)
	flag.Parse()

	cfg := mcpms.DefaultConfig()
	if *root == "" {
		if dir, err := exedir.Dir(); err == nil {
			cfg.Root = filepath.Join(dir, "capability")
		} else {
			cfg.Root = "capability"
		}
	} else {
		cfg.Root = *root
	}
	// 配置加载：显式 --config 优先；否则探测 <root>/../config.json（dist 布局 = capability 同级）。
	// stdio 形态的 root = 用户/项目级 capability 目录，其上级未必是发行目录 → 不做探测。
	if *configPath == "" && !*stdio {
		probe := filepath.Join(filepath.Dir(cfg.Root), "config.json")
		if _, err := os.Stat(probe); err == nil {
			*configPath = probe
		}
	}
	if *configPath != "" {
		raw, err := os.ReadFile(*configPath)
		if err != nil {
			log.Fatalf("read config %s: %v", *configPath, err)
		}
		if err := cfg.Apply(raw); err != nil {
			log.Fatalf("parse config %s: %v", *configPath, err)
		}
		log.Printf("[core-mcp] config loaded: %s", *configPath)
	}

	cfg.SetBridge(*pluginAddr, *pluginToken)

	// stdio 形态（GATEWAY-2 L5）：同一 RegisterContracts 扫契约根（tools + prompts/skills/
	// resources 四原语），在 stdin/stdout 上跑 MCP；stdout 只允许 JSON-RPC 帧（log 恒走 stderr）。
	if *stdio {
		srv := mcp.NewServer(&mcp.Implementation{Name: "chonkpilot-core-mcp-server", Version: "1.0.0"}, nil)
		if err := mcpms.RegisterContractsWithTools(srv, cfg.Root, cfg); err != nil {
			log.Fatalf("register contracts: %v", err)
		}
		log.Printf("[core-mcp] stdio mode, root=%s", cfg.Root)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
			log.Fatalf("stdio serve: %v", err)
		}
		log.Printf("[core-mcp] stdio stopped")
		return
	}

	// 一工具一 executor：契约里每个 *.tool.md 的 runtime 就是独立的 exe。
	tools, err := mcpms.LoadExecTools(cfg.Root, cfg)
	if err != nil {
		log.Fatalf("load exec tools: %v", err)
	}
	// execTools = 全部可执行工具（含 internal：不暴露 MCP 工具面，仅供 /internal/exec）。
	execTools := make(map[string]*mcpms.ExecTool, len(tools))
	ms := mcp.NewServer(&mcp.Implementation{Name: "chonkpilot-core-mcp-server", Version: "1.0.0"}, nil)
	exposed := 0
	for _, et := range tools {
		tool := et
		execTools[tool.Name] = tool
		// internal=true：插件查询工具（codegraph_* / vfts_query）由插件按启用状态注册给
		// gateway，core 只负责执行 → **不进入 MCP tools/list**（否则未启用时也会出现）。
		if tool.Doc != nil && tool.Doc.Internal {
			continue
		}
		ms.AddTool(tool.Tool, makeHandler(tool))
		exposed++
	}
	log.Printf("[core-mcp] executors loaded: %d（exposed=%d，root=%s）", len(tools), exposed, cfg.Root)

	// 资产面（GATEWAY-2 L5）：core 同时是 gateway 的 prompts/skills/resources 来源 —— 用同一契约根
	// 注册原语（tools 已由 LoadExecTools 提供，SkipTools 跳过重复工具）。
	cfg.SkipTools = true
	if err := mcpms.RegisterContracts(ms, cfg.Root, cfg); err != nil {
		log.Printf("[core-mcp] register contracts(assets): %v", err)
	}

	// 鉴权：Bearer token（随机生成时写 ready-file；server 与 gateway 同进程读取）。
	token := strings.TrimSpace(*tokenFlag)
	if token == "" {
		var buf [32]byte
		if _, err := rand.Read(buf[:]); err != nil {
			log.Fatalf("generate token: %v", err)
		}
		token = hex.EncodeToString(buf[:])
	}

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, nil))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "tools": exposed, "executors": len(tools)})
	})
	// 运行期执行配置（2026-10-19）：server 在 usr/prj 配置热生效时推送快照。
	mux.HandleFunc("/internal/runtime", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var rc mcpms.RuntimeConfig
		if err := json.NewDecoder(r.Body).Decode(&rc); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		cfg.ApplyRuntime(rc)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	// 内部执行面（2026-10-19）：插件把 LLM 工具调用委托给 core —— 每次调用仍 spawn
	// **一个单工具 executor**（与 MCP tools/call 完全同一执行链路），取消 = 请求 ctx 取消。
	// 仅 internal=true 的工具（codegraph_* / vfts_query）经此调用；其余工具走 MCP。
	mux.HandleFunc("/internal/exec", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req internalExecReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		et := execTools[req.Tool]
		if et == nil {
			http.Error(w, "unknown executor: "+req.Tool, http.StatusNotFound)
			return
		}
		cx := mcpms.CallContext{
			InstanceID: req.Context.InstanceID,
			SessionID:  req.Context.Session,
			TurnID:     req.Context.Turn,
			ToolCallID: req.Context.ToolCallID,
			WorkDir:    req.Context.WorkDir,
			DataDir:    req.Context.DataDir,
			Purpose:    req.Context.Purpose,
			Present: req.Context.InstanceID != "" || req.Context.WorkDir != "" ||
				req.Context.DataDir != "" || req.Context.Purpose != "" ||
				req.Context.Session != "" || req.Context.Turn != "" || req.Context.ToolCallID != "",
		}
		if err := mcpms.ValidateCallContext(cx); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		callCtx := r.Context()
		if !req.NoTimeout && req.DeadlineMS > 0 {
			var cancel context.CancelFunc
			callCtx, cancel = context.WithTimeout(callCtx, time.Duration(req.DeadlineMS)*time.Millisecond)
			defer cancel()
		}
		args := req.Args
		if args == nil {
			args = map[string]any{}
		}
		res, err := et.Call(callCtx, args, cx)
		if err != nil {
			if callCtx.Err() != nil {
				http.Error(w, callCtx.Err().Error(), http.StatusRequestTimeout)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		text, isErr := resultText(res), res.IsError
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"text": text, "isError": isErr})
	})
	handler := withHeaders(withAuth(token, mux), *workDir, *dataDir)

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen %s: %v", *addr, err)
	}
	url := fmt.Sprintf("http://%s/mcp", ln.Addr().String())
	srv := &http.Server{Handler: handler}
	log.Printf("[core-mcp] listening %s", url)

	// executor 需要的桥地址/令牌：经 Config 注入 spawn 环境（executorEnv 会剔除宿主残留的
	// CHONKPILOT_*，故不能只依赖 os.Setenv）。
	cfg.SetBridge(*pluginAddr, *pluginToken)

	if *readyFile != "" {
		if err := writeReady(*readyFile, readyInfo{URL: url, Token: token, PID: os.Getpid()}); err != nil {
			log.Printf("[core-mcp] write ready file: %v", err)
		}
	}

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("serve: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Printf("[core-mcp] stopped")
}

// makeHandler 把 ExecTool 包成 MCP handler：解析 args + 合并 HTTP 头/_meta 上下文 → et.Call。
func makeHandler(et *mcpms.ExecTool) func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := map[string]any{}
		if req != nil && req.Params != nil && len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return nil, fmt.Errorf("tool %s: parse arguments: %w", et.Name, err)
			}
		}
		var meta map[string]any
		if req != nil && req.Params != nil {
			meta = req.Params.Meta
		}
		cx := mcpms.CallContextFromMeta(meta)
		h := mcpms.HTTPCallContextFrom(ctx)
		cx = mcpms.MergeCallContext(cx, h)
		if err := mcpms.ValidateCallContext(cx); err != nil {
			return nil, err
		}
		callCtx := ctx
		if !h.NoTimeout && h.DeadlineMS > 0 {
			var cancel context.CancelFunc
			callCtx, cancel = context.WithTimeout(ctx, time.Duration(h.DeadlineMS)*time.Millisecond)
			defer cancel()
		}
		return et.Call(callCtx, args, cx)
	}
}

// withAuth 校验 Bearer token（constant-time）；未授权 → 401。
func withAuth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withHeaders 把 X-Chonk-* 头解析进请求 ctx；核心只做归集，校验在执行器/上层。
func withHeaders(next http.Handler, workDir, dataDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := mcpms.HTTPCallContext{
			InstanceID: r.Header.Get(headerInstance),
			SessionID:  r.Header.Get(headerSession),
			TurnID:     r.Header.Get(headerTurn),
			CallID:     r.Header.Get(headerCall),
			WorkDir:    firstNonEmpty(r.Header.Get(headerWorkDir), workDir),
			DataDir:    firstNonEmpty(r.Header.Get(headerDataDir), dataDir),
			Purpose:    r.Header.Get(headerPurpose),
			Scope:      r.Header.Get(headerScope),
			DeadlineMS: mcpms.DeadlineFromHeader(r.Header.Get(headerDeadline)),
			NoTimeout:  headerTruthy(r.Header.Get(headerNoLimit)),
		}
		next.ServeHTTP(w, r.WithContext(mcpms.WithHTTPCallContext(r.Context(), h)))
	})
}

func headerTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func firstNonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

// writeReady 原子写就绪文件（先写 .tmp 再 rename），权限 0600。
func writeReady(path string, info readyInfo) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(info)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
