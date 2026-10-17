//go:build windows

// Package host 是 ChonkPilot 的**单体装配宿主**（桌面单体 / GUI 客户端的启动装配）。
//
// 拆包背景（2026-10-10）：原 `chonkpilot-gui` 包同时含 (a) 客户端/窗口能力与 (b) 单体装配
// （内嵌 llm/server + filesys + 全部 plugin-* + gateway）。按包链接下，flash 薄壳
// `import chonkpilot-gui` 会把上述重依赖全编进产物。现将**装配**下沉到本包 `host`：
//
//   - 依赖方向：`host` → `gui` **单向**；`gui` **不 import `host`**（故 flash 只链接 gui 时不再
//     拖入 llm/server / filesys / plugin* / gateway）。
//   - `gui` 保留客户端能力（窗口工厂 / 桥 / appHandler / registry / bridge / testserver / 远程
//     薄壳 RunRemote / 日志 / 日志级别 / profile 清理等），并以导出 API（见 gui 的 hostapi.go）
//     供本包单向调用。
//
// 本包入口 = Main（调用方 = 壳 `src/desktop` / `src/gui`）。
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
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-filesys"
	"github.com/chonkpilot/chonkpilot-gui"
	"github.com/chonkpilot/chonkpilot-gui/bridge"
	"github.com/chonkpilot/chonkpilot-gui/flash"
	"github.com/chonkpilot/chonkpilot-gui/models"
	"github.com/chonkpilot/chonkpilot-lib/lockfile"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-llm/server"
	"github.com/chonkpilot/chonkpilot-plugin"
	"github.com/chonkpilot/chonkpilot-plugin-codegraph"
	"github.com/chonkpilot/chonkpilot-plugin-compress"
	"github.com/chonkpilot/chonkpilot-plugin-history"
	"github.com/chonkpilot/chonkpilot-plugin-memory"
	"github.com/chonkpilot/chonkpilot-plugin-vfts"
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
	// 且失败经原生消息框可见（windowsgui 下 stderr 不可见，D-06，口径同 exitWorkDirBusy）。
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
	flag.StringVar(&workDir, "work-dir", "", "工作目录（前端文件树根）")
	flag.StringVar(&dataDir, "data-dir", "", "项目数据根（默认 <workDir>/.chonkpilot；显式给出时 prj/prjusr 同根，缺省时 prjusr 落 ~/.chonkpilot/data/<prj-id>）")
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
	flag.Parse()
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
	// ⚠️ 先于锁计算：models.DataDir 仅读包级变量（**不开库**），可安全早于任何库打开。
	projectDataDir := models.DataDir(workDir)
	// 项目单实例占用校验（prjusr 单例锁 `<prjusr>/chonkpilot.lock`；与 flash **同名文件** →
	// 同项目互斥）：**先于任何库打开** —— 否则第二实例会被 prj 库的 bbolt 独占 flock 卡住
	// （启动长时间无响应）。此处非阻塞取锁，占用即明确报错退出；锁随进程存活（defer 释放；
	// 进程崩溃由 OS 回收）。锁设施自身故障（非占用）不阻断启动。
	//
	// 取舍：正式 prjusr 根在 desktop 缺省下 = `~/.chonkpilot/data/<prj-id>/`，须**读 prj 库取
	// project-id** 才能算出，与「锁先于库打开」矛盾 → 以**项目数据根**（= flash 的 prjusr 约定
	// `<workDir>/.chonkpilot`）作为锁目录：既满足「先于库打开」，又与 flash 的锁**同路径**
	// （同项目互斥，见 62 §3A / 19 §8.11）。
	//
	// 锁目录**恒取** `<workDir>/.chonkpilot`（与 flash.AcquireProject 的 DataDirFor 同路径），
	// **与 --data-dir 解耦**：若锁目录取 --data-dir（projectDataDir 在显式 --data-dir 时 = 该目录），
	// 则同 work_dir 下 desktop(--data-dir=X) 与 flash（恒 `<work_dir>/.chonkpilot`）会各持一把锁，
	// 互斥失效 → 同项目双实例。故锁路径统一为默认项目数据根（user-config 目录名）。
	lockDir := filepath.Join(workDir, models.UserConfigDirName)
	wdLock, wdErr := lockfile.AcquirePrjUsr(lockDir)
	switch {
	case wdErr == nil:
		defer wdLock.Release()
	case errors.Is(wdErr, lockfile.ErrBusy):
		exitWorkDirBusy(workDir)
	default:
		slog.Warn("prjusr lock unavailable", "err", wdErr) // 仅告警：不因锁设施故障阻断启动
	}
	// 传给 server / 桥的 data_dir（**数据层分支判据**；12-数据层 §3 · §5.3）：
	//   - 显式 --data-dir 恒生效（prj 与 prjusr 同根，兼容既有脚本/L2 用例）；
	//   - desktop 缺省（未给 --data-dir）**置空** → 数据层走 project-id 分支 → 个人运行态
	//     （会话/任务树/快照）落 `~/.chonkpilot/data/<prj-id>/`（B 方案，[24 §3.2] MW-7）。
	//     ⚠️ 不可再传 `models.DataDir(workDir)`：该访问器**恒非空**（缺省 = 项目数据根），
	//     会让 prjusr 与 prj 同库（个人运行态落进项目目录）。
	//   - gui 形态缺省：保持项目数据根（客户端薄壳；服务端另按用户数据根落库，本轨不改）。
	dataDirArg := projectDataDir
	if dataDir == "" && opts.Form == gui.FormDesktop {
		dataDirArg = ""
	}
	gui.LogStartupStage("参数与目录解析")
	// prjusr 数据根（个人运行态非库文件的落盘根：日志 logs/、附件/截图 tmp/uploads/、
	// fileserver 白名单；[24 §3.2] MW-8）。**必须先读 prj 库拿 project-id 才能算出**
	// （不可拿 models.DataDir(workDir) 当 prjusr 根，见 12-数据层 §3 实施注意）。
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
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		return fmt.Errorf("mq new: %w", err)
	}
	defer func() {
		if ts != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = ts.Shutdown(ctx)
			cancel()
		}
		if srv != nil {
			srv.Stop()
		}
		if fsys != nil {
			fsys.Stop()
		}
		if bus != nil {
			_ = bus.Close()
		}
	}()
	gui.LogStartupStage("MQ 总线初始化")

	// chonkpilot-filesys 文件服务：订阅 filesys.* 请求（list/content/create/…/watch/
	// unwatch）→ 写回 Result/Errors；watch 变更经 fsnotify 广播 filesys.changed →
	// 桥 ">" 订阅转发前端（文件域直连，61-消息一览 §2；-no-server 离线 UI 注入
	// 测试仍可用文件树）。
	fsys = filesys.New(bus)
	if err := fsys.Start(); err != nil {
		return fmt.Errorf("filesys start: %w", err)
	}
	gui.LogStartupStage("filesys 文件服务启动")

	// ── inprocess 会话服务（GUI 内嵌 chonkpilot-server lib，同进程内存 MQ）──
	// server v2 内嵌 persist（data-* 数据面应答）+ gateway + mcp-server，数据根由
	// bridge.Start 的 instance-register 绑定（persist 自持实例视图，61-消息一览 §4.1），
	// 宿主不再直接打开/持有数据层句柄。
	// -no-server：跳过（UI 注入测试模式，注入事件完全控制回合，无真实 LLM/数据服务）。
	if !noServer {
		srv = server.New(bus, server.Options{
			LLMBase:  llmBase,
			LLMModel: llmModel,
			// 服务端启动参数下发（唯一消费点 = instance-claim 的 desktop 回落，见 61 §4.1 ①：
			// 本机单用户 + --work-dir/--data-dir）。data_dir = `dataDirArg`（desktop 缺省为空 →
			// persist/instance 绑定 data_dir 空 → prjusr 按 project-id 落 ~/.chonkpilot/data/<id>/，
			// 见 12-数据层 §5.1 / [24 §3.2] MW-7）。
			WorkDir: workDir,
			DataDir: dataDirArg,
			// 运行形态（61 §4.6；阶段 2b-2）：**服务端判定**的形态来源（desktop / gui，
			// **由壳经 Options.Form 传入**）→ claim 鉴权分支与首屏 requireAuth 均据此判定；
			// **非 UA、非客户端自报**（22 §1）。
			Form: opts.Form,
			// 内嵌 server/插件的诊断输出（原先只写 stdout，windowsgui 下不可见）经同一文件 sink
			// 落 <dataDir>/logs/gui.log（2026-09-20；未挂上 sink = nil → 仅 stdout，行为不变）。
			LogWriter: gui.LogFileSink(),
			// 内嵌插件：compress（llm-compress 压缩）+ memory（每轮异步沉淀记忆）+ history（git 快照）+ codegraph/vfts（索引工具面，默认关闭）；就绪后广播 server-starting
			// 插件与入口桥依赖的门面绑定在**装配处**选择（23 §7）：本形态 = inline（同进程直调；
			// bus 传宿主实际总线 → config 域写入的变更广播照旧送达订阅方，见 facade/inline）
			Plugins: []plugin.Hook{
				compress.New(compress.DefaultOptions(), inline.New(bus)),
				memory.New(memory.DefaultOptions()),
				history.New(),
				codegraph.New(codegraph.Options{}),
				vfts.New(vfts.Options{}),
			},
		})
		gui.LogStartupStage("server 装配（capability 契约扫描 + gateway 构建）")
		if err := srv.Start(context.Background()); err != nil {
			return fmt.Errorf("server start: %w", err)
		}
		gui.LogStartupStage("server 启动（数据服务/gateway/能力注册/工具缓存预热/插件）")
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
	})

	// 主窗口（MW-1：主窗口同走窗口工厂；几何恢复 / 拦截器 / lifecycle / Show 逐条与改前一致）。
	// 测试通道（--test-port）：仅绑**主窗口**（每窗口一份 appHandler 后，多窗口回归不覆盖，
	// 见 24 §4.5 / U-3）；console 捕获注入须在导航前（见 onWired）。
	mainHost, err := env.StartMainWindow(gui.MainWindowSpec{
		Title: gui.MainWindowTitle(workDir), // 固定 Taskbar 标题 = chonkpilot-<工程目录名>（不随会话变化）
		OnWired: func(h *gui.Window) error {
			// 日志级别接线：数据面就绪后读 prj logLevel 初值（变更订阅见下方 WatchLogLevel）。
			gui.ApplyLogLevelFromConfig(h.Bridge().PrjConfigList())
			if testPort <= 0 {
				return nil
			}
			t, err := env.AttachTestServer(h, testPort)
			if err != nil {
				return err
			}
			ts = t
			return nil
		},
	})
	if err != nil {
		return fmt.Errorf("main window create: %w", err)
	}
	env.SetMain(mainHost)
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

	// 主窗口关闭 = 退出本进程（24 §4.4）：其余对话窗口一并关闭（各自发 instance-exit +
	// gui.window.closed），随后返回（收尾 defer 按依赖序关闭 ts/srv/fsys/bus）。
	<-mainHost.Done()
	env.Shutdown()
	return nil
}

// defaultWorkDir 在未显式 --work-dir 时解析工作目录：
// 最近项目（存在且未被占用）→ 默认工作目录（%APPDATA%\chonkpilot\workspace）→ 当前 cwd。
func defaultWorkDir() string {
	for _, d := range flash.LoadRecents(flash.RecentsPath()) {
		if d == "" {
			continue
		}
		if flash.DirExists(d) && flash.WorkDirFree(d) {
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

// exitWorkDirBusy 报告「该 work-dir 已被另一个实例打开」并退出（I-74）。
// 启动期文件日志尚未挂载 → 原生消息框 + stderr 双通道（windowsgui 构建下 stderr 不可见，
// 消息框是唯一可见提示）。
func exitWorkDirBusy(workDir string) {
	msg := "该工作目录已被另一个 ChonkPilot 实例打开：\n" + workDir +
		"\n\n请先关闭对应窗口，或改用其它目录（--work-dir <目录>）。"
	fmt.Fprintln(os.Stderr, msg)
	gui.NativeAlert("ChonkPilot", msg)
	os.Exit(1)
}
