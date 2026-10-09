// filesys 单测：目录子项过滤（I-44）+ 新建目录父批次广播（I-43）。
package filesys

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// TestListDirNodesFilter：隐藏/临时条目过滤（I-44，对齐旧 FilterDirEntry 口径）：
// "."/"~$" 前缀、"/~"/"-wal"/"-shm" 后缀跳过；普通文件保留。
func TestListDirNodesFilter(t *testing.T) {
	dir := t.TempDir()
	keep := []string{"keep.txt", "sub"}
	skip := []string{".hidden", "~$tmp.docx", "x-wal", "y~", "z-shm"}
	for _, n := range keep {
		if n == "sub" {
			if err := os.Mkdir(filepath.Join(dir, n), 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range skip {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	nodes := listDirNodes(dir, nil)
	got := map[string]bool{}
	for _, n := range nodes {
		got[n["name"].(string)] = true
	}
	for _, n := range keep {
		if !got[n] {
			t.Fatalf("过滤误删合法条目 %q（nodes=%v）", n, got)
		}
	}
	for _, n := range skip {
		if got[n] {
			t.Fatalf("临时/隐藏条目 %q 未被过滤（nodes=%v）", n, got)
		}
	}
}

// TestWatchNewDirBroadcastsParentBatch：新建目录时除自身外，**父目录批次**（带 children）
// 也须广播且父 children 含新目录节点（I-43；否则前端树不插入目录节点）。
func TestWatchNewDirBroadcastsParentBatch(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()

	wd := t.TempDir()
	m := newWatchManager(bus)
	defer m.Close()

	ch := make(chan map[string]any, 64)
	if _, err := bus.On("filesys.changed", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var ev map[string]any
		if json.Unmarshal(v.Payload, &ev) == nil {
			select {
			case ch <- ev:
			default:
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.watch(wd, wd, false, "ins-test"); err != nil {
		t.Fatal(err)
	}

	newDir := filepath.Join(wd, "probe_rootdir")
	if err := os.Mkdir(newDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 轮询等待：父目录批次（children 含 probe_rootdir + 携带声明者 instance_id）
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case ev := <-ch:
			children, ok := ev["children"].([]any)
			if !ok {
				continue // 单文件事件（无 children）
			}
			for _, c := range children {
				cm, _ := c.(map[string]any)
				if cm["name"] != "probe_rootdir" {
					continue
				}
				if ev["instance_id"] != "ins-test" {
					t.Fatalf("父批次缺/错 instance_id: %+v", ev)
				}
				return // 命中：父批次已含新目录节点
			}
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatal("新建目录未广播含新目录节点的父目录批次（I-43）")
}

// TestNextWaitCapsAtMaxWait 去抖等待封顶（D-37）：默认 debounce；窗口内累计耗时逼近 maxWait
// 时等待被压缩到剩余量；超出 maxWait 即 0（立即触发，不再被持续事件无限推后）。
func TestNextWaitCapsAtMaxWait(t *testing.T) {
	if got := nextWait(0); got != debounce {
		t.Fatalf("首事件应等待 debounce：got=%v", got)
	}
	if got := nextWait(maxWait - debounce - time.Millisecond); got != debounce {
		t.Fatalf("剩余 > debounce 时应等待 debounce：got=%v", got)
	}
	if got := nextWait(maxWait - 10*time.Millisecond); got != 10*time.Millisecond {
		t.Fatalf("剩余 < debounce 时应等待剩余量：got=%v", got)
	}
	if got := nextWait(maxWait + time.Millisecond); got != 0 {
		t.Fatalf("超过 maxWait 应立即触发（0）：got=%v", got)
	}
}

// TestWatchContinuousWritesNotStarved 持续写入下批次不被无限推后（D-37）：写入**仍在进行**时
// 就应收到 filesys.changed（旧实现每个事件都重置 debounce，直到写入停顿才触发 → 饥饿）。
func TestWatchContinuousWritesNotStarved(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()
	wd := t.TempDir()
	m := newWatchManager(bus)
	defer m.Close()
	ch := make(chan struct{}, 64)
	if _, err := bus.On("filesys.changed", 0, func(_ context.Context, _ string, _ *mq.Value) error {
		select {
		case ch <- struct{}{}:
		default:
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.watch(wd, wd, false, "ins-deb"); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(wd, "busy.log")

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				_ = os.WriteFile(target, []byte(time.Now().String()), 0o644)
			}
		}
	}()

	// 写入持续进行中：应在 maxWait 量级内收到至少一条事件（远早于写入停止 → 无饥饿）。
	select {
	case <-ch:
	case <-time.After(maxWait + 2*time.Second):
		close(stop)
		<-done
		t.Fatalf("持续写入下 %v 内未收到 filesys.changed（批次被无限推后，D-37 未生效）",
			maxWait+2*time.Second)
	}
	close(stop)
	<-done
}

// TestWatchRecursiveFlag：recursive 标志被消费（T-08）——递归监听时子目录内新增文件触发
// filesys.changed；非递归监听同一路径时不触发（同一 fsnotify watcher 只监听直接子项）。
func TestWatchRecursiveFlag(t *testing.T) {
	// 期望收到 path == want 的单文件 changed 事件（含 operation），超时返回 false。
	waitChanged := func(t *testing.T, ch chan map[string]any, want string, d time.Duration) bool {
		t.Helper()
		deadline := time.Now().Add(d)
		for time.Now().Before(deadline) {
			select {
			case ev := <-ch:
				if _, isBatch := ev["children"]; isBatch {
					continue
				}
				if ev["path"] == filepath.ToSlash(want) {
					return true
				}
			case <-time.After(50 * time.Millisecond):
			}
		}
		return false
	}
	newCh := func(t *testing.T, bus mq.Bus) chan map[string]any {
		t.Helper()
		ch := make(chan map[string]any, 64)
		if _, err := bus.On("filesys.changed", 0, func(_ context.Context, _ string, v *mq.Value) error {
			var ev map[string]any
			if json.Unmarshal(v.Payload, &ev) == nil {
				select {
				case ch <- ev:
				default:
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return ch
	}

	t.Run("recursive=true 子目录内新增触发", func(t *testing.T) {
		bus, err := mq.New(mq.Options{Prefix: "chonk."})
		if err != nil {
			t.Fatal(err)
		}
		defer bus.Close()
		wd := t.TempDir()
		sub := filepath.Join(wd, "sub")
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		m := newWatchManager(bus)
		defer m.Close()
		ch := newCh(t, bus)
		if err := m.watch(wd, wd, true, "ins-rec"); err != nil {
			t.Fatal(err)
		}
		deep := filepath.Join(sub, "deep.txt")
		if err := os.WriteFile(deep, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !waitChanged(t, ch, deep, 5*time.Second) {
			t.Fatal("recursive=true 时子目录内新增文件未触发 filesys.changed")
		}
	})

	t.Run("recursive=false 子目录内新增不触发", func(t *testing.T) {
		bus, err := mq.New(mq.Options{Prefix: "chonk."})
		if err != nil {
			t.Fatal(err)
		}
		defer bus.Close()
		wd := t.TempDir()
		sub := filepath.Join(wd, "sub")
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		m := newWatchManager(bus)
		defer m.Close()
		ch := newCh(t, bus)
		if err := m.watch(wd, wd, false, "ins-norec"); err != nil {
			t.Fatal(err)
		}
		deep := filepath.Join(sub, "deep.txt")
		if err := os.WriteFile(deep, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if waitChanged(t, ch, deep, 500*time.Millisecond) {
			t.Fatal("recursive=false 时子目录内新增文件不应触发 filesys.changed")
		}
		// 反向确认 watcher 正常工作：直接子项新增仍触发
		top := filepath.Join(wd, "top.txt")
		if err := os.WriteFile(top, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !waitChanged(t, ch, top, 5*time.Second) {
			t.Fatal("直接子项新增未触发 filesys.changed（watcher 未工作）")
		}
	})
}

// ─── D-39 层1：目录生命周期闭环 ─────────────────────────────────

// pollUntil 在超时内轮询 cond 直到为真；否则 fail。
func pollUntil(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(msg)
}

// hasPath 判定 dirWatcher.paths 是否含某路径（受 pathsMu 保护）。
func hasPath(dw *dirWatcher, abs string) bool {
	dw.pathsMu.Lock()
	defer dw.pathsMu.Unlock()
	return dw.paths[abs]
}

// pathsLen 取 dirWatcher.paths 条目数。
func pathsLen(dw *dirWatcher) int {
	dw.pathsMu.Lock()
	defer dw.pathsMu.Unlock()
	return len(dw.paths)
}

// TestRemoveTreeClearsSubtree：removeTree 按前缀摘除目标及其全部子孙（层1 单元）。
func TestRemoveTreeClearsSubtree(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()
	wd := t.TempDir()
	target := filepath.Join(wd, "target", "classes")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(wd, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := newWatchManager(bus)
	defer m.Close()
	if err := m.watch(wd, wd, true, "ins-rm"); err != nil {
		t.Fatal(err)
	}
	dw := m.watchers[wd]
	if dw == nil {
		t.Fatal("watcher 未建立")
	}
	if !hasPath(dw, filepath.Join(wd, "target")) || !hasPath(dw, target) {
		t.Fatalf("watch 后应含 target 子目录树: %v", dw.paths)
	}

	dw.removeTree(filepath.Join(wd, "target"))
	if hasPath(dw, filepath.Join(wd, "target")) || hasPath(dw, target) {
		t.Fatalf("removeTree 后 target 及其子孙应出表: %v", dw.paths)
	}
	// 无关路径不受影响
	if !hasPath(dw, filepath.Join(wd, "src")) {
		t.Fatalf("removeTree 误删无关路径 src: %v", dw.paths)
	}
}

// TestProcessBatchRemoveDirClearsPaths：目录被删除（fsnotify Remove）后 paths 无残留（层1 闭环）。
func TestProcessBatchRemoveDirClearsPaths(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()
	wd := t.TempDir()
	m := newWatchManager(bus)
	defer m.Close()
	if err := m.watch(wd, wd, true, "ins-rm2"); err != nil {
		t.Fatal(err)
	}
	dw := m.watchers[wd]
	if dw == nil {
		t.Fatal("watcher 未建立")
	}

	// 新建 target/ 目录树 → 等待 watcher 递归纳入
	target := filepath.Join(wd, "target", "classes")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	pollUntil(t, 5*time.Second, func() bool { return hasPath(dw, target) }, "新建目录未纳入 watch")

	// 删除整个 target 树 → 等待 paths 清理
	if err := os.RemoveAll(filepath.Join(wd, "target")); err != nil {
		t.Fatal(err)
	}
	pollUntil(t, 5*time.Second, func() bool {
		return !hasPath(dw, filepath.Join(wd, "target")) && !hasPath(dw, target)
	}, "目录删除后 paths 仍有残留（层1 未闭环）")
	if got := pathsLen(dw); got != 1 { // 仅剩 work_dir 根
		t.Fatalf("删除后应仅剩 work_dir 根，实得 %d 条: %v", got, dw.paths)
	}
}

// ─── D-39 层2：显示与监听同源 ───────────────────────────────────

// TestShouldHideDirNameSameSource：目录判定同源 —— 对同一目录名，
// 文件树过滤（filterDirEntry(isDir=true)）与 watcher 递归（shouldHideDirName）返回一致。
func TestShouldHideDirNameSameSource(t *testing.T) {
	hidden := []string{"target", "node_modules", "out"}
	names := []string{".git", ".hidden", "target", "node_modules", "out", "src", "Target", "dist", ""}
	for _, n := range names {
		if got, want := filterDirEntry(n, true, hidden), shouldHideDirName(n, hidden); got != want {
			t.Fatalf("目录判定不同源：name=%q filterDirEntry=%v shouldHideDirName=%v", n, got, want)
		}
	}
	// 点开头恒隐藏（不可配置放开）
	if !shouldHideDirName(".git", nil) || !shouldHideDirName(".chonkpilot", nil) {
		t.Fatal("点开头目录应恒隐藏")
	}
	// 清单命中隐藏；大小写敏感（目录名匹配，Windows 亦按字面）
	if !shouldHideDirName("target", hidden) {
		t.Fatal("清单命中项应隐藏")
	}
	if shouldHideDirName("Target", hidden) {
		t.Fatal("清单匹配应大小写敏感")
	}
	// 清单不影响**文件**判定（文件规则独立）
	if filterDirEntry("target", false, hidden) {
		t.Fatal("普通文件 target（同名）不应被目录清单隐藏")
	}
}

// TestListDirNodesHiddenDirsFilter：listDirNodes 按隐藏清单过滤目录，但不误伤同名文件。
func TestListDirNodesHiddenDirsFilter(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "target.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	nodes := listDirNodes(dir, []string{"target"})
	got := map[string]bool{}
	for _, n := range nodes {
		got[n["name"].(string)] = true
	}
	if got["target"] {
		t.Fatalf("隐藏清单命中目录不应出现: %v", got)
	}
	if !got["src"] || !got["target.txt"] {
		t.Fatalf("非同名的目录/文件不应被误过滤: %v", got)
	}
}

// TestWalkAddSkipsHiddenDirs：watcher 递归跳过隐藏清单目录（与文件树同源，层2）。
func TestWalkAddSkipsHiddenDirs(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()
	wd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wd, "target", "classes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(wd, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := newWatchManager(bus)
	defer m.Close()
	m.applyHidden(wd, []string{"target"}) // 装载清单（幂等；无 watcher 时仅记缓存）
	if err := m.watch(wd, wd, true, "ins-hide"); err != nil {
		t.Fatal(err)
	}
	dw := m.watchers[wd]
	if hasPath(dw, filepath.Join(wd, "target")) || hasPath(dw, filepath.Join(wd, "target", "classes")) {
		t.Fatalf("隐藏清单目录不应被 watch: %v", dw.paths)
	}
	if !hasPath(dw, filepath.Join(wd, "src")) {
		t.Fatalf("可见目录应被 watch: %v", dw.paths)
	}
}

// TestRefilterAppliesHiddenChange：清单变更即时重过滤 —— 现已隐藏者摘除，「由隐藏转可见」者补齐。
func TestRefilterAppliesHiddenChange(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()
	wd := t.TempDir()
	target := filepath.Join(wd, "target")
	if err := os.MkdirAll(filepath.Join(target, "classes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(wd, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := newWatchManager(bus)
	defer m.Close()
	if err := m.watch(wd, wd, true, "ins-refilter"); err != nil {
		t.Fatal(err)
	}
	dw := m.watchers[wd]
	if !hasPath(dw, target) {
		t.Fatalf("初始应 watch target: %v", dw.paths)
	}

	// 加入清单 → target 及其子树摘除，root 与 src 保留
	m.applyHidden(wd, []string{"target"})
	if hasPath(dw, target) || hasPath(dw, filepath.Join(target, "classes")) {
		t.Fatalf("清单加入后 target 树应摘除: %v", dw.paths)
	}
	if !hasPath(dw, wd) || !hasPath(dw, filepath.Join(wd, "src")) {
		t.Fatalf("root/src 不应被误摘: %v", dw.paths)
	}

	// 移出清单 → target 树补齐（由隐藏转可见）
	m.applyHidden(wd, []string{"node_modules"})
	if !hasPath(dw, target) || !hasPath(dw, filepath.Join(target, "classes")) {
		t.Fatalf("清单移除后 target 树应补齐: %v", dw.paths)
	}
}

// TestParseHiddenDirs：解析（逗号/分号/换行分隔、去空白、去尾分隔符、去重、空 → 缺省）。
func TestParseHiddenDirs(t *testing.T) {
	got := parseHiddenDirs(" target , node_modules;out/\n\n build ")
	want := []string{"target", "node_modules", "out", "build"}
	if !sameStrings(got, want) {
		t.Fatalf("parseHiddenDirs=%v want %v", got, want)
	}
	if got := parseHiddenDirs("target,target"); !sameStrings(got, []string{"target"}) {
		t.Fatalf("应去重: %v", got)
	}
	if got := parseHiddenDirs("   "); !sameStrings(got, defaultHiddenDirs) {
		t.Fatalf("空配置应回落缺省: %v", got)
	}
}
