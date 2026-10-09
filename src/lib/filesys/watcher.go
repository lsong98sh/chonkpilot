// 变更跟踪：fsnotify watch + 60ms 合并去抖 → 广播 filesys.changed / filesys.watch-error
// （61-消息一览 §2.4：单文件 + 目录批次合一广播）。
package filesys

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/fsnotify/fsnotify"
)

const debounce = 60 * time.Millisecond

// maxWait 是一个去抖窗口的最大等待（首事件起算，D-37）：被 watch 目录内持续写入时，
// 若每个事件都把触发时刻重置为「现在 + debounce」，processBatch 会被无限推迟（批次广播饥饿）。
// 故窗口内首个事件起算固定上限 maxWait，到点即强制处理（不再无限重置）。
const maxWait = 10 * debounce

// watchManager 按 work_dir 组织 fsnotify watcher。
type watchManager struct {
	bus      mq.Bus
	mu       sync.Mutex
	watchers map[string]*dirWatcher // key = work_dir（绝对）

	// cfgMu 保护「不显示的目录」清单缓存（与 m.mu 无嵌套：先 cfgMu 后 m.mu 或反之均不互相持有）。
	cfgMu   sync.Mutex
	hidden  map[string][]string // work_dir → 已加载的隐藏目录清单
	loading map[string]bool     // work_dir → 已发起一次性异步加载（去重）
}

func newWatchManager(bus mq.Bus) *watchManager {
	return &watchManager{
		bus:      bus,
		watchers: make(map[string]*dirWatcher),
		hidden:   make(map[string][]string),
		loading:  make(map[string]bool),
	}
}

func (m *watchManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, dw := range m.watchers {
		dw.stop()
	}
	m.watchers = make(map[string]*dirWatcher)
}

// watch 声明关注范围：work_dir 下 watch abs 路径（recursive 时递归子目录）。
// instanceID = 声明者实例（桥注入）；记入 dirWatcher，广播 filesys.changed/watch-error 时携带。
func (m *watchManager) watch(workDir, abs string, recursive bool, instanceID string) error {
	hidden := m.hiddenDirsOf(workDir)
	m.mu.Lock()
	dw, ok := m.watchers[workDir]
	if !ok {
		var err error
		dw, err = newDirWatcher(workDir, m.bus, hidden)
		if err != nil {
			m.mu.Unlock()
			return err
		}
		m.watchers[workDir] = dw
	}
	m.mu.Unlock()
	dw.addDeclarer(instanceID)
	return dw.add(abs, recursive)
}

// unwatch 取消对 abs 的 watch。
func (m *watchManager) unwatch(workDir, abs string) {
	m.mu.Lock()
	dw, ok := m.watchers[workDir]
	m.mu.Unlock()
	if !ok {
		return
	}
	dw.remove(abs)
}

// unwatchInstance 实例退出（instance-exit）时反登记其声明者（D-39）：声明者集只增不删会让
// 已退出实例**永远**收到后续事件，且 watcher 无法随实例回收。若本 work_dir 再无声明者，
// 则停止并移出该 watcher（避免空转 + 泄漏）。路径级 unwatch（unwatch）的既有取舍不变——
// 它只移出路径、不反登记声明者，以免误删仍关注其它路径的声明者（见 addDeclarer 注释）。
func (m *watchManager) unwatchInstance(workDir, instanceID string) {
	if workDir == "" || instanceID == "" {
		return
	}
	m.mu.Lock()
	dw, ok := m.watchers[workDir]
	if !ok {
		m.mu.Unlock()
		return
	}
	empty := dw.removeDeclarer(instanceID)
	if empty {
		delete(m.watchers, workDir)
	}
	m.mu.Unlock()
	if empty {
		dw.stop() // 无声明者 → 停 watcher（在解锁后调用，避免持 m.mu 关闭）
	}
}

// ─── 「不显示的目录」清单（层2：显示与监听同源）─────────────────────────

// hiddenDirsOf 取某 work_dir 的隐藏目录清单（未加载 → 缺省清单）。
func (m *watchManager) hiddenDirsOf(workDir string) []string {
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()
	if v, ok := m.hidden[workDir]; ok {
		return v
	}
	return defaultHiddenDirs
}

// ensureLoaded 首次为 workDir 发起一次性异步加载隐藏目录清单（跨实例同 work_dir 复用）。
// 异步（不阻塞 watch/list 请求）；失败保持缺省并允许下次重试。instanceID 用于按实例绑定解析项目配置。
func (m *watchManager) ensureLoaded(workDir, instanceID string) {
	if workDir == "" || instanceID == "" {
		return
	}
	m.cfgMu.Lock()
	if _, ok := m.hidden[workDir]; ok {
		m.cfgMu.Unlock()
		return
	}
	if m.loading[workDir] {
		m.cfgMu.Unlock()
		return
	}
	m.loading[workDir] = true
	m.cfgMu.Unlock()
	go func() {
		val, err := readPrjConfigKey(m.bus, instanceID, hiddenDirsKey)
		m.cfgMu.Lock()
		delete(m.loading, workDir)
		_, applied := m.hidden[workDir]
		m.cfgMu.Unlock()
		if err != nil {
			// 留痕（可观测性）：配置不可读 ≠ 应停止 watch → 保持缺省，下次 watch/list 重试。
			slog.Warn("filesys hide-dirs config load failed", "work_dir", workDir, "err", err)
			return
		}
		if applied {
			return // 加载期间已由 refresh 写入更新值 → 不覆盖（避免陈旧初值压掉新值）
		}
		m.applyHidden(workDir, parseHiddenDirs(val))
	}()
}

// applyHidden 应用某 work_dir 的新隐藏目录清单：更新缓存，并对该 work_dir 的活跃 watcher
// 重新过滤（现已隐藏 → 摘除；由隐藏转可见 → 补齐）。**未变化时内部短路**（refilter 自比较）。
func (m *watchManager) applyHidden(workDir string, list []string) {
	m.cfgMu.Lock()
	m.hidden[workDir] = list
	m.cfgMu.Unlock()
	m.mu.Lock()
	dw := m.watchers[workDir]
	m.mu.Unlock()
	if dw != nil {
		dw.refilter(list)
	}
}

// dirWatcher 单个 work_dir 的 fsnotify watcher（60ms 合并去抖）。
type dirWatcher struct {
	workDir string
	bus     mq.Bus
	w       *fsnotify.Watcher

	paths   map[string]bool // 已 watch 的绝对路径（防重）
	pathsMu sync.Mutex

	// hidden = 当前生效的「不显示的目录」清单（目录名匹配）；walkAdd 递归过滤用，
	// 与文件树显示过滤（filesys.go shouldHideDirName）**同一判据**（层2 同源）。
	hidden   []string
	hiddenMu sync.RWMutex

	// declarers = 声明者 instance_id 集合（filesys.watch 请求携带，桥注入）。业务 payload
	// 一律必带 instance_id（61-消息一览 §0.1）；同一 work_dir 被多实例声明时按声明者
	// 逐个各发一份事件（各自带上自己的 id），保持"所有订阅者仍能收到"的既有可见行为。
	declarers   map[string]bool
	declarersMu sync.Mutex

	queue   map[string]fsnotify.Event
	queueMu sync.Mutex
	timer   *time.Timer
	// windowStart = 当前去抖窗口首个事件的时刻（首事件起算 maxWait 上限，D-37）；
	// 零值 = 无活跃窗口。受 queueMu 保护，processBatch 处理完一批即复位。
	windowStart time.Time
	done        chan struct{}
}

func newDirWatcher(workDir string, bus mq.Bus, hidden []string) (*dirWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	dw := &dirWatcher{
		workDir:   workDir,
		bus:       bus,
		w:         w,
		paths:     make(map[string]bool),
		hidden:    hidden,
		declarers: make(map[string]bool),
		queue:     make(map[string]fsnotify.Event),
		done:      make(chan struct{}),
	}
	go dw.loop()
	return dw, nil
}

// addDeclarer 登记声明者 instance_id（同一 work_dir 可被多实例声明，各自的 watch 请求都登记）。
// 取舍：**路径级** unwatch 不反登记声明者 —— 避免因某路径 unwatch 而误删仍关注其它路径的
// 声明者，保持既有可见行为。**实例退出**（instance-exit）则须反登记，见 unwatchInstance（D-39）。
func (dw *dirWatcher) addDeclarer(instanceID string) {
	if instanceID == "" {
		return
	}
	dw.declarersMu.Lock()
	dw.declarers[instanceID] = true
	dw.declarersMu.Unlock()
}

// removeDeclarer 反登记声明者 instance_id（实例退出用，D-39）；返回反登记后是否已无声明者
// （true → 调用方可停止 watcher）。空 id 视为无操作（返回 false）。
func (dw *dirWatcher) removeDeclarer(instanceID string) bool {
	if instanceID == "" {
		return false
	}
	dw.declarersMu.Lock()
	delete(dw.declarers, instanceID)
	empty := len(dw.declarers) == 0
	dw.declarersMu.Unlock()
	return empty
}

// hiddenDirs 当前生效的隐藏目录清单（快照，调用方只读）。
func (dw *dirWatcher) hiddenDirs() []string {
	dw.hiddenMu.RLock()
	defer dw.hiddenMu.RUnlock()
	return dw.hidden
}

// emit 广播变更事件：按声明者逐个各发一份（各自 payload 带自己的 instance_id）。
// 无声明者（桥未注入 instance_id，如旧脚本/测试直发）时退化为单发、不带 instance_id。
func (dw *dirWatcher) emit(subject string, payload map[string]any) {
	dw.declarersMu.Lock()
	ids := make([]string, 0, len(dw.declarers))
	for id := range dw.declarers {
		ids = append(ids, id)
	}
	dw.declarersMu.Unlock()
	if len(ids) == 0 {
		raw, _ := json.Marshal(payload)
		_ = dw.bus.Emit(context.Background(), subject, raw)
		return
	}
	for _, id := range ids {
		p := make(map[string]any, len(payload)+1)
		for k, val := range payload {
			p[k] = val
		}
		p["instance_id"] = id
		raw, _ := json.Marshal(p)
		_ = dw.bus.Emit(context.Background(), subject, raw)
	}
}

// add 添加一个路径（目录或文件）；recursive 时递归子目录（过滤不显示的目录）。
func (dw *dirWatcher) add(abs string, recursive bool) error {
	// 「进表」与 fsnotify.Add 在同一临界区内完成：与 removeTree 的「出表 + fsnotify.Remove」
	// 原子配对，避免二者交错留下「fsnotify 在监听、paths 无记录」的悬挂（层1 并发要求）。
	dw.pathsMu.Lock()
	if dw.paths[abs] {
		dw.pathsMu.Unlock()
		return nil
	}
	if err := dw.w.Add(abs); err != nil {
		dw.pathsMu.Unlock()
		return err
	}
	dw.paths[abs] = true
	dw.pathsMu.Unlock()

	if recursive {
		fi, err := os.Stat(abs)
		if err == nil && fi.IsDir() {
			if err := dw.walkAdd(abs); err != nil {
				// 留痕（D-40）：递归添加子目录失败不该静默（否则部分子目录漏 watch 无从察觉）。
				slog.Warn("filesys watch walkAdd failed", "work_dir", dw.workDir, "path", abs, "err", err)
			}
		}
	}
	return nil
}

// walkAdd 递归添加子目录（目录内文件无需单独 Add，fsnotify 目录事件覆盖）。
// 过滤「不显示的目录」（shouldHideDirName：点开头 + 隐藏清单）——与文件树显示同源（层2）。
func (dw *dirWatcher) walkAdd(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	hidden := dw.hiddenDirs()
	for _, e := range entries {
		if !e.IsDir() || shouldHideDirName(e.Name(), hidden) {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		if err := dw.add(sub, true); err != nil {
			// 留痕（D-40）：单子目录添加失败继续处理其余，但记一笔（否则漏 watch 静默）。
			slog.Warn("filesys watch add subdir failed", "work_dir", dw.workDir, "path", sub, "err", err)
			continue
		}
	}
	return nil
}

func (dw *dirWatcher) remove(abs string) {
	dw.pathsMu.Lock()
	if !dw.paths[abs] {
		dw.pathsMu.Unlock()
		return
	}
	delete(dw.paths, abs)
	err := dw.w.Remove(abs)
	dw.pathsMu.Unlock()
	if err != nil {
		// 留痕（D-40）：移除失败不改可见行为（该路径已出表），但静默会掩盖 watcher 状态异常。
		slog.Warn("filesys watch remove failed", "work_dir", dw.workDir, "path", abs, "err", err)
	}
}

// removeTree 按前缀摘除 abs 自身及其**所有子孙**的 watch 条目（D-39 层1）。
// paths 只增不删会随目录反复重建累积泄漏（Java 项目 target/）；对已消失路径的 watch 亦无意义。
// 判据 = paths 中登记条目的路径前缀匹配（paths 中登记的恒为我们主动 watch 的目录，可靠）。
// 「出表 + fsnotify.Remove」在同一临界区内完成（与 add 配对，见 add 注释）。
func (dw *dirWatcher) removeTree(abs string) {
	prefix := abs + string(filepath.Separator)
	dw.pathsMu.Lock()
	for p := range dw.paths {
		if p == abs || strings.HasPrefix(p, prefix) {
			delete(dw.paths, p)
			if err := dw.w.Remove(p); err != nil {
				// 留痕（D-40）：移除失败不改可见行为（路径已出表），但静默会掩盖 watcher 状态异常。
				slog.Warn("filesys watch remove failed", "work_dir", dw.workDir, "path", p, "err", err)
			}
		}
	}
	dw.pathsMu.Unlock()
}

// refilter 按新的隐藏目录清单重新校准已 watch 集合（配置变更生效，层2）：
//   - 摘除「现已隐藏」的目录及其子孙（root 除外 —— 用户显式声明的 work_dir 本身不因清单误摘）；
//   - 对仍在册的目录重跑 walkAdd，补齐「由隐藏转可见」的子树（幂等，已在册者跳过）。
func (dw *dirWatcher) refilter(hidden []string) {
	dw.hiddenMu.Lock()
	prev := dw.hidden
	dw.hidden = hidden
	dw.hiddenMu.Unlock()
	if sameStrings(prev, hidden) {
		return
	}
	root := filepath.Clean(dw.workDir)
	dw.pathsMu.Lock()
	snap := make([]string, 0, len(dw.paths))
	for p := range dw.paths {
		snap = append(snap, p)
	}
	dw.pathsMu.Unlock()
	for _, p := range snap {
		if strings.EqualFold(filepath.Clean(p), root) {
			continue // 不摘除 work_dir 根
		}
		if shouldHideDirName(filepath.Base(p), hidden) {
			dw.removeTree(p)
		}
	}
	// 重新递归添加：补齐现已可见的子树（walkAdd → add 对已在册路径幂等短路）。
	dw.pathsMu.Lock()
	snap = snap[:0]
	for p := range dw.paths {
		snap = append(snap, p)
	}
	dw.pathsMu.Unlock()
	for _, p := range snap {
		if err := dw.walkAdd(p); err != nil {
			slog.Warn("filesys watch refilter walkAdd failed", "work_dir", dw.workDir, "path", p, "err", err)
		}
	}
}

// loop 读取 fsnotify 事件 → 入队去重 → 60ms 合并处理；Errors 通道故障 → 广播 watch-error。
func (dw *dirWatcher) loop() {
	for {
		select {
		case ev, ok := <-dw.w.Events:
			if !ok {
				return
			}
			dw.enqueue(ev)
		case werr, ok := <-dw.w.Errors:
			if !ok {
				continue
			}
			dw.publishWatchError(werr)
		case <-dw.done:
			return
		}
	}
}

// publishWatchError 广播 filesys.watch-error（watcher 运行期故障，非请求级错误）。
func (dw *dirWatcher) publishWatchError(err error) {
	if err == nil {
		return
	}
	dw.emit(msgkeys.TopicFilesysWatchError, map[string]any{
		"work_dir": dw.workDir,
		"error":    err.Error(),
	})
}

func (dw *dirWatcher) enqueue(ev fsnotify.Event) {
	dw.queueMu.Lock()
	dw.queue[ev.Name] = ev
	dw.queueMu.Unlock()
	dw.armTimer()
}

// nextWait 计算窗口内下次触发的等待：默认 debounce；但自首事件起算不得越过 maxWait——
// 剩余不足 debounce 时取剩余（剩余 <=0 → 0，立即触发）。纯函数，便于断言（D-37）。
func nextWait(elapsed time.Duration) time.Duration {
	if remaining := maxWait - elapsed; remaining < debounce {
		if remaining < 0 {
			return 0
		}
		return remaining
	}
	return debounce
}

// armTimer 重排本窗口的触发计时器：每事件仍做 debounce 重置，但以首事件（windowStart）
// 起算的 maxWait 封顶（D-37）——持续写入下窗口不再被无限推后，到点即触发 processBatch。
func (dw *dirWatcher) armTimer() {
	dw.queueMu.Lock()
	now := time.Now()
	if dw.windowStart.IsZero() {
		dw.windowStart = now
	}
	wait := nextWait(now.Sub(dw.windowStart))
	if dw.timer != nil {
		dw.timer.Stop()
	}
	dw.timer = time.AfterFunc(wait, dw.processBatch)
	dw.queueMu.Unlock()
}

// processBatch 合并事件：广播 filesys.changed（单文件） + 目录批次。
func (dw *dirWatcher) processBatch() {
	// 取批 + 复位本窗口状态在同一临界区内完成（原子）：否则「取批后、复位前」到达的事件
	// 会先 armTimer 排好计时器，又被复位清掉 → 该事件无计时器兜底，批次饥饿。
	dw.queueMu.Lock()
	batch := dw.queue
	dw.queue = make(map[string]fsnotify.Event)
	if dw.timer != nil {
		dw.timer.Stop()
		dw.timer = nil
	}
	dw.windowStart = time.Time{} // 本窗口结束：下一批重新起算 maxWait（D-37）
	dw.queueMu.Unlock()
	if len(batch) == 0 {
		return
	}
	dirs := make(map[string]bool)
	for path, ev := range batch {
		op := operation(ev.Op)
		dw.pubChanged(path, op)

		// 目录删除 / 重命名（D-39 层1）→ 摘除该路径及其子孙的 watch（paths 只增不删会泄漏）。
		// 文件事件无对应 watch 条目 → removeTree 为无操作；重命名到新路径由随后的 Create 补齐。
		if ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
			dw.removeTree(path)
		}

		// 新目录（recursive 语义）→ 递归添加；「不显示的目录」不监听（与文件树显示同源，层2）。
		if ev.Op&fsnotify.Create != 0 {
			if fi, err := os.Stat(path); err == nil && fi.IsDir() && !shouldHideDirName(filepath.Base(path), dw.hiddenDirs()) {
				dw.pathsMu.Lock()
				known := dw.paths[path]
				dw.pathsMu.Unlock()
				if !known {
					if err := dw.add(path, true); err != nil {
						// 留痕（D-40）：新建目录递归 watch 失败须留痕（否则新子目录漏 watch 静默）。
						slog.Warn("filesys watch new dir failed", "work_dir", dw.workDir, "path", path, "err", err)
					}
				}
			}
		}
		// 刷新目录 = 事件对象父目录；事件对象本身是目录时刷新自身
		dir := filepath.Dir(path)
		if fi, err := os.Stat(path); err == nil && fi.IsDir() {
			dir = path
			// 新建目录：其**父目录**也须刷新批次（父 children 需含新目录节点；
			// 仅刷新新目录自身时父批次缺失，前端树不会插入该目录节点，I-43）。
			if ev.Op&fsnotify.Create != 0 {
				dirs[filepath.Dir(path)] = true
			}
		}
		dirs[dir] = true
	}
	for dir := range dirs {
		dw.pubDirChanged(dir)
	}
	// 处理期间到达的新事件由 enqueue→armTimer 重排一轮（此时 windowStart 已复位 → 起新窗口），
	// 正常重排程已完整覆盖，故此处无需再调度（D-30：原 `dw.timer == nil` 分支永假，且在
	// stop() 后置 timer=nil 时反而会对已停 watcher 再调度一轮，删除）。
}

func operation(op fsnotify.Op) string {
	switch {
	case op&fsnotify.Create != 0:
		return "create"
	case op&fsnotify.Write != 0:
		return "write"
	case op&fsnotify.Remove != 0:
		return "remove"
	case op&fsnotify.Rename != 0:
		return "rename"
	default:
		return "write"
	}
}

func (dw *dirWatcher) pubChanged(abs, op string) {
	dw.emit(msgkeys.TopicFilesysChanged, map[string]any{
		"work_dir":  dw.workDir,
		"path":      filepath.ToSlash(abs),
		"operation": op,
	})
}

func (dw *dirWatcher) pubDirChanged(dir string) {
	children := listDirNodes(dir, dw.hiddenDirs())
	dw.emit(msgkeys.TopicFilesysChanged, map[string]any{
		"work_dir": dw.workDir,
		"path":     filepath.ToSlash(dir),
		"children": children,
	})
}

func (dw *dirWatcher) stop() {
	dw.queueMu.Lock()
	if dw.timer != nil {
		dw.timer.Stop()
		dw.timer = nil
	}
	dw.queueMu.Unlock()
	select {
	case <-dw.done:
	default:
		close(dw.done)
	}
	_ = dw.w.Close()
}
