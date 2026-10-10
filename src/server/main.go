// chonkpilot-server：LLM 会话服务（多实例，对齐 21-llm-server）。
//
//	单例常驻；mq.Bus 门面（进程内内存 MQ，2026-09-03 去 NATS，见 61-消息一览 §9）；
//	内嵌 mcp-server + mcp-gateway lib（server.Options，21-llm-server）；
//	会话持久化走 chonkpilot-data prj 库。
package main

import (
	"context"
	"flag"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-lib/exedir"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/chonkpilot/chonkpilot-lib/osuser"
	"github.com/chonkpilot/chonkpilot-lib/paths"
	"github.com/chonkpilot/chonkpilot-lib/procsingleton"
	"github.com/chonkpilot/chonkpilot-lib/uds"
	"github.com/chonkpilot/chonkpilot-lib/winlog"
	"github.com/chonkpilot/chonkpilot-llm/httpapi"
	"github.com/chonkpilot/chonkpilot-llm/server"
	"github.com/chonkpilot/chonkpilot-llm/udsapi"
	mcpgateway "github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
	"github.com/chonkpilot/chonkpilot-plugin"
	"github.com/chonkpilot/chonkpilot-plugin-codegraph"
	"github.com/chonkpilot/chonkpilot-plugin-compress"
	"github.com/chonkpilot/chonkpilot-plugin-history"
	"github.com/chonkpilot/chonkpilot-plugin-memory"
	"github.com/chonkpilot/chonkpilot-plugin-vfts"

	"github.com/chonkpilot/chonkpilot-plugin/instance"
)

// exitDuplicate 是「检测到已有 server 在运行 → 本进程为第二实例」的退出码（约定：非 0）。
const exitDuplicate = 2

func main() {
	// 日志落文件（🆕 2026-10-10）：本 exe 以 `-H windowsgui` 构建（GUI 子系统、无控制台，
	// stdout/stderr 不可见）→ 进程日志落 **exe 同目录 `logs/server.log`**（按大小滚动，见 winlog）；
	// 同一 sink 亦注入内嵌 llm server（`Options.LogWriter`），库侧诊断同落此文件（不入库，见 .gitignore）。
	logSink, closeLog := setupFileLogging()
	defer closeLog()

	var (
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
		// 🆕 2026-10-10 desktop 分体常驻版（flash + server；61 §4.1 ⑥⑦ · 62 §3A）：
		// 均为**可选能力**，非 flash 专属；server 无 `--desktop-server` / 无 `form=flash`
		//（免鉴权由**前门**决定；UDS 前门 = 本机信任）。
		autoExit  = flag.Bool("autoexit", false, "最后一个实例被删除后自动退出（未指定 = 永久常驻；仅 flash 拉起时传）")
		clientExe = flag.String("client-exe", "", "flash 可执行文件路径（托盘双击无实例时拉起；空 = 同目录 chonkpilot-flash.exe）")
		// 托盘**默认开**（🆕 2026-10-10）：本 exe 无控制台（-H windowsgui），系统托盘是唯一交互入口；
		// `--tray=false` 可显式关闭（如 browser 形态的无头部署）。
		trayEnable = flag.Bool("tray", true, "启用系统托盘（默认开；--tray=false 关闭）——双击=打开最近实例 / 右键=退出·打开新窗口·列举实例")
	)
	flag.Parse()

	// ── 进程级单例（🆕 2026-10-10；19 §8.11 · 62 §3A）：server 只要存在都只有一份 ──
	// 第二份**立即退出**（exit code = exitDuplicate，非 0），**不得**触碰 / 覆盖既有 UDS
	// socket，**不得**启动第二份服务。判定必须在**任何资源申请（MQ / LLM server / HTTP /
	// UDS / DB）之前**（无副作用）。判据 = Windows **命名互斥体**（见 procsingleton 包）——
	// **不再**依赖「UDS bind 失败」：`uds.Server.Start()` 先 `os.Remove` 旧 socket 再 listen，
	// Windows 下重复 bind **不失败**，旧口径不成立。
	if rel, duplicate, err := procsingleton.Acquire(osuser.CurrentUser()); err != nil {
		// fail-closed（R7-B-8）：判定失败即**拒绝启动**并留痕——若按非单例继续，与
		// uds.Server.Start() 无条件 os.Remove(旧 socket) 组合，异常环境下第二实例
		// 可能删掉在跑实例的前门 socket。
		log.Fatalf("[server] 单例判定失败，拒绝启动: %v", err)
	} else if duplicate {
		log.Printf("[server] 已有 chonkpilot-server 在运行 → 本进程为第二实例，退出（exit %d）", exitDuplicate)
		os.Exit(exitDuplicate)
	} else {
		defer rel()
	}

	// 形态（61 §4.6）：本 exe 的客户端入口若为 httpapi → browser（要求认证）；UDS 前门另见。
	form := server.FormBrowser

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// work_dir/data_dir **不是 server 的启动参数**（🆕 2026-10-09；62 §3）：它们属**实例**，
	// 由客户端 `instance-claim` 提交、按实例隔离（一个 server 可服务多 work_dir）。此处仅保留
	// **cwd 回落**（`Options.WorkDir/DataDir` 为进程内编程选项，供 desktop 单体内嵌时使用）。
	cwd, _ := os.Getwd()
	wd := paths.ResolveDir(cwd, "")
	dd := filepath.Join(wd, ".chonkpilot")
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
		// 库侧诊断额外 sink（🆕 2026-10-10）：内嵌 llm server（插件日志经 plugin.Deps.Logf 同此出口）
		// 与宿主同落 `logs/server.log`；nil = 仅 stdout（GUI 子系统下不可见），见 log.go。
		LogWriter: logSink,
		// 服务端启动参数下发（唯一消费点 = instance-claim 的 desktop 回落，见 61 §4.1 ①）
		WorkDir: wd,
		DataDir: dd,
		// 运行形态（61 §4.6；阶段 2b-2）：本 exe 的客户端入口 = httpapi。默认 browser（形态由
		// 服务端判定，非 UA、非客户端自报）→ claim 要求认证；desktop 分体常驻版 = flash（免鉴权）。
		Form: form,
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
			// 「允许根」取值回调 = llm server 的 `AllowedWorkRoots`（auth.projectRoots；
			// 缺省用户数据根，默认值解析单一来源在 llm server 侧）：供 browser 形态
			// `gui.show-dialog` 的 options 根 + `gui.dir.open` 归属校验（19 §C）。
			AllowedRoots: srv.AllowedWorkRoots,
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

	// ── desktop 分体常驻版：系统托盘 + 自动退出 + 拉起 flash（🆕 2026-10-09；61 §4.1 ⑥⑦ · 62 §3A）──
	// 托盘驻留：双击=打开最近实例（无则 spawn flash）；右键=退出/打开新窗口/列举实例（子窗口实例不列）。
	// --autoexit：最后一个实例被删除（连接断开 ∪ 显式 exit）后自动退出；未指定则永久常驻。
	// 注：托盘 / spawn / autoexit 均为**可选能力**，非 flash 专属。
	// 实例视图（UDS 前门与托盘共用同一份）：订阅 instance-register/-heartbeat/-exit，
	// 维护 instance → {work_dir, data_dir, user, parent_instance, ...}。
	im := instance.New(bus)
	if err := im.Start(); err != nil {
		log.Printf("[server] instance view start: %v", err)
	} else {
		defer im.Stop()
	}

	// UDS 前门（🆕 2026-10-09；62 §3A：**恒开、无 CLI 参数**；路径 = 约定值 uds.DefaultPath()）：
	// flash 薄壳经**本机 UDS**接入（本机信任 → 免鉴权），**instance ↔ 连接 1:1**，断连即退出。
	// 接点见 docs/spec/10-architecture/19-多租户服务端形态.md §8.11。
	gate := udsapi.New(udsapi.Options{
		Bus: bus,
		InstanceOf: func(id string) (string, string, bool) {
			if in, ok := im.Lookup(id); ok {
				return in.WorkDir, in.User, true
			}
			return "", "", false
		},
		TopByDataDir: func(dataDir string) (string, string, bool) {
			if in, ok := im.TopLevelByDataDir(dataDir); ok {
				return in.ID, in.User, true
			}
			return "", "", false
		},
		Logf: func(format string, args ...any) { log.Printf(format, args...) },
	})
	if err := gate.Start(); err != nil {
		// UDS 前门 bind 失败 = **异常告警**，**不作单例依据**（单例已由 procsingleton 在启动
		// 最先判定）。注：`uds.Server.Start()` 先 `os.Remove` 旧 socket 再 listen → Windows 下
		// 重复 bind **不失败**，故 bind 结果**不能**推断「已有 server 在跑」。
		log.Printf("[server] UDS 前门启动失败: %v", err)
	} else {
		log.Printf("[server] UDS entry: %s", uds.DefaultPath())
		defer gate.Stop()
	}

	if *trayEnable {
		clientPath := *clientExe
		spawnClient := func() {
			p := clientPath
			if p == "" {
				if exe, err := os.Executable(); err == nil {
					p = filepath.Join(filepath.Dir(exe), "chonkpilot-flash.exe")
				}
			}
			if p == "" {
				log.Printf("[server] spawn flash：未定位 flash exe（可经 --client-exe 指定）")
				return
			}
			// flash 自行决定打开哪个项目（最近关闭 → 次近 → 选目录，见 62 §3A），故不传 --work-dir。
			cmd := exec.Command(p)
			if err := cmd.Start(); err != nil {
				log.Printf("[server] spawn flash %s: %v", p, err)
			} else {
				// 不托管子进程（flash 独立常驻）：释放句柄，免 Wait/finalizer 延迟回收。
				_ = cmd.Process.Release()
			}
		}
		// bringToTop 定向投递 `instance-goto-top`（无 payload，连接即定位）——
		// 经 UDS 前门直达目标实例连接（61 §4.1 ⑦）。
		bringToTop := func(id string) {
			if !gate.SendControl(id, msgkeys.TopicInstanceGotoTop, nil) {
				log.Printf("[server] instance-goto-top → %s（无对应 UDS 连接）", id)
			}
		}
		// trayInstances 取托盘可见实例：**顶层 + 当前用户 + 持 UDS 前门连接（= flash 形态）**，
		// **排除 browser 形态**（browser 走 httpapi、无 UDS 连接）。收录规则见
		// instance.MenuInstances（61 §4.1 ⑥ · 62 §3A）。托盘双击/菜单共用同一份，口径一致。
		trayInstances := func() []instance.Info {
			return instance.MenuInstances(im.ListTopLevel(), osuser.CurrentUser(), gate.Connected)
		}
		stopTray, err := startTray(TrayCallbacks{
			OnDoubleClick: func() {
				if id := mostRecentInstanceID(trayInstances()); id != "" {
					bringToTop(id)
					return
				}
				spawnClient()
			},
			OnNewWindow: spawnClient,
			OnExit:      stop,
			ListInstances: func() []TrayInstance {
				list := trayInstances()
				out := make([]TrayInstance, 0, len(list))
				for _, in := range list {
					out = append(out, TrayInstance{ID: in.ID, Title: instanceMenuTitle(in)})
				}
				return out
			},
			OnPickInstance: bringToTop,
		})
		if err != nil {
			log.Printf("[server] tray unavailable: %v", err)
		} else {
			defer stopTray()
		}
	}
	if *autoExit && im != nil {
		go autoExitLoop(ctx, im, stop)
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

// setupFileLogging 把进程日志落 **exe 同目录 `logs/server.log`**（按大小滚动，见 winlog），
// 并返回该 sink（供 `server.Options.LogWriter` 注入内嵌库，库侧诊断同落此文件）。本 exe 以
// `-H windowsgui` 构建（GUI 子系统、无控制台）→ stdout/stderr 不可见，落文件是可诊断性的
// 唯一途径（与 gui 形态 `logfile.go` 同口径；落点在 `.gitignore` 覆盖范围内，不入库）。
// 定位 exe 目录 / 打开文件失败 → 返回 (nil, no-op)，日志回落标准库默认（stderr），**不阻断启动**。
func setupFileLogging() (io.Writer, func()) {
	dir, err := exedir.Dir()
	if err != nil {
		log.Printf("[server] 定位 exe 目录失败，日志不落文件: %v", err)
		return nil, func() {}
	}
	path := filepath.Join(dir, "logs", "server.log")
	fw, err := winlog.NewFileWriter(path, winlog.DefaultMaxBytes, winlog.DefaultMaxFiles)
	if err != nil {
		log.Printf("[server] 打开日志文件 %s 失败，日志不落文件: %v", path, err)
		return nil, func() {}
	}
	log.SetOutput(fw)
	log.Printf("[server] 日志落点: %s", path)
	return fw, func() { _ = fw.Close() }
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

// mostRecentInstanceID 返回**最近使用**的托盘可见实例 id（LastBeat 最新；无 → ""）。
// 入参 = 已按托盘收录规则过滤的列表（顶层 + 当前用户 + flash）——见 trayInstances /
// instance.MenuInstances（61 §4.1 ⑥ · 62 §3A）。托盘双击「打开最近实例」用。
func mostRecentInstanceID(list []instance.Info) string {
	var best instance.Info
	ok := false
	for _, in := range list {
		if !ok || in.LastBeat.After(best.LastBeat) {
			best, ok = in, true
		}
	}
	if ok {
		return best.ID
	}
	return ""
}

// instanceMenuTitle 生成托盘「列举实例」菜单项标题（work_dir 目录名；空则回落实例 id）。
func instanceMenuTitle(in instance.Info) string {
	name := filepath.Base(filepath.FromSlash(strings.TrimSpace(in.WorkDir)))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return in.ID
	}
	return name
}

// autoExitLoop（--autoexit；61 §4.1 ⑤）：每分钟检测一次，无在线实例且**曾有实例** → 停服务
// （自动退出）。未指定 --autoexit 时不启动本循环 → 永久常驻。超时实例的剔除由服务端
// sweep 与 persist 清理完成（**与 --autoexit 无关，恒生效**）；flash（UDS 前门）实例
// 不参与按 LastBeat 的 sweep 回收。
func autoExitLoop(ctx context.Context, im *instance.Manager, stop func()) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	ever := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if im.Count() > 0 {
				ever = true
				continue
			}
			if ever {
				log.Printf("[server] --autoexit：无在线实例 → 自动退出")
				stop()
				return
			}
		}
	}
}
