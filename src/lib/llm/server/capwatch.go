// T-21：capability 原语保存后立即生效（无需重启）——三级根全覆盖。
//
// ① 用户级 + 项目级 capability 根（12-数据层三级根中由 server 经 gateway dir 节点动态接入的
// 两级）加 fswatcher 监听：原语文件（*.tool.md/*.prompt.md/*.skill.md/*.resource.md）保存/新建/
// 删除 → 去抖合并 → 复用既有 servers/unregister + servers/register 重扫 dir 节点
// （见 Server.reconcileCapabilityNodes）→ 工具面数十毫秒内反映变化。
//
// ② 系统级 <exeDir>/capability（app 根，内嵌 self 节点承载）：同样监听，变更去抖后重跑
// RegisterContracts 到同一内嵌 go-sdk server + 复用既有 gateway/reload 刷新 self 节点 list
// （见 Server.reloadAppContracts）。只读语义不变——UI 不可改该根，仅外部/构建期更新后热生效。
//
// 零新增消息面主题、不重复广播既有 data-*-refresh（后者仅供 UI 列表刷新，与本链路互不相干）。
package server

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sgtdi/fswatcher"

	"github.com/chonkpilot/chonkpilot-data/persist"
)

// capNodeRef 是已登记的 capability dir 节点归属（T-21 重扫定位：实例 + 节点名 + 契约根）。
type capNodeRef struct {
	InstanceID string
	Name       string
	Root       string
}

// capWatchDebounce 变更合并去抖：一次保存常触发多个 fswatcher 事件（create/mod/chmod），
// 合并为单次重扫，避免重复重注册（"一次保存 → 一次 reconcile"）。
const capWatchDebounce = 60 * time.Millisecond

// capWatchReadyTimeout = 等待 fswatcher 初始路径就绪（WithReadyChannel）的兜底上限。
const capWatchReadyTimeout = 5 * time.Second

// capWatcher 监听 app + 用户/项目级 capability 根，变更去抖后触发对应重扫。
type capWatcher struct {
	s *Server
	w fswatcher.Watcher

	mu        sync.Mutex
	want      map[string]bool                 // 目标 capability 根（绝对、斜杠化）
	appWant   map[string]bool                 // 其中系统级 app 根（T-21②：走 RegisterContracts + gateway/reload 重扫）
	synced    map[string]fswatcher.WatchDepth // 已 AddPath 的根/祖先 → 监听深度（Nested / TopLevel）
	dirty     bool                            // 本轮是否有用户/项目级根内变更待重扫
	dirtyApp  bool                            // 本轮是否有 app 根内变更待重扫
	timer     *time.Timer
	done      chan struct{}
	closeOnce sync.Once // Close 幂等守卫（B-16：并发/重复 Close 不双重 close）

	cancel context.CancelFunc
	ready  <-chan struct{}

	// refs / instRoots 支撑实例退出时的监听回收（B-38：长驻 split 服务端下 want/synced
	// 只增不减 → 句柄随实例累积）。user/project 根可被多个实例共享 → 按引用计数，
	// 仅最后一个引用者退出才摘除；prjusr 根按实例唯一。
	refs      map[string]int             // 目标根 → 引用它的实例数
	instRoots map[string]map[string]bool // instanceID → 该实例登记的根集合（退出时精确回收，幂等）
}

// newCapWatcher 建立 watcher 并启动事件循环（失败返回错误，调用方降级）。
func newCapWatcher(s *Server) (*capWatcher, error) {
	// New 必须显式给至少一个路径（否则默认监听 cwd）；此处取用户级 capability 根或其最近
	// 已存在祖先（兜底系统临时目录），后续 syncLocked 按需增删目标根。
	initial := capInitialRoot(s)
	ready := make(chan struct{})
	w, err := fswatcher.New(
		fswatcher.WithPath(initial, fswatcher.WithDepth(fswatcher.WatchTopLevel)),
		fswatcher.WithCooldown(0), // 去抖由本结构自管（capWatchDebounce）
		fswatcher.WithSeverity(fswatcher.SeverityNone),
		fswatcher.WithReadyChannel(ready),
	)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	cw := &capWatcher{
		s:         s,
		w:         w,
		want:      make(map[string]bool),
		appWant:   make(map[string]bool),
		synced:    map[string]fswatcher.WatchDepth{normPath(initial): fswatcher.WatchTopLevel},
		refs:      make(map[string]int),
		instRoots: make(map[string]map[string]bool),
		done:      make(chan struct{}),
		cancel:    cancel,
		ready:     ready,
	}
	go cw.runWatcher(ctx)
	go cw.loop()
	return cw, nil
}

// capInitialRoot 返回 fswatcher 的初始监听路径（必须已存在）：用户级 capability 根 →
// 其最近已存在祖先 → 系统临时目录（测试/无 Home 兜底）。TopLevel 深度，避免误把大树整体递归接管。
func capInitialRoot(s *Server) string {
	if s != nil {
		root := normPath(persist.CapUserRoot(s.opts.UsrPath))
		if dirExists(root) {
			return root
		}
		if anc := nearestExistingDir(root); anc != "" {
			return anc
		}
	}
	return normPath(os.TempDir())
}

// runWatcher 驱动 fswatcher 主循环（阻塞至 ctx 取消 / Close）；失败只记日志
// （下次 instance-register 重建监听）。
func (cw *capWatcher) runWatcher(ctx context.Context) {
	if err := cw.w.Watch(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Warn("capability watcher stopped", "err", err)
	}
}

// waitReadyLocked 等待 fswatcher 初始路径就绪（AddPath/DropPath 仅运行中可用）；超时/停止则放弃。
// 调用方须持 cw.mu。
func (cw *capWatcher) waitReadyLocked() {
	timer := time.NewTimer(capWatchReadyTimeout)
	defer timer.Stop()
	select {
	case <-cw.ready:
	case <-cw.done:
	case <-timer.C:
	}
}

// watchApp 登记并监听系统级 app capability 根（T-21②：<exeDir>/capability，内嵌 self 节点承载；
// 只读语义不变——UI 不可改，仅外部/构建期更新后热生效）。
func (cw *capWatcher) watchApp(root string) {
	if cw == nil || root == "" {
		return
	}
	cw.mu.Lock()
	defer cw.mu.Unlock()
	r := normPath(root)
	cw.want[r] = true
	cw.appWant[r] = true
	cw.syncLocked()
}

// watchInstance 登记并监听某实例可见的用户级 + 项目级 + 项目私有级 capability 根（幂等）。
// 同一根可被多实例共享（user 根进程级、project 根按 workDir）→ 按实例集合去重并累加引用计数。
func (cw *capWatcher) watchInstance(instanceID, workDir string) {
	if cw == nil {
		return
	}
	cw.mu.Lock()
	defer cw.mu.Unlock()
	var roots []string
	roots = append(roots, normPath(persist.CapUserRoot(cw.s.opts.UsrPath)))
	if workDir != "" {
		roots = append(roots, normPath(persist.CapProjectRoot(workDir)))
	}
	if root := cw.s.prjUsrCapRoot(instanceID); root != "" {
		roots = append(roots, normPath(root)) // P4：项目私有级根（prjusr）纳入热生效
	}
	set := cw.instRoots[instanceID]
	if set == nil {
		set = map[string]bool{}
		cw.instRoots[instanceID] = set
	}
	for _, r := range roots {
		if set[r] {
			continue // 本实例已登记过 → 幂等，不重复计数
		}
		set[r] = true
		cw.refs[r]++
		cw.want[r] = true
	}
	cw.syncLocked()
}

// unwatchInstance 实例退出 → 回收该实例派生的 capability 根监听（B-38：want/synced 只增不减
// → 长驻 split 服务端下句柄随实例累积）。共享根按引用计数：仅最后一个引用者退出
// 才摘除 want 条目与 fswatcher 句柄。幂等（重复调用无副作用）；app 根不在本链路（进程级恒存在）。
func (cw *capWatcher) unwatchInstance(instanceID string) {
	if cw == nil || instanceID == "" {
		return
	}
	cw.mu.Lock()
	defer cw.mu.Unlock()
	set := cw.instRoots[instanceID]
	if set == nil {
		return
	}
	delete(cw.instRoots, instanceID)
	for r := range set {
		cw.refs[r]--
		if cw.refs[r] > 0 {
			continue // 仍有其他实例引用 → 保留监听
		}
		delete(cw.refs, r)
		delete(cw.want, r)
		// 引用归零 → 摘除该根及其祖先补位条目的监听（递归句柄随根一并收敛）
		for p := range cw.synced {
			if under(p, r) {
				cw.dropLocked(p)
			}
		}
	}
}

// Close 停止事件循环并关闭 fswatcher（幂等）。
func (cw *capWatcher) Close() {
	if cw == nil {
		return
	}
	// B-16：close(cw.done) 经 sync.Once 守卫，并发/重复 Close 不再双重 close panic。
	cw.closeOnce.Do(func() { close(cw.done) })
	cw.cancel()
	cw.w.Close()
	cw.mu.Lock()
	if cw.timer != nil {
		cw.timer.Stop()
		cw.timer = nil
	}
	cw.mu.Unlock()
}

// loop 读取 fswatcher 事件；Dropped 通道仅消费（丢弃不致命：事件面本身已去抖合并）；
// 通道关闭即退出（下次实例注册重建监听）。
func (cw *capWatcher) loop() {
	events := cw.w.Events()
	dropped := cw.w.Dropped()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			cw.handle(ev)
		case _, ok := <-dropped:
			if !ok {
				dropped = nil // 通道关闭 → 摘除该分支（避免零值忙轮询）
			}
		case <-cw.done:
			return
		}
	}
}

// handle 处理单个事件：Create/Remove/Rename 后补齐监听（fswatcher 递归自动覆盖新增子树）；
// 目标根内变更置脏并调度去抖重扫。祖先目录（根尚未创建时的监听点）内的无关文件
// （如 usr db / WebView2 profile）变动只做监听补齐，不触发重扫。
func (cw *capWatcher) handle(ev fswatcher.WatchEvent) {
	p := normPath(ev.Path)
	cw.mu.Lock()
	defer cw.mu.Unlock()
	if hasEventType(ev.Types, fswatcher.EventCreate) ||
		hasEventType(ev.Types, fswatcher.EventRemove) ||
		hasEventType(ev.Types, fswatcher.EventRename) {
		cw.syncLocked() // 目标根可能刚被创建/删除（此前监听其祖先）→ 收敛监听
	}
	if !cw.inWantLocked(p) {
		return
	}
	if cw.inAppLocked(p) {
		cw.dirtyApp = true // 系统级 app 根 → RegisterContracts + gateway/reload 重扫
	} else {
		cw.dirty = true // 用户/项目级根 → dir 节点重建
	}
	cw.armLocked()
}

// armLocked 重置去抖定时器（多次事件合并为一次 fire）。
func (cw *capWatcher) armLocked() {
	if cw.timer != nil {
		cw.timer.Stop()
	}
	cw.timer = time.AfterFunc(capWatchDebounce, cw.fire)
}

// fire 去抖到期：按脏标记触发对应重扫（用户/项目级 → dir 节点重建；app 根 → 契约重注册）。
func (cw *capWatcher) fire() {
	cw.mu.Lock()
	dirty, dirtyApp := cw.dirty, cw.dirtyApp
	cw.dirty, cw.dirtyApp = false, false
	cw.mu.Unlock()
	if dirty {
		cw.s.reconcileCapabilityNodes()
	}
	if dirtyApp {
		cw.s.reloadAppContracts()
	}
}

// syncLocked 保证每个目标根都有监听：根存在 → 递归监听（WatchNested）；不存在 → 监听最近的
// 已存在祖先（WatchTopLevel，根随后被创建时由 handle 捕获并收敛为递归监听）。
// 递归子树由 fswatcher 自动接管，不再逐目录展开（大 capability 树下无全树 ReadDir）。
func (cw *capWatcher) syncLocked() {
	for root := range cw.want {
		if dirExists(root) {
			cw.ensureWatchLocked(root, fswatcher.WatchNested)
			continue
		}
		// 根尚不存在 / 已被删除 → 摘除该根监听（避免目录重建后按陈旧句柄短路），改监听最近祖先。
		if _, ok := cw.synced[root]; ok {
			cw.dropLocked(root)
		}
		if anc := nearestExistingDir(root); anc != "" {
			cw.ensureWatchLocked(anc, fswatcher.WatchTopLevel)
		}
	}
}

// ensureWatchLocked 保证 path 以至少 depth 的深度被监听（幂等）。fswatcher 不允许重复
// AddPath，故 TopLevel → Nested 升级须先 DropPath 再 AddPath。
func (cw *capWatcher) ensureWatchLocked(path string, depth fswatcher.WatchDepth) {
	path = normPath(path)
	if cur, ok := cw.synced[path]; ok {
		if depth != fswatcher.WatchNested || cur == fswatcher.WatchNested {
			return
		}
		cw.dropLocked(path)
	}
	cw.waitReadyLocked()
	if err := cw.w.AddPath(path, fswatcher.WithDepth(depth)); err != nil {
		// 已由初始路径/其它根监听（synced 未覆盖的竞态）→ 登记深度，避免反复失败。
		if strings.Contains(err.Error(), "already being watched") {
			cw.synced[path] = depth
			return
		}
		slog.Debug("capability watch add failed", "path", path, "err", err)
		return
	}
	cw.synced[path] = depth
}

// dropLocked 摘除对 path 的监听并清理 synced 记录（未监听/未运行仅留痕）。
func (cw *capWatcher) dropLocked(path string) {
	cw.waitReadyLocked()
	if err := cw.w.DropPath(path); err != nil {
		slog.Debug("capability watch drop failed", "path", path, "err", err)
	}
	delete(cw.synced, path)
}

// inWantLocked 判定路径是否位于任一目标根内（含根自身）。
func (cw *capWatcher) inWantLocked(path string) bool {
	for root := range cw.want {
		if under(path, root) {
			return true
		}
	}
	return false
}

// inAppLocked 判定路径是否位于系统级 app capability 根内（含根自身）。
func (cw *capWatcher) inAppLocked(path string) bool {
	for root := range cw.appWant {
		if under(path, root) {
			return true
		}
	}
	return false
}

// hasEventType 判定事件类型集合是否含 t。
func hasEventType(types []fswatcher.EventType, t fswatcher.EventType) bool {
	for _, x := range types {
		if x == t {
			return true
		}
	}
	return false
}

// normPath 归一为斜杠绝对形式（比较用）。
func normPath(p string) string { return filepath.ToSlash(filepath.Clean(p)) }

// under 判定 path 是否等于 root 或位于 root 之下。
func under(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+"/")
}

// nearestExistingDir 返回 root 最近的已存在祖先目录（用于监听尚未创建的 capability 根）；
// 无可用祖先（走到卷根仍不存在）→ 空。
func nearestExistingDir(root string) string {
	for dir := filepath.Dir(root); ; {
		if dir == "" || dir == "." || dir == filepath.VolumeName(dir)+string(filepath.Separator) {
			return "" // 已到卷根仍不存在
		}
		if dirExists(dir) {
			return normPath(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
