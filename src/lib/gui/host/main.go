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
	"github.com/chonkpilot/chonkpilot-gui/bridge"
	"github.com/chonkpilot/chonkpilot-gui/mqpolicy"
	"github.com/chonkpilot/chonkpilot-llm/llmbridge"
	"github.com/chonkpilot/chonkpilot-llm/plugindata"
	"github.com/chonkpilot/chonkpilot-llm/server"
	"github.com/lsong98sh/chonkpilot-framework/host/assetserver"
	"github.com/lsong98sh/chonkpilot-framework/host/core/exedir"
	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-framework/host/core/tray"
	"github.com/lsong98sh/chonkpilot-framework/host/gui"
	"github.com/lsong98sh/chonkpilot-framework/host/models"
	"github.com/lsong98sh/chonkpilot-framework/host/singleinstance"
	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
	pluginmgr "github.com/lsong98sh/chonkpilot-plugin-framework/mgr"
	"github.com/lsong98sh/chonkpilot-plugin-framework/runtime/dispatch"
)

// Main 是单体装配宿主入口（webview2 宿主 + 内嵌 server/persist/filesys + 插件装配 + 桥接线）。
//
// **调用方 = 壳**（`src/desktop` = 桌面单体 / `src/gui` = GUI 客户端）：壳只做两件事 ——
// `//go:embed all:frontend/dist` 拿到前端 dist，并以 `opts.Form` 声明形态；装配实现唯一一份。
//
// distFS 必须是含 `frontend/dist` 前缀的嵌入文件系统（壳的 `//go:embed all:frontend/dist`），
// 内部 `fs.Sub(distFS, "frontend/dist")` 取 dist 根。启动参数（--work-dir/
// --bridge-url/--test-port/--llm-base/--llm-model/--no-server/--ui-assets-addr）仍在本函数内解析。
//
// activations = 壳从实例 socket 收到的「第二实例激活请求」（nil = 不接）；非 nil 时由
// [handleActivation] 在项目主窗口就绪后消费 —— 见 49 §2.22.6-5。
func Main(distFS fs.FS, opts gui.Options, activations <-chan singleinstance.Command) {
	// TOPO-1（2026-10-29）：窗口桥实现由业务仓注入 —— framework/host/gui 只声明端口
	// （gui.Bridge），具体实现（依赖 data / persist 的 chonkpilot-gui/bridge）留在主仓。
	gui.SetBridgeFactory(func(id, wd, dd string, eval func(script string), bus mq.Bus) gui.Bridge {
		return bridge.New(id, wd, dd, bridge.Eval(eval), bus)
	})
	// 日志级别：先以缺省级别装好 logger（早于配置可用无妨），待桥/数据面就绪后经既有配置
	// 通道读 prj logLevel 覆盖并订阅变更即时生效（见 gui 的 loglevel.go）。
	gui.InitLogging()
	// 主流程返回 error（**不再中途 os.Exit**，D-08）：保证已注册的收尾 defer 链按依赖序执行，
	// 且失败经原生消息框可见（windowsgui 下 stderr 不可见，D-06）。
	if err := runMain(distFS, opts, activations); err != nil {
		slog.Error("gui main failed", "err", err)
		gui.NativeAlert("ChonkPilot", err.Error())
		os.Exit(1)
	}
}

// pluginDataRoot 是插件平台数据根（与插件 mgr 的 `DataDir` 同源）：
// 显式 `--data-dir` 优先；desktop 缺省 = `%LOCALAPPDATA%\chonkpilot`（无 LOCALAPPDATA → temp）。
// 插件日志落 `<root>/plugins/<id>/logs/plugin.log`（G-83 ④，日志窗口按插件查看）。
func pluginDataRoot(dataDirArg string) string {
	if d := strings.TrimSpace(dataDirArg); d != "" {
		return d
	}
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "chonkpilot")
}

// pluginProjectLogPath 返回插件日志**按项目镜像**的落盘路径（PLUGIN-LOG-1）：
// `<workDir>/.chonkpilot/users/<OS 用户名>/logs/plugins/<id>.log`（= prjusr 数据根 `logs/plugins/`）。
// 只对**已打开项目**镜像（`.chonkpilot` 目录存在）—— 日志文本里的 wd 可能来自任意行，
// 不能凭它创建目录；插件 id 只取 basename。返回空 = 不镜像（只留全局 plugin.log）。
func pluginProjectLogPath(workDir, pluginID, dataDir string) string {
	if workDir == "" || pluginID == "" || !filepath.IsAbs(workDir) {
		return ""
	}
	if fi, err := os.Stat(filepath.Join(workDir, ".chonkpilot")); err != nil || !fi.IsDir() {
		return ""
	}
	root, err := data.PrjUsrDir(workDir, dataDir)
	if err != nil || root == "" {
		return ""
	}
	return filepath.Join(root, "logs", "plugins", filepath.Base(pluginID)+".log")
}

// runMain 是装配宿主主流程（Main 的实体）；任何失败以 error 返回，由 Main 统一弹框 + 退出。
func runMain(distFS fs.FS, opts gui.Options, activations <-chan singleinstance.Command) error {
	gui.MarkStartupBegin()

	var workDir, bridgeURL string
	var legacyNatsURL string
	var testPort int
	var llmBase, llmModel string
	var noServer bool
	var uiAssetsAddr string
	flag.StringVar(&workDir, "work-dir", "", "工作目录（前端文件树根）")
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
	// 项目数据根（prj 层所在目录）= `<workDir>/.chonkpilot`（固定规则；无启动参数可改）。
	projectDataDir := models.ProjectDataDir(workDir)
	// PRODSIM-1（产物简化）：**项目级单实例锁已删除** —— 唯一进程由 `singleinstance`
	// （实例 UDS 独占 listen）保证，同项目只能开一个主窗口由进程内 `work_dir → 主窗口` 映射表保证。
	// 传给 server / 桥的 data_dir（**数据层分支判据**；12-数据层 §3 · §5.3）：
	//   - desktop（唯一发行形态）**恒为空** → 数据层走 users/ 分支 → 个人运行态
	//     （会话/任务树/快照）落 `<workDir>/.chonkpilot/users/<OS 用户名>/`（DATAROOT-1）；
	//     ⚠️ 不可传 `projectDataDir`：那会让 prjusr 与 prj 同库（个人运行态落进项目目录）。
	//   - 历史分离形态（gui）：保持项目数据根（prj/prjusr 同库；该形态已无产物）。
	dataDirArg := projectDataDir
	if opts.Form == gui.FormDesktop {
		dataDirArg = ""
	}
	gui.LogStartupStage("参数与目录解析")
	// prjusr 数据根（个人运行态非库文件的落盘根：日志 logs/、附件/截图 tmp/uploads/、
	// fileserver 白名单；[24 §3.2] MW-8）。DATAROOT-1 后由 `<workDir>/.chonkpilot/users/<OS 用户名>/`
	// 直接算出（无需再读 prj 库取 project-id；也不可拿项目数据根当 prjusr 根）。
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
	// mqHub = 本进程总线的**分发出口**（单订阅 ">" 的分发器 + WorkDirPolicy）；窗口桥与插件桥
	// 共用同一个实例（49 §2.22.7 S3：插件订阅也走同一分发器与同一份策略）。
	var mqHub *mqpolicy.Hub
	var plugins *pluginmgr.Manager
	var bx *llmbridge.Server
	// 可选子系统在**后台 goroutine** 启动（主 goroutine 必须尽快进入窗口消息循环，否则
	// WebView2 事件被阻塞 → /eval 超时）。句柄的读写统一用 optMu 保护。
	var optMu sync.Mutex
	getPlugins := func() *pluginmgr.Manager { optMu.Lock(); defer optMu.Unlock(); return plugins }
	setPlugins := func(v *pluginmgr.Manager) { optMu.Lock(); plugins = v; optMu.Unlock() }
	getBridge := func() *llmbridge.Server { optMu.Lock(); defer optMu.Unlock(); return bx }
	setBridge := func(v *llmbridge.Server) { optMu.Lock(); bx = v; optMu.Unlock() }
	var assetSrv *assetserver.Server

	// ── 共用脊柱（appwire，GATEWAY-2 L12）：总线 → data 门面 → filesys → llm server + 默认插件 ──
	// 启动期输入快照（BootConfig）：只记环境输入（路径 / 旗标 / LLM 缺省 / 认证 / 池参数）；
	// **不复制运行期配置** —— usr/prj 由 llm/server 按实例从数据面读取并热生效（12-数据层）。
	capRoot := ""
	if dir, derr := exedir.Dir(); derr == nil {
		capRoot = filepath.Join(dir, "capability")
	}
	// 2026-11-02：桌面单体不再拉起 core-mcp-server（executor 已全退场，工具全部 runtime=builtin
	// → 由内嵌 gateway 进程内注册）。`CHONKPILOT_EXTERNAL_TOOLS` 不再设置。
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
	// 实例视图变更广播（异步改造 §10.1）：`workdir.ensure` 等隐藏打开后**不再轮询**实例视图，
	// 改由既有 `instance-register` / `instance-exit` 主题（61 §4.1，零新增主题）唤醒。
	// 订阅者只做非阻塞 close-and-replace 广播；权威状态仍由 `srv.MainInstanceForWorkDir` 反查。
	instanceChanges := newInstanceChangeBroadcaster()
	// 顺序 = 1：**必须晚于** server 实例视图（`instance.Manager` 以 order 0 维护登记）执行，
	// 否则唤醒方在 im 更新前重查会看到「仍未登记」，而同一事件不会二次唤醒（丢唤醒）。
	for _, topic := range []string{msgkeys.TopicInstanceRegister, msgkeys.TopicInstanceExit} {
		if _, err := bus.On(topic, 1, func(_ context.Context, _ string, _ *mq.Value) error {
			instanceChanges.notify()
			return nil
		}); err != nil {
			slog.Warn("instance change subscription failed（workdir.ensure 退化为仅超时）", "topic", topic, "err", err)
		}
	}
	defer func() {
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
		if v := getBridge(); v != nil {
			shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = v.Shutdown(shutCtx)
			cancel()
		}
		if srv != nil {
			srv.Stop()
		}
		if fsys != nil {
			fsys.Stop()
		}
		// 插件平台：先停插件（依赖插件桥在线发 shutdown），再关插件桥。
		if v := getPlugins(); v != nil {
			v.Stop()
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

		// MQ 分发出口策略 S2（49 §2.22.7）：workdir 级主题只投同 workdir 的窗口 —— 事件 workdir
		// 取值顺序 = 载荷 `work_dir` → 否则按 `instance_id` 反查。反查视图 = framework
		// instance.Manager（由 server 持有并维护；**不自建第二份登记表**）。窗口 sink 的 workdir
		// 由桥注册时自带（`dispatch.Sink.WorkDir`），策略侧不需要再查表。
		mqHub = mqpolicy.HubFor(bus)
		if mqHub != nil {
			// P5 步骤 3（C15）：路由规则收归 PM —— 分发策略与 PM 共用同一份发布账本 / 反查视图。
			if app.Workspace != nil {
				mqHub.Policy.SetRouter(app.Workspace.Router())
			}
			im := srv.InstanceManager()
			mqHub.Policy.SetInstanceWorkDir(func(id string) (string, bool) {
				for _, in := range im.List() {
					if in.ID == id && in.WorkDir != "" {
						return in.WorkDir, true
					}
				}
				return "", false
			})
		}

		// 插件桥接入口（BRIDGE-2）：桌面单体默认开启；gui 客户端不在此进程监听（由 server 提供）。
		if opts.Form == gui.FormDesktop {
			pb = pluginbridge.New(pluginbridge.Options{
				Bus:     bus,
				Version: "desktop",
				Form:    string(opts.Form),
				// 活动实例视图唯一来源 = framework instance.Manager（由 server 持有并维护）；
				// 桥不再自持登记副本（2026-10-29 内存单源化）。
				Instances: srv.InstanceManager(),
				// 插件 sink 接入**同一个**分发器与同一份策略（49 §2.22.7 S3）：桥不再自订阅 ">"，
				// 每个插件会话在 `plugin.hello` 后注册 sink，workdir 级事件由宿主强制隔离。
				Dispatch: func() *dispatch.Dispatcher {
					if mqHub == nil {
						return nil // 无总线（-no-server 等）→ 桥回落自有 fanout
					}
					return mqHub.Dispatch
				}(),
				// 监听地址覆盖（测试 / 多实例隔离）：tcp://127.0.0.1:PORT 或 UDS 路径；
				// 缺省 = pluginbridge.DefaultAddr()（UDS；`CHONK_PLUGIN_UDS` 可覆盖路径）。
				Addr: os.Getenv("CHONK_PLUGIN_LISTEN"),
				// PRODSIM-1：插件桥只保留 **UDS 免认证前门**（HTTP 前门已删除）。
				Logf: func(format string, args ...any) { slog.Info(fmt.Sprintf(format, args...)) },
			})
			if err := pb.Start(); err != nil {
				slog.Warn("plugin bridge 启动失败（可选子系统，继续运行）", "err", err)
				pb = nil
			} else {
				srv.SetFilters(pb.Filters())
				srv.SetPluginRegistry(pb.Registry())
				slog.Info("plugin bridge started", "addr", pb.Addr())
			}
		}
	}

	// ── 可选子系统（core-mcp-server / 插件平台 / LLM 桥）延后到主窗口首帧之后 ──
	// 动机（2026-10-26 实测）：插件 spawn+注册 ~2.4s，若排在主窗口之前，用户看到的是
	// 「什么都不出现」。现改为：主窗口先出（前端显示「插件初始化中」），随后再起插件；
	// 工具面在数秒内收敛，核心 UI 不受影响。启动失败仍全部降级不致命。

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
		PluginDataDir:  pluginDataRoot(dataDirArg),
		Form:           opts.Form,
		Server:         envServer,
		// --no-server：本地无数据服务 → 桥 data-* 快速失败（不等 3s 超时，首屏提速）。
		DataOffline: noServer,
		// 插件自持 webui（PLUGIN-UI-1）：`GET /plugins/<id>/<path>` → UDS
		// `plugin/v1/ui.asset.get` → 插件 embed 资源；缓存按「版本 + 重启代次」失效。
		PluginAsset: func(ctx context.Context, id, path string) ([]byte, bool, error) {
			m := getPlugins()
			if m == nil {
				return nil, false, errors.New("插件平台未启动")
			}
			return m.UIAsset(ctx, id, path)
		},
		PluginAssetVersion: func(id string) string {
			if m := getPlugins(); m != nil {
				return m.UIAssetVersion(id)
			}
			return ""
		},
		// 插件 UI 动作面（PLUGIN-UI-1 第二段）：`POST /plugins/<id>/api/<action>`
		// → 复用通用 RPC（`plugin.ui.rpc`）透传给已就绪插件。
		PluginAction: func(ctx context.Context, id, action string, payload map[string]any) (any, error) {
			m := getPlugins()
			if m == nil {
				return nil, errors.New("插件平台未启动")
			}
			return m.UIAction(ctx, id, action, payload)
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

	// 项目集合变更广播（异步改造 §10.1）：无托盘退回路径不再 500ms 轮询项目数。
	// 每个项目主窗口 `Done()` 关闭（= `windowHost.finish` 收尾）→ 一条 watcher 广播；
	// 所有打开路径（首个窗口 / 托盘 / gui.dir.open / workdir.ensure）都经 openProjectAs 或
	// 首次 OpenProject 登记 watcher。
	projectClosed := newProjectCloseTracker()

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
	// openProjectAs 是同进程「打开项目」的统一入口（托盘 / gui.dir.open / 插件桥 workdir.ensure 复用）。
	// hidden=true = 隐藏打开（C13/C17：走**正常打开流程**，仅窗口不显示，见 gui.ProjectConfig.Hidden）。
	openProjectAs := func(dir string, hidden bool) error {
		if st, statErr := os.Stat(dir); statErr != nil || !st.IsDir() {
			return fmt.Errorf("无效的项目目录: %s", dir)
		}
		pdd := filepath.Join(dir, models.UserConfigDirName)
		pur, perr := data.PrjUsrDir(dir, "")
		if perr != nil {
			return fmt.Errorf("项目数据根解析失败: %w", perr)
		}
		h, existed, oerr := env.OpenProject(gui.ProjectConfig{
			WorkDir:        dir,
			DataDir:        "",
			ProjectDataDir: pdd,
			PrjUsrRoot:     pur,
			Title:          gui.MainWindowTitle(dir),
			Hidden:         hidden,
			OnWired:        onProjectWired,
		})
		if oerr != nil {
			return oerr
		}
		if !existed {
			// 新窗口：关闭时广播项目集合变更（已存在窗口的 Done 只登记一次）。
			projectClosed.watch(h.Done())
		}
		return nil
	}
	openProject := func(dir string) error { return openProjectAs(dir, false) }
	// 回调必须先于**任何窗口创建**注入（createWindow 会把它转给桥的 gui.dir.open）。
	env.SetProjectOpener(openProject)

	// P5 步骤 2（C15 / 41 G-100）：PM 门面的窗口/会话动作全部接线 ——
	// 窗口类走框架 [gui.Env] 导出的薄动作（与托盘 / open-chat 同一实现），
	// 会话类走项目桥的既有 `data-session-ensure-session`（零新增消息面）。
	if app.Workspace != nil {
		app.Workspace.SetProjectOps(hostProjectOps{
			env: env,
			open: openProjectAs,
			instanceID: func(dir string) (string, bool) {
				return app.Workspace.Instances().MainInstanceForWorkDir(dir)
			},
		})
		// P5 步骤 2（41 G-101）：运行态归属的**存储单源**归 PM —— 框架不再自持副本。
		env.SetOwnerStore(app.Workspace)
	}

	// 首个项目主窗口（**先于日志窗口与插件平台**：用户先看到主视图，插件随后收敛）。
	h, existed, err := env.OpenProject(gui.ProjectConfig{
		WorkDir:        workDir,
		DataDir:        dataDirArg,
		ProjectDataDir: projectDataDir,
		PrjUsrRoot:     prjUsrRoot,
		Title:          gui.MainWindowTitle(workDir), // 固定 Taskbar 标题 = chonkpilot-<工程目录名>
		OnWired:        onProjectWired,
	})
	if err != nil {
		return fmt.Errorf("main window create: %w", err)
	}
	if !existed {
		projectClosed.watch(h.Done())
	}
	gui.LogStartupStage("主窗口创建（bridge/WebView2 环境就绪）")

	// 第二实例激活请求（49 §2.22.6-5）：首窗口就绪后再消费，避免与首个 OpenProject 竞态。
	// 命令只保留最新一条（壳侧已去重），这里再多一道保护：慢消费不堆积。
	if activations != nil {
		go func() {
			for cmd := range activations {
				handleActivation(env, openProject, workDir, cmd)
			}
		}()
	}

	// ── 日志窗口（PRODSIM-1）：进程级唯一、创建后**默认隐藏** ──
	// 2026-10-26：**移到主窗口之后**创建 —— 首次 WebView2 环境初始化 ~1–3s，若放在主窗口前，
	// 用户看到的是长时间空屏。这里主窗口已出，日志窗口随后补建（仍隐藏，托盘可唤出）。
	// 它是 server 进程的窗口载体：所有项目主窗口关闭后，进程仍靠它 + 托盘常驻；
	// 关闭 = 收起回托盘（HideOnClose），托盘「显示日志」唤出。
	if _, logErr := env.StartLogWindow(gui.LogWindowSpec{Title: "chonkpilot 日志"}); logErr != nil {
		slog.Warn("log window create failed（继续，无日志窗口）", "err", logErr)
	}
	gui.LogStartupStage("日志窗口创建（隐藏）")

	// 后台启动可选子系统：主 goroutine 继续进入窗口消息循环（否则 WebView2 事件被阻塞）。
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("可选子系统启动 panic（已恢复）", "panic", r)
			}
		}()
		// ── 可选子系统（插件平台 / LLM 桥）：主窗口已出后才启动 ──
		// 2026-10-26 实测：插件 spawn+注册 ~2.4s。此前排在主窗口之前 → 用户长时间看不到窗口；
		// 现改为后置（主视图先出，前端显示「插件初始化中」），工具面在数秒内收敛。
		// 2026-11-02：core-mcp-server 已退役（executor 全退场；工具 runtime=builtin 由内嵌 gateway 注册）。
		if srv != nil && pb != nil {
			// ── 插件平台（PLUGIN-1）：manifest 发现 / 拉起 / 守护 / MCP+UI 注册 ──
			pluginDataDir := pluginDataRoot(dataDirArg)
			userRoot := ""
			if home, err := os.UserHomeDir(); err == nil {
				userRoot = filepath.Join(home, ".chonkpilot")
			}
			// 插件根（2026-11-02）：直接扫描 `<root>/<id>/plugin.json`，插件与 capability 同级。
			// 优先级：项目用户数据 → 工作目录 .chonkpilot → 用户级 ~/.chonkpilot → 随产物 exeDir。
			roots := []string{
				filepath.Join(prjUsrRoot, "plugins"),
				filepath.Join(workDir, ".chonkpilot", "plugins"),
				filepath.Join(userRoot, "plugins"),
				filepath.Join(filepath.Dir(capRoot), "plugins"),
			}
			// SANDBOX-WRITE-1 P3：`plugin/v1/workdir.ensure` 宿主实现 —— 定时任务执行前确保项目
			// 已打开（未打开 → 隐藏打开 + 等 instance-register；见 workdirensure.go）。
			ensurer := &workdirEnsurer{
				open:       openProjectAs,
				registered: srv.MainInstanceForWorkDir,
				logf:       func(format string, args ...any) { slog.Info(fmt.Sprintf(format, args...)) },
				timeout:    workDirEnsureTimeout,
				// 事件驱动：既有 instance-register / instance-exit 广播（上方订阅）；
				// 不再 sleep/poll（异步改造 §10.1）。
				changed: instanceChanges.changed,
			}
			// SANDBOX-WRITE-1 P6：数据能力端点（插件不直接开库，C8/C18；方案 §9.1）。
			// 键空间 = (插件 id, work_dir, user)；跨用户默认拒绝；审计落 `<pluginDataDir>/_audit/audit.jsonl`。
			pluginData := plugindata.New(plugindata.Options{
				Root:    pluginDataDir,
				DataDir: dataDirArg,
				Logf:    func(format string, args ...any) { slog.Warn(fmt.Sprintf(format, args...)) },
			})
			mgr := pluginmgr.New(pluginmgr.Options{
				Bus:            bus,
				DataDir:        pluginDataDir,
				Roots:          roots,
				Data:           pluginData.Hooks(),
				ConfigStore:    openPluginConfigStore,
				RegisterMCP:    srv.RegisterPluginServer,
				UnregisterMCP:  srv.UnregisterPluginServer,
				ShutdownPlugin: pb.ShutdownPlugin,
				RequestPlugin:  pb.RequestPlugin,
				// SANDBOX-WRITE-1 P2：`plugin/v1/sandbox.resolve` 宿主实现（按 work_dir 取可写白名单）。
				SandboxResolve: srv.SandboxPolicyForWorkDir,
				// SANDBOX-WRITE-1 P3：`plugin/v1/workdir.ensure` 宿主实现（隐藏打开，C13/C17）。
				WorkdirEnsure: func(workDir string, hidden bool) (pluginbridge.WorkdirEnsureResult, error) {
					id, opened, eerr := ensurer.ensure(context.Background(), workDir, hidden)
					if eerr != nil {
						return pluginbridge.WorkdirEnsureResult{}, eerr
					}
					return pluginbridge.WorkdirEnsureResult{InstanceID: id, WorkDir: workDir, Opened: opened}, nil
				},
				Logf: func(format string, args ...any) { slog.Warn(fmt.Sprintf(format, args...)) },
				// PLUGIN-LOG-1：插件带 `wd=` 标签的日志行按项目镜像落盘（日志窗口「项目」过滤的数据源）。
				ProjectLogPath: func(wd, pluginID string) string {
					return pluginProjectLogPath(wd, pluginID, dataDirArg)
				},
			})
			setPlugins(mgr)
			pb.SetPluginHooks(mgr.Hooks())
			if err := mgr.Start(context.Background()); err != nil {
				slog.Warn("插件平台启动失败", "err", err)
			} else {
				slog.Info("插件平台已启动", "data", pluginDataDir)
			}

			// ── LLM 桥接入口（BRIDGE-3）：桌面单体同开，**只监听 UDS** ──
			if opts.Form == gui.FormDesktop {
				udsPath := strings.TrimSpace(os.Getenv("CHONK_LLM_UDS"))
				if udsPath == "" {
					udsPath = llmbridge.DefaultPath()
				}
				if udsPath != "off" && udsPath != "-" {
					b := llmbridge.New(llmbridge.Options{
						Path: udsPath,
						Resolve: func(name string) (llmbridge.Target, bool) {
							t, ok := srv.ResolveBridgeTarget(name)
							if !ok {
								return llmbridge.Target{}, false
							}
							return llmbridge.Target{BaseURL: t.BaseURL, APIKey: t.APIKey, Protocol: t.Protocol}, true
						},
					})
					if err := b.Start(); err != nil {
						slog.Warn("LLM 桥接入口启动失败（可选子系统，继续运行）", "err", err)
					} else {
						setBridge(b)
						slog.Info("LLM bridge started", "uds", b.Path())
					}
				}
			}
			gui.LogStartupStage("可选子系统（core 工具面 / 插件平台 / LLM 桥）")

			// 全部模块就绪 → 插件桥广播 server-ready（插件据此开始自管索引；D-71）。
			pb.MarkReady(map[string]any{"form": string(opts.Form), "at": time.Now().UTC().Format(time.RFC3339)})
		}
	}()
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
		// 事件驱动：项目主窗口 Done → projectClosed 广播；不再 500ms 轮询项目数。
		waitAllProjectsClosed(
			func() int { return len(env.Projects()) },
			projectClosed.changed,
			exitCh,
		)
		return nil
	}
	defer trayStop()
	gui.LogStartupStage("托盘就绪")

	// PRODSIM-1：进程常驻 —— 只有托盘「退出」才结束。
	// 单个项目主窗口关闭只移除该项目（见 windowHost.finish），server 与日志窗口继续运行。
	<-exitCh
	return nil
}

// waitAllProjectsClosed 见 projectclosed.go：事件驱动（窗口 Done → 广播 → 重查项目数）。

// activationEnv 是 handleActivation 依赖的最小窗口面（测试可替身）。
type activationEnv interface {
	ActivateProjectAny(preferred string) bool
}

// handleActivation 处理第二实例的激活请求（49 §2.22.6-5）：优先前置一个已打开的 workdir
// 窗口（匹配本次启动 work_dir → 前台项目 → 最近活动 → 字典序兜底）；一个都没开 →
// 执行「打开工作目录」（打开本次启动解析出的 work_dir）。
func handleActivation(env activationEnv, open func(string) error, startupWorkDir string, cmd singleinstance.Command) {
	if cmd.Cmd != singleinstance.CmdActivate {
		return
	}
	target := strings.TrimSpace(cmd.WorkDir)
	if target == "" {
		target = startupWorkDir
	}
	if env.ActivateProjectAny(target) {
		return
	}
	if err := open(target); err != nil {
		slog.Warn("第二实例激活请求：打开工作目录失败", "workdir", target, "err", err)
	}
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

// DesktopWorkDir 返回本次启动解析出的**工作目录**（与 runMain 同口径：`--work-dir` →
// 最近项目 → 默认工作目录 → 当前 cwd）。供壳在单例判定前使用（数据根与第二实例激活请求）。
func DesktopWorkDir() string {
	wd := scanWorkDirArg(os.Args[1:])
	if wd == "" {
		wd = defaultWorkDir()
	}
	if wd == "" {
		return ""
	}
	return models.ResolveDir(wd, "")
}

// DesktopDataRoot 返回本进程的**项目数据根**（`<work-dir>/.chonkpilot`），与 runMain 的
// projectDataDir 同口径。供壳（`src/desktop`）在做进程单例判定**之前**使用：`singleinstance`
// 以数据根指纹命名实例 UDS socket（同一数据根只允许一份进程，保护 bbolt 库不被双开；不同数据根可并存）。
//
// 此刻 flag.Parse 尚未执行（单例判定必须早于任何库/目录副作用），故自行读 `--work-dir`；
// 未显式给出时与 runMain 同口径回落 defaultWorkDir()。无法确定工作目录 → 返回 ""，
// 调用方退回全局单例 socket（`global.sock`，保住「整机一份」的旧行为）。
func DesktopDataRoot() string {
	wd := DesktopWorkDir()
	if wd == "" {
		return ""
	}
	if abs, err := filepath.Abs(wd); err == nil {
		wd = abs
	}
	return models.ProjectDataDir(wd)
}

// scanWorkDirArg 从命令行读取 `--work-dir`（不注册 flag）：兼容 `--work-dir=<值>` 与
// `--work-dir <值>` 两种形态（Go flag 两种都接受），`-work-dir` 单横线同义。
func scanWorkDirArg(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		for _, name := range []string{"--work-dir", "-work-dir"} {
			if a == name {
				if i+1 < len(args) {
					return args[i+1]
				}
				return ""
			}
			if strings.HasPrefix(a, name+"=") {
				return strings.TrimPrefix(a, name+"=")
			}
		}
	}
	return ""
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
