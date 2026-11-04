// 变更跟踪：fswatcher watch + 60ms 合并去抖 → 广播 filesys.changed / filesys.watch-error
// （61-消息一览 §2.4：单文件 + 目录批次合一广播）。
package filesys

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
	"github.com/chonkpilot/chonkpilot-workspace"
	wslayout "github.com/chonkpilot/chonkpilot-workspace/layout"
	"github.com/sgtdi/fswatcher"
)

const debounce = 60 * time.Millisecond

// maxWait 是一个去抖窗口的最大等待（首事件起算，D-37）：被 watch 目录内持续写入时，
// 若每个事件都把触发时刻重置为「现在 + debounce」，processBatch 会被无限推迟（批次广播饥饿）。
// 故窗口内首个事件起算固定上限 maxWait，到点即强制处理（不再无限重置）。
const maxWait = 10 * debounce

// readyTimeout = 等待 fswatcher 初始路径就绪（WithReadyChannel）的兜底上限。
const readyTimeout = 5 * time.Second

// watchManager 按 work_dir 组织 fswatcher watcher：工作区打开时建立**一次**，关闭时撤销。
type watchManager struct {
	bus mq.Bus
	pm  *workspace.Manager
	mu  sync.Mutex
	watchers map[string]*dirWatcher // key = work_dir（绝对）

	// cfgMu 保护「不显示的目录」清单缓存（与 m.mu 无嵌套：先 cfgMu 后 m.mu 或反之均不互相持有）。
	cfgMu   sync.Mutex
	hidden  map[string][]string // work_dir → 已加载的隐藏目录清单
	loading map[string]bool     // work_dir → 已发起一次性异步加载（去重）
}

func newWatchManager(bus mq.Bus, pm *workspace.Manager) *watchManager {
	return &watchManager{
		bus:      bus,
		pm:       pm,
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

// open 工作区可用：为该 work_dir 建立**一次**监听。
//
// 监听根 = 工作区根，子树由 fswatcher 递归接管（不再按前端展开逐目录声明）。
// instanceID 仅用于异步加载「不显示的目录」配置（按实例解析 prj 层）。
func (m *watchManager) open(workDir, instanceID string) error {
	hidden := m.hiddenDirsOf(workDir)
	m.mu.Lock()
	dw, ok := m.watchers[workDir]
	if !ok {
		var err error
		dw, err = newDirWatcher(workDir, m.bus, hidden, m.pm)
		if err != nil {
			m.mu.Unlock()
			return err
		}
		m.watchers[workDir] = dw
	}
	m.mu.Unlock()
	m.ensureLoaded(workDir, instanceID)
	return dw.addRoot()
}

// close 工作区关闭：停止并移出 watcher（**不删盘、不清索引**），同步清理隐藏目录缓存。
func (m *watchManager) close(workDir string) {
	m.mu.Lock()
	dw := m.watchers[workDir]
	delete(m.watchers, workDir)
	m.mu.Unlock()
	if dw != nil {
		dw.stop()
	}
	m.cfgMu.Lock()
	delete(m.hidden, workDir)
	delete(m.loading, workDir)
	m.cfgMu.Unlock()
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
		// 须校验该 work_dir 仍有活跃 watcher：加载期间可能已被 unwatchInstance 清理（无声明者 →
		// 停并移出 watcher + 删 hidden）——此时**不得**重建无 watcher 的 hidden 项（否则随出现过的
		// work_dir 无界增长）。校验与写入无法并入同一临界区（cfgMu 与 m.mu 不嵌套，见 watchManager
		// 注释），故采用**写入后复验、无 watcher 则撤销**：无论与 unwatchInstance 如何交错，最终
		// 都不会残留「无 watcher 的 hidden 项」（若仍按「先复验后写入」，复验与写入之间的窗口内
		// unwatchInstance 删掉 hidden 后本处又重建 → 残留）。
		list := parseHiddenDirs(val)
		m.applyHidden(workDir, list)
		m.mu.Lock()
		_, stillWatched := m.watchers[workDir]
		m.mu.Unlock()
		if !stillWatched {
			m.cfgMu.Lock()
			// 仅当仍为本处写入的值时才删（避免误删并发 refresh 写入的更新值）。
			if cur, ok := m.hidden[workDir]; ok && sameStrings(cur, list) {
				delete(m.hidden, workDir)
			}
			m.cfgMu.Unlock()
		}
	}()
}

// applyHidden 应用某 work_dir 的新隐藏目录清单：更新缓存，并对该 work_dir 的活跃 watcher
// 重新过滤。fswatcher 的递归监听不按目录名增删（递归接管子树），故 refilter 只更新清单；
// 事件过滤在 processBatch 按清单逐事件判定（不广播、不入队）。
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

// pendingEvent = 去抖批次中的一条合并事件（同一路径的事件类型取并集）。
type pendingEvent struct {
	path  string
	types []fswatcher.EventType
}

// dirWatcher 单个 work_dir 的 fswatcher watcher（60ms 合并去抖）。
type dirWatcher struct {
	workDir string
	bus     mq.Bus
	w          fswatcher.Watcher
	cancel     context.CancelFunc
	ready      <-chan struct{}

	// rootAdded 标记工作区根是否已登记递归监听（AddPath 幂等，但仍避免重复升级）。
	rootMu    sync.Mutex
	rootAdded bool
	// pm 取工作区当前活动实例（事件定向投递用；实例集合由 PM 唯一持有）。
	pm *workspace.Manager

	// hidden = 当前生效的「不显示的目录」清单（目录名匹配）；事件过滤用（入队前 + processBatch 兜底），
	// 与文件树显示过滤（filesys.go shouldHideDirName）**同一判据**（层2 同源）。
	hidden   []string
	hiddenMu sync.RWMutex

	// droppedHidden 统计「入队前被丢弃」的隐藏目录事件数（G-86 ① 回归断言用）。
	droppedHidden atomic.Int64

	queue   map[string]pendingEvent
	queueMu sync.Mutex
	timer   *time.Timer
	// windowStart = 当前去抖窗口首个事件的时刻（首事件起算 maxWait 上限，D-37）；
	// 零值 = 无活跃窗口。受 queueMu 保护，processBatch 处理完一批即复位。
	windowStart time.Time
	done        chan struct{}
}

func newDirWatcher(workDir string, bus mq.Bus, hidden []string, pm *workspace.Manager) (*dirWatcher, error) {
	workDir = filepath.Clean(workDir)
	ready := make(chan struct{})
	// 初始路径 = work_dir 顶层（保证 watcher 可启动）；递归由 addRoot → AddPath 升级。
	w, err := fswatcher.New(
		fswatcher.WithPath(workDir, fswatcher.WithDepth(fswatcher.WatchTopLevel)),
		fswatcher.WithCooldown(0), // 去抖由本结构自管（60ms + maxWait，D-37）
		fswatcher.WithSeverity(fswatcher.SeverityNone),
		fswatcher.WithReadyChannel(ready),
	)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	dw := &dirWatcher{
		workDir: workDir,
		bus:     bus,
		pm:      pm,
		w:       w,
		cancel:  cancel,
		ready:   ready,
		hidden:  hidden,
		queue:   make(map[string]pendingEvent),
		done:    make(chan struct{}),
	}
	go dw.runWatcher(ctx)
	go dw.loop()
	return dw, nil
}

// runWatcher 驱动 fswatcher 主循环（阻塞至 ctx 取消 / Close）。
func (dw *dirWatcher) runWatcher(ctx context.Context) {
	if err := dw.w.Watch(ctx); err != nil && !errors.Is(err, context.Canceled) {
		dw.publishWatchError(fmt.Errorf("watch: %w", err))
	}
}

// waitReady 等待 fswatcher 初始路径就绪（就绪后 AddPath/DropPath 才可用）；超时/停止则放弃。
func (dw *dirWatcher) waitReady() {
	timer := time.NewTimer(readyTimeout)
	defer timer.Stop()
	select {
	case <-dw.ready:
	case <-dw.done:
	case <-timer.C:
	}
}

// addRoot 把工作区根登记为**递归**监听（整棵子树由 fswatcher 接管）。
// 幂等：已登记则直接返回。
func (dw *dirWatcher) addRoot() error {
	dw.waitReady()
	dw.rootMu.Lock()
	defer dw.rootMu.Unlock()
	if dw.rootAdded {
		return nil
	}
	// 顶层 → 递归升级：fswatcher 不允许重复 AddPath，须先摘除再按递归重新加入。
	if err := dw.w.DropPath(dw.workDir); err != nil {
		slog.Debug("filesys watch drop for upgrade failed", "work_dir", dw.workDir, "err", err)
	}
	if err := dw.w.AddPath(dw.workDir, fswatcher.WithDepth(fswatcher.WatchNested)); err != nil {
		return err
	}
	dw.rootAdded = true
	return nil
}

// hiddenDirs 当前生效的隐藏目录清单（快照，调用方只读）。
func (dw *dirWatcher) hiddenDirs() []string {
	dw.hiddenMu.RLock()
	defer dw.hiddenMu.RUnlock()
	return dw.hidden
}

// emit 广播变更事件：按工作区当前活动实例逐个各发一份（各自 payload 带自己的 instance_id）。
// 无活动实例（如测试直驱）时退化为单发、不带 instance_id。
func (dw *dirWatcher) emit(subject string, payload map[string]any) {
	var ids []string
	if dw.pm != nil {
		ids = dw.pm.InstanceIDs(dw.workDir)
	}
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

// refilter 应用新的隐藏目录清单（配置变更生效，层2）：fswatcher 递归监听保持不变，
// 事件在**入队前**按新清单逐事件丢弃（现已隐藏 → 不进队列；由隐藏转可见 → 恢复入队/广播）。
func (dw *dirWatcher) refilter(hidden []string) {
	dw.hiddenMu.Lock()
	dw.hidden = hidden
	dw.hiddenMu.Unlock()
}

// hiddenPath 判定事件路径是否落在隐藏目录内（相对 workDir 的任一段命中隐藏目录名）。
// 命中 → 跳过该事件（不广播、不入队）：与文件树显示过滤同源（层2）。
func (dw *dirWatcher) hiddenPath(abs string) bool {
	rel, err := filepath.Rel(dw.workDir, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	hidden := dw.hiddenDirs()
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == "" || seg == "." {
			continue
		}
		if shouldHideDirName(seg, hidden) {
			return true
		}
	}
	return false
}

// loop 读取 fswatcher 事件 → 入队去重 → 60ms 合并处理；Dropped 通道 → 广播 watch-error。
func (dw *dirWatcher) loop() {
	events := dw.w.Events()
	dropped := dw.w.Dropped()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			// 「不显示的目录」内事件：**入队前**即丢弃（G-86 ①）——不进 60ms 队列、不做
			// 归一/真实路径解析/JSON 序列化，隐藏子树的事件成本只剩一次段名匹配。
			// 注：Windows RDCW 是「单句柄 + 子树」模型，库层无法让内核不上报；此处消除的是
			// **进程内**开销（队列/批处理/广播），内核通知本身需逐目录句柄才能完全避免。
			if dw.hiddenPath(ev.Path) {
				dw.droppedHidden.Add(1)
				continue
			}
			dw.enqueue(ev)
		case ev, ok := <-dropped:
			if !ok {
				dropped = nil // 通道关闭 → 摘除该分支（避免零值忙轮询）
				continue
			}
			dw.publishWatchError(fmt.Errorf("event dropped: %s", ev.Path))
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

func (dw *dirWatcher) enqueue(ev fswatcher.WatchEvent) {
	if hasEventType(ev.Types, fswatcher.EventOverflow) {
		// 溢出事件无实际路径语义（path = 被监听根）→ 不当作文件变更广播。
		return
	}
	dw.queueMu.Lock()
	cur, ok := dw.queue[ev.Path]
	if !ok {
		cur = pendingEvent{path: ev.Path}
	}
	cur.types = mergeEventTypes(cur.types, ev.Types)
	dw.queue[ev.Path] = cur
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
	dw.queue = make(map[string]pendingEvent)
	if dw.timer != nil {
		dw.timer.Stop()
		dw.timer = nil
	}
	dw.windowStart = time.Time{} // 本窗口结束：下一批重新起算 maxWait（D-37）
	dw.queueMu.Unlock()
	if len(batch) == 0 {
		return
	}
	// 一次解析 work_dir 真实路径，本批复用（避免逐事件/逐目录重复「逐段 Readlink」）；
	// dirs 携带各刷新目录的**真实路径**（解析一次），pubDirChanged 直接复用不再二次解析。
	baseReal := wslayout.Real(dw.workDir)
	dirs := make(map[string]string)
	putDir := func(dir, dirReal string) {
		if _, ok := dirs[dir]; !ok {
			dirs[dir] = dirReal
		}
	}
	for path, ev := range batch {
		// 「不显示的目录」内事件：不广播（层2 同源）
		if dw.hiddenPath(path) {
			continue
		}
		op := operation(path, ev.types)
		// 事件路径真实路径本事件内解析一次复用（越界复检 + pubChanged + 目录刷新 3 处旧调用合一）。
		pathReal := wslayout.Real(path)
		inWorkDir := wslayout.WithinLexical(dw.workDir, path) && wslayout.WithinLexical(baseReal, pathReal)
		dw.pubChanged(path, op, inWorkDir)
		fi, statErr := os.Lstat(path)
		isDir := statErr == nil && fi.IsDir()

		// 刷新目录 = 事件对象父目录；事件对象本身是**真实目录**（Lstat，不跟随软链）时刷新自身
		// （复用本事件已解析的真实路径，不再二次解析）。目录创建不再需要增删监听（fswatcher 递归接管）。
		if isDir && inWorkDir {
			// 新建/改名到的新路径：其**父目录**也须刷新批次（父 children 需含新目录节点；
			// 仅刷新新目录自身时父批次缺失，前端树不会插入该目录节点，I-43）。
			// fswatcher 把重命名新旧名均映射为 EventRename（无 Create）→ 同样需刷新父目录。
			if hasEventType(ev.types, fswatcher.EventCreate) || hasEventType(ev.types, fswatcher.EventRename) {
				parent := filepath.Dir(path)
				putDir(parent, wslayout.Real(parent))
			}
			putDir(path, pathReal)
		} else {
			parent := filepath.Dir(path)
			putDir(parent, wslayout.Real(parent))
		}
	}
	for dir, dirReal := range dirs {
		dw.pubDirChanged(dir, dirReal, baseReal)
	}
	// 处理期间到达的新事件由 enqueue→armTimer 重排一轮（此时 windowStart 已复位 → 起新窗口），
	// 正常重排程已完整覆盖，故此处无需再调度（D-30）。
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

// mergeEventTypes 合并事件类型（同路径多次事件取并集，去重）。
func mergeEventTypes(dst, src []fswatcher.EventType) []fswatcher.EventType {
	for _, t := range src {
		if !hasEventType(dst, t) {
			dst = append(dst, t)
		}
	}
	return dst
}

// operation 归一事件操作语义。fswatcher（Windows）把 RENAMED_OLD_NAME / RENAMED_NEW_NAME
// **都**映射为 EventRename（不发 Create），且同一 60ms 窗口内同路径的事件类型会合并（如
// [Mod, Rename] / [Create, Remove]）→ 不能只按类型优先级判定。改以「事件处理时对象是否仍存在」
// 为准，保持既有消息语义（下游 indexer：rename/remove → 删除；create/write → 索引）：
//   - 存在 → create（含改名新路径）/ write（仅修改）；
//   - 不存在 → remove（含删除 + 同窗口内创建后又删除）/ rename（改名旧路径，下游按旧路径删除）。
func operation(path string, types []fswatcher.EventType) string {
	_, err := os.Lstat(path)
	exists := err == nil
	missing := errors.Is(err, os.ErrNotExist)
	switch {
	case exists && (hasEventType(types, fswatcher.EventCreate) || hasEventType(types, fswatcher.EventRename)):
		return "create"
	case exists:
		return "write"
	case missing && hasEventType(types, fswatcher.EventRename):
		return "rename"
	case missing:
		return "remove"
	default:
		// 其他 Lstat 错误（如权限不足）不能断言对象已消失 → 保持写入语义，避免误删索引。
		return "write"
	}
}

func (dw *dirWatcher) pubChanged(abs, op string, inWorkDir bool) {
	// 复验真实路径仍在 work_dir 内（防经符号链接 / 目录联接广播根外文件路径；与 pubDirChanged 对齐）。
	// inWorkDir 由调用方按「词法 + 真实路径」一次判定后传入（见 processBatch），避免重复逐段解析。
	if !inWorkDir {
		return
	}
	dw.emit(msgkeys.TopicFilesysChanged, map[string]any{
		"work_dir":  dw.workDir,
		"path":      filepath.ToSlash(abs),
		"operation": op,
	})
}

func (dw *dirWatcher) pubDirChanged(dir, dirReal, baseReal string) {
	// 复验：目录真实路径须仍在 work_dir 内（防经符号链接 / 目录联接广播根外目录的 children 清单）。
	// dirReal / baseReal 由调用方预先解析复用（见 processBatch），避免逐目录重复「逐段 Readlink」。
	if !wslayout.WithinLexical(dw.workDir, dir) || !wslayout.WithinLexical(baseReal, dirReal) {
		return
	}
	children := listDirNodes(dir, dw.hiddenDirs())
	dw.emit(msgkeys.TopicFilesysChanged, map[string]any{
		"work_dir": dw.workDir,
		"path":     filepath.ToSlash(dir),
		"children": children,
	})
}

func (dw *dirWatcher) stop() {
	dw.queueMu.Lock()
	stopped := false
	select {
	case <-dw.done:
		stopped = true
	default:
		close(dw.done)
	}
	if dw.timer != nil {
		dw.timer.Stop()
		dw.timer = nil
	}
	dw.queueMu.Unlock()
	if stopped {
		return
	}
	dw.cancel()
	dw.w.Close()
}
