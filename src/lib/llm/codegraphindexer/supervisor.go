// Package codegraphindexer 监管 resident codegraph indexer（P5）：
// **每个宿主进程至多一个** `chonkpilot-codegraph-indexer.exe`，随宿主启动/退出。
//
// 单实例多 workdir（2026-10-19）：indexer 不再按 work_dir 各起一个进程，而是由宿主经
// `plugin.index.workdirs` 提供当前活实例的去重 work_dir 列表，单进程轮询消费各项目队列。
// 生命周期交给 pluginhost.Manager（单实例锁 + 崩溃重启）；本包只负责把宿主数据根
// （单实例锁位置）/ 插件桥地址 / indexer exe 组装成 pluginhost.Spec，并把关闭动作
// 经宿主注入的 Shutdown 钩子转成 `ctl plugin.shutdown`（用闭包避免包依赖循环）。
package codegraphindexer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-llm/pluginhost"
)

// PluginID 是 resident codegraph indexer 的固定插件 id（单实例；多 workdir 在进程内轮询）。
const PluginID = "codegraph-indexer"

// ShutdownTimeout 是 indexer 优雅关闭的等待上限（与 plugin.shutdown grace_ms 对齐）。
const ShutdownTimeout = 5 * time.Second

// Options 是 indexer 启动参数（与 work_dir 无关：单实例服务宿主内全部项目）。
type Options struct {
	// DataRoot 是宿主数据根（单实例锁目录 = <DataRoot>/indexers/codegraph）。
	// 空 → 回落 `%LOCALAPPDATA%/chonkpilot/indexers/codegraph`。
	DataRoot string
	// PluginAddr 插件桥帧地址（必填）。
	PluginAddr string
	// IndexerExe indexer 可执行文件路径（必填）。
	IndexerExe string
	// Logf 日志出口（可选）。
	Logf func(format string, args ...any)
	// Shutdown 由宿主注入（如 pluginbridge.Server.ShutdownPlugin）：
	// 向指定插件 id 发送 plugin.shutdown，返回是否找到会话。
	Shutdown func(id string) bool
	// MaxRestarts 崩溃重启上限（0 = pluginhost 缺省 3）。
	MaxRestarts int
}

// Manager 监管本宿主进程的 indexer（单实例）。
type Manager struct {
	hosts *pluginhost.Manager
}

// New 构造监管器。
func New(logf func(format string, args ...any)) *Manager {
	return &Manager{hosts: pluginhost.NewManager(logf)}
}

// Ensure 确保本宿主的 codegraph indexer 在运行（已运行 → 直接返回）。
func (m *Manager) Ensure(ctx context.Context, opts Options) error {
	exe := strings.TrimSpace(opts.IndexerExe)
	if exe == "" {
		return errors.New("codegraphindexer: IndexerExe 不能为空")
	}
	addr := strings.TrimSpace(opts.PluginAddr)
	if addr == "" {
		return errors.New("codegraphindexer: PluginAddr 不能为空")
	}
	lockDir := lockDirOf(opts.DataRoot)
	spec := pluginhost.Spec{
		ID:         PluginID,
		Exe:        exe,
		WorkDir:    lockDir,
		IndexDir:   lockDir,
		NoLock:     true, // 单实例由 indexer 注册全局窗口类判定（2026-10-19）；索引并发按 workdir 加锁
		PluginAddr: addr,
		Env: []string{
			"CHONKPILOT_PLUGIN_ADDR=" + addr,
			"CHONKPILOT_PLUGIN_ID=" + PluginID,
		},
		MaxRestarts:     opts.MaxRestarts,
		ShutdownTimeout: ShutdownTimeout,
		Logf:            opts.Logf,
	}
	if opts.Shutdown != nil {
		spec.Shutdown = func(context.Context) error {
			opts.Shutdown(PluginID)
			return nil
		}
	}
	return m.hosts.Ensure(ctx, spec)
}

// StopAll 停止 indexer（幂等；宿主退出时先于插件桥关闭调用）。
func (m *Manager) StopAll() {
	m.hosts.StopAll()
}

// lockDirOf 返回单实例锁目录（os.MkdirAll 由 pluginhost 负责）。
func lockDirOf(dataRoot string) string {
	base := strings.TrimSpace(dataRoot)
	if base == "" {
		if d, err := os.UserCacheDir(); err == nil && strings.TrimSpace(d) != "" {
			base = filepath.Join(d, "chonkpilot")
		} else {
			base = os.TempDir()
		}
	}
	return filepath.Join(base, "indexers", "codegraph")
}
