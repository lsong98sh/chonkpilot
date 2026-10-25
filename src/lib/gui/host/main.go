//go:build windows

// Package host 是 ChonkPilot 的**单体装配宿主**（唯一产物 `chonkpilot.exe` 的启动装配）。
//
// 拆包背景（2026-10-10）：原 `chonkpilot-gui` 包同时含 (a) 客户端/窗口能力与 (b) 单体装配
// （内嵌 llm/server + filesys + 全部 plugin-* + gateway）。现将**装配**下沉到本包 `host`：
//
//   - 依赖方向：`host` → `gui` **单向**；`gui` **不 import `host`**。
//   - `gui` 保留客户端能力（窗口工厂 / 桥 / appHandler / registry / bridge / testserver /
//     日志 / 日志级别 / profile 清理等），并以导出 API（见 gui 的 hostapi.go）供本包单向调用。
//
// 本包入口 = Main（调用方 = 壳 `src/desktop`；PRODSIM-1 后唯一壳）。
package host

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-appwire"
	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-filesys"
	"github.com/chonkpilot/chonkpilot-gui"
	"github.com/chonkpilot/chonkpilot-gui/bridge"
	"github.com/chonkpilot/chonkpilot-gui/internal/assetserver"
	"github.com/chonkpilot/chonkpilot-gui/models"
	"github.com/chonkpilot/chonkpilot-lib/exedir"
	"github.com/chonkpilot/chonkpilot-lib/tray"
	"github.com/chonkpilot/chonkpilot-llm/codegraphindexer"
	"github.com/chonkpilot/chonkpilot-llm/coretools"
	"github.com/chonkpilot/chonkpilot-llm/llmbridge"
	"github.com/chonkpilot/chonkpilot-llm/queuehooks"
	"github.com/chonkpilot/chonkpilot-llm/server"
	"github.com/chonkpilot/chonkpilot-llm/vftsindexer"
	pluginbridge "github.com/chonkpilot/chonkpilot-lib/pluginhost/bridge"
	pluginmgr "github.com/chonkpilot/chonkpilot-lib/pluginhost/mgr"
)

// Main 是单体装配宿主入口（webview2 宿主 + 内嵌 server/persist/filesys + 插件装配 + 桥接线）。
//
// **调用方 = 壳**（`src/desktop` = 桌面单体 / `src/gui` = GUI 客户端）：壳只做两件事 ——
// `//go:embed all:frontend/dist` 拿到前端 dist，并以 `opts.Form` 声明形态；装配实现唯一一份。
//
// distFS 必须是含 `frontend/dist` 前缀的嵌入文件系统（壳的 `//go:embed all:frontend/dist`），
// 内部 `fs.Sub(distFS, "frontend/dist")` 取 dist 根。启动参数（--work-dir/--data-dir/
// --bridge-url/--test-port/--llm-base/--llm-model/--no-server）仍在本函数内解析。
func Main(distFS fs.FS, opts gui.Options) {
	// 日志级别：先以缺省级别装好 logger（早于配置可用无妨），待桥/数据面就绪后经既有配置
	// 通道读 prj logLevel 覆盖并订阅变更即时生效（见 gui 的 loglevel.go）。
	gui.InitLogging()
	// 主流程返回 error（**不再中途 os.Exit**，D-08）：保证已注册的收尾 defer 链按依赖序执行，
	// 且失败经原生消息框可见（windowsgui 下 stderr 不可见，D-06）。
	if err := runMain(distFS, opts); err != nil {
		slog.Error("gui main failed", "err", err)
		gui.NativeAlert("ChonkPilot", err.Error())
		os.Exit(1)
	}
}

// runMain 是装配宿主主流程（Main 的实体）；任何失败以 error 返回，由 Main 统一弹框 + 退出。
func runMain(distFS fs.FS, opts gui.Options) error {
	gui.MarkStartupBegin()

	var workDir, dataDir, bridgeURL string
	var legacyNatsURL string
	var testPort int
	var llmBase, llmModel string
	var noServer bool
	var uiAssetsAddr string
	flag.StringVar(&workDir, "work-dir", "", "工作目录（前端文件树根）")
	flag.StringVar(&dataDir, "data-dir", "", "数据根（prj 与 users/ 的共同根；默认 <workDir>/.chonkpilot；显式给出时 prj/prjusr 同根）")
	flag.StringVar(&bridgeURL, "bridge-url", "", "分离形态（-tags split 编译）下 bridge 对端地址（HTTP）；合并单进程形态（默认构建）忽略")
	// 兼容旧名（已废弃，勿在新脚本中使用）：nats-url 语义已演进为 bridge-url，
	// 仅在未显式给出 --bridge-url 时生效，避免既有脚本/参数失效。
	flag.StringVar(&legacyNatsURL, "nats-url", "", "已废弃别名：等价于 --bridge-url（仅 --bridge-url 未给出时生效）")
	// --test-port 是**调试构建专属**开关（-tags debugport，见 gui.DebugPortEnabled）：
	// release 构建下不注册本 flag（传入即 unknown flag），testPort 恒 0 → 下方 OnWired 自然跳过接线。
	if gui.DebugPortEnabled {
		flag.IntVar(&testPort, "test-port", 0, "测试通道端口（0=禁用；>0 监听 127.0.0.1 供外部脚本驱动；仅 -tags debugport 构建可用）")
	}
	flag.StringVar(&llmBase, "llm-base", "http://127.0.0.1:8901/v1", "OpenAI 兼容 base URL（inprocess 会话服务）")
	flag.StringVar(&llmModel, "llm-model", "mock", "默认 LLM 模型名（inprocess 会话服务）")
	flag.BoolVar(&noServer, "no-server", false, "不启动 inprocess 会话服务（UI 注入测试模式：注入事件完全控制回合，无真实 LLM）")
	// 只读静态资产端口（UIBRIDGE-1）：第三方页面 include 公共前端资产用；`off` / 空 = 关闭。
	// 仅 loopback（非 loopback 拒启）、只接受 GET/HEAD、路径硬白名单、无任何消息/动态接口。
	flag.StringVar(&uiAssetsAddr, "ui-assets-addr", assetserver.DefaultAddr, "只读静态资产端口（/themes.css 与 /shared/<枚举文件>；off/空 = 关闭；仅 loopback）")
	flag.Parse()
	// 环境变量覆盖（优先级高于 flag 默认值；与 `CHONK_LLM_UDS` 的「env 可覆盖缺省」同款）：
	// 仅在命令行**未显式**给出 --ui-assets-addr 时生效。
	if !flagSet("ui-assets-addr") {
		if v := strings.TrimSpace(os.Getenv("CHONK_UI_ASSETS_ADDR")); v != "" {
			uiAssetsAddr = v
		}
	}
	if bridgeURL == "" {
		bridgeURL = legacyNatsURL
	}

	if workDir == "" {
		workDir = defaultWorkDir()
		if workDir == "" {
			return fmt.Errorf("无法确定工作目录")
		}
	}
	workDir = models.ResolveDir(workDir, "")
	if dataDir != "" {
		models.SetDataDir(models.ResolveDir(dataDir, workDir))
	}
	// 项目数据根（prj / prjusr 层所在：缺省 <workDir>/.chonkpilot；显式 --data-dir → 该目录）。
	projectDataDir := models.DataDir(workDir)
	// PRODSIM-1（产物简化）：**项目级单实例锁已删除** —— 唯一进程由 `procsingleton` 保证，
	// 同项目只能开一个主窗口由进程内 `work_dir → 主窗口` 映射表保证，无需跨进程文件锁。
	// 传给 server / 桥的 data_dir（**数据层分支判据**；12-数据层 §3 · §5.3）：
	//   - 显式 --data-dir 恒生效（prj 与 prjusr 同根，兼容既有脚本/L2 用例）；
	//   - desktop 缺省（未给 --data-dir）**置空** → 数据层走 users/ 分支 → 个人运行态
	//     （会话/任务树/快照）落 `<workDir>/.chonkpilot/users/<OS 用户名>/`（DATAROOT-1）。
	//     ⚠️ 不可再传 `models.DataDir(workDir)`：该访问器**恒非空**（缺省 = 项目数据根），
	//     会让 prjusr 与 prj 同库（个人运行态落进项目目录）。
	//   - gui 形态缺省：保持项目数据根（客户端薄壳；服务端另按用户数据根落库，本轨不改）。
	dataDirArg := projectDataDir
	if dataDir == "" && opts.Form == gui.FormDesktop {
		dataDirArg = ""
	}
	gui.LogStartupStage("参数与目录解析")
	// prjusr 数据根（个人运行态非库文件的落盘根：日志 logs/、附件/截图 tmp/uploads/、
	// fileserver 白名单；[24 §3.2] MW-8）。DATAROOT-1 后由 `<workDir>/.chonkpilot/users/<OS 用户名>/`
	// 直接算出（无需再读 prj 库取 project-id；也不可拿 models.DataDir(workDir) 当 prjusr 根）。
	prjUsrRoot, err := data.PrjUsrDir(workDir, dataDirArg)
	if err != nil {
		// 数据根初始化失败：返回 error 由 Main 弹原生消息框（windowsgui 下 stderr 不可见，D-06）。
		return fmt.Errorf("prjusr 数据根解析失败: %w", err)
	}
	gui.LogStartupStage("prjusr 数据根解析（含 prj 库打开）")
	// GUI 文件日志（可诊断性，2026-09-19）：windowsgui 下无控制台、stderr 不可见 → 在 prjusr
	// 数据根 `logs/` 挂滚动文件 sink（<prjUsrRoot>/logs/gui.log，按大小滚动）。失败降级为
	// 「仅 stderr」，不阻断启动。日志级别仍由 prj `logLevel` 运行时控制（同一 LevelVar）。
	logDir, logErr := gui.AttachFileLog(prjUsrRoot)
	if logErr != nil {
		slog.Warn("file log attach failed", "err", logErr)
	} else {
		defer gui.DetachFileLog() // 退出时关闭文件 sink（幂等）
		slog.Info("file log enabled", "dir", logDir)
	}
	gui.LogStartupStage("文件日志挂载")

	// 附件/截图留存目录容量兜底清扫（D-45，启动期一次）：正常使用（< 2 GiB）永不触发，
	// 仅目录无界累积/洪泛时按 mtime 最旧优先清理（口径见 bridge.SweepUploads 注释）。
	bridge.SweepUploads(prjUsrRoot)

	// 消息总线：命名空间前缀 chonk. 在此注入一次（server/gateway/persist/filesys/bridge
	// 共享同一总线），业务 publish/subscribe 一律写相对主题，见 61-消息一览 §0.1。
	// 收尾依赖序（D-05）：ts.Shutdown → srv.Stop → fsys.Stop → bus.Close。
	// 需要按**依赖序**（而非 defer LIFO 的注册逆序）停止，且早期 return err 亦须执行收尾（D-08）：
	// 先声明 fsys/srv/ts 供收尾闭包引用，随后注册**单一**收尾 defer；无论正常收尾还是中途
	// return err，都在 runMain 返回时按序停止（nil 守卫自动跳过未创建者）。
	var fsys *filesys.Filesys
	var srv *server.Server
	var ts *gui.TestServer
	var pb *pluginbridge.Server
	var plugins *pluginmgr.Manager
	var bx *llmbridge.Server
	var coreTools *coretools.Supervisor
	var vftsIdx *vftsindexer.Manager
	var cgIdx *codegraphindexer.Manager
	var assetSrv *assetserver.Server

	// ── 共用脊柱（appwire，GATEWAY-2 L12）：总线 → data 门面 → filesys → llm server + 默认插件 ──
	// 启动期输入快照（BootConfig）：只记环境输入（路径 / 旗标 / LLM 缺省 / 认证 / 池参数）；
	// **不复制运行期配置** —— usr/prj 由 llm/server 按实例从数据面读取并热生效（12-数据层）。
	capRoot := ""
	if dir, derr := exedir.Dir(); derr == nil {
		capRoot = filepath.Join(dir, "capability")
	}
	// 一工具一 exe：桌面单体由 core-mcp-server 注册/执行 app、meta、domain 工具（server 内不再注册）。
	if !noServer && opts.Form == gui.FormDesktop {
		_ = os.Setenv("CHONKPILOT_EXTERNAL_TOOLS", "1")
	}
	app, err := appwire.Build(appwire.Snapshot(appwire.BootConfig{
		Form:           opts.Form,
		WorkDir:        workDir,
		DataDir:        dataDirArg,
		ProjectDataDir: projectDataDir,
		CapabilityRoot: capRoot,
		LLMBase:        llmBase,
		LLMModel:       llmModel,
		NoServer:       noServer,
		Filesys:        true,
		Pool:           appwire.DefaultPool(),
		EnvKeys:        []string{"CHONK_PLUGIN_LISTEN", "CHONK_LLM_UDS"},
	}), appwire.BuildOptions{LogWriter: gui.LogFileSink()})
	if err != nil {
		return err
	}
	bus := app.Bus
	fsys = app.Filesys
	srv = app.Server
	dataFacade := app.Facade
	defer func() {
		// core-mcp-server 依赖插件桥与 server：先停（断连即退出），再按依赖序收尾。
		coreTools.Stop()
		// 只读静态资产端口（UIBRIDGE-1）：独立子系统（无总线 / 无数据依赖），直接关监听。
		if assetSrv != nil {
			shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = assetSrv.Shutdown(shutCtx)
			cancel()
		}
		if ts != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = ts.Shutdown(ctx)
			cancel()
		}
		// LLM 桥接入口（BRIDGE-1 步骤 8）：依赖 srv 的只读 provider 解析 → 先于 srv 停止。
		if bx != nil {
			shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = bx.Shutdown(shutCtx)
			cancel()
		}
		if srv != nil {
			srv.Stop()
		}
		if fsys != nil {
			fsys.Stop()
		}
		// resident indexer（vfts / codegraph）：优雅停止（StopAll 经 plugin.shutdown 通知
		// indexer，依赖 pb 仍在线），最后才关插件桥。
		if vftsIdx != nil {
			vftsIdx.StopAll()
		}
		if cgIdx != nil {
			cgIdx.StopAll()
		}
		// 插件平台：先停插件（依赖插件桥在线发 shutdown），再关插件桥。
		if plugins != nil {
			plugins.Stop()
		}
		if pb != nil {
			_ = pb.Close()
		}
		_ = bus.Close()
	}()
	gui.LogStartupStage("MQ 总线 + filesys 文件服务 + server 装配（appwire 共用脊柱）")

	// ── inprocess 会话服务（GUI 内嵌 chonkpilot-server lib，同进程内存 MQ）──
	// server v2 内嵌 persist（data-* 数据面应答）+ gateway + mcp-server，数据根由
	// bridge.Start 的 instance-register 绑定（persist 自持实例视图，61-消息一览 §4.1），
	// 宿主不再直接打开/持有数据层句柄。
	// -no-server：跳过（UI 注入测试模式，注入事件完全控制回合，无真实 LLM/数据服务）。
	if srv != nil {
		gui.LogStartupStage("server 装配（capability 契约扫描 + gateway 构建）")
		// 服务端启动参数（WorkDir/DataDir/Form/LogWriter/Plugins）已在 appwire.Build 内装配；
		// data_dir = `dataDirArg`（desktop 缺省为空 → persist/instance 绑定 data_dir 空 →
		// prjusr 落 `<workDir>/.chonkpilot/users/<OS 用户名>/`，见 12-数据层 §5.1 / [24 §3.2] MW-7）。
		if err := app.Start(context.Background()); err != nil {
			return fmt.Errorf("server start: %w", err)
		}
		gui.LogStartupStage("server 启动（数据服务/gateway/能力注册/工具缓存预热/插件）")

		// 插件桥接入口（BRIDGE-2）：桌面单体默认开启；gui 客户端不在此进程监听（由 server 提供）。
		if opts.Form == gui.FormDesktop {
			pb = pluginbridge.New(pluginbridge.Options{
				Bus:     bus,
				Version: "desktop",
				Form:    string(opts.Form),
				// 监听地址覆盖（测试 / 多实例隔离）：tcp://127.0.0.1:PORT 或 UDS 路径；
				// 缺省 = pluginbridge.DefaultAddr()（UDS；`CHONK_PLUGIN_UDS` 可覆盖路径）。
				Addr: os.Getenv("CHONK_PLUGIN_LISTEN"),
				// PRODSIM-1：插件桥只保留 **UDS 免认证前门**（HTTP 前门已删除）。
				// resident indexer 的待索引队列控制帧（plugin.index.*）→ data 门面直调
				// （P4b-1；与 filesys 入队共用同一 inline 绑定）；WorkDirs = 活实例 work_dir
				// （plugin.index.workdirs，单实例多 workdir 的项目发现）。
				IndexQueue: queuehooks.New(dataFacade),
				WorkDirs: func() []string {
					if srv == nil {
						return nil
					}
					return srv.LiveWorkDirs()
				},
				Logf: func(format string, args ...any) { slog.Info(fmt.Sprintf(format, args...)) },
			})
			if err := pb.Start(); err != nil {
				slog.Warn("plugin bridge 启动失败（可选子系统，继续运行）", "err", err)
				pb = nil
			} else {
				srv.SetFilters(pb.Filters())
				srv.SetPluginRegistry(pb.Registry())
				slog.Info("plugin bridge started", "addr", pb.Addr())
				// 2026-10-19（G-68 第二步）：meta/domain 控制面工具已并入 core-mcp-server 的
				// 一工具一 exe 执行面（不再启动 chonkpilot-server-tools.exe）。
				// 2026-10-19：core-mcp-server = 工具唯一前门（一工具一 executor），由宿主拉起并监管；
				// gateway 以内置 HTTP 下游接入（namespace=self_ → 暴露名不变）。取代 self-tools 的
				// tools.register/tools.call 路径。启动失败不致命：工具面降级，核心功能不受影响。
				if sup, err := coretools.Start(coretools.Options{
					Root:       capRoot,
					WorkDir:    workDir,
					DataDir:    dataDirArg,
					PluginAddr: pb.Addr(),
					Output:     gui.LogFileSink(),
					Logf:       func(format string, args ...any) { slog.Warn(fmt.Sprintf(format, args...)) },
				}); err != nil {
					slog.Warn("core-mcp-server 启动失败", "err", err)
				} else {
					coreTools = sup
					// usr/prj 执行配置热生效 → 推送给 core（timeout/并发/解释器/Chrome/沙箱等）。
					srv.SetCoreRuntimePusher(sup.PushRuntime)
					// 插件查询工具（codegraph_* / vfts_query）执行委托 core：一工具一 exe，单次取消。
					srv.SetToolExec(coretools.NewExecClient(sup.URL(), sup.Token()))
					if rerr := srv.RegisterCoreServer(sup.URL(), sup.Token()); rerr != nil {
						slog.Warn("core-mcp-server 注册失败", "err", rerr)
					} else {
						slog.Info("core-mcp-server started", "url", sup.URL(), "root", capRoot)
					}
				}
			}
		}

		// ── 插件平台（PLUGIN-1）：manifest 插件发现 / 拉起 / 守护 / MCP+UI 注册 ──
		// 依赖插件桥（UDS）与 core 工具面；启动失败不致命（平台降级，核心功能不受影响）。
		if pb != nil && srv != nil {
			pluginDataDir := strings.TrimSpace(dataDirArg)
			if pluginDataDir == "" {
				base := os.Getenv("LOCALAPPDATA")
				if base == "" {
					base = os.TempDir()
				}
				pluginDataDir = filepath.Join(base, "chonkpilot")
			}
			userCap := ""
			if home, err := os.UserHomeDir(); err == nil {
				userCap = filepath.Join(home, ".chonkpilot", "capability")
			}
			roots := []string{
				filepath.Join(prjUsrRoot, "capability"),
				filepath.Join(workDir, ".chonkpilot", "capability"),
				userCap,
				capRoot,
			}
			// D-71：插件启动清单固定在 exe 同目录的 plugin.ini；缺失时回落 enabled.json。
			bootFile := ""
			if exePath, err := os.Executable(); err == nil {
				bootFile = filepath.Join(filepath.Dir(exePath), "plugin.ini")
			}
			plugins = pluginmgr.New(pluginmgr.Options{
				Bus:            bus,
				DataDir:        pluginDataDir,
				Roots:          roots,
				BootstrapFile:  bootFile,
				ConfigStore:    openPluginConfigStore,
				RegisterMCP:    srv.RegisterPluginServer,
				UnregisterMCP:  srv.UnregisterPluginServer,
				ShutdownPlugin: pb.ShutdownPlugin,
				RequestPlugin:  pb.RequestPlugin,
				Logf:           func(format string, args ...any) { slog.Warn(fmt.Sprintf(format, args...)) },
			})
			pb.SetPluginHooks(plugins.Hooks())
			if err := plugins.Start(context.Background()); err != nil {
				slog.Warn("插件平台启动失败", "err", err)
			} else {
				slog.Info("插件平台已启动", "data", pluginDataDir)
			}
		}

		// ── LLM 桥接入口（BRIDGE-3，2026-10-24）：桌面单体同开，**只监听 UDS** ──
		// UDS 路径：`CHONK_LLM_UDS` 覆盖，缺省 `%LOCALAPPDATA%\chonkpilot\llm.sock`，`off`/`-` 关闭。
		// 透明反向代理：本机脚本把 SDK 指向 UDS（route `/llm/<name>/...`）即可免 api-key 调用。
		// 唯一边界 = socket 文件权限（目录 0700 / socket 0600）。启动失败不致命。
		if opts.Form == gui.FormDesktop {
			udsPath := strings.TrimSpace(os.Getenv("CHONK_LLM_UDS"))
			if udsPath == "" {
				udsPath = llmbridge.DefaultPath()
			}
			if udsPath != "off" && udsPath != "-" {
				bx = llmbridge.New(llmbridge.Options{
					Path: udsPath,
					// provider 解析 = llm server 只读查询（不 Register / 不 Call / 不经 canon）。
					Resolve: func(name string) (llmbridge.Target, bool) {
						t, ok := srv.ResolveBridgeTarget(name)
						if !ok {
							return llmbridge.Target{}, false
						}
						return llmbridge.Target{BaseURL: t.BaseURL, APIKey: t.APIKey, Protocol: t.Protocol}, true
					},
				})
				if err := bx.Start(); err != nil {
					slog.Warn("LLM 桥接入口启动失败（可选子系统，继续运行）", "err", err)
					bx = nil
				} else {
					slog.Info("LLM bridge started", "uds", bx.Path())
				}
			}
		}
	}

	// WebView2 用户数据目录必须**一实例一份**：fork 默认 DataPath = %AppData%\<exe名>，
	// 会让同一 exe 的多窗口共享同一 profile → 共享同一个浏览器进程（msedgewebview2.exe），
	// 任一方销毁/崩溃都会波及另一方的窗口（历史实测：销毁一个实例会冻结其他窗口）。
	// 多窗口起（24 §4.1）：**每窗口**一份（~/.chonkpilot/webview2/<instance_id>，由窗口工厂
	// 创建并清理）；前端 ui.locale 有 DB 兜底（MainLayout），不依赖 WebView2 缓存留存。
	wvRoot := gui.WebviewProfilesRoot()
	gui.PruneWebviewProfiles(wvRoot) // 清掉超期残留 profile（>24h 未变动的 gui-* 目录；强杀/崩溃未走退出清理的）

	subFS, err := fs.Sub(distFS, "frontend/dist")
	if err != nil {
		return fmt.Errorf("fs.Sub dist: %w", err)
	}

	// ── 只读静态资产端口（UIBRIDGE-1，2026-10-23）──
	// 第三方页面 / 外部工具 include 公共前端资产（`/themes.css`、`/shared/<枚举文件>`）的只读
	// 入口：仅 GET/HEAD + 路径硬白名单 + Host 校验 + no-store，**不承载任何消息面 / 动态接口**
	// （能力面一律走既有 UDS `pluginbridge` 的 `plugin/v1/*`），也不复活插件 HTTP 前门。
	// 仅 loopback（非 loopback 由 assetserver 拒启）；`off` / 空 = 不启动。与窗口同生命周期；
	// 启动失败（端口占用 / 拒启）**不阻断主程序** —— 宿主自身的同源路由 `/themes.css` 与
	// `/shared/*` 由 appHandler 照常提供。
	assetExeDir, _ := exedir.Dir()
	if asrv, aerr := assetserver.New(assetserver.Options{
		Addr:   uiAssetsAddr,
		DistFS: subFS, // `/shared/*` 只从 GUI 前端 dist（embed）取，禁止磁盘任意路径
		ExeDir: assetExeDir,
	}); aerr != nil {
		slog.Warn("静态资产端口未启动（可选子系统，继续运行）", "addr", uiAssetsAddr, "err", aerr)
	} else if asrv != nil {
		if aerr := asrv.Start(); aerr != nil {
			slog.Warn("静态资产端口启动失败（可选子系统，继续运行）", "addr", uiAssetsAddr, "err", aerr)
		} else {
			assetSrv = asrv
			slog.Info("UI assets server started", "addr", asrv.Addr())
		}
	}

	// 进程级装配上下文：窗口工厂据此**每窗口**一份桥 + appHandler（MW-1/MW-2/MW-3）。
	// ⚠️ Server 传 **nil 接口值**（noServer 时 srv 为 typed-nil，须显式转 nil 接口），
	// 否则 gui 侧 `env.srv != nil` 判定失真（--no-server 下会误接认证门面）。
	var envServer gui.Server
	if srv != nil {
		envServer = srv
	}
	env := gui.NewEnv(gui.EnvConfig{
		Bus:            bus,
		DistFS:         subFS, // embed 路径带 frontend/dist 前缀，Sub 后根即 dist 内容
		WorkDir:        workDir,
		DataDir:        dataDirArg, // 数据层分支判据（desktop 缺省为空，见上）
		ProjectDataDir: projectDataDir,
		PrjUsrRoot:     prjUsrRoot,
		LogDir:         logDir,
		Form:           opts.Form,
		Server:         envServer,
		// --no-server：本地无数据服务 → 桥 data-* 快速失败（不等 3s 超时，首屏提速）。
		DataOffline: noServer,
		// 插件自持 webui（PLUGIN-UI-1）：`GET /plugins/<id>/<path>` → UDS
		// `plugin/v1/ui.asset.get` → 插件 embed 资源；缓存按「版本 + 重启代次」失效。
		PluginAsset: func(ctx context.Context, id, path string) ([]byte, bool, error) {
			if plugins == nil {
				return nil, false, errors.New("插件平台未启动")
			}
			return plugins.UIAsset(ctx, id, path)
		},
		PluginAssetVersion: func(id string) string {
			if plugins == nil {
				return ""
			}
			return plugins.UIAssetVersion(id)
		},
		// 插件 UI 动作面（PLUGIN-UI-1 第二段）：`POST /plugins/<id>/api/<action>`
		// → 复用通用 RPC（`plugin.ui.rpc`）透传给已就绪插件。
		PluginAction: func(ctx context.Context, id, action string, payload map[string]any) (any, error) {
			if plugins == nil {
				return nil, errors.New("插件平台未启动")
			}
			return plugins.UIAction(ctx, id, action, payload)
		},
	})

	// 插件桥 GUI 反向通道 / 实例列表：窗口宿主就绪后注入。
	if pb != nil {
		pb.SetGUI(gui.NewPluginGUIController(env))
		pb.SetExistingInstanceIDs(env.InstanceIDs)
	}

	// 进程退出信号：托盘「退出」与测试通道 `POST /quit` 共用（同一收尾路径）。
	exitCh := make(chan struct{})
	var exitOnce sync.Once
	requestExit := func() { exitOnce.Do(func() { close(exitCh) }) }

	// 项目主窗口（PRODSIM-1：单进程多项目，统一走 OpenProject —— 同 work_dir 唯一）。
	// 测试通道（--test-port）：仅绑进程内**第一个**主窗口。
	onProjectWired := func(h *gui.Window) error {
		// 日志级别接线：数据面就绪后读 prj logLevel 初值（变更订阅见下方 WatchLogLevel）。
		gui.ApplyLogLevelFromConfig(h.Bridge().PrjConfigList())
		if testPort <= 0 || env.HasTestServer() {
			return nil
		}
		t, err := env.AttachTestServer(h, testPort)
		if err != nil {
			return err
		}
		// 测试通道优雅退出（`POST /quit`）：与托盘「退出」逐条一致 —— 关所有项目窗口
		// （各自发 instance-exit）→ 强制销毁日志窗口 → 结束进程。systest 收尾走这里，
		// 而不是 taskkill /F（后者不摘托盘图标 → 每次套件残留一个系统托盘图标）。
		t.SetQuit(func() {
			env.Shutdown()
			env.CloseLogWindow()
			requestExit()
		})
		ts = t
		return nil
	}
	// openProject 是同进程「打开项目」的统一入口（托盘 / gui.dir.open 复用）。
	openProject := func(dir string) error {
		if st, statErr := os.Stat(dir); statErr != nil || !st.IsDir() {
			return fmt.Errorf("无效的项目目录: %s", dir)
		}
		pdd := filepath.Join(dir, models.UserConfigDirName)
		pur, perr := data.PrjUsrDir(dir, "")
		if perr != nil {
			return fmt.Errorf("项目数据根解析失败: %w", perr)
		}
		_, _, oerr := env.OpenProject(gui.ProjectConfig{
			WorkDir:        dir,
			DataDir:        "",
			ProjectDataDir: pdd,
			PrjUsrRoot:     pur,
			Title:          gui.MainWindowTitle(dir),
			OnWired:        onProjectWired,
		})
		return oerr
	}
	// 回调必须先于**任何窗口创建**注入（createWindow 会把它转给桥的 gui.dir.open）。
	env.SetProjectOpener(openProject)

	// ── 日志窗口（PRODSIM-1）：进程级唯一、创建后**默认隐藏** ──
	// 它是 server 进程的窗口载体：所有项目主窗口关闭后，进程仍靠它 + 托盘常驻；
	// 关闭 = 收起回托盘（HideOnClose），托盘「显示日志」唤出。
	if _, logErr := env.StartLogWindow(gui.LogWindowSpec{Title: "chonkpilot 日志"}); logErr != nil {
		slog.Warn("log window create failed（继续，无日志窗口）", "err", logErr)
	}
	gui.LogStartupStage("日志窗口创建（隐藏）")

	// 首个项目主窗口。
	if _, _, err := env.OpenProject(gui.ProjectConfig{
		WorkDir:        workDir,
		DataDir:        dataDirArg,
		ProjectDataDir: projectDataDir,
		PrjUsrRoot:     prjUsrRoot,
		Title:          gui.MainWindowTitle(workDir), // 固定 Taskbar 标题 = chonkpilot-<工程目录名>
		OnWired:        onProjectWired,
	}); err != nil {
		return fmt.Errorf("main window create: %w", err)
	}
	gui.LogStartupStage("主窗口创建（bridge/WebView2 环境就绪）")
	// 进程级收尾改由总线下注册的**单一收尾 defer**（依赖序 ts→srv→fsys→bus）统一完成，
	// 不再各自 defer（原 defer LIFO 会使 bus.Close 早于 srv.Stop/fsys.Stop，D-05）。

	// 实例心跳发布侧（2026-09-20，阶段 2a）：**发布方由宿主改为前端 SPA**；本宿主不再发布心跳。
	// `--bridge-url`（分离形态 bridge 对端地址）当前仅登记 + 日志（bridge 路由为分阶段落地项）。
	if bridgeURL != "" {
		slog.Info("split bridge endpoint", "bridge_url", bridgeURL)
	}

	// 日志级别：各窗口建窗时读 prj logLevel 初值（主窗口见上方 OnWired）；此处订阅既有
	// data-prj-config-refresh（**进程级一次**）→ prj 配置一变更即应用、无需重启。
	gui.WatchLogLevel(bus)
	gui.LogStartupStage("启动流程就绪（进入窗口事件循环，首屏于导航完成后另记）")

	// 全部模块就绪 → 插件桥广播 server-ready（常驻索引器据此开始消费队列；P3）。
	// payload 为插件传输帧（非 MQ 主题），见 pluginbridge.MarkReady。
	if pb != nil {
		pb.MarkReady(map[string]any{"form": string(opts.Form), "at": time.Now().UTC().Format(time.RFC3339)})
	}

	// resident indexer（vfts P4b-2b / codegraph P5）：**每宿主进程每插件单实例**
	// （单实例多 workdir，2026-10-19）。项目发现由宿主实例表提供
	// （plugin.index.workdirs → srv.LiveWorkDirs），indexer 进程内含托盘输出窗口。
	// 不设 enable 门控（MVP 始终启动）；exe 缺失 → 静默跳过并只记一次日志。
	if pb != nil {
		logf := func(format string, args ...any) { slog.Info(fmt.Sprintf(format, args...)) }
		vftsExe := vftsIndexerExePath()
		if st, err := os.Stat(vftsExe); err == nil && !st.IsDir() {
			vftsIdx = vftsindexer.New(logf)
			if err := vftsIdx.Ensure(context.Background(), vftsindexer.Options{
				DataRoot:   prjUsrRoot,
				PluginAddr: pb.Addr(),
				IndexerExe: vftsExe,
				Logf:       logf,
				Shutdown:   pb.ShutdownPlugin,
			}); err != nil {
				slog.Warn("vfts indexer 启动失败", "err", err)
			}
		} else {
			slog.Info("vfts indexer 未安装（跳过）", "exe", vftsExe)
		}
		cgExe := codegraphIndexerExePath()
		if st, err := os.Stat(cgExe); err == nil && !st.IsDir() {
			cgIdx = codegraphindexer.New(logf)
			if err := cgIdx.Ensure(context.Background(), codegraphindexer.Options{
				DataRoot:   prjUsrRoot,
				PluginAddr: pb.Addr(),
				IndexerExe: cgExe,
				Logf:       logf,
				Shutdown:   pb.ShutdownPlugin,
			}); err != nil {
				slog.Warn("codegraph indexer 启动失败", "err", err)
			}
		} else {
			slog.Info("codegraph indexer 未安装（跳过）", "exe", cgExe)
		}
	}

	// ── 托盘（PRODSIM-1）：常驻入口 —— 显示日志 / 打开项目 / 项目列表 / 退出 ──
	// 托盘不可用（启动失败）→ 退回旧行为：所有项目主窗口关闭即退出。
	// （退出通道 exitCh / requestExit 已在建窗之前声明：测试通道 `/quit` 与托盘共用。）
	trayStop, trayErr := tray.Start(tray.Callbacks{
		Tip:           "Chonkpilot",
		OnDoubleClick: func() { env.ShowLogWindow() },
		OnShowLog:     func() { env.ShowLogWindow() },
		OnOpenProject: func() {
			dir := pickDirectory("选择项目目录")
			if dir == "" {
				return
			}
			if err := openProject(dir); err != nil {
				gui.NativeAlert("Chonkpilot", err.Error())
			}
		},
		OnExit: func() {
			env.Shutdown()       // 关所有项目主窗口/小窗口（各自发 instance-exit）
			env.CloseLogWindow() // 强制销毁（无视 HideOnClose）
			requestExit()
		},
		ListProjects: func() []tray.Instance {
			ps := env.Projects()
			out := make([]tray.Instance, 0, len(ps))
			for _, p := range ps {
				out = append(out, tray.Instance{ID: p.WorkDir, Title: p.Title})
			}
			return out
		},
		OnPickProject: func(workDir string) { env.ActivateProject(workDir) },
	})
	if trayErr != nil {
		slog.Warn("tray start failed（无托盘：所有主窗口关闭即退出）", "err", trayErr)
		waitAllProjectsClosed(env, exitCh)
		return nil
	}
	defer trayStop()
	gui.LogStartupStage("托盘就绪")

	// PRODSIM-1：进程常驻 —— 只有托盘「退出」才结束。
	// 单个项目主窗口关闭只移除该项目（见 windowHost.finish），server 与日志窗口继续运行。
	<-exitCh
	return nil
}

// waitAllProjectsClosed 是无托盘时的退回路径：轮询到所有项目主窗口关闭（或退出信号）后返回。
func waitAllProjectsClosed(env *gui.Env, exitCh <-chan struct{}) {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-exitCh:
			return
		case <-tick.C:
			if len(env.Projects()) == 0 {
				return
			}
		}
	}
}

// vftsIndexerExePath 定位 resident indexer：优先宿主 exe 同目录，
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

// codegraphIndexerExePath 定位 resident codegraph indexer：优先宿主 exe 同目录，
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

// flagSet 报告命令行是否**显式**给出过该 flag（用于「env 覆盖缺省值但不压过显式参数」）。
func flagSet(name string) bool {
	set := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// defaultWorkDir 在未显式 --work-dir 时解析工作目录：
// 最近项目（存在）→ 默认工作目录（%APPDATA%\chonkpilot\workspace）→ 当前 cwd。
func defaultWorkDir() string {
	for _, d := range models.LoadRecents(models.RecentsPath()) {
		if d == "" {
			continue
		}
		if models.DirExists(d) {
			return d
		}
	}
	if d, err := models.EnsureDefaultWorkspace(); err == nil {
		return d
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return ""
}
