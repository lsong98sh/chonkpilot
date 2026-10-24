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
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/chonkpilot/chonkpilot-appwire"
	"github.com/chonkpilot/chonkpilot-lib/exedir"
	"github.com/chonkpilot/chonkpilot-lib/osuser"
	"github.com/chonkpilot/chonkpilot-lib/paths"
	"github.com/chonkpilot/chonkpilot-lib/procsingleton"
	"github.com/chonkpilot/chonkpilot-lib/winlog"
	"github.com/chonkpilot/chonkpilot-llm/codegraphindexer"
	"github.com/chonkpilot/chonkpilot-llm/coretools"
	"github.com/chonkpilot/chonkpilot-llm/llmbridge"
	"github.com/chonkpilot/chonkpilot-llm/pluginbridge"
	"github.com/chonkpilot/chonkpilot-llm/queuehooks"
	"github.com/chonkpilot/chonkpilot-llm/server"
	"github.com/chonkpilot/chonkpilot-llm/vftsindexer"
	mcpgateway "github.com/lsong98sh/chonkpilot-mcp-gateway/gateway"
)

// exitDuplicate 是「检测到已有 server 在运行 → 本进程为第二实例」的退出码（约定：非 0）。
const exitDuplicate = 2

func main() {
	// 日志落文件（🆕 2026-10-10）：本 exe 以 `-H windowsgui` 构建（GUI 子系统、无控制台，
	// stdout/stderr 不可见）→ 进程日志落 **exe 同目录 `logs/server.log`**（按大小滚动，见 winlog）；
	// 同一 sink 亦注入内嵌 llm server（`Options.LogWriter`），库侧诊断同落此文件（不入库，见 .gitignore）。
	logSink, closeLog := setupFileLogging()
	defer closeLog()

	// fatalf 致命退出**前**先收尾日志：log.Fatalf / os.Exit 会跳过 defer → 文件日志未 flush/close。
	fatalf := func(format string, args ...any) {
		log.Printf(format, args...)
		closeLog()
		os.Exit(1)
	}

	var (
		servers  = flag.String("servers-file", "", "servers.list 文件（空 = 仅内嵌 mcp-server）")
		llmBase  = flag.String("llm-base", "http://127.0.0.1:8901/v1", "OpenAI 兼容 base URL")
		llmModel = flag.String("llm-model", "mock", "默认 LLM 模型名")
		// PRODSIM-1：本地认证域（`--auth-db` / `--user-data-root` / `--allow-register` /
		// `--project-roots`）已整体移除 —— 认证将来统一到 registry。
		// 插件桥接入口（默认 UDS，PRODSIM-1）：路径 = 本机 UDS；`tcp://host:port` 走 TCP。
		// 能力外溢**统一走 UDS**（HTTP 前门已删除，见 PRODSIM-1 端口收敛）。
		pluginAddr = flag.String("plugin-addr", pluginbridge.DefaultAddr(), "插件桥接入口（默认 UDS；空 = 关闭；UDS 路径，或 tcp://127.0.0.1:port）")
		// PRODSIM-1（产物简化，2026-10-22）：**flash 专用子系统已删除** ——
		// `--autoexit` / `--client-exe` / `--tray` 三个参数、托盘、spawn flash、实例视图与
		// UDS 前门（原 flash 接入面）整体移除；server 只剩本机 LLM 能力面（插件桥 + LLM Bridge）。
		// LLM 桥接入口（BRIDGE-1，2026-10-16；BRIDGE-3，2026-10-24 改 **UDS-only**）：
		// 透明反向代理，只监听 UDS（默认 %LOCALAPPDATA%\chonkpilot\llm.sock，socket 0600 / 目录 0700，
		// 免 api-key）——本机脚本把 SDK 指向该 socket 的 /llm/<name>/... 即调用本机已配置 LLM。
		// `off` / `-` = 关闭；**无任何 TCP 监听**（原 127.0.0.1:5669 已删除）。
		// 见 docs/llm-bridge/LLM桥接端口设计方案.md。
		llmBridgeUDS = flag.String("llm-bridge-uds", "", "LLM 桥接入口 UDS 路径（默认 %LOCALAPPDATA%\\chonkpilot\\llm.sock；CHONK_LLM_UDS 覆盖；off = 关闭）")
	)
	flag.Parse()

	// PRODSIM-1：browser 形态（`--http-addr` / `--web-root` / `httpapi`）已删除 ——
	// 单产物只保留 desktop 语义（UDS 前门 + WebView2 窗口，本机免鉴权）。

	// ── 进程级单例（🆕 2026-10-10；19 §8.11 · 62 §3A）：server 只要存在都只有一份 ──
	// 第二份**立即退出**（exit code = exitDuplicate，非 0），**不得**触碰 / 覆盖既有 UDS
	// socket，**不得**启动第二份服务。判定必须在**任何资源申请（MQ / LLM server / HTTP /
	// UDS / DB）之前**（无副作用）。判据 = **server exe 同目录的文件锁**
	// （`<exe目录>/chonkpilot-server.lock`，见 procsingleton 包 + lockfile）——**不再**依赖
	// 「UDS bind 失败」（`uds.Server.Start()` 先 `os.Remove` 旧 socket 再 listen，Windows 下
	// 重复 bind **不失败**，旧口径不成立）；亦**废弃**原「命名互斥体（CreateMutexW）」判据。
	if rel, duplicate, err := procsingleton.Acquire(osuser.CurrentUser()); err != nil {
		// fail-closed（R7-B-8）：判定失败即**拒绝启动**并留痕——若按非单例继续，与
		// uds.Server.Start() 无条件 os.Remove(旧 socket) 组合，异常环境下第二实例
		// 可能删掉在跑实例的前门 socket。
		fatalf("[server] 单例判定失败，拒绝启动: %v", err)
	} else if duplicate {
		log.Printf("[server] 已有 chonkpilot-server 在运行 → 本进程为第二实例，退出（exit %d）", exitDuplicate)
		closeLog()
		os.Exit(exitDuplicate)
	} else {
		defer rel()
	}

	// 形态（61 §4.6；PRODSIM-1）：单产物 = desktop 语义 —— 本机单用户、免鉴权；
	// browser 形态（`--http-addr` / `httpapi`）与 UDS 前门（原 flash 接入面）均已删除。
	form := server.FormDesktop

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// work_dir/data_dir **不是 server 的启动参数**（🆕 2026-10-09；62 §3）：它们属**实例**，
	// 由客户端 `instance-claim` 提交、按实例隔离（一个 server 可服务多 work_dir）。此处仅保留
	// **cwd 回落**（`Options.WorkDir/DataDir` 为进程内编程选项，供 desktop 单体内嵌时使用）。
	cwd, _ := os.Getwd()
	wd := paths.ResolveDir(cwd, "")
	dd := filepath.Join(wd, ".chonkpilot")

	// 1. LLM 会话服务（内嵌 mcp-server + gateway + persist 数据服务，21-llm-server）
	// RB-2：servers.list 由**面层**（本 exe）读取解析后传 `GatewayServers`——gateway lib 不读「源」。
	var gwServers []mcpgateway.ServerEntry
	if *servers != "" {
		raw, rerr := os.ReadFile(*servers)
		if rerr != nil {
			fatalf("[server] read servers.list %s: %v", *servers, rerr)
		}
		entries, perr := mcpgateway.ParseServersList(raw)
		if perr != nil {
			fatalf("[server] parse servers.list %s: %v", *servers, perr)
		}
		gwServers = entries
	}
	// BRIDGE-2 T7 / G-68：启用插件入口（帧或 HTTP 任一）时，域/元工具由 core-mcp-server
	// 的一工具一 exe 承接；进程内不再注册（避免与 core 的工具面重复）。
	if *pluginAddr != "" {
		_ = os.Setenv("CHONKPILOT_EXTERNAL_TOOLS", "1")
	}

	// 0+1. 共用脊柱（appwire）：总线 → data 门面 → llm server + 默认插件。
	// 启动期输入快照（BootConfig）：只记环境输入（路径 / 旗标 / LLM 缺省 / 认证 / 池参数）；
	// **不复制 usr/prj 运行期配置** —— 由 llm/server 按实例从数据面读取并热生效（12-数据层）。
	specs := make([]appwire.ServerSpec, 0, len(gwServers))
	for _, e := range gwServers {
		specs = append(specs, appwire.ServerSpec{
			ID: e.ID, Name: e.Name, Category: e.Category, Transport: e.Transport, Enabled: e.Enabled,
		})
	}
	cfg := appwire.Snapshot(appwire.BootConfig{
		Form:           form,
		WorkDir:        wd,
		DataDir:        dd,
		ProjectDataDir: dd,
		LLMBase:        *llmBase,
		LLMModel:       *llmModel,
		Servers:        specs,
		Pool:           appwire.DefaultPool(),
	})
	app, err := appwire.Build(cfg, appwire.BuildOptions{GatewayServers: gwServers, LogWriter: logSink})
	if err != nil {
		fatalf("[server] appwire.Build: %v", err)
	}
	bus := app.Bus
	defer bus.Close()
	srv := app.Server
	if err := app.Start(ctx); err != nil {
		fatalf("[server] server.Start: %v", err)
	}
	log.Printf("[server] chonkpilot-server started: workdir=%s llm=%s/%s", wd, *llmBase, *llmModel)

	// ── 插件桥接入口（BRIDGE-2；默认关闭）──
	// 与 pluginbridge 的另一个入口并列：连接 = 一个插件身份；能力经 pluginbridge Registry 叠加。
	var pb *pluginbridge.Server
	var coreTools *coretools.Supervisor
	var vftsIdx *vftsindexer.Manager
	var cgIdx *codegraphindexer.Manager
	if *pluginAddr != "" {
		// data 门面（inline 同进程直调）：resident indexer 的待索引队列控制帧（P4b-1）
		// 经 queuehooks 适配器桥接到门面；与 filesys 入队同源（共用 appwire 装配的同一绑定）。
		pb = pluginbridge.New(pluginbridge.Options{
			Bus:     bus,
			Addr:    *pluginAddr,
			Version: "server",
			Form:    string(form),
			// plugin.index.take / ack / pending → data 门面待索引队列；
			// WorkDirs = 活实例 work_dir（plugin.index.workdirs，单实例多 workdir 项目发现）。
			IndexQueue: queuehooks.New(app.Facade),
			WorkDirs:   srv.LiveWorkDirs,
			Logf:       log.Printf,
		})
		if err := pb.Start(); err != nil {
			log.Printf("[server] 插件入口启动失败（可选子系统，继续运行）: %v", err)
			pb = nil
		} else {
			defer pb.Close()
			srv.SetFilters(pb.Filters())
			srv.SetPluginRegistry(pb.Registry())
			log.Printf("[server] plugin bridge: %s", pb.Addr())
			// 2026-10-19（G-68 第二步）：meta/domain 控制面工具已并入 core-mcp-server 的
			// 一工具一 exe 执行面（不再启动 chonkpilot-server-tools.exe）。
			// 2026-10-19：core-mcp-server = 工具唯一前门（一工具一 executor），由 server 拉起并监管；
			// gateway 以内置 HTTP 下游接入（namespace=self_ → 暴露名不变）。取代 self-tools 的
			// tools.register/tools.call 路径。启动失败不致命：工具面降级，核心功能不受影响。
			capRoot := ""
			if dir, derr := exedir.Dir(); derr == nil {
				capRoot = filepath.Join(dir, "capability")
			}
			if sup, err := coretools.Start(coretools.Options{
				Root:       capRoot,
				WorkDir:    wd,
				DataDir:    dd,
				PluginAddr: pb.Addr(),
				Output:     logSink,
				Logf:       func(format string, args ...any) { log.Printf(format, args...) },
			}); err != nil {
				log.Printf("[server] core-mcp-server 启动失败: %v", err)
			} else {
				coreTools = sup
				defer coreTools.Stop()
				// usr/prj 执行配置热生效 → 推送给 core（timeout/并发/解释器/Chrome/沙箱等）。
				srv.SetCoreRuntimePusher(sup.PushRuntime)
				// 插件查询工具（codegraph_* / vfts_query）执行委托 core：一工具一 exe，单次取消。
				srv.SetToolExec(coretools.NewExecClient(sup.URL(), sup.Token()))
				if rerr := srv.RegisterCoreServer(sup.URL(), sup.Token()); rerr != nil {
					log.Printf("[server] core-mcp-server 注册失败: %v", rerr)
				} else {
					log.Printf("[server] core-mcp-server started (url=%s root=%s)", sup.URL(), capRoot)
				}
			}
		}
	}

	// ── PRODSIM-1：HTTP+SSE 跨进程入口（browser 形态）已删除 ──
	// 单产物下 GUI 窗口与 server 同进程（进程内内存总线），无跨进程 HTTP 面；
	// 对外能力只保留插件桥（UDS）与 LLM Bridge（UDS）。

	// ── LLM 桥接入口（BRIDGE-3，2026-10-24：**只监听 UDS**）──
	// 透明反向代理：本机脚本把 SDK 指向 UDS（`/llm/<name>/...`）即可免 api-key 调本机已配置 LLM。
	// **无任何 TCP 监听**（5669 已删除）；需要 TCP 的调用方自建 loopback→UDS 字节代理。
	// 唯一边界 = socket 文件权限（目录 0700 / socket 0600）。零新增 MQ 主题。
	var bx *llmbridge.Server
	udsPath := strings.TrimSpace(*llmBridgeUDS)
	if udsPath == "" {
		udsPath = strings.TrimSpace(os.Getenv("CHONK_LLM_UDS"))
	}
	if udsPath == "" {
		udsPath = llmbridge.DefaultPath()
	}
	if udsPath != "off" && udsPath != "-" {
		bx = llmbridge.New(llmbridge.Options{
			Path: udsPath,
			// provider 解析 = llm server 的只读查询（**不 Register / 不 Call / 不经 canon**；
			// 只读 usr `llms` 配置：name → base_url / apiKey / protocol）。
			Resolve: func(name string) (llmbridge.Target, bool) {
				t, ok := srv.ResolveBridgeTarget(name)
				if !ok {
					return llmbridge.Target{}, false
				}
				return llmbridge.Target{BaseURL: t.BaseURL, APIKey: t.APIKey, Protocol: t.Protocol}, true
			},
		})
		if err := bx.Start(); err != nil {
			// 可选子系统：启动失败不致命（记录 + 继续），与 HTTP 入口 / UDS 前门口径一致。
			log.Printf("[server] LLM 桥接入口启动失败（可选子系统，继续运行）: %v", err)
			bx = nil
		} else {
			log.Printf("[server] LLM bridge entry: unix://%s (routes=/llm/<name>/...)", bx.Path())
		}
	}

	// PRODSIM-1（2026-10-22）：原「desktop 分体常驻版」子系统（系统托盘 / 自动退出 /
	// 拉起 flash / 实例视图 / UDS 前门）**已整体删除** —— flash 薄壳与独立 server 产物均不再存在；
	// 唯一产物 `chonkpilot.exe` 自带窗口/托盘/多项目实例（`src/lib/gui/host`）。

	// 全部模块就绪 → 插件桥广播 server-ready（常驻索引器据此开始消费队列；P3）。
	// 载荷为插件传输帧（非 MQ 主题），见 pluginbridge.MarkReady；pb 未启用时跳过。
	if pb != nil {
		pb.MarkReady(map[string]any{"form": "server", "at": time.Now().UTC().Format(time.RFC3339)})
	}

	// resident indexer（vfts P4b-2b / codegraph P5）：**每宿主进程每插件单实例**
	// （单实例多 workdir，2026-10-19）。项目发现由宿主实例表提供
	// （plugin.index.workdirs → srv.LiveWorkDirs），indexer 进程内含托盘输出窗口。
	// 不设 enable 门控（MVP 始终启动）；exe 缺失 → 静默跳过并只记一次日志。
	if pb != nil {
		logf := func(format string, args ...any) { log.Printf(format, args...) }
		vftsExe := vftsIndexerExePath()
		if st, err := os.Stat(vftsExe); err == nil && !st.IsDir() {
			vftsIdx = vftsindexer.New(logf)
			if err := vftsIdx.Ensure(context.Background(), vftsindexer.Options{
				DataRoot:   dd,
				PluginAddr: pb.Addr(),
				IndexerExe: vftsExe,
				Logf:       logf,
				Shutdown:   pb.ShutdownPlugin,
			}); err != nil {
				log.Printf("[server] vfts indexer 启动失败: %v", err)
			}
		} else {
			log.Printf("[server] vfts indexer 未安装（跳过）: %s", vftsExe)
		}
		cgExe := codegraphIndexerExePath()
		if st, err := os.Stat(cgExe); err == nil && !st.IsDir() {
			cgIdx = codegraphindexer.New(logf)
			if err := cgIdx.Ensure(context.Background(), codegraphindexer.Options{
				DataRoot:   dd,
				PluginAddr: pb.Addr(),
				IndexerExe: cgExe,
				Logf:       logf,
				Shutdown:   pb.ShutdownPlugin,
			}); err != nil {
				log.Printf("[server] codegraph indexer 启动失败: %v", err)
			}
		} else {
			log.Printf("[server] codegraph indexer 未安装（跳过）: %s", cgExe)
		}
	}

	<-ctx.Done()
	log.Printf("[server] shutting down ...")
	// 先停外部工具进程（其调用依赖 server / 插件桥），再按依赖序收尾。
	coreTools.Stop()
	if bx != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = bx.Shutdown(shutCtx)
		cancel()
	}
	// resident indexer（vfts / codegraph）：优雅停止（StopAll 经 plugin.shutdown 通知
	// indexer，依赖 pb 仍在线），插件桥由 deferred Close 稍后关闭。
	if vftsIdx != nil {
		vftsIdx.StopAll()
	}
	if cgIdx != nil {
		cgIdx.StopAll()
	}
	srv.Stop()
}

// setupFileLogging 把进程日志落文件（按大小滚动，见 winlog），并返回该 sink（供
// `server.Options.LogWriter` 注入内嵌库，库侧诊断同落此文件）。本 exe 以 `-H windowsgui`
// 构建（GUI 子系统、无控制台）→ stdout/stderr 不可见，落文件是可诊断性的唯一途径。
//
// 落点优先级（🆕 2026-10-11）：
//  1. **exe 同目录 `logs/server.log`**（首选；与 gui 形态 `logfile.go` 同口径）；
//  2. exe 目录不可写时**回落 `%LOCALAPPDATA%\chonkpilot\logs\server.log`**（仍可诊断，
//     避免「exe 装在 Program Files 等只读位置 → 完全无日志」）；
//  3. 两处皆不可写 → 返回 (nil, no-op)，日志回落标准库默认（stderr；GUI 子系统下不可见）。
//
// 任何一步失败均**不阻断启动**（落点在 `.gitignore` 覆盖范围内，不入库）。
func setupFileLogging() (io.Writer, func()) {
	if dir, err := exedir.Dir(); err != nil {
		log.Printf("[server] 定位 exe 目录失败: %v", err)
	} else if fw, ferr := openServerLog(filepath.Join(dir, "logs", "server.log")); ferr != nil {
		log.Printf("[server] exe 目录日志不可用: %v", ferr)
	} else {
		return fw, func() { _ = fw.Close() }
	}
	// 回落本地目录（%LOCALAPPDATA%）
	path := fallbackLogPath()
	if path == "" {
		log.Printf("[server] 无 %%LOCALAPPDATA%%，日志回落 stderr（GUI 子系统下不可见）")
		return nil, func() {}
	}
	fw, err := openServerLog(path)
	if err != nil {
		log.Printf("[server] 回落日志文件 %s 不可用: %v（日志回落 stderr，GUI 子系统下不可见）", path, err)
		return nil, func() {}
	}
	log.Printf("[server] 日志回落本地目录: %s", path)
	return fw, func() { _ = fw.Close() }
}

// openServerLog 打开滚动日志文件并把标准库 log 输出切到该文件；返回写入器（调用方负责 Close）。
func openServerLog(path string) (*winlog.FileWriter, error) {
	fw, err := winlog.NewFileWriter(path, winlog.DefaultMaxBytes, winlog.DefaultMaxFiles)
	if err != nil {
		return nil, err
	}
	log.SetOutput(fw)
	log.Printf("[server] 日志落点: %s", path)
	return fw, nil
}

// fallbackLogPath 返回回落日志路径 `%LOCALAPPDATA%\chonkpilot\logs\server.log`（无本地
// AppData 目录 → ""）。`%LOCALAPPDATA%` 缺失时再用 os.UserCacheDir()（Windows 下同值）兜底。
func fallbackLogPath() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		if d, err := os.UserCacheDir(); err == nil {
			base = d
		}
	}
	if base == "" {
		return ""
	}
	return filepath.Join(base, "chonkpilot", "logs", "server.log")
}

// vftsIndexerExePath 定位 resident indexer：优先 server exe 同目录，
// 其次发行布局 `mcps/vfts/`（build-desktop / build-gui 的投放位置）。
func vftsIndexerExePath() string {
	const name = "chonkpilot-vfts-indexer.exe"
	exe, err := os.Executable()
	if err != nil {
		return name
	}
	dir := filepath.Dir(exe)
	primary := filepath.Join(dir, name)
	if _, err := os.Stat(primary); err == nil {
		return primary
	}
	return filepath.Join(dir, "mcps", "vfts", name)
}

// codegraphIndexerExePath 定位 resident codegraph indexer：优先 server exe 同目录，
// 其次发行布局 `mcps/codebase/`（build-desktop / build-gui 的投放位置）。
func codegraphIndexerExePath() string {
	const name = "chonkpilot-codegraph-indexer.exe"
	exe, err := os.Executable()
	if err != nil {
		return name
	}
	dir := filepath.Dir(exe)
	primary := filepath.Join(dir, name)
	if _, err := os.Stat(primary); err == nil {
		return primary
	}
	return filepath.Join(dir, "mcps", "codebase", name)
}
