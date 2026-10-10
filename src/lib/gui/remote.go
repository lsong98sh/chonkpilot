//go:build windows

package gui

// flash 薄壳客户端宿主（远程总线模式；🆕 2026-10-10，见 19 §8.11 / 62 §3A）。
//
// **薄壳语义**（与 desktop/gui 的唯一区别）：
//   - 有 WebView2 窗口 + 内嵌前端（复用本包的窗口工厂 / 桥 / 前端 embed）；
//   - **不启动内嵌 server、不启动 filesys**；数据访问全部经**远程总线**转发到常驻 server；
//   - 总线 = `chonkpilot-lib/uds` 的 `uds.Client`（实现 `mq.Bus`）——UDS 路径 = 约定值
//     （`uds.DefaultPath()`，`CHONK_UDS` 可覆盖），启动即建立连接；
//   - 信任边界 = UDS 本机传输：启动即 `instance-claim`（带必填 `user` + `work_dir` 提议），
//     前门注入 `__local=true` → 免鉴权（**不引入令牌 / UA 判定 / stdio**）；
//   - data-dir 约定 = `<work_dir>/.chonkpilot`（**非空** → `data.PrjUsrDir` 纯路径拼接，
//     不与 server 抢 bbolt flock；命中 `/show` 放行根）；
//   - **锁归 flash 持有**（`chonkpilot-lib/lockfile`，per-user → RDS 多用户同开同项目不冲突）；
//   - **无心跳**：flash 不发布 `instance-heartbeat`；**UDS 连接断开 = 实例退出**（进程退出）。
//
// **复用而非复制**：窗口创建 / 桥接线 / appHandler / 多窗口 / 前端静态面全部复用本包既有
// 实现（`startWindow` / `hostEnv`）；远程模式只额外做「打开语义 + claim + 单例 + 断开退出」。
// 故 flash 宿主外壳（`src/flash`）只是一层 `//go:embed` + 调用本入口（与 `src/desktop`
// / `src/gui` 同构：壳声明形态，实现唯一一份 → 零重复）。
//
// 形态注入（61 §4.6）：flash 复用 **`desktop`** 语义（免鉴权 + 前端不心跳）——三态取值
// 不含 flash；理由：flash 与桌面单体同为「本机单用户免鉴权 + 存活由进程/连接判定」，
// 而 `gui` 形态（分离客户端）要求认证且由前端发心跳，语义不合（见 62 §3A.2）。

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-gui/flash"
	"github.com/chonkpilot/chonkpilot-gui/internal/folder"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/chonkpilot/chonkpilot-lib/uds"
)

// RemoteOptions 是 flash 薄壳宿主参数（宿主外壳经 Options 传入；其余启动参数在 runRemote 内解析）。
type RemoteOptions struct {
	// UDSPath 覆盖 UDS 前门路径；空 = `uds.DefaultPath()`（含 `CHONK_UDS` 环境变量）。
	UDSPath string
}

// RunRemote 是 flash 薄壳宿主入口（调用方 = `src/flash` 外壳）。
// distFS 必须是含 `frontend/dist` 前缀的嵌入文件系统（口径与 gui.Main 一致）。
func RunRemote(distFS fs.FS, opts RemoteOptions) {
	initLogging()
	if err := runRemote(distFS, opts); err != nil {
		slog.Error("flash main failed", "err", err)
		nativeAlert("ChonkPilot", err.Error())
		os.Exit(1)
	}
}

// runRemote 是 flash 宿主主流程（失败以 error 返回，由 RunRemote 统一弹框 + 退出）。
func runRemote(distFS fs.FS, opts RemoteOptions) error {
	markStartupBegin()

	var workDirArg string
	var testPort int
	var serverExe string
	flag.StringVar(&workDirArg, "work-dir", "", "工作目录；空 = 最近项目 → 选目录（62 §3A）")
	flag.IntVar(&testPort, "test-port", 0, "测试通道端口（0=禁用；>0 监听 127.0.0.1 供外部脚本驱动）")
	flag.StringVar(&serverExe, "server-exe", "", "服务端 exe 路径（空 = flash 同目录 / 同级 server/ 推导 chonkpilot-server.exe；自动拉起时使用）")
	flag.Parse()

	base, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cwd: %w", err)
	}

	// ── 服务端探测 + 自动拉起（62 §3A）──────────────────────────────────
	// 约定 UDS 已连通 → 复用常驻 server、不重复拉起；未连通 → 同目录 spawn
	// `chonkpilot-server.exe --autoexit` → 有界重试 dial（见 flash.ServerOptions）。
	if started, err := (flash.ServerOptions{ServerExe: serverExe, Logf: logfDebug}).Ensure(); err != nil {
		return err
	} else if started {
		slog.Info("已拉起常驻服务端（--autoexit）")
	}

	// ── 打开语义 + 单例（见 flash 包与 62 §3A）────────────────────────────
	workDir, alreadyHandled, err := resolveWithSingleton(workDirArg, base, opts)
	if err != nil {
		return err
	}
	if alreadyHandled {
		return nil // 已打开的项目：置顶既有窗口后退出（不新开窗口）
	}
	// 窗口单例（具名窗口类）：同用户同项目重复启动 → 置顶既有主窗口、退出（不新开）。
	// 与下面的 work-dir 锁**并存、职责不同**：本步管「窗口单例」，锁管「项目是否已打开」。
	winClass := flash.WindowClassName(flash.CurrentUser(), workDir)
	winSingleton, existed, wserr := acquireWindowSingleton(winClass)
	if wserr != nil {
		slog.Warn("window singleton unavailable", "err", wserr) // 仅告警：不因设施故障阻断启动
	}
	if existed {
		return nil
	}
	if winSingleton != nil {
		defer winSingleton.release()
	}
	// 锁归 flash 持有（per-user；进程存活期持有，退出由 OS 回收）。
	wdLock, busy, lerr := flash.AcquireProject(workDir)
	switch {
	case busy:
		// 竞态兜底：resolveWithSingleton 已处理过占用；此处再命中 → 置顶既有窗口。
		return bringExistingToTop(workDir, opts)
	case lerr != nil:
		slog.Warn("workdir lock unavailable", "err", lerr) // 仅告警：不因锁设施故障阻断启动
	default:
		defer wdLock.Release()
	}

	// data-dir 约定 = <work_dir>/.chonkpilot（恒非空 → PrjUsrDir 纯路径拼接，见 flash.DataDirFor）。
	dataDir := flash.DataDirFor(workDir)

	// ── 连接 UDS + claim（本机信任免鉴权）──────────────────────────────
	var (
		envPtr     atomic.Pointer[hostEnv]
		closing    atomic.Bool
		connClosed atomic.Bool
	)
	bus, claim, err := dialAndClaim(workDir, opts, func(typ string, _ []byte) {
		// 定向控制帧（不下发前端）：`instance-goto-top` → 置顶本 flash 主窗口。
		if typ != msgkeys.TopicInstanceGotoTop {
			return
		}
		if e := envPtr.Load(); e != nil {
			e.windows.bringMainToTop()
		}
	}, func() {
		// UDS 连接断开 = 实例退出（61 §4.1 ③）：关闭主窗口 → 进程退出。
		connClosed.Store(true)
		if closing.Load() {
			return
		}
		slog.Info("UDS 连接断开 → flash 退出")
		if e := envPtr.Load(); e != nil {
			e.windows.destroyMain()
		}
	})
	if err != nil {
		return err
	}
	defer func() { _ = bus.Close() }()

	prjUsrRoot, err := data.PrjUsrDir(workDir, dataDir)
	if err != nil {
		return fmt.Errorf("prjusr 数据根解析失败: %w", err)
	}
	logDir, logErr := attachFileLog(prjUsrRoot)
	if logErr != nil {
		slog.Warn("file log attach failed", "err", logErr)
	} else {
		defer detachFileLog()
	}
	logStartupStage("UDS 连接 + instance-claim")

	subFS, err := fs.Sub(distFS, "frontend/dist")
	if err != nil {
		return fmt.Errorf("fs.Sub dist: %w", err)
	}

	env := &hostEnv{
		bus:                  bus, // uds.Client（远程总线）：数据/业务消息转发到常驻 server
		distFS:               subFS,
		workDir:              workDir,
		dataDir:              dataDir,
		projectDataDir:       dataDir,
		prjUsrRoot:           prjUsrRoot,
		logDir:               logDir,
		form:                 FormDesktop, // flash 复用 desktop 语义（免鉴权 + 不心跳）
		srv:                  nil,         // 薄壳：不启动内嵌 server → 桥 SetFacade(nil)，data-* 走总线
		instanceID:           claim.InstanceID,
		suppressInstanceExit: true,
		windows:              newWindowRegistry(),
	}

	var ts *testServer
	defer func() {
		if ts != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = ts.Shutdown(ctx)
			cancel()
		}
	}()

	mainHost, err := startWindow(env, windowSpec{
		role:      roleMain,
		title:     mainWindowTitle(workDir),
		url:       appOrigin + "/",
		frameless: true,
		onWired: func(h *windowHost) error {
			applyLogLevelFromConfig(logLevelVar, h.br.PrjConfigList())
			if testPort <= 0 {
				return nil
			}
			ts = newTestServer(h.w, h.br)
			if ts == nil {
				return errors.New("test server: webview lacks ScriptEval")
			}
			if err := ts.Start(testPort); err != nil {
				return fmt.Errorf("test server start failed: %w", err)
			}
			ts.hwnd = h.hwnd
			ts.destroy = h.destroy
			ts.handler = h.handler
			env.setTestServer(ts)
			ts.InjectConsoleCapture(h.chromium)
			h.onReady = ts.SetReady
			return nil
		},
	})
	if err != nil {
		return fmt.Errorf("main window create: %w", err)
	}
	env.windows.setMain(mainHost)
	if winSingleton != nil {
		// 具名窗口类收到置顶消息 → 置顶本进程主窗口（跨线程安全）。
		winSingleton.setBringToTop(env.windows.bringMainToTop)
	}
	envPtr.Store(env)
	logStartupStage("主窗口创建（bridge/WebView2 环境就绪）")
	// 连接在 claim 后、建窗前已断开 → 立即关闭（不阻塞在无连接的窗口上）。
	if connClosed.Load() {
		env.windows.destroyMain()
	}

	watchLogLevel(bus)
	// 记录最近项目（打开成功即记；「最近关闭 → 次近」据同一列表，见 flash 包）。
	if err := flash.SaveRecent(flash.RecentsPath(), workDir); err != nil {
		slog.Warn("save recents failed", "err", err)
	}
	logStartupStage("启动流程就绪（进入窗口事件循环）")

	// 主窗口关闭 = 退出本进程（其余对话窗口一并关闭）；连接断开亦经 destroyMain 到达此处。
	<-mainHost.done
	closing.Store(true) // 正常收尾：抑制 OnClose 的重复 destroy
	env.windows.shutdown()
	return nil
}

// dialAndClaim 连接 UDS 前门并**先** `instance-claim`（使连接绑定实例，其后 bridge.Start 的
// `instance-register` 方能经前门注入实例身份）。实现下沉至 `flash.DialClaim`（与 flash-cli 共用）。
func dialAndClaim(workDir string, opts RemoteOptions, onControl func(string, []byte), onClose func()) (*uds.Client, flash.ClaimInfo, error) {
	return flash.DialClaim(flash.ClaimOptions{
		WorkDir:   workDir,
		UDSPath:   opts.UDSPath,
		OnControl: onControl,
		OnClose:   onClose,
		Logf:      logfDebug,
	})
}

// logfDebug 是 flash 共享逻辑的诊断出口（写宿主统一 slog）。
func logfDebug(format string, args ...any) { slog.Debug(fmt.Sprintf(format, args...)) }

// resolveWithSingleton 解析工作目录并按**单例**语义处置。返回 (workDir, alreadyHandled, err)：
//   - alreadyHandled=true → 目标目录**已被本用户打开**（work-dir 锁占用，或已存在同用户同项目
//     窗口）→ 已请求置顶既有窗口（调用方应退出，不新开窗口）；
//   - 跨用户冲突（前门回 `instance-forbidden`）→ 让用户换目录继续选（有界重试）；
//   - 其余情况 → 返回待打开的工作目录。
func resolveWithSingleton(workDirArg, base string, opts RemoteOptions) (string, bool, error) {
	resolver := flash.DirResolver{
		BaseDir: base,
		Exists:  flash.DirExists,
		Free:    flash.WorkDirFree,
		Picker:  func() (string, error) { return folder.PickFolder("选择项目目录") },
	}
	recents := flash.LoadRecents(flash.RecentsPath())
	for attempt := 0; attempt < 8; attempt++ {
		workDir, err := resolver.Resolve(workDirArg, recents)
		if err != nil {
			if errors.Is(err, flash.ErrNoProject) {
				return "", false, errors.New("未选择项目目录（可用 --work-dir 指定，或从「最近项目」打开）")
			}
			return "", false, err
		}
		// 窗口单例（具名窗口类）：已有同用户同项目窗口 → 请求置顶后退出（不依赖 server 可达）。
		if bringWindowToTopByClass(flash.WindowClassName(flash.CurrentUser(), workDir)) {
			return "", true, nil
		}
		if flash.WorkDirFree(workDir) {
			return workDir, false, nil // 空闲 → 待打开（正式持锁在 runRemote）
		}
		// 已被本用户打开 → 置顶既有窗口后退出。
		if err := bringExistingToTop(workDir, opts); err == nil {
			return "", true, nil
		} else if !flash.IsForbidden(err) {
			return "", false, err
		}
		// 跨用户冲突：换目录继续选（清空显式 / 最近，强制走 picker）。
		slog.Warn("跨用户冲突，换目录继续选", "dir", workDir)
		workDirArg, recents = "", nil
	}
	return "", false, errors.New("多次选择的目录均不可用，已放弃")
}

// bringExistingToTop 连接服务端 + claim 自身身份，再发 `instance-bring-to-top{data_dir}`
// 请求置顶既有窗口（61 §4.1 ⑥）。实现下沉至 `flash.BringToTop`（与 flash-cli 共用）。
func bringExistingToTop(workDir string, opts RemoteOptions) error {
	return flash.BringToTop(workDir, opts.UDSPath, logfDebug)
}
