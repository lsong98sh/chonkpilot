// chonkpilot-server：LLM 会话服务（多实例，对齐 21-llm-server）。
//
//	单例常驻；mq.Bus 门面（进程内内存 MQ，2026-09-03 去 NATS，见 61-消息一览 §9）；
//	内嵌 mcp-server + mcp-gateway lib（server.Options，21-llm-server）；
//	会话持久化走 chonkpilot-data prj 库。
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/paths"
	"github.com/chonkpilot/chonkpilot-llm/httpapi"
	"github.com/chonkpilot/chonkpilot-llm/server"
	mcpgateway "github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
	"github.com/chonkpilot/chonkpilot-plugin"
	"github.com/chonkpilot/chonkpilot-plugin-codegraph"
	"github.com/chonkpilot/chonkpilot-plugin-compress"
	"github.com/chonkpilot/chonkpilot-plugin-history"
	"github.com/chonkpilot/chonkpilot-plugin-memory"
	"github.com/chonkpilot/chonkpilot-plugin-vfts"
)

func main() {
	var (
		workDir  = flag.String("work-dir", "", "工作目录（默认 cwd）")
		dataDir  = flag.String("data-dir", "", "项目数据根（默认 <work-dir>/.chonkpilot）")
		servers  = flag.String("servers-file", "", "servers.list 文件（空 = 仅内嵌 mcp-server）")
		manage   = flag.String("manage-addr", "", "gateway 管理 REST 地址（可选）")
		llmBase  = flag.String("llm-base", "http://127.0.0.1:8901/v1", "OpenAI 兼容 base URL")
		llmModel = flag.String("llm-model", "mock", "默认 LLM 模型名")
		// HTTP + SSE 跨进程入口（browser 形态最小切片 D；**默认关闭**，19 §8.5）。
		httpAddr = flag.String("http-addr", "", "HTTP+SSE 入口监听地址（空 = 关闭；裸端口如 5668 = 127.0.0.1:5668；绑定非本机地址须显式写全，必须自备 HTTPS 并限制暴露面）")
		webRoot  = flag.String("web-root", "", "HTTP 入口静态面根目录（前端 dist；**显式指定 = 覆盖**内嵌静态面；空 = 用内嵌面，见 frontend.go）")
		// 认证域（61-消息一览 §4.6；阶段 2b-1）：auth 库 / 用户数据根 / 是否允许自助注册。
		authDB   = flag.String("auth-db", "", "auth 库路径（bbolt 单文件，与业务三级库分离；空 = <exe 目录>/auth.db）")
		userData = flag.String("user-data-root", "", "用户数据根（注册建 <root>/<uid>/；空 = <exe 目录>/data）")
		allowReg = flag.Bool("allow-register", true, "是否允许自助注册（配置 auth.allowRegister；对外暴露必须关闭）")
		// auth.projectRoots（阶段 2b-2）：允许根列表（逗号分隔；空 = 缺省 = 用户数据根）。
		// 用户选定的 work_dir 落在某允许根之下且未登记为项目 → 视为用户自建项目（见 claim.go）。
		projRoots = flag.String("project-roots", "", "允许的项目根列表（逗号分隔；空 = 用户数据根；配置 auth.projectRoots）")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	wd := paths.ResolveDir(*workDir, "")
	if wd == "" {
		cwd, _ := os.Getwd()
		wd = paths.ResolveDir(cwd, "")
	}
	dd := paths.ResolveDir(*dataDir, wd)
	if dd == "" {
		dd = filepath.Join(wd, ".chonkpilot")
	}
	root := paths.ResolveDir(*webRoot, wd)

	// 0. 消息总线（进程内内存 MQ，2026-09-03 去 NATS；命名空间前缀 chonk. 在此注入一次，
	// 业务 publish/subscribe 一律写相对主题，见 61-消息一览 §0.1）
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		log.Fatalf("[server] mq.New: %v", err)
	}
	defer bus.Close()

	// 1. LLM 会话服务（内嵌 mcp-server + gateway + persist 数据服务，21-llm-server）
	// RB-2：servers.list 由**面层**（本 exe）读取解析后传 `GatewayServers`——gateway lib 不读「源」。
	var gwServers []mcpgateway.ServerEntry
	if *servers != "" {
		raw, err := os.ReadFile(*servers)
		if err != nil {
			log.Fatalf("[server] read servers.list %s: %v", *servers, err)
		}
		entries, err := mcpgateway.ParseServersList(raw)
		if err != nil {
			log.Fatalf("[server] parse servers.list %s: %v", *servers, err)
		}
		gwServers = entries
	}
	srv := server.New(bus, server.Options{
		LLMBase:           *llmBase,
		LLMModel:          *llmModel,
		GatewayServers:    gwServers,
		GatewayManageAddr: *manage,
		// 服务端启动参数下发（唯一消费点 = instance-claim 的 desktop 回落，见 61 §4.1 ①）
		WorkDir: wd,
		DataDir: dd,
		// 运行形态（61 §4.6；阶段 2b-2）：本 exe 的客户端入口 = httpapi（浏览器）→
		// **browser**（形态由服务端判定，非 UA、非客户端自报）→ claim 要求认证。
		Form: server.FormBrowser,
		// 认证域（61 §4.6；阶段 2b-1）：auth 库（与业务三级库分离的单文件）+ 用户数据根 +
		// 自助注册开关（auth.allowRegister；对外暴露必须关闭）。
		AuthDBPath:    *authDB,
		UserDataRoot:  *userData,
		AllowRegister: allowReg,
		// auth.projectRoots（阶段 2b-2）：允许根列表（空 = 缺省 = 用户数据根）。
		ProjectRoots: splitList(*projRoots),
		// 内嵌插件：compress（llm-compress 压缩）+ memory（每轮异步沉淀记忆）+ history（git 快照）+ codegraph/vfts（索引工具面，默认关闭）；启动就绪后广播 server-starting
		// 插件依赖的门面绑定在**装配处**选择（23 §7）：本形态 = inline（同进程直调；bus 传宿主
		// 实际总线 → config 域写入的变更广播照旧送达订阅方，见 facade/inline）。
		Plugins: []plugin.Hook{
			compress.New(compress.DefaultOptions(), inline.New(bus)),
			memory.New(memory.DefaultOptions()),
			history.New(),
			codegraph.New(codegraph.Options{}),
			vfts.New(vfts.Options{}),
		},
	})
	if err := srv.Start(ctx); err != nil {
		log.Fatalf("[server] server.Start: %v", err)
	}
	log.Printf("[server] chonkpilot-server started: workdir=%s llm=%s/%s", wd, *llmBase, *llmModel)

	// ── HTTP + SSE 跨进程入口（browser 形态最小切片 D；**默认关闭**）──
	// 开关 = --http-addr（空 = 不监听）；单 instance 由 --work-dir/--data-dir 派生（经
	// instance-register 下发数据面）；native 能力在入口侧明确禁用（19 §3.3/§6）。
	var hx *httpapi.Server
	if *httpAddr != "" {
		hx = httpapi.New(bus, httpapi.Options{
			Addr: *httpAddr,
			// 静态面（2026-09-21 用户拍板）：**内嵌面为默认**（go:embed frontend/dist，
			// 见 frontend.go），`--web-root` 显式指定时**覆盖**为外部目录（root 空 = 用内嵌）。
			WebRoot: root,
			WebFS:   webFS(),
			WorkDir: wd,
			DataDir: dd,
			// 首屏 `authed` 的判定回调 = llm server 的令牌校验（61 §4.6：服务端读 cookie
			// 判定，不读 UA、不接受前端自报；阶段 2b-2）。
			AuthCheck: srv.Authenticated,
			// data 门面绑定（阶段 4 第三批 / 41 G-34）：`data-session-*` 上行优先走门面
			// （服务端进程内直调，不经 MQ），未命中回落总线 persist 路径。绑定在**装配处**
			// 选择（23 §7）：本形态 = inline（bus 传宿主实际总线 → 变更广播照旧送达订阅方）。
			Facade: inline.New(bus),
		})
		if err := hx.Start(); err != nil {
			log.Fatalf("[server] httpapi.Start: %v", err)
		}
		log.Printf("[server] HTTP+SSE entry: http://%s (instance=%s static=%s)", hx.Addr(), hx.InstanceID(), staticSource(root))
	}

	<-ctx.Done()
	log.Printf("[server] shutting down ...")
	if hx != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = hx.Shutdown(shutCtx)
		cancel()
	}
	srv.Stop()
}

// staticSource 描述静态面来源（日志用）：`--web-root` 显式指定 → 外部目录路径；
// 空 → 内嵌静态面（go:embed frontend/dist，见 frontend.go）。
func staticSource(root string) string {
	if root == "" {
		return "<embedded frontend/dist>"
	}
	return root
}

// splitList 把逗号分隔的启动参数拆成去空白、去空项的非空列表（空入参 → nil =
// 「未配置」语义，各消费点自行取缺省，如 `auth.projectRoots` 缺省 = 用户数据根）。
func splitList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
