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
	"github.com/sgtdi/fswatcher"
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
	m := newWatchManager(bus, nil)
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

// TestOperationNormalize 事件操作归一：fswatcher（Windows）重命名新旧名均为 EventRename，
// 且同窗口内同路径事件类型会合并 → 以「处理时对象是否存在」定语义，保证 indexer / 文件树一致。
func TestOperationNormalize(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(dir, "gone.txt")
	cases := []struct {
		name  string
		path  string
		types []fswatcher.EventType
		want  string
	}{
		{"存在 + Create", file, []fswatcher.EventType{fswatcher.EventCreate}, "create"},
		{"存在 + Rename（改名新路径）", file, []fswatcher.EventType{fswatcher.EventRename}, "create"},
		{"存在 + Mod/Rename 合并", file, []fswatcher.EventType{fswatcher.EventMod, fswatcher.EventRename}, "create"},
		{"存在 + Mod", file, []fswatcher.EventType{fswatcher.EventMod}, "write"},
		{"不存在 + Rename（改名旧路径）", gone, []fswatcher.EventType{fswatcher.EventRename}, "rename"},
		{"不存在 + Remove", gone, []fswatcher.EventType{fswatcher.EventRemove}, "remove"},
		{"不存在 + Create/Remove 合并", gone, []fswatcher.EventType{fswatcher.EventCreate, fswatcher.EventRemove}, "remove"},
	}
	for _, c := range cases {
		if got := operation(c.path, c.types); got != c.want {
			t.Errorf("%s：operation=%q，期望 %q", c.name, got, c.want)
		}
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
	m := newWatchManager(bus, nil)
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
// filesys.changed；非递归监听同一路径时不触发（fswatcher WatchTopLevel 只投递直接子项事件）。
func TestWatchRecursiveFlag(t *testing.T) {
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
		m := newWatchManager(bus, nil)
		defer m.Close()
		ch := newChangedCh(t, bus)
		if err := m.watch(wd, wd, true, "ins-rec"); err != nil {
			t.Fatal(err)
		}
		deep := filepath.Join(sub, "deep.txt")
		if err := os.WriteFile(deep, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !waitChangedPath(t, ch, deep, 5*time.Second) {
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
		m := newWatchManager(bus, nil)
		defer m.Close()
		ch := newChangedCh(t, bus)
		if err := m.watch(wd, wd, false, "ins-norec"); err != nil {
			t.Fatal(err)
		}
		deep := filepath.Join(sub, "deep.txt")
		if err := os.WriteFile(deep, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if waitChangedPath(t, ch, deep, 500*time.Millisecond) {
			t.Fatal("recursive=false 时子目录内新增文件不应触发 filesys.changed")
		}
		// 反向确认 watcher 正常工作：直接子项新增仍触发
		top := filepath.Join(wd, "top.txt")
		if err := os.WriteFile(top, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !waitChangedPath(t, ch, top, 5*time.Second) {
			t.Fatal("直接子项新增未触发 filesys.changed（watcher 未工作）")
		}
	})
}

// newChangedCh 订阅 filesys.changed 并把 payload 投进带缓冲通道。
func newChangedCh(t *testing.T, bus mq.Bus) chan map[string]any {
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

// waitChangedPath 期望收到 path == want 的单文件 changed 事件（含 operation），超时返回 false。
func waitChangedPath(t *testing.T, ch chan map[string]any, want string, d time.Duration) bool {
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

// hasPath 判定 dirWatcher.paths（**声明根集合**，子目录由 fswatcher 递归接管）是否含某路径。
func hasPath(dw *dirWatcher, abs string) bool {
	dw.pathsMu.Lock()
	defer dw.pathsMu.Unlock()
	return dw.paths[filepath.Clean(abs)]
}

// pathsLen 取 dirWatcher.paths（声明根集合）条目数。
func pathsLen(dw *dirWatcher) int {
	dw.pathsMu.Lock()
	defer dw.pathsMu.Unlock()
	return len(dw.paths)
}

// TestRemoveTreeClearsSubtree：removeTree 按前缀摘除目标及其全部子孙的**声明条目**（层1 单元）。
func TestRemoveTreeClearsSubtree(t *testing.T) {
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
	src := filepath.Join(wd, "src")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	m := newWatchManager(bus, nil)
	defer m.Close()
	if err := m.watch(wd, wd, true, "ins-rm"); err != nil {
		t.Fatal(err)
	}
	// 显式声明 target 与 src（子目录本身不再逐条登记）。
	if err := m.watch(wd, target, true, "ins-rm"); err != nil {
		t.Fatal(err)
	}
	if err := m.watch(wd, src, true, "ins-rm"); err != nil {
		t.Fatal(err)
	}
	dw := m.watchers[wd]
	if dw == nil {
		t.Fatal("watcher 未建立")
	}
	if !hasPath(dw, target) || !hasPath(dw, src) {
		t.Fatalf("watch 后应含 target/src 声明根: %v", dw.paths)
	}

	dw.removeTree(target)
	if hasPath(dw, target) {
		t.Fatalf("removeTree 后 target 应出表: %v", dw.paths)
	}
	// 无关声明根不受影响
	if !hasPath(dw, src) || !hasPath(dw, wd) {
		t.Fatalf("removeTree 误删无关声明根: %v", dw.paths)
	}
}

// TestProcessBatchRemoveDirClearsPaths：目录被删除（fswatcher Remove）后其声明条目无残留（层1 闭环）。
func TestProcessBatchRemoveDirClearsPaths(t *testing.T) {
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
	m := newWatchManager(bus, nil)
	defer m.Close()
	if err := m.watch(wd, wd, true, "ins-rm2"); err != nil {
		t.Fatal(err)
	}
	// 显式声明 target 为监听根（模拟前端展开了该目录）。
	if err := m.watch(wd, target, true, "ins-rm2"); err != nil {
		t.Fatal(err)
	}
	dw := m.watchers[wd]
	if dw == nil {
		t.Fatal("watcher 未建立")
	}
	if !hasPath(dw, target) {
		t.Fatalf("target 应已声明: %v", dw.paths)
	}

	// 删除整个 target 树 → 等待声明条目清理
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	pollUntil(t, 5*time.Second, func() bool { return !hasPath(dw, target) },
		"目录删除后声明条目仍有残留（层1 未闭环）")
	if got := pathsLen(dw); got != 1 { // 仅剩 work_dir 根
		t.Fatalf("删除后应仅剩 work_dir 根，实得 %d 条: %v", got, dw.paths)
	}
}

// ─── D-39 层2：显示与监听同源 ───────────────────────────────────

// TestShouldHideDirNameSameSource：目录判定同源 —— 对同一目录名，
// 文件树过滤（filterDirEntry(isDir=true)）与 watcher 事件过滤（shouldHideDirName）返回一致。
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

// TestWatchSkipsHiddenDirEvents：隐藏清单内的目录事件不广播（与文件树显示同源，层2）；
// 监听本身由 fswatcher 递归覆盖，不再按清单增删 watch。
func TestWatchSkipsHiddenDirEvents(t *testing.T) {
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
	src := filepath.Join(wd, "src")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	m := newWatchManager(bus, nil)
	defer m.Close()
	m.applyHidden(wd, []string{"target"}) // 装载清单（无 watcher 时仅记缓存）
	ch := newChangedCh(t, bus)
	if err := m.watch(wd, wd, true, "ins-hide"); err != nil {
		t.Fatal(err)
	}

	hiddenFile := filepath.Join(target, "classes", "deep.txt")
	if err := os.WriteFile(hiddenFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if waitChangedPath(t, ch, hiddenFile, 500*time.Millisecond) {
		t.Fatal("隐藏目录内文件不应广播 filesys.changed")
	}

	visible := filepath.Join(src, "a.txt")
	if err := os.WriteFile(visible, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitChangedPath(t, ch, visible, 5*time.Second) {
		t.Fatal("可见目录内文件应广播 filesys.changed（watcher 未工作）")
	}
}

// TestRefilterAppliesHiddenChange：清单变更即时生效（事件过滤）——现已隐藏者不再广播，
// 「由隐藏转可见」者恢复广播；监听根集合不随清单增删（fswatcher 递归接管）。
func TestRefilterAppliesHiddenChange(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()
	wd := t.TempDir()
	target := filepath.Join(wd, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(wd, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := newWatchManager(bus, nil)
	defer m.Close()
	ch := newChangedCh(t, bus)
	if err := m.watch(wd, wd, true, "ins-refilter"); err != nil {
		t.Fatal(err)
	}
	dw := m.watchers[wd]

	// 初始可见 → 广播
	f1 := filepath.Join(target, "f1.txt")
	if err := os.WriteFile(f1, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitChangedPath(t, ch, f1, 5*time.Second) {
		t.Fatal("初始应广播 target 内变更")
	}

	// 加入清单 → target 内变更不再广播；声明根集合不变
	m.applyHidden(wd, []string{"target"})
	f2 := filepath.Join(target, "f2.txt")
	if err := os.WriteFile(f2, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if waitChangedPath(t, ch, f2, 500*time.Millisecond) {
		t.Fatal("清单加入后 target 内变更不应广播")
	}
	if !hasPath(dw, wd) || hasPath(dw, target) {
		t.Fatalf("refilter 不应增删声明根: %v", dw.paths)
	}

	// 移出清单 → 恢复广播
	m.applyHidden(wd, []string{"node_modules"})
	f3 := filepath.Join(target, "f3.txt")
	if err := os.WriteFile(f3, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitChangedPath(t, ch, f3, 5*time.Second) {
		t.Fatal("清单移除后 target 内变更应恢复广播")
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

// TestOnCopyRejectsNestedTarget（R7-C-5）：dest_dir 选源目录内部子目录时拒绝，
// 且不得在源目录内创建目标（否则 copyTree 会把新建目标递归吸入 → 无限嵌套复制）。
func TestOnCopyRejectsNestedTarget(t *testing.T) {
	wd := t.TempDir()
	src := filepath.Join(wd, "a")
	if err := os.MkdirAll(filepath.Join(src, "n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	f := &Filesys{insts: map[string]string{"i1": wd}}
	payload, _ := json.Marshal(map[string]any{
		"instance_id": "i1", "path": src, "dest_dir": filepath.Join("a", "sub"),
	})
	v := &mq.Value{Payload: payload}
	if err := f.onCopy(context.Background(), "", v); err != nil {
		t.Fatalf("onCopy err: %v", err)
	}
	if len(v.Errors) == 0 {
		t.Fatalf("嵌套复制应被拒绝，errors=%v result=%v", v.Errors, v.Result)
	}
	if _, err := os.Stat(filepath.Join(src, "sub")); !os.IsNotExist(err) {
		t.Fatalf("不得在源目录内创建目标目录（stat err=%v）", err)
	}
}

// TestOnCopyNormalTargetAllowed（R7-C-5 反向）：目标在源目录之外的正常复制仍成功。
func TestOnCopyNormalTargetAllowed(t *testing.T) {
	wd := t.TempDir()
	src := filepath.Join(wd, "a")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	f := &Filesys{insts: map[string]string{"i1": wd}}
	payload, _ := json.Marshal(map[string]any{
		"instance_id": "i1", "path": src, "dest_dir": "out",
	})
	v := &mq.Value{Payload: payload}
	if err := f.onCopy(context.Background(), "", v); err != nil {
		t.Fatalf("onCopy err: %v", err)
	}
	if len(v.Errors) != 0 {
		t.Fatalf("正常复制不应失败: %v", v.Errors)
	}
	if _, err := os.Stat(filepath.Join(wd, "out", "a", "f.txt")); err != nil {
		t.Fatalf("目标文件应存在：%v", err)
	}
}
