// 变更跟踪：fsnotify watch + 60ms 合并去抖 → 广播 filesys.changed / filesys.watch-error
// （61-消息一览 §2.4：单文件 + 目录批次合一广播）。
package filesys

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/fsnotify/fsnotify"
)

const debounce = 60 * time.Millisecond

// watchManager 按 work_dir 组织 fsnotify watcher。
type watchManager struct {
	bus      mq.Bus
	mu       sync.Mutex
	watchers map[string]*dirWatcher // key = work_dir（绝对）
}

func newWatchManager(bus mq.Bus) *watchManager {
	return &watchManager{bus: bus, watchers: make(map[string]*dirWatcher)}
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
	m.mu.Lock()
	dw, ok := m.watchers[workDir]
	if !ok {
		var err error
		dw, err = newDirWatcher(workDir, m.bus)
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

// dirWatcher 单个 work_dir 的 fsnotify watcher（60ms 合并去抖）。
type dirWatcher struct {
	workDir string
	bus     mq.Bus
	w       *fsnotify.Watcher

	paths   map[string]bool // 已 watch 的绝对路径（防重）
	pathsMu sync.Mutex

	// declarers = 声明者 instance_id 集合（filesys.watch 请求携带，桥注入）。业务 payload
	// 一律必带 instance_id（61-消息一览 §0.1）；同一 work_dir 被多实例声明时按声明者
	// 逐个各发一份事件（各自带上自己的 id），保持"所有订阅者仍能收到"的既有可见行为。
	declarers   map[string]bool
	declarersMu sync.Mutex

	queue   map[string]fsnotify.Event
	queueMu sync.Mutex
	timer   *time.Timer
	done    chan struct{}
}

func newDirWatcher(workDir string, bus mq.Bus) (*dirWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	dw := &dirWatcher{
		workDir:   workDir,
		bus:       bus,
		w:         w,
		paths:     make(map[string]bool),
		declarers: make(map[string]bool),
		queue:     make(map[string]fsnotify.Event),
		done:      make(chan struct{}),
	}
	go dw.loop()
	return dw, nil
}

// addDeclarer 登记声明者 instance_id（同一 work_dir 可被多实例声明，各自的 watch 请求都登记）。
// 取舍：unwatch 不反登记声明者 —— 与既有 watcher 生命周期一致（watchers 从不删除，只移出路径），
// 避免因某路径 unwatch 而误删仍关注其它路径的声明者，保持既有可见行为。
func (dw *dirWatcher) addDeclarer(instanceID string) {
	if instanceID == "" {
		return
	}
	dw.declarersMu.Lock()
	dw.declarers[instanceID] = true
	dw.declarersMu.Unlock()
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

// add 添加一个路径（目录或文件）；recursive 时递归子目录（过滤隐藏）。
func (dw *dirWatcher) add(abs string, recursive bool) error {
	dw.pathsMu.Lock()
	if dw.paths[abs] {
		dw.pathsMu.Unlock()
		return nil
	}
	dw.paths[abs] = true
	dw.pathsMu.Unlock()

	if err := dw.w.Add(abs); err != nil {
		dw.pathsMu.Lock()
		delete(dw.paths, abs)
		dw.pathsMu.Unlock()
		return err
	}
	if recursive {
		fi, err := os.Stat(abs)
		if err == nil && fi.IsDir() {
			_ = dw.walkAdd(abs)
		}
	}
	return nil
}

// walkAdd 递归添加子目录（目录内文件无需单独 Add，fsnotify 目录事件覆盖）。
func (dw *dirWatcher) walkAdd(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		if err := dw.add(sub, true); err != nil {
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
	dw.pathsMu.Unlock()
	_ = dw.w.Remove(abs)
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
	dw.emit("filesys.watch-error", map[string]any{
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

func (dw *dirWatcher) snapshot() map[string]fsnotify.Event {
	dw.queueMu.Lock()
	defer dw.queueMu.Unlock()
	if len(dw.queue) == 0 {
		return nil
	}
	batch := dw.queue
	dw.queue = make(map[string]fsnotify.Event)
	return batch
}

func (dw *dirWatcher) armTimer() {
	dw.queueMu.Lock()
	if dw.timer != nil {
		dw.timer.Stop()
	}
	dw.timer = time.AfterFunc(debounce, dw.processBatch)
	dw.queueMu.Unlock()
}

// processBatch 合并事件：广播 filesys.changed（单文件） + 目录批次。
func (dw *dirWatcher) processBatch() {
	batch := dw.snapshot()
	if len(batch) == 0 {
		return
	}
	dirs := make(map[string]bool)
	for path, ev := range batch {
		op := operation(ev.Op)
		dw.pubChanged(path, op)

		// 新目录（recursive 语义）→ 递归添加
		if ev.Op&fsnotify.Create != 0 {
			if fi, err := os.Stat(path); err == nil && fi.IsDir() {
				dw.pathsMu.Lock()
				known := dw.paths[path]
				dw.pathsMu.Unlock()
				if !known {
					_ = dw.add(path, true)
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
	// 处理期间又来新事件 → 再调度一轮
	dw.queueMu.Lock()
	if len(dw.queue) > 0 && dw.timer == nil {
		dw.timer = time.AfterFunc(debounce, dw.processBatch)
	}
	dw.queueMu.Unlock()
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
	dw.emit("filesys.changed", map[string]any{
		"work_dir":  dw.workDir,
		"path":      filepath.ToSlash(abs),
		"operation": op,
	})
}

func (dw *dirWatcher) pubDirChanged(dir string) {
	children := listDirNodes(dir)
	dw.emit("filesys.changed", map[string]any{
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
