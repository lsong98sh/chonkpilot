// T-21：capability 原语保存后立即生效（无需重启）——三级根全覆盖。
//
// ① 用户级 + 项目级 capability 根（12-数据层三级根中由 server 经 gateway dir 节点动态接入的
// 两级）加 fsnotify 监听：原语文件（*.tool.md/*.prompt.md/*.skill.md/*.resource.md）保存/新建/
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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/chonkpilot/chonkpilot-data/persist"
)

// capNodeRef 是已登记的 capability dir 节点归属（T-21 重扫定位：实例 + 节点名 + 契约根）。
type capNodeRef struct {
	InstanceID string
	Name       string
	Root       string
}

// capWatchDebounce 变更合并去抖：一次保存常触发多个 fsnotify 事件（create/write/chmod），
// 合并为单次重扫，避免重复重注册（"一次保存 → 一次 reconcile"）。
const capWatchDebounce = 60 * time.Millisecond

// capWatcher 监听 app + 用户/项目级 capability 根，变更去抖后触发对应重扫。
type capWatcher struct {
	s *Server
	w *fsnotify.Watcher

	mu        sync.Mutex
	want      map[string]bool // 目标 capability 根（绝对、斜杠化）
	appWant   map[string]bool // 其中系统级 app 根（T-21②：走 RegisterContracts + gateway/reload 重扫）
	watched   map[string]bool // 已 Add 的目录
	dirty     bool            // 本轮是否有用户/项目级根内变更待重扫
	dirtyApp  bool            // 本轮是否有 app 根内变更待重扫
	timer     *time.Timer
	done      chan struct{}
	closeOnce sync.Once // Close 幂等守卫（B-16：并发/重复 Close 不双重 close）
}

// newCapWatcher 建立 watcher 并启动事件循环（失败返回错误，调用方降级）。
func newCapWatcher(s *Server) (*capWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	cw := &capWatcher{
		s:       s,
		w:       w,
		want:    make(map[string]bool),
		appWant: make(map[string]bool),
		watched: make(map[string]bool),
		done:    make(chan struct{}),
	}
	go cw.loop()
	return cw, nil
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
func (cw *capWatcher) watchInstance(instanceID, workDir string) {
	if cw == nil {
		return
	}
	cw.mu.Lock()
	defer cw.mu.Unlock()
	cw.want[normPath(persist.CapUserRoot(cw.s.opts.UsrPath))] = true
	if workDir != "" {
		cw.want[normPath(persist.CapProjectRoot(workDir))] = true
	}
	if root := cw.s.prjUsrCapRoot(instanceID); root != "" {
		cw.want[normPath(root)] = true // P4：项目私有级根（prjusr）纳入热生效
	}
	cw.syncLocked()
}

// Close 停止事件循环并关闭 fsnotify（幂等）。
func (cw *capWatcher) Close() {
	if cw == nil {
		return
	}
	// B-16：close(cw.done) 经 sync.Once 守卫，并发/重复 Close 不再双重 close panic。
	cw.closeOnce.Do(func() { close(cw.done) })
	cw.mu.Lock()
	if cw.timer != nil {
		cw.timer.Stop()
		cw.timer = nil
	}
	cw.mu.Unlock()
	_ = cw.w.Close()
}

// loop 读取 fsnotify 事件；Errors 通道故障仅静默（下次实例注册会重建监听）。
func (cw *capWatcher) loop() {
	for {
		select {
		case ev, ok := <-cw.w.Events:
			if !ok {
				return
			}
			cw.handle(ev)
		case _, ok := <-cw.w.Errors:
			if !ok {
				return
			}
		case <-cw.done:
			return
		}
	}
}

// handle 处理单个事件：新建目录递归加监听；目标根内变更置脏并调度去抖重扫。
// 祖先目录（根尚未创建时的监听点）内的无关文件（如 usr db）变动只做监听补齐，不触发重扫。
func (cw *capWatcher) handle(ev fsnotify.Event) {
	p := normPath(ev.Name)
	cw.mu.Lock()
	defer cw.mu.Unlock()
	if ev.Op&fsnotify.Create != 0 {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			cw.addTreeLocked(p) // capability 树新增子目录 → 递归监听
		}
	}
	if ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
		// B-28：目录被删除/改名时 fsnotify 句柄随之失效（Windows 自动移除底层监听），
		// 须按该路径前缀清理 watched 中相关条目（含目录树下子项），否则目录重建后
		// syncLocked→addTreeLocked→addDirLocked 被 stale `watched[dir]=true` 短路，
		// 不再 w.Add → **永久失去监听**（热生效静默失效直到重启）。
		cw.removeWatchLocked(p)
	}
	cw.syncLocked() // 目标根可能刚被创建（此前监听其祖先）→ 补齐监听
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

// syncLocked 保证每个目标根都有监听：根存在 → 递归监听；不存在 → 监听最近的已存在祖先
// （根随后被创建时可在 handle 中捕获并切换监听）。
func (cw *capWatcher) syncLocked() {
	for root := range cw.want {
		if dirExists(root) {
			cw.addTreeLocked(root)
			continue
		}
		if anc := nearestExistingDir(root); anc != "" {
			cw.addDirLocked(anc)
		}
	}
}

// addDirLocked 添加对 dir 的监听（幂等）；已监听返回 false。
func (cw *capWatcher) addDirLocked(dir string) bool {
	dir = normPath(dir)
	if cw.watched[dir] {
		return false
	}
	if err := cw.w.Add(dir); err != nil {
		delete(cw.watched, dir) // B-28 兜底：w.Add 失败 → 清除可能残留的 stale 记录，保证后续可重试
		return false
	}
	cw.watched[dir] = true
	return true
}

// removeWatchLocked 从 watched 清理 path 及其子树条目（B-28：目录删除/改名后 fsnotify 句柄
// 自动失效，须同步移除，否则重建时被 stale `watched` 记录短路而永久失去监听）。
func (cw *capWatcher) removeWatchLocked(path string) {
	for d := range cw.watched {
		if under(d, path) {
			delete(cw.watched, d)
		}
	}
}

// addTreeLocked 递归监听 dir 及其子目录（跳过隐藏项）。
func (cw *capWatcher) addTreeLocked(dir string) {
	cw.addDirLocked(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		cw.addTreeLocked(filepath.Join(dir, e.Name()))
	}
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
